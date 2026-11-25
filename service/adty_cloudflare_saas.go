package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"golang.org/x/net/publicsuffix"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 自定义域名（任务 11.8），走 Cloudflare for SaaS 的自定义主机名。按官方 API 文档的示例写，没有实测
// （11.0 推迟到上线前），摘要在 adtyai-design/research/verify-p11/cf_api_digest.md。
// 测试替换 AdtyCloudflare 的 BaseURL 和 HTTP，指向 httptest 假服务器。API Token 和响应原文都不打日志：
// Turnstile widget 的 PUT 响应带 secret。

const adtyCloudflareDefaultBaseURL = "https://api.cloudflare.com/client/v4"

// AdtyCloudflareClient 调 Cloudflare 的 REST API。字段都可以在测试里换掉。
type AdtyCloudflareClient struct {
	BaseURL string
	HTTP    *http.Client
	Token   func() string
}

// AdtyCloudflare 是业务代码用的客户端，Token 读分站设置里的 cloudflare_apiToken。
var AdtyCloudflare = &AdtyCloudflareClient{
	BaseURL: adtyCloudflareDefaultBaseURL,
	HTTP:    NewVerifiedDirectNoRedirectHTTPClient(20 * time.Second),
	Token:   func() string { return adty_setting.GetResellerSetting().CloudflareApiToken },
}

var (
	// adtyCustomHostnameRecordDelay：创建后隔多久再 GET 取验证记录（POST 的响应可能不带）。测试换成 0。
	adtyCustomHostnameRecordDelay = 3 * time.Second
	// adtyCloudflareSleep 用来等待 Retry-After 和上面的延时，测试换掉。
	adtyCloudflareSleep = time.Sleep
	// adtyCustomDomainTimeout：这么久还没通就标 failed。
	adtyCustomDomainTimeout = 72 * time.Hour
	// adtyLookupOwnershipTXT 查归属 TXT。测试换成假的，避免真去解析。
	adtyLookupOwnershipTXT = func(ctx context.Context, name string) ([]string, error) {
		return net.DefaultResolver.LookupTXT(ctx, name)
	}
)

const (
	adtyCloudflareMaxRetries      = 3
	adtyCloudflareMaxRetryAfter   = 60 * time.Second
	adtyCloudflareDuplicateCode   = 1406
	adtyTurnstileDomainsPerWidget = 10
)

var (
	ErrAdtyCustomDomainOff          = common.NewMessage("Custom domains are not available")
	ErrAdtyCustomDomainInvalid      = common.NewMessage("Enter a subdomain such as api.example.com; root domains and our own domains cannot be used")
	ErrAdtyCustomDomainTaken        = common.NewMessage("This domain is already used by another site")
	ErrAdtyCustomDomainLimit        = common.NewMessage("This site already has the maximum number of custom domains")
	ErrAdtyCustomDomainRate         = common.NewMessage("This site has added too many custom domains this hour")
	ErrAdtyCustomDomainRoleInvalid  = common.NewMessage("Domain role must be site or base_url")
	ErrAdtyCustomDomainRoleLimit    = common.NewMessage("This site already has a custom domain for this purpose; remove it first to replace")
	ErrAdtyCustomDomainPairRequired = common.NewMessage("Both site domain and API domain are required")
	ErrAdtyCustomDomainPairSame     = common.NewMessage("Site domain and API domain must be different")
	ErrAdtyCloudflareRejected       = common.NewMessage("Cloudflare rejected the request; check the API token permissions")
	ErrAdtyCloudflareFailed         = common.NewMessage("Cloudflare could not complete the request; try again later")
)

// AdtyCloudflareError 是 Cloudflare 返回的错误（外壳 success=false 或非 2xx）。
type AdtyCloudflareError struct {
	StatusCode int
	Code       int
	Message    string
}

func (e *AdtyCloudflareError) Error() string {
	return fmt.Sprintf("cloudflare api error: status=%d code=%d message=%s", e.StatusCode, e.Code, e.Message)
}

type adtyCloudflareEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result common.RawMessage `json:"result"`
}

