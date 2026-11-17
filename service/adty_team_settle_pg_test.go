package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ISS-R4-0017：绑定团队的 Key 在结算补扣时扣团队共享余额、退还时退回团队，
// 成员个人钱包全程不动。预扣侧有 AdtyTeamReserveHook，结算侧必须有一支对称的
// 钩子，否则差额落到成员个人钱包（补扣扣错人，退还凭空多给），团队余额停在预扣值。
type teamSettleFixture struct {
	db     *gorm.DB
	member *model.User
	token  *model.Token
	team   model.AdtyTeam
}

func newTeamSettleFixture(t *testing.T, personalQuota int, teamQuota int64) *teamSettleFixture {
	t.Helper()
	db := prepareReferralDB(t)
	leader := referralUser(t, db, "team-settle-leader")
	member := referralUser(t, db, "team-settle-member")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", member.Id).Update("quota", personalQuota).Error)
	member.Quota = personalQuota
	created, err := CreateAdtyTeam(leader.Id, 0, "Settle", decimal.NewFromInt(1))
	require.NoError(t, err)
	_, err = JoinAdtyTeam(member.Id, created.Team.Id)
	require.NoError(t, err)
	token := &model.Token{UserId: member.Id, Key: fmt.Sprintf("team-settle-%d", member.Id), Name: "team-settle",
		RemainQuota: 100_000_000, UnlimitedQuota: true, Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, db.Create(token).Error)
	require.NoError(t, BindAdtyTeamKey(member.Id, token.Id))
	require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, teamQuota, "team-settle-credit"))
	var team model.AdtyTeam
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	return &teamSettleFixture{db: db, member: member, token: token, team: team}
}

func (f *teamSettleFixture) reserve(t *testing.T, key string, quota int) {
	t.Helper()
	_, err := model.ReserveBillingOperation(model.BillingOperation{
		OperationKey: key, UserId: f.member.Id, TokenId: f.token.Id, Kind: "request",
	}, quota)
	require.NoError(t, err)
}

func (f *teamSettleFixture) settle(t *testing.T, key string, actual int) *model.BillingOperation {
	t.Helper()
	require.NoError(t, model.QueueBillingSettlement(key, actual, false))
	row, applied, err := model.ApplyBillingSettlement(key)
	require.NoError(t, err)
	require.True(t, applied)
	return row
}

func (f *teamSettleFixture) teamQuota(t *testing.T) int64 {
	t.Helper()
	var team model.AdtyTeam
	require.NoError(t, f.db.First(&team, f.team.Id).Error)
	return team.SharedQuota
}

func (f *teamSettleFixture) personalQuota(t *testing.T) int {
	t.Helper()
	var user model.User
	require.NoError(t, f.db.First(&user, f.member.Id).Error)
	return user.Quota
}

// ledgerSum 汇总团队流水中「预扣/结算/退还」三类：充值那条 credit 是夹具的初始
// 入账，不算在这次请求的余额变化里。
func (f *teamSettleFixture) ledgerSum(t *testing.T) (int64, int64) {
	t.Helper()
	var rows []model.AdtyTeamLedger
	require.NoError(t, f.db.Where("team_id = ? AND type IN ?", f.team.Id,
		[]string{"reserve", "settle", "refund"}).Find(&rows).Error)
	var sum int64
	for _, row := range rows {
		sum += row.DeltaQuota
	}
	return sum, int64(len(rows))
}

// 补扣：预扣 50,000、实际 150,000，额外 100,000 必须从团队共享余额扣。
func TestTeamKeySettleTopUpComesFromSharedBalancePG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 50_000_000)
	f.reserve(t, "request:team-topup-1", 50_000)
	require.EqualValues(t, 49_950_000, f.teamQuota(t), "预扣从团队余额扣")

	row := f.settle(t, "request:team-topup-1", 150_000)

	assert.EqualValues(t, 49_850_000, f.teamQuota(t), "补扣 100,000 也从团队共享余额扣")
	assert.EqualValues(t, 500_000, f.personalQuota(t), "成员个人钱包全程不动")
	assert.EqualValues(t, 150_000, row.AppliedQuota)
	assert.Zero(t, row.Shortfall)
}

