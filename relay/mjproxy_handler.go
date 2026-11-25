package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	rkdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func RelayMidjourneyImage(c *gin.Context) {
	taskId := c.Param("id")
	exp, _ := strconv.ParseInt(c.Query("exp"), 10, 64)
	if !service.VerifyMidjourneyImageAccess(taskId, c.Query("sig"), exp, time.Now().Unix()) {
		c.JSON(http.StatusNotFound, gin.H{"error": "midjourney_task_not_found"})
		return
	}
	midjourneyTask := model.GetByOnlyMJId(taskId)
	if midjourneyTask == nil {
		c.JSON(400, gin.H{
			"error": "midjourney_task_not_found",
		})
		return
	}
	var httpClient *http.Client
	var proxy string
	var channelSetting rkdto.ChannelSettings
	if channel, err := model.CacheGetChannel(midjourneyTask.ChannelId); err == nil {
		channelSetting = channel.GetSetting()
		proxy = strings.TrimSpace(channelSetting.Proxy)
		if proxy != "" {
			if httpClient, err = service.GetHttpClientWithProxySettings(proxy, channelSetting); err != nil {
				c.JSON(400, gin.H{
					"error": "proxy_url_invalid",
				})
				return
			}
		}
	}
	if httpClient == nil {
		httpClient = service.GetSSRFProtectedHTTPClient()
	}
	var validateErr error
	logicalURL := midjourneyTask.ImageUrl
	imageURL := logicalURL
	serverName := ""
	requestHost := ""
	if proxy == "" {
		validateErr = service.ValidateSSRFProtectedFetchURL(imageURL)
	} else {
		fetchSetting := system_setting.GetFetchSetting()
		validateErr = common.ValidateURLWithFetchSetting(imageURL, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain)
		if validateErr == nil {
			imageURL, serverName, requestHost, validateErr = pinMidjourneyImageURL(imageURL)
		}
	}
	if validateErr != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"error": fmt.Sprintf("request blocked: %v", service.SanitizeOutboundError(validateErr)),
		})
		return
	}
	resp, err := fetchMidjourneyImage(c, httpClient, imageURL, logicalURL, serverName, requestHost, proxy != "", proxy, channelSetting)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "http_get_image_failed",
		})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		c.JSON(resp.StatusCode, gin.H{
			"error": "upstream_image_fetch_failed",
		})
		return
	}
	contentType, ok := midjourneyImageContentType(resp.Header.Get("Content-Type"))
	if !ok {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{
			"error": "upstream_content_type_not_image",
		})
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Type", contentType)
	_, err = io.Copy(c.Writer, resp.Body)
	if err != nil {
		log.Println(common.LogText("Failed to stream image: %v", err))
	}
	return
}

// lookupMidjourneyImageIPs resolves the image host immediately before a proxied
// fetch. Tests replace it to simulate a DNS answer that changes after the first check.
var lookupMidjourneyImageIPs = func(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}

// pinMidjourneyImageURL resolves a proxied image URL and rewrites it to one
// allowed IP. The proxy then connects to that IP instead of resolving the name again.
func pinMidjourneyImageURL(raw string) (pinnedURL, serverName, requestHost string, err error) {
	fetchSetting := system_setting.GetFetchSetting()
	if fetchSetting == nil || !fetchSetting.EnableSSRFProtection {
		return raw, "", "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", "", err
	}
	host := parsed.Hostname()
	if host == "" {
		return "", "", "", fmt.Errorf("invalid host")
	}
	// Redirect targets, including IP literals, must repeat the URL, private-network and port checks.
	// Returning a literal early let a public image redirect the proxy onto loopback.
	if err := common.ValidateURLWithFetchSetting(raw, true, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
		return "", "", "", err
	}
	if net.ParseIP(host) != nil {
		return raw, "", "", nil
	}
	ips, err := lookupMidjourneyImageIPs(host)
	if err != nil {
		return "", "", "", fmt.Errorf("DNS resolution failed for %s: %v", host, err)
	}
	if len(ips) == 0 {
		return "", "", "", fmt.Errorf("DNS resolution failed for %s", host)
	}
	protection, err := common.NewSSRFProtectionFromFetchSetting(fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain)
	if err != nil {
		return "", "", "", err
	}
	for _, ip := range ips {
		if err := protection.ValidateResolvedIP(host, ip); err != nil {
			return "", "", "", err
		}
	}
	origHost := parsed.Host
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	parsed.Host = net.JoinHostPort(ips[0].String(), port)
	if parsed.Scheme == "https" {
		serverName = host
	}
	return parsed.String(), serverName, origHost, nil
}

type transportClosingBody struct {
	io.ReadCloser
	closeTransport func()
}

func (b *transportClosingBody) Close() error {
	err := b.ReadCloser.Close()
	if b.closeTransport != nil {
		b.closeTransport()
	}
	return err
}

