/** Reads one cookie from a `document.cookie`-style string. */
export function parseCookie(cookieString: string, name: string): string | null {
  for (const part of cookieString.split(';')) {
    const eq = part.indexOf('=')
    if (eq < 0) continue
    if (part.slice(0, eq).trim() !== name) continue
    const raw = part.slice(eq + 1).trim()
    try {
      return decodeURIComponent(raw)
    } catch {
      return raw
    }
  }
  return null
}

export const CSRF_COOKIE = 'shinsou_csrf'

export function readCsrfCookie(): string | null {
  if (typeof document === 'undefined') return null
  const v = parseCookie(document.cookie, CSRF_COOKIE)
  return v && v.length > 0 ? v : null
}
