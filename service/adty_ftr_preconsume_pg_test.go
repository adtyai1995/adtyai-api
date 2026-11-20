package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrPreconsumeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := ftrRound1PG(t)
	oldRedis, oldBatch := common.RedisEnabled, common.BatchUpdateEnabled
	common.RedisEnabled, common.BatchUpdateEnabled = false, false
	t.Cleanup(func() {
		common.RedisEnabled, common.BatchUpdateEnabled = oldRedis, oldBatch
	})
	require.NoError(t, db.AutoMigrate(
		&model.Token{},
		&model.BillingOperation{},
		&model.BillingOperationMovement{},
		&model.AdtyFundBatch{},
		&model.AdtyFundAllocation{},
		&model.AdtyBillingHold{},
		&model.AdtySiteEarningEvent{},
	))
	return db
}

func ftrPreconsumeCaller(t *testing.T, db *gorm.DB, name, requestID string) (model.User, model.Token, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	user := ftrRound1User(t, db, name, common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Zero(t, user.SiteId)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 1000000).Error)
	token := model.Token{
		UserId: user.Id, Key: name + "-key", Name: name, Status: common.TokenStatusEnabled,
		RemainQuota: 100000, UsedQuota: 0, UnlimitedQuota: false, ExpiredTime: -1, OwnerSelfUseSiteId: 0,
	}
	require.NoError(t, db.Create(&token).Error)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: false,
		ForcePreConsume: true, RequestId: requestID, UserGroup: "default", UsingGroup: "default",
	}
	return user, token, ctx, info
}

// ADTY-07-02: a personal relay request reserves quota before the upstream call.
// POST /v1/chat/completions reaches PreConsumeBilling. Quota falls from 1000000 to 990000.
func TestFTRADTY0702PersonalPreConsumeReservesQuotaPG(t *testing.T) {
	db := ftrPreconsumeDB(t)
	user, token, ctx, info := ftrPreconsumeCaller(t, db, "ftr0702", "ftr0702-preconsume")
	require.Equal(t, http.MethodPost, ctx.Request.Method)
	require.Equal(t, "/v1/chat/completions", ctx.Request.URL.Path)

	require.Nil(t, PreConsumeBilling(ctx, 10000, info))
	require.NotNil(t, info.Billing)

	var stored model.User
	require.NoError(t, db.Select("id", "quota", "role", "site_id").First(&stored, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Zero(t, stored.SiteId)
	assert.Equal(t, 990000, stored.Quota)
	var storedToken model.Token
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, 90000, storedToken.RemainQuota)
	assert.Equal(t, 10000, storedToken.UsedQuota)
	op, err := model.GetBillingOperation("request:ftr0702-preconsume")
	require.NoError(t, err)
	require.NotNil(t, op)
	assert.Equal(t, model.BillingPayerPersonal, op.PayerKind)
	assert.Zero(t, op.PayerTeamId)
	assert.Equal(t, 10000, op.ReservedQuota)
	assert.Equal(t, model.BillingReserved, op.State)
	var movements []model.BillingOperationMovement
	require.NoError(t, db.Where("operation_key = ?", op.OperationKey).Find(&movements).Error)
	require.Len(t, movements, 1)
	assert.Equal(t, -10000, movements[0].WalletDelta)
	assert.Equal(t, -10000, movements[0].TokenDelta)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-07-07: settlement below the reservation releases the unused pre-charge.
// User quota 1000000, reserve 10000, settle 6000, quota becomes 994000.
func TestFTRADTY0707SettlementReleasesUnusedPreConsumePG(t *testing.T) {
	db := ftrPreconsumeDB(t)
	user, token, ctx, info := ftrPreconsumeCaller(t, db, "ftr0707", "ftr0707-release")
	require.Equal(t, http.MethodPost, ctx.Request.Method)
	require.Equal(t, "/v1/chat/completions", ctx.Request.URL.Path)
	require.Nil(t, PreConsumeBilling(ctx, 10000, info))
	require.NoError(t, SettleBilling(ctx, info, 6000))

	var stored model.User
	require.NoError(t, db.Select("id", "quota", "role").First(&stored, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, 994000, stored.Quota)
	var storedToken model.Token
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, 94000, storedToken.RemainQuota)
	assert.Equal(t, 6000, storedToken.UsedQuota)
	op, err := model.GetBillingOperation("request:ftr0707-release")
	require.NoError(t, err)
	require.NotNil(t, op)
	assert.Equal(t, model.BillingPayerPersonal, op.PayerKind)
	assert.Equal(t, 6000, op.ActualQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
