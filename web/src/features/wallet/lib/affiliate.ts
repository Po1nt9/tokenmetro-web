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
import type { StatusBadgeProps } from '@/components/status-badge'

import type { AffiliateRewardStatus } from '../types'

// ============================================================================
// Affiliate Functions
// ============================================================================

/**
 * Generate affiliate registration link
 */
export function generateAffiliateLink(affCode: string): string {
  if (typeof window === 'undefined') return ''
  return `${window.location.origin}/sign-up?aff=${affCode}`
}

interface AffiliateRewardStatusConfig {
  variant: StatusBadgeProps['variant']
  /** i18n key, rendered through t() at the call site */
  label: string
}

const AFFILIATE_REWARD_STATUS_CONFIG: Record<
  AffiliateRewardStatus,
  AffiliateRewardStatusConfig
> = {
  pending: { variant: 'warning', label: 'Pending Settlement' },
  credited: { variant: 'success', label: 'Credited' },
  voided: { variant: 'danger', label: 'Voided' },
}

/**
 * Status badge configuration for one reward ledger row. Unknown statuses fall
 * back to the raw value so a future backend state stays visible instead of
 * rendering blank.
 */
export function getAffiliateRewardStatusConfig(
  status: string
): AffiliateRewardStatusConfig {
  return (
    AFFILIATE_REWARD_STATUS_CONFIG[status as AffiliateRewardStatus] ?? {
      variant: 'neutral',
      label: status,
    }
  )
}
