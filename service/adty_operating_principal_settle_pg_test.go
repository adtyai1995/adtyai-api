package service

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 站点运营钱包的扣款入口只有 model.AdtyAfterPrincipalSpentTx。正常结算路径
// （预扣登记过分摊行）走的是 deductAdtyFundBatchTx，它不触发这个 hook，
// 于是「站长该付给平台的本金」在绝大多数用户身上从来没被扣过。
//
// 这个 fixture 造的就是最常见的形状：用户有本金批次，挂在站点下，
// 预扣 → 结算。断言站点运营钱包按本金份额减少。
type operatingSettleFixture struct {
	db       *gorm.DB
	site     *model.AdtySite
	customer *model.User
}

func TestOwnerSelfUseZeroEstimateRequiresSitePrincipalPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	owner := f.site.OwnerUserId
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", owner).Update("quota", 50000000).Error)
	require.NoError(t, model.CreateAdtyFundBatchesWithSiteTx(f.db, owner, model.AdtyFundBatchSiteID(0), "main-funds", 50000000, 0))
	key := model.Token{Key: "self-empty-site", UserId: owner, OwnerSelfUseSiteId: f.site.Id, UnlimitedQuota: true}
	require.NoError(t, f.db.Create(&key).Error)
	ctx, info := snapshotRelay(t, owner, "default", hosttypes.PriceData{ModelPrice: 1, UsePrice: true})
	ctx.Set(AdtyOwnerSelfUseContextKey, f.site)
	ctx.Set(AdtySiteIdContextKey, f.site.Id)
	ctx.Set(AdtySiteMarkupContextKey, float64(1))
	ctx.Set(AdtySiteCostFactorContextKey, 0.8)
	info.TokenId, info.TokenUnlimited = key.Id, true
	session, apiErr := NewBillingSession(ctx, info, 0)
	require.NotNil(t, apiErr, "main-site funds cannot admit a self-use request with no site principal")
	require.Nil(t, session)
}

func TestOwnerSelfUseMixedWalletSupplementCannotUseMainPrincipalPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	owner := f.site.OwnerUserId
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", owner).Update("quota", 50000000).Error)
	require.NoError(t, model.CreateAdtyFundBatchesWithSiteTx(f.db, owner, model.AdtyFundBatchSiteID(0), "main-funds", 50000000, 0))
	_, err := ApplyAdtySiteWalletChange(f.site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(100), AdtySiteWalletChange{Type: "fixture", IdempotencyKey: "mixed-earnings"})
	require.NoError(t, err)
	_, err = TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(1), "mixed-transfer")
	require.NoError(t, err)
	key := model.Token{Key: "mixed-self", UserId: owner, OwnerSelfUseSiteId: f.site.Id, UnlimitedQuota: true}
	require.NoError(t, f.db.Create(&key).Error)
	op := model.BillingOperation{OperationKey: "request:mixed-owner", UserId: owner, TokenId: key.Id, TokenUnlimited: true, SiteId: f.site.Id, SiteMarkup: 1, SiteCostFactor: .8}
	_, err = model.ReserveBillingOperation(op, 500001)
	require.ErrorIs(t, err, model.ErrBillingWalletInsufficient)
	_, err = model.ReserveBillingOperation(op, 400000)
	require.NoError(t, err)
	f.settle(t, op.OperationKey, 600000)
	settled, err := model.GetBillingOperation(op.OperationKey)
	require.NoError(t, err)
	require.Equal(t, 500000, settled.AppliedQuota)
	require.Equal(t, 100000, settled.Shortfall)
	var user model.User
	require.NoError(t, f.db.First(&user, owner).Error)
	require.Equal(t, 50000000, user.Quota)
	var mainLot model.AdtyFundBatch
	require.NoError(t, f.db.Where("user_id = ? AND source = ?", owner, "main-funds").First(&mainLot).Error)
	require.EqualValues(t, 50000000, mainLot.Remaining)
	require.True(t, f.operating(t).IsZero())
	var earnings model.AdtySiteWallet
	require.NoError(t, f.db.Where("site_id = ? AND kind = ?", f.site.Id, model.AdtySiteWalletEarnings).First(&earnings).Error)
	require.Equal(t, "99", earnings.Balance.String())
}

