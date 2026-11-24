package setting

import (
	"maps"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

var chatsMu sync.RWMutex

var Chats = []map[string]string{
	//{
	//	"ChatGPT Next Web 官方示例": "https://app.nextchat.dev/#/?settings={\"key\":\"{key}\",\"url\":\"{address}\"}",
	//},
	{
		"Cherry Studio": "cherrystudio://providers/api-keys?v=1&data={cherryConfig}",
	},
	{
		"AionUI": "aionui://provider/add?v=1&data={aionuiConfig}",
	},
	{
		"流畅阅读": "fluentread",
	},
	{
		"CC Switch": "ccswitch",
	},
	{
		"DeepChat": "deepchat://provider/install?v=1&data={deepchatConfig}",
	},
	{
		"AQBot": "aqbot://providers?{aqbotConfig}",
	},
	{
		"Lobe Chat 官方示例": "https://chat-preview.lobehub.com/?settings={\"keyVaults\":{\"openai\":{\"apiKey\":\"{key}\",\"baseURL\":\"{address}/v1\"}}}",
	},
	{
		"AI as Workspace": "https://aiaw.app/set-provider?provider={\"type\":\"openai\",\"settings\":{\"apiKey\":\"{key}\",\"baseURL\":\"{address}/v1\",\"compatibility\":\"strict\"}}",
	},
	{
		"AMA 问天": "ama://set-api-key?server={address}&key={key}",
	},
	{
		"OpenCat": "opencat://team/join?domain={address}&token={key}",
	},
}

func GetChats() []map[string]string {
	chatsMu.RLock()
	defer chatsMu.RUnlock()
	out := make([]map[string]string, len(Chats))
	for i, item := range Chats {
		out[i] = maps.Clone(item)
	}
	return out
}

func UpdateChatsByJsonString(jsonString string) error {
	parsed := make([]map[string]string, 0)
	if err := common.Unmarshal([]byte(jsonString), &parsed); err != nil {
		return err
	}
	chatsMu.Lock()
	Chats = parsed
	chatsMu.Unlock()
	return nil
}

func Chats2JsonString() string {
	chatsMu.RLock()
	snapshot := make([]map[string]string, len(Chats))
	for i, item := range Chats {
		snapshot[i] = maps.Clone(item)
	}
	chatsMu.RUnlock()
	jsonBytes, err := common.Marshal(snapshot)
	if err != nil {
		common.SysLog(common.LogText("error marshalling chats: %s", err.Error()))
		return "[]"
	}
	return string(jsonBytes)
}
