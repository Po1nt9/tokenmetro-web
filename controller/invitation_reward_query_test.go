package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type affiliateRewardsEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		PendingQuota int `json:"pending_quota"`
		Rewards      []struct {
			Id              int    `json:"id"`
			CreatedTime     int64  `json:"created_time"`
			InviteeUsername string `json:"invitee_username"`
			BasisQuota      int    `json:"basis_quota"`
			RewardQuota     int    `json:"reward_quota"`
			Status          string `json:"status"`
		} `json:"rewards"`
	} `json:"data"`
}

// setupAffiliateRewardsAPI installs the wallet's reward ledger route on the
// shared SQLite fixture. The route authenticates with the same UserAuth
// middleware as the real router, so the tests exercise the real identity
// boundary instead of a hand-set context value.
func setupAffiliateRewardsAPI(t *testing.T) *gin.Engine {
	t.Helper()
	require.NoError(t, i18n.Init())
	setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.InvitationReward{}))
	router := gin.New()
	router.GET("/api/user/aff/rewards", middleware.UserAuth(), GetAffiliateRewards)
	return router
}

func createAffiliateRewardsUser(t *testing.T, username string, email string) (*model.User, string) {
	t.Helper()
	token := username + "-token"
	user := &model.User{
		Username: username, Email: email, Password: "placeholder", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AccessToken: &token, AuthVersion: 1,
		AffCode: username,
	}
	require.NoError(t, model.DB.Create(user).Error)
	return user, token
}

func seedInvitationReward(t *testing.T, redemptionId int, inviterId int, inviteeId int, status string, rewardQuota int, createdTime int64) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.InvitationReward{
		RedemptionId: redemptionId, InviterId: inviterId, InviteeId: inviteeId,
		BasisQuota: rewardQuota * 20, Ratio: 0.05, RewardQuota: rewardQuota,
		Status: status, CreatedTime: createdTime,
		SettleAfter: createdTime + model.InvitationRewardObservationWindowSeconds,
	}).Error)
}

func fetchAffiliateRewards(t *testing.T, router http.Handler, token string) (*httptest.ResponseRecorder, affiliateRewardsEnvelope) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/user/aff/rewards", nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var decoded affiliateRewardsEnvelope
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &decoded))
	return response, decoded
}

func TestAffiliateRewardsEndpointReturnsOnlyCallersOwnLedger(t *testing.T) {
	router := setupAffiliateRewardsAPI(t)
	inviter, inviterToken := createAffiliateRewardsUser(t, "rewards-inviter-a", "")
	invitee, inviteeToken := createAffiliateRewardsUser(t, "rewards-invitee", "invitee@example.com")
	otherInviter, otherToken := createAffiliateRewardsUser(t, "rewards-inviter-b", "")
	assert.NotEmpty(t, inviteeToken)

	now := common.GetTimestamp()
	seedInvitationReward(t, 101, inviter.Id, invitee.Id, model.InvitationRewardStatusCredited, 2000, now-300)
	seedInvitationReward(t, 102, inviter.Id, invitee.Id, model.InvitationRewardStatusPending, 1000, now-200)
	seedInvitationReward(t, 103, inviter.Id, invitee.Id, model.InvitationRewardStatusVoided, 4000, now-100)
	seedInvitationReward(t, 104, otherInviter.Id, invitee.Id, model.InvitationRewardStatusPending, 7000, now)

	response, body := fetchAffiliateRewards(t, router, inviterToken)
	require.Equal(t, http.StatusOK, response.Code)
	require.True(t, body.Success)
	assert.Equal(t, 1000, body.Data.PendingQuota, "pending total counts pending rows only")
	require.Len(t, body.Data.Rewards, 3)
	assert.Equal(t, []string{
		model.InvitationRewardStatusVoided,
		model.InvitationRewardStatusPending,
		model.InvitationRewardStatusCredited,
	}, []string{
		body.Data.Rewards[0].Status,
		body.Data.Rewards[1].Status,
		body.Data.Rewards[2].Status,
	})
	assert.Equal(t, invitee.Username, body.Data.Rewards[0].InviteeUsername)
	assert.Equal(t, 80000, body.Data.Rewards[0].BasisQuota)
	assert.Equal(t, 4000, body.Data.Rewards[0].RewardQuota)
	assert.Equal(t, now-100, body.Data.Rewards[0].CreatedTime)
	assert.NotContains(t, response.Body.String(), invitee.Email, "the invitee email must never reach the inviter")

	// Another inviter's ledger is invisible: their pending total and row count
	// only reflect their own reward.
	_, otherBody := fetchAffiliateRewards(t, router, otherToken)
	assert.Equal(t, 7000, otherBody.Data.PendingQuota)
	require.Len(t, otherBody.Data.Rewards, 1)
	assert.Equal(t, model.InvitationRewardStatusPending, otherBody.Data.Rewards[0].Status)

	// Without credentials the endpoint answers 401 instead of any ledger data.
	anonymous, _ := fetchAffiliateRewards(t, router, "")
	assert.Equal(t, http.StatusUnauthorized, anonymous.Code)
	assert.NotContains(t, anonymous.Body.String(), invitee.Username)
}

