import { ref } from 'vue'
import { reloadMe } from '@/api/auth'
import { ApiError } from '@/api/client'
import { resendVerification } from '@/api/endpoints'
import { errorMessage, t } from '@/i18n'

/** Resend-verification-mail action shared by the banner, the library gate and the account page. */
export function useResendVerification() {
  const busy = ref(false)
  const message = ref<string | null>(null)
  const failed = ref(false)

  async function resend(): Promise<void> {
    if (busy.value) return
    busy.value = true
    message.value = null
    failed.value = false
    try {
      await resendVerification()
      message.value = t('banner.resent')
    } catch (e) {
      failed.value = true
      if (e instanceof ApiError && e.code === 'conflict') {
        // Already verified (for example in another tab): refresh the profile.
        await reloadMe().catch(() => undefined)
        failed.value = false
      } else {
        message.value = errorMessage(e)
      }
    } finally {
      busy.value = false
    }
  }

  return { busy, message, failed, resend }
}
