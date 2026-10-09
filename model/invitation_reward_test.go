package model

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const invitationRewardTestRatio = 0.05

// enableInviteRewardRatio writes the runtime ratio through the same option path
// the admin settings page uses, and restores the default afterwards.
func enableInviteRewardRatio(t *testing.T, value string) {
	t.Helper()
	require.NoError(t, UpdateOption("InviteRewardRatio", value))
	t.Cleanup(func() {
		common.InviteRewardRatio = 0
		require.NoError(t, DB.Where("key = ?", "InviteRewardRatio").Delete(&Option{}).Error)
	})
}

// useMultiConnectionSQLiteFixture swaps the package database for an in-memory
// SQLite database with more than one connection. The subscription redemption
// path reads the database clock inside its transaction, which would wait for
// the connection the open transaction holds on the shared single-connection
// fixture. Call it before setupInvitationRewardFixture.
func useMultiConnectionSQLiteFixture(t *testing.T) {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	initCol()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		initCol()
		_ = sqlDB.Close()
	})
}

// setupInvitationRewardFixture resets the reward-related tables and creates the
// inviter. Redeemers are created per case so tests can run with or without an
// invitation link.
func setupInvitationRewardFixture(t *testing.T) (inviterId int) {
	t.Helper()
	previousOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.InviteRewardRatio = 0
	require.NoError(t, DB.AutoMigrate(
		&Redemption{}, &User{}, &Log{}, &InvitationReward{},
		&SubscriptionPlan{}, &UserSubscription{}, &Option{},
	))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	for _, table := range []string{"invitation_rewards", "users", "logs", "user_subscriptions", "subscription_plans"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		common.InviteRewardRatio = 0
		common.OptionMap = previousOptionMap
		for _, table := range []string{"invitation_rewards", "users", "logs", "user_subscriptions", "subscription_plans"} {
			DB.Exec("DELETE FROM " + table)
		}
		DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{})
	})

	inviter := &User{Username: "reward-inviter", Password: "password", Status: common.UserStatusEnabled, AffCode: common.GetRandomString(4)}
	require.NoError(t, DB.Create(inviter).Error)
	return inviter.Id
}

func createInvitationRewardRedeemer(t *testing.T, inviterId int) int {
	t.Helper()
	user := &User{
		Username:  fmt.Sprintf("reward-invitee-%s", common.GetUUID()[:8]),
		Password:  "password",
		Status:    common.UserStatusEnabled,
		AffCode:   common.GetRandomString(4),
		InviterId: inviterId,
	}
	require.NoError(t, DB.Create(user).Error)
	return user.Id
}

func insertInvitationRewardRedemption(t *testing.T, quota int, rewardEligible bool) *Redemption {
	t.Helper()
	redemption := &Redemption{
		Name:           "reward-code",
		Key:            common.GetUUID(),
		Status:         common.RedemptionCodeStatusEnabled,
		Quota:          quota,
		RewardEligible: rewardEligible,
		CreatedTime:    common.GetTimestamp(),
	}
	require.NoError(t, redemption.Insert())
	return redemption
}

func assertInvitationRewardCount(t *testing.T, redemptionId int, want int64) {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&InvitationReward{}).Where("redemption_id = ?", redemptionId).Count(&count).Error)
	assert.EqualValues(t, want, count)
}

func TestRedeemCreatesPendingInvitationRewardFromBalanceCode(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	enableInviteRewardRatio(t, "0.05")
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	redemption := insertInvitationRewardRedemption(t, 2_000_000, true)

	result, err := Redeem(redemption.Key, inviteeId)
	require.NoError(t, err)
	assert.Equal(t, 2_000_000, result.WalletQuota)

	var rewards []InvitationReward
	require.NoError(t, DB.Where("inviter_id = ?", inviterId).Find(&rewards).Error)
	require.Len(t, rewards, 1)
	reward := rewards[0]
	assert.Equal(t, redemption.Id, reward.RedemptionId)
	assert.Equal(t, inviterId, reward.InviterId)
	assert.Equal(t, inviteeId, reward.InviteeId)
	assert.Equal(t, 2_000_000, reward.BasisQuota)
	assert.Equal(t, invitationRewardTestRatio, reward.Ratio)
	assert.Equal(t, 100_000, reward.RewardQuota)
	assert.Equal(t, InvitationRewardStatusPending, reward.Status)
	assert.Zero(t, reward.SettledTime)
	assert.Zero(t, reward.VoidedTime)

	var stored Redemption
	require.NoError(t, DB.First(&stored, redemption.Id).Error)
	assert.Equal(t, stored.RedeemedTime, reward.CreatedTime, "reward time must be the redemption moment")
	assert.Equal(t, reward.CreatedTime+168*60*60, reward.SettleAfter, "settlement waits the 168h observation window")
}