func TestAffiliateRewardsEndpointReturnsEmptyListInsteadOfNull(t *testing.T) {
	router := setupAffiliateRewardsAPI(t)
	_, token := createAffiliateRewardsUser(t, "rewards-empty", "")

	response, body := fetchAffiliateRewards(t, router, token)

	require.Equal(t, http.StatusOK, response.Code)
	assert.True(t, body.Success)
	assert.Equal(t, 0, body.Data.PendingQuota)
	require.NotNil(t, body.Data.Rewards)
	assert.Empty(t, body.Data.Rewards)
	assert.Contains(t, response.Body.String(), `"rewards":[]`)
}

func TestAffiliateRewardsEndpointCapsLedgerAtMostRecentRows(t *testing.T) {
	router := setupAffiliateRewardsAPI(t)
	inviter, token := createAffiliateRewardsUser(t, "rewards-capped", "")
	invitee, _ := createAffiliateRewardsUser(t, "rewards-capped-invitee", "")

	total := model.AffiliateRewardListLimit + 1
	now := common.GetTimestamp()
	for i := range total {
		seedInvitationReward(t, 200+i, inviter.Id, invitee.Id, model.InvitationRewardStatusPending, 100+i, now+int64(i))
	}

	response, body := fetchAffiliateRewards(t, router, token)
	require.Equal(t, http.StatusOK, response.Code)
	require.Len(t, body.Data.Rewards, model.AffiliateRewardListLimit)
	assert.Equal(t, now+int64(total-1), body.Data.Rewards[0].CreatedTime, "newest row comes first")
	assert.Equal(t, now+1, body.Data.Rewards[len(body.Data.Rewards)-1].CreatedTime, "the oldest row is dropped by the cap")
	assert.Equal(t, total*100+total*(total-1)/2, body.Data.PendingQuota,
		"the pending total covers the whole window, not just the listed rows")
}

// TestAffiliateRewardsQueryAcrossDialects runs the ledger query the endpoint
// serves on every supported database. The middleware path above is
// dialect-independent, so the matrix targets the SQL itself: the join with the
// users table (including its soft-delete column), the pending SUM and the
// quota column types must behave the same on SQLite, MySQL and PostgreSQL.
// MySQL/PostgreSQL runs need TEST_MYSQL_DSN / TEST_POSTGRES_DSN pointing at a
// loopback instance and are skipped otherwise; each run reuses the shared
// modelManagementDB fixture, which creates its own isolated database and logs
// the connected database version.
func TestAffiliateRewardsQueryAcrossDialects(t *testing.T) {
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			if dialect.env != "" && os.Getenv(dialect.env) == "" {
				t.Skip("set " + dialect.env + " to run this database")
			}
			db := modelManagementDB(t, dialect.kind, os.Getenv(dialect.env))
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.InvitationReward{}))

			marker := common.GetRandomString(8)
			inviter := &model.User{Username: "dialect-inviter-" + marker, Password: "placeholder", Status: common.UserStatusEnabled, AffCode: marker}
			invitee := &model.User{Username: "dialect-invitee-" + marker, Password: "placeholder", Status: common.UserStatusEnabled, AffCode: "i" + marker}
			removedInvitee := &model.User{Username: "dialect-removed-" + marker, Password: "placeholder", Status: common.UserStatusEnabled, AffCode: "r" + marker}
			require.NoError(t, db.Create(inviter).Error)
			require.NoError(t, db.Create(invitee).Error)
			require.NoError(t, db.Create(removedInvitee).Error)
			require.NoError(t, db.Delete(removedInvitee).Error)

			seedInvitationReward(t, 301, inviter.Id, invitee.Id, model.InvitationRewardStatusPending, 1234, 1_700_000_000)
			seedInvitationReward(t, 302, inviter.Id, removedInvitee.Id, model.InvitationRewardStatusCredited, 5000, 1_700_000_100)

			overview, err := model.GetAffiliateRewardOverview(inviter.Id, model.AffiliateRewardListLimit)
			require.NoError(t, err)
			assert.Equal(t, 1234, overview.PendingQuota)
			require.Len(t, overview.Rewards, 2)
			byStatus := map[string]model.AffiliateRewardEntry{}
			for _, reward := range overview.Rewards {
				byStatus[reward.Status] = reward
			}
			assert.Equal(t, invitee.Username, byStatus[model.InvitationRewardStatusPending].InviteeUsername)
			assert.Equal(t, 1234*20, byStatus[model.InvitationRewardStatusPending].BasisQuota)
			assert.Equal(t, "", byStatus[model.InvitationRewardStatusCredited].InviteeUsername, "a removed invitee leaves no username behind")
		})
	}
}
