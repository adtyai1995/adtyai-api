package model

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const (
	AuditResultSuccess = "success"
	AuditResultFailure = "failure"

	// officialAuditSiteContextKey matches the site already placed in the
	// request by site-resolution middleware. The model package cannot import
	// service, so the key is compared as a stable string.
	officialAuditSiteContextKey = "adty_site"
)

// ErrOfficialAuditIncomplete is returned when an official writer omits site,
// object, action, or result. Approval association may be empty.
var ErrOfficialAuditIncomplete = errors.New("official audit write requires site, object, action, and result")

// OfficialAuditWrite is the single official G.13.2 input. SiteID must be set
// even when it is 0 (the platform/main site). ApprovalRef may be empty when
// the action has no approval.
type OfficialAuditWrite struct {
	UserId      int
	Username    string
	ActorRole   int
	SiteID      *int
	ObjectType  string
	ObjectID    string
	Action      string
	Category    string
	Content     string
	Before      any
	After       any
	Reason      string
	ApprovalRef string
	Result      string
	Success     bool
	Status      int
	Ip          string
	TokenRef    string
	AuthMethod  string
	RequestId   string
	EventId     string
	CreatedAt   int64
	Other       AuditOther
}

const (
	auditSecretPEMExpr = `-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?:(?:\r?\n|\\r?\\n)[A-Za-z0-9+/=]{16,})*`

	auditSecretEnKeyNamesExpr = `password|passwd|passphrase|secret|token|api[_-]?key|apikey|access[_-]?token|auth[_-]?token|bot[_-]?token|telegram[_-]?bot[_-]?token|telegram[_-]?token|authorization|private[_-]?key|client[_-]?secret|verification[_-]?code|verify[_-]?code|otp|session[_-]?token|payment[_-]?secret|payment[_-]?key|epay[_-]?key|bepusdt[_-]?token|bepusdt[_-]?api[_-]?token|merchant[_-]?key|merchant[_-]?private[_-]?key|webhook[_-]?secret|refresh[_-]?token|bearer|backup[_-]?codes?|smtp[_-]?token|smtp[_-]?password|mail[_-]?password|resend[_-]?api[_-]?key|gotify[_-]?token|generated[_-]?password|new[_-]?password|old[_-]?password|original[_-]?password|totp[_-]?secret|recovery[_-]?codes?|reset[_-]?token|email[_-]?code|cf[_-]?api[_-]?token|cloudflare[_-]?api[_-]?token|alidns[_-]?access[_-]?key[_-]?secret|aliyun[_-]?access[_-]?key[_-]?id|access[_-]?key[_-]?secret|epay[_-]?private[_-]?key|signing[_-]?secret|secret[_-]?key|key[_-]?secret|voucher[_-]?code|redemption[_-]?code|bark[_-]?url|turnstile[_-]?secret[_-]?key|edge[_-]?token|key[_-]?pem|acme[_-]?key|acme[_-]?account[_-]?key`
	auditSecretZhKeyNamesExpr = `验证码|密码|口令|密钥|令牌|兑换码|充值码|支付密钥|商户私钥|私钥|发信密钥|接口密钥`

	auditSecretTokenExpr = `sk-[A-Za-z0-9_\-]{8,}|nap_[A-Za-z0-9_\-]{12,}|pat-[A-Za-z0-9_\-]{10,}|re_[A-Za-z0-9_]{16,}|bot\d{6,}:[A-Za-z0-9_\-]{10,}|\d{6,12}:[A-Za-z0-9_\-]{20,}|Bearer\s+[A-Za-z0-9._\-]{16,}|eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{8,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9\-]{10,}`
	auditSecretKVValExpr = `(?:\\"(?:[^"\\]|\\\\\\"|\\[^"\\]|\\\\)*\\"|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|(?:(?:Bearer|Basic)\s+)?[^\s,}\]'"]+)`
	auditSecretEnKVExpr  = `(?:` + auditSecretEnKeyNamesExpr + `)\s*[:：=＝]\s*(` + auditSecretKVValExpr + `)`
	auditSecretZhKVExpr  = `(?:` + auditSecretZhKeyNamesExpr + `)\s*[:：=＝]\s*(` + auditSecretKVValExpr + `)`

	maxAuditJSONRedactDepth = 16
)

