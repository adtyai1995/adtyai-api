package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 站点品牌、系统邮件的发件人和站长发件域名（任务 11.3）。
// 链接和站名一律从站点表取（AdtySiteForUser、AdtySiteForRequest、AdtySiteBaseURL，见 adty_site_resolve.go），
// 不读 Host 头之外的请求数据（执行层决定 28）。

// AdtySiteBrand 是一个站点对外显示的品牌。主站返回平台的站名和地址。
type AdtySiteBrand struct {
	Name               string `json:"name"`
	LogoURL            string `json:"logo_url"`
	Title              string `json:"title"`
	FaviconURL         string `json:"favicon_url"`
	ThemeColor         string `json:"theme_color,omitempty"`
	Tagline            string `json:"tagline,omitempty"`
	Notice             string `json:"notice"`
	SupportContact     string `json:"support_contact"`
	TawkPropertyID     string `json:"tawk_property_id"`
	TawkWidgetID       string `json:"tawk_widget_id"`
	WalletNotice       string `json:"wallet_notice"`
	VoucherPurchaseURL string `json:"voucher_purchase_url"`
	DocsURL            string `json:"docs_url"`
	BaseURL            string `json:"base_url"`
}

// AdtySiteBrandInput 是站长提交的品牌字段（11.6 的 PUT /api/adty/site/self 用）。
type AdtySiteBrandInput struct {
	Name               string `json:"name"`
	LogoURL            string `json:"logo_url"`
	Title              string `json:"title"`
	FaviconURL         string `json:"favicon_url"`
	ThemeColor         string `json:"theme_color,omitempty"`
	Tagline            string `json:"tagline,omitempty"`
	Notice             string `json:"notice"`
	SupportContact     string `json:"support_contact"`
	TawkPropertyID     string `json:"tawk_property_id"`
	TawkWidgetID       string `json:"tawk_widget_id"`
	WalletNotice       string `json:"wallet_notice"`
	VoucherPurchaseURL string `json:"voucher_purchase_url"`
	DocsURL            string `json:"docs_url"`
}

// 品牌字段的字数上限（按字符数，不是字节）。
const (
	adtyBrandNameMax    = 40
	adtyBrandNoticeMax  = 500
	adtyBrandSupportMax = 200
	adtyTawkIDMax       = 64
	// AdtySiteAssetPrefix 是站长上传的 Logo 的地址前缀（文件在 /data/uploads/site-assets）。
	AdtySiteAssetPrefix = "/uploads/site-assets/"
)

var ErrAdtySiteBrandInvalid = common.NewMessage("Site name up to 40 characters, notice up to 500 and support contact up to 200; the logo must be an uploaded image")

var (
	adtyTawkIDRegex = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// ValidateAdtyTawkID validates a Tawk.to property or widget identifier.
// Empty values are allowed (disabling live chat); non-empty values must be
// plain safe token characters to prevent script URL injection.
func ValidateAdtyTawkID(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) > adtyTawkIDMax || !adtyTawkIDRegex.MatchString(trimmed) {
		return common.NewMessage("Tawk.to ID must contain only letters, numbers, hyphens, or underscores")
	}
	return nil
}

// adtyPlainText 去掉控制字符和首尾空白；HTML 原样保留为文本，前端按纯文本渲染，进邮件时转义。
func adtyPlainText(value string, keepNewlines bool) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' && keepNewlines {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	return strings.TrimSpace(value)
}

// NormalizeAdtySiteBrand 校验并整理站长提交的品牌：只是模板字段，不接受 HTML 以外的格式，也不渲染 HTML。
func NormalizeAdtySiteBrand(input AdtySiteBrandInput) (AdtySiteBrandInput, error) {
	out := AdtySiteBrandInput{
		Name:               adtyPlainText(input.Name, false),
		Tagline:            "",
		Notice:             adtyPlainText(input.Notice, true),
		SupportContact:     adtyPlainText(input.SupportContact, false),
		LogoURL:            strings.TrimSpace(input.LogoURL),
		Title:              adtyPlainText(input.Title, false),
		FaviconURL:         strings.TrimSpace(input.FaviconURL),
		ThemeColor:         "",
		TawkPropertyID:     strings.TrimSpace(input.TawkPropertyID),
		TawkWidgetID:       strings.TrimSpace(input.TawkWidgetID),
		WalletNotice:       adtyPlainText(input.WalletNotice, true),
		VoucherPurchaseURL: strings.TrimSpace(input.VoucherPurchaseURL),
		DocsURL:            strings.TrimSpace(input.DocsURL),
	}
	if utf8.RuneCountInString(out.Title) > 80 {
		return out, common.NewMessage("Website title must be at most 80 characters")
	}
	if utf8.RuneCountInString(out.WalletNotice) > 2000 {
		return out, common.NewMessage("Wallet notice must be at most 2000 characters")
	}
	if out.VoucherPurchaseURL != "" {
		parsed, err := url.Parse(out.VoucherPurchaseURL)
		if err != nil || strings.ContainsAny(out.VoucherPurchaseURL, " \t\r\n") || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || len(out.VoucherPurchaseURL) > 500 {
			return out, common.NewMessage("Voucher purchase link must be a valid http:// or https:// URL")
		}
	}
	if out.DocsURL != "" {
		parsed, err := url.Parse(out.DocsURL)
		if err != nil || strings.ContainsAny(out.DocsURL, " \t\r\n") || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || len(out.DocsURL) > 500 {
			return out, common.NewMessage("Documentation link must be a valid http:// or https:// URL")
		}
	}
	if err := ValidateAdtyTawkID(out.TawkPropertyID); err != nil {
		return out, err
	}
	if err := ValidateAdtyTawkID(out.TawkWidgetID); err != nil {
		return out, err
	}
	if utf8.RuneCountInString(out.Name) > adtyBrandNameMax ||
		utf8.RuneCountInString(out.Notice) > adtyBrandNoticeMax || utf8.RuneCountInString(out.SupportContact) > adtyBrandSupportMax {
		return out, ErrAdtySiteBrandInvalid
	}
	if out.LogoURL != "" {
		name, ok := strings.CutPrefix(out.LogoURL, AdtySiteAssetPrefix)
		if !ok || !adtySiteAssetName.MatchString(name) {
			return out, ErrAdtySiteBrandInvalid
		}
	}
	if out.FaviconURL != "" {
		name, ok := strings.CutPrefix(out.FaviconURL, AdtySiteAssetPrefix)
		if !ok || !adtySiteAssetName.MatchString(name) {
			return out, common.NewMessage("Favicon must be an uploaded site image")
		}
	}
	return out, nil
}

func validateAdtySiteOwnedAsset(siteID int, urlPath string, invalidErr error) error {
	if urlPath == "" {
		return nil
	}
	name, ok := strings.CutPrefix(urlPath, AdtySiteAssetPrefix)
	if !ok || !adtySiteAssetName.MatchString(name) {
		return invalidErr
	}
	expectedPrefix := "site-" + strconv.Itoa(siteID) + "-"
	if !strings.HasPrefix(name, expectedPrefix) {
		return invalidErr
	}
	if _, _, err := OpenAdtySiteAsset(name); err != nil {
		return invalidErr
	}
	return nil
}

func validateAdtySiteHomePageContent(rawHome string) error {
	rawHome = strings.TrimSpace(rawHome)
	if rawHome == "" {
		return nil
	}
	if len(rawHome) > 200*1024 {
		return common.NewMessage("Home page content is too long")
	}
	// Strip all Unicode whitespace, control, and format characters before
	// checking dangerous URI prefixes so inputs like "java\tscript:..." or
	// "\u200bjavascript:..." cannot bypass prefix validation.
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, rawHome)
	lower := strings.ToLower(compact)
	for _, blocked := range []string{"javascript:", "vbscript:", "data:", "file:", "blob:"} {
		if strings.HasPrefix(lower, blocked) {
			return common.NewMessage("Home page URL must start with http:// or https://")
		}
	}
	if strings.HasPrefix(compact, "//") {
		return common.NewMessage("Home page URL must start with http:// or https://")
	}
	if !strings.HasPrefix(rawHome, "<") && !strings.ContainsAny(rawHome, " \t\r\n") {
		if colon := strings.IndexByte(rawHome, ':'); colon > 0 {
			scheme := rawHome[:colon]
			validScheme := true
			for i := 0; i < len(scheme); i++ {
				ch := scheme[i]
				if i == 0 {
					if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')) {
						validScheme = false
						break
					}
				} else if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '+' || ch == '-' || ch == '.') {
					validScheme = false
					break
				}
			}
			if validScheme {
				parsed, err := url.Parse(rawHome)
				if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(parsed.Host) == "" {
					return common.NewMessage("Home page URL must start with http:// or https://")
				}
			}
		}
	}
	return nil
}

