package perfmetrics

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

var hotBuckets sync.Map

// taskSampleMu only serializes the check, record, and mark steps in this process.
// It does not remember which tasks were sampled. The durable dedup is tasks.perf_sampled_at.
var taskSampleMu sync.Mutex

// taskSampleNow is the clock for a task sample's bucket. Tests move it to prove a
// later bucket does not count the same terminal task again.
var taskSampleNow = time.Now

// SetTaskSampleNowForTest moves the task-sample clock. A nil function restores time.Now.
// Production sampling does not call this.
func SetTaskSampleNowForTest(now func() time.Time) {
	if now == nil {
		taskSampleNow = time.Now
		return
	}
	taskSampleNow = now
}

// seriesSchema is a stable client cache/schema marker. Do not change it when
// hiding fields or making response-only privacy hardening changes.
const seriesSchema = "dbcd0a3c01b55203"

// MaxRecentModels bounds the historical lookups made by one summary request.
const MaxRecentModels = 50
const recentSuccessSampleLimit = 24
const recentSuccessHistoryHours = 7 * 24

func Init() {
	go flushLoop()
}

// GroupHealthObserver receives one finished relay classification. It must not change routing.
var GroupHealthObserver func(group, modelName string, userId int, outcome Outcome)

// RecordRelayResult samples one finished relay exactly once, at the request
// boundary, regardless of how many channel attempts it took.
func RecordRelayResult(ctx context.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) {
	if info == nil {
		return
	}
	outcome := ClassifyRelayOutcome(ctx, info, apiErr)
	if GroupHealthObserver != nil {
		GroupHealthObserver(info.UsingGroup, info.OriginModelName, info.UserId, outcome)
	}
	if outcome == OutcomeIgnored {
		return
	}
	now := time.Now()
	hasTtft := info.IsStream && info.HasSendResponse()
	ttftMs := int64(0)
	if hasTtft {
		ttftMs = info.FirstResponseTime.Sub(info.StartTime).Milliseconds()
	}
	latencyMs := now.Sub(info.StartTime).Milliseconds()
	generationMs := latencyMs
	if hasTtft {
		generationMs = now.Sub(info.FirstResponseTime).Milliseconds()
	}
	if generationMs <= 0 {
		generationMs = latencyMs
	}
	Record(Sample{
		Model:        info.OriginModelName,
		Group:        info.UsingGroup,
		LatencyMs:    latencyMs,
		TtftMs:       ttftMs,
		HasTtft:      hasTtft,
		Success:      outcome == OutcomeSuccess,
		OutputTokens: info.PerformanceOutputTokens,
		GenerationMs: generationMs,
	})
}

// RecordTaskResult samples one async task exactly once, at its terminal
// transition, after the polling status CAS is won. Latency is the end-to-end
// task duration (second resolution, unlike the ms-resolution relay samples);
// throughput is only present when the provider reports tokens.
//
// Recovery replays this and still settles billing. Dedup is tasks.perf_sampled_at,
// written only after the sample commits. A process-local map is not a dedup store.
// A missing column is logged and not created here; the task is not sampled.
func RecordTaskResult(task *model.Task, result *relaycommon.TaskInfo) {
	if task == nil {
		return
	}
	if task.Status != model.TaskStatusSuccess && task.Status != model.TaskStatusFailure {
		return
	}
	modelName := task.Properties.OriginModelName
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		modelName = bc.OriginModelName
	}
	setting := perf_metrics_setting.GetSetting()
	if !setting.Enabled || modelName == "" {
		return
	}
	group := task.Group
	if group == "" {
		group = "default"
	}
	endTs := task.FinishTime
	if endTs <= 0 {
		endTs = taskSampleNow().Unix()
	}
	sample := Sample{
		Model:   modelName,
		Group:   group,
		Success: task.Status == model.TaskStatusSuccess,
	}
	if task.SubmitTime > 0 && endTs > task.SubmitTime {
		sample.LatencyMs = (endTs - task.SubmitTime) * 1000
	}
	if sample.Success && result != nil {
		tokens := int64(cmp.Or(result.TotalTokens, result.CompletionTokens))
		genStart := cmp.Or(task.StartTime, task.SubmitTime)
		if tokens > 0 && genStart > 0 && endTs > genStart {
			sample.OutputTokens = tokens
			sample.GenerationMs = (endTs - genStart) * 1000
		}
	}
	if err := sampleTerminalTaskOnce(task, sample); err != nil {
		common.SysError(common.LogText("task performance sample was not recorded for %s: %s", taskSampleLogID(task), err.Error()))
	}
}

