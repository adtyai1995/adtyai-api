package service

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 分站定价按 G.2：供货价 = 模型基础价格 × 分站供货倍率 × 本次实际计费分组倍率，
// 普通售价 = 供货价 × (1 + 分站加价比例)。加价比例为 0 时售价等于供货价。
// 这些因子只乘进分组倍率，按倍率、按次、阶梯、图片、任务提交、工具附加费、违规扣费都从这条路径走，
// 不在 HandleGroupRatio 的 Realtime 预扣和任务按 token 重算里再乘一次。
// 加价幅度不进 PriceData.OtherRatios（会出现在客户日志的计价参数里）。
//
// 存储字段 Markup 仍是历史上的 k 倍率（库默认 1，界面也按倍率录入）。
// 1 表示不加价，因此计费时 加价比例 = k - 1；保存值 1.2 是 20% 加价，不能当成 120%。
// CostFactor 是供货倍率，小于 1 的值要原样使用，不再把 k<1 回退成 1，也不再要求供货倍率 ≤ 1。

// gin 上下文里记下的仍是存储口径的 k 和供货倍率 d：同一个请求后面的消费日志都从这里取（11.5 的记账）。
const (
	AdtyOwnerSelfUseContextKey    = "adty_owner_self_use_site"
	AdtySiteMarkupContextKey      = "adty_site_markup"
	AdtyTeamDiscountContextKey    = "adty_team_discount"
	AdtyTeamPriceBlockedKey       = "adty_team_price_blocked"
	AdtySiteCostFactorContextKey  = "adty_site_cost_factor"
	AdtySiteBilledRatioContextKey = "adty_site_billed_ratio"
	AdtySiteIdContextKey          = "adty_site_id"
	AdtySiteOwnerContextKey       = "adty_site_owner_user_id"
)

var (
	ErrAdtySitePricingConflict = common.NewMessage("The prices were changed elsewhere. Reload and try again")
	ErrAdtySitePaused          = common.NewMessage("Prices cannot be changed while the site is paused")
)

func adtyNormalizeSitePriceModel(name string) string {
	return strings.TrimSpace(name)
}

func adtySitePriceKey(group, modelName string) string {
	return group + "" + adtyNormalizeSitePriceModel(modelName)
}

func adtySitePrices(siteID, userID int) ([]model.AdtySitePrice, error) {
	var rows []model.AdtySitePrice
	err := model.DB.Where("site_id = ? AND user_id IN ?", siteID, []int{0, userID}).Order("id").Find(&rows).Error
	return rows, err
}

// Load the price rows and both scope counters in one statement so a concurrent
// edit cannot pair an old price with a new revision. The site anchors the joins
// even after the last override is deleted.
func adtyAdmittedSitePrice(siteID, userID int, group, modelName string) (model.AdtySitePrice, string, error) {
	var rows []struct {
		model.AdtySitePrice
		SiteVersion     int64
		CustomerVersion int64
	}
	err := model.DB.Model(&model.AdtySite{}).
		Select("prices.*, COALESCE(site_scope.version, 0) AS site_version, COALESCE(customer_scope.version, 0) AS customer_version").
		Joins("LEFT JOIN adty_site_price_scopes AS site_scope ON site_scope.site_id = adty_sites.id AND site_scope.user_id = 0").
		Joins("LEFT JOIN adty_site_price_scopes AS customer_scope ON customer_scope.site_id = adty_sites.id AND customer_scope.user_id = ?", userID).
		Joins("LEFT JOIN adty_site_prices AS prices ON prices.site_id = adty_sites.id AND prices.user_id IN ?", []int{0, userID}).
		Where("adty_sites.id = ?", siteID).Order("prices.id").Scan(&rows).Error
	if err != nil {
		return model.AdtySitePrice{}, "", err
	}
	if len(rows) == 0 {
		return model.AdtySitePrice{}, "", gorm.ErrRecordNotFound
	}
	prices := make([]model.AdtySitePrice, len(rows))
	for i, row := range rows {
		prices[i] = row.AdtySitePrice
	}
	selected := resolveAdtySitePrice(prices, userID, group, modelName)
	revision := fmt.Sprintf("site:%d:v%d:user:%d:row:%d:v%d", siteID, rows[0].SiteVersion, selected.UserId, selected.Id, selected.Version)
	if rows[0].CustomerVersion != 0 {
		revision += fmt.Sprintf(":customer:%d:v%d", userID, rows[0].CustomerVersion)
	}
	return selected, revision, nil
}

// AdtyBillingSiteForUser 是用户所属、并且按分站价扣费的站点：active、paused、closing 都按分站价，
// 其余（pending、closed、查不到）返回 nil。充值下单也用它决定加赠档位和谁出加赠。
func AdtyBillingSiteForUser(userId int) *model.AdtySite {
	if userId <= 0 || !resellerSetting().Enabled {
		return nil
	}
	site := AdtySiteForUser(userId)
	if !AdtySiteServesCustomers(site) {
		return nil
	}
	return site
}

// AdtySiteMarkup 返回分站客户在这个分组、这个模型上的存储 k（加价比例 = k-1）；主站客户、站点没有开着时返回 1。
// modelName 为空时使用该客户/分组层级的默认加价。
func AdtySiteMarkup(userId int, group, modelName string) decimal.Decimal {
	site := AdtyBillingSiteForUser(userId)
	if site == nil {
		return decimal.NewFromInt(1)
	}
	prices, err := adtySitePrices(site.Id, userId)
	if err != nil {
		common.SysError(common.LogText("load adty site prices failed: %s", err.Error()))
		return decimal.NewFromInt(1)
	}
	return ResolveAdtySiteMarkup(prices, userId, group, modelName)
}

// AdtySiteCostFactor 是站点当前的供货倍率：单站设的值 → 业绩档位 → 全局；低于 min_cost_factor 时用下限。
// 供货倍率是正的乘数，可以小于或大于 1。全局值没设时返回 false（这时分站功能本来就打不开）。
func AdtySiteCostFactor(site *model.AdtySite) (decimal.Decimal, bool) {
	cfg := resellerSetting()
	var d decimal.Decimal
	switch {
	case site != nil && site.CostFactor != nil:
		d = *site.CostFactor
	case site != nil && site.TierCostFactor != nil && (len(cfg.CostFactorTiers) > 0 || site.TierCode != ""):
		d = *site.TierCostFactor
	case cfg.CostFactor != nil:
		d = decimal.NewFromFloat(*cfg.CostFactor)
	default:
		return decimal.Zero, false
	}
	if cfg.MinCostFactor != nil {
		d = decimal.Max(d, decimal.NewFromFloat(*cfg.MinCostFactor))
	}
	if !d.IsPositive() {
		return decimal.Zero, false
	}
	return d, true
}

// adtyStoredMarkupRatio 把存储的 k 换成已确认的加价比例。历史字段是倍率，默认 1 表示不加价，所以比例 = k - 1。
func adtyStoredMarkupRatio(k decimal.Decimal) decimal.Decimal {
	return k.Sub(decimal.NewFromInt(1))
}

// adtyMarkupFloat 把存储的 k 转成 float 给上下文和快照用。NaN、Inf 或非正数按 1（不加价）处理并记日志；0<k<1 原样返回。
func adtyMarkupFloat(k decimal.Decimal, userId int, group string) float64 {
	return adtyValidStoredMarkup(k, userId, group).InexactFloat64()
}