// SaveAdtyBrandPatch checks only edited fields while holding the site's row lock.
func SaveAdtyBrandPatch(siteID, ownerID int, fields, expected map[string]string, seo *bool, homePageContent ...*string) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND owner_user_id = ?", siteID, ownerID).First(&site).Error; err != nil {
			return err
		}
		if site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused {
			return ErrAdtySiteClosing
		}
		updates := map[string]any{}
		if seo != nil {
			updates["seo_index"] = *seo
		}
		if len(homePageContent) > 0 && homePageContent[0] != nil {
			rawHome := strings.TrimSpace(*homePageContent[0])
			if err := validateAdtySiteHomePageContent(rawHome); err != nil {
				return err
			}
			updates["home_page_content"] = rawHome
		}
		if len(fields) > 0 {
			var brand AdtySiteBrandInput
			if site.Brand != "" {
				if err := common.UnmarshalJsonStr(site.Brand, &brand); err != nil {
					return err
				}
			}
			values := map[string]*string{
				"name": &brand.Name, "title": &brand.Title, "notice": &brand.Notice,
				"support_contact": &brand.SupportContact, "logo_url": &brand.LogoURL, "favicon_url": &brand.FaviconURL,
				"tawk_property_id": &brand.TawkPropertyID, "tawk_widget_id": &brand.TawkWidgetID,
				"wallet_notice": &brand.WalletNotice, "voucher_purchase_url": &brand.VoucherPurchaseURL,
				"docs_url": &brand.DocsURL,
			}
			for key, value := range fields {
				if key == "tagline" || key == "theme_color" {
					continue
				}
				dest, ok := values[key]
				if !ok {
					return ErrAdtySiteBrandInvalid
				}
				base := expected[key]
				if *dest != base && *dest != value {
					return common.NewMessage("Setting changed since editing began: {{key}}", map[string]any{"key": key})
				}
				*dest = value
			}
			normalized, err := NormalizeAdtySiteBrand(brand)
			if err != nil {
				return err
			}
			// R2-045: Enforce site-<siteID>- ownership & file existence whenever
			// logo_url or favicon_url is modified.
			if _, changed := fields["logo_url"]; changed {
				if err := validateAdtySiteOwnedAsset(siteID, normalized.LogoURL, ErrAdtySiteBrandInvalid); err != nil {
					return err
				}
			}
			if _, changed := fields["favicon_url"]; changed {
				if err := validateAdtySiteOwnedAsset(siteID, normalized.FaviconURL, common.NewMessage("Favicon must be an uploaded site image")); err != nil {
					return err
				}
			}
			raw, err := common.Marshal(normalized)
			if err != nil {
				return err
			}
			updates["brand"] = string(raw)
		}
		return tx.Model(&site).Updates(updates).Error
	})
}

// AdtyBrandFor 是站点的品牌；nil 返回主站品牌。没填站名的分站用子域名。
func AdtyBrandFor(site *model.AdtySite) AdtySiteBrand {
	if site == nil {
		return AdtySiteBrand{Name: common.GetSystemName(), BaseURL: AdtySiteBaseURL(nil)}
	}
	var brand AdtySiteBrandInput
	if strings.TrimSpace(site.Brand) != "" {
		_ = common.UnmarshalJsonStr(site.Brand, &brand)
	}
	name := strings.TrimSpace(brand.Name)
	if name == "" {
		name = site.Subdomain
	}
	return AdtySiteBrand{
		Name: name, LogoURL: strings.TrimSpace(brand.LogoURL), Notice: brand.Notice,
		Title: brand.Title, FaviconURL: strings.TrimSpace(brand.FaviconURL),
		SupportContact: brand.SupportContact, TawkPropertyID: strings.TrimSpace(brand.TawkPropertyID),
		TawkWidgetID: strings.TrimSpace(brand.TawkWidgetID), WalletNotice: brand.WalletNotice,
		VoucherPurchaseURL: strings.TrimSpace(brand.VoucherPurchaseURL),
		DocsURL:            strings.TrimSpace(brand.DocsURL),
		BaseURL:            AdtySiteBaseURL(site),
	}
}

// adtyPlatformSender 是全局的系统邮件发件地址：Resend 用 ResendFrom，SMTP 用 SMTPFrom。
func adtyPlatformSender() (string, string) {
	common.OptionMapRWMutex.RLock()
	provider, resendFrom := common.OptionMap["EmailProvider"], common.OptionMap["ResendFrom"]
	smtpFrom, smtpAccount := common.SMTPFrom, common.SMTPAccount
	common.OptionMapRWMutex.RUnlock()
	if provider == "resend" {
		if parsed, err := mail.ParseAddress(strings.TrimSpace(resendFrom)); err == nil && parsed != nil {
			return strings.TrimSpace(parsed.Address), provider
		}
	}
	from := strings.TrimSpace(smtpFrom)
	if from == "" {
		from = strings.TrimSpace(smtpAccount)
	}
	if parsed, err := mail.ParseAddress(from); err == nil && parsed != nil && strings.TrimSpace(parsed.Address) != "" {
		return strings.TrimSpace(parsed.Address), provider
	}
	return from, provider
}

// adtyFormatSender 写成「站名 <地址>」。站名只有字母、数字、空格和常见标点时不加引号（和平台的 ResendFrom 写法一致），
// 否则交给 net/mail 按 RFC 5322 加引号或编码。
func adtyFormatSender(name, address string) string {
	plain := name != ""
	for _, r := range name {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" -_!#$%&'*+/=?^`{|}~", r)) {
			plain = false
			break
		}
	}
	if plain && strings.TrimSpace(name) == name {
		return name + " <" + address + ">"
	}
	return (&mail.Address{Name: name, Address: address}).String()
}

// AdtySenderFor 是系统邮件的发件人：站点开了站长发件、发件域名在 Resend 验证通过，并且全局用 Resend 发信时，
// 用「站名 <noreply@站长发件域名>」；否则用「站名 <全局发件地址>」。SMTP 只能用全局发件地址，只换显示名。
func AdtySenderFor(site *model.AdtySite) string {
	name := AdtyBrandFor(site).Name
	if site != nil {
		if cfg, found, err := loadAdtySiteMailConfig(site.Id); err == nil && found && cfg != nil &&
			(cfg.Provider == "" || cfg.Provider == model.AdtySiteMailProviderPlatform) &&
			strings.TrimSpace(cfg.SenderName) != "" {
			name = strings.TrimSpace(cfg.SenderName)
		}
		if strings.TrimSpace(name) == "" {
			name = AdtySiteDisplayHost(site)
		}
	}
	platform, _ := adtyPlatformSender()
	return adtyFormatSender(name, platform)
}

// AdtySendSiteEmail 用站点的发件配置发一封系统邮件；site 为 nil 时和上游 SendEmail 一样。
func AdtySendSiteEmail(site *model.AdtySite, subject, receiver, content string) error {
	if site == nil {
		return common.SendEmail(subject, receiver, content)
	}
	if !adtyUsesDefaultDeliverSiteEmail() {
		return AdtyDeliverSiteEmail(site, subject, receiver, content)
	}
	return sendSubsiteConfiguredEmail(site, subject, receiver, content)
}

var (
	ErrAdtySenderDomainOff       = common.NewMessage("Sender domains are not available")
	ErrAdtySenderDomainInvalid   = common.NewMessage("Enter a domain you own, such as mail.example.com; our own domains cannot be used")
	ErrAdtySenderDomainQuotaFull = common.NewMessage("No sender domain slots are left. Please contact the platform")
	ErrAdtySenderDomainFailed    = common.NewMessage("The email provider could not complete the request; try again later")
)

// normalizeSenderDomain 校验发件域名：合法的 DNS 名，至少两段，不能是我们的根域名、主站域名下的。
func normalizeSenderDomain(raw string) (string, error) {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if strings.Count(domain, ".") < 1 || len(domain) > 253 {
		return "", ErrAdtySenderDomainInvalid
	}
	for _, label := range strings.Split(domain, ".") {
		if !adty_setting.ResellerLabelPattern.MatchString(label) {
			return "", ErrAdtySenderDomainInvalid
		}
	}
	blocked := append([]string{}, resellerSetting().RootDomains...)
	if main := adtyMainHost(); main != "" && main != "localhost" {
		blocked = append(blocked, adtyRegistrableDomain(main))
	}
	if apiHost := adtyAPIBaseHost(); apiHost != "" && apiHost != "localhost" {
		blocked = append(blocked, adtyRegistrableDomain(apiHost))
	}
	common.OptionMapRWMutex.RLock()
	resendFrom := strings.TrimSpace(common.OptionMap["ResendFrom"])
	common.OptionMapRWMutex.RUnlock()
	if resendFrom != "" {
		addrStr := resendFrom
		if parsed, err := mail.ParseAddress(resendFrom); err == nil && parsed != nil && parsed.Address != "" {
			addrStr = parsed.Address
		}
		if at := strings.LastIndex(addrStr, "@"); at >= 0 && at+1 < len(addrStr) {
			if fromHost := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(addrStr[at+1:])), "."); fromHost != "" && fromHost != "localhost" {
				blocked = append(blocked, adtyRegistrableDomain(fromHost))
			}
		}
	}
	blocked = append(blocked, strings.TrimPrefix(common.AdtyBrandURL, "https://"))
	for _, own := range blocked {
		if adtyHostUnder(domain, strings.ToLower(strings.TrimSpace(own))) {
			return "", ErrAdtySenderDomainInvalid
		}
	}
	return domain, nil
}

