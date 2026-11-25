package service

import "regexp"

const adtyPEMPrivateKeyExpr = `-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?:\r?\n[A-Za-z0-9+/=]{16,})*`

// adtyRedactPattern 是「脱敏」用的判据：命中就替换成 [redacted]。
//
// 它的目标只有一个——把用户粘进工单、公告里的凭据原文抹掉。所以它可以宽：
// 多打一段码的代价只是少了一点可读性，而漏掉一段 key 就是明文落库、跟着工单
// 一起被导出归档（ASVS 5.0.0 14.2.6）。
//
// JWT 那一段必须是三段式。eyJ 只是 base64 的 `{"`，单独出现于
// `data:application/json;base64,eyJ…` 和讲 JWT 的文档里都是正常文本；
// 写成 eyJ[A-Za-z0-9_-]{8,} 会把它们一起打码。
var adtyRedactPattern = regexp.MustCompile(`(?i)(` + adtyPEMPrivateKeyExpr + `|sk-[A-Za-z0-9_\-]{8,}|nap_[A-Za-z0-9_\-]{12,}|pat-[A-Za-z0-9_\-]{10,}|re_[A-Za-z0-9_]{16,}|bot\d{6,}:[A-Za-z0-9_\-]{10,}|\d{6,12}:[A-Za-z0-9_\-]{20,}|["']?(?:api[_-]?key|password|passwd|secret|token|access[_-]?token|auth[_-]?token|private[_-]?key|passphrase|epay[_-]?key|merchant[_-]?key|merchant[_-]?private[_-]?key|bepusdt[_-]?token|smtp[_-]?token|resend[_-]?api[_-]?key|telegram[_-]?bot[_-]?token|telegram[_-]?token|voucher[_-]?code|redemption[_-]?code)["']?\s*[:：=＝]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,}\]]+)|["']?(?:密码|验证码|口令|兑换码|充值码|支付密钥|商户私钥|私钥|发信密钥|接口密钥)["']?\s*[:：=＝]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,}\]]+)|Bearer\s+[A-Za-z0-9._\-]{16,}|eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{8,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9\-]{10,})`)

// adtyRejectPattern 是「拒单」用的判据：命中就整个操作被拒。
var adtyRejectPattern = regexp.MustCompile(`(?i)(` + adtyPEMPrivateKeyExpr + `|sk-[A-Za-z0-9_\-]{8,}|nap_[A-Za-z0-9_\-]{12,}|pat-[A-Za-z0-9_\-]{10,}|re_[A-Za-z0-9_]{16,}|bot\d{6,}:[A-Za-z0-9_\-]{10,}|\d{6,12}:[A-Za-z0-9_\-]{20,}|Bearer\s+[A-Za-z0-9._\-]{16,}|eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{8,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9\-]{10,})`)

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
