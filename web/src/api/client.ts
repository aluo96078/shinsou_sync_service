import { buildQuery, type QueryValue } from '@/lib/query'
import { handleAuthLost, refreshSession, session } from './session'
import type { ErrorBody, ErrorCode } from './types'

/** `code` is one of the OpenAPI error codes, or a local code (`network`, `http_<status>`). */
export class ApiError extends Error {
  readonly status: number
  readonly code: ErrorCode | (string & {})
  readonly retryAfterSeconds: number | undefined

  constructor(status: number, code: string, message: string, retryAfterSeconds?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfterSeconds = retryAfterSeconds
  }
}

export interface RequestOptions {
  body?: unknown
  query?: Record<string, QueryValue>
  /** Send the bearer token and handle 401 by refreshing. Default true. */
  auth?: boolean
  headers?: Record<string, string>
  signal?: AbortSignal
}

function isErrorBody(v: unknown): v is ErrorBody {
  if (typeof v !== 'object' || v === null) return false
  const e = (v as { error?: unknown }).error
  return typeof e === 'object' && e !== null && typeof (e as { code?: unknown }).code === 'string'
}

async function toApiError(res: Response): Promise<ApiError> {
  let parsed: unknown = null
  try {
    parsed = await res.clone().json()
  } catch {
    parsed = null
  }
  if (isErrorBody(parsed)) {
    const e = parsed.error
    return new ApiError(res.status, e.code, e.message ?? res.statusText, e.retryAfterSeconds)
  }
  const header = Number(res.headers.get('Retry-After'))
  return new ApiError(
    res.status,
    `http_${res.status}`,
    res.statusText || `HTTP ${res.status}`,
    Number.isFinite(header) && header > 0 ? header : undefined,
  )
}

async function send(method: string, path: string, opts: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    'Shinsou-Api-Version': '1',
    ...opts.headers,
  }
  if (opts.auth !== false && session.accessToken) {
    headers.Authorization = `Bearer ${session.accessToken}`
  }
  const init: RequestInit = { method, headers }
  if (opts.signal) init.signal = opts.signal
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(opts.body)
  }
  try {
    return await fetch(path + buildQuery(opts.query), init)
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, 'network', 'Network error')
  }
}

/**
 * Performs a request and returns the successful Response. On `401` the access token is refreshed
 * once and the request is repeated once; if the refresh is refused the session is cleared and the
 * router is told to go to the sign-in page. Non-2xx responses throw ApiError.
 */
export async function request(
  method: string,
  path: string,
  opts: RequestOptions = {},
): Promise<Response> {
  let res = await send(method, path, opts)
  if (res.status === 401 && opts.auth !== false) {
    const first = await toApiError(res)
    // A wrong password is a 401 too, but not a session problem.
    if (first.code === 'invalid_credentials') throw first
    const outcome = await refreshSession()
    if (outcome === 'denied') {
      handleAuthLost()
      throw first
    }
    if (outcome === 'failed') throw first
    res = await send(method, path, opts)
    if (res.status === 401) {
      const second = await toApiError(res)
      if (second.code !== 'invalid_credentials') handleAuthLost()
      throw second
    }
  }
  if (!res.ok) throw await toApiError(res)
  return res
}

export async function json<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await request(method, path, opts)
  return (await res.json()) as T
}

export async function noContent(
  method: string,
  path: string,
  opts: RequestOptions = {},
): Promise<void> {
  await request(method, path, opts)
}