func fetchMidjourneyImage(c *gin.Context, client *http.Client, rawURL, logicalURL, serverName, requestHost string, pinRedirects bool, proxy string, channelSetting rkdto.ChannelSettings) (*http.Response, error) {
	if client == nil {
		return nil, fmt.Errorf("http client is nil")
	}
	currentURL := rawURL
	currentLogicalURL := logicalURL
	if currentLogicalURL == "" {
		currentLogicalURL = rawURL
	}
	currentServerName := serverName
	currentRequestHost := requestHost
	for hop := 0; hop <= 5; hop++ {
		oneOff := *client
		oneOff.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		var clonedTransport *http.Transport
		if currentServerName != "" {
			var tErr error
			clonedTransport, tErr = service.NewPinnedRelayProxyTransport(proxy, currentURL, currentServerName, channelSetting)
			if tErr != nil {
				return nil, tErr
			}
			oneOff.Transport = clonedTransport
		}
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, currentURL, nil)
		if err != nil {
			if clonedTransport != nil {
				clonedTransport.CloseIdleConnections()
			}
			return nil, err
		}
		if currentRequestHost != "" {
			req.Host = currentRequestHost
		}
		resp, err := oneOff.Do(req)
		if err != nil {
			if clonedTransport != nil {
				clonedTransport.CloseIdleConnections()
			}
			return nil, err
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := strings.TrimSpace(resp.Header.Get("Location"))
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if clonedTransport != nil {
				clonedTransport.CloseIdleConnections()
			}
			if loc == "" || hop >= 5 {
				return nil, fmt.Errorf("stopped after redirects")
			}
			baseParsed, parseErr := url.Parse(currentLogicalURL)
			if parseErr != nil {
				return nil, parseErr
			}
			refParsed, parseErr := url.Parse(loc)
			if parseErr != nil {
				return nil, parseErr
			}
			nextParsed := baseParsed.ResolveReference(refParsed)
			if strings.EqualFold(baseParsed.Scheme, "https") && !strings.EqualFold(nextParsed.Scheme, "https") {
				return nil, fmt.Errorf("https to non-https redirect blocked")
			}
			nextURL := nextParsed.String()
			currentLogicalURL = nextURL
			if pinRedirects {
				pinned, nextServerName, nextRequestHost, pinErr := pinMidjourneyImageURL(nextURL)
				if pinErr != nil {
					return nil, pinErr
				}
				currentURL = pinned
				currentServerName = nextServerName
				currentRequestHost = nextRequestHost
			} else {
				if valErr := service.ValidateSSRFProtectedFetchURL(nextURL); valErr != nil {
					return nil, valErr
				}
				currentURL = nextURL
				currentServerName = ""
				currentRequestHost = ""
			}
			continue
		}
		if clonedTransport != nil && resp.Body != nil {
			resp.Body = &transportClosingBody{
				ReadCloser:     resp.Body,
				closeTransport: clonedTransport.CloseIdleConnections,
			}
		}
		return resp, nil
	}
	return nil, fmt.Errorf("stopped after redirects")
}

func isAmbiguousMidjourneySubmitError(resp *dto.MidjourneyResponseWithStatusCode, err error) bool {
	if resp != nil && (resp.StatusCode == http.StatusGatewayTimeout || (err != nil && resp.StatusCode >= 200 && resp.StatusCode < 300)) {
		return true
	}
	if err == nil {
		return false
	}
	if IsAmbiguousTaskSubmitTransportError(err) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

func midjourneyImageContentType(header string) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/avif", "image/bmp":
		return strings.ToLower(mediaType), true
	default:
		return "", false
	}
}

func RelayMidjourneyNotify(c *gin.Context) *dto.MidjourneyResponse {
	var midjRequest dto.MidjourneyDto
	err := common.UnmarshalBodyReusable(c, &midjRequest)
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "bind_request_body_failed",
			Properties:  nil,
			Result:      "",
		}
	}
	midjourneyTask := model.GetByOnlyMJId(midjRequest.MjId)
	if midjourneyTask == nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "midjourney_task_not_found",
			Properties:  nil,
			Result:      "",
		}
	}
	midjourneyTask.Progress = midjRequest.Progress
	midjourneyTask.PromptEn = midjRequest.PromptEn
	midjourneyTask.State = midjRequest.State
	midjourneyTask.SubmitTime = midjRequest.SubmitTime
	midjourneyTask.StartTime = midjRequest.StartTime
	midjourneyTask.FinishTime = midjRequest.FinishTime
	midjourneyTask.ImageUrl = midjRequest.ImageUrl
	midjourneyTask.VideoUrl = midjRequest.VideoUrl
	videoUrlsStr, _ := common.Marshal(midjRequest.VideoUrls)
	midjourneyTask.VideoUrls = string(videoUrlsStr)
	midjourneyTask.Status = midjRequest.Status
	midjourneyTask.FailReason = midjRequest.FailReason
	err = midjourneyTask.Update()
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "update_midjourney_task_failed",
		}
	}

	return nil
}

