package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// Administrative void failures of a single pending invitation reward. They stay
// distinct so the operator can tell "this code never earned a reward" from
// "the reward is already voided" and from "settlement already paid it out";
// the controller returns them as explicit messages instead of a generic error.
var (
	ErrInvitationRewardNotFound      = errors.New("no invitation reward exists for this redemption code")
	ErrInvitationRewardAlreadyVoided = errors.New("the invitation reward for this redemption code has already been voided")
	ErrInvitationRewardSettled       = errors.New("the invitation reward has been settled and cannot be reversed")
)

// VoidInvitationRewardByRedemptionId marks the reward ledger row produced by a
// redemption as voided, which is how an administrator refunds a recharge
// during the observation window. Only a pending row can be voided: credited is
// final, so an already-paid reward is refused instead of silently reversed.
// The transition is a conditional update on the pending status rather than a
// plain write, because SQLite has no row locks (lockForUpdate is a no-op there)
// and the hourly settlement task races this call for the same row. Losing that
// race reports the state the row actually reached.
func VoidInvitationRewardByRedemptionId(redemptionId int) error {
	if redemptionId <= 0 {
		return ErrInvitationRewardNotFound
	}
	var reward InvitationReward
	err := DB.Where("redemption_id = ?", redemptionId).First(&reward).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvitationRewardNotFound
		}
		return err
	}
	switch reward.Status {
	case InvitationRewardStatusCredited:
		return ErrInvitationRewardSettled
	case InvitationRewardStatusVoided:
		return ErrInvitationRewardAlreadyVoided
	}
	result := DB.Model(&InvitationReward{}).
		Where("id = ? AND status = ?", reward.Id, InvitationRewardStatusPending).
		Updates(map[string]any{
			"status":      InvitationRewardStatusVoided,
			"voided_time": common.GetTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	// The settlement task moved the row between the read and the update; report
	// the state that won instead of pretending the void succeeded.
	var current InvitationReward
	if err := DB.Select("status").First(&current, reward.Id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvitationRewardNotFound
		}
		return err
	}
	switch current.Status {
	case InvitationRewardStatusCredited:
		return ErrInvitationRewardSettled
	case InvitationRewardStatusVoided:
		return ErrInvitationRewardAlreadyVoided
	default:
		return errors.New("invitation reward could not be voided")
	}
}
