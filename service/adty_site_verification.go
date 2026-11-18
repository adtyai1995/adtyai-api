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
// 改价、改加赠、送试用额度、删除自定义域名、新建修改删除优惠码、删除发件域名。用上游的二次验证（/api/verify 拿 proof，请求带 X-Security-Proof），
// 和 2FA、Passkey、密码同一套；proof 只能用一次，并绑定操作和目标，不能拿去做别的事。

// VerificationScopeAdtySiteOwnerManage 是站长敏感操作的验证范围。
const VerificationScopeAdtySiteOwnerManage = "site.owner.manage"
const VerificationScopeAdtyStaffCustomerManage = "site.staff.customer.manage"

const (
	AdtyStaffActionCustomerPasswordReset = "customer.password.reset"
	AdtyStaffActionCustomerDisable       = "customer.disable"
)

// 站长敏感操作的种类。
const (
	AdtySiteOwnerActionPricing       = "pricing"       // target：0 是全站定价，否则是客户 id
	AdtySiteOwnerActionBonus         = "bonus"         // target：站点 id
	AdtySiteOwnerActionTrial         = "trial"         // target：客户 id
	AdtySiteOwnerActionDomain        = "domain.delete" // target：自定义域名 id
	AdtySiteOwnerActionSender        = "sender.delete" // target：站点 id
	AdtySiteOwnerActionStaffCreate   = "staff.create"  // target: site ID
	AdtySiteOwnerActionStaffUpdate   = "staff.update"  // target: membership ID
	AdtySiteOwnerActionStaffPassword = "staff.password"
	AdtySiteOwnerActionStaffDelete   = "staff.delete"
	AdtySiteOwnerActionVoucherIssue  = "voucher.issue"  // target: site ID
	AdtySiteOwnerActionVoucherFreeze = "voucher.freeze" // target: voucher ID
	AdtySiteOwnerActionVoucherCancel = "voucher.cancel" // target: voucher ID
)

var adtySiteOwnerActions = []string{AdtySiteOwnerActionPricing, AdtySiteOwnerActionBonus, AdtySiteOwnerActionTrial, AdtySiteOwnerActionDomain, AdtySiteOwnerActionSender,
	AdtySiteOwnerActionStaffCreate, AdtySiteOwnerActionStaffUpdate, AdtySiteOwnerActionStaffPassword, AdtySiteOwnerActionStaffDelete,
	AdtySiteOwnerActionVoucherIssue, AdtySiteOwnerActionVoucherFreeze, AdtySiteOwnerActionVoucherCancel}

// AdtySiteOwnerContext 是 proof 绑定的操作：哪种操作、对哪个目标，员工写操作还会绑定请求体摘要。
type AdtySiteOwnerContext struct {
	Action string `json:"action"`
	Target int    `json:"target"`
	Digest string `json:"digest,omitempty"`
}

func adtyIsSiteOwnerStaffAction(action string) bool {
	switch action {
	case AdtySiteOwnerActionStaffCreate, AdtySiteOwnerActionStaffUpdate, AdtySiteOwnerActionStaffPassword, AdtySiteOwnerActionStaffDelete:
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
		raw, err := json.Marshal(val)
		if err != nil {
			return ""
		}
		if common.Unmarshal(raw, &input) != nil {
			sum := sha256.Sum256(raw)
			return hex.EncodeToString(sum[:])
		}
	}
	input = normalizeAdtyStaffInputForDigest(input)
	raw, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func adtyIsSiteOwnerScope(scope string) bool {
	return scope == VerificationScopeAdtySiteOwnerManage
}

func adtyIsSiteVerificationScope(scope string) bool {
	return adtyIsSiteOwnerScope(scope) || scope == VerificationScopeAdtyStaffCustomerManage
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
		reqDigest := AdtyStaffRequestDigest(wire.Request)
		if reqDigest == "" || (digest != "" && digest != reqDigest) {
			return nil, false
		}
		digest = reqDigest
	}
	if adtyIsSiteOwnerStaffAction(wire.Action) {
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
		ctx.Digest = AdtyStaffRequestDigest(request[0])
	}
	payload, _ := common.Marshal(ctx)
	return VerificationOperation{Scope: VerificationScopeAdtySiteOwnerManage, Context: payload}
}

func AdtyStaffCustomerOperation(action string, target int) VerificationOperation {
	payload, _ := common.Marshal(AdtySiteOwnerContext{Action: action, Target: target})
	return VerificationOperation{Scope: VerificationScopeAdtyStaffCustomerManage, Context: payload}
}

func init() {
	// 访问令牌也可以做站长操作，但令牌要有钱包写权限（和站长接口登记的 wallet:write 一致）。
	accessTokenVerificationScopes[VerificationScopeAdtySiteOwnerManage] = "wallet:write"
	// Staff customer security actions require a browser session, never a PAT.
	accessTokenVerificationScopes[VerificationScopeAdtyStaffCustomerManage] = ""
}
