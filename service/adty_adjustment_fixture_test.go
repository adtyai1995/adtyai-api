package service

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Financial corrections need an actual original. The small original is paid and
// refunded through production transitions, leaving the test wallet unchanged.
func financialOriginalForTest(t *testing.T, db *gorm.DB, userID int) string {
	t.Helper()
	trade := fmt.Sprintf("finance-original-%d", userID)
	var order model.AdtyTopupOrder
	result := db.Where("trade_no = ?", trade).Limit(1).Find(&order)
	require.NoError(t, result.Error)
	if result.RowsAffected > 0 {
		return trade
	}
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	amount := decimal.RequireFromString("0.01")
	require.NoError(t, model.CreateAdtyTopUp(&model.TopUp{UserId: userID, TradeNo: trade, PaymentProvider: model.PaymentProviderAdtyBEpusdt, PaymentMethod: "usdt.trc20", Status: common.TopUpStatusPending},
		&model.AdtyTopupOrder{UserId: userID, TradeNo: trade, SiteId: user.SiteId, Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20", FaceUSD: amount, CreditQuota: 5000, PayAmount: amount, PayCurrency: "USDT", ExpireAt: common.GetTimestamp() + 900}))
	_, err := model.CompleteAdtyTopUp(trade, model.AdtyPaymentFacts{Provider: model.PaymentProviderAdtyBEpusdt, Source: model.AdtyPaymentSourceCallback, FiatAmount: amount, PaidAmount: amount, PaidCurrency: "USDT"}, "")
	require.NoError(t, err)
	_, err = completeManualRefundForTest(t, trade, "fixture original reversed", userID, fmt.Sprintf("finance-original-refund-%d", userID), amount)
	require.NoError(t, err)
	var after model.User
	require.NoError(t, db.First(&after, userID).Error)
	require.Equal(t, user.Quota, after.Quota, "original payment and refund must net to zero")
	require.Equal(t, user.QuotaPending, after.QuotaPending)
	return trade
}

func staffProvenanceForTest(t *testing.T, customerID int, direction string) model.AdtyStaffBalanceProvenance {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.First(&user, customerID).Error)
	trade := financialOriginalForTest(t, model.DB, customerID)
	prov := model.AdtyStaffBalanceProvenance{Reason: "reconcile original payment", OrderRef: trade, FundingSource: model.AdtyStaffFundingSourceEarnings}
	if direction == model.AdtyStaffBalanceDecrease {
		var batch model.AdtyFundBatch
		require.NoError(t, model.DB.Where("user_id = ? AND site_id = ? AND remaining > 0 AND kind = ?", customerID, user.SiteId, model.AdtyFundBatchKindPrincipal).Order("id DESC").First(&batch).Error)
		prov.FundingSource, prov.BatchKind, prov.BatchSource = "specified_batch", batch.Kind, batch.Source
	}
	return prov
}

func adjustmentRootForTest(t *testing.T, db *gorm.DB) int {
	t.Helper()
	var root model.User
	result := db.Where("username = ?", "financial-fixture-root").Limit(1).Find(&root)
	require.NoError(t, result.Error)
	if result.RowsAffected == 0 {
		root = model.User{Username: "financial-fixture-root", Password: "x", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default"}
		require.NoError(t, db.Create(&root).Error)
	}
	return root.Id
}