func TestOwnerSelfUsePrincipalCannotPayOrdinaryRequestPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	owner := f.site.OwnerUserId
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", owner).Update("quota", 10000000).Error)
	require.NoError(t, model.CreateAdtyFundBatchesWithSiteTx(f.db, owner, model.AdtyFundBatchSiteID(f.site.Id), "owner-voucher", 10000000, 0))
	_, err := model.ReserveBillingOperation(model.BillingOperation{OperationKey: "request:ordinary-owner", UserId: owner}, 400000)
	require.ErrorIs(t, err, model.ErrBillingWalletInsufficient)
	reserved, err := model.TryReserveUserQuotaForOperation(owner, 400000, "legacy:ordinary-owner")
	require.NoError(t, err)
	require.False(t, reserved, "legacy reservations must not spend owner site principal")
}

func TestOwnerSelfUseRefundKeepsOriginalPrincipalPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	owner := f.site.OwnerUserId
	_, err := ApplyAdtySiteWalletChange(f.site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(20), AdtySiteWalletChange{Type: "fixture", IdempotencyKey: "owner-refund-fund"})
	require.NoError(t, err)
	_, err = TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(20), "refund-fund")
	require.NoError(t, err)
	key := model.Token{Key: "owner-refund", UserId: owner, OwnerSelfUseSiteId: f.site.Id, UnlimitedQuota: true}
	require.NoError(t, f.db.Create(&key).Error)
	op := model.BillingOperation{OperationKey: "request:owner-refund", UserId: owner, TokenId: key.Id, TokenUnlimited: true, SiteId: f.site.Id, SiteMarkup: 1, SiteCostFactor: .8}
	_, err = model.ReserveBillingOperation(op, 400000)
	require.NoError(t, err)
	require.NoError(t, f.db.AutoMigrate(&model.Midjourney{}))
	mj := model.Midjourney{UserId: owner, TokenId: key.Id, Quota: 400000, BillingChargePending: true, BillingOperationKey: op.OperationKey, BillingSiteId: f.site.Id, BillingSiteMarkup: 1, BillingSiteCostFactor: .8, BillingPayerKind: model.BillingPayerOwner}
	require.NoError(t, f.db.Create(&mj).Error)
	chargeKey, err := model.QueueMidjourneyBillingOperation(&mj, false)
	require.NoError(t, err)
	_, _, err = model.ApplyBillingSettlement(chargeKey)
	require.NoError(t, err)
	require.NoError(t, f.db.First(&mj, mj.Id).Error)
	// Closing the site and deleting the key must not change a historical refund.
	require.NoError(t, f.db.Delete(&key).Error)
	require.NoError(t, f.db.Model(f.site).Update("status", model.AdtySiteStatusClosing).Error)
	refundKey, err := model.QueueMidjourneyBillingOperation(&mj, true)
	require.NoError(t, err)
	_, _, err = model.ApplyBillingSettlement(refundKey)
	require.NoError(t, err)
	left, err := model.AdtyOwnerPrincipalAvailable(f.db, owner, f.site.Id)
	require.NoError(t, err)
	require.EqualValues(t, 10000000, left)
	require.Equal(t, "20", f.operating(t).String())
}

