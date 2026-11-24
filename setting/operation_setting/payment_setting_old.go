/**
此文件为旧版支付设置文件，如需增加新的参数、变量等，请在 payment_setting.go 中添加
This file is the old version of the payment settings file. If you need to add new parameters, variables, etc., please add them in payment_setting.go
*/

package operation_setting

import "sync"

var paymentOldMu sync.RWMutex

var CustomCallbackAddress = ""
var Price = 7.3
var MinTopUp = 1
var USDExchangeRate = 7.3

func GetMinTopUp() int {
	paymentOldMu.RLock()
	v := MinTopUp
	paymentOldMu.RUnlock()
	return v
}

func SetMinTopUp(v int) {
	paymentOldMu.Lock()
	MinTopUp = v
	paymentOldMu.Unlock()
}

func GetPrice() float64 {
	paymentOldMu.RLock()
	v := Price
	paymentOldMu.RUnlock()
	return v
}

func SetPrice(v float64) {
	paymentOldMu.Lock()
	Price = v
	paymentOldMu.Unlock()
}

func GetUSDExchangeRate() float64 {
	paymentOldMu.RLock()
	v := USDExchangeRate
	paymentOldMu.RUnlock()
	return v
}

func SetUSDExchangeRate(v float64) {
	paymentOldMu.Lock()
	USDExchangeRate = v
	paymentOldMu.Unlock()
}

func GetCustomCallbackAddress() string {
	paymentOldMu.RLock()
	v := CustomCallbackAddress
	paymentOldMu.RUnlock()
	return v
}

func SetCustomCallbackAddress(v string) {
	paymentOldMu.Lock()
	CustomCallbackAddress = v
	paymentOldMu.Unlock()
}