func coverMidjourneyTaskDto(c *gin.Context, originTask *model.Midjourney) (midjourneyTask dto.MidjourneyDto) {
	midjourneyTask.MjId = originTask.MjId
	midjourneyTask.Progress = originTask.Progress
	midjourneyTask.PromptEn = originTask.PromptEn
	midjourneyTask.State = originTask.State
	midjourneyTask.SubmitTime = originTask.SubmitTime
	midjourneyTask.StartTime = originTask.StartTime
	midjourneyTask.FinishTime = originTask.FinishTime
	midjourneyTask.ImageUrl = ""
	if originTask.ImageUrl != "" && setting.GetMjForwardUrlEnabled() {
		imagePath, err := service.MidjourneyImagePath(originTask.MjId, time.Now())
		if err != nil {
			midjourneyTask.ImageUrl = ""
		} else {
			midjourneyTask.ImageUrl = strings.TrimRight(system_setting.GetServerAddress(), "/") + imagePath
			if originTask.Status != "SUCCESS" {
				sep := "?"
				if strings.Contains(midjourneyTask.ImageUrl, "?") {
					sep = "&"
				}
				midjourneyTask.ImageUrl += sep + "rand=" + strconv.FormatInt(time.Now().UnixNano(), 10)
			}
		}
	} else {
		midjourneyTask.ImageUrl = originTask.ImageUrl
	}
	if originTask.VideoUrl != "" {
		midjourneyTask.VideoUrl = originTask.VideoUrl
	}
	midjourneyTask.Status = originTask.Status
	midjourneyTask.FailReason = originTask.FailReason
	midjourneyTask.Action = originTask.Action
	midjourneyTask.Description = originTask.Description
	midjourneyTask.Prompt = originTask.Prompt
	if originTask.Buttons != "" {
		var buttons []dto.ActionButton
		err := common.Unmarshal([]byte(originTask.Buttons), &buttons)
		if err == nil {
			midjourneyTask.Buttons = buttons
		}
	}
	if originTask.VideoUrls != "" {
		var videoUrls []dto.ImgUrls
		err := common.Unmarshal([]byte(originTask.VideoUrls), &videoUrls)
		if err == nil {
			midjourneyTask.VideoUrls = videoUrls
		}
	}
	if originTask.Properties != "" {
		var properties dto.Properties
		err := common.Unmarshal([]byte(originTask.Properties), &properties)
		if err == nil {
			midjourneyTask.Properties = &properties
		}
	}
	return
}

