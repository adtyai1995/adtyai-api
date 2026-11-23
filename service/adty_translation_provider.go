package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"gorm.io/gorm"
)

// 系统翻译的出口实现（ISS-R4-0012 / ISS-R1-0143）。Root 在后台配好「平台已有模型」或
// 「独立翻译 API」后，这里按模式分别解析上游并发起请求；没配好或未启用就老实报错，
// 由调用方保留中文并标记英文未就绪。
// 约束：不把翻译请求发回本站中转，不用客户 Key，不扣客户、团队或站长余额。
const (
	adtyTranslationProtocolOpenAI    = "openai"
	adtyTranslationProtocolAnthropic = "anthropic"

	adtyTranslationDefaultTimeout  = 30 * time.Second
	adtyTranslationAnthropicVer    = "2023-06-01"
	adtyTranslationMaxBodyBytes    = 1 << 20
	adtyTranslationMaxOutputTokens = 2048
	adtyTranslationSystemPrompt    = "Translate the user's Simplified Chinese product text into English. " +
		"Keep every number, amount, currency code, order number, placeholder and URL exactly as written, " +
		"and keep the original negation. Reply with the English translation only."
)

var (
	errAdtyTranslationNoReply  = common.NewMessage("The translation service did not return English")
	ErrAdtyTranslationDisabled = common.NewMessage("The translator is not enabled")
)

// AdtyPlatformChannelTranslateFunc 允许 main.go 或测试在不引入 service -> relay 循环依赖的前提下
// 接入自定义渠道执行器。为空时默认按 model.Channel / model.Ability 和 common.ChannelType2APIType
// 直接向渠道上游发起 HTTP 调用。
var AdtyPlatformChannelTranslateFunc func(channel *model.Channel, modelName, systemPrompt, text string, timeout time.Duration) (string, error)

// adtyTranslationHTTPClient 造一次翻译用的客户端：复用进程的出口连接池，只覆盖超时，
// 免得慢翻译一直占着调用方的 goroutine。测试替换这里。
var adtyTranslationHTTPClient = func(timeout time.Duration) *http.Client {
	base := GetSSRFProtectedHTTPClient()
	if base == nil {
		base = GetHttpClient()
	}
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.Timeout = timeout
	// R2-026: Disable redirects to prevent SSRF bypass, HTTPS downgrade, and
	// upstream API key (Authorization / x-api-key) exfiltration; enforce verified TLS.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if verifiedTransport, err := cloneAdtyVerifiedPaymentTransport(client.Transport); err == nil {
		client.Transport = verifiedTransport
	}
	return &client
}

// adtyTranslateViaProvider 是生产用的 AdtyTranslateFunc 实现：每次调用读一次 Root 配置，
// 所以后台改完配置立刻生效，不用重启。
func adtyTranslateViaProvider(modelName, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ErrAdtyTranslationInvalid
	}
	cfg, err := loadAdtyTranslationConfig()
	if err != nil {
		return "", err
	}
	if !cfg.Enabled {
		return "", ErrAdtyTranslationDisabled
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = cfg.ModelName
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return "", ErrAdtyTranslationInvalid
	}

	switch cfg.Mode {
	case model.AdtyTranslationModeOwn:
		protocol, err := adtyTranslationProtocolName(cfg.Protocol)
		if err != nil {
			return "", err
		}
		endpoint, err := adtyTranslationEndpoint(cfg.BaseURL, protocol)
		if err != nil {
			return "", err
		}
		token, err := model.OpenAdtyPurposeSecret("translation", cfg.TokenCipher)
		if err != nil {
			return "", err
		}
		if err := rejectAdtyCustomerToken(token); err != nil {
			return "", err
		}
		return adtyDispatchTranslationHTTP(protocol, endpoint, token, modelName, text, cfg.TimeoutSec)

	case model.AdtyTranslationModeSet:
		channel, upstreamModel, protocol, endpoint, token, err := resolveAdtyPlatformChannel(modelName, "")
		if err != nil {
			return "", err
		}
		if AdtyPlatformChannelTranslateFunc != nil {
			return AdtyPlatformChannelTranslateFunc(channel, upstreamModel, buildAdtyTranslationSystemPrompt(text), text, adtyTranslationTimeout(cfg.TimeoutSec))
		}
		return adtyDispatchTranslationHTTP(protocol, endpoint, token, upstreamModel, text, cfg.TimeoutSec)

	default:
		return "", ErrAdtyTranslationInvalid
	}
}

