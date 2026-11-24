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
