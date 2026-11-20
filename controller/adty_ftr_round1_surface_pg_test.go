package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	ftrSurfaceOneUSDQuota   = 500000
	ftrSurfacePermanentGift = 250000
	ftrSurfaceExpiringGift  = 100000
	ftrUpstreamAgreement    = "FTR-UPSTREAM-AGREEMENT"
	ftrUpstreamPrivacy      = "FTR-UPSTREAM-PRIVACY"
)

func ftrSurfaceHTTP(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
}

func ftrSurfaceWalletDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.AdtySite{},
		&model.AdtyFundBatch{},
		&model.AdtyBalanceLedger{},
	))
}

func ftrSurfaceUser(t *testing.T, db *gorm.DB, name string, quota int) *model.User {
	t.Helper()
	user := &model.User{
		Username: name, Password: "hashed-password", Email: name + "@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
		AuthVersion: 1, Quota: quota,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func ftrAssertQuotaUnchanged(t *testing.T, db *gorm.DB, userID, quota int) {
	t.Helper()
	var stored model.User
	require.NoError(t, db.Unscoped().First(&stored, userID).Error)
	assert.Equal(t, quota, stored.Quota)
	assert.Zero(t, stored.QuotaPending)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ?", userID).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func ftrAssertLotSplit(t *testing.T, proj service.WalletLotProjection, expiresAt int64) {
	t.Helper()
	assert.Equal(t, service.WalletLotProjectionSourceBatches, proj.Source)
	assert.EqualValues(t, ftrSurfaceOneUSDQuota, proj.PrincipalQuota)
	assert.EqualValues(t, ftrSurfacePermanentGift, proj.BonusQuota)
	assert.EqualValues(t, ftrSurfaceExpiringGift, proj.ExpiringBonusQuota)
	assert.EqualValues(t, ftrSurfaceOneUSDQuota+ftrSurfacePermanentGift+ftrSurfaceExpiringGift, proj.TotalQuota)
	assert.Equal(t, expiresAt, proj.NextExpiresAt)
	assert.True(t, proj.PrincipalUSD.Equal(decimal.NewFromInt(1)))
	assert.True(t, proj.BonusUSD.Equal(decimal.RequireFromString("0.5")))
	assert.True(t, proj.ExpiringBonusUSD.Equal(decimal.RequireFromString("0.2")))
	assert.True(t, proj.TotalUSD.Equal(decimal.RequireFromString("1.7")))
}

type ftrLegalFixture struct {
	agreement     map[string]string
	privacy       map[string]string
	agreementJSON string
	privacyJSON   string
}

func ftrPersistLegalTexts(t *testing.T, db *gorm.DB) ftrLegalFixture {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	oldUpstream := *system_setting.GetLegalSettings()
	oldAdty := *adty_setting.GetLegalSetting()
	common.OptionMapRWMutex.Lock()
	previousMap := common.OptionMap
	nextMap := make(map[string]string, len(previousMap))
	for key, value := range previousMap {
		nextMap[key] = value
	}
	common.OptionMap = nextMap
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		*system_setting.GetLegalSettings() = oldUpstream
		*adty_setting.GetLegalSetting() = oldAdty
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.OptionMapRWMutex.Unlock()
	})
	*system_setting.GetLegalSettings() = system_setting.LegalSettings{}
	*adty_setting.GetLegalSetting() = adty_setting.LegalSetting{}

	fixture := ftrLegalFixture{
		agreement: map[string]string{
			"zh":    "FTR-0105-zh-agreement",
			"zh-CN": "FTR-0105-zh-CN-agreement",
			"en":    "FTR-0105-en-agreement",
		},
		privacy: map[string]string{
			"zh":    "FTR-0106-zh-privacy",
			"zh-CN": "FTR-0106-zh-CN-privacy",
			"en":    "FTR-0106-en-privacy",
		},
	}
	agreementJSON, err := common.Marshal(fixture.agreement)
	require.NoError(t, err)
	privacyJSON, err := common.Marshal(fixture.privacy)
	require.NoError(t, err)
	fixture.agreementJSON = string(agreementJSON)
	fixture.privacyJSON = string(privacyJSON)

	require.NoError(t, model.UpdateOption("legal.user_agreement", ftrUpstreamAgreement))
	require.NoError(t, model.UpdateOption("legal.privacy_policy", ftrUpstreamPrivacy))
	require.NoError(t, model.UpdateOption("adty_legal_setting.user_agreement", fixture.agreementJSON))
	require.NoError(t, model.UpdateOption("adty_legal_setting.privacy_policy", fixture.privacyJSON))
	assert.Equal(t, fixture.agreement, adty_setting.GetLegalSetting().UserAgreement)
	assert.Equal(t, fixture.privacy, adty_setting.GetLegalSetting().PrivacyPolicy)
	assert.Equal(t, ftrUpstreamAgreement, system_setting.GetLegalSettings().UserAgreement)
	assert.Equal(t, ftrUpstreamPrivacy, system_setting.GetLegalSettings().PrivacyPolicy)
	return fixture
}

