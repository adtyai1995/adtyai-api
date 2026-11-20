package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0704ReportedUsageCollectionPG(t *testing.T) {
	db := ftrRound1PG(t)
	pgtest.RecordServerVersion(t, db)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		OriginModelName: "ftr-0704",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	reported := dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 11, CompletionTokens: 7})
	outer := &dto.Usage{PromptTokens: 1, CompletionTokens: 1, BillingUsage: reported}
	got := effectiveBillingUsage(outer)
	require.Equal(t, 11, got.PromptTokens)
	require.Equal(t, 7, got.CompletionTokens)
	summary := calculateTextQuotaSummary(ctx, info, got)
	require.Equal(t, 11, summary.PromptTokens)
	require.Equal(t, 7, summary.CompletionTokens)

	info.SetEstimatePromptTokens(42)
	fallback := calculateTextQuotaSummary(ctx, info, nil)
	require.Equal(t, 42, fallback.PromptTokens)
	require.Zero(t, fallback.CompletionTokens)
}

func TestFTRADTY1606ReferralAccrualWaitsForSettleAtPG(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	cfg := adty_setting.GetReferralSetting()
	require.NotNil(t, cfg.DefaultRate)
	cfg.FreezeDays = 7

	affiliate, invitee := inviterAndInvitee(t, db, "ftr1606")
	paidTopUp(t, invitee, "EPU-FTR1606")
	var before int64
	require.NoError(t, db.Model(&model.ReferralCommission{}).Where("source_trade_no = ?", "EPU-FTR1606").Count(&before).Error)
	require.Zero(t, before)

	now := time.Now().Unix()
	settled, err := RunReferralSettlementOnce(now, 50)
	require.NoError(t, err)
	require.Zero(t, settled)

	var rows []model.ReferralCommission
	require.NoError(t, db.Where("source_trade_no = ?", "EPU-FTR1606").Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, model.ReferralCommissionStatusPending, rows[0].Status)
	require.InDelta(t, 10, rows[0].CommissionAmount, 1e-9)
	require.Greater(t, rows[0].SettleAt, now)
	var accrue []model.ReferralCommissionLedger
	require.NoError(t, db.Where("ref_id = ? AND type = ?", "EPU-FTR1606", "commission_accrue").Find(&accrue).Error)
	require.Len(t, accrue, 1)
	require.InDelta(t, 10, account(t, db, affiliate.Id).PendingAmount, 1e-9)

	settled, err = RunReferralSettlementOnce(now, 50)
	require.NoError(t, err)
	require.Zero(t, settled)
	var replay int64
	require.NoError(t, db.Model(&model.ReferralCommission{}).Where("source_trade_no = ?", "EPU-FTR1606").Count(&replay).Error)
	require.EqualValues(t, 1, replay)
	var accrueAgain int64
	require.NoError(t, db.Model(&model.ReferralCommissionLedger{}).Where("ref_id = ? AND type = ?", "EPU-FTR1606", "commission_accrue").Count(&accrueAgain).Error)
	require.EqualValues(t, 1, accrueAgain)

	moved, err := RunReferralSettlementOnce(rows[0].SettleAt, 50)
	require.NoError(t, err)
	require.Equal(t, 1, moved)
	done := commissionFor(t, db, "EPU-FTR1606")
	require.Equal(t, model.ReferralCommissionStatusAvailable, done.Status)
	var settleLedgers []model.ReferralCommissionLedger
	require.NoError(t, db.Where("commission_id = ? AND type = ?", done.Id, "commission_settle").Find(&settleLedgers).Error)
	require.Len(t, settleLedgers, 1)
	require.InDelta(t, 0, account(t, db, affiliate.Id).PendingAmount, 1e-9)
	require.InDelta(t, 10, account(t, db, affiliate.Id).AvailableAmount, 1e-9)

	var payer model.User
	require.NoError(t, db.Select("quota").First(&payer, invitee.Id).Error)
	require.Equal(t, 51_250_000, payer.Quota)
}
