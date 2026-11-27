package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// 站长中心的敏感操作要重新验证身份（任务 11.6，按 AGENTS.md 的 OWASP 要求做敏感操作的再认证）：
// 改价、改加赠、删除自定义域名、发放/冻结/作废兑换码、删除发件域名、员工写操作及审批、手动给客户/团队加余额。
// 用上游的二次验证（/api/verify 拿 proof，请求带 X-Security-Proof），和 2FA、Passkey、密码同一套；
// proof 只能用一次，并绑定操作、目标以及关键金额/幂等键请求摘要，不能拿去做别的事。

// VerificationScopeAdtySiteOwnerManage 是站长敏感操作的验证范围。
const VerificationScopeAdtySiteOwnerManage = "site.owner.manage"
const VerificationScopeAdtyStaffCustomerManage = "site.staff.customer.manage"

const (
	AdtyStaffActionCustomerPasswordReset = "customer.password.reset"
	AdtyStaffActionCustomerDisable       = "customer.disable"
)

// 站长敏感操作的种类。
const (
	AdtySiteOwnerActionPricing          = "pricing"       // target：0 是全站定价，否则是客户 id
	AdtySiteOwnerActionBonus            = "bonus"         // target：站点 id
	AdtySiteOwnerActionDomain           = "domain.delete" // target：自定义域名 id
	AdtySiteOwnerActionSender           = "sender.delete" // target：站点 id
	AdtySiteOwnerActionMailConfig       = "mail.config"   // target：站点 id
	AdtySiteOwnerActionAuthConfig       = "auth.config"   // target：站点 id
	AdtySiteOwnerActionStaffCreate      = "staff.create"  // target: site ID
	AdtySiteOwnerActionStaffUpdate      = "staff.update"  // target: membership ID
	AdtySiteOwnerActionStaffPassword    = "staff.password"
	AdtySiteOwnerActionStaffDelete      = "staff.delete"
	AdtySiteOwnerActionVoucherIssue     = "voucher.issue"     // target: site ID
	AdtySiteOwnerActionVoucherFreeze    = "voucher.freeze"    // target: voucher ID
	AdtySiteOwnerActionVoucherCancel    = "voucher.cancel"    // target: voucher ID
	AdtySiteOwnerActionCustomerPassword = "customer.password" // target: customer user ID
	AdtySiteOwnerActionWithdrawalCreate = "withdrawal.create" // target: site ID
)

var adtySiteOwnerActions = []string{AdtySiteOwnerActionPricing, AdtySiteOwnerActionBonus, AdtySiteOwnerActionDomain, AdtySiteOwnerActionSender, AdtySiteOwnerActionMailConfig, AdtySiteOwnerActionAuthConfig,
	AdtySiteOwnerActionStaffCreate, AdtySiteOwnerActionStaffUpdate, AdtySiteOwnerActionStaffPassword, AdtySiteOwnerActionStaffDelete,
	AdtySiteOwnerActionVoucherIssue, AdtySiteOwnerActionVoucherFreeze, AdtySiteOwnerActionVoucherCancel,
	AdtySiteOwnerActionCustomerPassword, AdtySiteOwnerActionWithdrawalCreate}

// AdtySiteOwnerContext 是 proof 绑定的操作：哪种操作、对哪个目标，员工写操作及资金操作还会绑定请求体摘要。
type AdtySiteOwnerContext struct {
	Action string `json:"action"`
	Target int    `json:"target"`
	Digest string `json:"digest,omitempty"`
}

// AdtyOwnerVoucherIssueDigestInput is the canonical digest payload for
// voucher.issue step-up verification (R1-44).
type AdtyOwnerVoucherIssueDigestInput struct {
	Purpose   string `json:"purpose"`
	AmountUSD string `json:"amount_usd"`
	Count     int    `json:"count"`
	Source    string `json:"source,omitempty"`
	Reason    string `json:"reason,omitempty"`
	IntentKey string `json:"intent_key"`
}

