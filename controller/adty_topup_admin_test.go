package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTopUpAdminTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.AuthFlow{},
		&model.TwoFA{},
		&model.PasskeyCredential{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyManualRefund{},
		&model.AdtyFundBatch{},
		&model.PaymentOrphanEvent{},
		&model.PaymentOrphanReview{},
		&model.AdtyGiftFulfillment{},
		&model.AdtySite{},
		&model.AdtySiteInviteCommission{},
		&model.AdtyCommissionRecovery{},
		&model.AuditIntent{},
		&model.AuditLog{},
	))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	oldSecret := common.SessionSecret

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	if common.SessionSecret == "" {
		common.SessionSecret = "test-session-secret-topup-admin"
	}
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		common.SessionSecret = oldSecret
		_ = sqlDB.Close()
	})
	return db
}

func seedCreditedTopUpWithManualRefund(t *testing.T, db *gorm.DB, userID int, tradeNo, refundID string) {
	t.Helper()
	now := time.Now().Unix()
	faceUSD := decimal.NewFromInt(10)
	creditQuota := faceUSD.Mul(common.QuotaPerUnitDecimal()).IntPart()

	require.NoError(t, db.Create(&model.TopUp{
		UserId:          userID,
		Amount:          10,
		Money:           10,
		TradeNo:         tradeNo,
		PaymentMethod:   "alipay",
		PaymentProvider: model.PaymentProviderAdtyEpay,
		CreateTime:      now,
		CompleteTime:    now,
		Status:          common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		Business:      model.AdtyBusinessWalletTopup,
		TradeNo:       tradeNo,
		UserId:        userID,
		Provider:      model.PaymentProviderAdtyEpay,
		Method:        "alipay",
		FaceUSD:       faceUSD,
		CreditQuota:   creditQuota,
		PayAmount:     decimal.NewFromInt(70),
		PayCurrency:   "CNY",
		PaidAmount:    decimal.NewFromInt(70),
		PaidCurrency:  "CNY",
		PriceSnapshot: decimal.NewFromInt(7),
	}).Error)
	require.NoError(t, db.Create(&model.AdtyManualRefund{
		RefundId:     refundID,
		TradeNo:      tradeNo,
		UserId:       userID,
		Status:       model.AdtyManualRefundStatusSubmitted,
		RequestUSD:   decimal.NewFromInt(5),
		RequestQuota: decimal.NewFromInt(5).Mul(common.QuotaPerUnitDecimal()).IntPart(),
		Reason:       "test refund",
		AppliedAt:    now,
	}).Error)
}

