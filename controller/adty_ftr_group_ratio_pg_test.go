package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-05-08: Root saves a group ratio and a closed site group allow-list.
func TestFTRADTY0508GroupRatioAndSiteAllowPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.AdtySite{}, &model.AdtySiteAudit{}))
	root := model.User{
		Username: "ftr0508-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	owner := model.User{
		Username: "ftr0508-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&owner).Error)
	require.Equal(t, common.RoleRootUser, root.Role)

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, had := common.OptionMap["GroupRatio"]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if common.OptionMap == nil {
			common.OptionMap = map[string]string{}
		}
		if had {
			common.OptionMap["GroupRatio"] = previous
		} else {
			delete(common.OptionMap, "GroupRatio")
		}
	})

	createCtx, createRec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/groups", map[string]any{
		"name": "FTR 0508 Group", "desc": "ratio and site allow",
	}, root.Id)
	createCtx.Set("role", root.Role)
	createCtx.Set("username", root.Username)
	PostAdtyGroup(createCtx)
	require.Contains(t, createRec.Body.String(), `"success":true`, createRec.Body.String())
	var groups []model.AdtyGroup
	require.NoError(t, db.Find(&groups).Error)
	var groupID string
	for _, group := range groups {
		if group.Names["en"] == "FTR 0508 Group" {
			groupID = group.ID
		}
	}
	require.NotEmpty(t, groupID)

	ratio := fmt.Sprintf("{\"%s\":1}", groupID)
	ratioCtx, ratioRec := newAuthenticatedContext(t, http.MethodPut, "/api/option/", map[string]any{
		"key": "GroupRatio", "value": ratio,
	}, root.Id)
	ratioCtx.Set("role", root.Role)
	ratioCtx.Set("username", root.Username)
	UpdateOption(ratioCtx)
	ratioBody := ratioRec.Body.String()
	require.Contains(t, ratioBody, `"success":true`, ratioBody)
	var storedRatio model.Option
	require.NoError(t, db.Where("key = ?", "GroupRatio").First(&storedRatio).Error)
	assert.Contains(t, storedRatio.Value, groupID)

	site := model.AdtySite{
		OwnerUserId:  owner.Id,
		Subdomain:    "ftr0508",
		Status:       model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	allow := fmt.Sprintf("[\"%s\"]", groupID)
	adjustCtx, adjustRec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/admin/sites/"+strconv.Itoa(site.Id), map[string]any{
		"allowed_groups": allow,
		"reason":         "FTR 0508 site group allow",
	}, root.Id)
	adjustCtx.Set("role", root.Role)
	adjustCtx.Set("username", root.Username)
	adjustCtx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(site.Id)}}
	AdjustAdtyAdminSite(adjustCtx)
	adjustBody := adjustRec.Body.String()
	require.Contains(t, adjustBody, `"success":true`, adjustBody)

	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.Contains(t, stored.AllowedGroups, groupID)
	assert.True(t, service.SiteGroupAllowed(site.Id, groupID))
	assert.False(t, service.SiteGroupAllowed(site.Id, "ftr-0508-other"))
	assert.Equal(t, common.RoleRootUser, root.Role)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
