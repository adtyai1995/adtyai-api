package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const failBillingCreateHook = "adty_test_fail_task_billing_create"

var perfRecoveryTestSequence atomic.Uint64

// quotaPollingAdaptor counts completion adjustments without changing the shared poll double.
type quotaPollingAdaptor struct {
	scriptedPollingAdaptor
	adjustQuota int
	adjusts     int
}

func (a *quotaPollingAdaptor) AdjustBillingOnComplete(ctx context.Context, task *model.Task, result *relaycommon.TaskInfo) int {
	a.adjusts++
	if a.onComplete != nil {
		a.onComplete(ctx)
	}
	return a.adjustQuota
}

type billingCreateFail struct {
	left  int
	fired int
}

func failNextTaskBillingCreate(t *testing.T, part string) *billingCreateFail {
	t.Helper()
	state := &billingCreateFail{left: 1}
	model.DB.Callback().Create().Remove(failBillingCreateHook)
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register(failBillingCreateHook, func(tx *gorm.DB) {
		if state.left <= 0 || tx == nil || tx.Statement == nil {
			return
		}
		row, ok := tx.Statement.Dest.(*model.BillingOperation)
		if !ok || !strings.Contains(row.OperationKey, part) {
			return
		}
		state.left--
		state.fired++
		_ = tx.AddError(errors.New("brief db error"))
	}))
	t.Cleanup(func() { model.DB.Callback().Create().Remove(failBillingCreateHook) })
	return state
}

type perfRecoveryEnv struct {
	t                     *testing.T
	userID, tokenID, chID int
	modelName             string
	started               time.Time
	when                  *time.Time
	adaptor               *quotaPollingAdaptor
	quotaAfter            int
	tokenAfter            int
}

func newPerfRecoveryEnv(t *testing.T, id int, modelName string) *perfRecoveryEnv {
	t.Helper()
	truncate(t)
	// The in-process perf buckets outlive both database fixtures.
	modelName = fmt.Sprintf("%s-%d", modelName, perfRecoveryTestSequence.Add(1))
	started := time.Now()
	when := started
	env := &perfRecoveryEnv{
		t: t, userID: id, tokenID: id, chID: id, modelName: modelName, started: started, when: &when,
		adaptor: &quotaPollingAdaptor{},
	}
	oldEnabled, oldClient := common.RedisEnabled, common.RDB
	live := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: live.Addr()})
	common.RedisEnabled = true
	common.RDB = client
	perfmetrics.SetTaskSampleNowForTest(func() time.Time { return *env.when })
	previous := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return env.adaptor }
	t.Cleanup(func() {
		GetTaskAdaptorFunc = previous
		perfmetrics.SetTaskSampleNowForTest(nil)
		model.DB.Callback().Create().Remove(failBillingCreateHook)
		if common.RDB != nil && common.RDB != oldClient {
			_ = common.RDB.Close()
		}
		common.RedisEnabled, common.RDB = oldEnabled, oldClient
	})
	return env
}

func (e *perfRecoveryEnv) seed(preConsumed int, status model.TaskStatus, mutate func(*model.Task)) *model.Task {
	e.t.Helper()
	seedUser(e.t, e.userID, 10_000)
	seedToken(e.t, e.tokenID, e.userID, "sk-"+e.modelName, 8_000)
	seedTaskPollingChannel(e.t, e.chID, true)
	task := makeTask(e.userID, e.chID, preConsumed, e.tokenID, BillingSourceWallet, 0)
	task.TaskID = "public-" + e.modelName
	task.Platform = constant.TaskPlatform("kling")
	task.Status = status
	task.Group = "default"
	task.SubmitTime = e.started.Unix() - 30
	task.StartTime = e.started.Unix() - 20
	task.Properties.OriginModelName = e.modelName
	task.PrivateData.BillingContext.OriginModelName = e.modelName
	task.PrivateData.UpstreamTaskID = "upstream-" + e.modelName
	if mutate != nil {
		mutate(task)
	}
	seedTaskReservation(e.t, task)
	if status != model.TaskStatusInProgress {
		task.FinishTime = e.started.Unix()
		task.BillingPending = true
	}
	require.NoError(e.t, model.DB.Create(task).Error)
	e.quotaAfter = getUserQuota(e.t, e.userID)
	e.tokenAfter = getTokenRemainQuota(e.t, e.tokenID)
	require.Equal(e.t, 10_000, e.quotaAfter)
	require.Equal(e.t, 8_000, e.tokenAfter)
	require.False(e.t, task.BillingInitialPending)
	return task
}

