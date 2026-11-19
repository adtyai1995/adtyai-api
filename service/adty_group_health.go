package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	// AdtyGroupProbeIdentity 是主动可用性探针的独立身份标识，不关联任何客户、团队或分站钱包。
	AdtyGroupProbeIdentity = "adty-group-probe"
	// AdtyGroupProbeUserID 固定为 0，确保探针绝不触发客户扣费、团队记账、分站抽成或邀请返佣。
	AdtyGroupProbeUserID = 0
	// AdtyGroupProbeHeader 标记由平台独立探针发起的上游探测请求。
	AdtyGroupProbeHeader = "X-Adty-Probe-Identity"
)

var (
	adtyGroupProbeOnce sync.Once
	// AdtyGroupProbeDispatchFunc 执行单次分组模型上游探活。默认直接用渠道密钥请求上游并分类结果，不经过客户计费链路。
	AdtyGroupProbeDispatchFunc = defaultAdtyGroupProbeDispatch
)

func init() {
	perfmetrics.GroupHealthObserver = observeAdtyGroupHealth
	adtyMasterTasks = append(adtyMasterTasks, StartAdtyGroupHealthProbes)
}

type AdtyGroupHealthView struct {
	Group              string                 `json:"group"`
	Status             string                 `json:"status"`
	RequestSuccessRate *float64               `json:"request_success_rate"`
	RequestSamples     int                    `json:"request_samples"`
	ProbeRate          *float64               `json:"probe_rate"`
	ProbeSamples       int                    `json:"probe_samples"`
	Scope              string                 `json:"scope"`
	UpdatedAt          int64                  `json:"updated_at"`
	Series             []AdtyGroupHealthPoint `json:"series"`
}

type AdtyGroupHealthPoint struct {
	Ts      int64 `json:"ts"`
	Success int   `json:"success"`
	Fail    int   `json:"fail"`
}

func observeAdtyGroupHealth(group, modelName string, userId int, outcome perfmetrics.Outcome) {
	if model.DB == nil || group == "" {
		return
	}
	kind, record := adtyGroupHealthFromOutcome(string(outcome))
	if !record {
		return
	}
	siteId := resolveAdtyGroupHealthUserSiteID(userId)
	_ = RecordAdtyGroupHealth(siteId, group, modelName, model.AdtyGroupHealthRequest, kind, time.Now())
}

func resolveAdtyGroupHealthUserSiteID(userId int) int {
	if userId <= 0 || model.DB == nil {
		return 0
	}
	var user model.User
	if err := model.DB.Select("site_id").First(&user, userId).Error; err == nil && user.SiteId > 0 {
		return user.SiteId
	}
	if model.DB.Migrator().HasTable(&model.AdtySiteCustomer{}) {
		var cust model.AdtySiteCustomer
		if err := model.AdtyLiveSiteCustomer(model.DB).Select("site_id").Where("user_id = ?", userId).First(&cust).Error; err == nil && cust.SiteId > 0 {
			return cust.SiteId
		}
	}
	return 0
}

func adtyGroupHealthFromOutcome(outcome string) (string, bool) {
	switch outcome {
	case string(perfmetrics.OutcomeSuccess):
		return model.AdtyGroupHealthSuccess, true
	case string(perfmetrics.OutcomeFailure):
		return model.AdtyGroupHealthUpstream, true
	default:
		return model.AdtyGroupHealthCustomer, false
	}
}

func RecordAdtyGroupHealth(siteId int, group, modelName, kind, outcome string, now time.Time) error {
	group = strings.TrimSpace(group)
	modelName = strings.TrimSpace(modelName)
	if siteId < 0 || group == "" || group == "auto" || (kind != model.AdtyGroupHealthRequest && kind != model.AdtyGroupHealthProbe) {
		return ErrAdtyTranslationInvalid
	}
	if outcome != model.AdtyGroupHealthSuccess && outcome != model.AdtyGroupHealthUpstream && outcome != model.AdtyGroupHealthCustomer {
		return ErrAdtyTranslationInvalid
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := recordAdtyGroupHealthSampleTx(tx, siteId, group, modelName, kind, outcome, now); err != nil {
			return err
		}
		return touchAdtyGroupHealthHeartbeatTx(tx, now)
	})
}

func recordAdtyGroupHealthSampleTx(tx *gorm.DB, siteId int, group, modelName, kind, outcome string, now time.Time) error {
	return tx.Create(&model.AdtyGroupHealthSample{
		SiteId: siteId, GroupName: group, ModelName: modelName, Kind: kind, Outcome: outcome, CreatedAt: now.Unix(),
	}).Error
}

