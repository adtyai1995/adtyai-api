package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func relayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		err = relay.ImageHelper(c, info)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = relay.AudioHelper(c, info)
	case relayconstant.RelayModeRerank:
		err = relay.RerankHelper(c, info)
	case relayconstant.RelayModeEmbeddings:
		err = relay.EmbeddingHelper(c, info)
	case relayconstant.RelayModeResponses, relayconstant.RelayModeResponsesCompact:
		err = relay.ResponsesHelper(c, info)
	case relayconstant.RelayModeAlphaSearch:
		err = relay.AlphaSearchHelper(c, info)
	case relayconstant.RelayModeToolsSearch, relayconstant.RelayModeToolsFetch:
		err = relay.DeepSeekToolsHelper(c, info)
	default:
		err = relay.TextHelper(c, info)
	}
	return err
}

func geminiRelayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	if helper.IsGeminiEmbeddingPath(c.Request.URL.Path) {
		err = relay.GeminiEmbeddingHandler(c, info)
	} else {
		err = relay.GeminiHelper(c, info)
	}
	return err
}

func Relay(c *gin.Context, relayFormat types.RelayFormat) {

	requestId := c.GetString(common.RequestIdKey)
	//group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	//originalModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	var (
		newAPIError *types.NewAPIError
		ws          *websocket.Conn
	)

	if relayFormat == types.RelayFormatOpenAIRealtime {
		var err error
		ws, err = upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			helper.WssError(c, ws, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry()).ToOpenAIError())
			return
		}
		defer ws.Close()
	}

	defer func() {
		if newAPIError != nil {
			service.RecordRequestPolicyTermination(c, newAPIError)
			logger.LogError(c, common.LogText("relay error: %s", common.LocalLogPreview(newAPIError.Error())))
			newAPIError.SetMessage(common.MessageWithRequestId(newAPIError.Error(), requestId))
			upstreamURLs := common.UpstreamAddresses(c)
			if relayFormat != types.RelayFormatOpenAIRealtime && c.Writer.Written() {
				if strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
					writeRelayStreamError(c, relayFormat, newAPIError)
				}
				return
			}
			if relayFormat != types.RelayFormatOpenAIRealtime {
				c.Header("Content-Type", "application/json; charset=utf-8")
			}
			switch relayFormat {
			case types.RelayFormatOpenAIRealtime:
				helper.WssError(c, ws, newAPIError.ToOpenAIError(upstreamURLs...))
			case types.RelayFormatClaude:
				c.JSON(newAPIError.StatusCode, gin.H{
					"type":  "error",
					"error": newAPIError.ToClaudeError(upstreamURLs...),
				})
			default:
				c.JSON(newAPIError.StatusCode, gin.H{
					"error": newAPIError.ToOpenAIError(upstreamURLs...),
				})
			}
		}
	}()

	request, err := helper.GetAndValidateRequest(c, relayFormat)
	if err != nil {
		// Map "request body too large" to 413 so clients can handle it correctly
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		} else {
			newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
		}
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
		return
	}

	defer func() {
		recovered := recover()
		if recovered != nil {
			// 退款 defer 先执行，当时 newAPIError 还是 nil，预扣不会退。这里补上。
			newAPIError = noteRelayPanic(c, relayInfo, newAPIError, recovered)
			panic(recovered)
		}
	}()

	if newAPIError = relay.PrepareRequestBilling(c, relayInfo); newAPIError != nil {
		return
	}
	defer func() {
		newAPIError = relay.RefundFailedRequestBilling(c, relayInfo, newAPIError)
	}()

	generalSettings := operation_setting.GetGeneralSetting()
	// 二开：所有流式格式在预扣准入通过后再开始保活，避免余额不足等准入失败被 SSE 200 头劫持；
	// 同时推理模型首字慢时 Cloudflare 不会 524 断开（任务 7.5）。Realtime 走 WebSocket，不发 SSE 注释行。
	// 此 defer 晚于第 94 行错误响应 defer 注册（LIFO 先执行），保证所有保活协程彻底退出后再写最终响应头/响应体。
	var stopKeepAlives []func()
	stopAllKeepAlive := func() {
		for i := len(stopKeepAlives) - 1; i >= 0; i-- {
			if stopKeepAlives[i] != nil {
				stopKeepAlives[i]()
			}
		}
		stopKeepAlives = nil
		helper.StopStreamKeepAlive(c)
	}
	// Binary audio/TTS channels (such as volcengine RelayModeAudioSpeech) and
	// image endpoints (types.RelayFormatOpenAIImage, which may return JSON or
	// manage their own SSE stream in relay_image.go) must not get pre-written
	// SSE headers or ": PING\n\n" keepalives before upstream responds.
	keepAliveEnabled := relayFormat != types.RelayFormatOpenAIRealtime &&
		relayFormat != types.RelayFormatOpenAIImage &&
		relayInfo.RelayMode != relayconstant.RelayModeAudioSpeech &&
		relayInfo.RelayMode != relayconstant.RelayModeImagesGenerations &&
		relayInfo.RelayMode != relayconstant.RelayModeImagesEdits &&
		relayInfo.IsStream &&
		generalSettings.PingIntervalEnabled
	keepAliveInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
	if relayFormat != types.RelayFormatOpenAIRealtime && relayInfo.IsStream {
		defer stopAllKeepAlive()
	}
	if keepAliveEnabled {
		if stop := helper.StartStreamKeepAlive(c, keepAliveInterval); stop != nil {
			stopKeepAlives = append(stopKeepAlives, stop)
		}
	}

	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayInfo.TokenGroup,
		ModelName:   relayInfo.OriginModelName,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}
	relayInfo.RetryIndex = 0
	relayInfo.LastError = nil

	retryTimes := common.GetRetryTimes()
	for attempt := 0; retryParam.GetRetry() <= retryTimes; retryParam.IncreaseRetry() {
		if attempt > 0 && keepAliveEnabled {
			if stop := helper.StartStreamKeepAlive(c, keepAliveInterval); stop != nil {
				stopKeepAlives = append(stopKeepAlives, stop)
			}
		}
		attempt++
		relayInfo.StreamStatus = nil
		relayInfo.PerformanceBusinessRejection = false
		relayInfo.PerformanceOutputTokens = 0
		relayInfo.RetryIndex = retryParam.GetRetry()
		channel, channelErr := getChannel(c, relayInfo, retryParam)
		if channelErr != nil {
			logger.LogError(c, channelErr.Error())
			newAPIError = channelErr
			break
		}
		service.AppendUsedChannel(c, channel.Id)
		if billingErr := service.PrepareTieredBillingForSelectedGroup(c, relayInfo); billingErr != nil {
			newAPIError = billingErr
			break
		}

		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			// Ensure consistent 413 for oversized bodies even when error occurs later (e.g., retry path)
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
			} else {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		switch relayFormat {
		case types.RelayFormatOpenAIRealtime:
			newAPIError = relay.WssHelper(c, relayInfo)
		case types.RelayFormatClaude:
			newAPIError = relay.ClaudeHelper(c, relayInfo)
		case types.RelayFormatGemini:
			newAPIError = geminiRelayHandler(c, relayInfo)
		default:
			newAPIError = relayHandler(c, relayInfo)
		}

		if newAPIError == nil {
			service.MarkRequestPolicySuccess(c, relayInfo.StreamStatus)
			relayInfo.LastError = nil
			return
		}

		newAPIError = service.NormalizeViolationFeeError(newAPIError)
		relayInfo.LastError = newAPIError

		// Only block retry when business payload frames were written to the client;
		// keepalive ": PING" comments set c.Writer.Written() without delivering output.
		var decision service.PolicyDecision
		if relayInfo.SendResponseCount > 0 || helper.HasWrittenStreamPayload(c) {
			decision = service.PolicyDecision{Action: "stop", Reason: "response_already_written", Source: "system"}
		} else {
			decision = service.DecideRelayRetry(c, newAPIError, retryTimes-retryParam.GetRetry())
		}
		service.RecordPolicyFailure(c, channel.Id, newAPIError, decision)
		processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError, relayInfo)

		if decision.Action != "retry" {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := common.LogText("retry: %s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}
}

// noteRelayPanic 把 panic 记成请求失败。退款 defer 已经跑过且当时没有错误时，这里补退预扣。
func noteRelayPanic(c *gin.Context, relayInfo *relaycommon.RelayInfo, existing *types.NewAPIError, recovered any) *types.NewAPIError {
	panicErr := types.NewError(fmt.Errorf("relay panic: %v", recovered), types.ErrorCodeBadResponse)
	if existing != nil || relayInfo == nil {
		return panicErr
	}
	return relay.RefundFailedRequestBilling(c, relayInfo, panicErr)
}

func writeRelayStreamError(c *gin.Context, format types.RelayFormat, apiError *types.NewAPIError) {
	// 心跳提交响应头后，最终错误继续通过同一个流返回。
	status, _ := common.GetContextKeyType[*relaycommon.StreamStatus](c, constant.ContextKeyResponseStreamStatus)
	if status == nil {
		status = relaycommon.NewStreamStatus()
		common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
	}
	status.MarkFailed(string(apiError.GetErrorCode()), string(apiError.GetErrorType()), apiError.StatusCode)
	upstreamURLs := common.UpstreamAddresses(c)
	if format == types.RelayFormatClaude {
		_ = helper.ClaudeData(c, dto.ClaudeResponse{Type: "error", Error: apiError.ToClaudeError(upstreamURLs...)})
		return
	}
	_ = helper.ObjectData(c, gin.H{"error": apiError.ToOpenAIError(upstreamURLs...)})
	helper.Done(c)
}

// CountClaudeTokens implements Anthropic's token-counting utility endpoint.
// It deliberately skips upstream generation and billing; callers use this
// endpoint to size prompts before creating a Message.
func CountClaudeTokens(c *gin.Context) {
	request, err := helper.GetAndValidateClaudeRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": common.MessageWithRequestId(err.Error(), c.GetString(common.RequestIdKey)),
			},
		})
		return
	}

	info := relaycommon.GenRelayInfoClaude(c, request)
	inputTokens, err := service.CountRequestToken(c, request.GetTokenCountMeta(), info)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": common.MessageWithRequestId(err.Error(), c.GetString(common.RequestIdKey)),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"input_tokens": inputTokens})
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime", "responses"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func getChannel(c *gin.Context, info *relaycommon.RelayInfo, retryParam *service.RetryParam) (*model.Channel, *types.NewAPIError) {
	if info.ChannelMeta == nil {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		channel := &model.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}
		service.RequestPolicy(c).BeginAttempt(channel, info.UsingGroup)
		return channel, nil
	}
	channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(retryParam)
	if err != nil {
		return nil, types.NewError(errors.New(i18n.T(c, i18n.MsgRelayRetryGetChannelFailed, map[string]any{"Group": selectGroup, "Model": info.OriginModelName, "Error": err.Error()})), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if channel == nil {
		return nil, types.NewError(errors.New(i18n.T(c, i18n.MsgRelayRetryNoAvailableChannel, map[string]any{"Group": selectGroup, "Model": info.OriginModelName})), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}

	prevGroup := info.UsingGroup
	info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)
	if service.AdtyTeamPriceBlocked(c) {
		return nil, types.NewErrorWithStatusCode(service.ErrAdtyTeamInvalid, types.ErrorCodeModelPriceError, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	if info.UsingGroup != prevGroup {
		if err := helper.RefreshPriceDataForSelectedGroup(c, info); err != nil {
			return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
	}

	service.RequestPolicy(c).BeginAttempt(channel, selectGroup)
	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName)
	if newAPIError != nil {
		return nil, newAPIError
	}
	return channel, nil
}

func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	service.ProcessChannelError(c, channelError, err, relayInfo)
}

func RelayMidjourney(c *gin.Context) {
	policy := service.RequestPolicy(c)
	defer func() {
		if policy.Attempts > 0 && !policy.Successful {
			service.RecordRequestPolicyTermination(c, types.NewErrorWithStatusCode(errors.New("Midjourney submission failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway, types.ErrOptionWithSkipRetry()))
		}
	}()
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatMjProxy, nil, nil)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"description": fmt.Sprintf("failed to generate relay info: %s", err.Error()),
			"type":        "upstream_error",
			"code":        4,
		})
		return
	}

	var mjErr *taskdto.MidjourneyResponse
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		mjErr = relay.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		mjErr = relay.RelayMidjourneyTask(c, relayInfo.RelayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		mjErr = relay.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		mjErr = relay.RelaySwapFace(c, relayInfo)
	default:
		mjErr = relay.RelayMidjourneySubmit(c, relayInfo)
	}
	//err = relayMidjourneySubmit(c, relayMode)
	log.Println(mjErr)
	if mjErr != nil {
		policy.Successful = false
		statusCode := http.StatusBadRequest
		if mjErr.Code == 30 {
			mjErr.Result = i18n.T(c, i18n.MsgRelayGroupSaturated)
			statusCode = http.StatusTooManyRequests
		}
		channelId := c.GetInt("channel_id")
		logger.LogError(c, common.LogText("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result)))
		if c.Writer.Written() {
			return
		}
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result),
			"type":        "upstream_error",
			"code":        mjErr.Code,
		})
	}
}

