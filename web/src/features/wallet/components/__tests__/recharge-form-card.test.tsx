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
import { fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'

import { useSystemConfigStore } from '@/stores/system-config-store'

import type { TopupInfo } from '../../types'
import { RechargeFormCard } from '../recharge-form-card'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: { amount: number }) =>
      values ? key.replace('{{amount}}', String(values.amount)) : key,
  }),
}))

const config = useSystemConfigStore.getState().config
const topupInfo: TopupInfo = {
  enable_redemption: true,
  payment_compliance_confirmed: true,
  enable_online_topup: false,
  enable_stripe_topup: false,
  pay_methods: [],
  min_topup: 0,
  stripe_min_topup: 0,
  amount_options: [],
  discount: {},
}

const props = {
  topupInfo,
  redemptionCode: '',
  onRedemptionCodeChange: vi.fn(),
  onRedeem: vi.fn(),
  preview: null,
  onConfirmRedemption: vi.fn(),
  onCancelPreview: vi.fn(),
  redeeming: false,
  confirmingRedemption: false,
  onOpenBilling: vi.fn(),
}

describe('redemption code purchases', () => {
  beforeEach(() => {
    useSystemConfigStore.setState({ config })
    vi.clearAllMocks()
  })

  afterEach(() => {
    useSystemConfigStore.setState({ config })
  })

  it.each(['USD', 'CNY', 'TOKENS'] as const)(
    'keeps fixed CNY prices and product links when the balance display is %s',
    (quotaDisplayType) => {
      useSystemConfigStore.setState({
        config: {
          ...config,
          currency: { ...config.currency, quotaDisplayType },
        },
      })
      render(<RechargeFormCard {...props} />)

      const products = [
        [5, '360gu5'],
        [20, 'g3cv58'],
        [50, 'szhv1m'],
        [100, '1dlqii'],
      ] as const
      for (const [amount, product] of products) {
        const link = screen.getByRole('link', {
          name: `Buy a ${amount} CNY redemption code`,
        })
        expect(link).toHaveAttribute('href', `https://wzyp.cn/item/${product}`)
        expect(link).toHaveAttribute('target', '_blank')
        expect(link).toHaveAttribute('rel', 'noopener noreferrer')
        expect(within(link).getByText('¥')).toBeInTheDocument()
      }
    }
  )

  it.each([
    { ...topupInfo, enable_redemption: false },
    { ...topupInfo, payment_compliance_confirmed: false },
    null,
  ])('does not offer purchase or redemption when unavailable: %j', (info) => {
    render(<RechargeFormCard {...props} topupInfo={info} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Order History' })).toBeEnabled()
  })

  it('submits the current redemption step with Enter and disables blank submissions', () => {
    render(<RechargeFormCard {...props} />)

    const input = screen.getByRole('textbox', { name: 'Redemption code' })
    expect(screen.getByRole('button', { name: 'Preview redemption' })).toBeDisabled()
    fireEvent.change(input, { target: { value: 'CODE' } })
    fireEvent.submit(input.closest('form') as HTMLFormElement)

    expect(props.onRedeem).toHaveBeenCalledOnce()
  })
  it('forwards code changes, redemption and order history actions', () => {
    render(<RechargeFormCard {...props} redemptionCode='CODE' />)
    fireEvent.change(screen.getByRole('textbox', { name: 'Redemption code' }), {
      target: { value: 'NEW-CODE' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Preview redemption' }))
    expect(props.onRedeem).toHaveBeenCalledOnce()
    fireEvent.click(screen.getByRole('button', { name: 'Order History' }))

    expect(props.onOpenBilling).toHaveBeenCalledOnce()
  })

  it('shows the server preview and exposes separate confirm and cancel actions', () => {
    render(
      <RechargeFormCard
        {...{
          ...props,
          redemptionCode: 'TEST-CODE',
          preview: {
            outcome_type: 'subscription',
            subscription: {
              plan_title: 'Monthly Pro',
              duration_unit: 'month',
              duration_value: 1,
              quota: 1200,
              reset_period: 'monthly',
              upgrade_group: 'pro',
            },
          },
        }}
      />
    )

    expect(screen.getByRole('status')).toHaveTextContent('Monthly Pro')
    expect(
      screen.getByRole('button', { name: 'Confirm redemption' })
    ).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm redemption' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(props.onConfirmRedemption).toHaveBeenCalledOnce()
    expect(props.onCancelPreview).toHaveBeenCalledOnce()
  })

  it('removes the old summary when the user edits the code and cannot confirm that preview', () => {
    const summary = {
      outcome_type: 'balance' as const,
      wallet_quota: 750,
    }
    const { rerender } = render(
      <RechargeFormCard
        {...props}
        redemptionCode='CODE-A'
        preview={summary}
        onRedemptionCodeChange={(code) => {
          expect(code).toBe('CODE-B')
          rerender(
            <RechargeFormCard {...props} redemptionCode={code} preview={null} />
          )
        }}
      />
    )

    fireEvent.change(screen.getByRole('textbox', { name: 'Redemption code' }), {
      target: { value: 'CODE-B' },
    })

    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Preview redemption' })
    ).toBeEnabled()
    expect(props.onConfirmRedemption).not.toHaveBeenCalled()
  })

  it('disables code editing while confirmation is in progress', () => {
    render(
      <RechargeFormCard
        {...props}
        redemptionCode='CODE-A'
        confirmingRedemption
      />
    )

    expect(
      screen.getByRole('textbox', { name: 'Redemption code' })
    ).toBeDisabled()
  })

  it('prevents repeated redemption while processing', () => {
    render(<RechargeFormCard {...props} redemptionCode='CODE' redeeming />)
    fireEvent.click(screen.getByRole('button', { name: 'Preview redemption' }))
    expect(props.onRedeem).not.toHaveBeenCalled()
  })
})