// adtyResendUserError 把 Resend 的错误换成给人看的提示：名额满了提示联系平台。原始错误只记状态码和名称。
func adtyResendUserError(err error) error {
	var apiErr *AdtyResendError
	if errors.As(err, &apiErr) {
		common.SysError(common.LogText("resend domain request failed: status=%d name=%s", apiErr.StatusCode, apiErr.Name))
		message := strings.ToLower(apiErr.Message)
		if apiErr.StatusCode == http.StatusForbidden || strings.Contains(message, "limit") || strings.Contains(message, "quota") {
			adtyNoteSenderQuotaFull() // 后台用量页显示最近一次名额满（11.9）
			return ErrAdtySenderDomainQuotaFull
		}
		return ErrAdtySenderDomainFailed
	}
	if errors.Is(err, ErrAdtyResendNotConfigured) {
		return ErrAdtySenderDomainOff
	}
	common.SysError(common.LogText("resend domain request failed: %s", err.Error()))
	return ErrAdtySenderDomainFailed
}

// deleteSenderDomainIfPresent 从 Resend 删域名。404 当成已经不在，避免旧 id 失效后换域名一直失败。
func deleteSenderDomainIfPresent(ctx context.Context, domainID string) error {
	if domainID == "" {
		return nil
	}
	err := AdtyResend.DeleteDomain(ctx, domainID)
	if err == nil {
		return nil
	}
	var apiErr *AdtyResendError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return adtyResendUserError(err)
}

// AddAdtySiteSenderDomain 给站长的站设置发件域名（sender_domain_enabled 打开后可用）：调 Resend 建域名，
// 存域名 id 和要加的 DNS 记录。换成别的域名时，先建新的并写库，库提交成功后再删旧的；
// 写库失败就删掉刚建的新域名，Resend 和库都留在原来的发件域名上。
func AddAdtySiteSenderDomain(ctx context.Context, ownerUserId int, rawDomain string) (*model.AdtySiteSenderDomain, error) {
	if !resellerSetting().SenderDomainEnabled {
		return nil, ErrAdtySenderDomainOff
	}
	site, err := adtyOwnerSite(ownerUserId)
	if err != nil {
		return nil, err
	}
	domain, err := normalizeSenderDomain(rawDomain)
	if err != nil {
		return nil, err
	}
	var existing model.AdtySiteSenderDomain
	lookup := model.DB.Where("site_id = ?", site.Id).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	found := lookup.RowsAffected > 0
	if found && existing.Status == model.AdtySenderDomainRemoving {
		return nil, common.NewMessage("Sender domain removal is still in progress. Try again shortly.")
	}
	if found && existing.Domain == domain && (existing.Status == model.AdtySenderDomainPending || existing.Status == model.AdtySenderDomainVerified) {
		return &existing, nil
	}
	// 同名重建时旧域名还占着这个名字，得先删掉才能再建。别的名字先留着，等库写成功再删。
	if found && existing.Domain == domain && existing.ResendDomainId != "" && existing.Status != model.AdtySenderDomainDisabled {
		if err := deleteSenderDomainIfPresent(ctx, existing.ResendDomainId); err != nil {
			return nil, err
		}
		if err := model.DB.Model(&model.AdtySiteSenderDomain{}).
			Where("site_id = ? AND resend_domain_id = ?", site.Id, existing.ResendDomainId).
			Updates(map[string]any{
				"status":           model.AdtySenderDomainDisabled,
				"resend_domain_id": "",
				"cleanup_error":    "",
				"retry_at":         0,
			}).Error; err != nil {
			return nil, err
		}
	}
	created, err := AdtyResend.CreateDomain(ctx, AdtyResendDomainCreate{Name: domain})
	if err != nil {
		return nil, adtyResendUserError(err)
	}
	records, _ := common.Marshal(created.Records)
	now := time.Now().Unix()
	row := model.AdtySiteSenderDomain{
		SiteId: site.Id, Domain: domain, ResendDomainId: created.ID, Status: model.AdtySenderDomainPending,
		Records: string(records), LastCheckedAt: now, CreatedAt: now,
	}
	if created.Status == "verified" {
		row.Status = model.AdtySenderDomainVerified
	}
	if created.ID == "" {
		return nil, ErrAdtySenderDomainInvalid
	}
	// Stage the new ID independently so a failed binding transaction leaves a cleanup job.
	job := model.AdtySenderDomainCleanup{SiteID: site.Id, RemoteID: created.ID, RetryAt: now + 600}
	staged := model.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&job)
	if staged.Error != nil {
		// Retry the journal before releasing anything. Its worker checks the live binding.
		if err := model.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error; err != nil {
			return nil, errors.Join(staged.Error, err)
		}
		return nil, staged.Error
	}
	if staged.RowsAffected == 0 {
		return nil, common.NewMessage("Sender domain creation is already being reconciled")
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var locked model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&locked, site.Id).Error; err != nil {
			return err
		}
		if locked.OwnerUserId != ownerUserId || (locked.Status != model.AdtySiteStatusActive && locked.Status != model.AdtySiteStatusPaused) {
			return ErrAdtySiteClosing
		}
		// Claim the staged resource under the same site lock as the cleanup worker.
		// Deleting the job rolls back if any subsequent binding write fails.
		remove := tx.Where("remote_id = ? AND site_id = ? AND retry_at = ?", created.ID, site.Id, job.RetryAt).Delete(&model.AdtySenderDomainCleanup{})
		if remove.Error != nil {
			return remove.Error
		}
		if remove.RowsAffected != 1 {
			return common.NewMessage("Sender domain creation expired")
		}
		var prior model.AdtySiteSenderDomain
		lookup := model.AdtyLockForUpdate(tx).Where("site_id = ?", site.Id).Limit(1).Find(&prior)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			if prior.ResendDomainId != "" && prior.ResendDomainId != row.ResendDomainId && prior.Status != model.AdtySenderDomainDisabled {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AdtySenderDomainCleanup{RemoteID: prior.ResendDomainId, SiteID: site.Id}).Error; err != nil {
					return err
				}
			}
			return tx.Model(&prior).Updates(map[string]any{"domain": row.Domain, "resend_domain_id": row.ResendDomainId, "status": row.Status, "records": row.Records, "disabled_by": 0, "cleanup_error": "", "retry_at": 0, "last_checked_at": now, "created_at": now}).Error
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		if updateErr := model.DB.Model(&model.AdtySenderDomainCleanup{}).Where("remote_id = ? AND site_id = ? AND retry_at = ?", job.RemoteID, job.SiteID, job.RetryAt).Update("retry_at", now).Error; updateErr != nil {
			return nil, errors.Join(err, updateErr)
		}
		if cleanupErr := cleanReplacedSenderDomains(ctx, now, site.Id); cleanupErr != nil {
			return nil, errors.Join(err, cleanupErr)
		}
		return nil, err
	}
	if err := cleanReplacedSenderDomains(ctx, now, site.Id); err != nil {
		common.SysError(common.LogText("sender cleanup: %s", err.Error()))
	}
	return &row, nil
}

// GetAdtySiteSenderDomain 是站长自己站上的发件域名；没有返回 nil。
func GetAdtySiteSenderDomain(ownerUserId int) (*model.AdtySiteSenderDomain, error) {
	site, err := adtyOwnerSite(ownerUserId)
	if err != nil {
		return nil, err
	}
	var row model.AdtySiteSenderDomain
	if model.DB.Where("site_id = ? AND (status <> ? OR disabled_by > 0)", site.Id, model.AdtySenderDomainDisabled).Limit(1).Find(&row).RowsAffected == 0 {
		return nil, nil
	}
	return &row, nil
}

// RemoveAdtySiteSenderDomain 是站长删除自己的发件域名。
func RemoveAdtySiteSenderDomain(ctx context.Context, ownerUserId int) error {
	site, err := adtyOwnerSite(ownerUserId)
	if err != nil {
		return err
	}
	return DisableAdtySiteSenderDomain(ctx, site.Id, 0)
}

// DisableAdtySiteSenderDomain 从 Resend 删掉站点的发件域名，回退到平台发件地址。operatorId 是停用的管理员（站长自己删时为 0）。
func DisableAdtySiteSenderDomain(ctx context.Context, siteId, operatorId int) error {
	return DisableAdtySiteSenderDomainForActor(ctx, siteId, operatorId, 0, false)
}

func DisableAdtySiteSenderDomainForActor(ctx context.Context, siteId, operatorId, actorRole int, denyAdminTarget bool) error {
	if siteId > 0 {
		if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Select("id", "owner_user_id", "status").Where("id = ?", siteId).First(&site).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtySiteNotFound
				}
				return err
			}
			if site.Status == model.AdtySiteStatusDeleting {
				return ErrAdtySiteStatus
			}
			if actorRole > 0 || denyAdminTarget {
				return checkAdtySiteTargetActorTx(tx, &site, operatorId, actorRole, denyAdminTarget)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	var row model.AdtySiteSenderDomain
	lookup := model.DB.Where("site_id = ?", siteId).Limit(1).Find(&row)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected == 0 || row.Status == model.AdtySenderDomainDisabled {
		return nil
	}
	if operatorId == 0 && row.Status == model.AdtySenderDomainRemoving && row.DisabledBy > 0 {
		return common.NewMessage("This sender domain is being disabled by the platform")
	}
	disabledBy := operatorId
	if disabledBy == 0 && row.DisabledBy > 0 {
		disabledBy = row.DisabledBy
	}
	writeSenderDomainUpdate := func(updates map[string]any) error {
		return model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if siteId > 0 {
				var site model.AdtySite
				if err := model.AdtyLockForUpdate(tx).Select("id", "status").Where("id = ?", siteId).First(&site).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return ErrAdtySiteNotFound
					}
					return err
				}
				if site.Status == model.AdtySiteStatusDeleting {
					return ErrAdtySiteStatus
				}
			}
			return tx.Model(&model.AdtySiteSenderDomain{}).Where("site_id = ? AND resend_domain_id = ?", siteId, row.ResendDomainId).Updates(updates).Error
		})
	}
	if row.ResendDomainId != "" {
		if err := AdtyResend.DeleteDomain(ctx, row.ResendDomainId); err != nil {
			var apiErr *AdtyResendError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
				failure := adtyResendUserError(err)
				if storeErr := writeSenderDomainUpdate(map[string]any{
					"status": model.AdtySenderDomainRemoving, "disabled_by": disabledBy, "cleanup_error": failure.Error(), "retry_at": time.Now().Add(10 * time.Minute).Unix(),
				}); storeErr != nil {
					return storeErr
				}
				return failure
			}
		}
	}
	return writeSenderDomainUpdate(map[string]any{
		"status": model.AdtySenderDomainDisabled, "disabled_by": disabledBy, "cleanup_error": "", "retry_at": 0, "last_checked_at": time.Now().Unix(),
	})
}

