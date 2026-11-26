package service

import "regexp"

const (
	adtyPEMPrivateKeyExpr = `-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?:(?:\r?\n|\\r?\\n)[A-Za-z0-9+/=]{16,})*`

	adtyRedactConnURIExpr = `(?:postgres(?:ql)?|rediss?|mysql|mongodb(?:\+srv)?|amqps?)://[^\s:/@'"?#]*:[^\s/@'"?#]+@`

	adtyRedactEnKeyNamesExpr = `turnstile[_-]?secret[_-]?key|turnstile[_-]?widget[_-]?secret|worker[_-]?valid[_-]?key|alidns[_-]?access[_-]?key[_-]?secret|aliyun[_-]?access[_-]?key[_-]?(?:id|secret)|access[_-]?key[_-]?secret|cloudflare[_-]?api[_-]?token|cf[_-]?api[_-]?token|merchant[_-]?private[_-]?key|epay[_-]?private[_-]?key|acme[_-]?account[_-]?key|acme[_-]?key|key[_-]?pem|bark[_-]?url|telegram[_-]?bot[_-]?token|telegram[_-]?token|bepusdt[_-]?api[_-]?token|bepusdt[_-]?token|resend[_-]?api[_-]?key|smtp[_-]?token|smtp[_-]?password|mail[_-]?password|postgres[_-]?password|redis[_-]?password|db[_-]?password|log[_-]?sql[_-]?dsn|sql[_-]?dsn|redis[_-]?conn[_-]?string|gotify[_-]?token|generated[_-]?password|original[_-]?password|new[_-]?password|old[_-]?password|recovery[_ -]?codes?|backup[_ -]?codes?|one[_ -]?time[_ -]?(?:code|password|passcode)|verification[_ -]?code|verify[_ -]?code|redemption[_-]?code|voucher[_-]?code|security[_ -]?code|auth[_ -]?code|login[_ -]?code|reset[_ -]?code|email[_ -]?code|reset[_-]?token|access[_-]?token|refresh[_-]?token|session[_-]?token|session[_-]?secret|crypto[_-]?secret|auth[_-]?token|bot[_-]?token|adty[_-]?flush[_-]?token|flush[_-]?token|adty[_-]?edge[_-]?token|edge[_-]?token|private[_-]?key|secret[_-]?key|key[_-]?secret|signing[_-]?secret|webhook[_-]?secret|client[_-]?secret|payment[_-]?secret|payment[_-]?key|merchant[_-]?key|epay[_-]?key|totp[_-]?secret|api[_-]?key|authorization|passphrase|passcode|password|passwd|secret|bearer|token|otp`

	adtyRedactZhKeyNamesExpr     = `访问令牌|刷新令牌|支付密钥|发信密钥|接口密钥|商户私钥|私钥|密码|口令|验证码|兑换码|充值码|恢复码|备份码`
	adtyRedactZhBareKeyNamesExpr = `密钥|令牌`

	adtyRedactEscapedQuotedValExpr = `\\"(?:[^"\\]|\\\\\\\\|\\\\\\"|\\\\[^"\\]|\\[^"\\])*\\"`
	adtyRedactDoubleQuotedValExpr  = `"(?:[^"\\]|\\[\s\S])*"`
	adtyRedactSingleQuotedValExpr  = `'(?:[^'\\]|\\[\s\S])*'`
	adtyRedactArrayItemExpr        = `(?:` + adtyRedactEscapedQuotedValExpr + `|` + adtyRedactDoubleQuotedValExpr + `|` + adtyRedactSingleQuotedValExpr + `|[A-Za-z0-9._+\-/]+)`
	adtyRedactLineArrayItemExpr    = `(?:\\"(?:[^"\\\r\n]|\\\\\\\\|\\\\\\"|\\\\[^"\\\r\n]|\\[^"\\\r\n])*\\"|"(?:[^"\\\r\n]|\\.)*"|'(?:[^'\\\r\n]|\\.)*'|[A-Za-z0-9._+\-/]+)`
	adtyRedactLineFinalItemExpr    = `(?:\\"(?:[^"\\\r\n]|\\\\\\\\|\\\\\\"|\\\\[^"\\\r\n]|\\[^"\\\r\n])*\\"|"(?:[^"\\\r\n]|\\.)*"|'(?:[^'\\\r\n]|\\.)*'|\\"(?:[^"\\\r\n]|\\.)*\\?|"(?:[^"\\\r\n]|\\.)*\\?|'(?:[^'\\\r\n]|\\.)*\\?|[A-Za-z0-9._+\-/]+)`
	adtyRedactClosedArrayExpr      = `\[(?:\s*` + adtyRedactArrayItemExpr + `\s*,?)*\s*\]`
	adtyRedactTruncArrayExpr       = `\[(?:\s*` + adtyRedactLineArrayItemExpr + `\s*,)*\s*\r?\n[ \t]*(?:` + adtyRedactLineArrayItemExpr + `[ \t]*,[ \t]*)*(?:` + adtyRedactLineFinalItemExpr + `)?`

	adtyRedactKVValExpr = `(?:` +
		adtyRedactEscapedQuotedValExpr + `|` +
		adtyRedactDoubleQuotedValExpr + `|` +
		adtyRedactSingleQuotedValExpr + `|` +
		adtyRedactClosedArrayExpr + `|` +
		`\[[^\]\r\n]*\]|` +
		`\\"(?:[^"\\\r\n]|\\.)*\\?|` +
		`"(?:[^"\\\r\n]|\\.)*\\?|` +
		`'(?:[^'\\\r\n]|\\.)*\\?|` +
		adtyRedactTruncArrayExpr + `|` +
		`\[[^\]\r\n]*|` +
		`(?:(?:Bearer|Basic)\s+)?[^\s,}\]'"]+)`

	// Bare Chinese nouns (密钥/令牌) only redact ASCII credential-shaped values
	// (quoted/array ASCII values or non-whitespace ASCII tokens with at least one
	// ASCII letter/digit) so prose such as "API 密钥：旧 auto 分组已下线，请绑定具体分组"
	// is neither mangled nor blocked from automatic translation.
	adtyRedactZhBareEscQuotedExpr     = `\\"(?:[\x20\x21\x23-\x5b\x5d-\x7e\t\r\n]|\\\\\\\\|\\\\\\"|\\\\[\x20\x21\x23-\x5b\x5d-\x7e\t\r\n]|\\[\x20\x21\x23-\x5b\x5d-\x7e\t\r\n])*[A-Za-z0-9](?:[\x20\x21\x23-\x5b\x5d-\x7e\t\r\n]|\\\\\\\\|\\\\\\"|\\\\[\x20\x21\x23-\x5b\x5d-\x7e\t\r\n]|\\[\x20\x21\x23-\x5b\x5d-\x7e\t\r\n])*\\"`
	adtyRedactZhBareDoubleQuotedExpr  = `"(?:[\x20\x21\x23-\x5b\x5d-\x7e\t\r\n]|\\[\x20-\x7e\t\r\n])*[A-Za-z0-9](?:[\x20\x21\x23-\x5b\x5d-\x7e\t\r\n]|\\[\x20-\x7e\t\r\n])*"`
	adtyRedactZhBareSingleQuotedExpr  = `'(?:[\x20-\x26\x28-\x5b\x5d-\x7e\t\r\n]|\\[\x20-\x7e\t\r\n])*[A-Za-z0-9](?:[\x20-\x26\x28-\x5b\x5d-\x7e\t\r\n]|\\[\x20-\x7e\t\r\n])*'`
	adtyRedactZhBareLineItemExpr      = `(?:\\"(?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\\\\\\\|\\\\\\"|\\\\[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20\x21\x23-\x5b\x5d-\x7e\t])*[A-Za-z0-9](?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\\\\\\\|\\\\\\"|\\\\[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20\x21\x23-\x5b\x5d-\x7e\t])*\\"|"(?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*[A-Za-z0-9](?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*"|'(?:[\x20-\x26\x28-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*[A-Za-z0-9](?:[\x20-\x26\x28-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*'|[A-Za-z0-9._+\-/]*[A-Za-z0-9][A-Za-z0-9._+\-/]*)`
	adtyRedactZhBareLineFinalItemExpr = `(?:` + adtyRedactZhBareLineItemExpr + `|\\"(?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*[A-Za-z0-9](?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*\\?(?m:"?\r?$)|"(?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*[A-Za-z0-9](?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*\\?(?m:\r?$)|'(?:[\x20-\x26\x28-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*[A-Za-z0-9](?:[\x20-\x26\x28-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*\\?(?m:\r?$))`
	adtyRedactZhBareClosedArrayExpr   = `\[(?:\s*(?:` + adtyRedactZhBareEscQuotedExpr + `|` + adtyRedactZhBareDoubleQuotedExpr + `|` + adtyRedactZhBareSingleQuotedExpr + `|[A-Za-z0-9._+\-/]*[A-Za-z0-9][A-Za-z0-9._+\-/]*)\s*,?)+\s*\]`
	adtyRedactZhBareTruncArrayExpr    = `\[(?:\s*` + adtyRedactZhBareLineItemExpr + `\s*,)*\s*\r?\n[ \t]*(?:` + adtyRedactZhBareLineItemExpr + `[ \t]*,[ \t]*)*` + adtyRedactZhBareLineFinalItemExpr + `|\[(?:\s*` + adtyRedactZhBareLineItemExpr + `\s*,)+\s*\r?\n[ \t]*`

	adtyRedactZhBareKVValExpr = `(?:` +
		adtyRedactZhBareEscQuotedExpr + `|` +
		adtyRedactZhBareDoubleQuotedExpr + `|` +
		adtyRedactZhBareSingleQuotedExpr + `|` +
		adtyRedactZhBareClosedArrayExpr + `|` +
		`\[[\x20-\x5c\x5e-\x7e\t]*[A-Za-z0-9][\x20-\x5c\x5e-\x7e\t]*\]|` +
		`\\"(?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*[A-Za-z0-9](?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*\\?(?m:"?\r?$)|` +
		`"(?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*[A-Za-z0-9](?:[\x20\x21\x23-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*\\?(?m:\r?$)|` +
		`'(?:[\x20-\x26\x28-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*[A-Za-z0-9](?:[\x20-\x26\x28-\x5b\x5d-\x7e\t]|\\[\x20-\x7e])*\\?(?m:\r?$)|` +
		adtyRedactZhBareTruncArrayExpr + `|` +
		`\[[\x20-\x5c\x5e-\x7e\t]*[A-Za-z0-9][\x20-\x5c\x5e-\x7e\t]*(?m:\r?$)|` +
		`(?:(?:Bearer|Basic)[ \t]+)?[\x21\x23-\x26\x28-\x2b\x2d-\x5a\x5e-\x7c\x7e]*[A-Za-z0-9][\x21\x23-\x26\x28-\x2b\x2d-\x5a\x5e-\x7c\x7e]*)`
)

