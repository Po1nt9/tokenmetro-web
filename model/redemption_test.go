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

func TestSearchRedemptionsFiltersAndPaginates(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	})

	now := common.GetTimestamp()
	redemptions := []Redemption{
		{Id: 1, Name: "alpha-active", Key: "00000000000000000000000000000001", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: 0},
		{Id: 2, Name: "alpha-future", Key: "00000000000000000000000000000002", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now + 3600},
		{Id: 3, Name: "alpha-expired", Key: "00000000000000000000000000000003", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now - 10},
		{Id: 4, Name: "beta-disabled", Key: "00000000000000000000000000000004", Status: common.RedemptionCodeStatusDisabled, ExpiredTime: 0},
		{Id: 5, Name: "beta-used", Key: "00000000000000000000000000000005", Status: common.RedemptionCodeStatusUsed, ExpiredTime: 0},
	}
	require.NoError(t, DB.Create(&redemptions).Error)

	tests := []struct {
		name      string
		keyword   string
		status    string
		startIdx  int
		num       int
		wantTotal int64
		wantIds   []int
	}{
		{name: "no filters returns all rows", num: 10, wantTotal: 5, wantIds: []int{5, 4, 3, 2, 1}},
		{name: "keyword filters by name prefix", keyword: "alpha", num: 10, wantTotal: 3, wantIds: []int{3, 2, 1}},
		{name: "enabled status excludes expired rows", status: "1", num: 10, wantTotal: 2, wantIds: []int{2, 1}},
		{name: "expired status returns enabled expired rows", status: "expired", num: 10, wantTotal: 1, wantIds: []int{3}},
		{name: "disabled status", status: "2", num: 10, wantTotal: 1, wantIds: []int{4}},
		{name: "used status", status: "3", num: 10, wantTotal: 1, wantIds: []int{5}},
		{name: "pagination keeps unpaged total", startIdx: 1, num: 2, wantTotal: 5, wantIds: []int{4, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := SearchRedemptions(tt.keyword, tt.status, tt.startIdx, tt.num)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, total)
			gotIds := make([]int, 0, len(rows))
			for _, row := range rows {
				gotIds = append(gotIds, row.Id)
			}
			assert.Equal(t, tt.wantIds, gotIds)
		})
	}
}

func setupRedeemFixture(t *testing.T, quota int) (userId int, key string) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Redemption{}, &User{}, &Log{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM logs")
	})

	user := &User{Username: "redeem-user", Password: "password", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, DB.Create(user).Error)
	key = "10000000000000000000000000000001"
	redemption := &Redemption{
		Name: "redeem-test", Key: key, Status: common.RedemptionCodeStatusEnabled,
		Quota: quota, CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)
	return user.Id, key
}

func TestRedeemCreditsQuotaExactlyOnce(t *testing.T) {
	userId, key := setupRedeemFixture(t, 500)
	result, err := Redeem(key, userId)
	require.NoError(t, err)
	assert.Equal(t, RedemptionOutcomeBalance, result.OutcomeType)
	assert.Equal(t, 500, result.WalletQuota)
	assert.Nil(t, result.RechargeEvent.SaleOrderId, "no linked-shop sale order is available in this system")

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)
	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "name = ?", "redeem-test").Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
	assert.Equal(t, userId, redemption.UsedUserId)
	_, err = Redeem(key, userId)
	require.Error(t, err)
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)
}

func TestRedeemSubscriptionCreatesEntitlementAndRechargeEvent(t *testing.T) {
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
	userId, key := setupRedeemFixture(t, 500)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}))
	plan := &SubscriptionPlan{Title: "Redemption plan", Enabled: true, DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1200, QuotaResetPeriod: SubscriptionResetMonthly, UpgradeGroup: "pro"}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	shopOrderId := "shop-order-test-001"
	require.NoError(t, DB.Model(&Redemption{}).Where("key = ?", key).Updates(map[string]any{
		"outcome_type":         RedemptionOutcomeSubscription,
		"subscription_plan_id": plan.Id,
		"sale_order_id":        shopOrderId,
	}).Error)
	result, err := Redeem(key, userId)
	require.NoError(t, err)
	assert.Equal(t, RedemptionOutcomeSubscription, result.OutcomeType)
	assert.Equal(t, plan.Id, result.Subscription.PlanId)
	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "key = ?", key).Error)
	assert.Equal(t, redemption.Id, result.RechargeEvent.RedemptionId)
	assert.Equal(t, RedemptionOutcomeSubscription, result.RechargeEvent.OutcomeType)
	assert.Zero(t, result.WalletQuota)
	require.NotNil(t, result.RechargeEvent.SaleOrderId)
	assert.Equal(t, shopOrderId, *result.RechargeEvent.SaleOrderId)
	var user User
	require.NoError(t, DB.First(&user, userId).Error)
	assert.Zero(t, user.Quota)
	assert.Equal(t, "pro", user.Group)
	var subscriptions []UserSubscription
	require.NoError(t, DB.Where("user_id = ?", userId).Find(&subscriptions).Error)
	require.Len(t, subscriptions, 1)
	assert.Equal(t, plan.Id, subscriptions[0].PlanId)
	assert.Equal(t, "redemption", subscriptions[0].Source)
}