func TestOwnerSelfUseTransferAndSettlementPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	owner := f.site.OwnerUserId
	_, err := ApplyAdtySiteWalletChange(f.site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(100), AdtySiteWalletChange{Type: "fixture", IdempotencyKey: "earn-initial"})
	require.NoError(t, err)
	transfer, err := TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(20), "move")
	require.NoError(t, err)
	again, err := TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(20), "move")
	require.NoError(t, err)
	require.Equal(t, transfer, again)
	_, err = TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(21), "move")
	require.Error(t, err)
	_, err = TransferAdtyEarningsToPersonal(f.site.Id, f.customer.Id, decimal.NewFromInt(20), "other")
	require.Error(t, err)
	_, err = TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(81), "overspend")
	require.Error(t, err)
	var wallet model.AdtySiteWallet
	require.NoError(t, f.db.Where("site_id = ? AND kind = ?", f.site.Id, model.AdtySiteWalletEarnings).First(&wallet).Error)
	require.Equal(t, "80", wallet.Balance.String())
	key := model.Token{Key: "self", UserId: owner, OwnerSelfUseSiteId: f.site.Id, UnlimitedQuota: true}
	require.NoError(t, f.db.Create(&key).Error)
	op := model.BillingOperation{OperationKey: "request:owner-self", UserId: owner, TokenId: key.Id, TokenUnlimited: true, SiteId: f.site.Id, SiteMarkup: 1, SiteCostFactor: 0.8}
	op.PriceSnapshot, err = model.EncodeRequestPriceSnapshot(&model.RequestPriceSnapshot{SchemaVersion: model.RequestPriceSnapshotVersion, ModelName: "self-model", ModelBasePrice: "1", BillingGroup: "default", GroupRatio: "1", SiteCostFactor: "0.8", SiteMarkupRatio: "0", Currency: "USD", AcceptedAt: time.Now().Unix(), RuleVersion: "owner-test", FinalPrice: "0.8", BillingUnit: model.BillingUnitPerCall})
	require.NoError(t, err)
	_, err = model.ReserveBillingOperation(op, 10000001)
	require.ErrorIs(t, err, model.ErrBillingWalletInsufficient)
	_, err = model.ReserveBillingOperation(op, 500000)
	require.NoError(t, err)
	f.settle(t, op.OperationKey, 400000)
	left, err := model.AdtyOwnerPrincipalAvailable(f.db, owner, f.site.Id)
	require.NoError(t, err)
	require.EqualValues(t, 9600000, left)
	require.Equal(t, "19.2", f.operating(t).String())
	var count int64
	require.NoError(t, f.db.Model(&model.AdtySiteEarningEvent{}).Where("operation_key = ?", op.OperationKey).Count(&count).Error)
	require.Zero(t, count)
	var entry model.AdtySiteWalletLedger
	require.NoError(t, f.db.Where("ref_id = ? AND ref_type = ?", op.OperationKey, model.BillingPayerOwner).First(&entry).Error)
	require.Equal(t, "-0.8", entry.Delta.String())
	require.NoError(t, f.db.AutoMigrate(&model.TopUp{}))
	stats, err := ComputeAdtySiteTierStats(f.site.Id)
	require.NoError(t, err)
	require.True(t, stats.PrincipalUSD.IsZero())
	require.True(t, stats.ConsumptionUSD.IsZero())
	require.NoError(t, f.db.AutoMigrate(&model.TopUp{}))
	now := time.Now().Unix()
	sheet, err := summarizeAdtySiteSettlement(f.site.Id, time.Now().Format("2006-01-02"), now-3600, now+3600)
	require.NoError(t, err)
	require.Equal(t, "20", sheet.OwnerTransferUSD.String())
	require.Equal(t, "0.8", sheet.OwnerSelfUseUSD.String())
	require.Equal(t, "0.8", sheet.OwnerSelfUseCostUSD.String())
	require.True(t, sheet.ConsumptionUSD.IsZero())
	require.True(t, sheet.SpreadUSD.IsZero())
	require.NoError(t, f.db.AutoMigrate(&model.Log{}))
	require.NoError(t, model.LOG_DB.Create(&model.Log{UserId: owner, RequestId: "owner-self", Type: model.LogTypeConsume, PromptTokens: 12, CompletionTokens: 8}).Error)
	rows, total, err := ListAdtyOwnerSelfUseUsage(f.site.Id, owner, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, "self-model", rows[0].ModelName)
	require.Equal(t, "0.8", rows[0].ChargedUSD.String())
	require.Equal(t, "0.8", rows[0].SupplyUnitPrice)
	require.Equal(t, 12, rows[0].PromptTokens)
	transfers, total, err := ListAdtyOwnerPrincipalTransfers(f.site.Id, owner, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, transfers, 1)
	require.Equal(t, "100", transfers[0].EarningsBefore.String())
	require.Equal(t, "80", transfers[0].EarningsAfter.String())
	require.Equal(t, "0", transfers[0].PersonalBefore.String())
	require.Equal(t, "20", transfers[0].PersonalAfter.String())
	_, _, err = ListAdtyOwnerSelfUseUsage(f.site.Id, f.customer.Id, 0, 20)
	require.ErrorIs(t, err, ErrAdtySiteNotOwner)
}

func TestOwnerSelfUseTransferFrozenConcurrentAndRollbackPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	owner := f.site.OwnerUserId
	_, err := ApplyAdtySiteWalletChange(f.site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(100), AdtySiteWalletChange{Type: "fixture", IdempotencyKey: "concurrent-fund"})
	require.NoError(t, err)
	err = f.db.Transaction(func(tx *gorm.DB) error {
		_, err := applyAdtySiteWalletChangeTx(tx, f.site.Id, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{Type: "fixture", HoldDelta: decimal.NewFromInt(80), IdempotencyKey: "concurrent-hold"})
		return err
	})
	require.NoError(t, err)
	_, err = TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(21), "held")
	require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)
	var user model.User
	require.NoError(t, f.db.First(&user, owner).Error)
	require.Zero(t, user.Quota, "the earlier personal credit must roll back")
	start := make(chan struct{})
	errs := make([]error, 2)
	refs := make([]string, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			<-start
			refs[i], errs[i] = TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(20), "same-intent")
		})
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, refs[0], refs[1])
	require.NoError(t, f.db.First(&user, owner).Error)
	require.Equal(t, 10000000, user.Quota)
	var count int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Where("ref_id = ?", refs[0]).Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, err = TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(1), "exhausted")
	require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)
}

