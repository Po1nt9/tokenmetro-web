package controller

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupInvitationRewardSettlementTaskFixture mirrors the model suite's
// single-connection SQLite fixture: a settlement transaction that reaches for
// the global DB instead of its own tx would deadlock here instead of in
// production.
func setupInvitationRewardSettlementTaskFixture(t *testing.T) int {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.InvitationReward{}, &model.SystemTask{}, &model.SystemTaskLock{},
	))
	inviter := &model.User{
		Username: "settlement-task-inviter",
		Password: "password",
		Status:   common.UserStatusEnabled,
		AffCode:  common.GetRandomString(4),
	}
	require.NoError(t, db.Create(inviter).Error)
	return inviter.Id
}

// startClaimedInvitationRewardSettlementTask creates the pending row the
// scheduler would create and claims it the way the runner does, so the handler
// can finish a run with its lease intact.
func startClaimedInvitationRewardSettlementTask(t *testing.T) (*model.SystemTask, string) {
	t.Helper()
	task, err := model.CreateSystemTask(model.SystemTaskTypeInvitationRewardSettlement, nil, nil)
	require.NoError(t, err)
	runnerID := "invitation-reward-settlement-test-runner"
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, runnerID, common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, claimed)
	return claimed, runnerID
}

func TestInvitationRewardSettlementTaskRunCreditsDueRewardAndRecordsSuccessfulRun(t *testing.T) {
	inviterId := setupInvitationRewardSettlementTaskFixture(t)
	now := common.GetTimestamp()
	reward := &model.InvitationReward{
		RedemptionId: 1,
		InviterId:    inviterId,
		BasisQuota:   1_000_000,
		Ratio:        0.05,
		RewardQuota:  50_000,
		Status:       model.InvitationRewardStatusPending,
		CreatedTime:  now - model.InvitationRewardObservationWindowSeconds - 1,
		SettleAfter:  now - 1,
	}
	require.NoError(t, model.DB.Create(reward).Error)

	handler := invitationRewardSettlementHandler{}
	assert.Equal(t, model.SystemTaskTypeInvitationRewardSettlement, handler.Type())
	assert.True(t, handler.Enabled())
	assert.Equal(t, time.Hour, handler.Interval())

	task, runnerID := startClaimedInvitationRewardSettlementTask(t)
	handler.Run(context.Background(), task, runnerID)

	stored, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, model.SystemTaskStatusSucceeded, stored.Status)
	assert.Empty(t, stored.Error)
	assert.Contains(t, stored.Result, `"credited":1`)
	assert.Contains(t, stored.Result, `"credited_quota":50000`)

	var storedReward model.InvitationReward
	require.NoError(t, model.DB.First(&storedReward, reward.Id).Error)
	assert.Equal(t, model.InvitationRewardStatusCredited, storedReward.Status)

	var inviter model.User
	require.NoError(t, model.DB.First(&inviter, inviterId).Error)
	assert.Equal(t, 50_000, inviter.AffQuota)
	assert.Equal(t, 50_000, inviter.AffHistoryQuota)
}

func TestInvitationRewardSettlementTaskRunRecordsFailedRunOnError(t *testing.T) {
	setupInvitationRewardSettlementTaskFixture(t)
	// Without the ledger table the pass cannot scan, which must surface as a
	// failed run instead of a silent success.
	require.NoError(t, model.DB.Migrator().DropTable(&model.InvitationReward{}))
	task, runnerID := startClaimedInvitationRewardSettlementTask(t)

	invitationRewardSettlementHandler{}.Run(context.Background(), task, runnerID)

	stored, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, model.SystemTaskStatusFailed, stored.Status)
	assert.NotEmpty(t, stored.Error)
}
