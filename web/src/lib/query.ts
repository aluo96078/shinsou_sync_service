export type QueryValue = string | number | boolean | null | undefined

/** Builds "?a=1&b=2", skipping undefined, null and empty strings. */
export function buildQuery(params: Record<string, QueryValue> | undefined): string {
  if (!params) return ''
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue
    sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}