func TestOwnerSelfUseRealtimeCountsPrincipalOncePG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	owner := f.site.OwnerUserId
	_, err := ApplyAdtySiteWalletChange(f.site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(20), AdtySiteWalletChange{Type: "fixture", IdempotencyKey: "stream-fund"})
	require.NoError(t, err)
	_, err = TransferAdtyEarningsToPersonal(f.site.Id, owner, decimal.NewFromInt(20), "stream")
	require.NoError(t, err)
	key := model.Token{Key: "stream-self", UserId: owner, OwnerSelfUseSiteId: f.site.Id, UnlimitedQuota: true}
	require.NoError(t, f.db.Create(&key).Error)
	root := model.BillingOperation{OperationKey: "request:owner-stream", UserId: owner, TokenId: key.Id, TokenUnlimited: true, SiteId: f.site.Id, SiteMarkup: 1, SiteCostFactor: .8}
	_, err = model.ReserveBillingOperation(root, 0)
	require.NoError(t, err)
	chunk := root
	chunk.OperationKey, chunk.EarningRootKey = "direct:owner-stream:0", root.OperationKey
	require.NoError(t, model.QueueDirectBillingDelta(chunk, 400000))
	_, _, err = model.ApplyBillingSettlement(chunk.OperationKey)
	require.NoError(t, err)
	require.NoError(t, model.QueueBillingSettlement(root.OperationKey, 300000, false, 400000, 400000))
	_, _, err = model.ApplyBillingSettlement(root.OperationKey)
	require.NoError(t, err)
	left, err := model.AdtyOwnerPrincipalAvailable(f.db, owner, f.site.Id)
	require.NoError(t, err)
	require.EqualValues(t, 9700000, left, "final $0.60 charge must refund $0.20 of the $0.80 stream charge")
	require.Equal(t, "19.4", f.operating(t).String())
}

// 站内客户的本金：$2（$1 = 500000 quota）。
const operatingSettlePrincipal = 1_000_000

func newOperatingSettleFixture(t *testing.T) *operatingSettleFixture {
	t.Helper()
	db := adtyTestDB(t)
	// 版本只作证据，不作通过条件（AGENTS.md：不设 PostgreSQL 版本上下限）。
	versionNum, version, err := model.OfficialPostgresServerVersion(db)
	require.NoError(t, err)
	t.Logf("evidence only: server_version_num=%s server_version=%s", versionNum, version)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	require.NoError(t, db.AutoMigrate(model.AdtySiteTables()...))
	common.BatchUpdateEnabled = false
	common.RedisEnabled = false

	owner := &model.User{Username: "op-settle-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Email: "op-settle-owner@example.com"}
	require.NoError(t, db.Create(owner).Error)

	site := &model.AdtySite{OwnerUserId: owner.Id, Subdomain: "op-settle", RootDomain: "sites.example.com",
		Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(site).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))

	customer := &model.User{Username: "op-settle-customer", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Email: "op-settle-customer@example.com",
		Quota: operatingSettlePrincipal, SiteId: site.Id}
	require.NoError(t, db.Create(customer).Error)
	require.NoError(t, db.Create(&model.AdtySiteCustomer{UserId: customer.Id, SiteId: site.Id}).Error)
	// 本金批次：站点充值入账时建出来的那种（source 不是 legacy）。
	require.NoError(t, db.Create(&model.AdtyFundBatch{
		UserId: customer.Id, Kind: "principal", Source: "topup",
		Original: operatingSettlePrincipal, Remaining: operatingSettlePrincipal,
	}).Error)

	return &operatingSettleFixture{db: db, site: site, customer: customer}
}

// fundOperating 给运营钱包垫上本金，模拟站长已经付过平台的那笔钱。
func (f *operatingSettleFixture) fundOperating(t *testing.T, usd string, key string) {
	t.Helper()
	_, err := ApplyAdtySiteWalletChange(f.site.Id, model.AdtySiteWalletOperating, decimal.RequireFromString(usd), AdtySiteWalletChange{
		Type: "topup_credit", RefType: "topup", RefID: key, IdempotencyKey: key,
	})
	require.NoError(t, err)
}