// taskPerfMark is the row lock target. It is not a second schema for the column.
type taskPerfMark struct {
	ID            int64 `gorm:"column:id"`
	PerfSampledAt int64 `gorm:"column:perf_sampled_at"`
}

type committedTaskSample struct {
	key          bucketKey
	sample       Sample
	redisWritten bool
}

// writeTaskSampleMark stores the durable marker. model.Task.PerfSampledAt is
// GORM-readonly, so a field Update would not write it and a later full-row
// update cannot clear it. Tests replace this to prove a failed write does not
// keep the sample and does not leave the marker set.
var writeTaskSampleMark = func(tx *gorm.DB, id int64, sampledAt int64) (int64, error) {
	res := tx.Exec(`UPDATE tasks SET perf_sampled_at = ? WHERE id = ? AND perf_sampled_at = 0`, sampledAt, id)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

func taskSampleLogID(task *model.Task) string {
	if task == nil {
		return "nil"
	}
	if task.ID > 0 {
		return fmt.Sprintf("id=%d", task.ID)
	}
	if tid := strings.TrimSpace(task.TaskID); tid != "" {
		return "task_id=" + tid
	}
	return "unpersisted"
}

func taskSampleNeedsDurableMark(task *model.Task) bool {
	if task == nil {
		return false
	}
	if task.ID > 0 || strings.TrimSpace(task.TaskID) != "" {
		return true
	}
	return strings.TrimSpace(task.PrivateData.BillingOperationKey) != ""
}

func perfSampleColumnError(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is unavailable")
	}
	if !db.Migrator().HasTable(&model.Task{}) && !db.Migrator().HasTable("tasks") {
		return errors.New("tasks table is missing")
	}
	if db.Migrator().HasColumn(&model.Task{}, "PerfSampledAt") || db.Migrator().HasColumn(&model.Task{}, "perf_sampled_at") {
		return nil
	}
	// The sampling path must not create the column. The formal migration owns that.
	return errors.New("tasks.perf_sampled_at is missing")
}

func taskSampleUsesRowLock(tx *gorm.DB) bool {
	if tx == nil || tx.Dialector == nil || tx.Dialector.Name() == "sqlite" {
		return false
	}
	// AdtyLockForUpdate also skips SQLite by the process-wide database type.
	// The dialector check covers a sampler test that opens its own SQLite file.
	return !common.UsingMainDatabase(common.DatabaseTypeSQLite)
}

func loadTaskPerfMark(tx *gorm.DB, id int64) (taskPerfMark, error) {
	query := "SELECT id, perf_sampled_at FROM tasks WHERE id = ?"
	if taskSampleUsesRowLock(tx) {
		query += " FOR UPDATE"
	}
	var mark taskPerfMark
	err := tx.Raw(query, id).Row().Scan(&mark.ID, &mark.PerfSampledAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, gorm.ErrRecordNotFound) {
			return mark, fmt.Errorf("task %d row is missing, refusing to sample without a durable marker", id)
		}
		return mark, err
	}
	if mark.ID == 0 {
		return mark, fmt.Errorf("task %d row is missing, refusing to sample without a durable marker", id)
	}
	return mark, nil
}