func adtyDecimalFloatInvalid(d decimal.Decimal) bool {
	value := d.InexactFloat64()
	return math.IsNaN(value) || math.IsInf(value, 0)
}

func adtyValidStoredMarkup(k decimal.Decimal, userId int, group string) decimal.Decimal {
	if !k.IsPositive() || adtyDecimalFloatInvalid(k) {
		common.SysError(common.LogText("invalid site markup %s for user %d group %s, using 1", k.String(), userId, group))
		return decimal.NewFromInt(1)
	}
	return k
}

func adtyDecimalFiniteNonNegative(d decimal.Decimal) bool {
	value := d.InexactFloat64()
	return !math.IsNaN(value) && !math.IsInf(value, 0) && !d.IsNegative()
}

// adtySiteOrdinarySellRatioDec 用精确十进制计算：供货价因子 = 分组倍率 × 供货倍率，
// 普通售价因子 = 供货价因子 × (1 + 加价比例)。计费结果不再经过 float64 往返。
func adtySiteOrdinarySellRatioDec(groupRatio, k, costFactor decimal.Decimal, userId int, group string) (marked, storedK decimal.Decimal, ok bool) {
	storedK = k
	if !storedK.IsPositive() || adtyDecimalFloatInvalid(storedK) {
		storedK = decimal.NewFromInt(1)
	}
	if !costFactor.IsPositive() {
		common.SysError(common.LogText("invalid site cost factor %s for user %d group %s, leaving group ratio unchanged", costFactor.String(), userId, group))
		return groupRatio, decimal.NewFromInt(1), false
	}
	marked = groupRatio.Mul(costFactor).Mul(decimal.NewFromInt(1).Add(adtyStoredMarkupRatio(storedK)))
	if !adtyDecimalFiniteNonNegative(marked) {
		common.SysError(common.LogText("site sell price overflow for user %d group %s, leaving group ratio unchanged", userId, group))
		return groupRatio, decimal.NewFromInt(1), false
	}
	return marked, storedK, true
}

func adtyRememberBilledRatio(c *gin.Context, billed decimal.Decimal) {
	if c != nil {
		c.Set(AdtySiteBilledRatioContextKey, billed)
	}
}

// AdtyBilledGroupRatioFromContext 取出本次请求按精确十进制算出的计费分组倍率。
func AdtyBilledGroupRatioFromContext(c *gin.Context) (decimal.Decimal, bool) {
	if c == nil {
		return decimal.Zero, false
	}
	value, ok := c.Get(AdtySiteBilledRatioContextKey)
	if !ok {
		return decimal.Zero, false
	}
	billed, ok := value.(decimal.Decimal)
	return billed, ok
}

// AdtyApplySiteMarkupDecimal 按已确认公式用精确十进制计算供货价、售价和团队下限。
// 返回最终计费分组倍率和存储 k（1 表示加价比例为 0）。
func AdtyApplySiteMarkupDecimal(c *gin.Context, userId int, group string, groupRatio decimal.Decimal, modelName string) (billed, storedK decimal.Decimal) {
	if snap := AdtyFrozenPriceSnapshot(c); snap != nil {
		if billed, ok := snap.BilledGroupRatioDecimal(); ok {
			k, ok := model.ParseSnapshotDecimal(snap.StoredMarkupK)
			if !ok {
				k = decimal.NewFromInt(1)
			}
			return billed, k
		}
	}
	if c != nil {
		if value, ok := c.Get(AdtyOwnerSelfUseContextKey); ok {
			site, ok := value.(*model.AdtySite)
			one := decimal.NewFromInt(1)
			if !ok || site == nil || site.OwnerUserId != userId {
				c.Set(AdtyTeamPriceBlockedKey, true)
				return groupRatio, one
			}
			d, ok := AdtySiteCostFactor(site)
			if !ok {
				c.Set(AdtyTeamPriceBlockedKey, true)
				return groupRatio, one
			}
			price := groupRatio.Mul(d)
			f := adtyDefaultPriceFactors(group, groupRatio)
			f.MainSite = false
			f.SiteId = site.Id
			f.SiteOwnerUserId = userId
			f.CostFactor = d
			f.SupplyPrice = price
			f.SellPrice = price
			adtyRememberPriceFactors(c, f)
			adtyRememberBilledRatio(c, price)
			c.Set(AdtySiteIdContextKey, site.Id)
			c.Set(AdtySiteOwnerContextKey, userId)
			c.Set(AdtySiteMarkupContextKey, float64(1))
			c.Set(AdtySiteCostFactorContextKey, d.InexactFloat64())
			return price, one
		}
	}
	marked, storedK := adtyApplySiteMarkupOnlyDecimal(c, userId, group, groupRatio, modelName)
	billed, storedK = adtyApplyTeamProtocolPriceDecimal(c, userId, groupRatio, marked, storedK)
	adtyRememberBilledRatio(c, billed)
	return billed, storedK
}

// AdtyApplySiteMarkup 在算出分组倍率之后调用：按供货倍率和加价比例改分组倍率，并把 k、d 写进 gin 上下文。
// 返回普通售价对应的分组倍率和存储 k（1 表示加价比例为 0）。上游的 HandleGroupRatio、Realtime 预扣都调它。
// 财务权威结果走 AdtyApplySiteMarkupDecimal；这里的 float64 仅留给仍使用浮点的调用方和快照字段。
func AdtyApplySiteMarkup(c *gin.Context, userId int, group string, groupRatio float64, modelName string) (float64, float64) {
	billed, storedK := AdtyApplySiteMarkupDecimal(c, userId, group, decimal.NewFromFloat(groupRatio), modelName)
	return billed.InexactFloat64(), storedK.InexactFloat64()
}

func adtyApplySiteMarkupOnlyDecimal(c *gin.Context, userId int, group string, groupRatio decimal.Decimal, modelName string) (decimal.Decimal, decimal.Decimal) {
	one := decimal.NewFromInt(1)
	factors := adtyDefaultPriceFactors(group, groupRatio)
	site := AdtyBillingSiteForUser(userId)
	if site == nil {
		adtyRememberPriceFactors(c, factors)
		return groupRatio, one
	}
	factors.MainSite = false
	factors.SiteId = site.Id
	selected, revision, err := adtyAdmittedSitePrice(site.Id, userId, group, modelName)
	if err != nil {
		if c != nil {
			c.Set(AdtyTeamPriceBlockedKey, true)
		}
		common.SysError(common.LogText("load adty site prices failed: %s", err.Error()))
		return groupRatio, one
	}
	factors.RuleVersion = revision
	kDec := selected.Markup
	kUsed := adtyValidStoredMarkup(kDec, userId, group)
	k := kUsed.InexactFloat64()
	d, dOK := AdtySiteCostFactor(site)
	if c != nil {
		c.Set(AdtySiteMarkupContextKey, k)
		c.Set(AdtySiteIdContextKey, site.Id)
		ownerID, err := model.CurrentAdtySiteFinancialOwnerTx(model.DB, site.Id)
		if err != nil {
			common.SysError("Request earning owner snapshot unavailable")
		}
		c.Set(AdtySiteOwnerContextKey, ownerID)
		factors.SiteOwnerUserId = ownerID
		if dOK {
			c.Set(AdtySiteCostFactorContextKey, d.InexactFloat64())
		}
	}
	if !dOK {
		common.SysError(common.LogText("site cost factor missing for user %d group %s, leaving group ratio unchanged", userId, group))
		adtyRememberPriceFactors(c, factors)
		return groupRatio, one
	}
	marked, storedK, ok := adtySiteOrdinarySellRatioDec(groupRatio, kUsed, d, userId, group)
	if !ok {
		adtyRememberPriceFactors(c, factors)
		return marked, storedK
	}
	factors.StoredK = storedK
	factors.MarkupRatio = adtyStoredMarkupRatio(storedK)
	factors.CostFactor = d
	factors.SupplyPrice = groupRatio.Mul(d)
	factors.SellPrice = marked
	adtyRememberPriceFactors(c, factors)
	return marked, storedK
}