func (f *operatingSettleFixture) operating(t *testing.T) decimal.Decimal {
	t.Helper()
	var wallet model.AdtySiteWallet
	require.NoError(t, f.db.Where("site_id = ? AND kind = ?", f.site.Id, model.AdtySiteWalletOperating).First(&wallet).Error)
	return wallet.Balance
}

func (f *operatingSettleFixture) quota(t *testing.T) int {
	t.Helper()
	var user model.User
	require.NoError(t, f.db.First(&user, f.customer.Id).Error)
	return user.Quota
}

func (f *operatingSettleFixture) remaining(t *testing.T) int64 {
	t.Helper()
	var sum struct{ Remaining int64 }
	require.NoError(t, f.db.Model(&model.AdtyFundBatch{}).Where("user_id = ?", f.customer.Id).
		Select("coalesce(sum(remaining),0) as remaining").Scan(&sum).Error)
	return sum.Remaining
}

func (f *operatingSettleFixture) consumeLedgers(t *testing.T) []model.AdtySiteWalletLedger {
	t.Helper()
	var rows []model.AdtySiteWalletLedger
	require.NoError(t, f.db.Where("site_id = ? AND wallet_kind = ? AND type = ?",
		f.site.Id, model.AdtySiteWalletOperating, "principal_consume").Find(&rows).Error)
	return rows
}

func (f *operatingSettleFixture) reserve(t *testing.T, key string, quota int) {
	t.Helper()
	_, err := model.ReserveBillingOperation(model.BillingOperation{
		OperationKey: key, UserId: f.customer.Id, Kind: "request",
		SiteId: f.site.Id, SiteMarkup: 2, SiteCostFactor: 0.8,
	}, quota)
	require.NoError(t, err)
}

func (f *operatingSettleFixture) settle(t *testing.T, key string, actual int) {
	t.Helper()
	require.NoError(t, model.QueueBillingSettlement(key, actual, false))
	_, _, err := model.ApplyBillingSettlement(key)
	require.NoError(t, err)
}

// 正常结算（有分摊行）必须按本金份额扣站点运营钱包。
//
// 0.6 USD = 300000 quota / 500000。修前运营钱包停在 1（一分没动），
// 而用户的本金批次已经从 1,000,000 扣到 700,000。
func TestUsageSettlementDebitsSiteOperatingWalletPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	f.fundOperating(t, "1", "op-settle-fund-1")

	f.reserve(t, "request:op-settle-1", 300_000)
	// 预扣登记了分摊行 —— 这决定了结算走 deductAdtyFundBatchTx 而不是 hook 那条。
	var shares int64
	require.NoError(t, f.db.Model(&model.AdtyFundAllocation{}).
		Where("user_id = ? AND operation_id = ?", f.customer.Id, "request:op-settle-1").Count(&shares).Error)
	require.EqualValues(t, 1, shares, "本金批次足够，预扣必须登记分摊行（这就是「正常路径」）")

	f.settle(t, "request:op-settle-1", 300_000)

	assert.Equal(t, 700_000, f.quota(t), "用户余额扣掉 300000")
	assert.EqualValues(t, 700_000, f.remaining(t), "本金批次同步扣掉 300000")
	assert.Equal(t, "0.4", f.operating(t).String(),
		"站点运营钱包必须按本金份额减少 300000/500000 = 0.6 USD；停在 1 就是「站长该付的钱没扣」")

	ledgers := f.consumeLedgers(t)
	require.Len(t, ledgers, 1, "一次结算只记一条本金消费")
	assert.Equal(t, "-0.6", ledgers[0].Delta.String())
	assert.Equal(t, "request:op-settle-1", ledgers[0].RefId, "流水要指回这笔 operation")
}

// 结算重放不能重复扣：ApplyBillingSettlement 本身按 state 短路，
// 这里同时钉住运营钱包那条流水不会出现第二条。
func TestSettlementReplayDoesNotDebitOperatingWalletTwicePG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	f.fundOperating(t, "1", "op-settle-fund-replay")

	f.reserve(t, "request:op-replay-1", 300_000)
	f.settle(t, "request:op-replay-1", 300_000)
	before := f.operating(t)

	_, _, err := model.ApplyBillingSettlement("request:op-replay-1")
	require.NoError(t, err)
	assert.True(t, before.Equal(f.operating(t)), "重放不得再扣一次运营钱包")
	require.Len(t, f.consumeLedgers(t), 1)
}

