package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetAdtyTranslationTasks(c *gin.Context) {
	tasks, err := service.ListAdtyTranslationTasks(c.Query("status"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"tasks": tasks})
}

func PostAdtyTranslationRetry(c *gin.Context) {
	var body struct {
		ObjectKey string `json:"object_key"`
		Version   int    `json:"version"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	result, err := service.RetryAdtyTranslationJob(body.ObjectKey, body.Version)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.translation.retry", map[string]any{
		"object_key": result.ObjectKey,
		"version":    result.Version,
		"status":     result.Status,
	})
	common.ApiSuccess(c, result)
}

func PostAdtyTranslationComplete(c *gin.Context) {
	var body struct {
		ObjectKey string `json:"object_key"`
		Version   int    `json:"version"`
		English   string `json:"english"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.CompleteAdtyTranslationJob(body.ObjectKey, body.Version, body.English); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"completed": true})
}

func PutAdtyTranslationEdit(c *gin.Context) {
	var body struct {
		ObjectKey string `json:"object_key"`
		English   string `json:"english"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.EditAdtyTranslation(body.ObjectKey, body.English); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.translation.edit", map[string]any{"object_key": body.ObjectKey})
	common.ApiSuccess(c, gin.H{"edited": true})
}

func GetAdtyTranslationGlossary(c *gin.Context) {
	terms, err := service.ListAdtyGlossaryTerms(c.Query("locale"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"terms": terms})
}

func PostAdtyTranslationGlossary(c *gin.Context) {
	var body struct {
		Id     int64  `json:"id"`
		Source string `json:"source"`
		Locale string `json:"locale"`
		Target string `json:"target"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	term, err := service.UpsertAdtyGlossaryTerm(body.Id, body.Source, body.Locale, body.Target)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.translation.glossary.save", map[string]any{
		"id":     term.Id,
		"source": term.Source,
		"locale": term.Locale,
		"target": term.Target,
	})
	common.ApiSuccess(c, term)
}

func PutAdtyTranslationGlossary(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		Source string `json:"source"`
		Locale string `json:"locale"`
		Target string `json:"target"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	term, err := service.UpsertAdtyGlossaryTerm(id, body.Source, body.Locale, body.Target)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.translation.glossary.save", map[string]any{
		"id":     term.Id,
		"source": term.Source,
		"locale": term.Locale,
		"target": term.Target,
	})
	common.ApiSuccess(c, term)
}

func DeleteAdtyTranslationGlossary(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	if err := service.DeleteAdtyGlossaryTerm(id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.translation.glossary.delete", map[string]any{"id": id})
	common.ApiSuccess(c, gin.H{"deleted": true})
}