func adtyDispatchTranslationHTTP(protocol, endpoint, token, modelName, text string, timeoutSec int) (string, error) {
	payload, err := adtyTranslationPayload(protocol, modelName, text)
	if err != nil {
		return "", err
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if protocol == adtyTranslationProtocolAnthropic {
		request.Header.Set("anthropic-version", adtyTranslationAnthropicVer)
		if token != "" {
			request.Header.Set("x-api-key", token)
		}
	} else if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := adtyTranslationHTTPClient(adtyTranslationTimeout(timeoutSec)).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, adtyTranslationMaxBodyBytes))
	if err != nil {
		return "", err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("translator returned HTTP %d", response.StatusCode)
	}
	return adtyTranslationReply(protocol, raw)
}

// CheckAdtyTranslationProvider 在启动时确认「Root 打开了系统翻译」这条链真的能用。
// 没配或没启用时保持安静；启用但模式、渠道或协议地址不完整时返回原因，由 StartAdty 记一条告警。
func CheckAdtyTranslationProvider() error {
	cfg, err := loadAdtyTranslationConfig()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	switch cfg.Mode {
	case model.AdtyTranslationModeOwn:
		protocol, err := adtyTranslationProtocolName(cfg.Protocol)
		if err != nil {
			return err
		}
		_, err = adtyTranslationEndpoint(cfg.BaseURL, protocol)
		return err
	case model.AdtyTranslationModeSet:
		_, _, _, _, _, err := resolveAdtyPlatformChannel(cfg.ModelName, "")
		return err
	default:
		return ErrAdtyTranslationInvalid
	}
}

// resolveAdtyPlatformChannel 为「平台已有模型」模式解析已启用的上游渠道，不依赖独立 API 的 BaseURL。
func resolveAdtyPlatformChannel(modelName, requestHost string) (*model.Channel, string, string, string, string, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" || model.DB == nil {
		return nil, "", "", "", "", ErrAdtyTranslationInvalid
	}
	if !model.DB.Migrator().HasTable(&model.Channel{}) {
		return nil, "", "", "", "", ErrAdtyTranslationInvalid
	}

	var selected *model.Channel
	if model.DB.Migrator().HasTable(&model.Ability{}) {
		var channelIDs []int
		err := model.DB.Table("abilities").
			Select("abilities.channel_id").
			Joins("JOIN channels ON channels.id = abilities.channel_id").
			Where("abilities.enabled = ? AND abilities.model = ? AND channels.status = ?", true, modelName, common.ChannelStatusEnabled).
			Order("COALESCE(abilities.priority, 0) DESC, abilities.weight DESC, channels.id DESC").
			Pluck("abilities.channel_id", &channelIDs).Error
		if err == nil {
			for _, id := range channelIDs {
				var ch model.Channel
				if err := model.DB.First(&ch, id).Error; err == nil && ch.Status == common.ChannelStatusEnabled {
					selected = &ch
					break
				}
			}
		}
	}

	if selected == nil {
		var channels []model.Channel
		if err := model.DB.Where("status = ?", common.ChannelStatusEnabled).
			Order("COALESCE(priority, 0) DESC, COALESCE(weight, 0) DESC, id DESC").
			Find(&channels).Error; err != nil {
			return nil, "", "", "", "", err
		}
		for i := range channels {
			if _, ok := adtyChannelSupportsModel(&channels[i], modelName); ok {
				selected = &channels[i]
				break
			}
		}
	}

	if selected == nil {
		return nil, "", "", "", "", ErrAdtyTranslationInvalid
	}

	upstreamModel, _ := adtyChannelSupportsModel(selected, modelName)
	if upstreamModel == "" {
		upstreamModel = modelName
	}

	protocol, err := adtyChannelProtocol(selected.Type)
	if err != nil {
		return nil, "", "", "", "", err
	}

	baseURL := strings.TrimSpace(selected.GetBaseURL())
	if baseURL == "" {
		baseURL = strings.TrimSpace(constant.GetChannelBaseURL(selected.Type))
	}
	endpoint, err := adtyTranslationEndpoint(baseURL, protocol)
	if err != nil {
		return nil, "", "", "", "", err
	}

	key, _, keyErr := selected.GetNextEnabledKey()
	if keyErr != nil {
		return nil, "", "", "", "", ErrAdtyTranslationInvalid
	}
	key = strings.TrimSpace(key)
	if requestHost != "" {
		if err := rejectAdtyTranslationSelf(baseURL, requestHost, key); err != nil {
			return nil, "", "", "", "", err
		}
	} else if err := rejectAdtyCustomerToken(key); err != nil {
		return nil, "", "", "", "", err
	}

	return selected, upstreamModel, protocol, endpoint, key, nil
}