func RelayNotImplemented(c *gin.Context) {
	err := types.OpenAIError{
		Message: "API not implemented",
		Type:    "adtyai_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	// The web fallback may already have applied static-asset cache headers.
	// A missing API or asset can appear after an upgrade; never cache its 404.
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	err := types.OpenAIError{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

// RelayTaskPluginEndpoint keeps unclaimed shared-endpoint traffic on its
// existing handler while claimed requests enter the generation-pinned
// host-owned protocol bridge.
func RelayTaskPluginEndpoint(c *gin.Context, fallback gin.HandlerFunc) {
	pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
	if !exists {
		fallback(c)
		return
	}
	pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint)
	if !ok || pinned.Plugin == nil || pinned.Generation == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Task protocol request failed",
				"type":    "adtyai_error",
				"code":    "task_protocol_error",
			},
		})
		return
	}
	switch pinned.Protocol {
	case "openai_responses":
		serveTaskPluginProtocol(c, pinned, defaultPluginProtocolBridgeDeps())
	case pluginruntime.ProtocolOpenAIImage:
		serveTaskPluginImageProtocol(c, pinned, defaultPluginProtocolBridgeDeps())
	default:
		fallback(c)
	}
}

func RelayTaskFetch(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if taskErr := relay.RelayTaskFetch(c, relayInfo.RelayMode); taskErr != nil {
		respondTaskError(c, taskErr)
	}
}

