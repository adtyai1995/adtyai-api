package controller

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 推广管理（任务 6.7 的后端）。所有接口都用 handlePermissionRoute 按推广返佣的权限注册。

func referralPageRows(c *gin.Context, query *gorm.DB, out any) (*common.PageInfo, bool) {
	pageInfo := common.GetPageQuery(c)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	if err := query.Order("id DESC").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(out).Error; err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	pageInfo.SetTotal(int(total))
	return pageInfo, true
}

func referralPage(c *gin.Context, query *gorm.DB, out any) bool {
	pageInfo, ok := referralPageRows(c, query, out)
	if !ok {
		return false
	}
	pageInfo.SetItems(out)
	common.ApiSuccess(c, pageInfo)
	return true
}

type referralAffiliateAdminItem struct {
	model.ReferralAffiliate
	Username string `json:"username"`
}

type referralBindingAdminItem struct {
	model.ReferralBinding
	InviteeUsername string `json:"invitee_username"`
	InviterUsername string `json:"inviter_username"`
}

type referralCommissionAdminItem struct {
	model.ReferralCommission
	AffiliateUsername string `json:"affiliate_username"`
	InviteeUsername   string `json:"invitee_username"`
}

type referralLedgerAdminItem struct {
	model.ReferralCommissionLedger
	Username         string `json:"username"`
	OperatorUsername string `json:"operator_username,omitempty"`
}

type referralAdminAuditLogItem struct {
	model.ReferralAdminAuditLog
	AdminUsername  string `json:"admin_username"`
	TargetUsername string `json:"target_username"`
}

type referralWithdrawalAdminItem struct {
	model.ReferralWithdrawal
	Username           string `json:"username"`
	ReviewedByUsername string `json:"reviewed_by_username,omitempty"`
	RejectedByUsername string `json:"rejected_by_username,omitempty"`
	CanceledByUsername string `json:"canceled_by_username,omitempty"`
}

// GetReferralAdminBadges 是侧边栏「推广管理」旁的待处理数：待审核提现 + 待打款提现（合作伙伴已下线，partners 恒为 0）。
// 计数按调用者的角色与站点过滤：Root 全局，非 Root 管理员只看主站，不再做未过滤的全局合计。
func GetReferralAdminBadges(c *gin.Context) {
	badges, err := service.CollectAdtyReferralAdminBadges(c.GetInt("id"), referralBadgeTokenScopes(c)...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"partners": 0, "pending_withdrawals": badges.PendingWithdrawals, "approved_withdrawals": badges.ApprovedWithdrawals,
		"total": badges.PendingWithdrawals + badges.ApprovedWithdrawals,
	})
}

// GetReferralOverview 是推广的总览数字（美元）。
// 口径与角标相同：Root 全局，非 Root 管理员只看主站用户（users.site_id = 0），不再做未过滤的全局合计。
func GetReferralOverview(c *gin.Context) {
	overview, err := service.CollectAdtyReferralOverview(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"affiliates": overview.Affiliates, "bindings": overview.Bindings, "pending": overview.Pending.StringFixed(2),
		"available": overview.Available.StringFixed(2), "frozen": overview.Frozen.StringFixed(2), "withdrawn": overview.Withdrawn.StringFixed(2),
	})
}

// GetReferralSettings 返回推广设置；提现汇率直接查库。
func GetReferralSettings(c *gin.Context) {
	cfg := *adty_setting.GetReferralSetting()
	hasRate := false
	if rate, updatedAt, err := service.CurrentWithdrawCNYRate(); err == nil {
		cfg.WithdrawCNYRate, cfg.WithdrawCNYRateUpdatedAt = rate.String(), updatedAt
		hasRate = true
	} else if !errors.Is(err, service.ErrReferralRateUnset) {
		common.ApiError(c, err)
		return
	} else {
		cfg.WithdrawCNYRate, cfg.WithdrawCNYRateUpdatedAt = "", 0
	}
	cfg.WithdrawMethods, cfg.WithdrawUSDTNetworks = cfg.EffectiveWithdrawConfig(hasRate)
	common.ApiSuccess(c, cfg)
}

