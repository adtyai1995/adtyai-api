package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ISS-R1-0167: UpdateAdtyResellerSettings sends scheduled cost-factor increase notices
// only after validation and option persistence succeed.
func TestUpdateAdtyResellerSettingsSendsPendingCostFactorNoticeOnlyAfterCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn, err := model.OfficialPostgresAcceptanceDSN()
	require.NoError(t, err)
	db := modelManagementDB(t, "postgres", dsn)
	require.NoError(t, i18n.Init())
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	require.NoError(t, db.AutoMigrate(&model.Option{}))

	previousSetting := *adty_setting.GetResellerSetting()
	t.Cleanup(func() {
		*adty_setting.GetResellerSetting() = previousSetting
	})

	minFactor := 0.60
	costFactor := 0.80
	settings := adty_setting.DefaultResellerSetting()
	settings.RootDomains = []string{"sites.example.test"}
	settings.MinCostFactor, settings.CostFactor = &minFactor, &costFactor
	*adty_setting.GetResellerSetting() = settings

	owner := model.User{
		Username: "reseller-mail-owner",
		Email:    "owner@example.test",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{
		OwnerUserId: owner.Id,
		Subdomain:   "mail-check",
		RootDomain:  "sites.example.test",
		Status:      model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(&site).Error)

	type mailRecord struct {
		subject string
		email   string
		content string
	}
	var sentMails []mailRecord
	previousSender := service.AdtySendSiteEmailAsync
	service.AdtySendSiteEmailAsync = func(_ *model.AdtySite, subject, email, content string) {
		var pending model.Option
		require.NoError(t, db.Where("key = ?", "adty_reseller_setting.pending_cost_factor").First(&pending).Error, "notice must see the committed SQL option")
		require.Equal(t, "0.9", pending.Value)
		sentMails = append(sentMails, mailRecord{subject: subject, email: email, content: content})
	}
	t.Cleanup(func() { service.AdtySendSiteEmailAsync = previousSender })

	callUpdate := func(body string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/adty/admin/sites/settings", strings.NewReader(body))
		c.Set("id", owner.Id)
		c.Set("role", common.RoleRootUser)
		UpdateAdtyResellerSettings(c)
		var resp map[string]any
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
		return resp
	}

	// 1. Validation failure after HoldMinCostFactorIncrease must NOT send any pending notice.
	invalidResp := callUpdate(`{"min_cost_factor":0.70,"root_domains":["invalid..domain"]}`)
	assert.Equal(t, false, invalidResp["success"], invalidResp)
	assert.Empty(t, sentMails, "validation failure must not trigger pending cost-factor email")

	// 2. Database persistence failure in UpdateOptionsBulk must NOT send any pending notice.
	const failOptionWriteCB = "test:fail_option_write_on_reseller_settings"
	failOptionGate := func(tx *gorm.DB) {
		if tx != nil && tx.Statement != nil && (tx.Statement.Table == "options" || (tx.Statement.Schema != nil && tx.Statement.Schema.Table == "options")) {
			_ = tx.AddError(errors.New("simulated option persistence failure"))
		}
	}
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(failOptionWriteCB, failOptionGate))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(failOptionWriteCB, failOptionGate))
	dbFailResp := callUpdate(`{"min_cost_factor":0.70,"cost_factor":0.90}`)
	require.NoError(t, db.Callback().Create().Remove(failOptionWriteCB))
	require.NoError(t, db.Callback().Update().Remove(failOptionWriteCB))
	assert.Equal(t, false, dbFailResp["success"], dbFailResp)
	assert.Empty(t, sentMails, "failed option persistence must not trigger pending cost-factor email")

	// 3. Immediate decrease (no pending increase) commits cleanly without sending a pending-increase notice.
	decreaseResp := callUpdate(`{"cost_factor":0.75}`)
	require.Equal(t, true, decreaseResp["success"], decreaseResp)
	assert.Empty(t, sentMails, "cost-factor decrease must not trigger pending increase email")

	// 4. Valid committed increase persists pending state and sends the notice after commit.
	increaseResp := callUpdate(`{"cost_factor":0.90}`)
	require.Equal(t, true, increaseResp["success"], increaseResp)
	require.Len(t, sentMails, 1)
	assert.Equal(t, "owner@example.test", sentMails[0].email)
	assert.Contains(t, sentMails[0].content, "0.9")

	live := adty_setting.GetResellerSetting()
	require.NotNil(t, live.PendingCostFactor)
	assert.InDelta(t, 0.90, *live.PendingCostFactor, 1e-6)
	assert.Greater(t, live.PendingCostFactorAt, int64(0))
}