func touchAdtyGroupHealthHeartbeatTx(tx *gorm.DB, now time.Time) error {
	var state model.AdtyGroupHealthState
	found := tx.First(&state, 1)
	state.Id = 1
	state.HeartbeatAt = now.Unix()
	if state.SuccessMin == "" {
		state.SuccessMin = "0.95"
	}
	if state.StaleSeconds == 0 {
		state.StaleSeconds = 6 * 3600
	}
	if found.Error == gorm.ErrRecordNotFound {
		return tx.Create(&state).Error
	}
	if found.Error != nil {
		return found.Error
	}
	return tx.Model(&state).Updates(map[string]any{"heartbeat_at": now.Unix()}).Error
}

func RecordAdtyGroupProbe(siteId int, group, modelName string, ok bool, now time.Time) error {
	outcome := model.AdtyGroupHealthUpstream
	if ok {
		outcome = model.AdtyGroupHealthSuccess
	}
	return recordAdtyGroupProbeOutcome(siteId, group, modelName, outcome, now)
}

func recordAdtyGroupProbeOutcome(siteId int, group, modelName, outcome string, now time.Time) error {
	group = strings.TrimSpace(group)
	modelName = strings.TrimSpace(modelName)
	if siteId < 0 || group == "" || group == "auto" {
		return ErrAdtyTranslationInvalid
	}
	if siteId > 0 && !SiteGroupAllowed(siteId, group) {
		return ErrAdtyTranslationInvalid
	}
	if err := RecordAdtyGroupHealth(siteId, group, modelName, model.AdtyGroupHealthProbe, outcome, now); err != nil {
		return err
	}
	// 平台级探针样本同步扇出到允许该分组的活跃分站，避免分站视图永远停在无样本或被迫只看主站 0。
	if siteId == 0 {
		for _, activeSiteID := range activeSiteIDsForGroupProbe(group) {
			_ = RecordAdtyGroupHealth(activeSiteID, group, modelName, model.AdtyGroupHealthProbe, outcome, now)
		}
	}
	return nil
}

func activeSiteIDsForGroupProbe(group string) []int {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtySite{}) {
		return nil
	}
	var sites []model.AdtySite
	if err := model.DB.Select("id", "status", "allowed_groups").
		Where("status = ?", model.AdtySiteStatusActive).
		Order("id").Find(&sites).Error; err != nil {
		return nil
	}
	out := make([]int, 0, len(sites))
	for _, site := range sites {
		if SiteGroupAllowed(site.Id, group) {
			out = append(out, site.Id)
		}
	}
	return out
}

// ValidateAdtyGroupProbeIdentity 校验主动探针的身份：必须使用独立标识 AdtyGroupProbeIdentity、
// user_id=0 且绝不能使用任何客户 Token。
func ValidateAdtyGroupProbeIdentity(identity string, userID int, token string) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		identity = AdtyGroupProbeIdentity
	}
	if identity != AdtyGroupProbeIdentity || userID != AdtyGroupProbeUserID {
		return ErrAdtyTranslationInvalid
	}
	if strings.TrimSpace(token) != "" {
		if err := rejectAdtyCustomerToken(token); err != nil {
			return err
		}
	}
	return nil
}

// AdtyGroupProbeResult 描述一次主动探针调用的响应状态与错误分类信息。
type AdtyGroupProbeResult struct {
	StatusCode int
	ErrorCode  string
	ErrorType  string
	Err        error
}

