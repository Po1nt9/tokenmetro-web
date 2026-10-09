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
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { formatQuota, formatTimestamp } from '@/lib/format'

import { getAffiliateRewardStatusConfig } from '../lib/affiliate'
import type { AffiliateRewardEntry } from '../types'

interface AffiliateRewardsDetailsProps {
  rewards: AffiliateRewardEntry[]
  loading: boolean
  failed: boolean
  onRetry: () => void
}

const REWARD_ROW_GRID =
  'gap-1 sm:grid sm:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)_minmax(0,0.9fr)_minmax(0,0.9fr)_auto] sm:items-center sm:gap-2'

/**
 * Read-only ledger of the inviter's own invitation rewards. Amounts are shown
 * in the configured display currency; the invitee is identified by username
 * only.
 */
export function AffiliateRewardsDetails(props: AffiliateRewardsDetailsProps) {
  const { t } = useTranslation()

  if (props.loading) {
    return (
      <LoadingState
        inline
        size='sm'
        message={t('Loading...')}
        className='text-muted-foreground py-2'
      />
    )
  }
  if (props.failed) {
    return (
      <ErrorState
        title={t('Failed to load invitation rewards')}
        className='min-h-0 py-4'
        onRetry={props.onRetry}
      />
    )
  }
  if (props.rewards.length === 0) {
    return (
      <EmptyState
        title={t('No invitation rewards yet')}
        className='min-h-0 py-4'
      />
    )
  }

  return (
    <div className='space-y-1'>
      <div
        className={`text-muted-foreground hidden px-2 text-[10px] font-medium tracking-wider uppercase sm:grid ${REWARD_ROW_GRID}`}
      >
        <span>{t('Time')}</span>
        <span>{t('Invitee')}</span>
        <span>{t('Reward Basis')}</span>
        <span>{t('Reward Amount')}</span>
        <span>{t('Status')}</span>
      </div>
      <ul className='divide-y'>
        {props.rewards.map((reward) => {
          const status = getAffiliateRewardStatusConfig(reward.status)
          return (
            <li
              key={reward.id}
              className={`grid px-2 py-2 text-xs ${REWARD_ROW_GRID}`}
            >
              <span className='text-muted-foreground'>
                {formatTimestamp(reward.created_time)}
              </span>
              <span className='min-w-0 truncate font-medium'>
                {reward.invitee_username || t('Deleted')}
              </span>
              <span className='text-muted-foreground sm:text-foreground tabular-nums'>
                <span className='sm:hidden'>{t('Reward Basis')}: </span>
                {formatQuota(reward.basis_quota)}
              </span>
              <span className='text-muted-foreground sm:text-foreground tabular-nums'>
                <span className='sm:hidden'>{t('Reward Amount')}: </span>
                {formatQuota(reward.reward_quota)}
              </span>
              <span>
                <StatusBadge
                  label={t(status.label)}
                  variant={status.variant}
                  copyable={false}
                />
              </span>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
