package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
)

// 易支付的 API 下单、页面跳转、查单和探活（任务 5.2、5.3）。各家程序有差异，返回字段要容错。

// AdtyEpayCreateRequest 是一笔易支付下单。
type AdtyEpayCreateRequest struct {
	TradeNo, Name, ClientIP, Device string
	MoneyCNY                        decimal.Decimal
	NotifyURL, ReturnURL            string // ReturnURL 不带 query
}

// AdtyEpayCreateResult 是网关返回的支付信息。Mode 是 qrcode、payurl、urlscheme 或 submit。
type AdtyEpayCreateResult struct{ Mode, QRCode, PayURL, URLScheme, GatewayTradeNo string }

// AdtyEpayQueryResult 是查单结果。查单返回没有签名，只对 https 网关当真。
var errEpayOrderAbsent = fmt.Errorf("epay order was not found")

type AdtyEpayQueryResult struct {
	PaidAt         int64
	Paid           bool
	RawStatus      string
	MoneyCNY       decimal.Decimal
	TypeCode       string
	GatewayTradeNo string
	OutTradeNo     string
	MerchantId     string
}

func epayRawString(raw common.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := common.Unmarshal(raw, &s); err == nil {
			return strings.TrimSpace(s)
		}
	}
	return strings.Trim(trimmed, `"`)
}

var adtyEpayHTTPClient = func() *http.Client {
	return GetVerifiedNoRedirectSSRFProtectedHTTPClient(30 * time.Second)
}

var adtyEpayGatewaySSRF = &common.SSRFProtection{
	AllowPrivateIp:   false,
	DomainFilterMode: false,
	IpFilterMode:     false,
}

// ValidateEpayGatewayURL trims and validates an Epay channel gateway URL:
// requires http:// or https:// with a non-empty public host, and rejects
// credentials (userinfo), query strings, fragments, whitespace/control chars,
// localhost, and private/loopback/link-local/metadata IP literals.
func ValidateEpayGatewayURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n") {
		return "", common.NewMessage("The gateway address must be a valid public http:// or https:// URL without credentials, query, or fragment")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		strings.TrimSpace(parsed.Hostname()) == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", common.NewMessage("The gateway address must be a valid public http:// or https:// URL without credentials, query, or fragment")
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(parsed.Hostname()), "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "", common.NewMessage("The gateway address cannot target a local or private network address")
	}
	port := 443
	if parsed.Scheme == "http" {
		port = 80
	}
	if portStr := strings.TrimSpace(parsed.Port()); portStr != "" {
		parsedPort, pErr := strconv.Atoi(portStr)
		if pErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return "", common.NewMessage("The gateway address must be a valid public http:// or https:// URL without credentials, query, or fragment")
		}
		port = parsedPort
	}
	if err := adtyEpayGatewaySSRF.ValidateNetworkTarget(host, port); err != nil {
		return "", common.NewMessage("The gateway address cannot target a local or private network address")
	}
	return trimmed, nil
}

func adtyEpayEndpoint(channel *model.AdtyEpayChannel, path string) string {
	base := strings.TrimRight(strings.TrimSpace(channel.GatewayURL), "/")
	for _, suffix := range []string{
		"/api/pay/submit", "/api/pay/create", "/api/pay/query", "/api/merchant/info",
		"/submit.php", "/mapi.php", "/api.php",
	} {
		base = strings.TrimSuffix(base, suffix)
	}
	return base + path
}

// adtyEpayOrderParams 是 mapi.php 和 submit.php 共用的参数，签名用原值。
func adtyEpayOrderParams(channel *model.AdtyEpayChannel, method *model.AdtyEpayChannelMethod, req AdtyEpayCreateRequest) map[string]string {
	typeCode := method.TypeCode
	if typeCode == "" {
		typeCode = method.Method
	}
	params := map[string]string{
		"pid":          channel.MerchantId,
		"type":         typeCode,
		"out_trade_no": req.TradeNo,
		"notify_url":   req.NotifyURL,
		"name":         req.Name,
		"money":        req.MoneyCNY.StringFixed(2),
	}
	if strings.TrimSpace(req.ReturnURL) != "" {
		params["return_url"] = req.ReturnURL
	}
	return params
}

