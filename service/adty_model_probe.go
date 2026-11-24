package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/console_setting"

	"golang.org/x/sync/errgroup"
)

const (
	adtyModelProbeConcurrency         = 4
	adtyModelProbeRetentionSeconds    = 25 * 3600
	adtyModelProbeStartupDelay        = 20 * time.Second
	adtyModelProbeSchedulerTick       = 1 * time.Minute
	adtyModelProbeScheduledRunTimeout = 5 * time.Minute
	adtyModelProbeMaxErrorBodyBytes   = 64 << 10
)

var (
	adtyModelProbeOnce        sync.Once
	adtyModelProbeRunning     atomic.Bool
	adtyModelProbeLastRunUnix atomic.Int64

	// AdtyModelProbeDispatchFunc performs one lightweight connectivity probe against
	// an upstream channel using independent probe identity (zero customer billing).
	AdtyModelProbeDispatchFunc = dispatchAdtyModelProbeCtx
)

func init() {
	adtyMasterTasks = append(adtyMasterTasks, StartAdtyModelProbes)
}

// AdtyModelProbeRunSummary summarizes a completed probe batch.
type AdtyModelProbeRunSummary struct {
	Probed    int   `json:"probed"`
	Up        int   `json:"up"`
	Down      int   `json:"down"`
	CheckedAt int64 `json:"checked_at"`
}

func isAdtyEmbeddingModel(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	return strings.Contains(lower, "embedding") || strings.Contains(lower, "embed-") || strings.HasPrefix(lower, "embed")
}