// adtyRedactPattern 是「脱敏」用的判据：命中就替换成 [redacted]。
//
// 它的目标只有一个——把用户粘进工单、公告里的凭据原文抹掉。所以它可以宽：
// 多打一段码的代价只是少了一点可读性，而漏掉一段 key 就是明文落库、跟着工单
// 一起被导出归档（ASVS 5.0.0 14.2.6）。
//
// JWT 那一段必须是三段式。eyJ 只是 base64 的 `{"`，单独出现于
// `data:application/json;base64,eyJ…` 和讲 JWT 的文档里都是正常文本；
// 写成 eyJ[A-Za-z0-9_-]{8,} 会把它们一起打码。
var adtyRedactPattern = regexp.MustCompile(`(?i)(` + adtyPEMPrivateKeyExpr + `|sk-[A-Za-z0-9_\-]{8,}|nap_[A-Za-z0-9_\-]{12,}|pat-[A-Za-z0-9_\-]{10,}|re_[A-Za-z0-9_]{16,}|bot\d{6,}:[A-Za-z0-9_\-]{10,}|\d{6,12}:[A-Za-z0-9_\-]{20,}|(?:\\"|["'])?\b(?:` + adtyRedactEnKeyNamesExpr + `)(?:\\"|["'])?\s*[:：=＝]\s*` + adtyRedactKVValExpr + `|(?:\\"|["'])?(?:` + adtyRedactZhKeyNamesExpr + `)(?:\\"|["'])?\s*[:：=＝]\s*` + adtyRedactKVValExpr + `|(?:\\"|["'])?(?:` + adtyRedactZhBareKeyNamesExpr + `)(?:\\"|["'])?[ \t]*[:：=＝][ \t]*` + adtyRedactZhBareKVValExpr + `|Bearer\s+[A-Za-z0-9._\-]{16,}|eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{8,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9\-]{10,}|` + adtyRedactConnURIExpr + `)`)