func adtyChannelSupportsModel(ch *model.Channel, modelName string) (string, bool) {
	if ch == nil || modelName == "" {
		return "", false
	}
	if rawMapping := strings.TrimSpace(ch.GetModelMapping()); rawMapping != "" {
		var mapping map[string]string
		if err := common.Unmarshal([]byte(rawMapping), &mapping); err == nil {
			if mapped, ok := mapping[modelName]; ok && strings.TrimSpace(mapped) != "" {
				return strings.TrimSpace(mapped), true
			}
		}
	}
	for _, item := range ch.GetModels() {
		item = strings.TrimSpace(item)
		if item == modelName || item == "*" {
			return modelName, true
		}
	}
	return "", false
}

func adtyChannelProtocol(channelType int) (string, error) {
	apiType, ok := common.ChannelType2APIType(channelType)
	if !ok && channelType == constant.ChannelTypeTaskPlugin {
		return "", ErrAdtyTranslationInvalid
	}
	if channelType == constant.ChannelTypeAnthropic || apiType == constant.APITypeAnthropic {
		return adtyTranslationProtocolAnthropic, nil
	}
	return adtyTranslationProtocolOpenAI, nil
}

func adtyTranslationProtocolName(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "openai", "openai-compatible", "chat-completions":
		return adtyTranslationProtocolOpenAI, nil
	case "anthropic", "claude":
		return adtyTranslationProtocolAnthropic, nil
	}
	return "", ErrAdtyTranslationInvalid
}

// adtyTranslationEndpoint 把 Root 填的接口地址补成完整的调用地址。地址允许写成根地址、
// 带 /v1 的地址，或已经带上 /chat/completions 的完整地址。和保存时一样只收 https。
func adtyTranslationEndpoint(rawBaseURL, protocol string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return "", ErrAdtyTranslationInvalid
	}
	if adtyTranslationTargetIsSelf(parsed.Host) {
		return "", ErrAdtyTranslationSelf
	}
	if err := validateURLWithCurrentFetchSetting(parsed.String(), false); err != nil {
		return "", ErrAdtyTranslationSelf
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if protocol == adtyTranslationProtocolAnthropic {
		if !strings.HasSuffix(path, "/messages") {
			if path == "" {
				path = "/v1/messages"
			} else {
				path += "/messages"
			}
		}
	} else if !strings.HasSuffix(path, "/chat/completions") {
		if path == "" {
			path = "/v1/chat/completions"
		} else {
			path += "/chat/completions"
		}
	}
	parsed.Path = path
	parsed.RawQuery = ""
	return parsed.String(), nil
}