func TestRedeemSubscriptionFailureLeavesCodeAvailable(t *testing.T) {
	userId, key := setupRedeemFixture(t, 100)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}))
	plan := &SubscriptionPlan{Title: "Disabled plan", DurationUnit: SubscriptionDurationMonth, DurationValue: 1}
	require.NoError(t, DB.Create(plan).Error)
	require.NoError(t, DB.Model(plan).Update("enabled", false).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	require.NoError(t, DB.Model(&Redemption{}).Where("key = ?", key).Updates(map[string]any{"outcome_type": RedemptionOutcomeSubscription, "subscription_plan_id": plan.Id}).Error)
	_, err := Redeem(key, userId)
	require.ErrorIs(t, err, ErrRedeemFailed)
	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "key = ?", key).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
	var subscriptions int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", userId).Count(&subscriptions).Error)
	assert.Zero(t, subscriptions)
}

func TestRedeemSubscriptionConcurrentSingleSuccess(t *testing.T) {
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
	sqlDB.SetMaxOpenConns(6)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		initCol()
		_ = sqlDB.Close()
	})
	userId, key := setupRedeemFixture(t, 100)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}))
	plan := &SubscriptionPlan{Title: "Single purchase", Enabled: true, DurationUnit: SubscriptionDurationMonth, DurationValue: 1, MaxPurchasePerUser: 1}
	require.NoError(t, DB.Create(plan).Error)
	require.NoError(t, DB.Model(&Redemption{}).Where("key = ?", key).Updates(map[string]any{"outcome_type": RedemptionOutcomeSubscription, "subscription_plan_id": plan.Id}).Error)
	const attempts = 4
	successes := make([]bool, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := range attempts {
		go func(index int) {
			defer wg.Done()
			_, err := Redeem(key, userId)
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
	assert.Equal(t, 1, successCount)
	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", userId, plan.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestRedeemDifferentCodesRespectSubscriptionPurchaseLimitConcurrently(t *testing.T) {
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
	sqlDB.SetMaxOpenConns(6)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		initCol()
		_ = sqlDB.Close()
	})

	userId, firstKey := setupRedeemFixture(t, 100)
	secondKey := "10000000000000000000000000000002"
	require.NoError(t, DB.Create(&Redemption{
		Name: "redeem-test-second", Key: secondKey, Status: common.RedemptionCodeStatusEnabled,
		Quota: 100, CreatedTime: common.GetTimestamp(),
	}).Error)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}))
	plan := &SubscriptionPlan{Title: "One purchase", Enabled: true, DurationUnit: SubscriptionDurationMonth, DurationValue: 1, MaxPurchasePerUser: 1}
	require.NoError(t, DB.Create(plan).Error)
	require.NoError(t, DB.Model(&Redemption{}).Where("key IN ?", []string{firstKey, secondKey}).Updates(map[string]any{
		"outcome_type": RedemptionOutcomeSubscription, "subscription_plan_id": plan.Id,
	}).Error)

	start := make(chan struct{})
	errors := make([]error, 2)
	var wg sync.WaitGroup
	for i, key := range []string{firstKey, secondKey} {
		wg.Add(1)
		go func(index int, code string) {
			defer wg.Done()
			<-start
			_, errors[index] = Redeem(code, userId)
		}(i, key)
	}
	close(start)
	wg.Wait()

	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", userId, plan.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count, "different codes must not bypass the per-user purchase limit")
	var usedCount int64
	require.NoError(t, DB.Model(&Redemption{}).Where("used_user_id = ? AND status = ?", userId, common.RedemptionCodeStatusUsed).Count(&usedCount).Error)
	assert.EqualValues(t, 1, usedCount, "only the redemption that created the subscription may be consumed")
}

func TestUpdateRedemptionAllowsUnusedOutcomeChange(t *testing.T) {
	_, key := setupRedeemFixture(t, 100)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPlan{}))
	plan := &SubscriptionPlan{Title: "Editable plan", Enabled: true, DurationUnit: SubscriptionDurationMonth, DurationValue: 1}
	require.NoError(t, DB.Create(plan).Error)
	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "key = ?", key).Error)
	redemption.OutcomeType = RedemptionOutcomeSubscription
	redemption.SubscriptionPlanId = plan.Id
	redemption.Quota = 0
	require.NoError(t, redemption.Update())
	var updated Redemption
	require.NoError(t, DB.First(&updated, "key = ?", key).Error)
	assert.Equal(t, RedemptionOutcomeSubscription, updated.EffectiveOutcomeType())
	assert.Equal(t, plan.Id, updated.SubscriptionPlanId)
}

