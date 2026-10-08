package model

import (
	"context"
	"fmt"
	"os"
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
