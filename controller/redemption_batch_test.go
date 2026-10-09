package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDeleteRedemptionBatch(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver, logDriver gorm.Dialector
			dbType := common.DatabaseTypeSQLite
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
				logDriver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
				logDSN := os.Getenv("TEST_MYSQL_LOG_DSN")
				if logDSN == "" {
					logDSN = dsn
				}
				logDriver = mysql.Open(logDSN)
				dbType = common.DatabaseTypeMySQL
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
				logDSN := os.Getenv("TEST_POSTGRES_LOG_DSN")
				if logDSN == "" {
					logDSN = dsn
				}
				logDriver = postgres.Open(logDSN)
				dbType = common.DatabaseTypePostgreSQL
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)

			logDB, err := gorm.Open(logDriver, &gorm.Config{})
			require.NoError(t, err)
			logSQL, err := logDB.DB()
			require.NoError(t, err)
			logSQL.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, logSQL.Close()) })
			previousDB, previousLogDB := model.DB, model.LOG_DB
			previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
			previousRedis := common.RedisEnabled
			model.DB, model.LOG_DB = db, logDB
			common.SetDatabaseTypes(dbType, dbType)
			common.RedisEnabled = false
			t.Cleanup(func() {
				model.DB, model.LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMain, previousLog)
				common.RedisEnabled = previousRedis
			})
			for _, table := range []any{&model.User{}, &model.Redemption{}} {
				require.False(t, db.Migrator().HasTable(table), "use an empty test database")
				require.NoError(t, db.AutoMigrate(table))
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(table)) })
			}
			require.False(t, logDB.Migrator().HasTable(&model.AuditLog{}), "use an empty test log database")
			require.NoError(t, logDB.AutoMigrate(&model.AuditLog{}))
			t.Cleanup(func() { require.NoError(t, logDB.Migrator().DropTable(&model.AuditLog{})) })
			token := "redemption-audit-test-token"
			admin := model.User{Username: "redemption-audit-admin", Password: "unused", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AccessToken: &token}
			require.NoError(t, db.Create(&admin).Error)
			codes := make([]model.Redemption, 16)
			for index := range codes {
				codes[index] = model.Redemption{Name: "selected", Key: fmt.Sprintf("%032d", index+1), Quota: 100, Status: common.RedemptionCodeStatusEnabled}
			}
			codes[1].Status = common.RedemptionCodeStatusUsed
			codes[15].Name = "unselected"
			codes[15].Status = common.RedemptionCodeStatusDisabled
			require.NoError(t, model.DB.Create(&codes).Error)
			router := gin.New()
			router.Use(middleware.RequestId())
			router.POST("/api/redemption/batch", middleware.AdminAuth(), DeleteRedemptionBatch)

			overLimit := make([]int, 1001)
			for index := range overLimit {
				overLimit[index] = codes[0].Id
			}
			oversized, err := common.Marshal(map[string]any{"ids": overLimit})
			require.NoError(t, err)
			for _, body := range []string{"{}", `{"ids":[]}`, `{"ids":null}`, `{"ids":[0]}`, `{"ids":[1,-1]}`, `{"ids":["1"]}`, "{", string(oversized)} {
				t.Run("invalid_"+body[:min(len(body), 30)], func(t *testing.T) {
					response := httptest.NewRecorder()
					request := httptest.NewRequest(http.MethodPost, "/api/redemption/batch", bytes.NewBufferString(body))
					request.Header.Set("Authorization", "Bearer "+token)
					router.ServeHTTP(response, request)
					var result struct {
						Success bool `json:"success"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
					assert.False(t, result.Success)
					var count int64
					require.NoError(t, model.DB.Model(&model.Redemption{}).Count(&count).Error)
					assert.EqualValues(t, 16, count)
					var events []model.AuditLog
					require.NoError(t, logDB.Where("request_id = ? AND category = ?", response.Header().Get(common.RequestIdKey), model.AuditCategoryOperation).Find(&events).Error)
					require.Len(t, events, 1)
					assert.False(t, events[0].Success)
					assert.Equal(t, "redemption.delete_batch", events[0].Action)
				})
			}
			_, err = model.BatchDeleteRedemptions(nil)
			require.Error(t, err)
			requestedIDs := make([]int, 0, 17)
			for _, code := range codes[:15] {
				requestedIDs = append(requestedIDs, code.Id)
			}
			requestedIDs = append(requestedIDs, codes[0].Id, 999999)
			payload, err := common.Marshal(map[string]any{"ids": requestedIDs})
			require.NoError(t, err)
			for _, expectedCount := range []int64{15, 0} {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/redemption/batch", bytes.NewReader(payload))
				request.Header.Set("Authorization", "Bearer "+token)
				router.ServeHTTP(response, request)
				assert.Equal(t, http.StatusOK, response.Code)
				var result struct {
					Success bool  `json:"success"`
					Data    int64 `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.True(t, result.Success)
				assert.Equal(t, expectedCount, result.Data)
				var events []model.AuditLog
				require.NoError(t, logDB.Where("request_id = ? AND category = ?", response.Header().Get(common.RequestIdKey), model.AuditCategoryOperation).Find(&events).Error)
				require.Len(t, events, 1, "one operation event, without a duplicate single-delete fallback")
				event := events[0]
				assert.Equal(t, "redemption.delete_batch", event.Action)
				assert.Equal(t, fmt.Sprintf("Batch deleted %d redemption codes", expectedCount), event.Content)
				assert.True(t, event.Success)
				assert.Equal(t, admin.Id, event.UserId)
				assert.Equal(t, "/api/redemption/batch", event.Route)
				require.NotNil(t, event.Other.Op)
				encoded, err := common.Marshal(event.Other.Op.Params)
				require.NoError(t, err)
				var params struct {
					Count int64 `json:"count"`
					Total int   `json:"total"`
					IDs   []int `json:"requested_redemption_ids"`
				}
				require.NoError(t, common.Unmarshal(encoded, &params))
				assert.Equal(t, expectedCount, params.Count)
				assert.Equal(t, len(requestedIDs), params.Total)
				assert.Equal(t, requestedIDs, params.IDs)
				encoded, err = common.Marshal(event)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), token)
				for _, code := range codes {
					assert.NotContains(t, string(encoded), code.Key)
				}
			}
			var active []model.Redemption
			require.NoError(t, model.DB.Find(&active).Error)
			require.Len(t, active, 1)
			assert.Equal(t, codes[15], active[0])
			var all []model.Redemption
			require.NoError(t, model.DB.Unscoped().Order("id").Find(&all).Error)
			require.Len(t, all, 16)
			for _, code := range all[:15] {
				assert.True(t, code.DeletedAt.Valid)
			}
			assert.False(t, all[15].DeletedAt.Valid)
		})
	}
}

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
