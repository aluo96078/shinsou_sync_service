import { reactive } from 'vue'
import { readCsrfCookie } from '@/lib/cookies'
import type { User, WebTokens } from './types'

/**
 * In-memory session. The access token is never written to localStorage, sessionStorage or a
 * cookie; the refresh token lives in an HttpOnly cookie the page cannot read.
 */
export const session = reactive<{
  accessToken: string | null
  expiresAt: number
  user: User | null
}>({ accessToken: null, expiresAt: 0, user: null })

export const REFRESH_LOCK = 'shinsou-refresh'
/** Refresh this long before the access token expires. */
export const REFRESH_LEAD_MS = 60_000

export type RefreshResult = 'ok' | 'denied' | 'failed'

let timer: ReturnType<typeof setTimeout> | null = null
let inflight: Promise<RefreshResult> | null = null
let authLostHandler: (() => void) | null = null

export function isSignedIn(): boolean {
  return session.user !== null
}

export function onAuthLost(handler: (() => void) | null): void {
  authLostHandler = handler
}

export function setTokens(tokens: Pick<WebTokens, 'accessToken' | 'accessTokenExpiresAt'>): void {
  session.accessToken = tokens.accessToken
  session.expiresAt = tokens.accessTokenExpiresAt
  scheduleRefresh()
}

export function clearSession(): void {
  session.accessToken = null
  session.expiresAt = 0
  session.user = null
  if (timer) clearTimeout(timer)
  timer = null
}

/** Clears the session and lets the router send the user to the sign-in page. */
export function handleAuthLost(): void {
  const wasSignedIn = session.user !== null || session.accessToken !== null
  clearSession()
  if (wasSignedIn) authLostHandler?.()
}

function scheduleRefresh(): void {
  if (timer) clearTimeout(timer)
  timer = null
  if (!session.accessToken) return
  const delay = Math.max(session.expiresAt - Date.now() - REFRESH_LEAD_MS, 5_000)
  timer = setTimeout(() => {
    void refreshSession().then((r) => {
      if (r === 'denied') handleAuthLost()
    })
  }, delay)
}

/** True when the token expires within the refresh lead time (used when a tab wakes up). */
export function tokenIsStale(): boolean {
  return session.accessToken !== null && session.expiresAt - Date.now() < REFRESH_LEAD_MS
}

/**
 * Runs fn while holding the cross-tab lock that guards the session cookies. Anything that reads
 * the CSRF cookie and then sends it (refresh, logout) has to run under it, or a rotation in
 * another tab can slip in between.
 */
export async function withRefreshLock<T>(fn: () => Promise<T>): Promise<T> {
  const locks = typeof navigator !== 'undefined' ? navigator.locks : undefined
  if (locks && typeof locks.request === 'function') return locks.request(REFRESH_LOCK, fn)
  return fn()
}

async function doRefresh(): Promise<RefreshResult> {
  // The CSRF cookie is read only once the lock is held: another tab may have rotated it.
  const csrf = readCsrfCookie()
  if (!csrf) return 'denied'
  let res: Response
  try {
    res = await fetch('/api/v1/web/refresh', {
      method: 'POST',
      headers: {
        'X-CSRF-Token': csrf,
        'Shinsou-Api-Version': '1',
        Accept: 'application/json',
      },
    })
  } catch {
    return 'failed'
  }
  if (res.status === 401 || res.status === 403) return 'denied'
  if (!res.ok) return 'failed'
  try {
    const body = (await res.json()) as WebTokens
    if (typeof body.accessToken !== 'string') return 'failed'
    setTokens(body)
    return 'ok'
  } catch {
    return 'failed'
  }
}

/**
 * Obtains a new access token from the refresh cookie. Concurrent callers in this tab share one
 * request; across tabs the Web Locks API serialises them.
 */
export function refreshSession(): Promise<RefreshResult> {
  if (!inflight) {
    inflight = withRefreshLock(doRefresh).finally(() => {
      inflight = null
    })
  }
  return inflight
}
