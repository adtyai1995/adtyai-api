package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftr0708DB(t *testing.T) *gorm.DB {
	t.Helper()
	opened, err := model.OpenAdtyTestDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opened.Close()) })
	db := opened.DB
	pgtest.RecordServerVersion(t, db)
	oldRedis, oldBatch, oldPerUnit := common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit
	common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit = false, false, 500000
	t.Cleanup(func() {
		common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit = oldRedis, oldBatch, oldPerUnit
	})
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Token{}, &model.Log{},
		&model.BillingOperation{}, &model.BillingOperationMovement{},
		&model.AdtyFundBatch{}, &model.AdtyFundAllocation{}, &model.AdtyBillingHold{},
	))
	require.NoError(t, model.MigrateAdtyTables())
	return db
}

func ftr0708Caller(t *testing.T, db *gorm.DB, name, requestID string) (model.User, model.Token, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	user := model.User{
		Username: name, Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: 1_000_000, AuthVersion: 1,
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.CreateAdtyFundBatchesTx(tx, user.Id, name+"-seed", 1_000_000, 0)
	}))
	token := model.Token{
		UserId: user.Id, Key: name + "-key", Name: name, Status: common.TokenStatusEnabled,
		RemainQuota: 100_000, UsedQuota: 0, UnlimitedQuota: false, ExpiredTime: -1,
	}
	require.NoError(t, db.Create(&token).Error)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: false,
		ForcePreConsume: true, RequestId: requestID, UserGroup: "default", UsingGroup: "default",
		StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{},
	}
	return user, token, ctx, info
}

func TestFTRADTY0708FailedRequestRefundOncePG(t *testing.T) {
	db := ftr0708DB(t)
	user, token, ctx, info := ftr0708Caller(t, db, "ftr0708-fail", "ftr0708-fail")
	require.Nil(t, service.PreConsumeBilling(ctx, 10_000, info))
	require.NotNil(t, info.Billing)

	var reserved model.User
	require.NoError(t, db.Select("quota").First(&reserved, user.Id).Error)
	require.Equal(t, 990_000, reserved.Quota)

	apiErr := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)
	require.NotNil(t, RefundFailedRequestBilling(ctx, info, apiErr))
	require.NotNil(t, RefundFailedRequestBilling(ctx, info, apiErr))

	var after model.User
	require.NoError(t, db.Select("quota").First(&after, user.Id).Error)
	require.Equal(t, 1_000_000, after.Quota)
	var afterToken model.Token
	require.NoError(t, db.First(&afterToken, token.Id).Error)
	require.Equal(t, 100_000, afterToken.RemainQuota)
	require.Zero(t, afterToken.UsedQuota)
	op, err := model.GetBillingOperation("request:ftr0708-fail")
	require.NoError(t, err)
	require.NotNil(t, op)
	require.Equal(t, 0, op.ActualQuota)
	var movements []model.BillingOperationMovement
	require.NoError(t, db.Where("operation_key = ?", op.OperationKey).Find(&movements).Error)
	require.NotEmpty(t, movements)

	settledUser, settledToken, settledCtx, settledInfo := ftr0708Caller(t, db, "ftr0708-settled", "ftr0708-settled")
	require.Nil(t, service.PreConsumeBilling(settledCtx, 10_000, settledInfo))
	require.NoError(t, service.SettleBilling(settledCtx, settledInfo, 7_500))
	var settled model.User
	require.NoError(t, db.Select("quota").First(&settled, settledUser.Id).Error)
	require.Equal(t, 992_500, settled.Quota)
	var beforeMovements int64
	require.NoError(t, db.Model(&model.BillingOperationMovement{}).
		Where("operation_key = ?", "request:ftr0708-settled").Count(&beforeMovements).Error)
	require.NotNil(t, RefundFailedRequestBilling(settledCtx, settledInfo, apiErr))
	var still model.User
	require.NoError(t, db.Select("quota").First(&still, settledUser.Id).Error)
	require.Equal(t, 992_500, still.Quota)
	var stillToken model.Token
	require.NoError(t, db.First(&stillToken, settledToken.Id).Error)
	require.Equal(t, 7_500, stillToken.UsedQuota)
	require.Equal(t, 92_500, stillToken.RemainQuota)
	var afterMovements int64
	require.NoError(t, db.Model(&model.BillingOperationMovement{}).
		Where("operation_key = ?", "request:ftr0708-settled").Count(&afterMovements).Error)
	require.Equal(t, beforeMovements, afterMovements)
}
