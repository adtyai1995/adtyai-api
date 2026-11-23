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
	"net/http"
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
	Timeout     int // 秒，最低 120
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
	StatusCode int             `json:"status_code"`
	Message    string          `json:"message"`
	Data       common.RawMessage `json:"data"`
}

func bepusdtPost(ctx context.Context, path string, body map[string]any) (common.RawMessage, error) {
	cfg := setting.GetBEpusdtSetting()
	if !setting.BEpusdtConfigured() {
		return nil, errBEpusdtNotConfigured
	}
	payload, err := bepusdtSignedBody(body, strings.TrimSpace(cfg.Secret))
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := bepusdtHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
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
	if !setting.BEpusdtChainEnabled(req.TradeType) {
		return nil, errBEpusdtChainDisabled
	}
	timeout := req.Timeout
	if timeout < 120 {
		timeout = bepusdtTimeoutSeconds
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	data, err := bepusdtPost(ctx, "/api/v1/order/create-transaction", body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		OrderID        string          `json:"order_id"`
		TradeType      string          `json:"trade_type"`
		Fiat           string          `json:"fiat"`
		Money          common.RawMessage `json:"amount"`
		TradeId        string            `json:"trade_id"`
		ActualAmount   common.RawMessage `json:"actual_amount"`
		Token          string          `json:"token"`
		ExpirationTime int64           `json:"expiration_time"`
		PaymentURL     string          `json:"payment_url"`
	}
	if err := common.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	if req.VerifyOrder {
		money, err := decimalFromJSON(parsed.Money)
		if err != nil || !money.Equal(req.AmountUSD) || parsed.Fiat != "USD" || parsed.OrderID != req.OrderID || parsed.TradeType != req.TradeType {
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
// 仅当网关成功取消、或明确返回「已取消/已过期/不存在」时返回 nil；
// 检测到已转账/已支付时返回 ErrAdtyPaymentDetected；
// 其余网络超时、5xx 或未知网关错误原样返回，禁止本地误取消并释放分站保障预留（A.8 / R2-019）。
func CancelBEpusdtOrder(tradeId string) error {
	if strings.TrimSpace(tradeId) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := bepusdtPost(ctx, "/api/v1/order/cancel-transaction", map[string]any{"trade_id": tradeId})
	if err == nil {
		return nil
	}
	if bepusdtPaymentDetected(err) {
		return fmt.Errorf("%w: %s", ErrAdtyPaymentDetected, err.Error())
	}
	if bepusdtAlreadyCanceled(err) {
		return nil
	}
	return err
}

func bepusdtAlreadyCanceled(err error) bool {
	var gateway BEpusdtGatewayError
	if !errors.As(err, &gateway) {
		return false
	}
	lower := strings.ToLower(gateway.Message)
	if strings.Contains(lower, "cancel") || strings.Contains(lower, "expired") || strings.Contains(lower, "not found") || strings.Contains(lower, "not exist") {
		return true
	}
	return strings.Contains(gateway.Message, "已取消") || strings.Contains(gateway.Message, "已过期") || strings.Contains(gateway.Message, "不存在") || strings.Contains(gateway.Message, "已关闭")
}

// bepusdtPaymentDetected 只认网关原文里的到账说法。状态码本身分不清已支付、已取消和网关故障。
func bepusdtPaymentDetected(err error) bool {
	var gateway BEpusdtGatewayError
	if !errors.As(err, &gateway) {
		return false
	}
	lower := strings.ToLower(gateway.Message)
	if strings.Contains(lower, "transfer") || strings.Contains(lower, "paid") {
		return true
	}
	return strings.Contains(gateway.Message, "已支付") || strings.Contains(gateway.Message, "转账") || strings.Contains(gateway.Message, "支付成功")
}

// decimalFromJSON 接受 JSON 字符串或数字。
func decimalFromJSON(raw common.RawMessage) (decimal.Decimal, error) {
	text := strings.TrimSpace(string(raw))
	text = strings.Trim(text, `"`)
	return decimal.NewFromString(text)
}

// AdtyDecimalFromAny 把回调里的值（float64 或字符串）转成 decimal。float64 按最短表示转换。
func AdtyDecimalFromAny(value any) (decimal.Decimal, error) {
	switch typed := value.(type) {
	case float64:
		return decimal.NewFromString(strconv.FormatFloat(typed, 'f', -1, 64))
	case string:
		return decimal.NewFromString(strings.TrimSpace(typed))
	case common.Number:
		return decimal.NewFromString(typed.String())
	}
	return decimal.Zero, fmt.Errorf("not a number: %v", value)
}
