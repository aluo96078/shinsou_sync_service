import type { ReportReason } from '@/api/types'

// Limits from docs/spec/reviews.md.
export const MAX_REVIEW_CHARS = 5000
export const MAX_REPLY_CHARS = 2000
export const MAX_NOTE_CHARS = 500
export const MAX_REASON_CHARS = 500

export const REPORT_REASONS: readonly ReportReason[] = ['spam', 'harassment', 'spoiler', 'illegal', 'other']

/** Length the way the server counts it: Unicode code points of the trimmed text. */
export function charCount(text: string): number {
  return Array.from(text.trim()).length
}

/** A review needs a rating or some text, and the text has to fit. */
export function canSubmitReview(rating: number | null, body: string): boolean {
  return (rating !== null || body.trim() !== '') && charCount(body) <= MAX_REVIEW_CHARS
}

/** Whole stars 1 to 5, or null for anything else. */
export function toRating(value: unknown): number | null {
  return typeof value === 'number' && Number.isInteger(value) && value >= 1 && value <= 5 ? value : null
}

/** Offset of the last page, used to step back when the last item of a page was removed. */
export function lastPageOffset(total: number, limit: number): number {
  return Math.max(0, Math.floor((total - 1) / limit) * limit)
}