// adtyApplyTeamProtocolPriceDecimal 按已确认口径把团队折后支付比例乘进分站普通售价：
// 分站团队折扣计算价 = 分站普通售价 × 分站团队折后支付比例（0.8 表示支付普通售价的 80%），
// 分站团队最终价 = max(分站供货价, 分站团队折扣计算价)。
// 供货价沿用 ISS-R1-0009：分组倍率 × 供货倍率。主站团队没有供货价下限（G.2-5）。
// 支付比例无效或结果溢出时仍置 AdtyTeamPriceBlockedKey；折后价低于供货价时改为按供货价计费，不再拒绝。
func adtyApplyTeamProtocolPriceDecimal(c *gin.Context, userId int, groupRatio, marked, storedK decimal.Decimal) (decimal.Decimal, decimal.Decimal) {
	if c == nil || userId <= 0 {
		return marked, storedK
	}
	tokenId := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	if tokenId <= 0 || model.DB == nil || !adtyTeamTableReadyFor(model.DB, &model.AdtyTeamKey{}) {
		return marked, storedK
	}
	var binding model.AdtyTeamKey
	lookup := model.DB.Where("token_id = ? AND user_id = ? AND status = ?", tokenId, userId, model.AdtyTeamMemberActive).Limit(1).Find(&binding)
	if lookup.Error != nil {
		c.Set(AdtyTeamPriceBlockedKey, true)
		return marked, storedK
	}
	if lookup.RowsAffected == 0 {
		return marked, storedK
	}
	var team model.AdtyTeam
	if err := model.DB.First(&team, binding.TeamId).Error; err != nil || team.Status != model.AdtyTeamStatusActive {
		c.Set(AdtyTeamPriceBlockedKey, true)
		return marked, storedK
	}
	factorDec := team.Discount
	if !adtySiteTeamPayRatioConfigValid(factorDec) || adtyDecimalFloatInvalid(factorDec) {
		c.Set(AdtyTeamPriceBlockedKey, true)
		return marked, storedK
	}
	discounted := marked.Mul(factorDec)
	nextDec := discounted
	var supply decimal.Decimal
	effectiveK := storedK
	floorApplied := false
	// 只有分站团队才有供货价下限；主站团队即使有很深的折扣也不抬价。
	if team.SiteId > 0 {
		var site model.AdtySite
		if err := model.DB.First(&site, team.SiteId).Error; err != nil || !AdtySiteServesCustomers(&site) {
			c.Set(AdtyTeamPriceBlockedKey, true)
			return marked, storedK
		}
		d, ok := AdtySiteCostFactor(&site)
		if !ok {
			c.Set(AdtyTeamPriceBlockedKey, true)
			return marked, storedK
		}
		supply = groupRatio.Mul(d)
		nextDec = decimal.Max(supply, discounted)
		floorApplied = true
		if supply.IsPositive() {
			effectiveK = decimal.Max(decimal.NewFromInt(1), nextDec.Div(supply))
		} else {
			effectiveK = decimal.Max(decimal.NewFromInt(1), storedK.Mul(factorDec))
		}
	}
	if !adtyDecimalFiniteNonNegative(nextDec) {
		c.Set(AdtyTeamPriceBlockedKey, true)
		return marked, storedK
	}
	if floorApplied {
		storedK = effectiveK
		c.Set(AdtySiteMarkupContextKey, storedK.InexactFloat64())
	}
	c.Set(AdtyTeamDiscountContextKey, factorDec.InexactFloat64())
	factors, ok := adtyPriceFactorsFromContext(c)
	if !ok {
		factors = adtyDefaultPriceFactors("", groupRatio)
		factors.SellPrice = marked
		factors.StoredK = storedK
		factors.MarkupRatio = adtyStoredMarkupRatio(storedK)
	}
	factors.TeamApplied = true
	factors.TeamPayRatio = factorDec
	if floorApplied {
		factors.StoredK = storedK
		factors.FloorApplied = true
		factors.SupplyFloor = supply
	}
	adtyRememberPriceFactors(c, factors)
	return nextDec, storedK
}

// AdtyTeamPriceBlocked 表示本次请求的团队支付比例无效或折后价溢出，计费应拒绝。
// 折后价低于供货价不再走这条拒绝路径。
func AdtyTeamPriceBlocked(c *gin.Context) bool {
	return c != nil && c.GetBool(AdtyTeamPriceBlockedKey)
}

// adtySiteTeamPayRatioConfigValid 判断分站团队折后支付比例在配置时是否可保存。
// 计费按 G.2：最终价 = max(分站供货价, 分站普通售价×折后支付比例)，折后价低于供货价时抬到供货价。
// 因此配置时不得把支付比例本身和供货倍率比较后拒绝；仅拒绝非正或大于 1 的比例。
func adtySiteTeamPayRatioConfigValid(ratio decimal.Decimal) bool {
	return ratio.IsPositive() && !ratio.GreaterThan(decimal.NewFromInt(1)) && ratio.Equal(ratio.Truncate(4))
}

// AdtyApplySiteMarkupHook 是 HandleGroupRatio 调用的入口（relay/helper/price.go），测试可以替换。
var AdtyApplySiteMarkupHook = AdtyApplySiteMarkup

// AdtySiteMarkupFromContext 取同一个请求里记下的 k 和 d；没有时 ok 为 false。
func AdtySiteMarkupFromContext(c *gin.Context) (k, d float64, ok bool) {
	if c == nil {
		return 0, 0, false
	}
	k = c.GetFloat64(AdtySiteMarkupContextKey)
	d = c.GetFloat64(AdtySiteCostFactorContextKey)
	return k, d, k > 0 && d > 0
}

// AdtyAppendSiteMarkupAdminInfo 把 k、d 记进日志的 admin_info，后台核对用；普通用户的日志投影看不到 admin_info。
func AdtyAppendSiteMarkupAdminInfo(c *gin.Context, other *model.LogOther) {
	k, d, ok := AdtySiteMarkupFromContext(c)
	if !ok || other == nil {
		return
	}
	other.SetAdmin("site_markup", k)
	other.SetAdmin("site_cost_factor", d)
	if siteId := c.GetInt(AdtySiteIdContextKey); siteId > 0 {
		other.SetAdmin("site_id", siteId)
		other.SetAdmin("site_owner_user_id", c.GetInt(AdtySiteOwnerContextKey))
	}
}