// CreateAdtyEpayOrder 下单。MD5 使用 mapi.php / submit.php；RSA 使用 /api/pay/create / /api/pay/submit。
func CreateAdtyEpayOrder(ctx context.Context, channel *model.AdtyEpayChannel, method *model.AdtyEpayChannelMethod, req AdtyEpayCreateRequest) (*AdtyEpayCreateResult, error) {
	if channel == nil || channel.Status == model.AdtyEpayStatusRevoked || !channel.HasCredentials() {
		return nil, common.NewMessage("This payment channel's credentials have been revoked")
	}
	params := adtyEpayOrderParams(channel, method, req)
	if channel.OrderMode == model.AdtyEpayOrderModeSubmit {
		if strings.TrimSpace(params["return_url"]) == "" {
			if callback := strings.TrimRight(strings.TrimSpace(GetCallbackAddress()), "/"); callback != "" {
				params["return_url"] = callback + "/wallet"
			}
		}
		if channel.IsRSA() && strings.TrimSpace(req.Device) != "" {
			params["device"] = req.Device
		}
		signed, err := EpaySignChannelParams(channel, params)
		if err != nil {
			return nil, err
		}
		values := url.Values{}
		for key, value := range signed {
			values.Set(key, value)
		}
		submitPath := "/submit.php"
		if channel.IsRSA() {
			submitPath = "/api/pay/submit"
		}
		result := &AdtyEpayCreateResult{Mode: "submit", PayURL: adtyEpayEndpoint(channel, submitPath) + "?" + values.Encode()}
		if err := adtyRejectUnsafeEpayLink(result); err != nil {
			return nil, err
		}
		return result, nil
	}
	if channel.IsRSA() {
		return createAdtyEpayOrderRSA(ctx, channel, params, req)
	}
	params["clientip"] = req.ClientIP
	params["device"] = req.Device
	signed, err := EpaySignChannelParams(channel, params)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	for key, value := range signed {
		form.Set(key, value)
	}
	statusCode, raw, err := adtyEpayPostForm(ctx, adtyEpayEndpoint(channel, "/mapi.php"), form, "epay mapi request failed")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Code      common.RawMessage `json:"code"`
		Msg       string            `json:"msg"`
		TradeNo   common.RawMessage `json:"trade_no"`
		PayURL    string            `json:"payurl"`
		QRCode    string            `json:"qrcode"`
		URLScheme string            `json:"urlscheme"`
	}
	if err := common.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("epay mapi returned non-JSON (http %d)", statusCode)
	}
	code := epayRawString(parsed.Code)
	if code != "1" {
		return nil, fmt.Errorf("epay mapi code %s: %s", code, parsed.Msg)
	}
	result := &AdtyEpayCreateResult{GatewayTradeNo: epayRawString(parsed.TradeNo), QRCode: parsed.QRCode, PayURL: parsed.PayURL, URLScheme: parsed.URLScheme}
	switch {
	case parsed.QRCode != "":
		result.Mode = "qrcode"
	case parsed.PayURL != "":
		result.Mode = "payurl"
	case parsed.URLScheme != "":
		result.Mode = "urlscheme"
	default:
		return nil, fmt.Errorf("epay mapi returned no payment link")
	}
	if err := adtyRejectUnsafeEpayLink(result); err != nil {
		return nil, err
	}
	return result, nil
}

