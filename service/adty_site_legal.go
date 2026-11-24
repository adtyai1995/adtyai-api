package service

import (
	"errors"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AdtySiteLegalDocMaxBytes caps each single-language legal document body on a subsite.
const AdtySiteLegalDocMaxBytes = 200 * 1024

func normalizeAdtyLegalLang(lang string) string {
	trimmed := strings.TrimSpace(lang)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(strings.ReplaceAll(trimmed, "_", "-"))
	switch {
	case lower == "zh" || lower == "zhcn" || strings.HasPrefix(lower, "zh-"):
		return "zh-CN"
	case lower == "en" || strings.HasPrefix(lower, "en-"):
		return "en"
	default:
		return trimmed
	}
}

// AdtyRequestLegalLang returns the explicit ?lang= query value if present,
// otherwise falls back to the Accept-Language header.
func AdtyRequestLegalLang(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if lang := strings.TrimSpace(c.Query("lang")); lang != "" {
		return lang
	}
	return i18n.ParseAcceptLanguage(c.GetHeader("Accept-Language"))
}

// AdtyHasLegalText reports whether any language entry has non-whitespace content.
func AdtyHasLegalText(texts map[string]string) bool {
	for _, v := range texts {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// AdtyResolveLegalText selects the best non-empty text for lang with fallback order:
// requested normalized lang -> "en" -> "zh-CN" -> first non-empty key in sorted key order -> "".
func AdtyResolveLegalText(texts map[string]string, lang string) string {
	if len(texts) == 0 {
		return ""
	}
	norm := normalizeAdtyLegalLang(lang)
	if norm != "" {
		if v := strings.TrimSpace(texts[norm]); v != "" {
			return v
		}
		if raw := strings.TrimSpace(lang); raw != "" && raw != norm {
			if v := strings.TrimSpace(texts[raw]); v != "" {
				return v
			}
		}
	}
	for _, fallback := range []string{"en", "zh-CN"} {
		if fallback == norm {
			continue
		}
		if v := strings.TrimSpace(texts[fallback]); v != "" {
			return v
		}
	}
	keys := make([]string, 0, len(texts))
	for k := range texts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := strings.TrimSpace(texts[k]); v != "" {
			return v
		}
	}
	return ""
}

// AdtyMainLegalDocEnabled reports whether a legal document is enabled on the main site.
func AdtyMainLegalDocEnabled(doc string) bool {
	switch doc {
	case "user-agreement":
		return AdtyHasLegalText(adty_setting.GetLegalSetting().UserAgreement) ||
			strings.TrimSpace(system_setting.GetLegalSettings().UserAgreement) != ""
	case "privacy-policy":
		return AdtyHasLegalText(adty_setting.GetLegalSetting().PrivacyPolicy) ||
			strings.TrimSpace(system_setting.GetLegalSettings().PrivacyPolicy) != ""
	case "refund-policy":
		return adty_setting.GetSiteSetting().RefundPolicyEnabled
	default:
		return false
	}
}

// AdtyLegalConsentRequiredForRequest reports whether sign-up on the current request's
// site requires accepting legal terms. Subsites never inherit main site legal settings.
func AdtyLegalConsentRequiredForRequest(c *gin.Context) bool {
	if site := AdtySiteForRequest(c); site != nil {
		legal := AdtySiteLegalFor(site)
		return AdtyHasLegalText(legal.UserAgreement) || AdtyHasLegalText(legal.PrivacyPolicy)
	}
	return AdtyMainLegalConsentRequired()
}

func normalizeAdtySiteLegalMap(in map[string]string) map[string]string {
	out := map[string]string{}
	if len(in) == 0 {
		return out
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, rawKey := range keys {
		norm := normalizeAdtyLegalLang(rawKey)
		if norm != "en" && norm != "zh-CN" {
			continue
		}
		trimmed := strings.TrimSpace(in[rawKey])
		if trimmed == "" {
			continue
		}
		if rawKey == norm || out[norm] == "" {
			out[norm] = trimmed
		}
	}
	return out
}

func sanitizeAdtySiteLegalMap(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	if len(in) == 0 {
		return out, nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, rawKey := range keys {
		norm := normalizeAdtyLegalLang(rawKey)
		if norm != "en" && norm != "zh-CN" {
			continue
		}
		trimmed := strings.TrimSpace(in[rawKey])
		if len(trimmed) > AdtySiteLegalDocMaxBytes {
			return nil, common.NewMessage("Legal document content is too long")
		}
		if trimmed == "" {
			continue
		}
		if rawKey == norm || out[norm] == "" {
			out[norm] = trimmed
		}
	}
	return out, nil
}

// AdtySiteLegalFor decodes a subsite's own legal documents with guaranteed non-nil maps
// and normalized formal language keys (en, zh-CN). It never reads main site settings.
func AdtySiteLegalFor(site *model.AdtySite) model.AdtySiteLegalDocs {
	var parsed model.AdtySiteLegalDocs
	if site != nil && strings.TrimSpace(site.LegalDocs) != "" {
		_ = common.UnmarshalJsonStr(site.LegalDocs, &parsed)
	}
	return model.AdtySiteLegalDocs{
		UserAgreement:       normalizeAdtySiteLegalMap(parsed.UserAgreement),
		PrivacyPolicy:       normalizeAdtySiteLegalMap(parsed.PrivacyPolicy),
		RefundPolicy:        normalizeAdtySiteLegalMap(parsed.RefundPolicy),
		RefundPolicyEnabled: parsed.RefundPolicyEnabled,
	}
}

func adtyLegalNonEmptyLangs(texts map[string]string) []string {
	langs := make([]string, 0, len(texts))
	for k, v := range texts {
		if strings.TrimSpace(v) != "" {
			langs = append(langs, k)
		}
	}
	sort.Strings(langs)
	return langs
}

func adtySiteLegalAuditSummary(docs model.AdtySiteLegalDocs) map[string]any {
	return map[string]any{
		"user_agreement_langs":  adtyLegalNonEmptyLangs(docs.UserAgreement),
		"privacy_policy_langs":  adtyLegalNonEmptyLangs(docs.PrivacyPolicy),
		"refund_policy_langs":   adtyLegalNonEmptyLangs(docs.RefundPolicy),
		"refund_policy_enabled": docs.RefundPolicyEnabled,
	}
}

// SaveAdtySiteLegal validates ownership and site status, sanitizes formal languages
// (en, zh-CN), writes legal_docs on AdtySite, records a legal_update audit row, and
// invalidates the site cache.
func SaveAdtySiteLegal(siteId, ownerUserId int, input model.AdtySiteLegalDocs) error {
	if siteId <= 0 || ownerUserId <= 0 {
		return ErrAdtySiteNotOwner
	}
	userAgreement, err := sanitizeAdtySiteLegalMap(input.UserAgreement)
	if err != nil {
		return err
	}
	privacyPolicy, err := sanitizeAdtySiteLegalMap(input.PrivacyPolicy)
	if err != nil {
		return err
	}
	refundPolicy, err := sanitizeAdtySiteLegalMap(input.RefundPolicy)
	if err != nil {
		return err
	}
	sanitized := model.AdtySiteLegalDocs{
		UserAgreement:       userAgreement,
		PrivacyPolicy:       privacyPolicy,
		RefundPolicy:        refundPolicy,
		RefundPolicyEnabled: input.RefundPolicyEnabled,
	}
	raw, err := common.Marshal(sanitized)
	if err != nil {
		return err
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteId).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if site.OwnerUserId != ownerUserId {
			return ErrAdtySiteNotOwner
		}
		switch site.Status {
		case model.AdtySiteStatusActive, model.AdtySiteStatusPaused:
		case model.AdtySiteStatusClosing, model.AdtySiteStatusClosed:
			return ErrAdtySiteClosing
		default:
			return ErrAdtySiteNotOwner
		}
		oldDocs := AdtySiteLegalFor(&site)
		if err := tx.Model(&site).Update("legal_docs", string(raw)).Error; err != nil {
			return err
		}
		return recordSiteCompatibilityAuditTx(
			tx,
			site.Id,
			"legal_update",
			ownerUserId,
			adtySiteLegalAuditSummary(oldDocs),
			adtySiteLegalAuditSummary(sanitized),
			"",
		)
	})
	if err != nil {
		return err
	}
	InvalidateAdtySiteCache()
	return nil
}