// AdtySitePricingInput 是站长保存定价的请求：默认 k、按分组的 k、按模型的独立 k，带乐观锁版本。
type AdtySitePricingInput struct {
	SiteId          int
	UserId          int // 0 = 全站定价；否则是这个客户的单独定价
	ActorId         int // 变更操作人（0 时默认取站点当前 OwnerUserId）
	DefaultMarkup   *decimal.Decimal
	ClearDefault    bool
	GroupMarkups    map[string]decimal.Decimal // 值为 0 表示删掉这个分组的单独价
	ModelMarkups    map[string]decimal.Decimal // 值为 0 表示删掉这个模型的独立加价，恢复全局
	ExpectedVersion int64
	Now             int64
}

func adtyEnsurePricingTables() {
	if model.DB == nil {
		return
	}
	if !model.DB.Migrator().HasTable(&model.AdtySitePriceScope{}) {
		_ = model.DB.AutoMigrate(&model.AdtySitePriceScope{})
	}
	if !model.DB.Migrator().HasTable(&model.AdtyPriceRevision{}) {
		_ = model.DB.AutoMigrate(&model.AdtyPriceRevision{})
	}
}

func adtyUpsertPriceScopeVersion(tx *gorm.DB, siteId, userId int, version, now int64) error {
	scope := model.AdtySitePriceScope{SiteId: siteId, UserId: userId, Version: version, UpdatedAt: now}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "site_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"version", "updated_at"}),
	}).Create(&scope).Error
}

// AdtySitePricingVersion 返回指定站点与客户作用域（userId=0 为全站）当前的持久定价版本号。
func AdtySitePricingVersion(siteId, userId int) (int64, error) {
	return adtySitePricingVersionTx(model.DB, siteId, userId)
}

func adtySitePricingVersionTx(tx *gorm.DB, siteId, userId int) (int64, error) {
	var version int64
	if err := tx.Model(&model.AdtySitePrice{}).Where("site_id = ? AND user_id = ?", siteId, userId).Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
		return 0, err
	}
	var scope model.AdtySitePriceScope
	result := tx.Where("site_id = ? AND user_id = ?", siteId, userId).Limit(1).Find(&scope)
	if result.Error != nil {
		return 0, result.Error
	}
	return max(version, scope.Version), nil
}

func adtyPriceRevisionKind(userId int, group, modelName string) string {
	prefix := ""
	if userId > 0 {
		prefix = "customer:"
	}
	var kind string
	switch {
	case modelName != "":
		kind = prefix + "model:" + modelName
	case group != "":
		kind = prefix + "group:" + group
	default:
		if prefix != "" {
			kind = "customer:default"
		} else {
			kind = "default"
		}
	}
	if len(kind) > 32 {
		kind = kind[:32]
	}
	return kind
}

// ListAdtySitePriceRevisions 按时间顺序返回指定站点的加价变更历史（追加只读）。
func ListAdtySitePriceRevisions(siteId int) ([]model.AdtyPriceRevision, error) {
	if model.DB == nil || siteId <= 0 {
		return nil, nil
	}
	adtyEnsurePricingTables()
	var revisions []model.AdtyPriceRevision
	if err := model.DB.Where("site_id = ?", siteId).Order("id asc").Find(&revisions).Error; err != nil {
		return nil, err
	}
	return revisions, nil
}

// Newly saved retail prices take effect at commit. Pending maps are retained for API compatibility.
type AdtySitePricingResult struct {
	Version        int64                 `json:"version"`
	Immediate      []string              `json:"immediate"`
	Pending        map[string]int64      `json:"pending"` // 分组（空串是默认）→ 生效时间
	ModelImmediate []string              `json:"model_immediate,omitempty"`
	ModelPending   map[string]int64      `json:"model_pending,omitempty"`
	Prices         []model.AdtySitePrice `json:"prices"`
	Raised         []model.AdtySitePrice `json:"-"`
	Site           *model.AdtySite       `json:"-"`
}

// Retail multipliers must fit the persisted NUMERIC(10,4) column exactly.
func validRetailMarkup(k decimal.Decimal) bool {
	return k.IsPositive() && k.Exponent() >= -32 && k.Exponent() <= 5 && k.NumDigits() <= 38 &&
		k.LessThan(decimal.New(1, 6)) && k.Equal(k.Truncate(4))
}