// RefreshAdtyEpaySubmitURL re-signs an RSA submit-mode PayURL when its timestamp
// query parameter is missing, invalid, or >= 120 seconds old (well inside Epay's
// 300-second replay window). Non-RSA or non-submit channels are returned unchanged.
func RefreshAdtyEpaySubmitURL(channel *model.AdtyEpayChannel, method *model.AdtyEpayChannelMethod, req AdtyEpayCreateRequest, currentPayURL string) (payURL string, refreshed bool, err error) {
	if channel == nil || !channel.IsRSA() || channel.OrderMode != model.AdtyEpayOrderModeSubmit {
		return currentPayURL, false, nil
	}
	needsRefresh := true
	if parsed, parseErr := url.Parse(strings.TrimSpace(currentPayURL)); parseErr == nil && parsed != nil {
		if tsRaw := strings.TrimSpace(parsed.Query().Get("timestamp")); tsRaw != "" {
			if ts, convErr := strconv.ParseInt(tsRaw, 10, 64); convErr == nil && ts > 0 {
				age := adtyEpayNow().Unix() - ts
				if age < 120 && age > -120 {
					needsRefresh = false
				}
			}
		}
	}
	if !needsRefresh {
		return currentPayURL, false, nil
	}
	if method == nil {
		return "", false, errors.New("epay channel method is required to refresh submit URL")
	}
	res, err := CreateAdtyEpayOrder(context.Background(), channel, method, req)
	if err != nil {
		return "", false, err
	}
	return res.PayURL, true, nil
}

func createAdtyEpayOrderRSA(ctx context.Context, channel *model.AdtyEpayChannel, params map[string]string, req AdtyEpayCreateRequest) (*AdtyEpayCreateResult, error) {
	params["clientip"] = req.ClientIP
	params["device"] = req.Device
	methods := []string{"web", "jump"}
	for idx, apiMethod := range methods {
		params["method"] = apiMethod
		signed, err := EpaySignChannelParams(channel, params)
		if err != nil {
			return nil, err
		}
		form := url.Values{}
		for key, value := range signed {
			form.Set(key, value)
		}
		statusCode, raw, err := adtyEpayPostForm(ctx, adtyEpayEndpoint(channel, "/api/pay/create"), form, "epay api request failed")
		if err != nil {
			return nil, err
		}
		fields, err := EpayVerifyRSAResponse(channel, raw)
		if err != nil {
			var apiErr *epayRSAAPIError
			if errors.As(err, &apiErr) {
				return nil, apiErr
			}
			if statusCode != http.StatusOK {
				return nil, fmt.Errorf("epay api http %d", statusCode)
			}
			return nil, err
		}
		payType := strings.ToLower(strings.TrimSpace(fields["pay_type"]))
		// Official Rainbow Epay v2 returns pay_type="html" for form-based plugins under method="web".
		// Retry once with method="jump" using the same out_trade_no to obtain a safe redirect URL.
		if payType == "html" && idx == 0 {
			continue
		}
		result := &AdtyEpayCreateResult{GatewayTradeNo: fields["trade_no"]}
		payInfo := strings.TrimSpace(fields["pay_info"])
		if payInfo != "" {
			switch payType {
			case "qrcode":
				result.Mode = "qrcode"
				result.QRCode = payInfo
			case "jump", "payurl", "url":
				result.Mode = "payurl"
				result.PayURL = payInfo
			case "urlscheme":
				result.Mode = "urlscheme"
				result.URLScheme = payInfo
			}
		}
		if result.Mode == "" {
			switch {
			case fields["qrcode"] != "":
				result.Mode = "qrcode"
				result.QRCode = fields["qrcode"]
			case fields["payurl"] != "":
				result.Mode = "payurl"
				result.PayURL = fields["payurl"]
			case fields["urlscheme"] != "":
				result.Mode = "urlscheme"
				result.URLScheme = fields["urlscheme"]
			case payType != "":
				return nil, fmt.Errorf("epay api returned unsupported pay_type %s", payType)
			default:
				return nil, fmt.Errorf("epay api returned no payment link")
			}
		}
		if err := adtyRejectUnsafeEpayLink(result); err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, fmt.Errorf("epay api returned unsupported pay_type html")
}

// adtyDangerousEpaySchemes 不能进二维码，也不能被浏览器打开。
var adtyDangerousEpaySchemes = map[string]bool{
	"javascript": true,
	"data":       true,
	"vbscript":   true,
	"file":       true,
	"blob":       true,
}

func adtyEpayScheme(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "invalid"
	}
	return strings.ToLower(parsed.Scheme)
}

