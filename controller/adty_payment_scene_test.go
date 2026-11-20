package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSiteBonusSaveUsesCurrentDatabaseAuthorization(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.AuditLog{}, &model.AuditIntent{}, &model.Log{}))
	require.NoError(t, model.MigrateAdtyTables())
	owner := model.User{Username: "bonus-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "bonus", RootDomain: "example.com", AgreementVer: service.AdtyCurrentSiteAgreementVersion()}
	require.NoError(t, db.Create(&site).Error)
	saved := *adty_setting.GetTopupSetting()
	oldOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { *adty_setting.GetTopupSetting() = saved; common.OptionMap = oldOptions })
	for _, allowed := range []bool{false, true} {
		rawAuth := `{}`
		if allowed {
			rawAuth = fmt.Sprintf(`{"%d":["usdt"]}`, site.Id)
		}
		require.NoError(t, db.Save(&model.Option{Key: adty_setting.TopupSettingKeySiteMethodAuth, Value: rawAuth}).Error)
		adty_setting.GetTopupSetting().SiteMethodAuth = map[int][]string{}
		if !allowed {
			adty_setting.GetTopupSetting().SiteMethodAuth[site.Id] = []string{"usdt"}
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/adty/site/bonus-methods", strings.NewReader(`{"method":"usdt","enabled":true,"rate":"0.05"}`))
		c.Set("id", owner.Id)
		c.Set("role", common.RoleCommonUser)
		PutAdtySiteMethodBonus(c)
		var result struct{ Success bool }
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
		require.Equal(t, allowed, result.Success, recorder.Body.String())
		var count int64
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", adty_setting.TopupSettingKeySiteMethodBonus).Count(&count).Error)
		if allowed {
			require.EqualValues(t, 1, count)
		} else {
			require.Zero(t, count)
		}
	}
}

func TestOwnerTopupQuoteUsesTrustedRequestSitePG(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}))
	require.NoError(t, model.MigrateAdtyTables())
	owner := model.User{Username: "owner-quote", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Email: "owner-quote@example.com"}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "quote", RootDomain: "example.com"}
	require.NoError(t, db.Create(&site).Error)
	sceneTestSetting(t, &common.RedisEnabled, false)
	sceneTestSetting(t, &operation_setting.GetGeneralSetting().QuotaDisplayType, operation_setting.QuotaDisplayTypeUSD)
	sceneTestSetting(t, &operation_setting.MinTopUp, 1)
	sceneTestSetting(t, setting.GetBEpusdtSetting(), setting.BEpusdtSetting{Enabled: true, BaseURL: "https://pay.example", Secret: "synthetic-secret", TradeTypes: []string{"usdt.trc20"}})
	payload, err := common.Marshal([]model.AdtyPaymentScene{{Key: "owner_checkout", Name: "Owner checkout", Business: "owner_self_use_topup", Enabled: true, Providers: []string{"bepusdt"}, SiteScope: "all_sites", UserTypes: []string{"site_owner"}}})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.AdtyPaymentSceneConfig{}).Where("id = 1").Update("payload", string(payload)).Error)
	for _, trusted := range []bool{true, false} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/topup/quote", strings.NewReader(`{"business":"owner_self_use_topup","payment_scene":"owner_checkout","amount":100,"method":"usdt.trc20","site_id":1}`))
		c.Set("id", owner.Id)
		c.Set("group", "default")
		if trusted {
			c.Set(service.AdtySiteContextKey, &site)
		}
		PostAdtyTopUpQuote(c)
		var response struct {
			Success bool
			Data    struct {
				Quote     service.AdtyQuote
				Available bool
			}
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, trusted, response.Success, recorder.Body.String())
		if trusted {
			require.EqualValues(t, 50000000, response.Data.Quote.CreditQuota)
			require.Equal(t, "100", response.Data.Quote.PayAmount.String())
			require.True(t, response.Data.Available)
		}
	}
}

