package controller

import (
	"cmp"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 主站的分站管理（任务 11.9 的后端）。全部用 handlePermissionRoute 按「分站」权限注册（router/adty-router.go）。

func siteIdParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	return id, true
}

// parseOptionalDecimal 把请求里的字符串数字转成 decimal；空串或没给返回 nil。
func parseOptionalDecimal(raw *string) (*decimal.Decimal, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	value, err := decimal.NewFromString(strings.TrimSpace(*raw))
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// adtySiteAdminView 是后台列表和明细里的一行：站点加站长、客户数、本月数字和生效的 d。
type adtySiteAdminView struct {
	model.AdtySite
	OwnerUsername       string `json:"owner_username"`
	Host                string `json:"host"`
	ApiHost             string `json:"api_host,omitempty"`
	Customers           int64  `json:"customers"`
	EffectiveCostFactor string `json:"effective_cost_factor"` // 当前生效的 d（单站 > 档位 > 全局，不低于下限）
	service.AdtySiteMonthTotals
}

func buildSiteAdminViews(sites []model.AdtySite) ([]adtySiteAdminView, error) {
	ownerIds := make([]int, 0, len(sites))
	siteIds := make([]int, 0, len(sites))
	for _, site := range sites {
		ownerIds = append(ownerIds, site.OwnerUserId)
		siteIds = append(siteIds, site.Id)
	}
	names := map[int]string{}
	if len(ownerIds) > 0 {
		var owners []model.User
		if err := model.DB.Unscoped().Select("id", "username").Where("id IN ?", ownerIds).Find(&owners).Error; err != nil {
			return nil, err
		}
		for _, owner := range owners {
			names[owner.Id] = owner.Username
		}
	}
	counts := map[int]int64{}
	if len(siteIds) > 0 {
		var rows []struct {
			SiteId int
			Total  int64
		}
		if err := model.DB.Model(&model.User{}).Select("site_id, COUNT(*) AS total").Where("site_id IN ?", siteIds).Group("site_id").Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			counts[row.SiteId] = row.Total
		}
	}
	apiHosts := map[int]string{}
	apiHostPriority := map[int]int{}
	if len(siteIds) > 0 {
		var apiDomains []model.AdtySiteDomain
		if err := model.DB.Select("site_id", "hostname", "status", "managed_by").
			Where("site_id IN ? AND role = ? AND status <> ?", siteIds, model.AdtySiteDomainRoleBaseURL, model.AdtySiteDomainRemoved).
			Order("id ASC").Find(&apiDomains).Error; err != nil {
			return nil, err
		}
		for _, dom := range apiDomains {
			prio := 1
			if dom.ManagedBy == "platform" {
				if dom.Status == model.AdtySiteDomainActive {
					prio = 3
				} else {
					prio = 2
				}
			} else if dom.Status == model.AdtySiteDomainActive {
				prio = 4
			}
			if prio > apiHostPriority[dom.SiteId] {
				apiHostPriority[dom.SiteId] = prio
				apiHosts[dom.SiteId] = dom.Hostname
			}
		}
	}
	totals, err := service.AdtySitesMonthTotals(siteIds, time.Now())
	if err != nil {
		return nil, err
	}
	views := make([]adtySiteAdminView, 0, len(sites))
	for _, site := range sites {
		// R2-044: Prefer live active API host from AdtySiteAPIBaseURL first,
		// falling back to pending/failed base_url DB rows only when no live host exists.
		apiHost := ""
		if rawAPI := strings.TrimSpace(service.AdtySiteAPIBaseURL(&site)); rawAPI != "" {
			if parsed, parseErr := url.Parse(rawAPI); parseErr == nil && parsed.Host != "" {
				apiHost = parsed.Host
			}
		}
		if apiHost == "" {
			apiHost = apiHosts[site.Id]
		}
		view := adtySiteAdminView{AdtySite: site, OwnerUsername: names[site.OwnerUserId], Host: service.AdtySiteDisplayHost(&site),
			ApiHost: apiHost, Customers: counts[site.Id], AdtySiteMonthTotals: totals[site.Id]}
		if d, ok := service.AdtySiteCostFactor(&site); ok {
			view.EffectiveCostFactor = d.String()
		}
		views = append(views, view)
	}
	return views, nil
}

// adtySiteSortKeys 是列表能排序的列：sort=字段，order=asc|desc（默认 desc）。
var adtySiteSortKeys = map[string]func(a, b adtySiteAdminView) int{
	"id":        func(a, b adtySiteAdminView) int { return cmp.Compare(a.Id, b.Id) },
	"customers": func(a, b adtySiteAdminView) int { return cmp.Compare(a.Customers, b.Customers) },
	"topup":     func(a, b adtySiteAdminView) int { return a.TopUpUSD.Cmp(b.TopUpUSD) },
	"revenue":   func(a, b adtySiteAdminView) int { return a.RevenueUSD.Cmp(b.RevenueUSD) },
	"earning":   func(a, b adtySiteAdminView) int { return a.EarningUSD.Cmp(b.EarningUSD) },
	"growth": func(a, b adtySiteAdminView) int {
		// 上月没有消费（没有增长率）的排在最小的一端。
		switch {
		case a.GrowthPct == nil && b.GrowthPct == nil:
			return 0
		case a.GrowthPct == nil:
			return -1
		case b.GrowthPct == nil:
			return 1
		}
		return cmp.Compare(*a.GrowthPct, *b.GrowthPct)
	},
}

