package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var errAdtyBonusSiteForbidden = errors.New("site is not active for bonus rule changes")

type adtyMethodBonusBody struct {
	Method  string `json:"method"`
	Enabled bool   `json:"enabled"`
	Rate    string `json:"rate"`
	Allowed *bool  `json:"allowed"`
}

// saveAdtyBonusMethodOption 把一个加赠配置写进 options，并与审计同事务。
//
// 先落库、提交后由配置层按落库值更新内存：写库失败时内存保持旧值，接口返回
// 错误，不会出现「界面显示已保存、重启后配置消失」。
//
// 合并基准在事务内用行锁从库里读（merge 收到的 current），不取本进程的内存
// 快照：整串 JSON 覆盖写时，取内存当基准会让另一个副本刚写入的规则被静默抹掉
// （ISS-R4-0099）。
func saveAdtyBonusMethodOption(c *gin.Context, key, action string, params map[string]any, after any, merge func(current string) (string, error), authorize ...func(*gorm.DB) error) bool {
	event := prepareManageAuditChange(c, 0, action, params, nil, after, "", "")
	var value string
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		for _, check := range authorize {
			if err := check(tx); err != nil {
				return err
			}
		}
		current, err := model.LockOptionValueTx(tx, key)
		if err != nil {
			return err
		}
		merged, err := merge(current)
		if err != nil {
			return err
		}
		if err := model.SaveOptionsTx(tx, map[string]string{key: merged}); err != nil {
			return err
		}
		value = merged
		return model.AppendAuditIntentTx(tx, event)
	})
	if err != nil {
		// R1-037: Business/authorization validation failures return 400/403 instead of 500.
		if errors.Is(err, errAdtyBonusSiteForbidden) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("This site is not active and cannot change bonus rules"))
			return false
		}
		if adty_setting.IsBonusMethodValidationError(err) {
			common.ApiErrorStatus(c, http.StatusForbidden, err)
			return false
		}
		// 写库失败必须是 500：这条接口以前谎报 success，配置静默丢失。
		common.ApiErrorStatus(c, http.StatusInternalServerError, err)
		return false
	}
	if err := model.ApplyOptionValues(map[string]string{key: value}); err != nil {
		common.SysError(common.LogText("apply %s after it was stored: %s", key, err.Error()))
	}
	deliverCommittedAudits(c, []model.AuditLog{event})
	markAuditLogged(c)
	return true
}

// GetAdtyMainMethodBonus returns main-site payment-method gift rules for Root (R1-040).
func GetAdtyMainMethodBonus(c *gin.Context) {
	common.ApiSuccess(c, adty_setting.MainMethodBonusMap())
}

// PutAdtyMainMethodBonus lets Root set one main-site payment-method gift rule.
func PutAdtyMainMethodBonus(c *gin.Context) {
	var body adtyMethodBonusBody
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	merge := func(current string) (string, error) {
		return adty_setting.MergeMainMethodBonus(current, body.Method, body.Enabled, body.Rate)
	}
	// 非法参数要在事务外就变成 400：事务里的错误一律按写库失败回 500。
	if _, err := merge(""); err != nil {
		common.ApiError(c, err)
		return
	}
	if !saveAdtyBonusMethodOption(c, adty_setting.TopupSettingKeyMethodBonus, "adty.topup.method_bonus.update",
		map[string]any{"method": body.Method, "enabled": body.Enabled, "rate": body.Rate},
		map[string]any{"method": body.Method, "enabled": body.Enabled}, merge) {
		return
	}
	common.ApiSuccess(c, adty_setting.MainMethodBonusMap())
}

// GetAdtySiteBonusMarginFill returns Root's margin-fill authorization for one site (R1-040).
func GetAdtySiteBonusMarginFill(c *gin.Context) {
	siteID, err := strconv.Atoi(c.Param("id"))
	if err != nil || siteID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	common.ApiSuccess(c, gin.H{"site_id": siteID, "enabled": adty_setting.SiteBonusMarginFillAllowed(siteID)})
}

// PutAdtySiteBonusMarginFill lets Root toggle margin-fill authorization for one site (R1-040).
func PutAdtySiteBonusMarginFill(c *gin.Context) {
	siteID, err := strconv.Atoi(c.Param("id"))
	if err != nil || siteID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || body.Enabled == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	merge := func(current string) (string, error) {
		return adty_setting.MergeSiteBonusMarginFill(current, siteID, *body.Enabled)
	}
	if !saveAdtyBonusMethodOption(c, adty_setting.TopupSettingKeySiteBonusMarginFill, "adty.topup.site_bonus_margin_fill.update",
		map[string]any{"site_id": siteID, "enabled": *body.Enabled},
		map[string]any{"site_id": siteID, "enabled": *body.Enabled}, merge) {
		return
	}
	common.ApiSuccess(c, gin.H{"site_id": siteID, "enabled": *body.Enabled})
}

// PutAdtySiteBonusMethodAuth is deprecated: subsites no longer require per-site
// payment-method bonus authorization from the main site.
func PutAdtySiteBonusMethodAuth(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Per-site payment method bonus authorization has been removed"))
}

// PutAdtySiteMethodBonus lets an active site owner configure a gift rule for any
// payment method currently enabled on the main site (or disable an existing rule
// even if the main site disabled that method later).
func PutAdtySiteMethodBonus(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	// R1-037 / R8-025: Only active sites may change bonus rules (blocks paused, closing, closed).
	if site.Status != model.AdtySiteStatusActive {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("This site is not active and cannot change bonus rules"))
		return
	}
	var body adtyMethodBonusBody
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if _, err := adty_setting.MergeMainMethodBonus("", body.Method, body.Enabled, body.Rate); err != nil {
		common.ApiError(c, err)
		return
	}
	if body.Enabled && !service.IsMainSiteBonusMethodEnabled(body.Method) {
		common.ApiError(c, common.NewMessage("This payment method is not enabled on the main site"))
		return
	}
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionBonus, site.Id) {
		return
	}
	actorID := c.GetInt("id")
	var txRef *gorm.DB
	merge := func(current string) (string, error) {
		// Lock AdtySite after options (site_method_bonus -> AdtySite)
		// matching CreateAdtyPaymentOrder canonical lock order to serialize with site close/pause.
		var locked model.AdtySite
		if err := model.AdtyLockForUpdate(txRef).Where("id = ?", site.Id).First(&locked).Error; err != nil {
			return "", err
		}
		if locked.OwnerUserId != actorID || locked.Status != model.AdtySiteStatusActive {
			return "", errAdtyBonusSiteForbidden
		}
		return adty_setting.MergeSiteMethodBonus(current, site.Id, body.Method, body.Enabled, body.Rate)
	}
	if !saveAdtyBonusMethodOption(c, adty_setting.TopupSettingKeySiteMethodBonus, "adty.topup.site_method_bonus.update",
		map[string]any{"method": body.Method, "enabled": body.Enabled, "rate": body.Rate, "site_id": site.Id},
		map[string]any{"method": body.Method, "enabled": body.Enabled}, merge, func(tx *gorm.DB) error {
			txRef = tx
			return nil
		}) {
		return
	}
	common.ApiSuccess(c, gin.H{"site_id": site.Id})
}