func cleanReplacedSenderDomains(ctx context.Context, now int64, siteID int) error {
	var rows []model.AdtySenderDomainCleanup
	q := model.DB.Where("retry_at <= ?", now)
	if siteID != 0 {
		q = q.Where("site_id = ?", siteID)
	}
	if err := q.Order("retry_at,remote_id").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var remoteID string
		var claimRetryAt int64
		// Phase 1 (short tx): lock AdtySite -> AdtySenderDomainCleanup, remove immediately if
		// actively bound, otherwise claim a short lease and release all DB locks before HTTP.
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			var site model.AdtySite
			siteMissing := false
			if err := model.AdtyLockForUpdate(tx).First(&site, row.SiteID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					siteMissing = true
				} else {
					return err
				}
			}
			if !siteMissing && site.Status == model.AdtySiteStatusDeleting {
				return nil
			}
			var job model.AdtySenderDomainCleanup
			lookup := model.AdtyLockForUpdate(tx).Where("remote_id = ? AND site_id = ? AND retry_at <= ?", row.RemoteID, row.SiteID, now).Limit(1).Find(&job)
			if lookup.Error != nil || lookup.RowsAffected == 0 {
				return lookup.Error
			}
			var bound int64
			if !siteMissing {
				if err := tx.Model(&model.AdtySiteSenderDomain{}).Where("resend_domain_id = ? AND status <> ?", job.RemoteID, model.AdtySenderDomainDisabled).Count(&bound).Error; err != nil {
					return err
				}
			}
			if bound > 0 || strings.TrimSpace(job.RemoteID) == "" {
				return tx.Delete(&job).Error
			}
			claimRetryAt = now + 120
			if err := tx.Model(&job).Update("retry_at", claimRetryAt).Error; err != nil {
				return err
			}
			remoteID = job.RemoteID
			return nil
		}); err != nil {
			common.SysError(common.LogText("sender domain cleanup job remote_id=%s site=%d: %s", row.RemoteID, row.SiteID, err.Error()))
			continue
		}
		if remoteID == "" {
			continue
		}

		// Phase 2 (outside tx, no DB locks held): delete the remote domain via Resend API.
		delErr := deleteSenderDomainIfPresent(ctx, remoteID)

		// Phase 3 (short tx): re-lock AdtySite -> AdtySenderDomainCleanup and persist retry backoff
		// on failure or remove the completed cleanup row on success.
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			var site model.AdtySite
			siteMissing := false
			if err := model.AdtyLockForUpdate(tx).First(&site, row.SiteID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					siteMissing = true
				} else {
					return err
				}
			}
			if !siteMissing && site.Status == model.AdtySiteStatusDeleting {
				return nil
			}
			var job model.AdtySenderDomainCleanup
			lookup := model.AdtyLockForUpdate(tx).Where("remote_id = ? AND site_id = ?", remoteID, row.SiteID).Limit(1).Find(&job)
			if lookup.Error != nil || lookup.RowsAffected == 0 {
				return lookup.Error
			}
			if delErr != nil {
				if job.RetryAt != claimRetryAt {
					return nil
				}
				return tx.Model(&job).Updates(map[string]any{"retry_at": now + 600, "last_error": delErr.Error()}).Error
			}
			return tx.Delete(&job).Error
		}); err != nil {
			common.SysError(common.LogText("sender domain cleanup job remote_id=%s site=%d: %s", row.RemoteID, row.SiteID, err.Error()))
		}
	}
	return nil
}

// RunAdtySenderDomainCheckOnce 清理已替换、停用或已关停分站的存量托管发件域名，并重试关站通知邮件。
// R-37 (#5) / R3-43: 平台托管 Resend 发件域名已退役，不再把历史 pending 记录升级为 verified。
func RunAdtySenderDomainCheckOnce(ctx context.Context, now time.Time) error {
	if err := cleanReplacedSenderDomains(ctx, now.Unix(), 0); err != nil {
		common.SysError(common.LogText("clean replaced sender domains: %s", err.Error()))
	}
	var cleanup []model.AdtySiteSenderDomain
	closedSites := model.DB.Model(&model.AdtySite{}).Select("id").Where("status = ?", model.AdtySiteStatusClosed)
	deletingSites := model.DB.Model(&model.AdtySite{}).Select("id").Where("status = ?", model.AdtySiteStatusDeleting)
	if err := model.DB.Where("status <> ? AND retry_at <= ? AND site_id NOT IN (?) AND (status = ? OR site_id IN (?))",
		model.AdtySenderDomainDisabled, now.Unix(), deletingSites, model.AdtySenderDomainRemoving, closedSites).
		Find(&cleanup).Error; err != nil {
		return err
	}
	for _, row := range cleanup {
		if err := DisableAdtySiteSenderDomain(ctx, row.SiteId, row.DisabledBy); err != nil {
			common.SysError(common.LogText("sender domain cleanup site %d: %s", row.SiteId, err.Error()))
		}
	}
	if err := RunAdtySiteCloseMailRetryOnce(now); err != nil {
		common.SysError(common.LogText("close migration mail retry: %s", err.Error()))
	}
	return nil
}

// adtyRemoveSenderDomainOfClosedSite 在关站后删掉发件域名（AdtyOnSiteClosed）。失败只记日志。
func adtyRemoveSenderDomainOfClosedSite(site *model.AdtySite) {
	if err := DisableAdtySiteSenderDomain(context.Background(), site.Id, 0); err != nil {
		common.SysError(common.LogText("remove sender domain of closed site %d failed", site.Id))
	}
}

func startAdtySenderDomainTask() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := RunAdtySenderDomainCheckOnce(ctx, time.Now()); err != nil {
				common.SysError(common.LogText("sender domain check task: %s", err.Error()))
			}
			cancel()
		}
	}()
}

func startAdtySiteCloseMailRetryTask() {
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := RunAdtySiteCloseMailRetryOnce(time.Now()); err != nil {
				common.SysError(common.LogText("site close mail retry task: %s", err.Error()))
			}
		}
	}()
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtySenderDomainTask, startAdtySiteCloseMailRetryTask)
	// The close transaction queues the mail batch. The retry worker dispatches it.
	AdtyOnSiteMarkupIncrease = adtyNotifySiteMarkupIncrease
	AdtyOnSiteCostFactorChanged = adtyNotifySiteCostFactor
	previous := AdtyOnSiteClosed
	AdtyOnSiteClosed = func(site *model.AdtySite) {
		if previous != nil {
			previous(site)
		}
		adtyRemoveSenderDomainOfClosedSite(site)
	}
}

func adtyMailWhen(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04 MST")
}

func adtySiteCustomerUsers(siteId int) []model.User {
	var users []model.User
	if err := model.DB.Select("id", "email", "setting", "site_id").Where("site_id = ? AND staff_site_id = 0", siteId).Find(&users).Error; err != nil {
		common.SysError(common.LogText("load site %d customers for mail: %s", siteId, err.Error()))
		return nil
	}
	return users
}

const (
	// AdtySiteCloseMailSending marks a recipient row that is currently claimed
	// by an in-flight delivery attempt so concurrent workers cannot double-send.
	AdtySiteCloseMailSending = "sending"
	// adtySiteCloseMailMaxAutoAttempts caps automatic background retry attempts
	// per recipient; Root manual retries may continue past this cap.
	adtySiteCloseMailMaxAutoAttempts = 5
)

// AdtySiteCloseMailBatchView exposes the close-migration mail batch and its
// recipient inventory for Root inspection and manual retry (A.16.2).
type AdtySiteCloseMailBatchView struct {
	model.AdtySiteCloseMailBatch
	Subdomain        string                             `json:"subdomain"`
	PendingCount     int64                              `json:"pending_count"`
	SendingCount     int64                              `json:"sending_count"`
	Recipients       []model.AdtySiteCloseMailRecipient `json:"recipients,omitempty"`
	FailedRecipients []model.AdtySiteCloseMailRecipient `json:"failed_recipients,omitempty"`
}

var defaultAdtyDeliverSiteEmail = func(site *model.AdtySite, subject, receiver, content string) error {
	return sendSubsiteConfiguredEmail(site, subject, receiver, content)
}

// AdtyDeliverSiteEmail is the official synchronous delivery used when a
// close-migration batch must record success or failure (A.16.2). Tests may
// replace it. Mail failure must not block internal migration.
var AdtyDeliverSiteEmail = defaultAdtyDeliverSiteEmail