// GetAdtyAdminSites 列出分站（分站·查看）：按状态、子域名或站长用户名筛选；带客户数、本月充值、消费、
// 平台收入、站长收益和比上月的增长；sort 可按 id、customers、topup、revenue、earning、growth 排序，order=asc|desc。
// 本月数字要从日汇总算，分站数量有限，所以先取出筛选后的站点，排序后再分页。
func GetAdtyAdminSites(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	query := model.DB.Model(&model.AdtySite{})
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		op := "LIKE"
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			op = "ILIKE"
		}
		like := "%" + keyword + "%"
		query = query.Where("subdomain "+op+" ? OR owner_user_id IN (?)", like,
			model.DB.Model(&model.User{}).Select("id").Where("username "+op+" ?", like))
	}
	compare, ok := adtySiteSortKeys[strings.TrimSpace(c.DefaultQuery("sort", "id"))]
	if !ok {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	ascending := c.Query("order") == "asc"
	if c.DefaultQuery("sort", "id") == "id" {
		var count int64
		if err := query.Count(&count).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		order := "id DESC"
		if ascending {
			order = "id ASC"
		}
		var sites []model.AdtySite
		if err := query.Order(order).Offset(pageInfo.GetStartIdx()).Limit(pageInfo.GetPageSize()).Find(&sites).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		views, err := buildSiteAdminViews(sites)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		pageInfo.SetTotal(int(count))
		pageInfo.SetItems(views)
		common.ApiSuccess(c, pageInfo)
		return
	}
	var views []adtySiteAdminView
	var batch []model.AdtySite
	// Bound each aggregate's IN list even when many sites match the filter.
	err := query.FindInBatches(&batch, 500, func(_ *gorm.DB, _ int) error {
		part, err := buildSiteAdminViews(batch)
		if err == nil {
			views = append(views, part...)
		}
		return err
	}).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	slices.SortStableFunc(views, func(a, b adtySiteAdminView) int {
		if compare(a, b) == 0 {
			return cmp.Compare(b.Id, a.Id)
		}
		if ascending {
			return compare(a, b)
		}
		return compare(b, a)
	})
	start := min(pageInfo.GetStartIdx(), len(views))
	end := min(start+pageInfo.GetPageSize(), len(views))
	pageInfo.SetTotal(len(views))
	pageInfo.SetItems(views[start:end])
	common.ApiSuccess(c, pageInfo)
}

