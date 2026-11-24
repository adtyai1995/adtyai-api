package setting

import (
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

var CheckSensitiveEnabled = false
var CheckSensitiveOnPromptEnabled = false

//var CheckSensitiveOnCompletionEnabled = true

// StopOnSensitiveEnabled 如果检测到敏感词，是否立刻停止生成，否则替换敏感词
var StopOnSensitiveEnabled = true

// StreamCacheQueueLength 流模式缓存队列长度，0表示无缓存
var StreamCacheQueueLength = 0

var sensitiveWordsMu sync.RWMutex

// SensitiveWords 敏感词
var SensitiveWords []string

func GetSensitiveWords() []string {
	sensitiveWordsMu.RLock()
	defer sensitiveWordsMu.RUnlock()
	return slices.Clone(SensitiveWords)
}

func SensitiveWordsToString() string {
	sensitiveWordsMu.RLock()
	snapshot := slices.Clone(SensitiveWords)
	sensitiveWordsMu.RUnlock()
	return strings.Join(snapshot, "\n")
}

func SensitiveWordsFromString(s string) {
	next := []string{}
	sw := strings.SplitSeq(s, "\n")
	for w := range sw {
		w = strings.TrimSpace(w)
		if w != "" {
			next = append(next, w)
		}
	}
	sensitiveWordsMu.Lock()
	SensitiveWords = next
	sensitiveWordsMu.Unlock()
}

func ShouldCheckPromptSensitive() bool {
	common.OptionMapRWMutex.RLock()
	enabled := CheckSensitiveEnabled && CheckSensitiveOnPromptEnabled
	common.OptionMapRWMutex.RUnlock()
	return enabled
}

//func ShouldCheckCompletionSensitive() bool {
//	return CheckSensitiveEnabled && CheckSensitiveOnCompletionEnabled
//}
