package perfmetrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClassifyRelayOutcome(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  *types.NewAPIError
		want Outcome
	}{
		{"success", context.Background(), nil, OutcomeSuccess},
		{"upstream bad request", context.Background(), types.InitOpenAIError("unknown", 400), OutcomeIgnored},
		{"upstream credentials returned as 400", context.Background(), types.InitOpenAIError("invalid_api_key", 400), OutcomeFailure},
		{"wrapped credentials keep their cause", context.Background(), types.NewErrorWithStatusCode(types.InitOpenAIError("invalid_api_key", 400), types.ErrorCodeInvalidRequest, 400), OutcomeFailure},
		{"upstream context limit returned as 500", context.Background(), types.InitOpenAIError("context_length_exceeded", 500), OutcomeIgnored},
		{"local rate limit", context.Background(), types.NewErrorWithStatusCode(errors.New("limited"), types.ErrorCodeInvalidRequest, 429), OutcomeIgnored},
		{"upstream rate limit", context.Background(), types.InitOpenAIError("rate_limit_exceeded", 429), OutcomeFailure},
		{"local quota", context.Background(), types.NewError(errors.New("quota"), types.ErrorCodeInsufficientUserQuota), OutcomeIgnored},
		{"local violation fee", context.Background(), types.NewError(errors.New("csam"), types.ErrorCodeViolationFeeGrokCSAM), OutcomeIgnored},
		{"upstream quota", context.Background(), types.InitOpenAIError("insufficient_quota", 429), OutcomeFailure},
		{"upstream gateway quota", context.Background(), types.InitOpenAIError(types.ErrorCodeInsufficientUserQuota, 403), OutcomeFailure},
		{"unavailable channel", context.Background(), types.NewErrorWithStatusCode(errors.New("disabled"), types.ErrorCodeGetChannelFailed, 403), OutcomeFailure},
		{"empty upstream response", context.Background(), types.NewError(errors.New("empty"), types.ErrorCodeEmptyResponse), OutcomeFailure},
		{"network failure", context.Background(), types.NewOpenAIError(errors.New("connection refused"), types.ErrorCodeDoRequestFailed, 500), OutcomeFailure},
		{"client cancellation", canceled, types.NewOpenAIError(errors.New("context canceled"), types.ErrorCodeDoRequestFailed, 500), OutcomeIgnored},
		{"upstream deadline", context.Background(), types.NewOpenAIError(context.DeadlineExceeded, types.ErrorCodeDoRequestFailed, 504), OutcomeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ClassifyRelayOutcome(tc.ctx, &relaycommon.RelayInfo{}, tc.err))
		})
	}
	assert.Equal(t, OutcomeIgnored, ClassifyRelayOutcome(context.Background(), &relaycommon.RelayInfo{PerformanceBusinessRejection: true}, nil))
}

func TestStreamOutcomeClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		mark func(*relaycommon.StreamStatus)
		end  relaycommon.StreamEndReason
		want Outcome
	}{
		{"completed", (*relaycommon.StreamStatus).MarkCompleted, relaycommon.StreamEndReasonEOF, OutcomeSuccess},
		{"business rejection", func(s *relaycommon.StreamStatus) { s.MarkFailed("context_length_exceeded", "", 0) }, relaycommon.StreamEndReasonEOF, OutcomeIgnored},
		{"service error", func(s *relaycommon.StreamStatus) { s.MarkFailed("server_error", "", 0) }, relaycommon.StreamEndReasonEOF, OutcomeFailure},
		{"error after completion", func(s *relaycommon.StreamStatus) { s.MarkCompleted(); s.MarkFailed("", "server_error", 0) }, relaycommon.StreamEndReasonEOF, OutcomeFailure},
		{"output limit", func(s *relaycommon.StreamStatus) { s.MarkIncomplete("max_output_tokens") }, relaycommon.StreamEndReasonEOF, OutcomeSuccess},
		{"content filter", func(s *relaycommon.StreamStatus) { s.MarkIncomplete("content_filter") }, relaycommon.StreamEndReasonEOF, OutcomeIgnored},
		{"client cancel", (*relaycommon.StreamStatus).MarkCancelled, relaycommon.StreamEndReasonEOF, OutcomeIgnored},
		{"client gone", nil, relaycommon.StreamEndReasonClientGone, OutcomeIgnored},
		{"timeout", nil, relaycommon.StreamEndReasonTimeout, OutcomeFailure},
		{"missing terminal", nil, relaycommon.StreamEndReasonEOF, OutcomeFailure},
		{"done marker without terminal", nil, relaycommon.StreamEndReasonDone, OutcomeSuccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := relaycommon.NewStreamStatus()
			stream.RequireTerminal()
			if tc.mark != nil {
				tc.mark(stream)
			}
			stream.SetEndReason(tc.end, nil)
			assert.Equal(t, tc.want, ClassifyRelayOutcome(context.Background(), &relaycommon.RelayInfo{StreamStatus: stream}, nil))
		})
	}
}

func TestClientCancellationDuringUpstreamRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := relaycommon.NewStreamStatus()
	stream.RequireTerminal()
	stream.SetEndReason(relaycommon.StreamEndReasonScannerErr, errors.New("reader closed"))
	assert.Equal(t, OutcomeIgnored, ClassifyRelayOutcome(ctx, &relaycommon.RelayInfo{StreamStatus: stream}, nil))

	deadline := relaycommon.NewStreamStatus()
	deadline.SetEndReason(relaycommon.StreamEndReasonClientGone, context.DeadlineExceeded)
	assert.Equal(t, OutcomeFailure, ClassifyRelayOutcome(context.Background(), &relaycommon.RelayInfo{StreamStatus: deadline}, nil))
}

func TestPerformanceWindowIncludesCurrentHour(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 37, 0, 0, time.UTC)
	start, end := queryWindow(now, 24)
	assert.Equal(t, time.Date(2026, 9, 13, 13, 0, 0, 0, time.UTC).Unix(), start)
	assert.Equal(t, now.Unix(), end)
}

func TestHourlySuccessSeriesWeightsSmallerBuckets(t *testing.T) {
	points := recentSuccessSeries(map[int64]counters{
		3600: {requestCount: 100, successCount: 100},
		3900: {requestCount: 1},
		7200: {requestCount: 1, successCount: 1},
	})
	assert.Equal(t, []SuccessRatePoint{{Ts: 3600, SuccessRate: 99.01}, {Ts: 7200, SuccessRate: 100}}, points)
}

func TestRecentHistoryRejectsOversizedModelLists(t *testing.T) {
	_, err := QuerySummaryAll(24, nil, make([]string, 51)...)
	require.EqualError(t, err, "Invalid parameters")
}

// Terminal task sampling: success/failure counts, end-to-end latency, and
// token throughput only for successful tasks that report tokens.
func TestRecordTaskResultSamplesTerminalTasks(t *testing.T) {
	hotBuckets.Clear()
	t.Cleanup(func() { hotBuckets.Clear() })
	now := time.Now().Unix()
	RecordTaskResult(&model.Task{
		Status:     model.TaskStatusSuccess,
		Group:      "a",
		SubmitTime: now - 120,
		StartTime:  now - 100,
		FinishTime: now,
		Properties: model.Properties{OriginModelName: "video-model"},
	}, &relaycommon.TaskInfo{TotalTokens: 5000})
	RecordTaskResult(&model.Task{
		Status:     model.TaskStatusFailure,
		Group:      "a",
		SubmitTime: now - 60,
		FinishTime: now,
		Properties: model.Properties{OriginModelName: "video-model"},
	}, relaycommon.FailTaskInfo("boom"))
	RecordTaskResult(&model.Task{Status: model.TaskStatusSuccess, FinishTime: now}, nil)

	merged := map[bucketKey]counters{}
	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		require.Equal(t, "video-model", k.model)
		require.Equal(t, "a", k.group)
		k.bucketTs = 0
		mergeCounters(merged, k, value.(*atomicBucket).snapshot())
		return true
	})
	assert.Equal(t, counters{
		requestCount:   2,
		successCount:   1,
		totalLatencyMs: 180000,
		outputTokens:   5000,
		generationMs:   100000,
	}, merged[bucketKey{model: "video-model", group: "a"}])
}