// SaveAdtySitePricing commits overrides and immutable history together under the site lock.
func SaveAdtySitePricing(input AdtySitePricingInput) (*AdtySitePricingResult, error) {
	now := input.Now
	if now == 0 {
		now = time.Now().Unix()
	}
	result := &AdtySitePricingResult{Pending: map[string]int64{}, ModelPending: map[string]int64{}}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", input.SiteId).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		switch site.Status {
		case model.AdtySiteStatusActive, model.AdtySiteStatusPaused:
			// leftover paused rows are not a live pause mode; pricing stays editable
		default:
			return ErrAdtySiteStatus
		}
		type priceChange struct {
			group, model string
			markup       decimal.Decimal
		}
		var changes []priceChange
		if input.ClearDefault {
			if input.DefaultMarkup != nil {
				return common.NewMessage("Invalid parameters")
			}
			changes = append(changes, priceChange{})
		} else if input.DefaultMarkup != nil {
			changes = append(changes, priceChange{markup: *input.DefaultMarkup})
		}
		if len(input.GroupMarkups) > 0 {
			groupKeys := make([]string, 0, len(input.GroupMarkups))
			for group := range input.GroupMarkups {
				groupKeys = append(groupKeys, group)
			}
			sort.Strings(groupKeys)
			for _, rawGroup := range groupKeys {
				markup := input.GroupMarkups[rawGroup]
				group := strings.TrimSpace(rawGroup)
				if group == "" || len(group) > 64 {
					return common.NewMessage("Invalid parameters")
				}
				changes = append(changes, priceChange{group: group, markup: markup})
			}
		}
		if len(input.ModelMarkups) > 0 {
			modelKeys := make([]string, 0, len(input.ModelMarkups))
			for modelName := range input.ModelMarkups {
				modelKeys = append(modelKeys, modelName)
			}
			sort.Strings(modelKeys)
			for _, rawModel := range modelKeys {
				markup := input.ModelMarkups[rawModel]
				modelName := adtyNormalizeSitePriceModel(rawModel)
				if modelName == "" || len(modelName) > 191 {
					return common.NewMessage("Invalid parameters")
				}
				changes = append(changes, priceChange{model: modelName, markup: markup})
			}
		}
		seenKeys := make(map[string]bool, len(changes))
		for _, change := range changes {
			key := adtySitePriceKey(change.group, change.model)
			if seenKeys[key] {
				return common.NewMessage("Invalid parameters")
			}
			seenKeys[key] = true
			if change.markup.IsZero() && (change.group != "" || change.model != "" || input.ClearDefault) {
				continue
			}
			if !validRetailMarkup(change.markup) {
				return common.NewMessage("Enter a positive retail multiplier with up to six integer digits and four decimal places")
			}
		}
		var rows []model.AdtySitePrice
		if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND user_id = ?", site.Id, input.UserId).Order("id asc").Find(&rows).Error; err != nil {
			return err
		}
		existing := map[string]model.AdtySitePrice{}
		var duplicateIDs []int
		for _, row := range rows {
			key := adtySitePriceKey(strings.TrimSpace(row.Group), adtyNormalizeSitePriceModel(row.Model))
			if prev, dup := existing[key]; dup {
				duplicateIDs = append(duplicateIDs, prev.Id)
			}
			existing[key] = row
		}
		if len(duplicateIDs) > 0 {
			if err := tx.Where("id IN ?", duplicateIDs).Delete(&model.AdtySitePrice{}).Error; err != nil {
				return err
			}
		}
		version, err := adtySitePricingVersionTx(tx, site.Id, input.UserId)
		if err != nil {
			return err
		}
		if version != input.ExpectedVersion {
			return ErrAdtySitePricingConflict
		}
		next := version + 1
		actorId := input.ActorId
		if actorId <= 0 {
			actorId = site.OwnerUserId
		}
		recordRevision := func(group, modelName, beforeVal, afterVal string) error {
			before, existed := existing[adtySitePriceKey(group, modelName)]
			var after model.AdtySitePrice
			saved := tx.Where(map[string]any{"site_id": site.Id, "user_id": input.UserId, "group": group, "model": modelName}).Limit(1).Find(&after)
			if saved.Error != nil {
				return saved.Error
			}
			action := "immediate"
			if saved.RowsAffected == 0 {
				action = "deleted"
			}
			kind := adtyPriceRevisionKind(input.UserId, group, modelName)
			beforeState, encodeErr := common.Marshal(map[string]any{"override": existed, "price": before})
			if encodeErr != nil {
				return encodeErr
			}
			afterState, encodeErr := common.Marshal(map[string]any{"override": saved.RowsAffected > 0, "price": after})
			if encodeErr != nil {
				return encodeErr
			}
			rev := model.AdtyPriceRevision{
				SiteId:  site.Id,
				ActorId: actorId,
				UserId:  input.UserId, Group: group, Model: modelName, Kind: kind,
				Action: action, Version: next, EffectiveAt: now,
				BeforeState: string(beforeState), AfterState: string(afterState),
				Before:    beforeVal,
				After:     afterVal,
				CreatedAt: now,
			}
			return tx.Create(&rev).Error
		}
		recordImmediate := func(change priceChange) {
			if change.model != "" {
				result.ModelImmediate = append(result.ModelImmediate, change.model)
				return
			}
			result.Immediate = append(result.Immediate, change.group)
		}
		// The history's before values come from one pre-edit view. Deletions inherit
		// the final same-save parent, so default edits precede group and model edits.
		var baseline []model.AdtySitePrice
		if err := tx.Where("site_id = ? AND user_id IN ?", site.Id, []int{0, input.UserId}).Order("id").Find(&baseline).Error; err != nil {
			return err
		}
		for _, change := range changes {
			group, modelName := change.group, change.model
			row, found := existing[adtySitePriceKey(group, modelName)]
			before := ResolveAdtySiteMarkup(baseline, input.UserId, group, modelName)
			after := change.markup
			if after.IsZero() {
				if !found {
					continue
				}
				if err := tx.Delete(&row).Error; err != nil {
					return err
				}
				var remaining []model.AdtySitePrice
				if err := tx.Where("site_id = ? AND user_id IN ?", site.Id, []int{0, input.UserId}).Order("id").Find(&remaining).Error; err != nil {
					return err
				}
				after = ResolveAdtySiteMarkup(remaining, input.UserId, group, modelName)
			} else if found {
				if err := tx.Model(&row).Updates(map[string]any{
					"markup": after, "pending_markup": decimal.Zero, "pending_at": 0, "version": next, "updated_at": now,
				}).Error; err != nil {
					return err
				}
			} else {
				row = model.AdtySitePrice{SiteId: site.Id, UserId: input.UserId, Group: group, Model: modelName, Markup: after, Version: next, UpdatedAt: now}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			}
			if err := recordRevision(group, modelName, before.String(), after.String()); err != nil {
				return err
			}
			recordImmediate(change)
			if after.GreaterThan(before) {
				result.Raised = append(result.Raised, model.AdtySitePrice{Group: group, Model: modelName, Markup: after, UpdatedAt: now})
			}
		}
		// 没有变更的行也要把版本推上去，这样下次带旧版本保存会冲突。
		if err := tx.Model(&model.AdtySitePrice{}).Where("site_id = ? AND user_id = ?", site.Id, input.UserId).Update("version", next).Error; err != nil {
			return err
		}
		if err := adtyUpsertPriceScopeVersion(tx, site.Id, input.UserId, next, now); err != nil {
			return err
		}
		result.Version, result.Site = next, &site
		return tx.Where("site_id = ? AND user_id = ?", site.Id, input.UserId).Order("id").Find(&result.Prices).Error
	})
	if err != nil {
		return nil, err
	}
	if len(result.Raised) > 0 && AdtyOnSiteMarkupIncrease != nil {
		AdtyOnSiteMarkupIncrease(result.Site, input.UserId, result.Raised)
	}
	return result, nil
}

// AdtyOnSiteMarkupIncrease 在涨价生效后调用（11.3 给受影响的客户发邮件，按客户所属站点的品牌）。
// userId 为 0 表示全站定价，影响这个站所有没有单独定价的客户。
var AdtyOnSiteMarkupIncrease func(site *model.AdtySite, userId int, raised []model.AdtySitePrice)

// ApplyDueAdtySiteMarkups honors pre-upgrade schedules until an owner supersedes the affected override.
func ApplyDueAdtySiteMarkups(now int64) (int64, error) {
	var dueRows []model.AdtySitePrice
	if err := model.DB.Where("pending_at > 0 AND pending_at <= ?", now).Order("id asc").Find(&dueRows).Error; err != nil {
		return 0, err
	}
	if len(dueRows) == 0 {
		return 0, nil
	}
	var changed int64
	for _, due := range dueRows {
		applied := false
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Match the save path's site -> price lock order. A concurrent save may cancel this due row.
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).First(&site, due.SiteId).Error; err != nil {
				return err
			}
			var row model.AdtySitePrice
			res := model.AdtyLockForUpdate(tx).Where("id = ? AND pending_at > 0 AND pending_at <= ?", due.Id, now).Limit(1).Find(&row)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return nil
			}
			version, err := adtySitePricingVersionTx(tx, row.SiteId, row.UserId)
			if err != nil {
				return err
			}
			next := version + 1
			after := row
			action := "activated"
			if row.PendingMarkup.IsPositive() {
				after.Markup = row.PendingMarkup
				after.PendingMarkup, after.PendingAt, after.Version, after.UpdatedAt = decimal.Zero, 0, next, now
				if err := tx.Model(&model.AdtySitePrice{}).Where("id = ?", row.Id).Updates(map[string]any{"markup": after.Markup, "pending_markup": 0, "pending_at": 0, "version": next, "updated_at": now}).Error; err != nil {
					return err
				}
			} else {
				var others []model.AdtySitePrice
				if err := tx.Where("site_id = ? AND id <> ?", site.Id, row.Id).Find(&others).Error; err != nil {
					return err
				}
				after = model.AdtySitePrice{Markup: ResolveAdtySiteMarkup(others, row.UserId, row.Group, row.Model)}
				action = "deletion_activated"
				if err := tx.Delete(&row).Error; err != nil {
					return err
				}
			}
			beforeState, err := common.Marshal(row)
			if err != nil {
				return err
			}
			afterState, err := common.Marshal(after)
			if err != nil {
				return err
			}
			kind := "default"
			if row.Model != "" {
				kind = "model"
			} else if row.Group != "" {
				kind = "group"
			}
			if err := tx.Create(&model.AdtyPriceRevision{SiteId: row.SiteId, UserId: row.UserId, Group: row.Group, Model: row.Model, Kind: kind, Action: action, Version: next, Before: row.Markup.String(), After: after.Markup.String(), BeforeState: string(beforeState), AfterState: string(afterState), EffectiveAt: now, CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := adtyUpsertPriceScopeVersion(tx, row.SiteId, row.UserId, next, now); err != nil {
				return err
			}
			applied = true
			return nil
		})
		if err != nil {
			return changed, err
		}
		if applied {
			changed++
		}
	}
	return changed, nil
}

