package model

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"

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

// InvitationRewardSettlementBatchSize bounds how many due rewards one scan
// claims. A pass repeats the scan until a batch comes back short, so the size
// only caps memory and transaction count per pass.
const InvitationRewardSettlementBatchSize = 200

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
// when the invitee has no inviter, rewards are disabled, the code's batch is
// marked as not earning rewards, or the reward basis is not positive (a
// zero-price plan is a recharge worth no reward). planPrice is only consulted
// for subscription redemptions, whose reward basis is the plan price at the
// site's quota rate. The reward quota is converted on the wallet scale
// (bigint columns, JavaScript-safe bound) and an out-of-range product fails the
// redemption transaction instead of being silently saturated.
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
	if basisQuota <= 0 {
		return nil
	}
	// The reward is wallet-scale: basis_quota and reward_quota are bigint, and a
	// basis can be as large as common.MaxWalletQuota. The ratio is validated
	// server-side to 0..1 and the basis never exceeds the wallet bound, so this
	// decimal product cannot leave the wallet domain in theory; keep the strict
	// conversion anyway, because if an oversized value ever reaches here, failing
	// the redemption transaction is better than silently recording a smaller
	// reward (the same rule the redemption wallet path follows).
	rewardQuota, err := common.WalletQuotaFromDecimalStrict(
		decimal.NewFromInt(int64(basisQuota)).Mul(decimal.NewFromFloat(ratio)),
	)
	if err != nil {
		return err
	}
	reward := &InvitationReward{
		RedemptionId: redemption.Id,
		InviterId:    inviterId,
		InviteeId:    inviteeId,
		BasisQuota:   basisQuota,
		Ratio:        ratio,
		RewardQuota:  rewardQuota,
		Status:       InvitationRewardStatusPending,
		CreatedTime:  now,
		SettleAfter:  now + InvitationRewardObservationWindowSeconds,
	}
	return tx.Create(reward).Error
}

// InvitationRewardSettlementSummary reports one settlement pass so the system
// task history shows what moved. Credited counts rows that reached the credited
// terminal state in this pass; CreditedQuota is the quota those rows added to
// inviter pools; MissingInviter is the subset of credited rows whose inviter row
// is gone, which is the only way a credited row moves nothing. Failed counts
// settlement attempts that errored: the row stays pending for a later pass, and
// because a failed row is still due it may be retried (and counted again) by a
// later batch round of the same pass. A failed row never blocks the other rows
// in the same pass.
type InvitationRewardSettlementSummary struct {
	Credited       int `json:"credited"`
	CreditedQuota  int `json:"credited_quota"`
	MissingInviter int `json:"missing_inviter"`
	Failed         int `json:"failed"`
}

// SettleDueInvitationRewards credits every pending reward whose observation
// window has closed into its inviter's affiliate pool: aff_quota and
// aff_history grow, while aff_count is left alone because it counts invited
// users, not rewards. Each row settles in its own transaction whose first write
// is the conditional status transition, so replaying a pass or running two
// instances at once cannot pay a reward twice — SQLite has no row locks
// (lockForUpdate is a no-op there), so the WHERE clause is the guard. Rows are
// read in id order in batches of batchSize (default
// InvitationRewardSettlementBatchSize) until a batch comes back short.
//
// A row whose own transaction fails is counted in the summary, logged, and
// skipped: it stays pending for a later pass instead of blocking every reward
// behind it (the batch scan restarts from the lowest pending id each round, so
// one permanently broken row must not pin the head of the queue). A failed row
// is still due, so a later batch round of the same pass may retry it. Only a
// failed scan (or a cancelled context) fails the whole pass.
func SettleDueInvitationRewards(ctx context.Context, now int64, batchSize int) (InvitationRewardSettlementSummary, error) {
	if batchSize <= 0 {
		batchSize = InvitationRewardSettlementBatchSize
	}
	var summary InvitationRewardSettlementSummary
	for {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		var due []InvitationReward
		if err := DB.Where("status = ? AND settle_after <= ?", InvitationRewardStatusPending, now).
			Order("id asc").
			Limit(batchSize).
			Find(&due).Error; err != nil {
			return summary, err
		}
		if len(due) == 0 {
			return summary, nil
		}
		progressed := false
		for i := range due {
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			outcome, err := settleInvitationRewardTx(due[i].Id, due[i].InviterId, due[i].RewardQuota, now)
			if err != nil {
				// Tolerate one bad row: its transaction rolled back, so it is
				// still pending and a later pass retries it, while every row
				// behind it settles in this pass.
				summary.Failed++
				common.SysError(fmt.Sprintf(
					"invitation reward %d settlement failed: %v", due[i].Id, err,
				))
				continue
			}
			if !outcome.credited {
				// A concurrent runner or an administrator reached the row first.
				continue
			}
			progressed = true
			summary.Credited++
			if outcome.missingInviter {
				// The row still becomes terminal so it is not rescanned every
				// hour, and the operator gets one line per orphaned reward.
				summary.MissingInviter++
				common.SysError(fmt.Sprintf(
					"invitation reward %d settled with no inviter %d to credit; %d quota stayed uncredited",
					due[i].Id, due[i].InviterId, due[i].RewardQuota,
				))
				continue
			}
			summary.CreditedQuota += due[i].RewardQuota
		}
		// A full batch with no progress means the rows were taken by someone
		// else; stop instead of spinning on a backlog another runner is draining.
		if len(due) < batchSize || !progressed {
			return summary, nil
		}
	}
}

type invitationRewardSettlementOutcome struct {
	credited       bool
	missingInviter bool
}

// settleInvitationRewardTx moves one pending reward to credited and adds its
// quota to the inviter's pool in a single transaction. It runs entirely on tx:
// with the shared single-connection SQLite fixtures a helper that reads the
// global DB inside this transaction would block until the test times out.
func settleInvitationRewardTx(rewardId int, inviterId int, rewardQuota int, now int64) (invitationRewardSettlementOutcome, error) {
	var outcome invitationRewardSettlementOutcome
	err := DB.Transaction(func(tx *gorm.DB) error {
		transition := tx.Model(&InvitationReward{}).
			Where("id = ? AND status = ?", rewardId, InvitationRewardStatusPending).
			Updates(map[string]any{
				"status":       InvitationRewardStatusCredited,
				"settled_time": now,
			})
		if transition.Error != nil {
			return transition.Error
		}
		if transition.RowsAffected != 1 {
			// The row was already settled or voided by another writer.
			return nil
		}
		outcome.credited = true

		credited := tx.Model(&User{}).
			Where("id = ?", inviterId).
			Updates(map[string]any{
				"aff_quota":   gorm.Expr("aff_quota + ?", rewardQuota),
				"aff_history": gorm.Expr("aff_history + ?", rewardQuota),
			})
		if credited.Error != nil {
			return credited.Error
		}
		if credited.RowsAffected > 0 {
			return nil
		}
		// MySQL counts changed rows rather than matched rows, so an update that
		// changed nothing reports zero even though the inviter exists.
		var existing int64
		if err := tx.Model(&User{}).Where("id = ?", inviterId).Count(&existing).Error; err != nil {
			return err
		}
		outcome.missingInviter = existing == 0
		return nil
	})
	return outcome, err
}