func TestOwnerTopupHistoryBusinessFilter(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/adty/topup/self?business=owner_self_use_topup", nil)
	filter, err := parseAdtyTopUpSelfFilter(c)
	require.NoError(t, err)
	require.True(t, filter.Active())
	ordinary := model.AdtyTopUpView{TopUp: model.TopUp{UserId: 7}, Ext: &model.AdtyTopupOrder{Business: "wallet_topup"}}
	owner := model.AdtyTopUpView{TopUp: model.TopUp{UserId: 7}, Ext: &model.AdtyTopupOrder{Business: "owner_self_use_topup"}}
	require.False(t, matchAdtyTopUpSelfFilter(7, ordinary, filter))
	require.True(t, matchAdtyTopUpSelfFilter(7, owner, filter))
	filter.OwnerSiteID = 12
	owner.Ext.SiteId = 11
	require.False(t, matchAdtyTopUpSelfFilter(7, owner, filter))
	owner.Ext.SiteId = 12
	require.True(t, matchAdtyTopUpSelfFilter(7, owner, filter))
}

func sceneTestSetting[T any](t *testing.T, target *T, value T) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

func callPaymentHandler(t *testing.T, handler gin.HandlerFunc, userID int, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/pay", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", userID)
	handler(ctx)
	return recorder
}

func TestPaymentScenesConstrainNativeProviderEntries(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}))
	require.NoError(t, model.MigrateAdtyTables())
	user := model.User{Username: "scene-native", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Email: "scene@example.com"}
	require.NoError(t, db.Create(&user).Error)
	sceneTestSetting(t, &common.RedisEnabled, false)
	sceneTestSetting(t, &operation_setting.GetGeneralSetting().QuotaDisplayType, operation_setting.QuotaDisplayTypeUSD)
	sceneTestSetting(t, &common.QuotaPerUnit, 500000.0)
	sceneTestSetting(t, &operation_setting.MinTopUp, 1)
	sceneTestSetting(t, setting.GetBEpusdtSetting(), setting.BEpusdtSetting{Enabled: true, BaseURL: "https://scene.example", Secret: "synthetic-scene-secret", TradeTypes: []string{"usdt.trc20"}})
	externalCalls := 0
	sceneTestSetting[http.RoundTripper](t, &http.DefaultTransport, emailHTTPTransport(func(*http.Request) (*http.Response, error) {
		externalCalls++
		return nil, errors.New("a denied scene must not call a provider")
	}))
	policy := []model.AdtyPaymentScene{{Key: "restricted", Name: "Restricted", Business: "wallet_topup", Enabled: true, Providers: []string{"bepusdt"}, SiteScope: "all", MinUSD: decimal.Zero, MaxUSD: decimal.NewFromInt(50)}}
	payload, err := common.Marshal(policy)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.AdtyPaymentSceneConfig{}).Where("id = ?", 1).Updates(map[string]any{"payload": string(payload), "version": 2}).Error)

	provider := service.AdtyPaymentProviderForMethod("usdt.trc20")
	require.NotNil(t, provider)
	require.True(t, provider.AvailableFor("usdt.trc20", &service.AdtyQuote{FaceUSD: decimal.NewFromInt(100), PayAmount: decimal.NewFromInt(100)}), "provider must be configured; the scene is what rejects this request")
	response := callPaymentHandler(t, RequestBEpusdtPay, user.Id, `{"amount":100,"chain":"usdt.trc20","payment_scene":"restricted"}`)
	require.NotContains(t, response.Body.String(), `"success":true`)
	var body struct {
		Success bool
		Message string
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.False(t, body.Success)
	require.NotEmpty(t, body.Message)
	var count int64
	require.NoError(t, db.Model(&model.TopUp{}).Count(&count).Error)
	require.Zero(t, count, "a denied native call cannot create orders")
	require.Zero(t, externalCalls)
}