// 退还：预扣 50,000、实际 10,000，未用的 40,000 必须退回团队，不能进成员钱包。
func TestTeamKeySettleRefundReturnsToSharedBalancePG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 50_000_000)
	f.reserve(t, "request:team-refund-1", 50_000)
	require.EqualValues(t, 49_950_000, f.teamQuota(t))

	row := f.settle(t, "request:team-refund-1", 10_000)

	assert.EqualValues(t, 49_990_000, f.teamQuota(t), "未用的 40,000 退回团队共享余额")
	assert.EqualValues(t, 500_000, f.personalQuota(t), "成员个人钱包不得被凭空加钱")
	assert.EqualValues(t, 10_000, row.AppliedQuota)
}

// 团队余额不够补扣时，扣到 0 为止，差额记 shortfall；成员钱包仍然不动。
func TestTeamKeySettleTopUpStopsAtTeamBalancePG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 60_000)
	f.reserve(t, "request:team-short-1", 50_000)
	require.EqualValues(t, 10_000, f.teamQuota(t))

	row := f.settle(t, "request:team-short-1", 150_000)

	assert.EqualValues(t, 0, f.teamQuota(t), "团队余额扣到 0 为止")
	assert.EqualValues(t, 500_000, f.personalQuota(t), "差额不得转嫁给成员个人钱包")
	assert.EqualValues(t, 90_000, row.Shortfall)
	assert.EqualValues(t, 60_000, row.AppliedQuota)
}

// 预扣与结算交错：团队余额的净变化必须等于实际消耗，成员个人余额净变化为 0，
// 团队流水合计等于团队余额变化。
func TestTeamKeyReserveSettleKeepsSharedBalanceConservedPG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 50_000_000)
	actuals := []int{30_000, 60_000, 45_000, 55_000}

	var wg sync.WaitGroup
	errs := make(chan error, len(actuals)*2)
	for i, actual := range actuals {
		key := fmt.Sprintf("request:team-conc-%d", i)
		wg.Add(1)
		go func(i, actual int, key string) {
			defer wg.Done()
			if _, err := model.ReserveBillingOperation(model.BillingOperation{
				OperationKey: key, UserId: f.member.Id, TokenId: f.token.Id, Kind: "request",
			}, 50_000); err != nil {
				errs <- err
				return
			}
			// 让别的预扣/结算穿插进来，制造团队行锁上的真实交错。
			time.Sleep(time.Duration(i%3) * 2 * time.Millisecond)
			if err := model.QueueBillingSettlement(key, actual, false); err != nil {
				errs <- err
				return
			}
			if _, _, err := model.ApplyBillingSettlement(key); err != nil {
				errs <- err
			}
		}(i, actual, key)
	}
	wg.Wait()
	close(errs)
	var collected []error
	for err := range errs {
		collected = append(collected, err)
	}
	require.Empty(t, collected, "concurrent reserve/settle must not fail")

	var consumed int64
	for _, actual := range actuals {
		consumed += int64(actual)
	}
	assert.EqualValues(t, 50_000_000-consumed, f.teamQuota(t), "团队余额净变化 = 实际消耗")
	assert.EqualValues(t, 500_000, f.personalQuota(t), "成员个人余额净变化为 0")
	ledgerDelta, rows := f.ledgerSum(t)
	assert.EqualValues(t, -consumed, ledgerDelta, "团队流水合计 = 团队余额变化")
	assert.EqualValues(t, int64(len(actuals))*2, rows, "每笔一笔预扣 + 一笔结算流水")
}

// 团队 Key 的准入门槛是团队共享余额：个人余额为 0 的成员拿着有余额的团队 Key 必须放行。
func TestTeamKeyAdmissionUsesSharedBalancePG(t *testing.T) {
	f := newTeamSettleFixture(t, 0, 5_000_000)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenId, f.token.Id)
	info := &relaycommon.RelayInfo{
		UserId: f.member.Id, TokenId: f.token.Id, TokenKey: f.token.Key, TokenUnlimited: true,
		RequestId: "team-admission-1", StartTime: time.Now(), UsingGroup: "default", UserGroup: "default",
		PriceData: hosttypes.PriceData{UsePrice: true, ModelPrice: 1,
			GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
	}

	session, apiErr := NewBillingSession(ctx, info, 50_000)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.EqualValues(t, 4_950_000, f.teamQuota(t), "预扣从团队共享余额扣")
	assert.EqualValues(t, 0, f.personalQuota(t), "个人钱包不动")
}

