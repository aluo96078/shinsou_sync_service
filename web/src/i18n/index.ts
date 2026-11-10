import { computed, ref } from 'vue'
import type { ErrorCode } from '@/api/types'
import { ApiError } from '@/api/client'
import { en } from './en'
import { zhHant, type MessageKey } from './zh-Hant'

export type { MessageKey }
export type Locale = 'zh-Hant' | 'en'
export const LOCALES: readonly Locale[] = ['zh-Hant', 'en']
export const LOCALE_NAMES: Record<Locale, string> = { 'zh-Hant': '繁體中文', en: 'English' }

const dictionaries: Record<Locale, Record<MessageKey, string>> = { 'zh-Hant': zhHant, en }

// Compile-time check: every API error code has a message (a new code in the OpenAPI file
// without a dictionary entry fails the type check here).
type MissingErrorKeys = Exclude<`error.${ErrorCode}`, MessageKey>
const _allErrorCodesTranslated: [MissingErrorKeys] extends [never] ? true : never = true
void _allErrorCodesTranslated

const STORAGE_KEY = 'shinsou.locale'

/** Picks a locale from navigator.languages: Chinese variants map to zh-Hant, English to en. */
export function detectLocale(languages: readonly string[] | undefined): Locale {
  for (const lang of languages ?? []) {
    const l = lang.toLowerCase()
    if (l.startsWith('zh')) return 'zh-Hant'
    if (l.startsWith('en')) return 'en'
  }
  return 'zh-Hant'
}

function isLocale(v: unknown): v is Locale {
  return v === 'zh-Hant' || v === 'en'
}

function loadStored(): Locale | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return isLocale(v) ? v : null
  } catch {
    return null
  }
}

export const locale = ref<Locale>(
  loadStored() ?? detectLocale(typeof navigator !== 'undefined' ? navigator.languages : undefined),
)

export function applyLocaleToDocument(): void {
  if (typeof document !== 'undefined') document.documentElement.lang = locale.value
}

export function setLocale(next: Locale, remember = true): void {
  locale.value = next
  applyLocaleToDocument()
  if (!remember) return
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // Preference simply is not remembered.
  }
}

export function interpolate(template: string, params?: Record<string, string | number>): string {
  if (!params) return template
  return template.replace(/\{(\w+)\}/g, (m, name: string) => {
    const v = params[name]
    return v === undefined ? m : String(v)
  })
}

export function t(key: MessageKey, params?: Record<string, string | number>): string {
  const dict = dictionaries[locale.value]
  return interpolate(dict[key] ?? zhHant[key], params)
}

export function hasMessage(key: string): key is MessageKey {
  return key in zhHant
}

/** User-facing text for any thrown value. Unknown API codes fall back to the server message. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const key = `error.${err.code}`
    if (hasMessage(key)) {
      let text = t(key)
      if (err.code === 'rate_limited' && err.retryAfterSeconds) {
        text += ' ' + t('error.rateLimitedWait', { seconds: err.retryAfterSeconds })
      }
      return text
    }
    return err.message || t('error.unknown')
  }
  return t('error.unknown')
}

// --- Intl formatting ---------------------------------------------------------------------------

const intlLocale = computed(() => (locale.value === 'en' ? 'en' : 'zh-Hant'))

export function formatDate(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return '—'
  return new Intl.DateTimeFormat(intlLocale.value, { dateStyle: 'medium' }).format(new Date(ms))
}

export function formatDateTime(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return '—'
  return new Intl.DateTimeFormat(intlLocale.value, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(ms))
}

export function formatNumber(n: number): string {
  return new Intl.NumberFormat(intlLocale.value).format(n)
}

/** A decimal number with exactly `digits` fraction digits at most and at least one. */
export function formatDecimal(n: number, digits = 2): string {
  return new Intl.NumberFormat(intlLocale.value, {
    minimumFractionDigits: 1,
    maximumFractionDigits: digits,
  }).format(n)
}

export function formatPercent(ratio: number): string {
  return new Intl.NumberFormat(intlLocale.value, {
    style: 'percent',
    maximumFractionDigits: 0,
  }).format(ratio)
}
