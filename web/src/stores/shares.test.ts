import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { useGate } from '@/lib/useGate'

const getShareSummary = vi.fn<() => Promise<{ pending: number }>>()
vi.mock('@/api/endpoints', () => ({ getShareSummary: () => getShareSummary() }))

import { clearPendingShares, pendingShares, refreshPendingShares } from './shares'

describe('pending share count', () => {
  beforeEach(() => {
    getShareSummary.mockReset()
    clearPendingShares()
  })

  it('takes the count from the server', async () => {
    getShareSummary.mockResolvedValue({ pending: 3 })
    await refreshPendingShares()
    expect(pendingShares.value).toBe(3)
  })

  it('shows nothing when the request fails', async () => {
    getShareSummary.mockResolvedValue({ pending: 2 })
    await refreshPendingShares()
    getShareSummary.mockRejectedValue(new ApiError(403, 'feature_disabled', 'off'))
    await refreshPendingShares()
    expect(pendingShares.value).toBe(0)
  })

  it('ignores an answer that arrives after a newer request', async () => {
    let finishFirst: (v: { pending: number }) => void = () => {}
    getShareSummary.mockReturnValueOnce(new Promise((resolve) => (finishFirst = resolve)))
    getShareSummary.mockResolvedValueOnce({ pending: 1 })
    const first = refreshPendingShares()
    await refreshPendingShares()
    finishFirst({ pending: 9 })
    await first
    expect(pendingShares.value).toBe(1)
  })

  it('ignores an answer that arrives after signing out', async () => {
    let finish: (v: { pending: number }) => void = () => {}
    getShareSummary.mockReturnValueOnce(new Promise((resolve) => (finish = resolve)))
    const pending = refreshPendingShares()
    clearPendingShares()
    finish({ pending: 4 })
    await pending
    expect(pendingShares.value).toBe(0)
  })
})

describe('useGate', () => {
  it('tells the closed states apart from ordinary errors', () => {
    const gate = useGate()
    gate.fail(new ApiError(403, 'email_unverified', 'x'))
    expect([gate.unverified.value, gate.disabled.value, gate.error.value]).toEqual([true, false, null])
    gate.reset()
    gate.fail(new ApiError(403, 'feature_disabled', 'x'))
    expect([gate.unverified.value, gate.disabled.value, gate.error.value]).toEqual([false, true, null])
    gate.reset()
    gate.fail(new ApiError(500, 'internal', 'x'))
    expect(gate.unverified.value || gate.disabled.value).toBe(false)
    expect(gate.error.value).toBeTruthy()
  })
})
