package service

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
)

// 从旧项目移植并改写（任务 4.1）：按 USD 下单，token 是收款地址，expiration_time 是剩余秒数，
// 签名照 BEpusdt 的 EpusdtSign：参数按键名排序、跳过 signature 和空值、值用 %v 拼接、末尾接密钥取 md5。

const bepusdtTimeoutSeconds = 900

// BEpusdtCreateRequest 是下单参数。
type BEpusdtCreateRequest struct {
	VerifyOrder bool
	OrderID     string
	AmountUSD   decimal.Decimal
	TradeType   string
	NotifyURL   string
	RedirectURL string
	Name        string
	Timeout     int // 秒，官方允许范围 180..3600
}

// BEpusdtOrder 是网关返回的订单。
type BEpusdtOrder struct {
	TradeId      string
	Address      string
	ActualAmount decimal.Decimal
	ExpireAt     int64 // 收到响应的时间 + expiration_time
	PaymentURL   string
}

// BEpusdtGatewayError 是网关返回的业务错误（status_code 不是 200）。
type BEpusdtGatewayError struct {
	StatusCode int
	Message    string
}

func (err BEpusdtGatewayError) Error() string {
	return fmt.Sprintf("bepusdt status %d: %s", err.StatusCode, err.Message)
}

var (
	errBEpusdtNotConfigured = errors.New("bepusdt is not configured")
	errBEpusdtChainDisabled = errors.New("bepusdt chain is not enabled")
	// bepusdtHTTPClient 默认用全局客户端，测试可以替换。
	bepusdtHTTPClient = func() *http.Client {
		if client := GetHttpClient(); client != nil {
			return client
		}
		return http.DefaultClient
	}
	bepusdtNow = func() time.Time { return time.Now() }
)

// BEpusdtSign 计算签名。values 里的数字要是 float64（JSON 解码出来的样子），和网关的 %v 一致。
func BEpusdtSign(values map[string]any, secret string) string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if key == "signature" || value == nil {
			continue
		}
		if text, ok := value.(string); ok && text == "" {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var b strings.Builder
	for i, key := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		fmt.Fprintf(&b, "%s=%v", key, values[key])
	}
	b.WriteString(secret)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// VerifyBEpusdtSignature 用当前密钥验签，常量时间比较。
func VerifyBEpusdtSignature(values map[string]any) bool {
	secret := strings.TrimSpace(setting.GetBEpusdtSetting().Secret)
	got, _ := values["signature"].(string)
	if secret == "" || got == "" {
		return false
	}
	want := BEpusdtSign(values, secret)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(got)), []byte(want)) == 1
}

// bepusdtSignedBody 把请求体做一次 JSON 往返，按解出来的 map 签名，再把签名放回去。
func bepusdtSignedBody(body map[string]any, secret string) ([]byte, error) {
	raw, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if err := common.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	body["signature"] = BEpusdtSign(values, secret)
	return common.Marshal(body)
}

type bepusdtResponse struct {
	StatusCode int               `json:"status_code"`
	Message    string            `json:"message"`
	Data       common.RawMessage `json:"data"`
}

func bepusdtPost(ctx context.Context, path string, body map[string]any) (common.RawMessage, error) {
	return bepusdtPostWithConfig(ctx, path, body, true)
}

func bepusdtPostWithConfig(ctx context.Context, path string, body map[string]any, requireEnabled bool) (common.RawMessage, error) {
	cfg := setting.GetBEpusdtSetting()
	if requireEnabled && !cfg.Enabled {
		return nil, errBEpusdtNotConfigured
	}
	baseURL, err := setting.ValidateBEpusdtBaseURL(cfg.BaseURL)
	secret := strings.TrimSpace(cfg.Secret)
	if err != nil || baseURL == "" || secret == "" {
		return nil, errBEpusdtNotConfigured
	}
	payload, err := bepusdtSignedBody(body, secret)
	if err != nil {
		return nil, err
	}
	targetURL := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return nil, common.SanitizeOutboundError(err, secret)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// R3-001: Never follow redirects and enforce verified TLS on BEpusdt order create/cancel.
	baseClient := bepusdtHTTPClient()
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
	resp, err := client.Do(req)
	if err != nil {
		return nil, common.SanitizeOutboundError(err, secret)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, common.SanitizeOutboundError(err, secret)
	}
	var parsed bepusdtResponse
	if err := common.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("bepusdt %s: http %d, not json", path, resp.StatusCode)
	}
	if parsed.StatusCode != http.StatusOK {
		return nil, BEpusdtGatewayError{StatusCode: parsed.StatusCode, Message: parsed.Message}
	}
	return parsed.Data, nil
}

// CreateBEpusdtOrder 下单：fiat=USD、rate="1"（字符串），amount 和 timeout 是数字。
func CreateBEpusdtOrder(req BEpusdtCreateRequest) (*BEpusdtOrder, error) {
	return CreateBEpusdtOrderContext(context.Background(), req)
}

