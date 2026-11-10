import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

type Handler = (url: string, init: RequestInit) => Response | Promise<Response>

const jsonResponse = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(body === null ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })

const apiError = (status: number, code: string) =>
  jsonResponse(status, { error: { code, message: `msg ${code}` } })

function mockFetch(handler: Handler) {
  const calls: { url: string; init: RequestInit }[] = []
  const fn = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    const u = String(url)
    calls.push({ url: u, init: init ?? {} })
    return handler(u, init ?? {})
  })
  vi.stubGlobal('fetch', fn)
  return calls
}

function headerOf(init: RequestInit, name: string): string | undefined {
  return (init.headers as Record<string, string> | undefined)?.[name]
}

function setCookie(value: string | null) {
  document.cookie = value === null ? 'shinsou_csrf=; Max-Age=0; Path=/' : `shinsou_csrf=${value}; Path=/`
}

async function load() {
  vi.resetModules()
  const session = await import('./session')
  const client = await import('./client')
  return { ...session, ...client }
}

beforeEach(() => {
  setCookie('csrf-1')
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  setCookie(null)
})

describe('request', () => {
  it('sends the bearer token, the API version header and JSON', async () => {
    const calls = mockFetch(() => jsonResponse(200, { ok: true }))
    const m = await load()
    m.setTokens({ accessToken: 'tok', accessTokenExpiresAt: Date.now() + 600_000 })
    await m.json('POST', '/api/v1/x', { body: { a: 1 }, query: { q: 'z' } })
    const { url, init } = calls[0]!
    expect(url).toBe('/api/v1/x?q=z')
    expect(headerOf(init, 'Authorization')).toBe('Bearer tok')
    expect(headerOf(init, 'Shinsou-Api-Version')).toBe('1')
    expect(headerOf(init, 'Content-Type')).toBe('application/json')
    expect(init.body).toBe('{"a":1}')
  })

  it('omits the bearer token for unauthenticated calls', async () => {
    const calls = mockFetch(() => jsonResponse(200, {}))
    const m = await load()
    m.setTokens({ accessToken: 'tok', accessTokenExpiresAt: Date.now() + 600_000 })
    await m.json('GET', '/.well-known/shinsou', { auth: false })
    expect(headerOf(calls[0]!.init, 'Authorization')).toBeUndefined()
  })

  it('parses API errors into ApiError with the stable code', async () => {
    mockFetch(() => jsonResponse(429, { error: { code: 'rate_limited', message: 'slow', retryAfterSeconds: 7 } }))
    const m = await load()
    const err = await m.json('GET', '/api/v1/x', { auth: false }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(m.ApiError)
    expect(err).toMatchObject({ status: 429, code: 'rate_limited', retryAfterSeconds: 7 })
  })

  it('turns network failures into ApiError(network)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('offline') }))
    const m = await load()
    await expect(m.json('GET', '/api/v1/x')).rejects.toMatchObject({ code: 'network', status: 0 })
  })
})

describe('401 handling', () => {
  it('refreshes once with the CSRF header and retries the request once', async () => {
    let api = 0
    const calls = mockFetch((url) => {
      if (url === '/api/v1/web/refresh') {
        return jsonResponse(200, { accessToken: 'tok-2', accessTokenExpiresAt: Date.now() + 600_000 })
      }
      api++
      return api === 1 ? apiError(401, 'unauthorized') : jsonResponse(200, { me: 1 })
    })
    const m = await load()
    m.setTokens({ accessToken: 'tok-1', accessTokenExpiresAt: Date.now() + 600_000 })
    const out = await m.json<{ me: number }>('GET', '/api/v1/me')
    expect(out.me).toBe(1)
    expect(calls.map((c) => c.url)).toEqual(['/api/v1/me', '/api/v1/web/refresh', '/api/v1/me'])
    expect(headerOf(calls[1]!.init, 'X-CSRF-Token')).toBe('csrf-1')
    expect(headerOf(calls[2]!.init, 'Authorization')).toBe('Bearer tok-2')
  })

  it('does not retry more than once when the retry is rejected again, and signs out', async () => {
    const calls = mockFetch((url) =>
      url === '/api/v1/web/refresh'
        ? jsonResponse(200, { accessToken: 'tok-2', accessTokenExpiresAt: Date.now() + 600_000 })
        : apiError(401, 'unauthorized'),
    )
    const m = await load()
    const lost = vi.fn()
    m.onAuthLost(lost)
    m.setTokens({ accessToken: 'tok-1', accessTokenExpiresAt: Date.now() + 600_000 })
    m.session.user = { id: 'u', username: 'a', role: 'user', createdAt: 0 }
    await expect(m.json('GET', '/api/v1/me')).rejects.toMatchObject({ code: 'unauthorized' })
    expect(calls.filter((c) => c.url === '/api/v1/me')).toHaveLength(2)
    expect(calls.filter((c) => c.url === '/api/v1/web/refresh')).toHaveLength(1)
    expect(lost).toHaveBeenCalledOnce()
    expect(m.session.accessToken).toBeNull()
  })

  it('clears the session and reports auth loss when the refresh is refused', async () => {
    const calls = mockFetch((url) =>
      url === '/api/v1/web/refresh' ? apiError(401, 'refresh_token_reused') : apiError(401, 'unauthorized'),
    )
    const m = await load()
    const lost = vi.fn()
    m.onAuthLost(lost)
    m.setTokens({ accessToken: 'tok-1', accessTokenExpiresAt: Date.now() + 600_000 })
    m.session.user = { id: 'u', username: 'a', role: 'user', createdAt: 0 }
    await expect(m.json('GET', '/api/v1/devices')).rejects.toMatchObject({ status: 401 })
    expect(calls.map((c) => c.url)).toEqual(['/api/v1/devices', '/api/v1/web/refresh'])
    expect(m.session.user).toBeNull()
    expect(m.session.accessToken).toBeNull()
    expect(lost).toHaveBeenCalledOnce()
  })

  it('does not treat a wrong password (401 invalid_credentials) as an expired session', async () => {
    const calls = mockFetch(() => apiError(401, 'invalid_credentials'))
    const m = await load()
    m.setTokens({ accessToken: 'tok-1', accessTokenExpiresAt: Date.now() + 600_000 })
    await expect(m.json('POST', '/api/v1/me/password', { body: {} })).rejects.toMatchObject({
      code: 'invalid_credentials',
    })
    expect(calls).toHaveLength(1)
    expect(m.session.accessToken).toBe('tok-1')
  })

  it('keeps the session when the refresh fails for a transient reason', async () => {
    mockFetch((url) => (url === '/api/v1/web/refresh' ? new Response('', { status: 503 }) : apiError(401, 'unauthorized')))
    const m = await load()
    const lost = vi.fn()
    m.onAuthLost(lost)
    m.setTokens({ accessToken: 'tok-1', accessTokenExpiresAt: Date.now() + 600_000 })
    await expect(m.json('GET', '/api/v1/me')).rejects.toMatchObject({ status: 401 })
    expect(lost).not.toHaveBeenCalled()
    expect(m.session.accessToken).toBe('tok-1')
  })
})

