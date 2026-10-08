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
import { ArrowUpRight, Gift, Loader2, Receipt, WalletCards } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'

import type { RedemptionPreview, TopupInfo } from '../types'

/**
 * 兑换码在链动小铺（wzyp.cn）的发售地址。
 *
 * 面额与商品 URL 是一一对应的一对，必须成对修改：只改面额不改 URL，
 * 用户会付对钱拿到错面额；只改 URL 不改面额，卡片上的数字就是假的。
 */
const REDEEM_CODE_SHOP_ITEMS = [
  { amount: 5, url: 'https://wzyp.cn/item/360gu5' },
  { amount: 20, url: 'https://wzyp.cn/item/g3cv58' },
  { amount: 50, url: 'https://wzyp.cn/item/szhv1m' },
  { amount: 100, url: 'https://wzyp.cn/item/1dlqii' },
] as const

interface RechargeFormCardProps {
  topupInfo: TopupInfo | null
  redemptionCode: string
  onRedemptionCodeChange: (code: string) => void
  onRedeem: () => void
  preview: RedemptionPreview | null
  onConfirmRedemption: () => void
  onCancelPreview: () => void
  redeeming: boolean
  confirmingRedemption: boolean
  loading?: boolean
  onOpenBilling?: () => void
}

export function RechargeFormCard({
  topupInfo,
  redemptionCode,
  onRedemptionCodeChange,
  onRedeem,
  preview,
  onConfirmRedemption,
  onCancelPreview,
  redeeming,
  confirmingRedemption,
  loading,
  onOpenBilling,
}: RechargeFormCardProps) {
  const { t } = useTranslation()
  const currencySymbol = '¥'
  const redemptionEnabled =
    !!topupInfo &&
    topupInfo.enable_redemption !== false &&
    topupInfo.payment_compliance_confirmed !== false

  if (loading) {
    return (
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='border-b p-3 !pb-3 sm:p-5 sm:!pb-5'>
          <Skeleton className='h-6 w-32' />
          <Skeleton className='mt-2 h-4 w-48' />
        </CardHeader>
        <CardContent className='space-y-4 p-3 sm:space-y-6 sm:p-5'>
          <div className='space-y-3'>
            <Skeleton className='h-3 w-16' />
            <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
              {REDEEM_CODE_SHOP_ITEMS.map((item) => (
                <Skeleton key={item.amount} className='h-[72px] rounded-lg' />
              ))}
            </div>
          </div>
          <div className='space-y-3 border-t pt-4 sm:pt-6'>
            <Skeleton className='h-9 w-full' />
          </div>
        </CardContent>
      </Card>
    )
  }

  return (
    <TitledCard
      title={t('Add Funds')}
      description={t('Pick an amount, buy a code, then redeem it here')}
      icon={<WalletCards className='h-4 w-4' />}
      iconTone='success'
      disableHoverEffect
      action={
        onOpenBilling ? (
          <Button
            variant='outline'
            size='sm'
            onClick={onOpenBilling}
            className='w-full gap-2 sm:w-auto'
          >
            <Receipt className='h-4 w-4' />
            {t('Order History')}
          </Button>
        ) : null
      }
      contentClassName='space-y-4 sm:space-y-6'
    >
      {redemptionEnabled ? (
        <>
          <div className='space-y-2.5 sm:space-y-3'>
            <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
              {t('Amount')}
            </Label>
            <div className='grid grid-cols-2 gap-1.5 sm:gap-3 md:grid-cols-4'>
              {REDEEM_CODE_SHOP_ITEMS.map((item) => (
                <a
                  key={item.amount}
                  href={item.url}
                  target='_blank'
                  rel='noopener noreferrer'
                  aria-label={t('Buy a {{amount}} CNY redemption code', {
                    amount: item.amount,
                  })}
                  className='group border-border hover:border-foreground/25 hover:bg-accent focus-visible:border-foreground focus-visible:ring-ring flex min-h-14 items-center rounded-lg border px-3 py-2 transition-colors outline-none focus-visible:ring-2 sm:min-h-16'
                >
                  {/* 三列栅格：左右各留一格占位，数字才是真的居中，
                      而 ↗ 落在行尾垂直居中处——横向行里眼睛就在那儿找"能进去"的信号 */}
                  <span className='grid w-full grid-cols-[1rem_1fr_1rem] items-center'>
                    <span aria-hidden='true' />
                    <span className='flex items-baseline justify-center gap-0.5'>
                      <span className='text-muted-foreground text-sm font-medium'>
                        {currencySymbol}
                      </span>
                      <span className='text-xl font-semibold tabular-nums sm:text-2xl'>
                        {item.amount}
                      </span>
                    </span>
                    <ArrowUpRight className='text-muted-foreground size-3.5 justify-self-end opacity-70 transition-opacity group-hover:opacity-100' />
                  </span>
                </a>
              ))}
            </div>
          </div>

          <div className='border-t pt-4 sm:pt-5'>
            <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-2'>
              <div className='relative'>
                {/* 纯装饰的输入框前缀，用静音色：它当区块标记时的琥珀色底是提醒用的，
                    挂到输入框上会变成没有缘由的高饱和色 */}
                <Gift className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2' />
                <Input
                  id='redemption-code'
                  value={redemptionCode}
                  onChange={(e) => onRedemptionCodeChange(e.target.value)}
                  placeholder={t('Paste your redemption code')}
                  aria-label={t('Redemption code')}
                  disabled={confirmingRedemption}
                  className='h-9 min-w-0 pl-9'
                />
              </div>
              <Button
                onClick={preview ? onConfirmRedemption : onRedeem}
                disabled={redeeming || (!!preview && !redemptionCode)}
                variant='outline'
                className='h-9 px-4'
              >
                {redeeming && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
                {preview ? t('Confirm redemption') : t('Preview redemption')}
              </Button>
            </div>
            {preview && (
              <div
                role='status'
                className='mt-3 space-y-2 rounded-md border p-3 text-sm'
              >
                {preview.outcome_type === 'balance' ? (
                  <p>
                    {t('Wallet balance will increase by {{quota}}', {
                      quota: preview.wallet_quota ?? 0,
                    })}
                  </p>
                ) : (
                  <>
                    <p>{preview.subscription?.plan_title}</p>
                    <p>
                      {t('Validity')}: {preview.subscription?.duration_value}{' '}
                      {preview.subscription?.duration_unit}
                    </p>
                    <p>
                      {t('Quota')}: {preview.subscription?.quota}
                    </p>
                    <p>
                      {t('Reset period')}: {preview.subscription?.reset_period}
                    </p>
                    {(preview.subscription?.upgrade_group ||
                      preview.subscription?.downgrade_group) && (
                      <p>
                        {t('Group effect')}:{' '}
                        {preview.subscription.upgrade_group || t('No change')} →{' '}
                        {preview.subscription.downgrade_group ||
                          t('Restore previous group')}
                      </p>
                    )}
                  </>
                )}
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  onClick={onCancelPreview}
                  disabled={redeeming}
                >
                  {t('Cancel')}
                </Button>
              </div>
            )}
          </div>
        </>
      ) : (
        <Alert className='border-t'>
          <AlertDescription>
            {topupInfo
              ? t(
                  'Redemption codes are disabled until the administrator confirms compliance terms.'
                )
              : t('Loading failed')}
          </AlertDescription>
        </Alert>
      )}
    </TitledCard>
  )
}
