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
	require.NoError(t, conn.AutoMigrate(&model.AdtySite{}, &model.AdtySiteOwnership{}, &model.AdtySiteEarningDaily{}, &model.User{}, &model.AdtyTopupOrder{}, &model.TopUp{}, &model.Option{}, &model.Log{}, &model.AuditIntent{}, &model.AuditLog{}))
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
		if err := common.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Errorf("decode settings response: %v", err)
			return false
		}
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
	// A dirty draft can change an unrelated option without undoing a concurrent edit.
	_, observed, err := resellerSettingsSnapshot(conn)
	require.NoError(t, err)
	oldVersions, err := common.Marshal(observed)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan bool, 2)
	for _, patch := range []string{`{"root_domains":["first.example"],"expected_versions":%s}`, `{"close_notice_days":8,"expected_versions":%s}`} {
		go func(body string) {
			<-start
			results <- save(body)
		}(fmt.Sprintf(patch, oldVersions))
	}
	close(start)
	first, second := <-results, <-results
	require.True(t, first && second)
	require.False(t, save(fmt.Sprintf(`{"root_domains":["stale.example"],"expected_versions":%s}`, oldVersions)))
	current, _, err := resellerSettingsSnapshot(conn)
	require.NoError(t, err)
	require.Equal(t, []string{"first.example"}, current.RootDomains)
	require.Equal(t, 8, current.CloseNoticeDays)
	sites := make([]model.AdtySite, 5001)
	for i := range sites {
		sites[i] = model.AdtySite{OwnerUserId: i + 1, Subdomain: fmt.Sprintf("shop-%04d", i), RootDomain: "sites.example.test", Status: model.AdtySiteStatusActive}
	}
	require.NoError(t, conn.CreateInBatches(&sites, 100).Error)
	require.NoError(t, conn.Create(&model.AdtySiteEarningDaily{SiteId: sites[0].Id, Kind: model.AdtySiteEarningUsage, Day: time.Now().Format("2006-01-02"), Earning: decimal.NewFromInt(50), Status: model.AdtySiteEarningAvailable}).Error)
	archived := model.AdtySite{OwnerUserId: 1, Subdomain: "archived-store", RootDomain: "sites.example.test", Status: model.AdtySiteStatusClosed}
	require.NoError(t, conn.Create(&archived).Error)
	for _, userID := range []int{1, 2} {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Set("id", userID)
		ctx.Request = httptest.NewRequest("GET", fmt.Sprintf("/api/adty/site/earnings?archived_site_id=%d", archived.Id), nil)
		historical, allowed := adtyOwnerAccountingSite(ctx)
		require.Equal(t, userID == 1, allowed)
		if allowed {
			require.Equal(t, archived.Id, historical.Id)
		} else {
			require.Equal(t, 403, w.Code)
		}
	}
	t.Run("historical accounting only returns personal rows and counts", func(t *testing.T) {
		require.NoError(t, conn.AutoMigrate(&model.AdtySiteEarningLine{}, &model.AdtySiteWallet{}, &model.AdtySiteWalletLedger{}, &model.AdtySiteWithdrawal{}, &model.AdtySiteSettlementSheet{}, &model.AdtyVoucher{}, &model.BillingOperation{}, &model.AdtyManualRefund{}, &model.AdtySiteInviteWithdrawal{}, &model.ReferralWithdrawal{}, &model.AdtySiteInviteCommission{}, &model.ReferralCommission{}, &model.AdtyStaffBalanceRequest{}, &model.AdtyBalanceLedger{}))
		require.NoError(t, conn.Create(&model.AdtySiteOwnership{SiteId: archived.Id, OldOwnerUserId: 2, NewOwnerUserId: 1, Version: 1}).Error)
		require.NoError(t, conn.Create(&model.AdtySiteSettlementSheet{SiteId: archived.Id, BusinessDay: "2026-11-12", BatchId: "mixed-sheet", ConsumptionUSD: decimal.NewFromInt(8800)}).Error)
		for owner, customer := range map[int]int{0: 9801, 2: 9802, 1: 9803} {
			require.NoError(t, conn.Create(&model.AdtySiteWithdrawal{SiteId: archived.Id, OwnerUserId: owner, IdempotencyKey: fmt.Sprint("archived-", owner), Status: model.AdtySiteWithdrawCancelled}).Error)
			require.NoError(t, conn.Create(&model.AdtySiteEarningDaily{SiteId: archived.Id, SiteOwnerUserId: owner, UserId: customer, Kind: model.AdtySiteEarningUsage, Day: "2026-11-12", Status: model.AdtySiteEarningPending, Earning: decimal.NewFromInt(3)}).Error)
			require.NoError(t, conn.Create(&model.AdtySiteEarningLine{SiteId: archived.Id, SiteOwnerUserId: owner, UserId: customer, ModelName: "model"}).Error)
		}
		for _, owner := range []int{1, 2, 3} {
			get := func(h gin.HandlerFunc) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(w)
				ctx.Set("id", owner)
				ctx.Request = httptest.NewRequest("GET", fmt.Sprintf("/?archived_site_id=%d", archived.Id), nil)
				h(ctx)
				return w
			}
			w := get(GetAdtyOwnerEarnings)
			if owner == 3 {
				require.Equal(t, 403, w.Code)
				require.Equal(t, 403, get(GetAdtyOwnerEarningLines).Code)
				require.Equal(t, 403, get(GetAdtyOwnerSettlementSheets).Code)
				continue
			}
			var result struct {
				Success bool
				Data    struct {
					Balances struct {
						Pending string `json:"pending_usd"`
					}
					Page struct {
						Total int
						Items []adtyOwnerEarningRow
					}
				}
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
			require.True(t, result.Success, w.Body.String())
			want := map[int][]int{1: {9803}, 2: {9802, 9801}}[owner]
			require.Equal(t, len(want), result.Data.Page.Total)
			require.Len(t, result.Data.Page.Items, len(want))
			for _, item := range result.Data.Page.Items {
				require.Contains(t, want, item.UserId)
			}
			require.Equal(t, decimal.NewFromInt(int64(len(want)*3)).String(), result.Data.Balances.Pending)
			var lines struct {
				Success bool
				Data    []model.AdtySiteEarningLine
			}
			w = get(GetAdtyOwnerEarningLines)
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &lines))
			require.True(t, lines.Success, w.Body.String())
			require.Len(t, lines.Data, len(want))
			for _, item := range lines.Data {
				require.Contains(t, want, item.UserId)
			}
			w = get(func(ctx *gin.Context) { getAdtyUnifiedOrders(ctx, &archived.Id, nil) })
			var orders struct {
				Success bool
				Data    struct {
					Total int
					Items []rechargeAuditOrder
				}
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &orders))
			require.True(t, orders.Success, w.Body.String())
			require.Equal(t, len(want), orders.Data.Total)
			require.Len(t, orders.Data.Items, len(want))
			for _, item := range orders.Data.Items {
				require.True(t, item.UserId == owner || owner == 2 && item.UserId == 0)
			}
			w = get(GetAdtyOwnerSettlementSheets)
			require.Contains(t, w.Body.String(), "Contact the platform")
			require.NotContains(t, w.Body.String(), "8800")
		}
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Set("role", common.RoleRootUser)
		ctx.Request = httptest.NewRequest("GET", fmt.Sprintf("/?site_id=%d", archived.Id), nil)
		GetAdtyAdminSettlementSheets(ctx)
		require.Contains(t, w.Body.String(), "8800")
	})
	require.NoError(t, conn.Delete(&archived).Error)
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
