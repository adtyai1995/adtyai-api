package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Create a paid and refunded original through the financial state machine before
// installing the legacy raw/pending wallet state exercised by adjustment tests.
func adjustmentOriginalFixture(t *testing.T, db *gorm.DB, userID int) (int, AdtyQuotaAdjustProvenance) {
	t.Helper()
	if !db.Migrator().HasTable(&AdtyFinancialAdjustment{}) {
		require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &Log{}))
		require.NoError(t, MigrateAdtyTablesOn(db))
	}
	require.NoError(t, db.AutoMigrate(&AuditIntent{}))
	var root User
	result := db.Where("username = ?", "adjustment-fixture-root").Limit(1).Find(&root)
	require.NoError(t, result.Error)
	if result.RowsAffected == 0 {
		root = User{Username: "adjustment-fixture-root", Password: "x", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default"}
		require.NoError(t, db.Create(&root).Error)
	}
	trade := fmt.Sprintf("adjustment-original-%d", userID)
	var existing AdtyTopupOrder
	result = db.Where("trade_no = ?", trade).Limit(1).Find(&existing)
	require.NoError(t, result.Error)
	if result.RowsAffected == 0 {
		var user User
		require.NoError(t, db.First(&user, userID).Error)
		// These are intentionally invalid legacy wallet fixtures, not test outcomes.
		raw, pending := user.Quota, user.QuotaPending
		require.NoError(t, db.Model(&user).Updates(map[string]any{"quota": 0, "quota_pending": 0}).Error)
		amount := decimal.RequireFromString("0.01")
		require.NoError(t, CreateAdtyTopUp(&TopUp{UserId: userID, TradeNo: trade, PaymentProvider: PaymentProviderAdtyBEpusdt, PaymentMethod: "usdt.trc20", Status: common.TopUpStatusPending},
			&AdtyTopupOrder{UserId: userID, TradeNo: trade, Provider: PaymentProviderAdtyBEpusdt, Method: "usdt.trc20", FaceUSD: amount, CreditQuota: 5000, PayAmount: amount, PayCurrency: "USDT", ExpireAt: common.GetTimestamp() + 900}))
		_, err := CompleteAdtyTopUp(trade, AdtyPaymentFacts{Provider: PaymentProviderAdtyBEpusdt, Source: AdtyPaymentSourceCallback, FiatAmount: amount, PaidAmount: amount, PaidCurrency: "USDT"}, "")
		require.NoError(t, err)
		_, err = completeManualRefundForTest(t, trade, "fixture original reversed", root.Id, fmt.Sprintf("adjustment-original-refund-%d", userID), amount)
		require.NoError(t, err)
		require.NoError(t, db.Model(&user).Updates(map[string]any{"quota": raw, "quota_pending": pending}).Error)
	}
	return root.Id, AdtyQuotaAdjustProvenance{OrderRef: trade, FundingSource: AdtyAdjustSourcePlatform}
}

func adjustmentTestRequest(t *testing.T, db *gorm.DB, userID int, mode, source string) (int, AdtyQuotaAdjustProvenance) {
	t.Helper()
	root, provenance := adjustmentOriginalFixture(t, db, userID)
	if mode == "subtract" || mode == "override" {
		provenance.FundingSource, provenance.BatchKind, provenance.BatchSource = AdtyAdjustSourceBatch, AdtyFundBatchKindPrincipal, source
		if source == "" {
			source = fmt.Sprintf("legacy-adjustment-%d", userID)
			provenance.BatchSource = source
			var count int64
			require.NoError(t, db.Model(&AdtyFundBatch{}).Where("user_id = ? AND source = ?", userID, source).Count(&count).Error)
			if count == 0 {
				var user User
				require.NoError(t, db.First(&user, userID).Error)
				balance, err := userWalletBalance(user.Quota, user.QuotaPending)
				require.NoError(t, err)
				if balance > 0 {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						return CreateAdtyFundBatchesWithSiteTx(tx, userID, AdtyFundBatchSiteID(0), source, int64(balance), 0)
					}))
				}
			}
		}
	}
	return root, provenance
}

func adjustLegacyWalletForTest(t *testing.T, userID, operatorID, operatorRole int, mode string, value int, reason, key string, source ...string) (*AdtyQuotaAdjustResult, error) {
	t.Helper()
	var root User
	require.NoError(t, DB.Where("username = ?", "adjustment-fixture-root").First(&root).Error)
	if operatorID == 1 {
		operatorID = root.Id
	}
	prov := AdtyQuotaAdjustProvenance{OrderRef: fmt.Sprintf("adjustment-original-%d", userID), FundingSource: AdtyAdjustSourcePlatform}
	var user User
	require.NoError(t, DB.First(&user, userID).Error)
	if mode == "subtract" || mode == "override" && value < user.Quota {
		prov.FundingSource, prov.BatchKind, prov.BatchSource = AdtyAdjustSourceBatch, AdtyFundBatchKindPrincipal, fmt.Sprintf("legacy-adjustment-%d", userID)
		if len(source) > 0 {
			prov.BatchSource = source[0]
		}
	}
	return AdjustUserQuotaWithLedger(userID, operatorID, operatorRole, mode, value, reason, key, prov)
}
