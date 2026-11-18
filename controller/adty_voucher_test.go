package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostAdtyAdminVoucherDesignatedOwnerAndIntentReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedis
		service.ResetAdtyVoucherGuesses()
	})
	service.ResetAdtyVoucherGuesses()

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.AuditLog{},
		&model.AuditIntent{},
		&model.Log{},
		&model.AdtySite{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
		&model.AdtyVoucher{},
		&model.AdtyVoucherLiability{},
		&model.AdtyVoucherAuthorization{},
		&model.AdtyVoucherCloseGrant{},
		&model.AdtyTopupOrder{},
		&model.AdtyFundBatch{},
	))

	root := model.User{
		Username: "ctrl-voucher-root",
		Password: "hashed-password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&root).Error)
	owner1 := model.User{
		Username: "ctrl-voucher-owner1",
		Password: "hashed-password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&owner1).Error)
	owner2 := model.User{
		Username: "ctrl-voucher-owner2",
		Password: "hashed-password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&owner2).Error)

	site := model.AdtySite{
		OwnerUserId:  owner1.Id,
		Subdomain:    "ctrl-voucher-site",
		Status:       model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, service.EnsureAdtySiteWallets(site.Id))

	callJSON := func(method, target, body string, userID, role int, headers map[string]string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			c.Request.Header.Set(k, v)
		}
		c.Set("id", userID)
		c.Set("role", role)
		handler(c)
		return rec
	}

	limitRec := callJSON(http.MethodPut, "/api/adty/admin/vouchers/limit", `{"limit_usd":"50"}`, root.Id, common.RoleRootUser, nil, PutAdtyVoucherIssuanceLimit)
	require.Contains(t, limitRec.Body.String(), `"success":true`)

	// Conflicting owner fields in request body must be rejected.
	conflictOwnerBody := fmt.Sprintf(
		`{"purpose":"site_margin","site_id":%d,"designated_owner_id":%d,"owner_user_id":%d,"amount_usd":"5","reason":"conflict"}`,
		site.Id, owner1.Id, owner2.Id,
	)
	conflictOwnerRec := callJSON(http.MethodPost, "/api/adty/admin/vouchers", conflictOwnerBody, root.Id, common.RoleRootUser, nil, PostAdtyAdminVoucher)
	require.Contains(t, conflictOwnerRec.Body.String(), `"success":false`)

	// Conflicting intent keys between body and header must be rejected.
	conflictKeyBody := fmt.Sprintf(
		`{"purpose":"site_margin","site_id":%d,"amount_usd":"5","reason":"conflict-key","intent_key":"key-a"}`,
		site.Id,
	)
	conflictKeyRec := callJSON(
		http.MethodPost,
		"/api/adty/admin/vouchers",
		conflictKeyBody,
		root.Id,
		common.RoleRootUser,
		map[string]string{"X-Intent-Key": "key-b"},
		PostAdtyAdminVoucher,
	)
	require.Contains(t, conflictKeyRec.Body.String(), `"success":false`)

	// Issue margin voucher with designated_owner_id and intent_key.
	issueBody := fmt.Sprintf(
		`{"purpose":"site_margin","site_id":%d,"designated_owner_id":%d,"amount_usd":"5","reason":"margin-snap","intent_key":"ctrl-margin-intent-1"}`,
		site.Id, owner1.Id,
	)
	firstRec := callJSON(http.MethodPost, "/api/adty/admin/vouchers", issueBody, root.Id, common.RoleRootUser, nil, PostAdtyAdminVoucher)
	var firstResp struct {
		Success bool                      `json:"success"`
		Data    service.AdtyVoucherIssued `json:"data"`
	}
	require.NoError(t, json.Unmarshal(firstRec.Body.Bytes(), &firstResp))
	require.True(t, firstResp.Success, firstRec.Body.String())
	require.NotEmpty(t, firstResp.Data.Code)
	assert.False(t, firstResp.Data.Replayed)
	assert.Equal(t, site.Id, firstResp.Data.TargetSiteId)
	assert.Equal(t, owner1.Id, firstResp.Data.DesignatedOwnerId)
	assert.Equal(t, "ctrl-margin-intent-1", firstResp.Data.IntentKey)

	// Replaying the same request reconciles without minting a second voucher or re-exposing the code.
	replayRec := callJSON(http.MethodPost, "/api/adty/admin/vouchers", issueBody, root.Id, common.RoleRootUser, nil, PostAdtyAdminVoucher)
	var replayResp struct {
		Success bool                      `json:"success"`
		Data    service.AdtyVoucherIssued `json:"data"`
	}
	require.NoError(t, json.Unmarshal(replayRec.Body.Bytes(), &replayResp))
	require.True(t, replayResp.Success, replayRec.Body.String())
	assert.True(t, replayResp.Data.Replayed)
	assert.Equal(t, firstResp.Data.Id, replayResp.Data.Id)
	assert.Empty(t, replayResp.Data.Code)
	assert.Equal(t, firstResp.Data.CodeTail, replayResp.Data.CodeTail)
	assert.Equal(t, owner1.Id, replayResp.Data.DesignatedOwnerId)

	// After site ownership changes to owner2, only the snapshotted designated owner (owner1) can redeem.
	require.NoError(t, db.Model(&site).Update("owner_user_id", owner2.Id).Error)

	redeemOwner2Rec := callJSON(
		http.MethodPost,
		"/api/adty/vouchers/redeem",
		fmt.Sprintf(`{"code":%q}`, firstResp.Data.Code),
		owner2.Id,
		common.RoleCommonUser,
		nil,
		PostAdtyVoucherRedeem,
	)
	require.Contains(t, redeemOwner2Rec.Body.String(), `"success":false`)

	redeemOwner1Rec := callJSON(
		http.MethodPost,
		"/api/adty/vouchers/redeem",
		fmt.Sprintf(`{"code":%q}`, firstResp.Data.Code),
		owner1.Id,
		common.RoleCommonUser,
		nil,
		PostAdtyVoucherRedeem,
	)
	require.Contains(t, redeemOwner1Rec.Body.String(), `"success":true`, redeemOwner1Rec.Body.String())
}
