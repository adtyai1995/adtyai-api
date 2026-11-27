package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 站点表的进程内缓存（执行层决定 28）。变更时清缓存；多实例下按 SYNC_FREQUENCY 过期重读（第八节）。
// 11.8 的自定义域名表建好后，在 AdtyLoadCustomDomains 里挂上「已 active 的自定义域名 → 站点 id」。

type adtySiteSnapshot struct {
	byId         map[int]*model.AdtySite
	byHost       map[string]*model.AdtySite // <子域名>.<根域名>
	byCustom     map[string]*model.AdtySite // 已 active 的自定义域名
	apiOnly      map[string]bool            // 用途是 base_url 的自定义域名：只放行 relay
	defaultAPI   map[int]string
	loadedAt     time.Time
	verCheckedAt time.Time
	redisVersion string
	loadErr      error
}

const (
	adtySiteCacheVersionRedisKey      = "adty:site_cache:version"
	adtySiteCacheVersionCheckInterval = 2 * time.Second
)

var (
	adtySiteCacheMu sync.Mutex
	adtySiteCache   *adtySiteSnapshot
)

// AdtyLoadCustomDomains 由 11.8 注册：返回已经 active 的自定义域名（小写主机名 → 站点 id）。
var AdtyLoadCustomDomains func() (map[string]int, error)

// AdtyLoadApiOnlyDomains 由 11.8 注册：返回已经 active、用途是 API 地址（role=base_url）的自定义域名。
// 这些主机只放行 relay 路径（/v1 等），页面和账号接口一律拒绝，让站长给客户的「API 地址」是个纯接口。
var AdtyLoadApiOnlyDomains func() (map[string]bool, error)

// AdtySiteNames returns display names without exposing the sites' financial or private settings.
// A missing historical site keeps its ID and an empty name; site 0 is always the main site.
func AdtySiteNames(siteIDs []int) (map[int]string, error) {
	names := map[int]string{0: common.GetSystemName()}
	ids := make([]int, 0, len(siteIDs))
	seen := map[int]bool{}
	for _, id := range siteIDs {
		if id > 0 && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if len(ids) == 0 {
		return names, nil
	}
	var sites []model.AdtySite
	if err := model.DB.Select("id", "subdomain", "root_domain", "brand").Where("id IN ?", ids).Find(&sites).Error; err != nil {
		return nil, err
	}
	for i := range sites {
		names[sites[i].Id] = AdtyBrandFor(&sites[i]).Name
	}
	return names, nil
}

func currentAdtySiteCacheRedisVersion() string {
	if !common.RedisEnabled || common.RDB == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	val, err := common.RDB.Get(ctx, adtySiteCacheVersionRedisKey).Result()
	if err != nil {
		return ""
	}
	return val
}

func newAdtySiteCacheVersionToken() string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return fmt.Sprintf("%d:%s", time.Now().UnixNano(), hex.EncodeToString(raw))
}

// InvalidateAdtySiteCache 在站点或自定义域名变更后调用，下次读取时重建并广播版本到其他实例。
func InvalidateAdtySiteCache() {
	adtySiteCacheMu.Lock()
	adtySiteCache = nil
	adtySiteCacheMu.Unlock()
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = common.RDB.Set(ctx, adtySiteCacheVersionRedisKey, newAdtySiteCacheVersionToken(), 0).Err()
		cancel()
	}
}