// AssignAdtySiteCostTiers 按上个月每个站的客户消费额取档（cost_factor_tiers 设了才启用），写进 TierCostFactor。
// revenue 是「站点 id → 上月客户消费额（美元）」，由 11.5 的日汇总算出；month 形如 2026-10。
// 返回 d 变了的站，由调用方通知站长。
func AssignAdtySiteCostTiers(month string, revenue map[int]decimal.Decimal) ([]model.AdtySite, error) {
	tiers := resellerSetting().CostFactorTiers
	if len(tiers) == 0 {
		return nil, nil
	}
	var sites []model.AdtySite
	if err := model.DB.Where("status IN ?", []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}).
		Where("(tier_mode = ? OR tier_mode IS NULL) AND (tier_code = ? OR tier_code IS NULL)", "", "").Find(&sites).Error; err != nil {
		return nil, err
	}
	var changed []model.AdtySite
	for _, site := range sites {
		amount := revenue[site.Id]
		var picked *decimal.Decimal
		best := decimal.NewFromInt(-1)
		for _, tier := range tiers {
			threshold := decimal.NewFromFloat(tier.MinMonthlyUSD)
			if threshold.LessThanOrEqual(amount) && threshold.GreaterThan(best) {
				best = threshold
				factor := decimal.NewFromFloat(tier.CostFactor)
				picked = &factor
			}
		}
		current, hasCurrent := AdtySiteCostFactor(&site)
		raise := picked != nil && hasCurrent && picked.GreaterThan(current)
		updates := map[string]any{"tier_month": month}
		if raise {
			delay := resellerSetting().MarkupIncreaseDelayHours
			if delay < 1 {
				delay = 24
			}
			pendingAt := time.Now().Unix() + int64(delay)*3600
			updates["pending_tier_cost_factor"] = *picked
			updates["pending_tier_cost_factor_at"] = pendingAt
		} else {
			if picked == nil {
				updates["tier_cost_factor"] = nil
			} else {
				updates["tier_cost_factor"] = *picked
			}
			updates["pending_tier_cost_factor"] = gorm.Expr("NULL")
			updates["pending_tier_cost_factor_at"] = 0
		}
		if err := model.DB.Model(&model.AdtySite{}).Where("id = ?", site.Id).
			Where("(tier_mode = ? OR tier_mode IS NULL) AND (tier_code = ? OR tier_code IS NULL)", "", "").Updates(updates).Error; err != nil {
			return nil, err
		}
		if raise {
			notice := site
			notice.PendingCostFactor = picked
			notice.PendingCostFactorAt = updates["pending_tier_cost_factor_at"].(int64)
			adtyNotifySiteCostFactorPending(&notice)
			continue
		}
		before := site.TierCostFactor
		if (before == nil) != (picked == nil) || (before != nil && picked != nil && !before.Equal(*picked)) {
			site.TierCostFactor, site.TierMonth = picked, month
			changed = append(changed, site)
		}
	}
	InvalidateAdtySiteCache()
	return changed, nil
}

// AdtyStampTaskSiteBilling 在任务提交时把这次请求的完整 G.2-11 快照和兼容 k、d 写进 BillingContext。
// 轮询结算和退款沿用快照，不受之后改价的影响。
func AdtyStampTaskSiteBilling(c *gin.Context, bc *model.TaskBillingContext) {
	if bc == nil {
		return
	}
	if k, d, ok := AdtySiteMarkupFromContext(c); ok {
		bc.SiteMarkup, bc.SiteCostFactor, bc.SiteId = k, d, c.GetInt(AdtySiteIdContextKey)
		bc.SiteOwnerUserId = c.GetInt(AdtySiteOwnerContextKey)
	}
	if snap := AdtyFrozenPriceSnapshot(c); snap != nil {
		bc.PriceSnapshot = snap
		if bc.SiteId == 0 && snap.SiteId > 0 {
			bc.SiteId, bc.SiteOwnerUserId = snap.SiteId, snap.SiteOwnerUserId
		}
		if bc.SiteMarkup == 0 {
			bc.SiteMarkup = snap.LegacyMarkupFloat()
		}
		if bc.SiteCostFactor == 0 {
			bc.SiteCostFactor = snap.LegacyCostFactorFloat()
		}
	}
}

func adtyTaskRecalcGroupRatioDecimal(task *model.Task, group string, groupRatio decimal.Decimal) decimal.Decimal {
	if task != nil {
		if bc := task.PrivateData.BillingContext; bc != nil {
			if snap := bc.PriceSnapshot; snap != nil {
				if billed, ok := snap.BilledGroupRatioDecimal(); ok {
					return billed
				}
			}
			// Old rows: keep the previously stored billed ratio and do not
			// reprice from live site markup / cost factor / team pay.
			if bc.GroupRatio > 0 {
				return decimal.NewFromFloat(bc.GroupRatio)
			}
		}
	}
	modelName := ""
	if task != nil {
		modelName = adtyNormalizeSitePriceModel(task.Properties.OriginModelName)
	}
	billed, _ := AdtyApplySiteMarkupDecimal(nil, task.UserId, group, groupRatio, modelName)
	return billed
}

func init() {
	model.AdtyOnConsumeLog = adtyOnConsumeLog
	adtyMasterTasks = append(adtyMasterTasks, startAdtySiteMarkupTask, startAdtySiteCostTierTask)
}

// adtyOnConsumeLog 在每条消费日志之前调用：记下 k、d（admin_info），再交给 11.5 的收益记账。
func adtyOnConsumeLog(c *gin.Context, userId int, params *model.RecordConsumeLogParams) {
	if params == nil {
		return
	}
	// 只有乘过 k 的请求才补 admin_info；Other 为 nil 的日志保持原样（上游按 nil 存空串）。
	if _, _, ok := AdtySiteMarkupFromContext(c); ok {
		if params.Other == nil {
			params.Other = model.NewLogOther()
		}
		AdtyAppendSiteMarkupAdminInfo(c, params.Other)
	}
	if c != nil {
		if discount := c.GetFloat64(AdtyTeamDiscountContextKey); discount > 0 {
			if params.Other == nil {
				params.Other = model.NewLogOther()
			}
			params.Other.SetAdmin("team_discount", discount)
		}
	}
	adtyTakeBillingShortfall(c, params) // 余额下限：结算没扣到的差额记 admin_info
	if c.GetBool(adtyDurableBillingContextKey) {
		if params.Other == nil {
			params.Other = model.NewLogOther()
		}
		params.Other.SetAdmin("billing_operation_key", c.GetString(adtyBillingOperationContextKey))
	}
	if AdtyRecordConsumeEarning != nil {
		AdtyRecordConsumeEarning(c, userId, params)
	}
}