describe('refreshSession', () => {
  it('shares one request between concurrent callers in a tab', async () => {
    let resolve!: (r: Response) => void
    const calls = mockFetch(() => new Promise<Response>((r) => { resolve = r }))
    const m = await load()
    const a = m.refreshSession()
    const b = m.refreshSession()
    const c = m.refreshSession()
    await vi.waitFor(() => expect(calls).toHaveLength(1))
    resolve(jsonResponse(200, { accessToken: 'tok', accessTokenExpiresAt: Date.now() + 600_000 }))
    expect(await Promise.all([a, b, c])).toEqual(['ok', 'ok', 'ok'])
    expect(calls).toHaveLength(1)
    // A later refresh is a new request.
    mockFetch(() => jsonResponse(200, { accessToken: 'tok-b', accessTokenExpiresAt: Date.now() + 600_000 }))
    expect(await m.refreshSession()).toBe('ok')
    expect(m.session.accessToken).toBe('tok-b')
  })

  it('shares one refresh between simultaneous 401s', async () => {
    let apiCalls = 0
    let refreshes = 0
    mockFetch((url) => {
      if (url === '/api/v1/web/refresh') {
        refreshes++
        return jsonResponse(200, { accessToken: 'tok-2', accessTokenExpiresAt: Date.now() + 600_000 })
      }
      apiCalls++
      return apiCalls <= 3 ? apiError(401, 'unauthorized') : jsonResponse(200, {})
    })
    const m = await load()
    m.setTokens({ accessToken: 'tok-1', accessTokenExpiresAt: Date.now() + 600_000 })
    await Promise.all([m.json('GET', '/api/v1/a'), m.json('GET', '/api/v1/b'), m.json('GET', '/api/v1/c')])
    expect(refreshes).toBe(1)
  })

  it('runs inside the shinsou-refresh Web Lock and reads the CSRF cookie only once the lock is held', async () => {
    const calls = mockFetch(() => jsonResponse(200, { accessToken: 'tok', accessTokenExpiresAt: Date.now() + 600_000 }))
    const names: string[] = []
    const lockRequest = vi.fn(async (name: string, cb: () => Promise<unknown>) => {
      names.push(name)
      // Another tab rotates the cookie while we wait for the lock.
      setCookie('csrf-rotated')
      return cb()
    })
    vi.stubGlobal('navigator', { ...navigator, locks: { request: lockRequest } })
    const m = await load()
    expect(await m.refreshSession()).toBe('ok')
    expect(names).toEqual(['shinsou-refresh'])
    expect(headerOf(calls[0]!.init, 'X-CSRF-Token')).toBe('csrf-rotated')
  })

  it('works without the Web Locks API', async () => {
    mockFetch(() => jsonResponse(200, { accessToken: 'tok', accessTokenExpiresAt: Date.now() + 600_000 }))
    vi.stubGlobal('navigator', { ...navigator, locks: undefined })
    const m = await load()
    expect(await m.refreshSession()).toBe('ok')
  })

  it('is denied without a CSRF cookie and does not call the server', async () => {
    setCookie(null)
    const calls = mockFetch(() => jsonResponse(200, {}))
    const m = await load()
    expect(await m.refreshSession()).toBe('denied')
    expect(calls).toHaveLength(0)
  })

  it('refreshes about a minute before the access token expires', async () => {
    vi.useFakeTimers()
    const calls = mockFetch(() => jsonResponse(200, { accessToken: 'tok-2', accessTokenExpiresAt: Date.now() + 600_000 }))
    const m = await load()
    m.setTokens({ accessToken: 'tok-1', accessTokenExpiresAt: Date.now() + 300_000 })
    await vi.advanceTimersByTimeAsync(238_000)
    expect(calls).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(3_000)
    expect(calls.map((c) => c.url)).toEqual(['/api/v1/web/refresh'])
    expect(m.session.accessToken).toBe('tok-2')
  })
})
