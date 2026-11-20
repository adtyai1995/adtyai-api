package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateOptionFreezesSQLBeforeAfterForNonSensitiveAndMasksSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.AuditLog{}, &model.AuditIntent{}, &model.Log{}, &model.User{}))

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap["SystemName"] = "StaleMemoryName"
	common.OptionMap["SMTPToken"] = ""
	common.OptionMapRWMutex.Unlock()

	require.NoError(t, db.Create(&model.Option{Key: "SystemName", Value: "OldSQLName"}).Error)

	callUpdateOption := func(key, value string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		body := fmt.Sprintf(`{"key":%q,"value":%q}`, key, value)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", 1)
		c.Set("username", "root")
		c.Set("role", common.RoleRootUser)
		UpdateOption(c)
		return rec
	}

	rec1 := callUpdateOption("SystemName", "NewSQLName")
	require.Equal(t, http.StatusOK, rec1.Code, rec1.Body.String())
	require.Contains(t, rec1.Body.String(), `"success":true`)

	var sysLog model.AuditLog
	require.NoError(t, db.Where("action = ? AND object_id = ?", "option.update", "SystemName").First(&sysLog).Error)
	assert.Equal(t, "OldSQLName", sysLog.BeforeValue)
	assert.Equal(t, "NewSQLName", sysLog.AfterValue)

	rawSecret := "smtp-secret-credential-987654"
	rec2 := callUpdateOption("SMTPToken", rawSecret)
	require.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
	require.Contains(t, rec2.Body.String(), `"success":true`)

	var secretLog model.AuditLog
	require.NoError(t, db.Where("action = ? AND object_id = ?", "option.update", "SMTPToken").First(&secretLog).Error)
	assert.NotEmpty(t, secretLog.BeforeValue)
	assert.NotEmpty(t, secretLog.AfterValue)
	assert.Contains(t, secretLog.BeforeValue, `"configured":false`)
	assert.Contains(t, secretLog.BeforeValue, `"redacted":true`)
	assert.Contains(t, secretLog.AfterValue, `"configured":true`)
	assert.Contains(t, secretLog.AfterValue, `"redacted":true`)
	assert.NotContains(t, secretLog.BeforeValue, rawSecret)
	assert.NotContains(t, secretLog.AfterValue, rawSecret)
	assert.NotContains(t, secretLog.Content, rawSecret)
	encodedOther, err := common.Marshal(secretLog.Other)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedOther), rawSecret)
}

func TestUpdateOptionEpayTimeoutCrossKeyLockedValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.AuditLog{}, &model.AuditIntent{}, &model.Log{}, &model.User{}))

	oldChannel := adty_setting.GetEpaySetting().ChannelTimeoutSeconds
	oldTotal := adty_setting.GetEpaySetting().TotalTimeoutSeconds
	defer func() {
		adty_setting.GetEpaySetting().ChannelTimeoutSeconds = oldChannel
		adty_setting.GetEpaySetting().TotalTimeoutSeconds = oldTotal
	}()

	require.NoError(t, db.Create(&model.Option{Key: adty_setting.EpaySettingKeyChannelTimeout, Value: "5"}).Error)
	require.NoError(t, db.Create(&model.Option{Key: adty_setting.EpaySettingKeyTotalTimeout, Value: "8"}).Error)

	// Stale in-process cache says total=20, but authoritative SQL row says total=8.
	adty_setting.GetEpaySetting().ChannelTimeoutSeconds = 5
	adty_setting.GetEpaySetting().TotalTimeoutSeconds = 20

	callUpdateOption := func(key, value string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		body := fmt.Sprintf(`{"key":%q,"value":%q}`, key, value)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", 1)
		c.Set("username", "root")
		c.Set("role", common.RoleRootUser)
		UpdateOption(c)
		return rec
	}

	rejectRec := callUpdateOption(adty_setting.EpaySettingKeyChannelTimeout, "10")
	assert.Contains(t, rejectRec.Body.String(), `"success":false`)

	var storedChannel model.Option
	require.NoError(t, db.Where("key = ?", adty_setting.EpaySettingKeyChannelTimeout).First(&storedChannel).Error)
	assert.Equal(t, "5", storedChannel.Value)

	okRec := callUpdateOption(adty_setting.EpaySettingKeyChannelTimeout, "7")
	require.Equal(t, http.StatusOK, okRec.Code, okRec.Body.String())
	require.Contains(t, okRec.Body.String(), `"success":true`)

	require.NoError(t, db.Where("key = ?", adty_setting.EpaySettingKeyChannelTimeout).First(&storedChannel).Error)
	assert.Equal(t, "7", storedChannel.Value)
	assert.Equal(t, 7, adty_setting.GetEpaySetting().ChannelTimeoutSeconds)
	assert.Equal(t, 8, adty_setting.GetEpaySetting().TotalTimeoutSeconds)

	var epayAudit model.AuditLog
	require.NoError(t, db.Where("action = ? AND object_id = ?", "option.update", adty_setting.EpaySettingKeyChannelTimeout).First(&epayAudit).Error)
	assert.Equal(t, "5", epayAudit.BeforeValue)
	assert.Equal(t, "7", epayAudit.AfterValue)
}
