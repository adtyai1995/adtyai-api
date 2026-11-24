package setting

import (
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
)

const DefaultMaxTokenAutoGroups = 5

var (
	autoGroupsMu sync.RWMutex
	autoGroups   = []string{
		"default",
	}
)

var DefaultUseAutoGroup = false

var maxTokenAutoGroups atomic.Int64

func init() {
	maxTokenAutoGroups.Store(DefaultMaxTokenAutoGroups)
}

func ContainsAutoGroup(group string) bool {
	autoGroupsMu.RLock()
	defer autoGroupsMu.RUnlock()
	return slices.Contains(autoGroups, group)
}

func UpdateAutoGroupsByJsonString(jsonString string) error {
	parsed := make([]string, 0)
	if err := common.Unmarshal([]byte(jsonString), &parsed); err != nil {
		return err
	}
	autoGroupsMu.Lock()
	autoGroups = parsed
	autoGroupsMu.Unlock()
	return nil
}

func AutoGroups2JsonString() string {
	autoGroupsMu.RLock()
	snapshot := slices.Clone(autoGroups)
	autoGroupsMu.RUnlock()
	jsonBytes, err := common.Marshal(snapshot)
	if err != nil {
		return "[]"
	}
	return string(jsonBytes)
}

func GetAutoGroups() []string {
	autoGroupsMu.RLock()
	defer autoGroupsMu.RUnlock()
	return slices.Clone(autoGroups)
}

func GetMaxTokenAutoGroups() int {
	return int(maxTokenAutoGroups.Load())
}

func ValidateMaxTokenAutoGroups(value string) error {
	maxCount, err := strconv.Atoi(value)
	if err != nil || maxCount <= 0 {
		return fmt.Errorf("MaxTokenAutoGroups must be a positive integer")
	}
	return nil
}

func UpdateMaxTokenAutoGroups(value string) error {
	if err := ValidateMaxTokenAutoGroups(value); err != nil {
		return err
	}
	maxCount, _ := strconv.Atoi(value)
	maxTokenAutoGroups.Store(int64(maxCount))
	return nil
}
