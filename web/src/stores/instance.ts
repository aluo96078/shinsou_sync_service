import { computed, ref } from 'vue'
import { getInstance } from '@/api/endpoints'
import type { Instance } from '@/api/types'
import { safeHttpUrl } from '@/lib/redirect'

export const instance = ref<Instance | null>(null)
export const instanceError = ref<unknown>(null)
let loading: Promise<void> | null = null

export function loadInstance(force = false): Promise<void> {
  if (loading && !force) return loading
  loading = getInstance()
    .then((i) => {
      instance.value = i
      instanceError.value = null
    })
    .catch((e: unknown) => {
      instanceError.value = e
      loading = null
    })
  return loading
}

// Fields an older server does not send are treated as absent / false.
export const setupPending = computed(() => instance.value?.setupPending === true)
export const emailFeature = computed(() => instance.value?.features?.email === true)
export const sharingFeature = computed(() => instance.value?.features?.sharing === true)
export const registrationMode = computed(() => instance.value?.registration ?? 'open')
export const verificationPolicy = computed(() => instance.value?.emailVerification ?? 'disabled')
/** Whether the register page should be offered. */
export const canRegister = computed(
  () => registrationMode.value !== 'closed' || setupPending.value,
)
export const termsUrl = computed(() => safeHttpUrl(instance.value?.termsUrl))
export const privacyUrl = computed(() => safeHttpUrl(instance.value?.privacyUrl))
export const instanceName = computed(() => instance.value?.name || 'Shinsou Sync')