// 退款（实际消耗 0）不能扣运营钱包：本金批次一分没动，就没有「平台成本」发生。
func TestRefundSettlementDoesNotDebitOperatingWalletPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	f.fundOperating(t, "1", "op-settle-fund-refund")

	f.reserve(t, "request:op-refund-1", 300_000)
	require.NoError(t, model.QueueBillingSettlement("request:op-refund-1", 0, true))
	_, _, err := model.ApplyBillingSettlement("request:op-refund-1")
	require.NoError(t, err)

	assert.Equal(t, operatingSettlePrincipal, f.quota(t), "全额退款，用户余额复原")
	assert.EqualValues(t, operatingSettlePrincipal, f.remaining(t), "批次从未被扣")
	assert.Equal(t, "1", f.operating(t).String(), "退款不是消费，运营钱包不能动")
	assert.Empty(t, f.consumeLedgers(t))
}

// 只消费一部分（预扣 300000，实际 120000）：运营钱包只按实际消耗的本金份额扣。
func TestPartialSettlementDebitsOnlyConsumedPrincipalPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	f.fundOperating(t, "1", "op-settle-fund-partial")

	f.reserve(t, "request:op-partial-1", 300_000)
	f.settle(t, "request:op-partial-1", 120_000)

	assert.Equal(t, 880_000, f.quota(t))
	assert.EqualValues(t, 880_000, f.remaining(t))
	assert.Equal(t, "0.76", f.operating(t).String(), "0.24 USD = 120000 quota")
}

// 异步任务的差额结算（kind=task 的 adjust operation）只承担**差额**：
// base 是 task.Quota，提交时那个 request operation 已经结过一次、批次早就扣过。
//
// 这条用例钉两件事：
//   - 运营钱包按差额扣（0.2 的首扣 + 0.1 的补扣），不是按「新的总额再扣一次」；
//   - 批次只再动 50000，不会被扣成「总额」并顺手补建一条 legacy 批次
//     （那条凭空出现的 legacy 正好会把 fund_batch_gap 的漂移抹平）。
func TestTaskAdjustmentDebitsOnlyTheIncrementPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	require.NoError(t, f.db.AutoMigrate(&model.Task{}, &model.Channel{}, &model.Midjourney{}, &model.Log{}))
	f.fundOperating(t, "1", "op-settle-fund-task")

	task := &model.Task{
		TaskID: "op-settle-task", UserId: f.customer.Id, Group: "default",
		Quota: 100_000, Status: model.TaskStatusInProgress,
		Properties: model.Properties{OriginModelName: "op-settle-model"},
	}
	require.NoError(t, f.db.Create(task).Error)

	// 提交时的 request operation：预扣 100000，落库时绑定到 task 上（InsertWithContext 的写法）。
	initialKey := "request:op-settle-task-submit"
	require.NoError(t, f.db.Create(&model.BillingOperation{
		OperationKey: initialKey, UserId: f.customer.Id, Kind: "request", ReferenceId: int64(task.ID),
		State: model.BillingSettlePending, ActualQuota: 100_000, ReservedQuota: 100_000,
		SiteId: f.site.Id, SiteMarkup: 2, SiteCostFactor: 0.8,
	}).Error)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return model.AllocateAdtyFundAllocationsTx(tx, f.customer.Id, initialKey, 100_000, operatingSettlePrincipal)
	}))
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.customer.Id).Update("quota", 900_000).Error)
	_, _, err := model.ApplyBillingSettlement(initialKey)
	require.NoError(t, err)
	require.Equal(t, 900_000, f.quota(t))
	require.EqualValues(t, 900_000, f.remaining(t))
	require.Equal(t, "0.8", f.operating(t).String(), "首扣 100000 quota = 0.2 USD")

	// 完成后按 150000 重算差额（RecalculateTaskQuota 的核心两步）。
	key, err := model.QueueTaskBillingOperation(task, 150_000, false)
	require.NoError(t, err)
	op, _, err := model.ApplyBillingSettlement(key)
	require.NoError(t, err)
	task.Quota = op.AppliedQuota

	assert.EqualValues(t, 100_000, op.ReservedQuota, "差额结算的 base 是 task 的旧额度（已被首扣结过）")
	assert.Equal(t, 850_000, f.quota(t), "用户只再付 50000")
	assert.EqualValues(t, 850_000, f.remaining(t), "批次也只再扣 50000")
	assert.Equal(t, "0.7", f.operating(t).String(), "运营钱包只按差额扣 0.1 USD，不是按新总额再扣一次")

	var lots []model.AdtyFundBatch
	require.NoError(t, f.db.Where("user_id = ?", f.customer.Id).Find(&lots).Error)
	require.Len(t, lots, 1, "差额结算不许补建 legacy 批次")
	assert.EqualValues(t, 0, op.FundBatchGap, "账平：差额确实被批次背书")

	ledgers := f.consumeLedgers(t)
	require.Len(t, ledgers, 2)
	assert.Equal(t, "-0.2", ledgers[0].Delta.String())
	assert.Equal(t, "-0.1", ledgers[1].Delta.String())
}