// AdtySiteCloseMailIdempotencyKey returns the deterministic Resend idempotency
// key for a close-migration recipient row.
func AdtySiteCloseMailIdempotencyKey(batchId int64, userId int) string {
	return fmt.Sprintf("site-close-mail:%d:%d", batchId, userId)
}

// SendAdtySiteCloseMigrationMail sends the official English
// close-migration mail and records the batch inventory required by A.16.2:
// target site, recipient count, success/fail counts, send time, and retry
// status. Already successful recipients are not mailed again. A delivery
// error is recorded as retryable and does not abort the rest of the batch
// or the close path. Each message is sent to one recipient only.
func SendAdtySiteCloseMigrationMail(site *model.AdtySite) error {
	return sendAdtySiteCloseMigrationMailWithOptions(site, false, 0, false)
}

// RetryAdtySiteCloseMigrationMail resends only failed or pending recipients
// of the official close-migration batch. It must not leak another recipient's
// email, or another same-email account's orders or balances.
func RetryAdtySiteCloseMigrationMail(site *model.AdtySite) error {
	return sendAdtySiteCloseMigrationMailWithOptions(site, true, 0, false)
}

// ListAdtySiteCloseMailBatches lists close-migration mail batches for Root
// inspection, optionally filtered by retry_status.
func ListAdtySiteCloseMailBatches(retryStatus string, start, limit int) ([]AdtySiteCloseMailBatchView, int64, error) {
	if model.DB == nil {
		return nil, 0, nil
	}
	query := model.DB.Model(&model.AdtySiteCloseMailBatch{})
	if status := strings.TrimSpace(retryStatus); status != "" {
		query = query.Where("retry_status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if start < 0 {
		start = 0
	}
	var batches []model.AdtySiteCloseMailBatch
	if err := query.Order("id DESC").Offset(start).Limit(limit).Find(&batches).Error; err != nil {
		return nil, 0, err
	}
	out := make([]AdtySiteCloseMailBatchView, 0, len(batches))
	for _, b := range batches {
		view, err := buildAdtySiteCloseMailBatchView(b, "")
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *view)
	}
	return out, total, nil
}

// GetAdtySiteCloseMailBatch returns the close-migration mail batch and
// recipient failure/status inventory for a site.
func GetAdtySiteCloseMailBatch(siteId int, statusFilter string) (*AdtySiteCloseMailBatchView, error) {
	if siteId <= 0 || model.DB == nil {
		return nil, common.NewMessage("Invalid parameters")
	}
	var batch model.AdtySiteCloseMailBatch
	if err := model.DB.Where("site_id = ?", siteId).Order("revision DESC").First(&batch).Error; err != nil {
		return nil, err
	}
	return buildAdtySiteCloseMailBatchView(batch, statusFilter)
}

func buildAdtySiteCloseMailBatchView(batch model.AdtySiteCloseMailBatch, statusFilter string) (*AdtySiteCloseMailBatchView, error) {
	view := &AdtySiteCloseMailBatchView{AdtySiteCloseMailBatch: batch}
	var site model.AdtySite
	if model.DB.Select("id", "subdomain").Where("id = ?", batch.SiteId).Limit(1).Find(&site).RowsAffected > 0 {
		view.Subdomain = site.Subdomain
	}
	_ = model.DB.Model(&model.AdtySiteCloseMailRecipient{}).
		Where("batch_id = ? AND status = ?", batch.Id, model.AdtySiteCloseMailPending).
		Count(&view.PendingCount).Error
	_ = model.DB.Model(&model.AdtySiteCloseMailRecipient{}).
		Where("batch_id = ? AND status = ?", batch.Id, AdtySiteCloseMailSending).
		Count(&view.SendingCount).Error
	recQuery := model.DB.Where("batch_id = ?", batch.Id)
	if filter := strings.TrimSpace(statusFilter); filter != "" {
		recQuery = recQuery.Where("status = ?", filter)
	}
	if err := recQuery.Order("user_id").Find(&view.Recipients).Error; err != nil {
		return nil, err
	}
	for _, r := range view.Recipients {
		if r.Status == model.AdtySiteCloseMailFailed {
			view.FailedRecipients = append(view.FailedRecipients, r)
		}
	}
	if strings.TrimSpace(statusFilter) != "" && strings.TrimSpace(statusFilter) != model.AdtySiteCloseMailFailed {
		if err := model.DB.Where("batch_id = ? AND status = ?", batch.Id, model.AdtySiteCloseMailFailed).
			Order("user_id").Find(&view.FailedRecipients).Error; err != nil {
			return nil, err
		}
	}
	return view, nil
}

// ResolveAdtyCloseMailRecipient records a Root decision after checking the provider.
func ResolveAdtyCloseMailRecipient(siteID, operatorID int, id int64, attempt int, decision, reason string) error {
	_, err := ResolveAdtyCloseMailRecipientWithAudit(siteID, operatorID, id, attempt, decision, reason)
	return err
}

func ResolveAdtyCloseMailRecipientWithAudit(siteID, operatorID int, id int64, attempt int, decision, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || (decision != "retry" && decision != "delivered") {
		return "", common.NewMessage("Invalid parameters")
	}
	var batchID int64
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteCloseMailRecipient
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND site_id = ?", id, siteID).First(&row).Error; err != nil {
			return err
		}
		var batch model.AdtySiteCloseMailBatch
		if err := model.AdtyLockForUpdate(tx).First(&batch, row.BatchId).Error; err != nil {
			return err
		}
		if batch.RetryStatus == "superseded" || row.AttemptCount != attempt || (row.Status != "review" && row.Status != model.AdtySiteCloseMailFailed) {
			return common.NewMessage("Refresh and try again")
		}
		updates := map[string]any{"status": model.AdtySiteCloseMailSuccess, "last_error": ""}
		if decision == "retry" {
			updates["status"] = model.AdtySiteCloseMailPending
			updates["first_attempt_at"] = 0
			updates["retry_allowance"] = max(row.RetryAllowance, row.AttemptCount-adtySiteCloseMailMaxAutoAttempts+1)
		}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return err
		}
		batchID = row.BatchId
		var auditErr error
		auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, siteID, "close_mail_"+decision, operatorID, map[string]any{"recipient_id": id, "status": row.Status, "attempt": attempt}, map[string]any{"status": updates["status"], "batch": batchID}, reason)
		return auditErr
	})
	if err != nil {
		return "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	if err := refreshAdtySiteCloseMailBatchCounts(batchID, time.Now().Unix()); err != nil {
		return eventID, err
	}
	return eventID, nil
}

// RetryAdtySiteCloseMigrationMailByRoot lets Root inspect and retry failed/pending
// recipients of a site's close-migration mail batch without resending already
// successful users, and records the official audit trail.
func RetryAdtySiteCloseMigrationMailByRoot(siteId, operatorId int, includeSending bool, reason string) (*AdtySiteCloseMailBatchView, error) {
	if siteId <= 0 || model.DB == nil {
		return nil, common.NewMessage("Invalid parameters")
	}
	var site model.AdtySite
	if err := model.DB.Where("id = ?", siteId).First(&site).Error; err != nil {
		return nil, err
	}
	if err := sendAdtySiteCloseMigrationMailWithOptions(&site, true, 0, includeSending); err != nil {
		return nil, err
	}
	view, err := GetAdtySiteCloseMailBatch(siteId, "")
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "retry close migration mail"
	}
	now := time.Now().Unix()
	var auditEvent *model.AuditLog
	if auditTxErr := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.AdtySiteAudit{
			SiteId:     siteId,
			OperatorId: operatorId,
			Action:     "close_mail_retry",
			Reason:     reason,
			NewValue:   fmt.Sprintf("batch=%d total=%d success=%d fail=%d status=%s", view.Id, view.RecipientCount, view.SuccessCount, view.FailCount, view.RetryStatus),
			CreatedAt:  now,
		}).Error; err != nil {
			return err
		}
		if operatorId > 0 {
			var auditErr error
			auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, operatorId, siteId, "site", fmt.Sprintf("%d", siteId), "site.close_mail.retry", reason, "", nil, map[string]any{
				"batch_id":        view.Id,
				"recipient_count": view.RecipientCount,
				"success_count":   view.SuccessCount,
				"fail_count":      view.FailCount,
				"retry_status":    view.RetryStatus,
			}, model.AuditResultSuccess)
			return auditErr
		}
		return nil
	}); auditTxErr != nil {
		common.SysError(common.LogText("persist close mail retry audit intent failed: %s", auditTxErr.Error()))
	} else if auditEvent != nil {
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return view, nil
}

