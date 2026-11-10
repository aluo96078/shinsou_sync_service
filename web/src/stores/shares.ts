import { ref } from 'vue'
import { getShareSummary } from '@/api/endpoints'

/** Received shares waiting for an answer; drives the badge in the navigation. */
export const pendingShares = ref(0)

let seq = 0

/**
 * Reloads the count. Failures (sharing turned off, unverified email, offline) leave the badge
 * empty: the pages themselves explain what is wrong.
 */
export async function refreshPendingShares(): Promise<void> {
  const mine = ++seq
  try {
    const { pending } = await getShareSummary()
    if (mine === seq) pendingShares.value = pending
  } catch {
    if (mine === seq) pendingShares.value = 0
  }
}

export function clearPendingShares(): void {
  seq++
  pendingShares.value = 0
}