// UpdateReferralSettings 保存推广设置。提现汇率和这些设置同一次提交，汇率不合法或写库失败时一起回滚。
func UpdateReferralSettings(c *gin.Context) {
	var body struct {
		DefaultRate              *float64  `json:"default_rate"`
		CookieTTLDays            *int      `json:"cookie_ttl_days"`
		FreezeDays               *int      `json:"freeze_days"`
		MinWithdrawUSD           *float64  `json:"min_withdraw_usd"`
		USDTWithdrawFee          *float64  `json:"usdt_withdraw_fee"`
		CancelWindowMinutes      *int      `json:"cancel_window_minutes"`
		SelfInviteWindowDays     *int      `json:"self_invite_window_days"`
		BatchRegisterThreshold   *int      `json:"batch_register_threshold"`
		BatchRegisterWindowHours *int      `json:"batch_register_window_hours"`
		Enabled                  *bool     `json:"enabled"`
		WithdrawCNYRate          string    `json:"withdraw_cny_rate"`
		WithdrawMethods          *[]string `json:"withdraw_methods"`
		WithdrawUSDTNetworks     *[]string `json:"withdraw_usdt_networks"`
		// Clear 列出要改回「未设置」的钱款项：default_rate、min_withdraw_usd、usdt_withdraw_fee、withdraw_cny_rate（约定 I）。
		Clear []string `json:"clear"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	values := map[string]string{}
	putFloat := func(key string, value *float64, minimum float64) bool {
		if value == nil {
			return true
		}
		if *value < minimum {
			common.ApiErrorT(c, "Invalid parameters")
			return false
		}
		values["adty_referral_setting."+key] = strconv.FormatFloat(*value, 'f', -1, 64)
		return true
	}
	putInt := func(key string, value *int, minimum int) bool {
		if value == nil {
			return true
		}
		if *value < minimum {
			common.ApiErrorT(c, "Invalid parameters")
			return false
		}
		values["adty_referral_setting."+key] = strconv.Itoa(*value)
		return true
	}
	if body.DefaultRate != nil && *body.DefaultRate > 100 {
		common.ApiErrorT(c, "The rate must be between 0 and 100")
		return
	}
	if !putFloat("default_rate", body.DefaultRate, 0) || !putInt("cookie_ttl_days", body.CookieTTLDays, 1) ||
		!putInt("freeze_days", body.FreezeDays, 0) || !putFloat("min_withdraw_usd", body.MinWithdrawUSD, 0) ||
		!putFloat("usdt_withdraw_fee", body.USDTWithdrawFee, 0) || !putInt("cancel_window_minutes", body.CancelWindowMinutes, 0) ||
		!putInt("self_invite_window_days", body.SelfInviteWindowDays, 1) || !putInt("batch_register_threshold", body.BatchRegisterThreshold, 0) ||
		!putInt("batch_register_window_hours", body.BatchRegisterWindowHours, 1) {
		return
	}
	if body.WithdrawMethods != nil {
		methods, ok := adty_setting.NormalizeReferralWithdrawMethods(*body.WithdrawMethods)
		if !ok {
			common.ApiErrorT(c, "Unsupported withdrawal method")
			return
		}
		encoded, err := common.Marshal(methods)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		values[adty_setting.ReferralKeyWithdrawMethods] = string(encoded)
	}
	if body.WithdrawUSDTNetworks != nil {
		networks, ok := adty_setting.NormalizeReferralWithdrawUSDTNetworks(*body.WithdrawUSDTNetworks)
		if !ok {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		encoded, err := common.Marshal(networks)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		values[adty_setting.ReferralKeyWithdrawUSDTNetworks] = string(encoded)
	}
	for _, key := range body.Clear {
		switch key {
		case "default_rate", "min_withdraw_usd", "usdt_withdraw_fee":
			values["adty_referral_setting."+key] = "null"
		case "withdraw_cny_rate":
			if strings.TrimSpace(body.WithdrawCNYRate) == "" {
				values[adty_setting.ReferralKeyWithdrawCNYRate] = ""
				values[adty_setting.ReferralKeyWithdrawCNYRateUpdatedAt] = "0"
			}
		default:
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	if body.Enabled != nil {
		values["adty_referral_setting.enabled"] = strconv.FormatBool(*body.Enabled)
	}
	if err := service.Referral.SaveReferralSettings(values, body.WithdrawCNYRate, c.GetInt("id"), c.ClientIP(), c.Request.UserAgent()); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.referral.settings", map[string]any{"keys": len(values)})
	GetReferralSettings(c)
}

func referralAdminScopeOrError(c *gin.Context) (*int, bool) {
	siteID, ok, err := service.ResolveAdtyReferralAdminScope(c.GetInt("id"))
	if err != nil || !ok {
		if err != nil {
			common.ApiError(c, err)
		} else {
			common.ApiErrorT(c, "No permission to perform this action")
		}
		return nil, false
	}
	return siteID, true
}

// GetReferralAffiliates 列出推广员，可按状态、合作伙伴申请、关键词筛选。
func GetReferralAffiliates(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	query := service.ScopeAdtyReferralUsersQuery(model.DB.Model(&model.ReferralAffiliate{}), "user_id", siteID)
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		query = query.Where("invite_code = ? OR user_id IN (?)", strings.ToUpper(keyword),
			model.DB.Model(&model.User{}).Select("id").Where("username LIKE ?", "%"+keyword+"%"))
	}
	var rows []model.ReferralAffiliate
	pageInfo, ok := referralPageRows(c, query, &rows)
	if !ok {
		return
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]referralAffiliateAdminItem, len(rows))
	for i, row := range rows {
		items[i] = referralAffiliateAdminItem{
			ReferralAffiliate: row,
			Username:          names[row.UserId],
		}
	}
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetReferralBindings 列出一个推广员带来的用户。
func GetReferralBindings(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.EnsureAdtyReferralTargetUserScope(siteID, userID); err != nil {
		common.ApiError(c, err)
		return
	}
	var targetUser model.User
	if err := model.DB.Select("id", "role").First(&targetUser, userID).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if !canManageTargetRole(c.GetInt("role"), targetUser.Role) {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	var rows []model.ReferralBinding
	pageInfo, ok := referralPageRows(c, service.ScopeAdtyReferralUsersQuery(model.DB.Model(&model.ReferralBinding{}).Where("inviter_user_id = ?", userID), "inviter_user_id", siteID), &rows)
	if !ok {
		return
	}
	ids := make([]int, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.InviteeUserId, row.InviterUserId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]referralBindingAdminItem, len(rows))
	for i, row := range rows {
		items[i] = referralBindingAdminItem{
			ReferralBinding: row,
			InviteeUsername: names[row.InviteeUserId],
			InviterUsername: names[row.InviterUserId],
		}
	}
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// ReviewReferralPartner 合作伙伴申请审核已下线（R-34 #2）。
func ReviewReferralPartner(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Partner applications are disabled"))
}

// UpdateReferralAffiliate 调整推广员（推广返佣·调整）：设比例、停用/恢复、冻结/恢复结算和提现、调佣金。
func UpdateReferralAffiliate(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.EnsureAdtyReferralTargetUserScope(siteID, userID); err != nil {
		common.ApiError(c, err)
		return
	}
	var body struct {
		Action         string          `json:"action"` // rate | disable | restore | freeze_settlement | restore_settlement | freeze_withdrawal | restore_withdrawal | restore_referral | adjust
		Rate           *float64        `json:"rate"`
		Amount         decimal.Decimal `json:"amount"`
		Reason         string          `json:"reason"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	adminID := c.GetInt("id")
	actorRole := c.GetInt("role")
	denyAdminTarget := isAccessTokenRequest(c)
	replayed := false
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// R3-08: Lock target user BEFORE referral affiliate/account rows to preserve
		// canonical User -> Affiliate -> Commission -> Withdrawal -> Account lock order.
		if err := service.CheckReferralAdminTargetUserTx(tx, userID, adminID, actorRole, denyAdminTarget); err != nil {
			return err
		}
		var affiliate model.ReferralAffiliate
		if err := model.AdtyLockForUpdate(tx).Where("user_id = ?", userID).First(&affiliate).Error; err != nil {
			return err
		}
		oldSnap := map[string]any{
			"rate_override":      affiliate.RateOverride,
			"status":             affiliate.Status,
			"settlement_enabled": affiliate.SettlementEnabled,
			"withdrawal_enabled": affiliate.WithdrawalEnabled,
			"site_owner_since":   affiliate.SiteOwnerSince,
		}
		updates := map[string]any{}
		switch body.Action {
		case "rate":
			if body.Rate != nil && (math.IsNaN(*body.Rate) || math.IsInf(*body.Rate, 0) || *body.Rate < 0 || *body.Rate > 100) {
				return common.NewMessage("The rate must be between 0 and 100")
			}
			if body.Rate == nil {
				updates["rate_override"] = nil
			} else {
				updates["rate_override"] = *body.Rate
			}
		case "disable":
			updates["status"], updates["disabled_at"], updates["disabled_by"] = model.ReferralAffiliateStatusDisabled, time.Now().Unix(), adminID
		case "restore":
			updates["status"], updates["disabled_at"], updates["disabled_by"] = model.ReferralAffiliateStatusApproved, 0, 0
		case "freeze_settlement", "restore_settlement":
			updates["settlement_enabled"] = body.Action == "restore_settlement"
			if body.Action == "restore_settlement" {
				updates["risk_reason"] = ""
				updates["risk_note"] = ""
			} else if note := strings.TrimSpace(body.Reason); note != "" {
				updates["risk_note"] = note
			}
		case "freeze_withdrawal", "restore_withdrawal":
			updates["withdrawal_enabled"] = body.Action == "restore_withdrawal"
		case "restore_referral":
			// 只有名下没有未关闭的站才能恢复邀请返佣。active、paused、closing 都还是站长。
			var openSites int64
			if err := tx.Model(&model.AdtySite{}).Where("owner_user_id = ? AND status <> ?", userID, model.AdtySiteStatusClosed).Count(&openSites).Error; err != nil {
				return err
			}
			if openSites > 0 {
				return common.NewMessage("Close the site before restoring referral commissions")
			}
			updates["site_owner_since"] = 0
		case "adjust":
			key := strings.TrimSpace(body.IdempotencyKey)
			if body.Amount.IsZero() || !body.Amount.Equal(body.Amount.Truncate(2)) || strings.TrimSpace(body.Reason) == "" || key == "" || len(key) > 80 || strings.ContainsAny(key, " \t\r\n") {
				return common.NewMessage("Enter an amount, a reason, and an idempotency key")
			}
			delta := body.Amount.Round(2)
			if delta.IsZero() {
				return common.NewMessage("Enter an amount, a reason, and an idempotency key")
			}
			ref := "admin-adjust:" + strconv.Itoa(adminID) + ":" + key
			var prior []model.ReferralCommissionLedger
			if err := tx.Where("external_ref_id = ?", ref).Limit(1).Find(&prior).Error; err != nil {
				return err
			}
			if len(prior) > 0 {
				if prior[0].AffiliateId != affiliate.Id || !decimal.NewFromFloat(prior[0].DeltaAvailable).Round(2).Equal(delta) {
					return model.ErrAdtyIdempotencyConflict
				}
				replayed = true
				return nil
			}
			var acc model.ReferralCommissionAccount
			if err := model.AdtyLockForUpdate(tx).Where("affiliate_id = ?", affiliate.Id).First(&acc).Error; err != nil {
				return err
			}
			if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: 0, AccountID: userID}); err != nil {
				return err
			}
			if decimal.NewFromFloat(acc.AvailableAmount).Add(delta).IsNegative() {
				return service.ErrReferralInsufficient
			}
			oldSnap = map[string]any{"available_amount": decimal.NewFromFloat(acc.AvailableAmount).Round(2).String()}
			if err := service.AdjustAccountTx(tx, &acc, decimal.Zero, delta, decimal.Zero, decimal.Zero, model.ReferralCommissionLedger{
				Type: "admin_adjust", RefType: "admin", RefId: fmt.Sprint(adminID), ExternalRefId: ref, Remark: strings.TrimSpace(body.Reason), Operator: fmt.Sprint(adminID),
			}); err != nil {
				return err
			}
		default:
			return common.NewMessage("Invalid parameters")
		}
		if len(updates) > 0 {
			if err := tx.Model(&model.ReferralAffiliate{}).Where("id = ?", affiliate.Id).Updates(updates).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.ReferralAdminAuditLog{
			Action: body.Action, TargetUserId: userID, AffiliateId: affiliate.Id, AdminUserId: adminID,
			Reason: strings.TrimSpace(body.Reason), Ip: c.ClientIP(), UserAgent: c.Request.UserAgent(),
			OldValue: common.GetJsonString(oldSnap), NewValue: common.GetJsonString(body),
		}).Error
	})
	if err != nil {
		writeReferralError(c, err)
		return
	}
	if replayed {
		markIdempotentReplayNoAudit(c)
	} else {
		recordManageAuditFor(c, userID, "adty.referral.affiliate_update", map[string]any{"action": body.Action})
	}
	common.ApiSuccess(c, nil)
}