func TestRedeemStoresRewardQuotaExactBeyondInt32(t *testing.T) {
	// The reward ledger columns are bigint and a basis is wallet-scale, so a
	// reward legitimately exceeds the single-request int32 boundary. 100e9 *
	// 0.05 = 5e9 exactly; a saturating int32 conversion would silently store
	// 2147483647 instead.
	inviterId := setupInvitationRewardFixture(t)
	enableInviteRewardRatio(t, "0.05")
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	redemption := insertInvitationRewardRedemption(t, 100_000_000_000, true)

	result, err := Redeem(redemption.Key, inviteeId)
	require.NoError(t, err)
	assert.Equal(t, 100_000_000_000, result.WalletQuota)

	var reward InvitationReward
	require.NoError(t, DB.First(&reward, "redemption_id = ?", redemption.Id).Error)
	assert.Equal(t, 100_000_000_000, reward.BasisQuota)
	assert.Equal(t, 5_000_000_000, reward.RewardQuota, "a wallet-scale reward must not saturate at the int32 boundary")
}

func TestRedeemCreatesPendingInvitationRewardFromSubscriptionCode(t *testing.T) {
	// The subscription path reads the database clock inside the redemption
	// transaction, so it needs a database with more than one connection.
	useMultiConnectionSQLiteFixture(t)
	inviterId := setupInvitationRewardFixture(t)
	enableInviteRewardRatio(t, "0.05")
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	plan := &SubscriptionPlan{
		Title: "Reward plan", Enabled: true, DurationUnit: SubscriptionDurationMonth,
		DurationValue: 1, PriceAmount: 1.5, TotalAmount: 1_000_000, QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	redemption := insertInvitationRewardRedemption(t, 100, true)
	require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", redemption.Id).Updates(map[string]any{
		"outcome_type":         RedemptionOutcomeSubscription,
		"subscription_plan_id": plan.Id,
	}).Error)

	result, err := Redeem(redemption.Key, inviteeId)
	require.NoError(t, err)
	assert.Equal(t, RedemptionOutcomeSubscription, result.OutcomeType)

	var reward InvitationReward
	require.NoError(t, DB.First(&reward, "redemption_id = ?", redemption.Id).Error)
	assert.Equal(t, 750_000, reward.BasisQuota, "basis is the plan price at QuotaPerUnit")
	assert.Equal(t, invitationRewardTestRatio, reward.Ratio)
	assert.Equal(t, 37_500, reward.RewardQuota)
	assert.Equal(t, InvitationRewardStatusPending, reward.Status)

	var user User
	require.NoError(t, DB.First(&user, inviteeId).Error)
	assert.Zero(t, user.Quota, "subscription redemptions must not touch wallet balance")
}

func TestRedeemSkipsInvitationRewardWhenRewardBasisIsZero(t *testing.T) {
	// A zero-price subscription plan still delivers its entitlement, but its
	// reward basis is zero, so it must not leave a pending "0 面额 / 0 返利" row
	// in the inviter's detail list.
	useMultiConnectionSQLiteFixture(t)
	inviterId := setupInvitationRewardFixture(t)
	enableInviteRewardRatio(t, "0.05")
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	plan := &SubscriptionPlan{
		Title: "Zero price plan", Enabled: true, DurationUnit: SubscriptionDurationMonth,
		DurationValue: 1, PriceAmount: 0, TotalAmount: 1_000_000, QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	redemption := insertInvitationRewardRedemption(t, 100, true)
	require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", redemption.Id).Updates(map[string]any{
		"outcome_type":         RedemptionOutcomeSubscription,
		"subscription_plan_id": plan.Id,
	}).Error)

	result, err := Redeem(redemption.Key, inviteeId)
	require.NoError(t, err)
	assert.Equal(t, RedemptionOutcomeSubscription, result.OutcomeType)
	assertInvitationRewardCount(t, redemption.Id, 0)
}

