import { ref } from 'vue'
import { ApiError } from '@/api/client'
import { errorMessage } from '@/i18n'

/**
 * Load state for the social pages: besides an ordinary error they can be closed because the
 * e-mail address is not verified or because the operator turned sharing off.
 */
export function useGate() {
  const unverified = ref(false)
  const disabled = ref(false)
  const error = ref<string | null>(null)

  function reset(): void {
    unverified.value = false
    disabled.value = false
    error.value = null
  }

  function fail(e: unknown): void {
    if (e instanceof ApiError && e.code === 'email_unverified') unverified.value = true
    else if (e instanceof ApiError && e.code === 'feature_disabled') disabled.value = true
    else error.value = errorMessage(e)
  }

  return { unverified, disabled, error, reset, fail }
}
