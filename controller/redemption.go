package controller

import (
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// redemptionMutationPayload binds the create/update request body. RewardEligible
// is a pointer so an omitted field keeps the default the column already carries
// (reward-eligible) instead of opting a batch out by accident.
type redemptionMutationPayload struct {
	model.Redemption
	RewardEligible *bool `json:"reward_eligible"`
}

// rewardEligibleOrDefault resolves the optional flag: omitted means the batch
// earns invitation rewards, which is what sold batches want.
func rewardEligibleOrDefault(payload *redemptionMutationPayload) bool {
	if payload.RewardEligible == nil {
		return true
	}
	return *payload.RewardEligible
}

func GetAllRedemptions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchRedemptions(c *gin.Context) {
	keyword := c.Query("keyword")
	status := c.Query("status")
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.SearchRedemptions(keyword, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption, err := model.GetRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    redemption,
	})
	return
}

func AddRedemption(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorI18n(c, i18n.MsgPaymentComplianceRequired)
		return
	}

	payload := redemptionMutationPayload{}
	err := c.ShouldBindJSON(&payload)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption := payload.Redemption
	rewardEligible := rewardEligibleOrDefault(&payload)
	if utf8.RuneCountInString(redemption.Name) == 0 || utf8.RuneCountInString(redemption.Name) > 20 {
		common.ApiErrorI18n(c, i18n.MsgRedemptionNameLength)
		return
	}
	if redemption.Count <= 0 {
		common.ApiErrorI18n(c, i18n.MsgRedemptionCountPositive)
		return
	}
	if redemption.Count > 100 {
		common.ApiErrorI18n(c, i18n.MsgRedemptionCountMax)
		return
	}
	planTitle, err := validateRedemptionOutcome(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if redemption.EffectiveOutcomeType() == model.RedemptionOutcomeBalance {
		if redemption.Quota <= 0 {
			common.ApiError(c, errors.New("redemption quota must be positive"))
			return
		}
		if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if valid, msg := validateExpiredTime(c, redemption.ExpiredTime); !valid {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
		return
	}
	var keys []string
	for i := 0; i < redemption.Count; i++ {
		key := common.GetUUID()
		cleanRedemption := model.Redemption{
			UserId:             c.GetInt("id"),
			Name:               redemption.Name,
			Key:                key,
			CreatedTime:        common.GetTimestamp(),
			Quota:              redemption.Quota,
			OutcomeType:        redemption.OutcomeType,
			SubscriptionPlanId: redemption.SubscriptionPlanId,
			ExpiredTime:        redemption.ExpiredTime,
			RewardEligible:     rewardEligible,
		}

		err = cleanRedemption.Insert()
		if err != nil {
			common.SysError("failed to insert redemption: " + err.Error())
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": i18n.T(c, i18n.MsgRedemptionCreateFailed),
				"data":    keys,
			})
			return
		}
		keys = append(keys, key)
	}
	if redemption.EffectiveOutcomeType() == model.RedemptionOutcomeSubscription {
		recordManageAudit(c, "redemption.create_subscription", map[string]any{
			"name":            redemption.Name,
			"count":           redemption.Count,
			"plan":            planTitle,
			"reward_eligible": rewardEligible,
		})
	} else {
		recordManageAudit(c, "redemption.create", map[string]any{
			"name":                 redemption.Name,
			"count":                redemption.Count,
			"quota":                logger.LogQuota(redemption.Quota),
			"outcome_type":         redemption.EffectiveOutcomeType(),
			"subscription_plan_id": redemption.SubscriptionPlanId,
			"reward_eligible":      rewardEligible,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    keys,
	})
	return
}

