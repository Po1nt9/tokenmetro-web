package model

import (
	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// Invitation reward ledger statuses. A row starts pending and then reaches one
// terminal state: credited once its observation window has closed, or voided by
// an administrator who refunded the recharge.
const (
	InvitationRewardStatusPending  = "pending"
	InvitationRewardStatusCredited = "credited"
	InvitationRewardStatusVoided   = "voided"
)

// InvitationRewardObservationWindowSeconds is the seven-day (168h) window
// between a recharge event and its reward settlement.
const InvitationRewardObservationWindowSeconds int64 = 168 * 60 * 60

// InvitationReward is the ledger row for one invitation reward: one row per
// recharge event, keyed by the redemption that produced it. RewardQuota is
// fixed when the row is created so a later ratio change cannot shrink an
// already-earned pending reward.
type InvitationReward struct {
	Id           int     `json:"id"`
	RedemptionId int     `json:"redemption_id" gorm:"uniqueIndex"`
	InviterId    int     `json:"inviter_id" gorm:"index"`
	InviteeId    int     `json:"invitee_id"`
	BasisQuota   int     `json:"basis_quota" gorm:"type:bigint;not null;default:0"`
	Ratio        float64 `json:"ratio" gorm:"not null;default:0"`
	RewardQuota  int     `json:"reward_quota" gorm:"type:bigint;not null;default:0"`
	Status       string  `json:"status" gorm:"type:varchar(32);not null;index:idx_invitation_rewards_status_settle_after,priority:1"`
	SettleAfter  int64   `json:"settle_after" gorm:"bigint;not null;default:0;index:idx_invitation_rewards_status_settle_after,priority:2"`
	CreatedTime  int64   `json:"created_time" gorm:"bigint;not null;default:0"`
	SettledTime  int64   `json:"settled_time" gorm:"bigint;not null;default:0"`
	VoidedTime   int64   `json:"voided_time" gorm:"bigint;not null;default:0"`
}

// createPendingInvitationRewardTx writes the pending ledger row for a
// successful sold-code redemption. It runs inside the redemption transaction so
// a redemption can never commit without its reward row, and it writes nothing
// when the invitee has no inviter, rewards are disabled, or the code's batch is
// marked as not earning rewards. planPrice is only consulted for subscription
// redemptions, whose reward basis is the plan price at the site's quota rate.
func createPendingInvitationRewardTx(tx *gorm.DB, redemption *Redemption, inviterId int, inviteeId int, planPrice float64, now int64) error {
	ratio := common.InviteRewardRatio
	if inviterId == 0 || ratio <= 0 || !redemption.RewardEligible {
		return nil
	}
	basisQuota := redemption.Quota
	if redemption.EffectiveOutcomeType() == RedemptionOutcomeSubscription {
		var err error
		basisQuota, err = calcSubscriptionBalanceQuota(planPrice)
		if err != nil {
			return err
		}
	}
	reward := &InvitationReward{
		RedemptionId: redemption.Id,
		InviterId:    inviterId,
		InviteeId:    inviteeId,
		BasisQuota:   basisQuota,
		Ratio:        ratio,
		RewardQuota:  common.QuotaFromFloat(float64(basisQuota) * ratio),
		Status:       InvitationRewardStatusPending,
		CreatedTime:  now,
		SettleAfter:  now + InvitationRewardObservationWindowSeconds,
	}
	return tx.Create(reward).Error
}