func ftrAssertStoredOption(t *testing.T, db *gorm.DB, key, value string) {
	t.Helper()
	var stored model.Option
	require.NoError(t, db.Where(&model.Option{Key: key}).First(&stored).Error)
	assert.Equal(t, key, stored.Key)
	assert.Equal(t, value, stored.Value)
}

func ftrAssertLegalDoc(t *testing.T, doc string, texts map[string]string, forbidden ...string) {
	t.Helper()
	for _, lang := range []string{"zh", "en", "zh-CN"} {
		code, body := getAdtyLegal(t, doc, lang)
		require.Equal(t, http.StatusOK, code, lang)
		assert.Equal(t, texts[lang], body, lang)
		for _, other := range forbidden {
			assert.NotContains(t, body, other, lang)
		}
	}
}

// TestFTRADTY0301SelfUSDBalancePG checks the account balance GetSelf actually returns.
// The handler exposes integer quota. 500000 quota is 1 USD at the fixed quota-per-unit.
func TestFTRADTY0301SelfUSDBalancePG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	ftrSurfaceHTTP(t)
	ftrSurfaceWalletDB(t, db)
	user := ftrSurfaceUser(t, db, "ftr0301", ftrSurfaceOneUSDQuota)

	ok, body, data := getSelfJSON(t, user)
	require.True(t, ok, body)
	quotaValue, isNumber := data["quota"].(float64)
	require.True(t, isNumber, body)
	assert.Equal(t, float64(ftrSurfaceOneUSDQuota), quotaValue)
	assert.Contains(t, body, `"quota":500000`)
	assert.True(t, common.QuotaPerUnitDecimal().Equal(decimal.NewFromInt(ftrSurfaceOneUSDQuota)))
	assert.True(t, service.QuotaToUSD(int64(quotaValue)).Equal(decimal.NewFromInt(1)))
	_, hasProjection := data["wallet_lot_projection"]
	assert.False(t, hasProjection)
	ftrAssertQuotaUnchanged(t, db, user.Id, ftrSurfaceOneUSDQuota)
}

func TestFTRADTY0302WalletLotSplitPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	ftrSurfaceHTTP(t)
	ftrSurfaceWalletDB(t, db)
	const expiresAt int64 = 1893456000
	total := ftrSurfaceOneUSDQuota + ftrSurfacePermanentGift + ftrSurfaceExpiringGift
	user := ftrSurfaceUser(t, db, "ftr0302", total)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := model.CreateAdtyFundBatchesTx(tx, user.Id, "ftr0302-paid", ftrSurfaceOneUSDQuota, ftrSurfacePermanentGift); err != nil {
			return err
		}
		_, err := model.CreateAdtyExpiringBonusBatchTx(tx, user.Id, nil, "ftr0302-expiring", ftrSurfaceExpiringGift, expiresAt)
		return err
	}))

	var rows []model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ?", user.Id).Find(&rows).Error)
	require.Len(t, rows, 3)
	byKey := map[string]model.AdtyFundBatch{}
	for _, row := range rows {
		byKey[row.Source+"/"+row.Kind] = row
	}
	principal := byKey["ftr0302-paid/"+model.AdtyFundBatchKindPrincipal]
	permanent := byKey["ftr0302-paid/"+model.AdtyFundBatchKindBonus]
	expiring := byKey["ftr0302-expiring/"+model.AdtyFundBatchKindBonus]
	assert.EqualValues(t, ftrSurfaceOneUSDQuota, principal.Remaining)
	assert.Equal(t, model.AdtyGiftCategoryPermanent, permanent.GiftCategory)
	assert.False(t, permanent.IsExpiringGift())
	assert.EqualValues(t, ftrSurfacePermanentGift, permanent.Remaining)
	assert.Equal(t, model.AdtyGiftCategoryExpiring, expiring.GiftCategory)
	assert.True(t, expiring.IsExpiringGift())
	require.NotNil(t, expiring.ExpiresAt)
	assert.Equal(t, expiresAt, *expiring.ExpiresAt)
	assert.EqualValues(t, ftrSurfaceExpiringGift, expiring.Remaining)

	ok, body, data := getSelfJSON(t, user)
	require.True(t, ok, body)
	assert.EqualValues(t, total, data["quota"])
	assert.Contains(t, body, `"principal_quota":500000`)
	assert.Contains(t, body, `"bonus_quota":250000`)
	assert.Contains(t, body, `"expiring_bonus_quota":100000`)
	ftrAssertLotSplit(t, lotProjectionFrom(t, data), expiresAt)

	direct, present, err := service.ProjectWalletLotsForUser(user)
	require.NoError(t, err)
	require.True(t, present)
	ftrAssertLotSplit(t, direct, expiresAt)

	var after []model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ?", user.Id).Find(&after).Error)
	require.Len(t, after, 3)
	for _, row := range after {
		assert.Equal(t, row.Original, row.Remaining)
	}
	sum, err := model.SumAdtyFundBatchRemainingByKind(user.Id)
	require.NoError(t, err)
	assert.EqualValues(t, ftrSurfaceOneUSDQuota, sum.Principal)
	assert.EqualValues(t, ftrSurfacePermanentGift, sum.Bonus)
	assert.EqualValues(t, ftrSurfaceExpiringGift, sum.ExpiringBonus)
	assert.Equal(t, expiresAt, sum.NextExpiresAt)
	ftrAssertQuotaUnchanged(t, db, user.Id, total)
}

