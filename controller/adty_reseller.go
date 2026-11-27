package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// 站长侧的分站接口（P11）。站长中心的其余接口在 11.6。
// 方案里写的 controller/adty_site.go 已经是主站站点配置接口，分站的站长侧接口放这个文件。

// ApplyAdtySite rejects the retired self-service application endpoint.
// Root creates sites by selecting an existing main-site user in the admin API.
func ApplyAdtySite(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Unauthorized, insufficient privileges"))
}

// RequestAdtySiteClose 站长申请关站：POST /api/adty/site/close {reason}。客户去向由管理员定。
func RequestAdtySiteClose(c *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	site, err := service.RequestAdtySiteClose(c.GetInt("id"), body.Reason)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"id": site.Id, "close_requested_at": site.CloseRequestedAt})
}

// GetAdtySiteDomains 列出站长自己站上的自定义域名（任务 11.8），带要加的 DNS 记录和状态。
func GetAdtySiteDomains(c *gin.Context) {
	domains, err := service.ListAdtySiteDomains(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, domains)
}

// AddAdtySiteDomain 绑定自定义域名：POST /api/adty/site/domains {hostname, role}（任务 11.8）。
// role 选填：site（站点域名，默认）或 base_url（API 地址，只放行 /v1 这类接口）。
func AddAdtySiteDomain(c *gin.Context) {
	var body struct {
		Hostname string `json:"hostname"`
		Role     string `json:"role"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	domain, err := service.AddAdtySiteDomainWithRole(c.Request.Context(), c.GetInt("id"), body.Hostname, body.Role)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, domain)
}

// AddAdtySiteDomainPair 同时绑定站点域名和 API 地址：POST /api/adty/site/domains/pair {site_hostname, api_hostname}。
func AddAdtySiteDomainPair(c *gin.Context) {
	var body struct {
		SiteHostname string `json:"site_hostname"`
		APIHostname  string `json:"api_hostname"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	domains, err := service.AddAdtySiteDomainPair(c.Request.Context(), c.GetInt("id"), body.SiteHostname, body.APIHostname)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, domains)
}

// CheckAdtySiteDomainNow 站长立即检测自定义域名 DNS 或边缘证书并触发一次对账：POST /api/adty/site/domains/:id/check。
func CheckAdtySiteDomainNow(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if c.Request.Body != nil {
		if err := common.DecodeJson(c.Request.Body, &body); err != nil && !errors.Is(err, io.EOF) {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if mode != "" && mode != "dns" && mode != "edge_cert" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 75*time.Second)
	defer cancel()
	domain, err := service.CheckAdtySiteDomainNowWithMode(ctx, c.GetInt("id"), id, mode)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, domain)
}

// RemoveAdtySiteDomain 删除自定义域名：DELETE /api/adty/site/domains/:id（任务 11.8）。
func RemoveAdtySiteDomain(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	if err := service.RemoveAdtySiteDomain(c.Request.Context(), c.GetInt("id"), id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"id": id})
}

// AdtyPayResult 是易支付付完跳回的主站页面：GET /pay/result/:token，302 到订单所属站点的钱包（任务 11.3）。
func AdtyPayResult(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, service.AdtyPayResultTarget(c.Param("token")))
}

// GetAdtySiteSenderDomain 是站长的发件域名和要加的 DNS 记录（任务 11.3）；没有时 data 为 null。
func GetAdtySiteSenderDomain(c *gin.Context) {
	domain, err := service.GetAdtySiteSenderDomain(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if domain != nil {
		sanitized := *domain
		sanitized.DisabledByPlatform = sanitized.DisabledBy > 0
		sanitized.DisabledBy = 0
		domain = &sanitized
	}
	common.ApiSuccess(c, domain)
}

// AddAdtySiteSenderDomain 托管 Resend 发件域名已退役（R-37 #5），请改用站长自有 Resend API Key 或自定义 SMTP。
func AddAdtySiteSenderDomain(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Managed Resend sender domains have been retired. Configure your own Resend API key or SMTP in site email settings"))
}

// RemoveAdtySiteSenderDomain 删除发件域名，回到平台发件地址：DELETE /api/adty/site/sender-domain（任务 11.3）。
func RemoveAdtySiteSenderDomain(c *gin.Context) {
	if err := service.RemoveAdtySiteSenderDomain(c.Request.Context(), c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// DisableAdtyAdminSiteDomain 是管理员停用一个自定义域名（分站·调整）。
func DisableAdtyAdminSiteDomain(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	if err := service.RemoveAdtySiteDomainByAdminForActor(c.Request.Context(), id, c.GetInt("id"), c.GetInt("role"), isAccessTokenRequest(c)); err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	recordManageAudit(c, "adty.site.domain_disable", map[string]any{"domain_id": id})
	common.ApiSuccess(c, nil)
}

// DisableAdtyAdminSiteSenderDomain 是管理员停用一个站的发件域名（分站·调整，任务 11.3、11.9）。
func DisableAdtyAdminSiteSenderDomain(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	if err := service.DisableAdtySiteSenderDomainForActor(c.Request.Context(), id, c.GetInt("id"), c.GetInt("role"), isAccessTokenRequest(c)); err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	recordManageAudit(c, "adty.site.sender_domain_disable", map[string]any{"site_id": id})
	common.ApiSuccess(c, nil)
}
