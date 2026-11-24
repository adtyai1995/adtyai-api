package service

import (
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 分站经营数据（站长中心 11.6、主站分站管理 11.9）。
// 消费额、平台收入、站长收益都从收益日汇总（11.5）算，所以「客户付 = 平台收 + 站长赚」在报表里同样成立；
// 充值额从订单表（adty_topup_orders.site_id）算。金额一律美元、decimal，返回字符串保留 2 位。
// 日汇总的 Day 可能带槽位后缀（"2026-10-31+1"），按日期汇总时去掉。

// AdtySiteDayStats 是一天的经营数据。
type AdtySiteDayStats struct {
	Day            string          `json:"day"`
	NewCustomers   int64           `json:"new_customers"`
	PaidCustomers  int64           `json:"paid_customers"`
	ActiveCustomer int64           `json:"active_customers"`
	TopUpUSD       decimal.Decimal `json:"topup_usd"`
	RevenueUSD     decimal.Decimal `json:"revenue_usd"`  // 客户消费额（扣掉任务退款）
	PlatformUSD    decimal.Decimal `json:"platform_usd"` // 平台收入
	EarningUSD     decimal.Decimal `json:"earning_usd"`  // 站长收益（含加赠、试用的负数，不含 skipped）
	BonusUSD       decimal.Decimal `json:"bonus_usd"`    // 站长出的加赠（负数是出了钱，退款扣回为正）
	RefundUSD      decimal.Decimal `json:"refund_usd"`   // 任务退款冲掉的消费额
	SkippedUSD     decimal.Decimal `json:"skipped_usd"`  // 风控不计的收益
}

// AdtySiteStats 是一段时间的汇总和按天的序列。
type AdtySiteStats struct {
	From   string             `json:"from"`
	To     string             `json:"to"`
	Total  AdtySiteDayStats   `json:"total"`
	Series []AdtySiteDayStats `json:"series"`
}

// adtyStatsRange 规范化日期范围：默认最近 30 天，最长 366 天。
func adtyStatsRange(from, to string, loc *time.Location) (string, string, error) {
	now := time.Now().In(loc)
	if to == "" {
		to = now.Format("2006-01-02")
	}
	if from == "" {
		from = now.AddDate(0, 0, -29).Format("2006-01-02")
	}
	start, err1 := time.ParseInLocation("2006-01-02", from, loc)
	end, err2 := time.ParseInLocation("2006-01-02", to, loc)
	if err1 != nil || err2 != nil || end.Before(start) || end.Sub(start) > 366*24*time.Hour {
		return "", "", common.NewMessage("Invalid date range")
	}
	return from, to, nil
}

type adtyCreditedCustomerOrder struct {
	model.AdtyTopupOrder
	CompleteTime int64
}

func adtyCreditedCustomerOrders(siteIDs []int, start, end int64, scopes ...func(*gorm.DB) *gorm.DB) ([]adtyCreditedCustomerOrder, error) {
	q := model.DB.Model(&model.AdtyTopupOrder{}).Scopes(adtyCustomerTopUpOrders).
		Select("adty_topup_orders.*, top_ups.complete_time").
		Joins("JOIN top_ups ON top_ups.trade_no = adty_topup_orders.trade_no").
		Where("top_ups.status IN ? AND top_ups.complete_time >= ? AND top_ups.complete_time <= ?", []string{common.TopUpStatusSuccess, model.TopUpStatusRefunded}, start, end)
	if len(siteIDs) > 0 {
		q = q.Where("adty_topup_orders.site_id IN ?", siteIDs)
	}
	var orders []adtyCreditedCustomerOrder
	err := q.Scopes(scopes...).Scan(&orders).Error
	return orders, err
}

// adtyRetainedTopUpUSD 是这笔订单还留在充值额里的面额。
// 没退本金时是整笔面额；退过一部分时按还没退的本金比例留下，应退本金为 0 时整笔仍在。
func adtyRetainedTopUpUSD(order *model.AdtyTopupOrder) decimal.Decimal {
	if order == nil || !order.FaceUSD.IsPositive() {
		return decimal.Zero
	}
	kept := decimal.NewFromInt(1).Sub(order.RefundRatio())
	if !kept.IsPositive() {
		return decimal.Zero
	}
	return order.FaceUSD.Mul(kept).Round(8)
}

func adtyDayUnix(day string, endOfDay bool, loc *time.Location) int64 {
	parsed, _ := time.ParseInLocation("2006-01-02", day, loc)
	if endOfDay {
		return parsed.AddDate(0, 0, 1).Unix() - 1
	}
	return parsed.Unix()
}

// adtySiteEarningSums 按日期汇总收益行：siteIds 为空表示全部分站。
func adtySiteEarningRows(siteIds []int, from, to string, scopes ...func(*gorm.DB) *gorm.DB) ([]model.AdtySiteEarningDaily, error) {
	query := model.DB.Where("day >= ? AND day <= ? AND status <> ?", from, to+"+~", model.AdtySiteEarningVoid)
	if len(siteIds) > 0 {
		query = query.Where("site_id IN ?", siteIds)
	}
	var rows []model.AdtySiteEarningDaily
	err := query.Scopes(scopes...).Find(&rows).Error
	return rows, err
}

// adtyApplyEarningRow 把一行收益加进一天的数据。
func adtyApplyEarningRow(stats *AdtySiteDayStats, row model.AdtySiteEarningDaily) {
	switch row.Kind {
	case model.AdtySiteEarningUsage, model.AdtySiteEarningRefund:
		stats.RevenueUSD = stats.RevenueUSD.Add(row.Revenue)
		stats.PlatformUSD = stats.PlatformUSD.Add(row.Platform)
		stats.EarningUSD = stats.EarningUSD.Add(row.Earning)
		if row.Kind == model.AdtySiteEarningRefund {
			stats.RefundUSD = stats.RefundUSD.Add(row.Revenue.Neg())
		}
	case model.AdtySiteEarningBonus, model.AdtySiteEarningBonusReversal:
		stats.BonusUSD = stats.BonusUSD.Add(row.Earning)
		stats.EarningUSD = stats.EarningUSD.Add(row.Earning)
	case model.AdtySiteEarningTrial:
		stats.EarningUSD = stats.EarningUSD.Add(row.Earning)
	case model.AdtySiteEarningSkipped:
		// 风控不计收益：客户照常付钱，平台照常收，只是站长这份不结算。
		stats.RevenueUSD = stats.RevenueUSD.Add(row.Revenue)
		stats.PlatformUSD = stats.PlatformUSD.Add(row.Platform)
		stats.SkippedUSD = stats.SkippedUSD.Add(row.Earning)
	}
}

func adtyAddDayStats(total *AdtySiteDayStats, day AdtySiteDayStats) {
	total.NewCustomers += day.NewCustomers
	total.TopUpUSD = total.TopUpUSD.Add(day.TopUpUSD)
	total.RevenueUSD = total.RevenueUSD.Add(day.RevenueUSD)
	total.PlatformUSD = total.PlatformUSD.Add(day.PlatformUSD)
	total.EarningUSD = total.EarningUSD.Add(day.EarningUSD)
	total.BonusUSD = total.BonusUSD.Add(day.BonusUSD)
	total.RefundUSD = total.RefundUSD.Add(day.RefundUSD)
	total.SkippedUSD = total.SkippedUSD.Add(day.SkippedUSD)
}

// GetAdtySiteStats 是一个站在 [from, to] 的经营数据（站长中心和后台明细共用）。
func GetAdtySiteStats(siteId int, from, to string) (*AdtySiteStats, error) {
	return getAdtySiteStats(siteId, from, to, nil)
}

func GetAdtyOwnerStats(siteID, ownerID int, from, to string) (*AdtySiteStats, error) {
	return getAdtySiteStats(siteID, from, to, model.AdtyFinancialOwnerScope(siteID, ownerID))
}

func getAdtySiteStats(siteId int, from, to string, scope func(*gorm.DB) *gorm.DB) (*AdtySiteStats, error) {
	loc, _, err := loadAdtyBusinessLocation()
	if err != nil {
		return nil, err
	}
	from, to, err = adtyStatsRange(from, to, loc)
	if err != nil {
		return nil, err
	}
	days := map[string]*AdtySiteDayStats{}
	dayOf := func(day string) *AdtySiteDayStats {
		if days[day] == nil {
			days[day] = &AdtySiteDayStats{Day: day}
		}
		return days[day]
	}
	active := map[string]map[int]bool{}
	var scopes []func(*gorm.DB) *gorm.DB
	if scope != nil {
		scopes = append(scopes, scope)
	}
	rows, err := adtySiteEarningRows([]int{siteId}, from, to, scopes...)
	if err != nil {
		return nil, err
	}
	allActive := map[int]bool{}
	for _, row := range rows {
		day := adtyEarningDayOf(row.Day)
		adtyApplyEarningRow(dayOf(day), row)
		if row.Kind == model.AdtySiteEarningUsage || row.Kind == model.AdtySiteEarningSkipped {
			if active[day] == nil {
				active[day] = map[int]bool{}
			}
			active[day][row.UserId] = true
			allActive[row.UserId] = true
		}
	}
	for day, users := range active {
		dayOf(day).ActiveCustomer = int64(len(users))
	}

	startUnix, endUnix := adtyDayUnix(from, false, loc), adtyDayUnix(to, true, loc)
	var joined []model.AdtySiteCustomer
	if err := model.DB.Select("user_id", "joined_at").Where("site_id = ? AND joined_at >= ? AND joined_at <= ?", siteId, startUnix, endUnix).Find(&joined).Error; err != nil {
		return nil, err
	}
	for _, customer := range joined {
		dayOf(time.Unix(customer.JoinedAt, 0).In(loc).Format("2006-01-02")).NewCustomers++
	}

	orders, err := adtyCreditedCustomerOrders([]int{siteId}, startUnix, endUnix, scopes...)
	if err != nil {
		return nil, err
	}
	payers := map[string]map[int]bool{}
	allPayers := map[int]bool{}
	for _, order := range orders {
		kept := adtyRetainedTopUpUSD(&order.AdtyTopupOrder)
		if !kept.IsPositive() {
			continue
		}
		day := time.Unix(order.CompleteTime, 0).In(loc).Format("2006-01-02")
		stats := dayOf(day)
		stats.TopUpUSD = stats.TopUpUSD.Add(kept)
		if payers[day] == nil {
			payers[day] = map[int]bool{}
		}
		payers[day][order.UserId] = true
		allPayers[order.UserId] = true
	}
	for day, users := range payers {
		dayOf(day).PaidCustomers = int64(len(users))
	}

	result := &AdtySiteStats{From: from, To: to, Series: make([]AdtySiteDayStats, 0, len(days)), Total: AdtySiteDayStats{Day: ""}}
	for _, stats := range days {
		result.Series = append(result.Series, *stats)
		adtyAddDayStats(&result.Total, *stats)
	}
	sort.Slice(result.Series, func(i, j int) bool { return result.Series[i].Day < result.Series[j].Day })
	result.Total.ActiveCustomer = int64(len(allActive))
	result.Total.PaidCustomers = int64(len(allPayers))
	return result, nil
}

// AdtySiteMonthTotals 是后台列表上每个站本月的数字。
type AdtySiteMonthTotals struct {
	TopUpUSD    decimal.Decimal `json:"month_topup_usd"`
	RevenueUSD  decimal.Decimal `json:"month_revenue_usd"`
	EarningUSD  decimal.Decimal `json:"month_earning_usd"`
	PlatformUSD decimal.Decimal `json:"month_platform_usd"`
	LastRevenue decimal.Decimal `json:"last_month_revenue_usd"`
	GrowthPct   *float64        `json:"growth_pct"` // 本月消费额比上月的增长（%），上月为 0 时为空
}

// AdtySitesMonthTotals 算这些站本月（和上月消费额，用来算增长）的数字。
func AdtySitesMonthTotals(siteIds []int, now time.Time) (map[int]AdtySiteMonthTotals, error) {
	result := map[int]AdtySiteMonthTotals{}
	if len(siteIds) == 0 {
		return result, nil
	}
	loc, _, err := loadAdtyBusinessLocation()
	if err != nil {
		return nil, err
	}
	now = now.In(loc)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	lastStart := monthStart.AddDate(0, -1, 0)
	rows, err := adtySiteEarningRows(siteIds, lastStart.Format("2006-01-02"), now.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	thisMonth := monthStart.Format("2006-01-02")
	for _, row := range rows {
		totals := result[row.SiteId]
		var day AdtySiteDayStats
		adtyApplyEarningRow(&day, row)
		if adtyEarningDayOf(row.Day) >= thisMonth {
			totals.RevenueUSD = totals.RevenueUSD.Add(day.RevenueUSD)
			totals.EarningUSD = totals.EarningUSD.Add(day.EarningUSD)
			totals.PlatformUSD = totals.PlatformUSD.Add(day.PlatformUSD)
		} else {
			totals.LastRevenue = totals.LastRevenue.Add(day.RevenueUSD)
		}
		result[row.SiteId] = totals
	}
	orders, err := adtyCreditedCustomerOrders(siteIds, monthStart.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	for _, order := range orders {
		kept := adtyRetainedTopUpUSD(&order.AdtyTopupOrder)
		if !kept.IsPositive() {
			continue
		}
		totals := result[order.SiteId]
		totals.TopUpUSD = totals.TopUpUSD.Add(kept)
		result[order.SiteId] = totals
	}
	for id, totals := range result {
		if totals.LastRevenue.IsPositive() {
			growth := totals.RevenueUSD.Sub(totals.LastRevenue).Div(totals.LastRevenue).Mul(decimal.NewFromInt(100)).Round(2).InexactFloat64()
			totals.GrowthPct = &growth
			result[id] = totals
		}
	}
	return result, nil
}

// AdtySitesOverview 是全部分站的汇总，和主站直客对比（后台 overview）。
type AdtySitesOverview struct {
	From              string          `json:"from"`
	To                string          `json:"to"`
	Sites             map[string]int  `json:"sites"` // 状态 → 站点数
	SiteCustomers     int64           `json:"site_customers"`
	MainCustomers     int64           `json:"main_customers"`
	SiteTopUpUSD      decimal.Decimal `json:"site_topup_usd"`
	MainTopUpUSD      decimal.Decimal `json:"main_topup_usd"`
	SiteRevenueUSD    decimal.Decimal `json:"site_revenue_usd"`
	SitePlatformUSD   decimal.Decimal `json:"site_platform_usd"`
	SiteEarningUSD    decimal.Decimal `json:"site_earning_usd"`
	SiteSkippedUSD    decimal.Decimal `json:"site_skipped_usd"`
	SiteBonusUSD      decimal.Decimal `json:"site_bonus_usd"`
	PendingReports    int64           `json:"pending_reports"`
	FlaggedCustomers  int64           `json:"flagged_customers"`
	PendingApplicants int64           `json:"pending_applications"`
	// 自定义主机名占 Cloudflare for SaaS 名额。含 100 个，超出的每个每月 0.10 美元（方案 11.8、11.9）。
	CustomHostnames       int64                `json:"custom_hostnames"`
	HostnameMonthlyFeeUSD decimal.Decimal      `json:"hostname_monthly_fee_usd"`
	EarningHookFailures   int64                `json:"earning_hook_failures"`
	SettlementHolds       []AdtySettlementHold `json:"settlement_holds"`
}

// AdtySettlementHold 是对账对不上、正收益暂停结算的站。
type AdtySettlementHold struct {
	SiteId           int    `json:"site_id"`
	Subdomain        string `json:"subdomain"`
	SettleHoldReason string `json:"settle_hold_reason"`
}

const (
	adtyIncludedCustomHostnames = 100
	adtyExtraHostnameMonthlyUSD = "0.10"
)

func adtyHostnameMonthlyFee(count int64) decimal.Decimal {
	extra := count - adtyIncludedCustomHostnames
	if extra < 0 {
		extra = 0
	}
	return decimal.NewFromInt(extra).Mul(decimal.RequireFromString(adtyExtraHostnameMonthlyUSD))
}

func adtyCustomerTopUpOrders(db *gorm.DB) *gorm.DB {
	return db.Where("COALESCE(business, '') NOT IN ?", []string{model.AdtyBusinessOwnerSelfUseTopup, model.AdtyBusinessOwnerMarginTopup}).
		Where("COALESCE(provider, '') <> ? OR method IN ?", model.AdtyVoucherProvider, []string{model.AdtyVoucherPurposeMainUser, model.AdtyVoucherPurposeSiteCustomer})
}

// GetAdtySitesOverview 汇总 [from, to] 内所有分站，各项是日汇总的直接合计。
func GetAdtySitesOverview(from, to string) (*AdtySitesOverview, error) {
	loc, _, err := loadAdtyBusinessLocation()
	if err != nil {
		return nil, err
	}
	from, to, err = adtyStatsRange(from, to, loc)
	if err != nil {
		return nil, err
	}
	result := &AdtySitesOverview{From: from, To: to, Sites: map[string]int{}}
	var statuses []struct {
		Status string
		Total  int
	}
	if err := model.DB.Model(&model.AdtySite{}).Select("status, COUNT(*) AS total").Group("status").Scan(&statuses).Error; err != nil {
		return nil, err
	}
	for _, row := range statuses {
		result.Sites[row.Status] = row.Total
		if row.Status == model.AdtySiteStatusPending {
			result.PendingApplicants = int64(row.Total)
		}
	}
	if err := model.DB.Model(&model.User{}).Where("site_id > 0").Count(&result.SiteCustomers).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Model(&model.User{}).Where("site_id = 0").Count(&result.MainCustomers).Error; err != nil {
		return nil, err
	}
	rows, err := adtySiteEarningRows(nil, from, to)
	if err != nil {
		return nil, err
	}
	var total AdtySiteDayStats
	for _, row := range rows {
		adtyApplyEarningRow(&total, row)
	}
	result.SiteRevenueUSD, result.SitePlatformUSD, result.SiteEarningUSD = total.RevenueUSD, total.PlatformUSD, total.EarningUSD
	result.SiteSkippedUSD, result.SiteBonusUSD = total.SkippedUSD, total.BonusUSD

	startUnix, endUnix := adtyDayUnix(from, false, loc), adtyDayUnix(to, true, loc)
	orders, err := adtyCreditedCustomerOrders(nil, startUnix, endUnix)
	if err != nil {
		return nil, err
	}
	for _, order := range orders {
		kept := adtyRetainedTopUpUSD(&order.AdtyTopupOrder)
		if order.SiteId > 0 {
			result.SiteTopUpUSD = result.SiteTopUpUSD.Add(kept)
		} else {
			result.MainTopUpUSD = result.MainTopUpUSD.Add(kept)
		}
	}
	if err := model.DB.Model(&model.AdtySiteReport{}).Where("status = ?", model.AdtySiteReportPending).Count(&result.PendingReports).Error; err != nil {
		return nil, err
	}
	if err := model.AdtyLiveSiteCustomer(model.DB.Model(&model.AdtySiteCustomer{})).Where("risk_flag <> '' AND reviewed = ?", false).Count(&result.FlaggedCustomers).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Model(&model.AdtySiteDomain{}).
		Where("status IN ?", []string{model.AdtySiteDomainPending, model.AdtySiteDomainActive}).
		Count(&result.CustomHostnames).Error; err != nil {
		return nil, err
	}
	result.HostnameMonthlyFeeUSD = adtyHostnameMonthlyFee(result.CustomHostnames)
	var hookSum struct {
		Total int64
	}
	if err := model.DB.Model(&model.AdtySite{}).Select("COALESCE(SUM(earning_hook_failures),0) AS total").Scan(&hookSum).Error; err != nil {
		return nil, err
	}
	result.EarningHookFailures = hookSum.Total
	var held []model.AdtySite
	if err := model.DB.Select("id", "subdomain", "settle_hold_reason").Where("settle_hold_reason <> ''").Order("id").Find(&held).Error; err != nil {
		return nil, err
	}
	result.SettlementHolds = make([]AdtySettlementHold, 0, len(held))
	for _, site := range held {
		result.SettlementHolds = append(result.SettlementHolds, AdtySettlementHold{
			SiteId: site.Id, Subdomain: site.Subdomain, SettleHoldReason: site.SettleHoldReason,
		})
	}
	return result, nil
}

// AdtySiteBalances 是站长中心收益页顶部的数字：待结算、可提现、已提现、风控不计。
type AdtySiteBalances struct {
	RequiresReview bool            `json:"requires_review,omitempty"`
	PendingUSD     decimal.Decimal `json:"pending_usd"`
	SkippedUSD     decimal.Decimal `json:"skipped_usd"`
	AvailableUSD   decimal.Decimal `json:"available_usd"` // 站点收益钱包可提现余额（balance - frozen_usd）
	FrozenUSD      decimal.Decimal `json:"frozen_usd"`
	WithdrawnUSD   decimal.Decimal `json:"withdrawn_usd"`
}

// GetAdtySiteBalances retains the administrative site totals.
func GetAdtySiteBalances(site *model.AdtySite) (*AdtySiteBalances, error) {
	return getAdtySiteBalances(site, 0)
}

func GetAdtyOwnerBalances(site *model.AdtySite, ownerID int) (*AdtySiteBalances, error) {
	if ownerID <= 0 {
		return nil, ErrAdtySiteNotOwner
	}
	return getAdtySiteBalances(site, ownerID)
}

func getAdtySiteBalances(site *model.AdtySite, ownerID int) (*AdtySiteBalances, error) {
	result := &AdtySiteBalances{}
	if site == nil || site.Id <= 0 {
		return result, nil
	}
	q := model.DB.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ? AND status = ?", site.Id, model.AdtySiteEarningPending)
	withdrawals := model.DB.Model(&model.AdtySiteWithdrawal{}).Where("site_id = ? AND status = ?", site.Id, model.AdtySiteWithdrawPaid)
	if ownerID > 0 {
		q = q.Scopes(model.AdtyFinancialOwnerScope(site.Id, ownerID))
		withdrawals = withdrawals.Scopes(model.AdtyWithdrawalOwnerScope(site.Id, ownerID))
	}
	var sums []struct {
		Kind  string
		Total decimal.Decimal
	}
	if err := q.Select("kind, SUM(earning) AS total").Group("kind").Scan(&sums).Error; err != nil {
		return nil, err
	}
	for _, sum := range sums {
		switch sum.Kind {
		case model.AdtySiteEarningSkipped:
			result.SkippedUSD = sum.Total
		case model.AdtySiteEarningUsage, model.AdtySiteEarningRefund:
			result.PendingUSD = result.PendingUSD.Add(sum.Total)
		}
	}
	if err := withdrawals.Select("COALESCE(SUM(amount_usd),0)").Scan(&result.WithdrawnUSD).Error; err != nil {
		return nil, err
	}
	var wallet model.AdtySiteWallet
	if err := model.DB.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).Limit(1).Find(&wallet).Error; err != nil {
		return nil, err
	}
	result.AvailableUSD, result.FrozenUSD = wallet.Balance.Sub(wallet.FrozenUSD), wallet.FrozenUSD
	if ownerID <= 0 {
		return result, nil
	}
	available, frozen, err := ownerEarningsWalletTx(model.DB, site.Id, ownerID, &wallet)
	if err == ErrAdtyOwnerWalletReview {
		result.RequiresReview = true
		result.AvailableUSD, result.FrozenUSD = decimal.Zero, decimal.Zero
		return result, nil
	}
	result.AvailableUSD, result.FrozenUSD = available, frozen
	return result, err
}

// AdtyMonthlySeries 把按天的数据合成按月（2026-10）。付费和活跃客户数是按天去重的，不能相加，按月序列里不给（为 0）；
// 整段时间的去重人数看 AdtySiteStats.Total。
func AdtyMonthlySeries(series []AdtySiteDayStats) []AdtySiteDayStats {
	months := map[string]*AdtySiteDayStats{}
	keys := []string{}
	for _, day := range series {
		month := day.Day
		if len(month) >= 7 {
			month = month[:7]
		}
		if months[month] == nil {
			months[month] = &AdtySiteDayStats{Day: month}
			keys = append(keys, month)
		}
		adtyAddDayStats(months[month], day)
	}
	sort.Strings(keys)
	result := make([]AdtySiteDayStats, 0, len(keys))
	for _, key := range keys {
		result = append(result, *months[key])
	}
	return result
}