func (f *teamSettleFixture) rebind(t *testing.T) model.AdtyTeam {
	t.Helper()
	require.NoError(t, LeaveAdtyTeam(f.member.Id, f.team.Id))
	leader := referralUser(t, f.db, "team-settle-other-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "Other", decimal.NewFromInt(1))
	require.NoError(t, err)
	invite, err := CreateAdtyTeamInvite(leader.Id, created.Team.Id, time.Hour)
	require.NoError(t, err)
	_, err = AcceptAdtyTeamInvite(f.member.Id, invite.Code)
	require.NoError(t, err)
	token, err := model.GetTokenById(f.token.Id)
	require.NoError(t, err)
	token.Status = common.TokenStatusEnabled
	require.NoError(t, token.SelectUpdate())
	require.NoError(t, BindAdtyTeamKey(f.member.Id, f.token.Id))
	require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 200_000, "other-credit"))
	return created.Team
}

// ISS-R1-0001：把结算或第二次预扣改回读取当前绑定，将使这些真实资金余额断言失败。
func TestBillingPayerSnapshotSurvivesRebindPG(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reserved  int
		increase  int
		actual    int
		wantQuota int64
	}{
		{name: "refund", reserved: 50_000, actual: 10_000, wantQuota: 90_000},
		{name: "extra_charge", reserved: 50_000, actual: 80_000, wantQuota: 20_000},
		{name: "reserve_increase", reserved: 10_000, increase: 50_000, actual: 50_000, wantQuota: 50_000},
		{name: "zero_reservation", actual: 20_000, wantQuota: 80_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTeamSettleFixture(t, 500_000, 100_000)
			const key = "request:frozen-team-payer"
			f.reserve(t, key, tc.reserved)
			other := f.rebind(t)
			if tc.increase > 0 {
				f.reserve(t, key, tc.increase)
			}
			f.settle(t, key, tc.actual)
			assert.EqualValues(t, tc.wantQuota, f.teamQuota(t), "原团队承担整笔请求")
			require.NoError(t, f.db.First(&other, other.Id).Error)
			assert.EqualValues(t, 200_000, other.SharedQuota, "新团队不接收旧请求退款或补扣")
			assert.EqualValues(t, 500_000, f.personalQuota(t))
			_, applied, err := model.ApplyBillingSettlement(key)
			require.NoError(t, err)
			assert.False(t, applied, "恢复重放不得再次动账")
			assert.EqualValues(t, tc.wantQuota, f.teamQuota(t))
		})
	}
}

func TestBillingPayerSnapshotKeepsPersonalWalletAfterBindingPG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 100_000)
	f.token = &model.Token{UserId: f.member.Id, Key: "personal-before-bind", Name: "personal",
		RemainQuota: 1_000_000, UnlimitedQuota: true, Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, f.token.Insert())
	const key = "request:personal-before-bind"
	f.reserve(t, key, 50_000)
	require.EqualValues(t, 450_000, f.personalQuota(t))
	require.NoError(t, BindAdtyTeamKey(f.member.Id, f.token.Id))
	f.settle(t, key, 10_000)
	assert.EqualValues(t, 490_000, f.personalQuota(t), "原个人预扣的差额退回个人账户")
	assert.EqualValues(t, 100_000, f.teamQuota(t), "后续团队绑定不能接管原个人请求")
}

func TestBillingPayerSnapshotRefundsAfterKeyDeletionPG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 100_000)
	const key = "request:deleted-team-key"
	f.reserve(t, key, 50_000)
	require.NoError(t, RevokeAdtyTeamKey(f.member.Id, f.team.Id, f.token.Id))
	require.NoError(t, model.DeleteTokenById(f.token.Id, f.member.Id))
	f.settle(t, key, 10_000)
	assert.EqualValues(t, 90_000, f.teamQuota(t))
	assert.EqualValues(t, 500_000, f.personalQuota(t))
}

