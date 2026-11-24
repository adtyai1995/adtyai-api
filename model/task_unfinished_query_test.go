package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestUnfinishedTaskQueriesAtFullProgress(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "task_polling_test_"},
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		assert.NoError(t, db.Migrator().DropTable(&Task{}))
		assert.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&Task{}))
	assert.False(t, HasUnfinishedSyncTasks())

	const cutoff int64 = 1_800_000_000
	for _, status := range []TaskStatus{TaskStatusNotStart, TaskStatusSubmitted, TaskStatusQueued, TaskStatusInProgress, TaskStatusSuccess, TaskStatusFailure} {
		for _, progress := range []string{"99%", "100%"} {
			t.Run(string(status)+"/"+progress, func(t *testing.T) {
				task := &Task{TaskID: "query_candidate", Status: status, Progress: progress, SubmitTime: cutoff - 1}
				require.NoError(t, DB.Create(task).Error)
				t.Cleanup(func() { assert.NoError(t, db.Delete(task).Error) })
				unfinished := status != TaskStatusSuccess && status != TaskStatusFailure
				assert.Equal(t, unfinished, HasUnfinishedSyncTasks())
				if unfinished {
					require.Len(t, GetAllUnFinishSyncTasks(1), 1)
					assert.Equal(t, task.ID, GetAllUnFinishSyncTasks(1)[0].ID)
					require.Len(t, GetTimedOutUnfinishedTasks(cutoff, 1), 1)
					assert.Equal(t, task.ID, GetTimedOutUnfinishedTasks(cutoff, 1)[0].ID)
				} else {
					assert.Empty(t, GetAllUnFinishSyncTasks(1))
					assert.Empty(t, GetTimedOutUnfinishedTasks(cutoff, 1))
				}
				require.NoError(t, db.Model(task).Update("submit_time", cutoff).Error)
				assert.Empty(t, GetTimedOutUnfinishedTasks(cutoff, 1), "the timeout cutoff is exclusive")
				assert.Equal(t, unfinished, HasUnfinishedSyncTasks())
			})
		}
	}

	t.Run("preserves ADTY billing pending and review guards", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			task *Task
		}{
			{
				name: "billing_initial_pending",
				task: &Task{TaskID: "init_pending", Status: TaskStatusInProgress, Progress: "100%", SubmitTime: cutoff - 1, BillingInitialPending: true},
			},
			{
				name: "billing_pending",
				task: &Task{TaskID: "settle_pending", Status: TaskStatusInProgress, Progress: "100%", SubmitTime: cutoff - 1, BillingPending: true},
			},
			{
				name: "billing_review_reason",
				task: &Task{
					TaskID: "review_held", Status: TaskStatusInProgress, Progress: "100%", SubmitTime: cutoff - 1,
					PrivateData: TaskPrivateData{BillingReviewReason: "manual_review"},
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.NoError(t, DB.Create(tc.task).Error)
				t.Cleanup(func() { assert.NoError(t, db.Delete(tc.task).Error) })
				assert.Empty(t, GetAllUnFinishSyncTasks(10))
				assert.Empty(t, GetTimedOutUnfinishedTasks(cutoff, 10))
			})
		}
	})
}
