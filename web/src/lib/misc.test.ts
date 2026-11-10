import { describe, expect, it } from 'vitest'
import { filenameFromDisposition } from './contentDisposition'
import { parseCookie } from './cookies'
import { buildQuery } from './query'

describe('filenameFromDisposition', () => {
  it('reads a quoted filename', () => {
    expect(
      filenameFromDisposition('attachment; filename="shinsou-export-aluo-20261110.json"', 'x.json'),
    ).toBe('shinsou-export-aluo-20261110.json')
  })
  it('prefers filename*', () => {
    expect(
      filenameFromDisposition(`attachment; filename="a.json"; filename*=UTF-8''%E6%9B%B8.json`, 'x.json'),
    ).toBe('書.json')
  })
  it('sanitises path separators and falls back', () => {
    expect(filenameFromDisposition('attachment; filename="../../etc/passwd"', 'x.json')).toBe('.._.._etc_passwd')
    expect(filenameFromDisposition(null, 'x.json')).toBe('x.json')
    expect(filenameFromDisposition('attachment', 'x.json')).toBe('x.json')
  })
})

describe('parseCookie', () => {
  it('finds a cookie by exact name', () => {
    const c = 'a=1; shinsou_csrf=tok-en_1; other_shinsou_csrf=nope'
    expect(parseCookie(c, 'shinsou_csrf')).toBe('tok-en_1')
    expect(parseCookie(c, 'missing')).toBeNull()
  })
})

describe('buildQuery', () => {
  it('skips empty values', () => {
    expect(buildQuery({ q: 'a b', offset: 0, category: '', x: undefined, y: null })).toBe('?q=a+b&offset=0')
    expect(buildQuery({})).toBe('')
  })
})