// RunAdtySiteCloseMailRetryOnce scans closing/closed sites that have incomplete
// close-migration mail batches (or closing sites that crashed before creating a
// batch) and retries pending/failed recipients up to the auto-retry cap.
func RunAdtySiteCloseMailRetryOnce(now time.Time) error {
	if model.DB == nil {
		return nil
	}
	siteIDs := make([]int, 0)
	var batches []model.AdtySiteCloseMailBatch
	if err := model.DB.Where("retry_status = ? OR (sent_at = 0 AND retry_status = ?)",
		model.AdtySiteCloseMailRetryRetryable, model.AdtySiteCloseMailRetryNone).
		Order("id").Find(&batches).Error; err != nil {
		return err
	}
	for _, b := range batches {
		if !slices.Contains(siteIDs, b.SiteId) {
			siteIDs = append(siteIDs, b.SiteId)
		}
	}
	var closingSites []model.AdtySite
	if err := model.DB.Where("status IN ?", []string{model.AdtySiteStatusClosing, model.AdtySiteStatusClosed}).
		Order("id").Find(&closingSites).Error; err != nil {
		return err
	}
	siteMap := make(map[int]model.AdtySite, len(closingSites))
	for _, s := range closingSites {
		siteMap[s.Id] = s
		var hasBatch int64
		if model.DB.Model(&model.AdtySiteCloseMailBatch{}).Where("site_id = ?", s.Id).Count(&hasBatch).Error == nil && hasBatch == 0 {
			if s.Status == model.AdtySiteStatusClosing && !slices.Contains(siteIDs, s.Id) {
				siteIDs = append(siteIDs, s.Id)
			}
		}
	}
	for _, siteID := range siteIDs {
		site, ok := siteMap[siteID]
		if !ok {
			if err := model.DB.Where("id = ?", siteID).First(&site).Error; err != nil {
				continue
			}
		}
		if err := sendAdtySiteCloseMigrationMailWithOptions(&site, true, adtySiteCloseMailMaxAutoAttempts, false); err != nil {
			common.SysError(common.LogText("retry close migration mail for site %d failed: %s", site.Id, err.Error()))
		}
	}
	return nil
}

func sendAdtySiteCloseMigrationMail(site *model.AdtySite, retryOnly bool) error {
	return sendAdtySiteCloseMigrationMailWithOptions(site, retryOnly, 0, false)
}

func sendAdtySiteCloseMigrationMailWithOptions(site *model.AdtySite, retryOnly bool, maxAttempts int, includeSending bool) error {
	if site == nil || model.DB == nil {
		return nil
	}
	now := time.Now().Unix()
	batch, err := loadOrCreateAdtySiteCloseMailBatch(site.Id, now)
	if err != nil {
		return err
	}
	if batch.ExecuteAt > 0 {
		copy := *site
		copy.CloseExecuteAt = batch.ExecuteAt
		site = &copy
	}
	if err := ensureAdtySiteCloseMailRecipients(batch, site.Id, now, retryOnly); err != nil {
		return err
	}
	statuses := []string{model.AdtySiteCloseMailPending, model.AdtySiteCloseMailFailed, AdtySiteCloseMailSending}
	var recipients []model.AdtySiteCloseMailRecipient
	if err := model.DB.Where("batch_id = ? AND status IN ?", batch.Id, statuses).
		Order("user_id").Find(&recipients).Error; err != nil {
		return err
	}
	for i := range recipients {
		candidate := &recipients[i]
		claimed, ok, err := claimAdtySiteCloseMailRecipient(candidate.Id, time.Now().Unix(), maxAttempts, includeSending)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		subject, body := renderAdtySiteCloseMigrationMail(site, claimed)
		messageID, deliverErr := deliverAdtySiteCloseMail(site, claimed, subject, body)
		finishedAt := time.Now().Unix()
		nextStatus := model.AdtySiteCloseMailSuccess
		lastError := ""
		if deliverErr != nil {
			nextStatus = model.AdtySiteCloseMailFailed
			if adtyCloseMailOutcomeUnknown(claimed.Provider, deliverErr) {
				nextStatus = "review"
			}
			common.OptionMapRWMutex.RLock()
			mailSecrets := []string{common.SMTPToken, common.OptionMap["ResendApiKey"]}
			if common.SMTPToken != "" {
				mailSecrets = append(mailSecrets, "\x00"+common.SMTPAccount+"\x00"+common.SMTPToken)
			}
			common.OptionMapRWMutex.RUnlock()
			lastError = truncateText(common.SanitizeOutboundError(deliverErr, mailSecrets...).Error(), 1000)
		}
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			update := tx.Model(&model.AdtySiteCloseMailRecipient{}).Where("id = ? AND status = ? AND attempt_count = ?", claimed.Id, AdtySiteCloseMailSending, claimed.AttemptCount).
				Updates(map[string]any{"status": nextStatus, "lease_until": 0, "last_error": lastError, "last_attempt_at": finishedAt, "updated_at": finishedAt})
			if update.Error != nil {
				return update.Error
			}
			return tx.Model(&model.AdtySiteCloseMailAttempt{}).Where("recipient_id = ? AND number = ?", claimed.Id, claimed.AttemptCount).Updates(map[string]any{"outcome": nextStatus, "finished_at": finishedAt, "provider_message_id": messageID}).Error
		}); err != nil {
			return err
		}
	}
	return refreshAdtySiteCloseMailBatchCounts(batch.Id, time.Now().Unix())
}

// Resend can accept a message before a connection or response-body failure.
func adtyCloseMailOutcomeUnknown(provider string, err error) bool {
	if provider != "resend" {
		return errors.Is(err, common.ErrSMTPDeliveryUnknown)
	}
	if errors.Is(err, ErrAdtyResendNotConfigured) {
		return false
	}
	var apiErr *AdtyResendError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= 500 || apiErr.StatusCode == http.StatusRequestTimeout
	}
	return true
}

