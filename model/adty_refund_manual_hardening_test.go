package model

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLoadAdtyRefundPageFactsSeparatesRefundedAndHeldFromConsumedPrincipal(t *testing.T) {
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&User{},
		&TopUp{},
		&AdtyTopupOrder{},
		&AdtyFundBatch{},
		&AdtyFundAllocation{},
		&AdtyManualRefund{},
		&PaymentOrphanEvent{},
		&PaymentOrphanReview{},
		&AdtySiteInviteCommission{},
	))

	oldDB := DB
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		common.RedisEnabled = oldRedis
		_ = sqlDB.Close()
	})

	now := time.Now().Unix()
	user := User{Id: 801, Username: "ref_user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 25_000_000}
	require.NoError(t, db.Create(&user).Error)

	tradeNo := "ORD_REF_FACTS_1"
	// Original $100 (50M quota).
	// Customer spent $20 (10M) settled -> lot.Remaining went 50M -> 40M.
	// Completed partial refund reclaimed $30 (15M) -> lot.Remaining went 40M -> 25M.
	// In-flight API call holds $10 (5M) via AdtyFundAllocation (not yet subtracted from lot.Remaining).
	// Terminated manual refund had DeductedPrincipalQuota=7.5M ($15) which was restored on terminate and MUST NOT reduce consumed.
	require.NoError(t, db.Create(&AdtyTopupOrder{
		TradeNo:      tradeNo,
		UserId:       user.Id,
		Business:     AdtyBusinessWalletTopup,
		FaceUSD:      decimal.NewFromInt(100),
		CreditQuota:  50_000_000,
		PayAmount:    decimal.NewFromInt(100),
		PayCurrency:  "USD",
		PaidAmount:   decimal.NewFromInt(100),
		PaidCurrency: "USD",
		RefundQuota:  15_000_000,
	}).Error)

	lot := AdtyFundBatch{
		UserId:    user.Id,
		Kind:      AdtyFundBatchKindPrincipal,
		Source:    tradeNo,
		Original:  50_000_000,
		Remaining: 25_000_000,
		Status:    AdtyFundBatchStatusActive,
	}
	require.NoError(t, db.Create(&lot).Error)

	require.NoError(t, db.Create(&AdtyFundAllocation{
		UserId:      user.Id,
		BatchId:     lot.Id,
		OperationId: "req-inflight-1",
		Seq:         1,
		Kind:        AdtyFundBatchKindPrincipal,
		Source:      tradeNo,
		Held:        5_000_000,
		Settled:     0,
	}).Error)

	// Reclaimed & completed refund ($30 = 15M deducted from lot).
	require.NoError(t, db.Create(&AdtyManualRefund{
		RefundId:               "RF_COMPLETED",
		TradeNo:                tradeNo,
		UserId:                 user.Id,
		Status:                 AdtyManualRefundStatusCompleted,
		RequestUSD:             decimal.NewFromInt(30),
		RequestQuota:           15_000_000,
		ReclaimedAt:            now - 100,
		DeductedPrincipalQuota: 15_000_000,
		CompletedAt:            now - 50,
	}).Error)

	// Terminated refund (was restored to lot on terminate -> must NOT count as refunded).
	require.NoError(t, db.Create(&AdtyManualRefund{
		RefundId:               "RF_TERMINATED",
		TradeNo:                tradeNo,
		UserId:                 user.Id,
		Status:                 AdtyManualRefundStatusTerminated,
		RequestUSD:             decimal.NewFromInt(15),
		RequestQuota:           7_500_000,
		ReclaimedAt:            now - 200,
		DeductedPrincipalQuota: 7_500_000,
		TerminatedAt:           now - 150,
	}).Error)

	facts, err := LoadAdtyRefundPageFacts(tradeNo, decimal.Zero)
	require.NoError(t, err)
	assert.Equal(t, "40", facts.PrincipalRemainingUSD, "remaining principal must subtract 5M ($10) in-flight hold from 25M ($50) lot remaining")
	assert.Equal(t, "20", facts.PrincipalConsumedUSD, "consumed principal must reflect only $20 API spend, excluding $30 refunded and $10 held")
}