func TestBillingPayerSnapshotTaskAdjustmentAndRefundPG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 100_000)
	require.NoError(t, f.db.AutoMigrate(&model.Task{}))
	const root = "request:frozen-task-payer"
	f.reserve(t, root, 50_000)
	task := &model.Task{UserId: f.member.Id, TaskID: "frozen-task-payer", Quota: 50_000,
		PrivateData: model.TaskPrivateData{TokenId: f.token.Id, BillingOperationKey: root}}
	require.NoError(t, task.Insert())
	_, applied, err := model.ApplyBillingSettlement(root)
	require.NoError(t, err)
	require.True(t, applied)
	other := f.rebind(t)
	for _, tc := range []struct {
		target int
		refund bool
		want   int64
	}{{target: 10_000, want: 90_000}, {refund: true, want: 100_000}} {
		var persisted model.Task
		require.NoError(t, f.db.First(&persisted, task.ID).Error)
		key, err := model.QueueTaskBillingOperation(&persisted, tc.target, tc.refund)
		require.NoError(t, err)
		_, applied, err := model.ApplyBillingSettlement(key)
		require.NoError(t, err)
		require.True(t, applied)
		assert.EqualValues(t, tc.want, f.teamQuota(t))
	}
	require.NoError(t, f.db.First(&other, other.Id).Error)
	assert.EqualValues(t, 200_000, other.SharedQuota)
	assert.EqualValues(t, 500_000, f.personalQuota(t))
}

func TestBillingPayerSnapshotDirectChargeInheritsRequestPG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 100_000)
	const root = "request:frozen-direct-payer"
	f.reserve(t, root, 50_000)
	f.settle(t, root, 50_000)
	other := f.rebind(t)
	require.NoError(t, model.QueueDirectBillingDelta(model.BillingOperation{
		OperationKey: "direct:frozen-direct-payer:0", UserId: f.member.Id,
		TokenId: f.token.Id, EarningRootKey: root,
	}, 20_000))
	_, applied, err := model.ApplyBillingSettlement("direct:frozen-direct-payer:0")
	require.NoError(t, err)
	require.True(t, applied)
	assert.EqualValues(t, 30_000, f.teamQuota(t))
	require.NoError(t, f.db.First(&other, other.Id).Error)
	assert.EqualValues(t, 200_000, other.SharedQuota)
	assert.EqualValues(t, 500_000, f.personalQuota(t))
}

func TestBillingPayerSnapshotMidjourneyPersistsBeforeChargePG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 100_000)
	require.NoError(t, f.db.AutoMigrate(&model.Midjourney{}))
	info := &relaycommon.RelayInfo{UserId: f.member.Id, TokenId: f.token.Id}
	task := &model.Midjourney{UserId: f.member.Id, MjId: "frozen-mj-payer"}
	prepared, err := PrepareMidjourneyTaskBilling(info, task, 50_000, true)
	require.NoError(t, err)
	require.True(t, prepared)
	other := f.rebind(t)
	require.NoError(t, task.Insert())
	var persisted model.Midjourney
	require.NoError(t, f.db.First(&persisted, task.Id).Error)
	billed, err := SettleMidjourneyTaskBilling(info, &persisted, prepared)
	require.NoError(t, err)
	require.True(t, billed)
	assert.EqualValues(t, 50_000, f.teamQuota(t), "恢复结算沿用受理时团队")
	require.True(t, RefundMidjourneyQuota(context.Background(), &persisted, "test upstream failure"))
	assert.EqualValues(t, 100_000, f.teamQuota(t))
	require.NoError(t, f.db.First(&other, other.Id).Error)
	assert.EqualValues(t, 200_000, other.SharedQuota)
	assert.EqualValues(t, 500_000, f.personalQuota(t))
}