func (e *perfRecoveryEnv) poll(task *model.Task) {
	e.t.Helper()
	ch, err := model.GetChannelById(e.chID, true)
	require.NoError(e.t, err)
	require.NoError(e.t, updateVideoSingleTask(context.Background(), e.adaptor, ch, task.GetUpstreamTaskID(), map[string]*model.Task{
		task.GetUpstreamTaskID(): task,
	}))
}

func (e *perfRecoveryEnv) redisKey(at time.Time) string {
	unix := at.Unix()
	seconds := perf_metrics_setting.GetBucketSeconds()
	if seconds <= 0 {
		seconds = 3600
	}
	bucket := unix - unix%seconds
	return fmt.Sprintf("perf:%s:%s:%d", e.modelName, "default", bucket)
}

func (e *perfRecoveryEnv) redisHash(at time.Time) map[string]string {
	e.t.Helper()
	values, err := common.RDB.HGetAll(context.Background(), e.redisKey(at)).Result()
	require.NoError(e.t, err)
	return values
}

func (e *perfRecoveryEnv) assertSample(req int64, successRate float64) {
	e.t.Helper()
	summary, err := perfmetrics.QuerySummaryAll(24, []string{"default"}, e.modelName)
	require.NoError(e.t, err)
	var found *perfmetrics.ModelSummary
	for i := range summary.Models {
		if summary.Models[i].ModelName == e.modelName {
			found = &summary.Models[i]
			break
		}
	}
	require.NotNil(e.t, found)
	assert.Equal(e.t, req, found.RequestCount)
	assert.Equal(e.t, successRate, found.SuccessRate)
}

func (e *perfRecoveryEnv) taskOps(taskID int64) []model.BillingOperation {
	e.t.Helper()
	var rows []model.BillingOperation
	require.NoError(e.t, model.DB.Where("reference_id = ? AND kind = ?", taskID, "task").Order("operation_key").Find(&rows).Error)
	return rows
}

func (e *perfRecoveryEnv) movements(key string) []model.BillingOperationMovement {
	e.t.Helper()
	var rows []model.BillingOperationMovement
	require.NoError(e.t, model.DB.Where("operation_key = ?", key).Find(&rows).Error)
	return rows
}

func (e *perfRecoveryEnv) reload(id int64) model.Task {
	e.t.Helper()
	var stored model.Task
	require.NoError(e.t, model.DB.First(&stored, id).Error)
	return stored
}