func RelaySwapFace(c *gin.Context, info *relaycommon.RelayInfo) *dto.MidjourneyResponse {
	var swapFaceRequest dto.SwapFaceRequest
	err := common.UnmarshalBodyReusable(c, &swapFaceRequest)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "bind_request_body_failed")
	}

	info.InitChannelMeta(c)

	if swapFaceRequest.SourceBase64 == "" || swapFaceRequest.TargetBase64 == "" {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "sour_base64_and_target_base64_is_required")
	}
	modelName := service.CovertMjpActionToModelName(constant.MjActionSwapFace)

	priceData, err := helper.ModelPriceHelperPerCall(c, info)
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: err.Error(),
		}
	}

	// R2-018: Authoritative balance/payer check (Personal / Owner Self-Use / Team)
	// is enforced transactionally in service.PrepareMidjourneyTaskBilling below.
	requestURL := getMjRequestPath(c.Request.URL.String())
	baseURL := c.GetString("base_url")
	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)
	midjourneyTask := &model.Midjourney{
		UserId:     info.UserId,
		Action:     constant.MjActionSwapFace,
		Prompt:     "InsightFace",
		PromptEn:   "",
		State:      "",
		SubmitTime: info.StartTime.UnixNano() / int64(time.Millisecond),
		StartTime:  time.Now().UnixNano() / int64(time.Millisecond),
		FinishTime: 0,
		ImageUrl:   "",
		Status:     "",
		Progress:   "0%",
		FailReason: "",
		ChannelId:  c.GetInt("channel_id"),
	}
	billingPrepared, billingErr := service.PrepareMidjourneyTaskBilling(
		info,
		midjourneyTask,
		priceData.Quota,
		true,
		c,
	)
	if billingErr != nil {
		if errors.Is(billingErr, model.ErrBillingWalletInsufficient) {
			return &dto.MidjourneyResponse{Code: 4, Description: "quota_not_enough"}
		}
		if errors.Is(billingErr, model.ErrBillingTokenInsufficient) {
			return &dto.MidjourneyResponse{Code: 4, Description: "token_quota_not_enough"}
		}
		return service.MidjourneyErrorWrapper(constant.MjRequestError, billingErr.Error())
	}
	billingApplied := false
	upstreamAcceptedBillable := false
	defer func() {
		// R4-021 / R6-010: Once upstream accepted or returned an ambiguous outcome
		// for a billable job, transition out of BillingReserved instead of releasing
		// or leaving reference_id=0 eligible for lease-expiry auto-refund.
		if !billingApplied {
			if upstreamAcceptedBillable {
				service.SettleAmbiguousMidjourneyTaskBilling(midjourneyTask, priceData.Quota)
			} else {
				service.ReleaseMidjourneyTaskBilling(midjourneyTask)
			}
		}
	}()
	service.RequestPolicy(c).BeginAttempt(&model.Channel{Id: c.GetInt("channel_id")}, info.UsingGroup)
	mjResp, _, err := service.DoMidjourneyHttpRequest(c, time.Second*60, fullRequestURL)
	accepted := service.RecordMidjourneyPolicyResponse(c, mjResp, err)
	if err != nil {
		if priceData.Quota > 0 && isAmbiguousMidjourneySubmitError(mjResp, err) {
			upstreamAcceptedBillable = true
		}
		return &mjResp.Response
	}
	midjResponse := &mjResp.Response
	midjourneyTask.Code, midjourneyTask.MjId = midjResponse.Code, midjResponse.Result
	midjourneyTask.Description = midjResponse.Description
	if mjResp.StatusCode == http.StatusGatewayTimeout && priceData.Quota > 0 {
		upstreamAcceptedBillable = true
		if midjourneyTask.MjId == "" {
			midjourneyTask.Status = "AMBIGUOUS"
			midjourneyTask.Progress = "100%"
			if midjourneyTask.FailReason == "" {
				midjourneyTask.FailReason = "ambiguous_upstream_submit"
			}
		}
	} else if mjResp.StatusCode != http.StatusOK || midjResponse.Code != 1 {
		billingPrepared, _ = service.PrepareMidjourneyTaskBilling(info, midjourneyTask, 0, false)
	} else if priceData.Quota > 0 {
		upstreamAcceptedBillable = true
	}
	err = midjourneyTask.Insert()
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "insert_midjourney_task_failed")
	}
	billingApplied, billingErr = service.SettleMidjourneyTaskBilling(info, midjourneyTask, billingPrepared)
	if billingErr != nil {
		common.SysLog(common.LogText("error settling Midjourney quota: %s", billingErr.Error()))
	}
	if accepted {
		service.MarkRequestPolicySuccess(c, nil)
	}
	if billingApplied {
		billingChannelId := midjourneyTask.GetBillingChannelId()
		tokenName := c.GetString("token_name")
		logContent := []*common.Message{common.NewMessage("Model fixed price {{model_price}}, group ratio {{group_ratio}}, action {{action}}", map[string]any{
			"model_price": fmt.Sprintf("%.2f", priceData.ModelPrice),
			"group_ratio": fmt.Sprintf("%.2f", priceData.GroupRatioInfo.GroupRatio),
			"action":      constant.MjActionSwapFace,
		})}
		other := service.GenerateMjOtherInfo(info, priceData)
		service.AppendRelayLogAdminInfo(c, info, other)
		model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
			ChannelId: billingChannelId,
			ModelName: modelName,
			TokenName: tokenName,
			Quota:     midjourneyTask.Quota,
			Content:   logContent,
			TokenId:   midjourneyTask.TokenId,
			Group:     info.UsingGroup,
			Other:     other,
		})
		model.UpdateUserUsedQuotaAndRequestCount(info.UserId, midjourneyTask.Quota)
		model.UpdateChannelUsedQuota(billingChannelId, midjourneyTask.Quota)
	}
	c.Writer.WriteHeader(mjResp.StatusCode)
	respBody, err := common.Marshal(midjResponse)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "unmarshal_response_body_failed")
	}
	_, err = io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "copy_response_body_failed")
	}
	return nil
}

func RelayMidjourneyTaskImageSeed(c *gin.Context) *dto.MidjourneyResponse {
	taskId := c.Param("id")
	userId := c.GetInt("id")
	originTask, err := model.GetByMJId(userId, taskId)
	if err != nil {
		return &dto.MidjourneyResponse{Code: 4, Description: "database_error"}
	}
	if originTask == nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_no_found")
	}
	channel, err := model.GetChannelById(originTask.ChannelId, true)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "get_channel_info_failed")
	}
	if channel.Status != common.ChannelStatusEnabled {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, i18n.T(c, i18n.MsgRelayTaskChannelDisabled))
	}
	key, _, keyErr := channel.GetNextEnabledKey()
	if keyErr != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, keyErr.Error())
	}
	common.SetContextKey(c, constant.ContextKeyChannelId, originTask.ChannelId)
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, channel.GetBaseURL())
	common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, channel.GetSetting())
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, channel.GetOtherSettings())
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, channel.GetParamOverride())
	common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, channel.GetHeaderOverride())
	c.Set("channel_id", originTask.ChannelId)
	c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))

	requestURL := getMjRequestPath(c.Request.URL.String())
	fullRequestURL := fmt.Sprintf("%s%s", channel.GetBaseURL(), requestURL)
	midjResponseWithStatus, _, err := service.DoMidjourneyHttpRequest(c, time.Second*30, fullRequestURL)
	if err != nil {
		return &midjResponseWithStatus.Response
	}
	midjResponse := &midjResponseWithStatus.Response
	respBody, err := common.Marshal(midjResponse)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "unmarshal_response_body_failed")
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(midjResponseWithStatus.StatusCode)
	service.IOCopyBytesGracefully(c, nil, respBody)
	return nil
}

