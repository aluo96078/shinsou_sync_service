import { readCsrfCookie } from '@/lib/cookies'
import * as endpoints from './endpoints'
import {
  clearSession,
  refreshSession,
  session,
  setTokens,
  tokenIsStale,
  handleAuthLost,
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

export async function logout(): Promise<void> {
  try {
    await endpoints.webLogout()
  } catch {
    // Even if the server cannot be reached, forget the session locally.
  } finally {
    clearSession()
  }
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