func sampleTerminalTaskOnce(task *model.Task, sample Sample) error {
	if !taskSampleNeedsDurableMark(task) {
		Record(sample)
		return nil
	}
	taskSampleMu.Lock()
	defer taskSampleMu.Unlock()
	if task.ID <= 0 {
		return errors.New("task has no primary key, refusing to sample without a durable marker")
	}
	if err := perfSampleColumnError(model.DB); err != nil {
		return err
	}
	var live *committedTaskSample
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		mark, err := loadTaskPerfMark(tx, task.ID)
		if err != nil {
			return err
		}
		if mark.PerfSampledAt > 0 {
			task.PerfSampledAt = mark.PerfSampledAt
			return nil
		}
		committed, err := commitTaskSample(sample)
		if err != nil {
			return err
		}
		live = &committed
		now := taskSampleNow().Unix()
		if now <= 0 {
			now = 1
		}
		affected, err := writeTaskSampleMark(tx, mark.ID, now)
		if err != nil || affected == 0 {
			undoErr := undoTaskSample(committed)
			live = nil
			if err == nil {
				err = errors.New("perf sample marker was not stored")
			}
			if undoErr != nil {
				return fmt.Errorf("%w; also failed to roll back the sample: %v", err, undoErr)
			}
			return err
		}
		task.PerfSampledAt = now
		return nil
	})
	if err != nil {
		task.PerfSampledAt = 0
		if live != nil {
			if undoErr := undoTaskSample(*live); undoErr != nil {
				return fmt.Errorf("%w; also failed to roll back the sample: %v", err, undoErr)
			}
		}
		return err
	}
	return nil
}

func commitTaskSample(sample Sample) (committedTaskSample, error) {
	setting := perf_metrics_setting.GetSetting()
	if !setting.Enabled || sample.Model == "" {
		return committedTaskSample{}, errors.New("performance sampling is disabled")
	}
	if sample.Group == "" {
		sample.Group = "default"
	}
	if sample.LatencyMs < 0 {
		sample.LatencyMs = 0
	}
	key := bucketKey{
		model:    sample.Model,
		group:    sample.Group,
		bucketTs: bucketStart(taskSampleNow().Unix()),
	}
	wroteRedis := false
	if common.RedisEnabled {
		if common.RDB == nil {
			return committedTaskSample{}, errors.New("redis is enabled but the client is nil")
		}
		if err := adjustRedis(key, sample, 1); err != nil {
			return committedTaskSample{}, err
		}
		wroteRedis = true
	}
	addHotSample(key, sample)
	return committedTaskSample{key: key, sample: sample, redisWritten: wroteRedis}, nil
}

func undoTaskSample(committed committedTaskSample) error {
	hotErr := subtractHotSample(committed.key, committed.sample)
	var redisErr error
	if committed.redisWritten {
		redisErr = adjustRedis(committed.key, committed.sample, -1)
	}
	if hotErr != nil && redisErr != nil {
		return fmt.Errorf("%v; redis rollback: %v", hotErr, redisErr)
	}
	if hotErr != nil {
		return hotErr
	}
	return redisErr
}

func Record(sample Sample) {
	setting := perf_metrics_setting.GetSetting()
	if !setting.Enabled || sample.Model == "" {
		return
	}
	if sample.Group == "" {
		sample.Group = "default"
	}
	if sample.LatencyMs < 0 {
		sample.LatencyMs = 0
	}

	key := bucketKey{
		model:    sample.Model,
		group:    sample.Group,
		bucketTs: bucketStart(time.Now().Unix()),
	}
	addHotSample(key, sample)
	// Relay samples stay best-effort. Task samples do not use this path; they
	// wait for Redis and only then store tasks.perf_sampled_at.
	gopool.Go(func() {
		_ = adjustRedis(key, sample, 1)
	})
}

func addHotSample(key bucketKey, sample Sample) {
	actual, _ := hotBuckets.LoadOrStore(key, &atomicBucket{})
	applyHotDelta(actual.(*atomicBucket), sample, 1)
}

func subtractHotSample(key bucketKey, sample Sample) error {
	value, ok := hotBuckets.Load(key)
	if !ok {
		return fmt.Errorf("hot bucket %s/%s/%d is gone; sample was not rolled back", key.model, key.group, key.bucketTs)
	}
	applyHotDelta(value.(*atomicBucket), sample, -1)
	return nil
}

