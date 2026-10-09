package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func setupRedemptionPreviewAPI(t *testing.T) (*gin.Engine, *model.User, string) {
	t.Helper()
	// The redemption model queries use the shared dialect column names
	// (model.commonKeyCol). Initialize them before the fixture installs its own
	// in-memory database, the same way model_list_test.go does.
	initModelListColumnNames(t)
	require.NoError(t, i18n.Init())
	user, token := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Redemption{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.Log{}))
	previousCompliance := *operation_setting.GetPaymentSetting()
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() { *operation_setting.GetPaymentSetting() = previousCompliance })

	const code = "10000000000000000000000000000071"
	require.NoError(t, model.DB.Create(&model.Redemption{
		Name: "api-preview-test", Key: code, Status: common.RedemptionCodeStatusEnabled,
		Quota: 750, CreatedTime: common.GetTimestamp(),
	}).Error)
	router := gin.New()
	router.POST("/api/user/topup/preview", middleware.UserAuth(), PreviewTopUp)
	router.POST("/api/user/topup", middleware.UserAuth(), TopUp)
	return router, user, token
}

func redemptionAPIRequest(router http.Handler, path, token, code string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"key":"`+code+`"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestRedemptionPreviewAPIRequiresAuthenticationAndReturnsSummary(t *testing.T) {
	router, _, token := setupRedemptionPreviewAPI(t)
	unauthenticated := redemptionAPIRequest(router, "/api/user/topup/preview", "", "missing-code")
	assert.NotEqual(t, http.StatusOK, unauthenticated.Code)

	response := redemptionAPIRequest(router, "/api/user/topup/preview", token, "10000000000000000000000000000071")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"outcome_type":"balance"`)
	assert.Contains(t, response.Body.String(), `"wallet_quota":750`)
	assert.NotContains(t, response.Body.String(), "10000000000000000000000000000071")
	assert.NotContains(t, response.Body.String(), "status")
}

func TestRedemptionPreviewAPIUsesGenericFailureForUnavailableCode(t *testing.T) {
	router, _, token := setupRedemptionPreviewAPI(t)
	for _, state := range []struct {
		name   string
		status int
		expiry int64
	}{
		{name: "invalid", status: -1},
		{name: "expired", status: common.RedemptionCodeStatusEnabled, expiry: common.GetTimestamp() - 1},
		{name: "disabled", status: common.RedemptionCodeStatusDisabled},
		{name: "used", status: common.RedemptionCodeStatusUsed},
	} {
		t.Run(state.name, func(t *testing.T) {
			testCode := "10000000000000000000000000000071"
			if state.status == -1 {
				testCode = "missing-code"
			} else {
				require.NoError(t, model.DB.Model(&model.Redemption{}).Where("`key` = ?", testCode).Updates(map[string]any{"status": state.status, "expired_time": state.expiry}).Error)
			}
			response := redemptionAPIRequest(router, "/api/user/topup/preview", token, testCode)
			require.Equal(t, http.StatusOK, response.Code)
			assert.Contains(t, response.Body.String(), `"success":false`)
			assert.Contains(t, response.Body.String(), i18n.Translate(i18n.DefaultLang, i18n.MsgRedeemFailed))
			assert.NotContains(t, response.Body.String(), state.name)
		})
	}
}

func TestRedemptionExecuteAfterPreviewIsRevalidated(t *testing.T) {
	router, user, token := setupRedemptionPreviewAPI(t)
	preview := redemptionAPIRequest(router, "/api/user/topup/preview", token, "10000000000000000000000000000071")
	require.Equal(t, http.StatusOK, preview.Code)
	_, err := model.Redeem("10000000000000000000000000000071", user.Id)
	require.NoError(t, err)

	confirmation := redemptionAPIRequest(router, "/api/user/topup", token, "10000000000000000000000000000071")
	require.Equal(t, http.StatusOK, confirmation.Code)
	assert.Contains(t, confirmation.Body.String(), `"success":false`)
	assert.Contains(t, confirmation.Body.String(), i18n.Translate(i18n.DefaultLang, i18n.MsgRedeemFailed))
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, 750, stored.Quota)
}

func TestRedemptionExecuteReturnsBareNumberForBalanceCodes(t *testing.T) {
	router, user, token := setupRedemptionPreviewAPI(t)
	response := redemptionAPIRequest(router, "/api/user/topup", token, "10000000000000000000000000000071")
	require.Equal(t, http.StatusOK, response.Code)

	var payload struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.NotEmpty(t, payload.Data)
	// Bundles built before the fork and third-party scripts read `data` as a
	// bare number, so a balance code must not wrap the quota in an object.
	assert.NotEqual(t, byte('{'), payload.Data[0])
	assert.NotContains(t, string(payload.Data), "outcome_type")
	var quota float64
	require.NoError(t, common.Unmarshal(payload.Data, &quota))
	assert.Equal(t, float64(750), quota)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, 750, stored.Quota)
}

func TestRedemptionExecuteKeepsObjectForSubscriptionCodes(t *testing.T) {
	router, user, token := setupRedemptionPreviewAPI(t)
	plan := &model.SubscriptionPlan{
		Title: "Redemption plan", DurationUnit: model.SubscriptionDurationMonth,
		DurationValue: 1, TotalAmount: 500000,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	const code = "10000000000000000000000000000072"
	require.NoError(t, model.DB.Create(&model.Redemption{
		Name: "api-subscription-test", Key: code, Status: common.RedemptionCodeStatusEnabled,
		Quota: 0, OutcomeType: model.RedemptionOutcomeSubscription, SubscriptionPlanId: plan.Id,
		CreatedTime: common.GetTimestamp(),
	}).Error)

	response := redemptionAPIRequest(router, "/api/user/topup", token, code)
	require.Equal(t, http.StatusOK, response.Code)

	var payload struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.NotEmpty(t, payload.Data)
	assert.Equal(t, byte('{'), payload.Data[0])

	var outcome struct {
		OutcomeType  string `json:"outcome_type"`
		Subscription *struct {
			Id     int `json:"id"`
			PlanId int `json:"plan_id"`
		} `json:"subscription"`
		RechargeEvent struct {
			RedemptionId   int    `json:"redemption_id"`
			UserId         int    `json:"user_id"`
			OutcomeType    string `json:"outcome_type"`
			PlanId         int    `json:"plan_id"`
			SubscriptionId int    `json:"subscription_id"`
		} `json:"recharge_event"`
	}
	require.NoError(t, common.Unmarshal(payload.Data, &outcome))
	assert.Equal(t, string(model.RedemptionOutcomeSubscription), outcome.OutcomeType)
	require.NotNil(t, outcome.Subscription)
	assert.Equal(t, plan.Id, outcome.Subscription.PlanId)
	assert.Equal(t, user.Id, outcome.RechargeEvent.UserId)
	assert.Equal(t, string(model.RedemptionOutcomeSubscription), outcome.RechargeEvent.OutcomeType)
	assert.Equal(t, plan.Id, outcome.RechargeEvent.PlanId)
	assert.Equal(t, outcome.Subscription.Id, outcome.RechargeEvent.SubscriptionId)
	assert.NotZero(t, outcome.RechargeEvent.RedemptionId)
}

func TestAddRedemptionRejectsUnavailableSubscriptionPlan(t *testing.T) {
	_, _, token := setupRedemptionPreviewAPI(t)
	disabledPlan := &model.SubscriptionPlan{Title: "Disabled plan", DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1}
	require.NoError(t, model.DB.Create(disabledPlan).Error)
	require.NoError(t, model.DB.Model(disabledPlan).Update("enabled", false).Error)

	router := gin.New()
	router.POST("/api/redemption", middleware.AdminAuth(), AddRedemption)
	for _, testCase := range []struct {
		name        string
		planId      int
		wantMessage string
	}{
		{name: "plan missing", planId: 999999, wantMessage: "subscription plan not found"},
		{name: "plan disabled", planId: disabledPlan.Id, wantMessage: "subscription plan is disabled"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			payload, err := common.Marshal(map[string]any{
				"name":                 "subscription batch",
				"count":                1,
				"quota":                0,
				"outcome_type":         model.RedemptionOutcomeSubscription,
				"subscription_plan_id": testCase.planId,
			})
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, "/api/redemption", bytes.NewReader(payload))
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			assert.Contains(t, response.Body.String(), `"success":false`)
			assert.Contains(t, response.Body.String(), testCase.wantMessage)
		})
	}
}