type taskSubmissionOutcome struct {
	Result    *relay.TaskSubmitResult
	Task      *model.Task
	RelayInfo *relaycommon.RelayInfo
}

func RelayTask(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		respondTaskSubmissionError(c, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if action := c.GetString("task_action"); action != "" {
		relayInfo.Action = action
	}

	if taskErr := relay.ResolveOriginTask(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	if taskErr := relay.ApplyOriginTaskAffinity(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}

	outcome, taskErr := executeTaskSubmission(c, relayInfo)
	if taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	presentTaskSubmission(c, outcome)
}

// executeTaskSubmission owns the retry, billing, and persistence lifecycle.
// It deliberately performs no client response writes so JSON and protocol
// presenters share the same durable task barrier. Its cancellation semantics
// come from c.Request.Context: native task endpoints use the client context,
// while the Responses bridge supplies an independently bounded context.
func executeTaskSubmission(c *gin.Context, relayInfo *relaycommon.RelayInfo) (*taskSubmissionOutcome, *taskdto.TaskError) {
	return executeTaskSubmissionWith(c, relayInfo, relay.RelayTaskSubmit)
}

type taskSubmitAttempt func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *taskdto.TaskError)

func executeTaskSubmissionWith(
	c *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	submit taskSubmitAttempt,
) (*taskSubmissionOutcome, *taskdto.TaskError) {
	policy := service.RequestPolicy(c)
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	diagnostics.start(relayInfo)
	var result *relay.TaskSubmitResult
	var taskErr *taskdto.TaskError
	durable := false
	stage := "start"
	defer func() {
		// Refund only when the client has not already received vendor output.
		// A relayed event stream that has written to the client is settled
		// instead; settlement goes through SettleBilling and never overdraws.
		if !durable && relayInfo.Billing != nil && !c.Writer.Written() {
			diagnostics.refund(stage)
			relayInfo.Billing.Refund(c)
		}
	}()
	stage = "before_attempt"
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_attempt", 0)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayInfo.TokenGroup,
		ModelName:   relayInfo.OriginModelName,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}

	retryTimes := common.GetRetryTimes()
	for ; retryParam.GetRetry() <= retryTimes; retryParam.IncreaseRetry() {
		stage = "select_channel"
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("before_attempt", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}
		var channel *model.Channel

		if lockedCh, ok := relayInfo.LockedChannel.(*model.Channel); ok && lockedCh != nil {
			channel = lockedCh
			policy.BeginAttempt(channel, relayInfo.UsingGroup)
			_, hasChannelSetting := c.Get(string(constant.ContextKeyChannelSetting))
			if retryParam.GetRetry() > 0 || common.GetContextKeyInt(c, constant.ContextKeyChannelId) != channel.Id || !hasChannelSetting {
				if setupErr := middleware.SetupContextForSelectedChannel(c, channel, relayInfo.OriginModelName); setupErr != nil {
					taskErr = service.TaskErrorWrapperLocal(setupErr.Err, "setup_locked_channel_failed", http.StatusInternalServerError)
					break
				}
			}
		} else {
			var channelErr *types.NewAPIError
			channel, channelErr = getChannel(c, relayInfo, retryParam)
			if channelErr != nil {
				logger.LogError(c, channelErr.Error())
				taskErr = service.TaskErrorWrapperLocal(channelErr.Err, "get_channel_failed", channelErr.StatusCode)
				break
			}
			if snapErr := service.SyncBillingGroupSnapshotWithContext(c, relayInfo); snapErr != nil {
				logger.LogError(c, snapErr.Error())
				taskErr = service.TaskErrorWrapperLocal(snapErr.Err, string(snapErr.GetErrorCode()), snapErr.StatusCode)
				break
			}
		}
		diagnostics.attempt(retryParam.GetRetry()+1, channel, relayInfo.LockedChannel != nil)

		service.AppendUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			stage = "read_body"
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusRequestEntityTooLarge)
			} else {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusBadRequest)
			}
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		stage = "submit"
		result, taskErr = submit(c, relayInfo)
		if taskErr == nil {
			// R3-011: Once upstream accepted the task, do not abort or refund even if
			// the client disconnected right after acceptance; persist and settle durably.
			diagnostics.attemptSucceeded(retryParam.GetRetry()+1, result)
			break
		}
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("after_submit", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}

		taskAPIError := taskSubmissionAPIError(taskErr)
		relayInfo.LastError = taskAPIError
		decision := decideTaskRetry(c, taskErr, retryTimes-retryParam.GetRetry())
		service.RecordPolicyFailure(c, channel.Id, taskAPIError, decision)
		if !taskErr.LocalError {
			processChannelError(c,
				*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey,
					common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()),
				taskAPIError,
				relayInfo)
		}

		willRetry := decision.Action == "retry"
		diagnostics.attemptFailed(retryParam.GetRetry()+1, channel, taskErr, willRetry)
		if !willRetry {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := common.LogText("retry: %s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}

	if taskErr != nil {
		// R4-019 / R7-005: Always persist ambiguous upstream submissions regardless of client
		// disconnect so pre-consumed funds are held for review instead of auto-refunded.
		// If persisting the review row fails, fall back to SettleBilling and mark durable
		// so the defer never auto-refunds a potentially accepted upstream job.
		if c != nil && c.GetBool("task_submit_ambiguous") {
			if !holdAmbiguousTaskSubmission(c, relayInfo, taskErr) {
				if settleErr := service.SettleBilling(c, relayInfo, relayInfo.PriceData.Quota); settleErr != nil {
					common.SysError(common.LogText("settle ambiguous task billing error after insert failure: %s", settleErr.Error()))
				}
			}
			durable = true
		}
		diagnostics.failed(stage, "task_error", taskErr, durable)
		return nil, taskErr
	}
	if result == nil {
		taskErr = service.TaskErrorWrapperLocal(errors.New("task submission returned no result"), "task_submit_failed", http.StatusInternalServerError)
		diagnostics.failed("submit", "missing_result", taskErr, false)
		return nil, taskErr
	}
	// R3-011 / R7-005: Upstream has already accepted this billable task (result != nil).
	// Never auto-refund via defer once upstream accepted. Attempt to reserve any
	// submit-time upward adjustment, persist with context.WithoutCancel, and let
	// SettleBilling apply DEC-003 (cap wallet debit at 0 and record shortfall hold).
	durable = true
	if relayInfo.Billing != nil {
		stage = "reserve"
		diagnostics.reserve("reserve_start", result.Quota)
		if reserveErr := relayInfo.Billing.Reserve(result.Quota); reserveErr != nil {
			common.SysError(common.LogText("reserve adjusted task billing error after upstream acceptance (continuing to persist and settle via DEC-003): %s", reserveErr.Error()))
		} else {
			diagnostics.reserve("reserve_complete", result.Quota)
		}
	}

	stage = "insert"
	task := model.InitTask(result.Platform, relayInfo)
	task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(c)
	task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
	task.PrivateData.BillingSource = relayInfo.BillingSource
	task.PrivateData.TokenId = relayInfo.TokenId
	task.PrivateData.NodeName = common.NodeName
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		ModelPrice:      relayInfo.PriceData.ModelPrice,
		GroupRatio:      relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		ModelRatio:      relayInfo.PriceData.ModelRatio,
		OtherRatios:     relayInfo.PriceData.OtherRatios(),
		OriginModelName: relayInfo.OriginModelName,
		PerCallBilling:  common.StringsContains(constant.TaskPricePatches, relayInfo.OriginModelName) || relayInfo.PriceData.UsePrice,
		TieredSnapshot:  relayInfo.TieredBillingSnapshot,
	}
	service.AdtyStampTaskSiteBilling(c, task.PrivateData.BillingContext) // 二开：任务 11.4、11.5
	task.Quota = result.Quota
	task.Data = result.TaskData
	if len(result.PluginState) > 0 {
		task.PrivateData.PluginState = result.PluginState
	}
	task.Action = relayInfo.Action
	if immediate := result.Immediate; immediate != nil {
		task.Status = model.TaskStatus(immediate.Status)
		task.Progress = immediate.Progress
		if immediate.Status == model.TaskStatusSuccess || immediate.Status == model.TaskStatusFailure {
			task.FinishTime = time.Now().Unix()
		}
		if immediate.Status == model.TaskStatusFailure {
			task.FailReason = immediate.Reason
		}
		if immediate.Url != "" {
			task.PrivateData.ResultURL = immediate.Url
		} else if immediate.Status == model.TaskStatusSuccess {
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
	}
	// A native submit route may declare retainResult: false. It applies only
	// to immediate terminal results: the client receives the complete response
	// once, the upstream snapshot is never persisted, and the task is not
	// retrievable afterwards. An asynchronous result on such a route keeps its
	// snapshot because polling and retrieval need it. The OpenAI Images
	// protocol delivers its images inline in the same HTTP response, so its
	// immediate results follow the same rule; an asynchronous image task is
	// expected there and is polled inside the request.
	var insertOmits []string
	immediateTerminal := result.Immediate != nil && (result.Immediate.Status == model.TaskStatusSuccess || result.Immediate.Status == model.TaskStatusFailure)
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		pinned, ok := pinnedValue.(pluginruntime.PinnedRoute)
		if ok && pinned.Route.RetainResult != nil && !*pinned.Route.RetainResult {
			if immediateTerminal {
				task.PrivateData.ResultDiscarded = true
				insertOmits = append(insertOmits, "data")
			} else {
				logger.LogWarn(c, common.LogText("task plugin route %s %s declares retainResult: false but returned an asynchronous result; retaining task %s", pinned.Route.Method, pinned.Route.Path, task.TaskID))
			}
		}
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists && immediateTerminal {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == pluginruntime.ProtocolOpenAIImage {
			task.PrivateData.ResultDiscarded = true
			insertOmits = append(insertOmits, "data")
		}
	}
	service.AttachTaskBillingReservation(relayInfo, task)
	diagnostics.insertStart(task)
	persistCtx := context.WithoutCancel(c.Request.Context())
	if insertErr := task.InsertWithContext(persistCtx, insertOmits...); insertErr != nil {
		common.SysError(common.LogText("insert task error: %s", insertErr.Error()))
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to persist task"), "task_insert_failed", http.StatusInternalServerError)
		diagnostics.failed("insert", "database_error", taskErr, durable)
		// R6-011 / R7-005: Once upstream accepted the task (result != nil), settle the
		// reservation even if task persistence fails and nothing was written to
		// the client, and always record consumption once settled (durable is already true).
		if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
			common.SysError(common.LogText("settle task billing error: %s", settleErr.Error()))
			return nil, taskErr
		}
		service.LogTaskConsumption(c, relayInfo, task)
		return nil, taskErr
	}
	stage = "settle"
	diagnostics.durable(task)
	diagnostics.settleStart(task, result.Quota)

	if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
		common.SysError(common.LogText("settle task billing error: %s", settleErr.Error()))
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to settle task billing"), "task_billing_settlement_failed", http.StatusInternalServerError)
		diagnostics.failed("settle", "billing_error", taskErr, true)
		return nil, taskErr
	}
	if task.Status != model.TaskStatusFailure {
		service.MarkRequestPolicySuccess(c, nil)
	} else {
		policy.AddEvent(service.PolicyEvent{Decision: service.PolicyDecision{Action: "stop", Reason: "task_failed", Source: "upstream"}})
	}
	service.LogTaskConsumption(c, relayInfo, task)
	diagnostics.complete(task, result.Quota)

	return &taskSubmissionOutcome{Result: result, Task: task, RelayInfo: relayInfo}, nil
}

