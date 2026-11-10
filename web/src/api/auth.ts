import { readCsrfCookie } from '@/lib/cookies'
import { ApiError } from './client'
import * as endpoints from './endpoints'
import {
  clearSession,
  refreshSession,
  session,
  setTokens,
  tokenIsStale,
  handleAuthLost,
  withRefreshLock,
} from './session'
import type { WebLoginRequest, WebRegisterRequest, WebSession } from './types'

function adopt(s: WebSession): void {
  setTokens(s)
  session.user = s.user
}

export async function login(body: WebLoginRequest): Promise<void> {
  adopt(await endpoints.webLogin(body))
}

export async function register(body: WebRegisterRequest): Promise<void> {
  adopt(await endpoints.webRegister(body))
}

/** Reloads `/me` (after verifying an email, changing it, ...). */
export async function reloadMe(): Promise<void> {
  session.user = await endpoints.getMe()
}

/**
 * Signs out on the server, then forgets the session locally. When the server refuses, the session
 * is kept and the error is thrown: the refresh cookie is still valid, so pretending otherwise would
 * sign the user back in on the next reload.
 */
export async function logout(): Promise<void> {
  try {
    // Under the refresh lock, so a rotation in another tab cannot swap the CSRF cookie mid-way.
    await withRefreshLock(() => endpoints.webLogout())
  } catch (e) {
    if (e instanceof ApiError && e.status !== 401) throw e
    // The server cannot be reached, or the session is gone already: forget it locally.
  }
  clearSession()
}

let ready: Promise<void> | null = null

/**
 * Restores a session from the refresh cookie, if there is one. The router waits for this before
 * the first navigation resolves.
 */
export function sessionReady(): Promise<void> {
  if (!ready) ready = restore()
  return ready
}

async function restore(): Promise<void> {
  if (readCsrfCookie()) {
    const outcome = await refreshSession()
    if (outcome === 'ok') {
      try {
        session.user = await endpoints.getMe()
      } catch {
        clearSession()
      }
    }
  }
  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible' && tokenIsStale()) {
        void refreshSession().then((r) => {
          if (r === 'denied') handleAuthLost()
        })
      }
    })
  }
}