// adtyEpayOpenedLinkSafe 是会交给浏览器打开的链接：只允许 http(s) 和支付宝、微信的固定 scheme。
func adtyEpayOpenedLinkSafe(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "\\") || strings.HasPrefix(trimmed, "//") {
		return false
	}
	switch adtyEpayScheme(trimmed) {
	case "http", "https", "alipay", "alipays", "weixin":
		return true
	default:
		return false
	}
}

// adtyEpayQRSafe 是画进二维码的内容。没有 scheme 的原文可以画；危险 scheme 不行。
func adtyEpayQRSafe(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "\\") || strings.HasPrefix(trimmed, "//") {
		return false
	}
	scheme := adtyEpayScheme(trimmed)
	if scheme == "" {
		return !strings.Contains(trimmed, ":")
	}
	return scheme != "invalid" && !adtyDangerousEpaySchemes[scheme]
}

func adtyRejectUnsafeEpayLink(result *AdtyEpayCreateResult) error {
	if result == nil {
		return fmt.Errorf("epay returned an unsafe payment link")
	}
	safe := false
	switch result.Mode {
	case "qrcode":
		safe = adtyEpayQRSafe(result.QRCode)
	case "payurl", "submit":
		safe = adtyEpayOpenedLinkSafe(result.PayURL)
	case "urlscheme":
		safe = adtyEpayOpenedLinkSafe(result.URLScheme)
	}
	if !safe {
		return fmt.Errorf("epay returned an unsafe payment link")
	}
	// Validate every non-empty secondary link field as well so unsafe schemes are never persisted or returned.
	if strings.TrimSpace(result.QRCode) != "" && !adtyEpayQRSafe(result.QRCode) {
		return fmt.Errorf("epay returned an unsafe payment link")
	}
	if strings.TrimSpace(result.PayURL) != "" && !adtyEpayOpenedLinkSafe(result.PayURL) {
		return fmt.Errorf("epay returned an unsafe payment link")
	}
	if strings.TrimSpace(result.URLScheme) != "" && !adtyEpayOpenedLinkSafe(result.URLScheme) {
		return fmt.Errorf("epay returned an unsafe payment link")
	}
	return nil
}

