package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRedemptionRewardControlAPI installs the three admin routes this ticket
// owns on a shared in-memory database, together with one admin and one common
// user so the authorization gate can be exercised.
func setupRedemptionRewardControlAPI(t *testing.T) (*gin.Engine, *model.User, string) {
	t.Helper()
	// The redemption model queries use the shared dialect column name helper, so
	// initialize it before the fixture installs its own database.
	initModelListColumnNames(t)
	require.NoError(t, i18n.Init())
	admin, adminToken := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(
		&model.Redemption{}, &model.InvitationReward{}, &model.Option{},
	))
	previousCompliance := *operation_setting.GetPaymentSetting()
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() { *operation_setting.GetPaymentSetting() = previousCompliance })

	commonToken := "redemption-reward-common-token"
	require.NoError(t, model.DB.Create(&model.User{
		Username: "reward-common", Password: "placeholder", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AccessToken: &commonToken, AuthVersion: 1,
		AffCode: common.GetRandomString(4),
	}).Error)

	router := gin.New()
	router.Use(middleware.RequestId())
	router.POST("/api/redemption", middleware.AdminAuth(), AddRedemption)
	router.PUT("/api/redemption", middleware.AdminAuth(), UpdateRedemption)
	router.POST("/api/redemption/reward/void", middleware.AdminAuth(), VoidRedemptionReward)
	return router, admin, adminToken
}

func redemptionRewardRequest(t *testing.T, router http.Handler, method, path, token, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &decoded))
	return response, decoded
}

// redemptionRewardAuditEvents returns the operation audit events the middleware
// recorded for one request. A handler that logs its own event must not also
// trigger the generic fallback, so the length assertion below is part of the
// contract.
func redemptionRewardAuditEvents(t *testing.T, response *httptest.ResponseRecorder) []model.AuditLog {
	t.Helper()
	var events []model.AuditLog
	require.NoError(t, model.LOG_DB.
		Where("request_id = ? AND category = ?", response.Header().Get(common.RequestIdKey), model.AuditCategoryOperation).
		Find(&events).Error)
	return events
}

type redemptionRewardAuditParams struct {
	RewardEligible *bool `json:"reward_eligible"`
	RedemptionId   int   `json:"redemption_id"`
}

func decodeRedemptionRewardAuditParams(t *testing.T, event model.AuditLog) redemptionRewardAuditParams {
	t.Helper()
	require.NotNil(t, event.Other.Op)
	encoded, err := common.Marshal(event.Other.Op.Params)
	require.NoError(t, err)
	var params redemptionRewardAuditParams
	require.NoError(t, common.Unmarshal(encoded, &params))
	return params
}

func TestRedemptionRewardEligibilityPersistsThroughCreateAndUpdate(t *testing.T) {
	router, _, token := setupRedemptionRewardControlAPI(t)

	// Opting out must survive the GORM default substitution on insert: the
	// column defaults to true, so a zero-value bool needs the compensating
	// write and the read-back must still show "not reward-eligible".
	response, result := redemptionRewardRequest(t, router, http.MethodPost, "/api/redemption", token,
		`{"name":"granted batch","count":2,"quota":1000,"outcome_type":"balance","reward_eligible":false}`)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, true, result["success"])

	var optedOut []model.Redemption
	require.NoError(t, model.DB.Where("name = ?", "granted batch").Order("id").Find(&optedOut).Error)
	require.Len(t, optedOut, 2)
	for _, code := range optedOut {
		assert.False(t, code.RewardEligible)
		reloaded, err := model.GetRedemptionById(code.Id)
		require.NoError(t, err)
		assert.False(t, reloaded.RewardEligible, "the edit form must read the opt-out back")
	}

	// Omitting the field keeps the batch reward-eligible (the safe default).
	_, result = redemptionRewardRequest(t, router, http.MethodPost, "/api/redemption", token,
		`{"name":"sold batch","count":1,"quota":1000,"outcome_type":"balance"}`)
	require.Equal(t, true, result["success"])
	var sold model.Redemption
	require.NoError(t, model.DB.First(&sold, "name = ?", "sold batch").Error)
	assert.True(t, sold.RewardEligible)

	// An update that omits the field must not flip an existing opt-out back on.
	_, result = redemptionRewardRequest(t, router, http.MethodPut, "/api/redemption", token,
		fmt.Sprintf(`{"id":%d,"name":"granted batch renamed","quota":1000,"outcome_type":"balance"}`, optedOut[0].Id))
	require.Equal(t, true, result["success"])
	reloaded, err := model.GetRedemptionById(optedOut[0].Id)
	require.NoError(t, err)
	assert.Equal(t, "granted batch renamed", reloaded.Name)
	assert.False(t, reloaded.RewardEligible)

	// Explicitly re-enabling the batch writes true back through the same path.
	_, result = redemptionRewardRequest(t, router, http.MethodPut, "/api/redemption", token,
		fmt.Sprintf(`{"id":%d,"name":"granted batch","quota":1000,"outcome_type":"balance","reward_eligible":true}`, optedOut[0].Id))
	require.Equal(t, true, result["success"])
	reloaded, err = model.GetRedemptionById(optedOut[0].Id)
	require.NoError(t, err)
	assert.True(t, reloaded.RewardEligible)
}

