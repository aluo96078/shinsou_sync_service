/** Extracts `token` from a location hash such as "#token=abc". */
export function parseHashToken(hash: string): string | null {
  const h = hash.startsWith('#') ? hash.slice(1) : hash
  if (!h) return null
  const token = new URLSearchParams(h).get('token')
  return token && token.length > 0 ? token : null
}

/**
 * Reads the token from the address bar and removes the fragment from it, so the token does not
 * stay in the visible URL or in history. `history.state` is preserved for vue-router.
 */
export function consumeHashToken(
  loc: Pick<Location, 'hash' | 'pathname' | 'search'> = window.location,
  hist: Pick<History, 'replaceState' | 'state'> = window.history,
): string | null {
  const token = parseHashToken(loc.hash)
  if (loc.hash) hist.replaceState(hist.state, '', loc.pathname + loc.search)
  return token
}