// isEpayOrderAbsentMsg reports whether an Epay gateway error message means the
// queried order does not exist on the gateway (e.g. a submit-mode order before redirect).
func isEpayOrderAbsentMsg(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return false
	}
	for _, needle := range []string{
		"订单不存在", "订单号不存在", "无此订单", "未找到订单", "订单未找到",
		"未找到该订单", "找不到订单", "查无此单",
		"order not found", "order does not exist", "order doesn't exist", "not exist",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// QueryAdtyEpayOrder 按我们的订单号查单。MD5 走 api.php?act=order；RSA 走 POST /api/pay/query 并验签。
func QueryAdtyEpayOrder(ctx context.Context, channel *model.AdtyEpayChannel, outTradeNo string) (*AdtyEpayQueryResult, error) {
	if channel == nil || channel.Status == model.AdtyEpayStatusRevoked || !channel.HasCredentials() {
		return nil, common.NewMessage("This payment channel's credentials have been revoked")
	}
	if channel.IsRSA() {
		return queryAdtyEpayOrderRSA(ctx, channel, outTradeNo)
	}
	values := url.Values{}
	values.Set("act", "order")
	values.Set("pid", channel.MerchantId)
	values.Set("key", channel.MerchantKey)
	values.Set("out_trade_no", outTradeNo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, adtyEpayEndpoint(channel, "/api.php")+"?"+values.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("epay query request failed")
	}
	// Merchant credentials are in the query. Never forward them to a redirect,
	// including an HTTPS gateway redirecting its order lookup to plain HTTP.
	baseClient := adtyVerifiedPaymentClientFromContext(ctx)
	if baseClient == nil {
		baseClient = adtyEpayHTTPClient()
	}
	var client http.Client
	if baseClient != nil {
		client = *baseClient
	}
	if strings.EqualFold(req.URL.Scheme, "https") {
		verifiedTransport, vErr := cloneAdtyVerifiedPaymentTransport(client.Transport)
		if vErr != nil {
			return nil, vErr
		}
		defer verifiedTransport.CloseIdleConnections()
		client.Transport = verifiedTransport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		// R2-026: Never return raw *url.Error containing ?key=<MerchantKey>.
		return nil, fmt.Errorf("epay query request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("epay query http %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Code       common.RawMessage `json:"code"`
		Msg        string            `json:"msg"`
		PaidAt     common.RawMessage `json:"endtime"`
		Status     common.RawMessage `json:"status"`
		Money      common.RawMessage `json:"money"`
		Type       string            `json:"type"`
		TradeNo    common.RawMessage `json:"trade_no"`
		OutTradeNo common.RawMessage `json:"out_trade_no"`
		MerchantId common.RawMessage `json:"pid"`
	}
	if err := common.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("epay query returned non-JSON")
	}
	if epayRawString(parsed.Code) != "1" {
		if isEpayOrderAbsentMsg(parsed.Msg) {
			return nil, errEpayOrderAbsent
		}
		return nil, fmt.Errorf("epay order query did not succeed")
	}
	status := epayRawString(parsed.Status)
	result := &AdtyEpayQueryResult{
		RawStatus:      status,
		TypeCode:       parsed.Type,
		GatewayTradeNo: epayRawString(parsed.TradeNo),
		OutTradeNo:     epayRawString(parsed.OutTradeNo),
		MerchantId:     epayRawString(parsed.MerchantId),
	}
	switch status {
	case "1":
		result.Paid = true
	case "0", "2", "3", "4":
		// 0=unpaid, 2=refunded, 3=frozen, 4=preauth: not creditable as paid.
	default:
		return nil, fmt.Errorf("epay returned unknown payment status")
	}
	result.PaidAt = AdtyEpayPaidTime(epayRawString(parsed.PaidAt))
	if money := epayRawString(parsed.Money); money != "" {
		if value, err := common.ParseBoundedDecimalString(money, 18, 8); err == nil {
			result.MoneyCNY = value
		}
	}
	return result, nil
}

func queryAdtyEpayOrderRSA(ctx context.Context, channel *model.AdtyEpayChannel, outTradeNo string) (*AdtyEpayQueryResult, error) {
	signed, err := EpaySignChannelParams(channel, map[string]string{"out_trade_no": outTradeNo})
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	for key, value := range signed {
		form.Set(key, value)
	}
	statusCode, raw, err := adtyEpayPostForm(ctx, adtyEpayEndpoint(channel, "/api/pay/query"), form, "epay query request failed")
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("epay query http %d", statusCode)
	}
	fields, err := EpayVerifyRSAResponse(channel, raw)
	if err != nil {
		var apiErr *epayRSAAPIError
		if errors.As(err, &apiErr) {
			if isEpayOrderAbsentMsg(apiErr.Msg) {
				return nil, errEpayOrderAbsent
			}
			return nil, fmt.Errorf("epay order query did not succeed")
		}
		return nil, err
	}
	status := strings.TrimSpace(fields["status"])
	result := &AdtyEpayQueryResult{
		RawStatus:      status,
		TypeCode:       fields["type"],
		GatewayTradeNo: fields["trade_no"],
		OutTradeNo:     fields["out_trade_no"],
		MerchantId:     strings.TrimSpace(fields["pid"]),
	}
	switch status {
	case "1":
		result.Paid = true
	case "0", "2", "3", "4":
		// 0=unpaid, 2=refunded, 3=frozen, 4=preauth: not creditable as paid.
	default:
		return nil, fmt.Errorf("epay returned unknown payment status")
	}
	result.PaidAt = AdtyEpayPaidTime(strings.TrimSpace(fields["endtime"]))
	if money := strings.TrimSpace(fields["money"]); money != "" {
		if value, err := common.ParseBoundedDecimalString(money, 18, 8); err == nil {
			result.MoneyCNY = value
		}
	}
	return result, nil
}