// GetAdtyAdminSiteStats 是一个站的经营数据（分站·查看）：GET /api/adty/admin/sites/:id/stats?from&to。
// 和站长中心同一份按天的数据，另外带按模型、分组的消费和按月的序列。
func GetAdtyAdminSiteStats(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	site, err := service.GetAdtySite(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	stats, err := service.GetAdtySiteStats(id, c.Query("from"), c.Query("to"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	breakdown, err := service.GetAdtySiteUsageBreakdown(id, stats.From, stats.To)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	body := gin.H{"stats": stats, "breakdown": breakdown, "monthly": service.AdtyMonthlySeries(stats.Series)}
	// 提现属于推广账户：要推广·查看，并且当前访问令牌要有 adty_referral:read scope（方案 11.9）。
	if adtyAdminFieldVisible(c, authz.AdtyReferralRead) {
		balances, err := service.GetAdtySiteBalances(site)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		body["withdrawn_usd"] = balances.WithdrawnUSD
	}
	common.ApiSuccess(c, body)
}

// HandoverAdtyAdminSiteOwner changes ownership through the Root-only handover.
func HandoverAdtyAdminSiteOwner(c *gin.Context) {
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	var input service.AdtySiteOwnerHandoverInput
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	input.SiteId, input.OperatorId = id, c.GetInt("id")
	site, err := service.HandoverAdtySiteOwner(input)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, site)
}

// GetAdtyAdminSitesOverview 是全部分站的汇总，和主站直客对比（分站·查看）：GET /api/adty/admin/sites/overview?from&to。
func GetAdtyAdminSitesOverview(c *gin.Context) {
	overview, err := service.GetAdtySitesOverview(c.Query("from"), c.Query("to"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, overview)
}

// GetAdtyAdminFlaggedCustomers 列出风控客户（分站·查看）：GET /api/adty/admin/sites/flagged-customers?all=1。
// 默认只列没复核的；后台能看到判断依据（risk_reason），站长看不到。
func GetAdtyAdminFlaggedCustomers(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	query := model.AdtyLiveSiteCustomer(model.DB.Model(&model.AdtySiteCustomer{})).Where("risk_flag <> ''")
	if c.Query("all") != "1" {
		query = query.Where("reviewed = ?", false)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var rows []model.AdtySiteCustomer
	if err := query.Order("joined_at DESC").Offset(pageInfo.GetStartIdx()).Limit(pageInfo.GetPageSize()).Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	userIds := make([]int, 0, len(rows))
	for _, row := range rows {
		userIds = append(userIds, row.UserId)
	}
	skipped := map[int]decimal.Decimal{}
	if len(userIds) > 0 {
		var sums []struct {
			UserId int
			Total  decimal.Decimal
		}
		if err := model.DB.Model(&model.AdtySiteEarningDaily{}).Select("user_id, COALESCE(SUM(earning),0) AS total").
			Where("user_id IN ? AND kind = ? AND status = ?", userIds, model.AdtySiteEarningSkipped, model.AdtySiteEarningPending).
			Group("user_id").Scan(&sums).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		for _, sum := range sums {
			skipped[sum.UserId] = sum.Total
		}
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, gin.H{"user_id": row.UserId, "site_id": row.SiteId, "risk_flag": row.RiskFlag, "risk_reason": row.RiskReason,
			"reviewed": row.Reviewed, "joined_at": row.JoinedAt, "skipped_usd": skipped[row.UserId].StringFixed(2)})
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetAdtyAdminSiteReports 列出站长的举报（分站·查看）：GET /api/adty/admin/sites/reports?status=pending|resolved|dismissed|all。
func GetAdtyAdminSiteReports(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	query := model.DB.Model(&model.AdtySiteReport{})
	if status := strings.TrimSpace(c.DefaultQuery("status", model.AdtySiteReportPending)); status != "all" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var reports []model.AdtySiteReport
	if err := query.Order("id DESC").Offset(pageInfo.GetStartIdx()).Limit(pageInfo.GetPageSize()).Find(&reports).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(reports)
	common.ApiSuccess(c, pageInfo)
}

// HandleAdtyAdminSiteReport 处理举报（分站·审核）：POST /api/adty/admin/sites/reports/:id/handle {status: resolved|dismissed, note}。
func HandleAdtyAdminSiteReport(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	report, eventID, err := service.HandleAdtySiteReportWithAudit(id, c.GetInt("id"), body.Status, body.Note)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, report)
}

// GetAdtyAdminSite 是一个分站的明细（分站·查看），带最近的操作记录。
func GetAdtyAdminSite(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	site, err := service.GetAdtySite(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	views, err := buildSiteAdminViews([]model.AdtySite{*site})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var audits []model.AdtySiteAudit
	if err := model.DB.Where("site_id = ?", id).Order("id DESC").Limit(50).Find(&audits).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var domains []model.AdtySiteDomain
	if err := model.DB.Where("site_id = ?", id).Order("id DESC").Find(&domains).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	for i := range domains {
		domains[i].CloseAction = service.AdtyDomainCloseAction(domains[i])
	}
	var senders []model.AdtySiteSenderDomain
	if err := model.DB.Where("site_id = ? AND status <> ?", id, model.AdtySenderDomainDisabled).Find(&senders).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"site": views[0], "audits": audits, "domains": domains, "sender_domains": senders})
}

// GetAdtyAdminSiteCustomers 列出一个分站的客户（分站·查看）。没有用户查看权限时邮箱打码。
func GetAdtyAdminSiteCustomers(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	query := model.DB.Model(&model.User{}).Where("site_id = ?", id)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var users []model.User
	if err := query.Select("id", "username", "email", "quota", "quota_pending", "used_quota", "status", "group", "created_at").
		Order("id DESC").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&users).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	userIds := make([]int, 0, len(users))
	for _, user := range users {
		userIds = append(userIds, user.Id)
	}
	risks := map[int]model.AdtySiteCustomer{}
	if len(userIds) > 0 {
		var rows []model.AdtySiteCustomer
		if err := model.DB.Where("user_id IN ?", userIds).Find(&rows).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		for _, row := range rows {
			risks[row.UserId] = row
		}
	}
	// 邮箱属于用户资料：要用户·查看，并且当前访问令牌要有 adty_user:read scope。
	showEmail := adtyAdminFieldVisible(c, authz.AdtyUserRead)
	items := make([]gin.H, 0, len(users))
	for _, user := range users {
		risk := risks[user.Id]
		email := service.AdtyMaskEmail(user.Email)
		if showEmail {
			email = user.Email
		}
		items = append(items, gin.H{
			"id": user.Id, "username": user.Username, "email": email, "quota": user.ToBaseUser().Quota, "used_quota": user.UsedQuota,
			"status": user.Status, "group": user.Group, "created_at": user.CreatedAt,
			"risk_flag": risk.RiskFlag, "risk_reason": risk.RiskReason, "reviewed": risk.Reviewed, "joined_at": risk.JoinedAt,
		})
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetAdtyAdminSiteOwnerCandidates lists eligible main-site users who can be designated
// as fixed owner of a new reseller site (Root only). Excludes disabled accounts, sub-site
// accounts, site staff, independent team employees, and users who already own a non-closed site.
func GetAdtyAdminSiteOwnerCandidates(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	pageSize := pageInfo.GetPageSize()
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 20
	}
	ownedSub := model.DB.Model(&model.AdtySite{}).Select("owner_user_id").Where("status <> ?", model.AdtySiteStatusClosed)
	empSub := model.DB.Model(&model.AdtyTeamMember{}).Select("user_id").Where("independent_employee = ?", true)
	query := model.DB.Model(&model.User{}).
		Where("site_id = 0 AND staff_site_id = 0 AND status = ?", common.UserStatusEnabled).
		Where("id NOT IN (?)", ownedSub).
		Where("id NOT IN (?)", empSub)
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		op := "LIKE"
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			op = "ILIKE"
		}
		like := "%" + keyword + "%"
		if id, err := strconv.Atoi(strings.TrimPrefix(keyword, "#")); err == nil && id > 0 {
			query = query.Where("(id = ? OR username "+op+" ? OR display_name "+op+" ? OR email "+op+" ?)", id, like, like, like)
		} else {
			query = query.Where("(username "+op+" ? OR display_name "+op+" ? OR email "+op+" ?)", like, like, like)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var users []model.User
	if err := query.Select("id", "username", "display_name", "email", "role", "created_at").
		Order("id DESC").Offset(pageInfo.GetStartIdx()).Limit(pageSize).Find(&users).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	showEmail := adtyAdminFieldVisible(c, authz.AdtyUserRead)
	items := make([]gin.H, 0, len(users))
	for _, user := range users {
		email := service.AdtyMaskEmail(user.Email)
		if showEmail {
			email = user.Email
		}
		items = append(items, gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        email,
			"role":         user.Role,
			"created_at":   user.CreatedAt,
		})
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// CreateAdtyAdminSite 是管理员直接开站（仅 Root）：{user_id, subdomain?, root_domain?, cost_factor?, tier_code?, tier_mode?, contact_info?}。
func CreateAdtyAdminSite(c *gin.Context) {
	var body struct {
		UserId         int     `json:"user_id"`
		Subdomain      string  `json:"subdomain"`
		RootDomain     string  `json:"root_domain"`
		CostFactor     *string `json:"cost_factor"`
		TierCode       string  `json:"tier_code"`
		TierMode       string  `json:"tier_mode"`
		ContactInfo    string  `json:"contact_info"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || body.UserId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	costFactor, err := parseOptionalDecimal(body.CostFactor)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		key = strings.TrimSpace(body.IdempotencyKey)
	}
	site, eventID, replayed, err := service.OpenAdtySiteWithAudit(body.UserId, body.Subdomain, model.AdtySiteOpenedByAdmin, service.AdtySitePreset{
		CostFactor: costFactor, TierCode: body.TierCode, TierMode: body.TierMode,
		ContactInfo: body.ContactInfo, OperatorId: c.GetInt("id"),
		ExplicitRoot: strings.ToLower(strings.TrimSpace(body.RootDomain)), IdempotencyKey: key,
	})
	if err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This request was already used for another operation"))
			return
		}
		common.ApiError(c, err)
		return
	}
	if replayed {
		markIdempotentReplayNoAudit(c)
	} else if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	views, err := buildSiteAdminViews([]model.AdtySite{*site})
	if err != nil || len(views) == 0 {
		common.ApiSuccess(c, site)
		return
	}
	common.ApiSuccess(c, views[0])
}

// ReviewAdtyAdminSite 通过或驳回开站申请（分站·审核）：{approve, cost_factor?, note}。
func ReviewAdtyAdminSite(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		Approve    bool    `json:"approve"`
		CostFactor *string `json:"cost_factor"`
		Note       string  `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var costFactor *decimal.Decimal
	if body.Approve {
		var err error
		costFactor, err = parseOptionalDecimal(body.CostFactor)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	site, eventID, err := service.ReviewAdtySiteApplicationWithAudit(id, c.GetInt("id"), body.Approve, costFactor, nil, body.Note)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, site)
}

// AdjustAdtyAdminSite 改单站 d、可见范围（分站·调整）。clear 列出要改回「用全局」的项：cost_factor。
func AdjustAdtyAdminSite(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		CostFactor    *string  `json:"cost_factor"`
		Visibility    *string  `json:"visibility"`
		AllowedGroups *string  `json:"allowed_groups"`
		Clear         []string `json:"clear"`
		Reason        string   `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	costFactor, err := parseOptionalDecimal(body.CostFactor)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	for _, key := range body.Clear {
		if key != "cost_factor" && key != "allowed_groups" {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	clearCostFactor := slices.Contains(body.Clear, "cost_factor")
	if (body.CostFactor != nil || clearCostFactor) && c.GetInt("role") < common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	site, eventID, err := service.AdjustAdtySiteWithAudit(id, c.GetInt("id"), service.AdtySiteAdjust{
		CostFactor: costFactor, ClearCostFactor: clearCostFactor,
		Visibility: body.Visibility, AllowedGroups: body.AllowedGroups,
		ClearAllowedGroups: slices.Contains(body.Clear, "allowed_groups"), Reason: body.Reason,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	} else {
		markIdempotentReplayNoAudit(c)
	}
	common.ApiSuccess(c, site)
}

// SetAdtyAdminSiteStatus 关站（仅Root）。独立暂停/恢复已退役：
// status=paused 或把 status=active 当作从暂停恢复，都会被拒绝，站点状态不变。
// {status: closed, transfer_to?, claw_back?, immediate?, reason}。
// 关站不必也不允许指定其他分站：省略 transfer_to 或传 0 表示由主站继承，非 0 为错误。
func SetAdtyAdminSiteStatus(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		ExecuteAt         int64  `json:"execute_at"`
		ExpectedExecuteAt int64  `json:"expected_execute_at"`
		Status            string `json:"status"`
		TransferTo        *int   `json:"transfer_to"`
		ClawBack          bool   `json:"claw_back"`
		Immediate         bool   `json:"immediate"`
		Reason            string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var site *model.AdtySite
	var eventID string
	var replayed bool
	var err error
	switch body.Status {
	case model.AdtySiteStatusActive, model.AdtySiteStatusPaused:
		common.ApiError(c, service.ErrAdtySitePauseRetired)
		return
	case model.AdtySiteStatusClosed:
		if c.GetInt("role") < common.RoleRootUser {
			denyAdtyAdminAction(c)
			return
		}
		transferTo := 0
		if body.TransferTo != nil {
			if *body.TransferTo != 0 {
				common.ApiError(c, service.ErrAdtySiteTransfer)
				return
			}
			transferTo = *body.TransferTo
		}
		site, eventID, replayed, err = service.ScheduleAdtySiteCloseWithAudit(id, c.GetInt("id"), service.AdtySiteClosePlan{
			ExecuteAt: body.ExecuteAt, ExpectedExecuteAt: body.ExpectedExecuteAt, TransferTo: transferTo, ClawBack: body.ClawBack, Immediate: body.Immediate, Reason: body.Reason,
		})
	default:
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if replayed {
		markIdempotentReplayNoAudit(c)
	} else if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, site)
}

// ListAdtyPausedSiteRepairs lists leftover status=paused sites for Root review.
// Listing does not change any row.
func ListAdtyPausedSiteRepairs(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	sites, err := service.ListLeftoverPausedAdtySites()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	views, err := buildSiteAdminViews(sites)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, views)
}

// RepairAdtyPausedSite is the Root-only one-time repair for one leftover paused site.
// outcome=active repairs the row to active; outcome=close enters the existing
// close/handover flow (main-site inheritance). Pause is not written back.
func RepairAdtyPausedSite(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		Outcome   string `json:"outcome"`
		Immediate bool   `json:"immediate"`
		Reason    string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	site, eventID, replayed, err := service.RepairLeftoverPausedAdtySiteWithAudit(id, c.GetInt("id"), body.Outcome, service.AdtySiteClosePlan{
		Immediate: body.Immediate, Reason: body.Reason,
	})
	if replayed {
		markIdempotentReplayNoAudit(c)
	} else if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, site)
}

// ResumeAdtySiteSettlement 清掉对账暂停并立刻再结一次。数字还是对不上时响应里仍有 settle_hold_reason。
func ResumeAdtySiteSettlement(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	site, eventID, err := service.ResumeAdtySiteSettlementWithAudit(id, c.GetInt("id"), time.Now())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, site)
}

// adtyResellerSettingsView 是后台读到的分站设置：密钥清空，只返回「已配置」；另给出各开关依赖的项是否齐全，
// 后台在没配好的项旁边显示「未配置」。
type adtyResellerSettingsView struct {
	AliyunIDConfigured         bool                                   `json:"aliyun_id_configured"`
	AliyunSecretConfigured     bool                                   `json:"aliyun_secret_configured"`
	DomainAutomationEnabled    bool                                   `json:"domain_automation_enabled"`
	AliyunDomain               string                                 `json:"aliyun_domain"`
	AliyunAccessKeyID          string                                 `json:"aliyun_access_key_id"`
	AliyunAccessKeySecret      string                                 `json:"aliyun_access_key_Secret"`
	DomainMainlandTarget       string                                 `json:"domain_mainland_target"`
	Enabled                    bool                                   `json:"enabled"`
	RootDomains                []string                               `json:"root_domains"`
	ReservedSubdomains         []string                               `json:"reserved_subdomains"`
	UnknownHostMode            string                                 `json:"unknown_host_mode"`
	MarkupIncreaseDelayHours   int                                    `json:"markup_increase_delay_hours"`
	SettleHour                 int                                    `json:"settle_hour"`
	FreezeDays                 int                                    `json:"freeze_days"`
	CloseNoticeDays            int                                    `json:"close_notice_days"`
	SiteLoginOnly              bool                                   `json:"site_login_only"`
	CustomDomainEnabled        bool                                   `json:"custom_domain_enabled"`
	SenderDomainEnabled        bool                                   `json:"sender_domain_enabled"`
	SelfUseCheckEnabled        bool                                   `json:"self_use_check_enabled"`
	MigrationCheckEnabled      bool                                   `json:"migration_check_enabled"`
	FingerprintWindowDays      int                                    `json:"fingerprint_window_days"`
	CustomerLookupPerHour      int                                    `json:"customer_lookup_per_hour"`
	MaxCustomDomainsPerSite    int                                    `json:"max_custom_domains_per_site"`
	CustomDomainCreatesPerHour int                                    `json:"custom_domain_creates_per_hour"`
	Visibility                 map[string]string                      `json:"visibility"`
	CloudflareApiToken         string                                 `json:"cloudflare_apiToken"`
	CloudflareZoneId           string                                 `json:"cloudflare_zone_id"`
	CloudflareAccountId        string                                 `json:"cloudflare_account_id"`
	CnameTarget                string                                 `json:"cname_target"`
	FallbackOrigin             string                                 `json:"fallback_origin"`
	TurnstileWidgets           []adty_setting.ResellerTurnstileWidget `json:"turnstile_widgets"`
	TurnstileWidgetSecret      map[string]string                      `json:"turnstile_widget_secret"`
	CloudflareTokenConfigured  bool                                   `json:"cloudflare_token_configured"`
	WidgetSecretConfigured     map[string]bool                        `json:"turnstile_secret_configured"`
	CloudflareConfigured       bool                                   `json:"cloudflare_configured"`
	// TurnstileConfigured 单独报，**不要并进 CloudflareConfigured**：
	// 一个是「域名加不上」，一个是「人机验证登记静默跳过」，缺它们的后果不同。
	TurnstileConfigured bool `json:"turnstile_configured"`
	// SettingsWarnings 是「保存成功了、但有一处配置不会生效」的可见提示。
	// 空数组表示没有。**它不是拒绝理由**——每一项都说明「功能降级到什么程度」。
	FieldVersions            map[string]string `json:"field_versions"`
	SettingsWarnings         []string          `json:"settings_warnings"`
	ResendConfigured         bool              `json:"resend_configured"`
	MoneyConfigured          bool              `json:"money_configured"`
	TierConfigured           bool              `json:"tier_configured"`
	SiteCustomerTermsVersion int               `json:"site_customer_terms_version"`
}

// adtyResellerSettingsWarnings 列出「保存是成功的，但这几项不会生效」的地方。
//
// **它不拒绝保存**：拒绝会让「先填一半、回头再补」变成不可能，而这几项的失败
// 后果都是「功能降级」而非「算错钱」。
//
// 第一条是最需要人看见的：`CustomDomainEnabled && !TurnstileConfigured()` 时，
// 自定义域名能用，但人机验证**从未登记**——`service/adty_turnstile_hosts.go` 的
// `adtyTurnstileAssign` 在 `account == ""` 或没有 widget 时直接 `return "", nil`，
// 无错、无日志、无标记，5 分钟一轮永远走同一空分支。
func adtyResellerSettingsWarnings(cfg *adty_setting.ResellerSetting) []string {
	warnings := []string{}
	if cfg.CustomDomainEnabled && !cfg.TurnstileConfigured() {
		warnings = append(warnings,
			"Custom domains are on but Turnstile is not configured (account id or widgets missing): new domains will not be registered for human verification.")
	}
	for _, widget := range cfg.TurnstileWidgets {
		if strings.TrimSpace(widget.Id) != "" && (strings.TrimSpace(widget.SiteKey) == "" || strings.TrimSpace(cfg.TurnstileWidgetSecret[widget.Id]) == "") {
			warnings = append(warnings,
				"Some Turnstile widgets are missing a site key or secret: affected sites will fall back until every widget secret is saved.")
			break
		}
	}
	if cfg.SenderDomainEnabled && !adtyResendConfigured() {
		warnings = append(warnings,
			"Sender domains are on but the Resend API key is empty: sender domains cannot be verified.")
	}
	return warnings
}

func adtyResendConfigured() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return strings.TrimSpace(common.OptionMap["ResendApiKey"]) != ""
}

// adtyClearedSecretWarnings 把「装载时解不开、已被清空」的密钥摆到设置页上。
//
// 密钥被清空时只留一行 SysError，而运营看到的现象是「分站注册/签发全挂了」，
// 根因却是另一条密钥解不开（ISS-R4-0122）。摆到设置页旁边，才知道要重填哪一条。
func adtyClearedSecretWarnings() []string {
	cleared := model.ClearedAdtyOptionSecrets()
	if len(cleared) == 0 {
		return nil
	}
	warnings := make([]string, 0, len(cleared))
	for _, key := range cleared {
		// R2-060: Map raw DB option keys to structured warning codes so raw DB
		// keys never leak into admin UI banners.
		warnings = append(warnings, "cleared_secret:"+adtyFriendlySecretCode(key))
	}
	return warnings
}

func adtyFriendlySecretCode(key string) string {
	switch key {
	case adty_setting.ResellerSettingModule + "." + adty_setting.ResellerKeyCloudflareTok:
		return "cloudflare_api_token"
	case adty_setting.ResellerSettingModule + "." + adty_setting.ResellerKeyWidgetSecret:
		return "turnstile_widget_secrets"
	case adty_setting.ResellerSettingModule + ".aliyun_access_key_id":
		return "aliyun_access_key_id"
	case adty_setting.ResellerSettingModule + ".aliyun_access_key_Secret":
		return "aliyun_access_key_secret"
	default:
		return "credential"
	}
}

func resellerSettingsSnapshot(db *gorm.DB) (adty_setting.ResellerSetting, map[string]string, error) {
	var cfg adty_setting.ResellerSetting
	defaults, err := config.ConfigToMap(adty_setting.GetResellerSetting())
	if err != nil {
		return cfg, nil, err
	}
	values, versions, err := model.ResellerOptionSnapshot(db)
	if err != nil {
		return cfg, nil, err
	}
	for key, value := range values {
		if _, known := defaults[key]; known {
			// A malformed legacy option must not prevent unrelated settings or its own replacement.
			if err := config.ValidateConfigValue(&cfg, key, value); err == nil {
				defaults[key] = value
			}
		}
	}
	defaults["unknown_host_mode"] = adty_setting.ResellerUnknownHostReject
	err = config.UpdateConfigFromMap(&cfg, defaults)
	return cfg, versions, err
}

// GetAdtyResellerSettings 读分站设置（分站·设置）。
func GetAdtyResellerSettings(c *gin.Context) {
	cfg, versions, err := resellerSettingsSnapshot(model.DB)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	hasTier, err := service.HasReadyAdtySiteTier(model.DB)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	warnings := append(adtyResellerSettingsWarnings(&cfg), adtyClearedSecretWarnings()...)
	view := adtyResellerSettingsView{
		FieldVersions:              versions,
		AliyunIDConfigured:         cfg.AliyunAccessKeyID != "",
		AliyunSecretConfigured:     cfg.AliyunAccessKeySecret != "",
		DomainAutomationEnabled:    cfg.DomainAutomationEnabled,
		AliyunDomain:               cfg.AliyunDomain,
		DomainMainlandTarget:       cfg.DomainMainlandTarget,
		Enabled:                    cfg.Enabled,
		RootDomains:                cfg.RootDomains,
		ReservedSubdomains:         cfg.ReservedSubdomains,
		UnknownHostMode:            cfg.UnknownHostMode,
		MarkupIncreaseDelayHours:   cfg.MarkupIncreaseDelayHours,
		SettleHour:                 cfg.SettleHour,
		FreezeDays:                 cfg.FreezeDays,
		CloseNoticeDays:            cfg.CloseNoticeDays,
		SiteLoginOnly:              cfg.SiteLoginOnly,
		CustomDomainEnabled:        cfg.CustomDomainEnabled,
		SenderDomainEnabled:        cfg.SenderDomainEnabled,
		SelfUseCheckEnabled:        cfg.SelfUseCheckEnabled,
		MigrationCheckEnabled:      cfg.MigrationCheckEnabled,
		FingerprintWindowDays:      cfg.FingerprintWindowDays,
		CustomerLookupPerHour:      cfg.CustomerLookupPerHour,
		MaxCustomDomainsPerSite:    cfg.MaxCustomDomainsPerSite,
		CustomDomainCreatesPerHour: cfg.CustomDomainCreatesPerHour,
		Visibility:                 cfg.Visibility,
		CloudflareZoneId:           cfg.CloudflareZoneId,
		CloudflareAccountId:        cfg.CloudflareAccountId,
		CnameTarget:                cfg.CnameTarget,
		FallbackOrigin:             cfg.FallbackOrigin,
		TurnstileWidgets:           cfg.TurnstileWidgets,
		CloudflareTokenConfigured:  strings.TrimSpace(cfg.CloudflareApiToken) != "",
		WidgetSecretConfigured:     map[string]bool{},
		CloudflareConfigured:       cfg.CloudflareConfigured(),
		TurnstileConfigured:        cfg.TurnstileConfigured(),
		SettingsWarnings:           warnings,
		ResendConfigured:           adtyResendConfigured(),
		MoneyConfigured:            hasTier,
		TierConfigured:             hasTier,
		SiteCustomerTermsVersion:   adty_setting.GetLegalSetting().SiteCustomerTermsVersion,
	}
	for _, widget := range cfg.TurnstileWidgets {
		view.WidgetSecretConfigured[widget.Id] = strings.TrimSpace(cfg.TurnstileWidgetSecret[widget.Id]) != ""
	}
	common.ApiSuccess(c, view)
}

// UpdateAdtyResellerSettings 保存分站设置（分站·设置）。请求体是要改的键（json 标签 → JSON 值），
// 金额项给 null 表示改回未设置。合并到当前值上整体校验，通过后一次写进 options，保存后立即生效。
// 密钥项给空串表示不改；要清空密钥时在 clear_secrets 里列出键名。
func UpdateAdtyResellerSettings(c *gin.Context) {
	var body map[string]common.RawMessage
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if value, ok := body["unknown_host_mode"]; ok {
		var legacy string
		if common.Unmarshal(value, &legacy) != nil || (legacy != "main" && legacy != "reject") {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		body["unknown_host_mode"] = common.RawMessage(`"reject"`)
	}
	for _, key := range []string{"trial_grant_max_usd", "trial_grant_daily_usd"} {
		if _, present := body[key]; present {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Trial credit budgets have been retired"))
			return
		}
	}
	for _, key := range []string{"cost_factor", "min_cost_factor", "cost_factor_tiers", "invite_code_enabled"} {
		if _, present := body[key]; present {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("This legacy reseller setting has been retired"))
			return
		}
	}
	if _, present := body["max_site_bonus_extra"]; present {
		common.ApiError(c, service.ErrAdtyAmountTierRetired)
		return
	}
	if c.GetInt("role") < common.RoleRootUser {
		for _, key := range []string{adty_setting.ResellerKeyCloudflareTok, adty_setting.ResellerKeyWidgetSecret, "turnstile_widgets", "aliyun_access_key_id", "aliyun_access_key_Secret", "domain_automation_enabled", "aliyun_domain", "domain_mainland_target", "cloudflare_zone_id", "cloudflare_account_id", "cname_target", "fallback_origin", "clear_secrets"} {
			if _, present := body[key]; present {
				denyAdtyAdminAction(c)
				return
			}
		}
	}
	var clearSecrets []string
	if raw, ok := body["clear_secrets"]; ok {
		if err := common.Unmarshal(raw, &clearSecrets); err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		delete(body, "clear_secrets")
	}
	var expected map[string]string
	if raw, ok := body["expected_versions"]; ok {
		if err := common.Unmarshal(raw, &expected); err != nil || expected == nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		delete(body, "expected_versions")
	}
	var next adty_setting.ResellerSetting
	values := map[string]string{}
	options := map[string]string{}
	// Serialize commit and publication on this node. The database lock also
	// serializes a patch with writers on other nodes before resolving dependencies.
	model.ResellerWriteMu.Lock()
	defer model.ResellerWriteMu.Unlock()
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.LockResellerOptionsTx(tx); err != nil {
			return err
		}
		current, observed, err := resellerSettingsSnapshot(tx)
		if err != nil {
			return err
		}
		known, err := config.ConfigToMap(&current)
		if err != nil {
			return err
		}

		for key, raw := range body {
			if _, ok := known[key]; !ok || strings.HasPrefix(key, "pending_") {
				return common.NewMessage("Invalid parameters")
			}
			text := strings.TrimSpace(string(raw))
			var asString string
			if strings.HasPrefix(text, `"`) && common.Unmarshal(raw, &asString) == nil {
				text = asString
			}
			if (key == adty_setting.ResellerKeyCloudflareTok || key == adty_setting.ResellerKeyWidgetSecret || key == "aliyun_access_key_id" || key == "aliyun_access_key_Secret") && (text == "" || text == "null") {
				continue
			}
			if key == adty_setting.ResellerKeyWidgetSecret {
				// 只合并给出的 widget，其余 secret 保留。
				merged := map[string]string{}
				for id, secret := range current.TurnstileWidgetSecret {
					merged[id] = secret
				}
				incoming := map[string]string{}
				if err := common.UnmarshalJsonStr(text, &incoming); err != nil {
					return common.NewMessage("Invalid parameters")
				}
				for id, secret := range incoming {
					if strings.TrimSpace(secret) != "" {
						merged[id] = strings.TrimSpace(secret)
					}
				}
				encoded, _ := common.Marshal(merged)
				text = string(encoded)
			}
			values[key] = text
		}
		for _, key := range clearSecrets {
			switch key {
			case adty_setting.ResellerKeyCloudflareTok, "aliyun_access_key_id", "aliyun_access_key_Secret":
				values[key] = ""
			case adty_setting.ResellerKeyWidgetSecret:
				values[key] = "{}"
			default:
				return common.NewMessage("Invalid parameters")
			}
		}
		for key := range values {
			if expected != nil && expected[key] != observed[key] {
				return common.NewMessage("Setting changed since editing began: {{key}}", map[string]any{"key": key})
			}
		}
		// Decode the complete candidate into fresh storage. Pointer-valued money
		// fields must not alias the live configuration before persistence succeeds.

		merged := map[string]string{}
		for key, value := range known {
			merged[key] = value
		}
		for key, value := range values {
			merged[key] = value
		}
		if err := config.UpdateConfigFromMap(&next, merged); err != nil {
			return err
		}
		// Verify the effective value after decoding and normalization.
		parsed, _ := config.ConfigToMap(&next)
		for key, value := range values {
			if !adtyConfigValueEqual(parsed[key], value) {
				return common.NewMessage("Invalid value for {{key}}", map[string]any{"key": key})
			}
		}
		for i := range next.RootDomains {
			next.RootDomains[i] = strings.ToLower(strings.TrimSpace(next.RootDomains[i]))
		}
		for i := range next.ReservedSubdomains {
			next.ReservedSubdomains[i] = strings.ToLower(strings.TrimSpace(next.ReservedSubdomains[i]))
		}
		if err := adty_setting.ValidateResellerPatch(&next, adtyResendConfigured(), values); err != nil {
			return err
		}
		if next.Enabled {
			hasTier, err := service.HasReadyAdtySiteTier(tx)
			if err != nil {
				return err
			}
			if !hasTier {
				return common.NewMessage("Configure at least one enabled site level with a supply discount before turning reseller sites on")
			}
		}
		if len(values) > 0 {
			normalized, _ := config.ConfigToMap(&next)

			for key := range values {
				options[adty_setting.ResellerSettingModule+"."+key] = normalized[key]
			}
			return model.SaveOptionsTx(tx, options)
		}
		return nil
	})
	if err == nil {
		err = model.ApplyOptionValues(options)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	service.InvalidateAdtySiteCache()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	recordManageAudit(c, "adty.site.settings", map[string]any{"keys": strings.Join(keys, ",")})
	GetAdtyResellerSettings(c)
}

// adtyConfigValueEqual 比较请求里的值和解析后再导出的值（JSON 的空白、数字写法可能不同）。
func adtyConfigValueEqual(parsed, requested string) bool {
	if parsed == requested {
		return true
	}
	var a, b any
	if common.UnmarshalJsonStr(parsed, &a) != nil || common.UnmarshalJsonStr(requested, &b) != nil {
		return false
	}
	left, _ := common.Marshal(a)
	right, _ := common.Marshal(b)
	return string(left) == string(right)
}

// ReviewAdtySiteCustomer 复核风控客户（分站·审核）：POST /api/adty/admin/sites/customers/:id/review {convert_skipped}。
// 认可后之后的消费正常计收益；convert_skipped 时还没结算的 skipped 收益改成 usage。
func ReviewAdtySiteCustomer(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		ConvertSkipped bool `json:"convert_skipped"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	eventID, err := service.ReviewAdtySiteCustomerWithAudit(id, c.GetInt("id"), body.ConvertSkipped)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"user_id": id, "reviewed": true})
}

// ClearAdtyAdminSiteNotice 一键下线分站公告（分站·调整）：DELETE /api/adty/admin/sites/:id/notice {reason?}。
func ClearAdtyAdminSiteNotice(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if c.Request.ContentLength > 0 {
		if err := common.DecodeJson(c.Request.Body, &body); err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	old, eventID, err := service.ClearAdtySiteNoticeWithAudit(id, c.GetInt("id"), body.Reason)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	} else {
		markIdempotentReplayNoAudit(c)
	}
	common.ApiSuccess(c, gin.H{"site_id": id, "cleared": old != ""})
}

// GetAdtyResellerUsage 是 Resend 发件域名和 Turnstile widget 的名额用量（分站·查看）：GET /api/adty/admin/reseller/usage。
func GetAdtyResellerUsage(c *gin.Context) {
	usage, err := service.GetAdtyResellerUsage()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, usage)
}

func GetAdtyAdminCloseMail(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var batches []model.AdtySiteCloseMailBatch
	if err := model.DB.Where("site_id = ?", id).Order("revision DESC").Find(&batches).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var batchID int64
	if len(batches) > 0 {
		batchID = batches[0].Id
	}
	if raw := c.Query("batch"); raw != "" {
		var err error
		batchID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	page := common.GetPageQuery(c)
	var rows []model.AdtySiteCloseMailRecipient
	var total int64
	q := model.DB.Model(&model.AdtySiteCloseMailRecipient{}).Where("site_id = ? AND batch_id = ?", id, batchID)
	if err := q.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := q.Order("CASE status WHEN 'review' THEN 0 WHEN 'failed' THEN 1 WHEN 'sending' THEN 2 WHEN 'pending' THEN 3 ELSE 4 END,id").Offset(page.GetStartIdx()).Limit(page.GetPageSize()).Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	showEmail := adtyAdminFieldVisible(c, authz.AdtyUserRead)
	if len(rows) > 0 {
		ids := make([]int64, len(rows))
		byID := make(map[int64]*model.AdtySiteCloseMailRecipient, len(rows))
		for i := range rows {
			if !showEmail {
				rows[i].Email = service.AdtyMaskEmail(rows[i].Email)
			}
			ids[i] = rows[i].Id
			byID[rows[i].Id] = &rows[i]
		}
		var attempts []model.AdtySiteCloseMailAttempt
		if err := model.DB.Where("recipient_id IN ?", ids).Order("recipient_id,number").Find(&attempts).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		for _, attempt := range attempts {
			row := byID[attempt.RecipientID]
			row.Attempts = append(row.Attempts, attempt)
		}
	}
	common.ApiSuccess(c, gin.H{"batches": batches, "batch_id": batchID, "recipients": rows, "total": total})
}

func ResolveAdtyAdminCloseMail(c *gin.Context) {
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var req struct {
		RecipientID int64  `json:"recipient_id"`
		Attempt     int    `json:"attempt"`
		Decision    string `json:"decision"`
		Reason      string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	eventID, err := service.ResolveAdtyCloseMailRecipientWithAudit(id, c.GetInt("id"), req.RecipientID, req.Attempt, req.Decision, req.Reason)
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