func adtySiteCacheTTL() time.Duration {
	seconds := common.SyncFrequency
	if seconds <= 0 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

func loadAdtySiteSnapshot() *adtySiteSnapshot {
	ttl := adtySiteCacheTTL()
	adtySiteCacheMu.Lock()
	if adtySiteCache != nil && time.Since(adtySiteCache.loadedAt) < ttl &&
		(!common.RedisEnabled || common.RDB == nil || time.Since(adtySiteCache.verCheckedAt) < adtySiteCacheVersionCheckInterval) {
		cached := adtySiteCache
		adtySiteCacheMu.Unlock()
		return cached
	}
	adtySiteCacheMu.Unlock()

	redisVer := currentAdtySiteCacheRedisVersion()
	now := time.Now()
	adtySiteCacheMu.Lock()
	defer adtySiteCacheMu.Unlock()
	if adtySiteCache != nil && time.Since(adtySiteCache.loadedAt) < ttl &&
		(redisVer == "" || adtySiteCache.redisVersion == redisVer) {
		adtySiteCache.verCheckedAt = now
		return adtySiteCache
	}
	snapshot := &adtySiteSnapshot{
		byId: map[int]*model.AdtySite{}, byHost: map[string]*model.AdtySite{},
		byCustom: map[string]*model.AdtySite{}, apiOnly: map[string]bool{}, defaultAPI: map[int]string{},
		loadedAt: now, verCheckedAt: now, redisVersion: redisVer,
	}
	var sites []model.AdtySite
	if err := model.DB.Find(&sites).Error; err != nil {
		// 读不到站点表时不缓存，并保留错误供调用方返回 503 而非误判 404 导致分站掉登录。
		common.SysError(common.LogText("load adty sites failed: %s", err.Error()))
		snapshot.loadErr = err
		return snapshot
	}
	for i := range sites {
		site := &sites[i]
		snapshot.byId[site.Id] = site
		snapshot.byHost[strings.ToLower(site.Subdomain+"."+site.RootDomain)] = site
	}
	var loadErr error
	if AdtyLoadCustomDomains != nil {
		domains, err := AdtyLoadCustomDomains()
		if err != nil {
			loadErr = err
			common.SysError(common.LogText("load adty custom domains failed: %s", err.Error()))
		}
		for host, siteId := range domains {
			if site, ok := snapshot.byId[siteId]; ok {
				snapshot.byCustom[strings.ToLower(host)] = site
			}
		}
	}
	if AdtyLoadApiOnlyDomains != nil {
		apiHosts, err := AdtyLoadApiOnlyDomains()
		if err != nil {
			if loadErr == nil {
				loadErr = err
			}
			common.SysError(common.LogText("load adty api-only domains failed: %s", err.Error()))
			// Never serve a custom API hostname as an account website without its role.
			clear(snapshot.byCustom)
		}
		for host := range apiHosts {
			snapshot.apiOnly[strings.ToLower(host)] = true
		}
	}
	var fixedDomains []model.AdtySiteDomain
	if err := model.DB.Where("managed_by = ? AND role = ?", "platform", model.AdtySiteDomainRoleBaseURL).Find(&fixedDomains).Error; err != nil {
		snapshot.loadErr = err
		return snapshot
	}
	fixedBySite := make(map[int]model.AdtySiteDomain, len(fixedDomains))
	for _, domain := range fixedDomains {
		fixedBySite[domain.SiteId] = domain
	}
	for i := range sites {
		site := &sites[i]
		label := fmt.Sprintf("api-s%d", site.Id)
		fixed := fixedBySite[site.Id]
		if fixed.Id > 0 {
			snapshot.apiOnly[fixed.Hostname] = true
			if fixed.Status == model.AdtySiteDomainActive && fixed.DesiredState == "serve" {
				snapshot.defaultAPI[site.Id] = fixed.Hostname
				snapshot.byHost[fixed.Hostname] = site
			}
			continue
		}
		for n := 0; ; n++ {
			candidate := label
			if n > 0 {
				candidate += fmt.Sprintf("-%d", n)
			}
			host := candidate + "." + strings.ToLower(site.RootDomain)
			if snapshot.byHost[host] != nil || snapshot.byCustom[host] != nil || AdtyIsMainHost(host) || host == resellerSetting().CnameTarget || host == resellerSetting().FallbackOrigin {
				continue
			}
			snapshot.byHost[host], snapshot.apiOnly[host], snapshot.defaultAPI[site.Id] = site, true, host
			break
		}
	}
	if loadErr == nil {
		adtySiteCache = snapshot
	} else {
		snapshot.loadErr = loadErr
	}
	return snapshot
}

// AdtyIsApiOnlyHost 识别本站默认 API 域名和已激活的 API 自定义域名。
// 这类主机既放行模型中转 API，也允许打开本站首页并完整登录、操作控制台。
func AdtyIsApiOnlyHost(host string) bool {
	host = AdtyNormalizeHost(host)
	if host == "" {
		return false
	}
	return loadAdtySiteSnapshot().apiOnly[host]
}

// AdtyIsApiOnlyHostTx checks whether host is a subsite default API domain or an
// active API custom domain strictly on tx without touching outer model.DB or
// adtySiteCacheMu (R14-P1-05).
func AdtyIsApiOnlyHostTx(tx *gorm.DB, host string) (bool, error) {
	host = AdtyNormalizeHost(host)
	if host == "" || tx == nil {
		return false, nil
	}
	hasDomains, err := model.AdtyTableExists(tx, &model.AdtySiteDomain{})
	if err != nil {
		return false, err
	}
	if hasDomains {
		var domainCount int64
		if err := tx.Model(&model.AdtySiteDomain{}).
			Where("LOWER(hostname) = ? AND role = ? AND (status = ? OR managed_by = ?)",
				host, model.AdtySiteDomainRoleBaseURL, model.AdtySiteDomainActive, "platform").
			Count(&domainCount).Error; err != nil {
			return false, err
		}
		if domainCount > 0 {
			return true, nil
		}
	}
	hasSites, err := model.AdtyTableExists(tx, &model.AdtySite{})
	if err != nil {
		return false, err
	}
	if !hasSites {
		return false, nil
	}
	var sites []model.AdtySite
	if err := tx.Select("id", "subdomain", "root_domain").Find(&sites).Error; err != nil {
		return false, err
	}
	if len(sites) == 0 {
		return false, nil
	}
	byHost := make(map[string]bool, len(sites)*2)
	byCustom := make(map[string]bool)
	fixedBySite := make(map[int]string)
	for i := range sites {
		byHost[strings.ToLower(sites[i].Subdomain+"."+sites[i].RootDomain)] = true
	}
	if hasDomains {
		var fixedDomains []model.AdtySiteDomain
		if err := tx.Select("site_id", "hostname", "status", "desired_state").
			Where("managed_by = ? AND role = ?", "platform", model.AdtySiteDomainRoleBaseURL).
			Find(&fixedDomains).Error; err != nil {
			return false, err
		}
		for _, domain := range fixedDomains {
			h := strings.ToLower(strings.TrimSpace(domain.Hostname))
			fixedBySite[domain.SiteId] = h
			if domain.Status == model.AdtySiteDomainActive && domain.DesiredState == "serve" && h != "" {
				byHost[h] = true
			}
		}
		var customDomains []model.AdtySiteDomain
		if err := tx.Select("hostname").
			Where("status = ? AND role <> ?", model.AdtySiteDomainActive, model.AdtySiteDomainRoleBaseURL).
			Find(&customDomains).Error; err != nil {
			return false, err
		}
		for _, domain := range customDomains {
			byCustom[strings.ToLower(strings.TrimSpace(domain.Hostname))] = true
		}
	}
	cnameTarget := resellerSetting().CnameTarget
	fallbackOrigin := resellerSetting().FallbackOrigin
	for i := range sites {
		site := &sites[i]
		if fixedBySite[site.Id] != "" {
			continue
		}
		label := fmt.Sprintf("api-s%d", site.Id)
		for n := 0; ; n++ {
			candidate := label
			if n > 0 {
				candidate += fmt.Sprintf("-%d", n)
			}
			synthHost := candidate + "." + strings.ToLower(site.RootDomain)
			if byHost[synthHost] || byCustom[synthHost] || AdtyIsMainHost(synthHost) || synthHost == cnameTarget || synthHost == fallbackOrigin {
				continue
			}
			byHost[synthHost] = true
			if synthHost == host {
				return true, nil
			}
			break
		}
	}
	return false, nil
}

// AdtySiteById 从缓存取站点（只读，调用方不要改返回值）；没有返回 nil。
func AdtySiteById(siteId int) *model.AdtySite {
	if siteId <= 0 {
		return nil
	}
	return loadAdtySiteSnapshot().byId[siteId]
}

// AdtySiteForUser 是用户所属的站点；主站用户、查不到用户或站点时返回 nil（11.3 的邮件链接用它）。
func AdtySiteForUser(userId int) *model.AdtySite {
	if userId <= 0 {
		return nil
	}
	user, err := model.GetUserCache(userId)
	if err != nil {
		return nil
	}
	return AdtySiteById(user.SiteId)
}

// AdtySiteContextKey 是 gin 上下文里当前站点的键（11.2 的中间件写入）。
const AdtySiteContextKey = "adty_site"

// AdtyNormalizeHost 去掉端口、末尾的点，转小写。
func AdtyNormalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func hostOfURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return AdtyNormalizeHost(parsed.Host)
}

