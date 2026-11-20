package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY2304TerminalReplayLeavesMoneyOncePG(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(
		&model.Task{}, &model.Channel{}, &model.Midjourney{},
		&model.SystemTask{}, &model.SystemTaskLock{}, &model.PerfMetric{},
		&model.BillingOperation{}, &model.BillingOperationMovement{},
	))

	const userID, tokenID, channelID = 23041, 23041, 23041
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "sk-ftr2304", 10_000)

	now := time.Now().Unix()
	modelName := fmt.Sprintf("ftr-2304-%d", now)
	task := makeTask(userID, channelID, 1_000, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task_ftr2304"
	task.Status = model.TaskStatusSuccess
	task.Group = "default"
	task.SubmitTime = now - 30
	task.StartTime = now - 20
	task.FinishTime = now
	task.Properties.OriginModelName = modelName
	task.PrivateData.BillingContext.OriginModelName = modelName
	task.PrivateData.UpstreamTaskID = "upstream_ftr2304"
	seedTaskReservation(t, task)
	require.NoError(t, model.DB.Create(task).Error)

	adaptor := &scriptedPollingAdaptor{}
	result := &relaycommon.TaskInfo{Status: string(model.TaskStatusSuccess), TotalTokens: 200}
	finalizeTerminalTask(context.Background(), adaptor, task, result)

	var sampled model.Task
	require.NoError(t, db.First(&sampled, task.ID).Error)
	require.NotZero(t, sampled.PerfSampledAt)
	quotaAfter := getUserQuota(t, userID)
	tokenAfter := getTokenRemainQuota(t, tokenID)

	replay := sampled
	replay.PerfSampledAt = 0
	finalizeTerminalTask(context.Background(), adaptor, &replay, result)
	require.Equal(t, quotaAfter, getUserQuota(t, userID))
	require.Equal(t, tokenAfter, getTokenRemainQuota(t, tokenID))

	summary, err := perfmetrics.QuerySummaryAll(24, []string{"default"}, modelName)
	require.NoError(t, err)
	var requestCount int64
	for _, item := range summary.Models {
		if item.ModelName == modelName {
			requestCount = item.RequestCount
		}
	}
	require.EqualValues(t, 1, requestCount)
}
