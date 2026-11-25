package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type auditDeliveryHandler struct{}

func (auditDeliveryHandler) Type() string            { return model.SystemTaskTypeAuditDelivery }
func (auditDeliveryHandler) Enabled() bool           { return model.HasPendingAuditIntents() }
func (auditDeliveryHandler) Interval() time.Duration { return 15 * time.Second }
func (auditDeliveryHandler) NewPayload() any         { return nil }

func (auditDeliveryHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if task == nil {
		return
	}
	delivered, err := model.DeliverPendingAuditIntents(ctx, 100)
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		cancelled, checkErr := model.SystemTaskCancelRequested(task.TaskID)
		if checkErr != nil {
			common.SysLog(common.LogText("audit delivery task %s cancel check failed after context cancellation", task.TaskID))
			return
		}
		if !cancelled {
			// Context cancellation without cancel_requested indicates lease loss;
			// do not overwrite the task state.
			logSystemTaskLockError(context.Background(), task, model.ErrSystemTaskLockLost)
			return
		}
		if finishErr := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusCancelled, map[string]int{"delivered": delivered}, "cancelled"); finishErr != nil {
			common.SysLog(common.LogText("audit delivery task result could not be persisted"))
		}
		return
	}
	status, errorCategory := model.SystemTaskStatusSucceeded, ""
	if err != nil {
		status, errorCategory = model.SystemTaskStatusFailed, "audit delivery incomplete"
	} else if cancelled, checkErr := model.SystemTaskCancelRequested(task.TaskID); checkErr == nil && cancelled {
		status, errorCategory = model.SystemTaskStatusCancelled, "cancelled"
	}
	// Task rows contain counters and a fixed category, never event payloads.
	if finishErr := model.FinishSystemTask(task.TaskID, runnerID, status, map[string]int{"delivered": delivered}, errorCategory); finishErr != nil {
		common.SysLog(common.LogText("audit delivery task result could not be persisted"))
	}
}

func init() { RegisterSystemTaskHandler(auditDeliveryHandler{}) }
