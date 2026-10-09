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
import { ChevronDown, Share2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { useAffiliateRewards } from '../hooks/use-affiliate-rewards'
import type { UserWalletData } from '../types'
import { AffiliateRewardsDetails } from './affiliate-rewards-details'
import { TransferDialog } from './dialogs/transfer-dialog'

interface AffiliateRewardsCardProps {
  user: UserWalletData | null
  affiliateLink: string
  loading?: boolean
  transferring?: boolean
  onTransfer: (quota: number) => Promise<boolean>
}

export function AffiliateRewardsCard(props: AffiliateRewardsCardProps) {
  const { t } = useTranslation()
  const { rewards, pendingQuota, loading, failed, refresh } =
    useAffiliateRewards()
  const [transferOpen, setTransferOpen] = useState(false)
  const [detailsOpen, setDetailsOpen] = useState(false)

  if (props.loading) {
    return (
      <Card data-card-hover='false' className='bg-muted/20 py-0'>
        <CardContent className='space-y-4 p-3 sm:p-4'>
          <div className='grid gap-3 lg:grid-cols-[minmax(200px,1fr)_minmax(260px,1fr)] lg:items-center'>
            <div>
              <Skeleton className='h-5 w-32' />
              <Skeleton className='mt-2 h-4 w-48' />
            </div>
            <Skeleton className='h-10 rounded-lg' />
          </div>
          <Skeleton className='h-14 rounded-lg' />
        </CardContent>
      </Card>
    )
  }

  const availableQuota = props.user?.aff_quota ?? 0
  // While the ledger request is in flight or failed, its total is unknown:
  // showing a zero would claim the user has nothing pending.
  const pendingSettlement = loading || failed ? '—' : formatQuota(pendingQuota)

  return (
    <>
      <Card data-card-hover='false' className='bg-muted/20 py-0'>
        <CardContent className='space-y-3 p-3 sm:space-y-4 sm:p-4'>
          <div className='grid gap-3 lg:grid-cols-[minmax(200px,1fr)_minmax(260px,1fr)] lg:items-center'>
            <div className='flex min-w-0 items-center gap-2.5'>
              <IconBadge tone='chart-3'>
                <Share2 />
              </IconBadge>
              <div className='min-w-0'>
                <h3 className='truncate text-sm font-semibold'>
                  {t('Referral Program')}
                </h3>
                <p className='text-muted-foreground line-clamp-2 text-xs'>
                  {t('Share your referral link and track invite history here.')}
                </p>
              </div>
            </div>

            <div className='flex min-w-0 items-center gap-2'>
              <Input
                value={props.affiliateLink}
                readOnly
                aria-label={t('Referral link')}
                className='border-muted bg-background/70 h-9 min-w-0 flex-1 font-mono text-xs'
              />
              <CopyButton
                value={props.affiliateLink}
                variant='outline'
                className='bg-background size-9 shrink-0'
                iconClassName='size-4'
                tooltip={t('Copy referral link')}
                aria-label={t('Copy referral link')}
              />
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='bg-background h-9 shrink-0'
                disabled={availableQuota <= 0}
                onClick={() => setTransferOpen(true)}
              >
                {t('Transfer to Balance')}
              </Button>
            </div>
          </div>

          <dl className='grid grid-cols-2 gap-2 border-t pt-3 text-center sm:grid-cols-4'>
            {[
              [t('Available to Transfer'), formatQuota(availableQuota)],
              [t('Pending Settlement'), pendingSettlement],
              [
                t('Total Earned'),
                formatQuota(props.user?.aff_history_quota ?? 0),
              ],
              [t('Invites'), String(props.user?.aff_count ?? 0)],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className='text-muted-foreground truncate text-[10px] font-medium tracking-wider uppercase'>
                  {label}
                </dt>
                <dd className='mt-0.5 truncate text-sm font-semibold tabular-nums'>
                  {value}
                </dd>
              </div>
            ))}
          </dl>

          <p className='text-muted-foreground text-xs'>
            {t(
              'Invitation rewards settle automatically seven days after an invitee redeems a sold code; transfer them to your balance anytime.'
            )}
          </p>

          <Collapsible
            open={detailsOpen}
            onOpenChange={setDetailsOpen}
            className='border-t pt-3'
          >
            <CollapsibleTrigger
              render={
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  className='-ml-2 h-7 gap-1.5 px-2 text-xs'
                />
              }
            >
              <ChevronDown
                className={cn(
                  'size-3.5 transition-transform',
                  detailsOpen && 'rotate-180'
                )}
                aria-hidden='true'
              />
              {t('Reward Details')}
            </CollapsibleTrigger>
            <CollapsibleContent className='mt-2'>
              <AffiliateRewardsDetails
                rewards={rewards}
                loading={loading}
                failed={failed}
                onRetry={refresh}
              />
            </CollapsibleContent>
          </Collapsible>
        </CardContent>
      </Card>

      <TransferDialog
        open={transferOpen}
        onOpenChange={setTransferOpen}
        onConfirm={props.onTransfer}
        availableQuota={availableQuota}
        transferring={props.transferring ?? false}
      />
    </>
  )
}