// GetAdminReferralCommissions 列出佣金（含作废、扣回）。
func GetAdminReferralCommissions(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	query := service.ScopeAdtyReferralUsersQuery(model.DB.Model(&model.ReferralCommission{}), "affiliate_user_id", siteID)
	if status := strings.TrimSpace(c.Query("status")); status != "" && status != "all" {
		query = query.Where("status = ?", status)
	}
	if raw := strings.TrimSpace(c.Query("affiliate_id")); raw != "" {
		aid, err := strconv.Atoi(raw)
		if err != nil || aid <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		query = query.Where("affiliate_id = ?", aid)
	}
	if raw := strings.TrimSpace(c.Query("affiliate_user_id")); raw != "" {
		uid, err := strconv.Atoi(raw)
		if err != nil || uid <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		query = query.Where("affiliate_user_id = ?", uid)
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		userSub := func() *gorm.DB {
			return model.DB.Unscoped().Table("users").Select("id").Where("LOWER(username) LIKE ?", like)
		}
		if id, err := strconv.Atoi(strings.TrimPrefix(q, "#")); err == nil && id > 0 {
			query = query.Where(
				"(LOWER(source_trade_no) LIKE ? OR affiliate_user_id = ? OR invitee_user_id = ? OR affiliate_user_id IN (?) OR invitee_user_id IN (?))",
				like, id, id, userSub(), userSub(),
			)
		} else {
			query = query.Where(
				"(LOWER(source_trade_no) LIKE ? OR affiliate_user_id IN (?) OR invitee_user_id IN (?))",
				like, userSub(), userSub(),
			)
		}
	}
	var rows []model.ReferralCommission
	pageInfo, ok := referralPageRows(c, query, &rows)
	if !ok {
		return
	}
	ids := make([]int, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.AffiliateUserId, row.InviteeUserId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]referralCommissionAdminItem, len(rows))
	for i, row := range rows {
		items[i] = referralCommissionAdminItem{
			ReferralCommission: row,
			AffiliateUsername:  names[row.AffiliateUserId],
			InviteeUsername:    names[row.InviteeUserId],
		}
	}
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetReferralCommissionJobs 列出计佣任务。
func GetReferralCommissionJobs(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	query := service.ScopeAdtyReferralAffiliateIDsQuery(model.DB.Model(&model.ReferralCommissionJob{}), "affiliate_id", siteID)
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	var items []model.ReferralCommissionJob
	referralPage(c, query, &items)
}