func claimAdtySiteCloseMailRecipient(recipientId int64, attemptAt int64, maxAttempts int, includeSending bool) (*model.AdtySiteCloseMailRecipient, bool, error) {
	allowedStatuses := []string{model.AdtySiteCloseMailPending, model.AdtySiteCloseMailFailed, AdtySiteCloseMailSending}
	var claimed model.AdtySiteCloseMailRecipient
	var won bool
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteCloseMailRecipient
		lookup := model.AdtyLockForUpdate(tx).Where("id = ?", recipientId).Limit(1).Find(&row)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 || !slices.Contains(allowedStatuses, row.Status) {
			return nil
		}
		var batch model.AdtySiteCloseMailBatch
		if err := model.AdtyLockForUpdate(tx).First(&batch, row.BatchId).Error; err != nil {
			return err
		}
		if batch.RetryStatus == "superseded" {
			return nil
		}
		provider := "smtp"
		if adtyUsesDefaultDeliverSiteEmail() && adtyIsResendActive() && AdtyResend != nil {
			provider = "resend"
		}
		if row.Status == model.AdtySiteCloseMailFailed && row.Provider == "resend" {
			// Older versions classified lost acknowledgements as ordinary failures.
			var prior model.AdtySiteCloseMailAttempt
			if err := tx.Where("recipient_id = ? AND number = ?", row.Id, row.AttemptCount).Limit(1).Find(&prior).Error; err != nil {
				return err
			}
			if prior.Id == 0 || prior.Outcome != model.AdtySiteCloseMailFailed {
				return tx.Model(&row).Updates(map[string]any{"status": "review", "last_error": "Delivery outcome unknown. Reconcile with the mail provider.", "lease_until": 0}).Error
			}
		}
		if row.Status == AdtySiteCloseMailSending {
			if row.LeaseUntil > attemptAt || (row.LeaseUntil == 0 && row.LastAttemptAt > attemptAt-600) {
				return nil
			}
			if err := tx.Model(&model.AdtySiteCloseMailAttempt{}).Where("recipient_id = ? AND number = ? AND outcome = ?", row.Id, row.AttemptCount, AdtySiteCloseMailSending).Update("outcome", "review").Error; err != nil {
				return err
			}
			if row.Provider != "resend" || provider != "resend" || row.FirstAttemptAt <= 0 || attemptAt-row.FirstAttemptAt >= 86400 || (maxAttempts > 0 && row.AttemptCount >= maxAttempts+row.RetryAllowance) {
				return tx.Model(&row).Updates(map[string]any{"status": "review", "last_error": "Delivery outcome unknown. Reconcile with the mail provider.", "lease_until": 0}).Error
			}
		}
		if maxAttempts > 0 && row.AttemptCount >= maxAttempts+row.RetryAllowance {
			return nil
		}
		if row.FirstAttemptAt == 0 {
			row.FirstAttemptAt = attemptAt
		}
		nextAttempt := row.AttemptCount + 1
		res := tx.Model(&model.AdtySiteCloseMailRecipient{}).
			Where("id = ? AND status IN ? AND attempt_count = ?", row.Id, allowedStatuses, row.AttemptCount).
			Updates(map[string]any{
				"status": AdtySiteCloseMailSending, "lease_until": attemptAt + 600, "first_attempt_at": row.FirstAttemptAt, "provider": provider,
				"attempt_count":   nextAttempt,
				"last_attempt_at": attemptAt,
				"updated_at":      attemptAt,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if err := tx.Create(&model.AdtySiteCloseMailAttempt{RecipientID: row.Id, Number: nextAttempt, Provider: provider, StartedAt: attemptAt, Outcome: AdtySiteCloseMailSending}).Error; err != nil {
			return err
		}
		row.Provider = provider
		row.Status = AdtySiteCloseMailSending
		row.AttemptCount = nextAttempt
		row.LastAttemptAt = attemptAt
		row.UpdatedAt = attemptAt
		claimed = row
		won = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if !won {
		return nil, false, nil
	}
	return &claimed, true, nil
}

func adtyUsesDefaultDeliverSiteEmail() bool {
	if AdtyDeliverSiteEmail == nil {
		return false
	}
	return reflect.ValueOf(AdtyDeliverSiteEmail).Pointer() == reflect.ValueOf(defaultAdtyDeliverSiteEmail).Pointer()
}

func adtyIsResendActive() bool {
	common.OptionMapRWMutex.RLock()
	provider := common.OptionMap["EmailProvider"]
	common.OptionMapRWMutex.RUnlock()
	return provider == "resend"
}

func deliverAdtySiteCloseMail(site *model.AdtySite, recipient *model.AdtySiteCloseMailRecipient, subject, body string) (string, error) {
	if AdtyDeliverSiteEmail == nil {
		return "", errors.New("mail delivery function is nil")
	}
	if recipient.Provider == "resend" {
		return AdtyResend.SendEmail(context.Background(), AdtyResendEmail{
			From:           AdtySenderFor(site),
			To:             []string{recipient.Email},
			Subject:        subject,
			HTML:           body,
			IdempotencyKey: AdtySiteCloseMailIdempotencyKey(recipient.BatchId, recipient.UserId),
		})
	}
	if adtyUsesDefaultDeliverSiteEmail() {
		return "", common.SendSMTPEmailFrom(AdtySenderFor(site), subject, recipient.Email, body)
	}
	return "", AdtyDeliverSiteEmail(site, subject, recipient.Email, body)
}

func loadOrCreateAdtySiteCloseMailBatch(siteId int, now int64) (*model.AdtySiteCloseMailBatch, error) {
	var batch model.AdtySiteCloseMailBatch
	lookup := model.DB.Where("site_id = ?", siteId).Order("revision DESC").Limit(1).Find(&batch)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected == 1 {
		return &batch, nil
	}
	batch = model.AdtySiteCloseMailBatch{
		SiteId:      siteId,
		RetryStatus: model.AdtySiteCloseMailRetryNone,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := model.DB.Create(&batch).Error; err != nil {
		// A concurrent first send may have inserted the unique site row.
		if err := model.DB.Where("site_id = ?", siteId).Order("revision DESC").First(&batch).Error; err != nil {
			return nil, err
		}
	}
	return &batch, nil
}

func collectAdtySiteCloseMailEligibleUsers(tx *gorm.DB, siteId int) ([]model.User, error) {
	byID := make(map[int]model.User)
	order := make([]int, 0)
	addUsers := func(rows []model.User) {
		for _, u := range rows {
			if u.Id <= 0 || u.StaffSiteId != 0 {
				continue
			}
			if _, exists := byID[u.Id]; !exists {
				order = append(order, u.Id)
			}
			byID[u.Id] = u
		}
	}
	var directUsers []model.User
	if err := tx.Select("id", "username", "email", "site_id", "staff_site_id").
		Where("site_id = ? AND staff_site_id = 0", siteId).
		Order("id").Find(&directUsers).Error; err != nil {
		return nil, err
	}
	addUsers(directUsers)

	var inheritedUsers []model.User
	if err := tx.Select("id", "username", "email", "site_id", "staff_site_id").
		Where("site_id = 0 AND inherited_from_site_id = ? AND staff_site_id = 0", siteId).
		Order("id").Find(&inheritedUsers).Error; err != nil {
		return nil, err
	}
	addUsers(inheritedUsers)

	archivedIDs := make([]int, 0)
	if tx.Migrator().HasTable(&model.AdtySiteCustomer{}) {
		var custIDs []int
		if err := tx.Model(&model.AdtySiteCustomer{}).Where("site_id = ?", siteId).Pluck("user_id", &custIDs).Error; err != nil {
			return nil, err
		}
		for _, id := range custIDs {
			if id > 0 && !slices.Contains(archivedIDs, id) {
				archivedIDs = append(archivedIDs, id)
			}
		}
	}
	if tx.Migrator().HasTable(&model.AdtyUserSiteMigration{}) {
		var migIDs []int
		if err := tx.Model(&model.AdtyUserSiteMigration{}).Where("source_site_id = ?", siteId).Pluck("user_id", &migIDs).Error; err != nil {
			return nil, err
		}
		for _, id := range migIDs {
			if id > 0 && !slices.Contains(archivedIDs, id) {
				archivedIDs = append(archivedIDs, id)
			}
		}
	}
	if len(archivedIDs) > 0 {
		var archivedUsers []model.User
		if err := tx.Select("id", "username", "email", "site_id", "staff_site_id").
			Where("id IN ? AND staff_site_id = 0", archivedIDs).
			Order("id").Find(&archivedUsers).Error; err != nil {
			return nil, err
		}
		addUsers(archivedUsers)
	}
	slices.Sort(order)
	out := make([]model.User, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}

func ensureAdtySiteCloseMailRecipients(batch *model.AdtySiteCloseMailBatch, siteId int, now int64, _ bool) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var lockedBatch model.AdtySiteCloseMailBatch
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", batch.Id).First(&lockedBatch).Error; err != nil {
			return err
		}
		users, err := collectAdtySiteCloseMailEligibleUsers(tx, siteId)
		if err != nil {
			return err
		}
		var existingUserIDs []int
		if err := tx.Model(&model.AdtySiteCloseMailRecipient{}).
			Where("batch_id = ?", batch.Id).
			Pluck("user_id", &existingUserIDs).Error; err != nil {
			return err
		}
		existingSet := make(map[int]bool, len(existingUserIDs))
		for _, id := range existingUserIDs {
			existingSet[id] = true
		}
		total := int64(len(existingUserIDs))
		for _, user := range users {
			email := strings.TrimSpace(user.Email)
			if email == "" || existingSet[user.Id] {
				continue
			}
			row := model.AdtySiteCloseMailRecipient{
				BatchId:   batch.Id,
				SiteId:    siteId,
				UserId:    user.Id,
				Username:  user.Username,
				Email:     email,
				Status:    model.AdtySiteCloseMailPending,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "batch_id"}, {Name: "user_id"}},
				DoNothing: true,
			}).Create(&row).Error; err != nil {
				return err
			}
			existingSet[user.Id] = true
			total++
		}
		updates := map[string]any{
			"recipient_count": total,
			"updated_at":      now,
		}
		if total > 0 && lockedBatch.RetryStatus == model.AdtySiteCloseMailRetryNone {
			updates["retry_status"] = model.AdtySiteCloseMailRetryRetryable
		}
		if err := tx.Model(&model.AdtySiteCloseMailBatch{}).Where("id = ?", batch.Id).Updates(updates).Error; err != nil {
			return err
		}
		batch.RecipientCount = int(total)
		if status, ok := updates["retry_status"].(string); ok {
			batch.RetryStatus = status
		}
		return nil
	})
}

func refreshAdtySiteCloseMailBatchCounts(batchId int64, now int64) error {
	var total, success, failed, incomplete int64
	if err := model.DB.Model(&model.AdtySiteCloseMailRecipient{}).Where("batch_id = ?", batchId).Count(&total).Error; err != nil {
		return err
	}
	if err := model.DB.Model(&model.AdtySiteCloseMailRecipient{}).Where("batch_id = ? AND status = ?", batchId, model.AdtySiteCloseMailSuccess).Count(&success).Error; err != nil {
		return err
	}
	if err := model.DB.Model(&model.AdtySiteCloseMailRecipient{}).Where("batch_id = ? AND status = ?", batchId, model.AdtySiteCloseMailFailed).Count(&failed).Error; err != nil {
		return err
	}
	if err := model.DB.Model(&model.AdtySiteCloseMailRecipient{}).Where("batch_id = ? AND status <> ?", batchId, model.AdtySiteCloseMailSuccess).Count(&incomplete).Error; err != nil {
		return err
	}
	retryStatus := model.AdtySiteCloseMailRetryComplete
	if total == 0 {
		retryStatus = model.AdtySiteCloseMailRetryNone
	} else if failed > 0 || incomplete > 0 || success < total {
		retryStatus = model.AdtySiteCloseMailRetryRetryable
	}
	return model.DB.Model(&model.AdtySiteCloseMailBatch{}).Where("id = ? AND retry_status <> ?", batchId, "superseded").Updates(map[string]any{
		"recipient_count": total,
		"success_count":   success,
		"fail_count":      failed,
		"sent_at":         now,
		"retry_status":    retryStatus,
		"updated_at":      now,
	}).Error
}

func adtyCloseMigrationSupportContact(site *model.AdtySite) string {
	if contact := strings.TrimSpace(AdtyBrandFor(site).SupportContact); contact != "" {
		return contact
	}
	if contact := strings.TrimSpace(adty_setting.GetSiteSetting().SupportEmail); contact != "" {
		return contact
	}
	return ""
}