func TestRedemptionRewardEligibilityIsAuditedAndImmutableOnceUsed(t *testing.T) {
	router, admin, token := setupRedemptionRewardControlAPI(t)

	response, result := redemptionRewardRequest(t, router, http.MethodPost, "/api/redemption", token,
		`{"name":"granted audit","count":1,"quota":500,"outcome_type":"balance","reward_eligible":false}`)
	require.Equal(t, true, result["success"])
	var created model.Redemption
	require.NoError(t, model.DB.First(&created, "name = ?", "granted audit").Error)

	events := redemptionRewardAuditEvents(t, response)
	require.Len(t, events, 1)
	assert.Equal(t, "redemption.create", events[0].Action)
	assert.Equal(t, admin.Id, events[0].UserId)
	require.NotNil(t, decodeRedemptionRewardAuditParams(t, events[0]).RewardEligible)
	assert.False(t, *decodeRedemptionRewardAuditParams(t, events[0]).RewardEligible)

	response, result = redemptionRewardRequest(t, router, http.MethodPut, "/api/redemption", token,
		fmt.Sprintf(`{"id":%d,"name":"granted audit","quota":500,"outcome_type":"balance","reward_eligible":true}`, created.Id))
	require.Equal(t, true, result["success"])
	events = redemptionRewardAuditEvents(t, response)
	require.Len(t, events, 1)
	assert.Equal(t, "redemption.reward_eligible_update", events[0].Action)
	require.NotNil(t, decodeRedemptionRewardAuditParams(t, events[0]).RewardEligible)
	assert.True(t, *decodeRedemptionRewardAuditParams(t, events[0]).RewardEligible)

	// A used code stays immutable, so its reward eligibility cannot move either.
	require.NoError(t, model.DB.Model(&model.Redemption{}).Where("id = ?", created.Id).
		Update("status", common.RedemptionCodeStatusUsed).Error)
	response, result = redemptionRewardRequest(t, router, http.MethodPut, "/api/redemption", token,
		fmt.Sprintf(`{"id":%d,"name":"granted audit","quota":500,"outcome_type":"balance","reward_eligible":false}`, created.Id))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, false, result["success"])
	attempted := redemptionRewardAuditEvents(t, response)
	for _, event := range attempted {
		assert.NotEqual(t, "redemption.reward_eligible_update", event.Action, "a refused update must not claim the flag changed")
	}
	reloaded, err := model.GetRedemptionById(created.Id)
	require.NoError(t, err)
	assert.True(t, reloaded.RewardEligible, "a used code keeps the eligibility it was redeemed with")
}

