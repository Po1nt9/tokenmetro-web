import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  getPublicPlans,
  getSelfSubscriptionFull,
} from '@/features/subscriptions/api'

import { SubscriptionPlansCard } from '../subscription-plans-card'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }),
}))

vi.mock('@/features/subscriptions/api', () => ({
  getPublicPlans: vi.fn(),
  getSelfSubscriptionFull: vi.fn(),
  updateBillingPreference: vi.fn(),
}))

vi.mock('@/lib/format', () => ({
  formatQuota: (value: number) => String(value),
}))
vi.mock('@/i18n/languages', () => ({ toIntlLocale: () => 'en-US' }))
vi.mock('@/lib/handle-server-error', () => ({ handleServerError: vi.fn() }))
vi.mock('@/lib/server-error-message', () => ({
  requireServerSuccess: (response: unknown) => response,
}))
vi.mock('@/components/status-badge', () => ({
  StatusBadge: () => <span />,
  textColorMap: { success: '' },
}))
vi.mock('@/components/ui/button', () => ({
  Button: (props: React.ButtonHTMLAttributes<HTMLButtonElement>) => (
    <button type='button' {...props} />
  ),
}))
vi.mock('@/components/ui/card', () => ({
  Card: (props: React.HTMLAttributes<HTMLDivElement>) => <div {...props} />,
  CardContent: (props: React.HTMLAttributes<HTMLDivElement>) => (
    <div {...props} />
  ),
  CardHeader: (props: React.HTMLAttributes<HTMLDivElement>) => (
    <div {...props} />
  ),
}))
vi.mock('@/components/ui/progress', () => ({ Progress: () => <div /> }))
vi.mock('@/components/ui/select', () => ({
  Select: (props: { children: React.ReactNode }) => <div>{props.children}</div>,
  SelectContent: (props: { children: React.ReactNode }) => (
    <div>{props.children}</div>
  ),
  SelectGroup: (props: { children: React.ReactNode }) => (
    <div>{props.children}</div>
  ),
  SelectItem: (props: { children: React.ReactNode }) => (
    <div>{props.children}</div>
  ),
  SelectTrigger: (props: {
    children: React.ReactNode
    'aria-label'?: string
  }) => (
    <button type='button' aria-label={props['aria-label']}>
      {props.children}
    </button>
  ),
  SelectValue: (props: { children: React.ReactNode }) => (
    <span>{props.children}</span>
  ),
}))
vi.mock('@/components/ui/separator', () => ({ Separator: () => <hr /> }))
vi.mock('@/components/ui/skeleton', () => ({
  Skeleton: () => <div role='status' />,
}))
vi.mock('@/components/ui/titled-card', () => ({
  TitledCard: (props: { title: string; children: React.ReactNode }) => (
    <section aria-label={props.title}>{props.children}</section>
  ),
}))

function settleInitialRequests() {
  vi.mocked(getPublicPlans).mockResolvedValue({ success: true, data: [] })
  vi.mocked(getSelfSubscriptionFull).mockResolvedValue({
    success: true,
    data: {
      subscriptions: [],
      all_subscriptions: [],
      billing_preference: 'subscription_first',
    },
  })
}

describe('subscription plans card', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    settleInitialRequests()
  })

  it('shows the verified ChainDong shop entry when the user has no subscriptions', async () => {
    render(<SubscriptionPlansCard />)

    expect(
      await screen.findByRole('link', {
        name: 'Browse subscription codes at the ChainDong Shop',
      })
    ).toHaveAttribute('href', 'https://wzyp.cn/')
    expect(
      screen.getByText(
        'No active subscriptions yet. Buy a subscription code from the ChainDong Shop and redeem it above.'
      )
    ).toBeInTheDocument()
  })

  it('refetches subscription entitlements when refreshVersion changes', async () => {
    const { rerender } = render(<SubscriptionPlansCard refreshVersion={0} />)
    await screen.findByRole('link', {
      name: 'Browse subscription codes at the ChainDong Shop',
    })
    vi.mocked(getSelfSubscriptionFull).mockClear()

    rerender(<SubscriptionPlansCard refreshVersion={1} />)

    expect(getSelfSubscriptionFull).toHaveBeenCalledTimes(1)
  })
})