func TestRedeemKeepsSingleInvitationRewardOnRepeatAndConcurrentAttempts(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	enableInviteRewardRatio(t, "0.05")
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	redemption := insertInvitationRewardRedemption(t, 1_000_000, true)

	const attempts = 5
	successes := make([]bool, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := range attempts {
		go func(index int) {
			defer wg.Done()
			_, err := Redeem(redemption.Key, inviteeId)
			successes[index] = err == nil
		}(i)
	}
	wg.Wait()
	successCount := 0
	for _, success := range successes {
		if success {
			successCount++
		}
	}
	assert.Equal(t, 1, successCount, "exactly one concurrent redemption may succeed")
	assertInvitationRewardCount(t, redemption.Id, 1)

	var user User
	require.NoError(t, DB.First(&user, inviteeId).Error)
	assert.Equal(t, 1_000_000, user.Quota, "quota must be credited exactly once")

	_, err := Redeem(redemption.Key, inviteeId)
	require.Error(t, err)
	assertInvitationRewardCount(t, redemption.Id, 1)

	// The redemption id is the ledger's idempotency key, enforced by the unique index.
	duplicate := &InvitationReward{RedemptionId: redemption.Id, InviterId: inviterId, InviteeId: inviteeId, Status: InvitationRewardStatusPending}
	require.Error(t, DB.Create(duplicate).Error, "a redemption must not have two reward rows")
	assertInvitationRewardCount(t, redemption.Id, 1)
}

func TestRedeemSkipsInvitationRewardWhenIneligible(t *testing.T) {
	t.Run("no inviter", func(t *testing.T) {
		setupInvitationRewardFixture(t)
		enableInviteRewardRatio(t, "0.05")
		redeemerId := createInvitationRewardRedeemer(t, 0)
		redemption := insertInvitationRewardRedemption(t, 500_000, true)

		_, err := Redeem(redemption.Key, redeemerId)
		require.NoError(t, err)
		assertInvitationRewardCount(t, redemption.Id, 0)
	})

	t.Run("ratio disabled", func(t *testing.T) {
		inviterId := setupInvitationRewardFixture(t)
		inviteeId := createInvitationRewardRedeemer(t, inviterId)
		redemption := insertInvitationRewardRedemption(t, 500_000, true)

		_, err := Redeem(redemption.Key, inviteeId)
		require.NoError(t, err)
		assertInvitationRewardCount(t, redemption.Id, 0)
	})

	t.Run("batch opted out of rewards", func(t *testing.T) {
		inviterId := setupInvitationRewardFixture(t)
		enableInviteRewardRatio(t, "0.05")
		inviteeId := createInvitationRewardRedeemer(t, inviterId)
		redemption := insertInvitationRewardRedemption(t, 500_000, false)

		_, err := Redeem(redemption.Key, inviteeId)
		require.NoError(t, err)
		assertInvitationRewardCount(t, redemption.Id, 0)
	})
}

func TestRedeemFailureLeavesNoInvitationReward(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	enableInviteRewardRatio(t, "0.05")
	inviteeId := createInvitationRewardRedeemer(t, inviterId)

	t.Run("unknown code", func(t *testing.T) {
		_, err := Redeem(common.GetUUID(), inviteeId)
		require.ErrorIs(t, err, ErrRedeemFailed)
		var count int64
		require.NoError(t, DB.Model(&InvitationReward{}).Count(&count).Error)
		assert.Zero(t, count)
	})

	t.Run("expired code", func(t *testing.T) {
		redemption := insertInvitationRewardRedemption(t, 500_000, true)
		require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", redemption.Id).
			Update("expired_time", common.GetTimestamp()-1).Error)

		_, err := Redeem(redemption.Key, inviteeId)
		require.ErrorIs(t, err, ErrRedeemFailed)
		assertInvitationRewardCount(t, redemption.Id, 0)
	})

	t.Run("already used code", func(t *testing.T) {
		redemption := insertInvitationRewardRedemption(t, 500_000, true)
		require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", redemption.Id).
			Update("status", common.RedemptionCodeStatusUsed).Error)

		_, err := Redeem(redemption.Key, inviteeId)
		require.ErrorIs(t, err, ErrRedeemFailed)
		assertInvitationRewardCount(t, redemption.Id, 0)
	})
}

