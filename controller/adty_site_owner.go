package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// 站长中心（任务 11.6、11.7）。每个处理函数先取「当前用户是 active、paused 或 closing 站点的站长」，否则 403；
// 查询一律带 site_id = 本站。改价、改加赠、送试用额度、删除域名要重新验证身份（X-Security-Proof，
// scope site.owner.manage，绑定操作和目标）。

// adtyOwnerSiteOr403 取当前用户的站；不是站长时写 403。
func adtyOwnerSiteOr403(c *gin.Context) (*model.AdtySite, bool) {
	site, err := service.AdtyOwnerSite(c.GetInt("id"))
	if errors.Is(err, service.ErrAdtySiteNotOwner) {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtySiteNotOwner)
		return nil, false
	}
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	if !service.AdtySiteAgreementAccepted(site.AgreementVer) {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtySiteAgreement)
		return nil, false
	}
	return site, true
}

// Only historical accounting endpoints opt in to selecting a closed site.
func adtyOwnerAccountingSite(c *gin.Context) (*model.AdtySite, bool) {
	raw, provided := c.GetQuery("archived_site_id")
	if !provided {
		return adtyOwnerSiteOr403(c)
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, false
	}
	var site model.AdtySite
	result := service.ArchivedAdtyOwnerSites(model.DB, c.GetInt("id")).Where("id = ?", id).Limit(1).Find(&site)
	if result.Error != nil {
		common.ApiError(c, result.Error)
		return nil, false
	}
	if result.RowsAffected == 0 {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtySiteNotOwner)
		return nil, false
	}
	return &site, true
}

