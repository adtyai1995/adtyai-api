package controller

import (
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type adtyPricingGroupMeta struct {
	Names model.AdtyLangText `json:"names"`
	Descs model.AdtyLangText `json:"descs"`
}

func pricingPreferredLangs(c *gin.Context, userID int) []string {
	var langs []string
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "*" {
			return
		}
		lower := strings.ToLower(strings.ReplaceAll(raw, "_", "-"))
		mapped := "en"
		if strings.HasPrefix(lower, "zh") {
			mapped = "zh-CN"
		}
		if !slices.Contains(langs, mapped) {
			langs = append(langs, mapped)
		}
	}
	if userID > 0 {
		add(model.GetUserLanguage(userID))
	}
	if c != nil {
		for part := range strings.SplitSeq(c.GetHeader("Accept-Language"), ",") {
			if before, _, ok := strings.Cut(part, ";"); ok {
				part = before
			}
			add(part)
		}
	}
	for _, fallback := range []string{"zh-CN", "en"} {
		if !slices.Contains(langs, fallback) {
			langs = append(langs, fallback)
		}
	}
	return langs
}

func activePricingTeamForUser(userID, siteID int) (*model.AdtyTeam, error) {
	if userID <= 0 || model.DB == nil {
		return nil, nil
	}
	hasTeamMemberTable, err := model.AdtyTableExists(model.DB, &model.AdtyTeamMember{})
	if err != nil {
		return nil, err
	}
	hasTeamTable, err := model.AdtyTableExists(model.DB, &model.AdtyTeam{})
	if err != nil {
		return nil, err
	}
	if !hasTeamMemberTable || !hasTeamTable {
		return nil, nil
	}
	var member model.AdtyTeamMember
	res := model.DB.Where("user_id = ? AND status = ?", userID, model.AdtyTeamMemberActive).Limit(1).Find(&member)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	var team model.AdtyTeam
	if err := model.DB.Where("id = ? AND site_id = ? AND status = ?", member.TeamId, siteID, model.AdtyTeamStatusActive).Limit(1).Find(&team).Error; err != nil {
		return nil, err
	}
	if team.Id == 0 {
		return nil, nil
	}
	return &team, nil
}

