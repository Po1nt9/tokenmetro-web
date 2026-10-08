/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { renderHook, act } from '@testing-library/react'
import { toast } from 'sonner'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { getSelf } from '@/lib/api'
import { formatQuota } from '@/lib/format'

import { previewRedemptionCode, redeemTopupCode } from '../../api'
import { useRedemption } from '../use-redemption'

vi.mock('@/lib/api', () => ({ getSelf: vi.fn() }))
vi.mock('@/lib/format', () => ({
  formatQuota: vi.fn((quota: number) => `quota:${quota}`),
}))
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
vi.mock('../../api', () => ({
  previewRedemptionCode: vi.fn(),
  redeemTopupCode: vi.fn(),
}))

describe('wallet redemption outcomes', () => {
  beforeEach(() => vi.clearAllMocks())

  test('previews a redemption without executing it and retains only the server summary', async () => {
    const summary = {
      outcome_type: 'balance' as const,
      wallet_quota: 750,
      balance_after: 1000,
    }
    vi.mocked(previewRedemptionCode).mockResolvedValue({
      success: true,
      data: summary,
    })
    const { result } = renderHook(() => useRedemption())

    await act(async () =>
      expect(await result.current.previewCode('TEST-CODE')).toBe(true)
    )

    expect(result.current.preview).toEqual(summary)
    expect(previewRedemptionCode).toHaveBeenCalledWith({ key: 'TEST-CODE' })
    expect(redeemTopupCode).not.toHaveBeenCalled()
  })

  test('ignores a delayed preview after the code is edited and cannot confirm it for the new code', async () => {
    let finishPreview: (response: {
      success: boolean
      data: { outcome_type: 'balance'; wallet_quota: number }
    }) => void = () => {}
    vi.mocked(previewRedemptionCode).mockImplementation(
      () =>
        new Promise((resolve) => {
          finishPreview = resolve
        })
    )
    const { result } = renderHook(() => useRedemption())

    let previewRequest: Promise<boolean> | undefined
    act(() => {
      previewRequest = result.current.previewCode('CODE-A')
    })
    act(() => result.current.clearPreview())
    finishPreview({
      success: true,
      data: { outcome_type: 'balance', wallet_quota: 750 },
    })

    if (!previewRequest) throw new Error('Preview request was not started')
    await act(async () => expect(await previewRequest).toBe(false))
    await act(async () =>
      expect(await result.current.confirmRedemption('CODE-B')).toBe(false)
    )

    expect(result.current.preview).toBeNull()
    expect(redeemTopupCode).not.toHaveBeenCalled()
  })

  test('confirms only the code whose preview is currently bound', async () => {
    vi.mocked(previewRedemptionCode).mockResolvedValue({
      success: true,
      data: { outcome_type: 'balance', wallet_quota: 750 },
    })
    vi.mocked(redeemTopupCode).mockResolvedValue({ success: true, data: 750 })
    const { result } = renderHook(() => useRedemption())

    await act(async () => result.current.previewCode('CODE-A'))
    await act(async () =>
      expect(await result.current.confirmRedemption('CODE-B')).toBe(false)
    )
    expect(redeemTopupCode).not.toHaveBeenCalled()

    await act(async () =>
      expect(await result.current.confirmRedemption('CODE-A')).toBe(true)
    )
    expect(redeemTopupCode).toHaveBeenCalledWith({ key: 'CODE-A' })
  })

  test('clears a stale preview when server preview validation fails', async () => {
    vi.mocked(previewRedemptionCode).mockResolvedValue({
      success: false,
      message: 'Redemption failed',
    })
    const { result } = renderHook(() => useRedemption())

    await act(async () =>
      expect(await result.current.previewCode('TEST-CODE')).toBe(false)
    )

    expect(result.current.preview).toBeNull()
    expect(redeemTopupCode).not.toHaveBeenCalled()
  })

  test('clears preview and reports failure when code was used before confirmation', async () => {
    vi.mocked(redeemTopupCode).mockResolvedValue({
      success: false,
      message: 'Redemption failed',
    })
    const { result } = renderHook(() => useRedemption())

    await act(async () => {
      await result.current.previewCode('TEST-CODE')
      await result.current.confirmRedemption('TEST-CODE')
    })

    expect(result.current.preview).toBeNull()
    expect(toast.success).not.toHaveBeenCalled()
    expect(getSelf).not.toHaveBeenCalled()
  })

  test('shows the credited wallet quota for a structured balance redemption', async () => {
    vi.mocked(redeemTopupCode).mockResolvedValue({
      success: true,
      data: { outcome_type: 'balance', wallet_quota: 750 },
    })
    vi.mocked(previewRedemptionCode).mockResolvedValue({
      success: true,
      data: { outcome_type: 'balance', wallet_quota: 750 },
    })
    const { result } = renderHook(() => useRedemption())

    await act(async () => result.current.previewCode('BALANCE'))
    await act(async () =>
      expect(await result.current.confirmRedemption('BALANCE')).toBe(true)
    )

    expect(formatQuota).toHaveBeenCalledWith(750)
    expect(toast.success).toHaveBeenCalledWith(
      'Redemption successful! Added: quota:750'
    )
    expect(getSelf).not.toHaveBeenCalled()
  })

  test('shows subscription activation instead of formatting the outcome object as quota', async () => {
    vi.mocked(redeemTopupCode).mockResolvedValue({
      success: true,
      data: {
        outcome_type: 'subscription',
        subscription: {
          id: 42,
          plan_id: 7,
          amount_total: 1200,
          end_time: 1_800_000_000,
        },
      },
    })
    vi.mocked(previewRedemptionCode).mockResolvedValue({
      success: true,
      data: { outcome_type: 'balance', wallet_quota: 750 },
    })
    const { result } = renderHook(() => useRedemption())

    await act(async () => result.current.previewCode('SUBSCRIPTION'))
    await act(async () =>
      expect(await result.current.confirmRedemption('SUBSCRIPTION')).toBe(true)
    )

    expect(toast.success).toHaveBeenCalledWith(
      'Subscription redemption successful'
    )
    expect(formatQuota).not.toHaveBeenCalled()
    expect(getSelf).not.toHaveBeenCalled()
  })

  test('keeps legacy numeric balance redemption responses compatible', async () => {
    vi.mocked(redeemTopupCode).mockResolvedValue({ success: true, data: 250 })
    vi.mocked(previewRedemptionCode).mockResolvedValue({
      success: true,
      data: { outcome_type: 'balance', wallet_quota: 250 },
    })
    const { result } = renderHook(() => useRedemption())

    await act(async () => result.current.previewCode('LEGACY'))
    await act(async () =>
      expect(await result.current.confirmRedemption('LEGACY')).toBe(true)
    )

    expect(formatQuota).toHaveBeenCalledWith(250)
    expect(toast.success).toHaveBeenCalledWith(
      'Redemption successful! Added: quota:250'
    )
  })
})