// AdtyRecordConsumeEarning 由 11.5 的收益记账注册。
var AdtyRecordConsumeEarning func(c *gin.Context, userId int, params *model.RecordConsumeLogParams)

// adtySiteMonthRevenue 是上个月每个站的客户消费额（美元）：usage 和 refund 行的 Revenue 合计，不算作废行。
func adtySiteMonthRevenue(month string) (map[int]decimal.Decimal, error) {
	start, err := time.ParseInLocation("2006-01", month, time.Local)
	if err != nil {
		return nil, err
	}
	var rows []model.AdtySiteEarningDaily
	err = model.DB.Select("site_id", "revenue").
		Where("kind IN ? AND status <> ? AND day >= ? AND day < ?",
			[]string{model.AdtySiteEarningUsage, model.AdtySiteEarningRefund}, model.AdtySiteEarningVoid,
			start.Format("2006-01-02"), start.AddDate(0, 1, 0).Format("2006-01-02")).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	revenue := map[int]decimal.Decimal{}
	for _, row := range rows {
		revenue[row.SiteId] = revenue[row.SiteId].Add(row.Revenue)
	}
	return revenue, nil
}

// RunAdtySiteCostTierAssignment 按上个月已记账消费给还没算过那个月的站分档。没有档位时什么都不写。
func RunAdtySiteCostTierAssignment(now time.Time) error {
	if len(resellerSetting().CostFactorTiers) == 0 {
		return nil
	}
	month := now.AddDate(0, -1, 0).Format("2006-01")
	var pending int64
	err := model.DB.Model(&model.AdtySite{}).
		Where("status IN ? AND tier_month <> ?", []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}, month).
		Count(&pending).Error
	if err != nil || pending == 0 {
		return err
	}
	revenue, err := adtySiteMonthRevenue(month)
	if err != nil {
		return err
	}
	_, err = AssignAdtySiteCostTiers(month, revenue)
	return err
}

// startAdtySiteCostTierTask 在主节点每小时检查一次：上个月还没分档的站，按已记账消费补算。
func startAdtySiteCostTierTask() {
	go func() {
		run := func() {
			if err := RunAdtySiteCostTierAssignment(time.Now()); err != nil {
				common.SysError(common.LogText("assign site cost tiers: %s", err.Error()))
			}
		}
		run()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()
}

// startAdtySiteMarkupTask 在主节点每 5 分钟把到期的涨价改成生效。
func startAdtySiteMarkupTask() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := ApplyDueAdtySiteMarkups(time.Now().Unix()); err != nil {
				common.SysError(common.LogText("apply site markups: %s", err.Error()))
			}
			if _, err := ApplyDueAdtySiteCostFactors(time.Now().Unix()); err != nil {
				common.SysError(common.LogText("apply site cost factors: %s", err.Error()))
			}
			if err := ApplyDueGlobalCostFactor(time.Now().Unix()); err != nil {
				common.SysError(common.LogText("apply global cost factor: %s", err.Error()))
			}
			if _, err := ApplyDueAdtySiteTierCostFactors(time.Now().Unix()); err != nil {
				common.SysError(common.LogText("apply site tier cost factors: %s", err.Error()))
			}
			if err := ApplyDueMinCostFactor(time.Now().Unix()); err != nil {
				common.SysError(common.LogText("apply minimum cost factor: %s", err.Error()))
			}
		}
	}()
}

// AdtyUserGroupRatio 是 /api/user/self/groups 显示的分组倍率：分站客户按供货倍率和加价比例计算。
func AdtyUserGroupRatio(userId int, userGroup, group string) float64 {
	ratio := GetUserGroupRatio(userGroup, group)
	marked, _ := AdtyApplySiteMarkup(nil, userId, group, ratio, "")
	return marked
}

// Deprecated: retail prices are read directly from the database at admission.
func InvalidateAdtySitePriceCache() {}

// AdtyApplySitePricing adjusts only the supplied group prices.
func AdtyApplySitePricing(c *gin.Context, groupRatio map[string]float64) {
	if _, err := AdtySitePriceList(c, groupRatio, "", nil); err != nil {
		common.SysError(common.LogText("load adty site prices failed: %s", err.Error()))
	}
}

// AdtyAdjustPricingForSiteModels adjusts a standalone price list for one display group.
func AdtyAdjustPricingForSiteModels(c *gin.Context, group string, pricing []model.Pricing) []model.Pricing {
	if c == nil || len(pricing) == 0 {
		return pricing
	}
	adjusted, err := AdtySitePriceList(c, nil, group, pricing)
	if err != nil {
		common.SysError(common.LogText("load adty site prices failed: %s", err.Error()))
		return pricing
	}
	return adjusted
}

// AdtySitePriceList presents group and model prices from the same committed overrides.
func AdtySitePriceList(c *gin.Context, groupRatio map[string]float64, displayGroup string, pricing []model.Pricing) ([]model.Pricing, error) {
	userID := c.GetInt("id")
	var site *model.AdtySite
	if userID > 0 {
		site = AdtyBillingSiteForUser(userID)
	} else {
		site = AdtySiteForRequest(c)
	}
	if !AdtySiteServesCustomers(site) || !resellerSetting().Enabled {
		return pricing, nil
	}
	d, ok := AdtySiteCostFactor(site)
	if !ok {
		return pricing, nil
	}
	prices, err := adtySitePrices(site.Id, userID)
	if err != nil {
		return nil, err
	}
	for group, ratio := range groupRatio {
		k := ResolveAdtySiteMarkup(prices, userID, group, "")
		marked, _, _ := adtySiteOrdinarySellRatioDec(decimal.NewFromFloat(ratio), k, d, userID, group)
		groupRatio[group] = marked.InexactFloat64()
	}
	return adtyAdjustModelPriceList(prices, userID, displayGroup, pricing), nil
}

func adtyAdjustModelPriceList(prices []model.AdtySitePrice, userId int, group string, pricing []model.Pricing) []model.Pricing {
	baseK := ResolveAdtySiteMarkup(prices, userId, group, "")
	if !baseK.IsPositive() {
		return pricing
	}
	out := make([]model.Pricing, len(pricing))
	copy(out, pricing)
	changed := false
	for i := range out {
		name := adtyNormalizeSitePriceModel(out[i].ModelName)
		if name == "" {
			continue
		}
		modelK := ResolveAdtySiteMarkup(prices, userId, group, name)
		if !modelK.IsPositive() || modelK.Equal(baseK) {
			continue
		}
		factor := modelK.Div(baseK)
		out[i].ModelPrice = decimal.NewFromFloat(out[i].ModelPrice).Mul(factor).InexactFloat64()
		if out[i].ModelRatio != nil {
			scaled := decimal.NewFromFloat(*out[i].ModelRatio).Mul(factor).InexactFloat64()
			out[i].ModelRatio = &scaled
		}
		out[i].BillingDisplayMultiplier = factor.String()
		changed = true
	}
	if !changed {
		return pricing
	}
	return out
}