// AdtyIsDefaultServerAddress reports whether ServerAddress is still at its
// unconfigured or localhost default (used to allow initial /setup on a fresh
// deployment before ServerAddress is configured — R1-013).
func AdtyIsDefaultServerAddress() bool {
	host := hostOfURL(system_setting.GetServerAddress())
	return host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// ValidateMainSiteDomainSeparation enforces that the main site's website host
// (ServerAddress) and dedicated API host (adty_site_setting.api_base_url) are
// strictly distinct whenever APIBaseURL is configured, and rejects invalid URLs
// with missing schemes or non-empty path/query/fragment/userinfo (R1-100).
func ValidateMainSiteDomainSeparation(serverAddress, apiBaseURL string) error {
	serverRaw := strings.TrimSpace(serverAddress)
	var siteHost string
	if serverRaw != "" {
		parsedServer, err := url.Parse(serverRaw)
		if err != nil || (parsedServer.Scheme != "http" && parsedServer.Scheme != "https") || AdtyNormalizeHost(parsedServer.Host) == "" {
			return common.NewMessage("Enter a full URL starting with http:// or https://")
		}
		siteHost = AdtyNormalizeHost(parsedServer.Host)
	}
	apiRaw := strings.TrimSpace(apiBaseURL)
	if apiRaw == "" {
		return nil
	}
	parsedAPI, err := url.Parse(apiRaw)
	if err != nil || (parsedAPI.Scheme != "http" && parsedAPI.Scheme != "https") || AdtyNormalizeHost(parsedAPI.Host) == "" {
		return common.NewMessage("Enter a full URL starting with http:// or https://")
	}
	if parsedAPI.User != nil || (parsedAPI.Path != "" && parsedAPI.Path != "/") || parsedAPI.RawQuery != "" || parsedAPI.Fragment != "" {
		return common.NewMessage("API base URL must contain only scheme and host without path, query, or fragment")
	}
	apiHost := AdtyNormalizeHost(parsedAPI.Host)
	if siteHost != "" && apiHost == siteHost {
		return common.NewMessage("API base URL must use a different domain from the website server address")
	}
	return nil
}

// AdtyIsMainSiteHost 识别主站网站/首页域名（ServerAddress）。
// 主站网站域名只提供网页与控制台，禁止调用 AI 模型中转 API。
func AdtyIsMainSiteHost(host string) bool {
	host = AdtyNormalizeHost(host)
	if host == "" {
		return false
	}
	siteHost := hostOfURL(system_setting.GetServerAddress())
	return siteHost != "" && host == siteHost
}

// AdtyIsMainAPIHost 识别主站专用 API 调用域名（adty_site_setting.api_base_url）。
// 主站 API 域名与主站网站域名分离时，既提供全部 AI 模型中转 API，也允许完整打开首页与控制台。
func AdtyIsMainAPIHost(host string) bool {
	host = AdtyNormalizeHost(host)
	if host == "" {
		return false
	}
	apiHost := hostOfURL(adty_setting.GetSiteSetting().APIBaseURL)
	return apiHost != "" && host == apiHost
}

// AdtyIsMainHost 说明这是不是主站自己的主机名：主站网站域名（ServerAddress）或主站 API 域名（api_base_url）。
func AdtyIsMainHost(host string) bool {
	return AdtyIsMainSiteHost(host) || AdtyIsMainAPIHost(host)
}

// AdtyHostAllowsRelay 判断该 Host 是否允许调用 AI 模型中转 API（主站与分站统一规则）：
//   - 网站/首页域名绝对禁止调用模型中转 API；
//   - 主站仅当配置了独立于 ServerAddress 的 api_base_url 时，由该 API 域名承接模型中转；
//   - 分站仅由本站默认 API 域名（api-s<id>.<root>）或已激活的 role=base_url 自定义 API 域名承接。
func AdtyHostAllowsRelay(host string) bool {
	host = AdtyNormalizeHost(host)
	if host == "" {
		return false
	}
	if AdtyIsMainHost(host) {
		return AdtyIsMainAPIHost(host) && !AdtyIsMainSiteHost(host)
	}
	if !resellerSetting().Enabled || !AdtyIsApiOnlyHost(host) {
		return false
	}
	return AdtySiteServesCustomers(AdtySiteForHost(host))
}

// AdtyAPIKeyHostStatus checks the key owner's site against the trusted request
// Host. It resolves the host itself so every token-authenticated route is covered,
// including routes mounted without the site-context middleware.
func AdtyAPIKeyHostStatus(siteID int, host string) int {
	if AdtyIsMainHost(host) {
		if siteID == 0 {
			return 0
		}
		return http.StatusForbidden
	}
	if !resellerSetting().Enabled {
		return http.StatusNotFound
	}
	site := AdtySiteForHost(host)
	if site == nil {
		return http.StatusNotFound
	}
	if !AdtySiteServesCustomers(site) || site.Id != siteID {
		return http.StatusForbidden
	}
	return 0
}

// AdtySiteForHost 按主机名查站点：先查已 active 的自定义域名，再按「子域名.根域名」查。
// 没有对应站点返回 nil。站点状态由调用方判断（pending 的站点还没开，调用方当作查不到）。
// AdtyIsCustomDomainHost reports an active custom hostname, not a platform subdomain.
func AdtyIsCustomDomainHost(host string) bool {
	host = AdtyNormalizeHost(host)
	if host == "" {
		return false
	}
	_, ok := loadAdtySiteSnapshot().byCustom[host]
	return ok
}

func AdtySiteForHost(host string) *model.AdtySite {
	site, _ := AdtySiteForHostWithError(host)
	return site
}

// AdtySiteForHostWithError distinguishes genuine database lookup errors
// (err != nil -> 503) from missing/inactive sites (nil, nil -> 404).
func AdtySiteForHostWithError(host string) (*model.AdtySite, error) {
	host = AdtyNormalizeHost(host)
	if host == "" {
		return nil, nil
	}
	snapshot := loadAdtySiteSnapshot()
	site := snapshot.byCustom[host]
	fromCustom := site != nil
	if site == nil {
		site = snapshot.byHost[host]
	}
	if site == nil {
		if snapshot.loadErr != nil {
			return nil, snapshot.loadErr
		}
		return nil, nil
	}
	// Read the authoritative status so a close on another instance wins over
	// the local branding cache. A database failure must not serve stale business
	// nor trigger a false 404 logout on subsites (R-32 #26).
	var current model.AdtySite
	if err := model.DB.Select("id", "status", "close_transfer_to", "brand", "home_page_content", "legal_docs", "archived_hidden_at").First(&current, site.Id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var binding model.AdtySiteDomain
	if err := model.DB.Where("hostname = ?", host).Limit(1).Find(&binding).Error; err != nil {
		return nil, err
	}
	if fromCustom && binding.Id == 0 {
		return nil, nil
	}
	if binding.Id > 0 && (binding.SiteId != site.Id || binding.Status != model.AdtySiteDomainActive || binding.DesiredState == "removed" || (binding.Role == model.AdtySiteDomainRoleBaseURL) != snapshot.apiOnly[host]) {
		return nil, nil
	}
	copy := *site
	copy.Status, copy.CloseTransferTo = current.Status, current.CloseTransferTo
	copy.Brand = current.Brand
	copy.HomePageContent = current.HomePageContent
	copy.LegalDocs = current.LegalDocs
	copy.ArchivedHiddenAt = current.ArchivedHiddenAt
	return &copy, nil
}

// AdtyRejectSessionAPIStatus 判断这个 Host 能不能调用登录、注册、验证码和找回密码。
// 主站、还在服务的平台子域名和还在服务的自定义域名返回 0（自定义域名承接完整客户端体验）。
// 已关闭、已停止服务的站返回 403，数据库瞬时异常返回 503，其余 Host 返回 404。
func AdtyRejectSessionAPIStatus(host string) int {
	host = AdtyNormalizeHost(host)
	if host == "" || AdtyIsMainHost(host) || !resellerSetting().Enabled {
		return 0
	}
	site, err := AdtySiteForHostWithError(host)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if site == nil {
		return http.StatusNotFound
	}
	if !AdtySiteServesCustomers(site) {
		return http.StatusForbidden
	}
	return 0
}

// AdtyRequestSiteID is the current request's site. The main site is 0.
// It uses the trusted host mapping already placed in the request context.
func AdtyRequestSiteID(c *gin.Context) int {
	if site := AdtySiteForRequest(c); site != nil {
		return site.Id
	}
	return 0
}

// AdtySiteForRequest 是当前请求的站点（11.2 的中间件放进上下文）；主站返回 nil。
// 只认站点表，不读 Host 头之外的东西（防 Host header injection，执行层决定 28）。
func AdtySiteForRequest(c *gin.Context) *model.AdtySite {
	if c == nil {
		return nil
	}
	value, ok := c.Get(AdtySiteContextKey)
	if !ok {
		return nil
	}
	site, _ := value.(*model.AdtySite)
	return site
}

// AdtySiteServesCustomers 说明站点现在对客户开放：active、paused、closing 都照常识别（11.2）。
func AdtySiteServesCustomers(site *model.AdtySite) bool {
	if site == nil {
		return false
	}
	switch site.Status {
	case model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing:
		return true
	}
	return false
}

// AdtySiteBaseURL 是站点的平台子域名地址，不用自定义域名；nil 返回 ServerAddress。
// 协议跟主站 ServerAddress 一样（生产是 https）。邮件链接和支付跳回都用它（11.2、11.3）。
func AdtySiteBaseURL(site *model.AdtySite) string {
	main := strings.TrimRight(system_setting.GetServerAddress(), "/")
	if site == nil {
		return main
	}
	scheme := "https"
	if parsed, err := url.Parse(main); err == nil && parsed.Scheme != "" {
		scheme = parsed.Scheme
	}
	return scheme + "://" + AdtySiteDisplayHost(site)
}

// AdtySitePublicBaseURL 是「用户看得见的返回地址」：站点在服务并且绑了已激活的自定义域名时，
// 优先用自定义域名（站长的品牌门面），否则退回平台子域名。
//
// 支付返回与账号邮件使用这个地址，SEO 的 canonical 继续使用稳定的平台子域名。
func AdtySitePublicBaseURL(site *model.AdtySite) string {
	main := strings.TrimRight(system_setting.GetServerAddress(), "/")
	if site == nil {
		return main
	}
	if host := adtyPreferredCustomHost(site.Id); host != "" {
		scheme := "https"
		if parsed, err := url.Parse(main); err == nil && parsed.Scheme != "" {
			scheme = parsed.Scheme
		}
		return scheme + "://" + host
	}
	return AdtySiteBaseURL(site)
}

// adtyPreferredCustomHost 挑一个站点对外露出的自定义域名：只取用途是站点域名（role=site）的那些，
// 按创建顺序取第一个。API 地址域名（role=base_url）不能拿来当跳回地址——那种域名不给人看页面。
// 没有已激活的站点域名时返回空，调用方退回平台子域名。
func adtyPreferredCustomHost(siteId int) string {
	return adtyAdvertisedDomain(siteId, model.AdtySiteDomainRoleSite)
}

func adtyAdvertisedDomain(siteID int, role string) string {
	if siteID <= 0 || model.DB == nil {
		return ""
	}
	var binding model.AdtySiteDomain
	err := model.DB.Select("hostname").Where("site_id = ? AND role = ? AND managed_by <> ? AND status = ? AND desired_state = ? AND automation_state IN ?", siteID, role, "platform", model.AdtySiteDomainActive, "serve", []string{"", "ready", "retry"}).Order("id").Limit(1).Find(&binding).Error
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(binding.Hostname))
}

// AdtySiteAPIBaseURL 返回客户调用模型的专用地址。
// 主站只返回独立配置的 api_base_url（未配置时为空，不回退到禁止调用模型的 ServerAddress）；
// 分站优先返回已激活的自定义 API 域名，其次返回就绪的平台默认 API 域名。
func AdtySiteAPIBaseURL(site *model.AdtySite) string {
	if site == nil {
		apiBase := strings.TrimRight(strings.TrimSpace(adty_setting.GetSiteSetting().APIBaseURL), "/")
		if apiBase == "" || hostOfURL(apiBase) == hostOfURL(system_setting.GetServerAddress()) {
			return ""
		}
		return apiBase
	}
	snapshot := loadAdtySiteSnapshot()
	if host := adtyAdvertisedDomain(site.Id, model.AdtySiteDomainRoleBaseURL); host != "" {
		return adtyScheme() + "://" + host
	}
	if host := snapshot.defaultAPI[site.Id]; host != "" && AdtySiteForHost(host) != nil {
		return adtyScheme() + "://" + host
	}
	return ""
}

// adtyScheme 跟主站 ServerAddress 用同一个协议（生产是 https）。
func adtyScheme() string {
	if parsed, err := url.Parse(strings.TrimRight(system_setting.GetServerAddress(), "/")); err == nil && parsed.Scheme != "" {
		return parsed.Scheme
	}
	return "https"
}

// IsAdtyReferralEnabledForSite reports whether referral/invite commissions are currently enabled for siteID.
// Main site (siteID == 0): checks adty_setting.GetReferralSetting().Enabled.
// Subsite (siteID > 0): checks site is active (not closed) and adty_site_referral_rates has enabled=true
// with either a positive site rate or at least one active affiliate carrying a positive rate_override.
func IsAdtyReferralEnabledForSite(siteID int) bool {
	if siteID < 0 {
		return false
	}
	if siteID == 0 {
		return adty_setting.GetReferralSetting().Enabled
	}
	if model.DB == nil {
		return false
	}
	var site model.AdtySite
	if err := model.DB.Select("id", "status").Where("id = ?", siteID).Limit(1).Find(&site).Error; err != nil || site.Id == 0 {
		return false
	}
	if !AdtySiteServesCustomers(&site) {
		return false
	}
	var rate model.AdtySiteReferralRate
	lookup := model.DB.Where("site_id = ?", siteID).Limit(1).Find(&rate)
	if lookup.Error != nil || lookup.RowsAffected == 0 || !rate.Enabled {
		return false
	}
	if rate.Rate.IsPositive() {
		return true
	}
	hasAffTable, err := model.AdtyTableExists(model.DB, &model.AdtySiteReferralAffiliate{})
	if err != nil {
		common.SysError(common.LogText("check site referral affiliate table: %s", err.Error()))
		return false
	}
	if !hasAffTable {
		return false
	}
	var overrides []model.AdtySiteReferralAffiliate
	if err := model.DB.Select("rate_override").
		Where("site_id = ? AND status <> ? AND rate_override IS NOT NULL", siteID, model.AdtySiteAffiliateStatusDisabled).
		Find(&overrides).Error; err != nil {
		return false
	}
	return slices.ContainsFunc(overrides, func(aff model.AdtySiteReferralAffiliate) bool {
		return aff.RateOverride != nil && aff.RateOverride.IsPositive()
	})
}

// AdtyPublicReferralInfo is the public non-sensitive referral summary exposed in GET /api/status
// so white-label documentation automatically reflects live main-site and subsite commission rules.
type AdtyPublicReferralInfo struct {
	Enabled              bool     `json:"enabled"`
	DefaultRatePercent   string   `json:"default_rate_percent,omitempty"`
	ObservationDays      int      `json:"observation_days"`
	MinWithdrawUSD       string   `json:"min_withdraw_usd,omitempty"`
	USDTWithdrawFeeUSD   string   `json:"usdt_withdraw_fee_usd,omitempty"`
	WithdrawMethods      []string `json:"withdraw_methods"`
	WithdrawUSDTNetworks []string `json:"withdraw_usdt_networks"`
}

func formatAdtyPublicDecimal(d decimal.Decimal) string {
	s := d.String()
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

func formatAdtyPublicFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// AdtyPublicReferralInfoForSite returns the public referral summary for siteID.
// Main site (siteID == 0) stores DefaultRate as percentage (10 = 10%);
// subsite (siteID > 0) stores AdtySiteReferralRate.Rate as fraction (0.10 = 10%).
func AdtyPublicReferralInfoForSite(siteID int) AdtyPublicReferralInfo {
	empty := AdtyPublicReferralInfo{
		Enabled:              false,
		WithdrawMethods:      []string{},
		WithdrawUSDTNetworks: []string{},
	}
	if siteID < 0 {
		return empty
	}
	if siteID == 0 {
		cfg := adty_setting.GetReferralSetting()
		if cfg == nil || !cfg.Enabled {
			return empty
		}
		info := AdtyPublicReferralInfo{
			Enabled:              true,
			ObservationDays:      max(cfg.FreezeDays, 0),
			WithdrawMethods:      []string{},
			WithdrawUSDTNetworks: []string{},
		}
		if cfg.DefaultRate != nil && *cfg.DefaultRate > 0 {
			info.DefaultRatePercent = formatAdtyPublicFloat(*cfg.DefaultRate)
		}
		hasPositiveCNYRate := false
		if rawRate := strings.TrimSpace(cfg.WithdrawCNYRate); rawRate != "" {
			if parsed, err := decimal.NewFromString(rawRate); err == nil && parsed.IsPositive() {
				hasPositiveCNYRate = true
			}
		}
		methods, networks := cfg.UsableWithdrawConfig(hasPositiveCNYRate)
		if methods != nil {
			info.WithdrawMethods = methods
		}
		if networks != nil {
			info.WithdrawUSDTNetworks = networks
		}
		if cfg.MinWithdrawUSD != nil && *cfg.MinWithdrawUSD > 0 && len(info.WithdrawMethods) > 0 {
			info.MinWithdrawUSD = formatAdtyPublicFloat(*cfg.MinWithdrawUSD)
		}
		if cfg.USDTWithdrawFee != nil && *cfg.USDTWithdrawFee >= 0 && slices.Contains(info.WithdrawMethods, adty_setting.ReferralWithdrawMethodUSDT) {
			info.USDTWithdrawFeeUSD = formatAdtyPublicFloat(*cfg.USDTWithdrawFee)
		}
		return info
	}
	if model.DB == nil {
		return empty
	}
	var site model.AdtySite
	if err := model.DB.Where("id = ?", siteID).Limit(1).Find(&site).Error; err != nil || site.Id == 0 {
		return empty
	}
	if !AdtySiteServesCustomers(&site) {
		return empty
	}
	var rate model.AdtySiteReferralRate
	lookup := model.DB.Where("site_id = ?", siteID).Limit(1).Find(&rate)
	if lookup.Error != nil || lookup.RowsAffected == 0 || !rate.Enabled {
		return empty
	}
	enabled := rate.Rate.IsPositive()
	if !enabled {
		hasAffTable, tblErr := model.AdtyTableExists(model.DB, &model.AdtySiteReferralAffiliate{})
		if tblErr != nil {
			common.SysError(common.LogText("check public referral affiliate table: %s", tblErr.Error()))
		} else if hasAffTable {
			var overrides []model.AdtySiteReferralAffiliate
			if err := model.DB.Select("rate_override").
				Where("site_id = ? AND status <> ? AND rate_override IS NOT NULL", siteID, model.AdtySiteAffiliateStatusDisabled).
				Find(&overrides).Error; err == nil {
				enabled = slices.ContainsFunc(overrides, func(aff model.AdtySiteReferralAffiliate) bool {
					return aff.RateOverride != nil && aff.RateOverride.IsPositive()
				})
			}
		}
	}
	if !enabled {
		return empty
	}
	info := AdtyPublicReferralInfo{
		Enabled:              true,
		WithdrawMethods:      []string{},
		WithdrawUSDTNetworks: []string{},
	}
	if rate.Rate.IsPositive() {
		info.DefaultRatePercent = formatAdtyPublicDecimal(rate.Rate.Mul(decimal.NewFromInt(100)))
	}
	if site.MarketingPolicy.ObservationDays != nil && *site.MarketingPolicy.ObservationDays > 0 {
		info.ObservationDays = int(*site.MarketingPolicy.ObservationDays)
	}
	methods, networks := usableSiteInviteWithdrawConfig(site.MarketingPolicy)
	if methods != nil {
		info.WithdrawMethods = methods
	}
	if networks != nil {
		info.WithdrawUSDTNetworks = networks
	}
	if site.MarketingPolicy.MinUSD != nil && site.MarketingPolicy.MinUSD.IsPositive() && len(info.WithdrawMethods) > 0 {
		info.MinWithdrawUSD = formatAdtyPublicDecimal(*site.MarketingPolicy.MinUSD)
	}
	if site.MarketingPolicy.FeeUSD != nil && !site.MarketingPolicy.FeeUSD.IsNegative() && slices.Contains(info.WithdrawMethods, adty_setting.ReferralWithdrawMethodUSDT) {
		info.USDTWithdrawFeeUSD = formatAdtyPublicDecimal(*site.MarketingPolicy.FeeUSD)
	}
	return info
}

// AdtyOverrideStatus 在 /api/status 末尾调用（controller/misc.go）：当前请求是分站时，换成分站的站名、Logo、
// 地址，关掉第三方登录和 Passkey，并清掉主站页脚、公告和 Passkey 域名。
// Turnstile 继续用全局 site key，不在这里查 widget。
// 它只读请求上下文和进程内站点：不查库，也不拿 OptionMap 的锁。所以调用点在锁内锁外都对——
// 现在 GetStatus 在拼响应之前就放掉了配置读锁（ISS-R4-0123）。
func AdtyOverrideStatus(c *gin.Context, data map[string]any) {
	site := AdtySiteForRequest(c)
	if site == nil {
		return
	}
	brand := AdtyBrandFor(site)
	legal, legalErr := AdtySiteLegalForWithError(site)
	data["system_name"] = brand.Name
	data["logo"] = brand.LogoURL
	data["site_title"] = brand.Title
	data["favicon"] = brand.FaviconURL
	delete(data, "theme_color")
	if legalErr != nil {
		data["user_agreement_enabled"] = true
		data["privacy_policy_enabled"] = false
	} else {
		data["user_agreement_enabled"] = AdtyHasLegalText(legal.UserAgreement)
		data["privacy_policy_enabled"] = AdtyHasLegalText(legal.PrivacyPolicy)
	}
	data["server_address"] = AdtySiteAPIBaseURL(site)
	data["adty_site"] = map[string]any{"id": site.Id, "status": site.Status, "subdomain": site.Subdomain}
	for _, key := range []string{"github_oauth", "discord_oauth", "linuxdo_oauth", "telegram_oauth", "wechat_login", "oidc_enabled", "passkey_login", "faq_enabled"} {
		data[key] = false
	}
	delete(data, "custom_oauth_providers")
	for _, key := range []string{"footer_html", "wechat_qrcode", "docs_link", "passkey_rp_id", "telegram_bot_name", "passkey_display_name"} {
		data[key] = ""
	}
	if brand.DocsURL != "" {
		data["docs_link"] = brand.DocsURL
	}
	for _, key := range []string{"chats", "api_info", "faq", "passkey_rp_ids"} {
		data[key] = []any{}
	}
	if data["announcements"] == nil {
		data["announcements"] = []any{}
	}
}

// AdtySiteLoginAllowedForUser 在已加载 *model.User 的入口强制站点域名登录绑定：
// 即使分站总开关关闭，仍拒绝既有分站客户（site_id > 0）和分站员工（staff_site_id > 0）在主站登录，
// 同时对普通主站用户保持零额外 SQL 查询。
func AdtySiteLoginAllowedForUser(c *gin.Context, user *model.User) bool {
	if user == nil || user.Id <= 0 {
		return false
	}
	if !adtySessionHostAllowed(c) {
		return false
	}
	if !resellerSetting().Enabled {
		return user.SiteId == 0 && user.StaffSiteId == 0
	}
	return AdtySiteLoginAllowed(c, user.Id)
}

// AdtySiteLoginAllowed 按 A.15 / DEC-023 始终强制站点域名登录绑定：
// 主站用户只能在主站登录，分站客户与分站员工只能在所属分站登录，站长可在主站或本人服务中分站登录。
// 只在建会话的入口调用（密码、二次验证、Passkey），API 令牌不受影响。
// 分站功能关闭时仍拒绝已有分站归属（site_id > 0 或 staff_site_id > 0）的账号在主站登录。
func AdtySiteLoginAllowed(c *gin.Context, userId int) bool {
	if !adtySessionHostAllowed(c) {
		return false
	}
	cfg := resellerSetting()
	var users []model.User
	if err := model.DB.Select("id", "site_id", "staff_site_id").Where("id = ?", userId).Limit(1).Find(&users).Error; err != nil || len(users) == 0 {
		return false
	}
	if !cfg.Enabled {
		return users[0].SiteId == 0 && users[0].StaffSiteId == 0
	}
	current := 0
	var reqSite *model.AdtySite
	if site := AdtySiteForRequest(c); site != nil {
		reqSite = site
		current = site.Id
	}
	if users[0].StaffSiteId > 0 {
		if current != users[0].StaffSiteId {
			return false
		}
		var staffSite model.AdtySite
		if err := model.DB.Select("id", "owner_user_id", "status").First(&staffSite, users[0].StaffSiteId).Error; err != nil || !AdtySiteServesCustomers(&staffSite) {
			return false
		}
		var owner model.User
		if err := model.DB.Select("id", "status", "staff_site_id").First(&owner, staffSite.OwnerUserId).Error; err != nil || owner.Status != common.UserStatusEnabled || owner.StaffSiteId != 0 {
			return false
		}
		var staffCount int64
		if err := model.DB.Model(&model.AdtySiteStaff{}).Where("user_id = ? AND site_id = ? AND owner_user_id = ? AND status = ?", userId, staffSite.Id, staffSite.OwnerUserId, "enabled").Count(&staffCount).Error; err != nil || staffCount == 0 {
			return false
		}
		return users[0].SiteId == current
	}
	if reqSite != nil && users[0].SiteId == 0 && reqSite.OwnerUserId == userId && AdtySiteServesCustomers(reqSite) {
		return true
	}
	return users[0].SiteId == current
}

// adtySessionHostAllowed 拒绝在已关停分站的主机上建立会话。自定义域名和平台子域名一样，
// 站点还在服务时都可以登录和二次验证；站点关掉后两种主机都拒绝。
// 每次校验权威数据库状态，防止多实例旧缓存放行已关站登录，DB 异常 fail-closed。
func adtySessionHostAllowed(c *gin.Context) bool {
	if c == nil || c.Request == nil || !resellerSetting().Enabled {
		return true
	}
	host := AdtyNormalizeHost(c.Request.Host)
	if host == "" || AdtyIsMainHost(host) {
		return true
	}
	site, err := AdtySiteForHostWithError(host)
	if err != nil || site == nil {
		return false
	}
	if !AdtySiteServesCustomers(site) {
		return false
	}
	// 权威查库复验分站状态，避免多实例旧缓存在另一实例关站后短暂放行登录。
	if model.DB != nil {
		var fresh model.AdtySite
		if err := model.DB.Select("id", "status").Where("id = ?", site.Id).First(&fresh).Error; err != nil {
			return false
		}
		if !AdtySiteServesCustomers(&fresh) {
			InvalidateAdtySiteCache()
			return false
		}
	}
	return true
}

// AdtyRegisterSiteId 决定新注册用户归哪个站（11.2）：当前站点 active 或 paused 时归这个站；
// 主站返回 0。closing 的站已经通知客户要关了，不再接受注册（ok 为 false）。
func AdtyRegisterSiteId(c *gin.Context) (siteId int, ok bool) {
	site := AdtySiteForRequest(c)
	if site == nil {
		return 0, true
	}
	switch site.Status {
	case model.AdtySiteStatusActive, model.AdtySiteStatusPaused:
		return site.Id, true
	}
	return 0, false
}

// AdtyMainLegalConsentRequired is true when the main site has published an
// agreement or privacy policy that registration must accept and record.
func AdtyMainLegalConsentRequired() bool {
	return AdtyMainLegalDocEnabled("user-agreement") || AdtyMainLegalDocEnabled("privacy-policy")
}

// AdtyRegisterTermsAccepted always returns true after R-32 (#9) cancelled
// subsite agreements and subsite customer terms.
func AdtyRegisterTermsAccepted(c *gin.Context, siteId int) bool {
	return true
}

func adtyRequestSiteTermsVersion(c *gin.Context) string {
	return strings.TrimSpace(c.Query("site_terms_version"))
}

// AdtyAcceptedSiteCustomerTerms returns empty string after R-32 (#9) cancelled
// subsite customer terms.
func AdtyAcceptedSiteCustomerTerms(c *gin.Context) string {
	return ""
}

// AdtyCheckSiteCustomerRisk 由 11.5 注册：注册时按指纹判断疑似自用、疑似主站迁移，返回风控标记和原因。
var AdtyCheckSiteCustomerRisk func(siteId, userId int, ipHash, deviceHash string) (flag, reason string)

// AdtyRecordSiteCustomerTx 在事务内原子写入或恢复分站客户关系（与 users.site_id 及客户条款版本一致）。
// 对同一 (userId, siteId) 幂等，支持注册事务内直接调用或事后补偿恢复。
func AdtyRecordSiteCustomerTx(tx *gorm.DB, userId, siteId int, ipHash, deviceHash, termsVersion string) error {
	var riskFlag, riskReason string
	if AdtyCheckSiteCustomerRisk != nil && (ipHash != "" || deviceHash != "") {
		riskFlag, riskReason = AdtyCheckSiteCustomerRisk(siteId, userId, ipHash, deviceHash)
	}
	return adtyRecordSiteCustomerWithRiskTx(tx, userId, siteId, termsVersion, riskFlag, riskReason)
}

func adtyRecordSiteCustomerWithRiskTx(tx *gorm.DB, userId, siteId int, termsVersion, riskFlag, riskReason string) error {
	if tx == nil {
		tx = model.DB
	}
	if tx == nil || userId <= 0 || siteId <= 0 {
		return common.NewMessage("Invalid parameters")
	}
	now := time.Now().Unix()
	termsVersion = strings.TrimSpace(termsVersion)
	var existing []model.AdtySiteCustomer
	if err := model.AdtyLockForUpdate(tx).Where("user_id = ?", userId).Limit(1).Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) > 0 {
		row := existing[0]
		if row.SiteId != siteId && row.Status != model.AdtySiteCustomerArchived {
			return common.NewMessage("Customer already belongs to another site")
		}
		updates := map[string]any{
			"site_id":     siteId,
			"status":      "active",
			"archived_at": 0,
		}
		if row.JoinedAt <= 0 {
			updates["joined_at"] = now
		}
		if termsVersion != "" && row.TermsVersion != termsVersion {
			updates["terms_version"] = termsVersion
			updates["terms_at"] = now
		}
		if riskFlag != "" && row.RiskFlag == "" {
			updates["risk_flag"] = riskFlag
			updates["risk_reason"] = riskReason
		}
		return tx.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", row.UserId).Updates(updates).Error
	}
	customer := model.AdtySiteCustomer{
		UserId:       userId,
		SiteId:       siteId,
		Status:       "active",
		JoinedAt:     now,
		TermsVersion: termsVersion,
		RiskFlag:     riskFlag,
		RiskReason:   riskReason,
	}
	if termsVersion != "" {
		customer.TermsAt = now
	}
	return tx.Create(&customer).Error
}

// AdtyEnsureSiteCustomer 确保分站账号对应的 AdtySiteCustomer 关系持久落库；
// 若写入失败，留下可追踪的官方审计记录并返回错误，此时 AdtySiteTermsStatus 保持 fail-closed 拦截。
func AdtyEnsureSiteCustomer(userId, siteId int, ipHash, deviceHash, termsVersion string) error {
	if userId <= 0 || siteId <= 0 || model.DB == nil {
		return common.NewMessage("Invalid parameters")
	}
	var riskFlag, riskReason string
	if AdtyCheckSiteCustomerRisk != nil && (ipHash != "" || deviceHash != "") {
		riskFlag, riskReason = AdtyCheckSiteCustomerRisk(siteId, userId, ipHash, deviceHash)
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return adtyRecordSiteCustomerWithRiskTx(tx, userId, siteId, termsVersion, riskFlag, riskReason)
	})
	if err != nil {
		sID := siteId
		username := ""
		actorRole := common.RoleCommonUser
		if u, uErr := model.GetUserById(userId, false); uErr == nil && u != nil {
			username = u.Username
			if u.Role > 0 {
				actorRole = u.Role
			}
		}
		if auditErr := model.WriteDurableOfficialAuditLog(nil, model.OfficialAuditWrite{
			UserId:     userId,
			Username:   username,
			ActorRole:  actorRole,
			SiteID:     &sID,
			ObjectType: "site_customer",
			ObjectID:   fmt.Sprintf("%d:%d", siteId, userId),
			Action:     "adty.site.customer_relation_recovery_needed",
			Category:   model.AuditCategoryOperation,
			Content:    "site customer relation persistence failed; account remains terms-gated until recovered",
			After: map[string]any{
				"user_id":       userId,
				"site_id":       siteId,
				"terms_version": strings.TrimSpace(termsVersion),
				"error":         err.Error(),
			},
			Result:  model.AuditResultFailure,
			Success: false,
		}); auditErr != nil {
			common.SysLog(common.LogText("site customer relation recovery audit write failed: %s", auditErr.Error()))
		}
		return err
	}
	return nil
}

// AdtyRecordSiteCustomer 在分站注册后写客户行（和 users.site_id 同步）。
// 若写入失败，记错误日志与可追踪审计，且缺失客户关系在 AdtySiteTermsStatus 中保持拦截，绝不当作免签放行。
func AdtyRecordSiteCustomer(userId, siteId int, ipHash, deviceHash, termsVersion string) {
	if err := AdtyEnsureSiteCustomer(userId, siteId, ipHash, deviceHash, termsVersion); err != nil {
		common.SysError(common.LogText("record site customer failed user=%d site=%d: %s", userId, siteId, err.Error()))
	}
}