var (
	auditSecretPEM    = regexp.MustCompile(`(?is)` + auditSecretPEMExpr)
	auditSecretJSONKV = regexp.MustCompile(`(?i)("(?:` + auditSecretEnKeyNamesExpr + `|` + auditSecretZhKeyNamesExpr + `)"\s*:\s*)"(?:[^"\\]|\\.)*"`)
	auditSecretToken  = regexp.MustCompile(`(?i)(?:` + auditSecretTokenExpr + `)`)
	auditSecretText   = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_])(?:` + auditSecretEnKVExpr + `|` + auditSecretZhKVExpr + `)`)
)

// auditVoucherCodePattern matches candidate ADTY 26-character unpadded Base32 voucher codes
// (mirroring service/adty_redact.go).
var auditVoucherCodePattern = regexp.MustCompile(`\b[A-Za-z2-7]{26}\b`)

var auditSecretKeyNames = map[string]struct{}{
	"password":                 {},
	"passwd":                   {},
	"passphrase":               {},
	"secret":                   {},
	"token":                    {},
	"api_key":                  {},
	"apikey":                   {},
	"access_token":             {},
	"auth_token":               {},
	"bot_token":                {},
	"telegram_bot_token":       {},
	"telegram_token":           {},
	"authorization":            {},
	"private_key":              {},
	"client_secret":            {},
	"verification_code":        {},
	"verify_code":              {},
	"otp":                      {},
	"session_token":            {},
	"payment_secret":           {},
	"payment_key":              {},
	"epay_key":                 {},
	"bepusdt_token":            {},
	"merchant_key":             {},
	"merchant_private_key":     {},
	"webhook_secret":           {},
	"refresh_token":            {},
	"bearer":                   {},
	"backup_codes":             {},
	"backup_code":              {},
	"smtp_token":               {},
	"smtp_password":            {},
	"mail_password":            {},
	"resend_api_key":           {},
	"gotify_token":             {},
	"generated_password":       {},
	"new_password":             {},
	"old_password":             {},
	"original_password":        {},
	"totp_secret":              {},
	"recovery_code":            {},
	"recovery_codes":           {},
	"reset_token":              {},
	"email_code":               {},
	"cf_api_token":             {},
	"cloudflare_api_token":     {},
	"alidns_access_key_secret": {},
	"aliyun_access_key_id":     {},
	"access_key_secret":        {},
	"epay_private_key":         {},
	"signing_secret":           {},
	"secret_key":               {},
	"key_secret":               {},
	"voucher_code":             {},
	"redemption_code":          {},
	"bark_url":                 {},
	"bepusdt_api_token":        {},
	"turnstile_secret_key":     {},
	"edge_token":               {},
	"key_pem":                  {},
	"acme_key":                 {},
	"acme_account_key":         {},
	"验证码":                      {},
	"密码":                       {},
	"口令":                       {},
	"密钥":                       {},
	"令牌":                       {},
	"兑换码":                      {},
	"充值码":                      {},
	"支付密钥":                     {},
	"商户私钥":                     {},
	"私钥":                       {},
	"发信密钥":                     {},
	"接口密钥":                     {},
}

var auditNonSecretKeyPrefixes = []string{
	"has_",
	"is_",
	"can_",
	"require_",
	"requires_",
	"enable_",
	"enabled_",
	"disable_",
	"disabled_",
	"allow_",
	"allows_",
	"use_",
	"using_",
	"need_",
	"needs_",
}

// PrepareOfficialAuditLog validates G.13.2 first-class fields, redacts secrets,
// and returns a complete record ready to persist. It does not write the store.
func PrepareOfficialAuditLog(c *gin.Context, rec OfficialAuditWrite) (AuditLog, error) {
	if rec.SiteID == nil || strings.TrimSpace(rec.ObjectType) == "" || strings.TrimSpace(rec.ObjectID) == "" || strings.TrimSpace(rec.Action) == "" || strings.TrimSpace(rec.Result) == "" {
		return AuditLog{}, ErrOfficialAuditIncomplete
	}
	before, err := encodeOfficialAuditValue(rec.Before)
	if err != nil {
		return AuditLog{}, err
	}
	after, err := encodeOfficialAuditValue(rec.After)
	if err != nil {
		return AuditLog{}, err
	}
	success := rec.Success
	switch rec.Result {
	case AuditResultFailure:
		success = false
	case AuditResultSuccess:
		success = true
	}
	entry := AuditLog{
		UserId:      rec.UserId,
		Username:    rec.Username,
		ActorRole:   rec.ActorRole,
		SiteId:      rec.SiteID,
		ObjectType:  strings.TrimSpace(rec.ObjectType),
		ObjectId:    truncateOfficialAuditID(strings.TrimSpace(rec.ObjectID)),
		Action:      strings.TrimSpace(rec.Action),
		Category:    rec.Category,
		Content:     rec.Content,
		BeforeValue: before,
		AfterValue:  after,
		Reason:      rec.Reason,
		ApprovalRef: strings.TrimSpace(rec.ApprovalRef),
		Result:      strings.TrimSpace(rec.Result),
		Success:     success,
		Status:      rec.Status,
		Ip:          rec.Ip,
		TokenRef:    rec.TokenRef,
		AuthMethod:  rec.AuthMethod,
		RequestId:   rec.RequestId,
		EventId:     rec.EventId,
		CreatedAt:   rec.CreatedAt,
		Other:       rec.Other,
	}
	if entry.Category == "" {
		entry.Category = AuditCategoryOperation
	}
	entry = PrepareAuditLog(c, entry)
	redactAuditLog(&entry)
	return entry, nil
}

// WriteOfficialAuditLog is the single official writer for new G.13.2 records.
func WriteOfficialAuditLog(c *gin.Context, rec OfficialAuditWrite) error {
	entry, err := PrepareOfficialAuditLog(c, rec)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	return writeAuditLog(context.WithoutCancel(ctx), entry, false)
}

// WriteDurableOfficialAuditLog persists the audit intent to the primary DB
// before best-effort delivery to the log store so transient LOG_DB outages
// cannot drop official audit records. Falls back to direct write only when
// the primary DB is unavailable.
func WriteDurableOfficialAuditLog(c *gin.Context, rec OfficialAuditWrite) error {
	entry, err := PrepareOfficialAuditLog(c, rec)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if DB == nil {
		return writeAuditLog(context.WithoutCancel(ctx), entry, false)
	}
	if err := AppendAuditIntentTx(DB, entry); err != nil {
		return err
	}
	deliverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := DeliverAuditIntent(deliverCtx, entry.EventId); err != nil {
		common.SysLog(common.LogText("official audit intent delivery deferred (%s): %s", entry.EventId, err.Error()))
	}
	return nil
}

func OfficialAuditSiteIDFromContext(c *gin.Context) int {
	if c == nil {
		return 0
	}
	value, ok := c.Get(officialAuditSiteContextKey)
	if !ok || value == nil {
		return 0
	}
	if site, ok := value.(*AdtySite); ok && site != nil {
		return site.Id
	}
	return 0
}

func OfficialAuditSiteIDPointer(id int) *int {
	siteID := id
	return &siteID
}

func OfficialAuditResult(success bool) string {
	if success {
		return AuditResultSuccess
	}
	return AuditResultFailure
}

// InferOfficialAuditObject derives a stable object type and identifier from an
// action and its structured parameters. The fallback IDs are used only when no
// parameter identifies the object.
func InferOfficialAuditObject(action string, params map[string]any, fallbackIDs ...string) (objectType, objectID string) {
	objectType = strings.TrimSpace(action)
	if i := strings.LastIndex(objectType, "."); i > 0 {
		objectType = objectType[:i]
	}
	if objectType == "" || objectType == "generic" {
		objectType = "request"
	}
	for _, key := range []string{
		"id", "target_user_id", "user_id", "site_id", "trade_no", "key", "model",
		"invite_id", "report_id", "domain_id", "code", "username", "channel_id",
		"ticket_id", "voucher_id", "withdrawal_id", "token_ref",
	} {
		if params == nil {
			break
		}
		if id := stringifyOfficialAuditID(params[key]); id != "" {
			return objectType, id
		}
	}
	for _, id := range fallbackIDs {
		if strings.TrimSpace(id) != "" {
			return objectType, strings.TrimSpace(id)
		}
	}
	if action != "" {
		return objectType, action
	}
	return objectType, "unknown"
}

func InferOfficialAuditBeforeAfter(params map[string]any) (before, after any) {
	if params == nil {
		return nil, nil
	}
	for _, key := range []string{"before", "old", "old_value", "from", "previous"} {
		if value, ok := params[key]; ok {
			before = value
			break
		}
	}
	for _, key := range []string{"after", "new", "new_value", "to"} {
		if value, ok := params[key]; ok {
			after = value
			break
		}
	}
	return before, after
}

func InferOfficialAuditReason(params map[string]any) string {
	if params == nil {
		return ""
	}
	for _, key := range []string{"reason", "note"} {
		if text := stringifyOfficialAuditID(params[key]); text != "" {
			return text
		}
	}
	return ""
}

func InferOfficialAuditApprovalRef(params map[string]any) string {
	if params == nil {
		return ""
	}
	for _, key := range []string{"approval_ref", "approval_id", "approval"} {
		if text := stringifyOfficialAuditID(params[key]); text != "" {
			return text
		}
	}
	return ""
}

func InferOfficialAuditSiteID(c *gin.Context, params map[string]any) int {
	if params != nil {
		if id, ok := intFromOfficialAuditValue(params["site_id"]); ok {
			return id
		}
	}
	return OfficialAuditSiteIDFromContext(c)
}

func encodeOfficialAuditValue(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	switch typed := value.(type) {
	case string:
		return redactAuditText(typed), nil
	case []byte:
		return redactAuditText(string(typed)), nil
	}
	encoded, err := common.Marshal(redactAuditValue(value))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func truncateOfficialAuditID(id string) string {
	if len(id) <= 128 {
		return id
	}
	return id[:128]
}

func stringifyOfficialAuditID(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.RawMessage:
		return stringifyOfficialAuditID(decodeOfficialRaw(typed))
	case AuditFields:
		return ""
	}
	if text, err := common.Marshal(value); err == nil {
		s := strings.TrimSpace(string(text))
		s = strings.Trim(s, `"`)
		return s
	}
	return strings.TrimSpace(common.Interface2String(value))
}

func intFromOfficialAuditValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed == float64(int(typed)) {
			return int(typed), true
		}
	case string:
		id, err := strconv.Atoi(strings.TrimSpace(typed))
		return id, err == nil
	case json.RawMessage:
		return intFromOfficialAuditValue(decodeOfficialRaw(typed))
	}
	return 0, false
}

func decodeOfficialRaw(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var decoded any
	if common.Unmarshal(raw, &decoded) != nil {
		return strings.TrimSpace(string(raw))
	}
	return decoded
}

func redactAuditLog(entry *AuditLog) {
	if entry == nil {
		return
	}
	entry.ObjectId = redactAuditText(entry.ObjectId)
	entry.UserAgent = redactAuditText(entry.UserAgent)
	entry.Content = redactAuditText(entry.Content)
	entry.BeforeValue = redactAuditText(entry.BeforeValue)
	entry.AfterValue = redactAuditText(entry.AfterValue)
	entry.Reason = redactAuditText(entry.Reason)
	entry.ApprovalRef = redactAuditText(entry.ApprovalRef)
	entry.Other = redactAuditOther(entry.Other)
}

func redactAuditOther(other AuditOther) AuditOther {
	if other.Op != nil {
		op := *other.Op
		op.Action = redactAuditText(op.Action)
		op.Params = redactAuditFields(op.Params)
		other.Op = &op
	}
	if other.AdminInfo != nil {
		info := *other.AdminInfo
		info.AdminUsername = redactAuditText(info.AdminUsername)
		info.AuthMethod = redactAuditText(info.AuthMethod)
		other.AdminInfo = &info
	}
	if other.AuditInfo != nil {
		info := *other.AuditInfo
		info.Params = redactStringMap(info.Params)
		other.AuditInfo = &info
	}
	other.RootInfo = redactAuditFields(other.RootInfo)
	other.LoginMethod = redactAuditText(other.LoginMethod)
	other.UserAgent = redactAuditText(other.UserAgent)
	return other
}

