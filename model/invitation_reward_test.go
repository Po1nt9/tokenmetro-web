package model

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
