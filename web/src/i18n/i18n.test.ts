import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError } from '@/api/client'
import { detectLocale, errorMessage, interpolate, setLocale, t } from './index'

describe('detectLocale', () => {
  it('maps Chinese variants to zh-Hant and English to en', () => {
    expect(detectLocale(['zh-TW'])).toBe('zh-Hant')
    expect(detectLocale(['zh-CN'])).toBe('zh-Hant')
    expect(detectLocale(['en-US', 'zh-TW'])).toBe('en')
    expect(detectLocale(['fr-FR', 'en-GB'])).toBe('en')
  })
  it('falls back to zh-Hant', () => {
    expect(detectLocale(['fr-FR', 'de'])).toBe('zh-Hant')
    expect(detectLocale([])).toBe('zh-Hant')
    expect(detectLocale(undefined)).toBe('zh-Hant')
  })
})

describe('interpolate', () => {
  it('replaces known params and keeps unknown placeholders', () => {
    expect(interpolate('{a} and {b}', { a: 1 })).toBe('1 and {b}')
  })
})

describe('errorMessage', () => {
  beforeEach(() => setLocale('en', false))

  it('translates known codes', () => {
    expect(errorMessage(new ApiError(401, 'invalid_credentials', 'raw'))).toBe(t('error.invalid_credentials'))
  })
  it('falls back to the server message for unknown codes', () => {
    expect(errorMessage(new ApiError(418, 'teapot', 'I am a teapot'))).toBe('I am a teapot')
  })
  it('appends the retry hint for rate limits', () => {
    const msg = errorMessage(new ApiError(429, 'rate_limited', 'slow down', 42))
    expect(msg).toContain('42')
  })
  it('handles non-API errors', () => {
    expect(errorMessage(new Error('boom'))).toBe(t('error.unknown'))
  })
  it('follows the active language', () => {
    setLocale('zh-Hant', false)
    expect(errorMessage(new ApiError(403, 'forbidden', 'x'))).toBe('你沒有權限執行這個操作。')
  })
})