// RetryReferralCommissionJob 立即重试一个计佣任务。
func RetryReferralCommissionJob(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	var body struct {
		TradeNo string `json:"trade_no"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || body.TradeNo == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.EnsureAdtyReferralTradeScope(siteID, body.TradeNo); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.ProcessAdtyTopUpCommission(body.TradeNo); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// RunReferralSettlement 手动跑一轮结算。
func RunReferralSettlement(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	settled, err := service.RunReferralSettlementOnceForSite(time.Now().Unix(), 500, siteID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"settled": settled})
}

// GetReferralLedgers 列出返佣流水。
func GetReferralLedgers(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	query := service.ScopeAdtyReferralUsersQuery(model.DB.Model(&model.ReferralCommissionLedger{}), "user_id", siteID)
	if raw := strings.TrimSpace(c.Query("affiliate_id")); raw != "" {
		aid, err := strconv.Atoi(raw)
		if err != nil || aid <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		query = query.Where("affiliate_id = ?", aid)
	}
	var rows []model.ReferralCommissionLedger
	pageInfo, ok := referralPageRows(c, query, &rows)
	if !ok {
		return
	}
	ids := make([]int, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.UserId)
		if opID, err := strconv.Atoi(strings.TrimSpace(row.Operator)); err == nil && opID > 0 {
			ids = append(ids, opID)
		}
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]referralLedgerAdminItem, len(rows))
	for i, row := range rows {
		opName := ""
		if opID, err := strconv.Atoi(strings.TrimSpace(row.Operator)); err == nil && opID > 0 {
			opName = names[opID]
		}
		items[i] = referralLedgerAdminItem{
			ReferralCommissionLedger: row,
			Username:                 names[row.UserId],
			OperatorUsername:         opName,
		}
	}
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetReferralAdminAuditLogs 列出推广管理的操作记录。
func GetReferralAdminAuditLogs(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	query := model.DB.Model(&model.ReferralAdminAuditLog{})
	if siteID != nil {
		if *siteID == 0 {
			query = query.Where("target_user_id = 0 OR target_user_id IN (?)", model.DB.Model(&model.User{}).Select("id").Where("site_id = ?", 0))
		} else {
			query = service.ScopeAdtyReferralUsersQuery(query.Where("target_user_id > 0"), "target_user_id", siteID)
		}
	}
	var rows []model.ReferralAdminAuditLog
	pageInfo, ok := referralPageRows(c, query, &rows)
	if !ok {
		return
	}
	ids := make([]int, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.AdminUserId, row.TargetUserId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]referralAdminAuditLogItem, len(rows))
	for i, row := range rows {
		items[i] = referralAdminAuditLogItem{
			ReferralAdminAuditLog: row,
			AdminUsername:         names[row.AdminUserId],
			TargetUsername:        names[row.TargetUserId],
		}
	}
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetAdminReferralWithdrawals 列出提现，收款码和凭证链接重新签名。
func GetAdminReferralWithdrawals(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	query := service.ScopeAdtyReferralUsersQuery(model.DB.Model(&model.ReferralWithdrawal{}), "user_id", siteID)
	if status := strings.TrimSpace(c.Query("status")); status == "actionable" {
		query = query.Where("status IN ?", []string{model.ReferralWithdrawalStatusPending, model.ReferralWithdrawalStatusApproved})
	} else if status != "" {
		query = query.Where("status = ?", status)
	}
	var rows []model.ReferralWithdrawal
	pageInfo, ok := referralPageRows(c, query, &rows)
	if !ok {
		return
	}
	resignWithdrawalAssets(rows)
	ids := make([]int, 0, len(rows)*4)
	for _, row := range rows {
		ids = append(ids, row.UserId, row.ReviewedBy, row.RejectedBy, row.CanceledBy)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]referralWithdrawalAdminItem, len(rows))
	for i, row := range rows {
		items[i] = referralWithdrawalAdminItem{
			ReferralWithdrawal: row,
			Username:           names[row.UserId],
			ReviewedByUsername: names[row.ReviewedBy],
			RejectedByUsername: names[row.RejectedBy],
			CanceledByUsername: names[row.CanceledBy],
		}
	}
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// ReviewReferralWithdrawal 通过或驳回提现（推广返佣·审核）。
func ReviewReferralWithdrawal(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.EnsureAdtyReferralWithdrawalScope(siteID, id); err != nil {
		common.ApiError(c, err)
		return
	}
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !body.Approve && strings.TrimSpace(body.Reason) == "" {
		common.ApiErrorT(c, "Enter a reason for rejecting")
		return
	}
	if err := service.Referral.ReviewWithdrawalForActor(id, c.GetInt("id"), c.GetInt("role"), isAccessTokenRequest(c), body.Approve, body.Reason, c.ClientIP(), c.Request.UserAgent()); err != nil {
		writeReferralError(c, err)
		return
	}
	recordManageAudit(c, "adty.referral.withdrawal_review", map[string]any{"id": id, "approve": body.Approve})
	common.ApiSuccess(c, nil)
}

// PayReferralWithdrawal 标记已打款（推广返佣·打款）。
func PayReferralWithdrawal(c *gin.Context) {
	siteID, ok := referralAdminScopeOrError(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.EnsureAdtyReferralWithdrawalScope(siteID, id); err != nil {
		common.ApiError(c, err)
		return
	}
	var body struct {
		TxnNo    string `json:"txn_no"`
		ProofURL string `json:"proof_url"`
		Note     string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.Referral.MarkWithdrawalPaidForActor(id, c.GetInt("id"), c.GetInt("role"), isAccessTokenRequest(c), body.TxnNo, body.ProofURL, body.Note, c.ClientIP(), c.Request.UserAgent()); err != nil {
		writeReferralError(c, err)
		return
	}
	recordManageAudit(c, "adty.referral.withdrawal_pay", map[string]any{"id": id})
	common.ApiSuccess(c, nil)
}