func GetAdtyArchivedOwnerSites(c *gin.Context) {
	type archivedSite struct {
		Id         int    `json:"id"`
		Subdomain  string `json:"subdomain"`
		RootDomain string `json:"root_domain"`
		ClosedAt   int64  `json:"closed_at"`
	}
	var rows []archivedSite
	err := service.ArchivedAdtyOwnerSites(model.DB, c.GetInt("id")).Select("id, subdomain, root_domain, closed_at").Order("closed_at DESC, id DESC").Find(&rows).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func GetAdtyArchivedWebsiteDomains(c *gin.Context) {
	siteID, err := strconv.Atoi(c.Param("site_id"))
	if err != nil || siteID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	rows, err := service.ListAdtyArchivedWebsiteDomains(c.GetInt("id"), siteID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func DeleteAdtyArchivedWebsiteDomain(c *gin.Context) {
	siteID, err := strconv.Atoi(c.Param("site_id"))
	if err != nil || siteID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	id, ok := adtyPathId(c)
	if !ok || !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionDomain, id) {
		return
	}
	if err := service.RemoveAdtyArchivedWebsiteDomain(c.Request.Context(), c.GetInt("id"), siteID, id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// AcceptAdtySiteTerms 接受当前开站协议：POST /api/adty/site/terms/accept。不走站长中心的协议门。
func AcceptAdtySiteTerms(c *gin.Context) {
	var body struct {
		Version string `json:"version"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.AcceptAdtySiteTerms(c.GetInt("id"), body.Version); err != nil {
		if errors.Is(err, service.ErrAdtySiteNotOwner) || errors.Is(err, service.ErrAdtySiteAgreement) {
			common.ApiErrorStatus(c, http.StatusForbidden, err)
			return
		}
		common.ApiError(c, err)
		return
	}
	required, version := service.AdtySiteTermsStatus(c.GetInt("id"))
	common.ApiSuccess(c, gin.H{
		"site_terms_required": required,
		"site_terms_version":  version,
	})
}

// adtyRequireOwnerProof 要求这次站长操作带上有效的二次验证 proof；失败时已经写好响应。
func adtyRequireOwnerProof(c *gin.Context, action string, target int) bool {
	return middleware.RequireSecurityProof(c, service.AdtySiteOwnerOperation(action, target)) != nil
}

func adtyPathId(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	return id, true
}

func adtyPage(c *gin.Context) (*common.PageInfo, int, int) {
	pageInfo := common.GetPageQuery(c)
	return pageInfo, pageInfo.GetStartIdx(), pageInfo.GetPageSize()
}

// GetAdtyOwnerSite 是站点资料：GET /api/adty/site/self。站长能看到自己的 d（要会算利润），看不到别人的。
func GetAdtyOwnerSite(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	d, _ := service.AdtySiteCostFactor(site)
	// 站长编辑用的原始品牌字段（站名没填时为空，不像 AdtyBrandFor 那样回退成子域名）。
	var brand service.AdtySiteBrandInput
	if strings.TrimSpace(site.Brand) != "" {
		_ = common.UnmarshalJsonStr(site.Brand, &brand)
	}
	prices, version, err := service.LoadAdtySitePricing(site.Id, 0)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	domains, err := service.ListAdtySiteDomains(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cfg := adty_setting.GetResellerSetting()
	common.ApiSuccess(c, gin.H{
		"id": site.Id, "status": site.Status, "subdomain": site.Subdomain, "root_domain": site.RootDomain,
		"host": service.AdtySiteDisplayHost(site), "base_url": service.AdtySiteBaseURL(site), "api_base_url": service.AdtySiteAPIBaseURL(site),
		"cost_factor":           d.String(),
		"custom_domain_enabled": cfg.CustomDomainEnabled,
		"sender_domain_enabled": cfg.SenderDomainEnabled, "brand": brand, "bonus_methods": service.AdtySiteBonusMethods(site),
		"seo_index": site.SeoIndex, "agreement_version": site.AgreementVer, "agreement_at": site.AgreementAt,
		"close_requested_at": site.CloseRequestedAt, "close_execute_at": site.CloseExecuteAt,
		"prices": prices, "pricing_version": version, "domains": domains, "visibility": service.AdtySiteVisibility(site),
	})
}

// PutAdtyOwnerSite 改站点资料：PUT /api/adty/site/self {brand?, bonus_rates?, seo_index?}。
// 改加赠要二次验证（target = 站点 id）；品牌和 SEO 开关不用。站点 paused 时不能改加赠。
// bonus_rates 只为了把退役的金额档位回成明确错误，按支付方式的加赠走 PUT /adty/site/bonus-methods。
func PutAdtyOwnerSite(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	var body struct {
		Brand         map[string]string `json:"brand"`
		ExpectedBrand map[string]string `json:"expected_brand"`
		BonusRates    *map[int]float64  `json:"bonus_rates"`
		SeoIndex      *bool             `json:"seo_index"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if (body.Brand != nil || body.SeoIndex != nil) && site.Status == model.AdtySiteStatusClosing {
		common.ApiError(c, service.ErrAdtySiteClosing)
		return
	}
	if body.BonusRates != nil {
		if err := service.ValidateAdtySiteBonusRates(*body.BonusRates); err != nil {
			common.ApiError(c, err)
			return
		}
		if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionBonus, site.Id) {
			return
		}
		if err := service.SaveAdtySiteBonusRates(site.Id, *body.BonusRates); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if body.Brand != nil || body.SeoIndex != nil {
		if err := service.SaveAdtyBrandPatch(site.Id, c.GetInt("id"), body.Brand, body.ExpectedBrand, body.SeoIndex); err != nil {
			common.ApiError(c, err)
			return
		}
		service.InvalidateAdtySiteCache()
	}
	GetAdtyOwnerSite(c)
}

// GetAdtyOwnerStats 是经营数据：GET /api/adty/site/stats?from&to（日期 2006-01-02）。
// 站长前台不展示累计或区间充值汇总，所以响应里省略 topup_usd；内部账务仍按原样计算。
func GetAdtyOwnerStats(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	stats, err := service.GetAdtyOwnerStats(site.Id, c.GetInt("id"), c.Query("from"), c.Query("to"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, adtyOwnerVisibleStats(stats))
}

// adtyOwnerDayStats 是站长可见的按日经营数据：不含充值汇总。
type adtyOwnerDayStats struct {
	Day            string          `json:"day"`
	NewCustomers   int64           `json:"new_customers"`
	PaidCustomers  int64           `json:"paid_customers"`
	ActiveCustomer int64           `json:"active_customers"`
	RevenueUSD     decimal.Decimal `json:"revenue_usd"`
	PlatformUSD    decimal.Decimal `json:"platform_usd"`
	EarningUSD     decimal.Decimal `json:"earning_usd"`
	BonusUSD       decimal.Decimal `json:"bonus_usd"`
	RefundUSD      decimal.Decimal `json:"refund_usd"`
	SkippedUSD     decimal.Decimal `json:"skipped_usd"`
}

type adtyOwnerStats struct {
	From   string              `json:"from"`
	To     string              `json:"to"`
	Total  adtyOwnerDayStats   `json:"total"`
	Series []adtyOwnerDayStats `json:"series"`
}

func adtyOwnerDayStatsOf(day service.AdtySiteDayStats) adtyOwnerDayStats {
	return adtyOwnerDayStats{
		Day:            day.Day,
		NewCustomers:   day.NewCustomers,
		PaidCustomers:  day.PaidCustomers,
		ActiveCustomer: day.ActiveCustomer,
		RevenueUSD:     day.RevenueUSD,
		PlatformUSD:    day.PlatformUSD,
		EarningUSD:     day.EarningUSD,
		BonusUSD:       day.BonusUSD,
		RefundUSD:      day.RefundUSD,
		SkippedUSD:     day.SkippedUSD,
	}
}

func adtyOwnerVisibleStats(stats *service.AdtySiteStats) adtyOwnerStats {
	out := adtyOwnerStats{From: stats.From, To: stats.To, Total: adtyOwnerDayStatsOf(stats.Total)}
	out.Series = make([]adtyOwnerDayStats, len(stats.Series))
	for i, day := range stats.Series {
		out.Series[i] = adtyOwnerDayStatsOf(day)
	}
	return out
}

// adtyOwnerEarningRow 是站长看到的一条收益：skipped 只给原因类别（self_use | migration），不给判断依据。
type adtyOwnerEarningRow struct {
	Id        int64           `json:"id"`
	Day       string          `json:"day"`
	UserId    int             `json:"user_id"`
	Kind      string          `json:"kind"`
	Reason    string          `json:"reason,omitempty"`
	Revenue   decimal.Decimal `json:"revenue_usd"`
	Earning   decimal.Decimal `json:"earning_usd"`
	Status    string          `json:"status"`
	SettledAt int64           `json:"settled_at"`
}

// GetAdtyOwnerEarnings 是收益流水：GET /api/adty/site/earnings?kind&p&page_size，带待结算、可提现、已提现。
func GetAdtyOwnerEarnings(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	pageInfo, offset, limit := adtyPage(c)
	query := model.DB.Model(&model.AdtySiteEarningDaily{}).Scopes(model.AdtyFinancialOwnerScope(site.Id, c.GetInt("id")))
	if kind := strings.TrimSpace(c.Query("kind")); kind != "" {
		query = query.Where("kind = ?", kind)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var rows []model.AdtySiteEarningDaily
	if err := query.Order("day DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]adtyOwnerEarningRow, 0, len(rows))
	for _, row := range rows {
		items = append(items, adtyOwnerEarningRow{Id: row.Id, Day: row.Day, UserId: row.UserId, Kind: row.Kind, Reason: row.Reason,
			Revenue: row.Revenue, Earning: row.Earning, Status: row.Status, SettledAt: row.SettledAt})
	}
	balances, err := service.GetAdtyOwnerBalances(site, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, gin.H{"balances": balances, "page": pageInfo})
}

// GetAdtyOwnerEarningLines lists per-request earnings with model and billing group.
// The response has no upstream channel fields.
func GetAdtyOwnerEarningLines(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	_, offset, limit := adtyPage(c)
	rows, err := service.ListAdtyFinancialOwnerEarningLines(site.Id, c.GetInt("id"), offset, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

// GetAdtyOwnerCustomers 按注册时间分页列出客户：GET /api/adty/site/customers。
func GetAdtyOwnerCustomers(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	pageInfo, offset, limit := adtyPage(c)
	items, total, err := service.ListAdtyOwnerCustomers(site, offset, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// LookupAdtyOwnerCustomer 精确查找：GET /api/adty/site/customers/lookup?q=（完整用户名或邮箱，有次数限制）。
func LookupAdtyOwnerCustomer(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	customer, err := service.LookupAdtyOwnerCustomer(site, c.GetInt("id"), c.Query("q"))
	if errors.Is(err, service.ErrAdtySiteLookupLimited) {
		common.ApiErrorStatus(c, http.StatusTooManyRequests, err)
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, customer)
}

// GetAdtyOwnerCustomer 是客户详情：GET /api/adty/site/customers/:id（带密钥列表，customer_tokens 关着时不带）。
func GetAdtyOwnerCustomer(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	customer, err := service.GetAdtyOwnerCustomer(site, id)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusNotFound, err)
		return
	}
	result := gin.H{"customer": customer}
	if tokens, err := service.ListAdtyOwnerCustomerTokens(site, id); err == nil {
		result["tokens"] = tokens
	}
	// 客户单独定价和它的版本号（保存时带 expected_version）。
	prices, version, err := service.LoadAdtySitePricing(site.Id, id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result["prices"], result["pricing_version"] = prices, version
	common.ApiSuccess(c, result)
}

func adtyOwnerHiddenOrError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAdtySiteHidden):
		common.ApiErrorStatus(c, http.StatusForbidden, err)
	default:
		common.ApiErrorStatus(c, http.StatusNotFound, err)
	}
}

// GetAdtyOwnerCustomerLogs 是客户的请求记录：GET /api/adty/site/customers/:id/logs（request_logs 打开时）。
func GetAdtyOwnerCustomerLogs(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	pageInfo, offset, limit := adtyPage(c)
	items, total, err := service.ListAdtyOwnerCustomerLogs(site, id, offset, limit)
	if err != nil {
		adtyOwnerHiddenOrError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetAdtyOwnerCustomerTopUps 是客户的充值：GET /api/adty/site/customers/:id/topups（customer_topups 打开时）。
func GetAdtyOwnerCustomerTopUps(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	pageInfo, offset, limit := adtyPage(c)
	items, total, err := service.ListAdtyOwnerCustomerTopUps(site, id, offset, limit)
	if err != nil {
		adtyOwnerHiddenOrError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// PostAdtyOwnerTrial 送试用额度：POST /api/adty/site/customers/:id/trial {amount_usd, idempotency_key}，要二次验证。
func PostAdtyOwnerTrial(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	var body struct {
		AmountUSD      string `json:"amount_usd"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(body.AmountUSD))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionTrial, id) {
		return
	}
	grant, err := service.GrantAdtySiteTrial(site.Id, id, amount, body.IdempotencyKey)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordUserSecurityAudit(c, c.GetInt("id"), "adty.site.trial", map[string]any{"site_id": site.Id, "customer_id": id, "amount_usd": grant.AmountUSD.String()})
	common.ApiSuccess(c, grant)
}

// PostAdtyOwnerReport 举报客户：POST /api/adty/site/report {user_id, reason}。
func PostAdtyOwnerReport(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	var body struct {
		UserId int    `json:"user_id"`
		Reason string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || body.UserId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	report, err := service.ReportAdtySiteCustomer(site, c.GetInt("id"), body.UserId, body.Reason)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"id": report.Id, "status": report.Status})
}

// AdtyOwnerExportMaxRows 是一次导出最多的行数。超过就拒绝，不返回缺了较新记录的 CSV。
var AdtyOwnerExportMaxRows = 100000

func adtyOwnerExportOverLimit(c *gin.Context, total int64) bool {
	if total <= int64(AdtyOwnerExportMaxRows) {
		return false
	}
	common.ApiErrorT(c, "This export has more than {{limit}} rows", map[string]any{"limit": AdtyOwnerExportMaxRows})
	return true
}

// GetAdtyOwnerExport 导出：GET /api/adty/site/export?kind=earnings|stats|customers&from&to，CSV。
// 只能导出收益流水、经营报表、按客户汇总；邮箱一律打码（不看 customer_email 设置）。
func GetAdtyOwnerExport(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	var b strings.Builder
	switch c.Query("kind") {
	case "earnings":
		var total int64
		earnings := model.DB.Model(&model.AdtySiteEarningDaily{}).Scopes(model.AdtyFinancialOwnerScope(site.Id, c.GetInt("id")))
		if err := earnings.Count(&total).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if adtyOwnerExportOverLimit(c, total) {
			return
		}
		var rows []model.AdtySiteEarningDaily
		if err := earnings.Order("day, id").Find(&rows).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		b.WriteString("day,user_id,kind,reason,revenue_usd,earning_usd,status\n")
		for _, row := range rows {
			fmt.Fprintf(&b, "%s,%d,%s,%s,%s,%s,%s\n", row.Day, row.UserId, row.Kind, row.Reason, row.Revenue.StringFixed(2), row.Earning.StringFixed(2), row.Status)
		}
	case "stats":
		stats, err := service.GetAdtyOwnerStats(site.Id, c.GetInt("id"), c.Query("from"), c.Query("to"))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		b.WriteString("day,new_customers,paid_customers,active_customers,revenue_usd,earning_usd,bonus_usd,refund_usd\n")
		for _, day := range stats.Series {
			fmt.Fprintf(&b, "%s,%d,%d,%d,%s,%s,%s,%s\n", day.Day, day.NewCustomers, day.PaidCustomers, day.ActiveCustomer,
				day.RevenueUSD.StringFixed(2), day.EarningUSD.StringFixed(2), day.BonusUSD.StringFixed(2), day.RefundUSD.StringFixed(2))
		}
	case "customers":
		var total int64
		if err := model.DB.Model(&model.User{}).Where("site_id = ?", site.Id).Count(&total).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if adtyOwnerExportOverLimit(c, total) {
			return
		}
		var users []model.User
		if err := model.DB.Select("id", "username", "email", "created_at").Where("site_id = ?", site.Id).Order("id").Find(&users).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		var sums []struct {
			UserId  int
			Revenue decimal.Decimal
			Earning decimal.Decimal
		}
		if err := model.DB.Model(&model.AdtySiteEarningDaily{}).Select("user_id, COALESCE(SUM(revenue),0) AS revenue, COALESCE(SUM(earning),0) AS earning").
			Scopes(model.AdtyFinancialOwnerScope(site.Id, c.GetInt("id"))).Where("status <> ?", model.AdtySiteEarningVoid).Group("user_id").Scan(&sums).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		byUser := map[int]int{}
		for i, sum := range sums {
			byUser[sum.UserId] = i
		}
		b.WriteString("user_id,username,email,created_at,revenue_usd,earning_usd\n")
		for _, user := range users {
			revenue, earning := decimal.Zero, decimal.Zero
			if i, ok := byUser[user.Id]; ok {
				revenue, earning = sums[i].Revenue, sums[i].Earning
			}
			fmt.Fprintf(&b, "%d,%s,%s,%d,%s,%s\n", user.Id, adtyCSVField(user.Username), adtyCSVField(service.AdtyOwnerVisibleEmail(user.Email, service.AdtySiteVisibility(site))), user.CreatedAt,
				revenue.StringFixed(2), earning.StringFixed(2))
		}
	default:
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="site-%d-%s.csv"`, site.Id, c.Query("kind")))
	c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(b.String()))
}

// adtyCSVField 给 CSV 字段加引号，并防止表格软件把 =、+、-、@ 开头的内容当公式执行。
func adtyCSVField(value string) string {
	if value != "" && strings.ContainsAny(value[:1], "=+-@\t\r") {
		value = "'" + value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// PutAdtySitePricingWithProof、PutAdtySiteCustomerPricingWithProof、RemoveAdtySiteDomainWithProof
// 在 11.4、11.8 的接口前面加一层二次验证（router/adty-router.go 注册时用这几个）。

// PutAdtySitePricingWithProof：全站定价，target = 0。
func PutAdtySitePricingWithProof(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionPricing, 0) {
		return
	}
	PutAdtySitePricing(c)
}

// PutAdtySiteCustomerPricingWithProof：单个客户的定价，target = 客户 id。
func PutAdtySiteCustomerPricingWithProof(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionPricing, id) {
		return
	}
	PutAdtySiteCustomerPricing(c)
}

// RemoveAdtySiteSenderDomainWithProof：删除发件域名，target = 站点 id（一个站最多一个发件域名）。
func RemoveAdtySiteSenderDomainWithProof(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionSender, site.Id) {
		return
	}
	RemoveAdtySiteSenderDomain(c)
}

// RemoveAdtySiteDomainWithProof：删除自定义域名，target = 域名 id。先过开站协议，和删除发件域名一样。
func RemoveAdtySiteDomainWithProof(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionDomain, id) {
		return
	}
	RemoveAdtySiteDomain(c)
}

// UploadAdtySiteLogo 上传 Logo：POST /api/adty/site/logo（multipart，字段 file）。返回地址，站长再用 PUT /self
// 把它写进 brand.logo_url。只收 PNG、JPEG、WebP，不收 SVG，最大 200 KB。
func UploadAdtySiteLogo(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	if site.Status == model.AdtySiteStatusClosing {
		common.ApiError(c, service.ErrAdtySiteClosing)
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		common.ApiErrorT(c, "Choose an image to upload")
		return
	}
	if header.Size > service.AdtySiteLogoMaxBytes {
		common.ApiError(c, service.ErrAdtySiteLogoTooLarge)
		return
	}
	file, err := header.Open()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, service.AdtySiteLogoMaxBytes+1))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	url, err := service.SaveAdtySiteLogo(site.Id, data)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"url": url})
}

// GetAdtySiteAsset 返回站长上传的 Logo（公开，分站页面和邮件都要用）。SVG 加 CSP 禁止脚本，所有类型都 nosniff。
func GetAdtySiteAsset(c *gin.Context) {
	path, contentType, err := service.OpenAdtySiteAsset(c.Param("name"))
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	c.Header("Cache-Control", "public, max-age=86400")
	c.Header("Content-Type", contentType)
	c.File(path)
}
