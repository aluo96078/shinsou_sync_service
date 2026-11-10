import { describe, expect, it } from 'vitest'
import { redirectOrHome, safeCoverUrl, safeHttpUrl, safeRedirect } from './redirect'

describe('safeRedirect', () => {
  it('accepts same-origin paths', () => {
    expect(safeRedirect('/')).toBe('/')
    expect(safeRedirect('/library/local:abc?x=1#y')).toBe('/library/local:abc?x=1#y')
    expect(safeRedirect(['/account', '/other'])).toBe('/account')
  })

  it.each([
    'https://evil.example/',
    '//evil.example/path',
    '/\\evil.example',
    '\\\\evil.example',
    'javascript:alert(1)',
    'account',
    '',
    '/ok\nLocation: x',
    '/a\u0000b',
  ])('rejects %j', (value) => {
    expect(safeRedirect(value)).toBeNull()
  })

  it('rejects non-strings', () => {
    expect(safeRedirect(undefined)).toBeNull()
    expect(safeRedirect(null)).toBeNull()
    expect(safeRedirect(42)).toBeNull()
    expect(safeRedirect('/' + 'a'.repeat(3000))).toBeNull()
  })
})

describe('redirectOrHome', () => {
  it('falls back to /', () => {
    expect(redirectOrHome('https://evil.example')).toBe('/')
    expect(redirectOrHome(undefined)).toBe('/')
  })
  it('does not bounce back to the login page', () => {
    expect(redirectOrHome('/login?redirect=/x')).toBe('/')
    expect(redirectOrHome('/register')).toBe('/')
  })
  it('keeps valid targets', () => {
    expect(redirectOrHome('/devices')).toBe('/devices')
  })
})

describe('url guards', () => {
  it('only renders https covers', () => {
    expect(safeCoverUrl('https://cdn.example/c.jpg')).toBe('https://cdn.example/c.jpg')
    expect(safeCoverUrl('http://cdn.example/c.jpg')).toBeNull()
    expect(safeCoverUrl('javascript:alert(1)')).toBeNull()
    expect(safeCoverUrl('data:image/png;base64,AAAA')).toBeNull()
    expect(safeCoverUrl(null)).toBeNull()
    expect(safeCoverUrl('not a url')).toBeNull()
  })
  it('only links http(s) for terms and privacy', () => {
    expect(safeHttpUrl('https://example.com/terms')).toBe('https://example.com/terms')
    expect(safeHttpUrl('http://example.com/')).toBe('http://example.com/')
    expect(safeHttpUrl('javascript:alert(1)')).toBeNull()
    expect(safeHttpUrl('')).toBeNull()
  })
})
