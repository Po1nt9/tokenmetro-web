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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { handleServerError } from '@/lib/handle-server-error'

import { voidRedemptionReward } from '../api'
import { ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import { useRedemptions } from './redemptions-provider'

/**
 * Admin action for a refunded recharge: void the single pending invitation
 * reward produced by one redemption code. This is not the batch-level
 * "not reward-eligible" switch on the code itself — that one stops the reward
 * from ever being created, while this one takes back an already created one.
 * The backend owns the rules (pending only, settled rewards are refused), so
 * the dialog reports its message verbatim instead of guessing locally.
 */
export function RedemptionsVoidRewardDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, triggerRefresh } = useRedemptions()
  const [isVoiding, setIsVoiding] = useState(false)

  const handleVoid = async () => {
    if (!currentRow) return

    setIsVoiding(true)
    try {
      const result = await voidRedemptionReward(currentRow.id)
      if (result.success) {
        toast.success(t(SUCCESS_MESSAGES.REWARD_VOIDED))
        setOpen(null)
        triggerRefresh()
      } else {
        handleServerError(result, t(ERROR_MESSAGES.REWARD_VOID_FAILED))
      }
    } catch (error) {
      handleServerError(error, t(ERROR_MESSAGES.REWARD_VOID_FAILED))
    } finally {
      setIsVoiding(false)
    }
  }

  return (
    <ConfirmDialog
      open={open === 'void-reward'}
      onOpenChange={(isOpen) => !isOpen && setOpen(null)}
      title={t('Void invitation reward')}
      desc={t(
        'Void the pending invitation reward produced by this redemption code. This action cannot be undone, and a reward that already settled can no longer be revoked.'
      )}
      confirmText={t('Void invitation reward')}
      destructive
      isLoading={isVoiding}
      handleConfirm={handleVoid}
    />
  )
}
