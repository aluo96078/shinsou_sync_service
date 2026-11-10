import { describe, expect, it } from 'vitest'
import { canSubmitReview, charCount, lastPageOffset, MAX_REVIEW_CHARS, toRating } from './reviews'

describe('charCount', () => {
  it('counts code points of the trimmed text', () => {
    expect(charCount('  好書  ')).toBe(2)
    expect(charCount('a😀b')).toBe(3)
    expect(charCount('   ')).toBe(0)
  })
})

describe('canSubmitReview', () => {
  it('needs a rating or a body', () => {
    expect(canSubmitReview(null, '  ')).toBe(false)
    expect(canSubmitReview(3, '')).toBe(true)
    expect(canSubmitReview(null, 'text')).toBe(true)
  })
  it('rejects a body over the limit', () => {
    expect(canSubmitReview(5, '😀'.repeat(MAX_REVIEW_CHARS))).toBe(true)
    expect(canSubmitReview(5, '😀'.repeat(MAX_REVIEW_CHARS + 1))).toBe(false)
  })
})

describe('toRating', () => {
  it('accepts whole stars only', () => {
    expect(toRating(1)).toBe(1)
    expect(toRating(5)).toBe(5)
    expect(toRating(0)).toBeNull()
    expect(toRating(6)).toBeNull()
    expect(toRating(2.5)).toBeNull()
    expect(toRating('3')).toBeNull()
    expect(toRating(null)).toBeNull()
  })
})

describe('lastPageOffset', () => {
  it('points at the last page', () => {
    expect(lastPageOffset(41, 20)).toBe(40)
    expect(lastPageOffset(40, 20)).toBe(20)
    expect(lastPageOffset(0, 20)).toBe(0)
  })
})