func presentTaskSubmission(c *gin.Context, outcome *taskSubmissionOutcome) {
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	// 二开：不再用响应头把计价倍率发给调用方（任务 7.1）
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedRoute); ok && pinned.Plugin != nil && pinned.Route.Render != "" {
			view, err := service.BuildTaskPluginView(outcome.Task)
			requestValue, _ := c.Get(pluginruntime.ContextKeyRouteRequest)
			requestContext, _ := requestValue.(pluginruntime.RouteRequestContext)
			if err == nil {
				viewValue, valueErr := taskPluginProtocolJSONValue(view)
				if valueErr == nil {
					if body, callErr := pinned.Plugin.Engine.CallPath(c.Request.Context(), "native", []string{pinned.Route.Render}, requestContext.JSValue(), viewValue); callErr == nil {
						diagnostics.present(outcome.Task, "native_presenter")
						c.JSON(http.StatusOK, body)
						return
					} else {
						logger.LogError(c, common.LogText("task plugin native submit presenter failed: %s", callErr.Error()))
					}
				} else {
					logger.LogError(c, common.LogText("encode task plugin native submit view failed: %s", valueErr.Error()))
				}
			} else {
				logger.LogError(c, common.LogText("build task plugin native submit view failed: %s", err.Error()))
			}
		}
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == "openai_video" && pinned.Operation.Name == "create" {
			diagnostics.present(outcome.Task, "openai_video_create")
			c.JSON(http.StatusOK, outcome.Task.ToOpenAIVideo())
			return
		}
	}
	createdAt := outcome.Task.CreatedAt
	if createdAt == 0 {
		createdAt = outcome.Task.SubmitTime
	}
	diagnostics.present(outcome.Task, "host_fallback")
	c.JSON(http.StatusOK, map[string]any{
		"id":         outcome.Task.TaskID,
		"task_id":    outcome.Task.TaskID,
		"status":     "queued",
		"model":      outcome.RelayInfo.OriginModelName,
		"created_at": createdAt,
	})
}