// do 发一个请求，429 时按 Retry-After 退避后重试。out 为 nil 时丢弃 result。
func (c *AdtyCloudflareClient) do(ctx context.Context, method, path string, body any, out any) error {
	token := strings.TrimSpace(c.Token())
	if token == "" {
		return ErrAdtyCustomDomainOff
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = common.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, reader)
		if err != nil {
			return common.SanitizeOutboundError(err, token)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return common.SanitizeOutboundError(err, token)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < adtyCloudflareMaxRetries {
			wait := time.Second
			if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds > 0 {
				wait = time.Duration(seconds) * time.Second
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			adtyCloudflareSleep(min(wait, adtyCloudflareMaxRetryAfter))
			continue
		}
		var envelope adtyCloudflareEnvelope
		decodeErr := common.DecodeJson(io.LimitReader(resp.Body, 4<<20), &envelope)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.Success || decodeErr != nil {
			apiErr := &AdtyCloudflareError{StatusCode: resp.StatusCode}
			if len(envelope.Errors) > 0 {
				apiErr.Code, apiErr.Message = envelope.Errors[0].Code, envelope.Errors[0].Message
			}
			return apiErr
		}
		if out == nil || len(envelope.Result) == 0 {
			return nil
		}
		return common.Unmarshal(envelope.Result, out)
	}
}

// adtyCustomHostname 是自定义主机名接口的 result，只取用得到的字段。
type adtyCustomHostname struct {
	CreatedAt             string            `json:"created_at"`
	Id                    string            `json:"id"`
	Hostname              string            `json:"hostname"`
	Status                string            `json:"status"`
	Metadata              map[string]string `json:"custom_metadata"`
	OwnershipVerification struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"ownership_verification"`
	SSL struct {
		Status            string `json:"status"`
		ValidationRecords []struct {
			TxtName  string `json:"txt_name"`
			TxtValue string `json:"txt_value"`
		} `json:"validation_records"`
	} `json:"ssl"`
	VerificationErrors []string `json:"verification_errors"`
}

func adtyCustomHostnameGeneration(domain *model.AdtySiteDomain) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", domain.Id, domain.SiteId, domain.Hostname, domain.OwnershipToken))))
}

const adtyCloudResourcePendingPrefix = "pending:"

func adtyPendingCloudResourceID(domain *model.AdtySiteDomain) string {
	gen := adtyCustomHostnameGeneration(domain)
	maxGen := 64 - len(adtyCloudResourcePendingPrefix)
	if len(gen) > maxGen {
		gen = gen[:maxGen]
	}
	return adtyCloudResourcePendingPrefix + gen
}

func adtyRecordCloudCreateIntent(zone string, domain *model.AdtySiteDomain) error {
	if domain == nil || domain.Id <= 0 || model.DB == nil {
		return nil
	}
	// Persist a pre-create journal row independently of the caller's tx so even
	// if the caller tx rolls back after an outbound Cloudflare POST, cleanup
	// knows a remote creation was attempted for this generation (R9-018).
	intent := model.AdtyDomainCloudResource{
		DomainId:   domain.Id,
		Generation: adtyCustomHostnameGeneration(domain),
		Zone:       zone,
		Hostname:   domain.Hostname,
		ResourceID: adtyPendingCloudResourceID(domain),
	}
	return model.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "resource_id"}},
		DoNothing: true,
	}).Create(&intent).Error
}

func adtyLookupCloudResourceState(tx *gorm.DB, zone string, domain *model.AdtySiteDomain) (resourceID string, createAttempted bool, err error) {
	if domain == nil || domain.Id <= 0 {
		return "", false, nil
	}
	createAttempted = domain.CFCreateStartedAt > 0
	var rows []model.AdtyDomainCloudResource
	if err = tx.Where("domain_id = ? AND generation = ? AND zone = ? AND hostname = ?",
		domain.Id, adtyCustomHostnameGeneration(domain), zone, domain.Hostname).
		Order("id DESC").Find(&rows).Error; err != nil {
		return "", createAttempted, err
	}
	for _, r := range rows {
		if strings.HasPrefix(r.ResourceID, adtyCloudResourcePendingPrefix) {
			createAttempted = true
			continue
		}
		if r.ResourceID != "" && resourceID == "" {
			resourceID = r.ResourceID
			createAttempted = true
		}
	}
	return resourceID, createAttempted, nil
}

func (c *AdtyCloudflareClient) createCustomHostname(ctx context.Context, zone string, domain *model.AdtySiteDomain) (*adtyCustomHostname, error) {
	if err := adtyRecordCloudCreateIntent(zone, domain); err != nil {
		return nil, fmt.Errorf("Cloudflare hostname creation journal could not be recorded")
	}
	var out adtyCustomHostname
	body := map[string]any{
		"hostname": domain.Hostname,
		"ssl":      map[string]string{"method": "http", "type": "dv"},
	}
	if domain.ManagedBy == "platform" {
		body["ssl"] = map[string]string{"method": "txt", "type": "dv"}
	}
	err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zone)+"/custom_hostnames", body, &out)
	if err == nil && domain.Id > 0 && out.Id != "" {
		resource := model.AdtyDomainCloudResource{DomainId: domain.Id, Generation: adtyCustomHostnameGeneration(domain), Zone: zone, Hostname: domain.Hostname, ResourceID: out.Id}
		if saveErr := model.DB.Create(&resource).Error; saveErr != nil {
			return &out, fmt.Errorf("Cloudflare hostname created but resource journal could not be saved")
		}
	}
	return &out, err
}

func (c *AdtyCloudflareClient) findCustomHostname(ctx context.Context, zone, hostname string) (*adtyCustomHostname, error) {
	var out []adtyCustomHostname
	path := "/zones/" + url.PathEscape(zone) + "/custom_hostnames?hostname=" + url.QueryEscape(hostname)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	for i := range out {
		if strings.EqualFold(out[i].Hostname, hostname) {
			return &out[i], nil
		}
	}
	return nil, nil
}

func (c *AdtyCloudflareClient) getCustomHostname(ctx context.Context, zone, id string) (*adtyCustomHostname, error) {
	var out adtyCustomHostname
	err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zone)+"/custom_hostnames/"+url.PathEscape(id), nil, &out)
	return &out, err
}

func (c *AdtyCloudflareClient) deleteCustomHostname(ctx context.Context, zone, id string) error {
	err := c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(zone)+"/custom_hostnames/"+url.PathEscape(id), nil, nil)
	var apiErr *AdtyCloudflareError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil // 已经不在了
	}
	return err
}

// adtyVerifyRecord 是给站长看的一条 DNS 记录。
type adtyVerifyRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// verifyRecordsFor 生成站长要加的记录：CNAME 指向 cname_target，再加 Cloudflare 要的 TXT（如果有）。
func verifyRecordsFor(hostname, cnameTarget string, h *adtyCustomHostname) string {
	records := []adtyVerifyRecord{{Type: "CNAME", Name: hostname, Value: cnameTarget}}
	if h != nil {
		if h.OwnershipVerification.Name != "" && h.OwnershipVerification.Value != "" {
			records = append(records, adtyVerifyRecord{Type: "TXT", Name: h.OwnershipVerification.Name, Value: h.OwnershipVerification.Value})
		}
		for _, record := range h.SSL.ValidationRecords {
			if record.TxtName != "" && record.TxtValue != "" {
				records = append(records, adtyVerifyRecord{Type: "TXT", Name: record.TxtName, Value: record.TxtValue})
			}
		}
	}
	data, _ := common.Marshal(records)
	return string(data)
}

// adtyMainHost 是主站的域名（来自后台的服务器地址），小写，没设就是空。
func adtyMainHost() string {
	parsed, err := url.Parse(strings.TrimSpace(system_setting.GetServerAddress()))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// adtyAPIBaseHost 是主站配置的 API 域名（来自 adty_site_setting.api_base_url），小写，没设就是空。
func adtyAPIBaseHost() string {
	parsed, err := url.Parse(strings.TrimSpace(adty_setting.GetSiteSetting().APIBaseURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// adtyRegistrableDomain 按公共后缀表取注册域名（eTLD+1），用来判断主站、CNAME 目标和回源域名下的子域名。
func adtyRegistrableDomain(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return ""
	}
	if registrable, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil && registrable != "" {
		return registrable
	}
	return host
}

func adtyHostUnder(host, domain string) bool {
	return domain != "" && (host == domain || strings.HasSuffix(host, "."+domain))
}

// normalizeCustomDomain 校验站长填的域名：小写、去掉末尾的点；必须是子域名（注册域本身拒绝，
// example.co.uk 这种多段后缀的根域名也拒绝），不能是 root_domains、cname_target、主站域名下的。
func normalizeCustomDomain(raw string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" || len(host) > 253 || adtyCustomDomainIsApex(host) {
		return "", ErrAdtyCustomDomainInvalid
	}
	for _, label := range strings.Split(host, ".") {
		if !adty_setting.ResellerLabelPattern.MatchString(label) {
			return "", ErrAdtyCustomDomainInvalid
		}
	}
	cfg := resellerSetting()
	blocked := append([]string{}, cfg.RootDomains...)
	if target := strings.ToLower(strings.TrimSpace(cfg.CnameTarget)); target != "" {
		blocked = append(blocked, adtyRegistrableDomain(target))
	}
	if fallback := strings.ToLower(strings.TrimSpace(cfg.FallbackOrigin)); fallback != "" {
		blocked = append(blocked, adtyRegistrableDomain(fallback))
	}
	if main := adtyMainHost(); main != "" && main != "localhost" {
		blocked = append(blocked, adtyRegistrableDomain(main))
	}
	if apiHost := adtyAPIBaseHost(); apiHost != "" && apiHost != "localhost" {
		blocked = append(blocked, adtyRegistrableDomain(apiHost))
	}
	blocked = append(blocked, strings.TrimPrefix(common.AdtyBrandURL, "https://"))
	for _, domain := range blocked {
		if adtyHostUnder(host, strings.ToLower(strings.TrimSpace(domain))) {
			return "", ErrAdtyCustomDomainInvalid
		}
	}
	return host, nil
}

// adtyCustomDomainIsApex 用公共后缀表判断主机名是不是注册域本身。认不出的也当作根域名拒绝。
func adtyCustomDomainIsApex(host string) bool {
	registrable, err := publicsuffix.EffectiveTLDPlusOne(host)
	return err != nil || registrable == host
}

const adtySiteDomainCreateAuditAction = "domain_create"

// adtyCustomDomainAllowCreate 检查这个站还能不能再新建一个自定义域名。
// reusingCountedRow 为真时，这一行已经是本站的 pending、active 或 failed，不占新名额；每小时新建数两种都算。
func adtyCustomDomainAllowCreate(tx *gorm.DB, siteId int, reusingCountedRow bool, now int64) error {
	newQuotaCount := 1
	if reusingCountedRow {
		newQuotaCount = 0
	}
	return adtyCustomDomainAllowCreateCount(tx, siteId, newQuotaCount, 1, now)
}

func adtyCustomDomainAllowCreateCount(tx *gorm.DB, siteId, newQuotaCount, createCount int, now int64) error {
	cfg := resellerSetting()
	if newQuotaCount > 0 {
		var counted int64
		err := tx.Model(&model.AdtySiteDomain{}).Where("managed_by <> ?", "platform").
			Where("site_id = ? AND status IN ?", siteId, []string{
				model.AdtySiteDomainPending, model.AdtySiteDomainActive, model.AdtySiteDomainFailed,
			}).Count(&counted).Error
		if err != nil {
			return err
		}
		if int(counted)+newQuotaCount > cfg.MaxCustomDomainsPerSite {
			return ErrAdtyCustomDomainLimit
		}
	}
	if createCount > 0 {
		var recentDomains int64
		if err := tx.Model(&model.AdtySiteDomain{}).Where("managed_by <> ?", "platform").
			Where("site_id = ? AND created_at >= ?", siteId, now-3600).Count(&recentDomains).Error; err != nil {
			return err
		}
		var recentAudits int64
		if err := tx.Model(&model.AdtySiteAudit{}).
			Where("site_id = ? AND action = ? AND created_at >= ?", siteId, adtySiteDomainCreateAuditAction, now-3600).Count(&recentAudits).Error; err != nil {
			return err
		}
		if int(max(recentDomains, recentAudits))+createCount > cfg.CustomDomainCreatesPerHour {
			return ErrAdtyCustomDomainRate
		}
	}
	return nil
}

func adtyNormalizeDomainRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return model.AdtySiteDomainRoleSite
	}
	return role
}

func adtyCustomDomainRoleOccupied(tx *gorm.DB, siteId int, role string, excludeIds ...int) (bool, error) {
	query := tx.Model(&model.AdtySiteDomain{}).
		Where("site_id = ? AND managed_by <> ? AND status IN ?",
			siteId, "platform", []string{
				model.AdtySiteDomainPending, model.AdtySiteDomainActive, model.AdtySiteDomainFailed,
			})
	if role == model.AdtySiteDomainRoleSite {
		query = query.Where("role IN ?", []string{"", model.AdtySiteDomainRoleSite})
	} else {
		query = query.Where("role = ?", role)
	}
	var filtered []int
	for _, id := range excludeIds {
		if id > 0 {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) > 0 {
		query = query.Where("id NOT IN ?", filtered)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func adtyUpsertCustomDomainTx(tx *gorm.DB, cfg *adty_setting.ResellerSetting, siteId, ownerUserId int, host, role string, found bool, existing *model.AdtySiteDomain, now int64) (model.AdtySiteDomain, error) {
	token, err := adtyNewOwnershipToken()
	if err != nil {
		return model.AdtySiteDomain{}, err
	}
	domain := model.AdtySiteDomain{
		SiteId: siteId, BoundBy: ownerUserId, Hostname: host, Role: role, OwnershipToken: token, Status: model.AdtySiteDomainPending,
		ManagedBy: "owner", DesiredState: "serve", ProviderZone: cfg.CloudflareZoneId, DNSTarget: cfg.CnameTarget,
		VerifyRecords: adtyOwnershipRecords(host, token, strings.TrimSpace(cfg.CnameTarget)),
		LastCheckedAt: now, CreatedAt: now,
	}
	if cfg.DomainAutomationEnabled {
		domain.AutomationState = "queued"
	}
	if !found {
		if err := tx.Create(&domain).Error; err != nil {
			return model.AdtySiteDomain{}, err
		}
	} else {
		domain.Id = existing.Id
		domain.Generation = existing.Generation + 1
		if err := tx.Model(&model.AdtySiteDomain{}).Where("id = ?", existing.Id).Updates(map[string]any{
			"site_id": siteId, "bound_by": ownerUserId, "role": role, "ownership_token": token, "cf_hostname_id": "", "status": model.AdtySiteDomainPending,
			"ssl_status": "", "verify_records": domain.VerifyRecords, "turnstile_widget": "",
			"last_error": "", "last_checked_at": now, "created_at": now,
			"managed_by": "owner", "desired_state": "serve", "automation_state": domain.AutomationState, "provider_zone": cfg.CloudflareZoneId, "dns_root": "", "dns_target": cfg.CnameTarget, "dns_records": "", "mainland_target": "", "lease_token": "", "lease_until": 0, "next_run_at": 0, "attempts": 0, "cf_create_started_at": 0, "generation": gorm.Expr("generation + 1"),
		}).Error; err != nil {
			return model.AdtySiteDomain{}, err
		}
	}
	if err := tx.Create(&model.AdtySiteAudit{
		SiteId:     siteId,
		Action:     adtySiteDomainCreateAuditAction,
		OperatorId: ownerUserId,
		NewValue:   host,
		CreatedAt:  now,
	}).Error; err != nil {
		return model.AdtySiteDomain{}, err
	}
	return domain, nil
}

func adtyLockSiteRow(tx *gorm.DB, id int, current *model.AdtySite) error {
	if err := tx.Model(&model.AdtySite{}).Where("id = ?", id).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
		return err
	}
	return model.AdtyLockForUpdate(tx).First(current, id).Error
}

// adtyOwnerSite 是这个用户名下能绑定域名的站：active 或 paused；closing 时返回 ErrAdtySiteClosing。
func adtyOwnerSite(ownerUserId int) (*model.AdtySite, error) {
	var site model.AdtySite
	err := model.DB.Where("owner_user_id = ? AND status IN ?", ownerUserId,
		[]string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused}).First(&site).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var closingSite model.AdtySite
		if closingErr := model.DB.Select("id").Where("owner_user_id = ? AND status = ?", ownerUserId, model.AdtySiteStatusClosing).First(&closingSite).Error; closingErr == nil {
			return nil, ErrAdtySiteClosing
		} else if !errors.Is(closingErr, gorm.ErrRecordNotFound) {
			return nil, closingErr
		}
		return nil, ErrAdtySiteNotFound
	}
	return &site, err
}

// adtyCloudflareUserError 把 Cloudflare 的错误换成给人看的提示；原始错误只记日志（不含响应原文和 Token）。
func adtyCloudflareUserError(err error) error {
	var apiErr *AdtyCloudflareError
	if errors.As(err, &apiErr) {
		common.SysError(common.LogText("cloudflare request failed: status=%d code=%d", apiErr.StatusCode, apiErr.Code))
		if apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden {
			return ErrAdtyCloudflareRejected
		}
		return ErrAdtyCloudflareFailed
	}
	common.SysError(common.LogText("cloudflare request failed: %s", err.Error()))
	return ErrAdtyCloudflareFailed
}

// AddAdtySiteDomain 给站长的站绑定一个自定义域名。先发归属 TXT，查到令牌才在 Cloudflare 建主机名。
// 别的站 pending、active、failed 的主机名算占用。removed 可以重加，但必须换新令牌并重新验证。
func AddAdtySiteDomain(ctx context.Context, ownerUserId int, rawHost string) (*model.AdtySiteDomain, error) {
	return AddAdtySiteDomainWithRole(ctx, ownerUserId, rawHost, model.AdtySiteDomainRoleSite)
}

// AddAdtySiteDomainWithRole 是 AddAdtySiteDomain 的带用途版本：role 只有 site（站点域名）
// 和 base_url（API 地址）两种，空值按 site 处理。数量仍受 MaxCustomDomainsPerSite 限制。
func AddAdtySiteDomainWithRole(ctx context.Context, ownerUserId int, rawHost, role string) (*model.AdtySiteDomain, error) {
	cfg := resellerSetting()
	if !cfg.CustomDomainEnabled || !cfg.CloudflareConfigured() {
		return nil, ErrAdtyCustomDomainOff
	}
	site, err := adtyOwnerSite(ownerUserId)
	if err != nil {
		return nil, err
	}
	host, err := normalizeCustomDomain(rawHost)
	if err != nil {
		return nil, err
	}
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "", model.AdtySiteDomainRoleSite:
		role = model.AdtySiteDomainRoleSite
	case model.AdtySiteDomainRoleBaseURL:
	default:
		return nil, ErrAdtyCustomDomainRoleInvalid
	}
	var domain model.AdtySiteDomain
	skipAttach := false
	err = model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lockedSite model.AdtySite
		if err := adtyLockSiteRow(tx, site.Id, &lockedSite); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if lockedSite.OwnerUserId != ownerUserId {
			return ErrAdtySiteNotFound
		}
		if lockedSite.Status == model.AdtySiteStatusClosing {
			return ErrAdtySiteClosing
		}
		if lockedSite.Status != model.AdtySiteStatusActive && lockedSite.Status != model.AdtySiteStatusPaused {
			return ErrAdtySiteNotFound
		}
		var existing model.AdtySiteDomain
		result := tx.Where("hostname = ?", host).Limit(1).Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		found := result.RowsAffected > 0
		if found {
			// Re-read after taking the same lock used by checker and removal.
			if err := adtyLockDomainRow(tx, existing.Id, &existing); err != nil {
				return err
			}
		}
		if found && existing.Status != model.AdtySiteDomainRemoved && existing.DesiredState == "removed" {
			return common.NewMessage("Domain removal is in progress; please try again shortly")
		}
		if found && existing.SiteId != site.Id && existing.Status != model.AdtySiteDomainRemoved {
			return ErrAdtyCustomDomainTaken
		}
		if found && existing.SiteId == site.Id && (existing.Status == model.AdtySiteDomainPending || existing.Status == model.AdtySiteDomainActive) {
			if existing.ManagedBy != "platform" && adtyNormalizeDomainRole(existing.Role) == role {
				domain = existing
				skipAttach = existing.Status == model.AdtySiteDomainActive || existing.CFHostnameId != ""
				return nil
			}
			return ErrAdtyCustomDomainRoleLimit
		}
		var activeCustomCount int64
		if err := tx.Model(&model.AdtySiteDomain{}).
			Where("site_id = ? AND managed_by <> ? AND status <> ?", site.Id, "platform", model.AdtySiteDomainRemoved).
			Count(&activeCustomCount).Error; err != nil {
			return err
		}
		if activeCustomCount == 0 {
			return common.NewMessage("Please configure both website and API custom domains together first")
		}
		excludeId := 0
		if found {
			excludeId = existing.Id
		}
		occupied, err := adtyCustomDomainRoleOccupied(tx, site.Id, role, excludeId)
		if err != nil {
			return err
		}
		if occupied {
			return ErrAdtyCustomDomainRoleLimit
		}
		now := time.Now().Unix()
		reusingCounted := found && existing.SiteId == site.Id && existing.Status != model.AdtySiteDomainRemoved
		if err := adtyCustomDomainAllowCreate(tx, site.Id, reusingCounted, now); err != nil {
			return err
		}
		domain, err = adtyUpsertCustomDomainTx(tx, cfg, site.Id, ownerUserId, host, role, found, &existing, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !skipAttach {
		if err := adtyAttachCustomHostname(ctx, &domain); err != nil {
			return nil, err
		}
	}
	return &domain, nil
}

// AddAdtySiteDomainPair 在同一个数据库事务里同时绑定一个站点域名（role=site）和一个 API 域名（role=base_url）。
// 任一域名校验失败、被其他站点占用、或对应用途槽位已被其他自定义域名占用时整体回滚，不产生半成品。
func AddAdtySiteDomainPair(ctx context.Context, ownerUserId int, rawSiteHost, rawAPIHost string) ([]model.AdtySiteDomain, error) {
	cfg := resellerSetting()
	if !cfg.CustomDomainEnabled || !cfg.CloudflareConfigured() {
		return nil, ErrAdtyCustomDomainOff
	}
	site, err := adtyOwnerSite(ownerUserId)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rawSiteHost) == "" || strings.TrimSpace(rawAPIHost) == "" {
		return nil, ErrAdtyCustomDomainPairRequired
	}
	siteHost, err := normalizeCustomDomain(rawSiteHost)
	if err != nil {
		return nil, err
	}
	apiHost, err := normalizeCustomDomain(rawAPIHost)
	if err != nil {
		return nil, err
	}
	if siteHost == apiHost {
		return nil, ErrAdtyCustomDomainPairSame
	}

	domains := make([]model.AdtySiteDomain, 2)
	err = model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lockedSite model.AdtySite
		if err := adtyLockSiteRow(tx, site.Id, &lockedSite); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if lockedSite.OwnerUserId != ownerUserId {
			return ErrAdtySiteNotFound
		}
		if lockedSite.Status == model.AdtySiteStatusClosing {
			return ErrAdtySiteClosing
		}
		if lockedSite.Status != model.AdtySiteStatusActive && lockedSite.Status != model.AdtySiteStatusPaused {
			return ErrAdtySiteNotFound
		}

		type pairItem struct {
			host       string
			role       string
			found      bool
			existing   model.AdtySiteDomain
			idempotent bool
		}
		items := [2]pairItem{
			{host: siteHost, role: model.AdtySiteDomainRoleSite},
			{host: apiHost, role: model.AdtySiteDomainRoleBaseURL},
		}

		for i := range items {
			res := tx.Where("hostname = ?", items[i].host).Limit(1).Find(&items[i].existing)
			if res.Error != nil {
				return res.Error
			}
			items[i].found = res.RowsAffected > 0
		}

		// Lock any existing rows in ascending ID order to avoid lock-order deadlocks.
		lockOrder := []int{}
		for i := range items {
			if items[i].found {
				lockOrder = append(lockOrder, i)
			}
		}
		if len(lockOrder) == 2 && items[lockOrder[0]].existing.Id > items[lockOrder[1]].existing.Id {
			lockOrder[0], lockOrder[1] = lockOrder[1], lockOrder[0]
		}
		for _, idx := range lockOrder {
			if err := adtyLockDomainRow(tx, items[idx].existing.Id, &items[idx].existing); err != nil {
				return err
			}
		}

		// Validate ownership and idempotency for BOTH hosts before mutating either.
		var excludeIds []int
		newQuotaCount := 0
		createCount := 0
		for i := range items {
			item := &items[i]
			if item.found && item.existing.Status != model.AdtySiteDomainRemoved && item.existing.DesiredState == "removed" {
				return common.NewMessage("Domain removal is in progress; please try again shortly")
			}
			if item.found && item.existing.SiteId != site.Id && item.existing.Status != model.AdtySiteDomainRemoved {
				return ErrAdtyCustomDomainTaken
			}
			if item.found && item.existing.SiteId == site.Id {
				excludeIds = append(excludeIds, item.existing.Id)
				if item.existing.Status == model.AdtySiteDomainPending || item.existing.Status == model.AdtySiteDomainActive {
					if item.existing.ManagedBy != "platform" && adtyNormalizeDomainRole(item.existing.Role) == item.role {
						item.idempotent = true
						continue
					}
					return ErrAdtyCustomDomainRoleLimit
				}
			}
			createCount++
			reusingCounted := item.found && item.existing.SiteId == site.Id && item.existing.Status != model.AdtySiteDomainRemoved
			if !reusingCounted {
				newQuotaCount++
			}
		}

		// Enforce 1-per-role custom domain slot cap for BOTH roles.
		for i := range items {
			occupied, err := adtyCustomDomainRoleOccupied(tx, site.Id, items[i].role, excludeIds...)
			if err != nil {
				return err
			}
			if occupied {
				return ErrAdtyCustomDomainRoleLimit
			}
		}

		now := time.Now().Unix()
		if err := adtyCustomDomainAllowCreateCount(tx, site.Id, newQuotaCount, createCount, now); err != nil {
			return err
		}

		for i := range items {
			if items[i].idempotent {
				domains[i] = items[i].existing
				continue
			}
			created, err := adtyUpsertCustomDomainTx(tx, cfg, site.Id, ownerUserId, items[i].host, items[i].role, items[i].found, &items[i].existing, now)
			if err != nil {
				return err
			}
			domains[i] = created
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var attachErr error
	for i := range domains {
		if err := adtyAttachCustomHostname(ctx, &domains[i]); err != nil && attachErr == nil {
			attachErr = err
		}
	}
	if attachErr != nil {
		return nil, attachErr
	}
	return domains, nil
}

func adtyNewOwnershipToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func adtyOwnershipTXTName(host string) string {
	return "_cf-custom-hostname." + host
}

func adtyOwnershipRecords(host, token, cnameTarget string) string {
	records := []adtyVerifyRecord{
		{Type: "TXT", Name: adtyOwnershipTXTName(host), Value: token},
		{Type: "CNAME", Name: host, Value: cnameTarget},
	}
	data, _ := common.Marshal(records)
	return string(data)
}

func adtyOwnershipTXTMatches(ctx context.Context, host, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	values, err := adtyLookupOwnershipTXT(ctx, adtyOwnershipTXTName(host))
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return false, nil
		}
		return false, err
	}
	for _, value := range values {
		if strings.TrimSpace(value) == token {
			return true, nil
		}
	}
	return false, nil
}

// adtyAttachCustomHostname 在归属 TXT 对上以后才向 Cloudflare 建主机名。
func adtyDomainSnapshotQuery(tx *gorm.DB, domain *model.AdtySiteDomain) *gorm.DB {
	return tx.Model(&model.AdtySiteDomain{}).Where(
		"id = ? AND site_id = ? AND hostname = ? AND status = ? AND ownership_token = ? AND cf_hostname_id = ?",
		domain.Id, domain.SiteId, domain.Hostname, domain.Status, domain.OwnershipToken, domain.CFHostnameId)
}

// The write acquires a database row lock on PostgreSQL/MySQL and a writer lock
// on SQLite before reading. All lifecycle entry points hold this transaction
// across their external requests, so another node cannot remove/reassign an
// in-flight attachment, widget update or timeout cleanup.
func adtyLockDomainRow(tx *gorm.DB, id int, current *model.AdtySiteDomain) error {
	if err := tx.Model(&model.AdtySiteDomain{}).Where("id = ?", id).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
		return err
	}
	return model.AdtyLockForUpdate(tx).First(current, id).Error
}

func adtyWithDomainLifecycle(ctx context.Context, snapshot *model.AdtySiteDomain, action func(*gorm.DB, *model.AdtySiteDomain) error) error {
	return model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.AdtySiteDomain
		if err := adtyLockDomainRow(tx, snapshot.Id, &current); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if current.SiteId != snapshot.SiteId || current.Hostname != snapshot.Hostname || current.OwnershipToken != snapshot.OwnershipToken || current.DesiredState == "removed" || current.AutomationState != "" {
			return nil
		}
		if current.LeaseUntil > time.Now().Unix() && current.LeaseToken != "" {
			return nil
		}
		var site model.AdtySite
		if err := tx.Select("id", "status").First(&site, current.SiteId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if site.Status == model.AdtySiteStatusDeleting {
			return nil
		}
		if err := action(tx, &current); err != nil {
			return err
		}
		*snapshot = current
		return nil
	})
}

func adtyAttachCustomHostname(ctx context.Context, domain *model.AdtySiteDomain) error {
	return adtyWithDomainLifecycle(ctx, domain, func(tx *gorm.DB, current *model.AdtySiteDomain) error {
		return adtyAttachCustomHostnameLocked(ctx, tx, current)
	})
}

func adtyAttachCustomHostnameLocked(ctx context.Context, tx *gorm.DB, domain *model.AdtySiteDomain) error {
	if domain.Status != model.AdtySiteDomainPending || domain.CFHostnameId != "" || domain.OwnershipToken == "" {
		return nil
	}
	matched, err := adtyOwnershipTXTMatches(ctx, domain.Hostname, domain.OwnershipToken)
	if err != nil {
		common.SysError(common.LogText("ownership lookup failed for domain %d: %s", domain.Id, err.Error()))
		_ = adtyDomainSnapshotQuery(tx, domain).Update("last_error", common.NewMessage("Domain ownership lookup failed").Error()).Error
		return nil
	}
	if !matched {
		return nil
	}
	cfg := resellerSetting()
	zone := adtyDomainEffectiveZone(domain, cfg)
	if domain.CFCreateStartedAt == 0 {
		startedAt := time.Now().Unix()
		if err := adtyDomainSnapshotQuery(tx, domain).Update("cf_create_started_at", startedAt).Error; err != nil {
			return err
		}
		domain.CFCreateStartedAt = startedAt
	}
	created, err := AdtyCloudflare.createCustomHostname(ctx, zone, domain)
	var apiErr *AdtyCloudflareError
	if errors.As(err, &apiErr) && apiErr.Code == adtyCloudflareDuplicateCode {
		// R9-018: Never blindly adopt an unowned duplicate 1406 hostname. Only
		// recover a resource ID already journaled for this exact domain generation.
		recordedID, _, lookupErr := adtyLookupCloudResourceState(tx, zone, domain)
		if lookupErr == nil && recordedID != "" {
			created = &adtyCustomHostname{Id: recordedID, Hostname: domain.Hostname}
			err = nil
		}
	}
	if err != nil {
		return adtyCloudflareUserError(err)
	}
	if adtyCustomHostnameRecordDelay > 0 {
		adtyCloudflareSleep(adtyCustomHostnameRecordDelay)
	}
	detail, err := AdtyCloudflare.getCustomHostname(ctx, zone, created.Id)
	if err != nil {
		common.SysError(common.LogText("cloudflare custom hostname detail failed for site %d", domain.SiteId))
		detail = created
	}
	now := time.Now().Unix()
	records := verifyRecordsFor(domain.Hostname, strings.TrimSpace(cfg.CnameTarget), detail)
	result := adtyDomainSnapshotQuery(tx, domain).Updates(map[string]any{
		"cf_hostname_id": created.Id, "ssl_status": detail.SSL.Status,
		"verify_records": records, "last_error": "", "last_checked_at": now,
	})
	if result.Error == nil && result.RowsAffected > 0 {
		domain.CFHostnameId, domain.SSLStatus = created.Id, detail.SSL.Status
		domain.VerifyRecords, domain.LastCheckedAt = records, now
	}
	return result.Error
}

// Archived domains are limited to the external websites bound by the requesting owner.
// A legacy site with a recorded owner change needs affirmative evidence of the binding owner.
func adtyArchivedWebsiteDomains(db *gorm.DB, ownerID, siteID int) *gorm.DB {
	sites := db.Model(&model.AdtySite{}).Select("id").Where("id = ? AND status = ?", siteID, model.AdtySiteStatusClosed)
	unchanged := db.Model(&model.AdtySite{}).Select("id").Where("id = ? AND owner_user_id = ?", siteID, ownerID).
		Where("NOT EXISTS (?)", db.Model(&model.AdtySiteOwnership{}).Select("1").Where("site_id = ?", siteID))
	return db.Model(&model.AdtySiteDomain{}).Where("site_id IN (?) AND role = ? AND managed_by <> ?", sites, model.AdtySiteDomainRoleSite, "platform").
		Where("(bound_by = ? OR (COALESCE(bound_by,0) = 0 AND site_id IN (?)))", ownerID, unchanged)
}

func ListAdtyArchivedWebsiteDomains(ownerID, siteID int) ([]model.AdtySiteDomain, error) {
	var rows []model.AdtySiteDomain
	err := adtyArchivedWebsiteDomains(model.DB, ownerID, siteID).Where("status <> ? AND desired_state <> ?", model.AdtySiteDomainRemoved, "removed").Order("id").Find(&rows).Error
	return rows, err
}

func RemoveAdtyArchivedWebsiteDomain(ctx context.Context, ownerID, siteID, domainID int) error {
	var domain model.AdtySiteDomain
	lookup := adtyArchivedWebsiteDomains(model.DB, ownerID, siteID).Where("id = ?", domainID).Limit(1).Find(&domain)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return ErrAdtySiteNotFound
	}
	return removeAdtySiteDomain(ctx, &domain)
}

// RemoveAdtySiteDomain 是站长删除自己站上的域名。
func RemoveAdtySiteDomain(ctx context.Context, ownerUserId, domainId int) error {
	site, err := adtyOwnerSite(ownerUserId)
	if err != nil {
		return err
	}
	var domain model.AdtySiteDomain
	if model.DB.Where("id = ? AND site_id = ?", domainId, site.Id).Limit(1).Find(&domain).RowsAffected == 0 {
		return ErrAdtySiteNotFound
	}
	if domain.ManagedBy == "platform" {
		return common.NewMessage("Platform domains are managed by the platform")
	}
	return removeAdtySiteDomain(ctx, &domain)
}

// RemoveAdtySiteDomainByAdmin 是主站管理员停用一个域名（11.9）。
func RemoveAdtySiteDomainByAdmin(ctx context.Context, domainId int) error {
	return RemoveAdtySiteDomainByAdminForActor(ctx, domainId, 0, 0, false)
}

func RemoveAdtySiteDomainByAdminForActor(ctx context.Context, domainId, actorID, actorRole int, denyAdminTarget bool) error {
	var domain model.AdtySiteDomain
	if model.DB.Where("id = ?", domainId).Limit(1).Find(&domain).RowsAffected == 0 {
		return ErrAdtySiteNotFound
	}
	if domain.ManagedBy == "platform" {
		return common.NewMessage("Platform domains are managed by the platform")
	}
	return removeAdtySiteDomainForActor(ctx, &domain, actorID, actorRole, denyAdminTarget)
}

// removeAdtySiteDomain 从 Turnstile widget 和 Cloudflare 删掉，行改成 removed。
func removeAdtySiteDomain(ctx context.Context, domain *model.AdtySiteDomain) error {
	return removeAdtySiteDomainForActor(ctx, domain, 0, 0, false)
}

func removeAdtySiteDomainForActor(ctx context.Context, domain *model.AdtySiteDomain, actorID, actorRole int, denyAdminTarget bool) error {
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if domain.SiteId > 0 {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Select("id", "owner_user_id", "status").Where("id = ?", domain.SiteId).First(&site).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtySiteNotFound
				}
				return err
			}
			if site.Status == model.AdtySiteStatusDeleting {
				return ErrAdtySiteStatus
			}
			if actorRole > 0 || denyAdminTarget {
				if err := checkAdtySiteTargetActorTx(tx, &site, actorID, actorRole, denyAdminTarget); err != nil {
					return err
				}
			}
		}
		var current model.AdtySiteDomain
		if err := adtyLockDomainRow(tx, domain.Id, &current); err != nil {
			return err
		}
		if current.SiteId != domain.SiteId || current.Hostname != domain.Hostname || current.OwnershipToken != domain.OwnershipToken {
			return ErrAdtySiteNotFound
		}
		if current.Status == model.AdtySiteDomainRemoved {
			return nil
		}
		if current.LeaseUntil > time.Now().Unix() && current.LeaseToken != "" {
			return common.NewMessage("Domain automation is in progress for this site; please try again shortly")
		}
		if current.AutomationState != "" {
			if current.DesiredState == "removed" || current.Status == model.AdtySiteDomainRemoved {
				return nil
			}
			return tx.Model(&current).Updates(map[string]any{"desired_state": "removed", "automation_state": "queued", "generation": gorm.Expr("generation + 1"), "next_run_at": 0}).Error
		}
		return removeAdtySiteDomainLocked(ctx, tx, &current)
	})
	if err == nil {
		InvalidateAdtySiteCache()
	}
	return err
}

func adtyDomainEffectiveZone(domain *model.AdtySiteDomain, cfg *adty_setting.ResellerSetting) string {
	if domain != nil {
		if z := strings.TrimSpace(domain.ProviderZone); z != "" {
			return z
		}
	}
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.CloudflareZoneId)
}

func removeAdtySiteDomainLocked(ctx context.Context, tx *gorm.DB, domain *model.AdtySiteDomain) error {
	if domain.Status == model.AdtySiteDomainRemoved {
		return nil
	}
	zone := adtyDomainEffectiveZone(domain, resellerSetting())
	hostnameID := domain.CFHostnameId
	createAttempted := domain.CFCreateStartedAt > 0
	if hostnameID == "" {
		var err error
		hostnameID, createAttempted, err = adtyLookupCloudResourceState(tx, zone, domain)
		if err != nil {
			return err
		}
	}
	if hostnameID == "" && createAttempted {
		// A remote create was attempted and may have succeeded before the database
		// transaction rolled back. Only our exact generation marker proves it is ours;
		// an unrecorded candidate fails closed so removal cannot orphan it in Cloudflare (R8-030 / R9-018).
		candidate, err := AdtyCloudflare.findCustomHostname(ctx, zone, domain.Hostname)
		if err != nil {
			return adtyCloudflareUserError(err)
		}
		if candidate != nil {
			if candidate.Metadata["site_id"] == strconv.Itoa(domain.SiteId) &&
				candidate.Metadata["adty_domain_id"] == strconv.Itoa(domain.Id) && candidate.Metadata["adty_generation"] == adtyCustomHostnameGeneration(domain) {
				hostnameID = candidate.Id
			} else {
				return common.NewMessage("Unrecorded Cloudflare creation requires reconciliation before cleanup")
			}
		}
	}
	// Scan configured widgets too: a successful remote PUT may not have been
	// recorded before rollback. The domain generation remains locked here.
	if hostnameID != "" || domain.TurnstileWidget != "" {
		if err := adtyTurnstileRemoveDomain(ctx, tx, domain.TurnstileWidget, domain.Hostname); err != nil {
			return adtyCloudflareUserError(err)
		}
	}
	if hostnameID != "" {
		if err := AdtyCloudflare.deleteCustomHostname(ctx, zone, hostnameID); err != nil {
			return adtyCloudflareUserError(err)
		}
	}
	if err := adtyDomainSnapshotQuery(tx, domain).Updates(map[string]any{
		"status":               model.AdtySiteDomainRemoved,
		"cf_hostname_id":       "",
		"cf_create_started_at": 0,
		"turnstile_widget":     "",
		"lease_token":          "",
		"lease_until":          0,
		"generation":           gorm.Expr("generation + 1"),
		"last_checked_at":      time.Now().Unix(),
	}).Error; err != nil {
		return err
	}
	if tx.Migrator().HasTable(&model.AdtyDomainCloudResource{}) {
		if err := tx.Where("domain_id = ?", domain.Id).Delete(&model.AdtyDomainCloudResource{}).Error; err != nil {
			return err
		}
	}
	domain.Status, domain.CFHostnameId, domain.CFCreateStartedAt, domain.TurnstileWidget = model.AdtySiteDomainRemoved, "", 0, ""
	domain.LeaseToken, domain.LeaseUntil = "", 0
	domain.Generation++
	return nil
}

// adtyRemoveDomainsOfClosedSite 在关站后删掉这个站的所有域名（AdtyOnSiteClosed）。失败只记日志，
// 行保持原状，管理员可以在分站管理里再删。
func adtyRemoveDomainsOfClosedSite(site *model.AdtySite) {
	if err := adtyCloseDomainIntentsTx(model.DB, site.Id); err != nil {
		common.SysError(common.LogText("load domains of closed site %d failed: %s", site.Id, err.Error()))
	}
}

// adtyMarkCustomDomainFailed 把超时的域名标成 failed。Cloudflare 上还有主机名（含仅写进云资源流水或已发起创建的）时先删掉；删不掉或存在未记账远端资源就保持 pending，下次再试。
func adtyMarkCustomDomainFailed(ctx context.Context, tx *gorm.DB, zone string, domain *model.AdtySiteDomain, updates map[string]any) error {
	hostnameID := domain.CFHostnameId
	createAttempted := domain.CFCreateStartedAt > 0
	if hostnameID == "" {
		var err error
		hostnameID, createAttempted, err = adtyLookupCloudResourceState(tx, zone, domain)
		if err != nil {
			return err
		}
	}
	if hostnameID == "" && createAttempted {
		candidate, err := AdtyCloudflare.findCustomHostname(ctx, zone, domain.Hostname)
		if err != nil {
			common.SysError(common.LogText("lookup timed out custom hostname for domain %d failed", domain.Id))
			updates["last_error"] = adtyDomainAutomationUserError(err).Error()
			return adtyDomainSnapshotQuery(tx, domain).Updates(updates).Error
		}
		if candidate != nil {
			if candidate.Metadata["site_id"] == strconv.Itoa(domain.SiteId) &&
				candidate.Metadata["adty_domain_id"] == strconv.Itoa(domain.Id) && candidate.Metadata["adty_generation"] == adtyCustomHostnameGeneration(domain) {
				hostnameID = candidate.Id
			} else {
				updates["last_error"] = common.NewMessage("Unrecorded Cloudflare creation requires reconciliation before cleanup").Error()
				return adtyDomainSnapshotQuery(tx, domain).Updates(updates).Error
			}
		}
	}
	clearedTurnstile := false
	if domain.TurnstileWidget != "" || (hostnameID != "" && resellerSetting().TurnstileConfigured()) {
		if err := adtyTurnstileRemoveDomain(ctx, tx, domain.TurnstileWidget, domain.Hostname); err != nil {
			common.SysError(common.LogText("remove timed out turnstile domain %d failed", domain.Id))
			updates["last_error"] = adtyDomainAutomationUserError(err).Error()
			return adtyDomainSnapshotQuery(tx, domain).Updates(updates).Error
		}
		updates["turnstile_widget"] = ""
		clearedTurnstile = true
	}
	clearedCFHostname := false
	if hostnameID != "" {
		if err := AdtyCloudflare.deleteCustomHostname(ctx, zone, hostnameID); err != nil {
			common.SysError(common.LogText("delete timed out custom hostname for domain %d failed", domain.Id))
			updates["last_error"] = adtyDomainAutomationUserError(err).Error()
			return adtyDomainSnapshotQuery(tx, domain).Updates(updates).Error
		}
		updates["cf_hostname_id"] = ""
		clearedCFHostname = true
	}
	updates["cf_create_started_at"] = 0
	updates["status"] = model.AdtySiteDomainFailed
	// R7-15: Keep domain.CFHostnameId intact until adtyDomainSnapshotQuery matches
	// the persisted row; otherwise WHERE cf_hostname_id = '' updates 0 rows and
	// loops forever against Cloudflare API.
	res := adtyDomainSnapshotQuery(tx, domain).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("custom domain snapshot changed during timeout cleanup")
	}
	if clearedTurnstile {
		domain.TurnstileWidget = ""
	}
	if clearedCFHostname {
		domain.CFHostnameId = ""
	}
	domain.CFCreateStartedAt = 0
	domain.Status = model.AdtySiteDomainFailed
	if tx.Migrator().HasTable(&model.AdtyDomainCloudResource{}) {
		if err := tx.Where("domain_id = ?", domain.Id).Delete(&model.AdtyDomainCloudResource{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func adtyAssignCheckedDomainTurnstile(ctx context.Context, tx *gorm.DB, domain *model.AdtySiteDomain) error {
	if domain.Status != model.AdtySiteDomainActive || domain.TurnstileWidget != "" {
		return nil
	}
	widget, err := adtyTurnstileAssign(ctx, tx, domain.Hostname)
	if err != nil {
		common.SysError(common.LogText("turnstile assign for domain %d failed", domain.Id))
		return nil
	}
	if widget == "" {
		return nil
	}
	if err := adtyDomainSnapshotQuery(tx, domain).Where("turnstile_widget = ?", "").Update("turnstile_widget", widget).Error; err != nil {
		return err
	}
	domain.TurnstileWidget = widget
	return nil
}

// RunAdtySiteDomainCheckOnce 查一遍 pending 的域名：status 和 ssl.status 都 active 才改成 active 并登记
// Turnstile；超过 72 小时还没通就标 failed，并删掉 Cloudflare 上的主机名。active 但还没登记进 widget 的也重试登记。
func RunAdtySiteDomainCheckOnce(ctx context.Context, now time.Time) error {
	cfg := resellerSetting()
	if !cfg.CloudflareConfigured() {
		return nil
	}
	var domains []model.AdtySiteDomain
	nonDeletingSites := model.DB.Model(&model.AdtySite{}).Select("id").Where("status <> ?", model.AdtySiteStatusDeleting)
	if err := model.DB.WithContext(ctx).
		Where("automation_state = ? AND lease_until <= ? AND site_id IN (?)", "", now.Unix(), nonDeletingSites).
		Where("status IN ?", []string{model.AdtySiteDomainPending, model.AdtySiteDomainActive}).
		Find(&domains).Error; err != nil {
		return err
	}
	changed := false
	// 一行坏域名不能把整轮停在自己身上：后面的行照样要查。失败逐行留痕，全部跑完
	// 再一起报上去——改成 continue 不等于让 DB 故障看不见。
	var failures []string
	for i := range domains {
		activated, err := adtyCheckDomainSnapshot(ctx, &domains[i], now)
		if err != nil {
			common.SysError(common.LogText("adty domain check failed for domain %d: %s", domains[i].Id, err.Error()))
			failures = append(failures, fmt.Sprintf("domain %d: %s", domains[i].Id, err.Error()))
			continue
		}
		changed = changed || activated
	}
	if changed {
		InvalidateAdtySiteCache()
	}
	if len(failures) > 0 {
		return fmt.Errorf("adty domain check failed for %d of %d domains: %s", len(failures), len(domains), strings.Join(failures, "; "))
	}
	return nil
}

func adtyCheckDomainSnapshot(ctx context.Context, snapshot *model.AdtySiteDomain, now time.Time) (changed bool, err error) {
	err = adtyWithDomainLifecycle(ctx, snapshot, func(tx *gorm.DB, domain *model.AdtySiteDomain) error {
		cfg := resellerSetting()
		zone := adtyDomainEffectiveZone(domain, cfg)
		if domain.Status == model.AdtySiteDomainActive {
			detail, err := AdtyCloudflare.getCustomHostname(ctx, zone, domain.CFHostnameId)
			if err != nil {
				return err
			}
			if detail.Status != "active" || detail.SSL.Status != "active" {
				changed = true
				return adtyDomainSnapshotQuery(tx, domain).Updates(map[string]any{"status": model.AdtySiteDomainPending, "ssl_status": detail.SSL.Status, "verify_records": verifyRecordsFor(domain.Hostname, cfg.CnameTarget, detail), "last_checked_at": now.Unix(), "created_at": now.Unix()}).Error
			}
			if err := adtyProbeDomainHTTPS(ctx, domain); err != nil {
				changed = true
				return adtyDomainSnapshotQuery(tx, domain).Updates(map[string]any{"status": model.AdtySiteDomainPending, "last_error": adtyDomainAutomationUserError(err).Error(), "created_at": now.Unix()}).Error
			}
			return adtyAssignCheckedDomainTurnstile(ctx, tx, domain)
		}
		if domain.Status != model.AdtySiteDomainPending {
			return nil
		}
		if domain.CFHostnameId == "" {
			if now.Sub(time.Unix(domain.CreatedAt, 0)) > adtyCustomDomainTimeout {
				return adtyMarkCustomDomainFailed(ctx, tx, zone, domain, map[string]any{"last_checked_at": now.Unix()})
			}
			if err := adtyAttachCustomHostnameLocked(ctx, tx, domain); err != nil {
				// Cloudflare 拒绝这个主机名（非法、已被别的 zone 占用、token 缺权限……）
				// 是这一行的属性，不是整批的：记账，并让超时判断也能看到它。
				// 其它错误（DB 写失败之类）照旧上抛：事务已经坏了，不能装作没事。
				if !errors.Is(err, ErrAdtyCloudflareFailed) && !errors.Is(err, ErrAdtyCloudflareRejected) {
					return err
				}
				updates := map[string]any{"last_checked_at": now.Unix(), "last_error": adtyDomainAutomationUserError(err).Error()}
				if now.Sub(time.Unix(domain.CreatedAt, 0)) > adtyCustomDomainTimeout {
					return adtyMarkCustomDomainFailed(ctx, tx, zone, domain, updates)
				}
				return adtyDomainSnapshotQuery(tx, domain).Updates(updates).Error
			}
			if domain.CFHostnameId == "" {
				updates := map[string]any{"last_checked_at": now.Unix()}
				if now.Sub(time.Unix(domain.CreatedAt, 0)) > adtyCustomDomainTimeout {
					return adtyMarkCustomDomainFailed(ctx, tx, zone, domain, updates)
				}
				return adtyDomainSnapshotQuery(tx, domain).Updates(updates).Error
			}
		}
		updates := map[string]any{"last_checked_at": now.Unix()}
		detail, err := AdtyCloudflare.getCustomHostname(ctx, zone, domain.CFHostnameId)
		switch {
		case err != nil:
			// 网络或 Cloudflare 出错：保持 pending，下次再查。
			updates["last_error"] = adtyDomainAutomationUserError(err).Error()
		case detail.Status == "active" && detail.SSL.Status == "active":
			if err := adtyProbeDomainHTTPS(ctx, domain); err != nil {
				updates["last_error"] = adtyDomainAutomationUserError(err).Error()
			} else {
				updates["status"], updates["ssl_status"], updates["last_error"] = model.AdtySiteDomainActive, detail.SSL.Status, ""
			}
		default:
			updates["ssl_status"] = detail.SSL.Status
			updates["verify_records"] = verifyRecordsFor(domain.Hostname, strings.TrimSpace(cfg.CnameTarget), detail)
			if len(detail.VerificationErrors) > 0 {
				updates["last_error"] = strings.Join(detail.VerificationErrors, "; ")
			}
		}
		if updates["status"] == nil && now.Sub(time.Unix(domain.CreatedAt, 0)) > adtyCustomDomainTimeout {
			return adtyMarkCustomDomainFailed(ctx, tx, zone, domain, updates)
		}
		result := adtyDomainSnapshotQuery(tx, domain).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 && updates["status"] == model.AdtySiteDomainActive {
			changed = true
			domain.Status = model.AdtySiteDomainActive
			if err := adtyAssignCheckedDomainTurnstile(ctx, tx, domain); err != nil {
				return err
			}
		}
		return nil
	})
	return changed, err
}

// adtyLoadActiveCustomDomains 是站点识别用的「active 的自定义域名 → 站点 id」（AdtyLoadCustomDomains）。
func adtyLoadActiveCustomDomains() (map[string]int, error) {
	var domains []model.AdtySiteDomain
	if err := model.DB.Select("hostname", "site_id").
		Where("status = ? AND managed_by <> ?", model.AdtySiteDomainActive, "platform").
		Find(&domains).Error; err != nil {
		return nil, err
	}
	hosts := make(map[string]int, len(domains))
	for _, domain := range domains {
		hosts[strings.ToLower(domain.Hostname)] = domain.SiteId
	}
	return hosts, nil
}

// adtyLoadApiOnlyCustomDomains 是「用途是 API 地址、已 active 的自定义域名」（AdtyLoadApiOnlyDomains）。
// 这些主机只放行 relay 和探活接口，页面和账号接口都拒绝。
func adtyLoadApiOnlyCustomDomains() (map[string]bool, error) {
	var domains []model.AdtySiteDomain
	if err := model.DB.Select("hostname").
		Where("status = ? AND role = ?", model.AdtySiteDomainActive, model.AdtySiteDomainRoleBaseURL).
		Find(&domains).Error; err != nil {
		return nil, err
	}
	hosts := make(map[string]bool, len(domains))
	for _, domain := range domains {
		hosts[strings.ToLower(domain.Hostname)] = true
	}
	return hosts, nil
}

func startAdtySiteDomainTask() {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		var nextLegacyCheck time.Time
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			if err := RunAdtyDomainAutomationOnce(ctx, time.Now()); err != nil {
				common.SysError(common.LogText("custom domain automation: %s", err.Error()))
			}
			if time.Now().After(nextLegacyCheck) {
				if err := RunAdtySiteDomainCheckOnce(ctx, time.Now()); err != nil {
					common.SysError(common.LogText("custom domain check task: %s", err.Error()))
				}
				nextLegacyCheck = time.Now().Add(5 * time.Minute)
			}
			cancel()
		}
	}()
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtySiteDomainTask)
	AdtyLoadCustomDomains = adtyLoadActiveCustomDomains
	AdtyLoadApiOnlyDomains = adtyLoadApiOnlyCustomDomains
	previous := AdtyOnSiteClosed
	AdtyOnSiteClosed = func(site *model.AdtySite) {
		if previous != nil {
			previous(site)
		}
		adtyRemoveDomainsOfClosedSite(site)
	}
}

// ListAdtySiteDomains 列出站长自己站上没删除的自定义域名。closing 仍可查看（11.6）。
func ListAdtySiteDomains(ownerUserId int) ([]model.AdtySiteDomain, error) {
	site, err := AdtyOwnerSite(ownerUserId)
	if errors.Is(err, ErrAdtySiteNotOwner) {
		return nil, ErrAdtySiteNotFound
	}
	if err != nil {
		return nil, err
	}
	domains := []model.AdtySiteDomain{}
	err = model.DB.Where("site_id = ? AND status <> ?", site.Id, model.AdtySiteDomainRemoved).Order("id").Find(&domains).Error
	return domains, err
}
