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
import i18next from 'i18next'
import { useState, useEffect, useCallback } from 'react'

import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getAffiliateRewards } from '../api'
import type { AffiliateRewardEntry } from '../types'

// ============================================================================
// Affiliate Rewards Hook
// ============================================================================

/**
 * Reads the signed-in user's own invitation reward ledger. The endpoint returns
 * both the pending-settlement total (including rows beyond the listed ones) and
 * the most recent rows, so one request drives the card's "Pending Settlement"
 * figure and its detail list.
 */
export function useAffiliateRewards() {
  const [rewards, setRewards] = useState<AffiliateRewardEntry[]>([])
  const [pendingQuota, setPendingQuota] = useState(0)
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)

  const fetchRewards = useCallback(async () => {
    try {
      setLoading(true)
      setFailed(false)
      const response = requireServerSuccess(await getAffiliateRewards())
      setRewards(response.data?.rewards ?? [])
      setPendingQuota(response.data?.pending_quota ?? 0)
    } catch (error) {
      handleServerError(error, i18next.t('Failed to load invitation rewards'))
      setRewards([])
      setPendingQuota(0)
      setFailed(true)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchRewards()
  }, [fetchRewards])

  return { rewards, pendingQuota, loading, failed, refresh: fetchRewards }
}
