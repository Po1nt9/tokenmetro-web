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
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import type { UserWalletData } from '../../types'
import { AffiliateRewardsCard } from '../affiliate-rewards-card'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

vi.mock('@/lib/format', () => ({
  formatQuota: (value: number) => String(value),
}))

vi.mock('@/components/copy-button', () => ({
  CopyButton: (props: { 'aria-label'?: string }) => (
    <button aria-label={props['aria-label']} type='button' />
  ),
}))

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

describe('affiliate rewards card', () => {
  it('keeps referral history read-only and explains that automatic rewards are disabled', () => {
    render(
      <AffiliateRewardsCard
        user={user}
        affiliateLink='https://tokenmetro.com/register?aff=abc'
      />
    )

    expect(screen.getByRole('textbox', { name: 'Referral link' })).toHaveValue(
      'https://tokenmetro.com/register?aff=abc'
    )
    expect(
      screen.getByText(
        'Automatic referral rewards are not enabled yet; pending rewards remain read-only.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Transfer to Balance' })).not.toBeInTheDocument()
  })
})
