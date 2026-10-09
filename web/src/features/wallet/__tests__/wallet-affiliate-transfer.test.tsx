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
*/
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { getSelf } from '@/lib/api'

import {
  getAffiliateCode,
  getAffiliateRewards,
  getTopupInfo,
  getUserBillingHistory,
  transferAffiliateQuota,
} from '../api'
import { Wallet } from '../index'
import type { UserWalletData } from '../types'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

// Wrapped so the assertions can tell a formatted amount from the raw quota.
vi.mock('@/lib/format', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/format')>()
  return { ...actual, formatQuota: (value: number) => `FQ(${value})` }
})

vi.mock('@/lib/handle-server-error', () => ({
  handleServerError: vi.fn(),
}))

vi.mock('@/components/copy-button', () => ({
  CopyButton: (props: { 'aria-label'?: string }) => (
    <button aria-label={props['aria-label']} type='button' />
  ),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, getSelf: vi.fn() }
})

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    getTopupInfo: vi.fn(),
    getAffiliateCode: vi.fn(),
    getAffiliateRewards: vi.fn(),
    transferAffiliateQuota: vi.fn(),
    getUserBillingHistory: vi.fn(),
  }
})

// Only the affiliate card and the wallet page wiring are under test here; the
// sibling cards own their own data fetching and their own tests.
vi.mock('../components/wallet-stats-card', () => ({
  WalletStatsCard: () => <div data-testid='wallet-stats' />,
}))
vi.mock('../components/recharge-form-card', () => ({
  RechargeFormCard: () => <div />,
}))
vi.mock('../components/subscription-plans-card', () => ({
  SubscriptionPlansCard: () => <div />,
}))
vi.mock('../components/dialogs/billing-history-dialog', () => ({
  BillingHistoryDialog: () => null,
}))

function walletUser(overrides: Partial<UserWalletData>): UserWalletData {
  return {
    id: 1,
    username: 'user',
    quota: 100,
    used_quota: 20,
    request_count: 3,
    aff_quota: 1_000_000,
    aff_history_quota: 1_500_000,
    aff_count: 2,
    group: 'default',
    ...overrides,
  }
}

const rewardFigures = () =>
  screen.getAllByRole('definition').map((definition) => definition.textContent)

describe('wallet affiliate transfer', () => {
  beforeEach(() => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      message: '',
      data: {
        enable_online_topup: false,
        enable_stripe_topup: false,
        pay_methods: [],
        min_topup: 0,
        stripe_min_topup: 0,
        amount_options: [],
        discount: {},
      },
    })
    vi.mocked(getAffiliateCode).mockResolvedValue({
      success: true,
      message: '',
      data: 'abc',
    })
    vi.mocked(getAffiliateRewards).mockResolvedValue({
      success: true,
      message: '',
      data: { pending_quota: 0, rewards: [] },
    })
    vi.mocked(getUserBillingHistory).mockResolvedValue({
      success: true,
      message: '',
      data: { items: [], total: 0 },
    })
  })

  it('moves rewards to the balance and refreshes both figures', async () => {
    let user = walletUser({ quota: 100, aff_quota: 1_000_000 })
    vi.mocked(getSelf).mockImplementation(async () => ({
      success: true,
      message: '',
      data: user,
    }))
    vi.mocked(transferAffiliateQuota).mockImplementation(async ({ quota }) => {
      // The backend moves the transferred quota out of the reward pool and into
      // the wallet balance, so the next self read must show both sides moved.
      user = walletUser({
        quota: user.quota + quota,
        aff_quota: user.aff_quota - quota,
      })
      return { success: true, message: '' }
    })

    render(<Wallet />)

    await waitFor(() => expect(rewardFigures()[0]).toBe('FQ(1000000)'))
    fireEvent.click(screen.getByRole('button', { name: 'Transfer to Balance' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Transfer' }))

    await waitFor(() =>
      expect(transferAffiliateQuota).toHaveBeenCalledWith({ quota: 500000 })
    )
    // The available figure can only drop to the post-transfer value if the
    // page re-read the user after the transfer succeeded.
    await waitFor(() => expect(rewardFigures()[0]).toBe('FQ(500000)'))
    await waitFor(() =>
      expect(screen.queryByText('Transfer Rewards')).not.toBeInTheDocument()
    )
  })
})
