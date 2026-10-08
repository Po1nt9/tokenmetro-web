package model

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

type RedemptionOutcomeType string

const (
	RedemptionOutcomeBalance      RedemptionOutcomeType = "balance"
	RedemptionOutcomeSubscription RedemptionOutcomeType = "subscription"
)

type Redemption struct {
	Id                 int                   `json:"id"`
	UserId             int                   `json:"user_id"`
	Key                string                `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status             int                   `json:"status" gorm:"default:1"`
	Name               string                `json:"name" gorm:"index"`
	Quota              int                   `json:"quota" gorm:"default:100"`
	OutcomeType        RedemptionOutcomeType `json:"outcome_type" gorm:"type:varchar(24);not null;default:'balance'"`
	SubscriptionPlanId int                   `json:"subscription_plan_id" gorm:"not null;default:0;index"`
	CreatedTime        int64                 `json:"created_time" gorm:"bigint"`
	RedeemedTime       int64                 `json:"redeemed_time" gorm:"bigint"`
	Count              int                   `json:"count" gorm:"-:all"` // only for api request
	UsedUserId         int                   `json:"used_user_id"`
	// SaleOrderId is nullable because this repository has no linked-shop order fact yet.
	// A future shop integration may populate its stable order reference without storing code text or inventing sale value.
	SaleOrderId *string `json:"sale_order_id,omitempty" gorm:"type:varchar(128);default:null"`
	// RewardEligible marks a batch that must not earn invitation rewards (granted,
	// trial, compensation, or test codes). The column defaults to true so codes
	// created before the field existed stay reward-eligible.
	RewardEligible bool           `json:"reward_eligible" gorm:"not null;default:true"`
	DeletedAt      gorm.DeletedAt `gorm:"index"`
	ExpiredTime    int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
}

func (redemption Redemption) EffectiveOutcomeType() RedemptionOutcomeType {
	if redemption.OutcomeType == "" {
		return RedemptionOutcomeBalance
	}
	return redemption.OutcomeType
}

func (redemption *Redemption) ValidateOutcome() error {
	switch redemption.EffectiveOutcomeType() {
	case RedemptionOutcomeBalance:
		if redemption.SubscriptionPlanId != 0 {
			return errors.New("balance redemption cannot reference a subscription plan")
		}
	case RedemptionOutcomeSubscription:
		if redemption.SubscriptionPlanId <= 0 {
			return errors.New("subscription redemption requires a plan")
		}
	default:
		return errors.New("invalid redemption outcome")
	}
	return nil
}

type RechargeEvent struct {
	RedemptionId   int                   `json:"redemption_id"`
	UserId         int                   `json:"user_id"`
	InviterId      int                   `json:"inviter_id"`
	OutcomeType    RedemptionOutcomeType `json:"outcome_type"`
	WalletQuota    int                   `json:"wallet_quota,omitempty"`
	PlanId         int                   `json:"plan_id,omitempty"`
	SubscriptionId int                   `json:"subscription_id,omitempty"`
	// SaleOrderId is absent until a trusted linked-shop order reference is available.
	SaleOrderId *string `json:"sale_order_id,omitempty"`
}

type RedemptionResult struct {
	OutcomeType   RedemptionOutcomeType `json:"outcome_type"`
	WalletQuota   int                   `json:"wallet_quota,omitempty"`
	Subscription  *UserSubscription     `json:"subscription,omitempty"`
	RechargeEvent RechargeEvent         `json:"recharge_event"`
}

type RedemptionPreview struct {
	OutcomeType  RedemptionOutcomeType          `json:"outcome_type"`
	WalletQuota  int                            `json:"wallet_quota,omitempty"`
	BalanceAfter int                            `json:"balance_after,omitempty"`
	Subscription *RedemptionSubscriptionPreview `json:"subscription,omitempty"`
}

type RedemptionSubscriptionPreview struct {
	PlanTitle          string `json:"plan_title"`
	DurationUnit       string `json:"duration_unit"`
	DurationValue      int    `json:"duration_value"`
	CustomSeconds      int64  `json:"custom_seconds,omitempty"`
	Quota              int64  `json:"quota"`
	ResetPeriod        string `json:"reset_period"`
	ResetCustomSeconds int64  `json:"reset_custom_seconds,omitempty"`
	UpgradeGroup       string `json:"upgrade_group,omitempty"`
	DowngradeGroup     string `json:"downgrade_group,omitempty"`
}

// PreviewRedemption returns only the grant summary. It deliberately does not lock or reserve the code.
func PreviewRedemption(key string, userId int) (RedemptionPreview, error) {
	var preview RedemptionPreview
	if key == "" || userId <= 0 {
		return preview, ErrRedeemFailed
	}
	var redemption Redemption
	if err := DB.Where(commonKeyCol+" = ? AND status = ?", key, common.RedemptionCodeStatusEnabled).First(&redemption).Error; err != nil {
		return preview, ErrRedeemFailed
	}
	if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
		return preview, ErrRedeemFailed
	}
	if redemption.OutcomeType == "" {
		redemption.OutcomeType = RedemptionOutcomeBalance
	}
	if err := redemption.ValidateOutcome(); err != nil {
		return RedemptionPreview{}, ErrRedeemFailed
	}
	preview.OutcomeType = redemption.EffectiveOutcomeType()
	if preview.OutcomeType == RedemptionOutcomeBalance {
		if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
			return RedemptionPreview{}, ErrRedeemFailed
		}
		if err := ValidateTopUpQuotaCapacity(userId, redemption.Quota); err != nil {
			return RedemptionPreview{}, ErrRedeemFailed
		}
		var user User
		if err := DB.Select("quota").First(&user, "id = ?", userId).Error; err != nil {
			return RedemptionPreview{}, ErrRedeemFailed
		}
		preview.WalletQuota = redemption.Quota
		preview.BalanceAfter = user.Quota + redemption.Quota
		return preview, nil
	}
	var plan SubscriptionPlan
	if err := DB.Where("id = ? AND enabled = ?", redemption.SubscriptionPlanId, true).First(&plan).Error; err != nil {
		return RedemptionPreview{}, ErrRedeemFailed
	}
	plan.NormalizeDefaults()
	if plan.MaxPurchasePerUser > 0 {
		count, err := CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil || count >= int64(plan.MaxPurchasePerUser) {
			return RedemptionPreview{}, ErrRedeemFailed
		}
	}
	preview.Subscription = &RedemptionSubscriptionPreview{
		PlanTitle: plan.Title, DurationUnit: plan.DurationUnit, DurationValue: plan.DurationValue,
		CustomSeconds: plan.CustomSeconds, Quota: plan.TotalAmount,
		ResetPeriod: NormalizeResetPeriod(plan.QuotaResetPeriod), ResetCustomSeconds: plan.QuotaResetCustomSeconds,
		UpgradeGroup: plan.UpgradeGroup, DowngradeGroup: plan.DowngradeGroup,
	}
	return preview, nil
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, status string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{})

	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}

	if status != "" {
		now := common.GetTimestamp()
		switch status {
		case "expired":
			query = query.Where(
				"status = ? AND expired_time != 0 AND expired_time < ?",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusEnabled):
			query = query.Where(
				"status = ? AND (expired_time = 0 OR expired_time >= ?)",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusDisabled):
			query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
		case strconv.Itoa(common.RedemptionCodeStatusUsed):
			query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
		}
	}

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
}

func Redeem(key string, userId int) (result RedemptionResult, err error) {
	if key == "" {
		return result, errors.New("未提供兑换码")
	}
	if userId == 0 {
		return result, errors.New("无效的 user id")
	}
	redemption := &Redemption{}
	var subscription *UserSubscription
	var walletQuota int
	var inviterId int
	var planTitle string
	var planPrice float64

	common.RandomSleep()
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where(commonKeyCol+" = ?", key).First(redemption).Error; err != nil {
			return errors.New("无效的兑换码")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("该兑换码已被使用")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return errors.New("该兑换码已过期")
		}
		if redemption.OutcomeType == "" {
			redemption.OutcomeType = RedemptionOutcomeBalance
		}
		if err := redemption.ValidateOutcome(); err != nil {
			return err
		}
		outcome := redemption.EffectiveOutcomeType()
		if outcome == RedemptionOutcomeSubscription {
			if redemption.SubscriptionPlanId <= 0 {
				return errors.New("invalid subscription redemption plan")
			}
			var plan SubscriptionPlan
			if err := tx.Where("id = ?", redemption.SubscriptionPlanId).First(&plan).Error; err != nil {
				return errors.New("兑换套餐不可用")
			}
			if !plan.Enabled {
				return errors.New("套餐未启用")
			}
			planTitle = plan.Title
			planPrice = plan.PriceAmount
			// Serialize purchases from different redemption codes for the same user.
			var userRow User
			if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&userRow).Error; err != nil {
				return err
			}
			plan.NormalizeDefaults()
			subscription, err = CreateUserSubscriptionFromPlanTx(tx, userId, &plan, "redemption")
			if err != nil {

				return err
			}
		} else {
			if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
				return err
			}
			if err := creditTopUpQuota(tx, userId, redemption.Quota, nil); err != nil {
				return err
			}
			walletQuota = redemption.Quota
		}

		now := common.GetTimestamp()
		updated := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Updates(map[string]any{
				"redeemed_time": now,
				"status":        common.RedemptionCodeStatusUsed,
				"used_user_id":  userId,
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return errors.New("该兑换码已被使用")
		}
		var user User
		if err := tx.Select("inviter_id").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		inviterId = user.InviterId
		return createPendingInvitationRewardTx(tx, redemption, inviterId, userId, planPrice, now)
	})
	if err != nil {
		common.SysError("redemption failed: " + err.Error())
		return RedemptionResult{}, ErrRedeemFailed
	}

	outcome := redemption.EffectiveOutcomeType()
	result = RedemptionResult{
		OutcomeType:  outcome,
		WalletQuota:  walletQuota,
		Subscription: subscription,
		RechargeEvent: RechargeEvent{
			RedemptionId: redemption.Id,
			UserId:       userId,
			InviterId:    inviterId,
			OutcomeType:  outcome,
			WalletQuota:  walletQuota,
			SaleOrderId:  redemption.SaleOrderId,
		},
	}
	if subscription != nil {
		result.RechargeEvent.PlanId = subscription.PlanId
		result.RechargeEvent.SubscriptionId = subscription.Id
		if subscription.UpgradeGroup != "" {
			refreshSubscriptionUserGroupCache(userId, "redemption")
		}
		RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码开通订阅，套餐: %s，兑换码ID %d", planTitle, redemption.Id))
	} else {
		syncCreditUserQuotaCache(userId, walletQuota, "redemption")
		RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码充值 %s，兑换码ID %d", logger.LogQuota(walletQuota), redemption.Id))
	}
	return result, nil
}

func (redemption *Redemption) Insert() error {
	if redemption.OutcomeType == "" {
		redemption.OutcomeType = RedemptionOutcomeBalance
	}
	if err := redemption.ValidateOutcome(); err != nil {
		return err
	}
	if redemption.EffectiveOutcomeType() == RedemptionOutcomeBalance {
		if redemption.Quota <= 0 {
			return errors.New("redemption quota must be positive")
		}
		if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
			return err
		}
	}
	// reward_eligible defaults to true in the schema so codes created before the
	// field existed stay reward-eligible; GORM substitutes that default for a zero
	// bool during Create, so an explicit opt-out is written in the same transaction.
	if redemption.RewardEligible {
		return DB.Create(redemption).Error
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(redemption).Error; err != nil {
			return err
		}
		return tx.Model(&Redemption{}).Where("id = ?", redemption.Id).
			Update("reward_eligible", false).Error
	})
	if err != nil {
		return err
	}
	redemption.RewardEligible = false
	return nil
}

func (redemption *Redemption) SelectUpdate() error {
	// This can update zero values
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (redemption *Redemption) Update() error {
	if redemption.Status == common.RedemptionCodeStatusUsed {
		return errors.New("used redemption outcome is immutable")
	}
	if redemption.OutcomeType == "" {
		redemption.OutcomeType = RedemptionOutcomeBalance
	}
	if err := redemption.ValidateOutcome(); err != nil {
		return err
	}
	if redemption.EffectiveOutcomeType() == RedemptionOutcomeBalance {
		if redemption.Quota <= 0 {
			return errors.New("redemption quota must be positive")
		}
		if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
			return err
		}
	}
	result := DB.Model(redemption).Where("id = ? AND status <> ?", redemption.Id, common.RedemptionCodeStatusUsed).
		Select("name", "status", "quota", "outcome_type", "subscription_plan_id", "reward_eligible", "redeemed_time", "expired_time").Updates(redemption)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var current Redemption
		if err := DB.Select("status").First(&current, "id = ?", redemption.Id).Error; err != nil {
			return err
		}
		if current.Status == common.RedemptionCodeStatusUsed {
			return errors.New("used redemption outcome is immutable")
		}
	}
	return nil
}

func (redemption *Redemption) Delete() error {
	var err error
	err = DB.Delete(redemption).Error
	return err
}

func DeleteRedemptionById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	err = DB.Where(redemption).First(&redemption).Error
	if err != nil {
		return err
	}
	return redemption.Delete()
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?)", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, now).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}

// BatchDeleteRedemptions soft-deletes the selected codes in one statement.
func BatchDeleteRedemptions(ids []int) (int64, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return 0, errors.New("select between 1 and 1000 redemption codes")
	}
	for _, id := range ids {
		if id <= 0 {
			return 0, errors.New("redemption IDs must be positive")
		}
	}
	result := DB.Where("id IN ?", ids).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