// AdtyOwnerWithdrawalDigestInput is the canonical digest payload for
// withdrawal.create step-up verification (R4-032).
type AdtyOwnerWithdrawalDigestInput struct {
	AmountUSD      string `json:"amount_usd"`
	Method         string `json:"method"`
	Network        string `json:"network"`
	AccountNo      string `json:"account_no"`
	AccountName    string `json:"account_name"`
	QRImageURL     string `json:"qr_image_url,omitempty"`
	ExpectedRate   string `json:"expected_rate"`
	IdempotencyKey string `json:"idempotency_key"`
}

func adtyIsSiteOwnerStaffAction(action string) bool {
	switch action {
	case AdtySiteOwnerActionStaffCreate, AdtySiteOwnerActionStaffUpdate, AdtySiteOwnerActionStaffPassword, AdtySiteOwnerActionStaffDelete:
		return true
	default:
		return false
	}
}

func adtyIsSiteOwnerDigestRequiredAction(action string) bool {
	if adtyIsSiteOwnerStaffAction(action) {
		return true
	}
	switch action {
	case AdtySiteOwnerActionVoucherIssue, AdtySiteOwnerActionWithdrawalCreate:
		return true
	default:
		return false
	}
}

func isAdtyHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

func normalizeAdtyStaffInputForDigest(input AdtyStaffInput) AdtyStaffInput {
	rv := reflect.ValueOf(&input).Elem()
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Field(i)
		if field.Kind() == reflect.Slice && field.Len() == 0 && !field.IsNil() {
			field.Set(reflect.Zero(field.Type()))
		}
	}
	return input
}

func normalizeAdtyOwnerVoucherIssueDigestInput(in AdtyOwnerVoucherIssueDigestInput) AdtyOwnerVoucherIssueDigestInput {
	amt := strings.TrimSpace(in.AmountUSD)
	if d, err := common.ParseBoundedDecimalString(amt, 12, 8); err == nil {
		amt = d.String()
	}
	count := in.Count
	if count <= 0 {
		count = 1
	}
	return AdtyOwnerVoucherIssueDigestInput{
		Purpose:   strings.TrimSpace(in.Purpose),
		AmountUSD: amt,
		Count:     count,
		Source:    strings.TrimSpace(in.Source),
		Reason:    strings.TrimSpace(in.Reason),
		IntentKey: strings.TrimSpace(in.IntentKey),
	}
}

func normalizeAdtyOwnerWithdrawalDigestInput(in AdtyOwnerWithdrawalDigestInput) AdtyOwnerWithdrawalDigestInput {
	amt := strings.TrimSpace(in.AmountUSD)
	if d, err := common.ParseBoundedDecimalString(amt, 12, 8); err == nil {
		amt = d.String()
	}
	rate := strings.TrimSpace(in.ExpectedRate)
	if d, err := common.ParseBoundedDecimalString(rate, 12, 8); err == nil {
		rate = d.String()
	}
	method := strings.ToLower(strings.TrimSpace(in.Method))
	network := ""
	if method == "usdt" {
		network = strings.ToUpper(strings.TrimSpace(in.Network))
	}
	qrImageURL := ""
	if method == "alipay" {
		qrImageURL = strings.TrimSpace(in.QRImageURL)
		if canonicalQR, ok := CanonicalReferralAssetURL(qrImageURL); ok {
			qrImageURL = canonicalQR
		}
	}
	return AdtyOwnerWithdrawalDigestInput{
		AmountUSD:      amt,
		Method:         method,
		Network:        network,
		AccountNo:      strings.TrimSpace(in.AccountNo),
		AccountName:    strings.TrimSpace(in.AccountName),
		QRImageURL:     qrImageURL,
		ExpectedRate:   rate,
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
	}
}

