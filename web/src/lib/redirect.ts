/**
 * Accepts only same-origin absolute paths ("/foo?x=1#y"). Anything else — absolute URLs,
 * protocol-relative "//host", backslash tricks, control characters — is rejected.
 */
export function safeRedirect(value: unknown): string | null {
  if (Array.isArray(value)) value = value[0]
  if (typeof value !== 'string') return null
  if (value.length === 0 || value.length > 2048) return null
  if (!value.startsWith('/')) return null
  if (value.startsWith('//')) return null
  if (value.includes('\\')) return null
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(value)) return null
  return value
}

/** Same as safeRedirect but falls back to "/" and never returns a login/auth page loop. */
export function redirectOrHome(value: unknown): string {
  const target = safeRedirect(value)
  if (!target) return '/'
  const path = target.split(/[?#]/)[0]
  if (path === '/login' || path === '/register') return '/'
  return target
}

/** Only `https:` cover URLs are ever rendered. */
export function safeCoverUrl(value: string | null | undefined): string | null {
  if (!value) return null
  try {
    const u = new URL(value)
    return u.protocol === 'https:' ? u.href : null
  } catch {
    return null
  }
}

/** Terms/privacy links: http(s) only. */
export function safeHttpUrl(value: string | null | undefined): string | null {
  if (!value) return null
  try {
    const u = new URL(value)
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.href : null
  } catch {
    return null
  }
}