func isAdtyNonChatModel(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	for _, kw := range []string{
		"dall-e", "gpt-image", "imagen", "flux", "ideogram", "midjourney", "mj_", "mj-",
		"stable-diffusion", "sdxl", "kolors", "recraft", "seedream",
		"whisper", "tts", "audio", "speech", "transcribe", "suno",
		"rerank", "moderation", "sora", "kling", "vidu", "veo", "runway", "luma", "hailuo", "jimeng", "video",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isAdtyModelMissingMessage(errCode, errType, errMsg string) bool {
	combined := strings.ToLower(strings.TrimSpace(errCode + " " + errType + " " + errMsg))
	for _, needle := range []string{
		"model_not_found",
		"does not exist",
		"not found",
		"unknown model",
		"invalid model",
		"no such model",
		"no available channel",
		"model is not supported",
	} {
		if strings.Contains(combined, needle) {
			return true
		}
	}
	return false
}

func isAdtyMaxTokensParamMismatch(errMsg string) bool {
	lower := strings.ToLower(strings.TrimSpace(errMsg))
	return strings.Contains(lower, "max_completion_tokens") ||
		(strings.Contains(lower, "max_tokens") && strings.Contains(lower, "unsupported"))
}

func deriveAdtyModelProbeTarget(channel *model.Channel, upstreamModel, protocol, endpoint string) (probeProtocol, probeEndpoint string, payload map[string]any) {
	embedding := isAdtyEmbeddingModel(upstreamModel)

	if channel != nil && channel.Type == constant.ChannelTypeGemini {
		parsed, err := url.Parse(endpoint)
		if err == nil && parsed.Host != "" {
			basePath := strings.TrimSuffix(parsed.Path, "/")
			basePath = strings.TrimSuffix(basePath, "/chat/completions")
			basePath = strings.TrimSuffix(basePath, "/v1")
			basePath = strings.TrimSuffix(basePath, "/v1beta")
			action := ":generateContent"
			if embedding {
				action = ":embedContent"
				payload = map[string]any{
					"content": map[string]any{
						"parts": []map[string]string{{"text": "hi"}},
					},
				}
			} else {
				payload = map[string]any{
					"contents": []map[string]any{
						{
							"role":  "user",
							"parts": []map[string]string{{"text": "hi"}},
						},
					},
					"generationConfig": map[string]any{
						"maxOutputTokens": 1,
					},
				}
			}
			parsed.Path = basePath + "/v1beta/models/" + url.PathEscape(upstreamModel) + action
			parsed.RawQuery = ""
			return "gemini", parsed.String(), payload
		}
	}

	if protocol == adtyTranslationProtocolAnthropic {
		return adtyTranslationProtocolAnthropic, endpoint, map[string]any{
			"model":      upstreamModel,
			"max_tokens": 1,
			"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		}
	}

	if embedding {
		embEndpoint := endpoint
		if strings.HasSuffix(embEndpoint, "/chat/completions") {
			embEndpoint = strings.TrimSuffix(embEndpoint, "/chat/completions") + "/embeddings"
		}
		return adtyTranslationProtocolOpenAI, embEndpoint, map[string]any{
			"model": upstreamModel,
			"input": "hi",
		}
	}

	return adtyTranslationProtocolOpenAI, endpoint, map[string]any{
		"model":      upstreamModel,
		"max_tokens": 1,
		"stream":     false,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}
}

func formatAdtyProbeHTTPMessage(statusCode int, errCode, errType string) string {
	detail := strings.TrimSpace(errCode)
	if detail == "" {
		detail = strings.TrimSpace(errType)
	}
	if detail != "" {
		if len(detail) > 80 {
			detail = detail[:80]
		}
		return fmt.Sprintf("HTTP %d: %s", statusCode, detail)
	}
	return fmt.Sprintf("HTTP %d", statusCode)
}

func dispatchAdtyModelProbe(channel *model.Channel, upstreamModel, protocol, endpoint, key string, timeout time.Duration) (status int, pingMs float64, message string) {
	return dispatchAdtyModelProbeCtx(context.Background(), channel, upstreamModel, protocol, endpoint, key, timeout)
}

func dispatchAdtyModelProbeCtx(ctx context.Context, channel *model.Channel, upstreamModel, protocol, endpoint, key string, timeout time.Duration) (status int, pingMs float64, message string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = time.Duration(console_setting.DefaultModelProbeTimeoutSeconds) * time.Second
	}

	probeProtocol, probeEndpoint, payload := deriveAdtyModelProbeTarget(channel, upstreamModel, protocol, endpoint)
	body, err := common.Marshal(payload)
	if err != nil {
		return model.AdtyModelProbeStatusDown, 0, "failed to encode probe payload"
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, probeEndpoint, bytes.NewReader(body))
	if err != nil {
		return model.AdtyModelProbeStatusDown, 0, "failed to build probe request"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(AdtyGroupProbeHeader, AdtyGroupProbeIdentity)
	switch probeProtocol {
	case adtyTranslationProtocolAnthropic:
		req.Header.Set("anthropic-version", adtyTranslationAnthropicVer)
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
	case "gemini":
		if key != "" {
			req.Header.Set("x-goog-api-key", key)
		}
	default:
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}

	start := time.Now()
	resp, err := adtyTranslationHTTPClient(timeout).Do(req)
	elapsedMs := float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return model.AdtyModelProbeStatusDown, elapsedMs, "timeout"
		}
		sanitized := common.SanitizeOutboundError(err, key)
		msg := "network error"
		if sanitized != nil && strings.TrimSpace(sanitized.Error()) != "" {
			msg = strings.TrimSpace(sanitized.Error())
		}
		return model.AdtyModelProbeStatusDown, elapsedMs, msg
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, adtyModelProbeMaxErrorBodyBytes))
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return model.AdtyModelProbeStatusUp, elapsedMs, ""
	}

	var parsedErr struct {
		Error struct {
			Code    any    `json:"code"`
			Type    string `json:"type"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = common.Unmarshal(raw, &parsedErr)

	errCode := strings.TrimSpace(common.Interface2String(parsedErr.Error.Code))
	errType := strings.TrimSpace(parsedErr.Error.Type)
	if errType == "" {
		errType = strings.TrimSpace(parsedErr.Error.Status)
	}
	errMsg := strings.TrimSpace(parsedErr.Error.Message)

	probeRes := AdtyGroupProbeResult{
		StatusCode: resp.StatusCode,
		ErrorCode:  errCode,
		ErrorType:  errType,
		Err:        fmt.Errorf("probe HTTP %d", resp.StatusCode),
	}
	outcome, _ := ClassifyAdtyGroupProbeOutcome(probeRes)
	switch outcome {
	case model.AdtyGroupHealthSuccess:
		return model.AdtyModelProbeStatusUp, elapsedMs, ""
	case model.AdtyGroupHealthCustomer:
		if isAdtyModelMissingMessage(errCode, errType, errMsg) {
			return model.AdtyModelProbeStatusDown, elapsedMs, fmt.Sprintf("HTTP %d: model not found", resp.StatusCode)
		}
		if isAdtyNonChatModel(upstreamModel) || isAdtyMaxTokensParamMismatch(errMsg) {
			return model.AdtyModelProbeStatusUp, elapsedMs, ""
		}
		return model.AdtyModelProbeStatusDown, elapsedMs, formatAdtyProbeHTTPMessage(resp.StatusCode, errCode, errType)
	default:
		return model.AdtyModelProbeStatusDown, elapsedMs, formatAdtyProbeHTTPMessage(resp.StatusCode, errCode, errType)
	}
}

func effectiveProbeGroupKey(g console_setting.UptimeKumaGroup) string {
	if id := strings.TrimSpace(g.GroupID); id != "" {
		return id
	}
	return strings.TrimSpace(g.CategoryName)
}

func hasConfiguredAdtyModelProbes() bool {
	settings := console_setting.GetConsoleSetting()
	if !settings.UptimeKumaEnabled {
		return false
	}
	for _, g := range console_setting.ParseUptimeKumaGroups(settings.UptimeKumaGroups) {
		if !g.IsEnabled() || len(g.Models) == 0 {
			continue
		}
		if effectiveProbeGroupKey(g) != "" {
			return true
		}
	}
	return false
}

// RunAdtyModelProbesOnce executes lightweight connectivity probes for all enabled
// configured (group, model) targets (optionally filtered to one filterGroupID),
// persists rows into adty_model_probes, and prunes records older than 25 hours.
func RunAdtyModelProbesOnce(ctx context.Context, filterGroupID string) (AdtyModelProbeRunSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().Unix()
	summary := AdtyModelProbeRunSummary{CheckedAt: now}

	if err := ValidateAdtyGroupProbeIdentity(AdtyGroupProbeIdentity, AdtyGroupProbeUserID, ""); err != nil {
		return summary, err
	}
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyModelProbe{}) {
		return summary, nil
	}

	settings := console_setting.GetConsoleSetting()
	groups := console_setting.ParseUptimeKumaGroups(settings.UptimeKumaGroups)
	timeout := time.Duration(console_setting.GetModelProbeTimeoutSeconds()) * time.Second
	filterGroupID = strings.TrimSpace(filterGroupID)

	type probeTarget struct {
		groupID   string
		modelName string
	}
	targets := make([]probeTarget, 0)
	seen := make(map[probeTarget]struct{})
	for _, g := range groups {
		if !g.IsEnabled() || len(g.Models) == 0 {
			continue
		}
		groupKey := effectiveProbeGroupKey(g)
		if groupKey == "" {
			continue
		}
		if filterGroupID != "" && groupKey != filterGroupID {
			continue
		}
		for _, m := range g.Models {
			modelName := strings.TrimSpace(m)
			if modelName == "" {
				continue
			}
			t := probeTarget{groupID: groupKey, modelName: modelName}
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			targets = append(targets, t)
		}
	}

	if len(targets) == 0 {
		_ = model.PruneAdtyModelProbesBefore(now - adtyModelProbeRetentionSeconds)
		if filterGroupID == "" {
			adtyModelProbeLastRunUnix.Store(now)
		}
		return summary, nil
	}

	var (
		mu     sync.Mutex
		eg     errgroup.Group
		probed int
		up     int
		down   int
	)
	eg.SetLimit(adtyModelProbeConcurrency)

	for _, target := range targets {
		eg.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			var (
				status  int
				pingMs  float64
				message string
			)
			channel, upstreamModel, protocol, endpoint, key, err := resolveAdtyGroupProbeChannel(target.groupID, target.modelName)
			if err != nil {
				status = model.AdtyModelProbeStatusDown
				pingMs = 0
				if errors.Is(err, ErrAdtyTranslationSelf) {
					message = "upstream endpoint blocked"
				} else {
					message = "no enabled channel for group/model"
				}
			} else if idErr := ValidateAdtyGroupProbeIdentity(AdtyGroupProbeIdentity, AdtyGroupProbeUserID, key); idErr != nil {
				status = model.AdtyModelProbeStatusDown
				pingMs = 0
				message = "probe identity rejected"
			} else {
				status, pingMs, message = AdtyModelProbeDispatchFunc(ctx, channel, upstreamModel, protocol, endpoint, key, timeout)
			}

			checkedAt := time.Now().Unix()
			row := &model.AdtyModelProbe{
				GroupId:   target.groupID,
				ModelName: target.modelName,
				Status:    status,
				PingMs:    math.Round(pingMs*100) / 100,
				Message:   message,
				CheckedAt: checkedAt,
			}
			if insertErr := model.InsertAdtyModelProbe(row); insertErr != nil {
				common.SysError(common.LogText("adty model probe insert (%s/%s): %s", target.groupID, target.modelName, insertErr.Error()))
			}

			mu.Lock()
			probed++
			if status == model.AdtyModelProbeStatusUp {
				up++
			} else {
				down++
			}
			mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()

	finishedAt := time.Now().Unix()
	_ = model.PruneAdtyModelProbesBefore(finishedAt - adtyModelProbeRetentionSeconds)
	if filterGroupID == "" {
		adtyModelProbeLastRunUnix.Store(finishedAt)
	}

	summary.Probed = probed
	summary.Up = up
	summary.Down = down
	summary.CheckedAt = finishedAt
	return summary, nil
}

func runScheduledAdtyModelProbes() {
	if !adtyModelProbeRunning.CompareAndSwap(false, true) {
		return
	}
	defer adtyModelProbeRunning.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), adtyModelProbeScheduledRunTimeout)
	defer cancel()
	if _, err := RunAdtyModelProbesOnce(ctx, ""); err != nil {
		common.SysError(common.LogText("adty model probe scheduled run failed: %s", err.Error()))
	}
}

// StartAdtyModelProbes starts the master-node periodic model connectivity probe loop.
func StartAdtyModelProbes() {
	if !common.IsMasterNode {
		return
	}
	adtyModelProbeOnce.Do(func() {
		go func() {
			startupTimer := time.NewTimer(adtyModelProbeStartupDelay)
			<-startupTimer.C

			if hasConfiguredAdtyModelProbes() {
				intervalSec := int64(console_setting.GetModelProbeIntervalMinutes()) * 60
				latest, err := model.LatestAdtyModelProbeCheckedAt()
				if err == nil && latest > 0 && time.Now().Unix()-latest < intervalSec {
					adtyModelProbeLastRunUnix.Store(latest)
				} else {
					runScheduledAdtyModelProbes()
				}
			}

			ticker := time.NewTicker(adtyModelProbeSchedulerTick)
			defer ticker.Stop()
			for range ticker.C {
				if !hasConfiguredAdtyModelProbes() {
					continue
				}
				intervalSec := int64(console_setting.GetModelProbeIntervalMinutes()) * 60
				lastRun := adtyModelProbeLastRunUnix.Load()
				if lastRun == 0 || time.Now().Unix()-lastRun >= intervalSec {
					runScheduledAdtyModelProbes()
				}
			}
		}()
	})
}