func respondTaskSubmissionError(c *gin.Context, taskErr *taskdto.TaskError) {
	service.RecordRequestPolicyTermination(c, taskSubmissionAPIError(taskErr))
	newTaskPluginSubmitDiagnostics(c).presentError(taskErr)
	if middleware.RespondTaskPluginError(c, taskErr) {
		return
	}
	respondTaskError(c, taskErr)
}

func holdAmbiguousTaskSubmission(c *gin.Context, relayInfo *relaycommon.RelayInfo, taskErr *taskdto.TaskError) bool {
	if relayInfo == nil || model.DB == nil {
		return false
	}
	platform := constant.TaskPlatform(c.GetString("platform"))
	if platform == "" {
		if key := c.GetString("task_plugin_key"); key != "" {
			platform = constant.TaskPlatform(key)
		} else {
			platform = constant.TaskPlatform(strconv.Itoa(relayInfo.ChannelType))
		}
	}
	task := model.InitTask(platform, relayInfo)
	task.Status = model.TaskStatusSubmitted
	task.BillingPending = true
	reason := "ambiguous_upstream_submit"
	if taskErr != nil && taskErr.Message != "" {
		reason += ": " + taskErr.Message
	}
	task.PrivateData.BillingReviewReason = reason
	task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(c)
	task.PrivateData.BillingSource = relayInfo.BillingSource
	task.PrivateData.TokenId = relayInfo.TokenId
	task.PrivateData.NodeName = common.NodeName
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		ModelPrice:      relayInfo.PriceData.ModelPrice,
		GroupRatio:      relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		ModelRatio:      relayInfo.PriceData.ModelRatio,
		OtherRatios:     relayInfo.PriceData.OtherRatios(),
		OriginModelName: relayInfo.OriginModelName,
		PerCallBilling:  common.StringsContains(constant.TaskPricePatches, relayInfo.OriginModelName) || relayInfo.PriceData.UsePrice,
		TieredSnapshot:  relayInfo.TieredBillingSnapshot,
	}
	service.AdtyStampTaskSiteBilling(c, task.PrivateData.BillingContext)
	task.Quota = relayInfo.PriceData.Quota
	task.Action = relayInfo.Action
	service.AttachTaskBillingReservation(relayInfo, task)
	if err := task.InsertWithContext(context.Background()); err != nil {
		common.SysError(common.LogText("persist ambiguous task submission failed: %s", err.Error()))
		return false
	}
	return true
}

