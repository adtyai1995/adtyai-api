package service

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
)

// Turnstile 和自定义域名（任务 11.8）。免费版每个 widget 最多 10 个域名、每个账号最多 20 个 widget。
// 主站域名和每个 root_domain 登记在 turnstile_widgets 的第一个里（一个域名连带它的子域名一起授权，
// 平台子域名不用逐个登记，部署时在 Cloudflare 后台配好）；每个 active 的自定义域名登记进还有空位的 widget。
// widget 的 PUT 是整体替换，先 GET 再原样带回 mode、name、region 等；PUT 的响应带 secret，不解析、不打日志。

// adtyTurnstileVerifyURL 是 siteverify 的地址，测试里换成假服务器。
var adtyTurnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// adtyTurnstileHTTP 调 siteverify 用，官方示例超时 10 秒，禁重定向并独立校验 TLS（R4-002）。
var adtyTurnstileHTTP = NewVerifiedDirectNoRedirectHTTPClient(10 * time.Second)

const adtyTurnstileMaxTokenLength = 2048

// adtyTurnstileWidget 是 widget 详情里要原样带回 PUT 的字段。
type adtyTurnstileWidget struct {
	Sitekey        string   `json:"sitekey"`
	Name           string   `json:"name"`
	Domains        []string `json:"domains"`
	Mode           string   `json:"mode"`
	Region         string   `json:"region,omitempty"`
	BotFightMode   *bool    `json:"bot_fight_mode,omitempty"`
	ClearanceLevel string   `json:"clearance_level,omitempty"`
	Offlabel       *bool    `json:"offlabel,omitempty"`
	EphemeralId    *bool    `json:"ephemeral_id,omitempty"`
}

func adtyWidgetPath(account, widgetId string) string {
	return "/accounts/" + url.PathEscape(account) + "/challenges/widgets/" + url.PathEscape(widgetId)
}

func (c *AdtyCloudflareClient) getTurnstileWidget(ctx context.Context, account, widgetId string) (*adtyTurnstileWidget, error) {
	var out adtyTurnstileWidget
	err := c.do(ctx, http.MethodGet, adtyWidgetPath(account, widgetId), nil, &out)
	return &out, err
}

// putTurnstileWidget 整体替换 widget。响应里带 secret，所以 out 传 nil，不解析响应。
func (c *AdtyCloudflareClient) putTurnstileWidget(ctx context.Context, account, widgetId string, widget *adtyTurnstileWidget) error {
	body := map[string]any{"domains": widget.Domains, "mode": widget.Mode, "name": widget.Name}
	if widget.Region != "" {
		body["region"] = widget.Region
	}
	if widget.BotFightMode != nil {
		body["bot_fight_mode"] = *widget.BotFightMode
	}
	if widget.ClearanceLevel != "" {
		body["clearance_level"] = widget.ClearanceLevel
	}
	if widget.Offlabel != nil {
		body["offlabel"] = *widget.Offlabel
	}
	if widget.EphemeralId != nil {
		body["ephemeral_id"] = *widget.EphemeralId
	}
	return c.do(ctx, http.MethodPut, adtyWidgetPath(account, widgetId), body, nil)
}

// adtyTurnstileAccountLocks serializes Cloudflare Turnstile widget GET+PUT
// read-modify-write mutations per Cloudflare account in-process without holding
// any database transaction or options row lock across external HTTP calls (R4-16).
// Canonical in-process lock order: adtyDomainLocks -> adtyTurnstileAccountLocks.
var adtyTurnstileAccountLocks = newAdtyKeyedLocker[string]()

// adtyTurnstileAssign 把一个自定义域名登记进还有空位的 widget，返回 widget id。名额全满时返回空串
// （这个域名照常能用，人机验证改在平台子域名上做）。已经在某个 widget 里的直接返回它。
func adtyTurnstileAssign(ctx context.Context, hostname string) (string, error) {
	cfg := resellerSetting()
	account := strings.TrimSpace(cfg.CloudflareAccountId)
	if account == "" || len(cfg.TurnstileWidgets) == 0 {
		// **配置缺失要喊一声，名额满要静默。** 这两种情形以前共用这一条 return，
		// 于是「没配好」和「没名额」在日志里完全同形，而前者是配置缺陷、后者是
		// 正常降级（域名照常能用，验证改在平台子域名上做）。
		//
		// 只喊缺配的那一半，而且带上缺的是哪一个：
		//   - account 为空 → Turnstile 那一侧根本没接通，每一轮都会落到这里；
		//   - widget 为空 → 同上。
		// 这条日志每 5 分钟回轮一次，**所以它必须点名缺的字段**，否则读的人只看
		// 到「又一条」而不知道要补哪里。
		common.SysError(common.LogText(
			"turnstile assignment skipped for %q: %s; the domain still works but has no human verification",
			hostname, missingTurnstileConfig(cfg)))
		return "", nil
	}
	unlock := adtyTurnstileAccountLocks.Lock(account)
	defer unlock()
	for _, configured := range cfg.TurnstileWidgets {
		widget, err := AdtyCloudflare.getTurnstileWidget(ctx, account, configured.Id)
		if err != nil {
			return "", err
		}
		if slices.Contains(widget.Domains, hostname) {
			return configured.Id, nil
		}
		if len(widget.Domains) >= adtyTurnstileDomainsPerWidget {
			continue
		}
		widget.Domains = append(widget.Domains, hostname)
		if err := AdtyCloudflare.putTurnstileWidget(ctx, account, configured.Id, widget); err != nil {
			return "", err
		}
		return configured.Id, nil
	}
	return "", nil
}