func applyPricingTeamMultiplier(c *gin.Context, userID, siteID int, rawGroupRatio, groupRatio map[string]float64, pricing []model.Pricing) ([]model.Pricing, error) {
	team, err := activePricingTeamForUser(userID, siteID)
	if err != nil {
		return nil, err
	}
	if team == nil {
		return pricing, nil
	}
	maxDiscount := decimal.NewFromInt(10)
	if siteID == 0 {
		if !team.Discount.IsPositive() || team.Discount.GreaterThan(maxDiscount) {
			return pricing, nil
		}
		for g, rawRatio := range rawGroupRatio {
			groupRatio[g] = decimal.NewFromFloat(rawRatio).Mul(team.Discount).InexactFloat64()
		}
		for i := range pricing {
			if pricing[i].GroupRatio != nil {
				pricing[i].GroupRatio = maps.Clone(groupRatio)
			}
		}
		return pricing, nil
	}
	if !adty_setting.GetResellerSetting().Enabled {
		return pricing, nil
	}
	minSubsiteMult := decimal.NewFromInt(1)
	if team.Discount.LessThan(minSubsiteMult) || team.Discount.GreaterThan(maxDiscount) {
		return pricing, nil
	}
	site := service.AdtyBillingSiteForUser(userID)
	if site == nil || site.Id != siteID {
		site = service.AdtySiteForRequest(c)
	}
	if site == nil || site.Id != siteID || !service.AdtySiteServesCustomers(site) {
		return pricing, nil
	}
	d, ok := service.AdtySiteCostFactor(site)
	if !ok {
		return pricing, nil
	}
	for g, rawRatio := range rawGroupRatio {
		supply := decimal.NewFromFloat(rawRatio).Mul(d)
		effective := decimal.Max(supply, supply.Mul(team.Discount))
		groupRatio[g] = effective.InexactFloat64()
	}
	for i := range pricing {
		pricing[i].GroupRatio = maps.Clone(groupRatio)
	}
	return pricing, nil
}

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string, siteID int) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	concreteUsable := make([]string, 0, len(usableGroup))
	for group := range usableGroup {
		if group == "" || group == "auto" || group == "all" {
			continue
		}
		if !service.SiteGroupAllowed(siteID, group) {
			continue
		}
		concreteUsable = append(concreteUsable, group)
	}
	slices.Sort(concreteUsable)
	if len(concreteUsable) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		var candidates []string
		if common.StringsContains(item.EnableGroup, "all") {
			candidates = concreteUsable
		} else {
			seen := make(map[string]struct{}, len(item.EnableGroup))
			candidates = make([]string, 0, len(item.EnableGroup))
			for _, group := range item.EnableGroup {
				if group == "" || group == "auto" || group == "all" {
					continue
				}
				if _, ok := usableGroup[group]; !ok {
					continue
				}
				if !service.SiteGroupAllowed(siteID, group) {
					continue
				}
				if _, dup := seen[group]; dup {
					continue
				}
				seen[group] = struct{}{}
				candidates = append(candidates, group)
			}
		}

		if len(candidates) == 0 {
			continue
		}
		copied := item
		copied.EnableGroup = candidates
		filtered = append(filtered, copied)
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	pricing, vendors, supportedEndpoints := model.GetPricingBundle()
	siteID := service.AdtyRequestSiteID(c)
	userID := c.GetInt("id")
	var group string
	if userID > 0 {
		if user, err := model.GetUserCache(userID); err == nil && user != nil {
			group = user.Group
			if siteID == 0 && user.SiteId > 0 {
				siteID = user.SiteId
			}
		}
	}

	activeAdtyGroups := map[string]model.AdtyGroup{}
	deletedAdtyGroups := map[string]struct{}{}
	if model.DB != nil {
		if allGroups, err := model.ListAdtyGroups(true); err == nil {
			for _, g := range allGroups {
				id := strings.TrimSpace(g.ID)
				if id == "" || id == "auto" {
					continue
				}
				if g.Deleted() {
					deletedAdtyGroups[id] = struct{}{}
					continue
				}
				activeAdtyGroups[id] = g
			}
		}
	}
	preferredLangs := pricingPreferredLangs(c, userID)

	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	for g, ratio := range ratio_setting.GetGroupRatioCopy() {
		g = strings.TrimSpace(g)
		if g == "" || g == "auto" {
			continue
		}
		if _, deleted := deletedAdtyGroups[g]; deleted {
			continue
		}
		groupRatio[g] = ratio
	}
	for id := range activeAdtyGroups {
		if _, exists := groupRatio[id]; !exists {
			groupRatio[id] = 1
		}
	}
	for _, item := range pricing {
		for _, g := range item.EnableGroup {
			g = strings.TrimSpace(g)
			if g == "" || g == "auto" || g == "all" {
				continue
			}
			if _, deleted := deletedAdtyGroups[g]; deleted {
				continue
			}
			if _, exists := groupRatio[g]; !exists {
				groupRatio[g] = 1
			}
		}
	}
	rawUsable := service.GetUserUsableGroups(group)
	for g, desc := range rawUsable {
		if g == "" || g == "auto" {
			continue
		}
		if _, deleted := deletedAdtyGroups[g]; deleted {
			continue
		}
		if !service.SiteGroupAllowed(siteID, g) {
			continue
		}
		usableGroup[g] = desc
	}
	for id, meta := range activeAdtyGroups {
		if !service.SiteGroupAllowed(siteID, id) {
			continue
		}
		displayName := meta.DisplayName(preferredLangs...)
		existing, exists := usableGroup[id]
		if !exists || strings.TrimSpace(existing) == "" || existing == id || (displayName != "" && displayName != id) {
			usableGroup[id] = displayName
		}
	}
	for g := range groupRatio {
		if g == "" || g == "auto" || !service.SiteGroupAllowed(siteID, g) {
			continue
		}
		if _, ok := usableGroup[g]; !ok {
			usableGroup[g] = setting.GetUsableGroupDescription(g)
		}
	}

	requested := strings.TrimSpace(c.Query("group"))
	if requested != "" && requested != "all" {
		if requested == "auto" || !service.SiteGroupAllowed(siteID, requested) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
			return
		}
	}

	pricing = filterPricingByUsableGroups(pricing, usableGroup, siteID)
	adjustGroup := group
	if adjustGroup == "auto" || (adjustGroup != "" && !service.SiteGroupAllowed(siteID, adjustGroup)) {
		adjustGroup = ""
	}
	if requested != "" && requested != "all" {
		if _, ok := usableGroup[requested]; ok {
			adjustGroup = requested
		}
	}
	for g := range groupRatio {
		if _, ok := usableGroup[g]; !ok || !service.SiteGroupAllowed(siteID, g) {
			delete(groupRatio, g)
		}
	}
	rawGroupRatio := maps.Clone(groupRatio)
	pricing, err := service.AdtySitePriceList(c, groupRatio, adjustGroup, pricing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pricing, err = applyPricingTeamMultiplier(c, userID, siteID, rawGroupRatio, groupRatio, pricing)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	adtyGroupMeta := make(map[string]adtyPricingGroupMeta)
	for g := range usableGroup {
		if g == "auto" {
			continue
		}
		if meta, ok := activeAdtyGroups[g]; ok {
			adtyGroupMeta[g] = adtyPricingGroupMeta{
				Names: meta.Names,
				Descs: meta.Descs,
			}
		}
	}

	autoGroups := []string{}

	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            vendors,
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"adty_group_meta":    adtyGroupMeta,
		"supported_endpoint": supportedEndpoints,
		"auto_groups":        autoGroups,
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccessT(c, "Model ratios reset", nil)
}
