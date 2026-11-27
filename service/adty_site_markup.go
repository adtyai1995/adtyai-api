package service

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
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
	ErrAdtySitePricingConflict           = common.NewMessage("The prices were changed elsewhere. Reload and try again")
	ErrAdtySitePaused                    = common.NewMessage("Prices cannot be changed while the site is paused")
	ErrAdtySiteSingleMultiplierOnly      = common.NewMessage("Subsite pricing only supports a single site-wide multiplier between 1x and 10x")
	ErrAdtySubsiteTeamDiscountBelowFloor = common.NewMessage("Subsite team multiplier cannot be below 1 (supply price floor)")
	adtySiteMinMarkup                    = decimal.NewFromInt(1)
	adtySiteMaxMarkup                    = decimal.NewFromInt(10)
	adtyTeamMaxDiscount                  = decimal.NewFromInt(10)
)

func adtyNormalizeSitePriceModel(name string) string {
	return strings.TrimSpace(name)
}

func adtySitePriceKey(group, modelName string) string {
	return group + "" + adtyNormalizeSitePriceModel(modelName)
}

func adtySitePrices(siteID, userID int) ([]model.AdtySitePrice, error) {
	var rows []model.AdtySitePrice
	q := model.DB.Where("site_id = ?", siteID)
	if userID == 0 {
		q = q.Where("user_id = 0")
	} else {
		q = q.Where("user_id IN ?", []int{0, userID})
	}
	err := q.Order("id").Find(&rows).Error
	return rows, err
}