func TestRecordTaskResultRefusesIdentityWithoutPrimaryKey(t *testing.T) {
	hotBuckets.Clear()
	t.Cleanup(func() { hotBuckets.Clear() })
	now := time.Now().Unix()
	var logs []string
	restore := common.SetSysLogSink(func(level, message string) {
		if level == "ERROR" {
			logs = append(logs, message)
		}
	})
	defer restore()
	RecordTaskResult(&model.Task{
		TaskID:     "task-no-pk",
		Status:     model.TaskStatusSuccess,
		Group:      "a",
		SubmitTime: now - 10,
		FinishTime: now,
		Properties: model.Properties{OriginModelName: "video-no-pk"},
	}, &relaycommon.TaskInfo{TotalTokens: 9})
	assert.Zero(t, hotCounters(t, "video-no-pk", "a").requestCount)
	require.NotEmpty(t, logs)
	assert.Contains(t, strings.Join(logs, "\n"), "task_id=task-no-pk")
	assert.Contains(t, strings.Join(logs, "\n"), "no primary key")
}

func TestRecordTaskResultDeduplicatesRecoveredTask(t *testing.T) {
	db := openTaskSampleDB(t)
	hotBuckets.Clear()
	oldNow := taskSampleNow
	oldRedis, oldClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = false, nil
	t.Cleanup(func() {
		hotBuckets.Clear()
		taskSampleNow = oldNow
		common.RedisEnabled, common.RDB = oldRedis, oldClient
	})
	firstBucket := time.Unix(1_700_000_000, 0)
	taskSampleNow = func() time.Time { return firstBucket }
	now := firstBucket.Unix()
	task := &model.Task{
		TaskID:     "task-recover-901",
		ChannelId:  12,
		Status:     model.TaskStatusSuccess,
		Group:      "a",
		SubmitTime: now - 120,
		StartTime:  now - 100,
		FinishTime: now,
		Data:       json.RawMessage(`{}`),
		Properties: model.Properties{OriginModelName: "video-recover-model"},
	}
	require.NoError(t, db.Create(task).Error)
	info := &relaycommon.TaskInfo{TotalTokens: 3000}
	RecordTaskResult(task, info)

	var stored model.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.NotZero(t, stored.PerfSampledAt)
	stale := stored
	stale.PerfSampledAt = 0
	stale.FailReason = "stale full-row update"
	require.NoError(t, db.Model(&model.Task{}).Where("id = ?", task.ID).Select("*").Updates(&stale).Error)
	var kept model.Task
	require.NoError(t, db.First(&kept, task.ID).Error)
	assert.Equal(t, "stale full-row update", kept.FailReason)
	assert.Equal(t, stored.PerfSampledAt, kept.PerfSampledAt)
	assert.Equal(t, counters{
		requestCount:   1,
		successCount:   1,
		totalLatencyMs: 120000,
		outputTokens:   3000,
		generationMs:   100000,
	}, hotCounters(t, "video-recover-model", "a"))

	// Recovery reloads the row and must not count again, even if the in-memory copy forgot the marker.
	replay := stored
	replay.PerfSampledAt = 0
	RecordTaskResult(&replay, info)
	assert.Equal(t, stored.PerfSampledAt, replay.PerfSampledAt)
	assert.Equal(t, int64(1), hotCounters(t, "video-recover-model", "a").requestCount)

	// A cleared hot bucket and a later time bucket must not count the same terminal task again.
	hotBuckets.Clear()
	taskSampleNow = func() time.Time { return firstBucket.Add(2 * time.Hour) }
	later := replay
	later.PerfSampledAt = 0
	RecordTaskResult(&later, info)
	assert.Equal(t, stored.PerfSampledAt, later.PerfSampledAt)
	assert.Zero(t, hotCounters(t, "video-recover-model", "a").requestCount)
	var marked model.Task
	require.NoError(t, db.First(&marked, task.ID).Error)
	assert.Equal(t, stored.PerfSampledAt, marked.PerfSampledAt)
}

