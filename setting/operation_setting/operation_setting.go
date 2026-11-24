package operation_setting

import (
	"slices"
	"strings"
	"sync"
)

var operationSettingMu sync.RWMutex

var DemoSiteEnabled = false
var SelfUseModeEnabled = false

var AutomaticDisableKeywords = []string{
	"Your credit balance is too low",
	"This organization has been disabled.",
	"You exceeded your current quota",
	"Permission denied",
	"The security token included in the request is invalid",
	"Operation not allowed",
	"Your account is not authorized",
}

func GetDemoSiteEnabled() bool {
	operationSettingMu.RLock()
	v := DemoSiteEnabled
	operationSettingMu.RUnlock()
	return v
}

func SetDemoSiteEnabled(v bool) {
	operationSettingMu.Lock()
	DemoSiteEnabled = v
	operationSettingMu.Unlock()
}

func GetSelfUseModeEnabled() bool {
	operationSettingMu.RLock()
	v := SelfUseModeEnabled
	operationSettingMu.RUnlock()
	return v
}

func SetSelfUseModeEnabled(v bool) {
	operationSettingMu.Lock()
	SelfUseModeEnabled = v
	operationSettingMu.Unlock()
}

func GetAutomaticDisableKeywords() []string {
	operationSettingMu.RLock()
	defer operationSettingMu.RUnlock()
	return slices.Clone(AutomaticDisableKeywords)
}

func AutomaticDisableKeywordsToString() string {
	operationSettingMu.RLock()
	snapshot := slices.Clone(AutomaticDisableKeywords)
	operationSettingMu.RUnlock()
	return strings.Join(snapshot, "\n")
}

func AutomaticDisableKeywordsFromString(s string) {
	next := []string{}
	ak := strings.SplitSeq(s, "\n")
	for k := range ak {
		k = strings.TrimSpace(k)
		k = strings.ToLower(k)
		if k != "" {
			next = append(next, k)
		}
	}
	operationSettingMu.Lock()
	AutomaticDisableKeywords = next
	operationSettingMu.Unlock()
}