// CreateBEpusdtOrderContext 使用调用方 ctx 下单；ctx 为空或未设超时时默认 15 秒超时。
func CreateBEpusdtOrderContext(ctx context.Context, req BEpusdtCreateRequest) (*BEpusdtOrder, error) {
	if !setting.BEpusdtChainEnabled(req.TradeType) {
		return nil, errBEpusdtChainDisabled
	}
	timeout := req.Timeout
	if timeout < 180 {
		timeout = bepusdtTimeoutSeconds
	} else if timeout > 3600 {
		timeout = 3600
	}
	amount, _ := req.AmountUSD.Float64()
	body := map[string]any{
		"order_id":     req.OrderID,
		"amount":       amount,
		"fiat":         "USD",
		"trade_type":   req.TradeType,
		"timeout":      timeout,
		"rate":         "1",
		"notify_url":   req.NotifyURL,
		"redirect_url": req.RedirectURL,
	}
	if req.Name != "" {
		body["name"] = req.Name
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	data, err := bepusdtPostWithConfig(ctx, "/api/v1/order/create-transaction", body, true)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		OrderID        string            `json:"order_id"`
		TradeType      string            `json:"trade_type"`
		Fiat           string            `json:"fiat"`
		Money          common.RawMessage `json:"amount"`
		TradeId        string            `json:"trade_id"`
		ActualAmount   common.RawMessage `json:"actual_amount"`
		Token          string            `json:"token"`
		ExpirationTime int64             `json:"expiration_time"`
		PaymentURL     string            `json:"payment_url"`
	}
	if err := common.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	if req.VerifyOrder {
		money, err := decimalFromJSON(parsed.Money)
		if err != nil || !money.Equal(req.AmountUSD) || parsed.OrderID != req.OrderID ||
			(parsed.TradeType != "" && parsed.TradeType != req.TradeType) ||
			(parsed.Fiat != "" && parsed.Fiat != "USD") {
			return nil, fmt.Errorf("bepusdt returned a different order")
		}
	}
	actual, err := decimalFromJSON(parsed.ActualAmount)
	if err != nil || !actual.IsPositive() || parsed.Token == "" || parsed.TradeId == "" {
		return nil, fmt.Errorf("bepusdt returned an incomplete order")
	}
	expires := parsed.ExpirationTime
	if expires <= 0 {
		expires = int64(timeout)
	}
	return &BEpusdtOrder{
		TradeId:      parsed.TradeId,
		Address:      parsed.Token,
		ActualAmount: actual,
		ExpireAt:     bepusdtNow().Unix() + expires,
		PaymentURL:   parsed.PaymentURL,
	}, nil
}

// CancelBEpusdtOrder 取消等待支付的订单。
// 停用新单（cfg.Enabled=false）不应阻断历史待支付订单的取消，仍要求有效网关地址与密钥。
// 仅当网关成功取消、或明确返回「已取消/已过期/不存在」、或状态不允许取消时经 HTTPS /api/v1/pay/info
// 核实为终态未付（3/4/6）时返回 nil；检测到已支付或链上确认中（2/5）时返回 ErrAdtyPaymentDetected；
// 其余网络超时、HTTP 非 HTTPS 查单或未知网关错误原样返回，禁止本地误取消并释放分站保障预留（A.8 / R2-019）。
func CancelBEpusdtOrder(tradeId string) error {
	if strings.TrimSpace(tradeId) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := bepusdtPostWithConfig(ctx, "/api/v1/order/cancel-transaction", map[string]any{"trade_id": tradeId}, false)
	if err == nil {
		return nil
	}
	if bepusdtPaymentDetected(err) {
		return fmt.Errorf("%w: %s", ErrAdtyPaymentDetected, err.Error())
	}
	if bepusdtStatusDisallowsCancel(err) {
		queryCtx, queryCancel := context.WithTimeout(context.Background(), 8*time.Second)
		status, ok := queryBEpusdtOrderStatus(queryCtx, tradeId)
		queryCancel()
		if ok {
			switch status {
			case 2, 5:
				return fmt.Errorf("%w: %s", ErrAdtyPaymentDetected, err.Error())
			case 3, 4, 6:
				return nil
			}
		}
		return err
	}
	if bepusdtAlreadyCanceled(err) {
		return nil
	}
	return err
}

func bepusdtStatusDisallowsCancel(err error) bool {
	var gateway BEpusdtGatewayError
	if !errors.As(err, &gateway) {
		return false
	}
	msg := strings.TrimSpace(gateway.Message)
	lower := strings.ToLower(msg)
	return strings.Contains(msg, "状态不允许取消") || strings.Contains(lower, "status does not allow")
}

