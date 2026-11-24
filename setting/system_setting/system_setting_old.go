package system_setting

import "github.com/QuantumNous/new-api/common"

var ServerAddress = "http://localhost:3000"
var TaskPublicAddress = ""
var WorkerUrl = ""
var WorkerValidKey = ""
var WorkerAllowHttpImageRequestEnabled = false

func GetServerAddress() string {
	common.OptionMapRWMutex.RLock()
	addr := ServerAddress
	common.OptionMapRWMutex.RUnlock()
	return addr
}

func GetWorkerConfig() (workerURL, validKey string, allowHTTPImage bool) {
	common.OptionMapRWMutex.RLock()
	workerURL, validKey, allowHTTPImage = WorkerUrl, WorkerValidKey, WorkerAllowHttpImageRequestEnabled
	common.OptionMapRWMutex.RUnlock()
	return
}

func GetWorkerValidKey() string {
	common.OptionMapRWMutex.RLock()
	key := WorkerValidKey
	common.OptionMapRWMutex.RUnlock()
	return key
}

func EnableWorker() bool {
	common.OptionMapRWMutex.RLock()
	enabled := WorkerUrl != ""
	common.OptionMapRWMutex.RUnlock()
	return enabled
}
