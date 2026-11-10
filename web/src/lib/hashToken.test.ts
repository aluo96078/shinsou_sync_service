import { describe, expect, it, vi } from 'vitest'
import { consumeHashToken, parseHashToken } from './hashToken'

describe('parseHashToken', () => {
  it('reads the token', () => {
    expect(parseHashToken('#token=abc-DEF_123')).toBe('abc-DEF_123')
    expect(parseHashToken('token=abc')).toBe('abc')
  })
  it('decodes percent-encoding and ignores other keys', () => {
    expect(parseHashToken('#x=1&token=a%2Bb')).toBe('a+b')
  })
  it('returns null when there is no token', () => {
    expect(parseHashToken('')).toBeNull()
    expect(parseHashToken('#')).toBeNull()
    expect(parseHashToken('#token=')).toBeNull()
    expect(parseHashToken('#other=1')).toBeNull()
  })
})

describe('consumeHashToken', () => {
  it('returns the token and strips the fragment, keeping path, query and history state', () => {
    const replaceState = vi.fn()
    const token = consumeHashToken(
      { hash: '#token=secret', pathname: '/reset-password', search: '?a=1' },
      { replaceState, state: { position: 3 } },
    )
    expect(token).toBe('secret')
    expect(replaceState).toHaveBeenCalledWith({ position: 3 }, '', '/reset-password?a=1')
  })
  it('leaves the address alone when there is no fragment', () => {
    const replaceState = vi.fn()
    expect(consumeHashToken({ hash: '', pathname: '/verify-email', search: '' }, { replaceState, state: null })).toBeNull()
    expect(replaceState).not.toHaveBeenCalled()
  })
  it('strips a fragment even if it holds no token', () => {
    const replaceState = vi.fn()
    expect(consumeHashToken({ hash: '#junk', pathname: '/verify-email', search: '' }, { replaceState, state: null })).toBeNull()
    expect(replaceState).toHaveBeenCalledOnce()
  })
})
