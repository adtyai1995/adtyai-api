package controller

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// 推广返佣的用户侧接口（任务 6.1–6.4）。

// PostAdtyReferralClick 记点击并写签名 cookie（不用登录）。每次点击都覆盖，以最后一次为准。
func PostAdtyReferralClick(c *gin.Context) {
	if service.AdtyRequestSiteID(c) != 0 {
		writeReferralError(c, service.ErrReferralDisabled)
		return
	}
	var body struct {
		Code        string `json:"code"`
		LandingPath string `json:"landing_path"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	affiliate, err := service.Referral.RecordClick(body.Code, body.LandingPath, c.Request.Referer(), c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		writeReferralError(c, err)
		return
	}
	if err := service.SetAdtyReferralCookie(c, affiliate.InviteCode); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"code": affiliate.InviteCode})
}

// GetAdtyReferralPublicSettings 是推广计划页要的公开数字。
func GetAdtyReferralPublicSettings(c *gin.Context) {
	if service.AdtyRequestSiteID(c) != 0 {
		common.ApiSuccess(c, gin.H{"enabled": false})
		return
	}
	cfg := adty_setting.GetReferralSetting()
	hasRate := false
	if _, _, err := service.CurrentWithdrawCNYRate(); err == nil {
		hasRate = true
	} else if !errors.Is(err, service.ErrReferralRateUnset) {
		common.ApiError(c, err)
		return
	}
	methods, networks := cfg.UsableWithdrawConfig(hasRate)
	common.ApiSuccess(c, gin.H{
		"enabled":                cfg.Enabled,
		"default_rate":           cfg.DefaultRate,
		"partner_min_rate":       cfg.PartnerMinRate,
		"partner_max_rate":       cfg.PartnerMaxRate,
		"freeze_days":            cfg.FreezeDays,
		"min_withdraw_usd":       cfg.MinWithdrawUSD,
		"usdt_withdraw_fee":      cfg.USDTWithdrawFee,
		"cookie_ttl_days":        cfg.CookieTTLDays,
		"withdraw_methods":       methods,
		"withdraw_usdt_networks": networks,
	})
}

type referralProfile struct {
	Affiliate            *model.ReferralAffiliate         `json:"affiliate"`
	Account              *model.ReferralCommissionAccount `json:"account"`
	Rate                 string                           `json:"rate"`
	PartnerMinRate       *float64                         `json:"partner_min_rate"` // null：未设置
	PartnerMaxRate       *float64                         `json:"partner_max_rate"` // null：未设置
	FreezeDays           int                              `json:"freeze_days"`
	MinWithdrawUSD       *float64                         `json:"min_withdraw_usd"`  // null：未设置
	USDTWithdrawFee      *float64                         `json:"usdt_withdraw_fee"` // null：未设置
	WithdrawMethods      []string                         `json:"withdraw_methods"`
	WithdrawUSDTNetworks []string                         `json:"withdraw_usdt_networks"`
	SiteOwner            bool                             `json:"site_owner"` // 开了分站，邀请返佣停止
	CancelWindowMins     int                              `json:"cancel_window_minutes"`
	CNYRate              string                           `json:"cny_rate"`
	CNYRateUpdatedAt     int64                            `json:"cny_rate_updated_at"`
	Enabled              bool                             `json:"enabled"`
	Funnel               map[string]any                   `json:"funnel"`
}

// GetReferralProfile 返回推广资料：推广员（没有就兜底建一个）、账户、比例、规则、提现汇率（查库）。
func GetReferralProfile(c *gin.Context) {
	userID := c.GetInt("id")
	affiliate, err := service.Referral.EnsureAffiliate(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var account model.ReferralCommissionAccount
	if err := model.DB.Where("affiliate_id = ?", affiliate.Id).First(&account).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	cfg := adty_setting.GetReferralSetting()
	redactedAffiliate := redactReferralAffiliateForUser(affiliate)
	profile := referralProfile{
		Affiliate: redactedAffiliate, Account: &account,
		PartnerMinRate: nil, PartnerMaxRate: nil,
		FreezeDays: cfg.FreezeDays, MinWithdrawUSD: cfg.MinWithdrawUSD, USDTWithdrawFee: cfg.USDTWithdrawFee,
		CancelWindowMins: cfg.CancelWindowMinutes, Enabled: cfg.Enabled, SiteOwner: affiliate.SiteOwnerSince > 0,
	}
	if rate, ok := service.AffiliateRate(affiliate); ok {
		profile.Rate = rate.String()
	}
	hasRate := false
	if rate, updatedAt, err := service.CurrentWithdrawCNYRate(); err == nil {
		profile.CNYRate, profile.CNYRateUpdatedAt = rate.String(), updatedAt
		hasRate = true
	} else if !errors.Is(err, service.ErrReferralRateUnset) {
		common.ApiError(c, err)
		return
	}
	profile.WithdrawMethods, profile.WithdrawUSDTNetworks = cfg.UsableWithdrawConfig(hasRate)
	var clicks, signups, paying int64
	var earned decimal.Decimal
	if err := model.DB.Model(&model.ReferralClick{}).Where("affiliate_id = ?", affiliate.Id).Count(&clicks).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Model(&model.ReferralBinding{}).Where("affiliate_id = ?", affiliate.Id).Count(&signups).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Model(&model.ReferralCommission{}).Where("affiliate_id = ? AND status NOT IN ?", affiliate.Id,
		[]string{model.ReferralCommissionStatusVoid, model.ReferralCommissionStatusClawedBack}).Distinct("invitee_user_id").Count(&paying).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Model(&model.ReferralCommission{}).Where("affiliate_id = ? AND status NOT IN ?", affiliate.Id,
		[]string{model.ReferralCommissionStatusVoid, model.ReferralCommissionStatusClawedBack}).Select("COALESCE(SUM(commission_amount), 0)").Scan(&earned).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	profile.Funnel = map[string]any{"clicks": clicks, "signups": signups, "paying": paying, "earned": earned.StringFixed(2)}
	common.ApiSuccess(c, profile)
}

// ApplyReferralPartner 合作伙伴申请已下线（R-34 #2）。
func ApplyReferralPartner(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Partner applications are disabled"))
}

// GetReferralCommissions 分页列出本人的佣金。
func GetReferralCommissions(c *gin.Context) {
	affiliate, err := service.Referral.EnsureAffiliate(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo := common.GetPageQuery(c)
	var items []model.ReferralCommission
	var total int64
	query := model.DB.Model(&model.ReferralCommission{}).Where("affiliate_id = ?", affiliate.Id)
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := query.Order("id DESC").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&items).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetReferralWithdrawals 分页列出本人的提现；收款码和凭证链接重新签名。
func GetReferralWithdrawals(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	var items []model.ReferralWithdrawal
	var total int64
	query := model.DB.Model(&model.ReferralWithdrawal{}).Where("user_id = ?", c.GetInt("id"))
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := query.Order("id DESC").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&items).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	resignWithdrawalAssets(items)
	for i := range items {
		redactReferralWithdrawalForUser(&items[i])
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func redactReferralAffiliateForUser(affiliate *model.ReferralAffiliate) *model.ReferralAffiliate {
	if affiliate == nil {
		return nil
	}
	copyVal := *affiliate
	copyVal.RiskNote = ""
	copyVal.ApprovedBy = 0
	copyVal.DisabledBy = 0
	return &copyVal
}

func redactReferralWithdrawalForUser(w *model.ReferralWithdrawal) {
	if w == nil {
		return
	}
	w.AdminNote = ""
	w.ReviewedBy = 0
	w.RejectedBy = 0
	w.CanceledBy = 0
}

func resignWithdrawalAssets(items []model.ReferralWithdrawal) {
	now := time.Now()
	for i := range items {
		items[i].QRImageURL = service.ResignReferralAssetURLForWithdrawal(items[i].QRImageURL, items[i].UserId, model.ReferralAssetPurposeWithdrawalQR, now)
		items[i].PaymentProofURL = service.ResignReferralAssetURLForWithdrawal(items[i].PaymentProofURL, 0, model.ReferralAssetPurposePaymentProof, now)
	}
}

type referralWithdrawRequest struct {
	Amount          decimal.Decimal `json:"amount"`
	AccountType     string          `json:"account_type"`
	AccountNetwork  string          `json:"account_network"`
	AccountNo       string          `json:"account_no"`
	AccountName     string          `json:"account_name"`
	QRImageURL      string          `json:"qr_image_url"`
	ExpectedCNYRate decimal.Decimal `json:"expected_cny_rate"`
	IdempotencyKey  string          `json:"idempotency_key"`
}

// CreateReferralWithdrawal 提交提现。人民币汇率变了时返回 rate_changed 和新的汇率、人民币金额。
func CreateReferralWithdrawal(c *gin.Context) {
	var req referralWithdrawRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	withdrawal, err := service.Referral.CreateWithdrawal(service.ReferralWithdrawalCreateInput{
		UserId: c.GetInt("id"), Amount: req.Amount, AccountType: req.AccountType, AccountNetwork: req.AccountNetwork,
		AccountNo: req.AccountNo, AccountName: req.AccountName, QRImageURL: req.QRImageURL,
		ExpectedCNYRate: req.ExpectedCNYRate, IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		var changed *service.RateChangedError
		if errors.As(err, &changed) {
			common.ApiErrorStatus(c, http.StatusOK, common.NewMessage("The exchange rate has changed. Confirm the new amount"), gin.H{"code": "rate_changed", "data": changed.Change})
			return
		}
		writeReferralError(c, err)
		return
	}
	if withdrawal != nil {
		redacted := *withdrawal
		redactReferralWithdrawalForUser(&redacted)
		common.ApiSuccess(c, &redacted)
		return
	}
	common.ApiSuccess(c, withdrawal)
}

// CancelReferralWithdrawal 在撤回窗口内撤回提现。
func CancelReferralWithdrawal(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.Referral.CancelWithdrawal(id, c.GetInt("id"), time.Now().Unix()); err != nil {
		writeReferralError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// UploadReferralAsset 上传收款码（用户）或打款凭证（后台），最大 5 MB，只收 PNG、JPEG、WebP。
func UploadReferralAsset(c *gin.Context) {
	purpose := model.ReferralAssetPurposeWithdrawalQR
	createdBy := "user"
	if strings.HasPrefix(c.FullPath(), "/api/user/admin/") {
		purpose, createdBy = model.ReferralAssetPurposePaymentProof, "admin"
	}
	header, err := c.FormFile("file")
	if err != nil {
		common.ApiErrorT(c, "Choose an image to upload")
		return
	}
	file, err := header.Open()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 5<<20+1))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(data) > 5<<20 {
		common.ApiErrorT(c, "The image must be at most 5 MB")
		return
	}
	link, err := service.SaveReferralAsset(data, http.DetectContentType(data), c.GetInt("id"), purpose, createdBy)
	if err != nil {
		common.ApiErrorT(c, "Only PNG, JPEG or WebP images are accepted")
		return
	}
	common.ApiSuccess(c, gin.H{"url": link})
}

// GetReferralAsset 按签名链接返回图片（不用登录）。
func GetReferralAsset(c *gin.Context) {
	path, err := service.OpenReferralAsset(c.Param("path"), c.Query("expires"), c.Query("sig"), time.Now())
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "private, max-age=60")
	c.File(path)
}

func writeReferralError(c *gin.Context, err error) {
	var message *common.Message
	switch {
	case errors.Is(err, model.ErrAdminTargetSessionRequired):
		denyAdtyAdminAction(c)
	case errors.Is(err, model.ErrUserQuotaPermission):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
	case errors.As(err, &message):
		common.ApiError(c, err)
	case errors.Is(err, service.ErrReferralDisabled):
		common.ApiErrorT(c, "The referral program is not open")
	case errors.Is(err, service.ErrReferralCodeInvalid):
		common.ApiErrorT(c, "This referral link is invalid")
	case errors.Is(err, service.ErrReferralInsufficient):
		common.ApiErrorT(c, "Not enough withdrawable commission")
	case errors.Is(err, service.ErrReferralWithdrawBlocked):
		common.ApiErrorT(c, "Withdrawals are paused for this account")
	case errors.Is(err, service.ErrReferralRateUnset):
		common.ApiErrorT(c, "CNY withdrawals are not available yet")
	case errors.Is(err, service.ErrReferralWithdrawUnavailable):
		common.ApiErrorT(c, "This withdrawal method is not available yet")
	case errors.Is(err, model.ErrAdtyIdempotencyConflict):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This request was already used for another operation"))
	default:
		common.ApiError(c, err)
	}
}
