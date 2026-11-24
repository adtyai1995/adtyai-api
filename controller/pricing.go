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
		if raw == "" {
			return
		}
		lower := strings.ToLower(raw)
		var mapped string
		switch {
		case strings.HasPrefix(lower, "zh-tw"), strings.HasPrefix(lower, "zh-hk"), strings.HasPrefix(lower, "zh-hant"):
			mapped = "zh-TW"
		case strings.HasPrefix(lower, "zh"):
			mapped = "zh-CN"
		case strings.HasPrefix(lower, "en"):
			mapped = "en"
		case strings.HasPrefix(lower, "ja"):
			mapped = "ja"
		case strings.HasPrefix(lower, "ko"):
			mapped = "ko"
		case strings.HasPrefix(lower, "fr"):
			mapped = "fr"
		case strings.HasPrefix(lower, "ru"):
			mapped = "ru"
		case strings.HasPrefix(lower, "vi"):
			mapped = "vi"
		}
		if mapped != "" && !slices.Contains(langs, mapped) {
			langs = append(langs, mapped)
		}
	}
	if userID > 0 {
		add(model.GetUserLanguage(userID))
	}
	if c != nil {
		for _, part := range strings.Split(c.GetHeader("Accept-Language"), ",") {
			if semi := strings.IndexByte(part, ';'); semi >= 0 {
				part = part[:semi]
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

func activePricingTeamForUser(userID, siteID int) *model.AdtyTeam {
	if userID <= 0 || model.DB == nil {
		return nil
	}
	if !model.DB.Migrator().HasTable(&model.AdtyTeamMember{}) || !model.DB.Migrator().HasTable(&model.AdtyTeam{}) {
		return nil
	}
	var member model.AdtyTeamMember
	res := model.DB.Where("user_id = ? AND status = ?", userID, model.AdtyTeamMemberActive).Limit(1).Find(&member)
	if res.Error != nil || res.RowsAffected == 0 {
		return nil
	}
	var team model.AdtyTeam
	if err := model.DB.Where("id = ? AND site_id = ? AND status = ?", member.TeamId, siteID, model.AdtyTeamStatusActive).Limit(1).Find(&team).Error; err != nil || team.Id == 0 {
		return nil
	}
	return &team
}

func applyPricingTeamMultiplier(c *gin.Context, userID, siteID int, rawGroupRatio, groupRatio map[string]float64, pricing []model.Pricing) []model.Pricing {
	team := activePricingTeamForUser(userID, siteID)
	if team == nil {
		return pricing
	}
	maxDiscount := decimal.NewFromInt(10)
	if siteID == 0 {
		if !team.Discount.IsPositive() || team.Discount.GreaterThan(maxDiscount) {
			return pricing
		}
		for g, rawRatio := range rawGroupRatio {
			groupRatio[g] = decimal.NewFromFloat(rawRatio).Mul(team.Discount).InexactFloat64()
		}
		for i := range pricing {
			if pricing[i].GroupRatio != nil {
				pricing[i].GroupRatio = maps.Clone(groupRatio)
			}
		}
		return pricing
	}
	if !adty_setting.GetResellerSetting().Enabled {
		return pricing
	}
	minSubsiteMult := decimal.NewFromInt(1)
	if team.Discount.LessThan(minSubsiteMult) || team.Discount.GreaterThan(maxDiscount) {
		return pricing
	}
	site := service.AdtyBillingSiteForUser(userID)
	if site == nil || site.Id != siteID {
		site = service.AdtySiteForRequest(c)
	}
	if site == nil || site.Id != siteID || !service.AdtySiteServesCustomers(site) {
		return pricing
	}
	d, ok := service.AdtySiteCostFactor(site)
	if !ok {
		return pricing
	}
	for g, rawRatio := range rawGroupRatio {
		supply := decimal.NewFromFloat(rawRatio).Mul(d)
		effective := decimal.Max(supply, supply.Mul(team.Discount))
		groupRatio[g] = effective.InexactFloat64()
	}
	for i := range pricing {
		pricing[i].GroupRatio = maps.Clone(groupRatio)
		pricing[i].BillingDisplayMultiplier = ""
	}
	return pricing
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
	if userID > 0 && group != "" {
		for g := range groupRatio {
			ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
			if ok {
				groupRatio[g] = ratio
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
	if desc, ok := rawUsable["auto"]; ok && (siteID == 0 || len(usableGroup) > 0) {
		usableGroup["auto"] = desc
	}

	requested := strings.TrimSpace(c.Query("group"))
	if requested != "" && requested != "all" {
		if requested == "auto" {
			if _, ok := usableGroup["auto"]; !ok {
				common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
				return
			}
		} else if !service.SiteGroupAllowed(siteID, requested) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
			return
		}
	}

	pricing = filterPricingByUsableGroups(pricing, usableGroup, siteID)
	adjustGroup := group
	if adjustGroup != "" && adjustGroup != "auto" && !service.SiteGroupAllowed(siteID, adjustGroup) {
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
	pricing = applyPricingTeamMultiplier(c, userID, siteID, rawGroupRatio, groupRatio, pricing)

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

	rawAutoGroups := service.GetUserAutoGroup(group)
	autoGroups := make([]string, 0, len(rawAutoGroups))
	for _, g := range rawAutoGroups {
		if _, deleted := deletedAdtyGroups[g]; deleted {
			continue
		}
		if service.SiteGroupAllowed(siteID, g) {
			autoGroups = append(autoGroups, g)
		}
	}

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
