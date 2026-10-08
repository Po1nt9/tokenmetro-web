package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRedemptionPreviewAPI(t *testing.T) (*gin.Engine, *model.User, string) {
	t.Helper()
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
			assert.Contains(t, response.Body.String(), "redeem.failed")
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
	assert.Contains(t, confirmation.Body.String(), "兑换失败")
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, 750, stored.Quota)
}