func adtyScaleBillingExpr(exprStr string, factor decimal.Decimal) string {
	trimmed := strings.TrimSpace(exprStr)
	if trimmed == "" || !factor.IsPositive() || factor.Equal(decimal.NewFromInt(1)) {
		return exprStr
	}
	prefix := ""
	body := trimmed
	if strings.HasPrefix(trimmed, "v1:") {
		prefix = "v1:"
		body = strings.TrimSpace(trimmed[3:])
	}
	scaledBody, foundTier := adtyRewriteTierCalls(body, factor)
	if !foundTier {
		scaledBody = adtyScaleTierPriceExpr(body, factor)
	}
	candidate := prefix + scaledBody
	if _, err := billingexpr.CompileFromCache(candidate); err != nil {
		return exprStr
	}
	return candidate
}

func adtyIsIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

func adtyScanStringLiteral(s string, start int) int {
	quote := s[start]
	for i := start + 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == quote {
			return i + 1
		}
	}
	return len(s)
}

func adtyFindMatchingParen(s string, openIdx int) int {
	if openIdx < 0 || openIdx >= len(s) || s[openIdx] != '(' {
		return -1
	}
	depth := 0
	for i := openIdx; i < len(s); i++ {
		ch := s[i]
		if ch == '"' || ch == '\'' {
			i = adtyScanStringLiteral(s, i) - 1
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func adtySplitTopLevelComma(s string) (string, string, bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' || ch == '\'' {
			i = adtyScanStringLiteral(s, i) - 1
			continue
		}
		switch ch {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				return s[:i], s[i+1:], true
			}
		}
	}
	return "", "", false
}

func adtyRewriteTierCalls(s string, factor decimal.Decimal) (string, bool) {
	var b strings.Builder
	found := false
	i := 0
	for i < len(s) {
		ch := s[i]
		if ch == '"' || ch == '\'' {
			next := adtyScanStringLiteral(s, i)
			b.WriteString(s[i:next])
			i = next
			continue
		}
		if strings.HasPrefix(s[i:], "tier") && (i == 0 || !adtyIsIdentChar(s[i-1])) {
			j := i + 4
			if j >= len(s) || !adtyIsIdentChar(s[j]) {
				for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
					j++
				}
				if j < len(s) && s[j] == '(' {
					closeIdx := adtyFindMatchingParen(s, j)
					if closeIdx > j {
						inner := s[j+1 : closeIdx]
						if arg0, arg1, ok := adtySplitTopLevelComma(inner); ok {
							found = true
							b.WriteString("tier(")
							b.WriteString(strings.TrimSpace(arg0))
							b.WriteString(", ")
							b.WriteString(adtyScaleTierPriceExpr(arg1, factor))
							b.WriteByte(')')
							i = closeIdx + 1
							continue
						}
					}
				}
			}
		}
		b.WriteByte(ch)
		i++
	}
	return b.String(), found
}

func adtyParsePureDecimal(s string) (decimal.Decimal, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\n\r") {
		return decimal.Zero, false
	}
	d, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}

func adtyScaleTierPriceExpr(expr string, factor decimal.Decimal) string {
	s := strings.TrimSpace(expr)
	if s == "" {
		return expr
	}
	if s[0] == '(' && adtyFindMatchingParen(s, 0) == len(s)-1 {
		return "(" + adtyScaleTierPriceExpr(s[1:len(s)-1], factor) + ")"
	}
	if strings.HasPrefix(s, "fixed") && (len(s) == 5 || !adtyIsIdentChar(s[5])) {
		j := 5
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j < len(s) && s[j] == '(' && adtyFindMatchingParen(s, j) == len(s)-1 {
			inner := s[j+1 : len(s)-1]
			if num, ok := adtyParsePureDecimal(inner); ok {
				return "fixed(" + num.Mul(factor).String() + ")"
			}
		}
	}
	if num, ok := adtyParsePureDecimal(s); ok {
		return num.Mul(factor).String()
	}
	if cond, branch1, branch2, ok := adtySplitTopLevelTernary(s); ok {
		return strings.TrimSpace(cond) + " ? " + adtyScaleTierPriceExpr(branch1, factor) + " : " + adtyScaleTierPriceExpr(branch2, factor)
	}
	if terms, ops, ok := adtySplitTopLevelAddSub(s); ok {
		var b strings.Builder
		for idx, term := range terms {
			if idx > 0 {
				b.WriteByte(' ')
				b.WriteByte(ops[idx-1])
				b.WriteByte(' ')
			}
			b.WriteString(adtyScaleTierPriceExpr(term, factor))
		}
		return b.String()
	}
	if lhs, rhs, op, ok := adtySplitLastTopLevelMulDiv(s); ok {
		lhsTrim := strings.TrimSpace(lhs)
		rhsTrim := strings.TrimSpace(rhs)
		if op == '/' {
			if _, ok := adtyParsePureDecimal(rhsTrim); ok {
				return adtyScaleTierPriceExpr(lhsTrim, factor) + " / " + rhsTrim
			}
		}
		if op == '*' {
			if num, ok := adtyParsePureDecimal(rhsTrim); ok {
				return lhsTrim + " * " + num.Mul(factor).String()
			}
			if num, ok := adtyParsePureDecimal(lhsTrim); ok {
				return num.Mul(factor).String() + " * " + rhsTrim
			}
			return adtyScaleTierPriceExpr(lhsTrim, factor) + " * " + rhsTrim
		}
	}
	return "(" + s + ") * " + factor.String()
}

func adtySplitTopLevelTernary(s string) (string, string, string, bool) {
	depth := 0
	qIdx := -1
	colonDepth := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' || ch == '\'' {
			i = adtyScanStringLiteral(s, i) - 1
			continue
		}
		switch ch {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '?':
			if depth == 0 {
				if qIdx == -1 {
					qIdx = i
				} else {
					colonDepth++
				}
			}
		case ':':
			if depth == 0 && qIdx != -1 {
				if colonDepth == 0 {
					return s[:qIdx], s[qIdx+1 : i], s[i+1:], true
				}
				colonDepth--
			}
		}
	}
	return "", "", "", false
}

func adtyIsOperatorPrev(s string, idx int) bool {
	for i := idx - 1; i >= 0; i-- {
		ch := s[i]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			continue
		}
		switch ch {
		case '+', '-', '*', '/', '%', '?', ':', '(', '[', ',', '<', '>', '=', '!', '&', '|':
			return true
		default:
			return false
		}
	}
	return true
}

func adtySplitTopLevelAddSub(s string) ([]string, []byte, bool) {
	depth := 0
	var terms []string
	var ops []byte
	last := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' || ch == '\'' {
			i = adtyScanStringLiteral(s, i) - 1
			continue
		}
		switch ch {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '+', '-':
			if depth == 0 && !adtyIsOperatorPrev(s, i) {
				terms = append(terms, s[last:i])
				ops = append(ops, ch)
				last = i + 1
			}
		}
	}
	if len(ops) == 0 {
		return nil, nil, false
	}
	terms = append(terms, s[last:])
	return terms, ops, true
}

func adtySplitLastTopLevelMulDiv(s string) (string, string, byte, bool) {
	depth := 0
	lastIdx := -1
	var lastOp byte
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' || ch == '\'' {
			i = adtyScanStringLiteral(s, i) - 1
			continue
		}
		switch ch {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '*', '/':
			if depth == 0 {
				lastIdx = i
				lastOp = ch
			}
		}
	}
	if lastIdx <= 0 {
		return "", "", 0, false
	}
	return s[:lastIdx], s[lastIdx+1:], lastOp, true
}
