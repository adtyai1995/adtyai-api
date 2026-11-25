package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

// GetAdtyLegal 返回一份条款（任务 8.6）：GET /api/adty/legal/:doc?lang=，doc 为
// user-agreement、privacy-policy、refund-policy。
// 分站只读自己的多语言条款（严格隔离，绝不回退到主站）；
// 主站按「本语言 → en → zh-CN → 首个非空语言 → 上游 legal.*」取第一个非空的（退款政策无上游回退）。
// 返回格式和 ETag 跟上游 /api/user-agreement 一样。
func GetAdtyLegal(c *gin.Context) {
	doc := c.Param("doc")
	lang := service.AdtyRequestLegalLang(c)

	if site := service.AdtySiteForRequest(c); site != nil {
		legal, err := service.AdtySiteLegalForWithError(site)
		if err != nil {
			common.SysError(common.LogText("failed to load legal docs for site %d: %s", site.Id, err.Error()))
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Site legal documents are temporarily unavailable"))
			return
		}
		var localized map[string]string
		switch doc {
		case "user-agreement":
			localized = legal.UserAgreement
		case "privacy-policy":
			localized = legal.PrivacyPolicy
		case "refund-policy":
			if !legal.RefundPolicyEnabled {
				serveRevalidatedJSON(c, "")
				return
			}
			localized = legal.RefundPolicy
		default:
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Invalid parameters"))
			return
		}
		serveRevalidatedJSON(c, service.AdtyResolveLegalText(localized, lang))
		return
	}

	texts := adty_setting.GetLegalSetting()
	upstream := system_setting.GetLegalSettings()
	var localized map[string]string
	var fallback string
	switch doc {
	case "user-agreement":
		localized, fallback = texts.UserAgreement, upstream.UserAgreement
	case "privacy-policy":
		localized, fallback = texts.PrivacyPolicy, upstream.PrivacyPolicy
	case "refund-policy":
		if !adty_setting.GetSiteSetting().RefundPolicyEnabled {
			serveRevalidatedJSON(c, "")
			return
		}
		localized = texts.RefundPolicy
	default:
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Invalid parameters"))
		return
	}

	if text := service.AdtyResolveLegalText(localized, lang); text != "" {
		serveRevalidatedJSON(c, text)
		return
	}
	serveRevalidatedJSON(c, fallback)
}