func TestSettlementWithInsufficientOperatingWalletRetriesAfterCoverPG(t *testing.T) {
	for _, balance := range []string{"0", "0.2"} {
		t.Run(balance, func(t *testing.T) {
			f := newOperatingSettleFixture(t)
			if balance != "0" {
				f.fundOperating(t, balance, "op-partial-cover")
			}
			key := "request:op-unfunded-1"
			f.reserve(t, key, 300_000)
			require.NoError(t, model.QueueBillingSettlement(key, 300_000, false))
			_, applied, err := model.ApplyBillingSettlement(key)
			require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)
			assert.False(t, applied)
			assert.Equal(t, balance, f.operating(t).String())
			assert.Equal(t, 700_000, f.quota(t), "the original reservation remains in place")
			assert.EqualValues(t, operatingSettlePrincipal, f.remaining(t), "failed settlement must not consume principal batches")
			assert.Empty(t, f.consumeLedgers(t))
			var operation model.BillingOperation
			require.NoError(t, f.db.Where("operation_key = ?", key).First(&operation).Error)
			assert.Equal(t, model.BillingSettlePending, operation.State)
			var events int64
			require.NoError(t, f.db.Model(&model.AdtySiteEarningEvent{}).Where("operation_key = ?", key).Count(&events).Error)
			assert.Zero(t, events, "failed settlement must not publish earnings")

			root := &model.User{Username: "op-cover-root", Password: "hashed-password", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
			require.NoError(t, f.db.Create(root).Error)
			missing := decimal.RequireFromString("0.6").Sub(decimal.RequireFromString(balance))
			_, err = ApplyAdtySiteWalletChange(f.site.Id, model.AdtySiteWalletOperating, missing, AdtySiteWalletChange{
				Type: "adjustment", RefType: "adjustment", RefID: "op-cover-1", Reason: "verified missing principal cover",
				OperatorID: root.Id, IdempotencyKey: "op-root-cover-1",
			})
			require.NoError(t, err)
			_, applied, err = model.ApplyBillingSettlement(key)
			require.NoError(t, err)
			assert.True(t, applied)
			assert.True(t, f.operating(t).IsZero(), "exact cover permits settlement without overdraft")
			assert.EqualValues(t, 700_000, f.remaining(t))
			assert.Equal(t, 700_000, f.quota(t))
			require.Len(t, f.consumeLedgers(t), 1)
			_, applied, err = model.ApplyBillingSettlement(key)
			require.NoError(t, err)
			assert.False(t, applied)
			assert.True(t, f.operating(t).IsZero())
			require.Len(t, f.consumeLedgers(t), 1)
		})
	}
}

func TestOperatingWalletCannotEnableOverdraftPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	_, err := ApplyAdtySiteWalletChange(f.site.Id, model.AdtySiteWalletOperating, decimal.RequireFromString("-0.6"), AdtySiteWalletChange{
		Type: "principal_consume", RefType: "consume", RefID: "op-no-overdraft",
		IdempotencyKey: "op-no-overdraft", AllowOverdraft: true,
	})
	require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)
	assert.True(t, f.operating(t).IsZero())
	assert.Empty(t, f.consumeLedgers(t))
}