// ClassifyAdtyGroupProbeOutcome 将主动探针结果分为：
// - AdtyGroupHealthSuccess (true): 上游正常返回
// - AdtyGroupHealthCustomer (false): 客户端参数/内容拒绝，不计入上游失败
// - AdtyGroupHealthUpstream (true): 上游鉴权、限流、超额、5xx 或网络错误
func ClassifyAdtyGroupProbeOutcome(res AdtyGroupProbeResult) (string, bool) {
	code := strings.ToLower(strings.TrimSpace(res.ErrorCode))
	errType := strings.ToLower(strings.TrimSpace(res.ErrorType))
	if res.Err == nil && res.StatusCode >= http.StatusOK && res.StatusCode < http.StatusMultipleChoices && code == "" && errType == "" {
		return model.AdtyGroupHealthSuccess, true
	}
	for _, val := range []string{code, errType} {
		switch val {
		case "invalid_api_key", "api_key_invalid", "api_key_expired", "api_key_service_blocked", "invalid_authentication",
			"authentication_error", "unauthenticated", "permission_denied", "permission_error", "access_denied",
			"insufficient_quota", "quota_exceeded", "resource_exhausted", "rate_limit_exceeded", "rate_limit_error",
			"overloaded_error", "server_error", "internal_error", "service_unavailable", "model_not_found":
			return model.AdtyGroupHealthUpstream, true
		case "context_length_exceeded", "invalid_request", "invalid_request_error", "invalid_argument",
			"sensitive_words_detected", "prompt_blocked", "content_filter", "content_policy_violation", "safety",
			"bad_request_body":
			return model.AdtyGroupHealthCustomer, false
		}
		if strings.HasPrefix(val, "violation_fee.") {
			return model.AdtyGroupHealthCustomer, false
		}
	}
	switch res.StatusCode {
	case http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusConflict,
		http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return model.AdtyGroupHealthCustomer, false
	}
	return model.AdtyGroupHealthUpstream, true
}

// ExecuteAdtyGroupProbe 以独立探针身份对指定分站/分组/模型执行一次真实上游探测，并写入分组健康样本。
// 不绑定客户或团队身份，不写消费日志、余额流水、分站收益、邀请佣金或业绩统计，也不改动任何密钥分组。
func ExecuteAdtyGroupProbe(siteId int, group, modelName string, now time.Time) (model.AdtyGroupHealthSample, error) {
	group = strings.TrimSpace(group)
	modelName = strings.TrimSpace(modelName)
	if siteId < 0 || group == "" || group == "auto" || modelName == "" || model.DB == nil {
		return model.AdtyGroupHealthSample{}, ErrAdtyTranslationInvalid
	}
	if err := ValidateAdtyGroupProbeIdentity(AdtyGroupProbeIdentity, AdtyGroupProbeUserID, ""); err != nil {
		return model.AdtyGroupHealthSample{}, err
	}
	if siteId > 0 && !SiteGroupAllowed(siteId, group) {
		return model.AdtyGroupHealthSample{}, ErrAdtyTranslationInvalid
	}
	if !GroupModelAllowed(group, modelName) {
		return model.AdtyGroupHealthSample{}, ErrAdtyTranslationInvalid
	}
	channel, upstreamModel, protocol, endpoint, key, err := resolveAdtyGroupProbeChannel(group, modelName)
	var outcome string
	if err != nil {
		outcome = model.AdtyGroupHealthUpstream
	} else {
		if err := ValidateAdtyGroupProbeIdentity(AdtyGroupProbeIdentity, AdtyGroupProbeUserID, key); err != nil {
			return model.AdtyGroupHealthSample{}, err
		}
		probeRes := AdtyGroupProbeDispatchFunc(channel, upstreamModel, protocol, endpoint, key)
		outcome, _ = ClassifyAdtyGroupProbeOutcome(probeRes)
	}
	if err := recordAdtyGroupProbeOutcome(siteId, group, modelName, outcome, now); err != nil {
		return model.AdtyGroupHealthSample{}, err
	}
	return model.AdtyGroupHealthSample{
		SiteId:    siteId,
		GroupName: group,
		ModelName: modelName,
		Kind:      model.AdtyGroupHealthProbe,
		Outcome:   outcome,
		CreatedAt: now.Unix(),
	}, nil
}

// RunAdtyGroupProbesOnce 扫描当前已启用渠道的（分组, 模型）路由，逐条用独立身份执行主动探活并记录样本。
func RunAdtyGroupProbesOnce(now time.Time) (int, error) {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.Channel{}) {
		return 0, nil
	}
	type pair struct {
		group string
		model string
	}
	var pairs []pair
	seen := map[pair]struct{}{}
	addPair := func(group, modelName string) {
		group = strings.TrimSpace(group)
		modelName = strings.TrimSpace(modelName)
		if group == "" || group == "auto" || modelName == "" || modelName == "*" {
			return
		}
		if !GroupModelAllowed(group, modelName) {
			return
		}
		p := pair{group: group, model: modelName}
		if _, exists := seen[p]; exists {
			return
		}
		seen[p] = struct{}{}
		pairs = append(pairs, p)
	}

	if model.DB.Migrator().HasTable(&model.Ability{}) {
		var rows []struct {
			Group string `gorm:"column:group"`
			Model string `gorm:"column:model"`
		}
		if err := model.DB.Table("abilities").
			Select("abilities."+model.AdtyGroupColumn()+" AS \"group\", abilities.model AS model").
			Joins("JOIN channels ON channels.id = abilities.channel_id").
			Where("abilities.enabled = ? AND channels.status = ?", true, common.ChannelStatusEnabled).
			Order("abilities.model ASC").
			Scan(&rows).Error; err == nil {
			for _, row := range rows {
				addPair(row.Group, row.Model)
			}
		}
	}
	if len(pairs) == 0 {
		var channels []model.Channel
		if err := model.DB.Where("status = ?", common.ChannelStatusEnabled).Order("id ASC").Find(&channels).Error; err != nil {
			return 0, err
		}
		for _, ch := range channels {
			models := ch.GetModels()
			for _, group := range ch.GetGroups() {
				if ch.TestModel != nil && strings.TrimSpace(*ch.TestModel) != "" {
					addPair(group, *ch.TestModel)
					continue
				}
				if len(models) > 0 {
					addPair(group, models[0])
				}
			}
		}
	}

	probed := 0
	for _, item := range pairs {
		if _, err := ExecuteAdtyGroupProbe(0, item.group, item.model, now); err == nil {
			probed++
		}
	}
	return probed, nil
}