// TestTerminalTaskRecoverySamplesOnceAndSettlesOnce covers the unrun ISS-R1-0198
// recovery scope. It does not repeat the already-passed sampler parents, and it
// is not an L0 or L1 smoke pass.
func TestTerminalTaskRecoverySamplesOnceAndSettlesOnce(t *testing.T) {
	t.Run("per-call same bucket recovers one adjust", func(t *testing.T) {
		env := newPerfRecoveryEnv(t, 8101, "perf-r1-percall-same")
		env.adaptor.parse = &relaycommon.TaskInfo{Status: string(model.TaskStatusSuccess), TotalTokens: 200}
		task := env.seed(5_000, model.TaskStatusInProgress, func(task *model.Task) {
			task.PrivateData.BillingContext.PerCallBilling = true
		})
		fail := failNextTaskBillingCreate(t, ":adjust:")
		env.poll(task)
		require.Equal(t, 1, fail.fired)

		stored := env.reload(task.ID)
		require.NotZero(t, stored.PerfSampledAt)
		assert.True(t, stored.BillingPending)
		assert.False(t, stored.BillingInitialPending)
		assert.Empty(t, env.taskOps(task.ID))
		assert.Equal(t, env.quotaAfter, getUserQuota(t, env.userID))
		assert.Equal(t, env.tokenAfter, getTokenRemainQuota(t, env.tokenID))
		firstHash := env.redisHash(env.started)
		assert.Equal(t, "1", firstHash["req"])
		assert.Equal(t, "1", firstHash["ok"])
		env.assertSample(1, 100)

		model.DB.Callback().Create().Remove(failBillingCreateHook)
		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		stored = env.reload(task.ID)
		assert.False(t, stored.BillingPending)
		assert.Empty(t, stored.PrivateData.BillingReviewReason)
		ops := env.taskOps(task.ID)
		require.Len(t, ops, 1)
		assert.Equal(t, model.BillingSettled, ops[0].State)
		assert.Equal(t, fmt.Sprintf("task:%d:adjust:5000", task.ID), ops[0].OperationKey)
		moves := env.movements(ops[0].OperationKey)
		require.Len(t, moves, 1)
		assert.Zero(t, moves[0].WalletDelta)
		assert.Equal(t, env.quotaAfter, getUserQuota(t, env.userID))
		assert.Equal(t, env.tokenAfter, getTokenRemainQuota(t, env.tokenID))
		assert.Equal(t, 5_000, stored.Quota)
		assert.Equal(t, firstHash, env.redisHash(env.started))
		env.assertSample(1, 100)

		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		assert.Equal(t, env.quotaAfter, getUserQuota(t, env.userID))
		assert.Equal(t, firstHash, env.redisHash(env.started))
		assert.Len(t, env.taskOps(task.ID), 1)
		assert.Len(t, env.movements(ops[0].OperationKey), 1)
		env.assertSample(1, 100)
	})

	t.Run("per-call cross bucket does not open a second sample", func(t *testing.T) {
		env := newPerfRecoveryEnv(t, 8102, "perf-r1-percall-cross")
		env.adaptor.parse = &relaycommon.TaskInfo{Status: string(model.TaskStatusSuccess), TotalTokens: 80}
		task := env.seed(4_000, model.TaskStatusInProgress, func(task *model.Task) {
			task.PrivateData.BillingContext.PerCallBilling = true
		})
		fail := failNextTaskBillingCreate(t, ":adjust:")
		env.poll(task)
		require.Equal(t, 1, fail.fired)
		firstHash := env.redisHash(env.started)
		assert.Equal(t, "1", firstHash["req"])
		env.assertSample(1, 100)

		model.DB.Callback().Create().Remove(failBillingCreateHook)
		*env.when = env.started.Add(2 * time.Hour)
		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		stored := env.reload(task.ID)
		assert.False(t, stored.BillingPending)
		assert.Equal(t, env.quotaAfter, getUserQuota(t, env.userID))
		assert.Equal(t, env.tokenAfter, getTokenRemainQuota(t, env.tokenID))
		assert.Equal(t, firstHash, env.redisHash(env.started))
		exists, err := common.RDB.Exists(context.Background(), env.redisKey(*env.when)).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(0), exists)
		ops := env.taskOps(task.ID)
		require.Len(t, ops, 1)
		assert.Equal(t, model.BillingSettled, ops[0].State)
		assert.Len(t, env.movements(ops[0].OperationKey), 1)
		env.assertSample(1, 100)
	})

	t.Run("adaptor success settles the difference once", func(t *testing.T) {
		env := newPerfRecoveryEnv(t, 8103, "perf-r1-adaptor")
		env.adaptor.adjustQuota = 3_000
		env.adaptor.parse = &relaycommon.TaskInfo{Status: string(model.TaskStatusSuccess)}
		task := env.seed(5_000, model.TaskStatusInProgress, nil)
		fail := failNextTaskBillingCreate(t, ":adjust:")
		logsBefore := countLogs(t)
		env.poll(task)
		require.Equal(t, 1, fail.fired)
		assert.Equal(t, env.quotaAfter, getUserQuota(t, env.userID))
		assert.Equal(t, int64(logsBefore), countLogs(t))
		assert.Empty(t, env.taskOps(task.ID))
		env.assertSample(1, 100)

		model.DB.Callback().Create().Remove(failBillingCreateHook)
		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		stored := env.reload(task.ID)
		assert.False(t, stored.BillingPending)
		assert.Equal(t, 3_000, stored.Quota)
		assert.Equal(t, env.quotaAfter+2_000, getUserQuota(t, env.userID))
		assert.Equal(t, env.tokenAfter+2_000, getTokenRemainQuota(t, env.tokenID))
		assert.Equal(t, int64(logsBefore+1), countLogs(t))
		ops := env.taskOps(task.ID)
		require.Len(t, ops, 1)
		assert.Equal(t, model.BillingSettled, ops[0].State)
		assert.Equal(t, fmt.Sprintf("task:%d:adjust:3000", task.ID), ops[0].OperationKey)
		moves := env.movements(ops[0].OperationKey)
		require.Len(t, moves, 1)
		assert.Equal(t, 2_000, moves[0].WalletDelta)
		assert.Equal(t, "1", env.redisHash(env.started)["req"])
		assert.Equal(t, "1", env.redisHash(env.started)["ok"])
		env.assertSample(1, 100)

		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		assert.Equal(t, env.quotaAfter+2_000, getUserQuota(t, env.userID))
		assert.Len(t, env.movements(ops[0].OperationKey), 1)
		assert.Equal(t, int64(logsBefore+1), countLogs(t))
		env.assertSample(1, 100)
	})

	t.Run("expression success settles the recomputed quota once", func(t *testing.T) {
		env := newPerfRecoveryEnv(t, 8104, "perf-r1-expression")
		expression := `tier("base", u("seconds") + u("clips") * 10)`
		env.adaptor.parse = &relaycommon.TaskInfo{
			Status:     string(model.TaskStatusSuccess),
			UsageFacts: map[string]any{"seconds": float64(8)},
		}
		task := env.seed(50, model.TaskStatusInProgress, func(task *model.Task) {
			task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
				ExprString:       expression,
				ExprHash:         billingexpr.ExprHashString(expression),
				GroupRatio:       1,
				QuotaPerUnit:     1,
				ExprVersion:      1,
				TaskUsageBilling: true,
				UsageFacts:       map[string]any{"seconds": float64(5), "clips": float64(2)},
				EstimatedTier:    "base",
			}
		})
		fail := failNextTaskBillingCreate(t, ":adjust:")
		env.poll(task)
		require.Equal(t, 1, fail.fired)
		assert.Equal(t, env.quotaAfter, getUserQuota(t, env.userID))
		assert.Empty(t, env.taskOps(task.ID))
		env.assertSample(1, 100)

		model.DB.Callback().Create().Remove(failBillingCreateHook)
		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		stored := env.reload(task.ID)
		assert.False(t, stored.BillingPending)
		assert.Equal(t, 28, stored.Quota)
		assert.Equal(t, env.quotaAfter+(50-28), getUserQuota(t, env.userID))
		ops := env.taskOps(task.ID)
		require.Len(t, ops, 1)
		assert.Equal(t, model.BillingSettled, ops[0].State)
		assert.Len(t, env.movements(ops[0].OperationKey), 1)
		assert.Equal(t, "1", env.redisHash(env.started)["req"])
		env.assertSample(1, 100)

		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		assert.Equal(t, env.quotaAfter+(50-28), getUserQuota(t, env.userID))
		assert.Len(t, env.movements(ops[0].OperationKey), 1)
		env.assertSample(1, 100)
	})

	t.Run("failure refunds once and samples the failure", func(t *testing.T) {
		env := newPerfRecoveryEnv(t, 8105, "perf-r1-failure")
		env.adaptor.parse = &relaycommon.TaskInfo{Status: string(model.TaskStatusFailure), Reason: "provider rejected"}
		task := env.seed(5_000, model.TaskStatusInProgress, nil)
		fail := failNextTaskBillingCreate(t, ":refund")
		env.poll(task)
		require.Equal(t, 1, fail.fired)
		stored := env.reload(task.ID)
		assert.EqualValues(t, model.TaskStatusFailure, stored.Status)
		assert.True(t, stored.BillingPending)
		require.NotZero(t, stored.PerfSampledAt)
		assert.Empty(t, env.taskOps(task.ID))
		assert.Equal(t, env.quotaAfter, getUserQuota(t, env.userID))
		assert.Equal(t, "1", env.redisHash(env.started)["req"])
		assert.Empty(t, env.redisHash(env.started)["ok"])
		env.assertSample(1, 0)

		model.DB.Callback().Create().Remove(failBillingCreateHook)
		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		stored = env.reload(task.ID)
		assert.False(t, stored.BillingPending)
		assert.Zero(t, stored.Quota)
		assert.Equal(t, env.quotaAfter+5_000, getUserQuota(t, env.userID))
		assert.Equal(t, env.tokenAfter+5_000, getTokenRemainQuota(t, env.tokenID))
		ops := env.taskOps(task.ID)
		require.Len(t, ops, 1)
		assert.Equal(t, model.BillingRefunded, ops[0].State)
		assert.Equal(t, fmt.Sprintf("task:%d:refund", task.ID), ops[0].OperationKey)
		moves := env.movements(ops[0].OperationKey)
		require.Len(t, moves, 1)
		assert.Equal(t, 5_000, moves[0].WalletDelta)
		assert.Equal(t, "1", env.redisHash(env.started)["req"])
		assert.Empty(t, env.redisHash(env.started)["ok"])
		env.assertSample(1, 0)

		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		assert.Equal(t, env.quotaAfter+5_000, getUserQuota(t, env.userID))
		assert.Len(t, env.movements(ops[0].OperationKey), 1)
		env.assertSample(1, 0)
	})

	t.Run("queued adjust is applied before another finalize", func(t *testing.T) {
		env := newPerfRecoveryEnv(t, 8106, "perf-r1-queued-apply")
		env.adaptor.adjustQuota = 5_000
		task := env.seed(5_000, model.TaskStatusSuccess, nil)
		perfmetrics.RecordTaskResult(task, &relaycommon.TaskInfo{Status: string(model.TaskStatusSuccess), TotalTokens: 40})
		stored := env.reload(task.ID)
		require.NotZero(t, stored.PerfSampledAt)
		key, err := model.QueueTaskBillingOperation(task, task.Quota, false)
		require.NoError(t, err)
		require.NoError(t, model.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("billing_pending", true).Error)
		assert.Equal(t, "1", env.redisHash(env.started)["req"])
		env.assertSample(1, 100)

		*env.when = env.started.Add(2 * time.Hour)
		require.NoError(t, RecoverBillingOperations(context.Background(), 100))
		stored = env.reload(task.ID)
		assert.False(t, stored.BillingPending)
		assert.Empty(t, stored.PrivateData.BillingReviewReason)
		assert.Zero(t, env.adaptor.adjusts)
		op, err := model.GetBillingOperation(key)
		require.NoError(t, err)
		assert.Equal(t, model.BillingSettled, op.State)
		moves := env.movements(key)
		require.Len(t, moves, 1)
		assert.Zero(t, moves[0].WalletDelta)
		assert.Equal(t, env.quotaAfter, getUserQuota(t, env.userID))
		assert.Equal(t, env.tokenAfter, getTokenRemainQuota(t, env.tokenID))
		assert.Equal(t, "1", env.redisHash(env.started)["req"])
		exists, err := common.RDB.Exists(context.Background(), env.redisKey(*env.when)).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(0), exists)
		env.assertSample(1, 100)
	})
}