// queryBEpusdtOrderStatus queries BEpusdt's public /api/v1/pay/info over verified
// HTTPS only (plain HTTP gateways cannot authenticate unsigned status responses).
func queryBEpusdtOrderStatus(ctx context.Context, tradeId string) (int, bool) {
	cfg := setting.GetBEpusdtSetting()
	endpoint, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || endpoint == nil || endpoint.Scheme != "https" || strings.TrimSpace(endpoint.Host) == "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return 0, false
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/pay/info"
	endpoint.RawPath = ""
	payload, err := common.Marshal(map[string]string{"trade_id": tradeId})
	if err != nil {
		return 0, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return 0, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	baseClient := adtyVerifiedPaymentClientFromContext(ctx)
	if baseClient == nil {
		baseClient = bepusdtHTTPClient()
	}
	var client http.Client
	if baseClient != nil {
		client = *baseClient
	}
	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	if tr, ok := baseTransport.(*http.Transport); !ok || tr.DialTLS != nil || tr.DialTLSContext != nil {
		return 0, false
	}
	verifiedTransport, vErr := cloneAdtyVerifiedPaymentTransport(baseTransport)
	if vErr != nil {
		return 0, false
	}
	verifiedTransport.TLSClientConfig.ServerName = ""
	client.Transport = verifiedTransport
	defer verifiedTransport.CloseIdleConnections()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 || client.Timeout > 10*time.Second {
		client.Timeout = 10 * time.Second
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return 0, false
	}
	var parsed struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			TradeID string `json:"trade_id"`
			Status  *int   `json:"status"`
		} `json:"data"`
	}
	if common.Unmarshal(raw, &parsed) != nil || parsed.StatusCode != http.StatusOK ||
		parsed.Data.TradeID != tradeId || parsed.Data.Status == nil ||
		*parsed.Data.Status < 1 || *parsed.Data.Status > 6 {
		return 0, false
	}
	return *parsed.Data.Status, true
}

func bepusdtAlreadyCanceled(err error) bool {
	var gateway BEpusdtGatewayError
	if !errors.As(err, &gateway) {
		return false
	}
	msg := strings.TrimSpace(gateway.Message)
	lower := strings.ToLower(msg)
	// R3-012: Reject negation/failure phrases first so "cannot cancel" or "cancel failed"
	// is never misclassified as an already-canceled terminal order.
	for _, neg := range []string{"cannot", "can't", "unable", "fail", "error", "denied", "refuse", "invalid", "无法", "不能", "失败", "错误"} {
		if strings.Contains(lower, neg) || strings.Contains(msg, neg) {
			return false
		}
	}
	for _, phrase := range []string{"already cancel", "canceled", "cancelled", "expired", "not found", "not exist", "closed"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return strings.Contains(msg, "已取消") || strings.Contains(msg, "已过期") || strings.Contains(msg, "不存在") || strings.Contains(msg, "已关闭")
}

// bepusdtPaymentDetected 只认网关原文里的肯定到账说法，排除 unpaid / not paid / 未转账 等否定表达。
func bepusdtPaymentDetected(err error) bool {
	var gateway BEpusdtGatewayError
	if !errors.As(err, &gateway) {
		return false
	}
	msg := strings.TrimSpace(gateway.Message)
	lower := strings.ToLower(msg)
	if bepusdtContainsAffirmativeWord(lower, "paid") || bepusdtContainsAffirmativeWord(lower, "transfer") {
		return true
	}
	if strings.Contains(msg, "已支付") || strings.Contains(msg, "支付成功") {
		return true
	}
	if strings.Contains(msg, "转账") && !strings.Contains(msg, "未转账") && !strings.Contains(msg, "没有转账") && !strings.Contains(msg, "无转账") {
		return true
	}
	return false
}

func bepusdtContainsAffirmativeWord(lower, word string) bool {
	search := lower
	offset := 0
	for {
		idx := strings.Index(search, word)
		if idx < 0 {
			return false
		}
		absIdx := offset + idx
		// Reject word embedded inside a negative prefix like "unpaid".
		prevIsLetter := absIdx > 0 && lower[absIdx-1] >= 'a' && lower[absIdx-1] <= 'z'
		if !prevIsLetter {
			prefix := strings.TrimRight(lower[:absIdx], " \t\r\n-:;,")
			negated := false
			for _, neg := range []string{"not", "not yet", "never", "no", "non", "without"} {
				if prefix == neg || (strings.HasSuffix(prefix, neg) && len(prefix) > len(neg) && !(prefix[len(prefix)-len(neg)-1] >= 'a' && prefix[len(prefix)-len(neg)-1] <= 'z')) {
					negated = true
					break
				}
			}
			if !negated {
				return true
			}
		}
		step := idx + len(word)
		offset += step
		search = search[step:]
	}
}

// decimalFromJSON 接受 JSON 字符串或数字（有界十进制防大指数 DoS，R7-03）。
func decimalFromJSON(raw common.RawMessage) (decimal.Decimal, error) {
	text := strings.TrimSpace(string(raw))
	text = strings.Trim(text, `"`)
	return common.ParseBoundedDecimalString(text, 18, 8)
}

// AdtyDecimalFromAny 把回调里的值（float64 或字符串）转成 decimal。float64 按最短表示转换。
func AdtyDecimalFromAny(value any) (decimal.Decimal, error) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return decimal.Zero, fmt.Errorf("not a finite number")
		}
		return common.ParseBoundedDecimalString(strconv.FormatFloat(typed, 'f', -1, 64), 18, 8)
	case string:
		return common.ParseBoundedDecimalString(typed, 18, 8)
	case common.Number:
		return common.ParseBoundedDecimalString(typed.String(), 18, 8)
	}
	return decimal.Zero, fmt.Errorf("not a number: %v", value)
}
