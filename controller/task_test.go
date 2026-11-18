package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscardedResultTaskArtifactsReturnNotFoundAcrossSurfaces(t *testing.T) {
	// ISS-R1-0197: Immediate terminal tasks with retainResult=false (PrivateData.ResultDiscarded=true)
	// must return 404 artifact_not_found on both /v1/tasks/:key/artifacts and /api/task/:task_id/artifacts,
	// consistent with TaskArtifactContent and missing/foreign tasks, while keeping the accounting/log row.
	retained := setupGenericTaskTest(t)

	discarded := &model.Task{
		TaskID:      "task_discarded",
		Platform:    "document",
		UserId:      retained.UserId,
		ChannelId:   retained.ChannelId,
		Action:      constant.TaskActionTextToVideo,
		Status:      model.TaskStatusSuccess,
		Progress:    "100%",
		FailReason:  "https://upstream.invalid/should-not-leak.mp4",
		SubmitTime:  10,
		FinishTime:  20,
		Quota:       42,
		PrivateData: model.TaskPrivateData{ResultDiscarded: true, ResultURL: "https://upstream.invalid/should-not-leak.mp4"},
	}
	require.NoError(t, model.DB.Create(discarded).Error)

	callV1Artifacts := func(userID int, taskID string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", userID)
		c.Params = gin.Params{{Key: "key", Value: taskID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+taskID+"/artifacts", nil)
		GetTaskArtifacts(c)
		return recorder
	}

	callDashboardArtifacts := func(userID, role int, taskID string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", userID)
		c.Set("role", role)
		c.Params = gin.Params{{Key: "task_id", Value: taskID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/api/task/"+taskID+"/artifacts", nil)
		GetDashboardTaskArtifacts(c)
		return recorder
	}

	// Retained async task remains accessible (200 OK).
	retainedV1 := callV1Artifacts(retained.UserId, retained.TaskID)
	assert.Equal(t, http.StatusOK, retainedV1.Code)
	retainedDash := callDashboardArtifacts(retained.UserId, common.RoleCommonUser, retained.TaskID)
	assert.Equal(t, http.StatusOK, retainedDash.Code)

	// Missing, foreign, and ResultDiscarded tasks all return 404 artifact_not_found on /v1/tasks/:key/artifacts.
	missingV1 := callV1Artifacts(retained.UserId, "task_missing")
	foreignV1 := callV1Artifacts(999, retained.TaskID)
	discardedV1 := callV1Artifacts(discarded.UserId, discarded.TaskID)
	assert.Equal(t, http.StatusNotFound, missingV1.Code)
	assert.Equal(t, http.StatusNotFound, foreignV1.Code)
	assert.Equal(t, http.StatusNotFound, discardedV1.Code)
	assert.JSONEq(t, missingV1.Body.String(), discardedV1.Body.String())
	assert.JSONEq(t, foreignV1.Body.String(), discardedV1.Body.String())
	assert.Contains(t, discardedV1.Body.String(), `"code":"artifact_not_found"`)

	// Missing, foreign, and ResultDiscarded tasks all return 404 artifact_not_found on /api/task/:task_id/artifacts (even for owner or Root).
	missingDash := callDashboardArtifacts(retained.UserId, common.RoleCommonUser, "task_missing")
	foreignDash := callDashboardArtifacts(999, common.RoleCommonUser, retained.TaskID)
	discardedOwnerDash := callDashboardArtifacts(discarded.UserId, common.RoleCommonUser, discarded.TaskID)
	discardedRootDash := callDashboardArtifacts(1, common.RoleRootUser, discarded.TaskID)
	assert.Equal(t, http.StatusNotFound, missingDash.Code)
	assert.Equal(t, http.StatusNotFound, foreignDash.Code)
	assert.Equal(t, http.StatusNotFound, discardedOwnerDash.Code)
	assert.Equal(t, http.StatusNotFound, discardedRootDash.Code)
	assert.JSONEq(t, missingDash.Body.String(), discardedOwnerDash.Body.String())
	assert.JSONEq(t, missingDash.Body.String(), discardedRootDash.Body.String())

	// TaskArtifactContent also rejects ResultDiscarded with the same 404 response.
	contentRecorder := httptest.NewRecorder()
	contentCtx, _ := gin.CreateTestContext(contentRecorder)
	contentCtx.Set("id", discarded.UserId)
	contentCtx.Set("role", common.RoleCommonUser)
	contentCtx.Params = gin.Params{{Key: "key", Value: discarded.TaskID}, {Key: "artifact_key", Value: "video"}}
	contentCtx.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+discarded.TaskID+"/artifacts/video/content", nil)
	TaskArtifactContent(contentCtx)
	assert.Equal(t, http.StatusNotFound, contentRecorder.Code)
	assert.JSONEq(t, discardedV1.Body.String(), contentRecorder.Body.String())

	// Task accounting/log row remains visible in user/admin task lists with result_discarded=true and no leaked URL.
	userItems := model.TaskGetAllUserTask(discarded.UserId, 0, 10, model.SyncTaskQueryParams{TaskID: discarded.TaskID})
	require.Len(t, userItems, 1)
	dtos := tasksToDto(userItems, false, common.RoleCommonUser)
	require.Len(t, dtos, 1)
	assert.True(t, dtos[0].ResultDiscarded)
	assert.False(t, dtos[0].LegacyVideoAvailable)
	assert.Empty(t, dtos[0].ResultURL)
	assert.Empty(t, dtos[0].FailReason)
	assert.Equal(t, 42, dtos[0].Quota)
}