// adtyTurnstileRemoveDomain 从 widget 的 domains 里去掉一个域名。
func adtyTurnstileRemoveDomain(ctx context.Context, widgetId, hostname string) error {
	cfg := resellerSetting()
	account := strings.TrimSpace(cfg.CloudflareAccountId)
	if account == "" {
		if strings.TrimSpace(widgetId) != "" {
			return common.NewMessage("Restore the Turnstile account to release the hostname")
		}
		return nil
	}
	unlock := adtyTurnstileAccountLocks.Lock(account)
	defer unlock()
	ids := []string{}
	if widgetId != "" {
		ids = append(ids, widgetId)
	}
	for _, configured := range cfg.TurnstileWidgets {
		if !slices.Contains(ids, configured.Id) {
			ids = append(ids, configured.Id)
		}
	}
	for _, id := range ids {
		widget, err := AdtyCloudflare.getTurnstileWidget(ctx, account, id)
		if err != nil {
			return err
		}
		if !slices.Contains(widget.Domains, hostname) {
			continue
		}
		widget.Domains = slices.DeleteFunc(widget.Domains, func(domain string) bool { return domain == hostname })
		if err := AdtyCloudflare.putTurnstileWidget(ctx, account, id, widget); err != nil {
			return err
		}
	}
	return nil
}

// AdtyTurnstileChoice 是当前请求用哪个 widget：SiteKey 给 /api/status，Secret 给 TurnstileCheck。
// 两个都为空时用上游的 TurnstileSiteKey、TurnstileSecretKey（主站）。
type AdtyTurnstileChoice struct {
	SiteKey string
	Secret  string
}

// AdtyTurnstileFor selects the widget bound to either kind of website.
// A legacy platform hostname without an explicit binding inherits the root-domain widget.
func AdtyTurnstileFor(site *model.AdtySite, requestHost string) AdtyTurnstileChoice {
	if site == nil {
		return AdtyTurnstileChoice{}
	}
	cfg := resellerSetting()
	if len(cfg.TurnstileWidgets) == 0 {
		return AdtyTurnstileChoice{}
	}
	widgetId := cfg.TurnstileWidgets[0].Id
	host := AdtyNormalizeHost(requestHost)
	var domain model.AdtySiteDomain
	if model.DB.Select("turnstile_widget").Where("hostname = ? AND site_id = ? AND role IN ? AND status = ?", host, site.Id, []string{"", model.AdtySiteDomainRoleSite, model.AdtySiteDomainRoleBaseURL}, model.AdtySiteDomainActive).Limit(1).Find(&domain).RowsAffected > 0 && domain.TurnstileWidget != "" {
		widgetId = domain.TurnstileWidget
	}
	for _, widget := range cfg.TurnstileWidgets {
		if widget.Id == widgetId {
			siteKey := strings.TrimSpace(widget.SiteKey)
			secret := strings.TrimSpace(cfg.TurnstileWidgetSecret[widget.Id])
			if siteKey == "" || secret == "" {
				return AdtyTurnstileChoice{}
			}
			return AdtyTurnstileChoice{SiteKey: siteKey, Secret: secret}
		}
	}
	return AdtyTurnstileChoice{}
}

// AdtyTurnstileVerify 调 siteverify。通过还要求返回的 hostname 是 expectHost（不看大小写）；
// token 超过 2048 字符直接拒，不发请求。网络错误返回 err，调用方按失败处理。
func AdtyTurnstileVerify(ctx context.Context, secret, token, remoteIP, expectHost string) (bool, error) {
	return AdtyTurnstileVerifyAt(ctx, adtyTurnstileVerifyURL, secret, token, remoteIP, expectHost)
}

// AdtyTurnstileVerifyAt 同 AdtyTurnstileVerify，siteverify 的地址由调用方给（middleware 的测试替换它）。
func AdtyTurnstileVerifyAt(ctx context.Context, verifyURL, secret, token, remoteIP, expectHost string) (bool, error) {
	if token == "" || len(token) > adtyTurnstileMaxTokenLength {
		return false, nil
	}
	form := url.Values{"secret": {secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, common.SanitizeOutboundError(err, secret)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := adtyTurnstileHTTP.Do(req)
	if err != nil {
		return false, common.SanitizeOutboundError(err, secret)
	}
	defer resp.Body.Close()
	var result struct {
		Success  bool   `json:"success"`
		Hostname string `json:"hostname"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, 1<<20), &result); err != nil {
		return false, err
	}
	if !result.Success {
		return false, nil
	}
	host := AdtyNormalizeHost(expectHost)
	return host == "" || strings.EqualFold(AdtyNormalizeHost(result.Hostname), host), nil
}

// missingTurnstileConfig 说出 Turnstile 登记缺的是哪一块。**只报缺项，不报「未配置」**——
// 后者读的人还得自己去翻设置页。
func missingTurnstileConfig(cfg *adty_setting.ResellerSetting) string {
	missing := make([]string, 0, 2)
	if strings.TrimSpace(cfg.CloudflareAccountId) == "" {
		missing = append(missing, "cloudflare_account_id is empty")
	}
	if len(cfg.TurnstileWidgets) == 0 {
		missing = append(missing, "no turnstile widgets configured")
	}
	return strings.Join(missing, " and ")
}
