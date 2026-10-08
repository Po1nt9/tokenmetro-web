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
import { useState, useCallback, useRef } from 'react'
import { toast } from 'sonner'

import { formatQuota } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import { previewRedemptionCode, redeemTopupCode } from '../api'
import type { RedemptionOutcome, RedemptionPreview } from '../types'

// ============================================================================
// Redemption Hook
// ============================================================================

function getRedemptionSuccessMessage(data: RedemptionOutcome | number): string {
  if (typeof data === 'number') {
    return i18next.t('Redemption successful! Added: {{quota}}', {
      quota: formatQuota(data),
    })
  }

  if (data.outcome_type === 'balance') {
    return i18next.t('Redemption successful! Added: {{quota}}', {
      quota: formatQuota(data.wallet_quota ?? 0),
    })
  }

  return i18next.t('Subscription redemption successful')
}

export function useRedemption() {
  const [redeeming, setRedeeming] = useState(false)
  const [confirmingRedemption, setConfirmingRedemption] = useState(false)
  const [preview, setPreview] = useState<RedemptionPreview | null>(null)
  const previewCodeRef = useRef<string | null>(null)
  const previewRequestIdRef = useRef(0)

  const previewCode = useCallback(async (code: string): Promise<boolean> => {
    if (!code || code.trim() === '') {
      toast.error(i18next.t('Please enter a redemption code'))
      return false
    }
    const requestId = ++previewRequestIdRef.current
    previewCodeRef.current = null
    setPreview(null)
    try {
      setRedeeming(true)
      const response = await previewRedemptionCode({ key: code })
      if (requestId !== previewRequestIdRef.current) return false
      if (response.success && response.data) {
        previewCodeRef.current = code
        setPreview(response.data)
        return true
      }
      handleServerError(response, i18next.t('Redemption failed'))
      return false
    } catch (error) {
      if (requestId !== previewRequestIdRef.current) return false
      handleServerError(error, i18next.t('Redemption failed'))
      return false
    } finally {
      if (requestId === previewRequestIdRef.current) setRedeeming(false)
    }
  }, [])

  const confirmRedemption = useCallback(
    async (code: string): Promise<boolean> => {
      if (!preview || !code || code !== previewCodeRef.current) return false

      const requestId = ++previewRequestIdRef.current
      previewCodeRef.current = null
      setPreview(null)
      try {
        setRedeeming(true)
        setConfirmingRedemption(true)
        const response = await redeemTopupCode({ key: code })
        if (response.success && response.data) {
          toast.success(getRedemptionSuccessMessage(response.data))
          return true
        }
        handleServerError(response, i18next.t('Redemption failed'))
        return false
      } catch (error) {
        handleServerError(error, i18next.t('Redemption failed'))
        return false
      } finally {
        if (requestId === previewRequestIdRef.current) {
          setRedeeming(false)
          setConfirmingRedemption(false)
        }
      }
    },
    [preview]
  )

  const clearPreview = useCallback(() => {
    previewRequestIdRef.current += 1
    previewCodeRef.current = null
    setPreview(null)
    if (!confirmingRedemption) setRedeeming(false)
  }, [confirmingRedemption])

  return {
    redeeming,
    confirmingRedemption,
    preview,
    previewCode,
    confirmRedemption,
    clearPreview,
  }
}