func redactAuditFields(fields AuditFields) AuditFields {
	return redactAuditFieldsWithDepth(fields, 0)
}

func redactAuditFieldsWithDepth(fields AuditFields, depth int) AuditFields {
	if fields == nil {
		return nil
	}
	redacted := make(AuditFields, len(fields))
	for key, value := range fields {
		if shouldRedactAuditField(key, value) {
			redacted[key] = "[redacted]"
			continue
		}
		redacted[key] = redactAuditValueWithDepth(value, depth)
	}
	return redacted
}

func redactStringMap(values map[string]string) map[string]string {
	return redactStringMapWithDepth(values, 0)
}

func redactStringMapWithDepth(values map[string]string, depth int) map[string]string {
	if values == nil {
		return nil
	}
	redacted := make(map[string]string, len(values))
	for key, value := range values {
		if isOfficialAuditSecretKey(key) {
			redacted[key] = "[redacted]"
			continue
		}
		redacted[key] = redactAuditTextWithDepth(value, depth)
	}
	return redacted
}

func redactAuditValue(value any) any {
	return redactAuditValueWithDepth(value, 0)
}

func redactAuditValueWithDepth(value any, depth int) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return redactAuditTextWithDepth(typed, depth)
	case []byte:
		return redactAuditTextWithDepth(string(typed), depth)
	case json.RawMessage:
		return redactAuditValueWithDepth(decodeOfficialRaw(typed), depth)
	case AuditFields:
		return redactAuditFieldsWithDepth(typed, depth)
	case map[string]string:
		return redactStringMapWithDepth(typed, depth)
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if shouldRedactAuditField(key, item) {
				redacted[key] = "[redacted]"
				continue
			}
			redacted[key] = redactAuditValueWithDepth(item, depth)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, item := range typed {
			redacted[i] = redactAuditValueWithDepth(item, depth)
		}
		return redacted
	case []string:
		redacted := make([]string, len(typed))
		for i, item := range typed {
			redacted[i] = redactAuditTextWithDepth(item, depth)
		}
		return redacted
	default:
		switch value.(type) {
		case bool,
			int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64,
			json.Number:
			return value
		}
		raw, err := common.Marshal(value)
		if err != nil {
			return value
		}
		if depth >= maxAuditJSONRedactDepth {
			return redactAuditPlainText(string(raw))
		}
		var normalized any
		if err := common.Unmarshal(raw, &normalized); err != nil {
			return redactAuditPlainText(string(raw))
		}
		return redactAuditValueWithDepth(normalized, depth+1)
	}
}

