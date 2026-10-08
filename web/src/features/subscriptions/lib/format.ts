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
import type { TFunction } from 'i18next'

import dayjs from '@/lib/dayjs'

import type { SubscriptionPlan } from '../types'

function formatCustomSeconds(seconds: number, t: TFunction): string {
  const units = [
    { seconds: 86400, singular: 'day', plural: 'days' },
    { seconds: 3600, singular: 'hour', plural: 'hours' },
    { seconds: 60, singular: 'minute', plural: 'minutes' },
    { seconds: 1, singular: 'second', plural: 'seconds' },
  ]
  let remainder = seconds
  const parts: string[] = []

  for (const unit of units) {
    const value = Math.floor(remainder / unit.seconds)
    if (value > 0) {
      parts.push(`${value} ${t(value === 1 ? unit.singular : unit.plural)}`)
      remainder %= unit.seconds
    }
  }

  return parts.join(' ') || `0 ${t('seconds')}`
}

export function formatDuration(
  plan: Partial<SubscriptionPlan>,
  t: TFunction
): string {
  const unit = plan?.duration_unit || 'month'
  const value = plan?.duration_value || 1
  const unitLabels: Record<string, string> = {
    year: t('years'),
    month: t('months'),
    day: t('days'),
    hour: t('hours'),
    custom: t('Custom (seconds)'),
  }
  if (unit === 'custom') {
    return formatCustomSeconds(plan?.custom_seconds || 0, t)
  }
  return `${value} ${unitLabels[unit] || unit}`
}

export function formatResetPeriod(
  plan: Partial<SubscriptionPlan>,
  t: TFunction
): string {
  const period = plan?.quota_reset_period || 'never'
  if (period === 'daily') return t('Daily')
  if (period === 'weekly') return t('Weekly')
  if (period === 'monthly') return t('Monthly')
  if (period === 'custom') {
    const seconds = Number(plan?.quota_reset_custom_seconds || 0)
    return formatCustomSeconds(seconds, t)
  }
  return t('No Reset')
}

export function formatTimestamp(ts: number): string {
  if (!ts) return '-'
  return dayjs(ts * 1000).format('YYYY-MM-DD HH:mm:ss')
}