// adtyRejectPattern 是「拒单」用的判据：命中就整个操作被拒。
var adtyRejectPattern = regexp.MustCompile(`(?i)(` + adtyPEMPrivateKeyExpr + `|sk-[A-Za-z0-9_\-]{8,}|nap_[A-Za-z0-9_\-]{12,}|pat-[A-Za-z0-9_\-]{10,}|re_[A-Za-z0-9_]{16,}|bot\d{6,}:[A-Za-z0-9_\-]{10,}|\d{6,12}:[A-Za-z0-9_\-]{20,}|Bearer\s+[A-Za-z0-9._\-]{16,}|eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{8,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9\-]{10,}|` + adtyRedactConnURIExpr + `)`)

// adtyVoucherCodePattern matches candidate ADTY 26-character unpadded Base32 voucher codes
// (see newAdtyVoucherCode in service/adty_voucher.go). Word boundaries avoid matching inside
// longer alphanumeric tokens while still catching codes adjacent to Chinese text or punctuation.
var adtyVoucherCodePattern = regexp.MustCompile(`\b[A-Za-z2-7]{26}\b`)

func adtyIsVoucherCodeCandidate(token string) bool {
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
	// Pure uppercase Base32 tokens match generated voucher codes (including rare all-letter ones);
	// lowercase/mixed-case tokens require at least one Base32 digit to avoid flagging normal English words.
	if hasLower {
		return hasDigit
	}
	return true
}

func adtyHasVoucherCodeSecret(text string) bool {
	for _, match := range adtyVoucherCodePattern.FindAllString(text, -1) {
		if adtyIsVoucherCodeCandidate(match) {
			return true
		}
	}
	return false
}

func redactAdtySecrets(text string) string {
	redacted := adtyRedactPattern.ReplaceAllString(text, "[redacted]")
	return adtyVoucherCodePattern.ReplaceAllStringFunc(redacted, func(match string) string {
		if adtyIsVoucherCodeCandidate(match) {
			return "[redacted]"
		}
		return match
	})
}

func adtyTextHasSecret(text string) bool {
	return adtyRejectPattern.MatchString(text)
}

// adtyTextHasOutboundSecret applies the full secret detector (including password/api_key/verification-code,
// PEM private keys, payment/mail secrets, Chinese secret fields, and 26-char Base32 voucher codes)
// before sending text to external translation LLMs or private Telegram bots (R3-028 / R1-18).
func adtyTextHasOutboundSecret(text string) bool {
	return adtyRejectPattern.MatchString(text) || adtyRedactPattern.MatchString(text) || adtyHasVoucherCodeSecret(text)
}