// AdtyOwnerActionRequestDigest computes the canonical SHA-256 hex digest for
// site-owner actions that bind payload parameters to their step-up proof (R1-018).
func AdtyOwnerActionRequestDigest(action string, v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		trimmed := strings.TrimSpace(s)
		if isAdtyHexDigest(trimmed) {
			return strings.ToLower(trimmed)
		}
	}
	if adtyIsSiteOwnerStaffAction(action) {
		return AdtyStaffRequestDigest(v)
	}
	var rawBytes []byte
	switch val := v.(type) {
	case string:
		rawBytes = []byte(strings.TrimSpace(val))
	case []byte:
		rawBytes = bytes.TrimSpace(val)
	case json.RawMessage:
		rawBytes = bytes.TrimSpace(val)
	default:
		b, err := common.Marshal(val)
		if err != nil {
			return ""
		}
		rawBytes = b
	}
	if len(rawBytes) == 0 {
		return ""
	}
	switch action {
	case AdtySiteOwnerActionVoucherIssue:
		var in AdtyOwnerVoucherIssueDigestInput
		if common.Unmarshal(rawBytes, &in) != nil {
			sum := sha256.Sum256(rawBytes)
			return hex.EncodeToString(sum[:])
		}
		in = normalizeAdtyOwnerVoucherIssueDigestInput(in)
		canonical, err := common.Marshal(in)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(canonical)
		return hex.EncodeToString(sum[:])
	case AdtySiteOwnerActionWithdrawalCreate:
		var in AdtyOwnerWithdrawalDigestInput
		if common.Unmarshal(rawBytes, &in) != nil {
			sum := sha256.Sum256(rawBytes)
			return hex.EncodeToString(sum[:])
		}
		in = normalizeAdtyOwnerWithdrawalDigestInput(in)
		canonical, err := common.Marshal(in)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(canonical)
		return hex.EncodeToString(sum[:])
	default:
		sum := sha256.Sum256(rawBytes)
		return hex.EncodeToString(sum[:])
	}
}