// adtyTranslationTargetIsSelf 防止翻译请求回到本站：本站品牌域名、主站域名、API 域名、分站根域名/自定义域名与私网/环回地址都算自己。
func adtyTranslationTargetIsSelf(host string) bool {
	host = strings.Trim(AdtyNormalizeHost(host), "[]")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "0.0.0.0" {
		return true
	}
	if ip := common.ParseIP(host); ip != nil {
		if common.IsPrivateIP(ip) || ip.IsPrivate() || ip.IsLoopback() ||
			ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified() || ip.IsMulticast() ||
			common.IsIpInCIDRList(ip, []string{"100.64.0.0/10"}) {
			return true
		}
	}
	if AdtyIsMainHost(host) {
		return true
	}
	own := []string{}
	addOwn := func(domain string) {
		domain = strings.Trim(AdtyNormalizeHost(domain), "[]")
		if domain != "" && domain != "localhost" {
			own = append(own, domain)
		}
	}
	addOwn(hostOfURL(common.AdtyBrandURL))
	if main := adtyMainHost(); main != "" && main != "localhost" {
		addOwn(main)
		if common.ParseIP(main) == nil {
			addOwn(adtyRegistrableDomain(main))
		}
	}
	if apiHost := hostOfURL(adty_setting.GetSiteSetting().APIBaseURL); apiHost != "" && apiHost != "localhost" {
		addOwn(apiHost)
		if common.ParseIP(apiHost) == nil {
			addOwn(adtyRegistrableDomain(apiHost))
		}
	}
	reseller := resellerSetting()
	for _, domain := range reseller.RootDomains {
		addOwn(domain)
	}
	addOwn(reseller.CnameTarget)
	addOwn(reseller.FallbackOrigin)
	for _, domain := range own {
		if adtyHostUnder(host, domain) {
			return true
		}
	}
	if model.DB != nil {
		if model.DB.Migrator().HasTable(&model.AdtySite{}) {
			if AdtyIsCustomDomainHost(host) || AdtyIsApiOnlyHost(host) || AdtySiteForHost(host) != nil {
				return true
			}
			if sub, root, ok := strings.Cut(host, "."); ok && sub != "" && root != "" {
				var count int64
				if err := model.DB.Model(&model.AdtySite{}).
					Where("LOWER(subdomain) = ? AND LOWER(root_domain) = ?", sub, root).
					Count(&count).Error; err == nil && count > 0 {
					return true
				}
			}
		}
		if model.DB.Migrator().HasTable(&model.AdtySiteDomain{}) {
			var count int64
			if err := model.DB.Model(&model.AdtySiteDomain{}).
				Where("LOWER(hostname) = ?", host).
				Count(&count).Error; err == nil && count > 0 {
				return true
			}
		}
	}
	return false
}

func adtyTranslationTimeout(seconds int) time.Duration {
	if seconds <= 0 || seconds > maxTimeoutSeconds {
		return adtyTranslationDefaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

func adtyTranslationPayload(protocol, modelName, text string) (any, error) {
	systemPrompt := buildAdtyTranslationSystemPrompt(text)
	switch protocol {
	case adtyTranslationProtocolAnthropic:
		return map[string]any{
			"model":      modelName,
			"max_tokens": adtyTranslationMaxOutputTokens,
			"system":     systemPrompt,
			"messages":   []map[string]string{{"role": "user", "content": text}},
		}, nil
	case adtyTranslationProtocolOpenAI:
		return map[string]any{
			"model":  modelName,
			"stream": false,
			"messages": []map[string]string{
				{"role": "system", "content": systemPrompt},
				{"role": "user", "content": text},
			},
		}, nil
	}
	return nil, ErrAdtyTranslationInvalid
}

func adtyTranslationReply(protocol string, raw []byte) (string, error) {
	var english string
	if protocol == adtyTranslationProtocolAnthropic {
		var decoded struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := common.Unmarshal(raw, &decoded); err != nil {
			return "", errAdtyTranslationNoReply
		}
		for _, part := range decoded.Content {
			if part.Type == "text" {
				english = part.Text
				break
			}
		}
	} else {
		var decoded struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := common.Unmarshal(raw, &decoded); err != nil {
			return "", errAdtyTranslationNoReply
		}
		if len(decoded.Choices) > 0 {
			english = decoded.Choices[0].Message.Content
		}
	}
	english = strings.TrimSpace(english)
	if english == "" {
		return "", errAdtyTranslationNoReply
	}
	return english, nil
}