func TestVoidRedemptionRewardTransitions(t *testing.T) {
	router, admin, token := setupRedemptionRewardControlAPI(t)
	inviter := &model.User{
		Username: "void-inviter", Password: "placeholder", Status: common.UserStatusEnabled,
		Group: "default", AffCode: common.GetRandomString(4),
	}
	require.NoError(t, model.DB.Create(inviter).Error)

	createRedemption := func(name string) *model.Redemption {
		redemption := &model.Redemption{
			Name: name, Key: common.GetUUID(), Status: common.RedemptionCodeStatusUsed,
			Quota: 500, CreatedTime: common.GetTimestamp(),
		}
		require.NoError(t, model.DB.Create(redemption).Error)
		return redemption
	}
	createReward := func(redemptionId int, status string) {
		reward := &model.InvitationReward{
			RedemptionId: redemptionId, InviterId: inviter.Id, InviteeId: inviter.Id,
			BasisQuota: 500, Ratio: 0.05, RewardQuota: 25, Status: status,
			CreatedTime: common.GetTimestamp() - 1, SettleAfter: common.GetTimestamp() - 1,
		}
		require.NoError(t, model.DB.Create(reward).Error)
	}

	t.Run("pending reward is voided and never settles", func(t *testing.T) {
		redemption := createRedemption("void-pending")
		createReward(redemption.Id, model.InvitationRewardStatusPending)

		response, result := redemptionRewardRequest(t, router, http.MethodPost, "/api/redemption/reward/void", token,
			fmt.Sprintf(`{"redemption_id":%d}`, redemption.Id))
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, true, result["success"])

		var voided model.InvitationReward
		require.NoError(t, model.DB.First(&voided, "redemption_id = ?", redemption.Id).Error)
		assert.Equal(t, model.InvitationRewardStatusVoided, voided.Status)
		assert.NotZero(t, voided.VoidedTime)

		// The hourly settlement pass only advances pending rows, so a voided
		// reward must not reach the inviter's pool.
		summary, err := model.SettleDueInvitationRewards(context.Background(), common.GetTimestamp(), 0)
		require.NoError(t, err)
		assert.Zero(t, summary.Credited)
		var inviterRow model.User
		require.NoError(t, model.DB.First(&inviterRow, inviter.Id).Error)
		assert.Zero(t, inviterRow.AffQuota)
		require.NoError(t, model.DB.First(&voided, "redemption_id = ?", redemption.Id).Error)
		assert.Equal(t, model.InvitationRewardStatusVoided, voided.Status)

		events := redemptionRewardAuditEvents(t, response)
		require.Len(t, events, 1)
		assert.Equal(t, "redemption.reward_void", events[0].Action)
		assert.Equal(t, admin.Id, events[0].UserId)
		assert.Equal(t, redemption.Id, decodeRedemptionRewardAuditParams(t, events[0]).RedemptionId)

		// Voiding twice reports the state the row already reached.
		_, result = redemptionRewardRequest(t, router, http.MethodPost, "/api/redemption/reward/void", token,
			fmt.Sprintf(`{"redemption_id":%d}`, redemption.Id))
		assert.Equal(t, false, result["success"])
		assert.Contains(t, result["message"], "already been voided")
	})

	t.Run("credited reward is refused with an explicit message", func(t *testing.T) {
		redemption := createRedemption("void-credited")
		createReward(redemption.Id, model.InvitationRewardStatusCredited)

		response, result := redemptionRewardRequest(t, router, http.MethodPost, "/api/redemption/reward/void", token,
			fmt.Sprintf(`{"redemption_id":%d}`, redemption.Id))
		require.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, false, result["success"])
		assert.Contains(t, result["message"], "has been settled and cannot be reversed")

		var rewarded model.InvitationReward
		require.NoError(t, model.DB.First(&rewarded, "redemption_id = ?", redemption.Id).Error)
		assert.Equal(t, model.InvitationRewardStatusCredited, rewarded.Status)
		assert.Zero(t, rewarded.VoidedTime)
	})

	t.Run("code without a reward reports that clearly", func(t *testing.T) {
		redemption := createRedemption("void-missing")
		response, result := redemptionRewardRequest(t, router, http.MethodPost, "/api/redemption/reward/void", token,
			fmt.Sprintf(`{"redemption_id":%d}`, redemption.Id))
		require.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, false, result["success"])
		assert.Contains(t, result["message"], "no invitation reward exists")

		_, result = redemptionRewardRequest(t, router, http.MethodPost, "/api/redemption/reward/void", token, `{}`)
		assert.Equal(t, false, result["success"])
	})

	t.Run("non-admin callers are rejected without touching the reward", func(t *testing.T) {
		redemption := createRedemption("void-forbidden")
		createReward(redemption.Id, model.InvitationRewardStatusPending)

		response, result := redemptionRewardRequest(t, router, http.MethodPost, "/api/redemption/reward/void",
			"redemption-reward-common-token", fmt.Sprintf(`{"redemption_id":%d}`, redemption.Id))
		require.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, false, result["success"])

		var rewarded model.InvitationReward
		require.NoError(t, model.DB.First(&rewarded, "redemption_id = ?", redemption.Id).Error)
		assert.Equal(t, model.InvitationRewardStatusPending, rewarded.Status)
	})
}
