package controller

import (
	"net/http"
	"net/mail"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// adtySiteView 是站点配置加上首页要显示的公开数字。
type adtySiteView struct {
	adty_setting.SiteSetting
	// MinTopUp 是后台「充值设置」的最低充值金额（美元），首页数字条显示「$N 起充」。
	// 充值接口 /api/adty/topup/info 要登录，访客读不到，所以在这里公开一份。
	MinTopUp       int    `json:"min_topup"`
	SupportContact string `json:"support_contact,omitempty"`
}

// GetAdtySite 是公开的站点配置（任务 8.3）：首页、文档、快速接入卡、客服和退款政策开关都读它。
func GetAdtySite(c *gin.Context) {
	settings := *adty_setting.GetSiteSetting()
	settings.APIBaseURL = service.AdtySiteAPIBaseURL(nil)
	var contact string
	if site := service.AdtySiteForRequest(c); site != nil {
		brand := service.AdtyBrandFor(site)
		legal, err := service.AdtySiteLegalForWithError(site)
		if err != nil {
			common.SysError(common.LogText("failed to load legal docs for site %d: %s", site.Id, err.Error()))
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Site legal documents are temporarily unavailable"))
			return
		}
		settings = adty_setting.SiteSetting{
			APIBaseURL:          service.AdtySiteAPIBaseURL(site),
			TawkPropertyID:      brand.TawkPropertyID,
			TawkWidgetID:        brand.TawkWidgetID,
			WalletNotice:        brand.WalletNotice,
			VoucherPurchaseURL:  brand.VoucherPurchaseURL,
			RefundPolicyEnabled: legal.RefundPolicyEnabled,
		}
		contact = strings.TrimSpace(brand.SupportContact)
		if addr, err := mail.ParseAddress(contact); err == nil && addr.Address == contact {
			settings.SupportEmail = addr.Address
		}
	}
	common.ApiSuccess(c, adtySiteView{
		SiteSetting:    settings,
		SupportContact: contact,
		MinTopUp:       operation_setting.GetMinTopUp(),
	})
}
