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
import { Crown, RefreshCw } from 'lucide-react'
import { useState, useEffect, useMemo, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  StatusBadge,
  textColorMap,
} from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import {
  getPublicPlans,
  getSelfSubscriptionFull,
  updateBillingPreference,
} from '@/features/subscriptions/api'
import type {
  PlanRecord,
  UserSubscriptionRecord,
} from '@/features/subscriptions/types'
import { formatQuota } from '@/lib/format'
import { toIntlLocale } from '@/i18n/languages'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

interface SubscriptionPlansCardProps {
  topupInfo?: never
  onAvailabilityChange?: (available: boolean) => void
  userQuota?: never
  onPurchaseSuccess?: never
}

function getBillingPreferenceLabel(
  preference: string,
  t: (key: string) => string
): string {
  switch (preference) {
    case 'subscription_first':
      return t('Subscription First')
    case 'wallet_first':
      return t('Wallet First')
    case 'subscription_only':
      return t('Subscription Only')
    case 'wallet_only':
      return t('Wallet Only')
    default:
      return preference
  }
}

export function SubscriptionPlansCard(props: SubscriptionPlansCardProps) {
  const { t, i18n } = useTranslation()
  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [subscriptions, setSubscriptions] = useState<UserSubscriptionRecord[]>([])
  const [billingPreference, setBillingPreference] = useState('subscription_first')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  const fetchPlans = useCallback(async () => {
    try {
      const response = requireServerSuccess(await getPublicPlans())
      if (response.success) setPlans(response.data || [])
    } catch (error) {
      handleServerError(error)
      setPlans([])
    }
  }, [])

  const fetchSubscriptions = useCallback(async () => {
    try {
      const response = requireServerSuccess(await getSelfSubscriptionFull())
      if (response.success && response.data) {
        setBillingPreference(
          response.data.billing_preference || 'subscription_first'
        )
        setSubscriptions(response.data.all_subscriptions || [])
      }
    } catch (error) {
      handleServerError(error)
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    const init = async () => {
      setLoading(true)
      await Promise.all([fetchPlans(), fetchSubscriptions()])
      if (!cancelled) setLoading(false)
    }
    void init()
    return () => {
      cancelled = true
    }
  }, [fetchPlans, fetchSubscriptions])

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      await fetchSubscriptions()
    } finally {
      setRefreshing(false)
    }
  }

  const handlePreferenceChange = async (preference: string) => {
    const previous = billingPreference
    setBillingPreference(preference)
    try {
      const response = await updateBillingPreference(preference)
      if (response.success) {
        toast.success(t('Updated successfully'))
        setBillingPreference(response.data?.billing_preference || preference)
      } else {
        handleServerError(response, t('Update failed'))
        setBillingPreference(previous)
      }
    } catch (error) {
      handleServerError(error, t('Request failed'))
      setBillingPreference(previous)
    }
  }

  const planTitleMap = useMemo(() => {
    const map = new Map<number, string>()
    for (const plan of plans) {
      if (plan.plan?.id) map.set(plan.plan.id, plan.plan.title || '')
    }
    return map
  }, [plans])

  const activeSubscriptions = subscriptions.filter((item) => {
    const subscription = item.subscription
    return (
      subscription?.status === 'active' &&
      (subscription.end_time || 0) >= Date.now() / 1000
    )
  })
  const hasActive = activeSubscriptions.length > 0
  const hasSubscriptions = subscriptions.length > 0
  const isAvailable = loading || hasSubscriptions
  const subscriptionPreference =
    billingPreference === 'subscription_first' ||
    billingPreference === 'subscription_only'
  const onAvailabilityChange = props.onAvailabilityChange

  useEffect(() => {
    onAvailabilityChange?.(isAvailable)
  }, [isAvailable, onAvailabilityChange])

  if (loading) {
    return (
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='border-b p-3 !pb-3 sm:p-5 sm:!pb-5'>
          <Skeleton className='h-6 w-40' />
          <Skeleton className='mt-2 h-4 w-64' />
        </CardHeader>
        <CardContent className='space-y-4 p-3 sm:p-5'>
          <Skeleton className='h-24 w-full' />
          <Skeleton className='h-20 w-full' />
        </CardContent>
      </Card>
    )
  }

  if (!hasSubscriptions) return null

  return (
    <TitledCard
      title={t('Subscription Entitlements')}
      description={t('Your new-api subscription access is separate from wallet balance')}
      icon={<Crown className='h-4 w-4' />}
      iconTone='warning'
      disableHoverEffect
      contentClassName='space-y-4 sm:space-y-5'
    >
      <div className='rounded-xl border p-3 sm:p-4'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div className='min-w-0'>
            <p className='text-sm font-medium'>{t('Current subscription access')}</p>
            <p className='text-muted-foreground mt-1 text-xs'>
              {hasActive
                ? t('{{count}} active', { count: activeSubscriptions.length })
                : t('No Active')}
              {subscriptions.length > activeSubscriptions.length &&
                ` · ${subscriptions.length - activeSubscriptions.length} ${t('expired')}`}
            </p>
          </div>
          <div className='flex w-full items-center gap-2 sm:w-auto'>
            <Select
              items={[
                {
                  value: 'subscription_first',
                  label: `${getBillingPreferenceLabel('subscription_first', t)}${!hasActive ? ` (${t('No Active')})` : ''}`,
                },
                {
                  value: 'wallet_first',
                  label: getBillingPreferenceLabel('wallet_first', t),
                },
                {
                  value: 'subscription_only',
                  label: `${getBillingPreferenceLabel('subscription_only', t)}${!hasActive ? ` (${t('No Active')})` : ''}`,
                },
                {
                  value: 'wallet_only',
                  label: getBillingPreferenceLabel('wallet_only', t),
                },
              ]}
              value={billingPreference}
              onValueChange={(value) => value && void handlePreferenceChange(value)}
            >
              <SelectTrigger
                aria-label={t('Billing preference')}
                className='h-8 min-w-0 flex-1 text-xs sm:w-[160px] sm:flex-none'
              >
                <SelectValue>
                  {getBillingPreferenceLabel(billingPreference, t)}
                </SelectValue>
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='subscription_first' disabled={!hasActive}>
                    {getBillingPreferenceLabel('subscription_first', t)}
                  </SelectItem>
                  <SelectItem value='wallet_first'>
                    {getBillingPreferenceLabel('wallet_first', t)}
                  </SelectItem>
                  <SelectItem value='subscription_only' disabled={!hasActive}>
                    {getBillingPreferenceLabel('subscription_only', t)}
                  </SelectItem>
                  <SelectItem value='wallet_only'>
                    {getBillingPreferenceLabel('wallet_only', t)}
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 shrink-0'
              onClick={() => void handleRefresh()}
              disabled={refreshing}
              aria-label={t('Refresh subscriptions')}
            >
              <RefreshCw className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`} />
            </Button>
          </div>
        </div>
        {!hasActive && subscriptionPreference && (
          <p className='text-muted-foreground mt-2 text-xs'>
            {t('Preference saved, but no active subscription is available.')}
          </p>
        )}
        <Separator className='my-3' />
        <div className='max-h-80 space-y-3 overflow-y-auto pr-1'>
          {subscriptions.map((item) => {
            const subscription = item.subscription
            const total = Number(subscription?.amount_total || 0)
            const used = Number(subscription?.amount_used || 0)
            const remaining = total > 0 ? Math.max(0, total - used) : 0
            const usage = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0
            const endTime = subscription?.end_time || 0
            const active =
              subscription?.status === 'active' && endTime >= Date.now() / 1000
            const cancelled = subscription?.status === 'cancelled'
            const title =
              planTitleMap.get(subscription?.plan_id || 0) || t('Subscription')
            let status = 'Expired'
            if (active) {
              status = 'Active'
            } else if (cancelled) {
              status = 'Cancelled'
            }
            const days = active
              ? Math.max(0, Math.ceil((endTime - Date.now() / 1000) / 86400))
              : 0

            return (
              <div key={subscription?.id} className='bg-background rounded-md border p-3 text-xs'>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <div className='flex min-w-0 items-center gap-2'>
                    <span className='truncate font-medium'>
                      {title} · {t('Subscription')} #{subscription?.id}
                    </span>
                    <StatusBadge
                      label={t(status)}
                      variant={active ? 'success' : 'neutral'}
                      copyable={false}
                    />
                  </div>
                  {active && (
                    <span className={textColorMap.success}>
                      {t('{{count}} days remaining', { count: days })}
                    </span>
                  )}
                </div>
                <div className='text-muted-foreground mt-1.5'>
                  {(() => {
                    if (active) return t('Until')
                    if (cancelled) return t('Cancelled at')
                    return t('Expired at')
                  })()}{' '}
                  {new Date(endTime * 1000).toLocaleString(locale)}
                </div>
                <div className='text-muted-foreground mt-1'>
                  {t('Total Quota')}:{' '}
                  {total > 0
                    ? `${formatQuota(used)}/${formatQuota(total)} · ${t('Remaining')} ${formatQuota(remaining)}`
                    : t('Unlimited')}
                </div>
                {total > 0 && active && <Progress value={usage} className='mt-2 h-1.5' />}
                {subscription?.next_reset_time ? (
                  <div className='text-muted-foreground mt-1'>
                    {t('Next reset')}:{' '}
                    {new Date(subscription.next_reset_time * 1000).toLocaleString(locale)}
                  </div>
                ) : null}
              </div>
            )
          })}
        </div>
      </div>

      <div className='rounded-lg border border-dashed p-3 text-xs sm:p-4'>
        <p className='font-medium'>{t('Subscription plans')}</p>
        <p className='text-muted-foreground mt-1'>
          {plans.length > 0
            ? t('Buy subscription entitlements from the ChainDong Shop and redeem the code above.')
            : t('No subscription plans are currently published.')}
        </p>
        {plans.length > 0 && (
            <a
              href='https://wzyp.cn/'
              target='_blank'
              rel='noopener noreferrer'
              className='text-primary mt-2 inline-flex min-h-10 items-center text-sm underline-offset-4 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
            >
              {t('Browse subscription codes at the ChainDong Shop')}
            </a>
        )}
        <p className='text-muted-foreground mt-2'>
          {t('Subscription entitlements are never merged into wallet balance.')}
        </p>
      </div>
    </TitledCard>
  )
}