// RedactAuditText masks secrets from free-form or serialized audit text.
func RedactAuditText(text string) string {
	return redactAuditText(text)
}

// RedactAuditJSON encodes any audit payload after recursive key/value redaction.
func RedactAuditJSON(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := encodeOfficialAuditValue(value)
	if err != nil {
		return redactAuditText(common.GetJsonString(value))
	}
	return encoded
}

func isAuditVoucherCodeCandidate(token string) bool {
	if len(token) != 26 {
		return false
	}
	var hasUpper, hasLower, hasDigit bool
	for i := range len(token) {
		c := token[i]
		switch {
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= '2' && c <= '7':
			hasDigit = true
		default:
			return false
		}
	}
	if !hasUpper && !hasLower {
		return false
	}
	if hasLower {
		return hasDigit
	}
	return true
}

func isAuditRedactedPlaceholder(val string) bool {
	val = strings.TrimSpace(val)
	if strings.HasPrefix(val, `\"`) && strings.HasSuffix(val, `\"`) && len(val) >= 4 {
		val = strings.TrimSpace(val[2 : len(val)-2])
	}
	val = strings.Trim(val, `"'`)
	return strings.EqualFold(val, "[redacted]")
}

func redactAuditSecretKVText(text string) string {
	matches := auditSecretText.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	last := 0
	for _, loc := range matches {
		b.WriteString(text[last:loc[0]])
		valStart, valEnd := loc[4], loc[5]
		if valStart < 0 {
			valStart, valEnd = loc[6], loc[7]
		}
		val := strings.TrimSpace(text[valStart:valEnd])
		if isAuditRedactedPlaceholder(val) {
			b.WriteString(text[loc[0]:loc[1]])
		} else {
			b.WriteString(text[loc[2]:loc[3]])
			b.WriteString("[redacted]")
		}
		last = loc[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

func redactAuditText(text string) string {
	return redactAuditTextWithDepth(text, 0)
}

func redactAuditTextWithDepth(text string, depth int) string {
	if text == "" {
		return text
	}
	if depth < maxAuditJSONRedactDepth {
		if trimmed := strings.TrimSpace(text); len(trimmed) >= 2 && (trimmed[0] == '{' || trimmed[0] == '[') {
			var decoded any
			if err := common.Unmarshal([]byte(trimmed), &decoded); err == nil {
				if encoded, err := common.Marshal(redactAuditValueWithDepth(decoded, depth+1)); err == nil {
					return string(encoded)
				}
			}
		}
	}
	return redactAuditPlainText(text)
}

func redactAuditPlainText(text string) string {
	text = auditSecretPEM.ReplaceAllString(text, "[redacted]")
	text = auditSecretJSONKV.ReplaceAllString(text, `$1"[redacted]"`)
	text = redactAuditSecretKVText(text)
	text = auditSecretToken.ReplaceAllString(text, "[redacted]")
	return auditVoucherCodePattern.ReplaceAllStringFunc(text, func(match string) string {
		if isAuditVoucherCodeCandidate(match) {
			return "[redacted]"
		}
		return match
	})
}

func normalizeOfficialAuditKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return strings.ReplaceAll(normalized, "-", "_")
}

func shouldRedactAuditField(key string, value any) bool {
	normalized := normalizeOfficialAuditKey(key)
	if _, ok := auditSecretKeyNames[normalized]; ok {
		return true
	}
	if _, isBool := value.(bool); isBool {
		return false
	}
	return isOfficialAuditSecretKey(normalized)
}

func isOfficialAuditSecretKey(key string) bool {
	normalized := normalizeOfficialAuditKey(key)
	if _, ok := auditSecretKeyNames[normalized]; ok {
		return true
	}
	for _, prefix := range auditNonSecretKeyPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return false
		}
	}
	for _, suffix := range []string{"_password", "_passwd", "_secret", "_private_key", "_api_key", "_apikey", "_token"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
