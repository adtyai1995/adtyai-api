package setting

import "github.com/QuantumNous/new-api/common"

var MjNotifyEnabled = false
var MjAccountFilterEnabled = false
var MjModeClearEnabled = false
var MjForwardUrlEnabled = true
var MjActionCheckSuccessEnabled = true

func GetMjForwardUrlEnabled() bool {
	common.OptionMapRWMutex.RLock()
	v := MjForwardUrlEnabled
	common.OptionMapRWMutex.RUnlock()
	return v
}

func GetMjActionCheckSuccessEnabled() bool {
	common.OptionMapRWMutex.RLock()
	v := MjActionCheckSuccessEnabled
	common.OptionMapRWMutex.RUnlock()
	return v
}

func GetMjAccountFilterEnabled() bool {
	common.OptionMapRWMutex.RLock()
	v := MjAccountFilterEnabled
	common.OptionMapRWMutex.RUnlock()
	return v
}

func GetMjNotifyEnabled() bool {
	common.OptionMapRWMutex.RLock()
	v := MjNotifyEnabled
	common.OptionMapRWMutex.RUnlock()
	return v
}

func GetMjModeClearEnabled() bool {
	common.OptionMapRWMutex.RLock()
	v := MjModeClearEnabled
	common.OptionMapRWMutex.RUnlock()
	return v
}