func renderAdtySiteCloseMigrationMail(site *model.AdtySite, recipient *model.AdtySiteCloseMailRecipient) (subject, body string) {
	support := adtyCloseMigrationSupportContact(site)
	if support == "" {
		support = "the main site support channel"
	}
	name := AdtyBrandFor(site).Name
	subject = name + " 即将关闭，请登录主站 / Site closure and main-site sign-in"
	intro := name + " is closing."
	if when := adtyMailWhen(site.CloseExecuteAt); when != "" {
		intro = name + " will close on " + when + "."
	}
	paragraphs := []string{
		"您的账号将迁入主站。请使用原用户名 " + recipient.Username + " 登录主站并完成验证。",
		"您的订单和余额仍归原账号。分站 API Key 将被撤销，请在主站重新创建。客服联系方式：" + support,
		"Your account will move to the main site only. It cannot be transferred to another site.",
		"Sign in on the main site with your unique username: " + recipient.Username + ". Use that username to select your account, then verify your password and complete any required verification.",
		"Your orders and balance stay with this account. They will not be merged with another account that uses the same email address.",
		"Your API keys from the closing site will be revoked at migration. Your historical records will be kept; create new API keys on the main site.",
	}
	if tpl, customized, err := GetAdtyNoticeTemplate("site_close_migration"); err == nil && customized && tpl != nil {
		vars := map[string]string{
			"site_name": name,
			"username":  recipient.Username,
			"support":   support,
		}
		zhTitle := strings.TrimSpace(applyAdtyNoticeVars(tpl.TitleZh, vars, name))
		enTitle := strings.TrimSpace(applyAdtyNoticeVars(tpl.TitleEn, vars, name))
		zhBody := strings.TrimSpace(applyAdtyNoticeVars(tpl.ContentZh, vars, name))
		enBody := strings.TrimSpace(applyAdtyNoticeVars(tpl.ContentEn, vars, name))
		if zhTitle != "" && enTitle != "" {
			subject = zhTitle + " / " + enTitle
		} else if zhTitle != "" {
			subject = zhTitle
		} else if enTitle != "" {
			subject = enTitle
		}
		customLead := make([]string, 0, 2)
		if zhBody != "" {
			customLead = append(customLead, zhBody)
		}
		if enBody != "" {
			customLead = append(customLead, enBody)
		}
		if len(customLead) > 0 {
			paragraphs = append(customLead, paragraphs...)
		}
	}
	return subject, AdtyRenderMail(site, AdtyMailMessage{
		Title: subject, Intro: "分站关闭时间 / Site closure: " + adtyMailWhen(site.CloseExecuteAt) + ". " + intro,
		Paragraphs:  paragraphs,
		ActionLabel: "登录主站 / Sign in on the main site", ActionURL: AdtySiteBaseURL(nil),
		Footnote: "For support, contact: " + support,
	})
}

// adtyNotifySiteClosing 给这个站的客户发官方关站迁移邮件（A.16.2）。
// 发送失败只记入批次失败清单，不能阻断后续内部迁移。
func adtyNotifySiteClosing(site *model.AdtySite) {
	if site == nil {
		return
	}
	if err := SendAdtySiteCloseMigrationMail(site); err != nil {
		common.SysError(common.LogText("close migration mail for site %d failed: %s", site.Id, err.Error()))
	}
}

func adtyMarkupRaisedDetail(raised []model.AdtySitePrice) (string, int64) {
	parts := make([]string, 0, len(raised))
	var at int64
	for _, row := range raised {
		group := row.Group
		if group == "" {
			group = "all groups"
		}
		if modelName := strings.TrimSpace(row.Model); modelName != "" {
			group = group + " / " + modelName
		}
		next := row.Markup
		parts = append(parts, group+" → "+next.String())
		if row.UpdatedAt > at {
			at = row.UpdatedAt
		}
	}
	return strings.Join(parts, "; "), at
}

// adtyNotifySiteMarkupIncrease 给 k 变高的客户发邮件。
func adtyNotifySiteMarkupIncrease(site *model.AdtySite, userId int, raised []model.AdtySitePrice) {
	if site == nil || len(raised) == 0 {
		return
	}
	detail, at := adtyMarkupRaisedDetail(raised)
	name := AdtyBrandFor(site).Name
	message := AdtyMailMessage{
		Title:       "Prices on " + name + " have increased",
		Intro:       "New pricing: " + detail,
		Paragraphs:  []string{"The new pricing applies to requests accepted from " + adtyMailWhen(at) + ". Previously accepted requests and tasks keep their original price."},
		ActionLabel: "Sign in", ActionURL: AdtySitePublicBaseURL(site),
	}
	var users []model.User
	if userId > 0 {
		if user, err := model.GetUserById(userId, false); err == nil {
			users = []model.User{*user}
		}
	} else {
		users = adtySiteCustomerUsers(site.Id)
	}
	for i := range users {
		adtySendSitePricingMail(site, &users[i], message)
	}
}

func adtySendSitePricingMail(site *model.AdtySite, user *model.User, message AdtyMailMessage) {
	if user == nil || strings.TrimSpace(user.Email) == "" || AdtySendSiteEmailAsync == nil {
		return
	}
	AdtySendSiteEmailAsync(site, message.Title, user.Email, AdtyRenderMail(site, message))
}

// adtyNotifySiteCostFactorPending 在提高 d 写入待生效时通知站长，不把新系数当成已经生效。
// 平台向站长发送的管理通知固定使用平台发信通道（site = nil），不走分站自有发件域。
func adtyNotifySiteCostFactorPending(site *model.AdtySite) {
	if site == nil || site.PendingCostFactor == nil {
		return
	}
	owner, err := model.GetUserById(site.OwnerUserId, false)
	if err != nil {
		common.SysError(common.LogText("load site %d owner for pending cost factor mail: %s", site.Id, err.Error()))
		return
	}
	adtySendSitePricingMail(nil, owner, AdtyMailMessage{
		Title: "Your site cost factor will change",
		Intro: "Your site cost factor will change to " + site.PendingCostFactor.String() + " on " + adtyMailWhen(site.PendingCostFactorAt) + ". Until then the current factor applies.",
	})
}

// adtyNotifySiteCostFactor 给站长发邮件，说明当前生效的 d。
// 平台向站长发送的管理通知固定使用平台发信通道（site = nil），不走分站自有发件域。
func adtyNotifySiteCostFactor(site *model.AdtySite) {
	if site == nil {
		return
	}
	owner, err := model.GetUserById(site.OwnerUserId, false)
	if err != nil {
		common.SysError(common.LogText("load site %d owner for cost factor mail: %s", site.Id, err.Error()))
		return
	}
	factor, ok := AdtySiteCostFactor(site)
	if !ok {
		factor = decimal.Zero
	}
	adtySendSitePricingMail(nil, owner, AdtyMailMessage{
		Title: "Your site cost factor changed",
		Intro: "Your site cost factor is now " + factor.String() + ".",
	})
}

// 支付跳回（任务 11.3，执行层决定 30）。易支付的 return_url 一律是主站的 /pay/result/<订单号>.<签名>：
// 网关会往 return_url 后面加自己的参数，所以不带 query；签名防止拿别人的订单号探测订单属于哪个站。
// 分站客户在主站没有登录会话（会话按 Host），这里不能靠登录判断订单归属，靠的是签名：只有我们生成的地址才会跳。

const adtyPayResultPath = "/pay/result/"

func adtyPayResultSignature(tradeNo string) string {
	return common.GenerateHMAC("adty-pay-result:" + tradeNo)[:16]
}

// AdtyPayResultURL 是给易支付的 return_url。
func AdtyPayResultURL(tradeNo string) string {
	return AdtySiteBaseURL(nil) + adtyPayResultPath + tradeNo + "." + adtyPayResultSignature(tradeNo)
}

// AdtyPayResultURLForHost returns a signed pay-result return URL that preserves
// the main-host session when a site owner starts margin top-up on the main host.
func AdtyPayResultURLForHost(tradeNo string, preferMainHost bool) string {
	if !preferMainHost {
		return AdtyPayResultURL(tradeNo)
	}
	payload := tradeNo + ".main"
	return AdtySiteBaseURL(nil) + adtyPayResultPath + payload + "." + adtyPayResultSignature(payload)
}

// AdtyPayResultTarget 是 /pay/result/<订单号>.<签名> 要跳到的地址：签名对、订单存在时，跳订单所属用户的站点的
// 钱包并带上订单号；其余一律跳主站钱包，不带订单号。
func AdtyPayResultTarget(token string) string {
	fallback := AdtySiteBaseURL(nil) + "/wallet"
	// 订单号本身可能带点：签名是最后一段
	dot := strings.LastIndex(token, ".")
	if dot <= 0 {
		return fallback
	}
	payload, signature := token[:dot], token[dot+1:]
	if subtle.ConstantTimeCompare([]byte(signature), []byte(adtyPayResultSignature(payload))) != 1 {
		return fallback
	}
	tradeNo := payload
	preferMainHost := false
	if trimmed, ok := strings.CutSuffix(payload, ".main"); ok && trimmed != "" {
		tradeNo = trimmed
		preferMainHost = true
	}
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		return fallback
	}
	ext, err := model.GetAdtyTopupOrder(tradeNo)
	if err != nil {
		return fallback
	}
	if ext.IsOwnerBusiness() {
		site := AdtySiteById(ext.SiteId)
		if site == nil || site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting {
			return fallback
		}
		baseURL := AdtySitePublicBaseURL(site)
		if preferMainHost {
			baseURL = AdtySiteBaseURL(nil)
		}
		target := baseURL + "/site-owner/wallets?trade_no=" + url.QueryEscape(tradeNo)
		if ext.IsOwnerMargin() {
			target += "&business=owner_margin_topup#margin"
		}
		return target
	}
	if ext.IsTeamTopup() {
		return AdtySitePublicBaseURL(AdtySiteForUser(topUp.UserId)) + "/my-team?trade_no=" + url.QueryEscape(tradeNo)
	}
	// 跳回优先用站长的自定义域名：客户是在站长的牌子上付的钱，就该回到站长的站。
	// 客户在那个域名上本来就登录着，回来直接看到钱包和这笔订单。
	return AdtySitePublicBaseURL(AdtySiteForUser(topUp.UserId)) + "/wallet?trade_no=" + url.QueryEscape(tradeNo)
}