func TestRecordTaskResultDoesNotMarkWhenRecordFails(t *testing.T) {
	db := openTaskSampleDB(t)
	hotBuckets.Clear()
	oldNow := taskSampleNow
	oldWrite := writeTaskSampleMark
	oldRedis, oldClient := common.RedisEnabled, common.RDB
	taskSampleNow = func() time.Time { return time.Unix(1_700_003_600, 0) }
	t.Cleanup(func() {
		hotBuckets.Clear()
		taskSampleNow = oldNow
		writeTaskSampleMark = oldWrite
		common.RedisEnabled, common.RDB = oldRedis, oldClient
	})
	now := taskSampleNow().Unix()
	task := &model.Task{
		TaskID:     "task-mark-after-record",
		ChannelId:  4,
		Status:     model.TaskStatusFailure,
		Group:      "a",
		SubmitTime: now - 60,
		FinishTime: now,
		Data:       json.RawMessage(`{}`),
		Properties: model.Properties{OriginModelName: "video-mark-order"},
	}
	require.NoError(t, db.Create(task).Error)
	info := relaycommon.FailTaskInfo("boom")

	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() {
		if common.RDB != nil {
			_ = common.RDB.Close()
		}
	})
	var logs []string
	restore := common.SetSysLogSink(func(level, message string) {
		if level == "ERROR" {
			logs = append(logs, message)
		}
	})
	defer restore()
	RecordTaskResult(task, info)
	require.NotEmpty(t, logs)
	assert.Contains(t, logs[len(logs)-1], "id="+fmt.Sprint(task.ID))
	assert.Zero(t, hotCounters(t, "video-mark-order", "a").requestCount)
	var unmarked model.Task
	require.NoError(t, db.First(&unmarked, task.ID).Error)
	assert.Zero(t, unmarked.PerfSampledAt)

	live := miniredis.RunT(t)
	require.NoError(t, common.RDB.Close())
	common.RDB = redis.NewClient(&redis.Options{Addr: live.Addr()})
	firstKey := redisBucketKey(bucketKey{model: "video-mark-order", group: "a", bucketTs: bucketStart(now)})
	RecordTaskResult(task, info)
	assert.Equal(t, counters{requestCount: 1, totalLatencyMs: 60000}, hotCounters(t, "video-mark-order", "a"))
	require.NoError(t, db.First(&unmarked, task.ID).Error)
	require.NotZero(t, unmarked.PerfSampledAt)
	values, err := common.RDB.HGetAll(context.Background(), firstKey).Result()
	require.NoError(t, err)
	assert.Equal(t, "1", values["req"])
	assert.Empty(t, values["ok"])
	assert.Equal(t, "60000", values["lat"])

	hotBuckets.Clear()
	laterAt := time.Unix(now+7200, 0)
	taskSampleNow = func() time.Time { return laterAt }
	laterKey := redisBucketKey(bucketKey{model: "video-mark-order", group: "a", bucketTs: bucketStart(laterAt.Unix())})
	replay := unmarked
	replay.PerfSampledAt = 0
	RecordTaskResult(&replay, info)
	assert.Zero(t, hotCounters(t, "video-mark-order", "a").requestCount)
	exists, err := common.RDB.Exists(context.Background(), laterKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists)
	values, err = common.RDB.HGetAll(context.Background(), firstKey).Result()
	require.NoError(t, err)
	assert.Equal(t, "1", values["req"])

	writeTaskSampleMark = func(tx *gorm.DB, id int64, sampledAt int64) (int64, error) {
		return 0, errors.New("marker write failed")
	}
	other := &model.Task{
		TaskID:     "task-mark-write-fails",
		ChannelId:  4,
		Status:     model.TaskStatusSuccess,
		Group:      "a",
		SubmitTime: now - 30,
		StartTime:  now - 20,
		FinishTime: now,
		Data:       json.RawMessage(`{}`),
		Properties: model.Properties{OriginModelName: "video-mark-write"},
	}
	require.NoError(t, db.Create(other).Error)
	logs = nil
	RecordTaskResult(other, &relaycommon.TaskInfo{TotalTokens: 100})
	require.NotEmpty(t, logs)
	assert.Contains(t, logs[len(logs)-1], "marker write failed")
	assert.Zero(t, hotCounters(t, "video-mark-write", "a").requestCount)
	writeKey := redisBucketKey(bucketKey{model: "video-mark-write", group: "a", bucketTs: bucketStart(taskSampleNow().Unix())})
	failedValues, err := common.RDB.HGetAll(context.Background(), writeKey).Result()
	require.NoError(t, err)
	assert.Contains(t, []string{"", "0"}, failedValues["req"])
	var notMarked model.Task
	require.NoError(t, db.First(&notMarked, other.ID).Error)
	assert.Zero(t, notMarked.PerfSampledAt)
	writeTaskSampleMark = oldWrite
	RecordTaskResult(other, &relaycommon.TaskInfo{TotalTokens: 100})
	assert.Equal(t, int64(1), hotCounters(t, "video-mark-write", "a").requestCount)
	retriedValues, err := common.RDB.HGetAll(context.Background(), writeKey).Result()
	require.NoError(t, err)
	assert.Equal(t, "1", retriedValues["req"])
	require.NoError(t, db.First(&notMarked, other.ID).Error)
	assert.NotZero(t, notMarked.PerfSampledAt)
}