// applyHotDelta must match the fields atomicBucket.add counts. Sign is 1 or -1.
func applyHotDelta(bucket *atomicBucket, sample Sample, sign int64) {
	bucket.requestCount.Add(sign)
	if sample.Success {
		bucket.successCount.Add(sign)
	}
	if sample.LatencyMs > 0 {
		bucket.totalLatencyMs.Add(sample.LatencyMs * sign)
	}
	if sample.HasTtft && sample.TtftMs >= 0 {
		bucket.ttftSumMs.Add(sample.TtftMs * sign)
		bucket.ttftCount.Add(sign)
	}
	if sample.OutputTokens > 0 && sample.GenerationMs > 0 {
		bucket.outputTokens.Add(sample.OutputTokens * sign)
		bucket.generationMs.Add(sample.GenerationMs * sign)
	}
}

// queryWindow covers the current partial hour plus the preceding complete
// hours, so every reader shares the same hourly buckets.
func queryWindow(now time.Time, hours int) (int64, int64) {
	if hours <= 0 {
		hours = 24
	}
	hours = min(hours, 24*30)
	endTs := now.Unix()
	return endTs - endTs%3600 - int64(hours-1)*3600, endTs
}

func Query(params QueryParams) (QueryResult, error) {
	startTs, endTs := queryWindow(time.Now(), params.Hours)
	allowedGroups := allowedGroupSet(params.AllowedGroups)

	merged := map[bucketKey]counters{}
	rows, err := model.GetPerfMetrics(params.Model, params.Group, startTs, endTs)
	if err != nil {
		return QueryResult{}, err
	}
	for _, row := range rows {
		if allowedGroups != nil {
			if _, ok := allowedGroups[row.Group]; !ok {
				continue
			}
		}
		mergeCounters(merged, bucketKey{
			model:    row.ModelName,
			group:    row.Group,
			bucketTs: row.BucketTs,
		}, counters{
			requestCount:   row.RequestCount,
			successCount:   row.SuccessCount,
			totalLatencyMs: row.TotalLatencyMs,
			ttftSumMs:      row.TtftSumMs,
			ttftCount:      row.TtftCount,
			outputTokens:   row.OutputTokens,
			generationMs:   row.GenerationMs,
		})
	}

	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.model != params.Model || k.bucketTs < startTs || k.bucketTs > endTs {
			return true
		}
		if params.Group != "" && k.group != params.Group {
			return true
		}
		if allowedGroups != nil {
			if _, ok := allowedGroups[k.group]; !ok {
				return true
			}
		}
		mergeCounters(merged, k, value.(*atomicBucket).snapshot())
		return true
	})

	result := buildQueryResult(params.Model, merged)
	result.WindowStart, result.WindowEnd = startTs, endTs
	return result, nil
}

func QuerySummaryAll(hours int, groups []string, recentModels ...string) (SummaryAllResult, error) {
	return QuerySummaryAllFiltered(hours, groups, nil, recentModels...)
}

