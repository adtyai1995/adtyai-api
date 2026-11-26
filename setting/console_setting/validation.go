package console_setting

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/QuantumNous/new-api/common"
)

var (
	urlRegex       = regexp.MustCompile(`^https?://(?:(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?|(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?))(?:\:[0-9]{1,5})?(?:/.*)?$`)
	dangerousChars = []string{"<script", "<iframe", "javascript:", "onload=", "onerror=", "onclick="}
	validColors    = map[string]bool{
		"blue": true, "green": true, "cyan": true, "purple": true, "pink": true,
		"red": true, "orange": true, "amber": true, "yellow": true, "lime": true,
		"light-green": true, "teal": true, "light-blue": true, "indigo": true,
		"violet": true, "grey": true, "slate": true,
	}
	slugRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

func parseJSONArray(jsonStr string, formatErrorKey string) ([]map[string]any, error) {
	var list []map[string]any
	if err := common.UnmarshalJsonStr(jsonStr, &list); err != nil {
		return nil, common.NewMessage(formatErrorKey, map[string]any{"error": err.Error()})
	}
	return list, nil
}

func exceedsMaxCharacters(s string, max int) bool {
	return len(utf16.Encode([]rune(s))) > max
}

// itemMessages are the keys of the URL and content checks shared by the
// API info and Uptime Kuma group lists.
type itemMessages struct {
	invalidURL    string
	unparsableURL string
	unsafeContent string
}

var (
	apiInfoMessages = itemMessages{
		invalidURL:    "API info #{{index}} has an invalid URL format",
		unparsableURL: "API info #{{index}} has a URL that cannot be parsed: {{error}}",
		unsafeContent: "API info #{{index}} contains disallowed content",
	}
	groupMessages = itemMessages{
		invalidURL:    "Group #{{index}} has an invalid URL format",
		unparsableURL: "Group #{{index}} has a URL that cannot be parsed: {{error}}",
		unsafeContent: "Group #{{index}} contains disallowed content",
	}
)

func validateURL(urlStr string, index int, messages itemMessages) error {
	if !urlRegex.MatchString(urlStr) {
		return common.NewMessage(messages.invalidURL, map[string]any{"index": index})
	}
	if _, err := url.Parse(urlStr); err != nil {
		return common.NewMessage(messages.unparsableURL, map[string]any{"index": index, "error": err.Error()})
	}
	return nil
}

func checkDangerousContent(content string, index int, messages itemMessages) error {
	lower := strings.ToLower(content)
	for _, d := range dangerousChars {
		if strings.Contains(lower, d) {
			return common.NewMessage(messages.unsafeContent, map[string]any{"index": index})
		}
	}
	return nil
}

func getJSONList(jsonStr string) []map[string]any {
	if jsonStr == "" {
		return []map[string]any{}
	}
	var list []map[string]any
	_ = common.UnmarshalJsonStr(jsonStr, &list)
	return list
}

const (
	maxUptimeKumaGroups              = 50
	maxUptimeKumaGroupModels         = 500
	DefaultModelProbeIntervalMinutes = 10
	MinModelProbeIntervalMinutes     = 1
	MaxModelProbeIntervalMinutes     = 60
	DefaultImageProbeIntervalMinutes = 60
	MinImageProbeIntervalMinutes     = 15
	MaxImageProbeIntervalMinutes     = 360
	DefaultModelProbeTimeoutSeconds  = 15
	MinModelProbeTimeoutSeconds      = 5
	MaxModelProbeTimeoutSeconds      = 60
)

var (
	validUptimePlatforms = map[string]bool{
		"":          true,
		"openai":    true,
		"anthropic": true,
		"gemini":    true,
		"zhipu":     true,
		"deepseek":  true,
		"moonshot":  true,
		"xai":       true,
		"azure":     true,
		"other":     true,
	}
	validUptimeProbeTypes = map[string]bool{
		"":      true,
		"auto":  true,
		"text":  true,
		"image": true,
	}
)

type UptimeKumaGroup struct {
	GroupID      string            `json:"groupId,omitempty"`
	CategoryName string            `json:"categoryName"`
	Enabled      *bool             `json:"enabled,omitempty"`
	Platform     string            `json:"platform,omitempty"`
	ProbeType    string            `json:"probeType,omitempty"`
	ProbeModel   string            `json:"probeModel,omitempty"`
	URL          string            `json:"url,omitempty"`
	Slug         string            `json:"slug,omitempty"`
	Description  string            `json:"description,omitempty"`
	Models       []string          `json:"models,omitempty"`
	MonitorMap   map[string]string `json:"monitorMap,omitempty"`
}

func (g UptimeKumaGroup) IsEnabled() bool {
	return g.Enabled == nil || *g.Enabled
}

func (g UptimeKumaGroup) EffectiveProbeModel() string {
	if pm := strings.TrimSpace(g.ProbeModel); pm != "" {
		return pm
	}
	for _, m := range g.Models {
		if trimmed := strings.TrimSpace(m); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ValidateModelProbeIntervalMinutes(raw string) error {
	trimmed := strings.TrimSpace(raw)
	val, err := strconv.Atoi(trimmed)
	if err != nil || val < MinModelProbeIntervalMinutes || val > MaxModelProbeIntervalMinutes {
		return common.NewMessage("Model probe interval must be an integer between 1 and 60 minutes")
	}
	return nil
}

func ValidateImageProbeIntervalMinutes(raw string) error {
	trimmed := strings.TrimSpace(raw)
	val, err := strconv.Atoi(trimmed)
	if err != nil || val < MinImageProbeIntervalMinutes || val > MaxImageProbeIntervalMinutes {
		return common.NewMessage("Image probe interval must be an integer between 15 and 360 minutes")
	}
	return nil
}

func ValidateModelProbeTimeoutSeconds(raw string) error {
	trimmed := strings.TrimSpace(raw)
	val, err := strconv.Atoi(trimmed)
	if err != nil || val < MinModelProbeTimeoutSeconds || val > MaxModelProbeTimeoutSeconds {
		return common.NewMessage("Model probe timeout must be an integer between 5 and 60 seconds")
	}
	return nil
}

func GetModelProbeIntervalMinutes() int {
	val := GetConsoleSetting().ModelProbeIntervalMinutes
	if val <= 0 {
		return DefaultModelProbeIntervalMinutes
	}
	if val < MinModelProbeIntervalMinutes {
		return MinModelProbeIntervalMinutes
	}
	if val > MaxModelProbeIntervalMinutes {
		return MaxModelProbeIntervalMinutes
	}
	return val
}

func GetImageProbeIntervalMinutes() int {
	val := GetConsoleSetting().ImageProbeIntervalMinutes
	if val <= 0 {
		return DefaultImageProbeIntervalMinutes
	}
	if val < MinImageProbeIntervalMinutes {
		return MinImageProbeIntervalMinutes
	}
	if val > MaxImageProbeIntervalMinutes {
		return MaxImageProbeIntervalMinutes
	}
	return val
}

func GetModelProbeTimeoutSeconds() int {
	val := GetConsoleSetting().ModelProbeTimeoutSeconds
	if val <= 0 {
		return DefaultModelProbeTimeoutSeconds
	}
	if val < MinModelProbeTimeoutSeconds {
		return MinModelProbeTimeoutSeconds
	}
	if val > MaxModelProbeTimeoutSeconds {
		return MaxModelProbeTimeoutSeconds
	}
	return val
}

func ValidateUptimeKumaSlug(slug string) error {
	trimmed := strings.TrimSpace(slug)
	if trimmed == "" {
		return common.NewMessage("Status page slug is required")
	}
	if exceedsMaxCharacters(trimmed, 100) {
		return common.NewMessage("Status page slug cannot exceed 100 characters")
	}
	if !slugRegex.MatchString(trimmed) {
		return common.NewMessage("Status page slug can only contain letters, numbers, underscores and hyphens")
	}
	return nil
}

func ValidateConsoleSettings(settingsStr string, settingType string) error {
	switch settingType {
	case "ModelProbeIntervalMinutes", "console_setting.model_probe_interval_minutes":
		return ValidateModelProbeIntervalMinutes(settingsStr)
	case "ImageProbeIntervalMinutes", "console_setting.image_probe_interval_minutes":
		return ValidateImageProbeIntervalMinutes(settingsStr)
	case "ModelProbeTimeoutSeconds", "console_setting.model_probe_timeout_seconds":
		return ValidateModelProbeTimeoutSeconds(settingsStr)
	}

	if settingsStr == "" {
		return nil
	}

	switch settingType {
	case "ApiInfo":
		return validateApiInfo(settingsStr)
	case "Announcements":
		return validateAnnouncements(settingsStr)
	case "FAQ":
		return validateFAQ(settingsStr)
	case "UptimeKumaGroups":
		return validateUptimeKumaGroups(settingsStr)
	default:
		return common.NewMessage("Unknown setting type: {{type}}", map[string]any{"type": settingType})
	}
}

func validateApiInfo(apiInfoStr string) error {
	apiInfoList, err := parseJSONArray(apiInfoStr, "Invalid API info format: {{error}}")
	if err != nil {
		return err
	}

	if len(apiInfoList) > 50 {
		return common.NewMessage("API info cannot exceed 50 entries")
	}

	for i, apiInfo := range apiInfoList {
		urlStr, ok := apiInfo["url"].(string)
		if !ok || urlStr == "" {
			return common.NewMessage("API info #{{index}} is missing the URL field", map[string]any{"index": i + 1})
		}
		route, ok := apiInfo["route"].(string)
		if !ok || route == "" {
			return common.NewMessage("API info #{{index}} is missing the route description field", map[string]any{"index": i + 1})
		}
		description, ok := apiInfo["description"].(string)
		if !ok || description == "" {
			return common.NewMessage("API info #{{index}} is missing the description field", map[string]any{"index": i + 1})
		}
		color, ok := apiInfo["color"].(string)
		if !ok || color == "" {
			return common.NewMessage("API info #{{index}} is missing the color field", map[string]any{"index": i + 1})
		}

		if err := validateURL(urlStr, i+1, apiInfoMessages); err != nil {
			return err
		}

		if exceedsMaxCharacters(urlStr, 500) {
			return common.NewMessage("API info #{{index}} URL cannot exceed 500 characters", map[string]any{"index": i + 1})
		}
		if exceedsMaxCharacters(route, 100) {
			return common.NewMessage("API info #{{index}} route description cannot exceed 100 characters", map[string]any{"index": i + 1})
		}
		if exceedsMaxCharacters(description, 200) {
			return common.NewMessage("API info #{{index}} description cannot exceed 200 characters", map[string]any{"index": i + 1})
		}

		if !validColors[color] {
			return common.NewMessage("API info #{{index}} has an invalid color value", map[string]any{"index": i + 1})
		}

		if err := checkDangerousContent(description, i+1, apiInfoMessages); err != nil {
			return err
		}
		if err := checkDangerousContent(route, i+1, apiInfoMessages); err != nil {
			return err
		}
	}
	return nil
}

func GetApiInfo() []map[string]any {
	return getJSONList(GetConsoleSetting().ApiInfo)
}

func validateAnnouncements(announcementsStr string) error {
	list, err := parseJSONArray(announcementsStr, "Invalid announcements format: {{error}}")
	if err != nil {
		return err
	}
	if len(list) > 100 {
		return common.NewMessage("Announcements cannot exceed 100 entries")
	}
	validTypes := map[string]bool{
		"default": true, "ongoing": true, "success": true, "warning": true, "error": true,
	}
	for i, ann := range list {
		content, ok := ann["content"].(string)
		if !ok || content == "" {
			return common.NewMessage("Announcement #{{index}} is missing the content field", map[string]any{"index": i + 1})
		}
		publishDateAny, exists := ann["publishDate"]
		if !exists {
			return common.NewMessage("Announcement #{{index}} is missing the publish date field", map[string]any{"index": i + 1})
		}
		publishDateStr, ok := publishDateAny.(string)
		if !ok || publishDateStr == "" {
			return common.NewMessage("Announcement #{{index}} publish date cannot be empty", map[string]any{"index": i + 1})
		}
		if _, err := time.Parse(time.RFC3339, publishDateStr); err != nil {
			return common.NewMessage("Announcement #{{index}} has an invalid publish date format", map[string]any{"index": i + 1})
		}
		if t, exists := ann["type"]; exists {
			if typeStr, ok := t.(string); ok {
				if !validTypes[typeStr] {
					return common.NewMessage("Announcement #{{index}} has an invalid type value", map[string]any{"index": i + 1})
				}
			}
		}
		if exceedsMaxCharacters(content, 500) {
			return common.NewMessage("Announcement #{{index}} content cannot exceed 500 characters", map[string]any{"index": i + 1})
		}
		if extra, exists := ann["extra"]; exists {
			if extraStr, ok := extra.(string); ok && exceedsMaxCharacters(extraStr, 100) {
				return common.NewMessage("Announcement #{{index}} note cannot exceed 100 characters", map[string]any{"index": i + 1})
			}
		}
	}
	return nil
}

func validateFAQ(faqStr string) error {
	list, err := parseJSONArray(faqStr, "Invalid FAQ format: {{error}}")
	if err != nil {
		return err
	}
	if len(list) > 100 {
		return common.NewMessage("FAQ cannot exceed 100 entries")
	}
	for i, faq := range list {
		question, ok := faq["question"].(string)
		if !ok || question == "" {
			return common.NewMessage("FAQ #{{index}} is missing the question field", map[string]any{"index": i + 1})
		}
		answer, ok := faq["answer"].(string)
		if !ok || answer == "" {
			return common.NewMessage("FAQ #{{index}} is missing the answer field", map[string]any{"index": i + 1})
		}
		if exceedsMaxCharacters(question, 200) {
			return common.NewMessage("FAQ #{{index}} question cannot exceed 200 characters", map[string]any{"index": i + 1})
		}
		if exceedsMaxCharacters(answer, 1000) {
			return common.NewMessage("FAQ #{{index}} answer cannot exceed 1000 characters", map[string]any{"index": i + 1})
		}
	}
	return nil
}

func getPublishTime(item map[string]any) time.Time {
	if v, ok := item["publishDate"]; ok {
		if s, ok2 := v.(string); ok2 {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func GetAnnouncements() []map[string]any {
	list := getJSONList(GetConsoleSetting().Announcements)
	sort.SliceStable(list, func(i, j int) bool {
		return getPublishTime(list[i]).After(getPublishTime(list[j]))
	})
	return list
}

func GetFAQ() []map[string]any {
	return getJSONList(GetConsoleSetting().FAQ)
}

func validateUptimeKumaGroups(groupsStr string) error {
	groups, err := parseJSONArray(groupsStr, "Invalid model probe group settings format: {{error}}")
	if err != nil {
		return err
	}

	if len(groups) > maxUptimeKumaGroups {
		return common.NewMessage("Model probe groups cannot exceed 50 entries")
	}

	effectiveKeySet := make(map[string]bool)

	for i, group := range groups {
		index := i + 1

		var groupID string
		if rawGroupID, exists := group["groupId"]; exists && rawGroupID != nil {
			s, ok := rawGroupID.(string)
			if !ok {
				return common.NewMessage("Group #{{index}} has an invalid group ID field", map[string]any{"index": index})
			}
			groupID = strings.TrimSpace(s)
			if exceedsMaxCharacters(groupID, 64) {
				return common.NewMessage("Group #{{index}} group ID cannot exceed 64 characters", map[string]any{"index": index})
			}
			if err := checkDangerousContent(groupID, index, groupMessages); err != nil {
				return err
			}
		}

		var categoryName string
		if rawCategoryName, exists := group["categoryName"]; exists && rawCategoryName != nil {
			s, ok := rawCategoryName.(string)
			if !ok {
				return common.NewMessage("Group #{{index}} has an invalid category name field", map[string]any{"index": index})
			}
			categoryName = strings.TrimSpace(s)
			if exceedsMaxCharacters(categoryName, 50) {
				return common.NewMessage("Group #{{index}} category name cannot exceed 50 characters", map[string]any{"index": index})
			}
			if err := checkDangerousContent(categoryName, index, groupMessages); err != nil {
				return err
			}
		}

		if groupID == "" && categoryName == "" {
			return common.NewMessage("Group #{{index}} is missing the category name field", map[string]any{"index": index})
		}

		effectiveKey := groupID
		if effectiveKey == "" {
			effectiveKey = categoryName
		}
		if effectiveKeySet[effectiveKey] {
			if groupID != "" {
				return common.NewMessage("Group #{{index}} group ID duplicates another group", map[string]any{"index": index})
			}
			return common.NewMessage("Group #{{index}} category name duplicates another group", map[string]any{"index": index})
		}
		effectiveKeySet[effectiveKey] = true

		if rawEnabled, exists := group["enabled"]; exists && rawEnabled != nil {
			if _, ok := rawEnabled.(bool); !ok {
				return common.NewMessage("Group #{{index}} has an invalid enabled field", map[string]any{"index": index})
			}
		}

		if rawPlatform, exists := group["platform"]; exists && rawPlatform != nil {
			s, ok := rawPlatform.(string)
			if !ok {
				return common.NewMessage("Group #{{index}} has an invalid platform field", map[string]any{"index": index})
			}
			platform := strings.ToLower(strings.TrimSpace(s))
			if !validUptimePlatforms[platform] {
				return common.NewMessage("Group #{{index}} has an invalid platform value", map[string]any{"index": index})
			}
		}

		if rawProbeType, exists := group["probeType"]; exists && rawProbeType != nil {
			s, ok := rawProbeType.(string)
			if !ok {
				return common.NewMessage("Group #{{index}} has an invalid probeType field", map[string]any{"index": index})
			}
			probeType := strings.ToLower(strings.TrimSpace(s))
			if !validUptimeProbeTypes[probeType] {
				return common.NewMessage("Group #{{index}} has an invalid probeType value", map[string]any{"index": index})
			}
		}

		if rawProbeModel, exists := group["probeModel"]; exists && rawProbeModel != nil {
			s, ok := rawProbeModel.(string)
			if !ok {
				return common.NewMessage("Group #{{index}} has an invalid probeModel field", map[string]any{"index": index})
			}
			probeModel := strings.TrimSpace(s)
			if exceedsMaxCharacters(probeModel, 200) {
				return common.NewMessage("Group #{{index}} probe model name cannot exceed 200 characters", map[string]any{"index": index})
			}
			if err := checkDangerousContent(probeModel, index, groupMessages); err != nil {
				return err
			}
		}

		// Legacy url/slug fields are optional and only validated when non-empty.
		if rawURL, exists := group["url"]; exists && rawURL != nil {
			s, ok := rawURL.(string)
			if !ok {
				return common.NewMessage("Group #{{index}} has an invalid URL format", map[string]any{"index": index})
			}
			urlStr := strings.TrimSpace(s)
			if urlStr != "" {
				if err := validateURL(urlStr, index, groupMessages); err != nil {
					return err
				}
				if exceedsMaxCharacters(urlStr, 500) {
					return common.NewMessage("Group #{{index}} URL cannot exceed 500 characters", map[string]any{"index": index})
				}
				if err := checkDangerousContent(urlStr, index, groupMessages); err != nil {
					return err
				}
			}
		}

		if rawSlug, exists := group["slug"]; exists && rawSlug != nil {
			s, ok := rawSlug.(string)
			if !ok {
				return common.NewMessage("Group #{{index}} has an invalid slug field", map[string]any{"index": index})
			}
			slug := strings.TrimSpace(s)
			if slug != "" {
				if exceedsMaxCharacters(slug, 100) {
					return common.NewMessage("Group #{{index}} slug cannot exceed 100 characters", map[string]any{"index": index})
				}
				if !slugRegex.MatchString(slug) {
					return common.NewMessage("Group #{{index}} slug can only contain letters, numbers, underscores and hyphens", map[string]any{"index": index})
				}
			}
		}

		var description string
		if rawDesc, exists := group["description"]; exists && rawDesc != nil {
			s, ok := rawDesc.(string)
			if !ok {
				return common.NewMessage("Group #{{index}} has an invalid description field", map[string]any{"index": index})
			}
			description = s
		}

		if exceedsMaxCharacters(description, 200) {
			return common.NewMessage("Group #{{index}} description cannot exceed 200 characters", map[string]any{"index": index})
		}

		if err := checkDangerousContent(description, index, groupMessages); err != nil {
			return err
		}

		if rawModels, exists := group["models"]; exists && rawModels != nil {
			modelsSlice, ok := rawModels.([]any)
			if !ok {
				return common.NewMessage("Group #{{index}} models must be an array of strings", map[string]any{"index": index})
			}
			if len(modelsSlice) > maxUptimeKumaGroupModels*4 {
				return common.NewMessage("Group #{{index}} models cannot exceed 500 entries", map[string]any{"index": index})
			}
			seenModels := make(map[string]struct{}, min(len(modelsSlice), maxUptimeKumaGroupModels))
			for _, item := range modelsSlice {
				modelStr, ok := item.(string)
				if !ok {
					return common.NewMessage("Group #{{index}} models must contain only strings", map[string]any{"index": index})
				}
				trimmed := strings.TrimSpace(modelStr)
				if trimmed == "" {
					continue
				}
				if exceedsMaxCharacters(trimmed, 200) {
					return common.NewMessage("Group #{{index}} model name cannot exceed 200 characters", map[string]any{"index": index})
				}
				if err := checkDangerousContent(trimmed, index, groupMessages); err != nil {
					return err
				}
				seenModels[trimmed] = struct{}{}
				if len(seenModels) > maxUptimeKumaGroupModels {
					return common.NewMessage("Group #{{index}} models cannot exceed 500 entries", map[string]any{"index": index})
				}
			}
		}

		if rawMonitorMap, exists := group["monitorMap"]; exists && rawMonitorMap != nil {
			monitorMap, ok := rawMonitorMap.(map[string]any)
			if !ok {
				return common.NewMessage("Group #{{index}} monitorMap must be a string map", map[string]any{"index": index})
			}
			if len(monitorMap) > maxUptimeKumaGroupModels {
				return common.NewMessage("Group #{{index}} monitorMap cannot exceed 500 entries", map[string]any{"index": index})
			}
			for key, val := range monitorMap {
				valStr, ok := val.(string)
				if !ok {
					return common.NewMessage("Group #{{index}} monitorMap values must be strings", map[string]any{"index": index})
				}
				trimmedKey := strings.TrimSpace(key)
				trimmedVal := strings.TrimSpace(valStr)
				if trimmedKey == "" {
					return common.NewMessage("Group #{{index}} monitorMap cannot contain empty keys", map[string]any{"index": index})
				}
				if exceedsMaxCharacters(trimmedKey, 200) || exceedsMaxCharacters(trimmedVal, 200) {
					return common.NewMessage("Group #{{index}} monitorMap entries cannot exceed 200 characters", map[string]any{"index": index})
				}
				if err := checkDangerousContent(trimmedKey, index, groupMessages); err != nil {
					return err
				}
				if err := checkDangerousContent(trimmedVal, index, groupMessages); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func ParseUptimeKumaGroups(groupsStr string) []UptimeKumaGroup {
	if strings.TrimSpace(groupsStr) == "" {
		return []UptimeKumaGroup{}
	}
	var raw []UptimeKumaGroup
	if err := common.UnmarshalJsonStr(groupsStr, &raw); err != nil {
		return []UptimeKumaGroup{}
	}
	result := make([]UptimeKumaGroup, 0, len(raw))
	seenGroupIDs := make(map[string]struct{}, len(raw))
	seenNames := make(map[string]struct{}, len(raw))
	for _, g := range raw {
		g.GroupID = strings.TrimSpace(g.GroupID)
		g.CategoryName = strings.TrimSpace(g.CategoryName)
		g.Platform = strings.ToLower(strings.TrimSpace(g.Platform))
		if !validUptimePlatforms[g.Platform] {
			g.Platform = ""
		}
		g.ProbeType = strings.ToLower(strings.TrimSpace(g.ProbeType))
		if !validUptimeProbeTypes[g.ProbeType] {
			g.ProbeType = ""
		}
		g.ProbeModel = strings.TrimSpace(g.ProbeModel)
		g.URL = strings.TrimSpace(g.URL)
		g.Slug = strings.TrimSpace(g.Slug)
		g.Description = strings.TrimSpace(g.Description)

		if g.GroupID == "" && g.CategoryName == "" {
			continue
		}
		if g.GroupID != "" {
			if _, dup := seenGroupIDs[g.GroupID]; dup {
				continue
			}
			seenGroupIDs[g.GroupID] = struct{}{}
		} else {
			if _, dup := seenNames[g.CategoryName]; dup {
				continue
			}
			seenNames[g.CategoryName] = struct{}{}
		}

		if len(g.Models) > 0 {
			seen := make(map[string]struct{}, min(len(g.Models), maxUptimeKumaGroupModels))
			models := make([]string, 0, min(len(g.Models), maxUptimeKumaGroupModels))
			for _, m := range g.Models {
				trimmed := strings.TrimSpace(m)
				if trimmed == "" {
					continue
				}
				if _, dup := seen[trimmed]; dup {
					continue
				}
				seen[trimmed] = struct{}{}
				models = append(models, trimmed)
				if len(models) >= maxUptimeKumaGroupModels {
					break
				}
			}
			if len(models) > 0 {
				g.Models = models
			} else {
				g.Models = nil
			}
		} else {
			g.Models = nil
		}

		if g.ProbeModel != "" && len(g.Models) == 0 {
			g.Models = []string{g.ProbeModel}
		} else if g.ProbeModel == "" && len(g.Models) > 0 {
			g.ProbeModel = g.Models[0]
		}

		if len(g.MonitorMap) > 0 {
			cleaned := make(map[string]string, len(g.MonitorMap))
			for k, v := range g.MonitorMap {
				kt := strings.TrimSpace(k)
				vt := strings.TrimSpace(v)
				if kt == "" || vt == "" {
					continue
				}
				cleaned[kt] = vt
			}
			if len(cleaned) > 0 {
				g.MonitorMap = cleaned
			} else {
				g.MonitorMap = nil
			}
		} else {
			g.MonitorMap = nil
		}

		result = append(result, g)
	}
	return result
}

func GetUptimeKumaGroupConfigs() []UptimeKumaGroup {
	return ParseUptimeKumaGroups(GetConsoleSetting().UptimeKumaGroups)
}

func GetUptimeKumaGroups() []map[string]any {
	return getJSONList(GetConsoleSetting().UptimeKumaGroups)
}