// AdtyStaffRequestDigest computes the canonical SHA-256 hex digest of a staff mutation payload.
func AdtyStaffRequestDigest(v any) string {
	if v == nil {
		return ""
	}
	var input AdtyStaffInput
	switch val := v.(type) {
	case AdtyStaffInput:
		input = val
	case *AdtyStaffInput:
		if val == nil {
			return ""
		}
		input = *val
	case string:
		trimmed := strings.TrimSpace(val)
		if trimmed == "" {
			return ""
		}
		if isAdtyHexDigest(trimmed) {
			return strings.ToLower(trimmed)
		}
		if common.Unmarshal([]byte(trimmed), &input) != nil {
			sum := sha256.Sum256([]byte(trimmed))
			return hex.EncodeToString(sum[:])
		}
	case []byte:
		trimmed := bytes.TrimSpace(val)
		if len(trimmed) == 0 {
			return ""
		}
		if common.Unmarshal(trimmed, &input) != nil {
			sum := sha256.Sum256(trimmed)
			return hex.EncodeToString(sum[:])
		}
	case json.RawMessage:
		trimmed := bytes.TrimSpace(val)
		if len(trimmed) == 0 {
			return ""
		}
		if common.Unmarshal(trimmed, &input) != nil {
			sum := sha256.Sum256(trimmed)
			return hex.EncodeToString(sum[:])
		}
	default:
		raw, err := common.Marshal(val)
		if err != nil {
			return ""
		}
		if common.Unmarshal(raw, &input) != nil {
			sum := sha256.Sum256(raw)
			return hex.EncodeToString(sum[:])
		}
	}
	input = normalizeAdtyStaffInputForDigest(input)
	raw, err := common.Marshal(input)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func adtyIsSiteOwnerScope(scope string) bool {
	return scope == VerificationScopeAdtySiteOwnerManage
}

func adtyIsSiteOwnerManagementScope(scope string) bool {
	return scope == VerificationScopeAdtySiteOwnerManage ||
		scope == VerificationScopeAdtyOwnerOrderRefund ||
		scope == VerificationScopeAdtyOwnerOrderRefundStep
}

func adtyIsSiteVerificationScope(scope string) bool {
	return adtyIsSiteOwnerManagementScope(scope) || scope == VerificationScopeAdtyStaffCustomerManage
}

// adtyBindSiteOwnerOperation 校验并规范化站长操作的上下文（BindVerificationOperation 调用）。
func adtyBindSiteOwnerOperation(scope string, fields map[string]json.RawMessage, raw json.RawMessage) (any, bool) {
	if !adtyIsSiteVerificationScope(scope) {
		return nil, false
	}
	if fields["action"] == nil || fields["target"] == nil {
		return nil, false
	}
	for k := range fields {
		if k != "action" && k != "target" && k != "digest" && k != "request" {
			return nil, false
		}
	}
	var wire struct {
		Action  string          `json:"action"`
		Target  int             `json:"target"`
		Digest  string          `json:"digest,omitempty"`
		Request json.RawMessage `json:"request,omitempty"`
	}
	actions := adtySiteOwnerActions
	if scope == VerificationScopeAdtyStaffCustomerManage {
		actions = []string{AdtyStaffActionCustomerDisable}
	}
	if common.Unmarshal(raw, &wire) != nil || !slices.Contains(actions, wire.Action) || wire.Target < 0 {
		return nil, false
	}
	if wire.Target == 0 && wire.Action != AdtySiteOwnerActionPricing {
		return nil, false
	}
	digest := strings.TrimSpace(wire.Digest)
	if digest != "" {
		if !isAdtyHexDigest(digest) {
			return nil, false
		}
		digest = strings.ToLower(digest)
	}
	if len(bytes.TrimSpace(wire.Request)) > 0 {
		reqDigest := AdtyOwnerActionRequestDigest(wire.Action, wire.Request)
		if reqDigest == "" || (digest != "" && digest != reqDigest) {
			return nil, false
		}
		digest = reqDigest
	}
	if adtyIsSiteOwnerDigestRequiredAction(wire.Action) {
		if digest == "" {
			return nil, false
		}
	} else if digest != "" || fields["digest"] != nil || fields["request"] != nil {
		return nil, false
	}
	return AdtySiteOwnerContext{Action: wire.Action, Target: wire.Target, Digest: digest}, true
}

// AdtySiteOwnerOperation 构造要验证的操作，controller 交给 middleware.RequireSecurityProof。
func AdtySiteOwnerOperation(action string, target int, request ...any) VerificationOperation {
	ctx := AdtySiteOwnerContext{Action: action, Target: target}
	if len(request) > 0 && request[0] != nil {
		ctx.Digest = AdtyOwnerActionRequestDigest(action, request[0])
	}
	payload, _ := common.Marshal(ctx)
	return VerificationOperation{Scope: VerificationScopeAdtySiteOwnerManage, Context: payload}
}

func AdtyStaffCustomerOperation(action string, target int) VerificationOperation {
	payload, _ := common.Marshal(AdtySiteOwnerContext{Action: action, Target: target})
	return VerificationOperation{Scope: VerificationScopeAdtyStaffCustomerManage, Context: payload}
}

func adtySiteOwnerActionAccessTokenScope(ctx AdtySiteOwnerContext) string {
	switch ctx.Action {
	case AdtySiteOwnerActionCustomerPassword:
		return AccessTokenResourceSiteOwnerCustomer + ":write"
	case AdtySiteOwnerActionPricing:
		if ctx.Target > 0 {
			return AccessTokenResourceSiteOwnerCustomer + ":write"
		}
		return AccessTokenResourceSiteOwnerSetting + ":write"
	case AdtySiteOwnerActionBonus, AdtySiteOwnerActionVoucherIssue, AdtySiteOwnerActionVoucherFreeze, AdtySiteOwnerActionVoucherCancel, AdtySiteOwnerActionWithdrawalCreate:
		return AccessTokenResourceSiteOwnerOrder + ":write"
	case AdtySiteOwnerActionDomain, AdtySiteOwnerActionSender, AdtySiteOwnerActionMailConfig, AdtySiteOwnerActionAuthConfig:
		return AccessTokenResourceSiteOwnerSetting + ":write"
	default:
		// staff.* actions require a browser session, never a PAT (R2-004).
		return ""
	}
}

func init() {
	// R1-016 / R2-004: Site owner step-up proofs require dedicated site_owner_*:write scopes,
	// resolved per action/target during verification and consumption.
	accessTokenVerificationScopes[VerificationScopeAdtySiteOwnerManage] = AccessTokenResourceSiteOwnerSetting + ":write"
	// Staff customer security actions require a browser session, never a PAT.
	accessTokenVerificationScopes[VerificationScopeAdtyStaffCustomerManage] = ""
}