// ProbeAdtyEpayChannel 探活：MD5 按 ProbeMode 调 api.php；RSA 调 /api/merchant/info 或 /api/pay/query。
// HTTP 200 并且返回 JSON 就算通（查不到订单的 code=-1 也算网关正常）。
func ProbeAdtyEpayChannel(ctx context.Context, channel *model.AdtyEpayChannel) error {
	if channel == nil || channel.Status == model.AdtyEpayStatusRevoked || !channel.HasCredentials() {
		return common.NewMessage("This payment channel's credentials have been revoked")
	}
	if channel.IsRSA() {
		path := "/api/pay/query"
		probeParams := map[string]string{"out_trade_no": "PROBE" + fmt.Sprint(adtyEpayNow().Unix())}
		if channel.ProbeMode == "query" || channel.ProbeMode == "balance" {
			path = "/api/merchant/info"
			probeParams = map[string]string{}
		}
		signed, err := EpaySignChannelParams(channel, probeParams)
		if err != nil {
			return err
		}
		form := url.Values{}
		for key, value := range signed {
			form.Set(key, value)
		}
		statusCode, raw, err := adtyEpayPostForm(ctx, adtyEpayEndpoint(channel, path), form, "epay request failed")
		if err != nil {
			return err
		}
		if statusCode != http.StatusOK {
			return fmt.Errorf("epay http %d", statusCode)
		}
		var parsed map[string]any
		if err := common.Unmarshal(raw, &parsed); err != nil {
			return fmt.Errorf("epay probe returned non-JSON")
		}
		return nil
	}
	values := url.Values{}
	values.Set("pid", channel.MerchantId)
	values.Set("key", channel.MerchantKey)
	switch channel.ProbeMode {
	case "query":
		values.Set("act", "query")
	case "balance":
		values.Set("act", "balance")
	default:
		values.Set("act", "order")
		values.Set("out_trade_no", "PROBE"+fmt.Sprint(adtyEpayNow().Unix()))
	}
	raw, err := adtyEpayGet(ctx, adtyEpayEndpoint(channel, "/api.php")+"?"+values.Encode())
	if err != nil {
		return err
	}
	var parsed map[string]any
	if err := common.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("epay probe returned non-JSON")
	}
	return nil
}

func adtyEpayPostForm(ctx context.Context, target string, form url.Values, errMsg string) (int, []byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, errors.New(errMsg)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// R2-026: Disable redirects and enforce verified TLS on Epay POST requests.
	baseClient := adtyVerifiedPaymentClientFromContext(ctx)
	if baseClient == nil {
		baseClient = adtyEpayHTTPClient()
	}
	var client http.Client
	if baseClient != nil {
		client = *baseClient
	}
	if strings.EqualFold(httpReq.URL.Scheme, "https") {
		verifiedTransport, vErr := cloneAdtyVerifiedPaymentTransport(client.Transport)
		if vErr != nil {
			return 0, nil, vErr
		}
		defer verifiedTransport.CloseIdleConnections()
		client.Transport = verifiedTransport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, nil, errors.New(errMsg)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func adtyEpayGet(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("epay request failed")
	}
	baseClient := adtyVerifiedPaymentClientFromContext(ctx)
	if baseClient == nil {
		baseClient = adtyEpayHTTPClient()
	}
	client := http.Client{Timeout: baseClient.Timeout, Transport: baseClient.Transport}
	if strings.EqualFold(req.URL.Scheme, "https") {
		verifiedTransport, vErr := cloneAdtyVerifiedPaymentTransport(client.Transport)
		if vErr != nil {
			return nil, vErr
		}
		defer verifiedTransport.CloseIdleConnections()
		client.Transport = verifiedTransport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		// R2-026: Scrub *url.Error so ?key=<MerchantKey> never leaks in errors/logs.
		return nil, fmt.Errorf("epay request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("epay http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// Accept an absolute gateway timestamp only, allowing up to 60 seconds of forward
// clock skew relative to adtyEpayNow(). An offset-free clock reading cannot
// establish payment order across gateways in different time zones.
func AdtyEpayPaidTime(value string) int64 {
	now := adtyEpayNow()
	maxAllowed := now.Add(60 * time.Second)
	trimmed := strings.TrimSpace(value)
	if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil && n > 0 && n <= maxAllowed.Unix() {
		return n
	}
	if dt, err := time.Parse(time.RFC3339, trimmed); err == nil && dt.Unix() > 0 && !dt.After(maxAllowed) {
		return dt.Unix()
	}
	return 0
}