func TestRecordTaskResultMissingColumnDoesNotAddColumn(t *testing.T) {
	db := openTaskSampleDB(t)
	hotBuckets.Clear()
	oldRedis, oldClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = false, nil
	t.Cleanup(func() {
		hotBuckets.Clear()
		common.RedisEnabled, common.RDB = oldRedis, oldClient
	})
	now := time.Now().Unix()
	task := &model.Task{
		TaskID:     "task-missing-column",
		ChannelId:  9,
		Status:     model.TaskStatusSuccess,
		Group:      "a",
		SubmitTime: now - 10,
		FinishTime: now,
		Data:       json.RawMessage(`{}`),
		Properties: model.Properties{OriginModelName: "video-missing-column"},
	}
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, db.Exec("ALTER TABLE tasks DROP COLUMN perf_sampled_at").Error)
	require.False(t, db.Migrator().HasColumn(&model.Task{}, "perf_sampled_at"))
	require.False(t, db.Migrator().HasColumn(&model.Task{}, "PerfSampledAt"))

	var logs []string
	restore := common.SetSysLogSink(func(level, message string) {
		if level == "ERROR" {
			logs = append(logs, message)
		}
	})
	defer restore()
	RecordTaskResult(task, &relaycommon.TaskInfo{TotalTokens: 40})

	require.False(t, db.Migrator().HasColumn(&model.Task{}, "perf_sampled_at"))
	require.False(t, db.Migrator().HasColumn(&model.Task{}, "PerfSampledAt"))
	assert.Zero(t, hotCounters(t, "video-missing-column", "a").requestCount)
	require.NotEmpty(t, logs)
	assert.Contains(t, strings.Join(logs, "\n"), "tasks.perf_sampled_at is missing")
	assert.Contains(t, strings.Join(logs, "\n"), "id="+fmt.Sprint(task.ID))
}

func openTaskSampleDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tasks.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous })
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	return db
}

func hotCounters(t *testing.T, modelName, group string) counters {
	t.Helper()
	merged := map[bucketKey]counters{}
	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.model != modelName || k.group != group {
			return true
		}
		k.bucketTs = 0
		mergeCounters(merged, k, value.(*atomicBucket).snapshot())
		return true
	})
	return merged[bucketKey{model: modelName, group: group}]
}

// TEST_PERF_MYSQL_DSN / TEST_PERF_POSTGRES_DSN optionally run the aggregation
// against isolated real MySQL/PostgreSQL databases.
func TestPerformanceAggregationAndFlush(t *testing.T) {
	for _, dialect := range []struct{ name, env string }{
		{"sqlite", ""}, {"mysql", "TEST_PERF_MYSQL_DSN"}, {"postgres", "TEST_PERF_POSTGRES_DSN"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			dsn := ""
			if dialect.env != "" {
				dsn = os.Getenv(dialect.env)
				if dsn == "" {
					t.Skip("isolated test database DSN is not configured")
				}
			}
			t.Setenv("SQL_DSN", dsn)
			oldDB, oldPath, oldMaster, oldRedis := model.DB, common.SQLitePath, common.IsMasterNode, common.RedisEnabled
			oldType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			common.SQLitePath, common.IsMasterNode, common.RedisEnabled = filepath.Join(t.TempDir(), "perf.db"), false, false
			hotBuckets.Clear()
			t.Cleanup(func() {
				model.DB, common.SQLitePath, common.IsMasterNode, common.RedisEnabled = oldDB, oldPath, oldMaster, oldRedis
				common.SetDatabaseTypes(oldType, oldLogType)
				hotBuckets.Clear()
			})
			require.NoError(t, model.InitDB())
			db := model.DB
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.Migrator().DropTable(&model.PerfMetric{}))
			require.NoError(t, db.AutoMigrate(&model.PerfMetric{}))
			versionQuery := "SELECT version()"
			if dialect.name == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			now := time.Now()
			start, _ := queryWindow(now, 24)
			hour := now.Unix() - now.Unix()%3600 - 3600
			// Historical counters remain usable without reclassification or migration.
			for _, row := range []model.PerfMetric{
				{ModelName: "test-model", Group: "a", BucketTs: hour, RequestCount: 100, SuccessCount: 100, TotalLatencyMs: 100000, TtftCount: 100, TtftSumMs: 10000, OutputTokens: 200, GenerationMs: 40000},
				{ModelName: "test-model", Group: "inactive", BucketTs: hour, RequestCount: 100},
				{ModelName: "test-model", Group: "a", BucketTs: start - 3600, RequestCount: 100},
			} {
				require.NoError(t, model.UpsertPerfMetric(&row))
			}
			groups := []string{"a", "b"}

			RecordRelayResult(context.Background(), &relaycommon.RelayInfo{OriginModelName: "test-model", UsingGroup: "b", StartTime: now}, types.InitOpenAIError("unknown", 400))
			businessRejected, err := QuerySummaryAll(24, groups)
			require.NoError(t, err)
			require.NotNil(t, businessRejected.Summary)
			assert.Equal(t, 100.0, businessRejected.Summary.SuccessRate)

			failure := &atomicBucket{}
			failure.add(Sample{LatencyMs: 2000})
			hotBuckets.Store(bucketKey{model: "test-model", group: "b", bucketTs: hour}, failure)
			before, err := Query(QueryParams{Model: "test-model", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			require.NotNil(t, before.Summary)
			assert.Equal(t, Summary{SuccessRate: 99.01, AvgLatencyMs: 1009, AvgTps: 5}, *before.Summary)
			assert.Equal(t, start, before.WindowStart)
			require.Len(t, before.Series, 1)
			assert.Equal(t, hour, before.Series[0].Ts)
			assert.InDelta(t, 99.01, before.Series[0].SuccessRate, 0.01)
			require.Len(t, before.Groups, 2)

			summary, err := QuerySummaryAll(24, groups)
			require.NoError(t, err)
			assert.Equal(t, before.Summary, summary.Summary)
			require.Len(t, summary.Models, 1)
			assert.Equal(t, 99.01, summary.Models[0].SuccessRate)
			assert.Equal(t, 99.01, summary.Models[0].RecentSuccessSeries[0].SuccessRate)
			encoded, err := common.Marshal(summary)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "request_count")
			assert.NotContains(t, string(encoded), "success_count")

			flushCompletedBuckets()
			flushCompletedBuckets()
			after, err := Query(QueryParams{Model: "test-model", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			assert.Equal(t, before.Summary, after.Summary)
			assert.Equal(t, before.Series, after.Series)

			RecordRelayResult(context.Background(), &relaycommon.RelayInfo{OriginModelName: "test-model", UsingGroup: "a", StartTime: now}, types.InitOpenAIError("context_length_exceeded", 400))
			after, err = Query(QueryParams{Model: "test-model", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			assert.Equal(t, before.Summary, after.Summary)
			onlyA, err := Query(QueryParams{Model: "test-model", Group: "a", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			assert.Equal(t, 100.0, onlyA.Summary.SuccessRate)
			empty, err := Query(QueryParams{Model: "missing", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			assert.Nil(t, empty.Summary)
			assert.Empty(t, empty.Series)

			require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{ModelName: "second-model", Group: "a", BucketTs: hour, RequestCount: 1}))
			combined, err := QuerySummaryAll(24, groups)
			require.NoError(t, err)
			assert.Equal(t, 98.04, combined.Summary.SuccessRate)
			assert.Equal(t, 99.01, combined.Models[0].SuccessRate)

			t.Run("recent models are summarized over their status sample hours", func(t *testing.T) {
				for i := range 26 {
					require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{
						ModelName: "history-model", Group: "a", BucketTs: hour - int64(i+1)*6*3600,
						RequestCount: 1, SuccessCount: 1,
					}))
				}
				for _, row := range []model.PerfMetric{
					{ModelName: "history-model", Group: "a", BucketTs: hour + 60, RequestCount: 100, SuccessCount: 100},
					{ModelName: "history-model", Group: "inactive", BucketTs: hour + 120, RequestCount: 100},
					{ModelName: "history-model", Group: "a", BucketTs: hour + 2*3600, RequestCount: 1},
					{ModelName: "history-idle", Group: "a", BucketTs: hour - 3*24*3600, RequestCount: 1},
					{ModelName: "history-hidden", Group: "inactive", BucketTs: hour - 3*24*3600, RequestCount: 1},
				} {
					require.NoError(t, model.UpsertPerfMetric(&row))
				}
				require.NoError(t, db.Create(&model.PerfMetric{ModelName: "history-model", Group: "a", BucketTs: hour - 3600}).Error)
				hotFailure := &atomicBucket{}
				hotFailure.add(Sample{})
				hotBuckets.Store(bucketKey{model: "history-model", group: "b", bucketTs: hour + 120}, hotFailure)
				hotSuccess := &atomicBucket{}
				hotSuccess.add(Sample{Success: true})
				hotBuckets.Store(bucketKey{model: "history-hot", group: "a", bucketTs: hour}, hotSuccess)
				hiddenFailure := &atomicBucket{}
				hiddenFailure.add(Sample{})
				hotBuckets.Store(bucketKey{model: "history-hidden", group: "inactive", bucketTs: hour}, hiddenFailure)

				window, err := QuerySummaryAll(24, groups)
				require.NoError(t, err)
				history, err := QuerySummaryAll(24, groups, "history-model", "history-idle", "history-hidden", "history-hot", "missing")
				require.NoError(t, err)
				assert.Equal(t, window.WindowStart, history.WindowStart)
				summaries := lo.KeyBy(history.Models, func(m ModelSummary) string { return m.ModelName })
				assert.Equal(t, int64(124), summaries["history-model"].RequestCount)
				assert.Equal(t, 99.19, summaries["history-model"].SuccessRate)
				assert.Equal(t, int64(1), summaries["history-idle"].RequestCount)
				assert.Equal(t, 0.0, summaries["history-idle"].SuccessRate)
				assert.NotContains(t, summaries, "history-hidden")
				points := history.RecentSuccessSeries["history-model"]
				require.Len(t, points, 24)
				expected := make([]SuccessRatePoint, 0, 24)
				for i := 23; i > 0; i-- {
					expected = append(expected, SuccessRatePoint{Ts: hour - int64(i)*6*3600, SuccessRate: 100})
				}
				expected = append(expected, SuccessRatePoint{Ts: hour, SuccessRate: 99.01})
				assert.Equal(t, expected, points)
				assert.Equal(t, []SuccessRatePoint{{Ts: hour - 3*24*3600, SuccessRate: 0}}, history.RecentSuccessSeries["history-idle"])
				assert.Equal(t, []SuccessRatePoint{{Ts: hour, SuccessRate: 100}}, history.RecentSuccessSeries["history-hot"])
				assert.Empty(t, history.RecentSuccessSeries["history-hidden"])
				assert.Empty(t, history.RecentSuccessSeries["missing"])

				denied, err := QuerySummaryAll(24, []string{}, "history-model", "history-hidden")
				require.NoError(t, err)
				assert.Empty(t, denied.RecentSuccessSeries)
				flushCompletedBuckets()
				persisted, err := QuerySummaryAll(24, groups, "history-model", "history-hot")
				require.NoError(t, err)
				assert.Equal(t, points, persisted.RecentSuccessSeries["history-model"])
				assert.Equal(t, history.RecentSuccessSeries["history-hot"], persisted.RecentSuccessSeries["history-hot"])
			})

			t.Run("recent history stops at the 7 day boundary even with fewer than 24 samples", func(t *testing.T) {
				historyStart, _ := queryWindow(now, 7*24)
				for _, row := range []model.PerfMetric{
					{ModelName: "history-boundary", Group: "a", BucketTs: historyStart, RequestCount: 1, SuccessCount: 1},
					{ModelName: "history-boundary", Group: "a", BucketTs: historyStart - 3600, RequestCount: 1},
					{ModelName: "history-expired", Group: "a", BucketTs: historyStart - 3600, RequestCount: 1},
				} {
					require.NoError(t, model.UpsertPerfMetric(&row))
				}
				hotExpired := &atomicBucket{}
				hotExpired.add(Sample{Success: true})
				hotBuckets.Store(bucketKey{model: "history-hot-expired", group: "a", bucketTs: historyStart - 3600}, hotExpired)
				hotBoundary := &atomicBucket{}
				hotBoundary.add(Sample{Success: true})
				hotBuckets.Store(bucketKey{model: "history-hot-boundary", group: "a", bucketTs: historyStart}, hotBoundary)

				history, err := QuerySummaryAll(24, groups, "history-boundary", "history-expired", "history-hot-expired", "history-hot-boundary")
				require.NoError(t, err)
				assert.Equal(t, []SuccessRatePoint{{Ts: historyStart, SuccessRate: 100}}, history.RecentSuccessSeries["history-boundary"])
				assert.Equal(t, []SuccessRatePoint{{Ts: historyStart, SuccessRate: 100}}, history.RecentSuccessSeries["history-hot-boundary"])
				assert.Empty(t, history.RecentSuccessSeries["history-expired"])
				assert.Empty(t, history.RecentSuccessSeries["history-hot-expired"])
			})
		})
	}
}
