/** Filename from a Content-Disposition header (RFC 6266 / 5987 subset), sanitised for saving. */
export function filenameFromDisposition(header: string | null, fallback: string): string {
  if (!header) return fallback
  let name: string | null = null
  const star = /filename\*\s*=\s*([^']*)'[^']*'([^;]+)/i.exec(header)
  if (star?.[2]) {
    try {
      name = decodeURIComponent(star[2].trim())
    } catch {
      name = null
    }
  }
  if (!name) {
    const quoted = /filename\s*=\s*"((?:[^"\\]|\\.)*)"/i.exec(header)
    if (quoted?.[1] !== undefined) name = quoted[1].replace(/\\(.)/g, '$1')
    else {
      const bare = /filename\s*=\s*([^;]+)/i.exec(header)
      if (bare?.[1]) name = bare[1].trim()
    }
  }
  if (!name) return fallback
  name = name.replace(/[\\/:*?"<>|\u0000-\u001f]/g, '_').trim()
  return name.length > 0 ? name : fallback
}
