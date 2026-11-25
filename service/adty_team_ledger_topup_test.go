package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListAdtyTeamTopupsExpiryPaginationAndLedgersForLeader(t *testing.T) {
	db := setupTeamPayLifecycleTestDB(t)
	now := time.Now().Unix()

	require.NoError(t, db.Create(&model.User{Id: 301, Username: "leader301", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.User{Id: 302, Username: "member302", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error)
	team := &model.AdtyTeam{
		Id: 30, SiteId: 0, LeaderUserId: 301, Name: "LedgerTeam",
		Discount: decimal.NewFromInt(1), Status: model.AdtyTeamStatusActive,
	}
	require.NoError(t, db.Create(team).Error)

	seedOrder := func(tradeNo, status string, expireAt int64) {
		require.NoError(t, db.Create(&model.TopUp{
			UserId: 301, Amount: 10, Money: 70, TradeNo: tradeNo,
			PaymentMethod: "alipay", PaymentProvider: model.PaymentProviderAdtyEpay,
			CreateTime: now, Status: status,
		}).Error)
		require.NoError(t, db.Create(&model.AdtyTopupOrder{
			Business: model.AdtyBusinessTeamTopup, TradeNo: tradeNo, UserId: 301, TeamId: 30,
			Provider: model.PaymentProviderAdtyEpay, Method: "alipay",
			FaceUSD: decimal.NewFromInt(10), PayAmount: decimal.NewFromInt(70), PayCurrency: "CNY",
			ExpireAt: expireAt,
		}).Error)
	}

	seedOrder("TTEP_PEND_ACTIVE", common.TopUpStatusPending, now+600)
	seedOrder("TTEP_PEND_NOEXP", common.TopUpStatusPending, 0)
	seedOrder("TTEP_PEND_EXPIRED", common.TopUpStatusPending, now-10)
	seedOrder("TTEP_PAID_1", common.TopUpStatusSuccess, now-20)

	// ListAdtyTeamTopups(pendingOnly=true) excludes expired pending rows.
	pendingRows, err := ListAdtyTeamTopups(301, 30, true)
	require.NoError(t, err)
	require.Len(t, pendingRows, 2)
	assert.Equal(t, "TTEP_PEND_NOEXP", pendingRows[0].TradeNo)
	assert.Equal(t, "TTEP_PEND_ACTIVE", pendingRows[1].TradeNo)

	// ListAdtyTeamTopupsPage returns enriched views with status and pagination.
	page1, total, err := ListAdtyTeamTopupsPage(301, 30, "", 1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	require.Len(t, page1, 2)
	assert.Equal(t, "TTEP_PAID_1", page1[0].TradeNo)
	assert.Equal(t, common.TopUpStatusSuccess, page1[0].Status)
	require.NotNil(t, page1[0].Ext)

	pendPage, pendTotal, err := ListAdtyTeamTopupsPage(301, 30, "pending", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), pendTotal)
	require.Len(t, pendPage, 2)

	// Non-leader is forbidden.
	_, _, err = ListAdtyTeamTopupsPage(302, 30, "", 1, 10)
	assert.ErrorIs(t, err, ErrAdtyTeamForbidden)

	// Seed ledgers and verify ListAdtyTeamLedgersForLeader.
	require.NoError(t, db.Create(&model.AdtyTeamLedger{
		TeamId: 30, DeltaQuota: 500000, BalanceBefore: 0, BalanceAfter: 500000,
		Type: "topup", RefId: "TTEP_PAID_1", ActorUserId: 301, IdempotencyKey: "topup:TTEP_PAID_1",
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTeamLedger{
		TeamId: 30, DeltaQuota: -100000, BalanceBefore: 500000, BalanceAfter: 400000,
		Type: "spend", RefId: "req-1", ActorUserId: 302, IdempotencyKey: "spend:req-1",
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTeamLedger{
		TeamId: 30, DeltaQuota: -50000, BalanceBefore: 400000, BalanceAfter: 350000,
		Type: "spend", RefId: "req-2", ActorUserId: 302, IdempotencyKey: "spend:req-2",
	}).Error)

	ledgers, lTotal, err := ListAdtyTeamLedgersForLeader(301, 30, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), lTotal)
	require.Len(t, ledgers, 2)
	assert.Equal(t, "req-2", ledgers[0].RefId)
	assert.Equal(t, "req-1", ledgers[1].RefId)

	ledgers2, _, err := ListAdtyTeamLedgersForLeader(301, 30, 2, 2)
	require.NoError(t, err)
	require.Len(t, ledgers2, 1)
	assert.Equal(t, "TTEP_PAID_1", ledgers2[0].RefId)

	// Non-leader cannot view team ledgers.
	_, _, err = ListAdtyTeamLedgersForLeader(302, 30, 1, 20)
	assert.ErrorIs(t, err, ErrAdtyTeamForbidden)
}
