package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftr0609DB(t *testing.T, withTask bool) *gorm.DB {
	t.Helper()
	opened, err := model.OpenAdtyTestDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opened.Close()) })
	db := opened.DB
	pgtest.RecordServerVersion(t, db)
	oldRedis, oldBatch, oldLog, oldExport, oldPerUnit := common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, common.QuotaPerUnit
	common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, common.QuotaPerUnit = false, false, false, false, 500000
	t.Cleanup(func() {
		common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, common.QuotaPerUnit = oldRedis, oldBatch, oldLog, oldExport, oldPerUnit
	})
	models := []any{&model.User{}, &model.Token{}, &model.Log{}, &model.Channel{}}
	if withTask {
		models = append(models, &model.Task{})
	}
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, model.MigrateAdtyTables())
	return db
}

func ftr0609Caller(t *testing.T, db *gorm.DB, name, requestID, taskID string, quota int) (model.User, model.Token, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	user := model.User{
		Username: name, Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: quota, AuthVersion: 1,
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.CreateAdtyFundBatchesTx(tx, user.Id, name+"-seed", int64(quota), 0)
	}))
	token := model.Token{
		UserId: user.Id, Key: name + "-key", Name: name, Status: common.TokenStatusEnabled,
		RemainQuota: 100_000, UsedQuota: 0, UnlimitedQuota: false, ExpiredTime: -1,
	}
	require.NoError(t, db.Create(&token).Error)
	gin.SetMode(gin.TestMode)
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/"+name, bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = req
	info := &relaycommon.RelayInfo{
		UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: false,
		ForcePreConsume: true, RequestId: requestID, UserGroup: "default", UsingGroup: "default",
		OriginModelName: "ftr-0609", StartTime: time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:  taskID,
			Action:        "submit",
			LockedChannel: &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "ftr-0609"},
		},
	}
	return user, token, ctx, info
}

func TestFTRADTY0609TaskReserveInsertSettlePG(t *testing.T) {
	t.Run("reserve_failure_before_insert_returns_403", func(t *testing.T) {
		db := ftr0609DB(t, true)
		user, token, ctx, info := ftr0609Caller(t, db, "ftr0609-short", "ftr0609-short", "task_ftr0609_short", 2_000)
		require.Nil(t, service.PreConsumeBilling(ctx, 1_000, info))
		outcome, taskErr := executeTaskSubmissionWith(ctx, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			return &relay.TaskSubmitResult{UpstreamTaskID: "upstream-short", Platform: constant.TaskPlatform("ftr0609"), Quota: 5_000}, nil
		})
		require.Nil(t, outcome)
		require.NotNil(t, taskErr)
		require.Equal(t, http.StatusForbidden, taskErr.StatusCode)
		require.Contains(t, taskErr.Message, "insufficient quota")
		require.False(t, ctx.Writer.Written())
		var tasks int64
		require.NoError(t, db.Model(&model.Task{}).Where("task_id = ?", "task_ftr0609_short").Count(&tasks).Error)
		require.Zero(t, tasks)
		var after model.User
		require.NoError(t, db.Select("quota").First(&after, user.Id).Error)
		require.Equal(t, 2_000, after.Quota)
		var afterToken model.Token
		require.NoError(t, db.First(&afterToken, token.Id).Error)
		require.Equal(t, 100_000, afterToken.RemainQuota)
		require.Zero(t, afterToken.UsedQuota)
	})

	t.Run("insert_then_settle_once", func(t *testing.T) {
		db := ftr0609DB(t, true)
		user, token, ctx, info := ftr0609Caller(t, db, "ftr0609-ok", "ftr0609-ok", "task_ftr0609_ok", 1_000_000)
		require.Nil(t, service.PreConsumeBilling(ctx, 1_000, info))
		outcome, taskErr := executeTaskSubmissionWith(ctx, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			return &relay.TaskSubmitResult{UpstreamTaskID: "upstream-ok", Platform: constant.TaskPlatform("ftr0609"), Quota: 1_000}, nil
		})
		require.Nil(t, taskErr)
		require.NotNil(t, outcome)
		require.Equal(t, "task_ftr0609_ok", outcome.Task.TaskID)
		var tasks int64
		require.NoError(t, db.Model(&model.Task{}).Where("task_id = ?", "task_ftr0609_ok").Count(&tasks).Error)
		require.EqualValues(t, 1, tasks)
		var after model.User
		require.NoError(t, db.Select("quota").First(&after, user.Id).Error)
		require.Equal(t, 999_000, after.Quota)
		var afterToken model.Token
		require.NoError(t, db.First(&afterToken, token.Id).Error)
		require.Equal(t, 99_000, afterToken.RemainQuota)
		require.Equal(t, 1_000, afterToken.UsedQuota)
		op, err := model.GetBillingOperation("request:ftr0609-ok")
		require.NoError(t, err)
		require.Equal(t, model.BillingSettled, op.State)
		require.Equal(t, 1_000, op.ActualQuota)
		var applies int64
		require.NoError(t, db.Model(&model.BillingOperationMovement{}).Where("operation_key = ? AND stage = ?", op.OperationKey, "apply").Count(&applies).Error)
		require.EqualValues(t, 1, applies)
	})

	t.Run("written_response_settles_when_insert_fails", func(t *testing.T) {
		db := ftr0609DB(t, false)
		user, token, ctx, info := ftr0609Caller(t, db, "ftr0609-written", "ftr0609-written", "task_ftr0609_written", 1_000_000)
		require.Nil(t, service.PreConsumeBilling(ctx, 1_000, info))
		_, err := ctx.Writer.Write([]byte("accepted"))
		require.NoError(t, err)
		require.True(t, ctx.Writer.Written())
		outcome, taskErr := executeTaskSubmissionWith(ctx, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			return &relay.TaskSubmitResult{UpstreamTaskID: "upstream-written", Platform: constant.TaskPlatform("ftr0609"), Quota: 1_000}, nil
		})
		require.Nil(t, outcome)
		require.NotNil(t, taskErr)
		require.Equal(t, "task_insert_failed", taskErr.Code)
		var after model.User
		require.NoError(t, db.Select("quota").First(&after, user.Id).Error)
		require.Equal(t, 999_000, after.Quota)
		var afterToken model.Token
		require.NoError(t, db.First(&afterToken, token.Id).Error)
		require.Equal(t, 99_000, afterToken.RemainQuota)
		op, err := model.GetBillingOperation("request:ftr0609-written")
		require.NoError(t, err)
		require.Equal(t, model.BillingSettled, op.State)
		require.Equal(t, 1_000, op.ActualQuota)
		var applies int64
		require.NoError(t, db.Model(&model.BillingOperationMovement{}).Where("operation_key = ? AND stage = ?", op.OperationKey, "apply").Count(&applies).Error)
		require.EqualValues(t, 1, applies)
	})
}