func RelayMidjourneyTask(c *gin.Context, relayMode int) *dto.MidjourneyResponse {
	userId := c.GetInt("id")
	var err error
	var respBody []byte
	switch relayMode {
	case relayconstant.RelayModeMidjourneyTaskFetch:
		taskId := c.Param("id")
		originTask, err := model.GetByMJId(userId, taskId)
		if err != nil {
			return &dto.MidjourneyResponse{Code: 4, Description: "database_error"}
		}
		if originTask == nil {
			return &dto.MidjourneyResponse{
				Code:        4,
				Description: "task_no_found",
			}
		}
		midjourneyTask := coverMidjourneyTaskDto(c, originTask)
		respBody, err = common.Marshal(midjourneyTask)
		if err != nil {
			return &dto.MidjourneyResponse{
				Code:        4,
				Description: "unmarshal_response_body_failed",
			}
		}
	case relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		var condition = struct {
			IDs []string `json:"ids"`
		}{}
		err = c.BindJSON(&condition)
		if err != nil {
			return &dto.MidjourneyResponse{
				Code:        4,
				Description: "do_request_failed",
			}
		}
		var tasks []dto.MidjourneyDto
		if len(condition.IDs) != 0 {
			originTasks, err := model.GetByMJIds(userId, condition.IDs)
			if err != nil {
				return &dto.MidjourneyResponse{Code: 4, Description: "database_error"}
			}
			for _, originTask := range originTasks {
				midjourneyTask := coverMidjourneyTaskDto(c, originTask)
				tasks = append(tasks, midjourneyTask)
			}
		}
		if tasks == nil {
			tasks = make([]dto.MidjourneyDto, 0)
		}
		respBody, err = common.Marshal(tasks)
		if err != nil {
			return &dto.MidjourneyResponse{
				Code:        4,
				Description: "unmarshal_response_body_failed",
			}
		}
	}

	c.Writer.Header().Set("Content-Type", "application/json")

	_, err = io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		logger.LogError(c, common.LogText("midjourney copy response body failed: %s", err.Error()))
	}
	return nil
}

