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
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'

import { getSelf } from '@/lib/api'
import { formatQuota } from '@/lib/format'
import { toast } from 'sonner'

import { redeemTopupCode } from '../../api'
import { useRedemption } from '../use-redemption'

vi.mock('@/lib/api', () => ({ getSelf: vi.fn() }))
vi.mock('@/lib/format', () => ({ formatQuota: vi.fn((quota: number) => `quota:${quota}`) }))
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
vi.mock('../../api', () => ({ redeemTopupCode: vi.fn() }))

describe('wallet redemption outcomes', () => {
  beforeEach(() => vi.clearAllMocks())

  test('shows the credited wallet quota for a structured balance redemption', async () => {
    vi.mocked(redeemTopupCode).mockResolvedValue({
      success: true,
      data: { outcome_type: 'balance', wallet_quota: 750 },
    })
    const { result } = renderHook(() => useRedemption())

    await act(async () => expect(await result.current.redeemCode('BALANCE')).toBe(true))

    expect(formatQuota).toHaveBeenCalledWith(750)
    expect(toast.success).toHaveBeenCalledWith('Redemption successful! Added: quota:750')
    expect(getSelf).toHaveBeenCalled()
  })

  test('shows subscription activation instead of formatting the outcome object as quota', async () => {
    vi.mocked(redeemTopupCode).mockResolvedValue({
      success: true,
      data: {
        outcome_type: 'subscription',
        subscription: { id: 42, plan_id: 7, amount_total: 1200, end_time: 1_800_000_000 },
      },
    })
    const { result } = renderHook(() => useRedemption())

    await act(async () => expect(await result.current.redeemCode('SUBSCRIPTION')).toBe(true))

    expect(toast.success).toHaveBeenCalledWith('Subscription redemption successful')
    expect(formatQuota).not.toHaveBeenCalled()
    expect(getSelf).toHaveBeenCalled()
  })

  test('keeps legacy numeric balance redemption responses compatible', async () => {
    vi.mocked(redeemTopupCode).mockResolvedValue({ success: true, data: 250 })
    const { result } = renderHook(() => useRedemption())

    await act(async () => expect(await result.current.redeemCode('LEGACY')).toBe(true))

    expect(formatQuota).toHaveBeenCalledWith(250)
    expect(toast.success).toHaveBeenCalledWith('Redemption successful! Added: quota:250')
  })
})