func TestPostAdtyGiftFulfillmentReviewStepUpAndRootEnforcement(t *testing.T) {
	db := setupTopUpAdminTestDB(t)
	now := time.Now().Unix()

	rootUser := model.User{
		Username:    "review_root",
		Role:        common.RoleRootUser,
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	adminUser := model.User{
		Username:    "review_admin",
		Role:        common.RoleAdminUser,
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	buyer := model.User{
		Username:    "review_buyer",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(&rootUser).Error)
	require.NoError(t, db.Create(&adminUser).Error)
	require.NoError(t, db.Create(&buyer).Error)

	rootSession := model.UserSession{
		SID:             "sess-gift-review-root",
		UserID:          rootUser.Id,
		Version:         1,
		UserAuthVersion: rootUser.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     strings.Repeat("a", 64),
		LoginMethod:     "password",
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       now + 3600,
	}
	require.NoError(t, db.Create(&rootSession).Error)

	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		Business:              model.AdtyBusinessWalletTopup,
		TradeNo:               "GF-ORDER-1",
		UserId:                buyer.Id,
		Provider:              model.PaymentProviderAdtyEpay,
		Method:                "alipay",
		FaceUSD:               decimal.NewFromInt(20),
		BonusUSD:              decimal.NewFromInt(2),
		CreditQuota:           decimal.NewFromInt(22).Mul(common.QuotaPerUnitDecimal()).IntPart(),
		GiftFulfillmentStatus: model.AdtyGiftFulfillmentPendingReview,
	}).Error)

	fulfillment := model.AdtyGiftFulfillment{
		TradeNo:    "GF-ORDER-1",
		UserId:     buyer.Id,
		BonusUSD:   decimal.NewFromInt(2),
		BonusQuota: decimal.NewFromInt(2).Mul(common.QuotaPerUnitDecimal()).IntPart(),
		Status:     model.AdtyGiftFulfillmentPendingReview,
		Reason:     "flagged for review",
	}
	require.NoError(t, db.Create(&fulfillment).Error)

	newReviewRequest := func(actor model.User, sid string, body string, proofToken string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, "/api/adty/admin/gift-fulfillments/"+strconv.FormatInt(fulfillment.Id, 10)+"/review", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if proofToken != "" {
			req.Header.Set("X-Security-Proof", proofToken)
		}
		c.Request = req
		c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(fulfillment.Id, 10)}}
		c.Set("id", actor.Id)
		c.Set("role", actor.Role)
		if sid != "" {
			c.Set("session_id", sid)
			c.Set("auth_version", actor.AuthVersion)
			c.Set("session_version", int64(1))
		}
		PostAdtyGiftFulfillmentReview(c)
		return rec
	}

	t.Run("non_root_rejected", func(t *testing.T) {
		rec := newReviewRequest(adminUser, "", `{"action":"reject","note":"nope"}`, "")
		assert.Equal(t, http.StatusForbidden, rec.Code)

		var current model.AdtyGiftFulfillment
		require.NoError(t, db.First(&current, fulfillment.Id).Error)
		assert.Equal(t, model.AdtyGiftFulfillmentPendingReview, current.Status)
	})

	t.Run("root_without_proof_rejected", func(t *testing.T) {
		rec := newReviewRequest(rootUser, rootSession.SID, `{"action":"reject","note":"suspected abuse"}`, "")
		assert.Equal(t, http.StatusForbidden, rec.Code)

		var resp struct {
			Success bool   `json:"success"`
			Code    string `json:"code"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
		assert.False(t, resp.Success)
		assert.Equal(t, "SECURITY_PROOF_REQUIRED", resp.Code)

		var current model.AdtyGiftFulfillment
		require.NoError(t, db.First(&current, fulfillment.Id).Error)
		assert.Equal(t, model.AdtyGiftFulfillmentPendingReview, current.Status)
	})

	rootIdentity := service.AuthIdentity{
		UserID:          rootUser.Id,
		SessionID:       rootSession.SID,
		UserAuthVersion: rootUser.AuthVersion,
		SessionVersion:  rootSession.Version,
	}

	t.Run("root_with_mismatched_context_proof_rejected", func(t *testing.T) {
		wrongBinding, err := service.BindVerificationOperation(service.AdtyGiftFulfillmentReviewOperation(fulfillment.Id, "approve", ""))
		require.NoError(t, err)
		proof, err := service.CompleteSecurityVerification(rootIdentity, wrongBinding, service.VerificationMethodRootExempt)
		require.NoError(t, err)

		rec := newReviewRequest(rootUser, rootSession.SID, `{"action":"reject","note":"suspected abuse"}`, proof.ProofToken)
		assert.Equal(t, http.StatusForbidden, rec.Code)

		var resp struct {
			Success bool   `json:"success"`
			Code    string `json:"code"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
		assert.False(t, resp.Success)
		assert.Equal(t, "SECURITY_PROOF_CONTEXT_MISMATCH", resp.Code)

		var current model.AdtyGiftFulfillment
		require.NoError(t, db.First(&current, fulfillment.Id).Error)
		assert.Equal(t, model.AdtyGiftFulfillmentPendingReview, current.Status)
	})

	t.Run("root_with_valid_proof_succeeds_and_proof_is_single_use", func(t *testing.T) {
		binding, err := service.BindVerificationOperation(service.AdtyGiftFulfillmentReviewOperation(fulfillment.Id, "reject", "suspected abuse"))
		require.NoError(t, err)
		proof, err := service.CompleteSecurityVerification(rootIdentity, binding, service.VerificationMethodRootExempt)
		require.NoError(t, err)

		rec := newReviewRequest(rootUser, rootSession.SID, `{"action":" REJECT ","note":"  suspected abuse  "}`, proof.ProofToken)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
		assert.True(t, resp.Success)

		var current model.AdtyGiftFulfillment
		require.NoError(t, db.First(&current, fulfillment.Id).Error)
		assert.Equal(t, model.AdtyGiftFulfillmentCanceled, current.Status)

		replayRec := newReviewRequest(rootUser, rootSession.SID, `{"action":"reject","note":"suspected abuse"}`, proof.ProofToken)
		assert.Equal(t, http.StatusForbidden, replayRec.Code)
		var replayResp struct {
			Code string `json:"code"`
		}
		require.NoError(t, common.Unmarshal(replayRec.Body.Bytes(), &replayResp))
		assert.Equal(t, "SECURITY_PROOF_CONSUMED", replayResp.Code)
	})
}

