package controller

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestResellerSettingsRollbackAndFullSiteRanking(t *testing.T) {
	dsn := os.Getenv("ADTY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL 18")
	}
	db, err := gorm.Open(postgres.Open(dsn))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	var version int
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, 18, version/10000)
	schema := fmt.Sprintf("site_ops_%d", time.Now().UnixNano())
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	defer db.Exec("DROP SCHEMA " + schema + " CASCADE")
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	conn, err := gorm.Open(postgres.Open(parsed.String()))
	require.NoError(t, err)
	scopedPool, err := conn.DB()
	require.NoError(t, err)
	defer scopedPool.Close()
	origDB, origLog, prior := model.DB, model.LOG_DB, *adty_setting.GetResellerSetting()
	origMap := common.OptionMap
	model.DB, model.LOG_DB, common.OptionMap = conn, conn, map[string]string{}
	defer func() {
		model.DB, model.LOG_DB, common.OptionMap = origDB, origLog, origMap
		*adty_setting.GetResellerSetting() = prior
	}()
	require.NoError(t, conn.AutoMigrate(&model.AdtySite{}, &model.AdtySiteEarningDaily{}, &model.User{}, &model.AdtyTopupOrder{}, &model.TopUp{}, &model.Option{}, &model.Log{}, &model.AuditIntent{}, &model.AuditLog{}))
	cfg := adty_setting.DefaultResellerSetting()
	cost := 0.8
	cfg.CostFactor = &cost
	*adty_setting.GetResellerSetting() = cfg
	key := adty_setting.ResellerSettingModule + ".cost_factor"
	require.NoError(t, conn.Create(&model.Option{Key: key, Value: "0.8"}).Error)
	save := func(body string) bool {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("role", common.RoleRootUser)
		c.Request = httptest.NewRequest("PUT", "/api/adty/admin/reseller/settings", strings.NewReader(body))
		UpdateAdtyResellerSettings(c)
		var resp struct {
			Success bool
			Message string
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp))
		t.Logf("save success=%t feedback=%s", resp.Success, resp.Message)
		return resp.Success
	}
	assertCost := func(want string) {
		var row model.Option
		require.NoError(t, conn.First(&row, "key = ?", key).Error)
		require.Equal(t, want, row.Value)
		require.Equal(t, want, decimal.NewFromFloat(*adty_setting.GetResellerSetting().CostFactor).String())
	}
	require.False(t, save(`{"cost_factor":0.6,"root_domains":["bad..domain"]}`))
	assertCost("0.8")
	require.NoError(t, conn.Exec("ALTER TABLE options ADD CONSTRAINT reject_cost CHECK (value <> '0.6')").Error)
	require.False(t, save(`{"cost_factor":0.6}`))
	assertCost("0.8")
	// A stale sender-domain dependency must not prevent a supply price edit.
	adty_setting.GetResellerSetting().SenderDomainEnabled = true
	require.True(t, save(`{"cost_factor":0.7}`))
	assertCost("0.7")
	sites := make([]model.AdtySite, 5001)
	for i := range sites {
		sites[i] = model.AdtySite{OwnerUserId: i + 1, Subdomain: fmt.Sprintf("shop-%04d", i), RootDomain: "sites.example.test", Status: model.AdtySiteStatusActive}
	}
	require.NoError(t, conn.CreateInBatches(&sites, 100).Error)
	require.NoError(t, conn.Create(&model.AdtySiteEarningDaily{SiteId: sites[0].Id, Kind: model.AdtySiteEarningUsage, Day: time.Now().Format("2006-01-02"), Earning: decimal.NewFromInt(50), Status: model.AdtySiteEarningAvailable}).Error)
	for _, query := range []string{"sort=earning&order=desc&p=1&page_size=1", "sort=id&order=desc&p=5001&page_size=1"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adty/admin/sites?"+query, nil)
		GetAdtyAdminSites(c)
		var resp struct {
			Success bool
			Data    struct {
				Total int
				Items []adtySiteAdminView
			}
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp))
		require.True(t, resp.Success, w.Body.String())
		require.Equal(t, 5001, resp.Data.Total)
		require.Len(t, resp.Data.Items, 1)
		require.Equal(t, sites[0].Id, resp.Data.Items[0].Id)
	}

}
