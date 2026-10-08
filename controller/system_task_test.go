package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupSystemTaskDB installs a single-connection in-memory SQLite database for
// the system task tables. The single connection is a deadlock detector: a
// handler that reaches for the global DB inside a transaction instead of using
// its own tx would hang here instead of in production.
func setupSystemTaskDB(t *testing.T) {
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
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.InvitationReward{}, &model.SystemTask{}, &model.SystemTaskLock{}))
}

func TestSystemTaskListFiltersAndPaginationResponse(t *testing.T) {
	setupSystemTaskDB(t)
	require.NoError(t, model.DB.Create(&[]model.SystemTask{
		{TaskID: "older", Type: model.SystemTaskTypeModelUpdate, Status: model.SystemTaskStatusFailed},
		{TaskID: "newer", Type: model.SystemTaskTypeModelUpdate, Status: model.SystemTaskStatusFailed},
		{TaskID: "other-status", Type: model.SystemTaskTypeModelUpdate, Status: model.SystemTaskStatusSucceeded},
		{TaskID: "other-type", Type: model.SystemTaskTypeChannelTest, Status: model.SystemTaskStatusFailed},
	}).Error)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/system-task/list?scope=history&type=model_update&status=failed&offset=1&limit=1", nil)
	ListSystemTasks(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool                       `json:"success"`
		Total   int64                      `json:"total"`
		Data    []model.SystemTaskResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.EqualValues(t, 2, response.Total)
	require.Len(t, response.Data, 1)
	assert.Equal(t, "older", response.Data[0].TaskID)
}

func TestSystemTaskInvalidFiltersAreRejected(t *testing.T) {
	for _, tc := range []struct {
		query   string
		handler gin.HandlerFunc
	}{
		{"?scope=invalid", ListSystemTasks},
		{"?status=unknown", ListSystemTasks},
		{"?offset=-1", ListSystemTasks},
		{"?limit=invalid", ListSystemTasks},
		{"?status=unknown", DeleteSystemTaskHistory},
		{"?scope=invalid", DeleteSystemTaskHistory},
	} {
		t.Run(tc.query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/"+tc.query, nil)
			tc.handler(c)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.JSONEq(t, `{"success":false,"message":"invalid system task filters"}`, recorder.Body.String())
		})
	}
}

// setupInvitationRewardSettlementTaskFixture reuses the single-connection
// SQLite fixture: a settlement transaction that reaches for the global DB
// instead of its own tx would deadlock here instead of in production.
func setupInvitationRewardSettlementTaskFixture(t *testing.T) int {
	t.Helper()
	setupSystemTaskDB(t)
	inviter := &model.User{
		Username: "settlement-task-inviter",
		Password: "password",
		Status:   common.UserStatusEnabled,
		AffCode:  common.GetRandomString(4),
	}
	require.NoError(t, model.DB.Create(inviter).Error)
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