func TestAdminRefundReadEndpointsEnforceRoleHierarchyAndPATAdminTarget(t *testing.T) {
	db := setupTopUpAdminTestDB(t)

	rootUser := model.User{Username: "rf_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: 5000000}
	adminActor := model.User{Username: "rf_admin_actor", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Quota: 5000000}
	adminPeer := model.User{Username: "rf_admin_peer", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Quota: 5000000}
	commonUser := model.User{Username: "rf_common", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 5000000}
	require.NoError(t, db.Create(&rootUser).Error)
	require.NoError(t, db.Create(&adminActor).Error)
	require.NoError(t, db.Create(&adminPeer).Error)
	require.NoError(t, db.Create(&commonUser).Error)

	seedCreditedTopUpWithManualRefund(t, db, rootUser.Id, "ORD-ROOT", "RF-ROOT")
	seedCreditedTopUpWithManualRefund(t, db, adminPeer.Id, "ORD-ADMIN", "RF-ADMIN")
	seedCreditedTopUpWithManualRefund(t, db, commonUser.Id, "ORD-USER", "RF-USER")

	endpoints := []struct {
		name    string
		handler func(c *gin.Context)
	}{
		{name: "preview", handler: PreviewAdtyTopUpRefund},
		{name: "list", handler: ListAdtyTopUpManualRefunds},
		{name: "get", handler: GetAdtyTopUpManualRefund},
	}

	cases := []struct {
		name       string
		actor      model.User
		isPAT      bool
		tradeNo    string
		refundID   string
		wantStatus int
	}{
		{name: "admin_session_cannot_read_root_order", actor: adminActor, isPAT: false, tradeNo: "ORD-ROOT", refundID: "RF-ROOT", wantStatus: http.StatusForbidden},
		{name: "admin_session_cannot_read_peer_admin_order", actor: adminActor, isPAT: false, tradeNo: "ORD-ADMIN", refundID: "RF-ADMIN", wantStatus: http.StatusForbidden},
		{name: "admin_session_can_read_common_user_order", actor: adminActor, isPAT: false, tradeNo: "ORD-USER", refundID: "RF-USER", wantStatus: http.StatusOK},
		{name: "root_pat_cannot_read_root_order", actor: rootUser, isPAT: true, tradeNo: "ORD-ROOT", refundID: "RF-ROOT", wantStatus: http.StatusForbidden},
		{name: "root_pat_cannot_read_admin_order", actor: rootUser, isPAT: true, tradeNo: "ORD-ADMIN", refundID: "RF-ADMIN", wantStatus: http.StatusForbidden},
		{name: "root_pat_can_read_common_user_order", actor: rootUser, isPAT: true, tradeNo: "ORD-USER", refundID: "RF-USER", wantStatus: http.StatusOK},
		{name: "admin_pat_cannot_read_peer_admin_order", actor: adminActor, isPAT: true, tradeNo: "ORD-ADMIN", refundID: "RF-ADMIN", wantStatus: http.StatusForbidden},
		{name: "admin_pat_can_read_common_user_order", actor: adminActor, isPAT: true, tradeNo: "ORD-USER", refundID: "RF-USER", wantStatus: http.StatusOK},
		{name: "root_session_can_read_root_order", actor: rootUser, isPAT: false, tradeNo: "ORD-ROOT", refundID: "RF-ROOT", wantStatus: http.StatusOK},
		{name: "root_session_can_read_admin_order", actor: rootUser, isPAT: false, tradeNo: "ORD-ADMIN", refundID: "RF-ADMIN", wantStatus: http.StatusOK},
		{name: "root_session_can_read_common_user_order", actor: rootUser, isPAT: false, tradeNo: "ORD-USER", refundID: "RF-USER", wantStatus: http.StatusOK},
	}

	for _, ep := range endpoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodGet, "/api/adty/admin/topup/"+tc.tradeNo, nil)
				c.Params = gin.Params{
					{Key: "trade_no", Value: tc.tradeNo},
					{Key: "refund_id", Value: tc.refundID},
				}
				c.Set("id", tc.actor.Id)
				c.Set("role", tc.actor.Role)
				if tc.isPAT {
					c.Set("use_access_token", true)
				}

				ep.handler(c)

				assert.Equal(t, tc.wantStatus, rec.Code, "body=%s", rec.Body.String())
				var resp struct {
					Success bool `json:"success"`
				}
				require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
				assert.Equal(t, tc.wantStatus == http.StatusOK, resp.Success, "body=%s", rec.Body.String())
			})
		}
	}
}