// Load the site-level price rows and scope counter in one statement so a concurrent
// edit cannot pair an old price with a new revision. The site anchors the joins
// even after the last override is deleted. Only the single site-wide multiplier
// row (user_id=0, group="", model="") governs retail pricing (R-32).
func adtyAdmittedSitePrice(siteID, userID int, group, modelName string) (model.AdtySitePrice, string, error) {
	var rows []struct {
		model.AdtySitePrice
		SiteVersion int64
	}
	err := model.DB.Model(&model.AdtySite{}).
		Select("prices.*, COALESCE(site_scope.version, 0) AS site_version").
		Joins("LEFT JOIN adty_site_price_scopes AS site_scope ON site_scope.site_id = adty_sites.id AND site_scope.user_id = 0").
		Joins("LEFT JOIN adty_site_prices AS prices ON prices.site_id = adty_sites.id AND prices.user_id = 0").
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
	selected := resolveAdtySitePrice(prices, 0, "", "")
	revision := fmt.Sprintf("site:%d:v%d:user:0:row:%d:v%d", siteID, rows[0].SiteVersion, selected.Id, selected.Version)
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

// AdtySiteMarkup 返回分站客户的全站单一零售倍率 M（1 <= M <= 10，加价比例 = M-1）；主站客户、站点没有开着时返回 1。
func AdtySiteMarkup(userId int, group, modelName string) decimal.Decimal {
	site := AdtyBillingSiteForUser(userId)
	if site == nil {
		return adtySiteMinMarkup
	}
	prices, err := adtySitePrices(site.Id, 0)
	if err != nil {
		common.SysError(common.LogText("load adty site prices failed: %s", err.Error()))
		return adtySiteMinMarkup
	}
	return adtyValidStoredMarkup(ResolveAdtySiteMarkup(prices, 0, "", ""), userId, group)
}

// AdtySiteCostFactor 是站点当前的供货折扣：单站覆盖 → 分站等级供货折扣（历史无等级行才回退旧全局值）。
func AdtySiteCostFactor(site *model.AdtySite) (decimal.Decimal, bool) {
	cfg := resellerSetting()
	var d decimal.Decimal
	switch {
	case site != nil && site.CostFactor != nil:
		d = *site.CostFactor
	case site != nil && site.TierCostFactor != nil:
		d = *site.TierCostFactor
	case cfg.CostFactor != nil:
		d = decimal.NewFromFloat(*cfg.CostFactor)
	default:
		return decimal.Zero, false
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
	// R5-05: Guard against huge exponents (e.g. "1e2000000000") before InexactFloat64/Rat
	// invokes big.Int.Exp and exhausts CPU/memory.
	if exp := d.Exponent(); exp < -18 || exp > 18 {
		return true
	}
	value := d.InexactFloat64()
	return math.IsNaN(value) || math.IsInf(value, 0)
}

func adtyValidStoredMarkup(k decimal.Decimal, userId int, group string) decimal.Decimal {
	if !k.IsPositive() || adtyDecimalFloatInvalid(k) {
		common.SysError(common.LogText("invalid site markup %s for user %d group %s, using 1", k.String(), userId, group))
		return adtySiteMinMarkup
	}
	if k.LessThan(adtySiteMinMarkup) {
		return adtySiteMinMarkup
	}
	if k.GreaterThan(adtySiteMaxMarkup) {
		return adtySiteMaxMarkup
	}
	return k
}

func adtyDecimalFiniteNonNegative(d decimal.Decimal) bool {
	value := d.InexactFloat64()
	return !math.IsNaN(value) && !math.IsInf(value, 0) && !d.IsNegative()
}

// adtySiteOrdinarySellRatioDec 用精确十进制计算：供货价因子 = 分组倍率 × 供货倍率，
// 普通售价因子 = 供货价因子 × 全站单一倍率 M（1 <= M <= 10）。计费结果不再经过 float64 往返。
func adtySiteOrdinarySellRatioDec(groupRatio, k, costFactor decimal.Decimal, userId int, group string) (marked, storedK decimal.Decimal, ok bool) {
	storedK = adtyValidStoredMarkup(k, userId, group)
	if !costFactor.IsPositive() {
		common.SysError(common.LogText("invalid site cost factor %s for user %d group %s, leaving group ratio unchanged", costFactor.String(), userId, group))
		return groupRatio, adtySiteMinMarkup, false
	}
	marked = groupRatio.Mul(costFactor).Mul(storedK)
	if !adtyDecimalFiniteNonNegative(marked) {
		common.SysError(common.LogText("site sell price overflow for user %d group %s, leaving group ratio unchanged", userId, group))
		return groupRatio, adtySiteMinMarkup, false
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
	if snap := AdtyFrozenPriceSnapshot(c); snap != nil && (snap.BillingGroup == "" || snap.BillingGroup == group) {
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
		if c != nil {
			c.Set(AdtyTeamPriceBlockedKey, true)
		}
		common.SysError(common.LogText("site cost factor missing for user %d group %s, blocking billing fail-closed", userId, group))
		adtyRememberPriceFactors(c, factors)
		return groupRatio, one
	}
	marked, storedK, ok := adtySiteOrdinarySellRatioDec(groupRatio, kUsed, d, userId, group)
	if !ok {
		if c != nil {
			c.Set(AdtyTeamPriceBlockedKey, true)
		}
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

// adtyApplyTeamProtocolPriceDecimal 计算团队计费倍率（R-32 #24 & #7）：
//   - 主站团队（team.SiteId == 0）：team_price = main_group_price × team.Discount（0 < discount <= 10，无下限）。
//   - 分站团队（team.SiteId > 0）：team_price = max(supply_price, supply_price × team.Discount)，
//     其中 supply_price = 分组倍率 × 供货倍率，team.Discount >= 1.0 是基于供货价的直接倍率（不得低于供货价）。
//
// 支付比例无效或结果溢出时置 AdtyTeamPriceBlockedKey，在预扣前拒绝。
func adtyApplyTeamProtocolPriceDecimal(c *gin.Context, userId int, groupRatio, marked, storedK decimal.Decimal) (decimal.Decimal, decimal.Decimal) {
	if c == nil || userId <= 0 {
		return marked, storedK
	}
	tokenId := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	if tokenId <= 0 || model.DB == nil {
		return marked, storedK
	}
	hasTeamKeyTable, err := model.AdtyTableExists(model.DB, &model.AdtyTeamKey{})
	if err != nil {
		c.Set(AdtyTeamPriceBlockedKey, true)
		return marked, storedK
	}
	if !hasTeamKeyTable {
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
	if !adtySiteTeamPayRatioConfigValidForSite(team.SiteId, factorDec) || adtyDecimalFloatInvalid(factorDec) {
		c.Set(AdtyTeamPriceBlockedKey, true)
		return marked, storedK
	}
	var nextDec decimal.Decimal
	var supply decimal.Decimal
	effectiveK := storedK
	floorApplied := false
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
		nextDec = decimal.Max(supply, supply.Mul(factorDec))
		floorApplied = true
		if supply.IsPositive() {
			effectiveK = decimal.Max(adtySiteMinMarkup, nextDec.Div(supply))
		} else {
			effectiveK = decimal.Max(adtySiteMinMarkup, factorDec)
		}
	} else {
		nextDec = groupRatio.Mul(factorDec)
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
		factors.MarkupRatio = adtyStoredMarkupRatio(storedK)
		factors.FloorApplied = true
		factors.SupplyFloor = supply
	}
	adtyRememberPriceFactors(c, factors)
	return nextDec, storedK
}

// AdtyTeamPriceBlocked 表示本次请求的团队支付比例无效或折后价溢出，计费应拒绝。
func AdtyTeamPriceBlocked(c *gin.Context) bool {
	return c != nil && c.GetBool(AdtyTeamPriceBlockedKey)
}

// validateAdtyTeamDiscountForSite 校验团队倍率配置（R-32 #24 & #7）：
// - 主站团队（siteID == 0）：0 < discount <= 10（最多 4 位小数）。
// - 分站团队（siteID > 0）：1 <= discount <= 10（最多 4 位小数，低于 1 返回供货价下限错误）。
func validateAdtyTeamDiscountForSite(siteID int, ratio decimal.Decimal) error {
	if adtyDecimalFloatInvalid(ratio) {
		return ErrAdtyTeamInvalid
	}
	if siteID > 0 {
		if ratio.LessThan(adtySiteMinMarkup) {
			return ErrAdtySubsiteTeamDiscountBelowFloor
		}
		if ratio.GreaterThan(adtyTeamMaxDiscount) || !ratio.Equal(ratio.Truncate(4)) {
			return ErrAdtyTeamInvalid
		}
		return nil
	}
	if !ratio.IsPositive() || ratio.GreaterThan(adtyTeamMaxDiscount) || !ratio.Equal(ratio.Truncate(4)) {
		return ErrAdtyTeamInvalid
	}
	return nil
}

// adtySiteTeamPayRatioConfigValidForSite 判断指定站点的团队倍率是否合法。
func adtySiteTeamPayRatioConfigValidForSite(siteID int, ratio decimal.Decimal) bool {
	return validateAdtyTeamDiscountForSite(siteID, ratio) == nil
}

// adtySiteTeamPayRatioConfigValid 兼容入口：按主站团队口径校验（0 < ratio <= 10，最多 4 位小数）。
func adtySiteTeamPayRatioConfigValid(ratio decimal.Decimal) bool {
	return validateAdtyTeamDiscountForSite(0, ratio) == nil
}

// AdtyApplySiteMarkupHook 是兼容保留的浮点入口；财务权威入口为 AdtyApplySiteMarkupDecimalHook。
var AdtyApplySiteMarkupHook = AdtyApplySiteMarkup

// AdtyApplySiteMarkupDecimalHook 是 HandleGroupRatio 调用的精确十进制入口（relay/helper/price.go）。
var AdtyApplySiteMarkupDecimalHook = AdtyApplySiteMarkupDecimal

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
	if hasScope, err := model.AdtyTableExists(model.DB, &model.AdtySitePriceScope{}); err == nil && !hasScope {
		_ = model.DB.AutoMigrate(&model.AdtySitePriceScope{})
	}
	if hasRev, err := model.AdtyTableExists(model.DB, &model.AdtyPriceRevision{}); err == nil && !hasRev {
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

// Retail multipliers must be between 1.0 and 10.0 with at most 4 decimal places.
func validRetailMarkup(k decimal.Decimal) bool {
	return !adtyDecimalFloatInvalid(k) && !k.LessThan(adtySiteMinMarkup) && !k.GreaterThan(adtySiteMaxMarkup) && k.Equal(k.Truncate(4))
}

// SaveAdtySitePricing commits the single site-wide retail multiplier (1x–10x),
// clears any legacy per-group/per-model/per-customer override rows for the site,
// and records immutable revision history under the site lock.
func SaveAdtySitePricing(input AdtySitePricingInput) (*AdtySitePricingResult, error) {
	if input.UserId != 0 || len(input.GroupMarkups) > 0 || len(input.ModelMarkups) > 0 {
		return nil, ErrAdtySiteSingleMultiplierOnly
	}
	if input.ClearDefault && input.DefaultMarkup != nil {
		return nil, common.NewMessage("Invalid parameters")
	}
	if input.DefaultMarkup != nil && !validRetailMarkup(*input.DefaultMarkup) {
		return nil, ErrAdtySiteSingleMultiplierOnly
	}
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
		var allRows []model.AdtySitePrice
		if err := model.AdtyLockForUpdate(tx).Where("site_id = ?", site.Id).Order("id asc").Find(&allRows).Error; err != nil {
			return err
		}
		var defaultRow model.AdtySitePrice
		hasDefault := false
		var deleteIDs []int
		for _, row := range allRows {
			if row.UserId == 0 && strings.TrimSpace(row.Group) == "" && adtyNormalizeSitePriceModel(row.Model) == "" {
				if hasDefault {
					deleteIDs = append(deleteIDs, defaultRow.Id)
				}
				defaultRow = row
				hasDefault = true
				continue
			}
			// Clear all legacy per-group, per-model, and per-customer override rows.
			deleteIDs = append(deleteIDs, row.Id)
		}
		version, err := adtySitePricingVersionTx(tx, site.Id, 0)
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
		if len(deleteIDs) > 0 {
			if err := tx.Where("id IN ?", deleteIDs).Delete(&model.AdtySitePrice{}).Error; err != nil {
				return err
			}
		}
		before := adtySiteMinMarkup
		if hasDefault && defaultRow.Markup.IsPositive() {
			before = defaultRow.Markup
		}
		changed := false
		after := before
		action := "immediate"
		var afterRow model.AdtySitePrice
		hasAfter := hasDefault
		if input.ClearDefault {
			if hasDefault {
				after = adtySiteMinMarkup
				if after.GreaterThan(before) {
					if err := EnsureAdtySiteMailConfiguredTx(tx, &site); err != nil {
						return err
					}
				}
				if err := tx.Delete(&defaultRow).Error; err != nil {
					return err
				}
				changed = true
				action = "deleted"
				hasAfter = false
			}
		} else if input.DefaultMarkup != nil {
			after = *input.DefaultMarkup
			if after.GreaterThan(before) {
				if err := EnsureAdtySiteMailConfiguredTx(tx, &site); err != nil {
					return err
				}
			}
			if hasDefault {
				if err := tx.Model(&defaultRow).Updates(map[string]any{
					"markup": after, "pending_markup": decimal.Zero, "pending_at": 0, "version": next, "updated_at": now,
				}).Error; err != nil {
					return err
				}
				afterRow = defaultRow
				afterRow.Markup, afterRow.PendingMarkup, afterRow.PendingAt, afterRow.Version, afterRow.UpdatedAt = after, decimal.Zero, 0, next, now
			} else {
				afterRow = model.AdtySitePrice{SiteId: site.Id, UserId: 0, Group: "", Model: "", Markup: after, Version: next, UpdatedAt: now}
				if err := tx.Create(&afterRow).Error; err != nil {
					return err
				}
			}
			changed = true
			hasAfter = true
		}
		if changed {
			beforeState, encodeErr := common.Marshal(map[string]any{"override": hasDefault, "price": defaultRow})
			if encodeErr != nil {
				return encodeErr
			}
			afterState, encodeErr := common.Marshal(map[string]any{"override": hasAfter, "price": afterRow})
			if encodeErr != nil {
				return encodeErr
			}
			rev := model.AdtyPriceRevision{
				SiteId:      site.Id,
				ActorId:     actorId,
				UserId:      0,
				Group:       "",
				Model:       "",
				Kind:        adtyPriceRevisionKind(0, "", ""),
				Action:      action,
				Version:     next,
				EffectiveAt: now,
				BeforeState: string(beforeState),
				AfterState:  string(afterState),
				Before:      before.String(),
				After:       after.String(),
				CreatedAt:   now,
			}
			if err := tx.Create(&rev).Error; err != nil {
				return err
			}
			result.Immediate = append(result.Immediate, "")
			if after.GreaterThan(before) {
				result.Raised = append(result.Raised, model.AdtySitePrice{Markup: after, UpdatedAt: now})
			}
		}
		// 没有变更的行也要把版本推上去，这样下次带旧版本保存会冲突。
		if err := tx.Model(&model.AdtySitePrice{}).Where("site_id = ? AND user_id = 0", site.Id).Update("version", next).Error; err != nil {
			return err
		}
		if err := adtyUpsertPriceScopeVersion(tx, site.Id, 0, next, now); err != nil {
			return err
		}
		result.Version, result.Site = next, &site
		return tx.Where("site_id = ? AND user_id = 0", site.Id).Order("id").Find(&result.Prices).Error
	})
	if err != nil {
		return nil, err
	}
	if len(result.Raised) > 0 && AdtyOnSiteMarkupIncrease != nil {
		AdtyOnSiteMarkupIncrease(result.Site, 0, result.Raised)
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
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return tx.Model(&model.AdtySitePrice{}).Where("id = ?", due.Id).Updates(map[string]any{"pending_markup": 0, "pending_at": 0}).Error
				}
				return err
			}
			if site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting {
				return tx.Model(&model.AdtySitePrice{}).Where("id = ? AND pending_at > 0", due.Id).Updates(map[string]any{"pending_markup": 0, "pending_at": 0}).Error
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

// AssignAdtySiteCostTiers is retired: supply discounts are governed by site levels (AdtySiteTier).
func AssignAdtySiteCostTiers(month string, revenue map[int]decimal.Decimal) ([]model.AdtySite, error) {
	return nil, nil
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
		key := c.GetString(adtyBillingOperationContextKey)
		params.Other.SetAdmin("billing_operation_key", key)
		if value, ok := c.Get(usageBillingContextKey); ok {
			if op, ok := value.(*model.BillingOperation); ok && op.OperationKey == key && op.UserId == userId {
				stampRequestUsageOwner(params.Other, op)
			}
		}
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

// RunAdtySiteCostTierAssignment is retired: supply discounts are governed by site levels (AdtySiteTier).
func RunAdtySiteCostTierAssignment(now time.Time) error {
	return nil
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
			if _, err := ApplyDueAdtySiteTierCostFactors(time.Now().Unix()); err != nil {
				common.SysError(common.LogText("apply site tier cost factors: %s", err.Error()))
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

// AdtyApplySitePricing adjusts only the supplied group prices and fails closed on subsite pricing errors (R5-039).
func AdtyApplySitePricing(c *gin.Context, groupRatio map[string]float64) error {
	if _, err := AdtySitePriceList(c, groupRatio, "", nil); err != nil {
		common.SysError(common.LogText("load adty site prices failed: %s", err.Error()))
		return err
	}
	return nil
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
	priceUserID := userID
	var site *model.AdtySite
	if userID > 0 {
		site = AdtyBillingSiteForUser(userID)
	}
	// R3-052: When browsing a subsite anonymously or as a main-site user (e.g. the site owner
	// previewing their own subsite pricing), fall back to the request host's site with
	// site-level pricing (priceUserID = 0) instead of serving un-marked main-site prices.
	if site == nil {
		site = AdtySiteForRequest(c)
		if site != nil {
			priceUserID = 0
		}
	}
	if !AdtySiteServesCustomers(site) || !resellerSetting().Enabled {
		return pricing, nil
	}
	d, ok := AdtySiteCostFactor(site)
	if !ok {
		return nil, common.NewMessage("Subsite pricing configuration is invalid")
	}
	prices, err := adtySitePrices(site.Id, 0)
	if err != nil {
		return nil, err
	}
	siteMultiplier := adtyValidStoredMarkup(ResolveAdtySiteMarkup(prices, 0, "", ""), priceUserID, "")
	rawGroupRatio := maps.Clone(groupRatio)
	for group, ratio := range groupRatio {
		marked, _, _ := adtySiteOrdinarySellRatioDec(decimal.NewFromFloat(ratio), siteMultiplier, d, priceUserID, group)
		groupRatio[group] = marked.InexactFloat64()
	}
	_ = displayGroup
	return adtyAdjustModelPriceList(siteMultiplier, priceUserID, d, rawGroupRatio, pricing), nil
}

func adtyAdjustModelPriceList(siteMultiplier decimal.Decimal, userId int, costFactor decimal.Decimal, rawGroupRatio map[string]float64, pricing []model.Pricing) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	out := make([]model.Pricing, len(pricing))
	copy(out, pricing)
	for i := range out {
		modelGroupRatio := make(map[string]float64, len(rawGroupRatio))
		for group, rawRatio := range rawGroupRatio {
			marked, _, _ := adtySiteOrdinarySellRatioDec(decimal.NewFromFloat(rawRatio), siteMultiplier, costFactor, userId, group)
			modelGroupRatio[group] = marked.InexactFloat64()
		}
		out[i].GroupRatio = modelGroupRatio
	}
	return out
}
