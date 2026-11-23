package common

import (
	"math"
	"sync"
)

var topupGroupRatio = map[string]float64{
	"default": 1,
	"vip":     1,
	"svip":    1,
}
var topupGroupRatioMutex sync.RWMutex

func TopupGroupRatio2JSONString() string {
	topupGroupRatioMutex.RLock()
	defer topupGroupRatioMutex.RUnlock()
	jsonBytes, err := Marshal(topupGroupRatio)
	if err != nil {
		SysError(LogText("error marshalling topup group ratio: %s", err.Error()))
	}
	return string(jsonBytes)
}

func UpdateTopupGroupRatioByJSONString(jsonStr string) error {
	parsed := make(map[string]float64)
	if err := Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return err
	}
	topupGroupRatioMutex.Lock()
	defer topupGroupRatioMutex.Unlock()
	topupGroupRatio = parsed
	return nil
}

func GetTopupGroupRatio(name string) float64 {
	topupGroupRatioMutex.RLock()
	defer topupGroupRatioMutex.RUnlock()
	ratio, ok := topupGroupRatio[name]
	if !ok || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 {
		return 1
	}
	return ratio
}
