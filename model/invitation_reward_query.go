package model

// AffiliateRewardListLimit is how many of an inviter's most recent reward
// ledger rows the wallet detail list shows. The list is a reconciliation aid,
// not an export, so the limit is fixed and the endpoint offers no filtering.
const AffiliateRewardListLimit = 20

// AffiliateRewardEntry is one invitation reward as shown to the inviter who
// earned it. It carries the invitee's username and never the invitee's email,
// so the inviter can tell rows apart without learning account details.
type AffiliateRewardEntry struct {
	Id              int    `json:"id"`
	CreatedTime     int64  `json:"created_time"`
	InviteeUsername string `json:"invitee_username"`
	BasisQuota      int    `json:"basis_quota"`
	RewardQuota     int    `json:"reward_quota"`
	Status          string `json:"status"`
}

// AffiliateRewardOverview is one inviter's own reward view: the quota still
// inside the observation window plus the most recent ledger rows. PendingQuota
// counts every pending row, not only the listed ones, so the number matches
// what settlement will later add to the affiliate pool.
type AffiliateRewardOverview struct {
	PendingQuota int                    `json:"pending_quota"`
	Rewards      []AffiliateRewardEntry `json:"rewards"`
}

// GetAffiliateRewardOverview reads the reward ledger of a single inviter in
// newest-first order. The caller supplies the inviter id from the
// authenticated session, so this query is also the isolation boundary: it
// never returns another inviter's rows. Invitees deleted through the users
// table's soft delete leave an empty username, which the UI renders as a
// deleted account instead of leaking a stale name.
func GetAffiliateRewardOverview(inviterId int, limit int) (AffiliateRewardOverview, error) {
	if limit <= 0 {
		limit = AffiliateRewardListLimit
	}
	overview := AffiliateRewardOverview{Rewards: make([]AffiliateRewardEntry, 0, limit)}
	err := DB.Model(&InvitationReward{}).
		Where("inviter_id = ? AND status = ?", inviterId, InvitationRewardStatusPending).
		Select("COALESCE(SUM(reward_quota), 0)").
		Scan(&overview.PendingQuota).Error
	if err != nil {
		return overview, err
	}
	err = DB.Table("invitation_rewards AS r").
		Select("r.id AS id, r.created_time AS created_time, COALESCE(u.username, '') AS invitee_username, r.basis_quota AS basis_quota, r.reward_quota AS reward_quota, r.status AS status").
		Joins("LEFT JOIN users AS u ON u.id = r.invitee_id AND u.deleted_at IS NULL").
		Where("r.inviter_id = ?", inviterId).
		Order("r.id DESC").
		Limit(limit).
		Scan(&overview.Rewards).Error
	if err != nil {
		return overview, err
	}
	return overview, nil
}