// These fixtures represent operations written by the pre-snapshot version.
// Only the newly introduced identity metadata is cleared; balances and original
// movements are produced by the real reservation, never manufactured as success.
func TestBillingPayerSnapshotRecoversLegacyReservationPG(t *testing.T) {
	for _, tc := range []struct {
		name     string
		personal bool
	}{
		{name: "original_team_ledger"},
		{name: "original_personal_movement", personal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTeamSettleFixture(t, 500_000, 100_000)
			if tc.personal {
				f.token = &model.Token{UserId: f.member.Id, Key: "legacy-personal", RemainQuota: 1_000_000,
					UnlimitedQuota: true, Status: common.TokenStatusEnabled, ExpiredTime: -1}
				require.NoError(t, f.token.Insert())
			}
			const key = "request:legacy-payer"
			f.reserve(t, key, 50_000)
			if f.db.Migrator().HasColumn(&model.BillingOperation{}, "payer_kind") {
				require.NoError(t, f.db.Model(&model.BillingOperation{}).Where("operation_key = ?", key).
					UpdateColumns(map[string]any{"payer_kind": "", "payer_team_id": 0}).Error)
			}
			if tc.personal {
				require.NoError(t, BindAdtyTeamKey(f.member.Id, f.token.Id))
			} else {
				other := f.rebind(t)
				f.settle(t, key, 10_000)
				assert.EqualValues(t, 90_000, f.teamQuota(t))
				require.NoError(t, f.db.First(&other, other.Id).Error)
				assert.EqualValues(t, 200_000, other.SharedQuota)
				return
			}
			f.settle(t, key, 10_000)
			assert.EqualValues(t, 490_000, f.personalQuota(t))
			assert.EqualValues(t, 100_000, f.teamQuota(t))
		})
	}
}

func TestBillingPayerSnapshotDoesNotGuessLegacyZeroReservationPG(t *testing.T) {
	f := newTeamSettleFixture(t, 500_000, 100_000)
	const key = "request:legacy-zero-payer"
	f.reserve(t, key, 0)
	if f.db.Migrator().HasColumn(&model.BillingOperation{}, "payer_kind") {
		require.NoError(t, f.db.Model(&model.BillingOperation{}).Where("operation_key = ?", key).
			UpdateColumns(map[string]any{"payer_kind": "", "payer_team_id": 0}).Error)
	}
	other := f.rebind(t)
	require.NoError(t, model.QueueBillingSettlement(key, 20_000, false))
	_, applied, err := model.ApplyBillingSettlement(key)
	require.ErrorIs(t, err, model.ErrBillingOperationConflict, "原账户无证据时保留待核查，不能猜当前绑定")
	assert.False(t, applied)
	assert.EqualValues(t, 100_000, f.teamQuota(t))
	require.NoError(t, f.db.First(&other, other.Id).Error)
	assert.EqualValues(t, 200_000, other.SharedQuota)
	assert.EqualValues(t, 500_000, f.personalQuota(t))
	row, err := model.GetBillingOperation(key)
	require.NoError(t, err)
	assert.Equal(t, model.BillingSettlePending, row.State)
}

func TestBillingPayerSnapshotLegacyDirectSiblingPG(t *testing.T) {
	for _, priorCharge := range []int{20_000, 0} {
		t.Run(fmt.Sprintf("prior_charge_%d", priorCharge), func(t *testing.T) {
			f := newTeamSettleFixture(t, 500_000, 100_000)
			const root = "request:legacy-direct-without-root"
			const first = "direct:legacy-direct-without-root:0"
			require.NoError(t, model.QueueDirectBillingDelta(model.BillingOperation{
				OperationKey: first, EarningRootKey: root, UserId: f.member.Id, TokenId: f.token.Id,
			}, priorCharge))
			_, _, err := model.ApplyBillingSettlement(first)
			require.NoError(t, err)
			// Reproduce an upgrade: only erase metadata absent from the old schema.
			require.NoError(t, f.db.Model(&model.BillingOperation{}).Where("operation_key = ?", first).
				UpdateColumns(map[string]any{"payer_kind": "", "payer_team_id": 0}).Error)
			other := f.rebind(t)
			const next = "direct:legacy-direct-without-root:1"
			err = model.QueueDirectBillingDelta(model.BillingOperation{
				OperationKey: next, EarningRootKey: root, UserId: f.member.Id, TokenId: f.token.Id,
			}, 10_000)
			if priorCharge == 0 {
				require.ErrorIs(t, err, model.ErrBillingOperationConflict, "an unproved sibling cannot authorize a new payer")
				assert.EqualValues(t, 100_000, f.teamQuota(t))
			} else {
				require.NoError(t, err)
				_, _, err = model.ApplyBillingSettlement(next)
				require.NoError(t, err)
				assert.EqualValues(t, 70_000, f.teamQuota(t), "legacy movements preserve the original payer")
			}
			require.NoError(t, f.db.First(&other, other.Id).Error)
			assert.EqualValues(t, 200_000, other.SharedQuota)
			assert.EqualValues(t, 500_000, f.personalQuota(t))
		})
	}
}