func QuerySummaryAllFiltered(hours int, groups []string, allowModel func(modelName string) bool, recentModels ...string) (SummaryAllResult, error) {
	if len(recentModels) > MaxRecentModels {
		return SummaryAllResult{}, common.NewMessage("Invalid parameters")
	}
	now := time.Now()
	startTs, endTs := queryWindow(now, hours)
	recentStartTs, _ := queryWindow(now, recentSuccessHistoryHours)
	allowedGroups := allowedGroupSet(groups)
	queryStartTs := startTs

	recentBuckets := make(map[string]map[int64]counters, len(recentModels))
	for _, name := range recentModels {
		if name == "" {
			continue
		}
		if _, exists := recentBuckets[name]; exists {
			continue
		}
		recentBuckets[name] = map[int64]counters{}
		buckets, err := model.GetRecentPerfMetricSuccessBuckets(name, recentStartTs, endTs, groups, recentSuccessSampleLimit)
		if err != nil {
			return SummaryAllResult{}, err
		}
		for _, bucket := range buckets {
			queryStartTs = min(queryStartTs, bucket.BucketTs)
			mergeModelBucket(recentBuckets, name, bucket.BucketTs, counters{
				requestCount: bucket.RequestCount,
				successCount: bucket.SuccessCount,
			})
		}
	}

	rows, err := model.GetPerfMetricsSummaryBucketsAll(queryStartTs, endTs, groups)
	if err != nil {
		return SummaryAllResult{}, err
	}

	modelBuckets := map[string]map[int64]counters{}
	for _, row := range rows {
		value := counters{
			requestCount:   row.RequestCount,
			successCount:   row.SuccessCount,
			totalLatencyMs: row.TotalLatencyMs,
			outputTokens:   row.OutputTokens,
			generationMs:   row.GenerationMs,
		}
		mergeModelBucket(modelBuckets, row.ModelName, row.BucketTs, value)
	}

	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.bucketTs > endTs {
			return true
		}
		if allowedGroups != nil {
			if _, ok := allowedGroups[k.group]; !ok {
				return true
			}
		}
		snap := value.(*atomicBucket).snapshot()
		if snap.requestCount == 0 {
			return true
		}
		if _, requested := recentBuckets[k.model]; requested && k.bucketTs >= recentStartTs {
			mergeModelBucket(recentBuckets, k.model, k.bucketTs, snap)
		}
		if k.bucketTs < queryStartTs {
			return true
		}
		mergeModelBucket(modelBuckets, k.model, k.bucketTs, snap)
		return true
	})

	recentSeries := make(map[string][]SuccessRatePoint, len(recentBuckets))
	for name, buckets := range recentBuckets {
		points := recentSuccessSeries(buckets)
		if len(points) == 0 {
			continue
		}
		recentSeries[name] = points[max(0, len(points)-recentSuccessSampleLimit):]
	}

	// Requested models are summarized over the same hours as their status samples.
	totals := map[string]counters{}
	for name, buckets := range modelBuckets {
		fromTs := startTs
		if points, ok := recentSeries[name]; ok {
			fromTs = points[0].Ts
		}
		for bucketTs, value := range buckets {
			if bucketTs < fromTs {
				delete(buckets, bucketTs)
				continue
			}
			mergeModelTotals(totals, name, value)
		}
	}

	all := counters{}
	models := make([]ModelSummary, 0, len(totals))
	for name, total := range totals {
		if total.requestCount == 0 {
			continue
		}
		if allowModel != nil && !allowModel(name) {
			continue
		}
		all.requestCount += total.requestCount
		all.successCount += total.successCount
		all.totalLatencyMs += total.totalLatencyMs
		all.outputTokens += total.outputTokens
		all.generationMs += total.generationMs
		avgLatency := total.totalLatencyMs / total.requestCount
		successRate := float64(total.successCount) / float64(total.requestCount) * 100
		avgTps := 0.0
		if total.generationMs > 0 {
			avgTps = float64(total.outputTokens) / (float64(total.generationMs) / 1000.0)
		}
		models = append(models, ModelSummary{
			ModelName:           name,
			AvgLatencyMs:        avgLatency,
			SuccessRate:         math.Round(successRate*100) / 100,
			AvgTps:              math.Round(avgTps*100) / 100,
			RecentSuccessSeries: recentSuccessSeries(modelBuckets[name]),
			RequestCount:        total.requestCount,
		})
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].RequestCount > models[j].RequestCount
	})

	return SummaryAllResult{
		Summary: summarize(all), WindowStart: startTs, WindowEnd: endTs,
		Models: models, RecentSuccessSeries: recentSeries,
	}, nil
}

func mergeModelTotals(totals map[string]counters, modelName string, value counters) {
	if value.requestCount == 0 {
		return
	}
	current := totals[modelName]
	current.requestCount += value.requestCount
	current.successCount += value.successCount
	current.totalLatencyMs += value.totalLatencyMs
	current.ttftSumMs += value.ttftSumMs
	current.ttftCount += value.ttftCount
	current.outputTokens += value.outputTokens
	current.generationMs += value.generationMs
	totals[modelName] = current
}