// GetAdtyLegal looks up the query lang exactly. Product pages send zh-CN for UI language zh.
func TestFTRADTY0105TermsPagePG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	ftrSurfaceHTTP(t)
	fixture := ftrPersistLegalTexts(t, db)
	ftrAssertStoredOption(t, db, "adty_legal_setting.user_agreement", fixture.agreementJSON)
	ftrAssertStoredOption(t, db, "legal.user_agreement", ftrUpstreamAgreement)
	ftrAssertLegalDoc(t, "user-agreement", fixture.agreement, ftrUpstreamAgreement, "FTR-0106")
}

func TestFTRADTY0106PrivacyPagePG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	ftrSurfaceHTTP(t)
	fixture := ftrPersistLegalTexts(t, db)
	ftrAssertStoredOption(t, db, "adty_legal_setting.privacy_policy", fixture.privacyJSON)
	ftrAssertStoredOption(t, db, "legal.privacy_policy", ftrUpstreamPrivacy)
	ftrAssertLegalDoc(t, "privacy-policy", fixture.privacy, ftrUpstreamPrivacy, "FTR-0105")
}

// Channel model_mapping save, normalize, and relay rewrite. There is no controller CRUD for AdtyModelAlias.
func TestFTRADTY0503ModelAliasMappingPG(t *testing.T) {
	restoreChannelCache := false
	t.Cleanup(func() {
		if restoreChannelCache {
			_ = model.InitChannelCache()
		}
	})
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	ftrSurfaceHTTP(t)
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))

	tag := "ftr-0503"
	channel := &model.Channel{
		Name: "ftr-0503", Type: constant.ChannelTypeOpenAI, Key: "sk-ftr-0503",
		Models: "ftr-alias", Group: "default", Status: common.ChannelStatusEnabled, Tag: &tag,
	}
	require.NoError(t, db.Create(channel).Error)
	mappingBytes, err := common.Marshal(map[string]string{
		"ftr-alias":      "ftr-upstream",
		" spaced-alias ": " spaced-upstream ",
		"":               "drop-empty-key",
		"blank-target":   "   ",
	})
	require.NoError(t, err)
	rawMapping := string(mappingBytes)
	payload, err := common.Marshal(map[string]string{"tag": tag, "model_mapping": rawMapping})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel/tag", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 1)
	ctx.Set("role", common.RoleRootUser)
	ctx.Set("username", "ftr-0503-admin")
	restoreChannelCache = true
	EditTagChannels(ctx)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), `"success":true`)

	var stored model.Channel
	require.NoError(t, db.First(&stored, channel.Id).Error)
	require.NotNil(t, stored.ModelMapping)
	assert.JSONEq(t, rawMapping, *stored.ModelMapping)
	assert.Contains(t, *stored.ModelMapping, `" spaced-alias "`)
	assert.Equal(t, "ftr-alias", stored.Models)
	assert.Equal(t, "sk-ftr-0503", stored.Key)
	assert.Equal(t, map[string]string{
		"ftr-alias":    "ftr-upstream",
		"spaced-alias": "spaced-upstream",
	}, normalizeChannelModelMapping(&stored))

	ctx.Set("model_mapping", *stored.ModelMapping)
	request := &dto.GeneralOpenAIRequest{Model: "ftr-alias"}
	info := &relaycommon.RelayInfo{
		OriginModelName: "ftr-alias",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "ftr-alias"},
	}
	require.NoError(t, helper.ModelMappedHelper(ctx, info, request))
	assert.True(t, info.IsModelMapped)
	assert.Equal(t, "ftr-upstream", info.UpstreamModelName)
	assert.Equal(t, "ftr-upstream", request.Model)

	untrimmed := &dto.GeneralOpenAIRequest{Model: "spaced-alias"}
	untrimmedInfo := &relaycommon.RelayInfo{
		OriginModelName: "spaced-alias",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "spaced-alias"},
	}
	require.NoError(t, helper.ModelMappedHelper(ctx, untrimmedInfo, untrimmed))
	assert.False(t, untrimmedInfo.IsModelMapped)
	assert.Equal(t, "spaced-alias", untrimmedInfo.UpstreamModelName)
	assert.Equal(t, "spaced-alias", untrimmed.Model)
}