// StartAdtyGroupHealthProbes 在主节点启动周期性分组可用性主动探针（默认关闭自动轮询，可通过 ADTY_GROUP_PROBE_INTERVAL_SECONDS 开启）。
func StartAdtyGroupHealthProbes() {
	if !common.IsMasterNode {
		return
	}
	interval := common.GetEnvOrDefault("ADTY_GROUP_PROBE_INTERVAL_SECONDS", 0)
	if interval <= 0 {
		return
	}
	if interval < 30 {
		interval = 30
	}
	adtyGroupProbeOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Duration(interval) * time.Second)
			defer ticker.Stop()
			for now := range ticker.C {
				_, _ = RunAdtyGroupProbesOnce(now)
			}
		}()
	})
}

func resolveAdtyGroupProbeChannel(group, modelName string) (*model.Channel, string, string, string, string, error) {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.Channel{}) {
		return nil, "", "", "", "", ErrAdtyTranslationInvalid
	}
	var selected *model.Channel
	if model.DB.Migrator().HasTable(&model.Ability{}) {
		var channelIDs []int
		err := model.DB.Table("abilities").
			Select("abilities.channel_id").
			Joins("JOIN channels ON channels.id = abilities.channel_id").
			Where("abilities.enabled = ? AND abilities."+model.AdtyGroupColumn()+" = ? AND abilities.model = ? AND channels.status = ?",
				true, group, modelName, common.ChannelStatusEnabled).
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
			if !slices.Contains(channels[i].GetGroups(), group) {
				continue
			}
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
	if err := rejectAdtyCustomerToken(key); err != nil {
		return nil, "", "", "", "", err
	}
	return selected, upstreamModel, protocol, endpoint, key, nil
}