func mergeModelBucket(modelBuckets map[string]map[int64]counters, modelName string, bucketTs int64, value counters) {
	if value.requestCount == 0 {
		return
	}
	if _, ok := modelBuckets[modelName]; !ok {
		modelBuckets[modelName] = map[int64]counters{}
	}
	current := modelBuckets[modelName][bucketTs]
	current.requestCount += value.requestCount
	current.successCount += value.successCount
	current.totalLatencyMs += value.totalLatencyMs
	current.ttftSumMs += value.ttftSumMs
	current.ttftCount += value.ttftCount
	current.outputTokens += value.outputTokens
	current.generationMs += value.generationMs
	modelBuckets[modelName][bucketTs] = current
}

func recentSuccessSeries(buckets map[int64]counters) []SuccessRatePoint {
	if len(buckets) == 0 {
		return nil
	}
	hourly := map[int64]counters{}
	for ts, value := range buckets {
		hourTs := ts - ts%3600
		merged := hourly[hourTs]
		merged.requestCount += value.requestCount
		merged.successCount += value.successCount
		hourly[hourTs] = merged
	}
	timestamps := make([]int64, 0, len(hourly))
	for hourTs, value := range hourly {
		if value.requestCount == 0 {
			continue
		}
		timestamps = append(timestamps, hourTs)
	}
	if len(timestamps) == 0 {
		return nil
	}
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i] < timestamps[j]
	})
	points := make([]SuccessRatePoint, 0, len(timestamps))
	for _, hourTs := range timestamps {
		points = append(points, SuccessRatePoint{
			Ts:          hourTs,
			SuccessRate: math.Round(successRate(hourly[hourTs])*100) / 100,
		})
	}
	return points
}

func allowedGroupSet(groups []string) map[string]struct{} {
	if groups == nil {
		return nil
	}
	allowed := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		allowed[group] = struct{}{}
	}
	return allowed
}

func bucketStart(ts int64) int64 {
	bucketSeconds := perf_metrics_setting.GetBucketSeconds()
	if bucketSeconds <= 0 {
		bucketSeconds = 3600
	}
	return ts - (ts % bucketSeconds)
}

func mergeCounters(merged map[bucketKey]counters, key bucketKey, value counters) {
	if value.requestCount == 0 {
		return
	}
	current := merged[key]
	current.requestCount += value.requestCount
	current.successCount += value.successCount
	current.totalLatencyMs += value.totalLatencyMs
	current.ttftSumMs += value.ttftSumMs
	current.ttftCount += value.ttftCount
	current.outputTokens += value.outputTokens
	current.generationMs += value.generationMs
	merged[key] = current
}

