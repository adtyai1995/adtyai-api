package adty_setting

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDomainAutomationRequiresScopedConfiguration(t *testing.T) {
	cfg := DefaultResellerSetting()
	cfg.DomainAutomationEnabled = true
	require.Error(t, ValidateResellerSetting(&cfg, false))
	cfg.CloudflareApiToken, cfg.CloudflareZoneId, cfg.CnameTarget = "token", "zone", "edge.example.com"
	cfg.FallbackOrigin = "origin.example.com"
	cfg.RootDomains = []string{"sites.example.com"}
	cfg.AliyunDomain = "sites.example.com"
	cfg.AliyunAccessKeyID, cfg.AliyunAccessKeySecret = "id", "secret"
	cfg.DomainMainlandTarget = "fast.example.com"
	require.NoError(t, ValidateResellerSetting(&cfg, false))
	cfg.AliyunDomain = "other.example.com"
	require.Error(t, ValidateResellerSetting(&cfg, false))
}