// respondTaskError 统一输出 Task 错误响应（含 429 限流提示改写）
func respondTaskError(c *gin.Context, taskErr *taskdto.TaskError) {
	if c.Writer.Written() {
		return
	}
	if taskErr.StatusCode == http.StatusTooManyRequests {
		taskErr.Message = i18n.T(c, i18n.MsgRelayGroupUpstreamSaturated)
	}
	c.JSON(taskErr.StatusCode, taskErr)
}

// taskSubmissionAPIError adapts a task error for the shared relay error paths.
// TaskError.Error is nil for many local rejections, so fall back to the message.
func taskSubmissionAPIError(taskErr *taskdto.TaskError) *types.NewAPIError {
	err := taskErr.Error
	if err == nil {
		err = errors.New(taskErr.Message)
	}
	return types.NewOpenAIError(err, types.ErrorCodeBadResponseStatusCode, taskErr.StatusCode)
}

// decideTaskRetry is the single retry decision for task submissions. The
// reason is recorded in the request policy decision events of the log details.
func decideTaskRetry(c *gin.Context, taskErr *taskdto.TaskError, retryTimes int) service.PolicyDecision {
	stop := service.PolicyDecision{Action: "stop", Source: "system"}
	retry := service.PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "system"}
	switch {
	case taskErr == nil:
		stop.Reason = "request_completed"
	case taskErr.NoRetry:
		stop.Reason = "task_accepted"
	case taskErr.Code == string(types.ErrorCodeRequestCancelled) || (c != nil && c.Request != nil && c.Request.Context() != nil && errors.Is(c.Request.Context().Err(), context.Canceled)):
		stop.Reason, stop.Source = "client_cancelled", "client"
	case service.ShouldSkipRetryAfterChannelAffinityFailure(c):
		stop.Reason, stop.Source = "strict_session", "session_rule"
		if source := service.RequestPolicy(c).SessionModeSource; source != "" {
			stop.Source = source
		}
	case retryTimes <= 0:
		stop.Reason, stop.Source = "attempt_budget_exhausted", "global"
	case service.GetChannelConstraints(c).SuppressesRetry():
		stop.Reason, stop.Source = "pinned_channel", "channel_constraint"
	case taskErr.StatusCode == http.StatusTooManyRequests, taskErr.StatusCode == 307:
		return retry
	case taskErr.StatusCode/100 == 5:
		// 超时不重试
		if operation_setting.IsAlwaysSkipRetryStatusCode(taskErr.StatusCode) {
			stop.Reason = "system_retry_exclusion"
			break
		}
		return retry
	case taskErr.StatusCode == http.StatusBadRequest, taskErr.StatusCode == 408, taskErr.StatusCode == http.StatusGatewayTimeout:
		// azure处理超时或网关超时不重试
		stop.Reason = "status_not_retryable"
	case taskErr.LocalError:
		stop.Reason = "local_rejection"
	case taskErr.StatusCode/100 == 2:
		stop.Reason = "system_retry_exclusion"
	default:
		return retry
	}
	return stop
}
