package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `0069` 的三条连锁后果全都在两个「总开关」上：这两处读 `CloudflareConfigured()`，
// 一旦它把 Turnstile 的字段算进来，开自定义域名但不用 Turnstile 的部署就会
// **静默停摆**——新增域名被关掉、定期检查任务每 5 分钟空转。
//
// 所以这里直接验这两处放行并真正推进待检查域名，而不是在空库上只验 `nil` 返回值。
func TestCloudflareGatesAllowDomainWithoutTurnstile(t *testing.T) {
	db, fake := prepareDomainDB(t)
	owner, _ := openActiveSite(t, db, "no-turnstile")

	cfg := adty_setting.GetResellerSetting()
	cfg.Enabled = true
	cfg.CustomDomainEnabled = true
	cfg.RootDomains = []string{"example.com"}
	cfg.CloudflareApiToken = "cf-token"
	cfg.CloudflareZoneId = "zone-1"
	cfg.CnameTarget = "edge.example.com"
	// 不给 account / widget：这个部署不用 Turnstile。
	cfg.CloudflareAccountId = ""
	cfg.TurnstileWidgets = nil
	cfg.TurnstileWidgetSecret = nil

	// ① 新增自定义域名不能被关掉，且能在无 Turnstile 配置下完成创建与挂载。
	domain, err := AddAdtySiteDomain(context.Background(), owner.Id, "shop.customer.org")
	require.NoError(t, err, "开自定义域名但不用 Turnstile 时，新增域名应正常通过门控")
	require.NotEmpty(t, domain.CFHostnameId)
	assert.Equal(t, model.AdtySiteDomainPending, domain.Status)

	// ② 定期检查任务不能因为「没配 Turnstile」而提前 return nil：
	// 待检查域名必须被查询并从 pending 推进到 active。
	fake.setStatus(domain.CFHostnameId, "active", "active")
	require.NoError(t, RunAdtySiteDomainCheckOnce(context.Background(), time.Now()))

	var stored model.AdtySiteDomain
	require.NoError(t, db.First(&stored, domain.Id).Error)
	assert.Equal(t, model.AdtySiteDomainActive, stored.Status, "轮询任务必须穿过总门控并把已就绪的 pending 域名推进到 active")
	assert.Empty(t, stored.TurnstileWidget)
}