func defaultAdtyGroupProbeDispatch(_ *model.Channel, modelName, protocol, endpoint, token string) AdtyGroupProbeResult {
	var payload map[string]any
	if protocol == adtyTranslationProtocolAnthropic {
		payload = map[string]any{
			"model":      modelName,
			"max_tokens": 1,
			"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		}
	} else {
		payload = map[string]any{
			"model":      modelName,
			"max_tokens": 1,
			"stream":     false,
			"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return AdtyGroupProbeResult{Err: err}
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return AdtyGroupProbeResult{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(AdtyGroupProbeHeader, AdtyGroupProbeIdentity)
	if protocol == adtyTranslationProtocolAnthropic {
		req.Header.Set("anthropic-version", adtyTranslationAnthropicVer)
		if token != "" {
			req.Header.Set("x-api-key", token)
		}
	} else if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := adtyTranslationHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return AdtyGroupProbeResult{Err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return AdtyGroupProbeResult{StatusCode: resp.StatusCode}
	}
	var parsedErr struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &parsedErr)
	return AdtyGroupProbeResult{
		StatusCode: resp.StatusCode,
		ErrorCode:  parsedErr.Error.Code,
		ErrorType:  parsedErr.Error.Type,
		Err:        fmt.Errorf("probe HTTP %d", resp.StatusCode),
	}
}

// AdtyGroupHealthConfig is what Root tunes for the API key group picker. The
// defaults match listAdtyGroupHealth when nothing has been saved yet.
type AdtyGroupHealthConfig struct {
	SuccessMin   string `json:"success_min"`
	StaleSeconds int    `json:"stale_seconds"`
	HeartbeatAt  int64  `json:"heartbeat_at"`
}

func GetAdtyGroupHealthConfig() (AdtyGroupHealthConfig, error) {
	var state model.AdtyGroupHealthState
	if err := model.DB.Where("id = ?", 1).Limit(1).Find(&state).Error; err != nil {
		return AdtyGroupHealthConfig{}, err
	}
	view := AdtyGroupHealthConfig{SuccessMin: state.SuccessMin, StaleSeconds: state.StaleSeconds, HeartbeatAt: state.HeartbeatAt}
	if view.SuccessMin == "" {
		view.SuccessMin = "0.95"
	}
	if view.StaleSeconds == 0 {
		view.StaleSeconds = 6 * 3600
	}
	return view, nil
}

func PutAdtyGroupHealthConfig(successMin string, staleSeconds int, audits ...model.AuditLog) error {
	value, err := decimal.NewFromString(successMin)
	if err != nil || value.IsNegative() || value.GreaterThan(decimal.NewFromInt(1)) || staleSeconds < 60 {
		return ErrAdtyTranslationInvalid
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var state model.AdtyGroupHealthState
		found := model.AdtyLockForUpdate(tx).First(&state, 1)
		before := map[string]any{"success_min": state.SuccessMin, "stale_seconds": state.StaleSeconds}
		state.Id = 1
		state.SuccessMin = successMin
		state.StaleSeconds = staleSeconds
		if found.Error == gorm.ErrRecordNotFound {
			if err := tx.Create(&state).Error; err != nil {
				return err
			}
		} else if found.Error != nil {
			return found.Error
		} else if err := tx.Model(&state).Updates(map[string]any{"success_min": successMin, "stale_seconds": staleSeconds}).Error; err != nil {
			return err
		}
		for _, event := range audits {
			event.BeforeValue = common.GetJsonString(before)
			event.AfterValue = common.GetJsonString(map[string]any{"success_min": successMin, "stale_seconds": staleSeconds})
			if err := model.AppendAuditIntentTx(tx, event); err != nil {
				return err
			}
		}
		return nil
	})
}

func ListAdtyGroupHealthForUser(userId int, userGroup, modelName string, allowed []string, now time.Time) ([]AdtyGroupHealthView, error) {
	var user model.User
	if err := model.DB.Select("id", "site_id").First(&user, userId).Error; err != nil {
		return nil, err
	}
	rows, err := listAdtyGroupHealth(user.SiteId, modelName, now)
	if err != nil {
		return nil, err
	}
	allow := map[string]struct{}{}
	for _, group := range allowed {
		allow[group] = struct{}{}
	}
	if userGroup != "" {
		allow[userGroup] = struct{}{}
	}
	filtered := make([]AdtyGroupHealthView, 0, len(allow))
	seen := map[string]struct{}{}
	scope := "platform"
	if user.SiteId > 0 {
		scope = "site"
	}
	for _, row := range rows {
		if _, ok := allow[row.Group]; !ok {
			continue
		}
		seen[row.Group] = struct{}{}
		filtered = append(filtered, row)
	}
	for group := range allow {
		if _, ok := seen[group]; ok || group == "" || group == "auto" {
			continue
		}
		filtered = append(filtered, AdtyGroupHealthView{
			Group: group, Status: model.AdtyGroupHealthNoData, Scope: scope, Series: adtyHealthSeries(now, nil),
		})
	}
	return filtered, nil
}

func listAdtyGroupHealth(siteId int, modelName string, now time.Time) ([]AdtyGroupHealthView, error) {
	var state model.AdtyGroupHealthState
	_ = model.DB.First(&state, 1).Error
	if state.SuccessMin == "" {
		state.SuccessMin = "0.95"
	}
	if state.StaleSeconds == 0 {
		state.StaleSeconds = 6 * 3600
	}
	minRate, _ := decimal.NewFromString(state.SuccessMin)
	since := now.Add(-24 * time.Hour).Unix()
	query := model.DB.Where("site_id = ? AND created_at >= ?", siteId, since)
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	var samples []model.AdtyGroupHealthSample
	if err := query.Order("created_at ASC").Find(&samples).Error; err != nil {
		return nil, err
	}
	type tally struct {
		success int
		fail    int
		refuse  int
		probeOK int
		probeNo int
		updated int64
		series  map[int64]*AdtyGroupHealthPoint
	}
	grouped := map[string]*tally{}
	for _, sample := range samples {
		row := grouped[sample.GroupName]
		if row == nil {
			row = &tally{series: map[int64]*AdtyGroupHealthPoint{}}
			grouped[sample.GroupName] = row
		}
		if sample.Outcome == model.AdtyGroupHealthCustomer {
			row.refuse++
			continue
		}
		if sample.CreatedAt > row.updated {
			row.updated = sample.CreatedAt
		}
		if sample.Kind == model.AdtyGroupHealthProbe {
			if sample.Outcome == model.AdtyGroupHealthSuccess {
				row.probeOK++
			} else if sample.Outcome == model.AdtyGroupHealthUpstream {
				row.probeNo++
			}
			continue
		}
		window := sample.CreatedAt - (sample.CreatedAt % (15 * 60))
		point := row.series[window]
		if point == nil {
			point = &AdtyGroupHealthPoint{Ts: window}
			row.series[window] = point
		}
		if sample.Outcome == model.AdtyGroupHealthSuccess {
			row.success++
			point.Success++
		} else {
			row.fail++
			point.Fail++
		}
	}
	interrupted := state.HeartbeatAt > 0 && now.Unix()-state.HeartbeatAt > int64(state.StaleSeconds)
	scope := "platform"
	if siteId > 0 {
		scope = "site"
	}
	out := make([]AdtyGroupHealthView, 0, len(grouped))
	for group, row := range grouped {
		view := AdtyGroupHealthView{Group: group, Status: model.AdtyGroupHealthNoData, Scope: scope, UpdatedAt: row.updated, Series: adtyHealthSeries(now, row.series)}
		requestTotal := row.success + row.fail
		view.RequestSamples = requestTotal
		view.ProbeSamples = row.probeOK + row.probeNo
		if interrupted {
			view.Status = model.AdtyGroupHealthInterrupted
			out = append(out, view)
			continue
		}
		if view.ProbeSamples > 0 {
			rate := float64(row.probeOK) / float64(view.ProbeSamples)
			view.ProbeRate = &rate
		}
		if HealthSampleStatus(view.RequestSamples+view.ProbeSamples, row.updated, now.Unix(), int64(state.StaleSeconds)) == "stale" {
			if requestTotal > 0 {
				rate := float64(row.success) / float64(requestTotal)
				view.RequestSuccessRate = &rate
			}
			view.Status = model.AdtyGroupHealthStale
			out = append(out, view)
			continue
		}
		if requestTotal == 0 {
			if view.ProbeRate != nil {
				if decimal.NewFromFloat(*view.ProbeRate).GreaterThanOrEqual(minRate) {
					view.Status = model.AdtyGroupHealthOK
				} else {
					view.Status = model.AdtyGroupHealthDegraded
					_ = openAdtyGroupHealthAlert(siteId, group, "degraded")
				}
			} else {
				view.Status = model.AdtyGroupHealthNoData
			}
		} else {
			rate := float64(row.success) / float64(requestTotal)
			view.RequestSuccessRate = &rate
			if decimal.NewFromFloat(rate).GreaterThanOrEqual(minRate) {
				view.Status = model.AdtyGroupHealthOK
			} else {
				view.Status = model.AdtyGroupHealthDegraded
				_ = openAdtyGroupHealthAlert(siteId, group, "degraded")
			}
		}
		if view.Status == model.AdtyGroupHealthOK {
			_ = closeAdtyGroupHealthAlert(siteId, group)
		}
		out = append(out, view)
	}
	return out, nil
}

func adtyHealthSeries(now time.Time, points map[int64]*AdtyGroupHealthPoint) []AdtyGroupHealthPoint {
	starts := AdtyHealthWindowStarts(now)
	out := make([]AdtyGroupHealthPoint, len(starts))
	for i, ts := range starts {
		if point := points[ts]; point != nil {
			out[i] = *point
			continue
		}
		out[i] = AdtyGroupHealthPoint{Ts: ts}
	}
	return out
}

func openAdtyGroupHealthAlert(siteId int, group, kind string) error {
	var count int64
	if err := model.DB.Model(&model.AdtyGroupHealthAlert{}).Where("site_id = ? AND group_name = ? AND open = ?", siteId, group, true).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return model.DB.Create(&model.AdtyGroupHealthAlert{SiteId: siteId, GroupName: group, Kind: kind, Open: true}).Error
}

func closeAdtyGroupHealthAlert(siteId int, group string) error {
	return model.DB.Model(&model.AdtyGroupHealthAlert{}).Where("site_id = ? AND group_name = ? AND open = ?", siteId, group, true).Update("open", false).Error
}