func TestUpdateRedemptionRejectsUsedOutcomeChange(t *testing.T) {
	userId, key := setupRedeemFixture(t, 100)
	_, err := Redeem(key, userId)
	require.NoError(t, err)
	redemption, err := GetRedemptionById(func() int {
		var stored Redemption
		require.NoError(t, DB.First(&stored, "key = ?", key).Error)
		return stored.Id
	}())
	require.NoError(t, err)
	redemption.Quota = 200
	err = redemption.Update()
	require.Error(t, err)
	var stored Redemption
	require.NoError(t, DB.First(&stored, "key = ?", key).Error)
	assert.Equal(t, 100, stored.Quota)
}

func TestRedemptionSaleOrderReferenceIsNullableAndMigratesIdempotently(t *testing.T) {
	db := DB
	legacy := struct {
		Id          int    `gorm:"primaryKey"`
		Key         string `gorm:"type:char(32);uniqueIndex"`
		Status      int
		Quota       int
		Name        string
		UserId      int
		CreatedTime int64
	}{Key: "10000000000000000000000000000010", Status: common.RedemptionCodeStatusEnabled, Quota: 10, Name: "sale-reference"}
	require.NoError(t, db.Table("sale_reference_redemptions").AutoMigrate(&legacy))
	require.NoError(t, db.Table("sale_reference_redemptions").Create(&legacy).Error)
	require.NoError(t, db.Table("sale_reference_redemptions").AutoMigrate(&Redemption{}))
	require.NoError(t, db.Table("sale_reference_redemptions").AutoMigrate(&Redemption{}))

	var stored Redemption
	require.NoError(t, db.Table("sale_reference_redemptions").First(&stored, legacy.Id).Error)
	assert.Nil(t, stored.SaleOrderId, "legacy rows have no external sale fact")

	orderId := "shop-order-2026-10-08-001"
	require.NoError(t, db.Table("sale_reference_redemptions").Model(&stored).Select("sale_order_id").Updates(map[string]any{
		"sale_order_id": orderId,
	}).Error)
	var updated Redemption
	require.NoError(t, db.Table("sale_reference_redemptions").First(&updated, legacy.Id).Error)
	require.NotNil(t, updated.SaleOrderId)
	assert.Equal(t, orderId, *updated.SaleOrderId)
}

func TestRedemptionOutcomeMigrationPreservesLegacyRowsAndIsIdempotent(t *testing.T) {
	db := DB
	legacy := struct {
		Id     int    `gorm:"primaryKey"`
		Key    string `gorm:"type:char(32);uniqueIndex"`
		Status int
		Quota  int
	}{Key: "10000000000000000000000000000009", Status: common.RedemptionCodeStatusEnabled, Quota: 500}
	require.NoError(t, db.Table("legacy_redemptions").AutoMigrate(&legacy))
	require.NoError(t, db.Table("legacy_redemptions").Create(&legacy).Error)
	require.NoError(t, db.Table("legacy_redemptions").AutoMigrate(&Redemption{}))
	require.NoError(t, db.Table("legacy_redemptions").AutoMigrate(&Redemption{}))
	var upgraded []Redemption
	require.NoError(t, db.Table("legacy_redemptions").Find(&upgraded).Error)
	require.Len(t, upgraded, 1)
	assert.Equal(t, RedemptionOutcomeBalance, upgraded[0].EffectiveOutcomeType())
	assert.Equal(t, 500, upgraded[0].Quota)
}

func TestRedeemRejectsWalletOverflow(t *testing.T) {
	userId, key := setupRedeemFixture(t, 11)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userId).Update("quota", common.MaxWalletQuota-10).Error)
	_, err := Redeem(key, userId)
	require.ErrorIs(t, err, ErrRedeemFailed)
	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, common.MaxWalletQuota-10, user.Quota)
	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "key = ?", key).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
}

func TestRedemptionQuotaRejectsWalletOverflow(t *testing.T) {
	setupRedeemFixture(t, 500)
	redemption := &Redemption{Name: "overflow-redemption", Key: "10000000000000000000000000000002", Status: common.RedemptionCodeStatusEnabled, Quota: common.MaxWalletQuota + 1, CreatedTime: common.GetTimestamp()}
	require.Error(t, redemption.Insert())
}

// Exactly one concurrent redemption may succeed and credit quota.
func TestRedeemConcurrentSingleSuccess(t *testing.T) {
	userId, key := setupRedeemFixture(t, 300)
	const goroutines = 5
	successes := make([]bool, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			if _, err := Redeem(key, userId); err == nil {
				successes[idx] = true
			}
		}(i)
	}
	wg.Wait()
	successCount := 0
	for _, ok := range successes {
		if ok {
			successCount++
		}
	}
	assert.Equal(t, 1, successCount, "exactly one concurrent redeem should succeed")
	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 300, user.Quota, "quota must be credited exactly once")
}