func TestTaskRefundAndDownwardAdjustmentReversePrincipalAndBatchPG(t *testing.T) {
	t.Run("downward adjustment reverses batch and credits operating wallet idempotently", func(t *testing.T) {
		f := newOperatingSettleFixture(t)
		require.NoError(t, f.db.AutoMigrate(&model.Task{}, &model.Channel{}, &model.Midjourney{}, &model.Log{}))
		f.fundOperating(t, "2", "op-down-fund")

		initialKey := "request:op-down-submit"
		task := &model.Task{
			TaskID: "op-down-task", UserId: f.customer.Id, Group: "default",
			Quota: 100_000, Status: model.TaskStatusInProgress,
			Properties:  model.Properties{OriginModelName: "op-settle-model"},
			PrivateData: model.TaskPrivateData{BillingOperationKey: initialKey},
		}
		require.NoError(t, f.db.Create(task).Error)
		require.NoError(t, f.db.Create(&model.BillingOperation{
			OperationKey: initialKey, UserId: f.customer.Id, Kind: "request", ReferenceId: int64(task.ID),
			State: model.BillingSettlePending, ActualQuota: 100_000, ReservedQuota: 100_000,
			SiteId: f.site.Id, SiteMarkup: 2, SiteCostFactor: 0.8, EarningRootKey: initialKey,
		}).Error)
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			return model.AllocateAdtyFundAllocationsTx(tx, f.customer.Id, initialKey, 100_000, operatingSettlePrincipal)
		}))
		require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.customer.Id).Update("quota", 900_000).Error)
		_, _, err := model.ApplyBillingSettlement(initialKey)
		require.NoError(t, err)
		require.Equal(t, 900_000, f.quota(t))
		require.EqualValues(t, 900_000, f.remaining(t))
		require.Equal(t, "1.8", f.operating(t).String())

		// Downward adjust from 100,000 -> 40,000 refunds 60,000 quota (0.12 USD).
		adjKey, err := model.QueueTaskBillingOperation(task, 40_000, false)
		require.NoError(t, err)
		op, applied, err := model.ApplyBillingSettlement(adjKey)
		require.NoError(t, err)
		require.True(t, applied)
		assert.EqualValues(t, 40_000, op.AppliedQuota)
		assert.EqualValues(t, 0, op.FundBatchGap)
		assert.Equal(t, 960_000, f.quota(t))
		assert.EqualValues(t, 960_000, f.remaining(t), "principal batch remaining must restore the refunded 60,000")
		assert.Equal(t, "1.92", f.operating(t).String(), "operating wallet must be credited +0.12 USD for the reversed principal")

		// Idempotent replay must not credit operating wallet or batch again.
		_, appliedAgain, err := model.ApplyBillingSettlement(adjKey)
		require.NoError(t, err)
		assert.False(t, appliedAgain)
		assert.EqualValues(t, 960_000, f.remaining(t))
		assert.Equal(t, "1.92", f.operating(t).String())
	})

	t.Run("failure refund after settled initial request restores full batch and operating principal", func(t *testing.T) {
		f := newOperatingSettleFixture(t)
		require.NoError(t, f.db.AutoMigrate(&model.Task{}, &model.Channel{}, &model.Midjourney{}, &model.Log{}))
		f.fundOperating(t, "2", "op-refund-fund")

		initialKey := "request:op-refund-submit"
		task := &model.Task{
			TaskID: "op-refund-task", UserId: f.customer.Id, Group: "default",
			Quota: 100_000, Status: model.TaskStatusInProgress,
			Properties:  model.Properties{OriginModelName: "op-settle-model"},
			PrivateData: model.TaskPrivateData{BillingOperationKey: initialKey},
		}
		require.NoError(t, f.db.Create(task).Error)
		require.NoError(t, f.db.Create(&model.BillingOperation{
			OperationKey: initialKey, UserId: f.customer.Id, Kind: "request", ReferenceId: int64(task.ID),
			State: model.BillingSettlePending, ActualQuota: 100_000, ReservedQuota: 100_000,
			SiteId: f.site.Id, SiteMarkup: 2, SiteCostFactor: 0.8, EarningRootKey: initialKey,
		}).Error)
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			return model.AllocateAdtyFundAllocationsTx(tx, f.customer.Id, initialKey, 100_000, operatingSettlePrincipal)
		}))
		require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.customer.Id).Update("quota", 900_000).Error)
		_, _, err := model.ApplyBillingSettlement(initialKey)
		require.NoError(t, err)

		refKey, err := model.QueueTaskBillingOperation(task, 0, true)
		require.NoError(t, err)
		op, applied, err := model.ApplyBillingSettlement(refKey)
		require.NoError(t, err)
		require.True(t, applied)
		assert.EqualValues(t, 0, op.AppliedQuota)
		assert.EqualValues(t, 0, op.FundBatchGap)
		assert.Equal(t, operatingSettlePrincipal, f.quota(t))
		assert.EqualValues(t, operatingSettlePrincipal, f.remaining(t))
		assert.Equal(t, "2", f.operating(t).String())
	})
}
