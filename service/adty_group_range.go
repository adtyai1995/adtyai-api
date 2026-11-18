package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const groupModelAllowOption = "GroupModelAllow"

// SelectedBillingGroupAllowed reports whether a resolved concrete billing group
// satisfies both the current site group allow-list and the requested model's
// group allow-list. Unlike pre-selection checks, it rejects empty or unresolved
// "auto" placeholders.
func SelectedBillingGroupAllowed(c *gin.Context, group, modelName string) bool {
	group = strings.TrimSpace(group)
	if group == "" || group == "auto" {
		return false
	}
	siteID := AdtyRequestSiteID(c)
	if siteID == 0 && c != nil {
		siteID = common.GetContextKeyInt(c, constant.ContextKeyUserSiteId)
	}
	if !SiteGroupAllowed(siteID, group) {
		return false
	}
	return GroupModelAllowed(group, modelName)
}

// GroupModelAllowed reports whether a billing group may call a model.
// A missing or empty list keeps the live catalog. A configured list is closed.
func GroupModelAllowed(group, modelName string) bool {
	group, modelName = strings.TrimSpace(group), strings.TrimSpace(modelName)
	if group == "" || group == "auto" || modelName == "" {
		return true
	}
	common.OptionMapRWMutex.RLock()
	raw := strings.TrimSpace(common.OptionMap[groupModelAllowOption])
	common.OptionMapRWMutex.RUnlock()
	if raw == "" {
		return true
	}
	var lists map[string][]string
	if err := common.UnmarshalJsonStr(raw, &lists); err != nil {
		return false
	}
	list := lists[group]
	if len(list) == 0 {
		return true
	}
	for _, name := range list {
		if strings.TrimSpace(name) == modelName {
			return true
		}
	}
	return false
}

// SiteGroupAllowed reports whether a site may use a billing group.
// An empty site list keeps the live group ratio. A configured list is closed.
func SiteGroupAllowed(siteID int, group string) bool {
	group = strings.TrimSpace(group)
	if siteID <= 0 || group == "" || group == "auto" {
		return true
	}
	if model.DB == nil {
		return false
	}
	site, err := GetAdtySite(siteID)
	if err != nil {
		return false
	}
	raw := strings.TrimSpace(site.AllowedGroups)
	if raw == "" {
		return true
	}
	var groups []string
	if err := common.UnmarshalJsonStr(raw, &groups); err != nil {
		return false
	}
	if len(groups) == 0 {
		return true
	}
	for _, id := range groups {
		if strings.TrimSpace(id) == group {
			return true
		}
	}
	return false
}

// ParseAdtyAllowedGroups checks a site allow-list. An empty value clears it.
func ParseAdtyAllowedGroups(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var groups []string
	if err := common.UnmarshalJsonStr(raw, &groups); err != nil || len(groups) == 0 {
		return "", common.NewMessage("Invalid parameters")
	}
	active, err := model.ActiveAdtyGroupIDs()
	if err != nil {
		return "", err
	}
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(groups))
	for _, id := range groups {
		id = strings.TrimSpace(id)
		if id == "" || id == "auto" {
			return "", common.NewMessage("Invalid parameters")
		}
		if _, ok := active[id]; !ok {
			return "", common.NewMessage("Unknown group ID {{id}} in {{key}}", map[string]any{"id": id, "key": "allowed_groups"})
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	encoded, err := common.Marshal(clean)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