// TestRedeemRollsBackWhenTheRewardLedgerRowAlreadyExists pins the deliberate
// design around the unique index on redemption_id: it is a backstop that must
// fail the whole redemption loudly, not a conflict to swallow. Redeem's own
// conditional status advance makes the seeded state unreachable, which is the
// point — the case reproduces what a regression in that advance would leave
// behind, so a future OnConflict DoNothing cannot quietly change the outcome.
func TestRedeemRollsBackWhenTheRewardLedgerRowAlreadyExists(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	enableInviteRewardRatio(t, "0.05")
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	redemption := insertInvitationRewardRedemption(t, 500_000, true)
	seeded := &InvitationReward{
		RedemptionId: redemption.Id,
		InviterId:    inviterId,
		InviteeId:    inviteeId,
		BasisQuota:   500_000,
		Ratio:        invitationRewardTestRatio,
		RewardQuota:  25_000,
		Status:       InvitationRewardStatusPending,
		CreatedTime:  common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(seeded).Error)

	_, err := Redeem(redemption.Key, inviteeId)
	require.ErrorIs(t, err, ErrRedeemFailed, "the unique-index conflict must propagate, not be skipped")

	var stored Redemption
	require.NoError(t, DB.First(&stored, redemption.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, stored.Status, "the whole redemption must roll back so the code stays usable")
	assert.Zero(t, stored.RedeemedTime)
	assert.Zero(t, stored.UsedUserId)

	var user User
	require.NoError(t, DB.First(&user, inviteeId).Error)
	assert.Zero(t, user.Quota, "the balance credit must roll back with the redemption")

	var rewards []InvitationReward
	require.NoError(t, DB.Where("redemption_id = ?", redemption.Id).Find(&rewards).Error)
	require.Len(t, rewards, 1)
	assert.Equal(t, seeded.Id, rewards[0].Id, "the pre-existing ledger row must be left untouched")
}

func TestInvitationRewardRatioSnapshotSurvivesOptionChange(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	enableInviteRewardRatio(t, "0.05")
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	first := insertInvitationRewardRedemption(t, 1_000_000, true)
	_, err := Redeem(first.Key, inviteeId)
	require.NoError(t, err)

	require.NoError(t, UpdateOption("InviteRewardRatio", "0.10"))

	var stored InvitationReward
	require.NoError(t, DB.First(&stored, "redemption_id = ?", first.Id).Error)
	assert.Equal(t, invitationRewardTestRatio, stored.Ratio, "the ratio snapshot must not follow later changes")
	assert.Equal(t, 50_000, stored.RewardQuota, "the reward quota is fixed when the row is created")

	second := insertInvitationRewardRedemption(t, 1_000_000, true)
	_, err = Redeem(second.Key, inviteeId)
	require.NoError(t, err)
	var secondReward InvitationReward
	require.NoError(t, DB.First(&secondReward, "redemption_id = ?", second.Id).Error)
	assert.Equal(t, 0.10, secondReward.Ratio)
	assert.Equal(t, 100_000, secondReward.RewardQuota)
}

func TestInviteRewardRatioOptionUpdatesRuntimeRatio(t *testing.T) {
	setupInvitationRewardFixture(t)
	enableInviteRewardRatio(t, "0.05")
	assert.Equal(t, invitationRewardTestRatio, common.InviteRewardRatio)

	var option Option
	require.NoError(t, DB.First(&option, "key = ?", "InviteRewardRatio").Error)
	assert.Equal(t, "0.05", option.Value)
}

func TestRedemptionRewardEligiblePersistsOptOutAndLegacyRowsStayEligible(t *testing.T) {
	setupInvitationRewardFixture(t)

	optedOut := insertInvitationRewardRedemption(t, 500_000, false)
	var stored Redemption
	require.NoError(t, DB.First(&stored, optedOut.Id).Error)
	assert.False(t, stored.RewardEligible, "a batch opted out of rewards must persist as not eligible")
	assert.False(t, optedOut.RewardEligible)

	eligible := insertInvitationRewardRedemption(t, 500_000, true)
	var storedEligible Redemption
	require.NoError(t, DB.First(&storedEligible, eligible.Id).Error)
	assert.True(t, storedEligible.RewardEligible)

	legacy := struct {
		Id     int    `gorm:"primaryKey"`
		Key    string `gorm:"type:char(32);uniqueIndex"`
		Status int
		Quota  int
	}{Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, Quota: 500}
	require.NoError(t, DB.Table("legacy_reward_redemptions").AutoMigrate(&legacy))
	require.NoError(t, DB.Table("legacy_reward_redemptions").Create(&legacy).Error)
	require.NoError(t, DB.Table("legacy_reward_redemptions").AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Table("legacy_reward_redemptions").AutoMigrate(&Redemption{}))
	var upgraded Redemption
	require.NoError(t, DB.Table("legacy_reward_redemptions").First(&upgraded, legacy.Id).Error)
	assert.True(t, upgraded.RewardEligible, "codes created before the column existed stay reward-eligible")
}

// settlementRewardKey hands each ledger row its own redemption id. The unique
// index on that column is the idempotency key, so a settlement case must never
// reuse one.
var settlementRewardKey atomic.Int64

// insertSettlementReward writes one ledger row with an explicit observation
// window: settleAfter decides whether a pass may touch the row, so cases never
// sleep and never depend on the wall clock.
func insertSettlementReward(t *testing.T, inviterId int, inviteeId int, rewardQuota int, status string, settleAfter int64) *InvitationReward {
	t.Helper()
	reward := &InvitationReward{
		RedemptionId: int(settlementRewardKey.Add(1)),
		InviterId:    inviterId,
		InviteeId:    inviteeId,
		BasisQuota:   rewardQuota * 20,
		Ratio:        0.05,
		RewardQuota:  rewardQuota,
		Status:       status,
		CreatedTime:  settleAfter - InvitationRewardObservationWindowSeconds,
		SettleAfter:  settleAfter,
	}
	require.NoError(t, DB.Create(reward).Error)
	return reward
}

func assertAffiliatePool(t *testing.T, userId int, wantQuota int, wantHistory int) {
	t.Helper()
	var user User
	require.NoError(t, DB.First(&user, userId).Error)
	assert.Equal(t, wantQuota, user.AffQuota, "aff_quota")
	assert.Equal(t, wantHistory, user.AffHistoryQuota, "aff_history")
	assert.Zero(t, user.AffCount, "settlement must not count the invitation a second time")
}

func TestSettleDueInvitationRewardsCreditsDueRewardIntoInviterPool(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	now := common.GetTimestamp()
	reward := insertSettlementReward(t, inviterId, inviteeId, 50_000, InvitationRewardStatusPending, now-60)

	summary, err := SettleDueInvitationRewards(context.Background(), now, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Credited)
	assert.Equal(t, 50_000, summary.CreditedQuota)
	assert.Zero(t, summary.MissingInviter)

	var stored InvitationReward
	require.NoError(t, DB.First(&stored, reward.Id).Error)
	assert.Equal(t, InvitationRewardStatusCredited, stored.Status)
	assert.Equal(t, now, stored.SettledTime)
	assert.Zero(t, stored.VoidedTime)

	assertAffiliatePool(t, inviterId, 50_000, 50_000)
}

func TestSettleDueInvitationRewardsLeavesUnmaturedAndVoidedRowsUntouched(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	now := common.GetTimestamp()
	unmatured := insertSettlementReward(t, inviterId, inviteeId, 40_000, InvitationRewardStatusPending, now+1)
	voided := insertSettlementReward(t, inviterId, inviteeId, 30_000, InvitationRewardStatusVoided, now-1)
	boundary := insertSettlementReward(t, inviterId, inviteeId, 20_000, InvitationRewardStatusPending, now)

	summary, err := SettleDueInvitationRewards(context.Background(), now, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Credited, "only the row whose window closed by the pass timestamp settles")
	assert.Equal(t, 20_000, summary.CreditedQuota)

	var storedUnmatured InvitationReward
	require.NoError(t, DB.First(&storedUnmatured, unmatured.Id).Error)
	assert.Equal(t, InvitationRewardStatusPending, storedUnmatured.Status)
	assert.Zero(t, storedUnmatured.SettledTime)

	var storedVoided InvitationReward
	require.NoError(t, DB.First(&storedVoided, voided.Id).Error)
	assert.Equal(t, InvitationRewardStatusVoided, storedVoided.Status)
	assert.Zero(t, storedVoided.SettledTime)

	var storedBoundary InvitationReward
	require.NoError(t, DB.First(&storedBoundary, boundary.Id).Error)
	assert.Equal(t, InvitationRewardStatusCredited, storedBoundary.Status)
	assert.Equal(t, now, storedBoundary.SettledTime)

	assertAffiliatePool(t, inviterId, 20_000, 20_000)
}

func TestSettleDueInvitationRewardsNeverPaysTwice(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	now := common.GetTimestamp()
	reward := insertSettlementReward(t, inviterId, inviteeId, 50_000, InvitationRewardStatusPending, now-1)

	first, err := SettleDueInvitationRewards(context.Background(), now, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, first.Credited)
	assert.Equal(t, 50_000, first.CreditedQuota)

	second, err := SettleDueInvitationRewards(context.Background(), now, 0)
	require.NoError(t, err)
	assert.Zero(t, second.Credited)
	assert.Zero(t, second.CreditedQuota)
	assert.Zero(t, second.MissingInviter)

	var stored InvitationReward
	require.NoError(t, DB.First(&stored, reward.Id).Error)
	assert.Equal(t, InvitationRewardStatusCredited, stored.Status)
	assert.Equal(t, now, stored.SettledTime, "a replayed pass must not rewrite the terminal row")

	assertAffiliatePool(t, inviterId, 50_000, 50_000)
}

func TestSettleDueInvitationRewardsAdvancesRowsWhoseInviterIsGone(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	gone := &User{Username: "settlement-gone-inviter", Password: "password", Status: common.UserStatusEnabled, AffCode: common.GetRandomString(4)}
	require.NoError(t, DB.Create(gone).Error)
	require.NoError(t, DB.Delete(&User{}, gone.Id).Error)
	now := common.GetTimestamp()
	reward := insertSettlementReward(t, gone.Id, inviteeId, 10_000, InvitationRewardStatusPending, now-1)

	summary, err := SettleDueInvitationRewards(context.Background(), now, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Credited)
	assert.Equal(t, 1, summary.MissingInviter, "the orphaned row is counted as credited and as unpaid")
	assert.Zero(t, summary.CreditedQuota, "no quota can reach a pool without an inviter")

	var stored InvitationReward
	require.NoError(t, DB.First(&stored, reward.Id).Error)
	assert.Equal(t, InvitationRewardStatusCredited, stored.Status, "the row must reach a terminal state so it stops being rescanned")
	assert.Equal(t, now, stored.SettledTime)

	replay, err := SettleDueInvitationRewards(context.Background(), now, 0)
	require.NoError(t, err)
	assert.Zero(t, replay.Credited)
	assert.Zero(t, replay.MissingInviter, "a settled row is never scanned again")

	assertAffiliatePool(t, inviterId, 0, 0)
}

func TestSettleDueInvitationRewardsDrainsBatchesAndSumsQuota(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	now := common.GetTimestamp()
	insertSettlementReward(t, inviterId, inviteeId, 10_000, InvitationRewardStatusPending, now-3)
	insertSettlementReward(t, inviterId, inviteeId, 20_000, InvitationRewardStatusPending, now-2)
	insertSettlementReward(t, inviterId, inviteeId, 30_000, InvitationRewardStatusPending, now-1)

	summary, err := SettleDueInvitationRewards(context.Background(), now, 2)
	require.NoError(t, err)
	assert.Equal(t, 3, summary.Credited, "the pass must drain every batch, not just the first")
	assert.Equal(t, 60_000, summary.CreditedQuota)

	var pending int64
	require.NoError(t, DB.Model(&InvitationReward{}).Where("status = ?", InvitationRewardStatusPending).Count(&pending).Error)
	assert.Zero(t, pending)

	assertAffiliatePool(t, inviterId, 60_000, 60_000)
}

// TestSettleDueInvitationRewardsContinuesPastAFailingRow proves one broken row
// cannot hold back the rows behind it. A SQLite trigger aborts the pool credit
// for a single inviter without any production test hook, which makes the
// lowest-id row fail while a healthy row follows it.
func TestSettleDueInvitationRewardsContinuesPastAFailingRow(t *testing.T) {
	inviterId := setupInvitationRewardFixture(t)
	inviteeId := createInvitationRewardRedeemer(t, inviterId)
	brokenInviter := &User{Username: "settlement-broken-inviter", Password: "password", Status: common.UserStatusEnabled, AffCode: common.GetRandomString(4)}
	require.NoError(t, DB.Create(brokenInviter).Error)

	now := common.GetTimestamp()
	broken := insertSettlementReward(t, brokenInviter.Id, inviteeId, 10_000, InvitationRewardStatusPending, now-2)
	healthy := insertSettlementReward(t, inviterId, inviteeId, 20_000, InvitationRewardStatusPending, now-1)
	require.Less(t, broken.Id, healthy.Id, "the failing row must be the first one scanned")

	trigger := fmt.Sprintf("invitation_reward_fail_%d", broken.Id)
	require.NoError(t, DB.Exec(fmt.Sprintf(
		"CREATE TRIGGER %s BEFORE UPDATE OF aff_quota ON users WHEN NEW.id = %d BEGIN SELECT RAISE(ABORT, 'test settlement failure'); END",
		trigger, brokenInviter.Id,
	)).Error)
	t.Cleanup(func() { require.NoError(t, DB.Exec("DROP TRIGGER IF EXISTS "+trigger).Error) })

	summary, err := SettleDueInvitationRewards(context.Background(), now, 0)
	require.NoError(t, err, "a single row failure must not fail the pass")
	assert.Equal(t, 1, summary.Failed, "the broken row is counted, not hidden")
	assert.Equal(t, 1, summary.Credited)
	assert.Equal(t, 20_000, summary.CreditedQuota)

	var storedBroken InvitationReward
	require.NoError(t, DB.First(&storedBroken, broken.Id).Error)
	assert.Equal(t, InvitationRewardStatusPending, storedBroken.Status, "a failed row stays pending for a later pass")
	assert.Zero(t, storedBroken.SettledTime)

	var storedHealthy InvitationReward
	require.NoError(t, DB.First(&storedHealthy, healthy.Id).Error)
	assert.Equal(t, InvitationRewardStatusCredited, storedHealthy.Status)
	assert.Equal(t, now, storedHealthy.SettledTime)

	assertAffiliatePool(t, inviterId, 20_000, 20_000)
	var brokenUser User
	require.NoError(t, DB.First(&brokenUser, brokenInviter.Id).Error)
	assert.Zero(t, brokenUser.AffQuota, "the failing row must move nothing")
	assert.Zero(t, brokenUser.AffHistoryQuota)
}

// TestInvitationRewardSettlementDatabaseMatrix proves the conditional update and
// the inviter pool arithmetic behave identically on every supported engine.
// Engines whose DSN fixture is absent are skipped, following the existing
// database-matrix convention; prefixed tables keep the shared fixtures clean.
func TestInvitationRewardSettlementDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var dialector gorm.Dialector
			dbType := common.DatabaseTypeSQLite
			switch dialect {
			case "sqlite":
				dialector = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				dialector = mysql.Open(dsn)
				dbType = common.DatabaseTypeMySQL
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				dialector = postgres.Open(dsn)
				dbType = common.DatabaseTypePostgreSQL
			}
			db, err := gorm.Open(dialector, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "invitation_settlement_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousDB := DB
			previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
			DB = db
			common.SetDatabaseTypes(dbType, dbType)
			t.Cleanup(func() {
				DB = previousDB
				common.SetDatabaseTypes(previousMain, previousLog)
				require.NoError(t, db.Migrator().DropTable(&InvitationReward{}, &User{}))
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&User{}, &InvitationReward{}))
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			inviter := User{Username: "matrix-inviter", Password: "password", Status: common.UserStatusEnabled, AffCode: common.GetRandomString(4)}
			require.NoError(t, db.Create(&inviter).Error)
			now := common.GetTimestamp()
			reward := InvitationReward{
				RedemptionId: 1,
				InviterId:    inviter.Id,
				BasisQuota:   1_000_000,
				Ratio:        0.05,
				RewardQuota:  50_000,
				Status:       InvitationRewardStatusPending,
				CreatedTime:  now - InvitationRewardObservationWindowSeconds - 1,
				SettleAfter:  now - 1,
			}
			require.NoError(t, db.Create(&reward).Error)

			summary, err := SettleDueInvitationRewards(t.Context(), now, 0)
			require.NoError(t, err)
			assert.Equal(t, 1, summary.Credited)
			assert.Equal(t, 50_000, summary.CreditedQuota)

			replay, err := SettleDueInvitationRewards(t.Context(), now, 0)
			require.NoError(t, err)
			assert.Zero(t, replay.Credited)

			// An orphaned inviter must still advance on every engine: MySQL
			// reports zero changed rows for updates that match nothing, which is
			// the case the existence fallback exists for.
			orphan := User{Username: "matrix-orphan-inviter", Password: "password", Status: common.UserStatusEnabled, AffCode: common.GetRandomString(4)}
			require.NoError(t, db.Create(&orphan).Error)
			require.NoError(t, db.Delete(&orphan).Error)
			orphanReward := InvitationReward{
				RedemptionId: 2,
				InviterId:    orphan.Id,
				BasisQuota:   200_000,
				Ratio:        0.05,
				RewardQuota:  10_000,
				Status:       InvitationRewardStatusPending,
				CreatedTime:  now - InvitationRewardObservationWindowSeconds - 1,
				SettleAfter:  now - 1,
			}
			require.NoError(t, db.Create(&orphanReward).Error)

			orphanPass, err := SettleDueInvitationRewards(t.Context(), now, 0)
			require.NoError(t, err)
			assert.Equal(t, 1, orphanPass.Credited)
			assert.Equal(t, 1, orphanPass.MissingInviter)
			assert.Zero(t, orphanPass.CreditedQuota)

			var storedOrphan InvitationReward
			require.NoError(t, db.First(&storedOrphan, orphanReward.Id).Error)
			assert.Equal(t, InvitationRewardStatusCredited, storedOrphan.Status)
			assert.Equal(t, now, storedOrphan.SettledTime)

			var storedReward InvitationReward
			require.NoError(t, db.First(&storedReward, reward.Id).Error)
			assert.Equal(t, InvitationRewardStatusCredited, storedReward.Status)
			assert.Equal(t, now, storedReward.SettledTime)

			var storedInviter User
			require.NoError(t, db.First(&storedInviter, inviter.Id).Error)
			assert.Equal(t, 50_000, storedInviter.AffQuota, "the pool must be credited exactly once")
			assert.Equal(t, 50_000, storedInviter.AffHistoryQuota)
			assert.Zero(t, storedInviter.AffCount)
		})
	}
}