func buildQueryResult(modelName string, merged map[bucketKey]counters) QueryResult {
	groupBuckets := map[string]map[int64]counters{}
	modelBuckets := map[string]map[int64]counters{}
	for key, value := range merged {
		if value.requestCount == 0 {
			continue
		}
		if _, ok := groupBuckets[key.group]; !ok {
			groupBuckets[key.group] = map[int64]counters{}
		}
		groupBuckets[key.group][key.bucketTs] = value
		mergeModelBucket(modelBuckets, modelName, key.bucketTs, value)
	}

	groups := make([]string, 0, len(groupBuckets))
	for group := range groupBuckets {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	all := counters{}
	results := make([]GroupResult, 0, len(groups))
	for _, group := range groups {
		buckets := groupBuckets[group]
		timestamps := make([]int64, 0, len(buckets))
		for ts := range buckets {
			timestamps = append(timestamps, ts)
		}
		sort.Slice(timestamps, func(i, j int) bool {
			return timestamps[i] < timestamps[j]
		})

		total := counters{}
		series := make([]BucketPoint, 0, len(timestamps))
		for _, ts := range timestamps {
			value := buckets[ts]
			total.requestCount += value.requestCount
			total.successCount += value.successCount
			total.totalLatencyMs += value.totalLatencyMs
			total.ttftSumMs += value.ttftSumMs
			total.ttftCount += value.ttftCount
			total.outputTokens += value.outputTokens
			total.generationMs += value.generationMs
			series = append(series, bucketPoint(ts, value))
		}
		all.requestCount += total.requestCount
		all.successCount += total.successCount
		all.totalLatencyMs += total.totalLatencyMs
		all.outputTokens += total.outputTokens
		all.generationMs += total.generationMs

		results = append(results, GroupResult{
			Group:        group,
			AvgTtftMs:    avg(total.ttftSumMs, total.ttftCount),
			AvgLatencyMs: avg(total.totalLatencyMs, total.requestCount),
			SuccessRate:  successRate(total),
			AvgTps:       avgTps(total),
			Series:       series,
		})
	}

	modelSeries := make([]BucketPoint, 0, len(modelBuckets[modelName]))
	for ts, value := range modelBuckets[modelName] {
		modelSeries = append(modelSeries, bucketPoint(ts, value))
	}
	sort.Slice(modelSeries, func(i, j int) bool {
		return modelSeries[i].Ts < modelSeries[j].Ts
	})

	return QueryResult{
		ModelName:    modelName,
		SeriesSchema: seriesSchema,
		Summary:      summarize(all),
		Series:       modelSeries,
		Groups:       results,
	}
}

// summarize returns nil when nothing was sampled, so clients can show
// "no data" instead of a fabricated 0% success rate.
func summarize(total counters) *Summary {
	if total.requestCount <= 0 {
		return nil
	}
	return &Summary{
		AvgLatencyMs: avg(total.totalLatencyMs, total.requestCount),
		SuccessRate:  math.Round(successRate(total)*100) / 100,
		AvgTps:       math.Round(avgTps(total)*100) / 100,
	}
}

func bucketPoint(ts int64, value counters) BucketPoint {
	return BucketPoint{
		Ts:           ts,
		AvgTtftMs:    avg(value.ttftSumMs, value.ttftCount),
		AvgLatencyMs: avg(value.totalLatencyMs, value.requestCount),
		SuccessRate:  successRate(value),
		AvgTps:       avgTps(value),
	}
}

func avg(sum int64, count int64) int64 {
	if count <= 0 {
		return 0
	}
	return sum / count
}

func successRate(value counters) float64 {
	if value.requestCount <= 0 {
		return 0
	}
	return float64(value.successCount) / float64(value.requestCount) * 100
}

func avgTps(value counters) float64 {
	if value.outputTokens <= 0 || value.generationMs <= 0 {
		return 0
	}
	return float64(value.outputTokens) / (float64(value.generationMs) / 1000)
}

func adjustRedis(key bucketKey, sample Sample, sign int64) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	redisKey := redisBucketKey(key)
	pipe := common.RDB.TxPipeline()
	pipe.HIncrBy(ctx, redisKey, "req", sign)
	if sample.Success {
		pipe.HIncrBy(ctx, redisKey, "ok", sign)
	}
	if sample.LatencyMs > 0 {
		pipe.HIncrBy(ctx, redisKey, "lat", sample.LatencyMs*sign)
	}
	if sample.HasTtft && sample.TtftMs >= 0 {
		pipe.HIncrBy(ctx, redisKey, "ttft", sample.TtftMs*sign)
		pipe.HIncrBy(ctx, redisKey, "ttft_n", sign)
	}
	if sample.OutputTokens > 0 && sample.GenerationMs > 0 {
		pipe.HIncrBy(ctx, redisKey, "out", sample.OutputTokens*sign)
		pipe.HIncrBy(ctx, redisKey, "gen_ms", sample.GenerationMs*sign)
	}
	pipe.Expire(ctx, redisKey, time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

func mergeRedisActiveBuckets(merged map[bucketKey]counters, params QueryParams, startTs int64, endTs int64) {
	if !common.RedisEnabled || common.RDB == nil || params.Model == "" || params.Group == "" {
		return
	}
	active := bucketStart(time.Now().Unix())
	if active < startTs || active > endTs {
		return
	}
	key := bucketKey{model: params.Model, group: params.Group, bucketTs: active}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	values, err := common.RDB.HGetAll(ctx, redisBucketKey(key)).Result()
	if err != nil || len(values) == 0 {
		return
	}
	mergeCounters(merged, key, redisCounters(values))
}

func redisBucketKey(key bucketKey) string {
	return fmt.Sprintf("perf:%s:%s:%d", key.model, key.group, key.bucketTs)
}