// VoidRedemptionReward marks the invitation reward produced by one redemption
// code as voided, which is how an administrator takes back a pending reward
// after refunding the recharge. It never touches the redemption row itself:
// a used code stays immutable (model/redemption.go), so the refund bookkeeping
// lives on the reward ledger. The redeemed user's quota is subtracted through
// the existing user management endpoint, not here.
func VoidRedemptionReward(c *gin.Context) {
	var request struct {
		RedemptionId int `json:"redemption_id" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := model.VoidInvitationRewardByRedemptionId(request.RedemptionId); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "redemption.reward_void", map[string]any{
		"redemption_id": request.RedemptionId,
	})
	common.ApiSuccess(c, nil)
}

func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateRedemption(c *gin.Context) {
	statusOnly := c.Query("status_only")
	payload := redemptionMutationPayload{}
	err := c.ShouldBindJSON(&payload)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption := payload.Redemption
	cleanRedemption, err := model.GetRedemptionById(redemption.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	previousRewardEligible := cleanRedemption.RewardEligible
	if statusOnly == "" {
		if cleanRedemption.Status == common.RedemptionCodeStatusUsed {
			common.ApiError(c, errors.New("used redemption outcome is immutable"))
			return
		}
		if redemption.OutcomeType != "" || redemption.SubscriptionPlanId != 0 {
			if _, err := validateRedemptionOutcome(&redemption); err != nil {
				common.ApiError(c, err)
				return
			}
			cleanRedemption.OutcomeType = redemption.OutcomeType
			cleanRedemption.SubscriptionPlanId = redemption.SubscriptionPlanId
		}
		if cleanRedemption.EffectiveOutcomeType() == model.RedemptionOutcomeBalance {
			if redemption.Quota <= 0 {
				common.ApiError(c, errors.New("redemption quota must be positive"))
				return
			}
			if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
				common.ApiError(c, err)
				return
			}
		}
		if valid, msg := validateExpiredTime(c, redemption.ExpiredTime); !valid {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
			return
		}
		// If you add more fields, please also update redemption.Update()
		cleanRedemption.Name = redemption.Name
		cleanRedemption.Quota = redemption.Quota
		if redemption.OutcomeType != "" || redemption.SubscriptionPlanId != 0 {
			cleanRedemption.OutcomeType = redemption.OutcomeType
			cleanRedemption.SubscriptionPlanId = redemption.SubscriptionPlanId
		}
		cleanRedemption.ExpiredTime = redemption.ExpiredTime
		if payload.RewardEligible != nil {
			cleanRedemption.RewardEligible = *payload.RewardEligible
		}

	}
	if statusOnly != "" {
		cleanRedemption.Status = redemption.Status
	}
	err = cleanRedemption.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if cleanRedemption.RewardEligible != previousRewardEligible {
		recordManageAudit(c, "redemption.reward_eligible_update", map[string]any{
			"redemption_id":   cleanRedemption.Id,
			"name":            cleanRedemption.Name,
			"reward_eligible": cleanRedemption.RewardEligible,
			"from":            previousRewardEligible,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanRedemption,
	})
	return
}

func DeleteInvalidRedemption(c *gin.Context) {
	rows, err := model.DeleteInvalidRedemptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func validateRedemptionOutcome(redemption *model.Redemption) (string, error) {
	if redemption.OutcomeType == "" {
		redemption.OutcomeType = model.RedemptionOutcomeBalance
	}
	if err := redemption.ValidateOutcome(); err != nil {
		return "", err
	}
	if redemption.EffectiveOutcomeType() == model.RedemptionOutcomeSubscription {
		var plan model.SubscriptionPlan
		if err := model.DB.Where("id = ?", redemption.SubscriptionPlanId).First(&plan).Error; err != nil {
			common.SysError("failed to load subscription plan for redemption: " + err.Error())
			return "", errors.New("subscription plan not found")
		}
		if !plan.Enabled {
			return "", errors.New("subscription plan is disabled")
		}
		return plan.Title, nil
	}
	return "", nil
}

func validateExpiredTime(c *gin.Context, expired int64) (bool, string) {
	if expired != 0 && expired < common.GetTimestamp() {
		return false, i18n.T(c, i18n.MsgRedemptionExpireTimeInvalid)
	}
	return true, ""
}

func DeleteRedemptionBatch(c *gin.Context) {
	var request struct {
		Ids []int `json:"ids" binding:"required,min=1,max=1000,dive,gt=0"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	count, err := model.BatchDeleteRedemptions(request.Ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "redemption.delete_batch", map[string]any{
		"count":                    count,
		"total":                    len(request.Ids),
		"requested_redemption_ids": request.Ids,
	})
	common.ApiSuccess(c, count)
}
