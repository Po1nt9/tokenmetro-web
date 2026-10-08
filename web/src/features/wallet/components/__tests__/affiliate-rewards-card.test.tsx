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
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { getAffiliateRewards } from '../../api'
import type { AffiliateRewardOverview, UserWalletData } from '../../types'
import { AffiliateRewardsCard } from '../affiliate-rewards-card'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

// The formatters are wrapped so the assertions can tell a formatted amount from
// the raw quota number the API returned.
vi.mock('@/lib/format', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/format')>()
  return {
    ...actual,
    formatQuota: (value: number) => `FQ(${value})`,
    formatTimestamp: (timestamp: number) => `TS(${timestamp})`,
  }
})

vi.mock('@/lib/handle-server-error', () => ({
  handleServerError: vi.fn(),
}))

vi.mock('@/components/copy-button', () => ({
  CopyButton: (props: { 'aria-label'?: string }) => (
    <button aria-label={props['aria-label']} type='button' />
  ),
}))

vi.mock('../../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api')>()
  return { ...actual, getAffiliateRewards: vi.fn() }
})

const user: UserWalletData = {
  id: 1,
  username: 'user',
  quota: 100,
  used_quota: 20,
  request_count: 3,
  aff_quota: 10,
  aff_history_quota: 25,
  aff_count: 2,
  group: 'default',
}

const overview: AffiliateRewardOverview = {
  pending_quota: 1000,
  rewards: [
    {
      id: 3,
      created_time: 1_700_000_200,
      invitee_username: 'bob',
      basis_quota: 500_000,
      reward_quota: 25_000,
      status: 'pending',
    },
    {
      id: 2,
      created_time: 1_700_000_100,
      invitee_username: 'carol',
      basis_quota: 1_000_000,
      reward_quota: 50_000,
      status: 'credited',
    },
    {
      id: 1,
      created_time: 1_700_000_000,
      invitee_username: '',
      basis_quota: 200_000,
      reward_quota: 10_000,
      status: 'voided',
    },
  ],
}

function renderCard(overrides?: {
  user?: UserWalletData
  onTransfer?: (quota: number) => Promise<boolean>
}) {
  return render(
    <AffiliateRewardsCard
      user={overrides?.user ?? user}
      affiliateLink='https://tokenmetro.com/sign-up?aff=abc'
      onTransfer={overrides?.onTransfer ?? vi.fn().mockResolvedValue(true)}
    />
  )
}

const expandDetails = () => {
  fireEvent.click(screen.getByRole('button', { name: 'Reward Details' }))
}

describe('affiliate rewards card', () => {
  beforeEach(() => {
    vi.mocked(getAffiliateRewards).mockResolvedValue({
      success: true,
      message: '',
      data: overview,
    })
  })

  it('shows the four affiliate figures including the pending settlement total', async () => {
    renderCard()

    expect(screen.getByRole('textbox', { name: 'Referral link' })).toHaveValue(
      'https://tokenmetro.com/sign-up?aff=abc'
    )
    expect(screen.getAllByRole('term').map((term) => term.textContent)).toEqual(
      ['Available to Transfer', 'Pending Settlement', 'Total Earned', 'Invites']
    )
    // The pending total arrives with the ledger request, so it starts unknown
    // instead of claiming zero.
    expect(
      screen
        .getAllByRole('definition')
        .map((definition) => definition.textContent)
    ).toEqual(['FQ(10)', '—', 'FQ(25)', '2'])
    await waitFor(() =>
      expect(
        screen
          .getAllByRole('definition')
          .map((definition) => definition.textContent)
      ).toEqual(['FQ(10)', 'FQ(1000)', 'FQ(25)', '2'])
    )
    expect(getAffiliateRewards).toHaveBeenCalledTimes(1)
  })

  it('offers the transfer to balance entry and drops the read-only notice', async () => {
    const onTransfer = vi.fn().mockResolvedValue(true)
    renderCard({ onTransfer })

    const transfer = screen.getByRole('button', { name: 'Transfer to Balance' })
    expect(transfer).toBeEnabled()
    expect(
      screen.queryByText(
        'Automatic referral rewards are not enabled yet; pending rewards remain read-only.'
      )
    ).not.toBeInTheDocument()

    fireEvent.click(transfer)
    expect(await screen.findByText('Transfer Rewards')).toBeInTheDocument()
    expect(screen.getByText('Available Rewards')).toBeInTheDocument()
  })

  it('disables the transfer entry when no reward is transferable', () => {
    renderCard({ user: { ...user, aff_quota: 0 } })

    expect(
      screen.getByRole('button', { name: 'Transfer to Balance' })
    ).toBeDisabled()
  })

  it('lists reward rows with invitee username, amounts and status after expanding', async () => {
    renderCard()
    expandDetails()

    const rows = within(await screen.findByRole('list')).getAllByRole(
      'listitem'
    )
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('TS(1700000200)')
    expect(rows[0]).toHaveTextContent('bob')
    expect(rows[0]).toHaveTextContent('FQ(500000)')
    expect(rows[0]).toHaveTextContent('FQ(25000)')
    expect(within(rows[0]).getByText('Pending Settlement')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Credited')).toBeInTheDocument()
    expect(within(rows[2]).getByText('Voided')).toBeInTheDocument()
    // A vanished invitee account renders as deleted instead of a blank cell.
    expect(rows[2]).toHaveTextContent('Deleted')
    // The invitee's email is never part of the ledger.
    expect(
      within(screen.getByRole('list')).queryByText(/@/)
    ).not.toBeInTheDocument()
  })

  it('shows the empty state when no reward has been earned yet', async () => {
    vi.mocked(getAffiliateRewards).mockResolvedValue({
      success: true,
      message: '',
      data: { pending_quota: 0, rewards: [] },
    })
    renderCard()
    expandDetails()

    expect(
      await screen.findByText('No invitation rewards yet')
    ).toBeInTheDocument()
    expect(screen.queryByRole('list')).not.toBeInTheDocument()
  })

  it('shows an error state and retries the ledger request', async () => {
    vi.mocked(getAffiliateRewards)
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce({ success: true, message: '', data: overview })
    renderCard()
    expandDetails()

    expect(
      await screen.findByText('Failed to load invitation rewards')
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      within(await screen.findByRole('list')).getAllByRole('listitem')
    ).toHaveLength(3)
  })
})
