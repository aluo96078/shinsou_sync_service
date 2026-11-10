import { ref } from 'vue'
import { errorMessage } from '@/i18n'

/** Wraps an async action with busy/error state. Resolves to the result, or undefined on error. */
export function useAction() {
  const busy = ref(false)
  const error = ref<string | null>(null)
  const lastError = ref<unknown>(null)

  async function run<T>(fn: () => Promise<T>): Promise<T | undefined> {
    if (busy.value) return undefined
    busy.value = true
    error.value = null
    lastError.value = null
    try {
      return await fn()
    } catch (e) {
      lastError.value = e
      error.value = errorMessage(e)
      return undefined
    } finally {
      busy.value = false
    }
  }

  function reset(): void {
    error.value = null
    lastError.value = null
  }

  return { busy, error, lastError, run, reset }
}