func RelayMidjourneySubmit(c *gin.Context, relayInfo *relaycommon.RelayInfo) *dto.MidjourneyResponse {
	consumeQuota := true
	var midjRequest dto.MidjourneyRequest
	err := common.UnmarshalBodyReusable(c, &midjRequest)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "bind_request_body_failed")
	}

	relayInfo.InitChannelMeta(c)

	if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyAction { // midjourney plus，需要从customId中获取任务信息
		mjErr := service.CoverPlusActionToNormalAction(&midjRequest)
		if mjErr != nil {
			return mjErr
		}
		relayInfo.RelayMode = relayconstant.RelayModeMidjourneyChange
	}
	if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyVideo {
		midjRequest.Action = constant.MjActionVideo
	}

	if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyImagine { //绘画任务，此类任务可重复
		if midjRequest.Prompt == "" {
			return service.MidjourneyErrorWrapper(constant.MjRequestError, "prompt_is_required")
		}
		midjRequest.Action = constant.MjActionImagine
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyDescribe { //按图生文任务，此类任务可重复
		midjRequest.Action = constant.MjActionDescribe
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyEdits { //编辑任务，此类任务可重复
		midjRequest.Action = constant.MjActionEdits
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyShorten { //缩短任务，此类任务可重复，plus only
		midjRequest.Action = constant.MjActionShorten
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyBlend { //绘画任务，此类任务可重复
		midjRequest.Action = constant.MjActionBlend
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyUpload { //绘画任务，此类任务可重复
		midjRequest.Action = constant.MjActionUpload
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneySimpleChange || midjRequest.TaskId != "" { //放大、变换任务，此类任务，如果重复且已有结果，远端api会直接返回最终结果
		mjId := ""
		if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyChange {
			if midjRequest.TaskId == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_id_is_required")
			} else if midjRequest.Action == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "action_is_required")
			} else if midjRequest.Index == 0 {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "index_is_required")
			}
			//action = midjRequest.Action
			mjId = midjRequest.TaskId
		} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneySimpleChange {
			if midjRequest.Content == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "content_is_required")
			}
			params := service.ConvertSimpleChangeParams(midjRequest.Content)
			if params == nil {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "content_parse_failed")
			}
			mjId = params.TaskId
			midjRequest.TaskId = params.TaskId
			midjRequest.Action = params.Action
			midjRequest.Index = params.Index
		} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyModal {
			//if midjRequest.MaskBase64 == "" {
			//	return service.MidjourneyErrorWrapper(constant.MjRequestError, "mask_base64_is_required")
			//}
			mjId = midjRequest.TaskId
			midjRequest.Action = constant.MjActionModal
		} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyVideo {
			midjRequest.Action = constant.MjActionVideo
			if midjRequest.TaskId == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_id_is_required")
			} else if midjRequest.Action == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "action_is_required")
			}
			mjId = midjRequest.TaskId
		}

		originTask, err := model.GetByMJId(relayInfo.UserId, mjId)
		if err != nil {
			return &dto.MidjourneyResponse{Code: 4, Description: "database_error"}
		}
		if originTask == nil {
			return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_not_found")
		} else { //原任务的Status=SUCCESS，则可以做放大UPSCALE、变换VARIATION等动作，此时必须使用原来的请求地址才能正确处理
			if setting.GetMjActionCheckSuccessEnabled() {
				if originTask.Status != "SUCCESS" && relayInfo.RelayMode != relayconstant.RelayModeMidjourneyModal {
					return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_status_not_success")
				}
			}
			channel, err := model.GetChannelById(originTask.ChannelId, true)
			if err != nil {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "get_channel_info_failed")
			}
			if channel.Status != common.ChannelStatusEnabled {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, i18n.T(c, i18n.MsgRelayTaskChannelDisabled))
			}
			// R9-04: Enforce current group/model authorization and re-resolve
			// "auto" group for the origin channel before reusing it (mirroring R8-05).
			actionModelName := service.CovertMjpActionToModelName(midjRequest.Action)
			if actionModelName != "" && relayInfo != nil {
				if relayInfo.TokenGroup == "auto" || relayInfo.UsingGroup == "auto" {
					userGroup := relayInfo.UserGroup
					if userGroup == "" {
						userGroup = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
					}
					resolvedGroup := ""
					for _, g := range service.GetRequestAutoGroups(c, userGroup) {
						if g == "" || g == "auto" {
							continue
						}
						if !service.SelectedBillingGroupAllowed(c, g, actionModelName) {
							continue
						}
						if model.IsChannelEnabledForGroupModel(g, actionModelName, channel.Id) {
							resolvedGroup = g
							break
						}
					}
					if resolvedGroup == "" {
						return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_channel_forbidden")
					}
					common.SetContextKey(c, constant.ContextKeyAutoGroup, resolvedGroup)
					relayInfo.UsingGroup = resolvedGroup
				} else {
					effectiveGroup := relayInfo.UsingGroup
					if effectiveGroup == "" {
						effectiveGroup = relayInfo.TokenGroup
					}
					if effectiveGroup == "" ||
						!service.SelectedBillingGroupAllowed(c, effectiveGroup, actionModelName) ||
						!model.IsChannelEnabledForGroupModel(effectiveGroup, actionModelName, channel.Id) {
						return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_channel_forbidden")
					}
				}
			}
			key, _, keyErr := channel.GetNextEnabledKey()
			if keyErr != nil {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, keyErr.Error())
			}
			channelSetting := channel.GetSetting()
			channelOtherSettings := channel.GetOtherSettings()
			common.SetContextKey(c, constant.ContextKeyChannelId, originTask.ChannelId)
			common.SetContextKey(c, constant.ContextKeyChannelKey, key)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, channel.GetBaseURL())
			common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, channelSetting)
			common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, channelOtherSettings)
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, channel.GetParamOverride())
			common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, channel.GetHeaderOverride())
			c.Set("base_url", channel.GetBaseURL())
			c.Set("channel_id", originTask.ChannelId)
			c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
			if relayInfo != nil {
				relayInfo.ChannelId = originTask.ChannelId
				relayInfo.ChannelBaseUrl = channel.GetBaseURL()
				relayInfo.ChannelType = channel.Type
				relayInfo.ApiKey = key
				relayInfo.ChannelSetting = channelSetting
				relayInfo.ChannelOtherSettings = channelOtherSettings
			}
			logger.LogDebug(c, "Midjourney action uses origin channel: id=%s, base_url=%s", strconv.Itoa(originTask.ChannelId), channel.GetBaseURL())
		}
		midjRequest.Prompt = originTask.Prompt

		//if channelType == common.ChannelTypeMidjourneyPlus {
		//	// plus
		//} else {
		//	// 普通版渠道
		//
		//}
	}

	if midjRequest.Action == constant.MjActionInPaint || midjRequest.Action == constant.MjActionCustomZoom {
		consumeQuota = false
	}

	//baseURL := common.ChannelBaseURLs[channelType]
	requestURL := getMjRequestPath(c.Request.URL.String())

	baseURL := c.GetString("base_url")

	//midjRequest.NotifyHook = "http://127.0.0.1:3000/mj/notify"

	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	modelName := service.CovertMjpActionToModelName(midjRequest.Action)

	priceData, err := helper.ModelPriceHelperPerCall(c, relayInfo)
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: err.Error(),
		}
	}

	// R2-018: Authoritative balance/payer check (Personal / Owner Self-Use / Team)
	// is enforced transactionally in service.PrepareMidjourneyTaskBilling below.
	midjourneyTask := &model.Midjourney{
		UserId:     relayInfo.UserId,
		Action:     midjRequest.Action,
		Prompt:     midjRequest.Prompt,
		PromptEn:   "",
		State:      "",
		SubmitTime: time.Now().UnixNano() / int64(time.Millisecond),
		StartTime:  0,
		FinishTime: 0,
		ImageUrl:   "",
		Status:     "",
		Progress:   "0%",
		FailReason: "",
		ChannelId:  c.GetInt("channel_id"),
	}
	billingPrepared, billingErr := service.PrepareMidjourneyTaskBilling(relayInfo, midjourneyTask, priceData.Quota, consumeQuota, c)
	if billingErr != nil {
		if errors.Is(billingErr, model.ErrBillingWalletInsufficient) {
			return &dto.MidjourneyResponse{Code: 4, Description: "quota_not_enough"}
		}
		if errors.Is(billingErr, model.ErrBillingTokenInsufficient) {
			return &dto.MidjourneyResponse{Code: 4, Description: "token_quota_not_enough"}
		}
		return service.MidjourneyErrorWrapper(constant.MjRequestError, billingErr.Error())
	}
	billingApplied := false
	upstreamAcceptedBillable := false
	defer func() {
		// R4-021 / R6-010: Never auto-refund (or leave in BillingReserved) after
		// upstream accepted or returned an ambiguous outcome for a billable submit.
		if !billingApplied {
			if upstreamAcceptedBillable {
				service.SettleAmbiguousMidjourneyTaskBilling(midjourneyTask, priceData.Quota)
			} else {
				service.ReleaseMidjourneyTaskBilling(midjourneyTask)
			}
		}
	}()
	service.RequestPolicy(c).BeginAttempt(&model.Channel{Id: c.GetInt("channel_id")}, relayInfo.UsingGroup)
	midjResponseWithStatus, responseBody, err := service.DoMidjourneyHttpRequest(c, time.Second*60, fullRequestURL)
	accepted := service.RecordMidjourneyPolicyResponse(c, midjResponseWithStatus, err)
	if err != nil {
		if consumeQuota && priceData.Quota > 0 && isAmbiguousMidjourneySubmitError(midjResponseWithStatus, err) {
			upstreamAcceptedBillable = true
		}
		return &midjResponseWithStatus.Response
	}
	midjResponse := &midjResponseWithStatus.Response
	midjourneyTask.Code, midjourneyTask.MjId = midjResponse.Code, midjResponse.Result
	midjourneyTask.Description = midjResponse.Description

	// 文档：https://github.com/novicezk/midjourney-proxy/blob/main/docs/api.md
	//1-提交成功
	// 21-任务已存在（处理中或者有结果了） {"code":21,"description":"任务已存在","result":"0741798445574458","properties":{"status":"SUCCESS","imageUrl":"https://xxxx"}}
	// 22-排队中 {"code":22,"description":"排队中，前面还有1个任务","result":"0741798445574458","properties":{"numberOfQueues":1,"discordInstanceId":"1118138338562560102"}}
	// 23-队列已满，请稍后再试 {"code":23,"description":"队列已满，请稍后尝试","result":"14001929738841620","properties":{"discordInstanceId":"1118138338562560102"}}
	// 24-prompt包含敏感词 {"code":24,"description":"可能包含敏感词","properties":{"promptEn":"nude body","bannedWord":"nude"}}
	// other: 提交错误，description为错误描述
	if midjResponse.Code == 3 {
		//无实例账号自动禁用渠道（No available account instance）
		channel, err := model.GetChannelById(midjourneyTask.ChannelId, true)
		if err != nil {
			common.SysLog(common.LogText("get_channel_null: %s", err.Error()))
		}
		if channel != nil && channel.GetAutoBan() && common.GetAutomaticDisableChannelEnabled() {
			if model.UpdateChannelStatus(midjourneyTask.ChannelId, "", common.ChannelStatusManuallyDisabled, "No available account instance") {
				service.CloseActiveWebSocketsForChannel(midjourneyTask.ChannelId, service.ChannelDisabledCloseReason)
				service.RequestPolicy(c).AddEvent(service.PolicyEvent{ChannelID: midjourneyTask.ChannelId, Decision: service.PolicyDecision{Action: "stop", Reason: "task_failed", Source: "upstream"}, Health: "channel_disable_requested"})
			}
		}
	}
	// 21 表示同一上游任务已经存在。第一次提交已经扣过费，重试不能再扣。
	alreadyExists := midjResponse.Code == 21
	if midjResponse.Code != 1 && midjResponse.Code != 21 && midjResponse.Code != 22 {
		//非1-提交成功,21-任务已存在和22-排队中，则记录错误原因
		midjourneyTask.FailReason = midjResponse.Description
		consumeQuota = false
	}
	if alreadyExists {
		consumeQuota = false
	}

	if midjResponse.Code == 21 { //21-任务已存在（处理中或者有结果了）
		// 将 properties 转换为一个 map
		properties, ok := midjResponse.Properties.(map[string]any)
		if ok {
			imageUrl, ok1 := properties["imageUrl"].(string)
			status, ok2 := properties["status"].(string)
			if ok1 && ok2 {
				midjourneyTask.ImageUrl = imageUrl
				midjourneyTask.Status = status
				if status == "SUCCESS" {
					midjourneyTask.Progress = "100%"
					midjourneyTask.StartTime = time.Now().UnixNano() / int64(time.Millisecond)
					midjourneyTask.FinishTime = time.Now().UnixNano() / int64(time.Millisecond)
					midjResponse.Code = 1
				}
			}
		}
		//修改返回值
		if midjRequest.Action != constant.MjActionInPaint && midjRequest.Action != constant.MjActionCustomZoom {
			newBody := strings.Replace(string(responseBody), `"code":21`, `"code":1`, -1)
			responseBody = []byte(newBody)
		}
	}
	if midjResponse.Code == 1 && midjRequest.Action == "UPLOAD" {
		midjourneyTask.Progress = "100%"
		midjourneyTask.Status = "SUCCESS"
	}
	// 本地已经有同一用户的这一条上游任务时不再插第二行。轮询按 mj_id 只更新一条，
	// 多出来的未完成行不会退款。
	skipNewRow := false
	if alreadyExists && midjourneyTask.MjId != "" {
		existing, err := model.GetByMJId(relayInfo.UserId, midjourneyTask.MjId)
		if err != nil {
			return &dto.MidjourneyResponse{Code: 4, Description: "database_error"}
		}
		if existing != nil {
			skipNewRow = true
		}
	}
	if !skipNewRow {
		if midjResponseWithStatus.StatusCode == http.StatusGatewayTimeout && priceData.Quota > 0 {
			upstreamAcceptedBillable = true
			if midjourneyTask.MjId == "" {
				midjourneyTask.Status = "AMBIGUOUS"
				midjourneyTask.Progress = "100%"
				if midjourneyTask.FailReason == "" {
					midjourneyTask.FailReason = "ambiguous_upstream_submit"
				}
			}
		} else if !consumeQuota || midjResponseWithStatus.StatusCode != http.StatusOK {
			billingPrepared, _ = service.PrepareMidjourneyTaskBilling(relayInfo, midjourneyTask, 0, false)
		} else if priceData.Quota > 0 {
			upstreamAcceptedBillable = true
		}
		err = midjourneyTask.Insert()
		if err != nil {
			return &dto.MidjourneyResponse{
				Code:        4,
				Description: "insert_midjourney_task_failed",
			}
		}
		var settleErr error
		billingApplied, settleErr = service.SettleMidjourneyTaskBilling(relayInfo, midjourneyTask, billingPrepared)
		if settleErr != nil {
			common.SysLog(common.LogText("error settling Midjourney quota: %s", settleErr.Error()))
		}
	}
	if accepted {
		service.MarkRequestPolicySuccess(c, nil)
	}
	if billingApplied {
		billingChannelId := midjourneyTask.GetBillingChannelId()
		tokenName := c.GetString("token_name")
		logContent := []*common.Message{common.NewMessage("Model fixed price {{model_price}}, group ratio {{group_ratio}}, action {{action}}, ID {{id}}", map[string]any{
			"model_price": fmt.Sprintf("%.2f", priceData.ModelPrice),
			"group_ratio": fmt.Sprintf("%.2f", priceData.GroupRatioInfo.GroupRatio),
			"action":      midjRequest.Action,
			"id":          midjResponse.Result,
		})}
		other := service.GenerateMjOtherInfo(relayInfo, priceData)
		service.AppendRelayLogAdminInfo(c, relayInfo, other)
		model.RecordConsumeLog(c, relayInfo.UserId, model.RecordConsumeLogParams{
			ChannelId: billingChannelId,
			ModelName: modelName,
			TokenName: tokenName,
			Quota:     midjourneyTask.Quota,
			Content:   logContent,
			TokenId:   midjourneyTask.TokenId,
			Group:     relayInfo.UsingGroup,
			Other:     other,
		})
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, midjourneyTask.Quota)
		model.UpdateChannelUsedQuota(billingChannelId, midjourneyTask.Quota)
	}

	if midjResponse.Code == 22 { //22-排队中，说明任务已存在
		//修改返回值
		newBody := strings.Replace(string(responseBody), `"code":22`, `"code":1`, -1)
		responseBody = []byte(newBody)
	}
	//resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))
	bodyReader := io.NopCloser(bytes.NewBuffer(responseBody))

	//for k, v := range resp.Header {
	//	c.Writer.Header().Set(k, v[0])
	//}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(midjResponseWithStatus.StatusCode)

	_, err = io.Copy(c.Writer, bodyReader)
	if err != nil {
		logger.LogError(c, common.LogText("midjourney copy response body failed: %s", err.Error()))
	}
	if closeErr := bodyReader.Close(); closeErr != nil {
		logger.LogError(c, common.LogText("midjourney close response body failed: %s", closeErr.Error()))
	}
	return nil
}

type taskChangeParams struct {
	ID     string
	Action string
	Index  int
}

func getMjRequestPath(path string) string {
	requestURL := path
	if strings.Contains(requestURL, "/mj-") {
		urls := strings.Split(requestURL, "/mj/")
		if len(urls) < 2 {
			return requestURL
		}
		requestURL = "/mj/" + urls[1]
	}
	return requestURL
}
