package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOwnerTopupSceneConfigurationPG(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(&model.AuditIntent{}))
	root := referralUser(t, db, "owner-topup-root")
	require.NoError(t, db.Model(root).Update("role", common.RoleRootUser).Error)
	scene := model.AdtyPaymentScene{Key: "owner_checkout", Name: "Owner self-use top-up", Business: "owner_self_use_topup", Enabled: true, Providers: []string{"bepusdt"}, SiteScope: "all_sites", UserTypes: []string{"site_owner"}, MinUSD: decimal.NewFromInt(1), MaxUSD: decimal.NewFromInt(1000)}
	_, err := SaveAdtyPaymentScenes(root.Id, 1, []model.AdtyPaymentScene{scene}, model.AuditLog{})
	require.NoError(t, err)
	scene.SiteScope = "main"
	_, err = SaveAdtyPaymentScenes(root.Id, 2, []model.AdtyPaymentScene{scene}, model.AuditLog{})
	require.Error(t, err)
}

func TestOwnerTopupCreditAndCustomerMetricsPG(t *testing.T) {
	setAdtyQuoteDefaults(t)
	SetAdtyMethodBonusForTest(t, "usdt", true, "0.1")
	SetAdtyMethodBonusForTest(t, "alipay", true, "0.1")
	oldRatio := common.TopupGroupRatio2JSONString()
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":0.5}`))
	t.Cleanup(func() { require.NoError(t, common.UpdateTopupGroupRatioByJSONString(oldRatio)) })
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(model.AdtySiteTables()...))
	oldGateway := *setting.GetBEpusdtSetting()
	*setting.GetBEpusdtSetting() = setting.BEpusdtSetting{Enabled: true, BaseURL: "https://owner-pay.example", Secret: "synthetic-test-secret", TradeTypes: []string{"usdt.trc20"}}
	t.Cleanup(func() { *setting.GetBEpusdtSetting() = oldGateway })
	_, owner := inviterAndInvitee(t, db, "owner-topup")
	site := &model.AdtySite{OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "self", RootDomain: "example.com"}
	require.NoError(t, db.Create(site).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	scenes := []model.AdtyPaymentScene{{Key: "owner_checkout", Name: "Owner checkout", Business: "owner_self_use_topup", Enabled: true, Providers: []string{"bepusdt"}, SiteScope: "all_sites", UserTypes: []string{"site_owner"}}}
	payload, err := common.Marshal(scenes)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.AdtyPaymentSceneConfig{}).Where("id = 1").Update("payload", string(payload)).Error)
	quote, err := AdtyQuoteForPayment(owner.Id, site.Id, "default", "owner_self_use_topup", 100, "usdt.trc20")
	require.NoError(t, err)
	require.Equal(t, "100", quote.PayAmount.String())
	require.EqualValues(t, 50000000, quote.CreditQuota)
	require.True(t, quote.BonusUSD.IsZero())
	require.Equal(t, "1", quote.TopupGroupRatio.String())
	cny, err := AdtyQuoteForPayment(owner.Id, site.Id, "default", "owner_self_use_topup", 100, "alipay")
	require.NoError(t, err)
	require.Equal(t, "730", cny.PayAmount.String())
	require.True(t, cny.BonusUSD.IsZero())
	ordinaryQuote, err := AdtyQuoteForPayment(owner.Id, 0, "default", "wallet_topup", 100, "usdt.trc20")
	require.NoError(t, err)
	require.Equal(t, "50", ordinaryQuote.PayAmount.String())
	require.Equal(t, "10", ordinaryQuote.BonusUSD.String())
	for _, business := range []string{"", model.AdtyBusinessWalletTopup} {
		_, err = AdtyQuoteForPayment(owner.Id, site.Id, "default", business, 100, "usdt.trc20")
		assert.Error(t, err, "owner subsite checkout cannot fall back to main-wallet pricing")
	}
	top, ext := paymentSceneOrder(owner.Id, "owner-paid")
	ext.Business, ext.SiteId, ext.TopupGroupRatio = "owner_self_use_topup", site.Id, quote.TopupGroupRatio
	require.NoError(t, CreateAdtyPaymentOrder(top, ext, "owner_checkout"))
	require.Zero(t, ext.ReferralAffiliateId)
	require.Equal(t, owner.Id, ext.SiteOwnerUserId)
	ordinary, ordinaryExt := paymentSceneOrder(owner.Id, "wrong-business")
	require.Error(t, CreateAdtyPaymentOrder(ordinary, ordinaryExt, "owner_checkout"))
	customer := referralUser(t, db, "not-the-owner")
	_, err = AdtyQuoteForPayment(customer.Id, site.Id, "default", "owner_self_use_topup", 100, "usdt.trc20")
	require.Error(t, err)
	_, err = AdtyQuoteForPayment(owner.Id, 0, "default", "owner_self_use_topup", 100, "usdt.trc20")
	require.Error(t, err)
	for _, status := range []string{model.AdtySiteStatusPaused, model.AdtySiteStatusClosing, model.AdtySiteStatusClosed} {
		require.NoError(t, db.Model(site).Update("status", status).Error)
		_, err = AdtyQuoteForPayment(owner.Id, site.Id, "default", model.AdtyBusinessOwnerSelfUseTopup, 100, "usdt.trc20")
		require.Error(t, err, status)
		draft, frozen := paymentSceneOrder(owner.Id, status)
		frozen.Business, frozen.SiteId, frozen.TopupGroupRatio = model.AdtyBusinessOwnerSelfUseTopup, site.Id, decimal.NewFromInt(1)
		require.Error(t, CreateAdtyPaymentOrder(draft, frozen, "owner_checkout"), "checkout must recheck SQL site status")
	}
	require.NoError(t, db.Model(site).Update("status", model.AdtySiteStatusActive).Error)
	for _, mutation := range []struct {
		name   string
		change func(*model.AdtyTopupOrder)
	}{
		{"team", func(o *model.AdtyTopupOrder) { o.TeamId = 7 }},
		{"gift", func(o *model.AdtyTopupOrder) { o.BonusUSD = decimal.NewFromInt(1) }},
		{"discount", func(o *model.AdtyTopupOrder) { o.TopupGroupRatio = decimal.RequireFromString("0.8") }},
		{"extra_quota", func(o *model.AdtyTopupOrder) { o.CreditQuota++ }},
		{"missing_quota", func(o *model.AdtyTopupOrder) { o.CreditQuota-- }},
	} {
		draft, frozen := paymentSceneOrder(owner.Id, mutation.name)
		frozen.Business, frozen.SiteId, frozen.TopupGroupRatio = model.AdtyBusinessOwnerSelfUseTopup, site.Id, decimal.NewFromInt(1)
		mutation.change(frozen)
		require.Error(t, CreateAdtyPaymentOrder(draft, frozen, "owner_checkout"), mutation.name)
		var count int64
		require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", draft.TradeNo).Count(&count).Error)
		require.Zero(t, count)
	}
	facts := model.AdtyPaymentFacts{Provider: "bepusdt", Method: "usdt.trc20", PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100), PaidCurrency: "USDT", Source: "callback"}
	require.NoError(t, db.Model(ext).Update("credit_quota", ext.CreditQuota-1).Error)
	_, err = model.CompleteAdtyTopUp(top.TradeNo, facts, "")
	require.ErrorIs(t, err, model.ErrInvalidTopUpQuota)
	require.NoError(t, db.Model(ext).Update("credit_quota", quote.CreditQuota).Error)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("owner-credit-outage", func(tx *gorm.DB) {
		if tx.Statement.Table == "adty_site_wallet_ledgers" {
			tx.AddError(errors.New("synthetic operating ledger failure"))
		}
	}))
	_, err = model.CompleteAdtyTopUp(top.TradeNo, facts, "")
	require.Error(t, err)
	require.NoError(t, db.Callback().Create().Remove("owner-credit-outage"))
	var unchanged model.User
	require.NoError(t, db.First(&unchanged, owner.Id).Error)
	require.Zero(t, unchanged.Quota+unchanged.QuotaPending, "failed operating ledger write rolls back personal credit")
	var pending model.TopUp
	require.NoError(t, db.First(&pending, top.Id).Error)
	require.Equal(t, common.TopUpStatusPending, pending.Status)
	type result struct {
		replay bool
		err    error
	}
	results := make(chan result, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			replay, err := model.CompleteAdtyTopUp(top.TradeNo, facts, "")
			results <- result{replay, err}
		})
	}
	group.Wait()
	close(results)
	replays := 0
	for outcome := range results {
		require.NoError(t, outcome.err)
		if outcome.replay {
			replays++
		}
	}
	require.Equal(t, 1, replays)
	available, err := model.AdtyOwnerPrincipalAvailable(db, owner.Id, site.Id)
	require.NoError(t, err)
	require.EqualValues(t, 50000000, available)
	var jobs int64
	require.NoError(t, db.Model(&model.ReferralCommissionJob{}).Where("source_trade_no = ?", top.TradeNo).Count(&jobs).Error)
	require.Zero(t, jobs)
	var wallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).First(&wallet).Error)
	require.Equal(t, "100", wallet.Balance.String())
	anomalies, err := detectExistingAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	assert.Empty(t, anomalies, "owner principal must balance the original site's operating ledger")
	key := model.Token{Key: "owner-online", UserId: owner.Id, OwnerSelfUseSiteId: site.Id, UnlimitedQuota: true}
	require.NoError(t, db.Create(&key).Error)
	op := model.BillingOperation{OperationKey: "request:owner-online", UserId: owner.Id, TokenId: key.Id, TokenUnlimited: true, SiteId: site.Id, SiteMarkup: 1, SiteCostFactor: .8}
	_, err = model.ReserveBillingOperation(op, 400000)
	require.NoError(t, err)
	anomalies, err = detectExistingAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	assert.Empty(t, anomalies, "reserved owner principal remains an operating liability until settlement")
	f := &operatingSettleFixture{db: db, site: site}
	f.settle(t, op.OperationKey, 400000)
	available, err = model.AdtyOwnerPrincipalAvailable(db, owner.Id, site.Id)
	require.NoError(t, err)
	require.EqualValues(t, 49600000, available)
	anomalies, err = detectExistingAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	assert.Empty(t, anomalies)
	day := time.Now().Format("2006-01-02")
	stats, err := GetAdtySiteStats(site.Id, day, day)
	require.NoError(t, err)
	require.True(t, stats.Total.TopUpUSD.IsZero(), "owner funds are not customer performance")
	overview, err := GetAdtySitesOverview(day, day)
	require.NoError(t, err)
	assert.True(t, overview.SiteTopUpUSD.IsZero(), "cross-site totals must also exclude owner online payments")
	tier, err := ComputeAdtySiteTierStats(site.Id)
	require.NoError(t, err)
	require.True(t, tier.PrincipalUSD.IsZero(), "owner funds must not improve site tier")
	late, lateExt := paymentSceneOrder(owner.Id, "owner-late")
	lateExt.Business, lateExt.SiteId, lateExt.TopupGroupRatio = "owner_self_use_topup", site.Id, decimal.NewFromInt(1)
	require.NoError(t, CreateAdtyPaymentOrder(late, lateExt, "owner_checkout"))
	require.NoError(t, db.Model(site).Update("status", model.AdtySiteStatusClosed).Error)
	_, err = model.CompleteAdtyTopUp(late.TradeNo, facts, "")
	require.Error(t, err, "closed-site payment requires review")
	available, err = model.AdtyOwnerPrincipalAvailable(db, owner.Id, site.Id)
	require.NoError(t, err)
	require.EqualValues(t, 49600000, available)
	priorLookup := adtyHostedRecoveryProviderByName
	t.Cleanup(func() { adtyHostedRecoveryProviderByName = priorLookup })
	facts.TxHash, facts.Payload = "verified-owner-tx", `{"verified":true}`
	adtyHostedRecoveryProviderByName = func(string) any {
		return stubHostedOrderQuerier{queryFn: func(context.Context, *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
			return &facts, nil
		}}
	}
	for range 2 {
		require.ErrorIs(t, RecoverAdtyHostedPaymentAs(context.Background(), lateExt, customer.Id), model.ErrAdtyOwnerTopupReview)
	}
	require.NoError(t, db.Model(lateExt).Updates(map[string]any{"created_at": common.GetTimestamp() - 180, "last_query_at": 0}).Error)
	recoverAdtyHostedPayments(common.GetTimestamp())
	adtyHostedRecoveryProviderByName = func(string) any {
		return stubHostedOrderQuerier{queryFn: func(context.Context, *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
			return nil, errors.New("provider temporarily unavailable")
		}}
	}
	recoverAdtyHostedPayments(common.GetTimestamp() + 120)
	var reviews []model.PaymentOrphanEvent
	require.NoError(t, db.Where("event_id = ?", "hosted-query:"+late.TradeNo).Find(&reviews).Error)
	require.Len(t, reviews, 1)
	require.Equal(t, model.PaymentOrphanStatusPendingReview, reviews[0].Status)
	require.Contains(t, reviews[0].Payload, "verified-owner-tx")
}

func TestOwnerTopupDailySettlementSeparatesCashPG(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(model.AdtySiteTables()...))
	owner := referralUser(t, db, "sheet-owner")
	site := model.AdtySite{OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "sheet-self", RootDomain: "example.com"}
	require.NoError(t, db.Create(&site).Error)
	top, ext := paymentSceneOrder(owner.Id, "self-sheet")
	top.Status, top.CompleteTime = model.TopUpStatusRefunded, 100
	ext.Business, ext.SiteId, ext.PaidCurrency, ext.PaidAmount = "owner_self_use_topup", site.Id, "USDT", decimal.NewFromInt(100)
	require.NoError(t, db.Create(top).Error)
	require.NoError(t, db.Create(ext).Error)
	for _, voucher := range []struct {
		purpose string
		amount  int64
	}{
		{model.AdtyVoucherPurposeSiteOwner, 20},
		{model.AdtyVoucherPurposeSiteCustomer, 30},
		{model.AdtyVoucherPurposeSiteMargin, 40},
	} {
		require.NoError(t, insertAdtyVoucherOrderTx(db, owner.Id, voucher.purpose, &model.AdtyVoucher{IssuerId: owner.Id, TargetSiteId: site.Id, Purpose: voucher.purpose, AmountUSD: decimal.NewFromInt(voucher.amount), Quota: voucher.amount * 500000}, 100))
	}
	require.NoError(t, db.Create(&model.AdtyManualRefund{RefundId: "self-sheet-refund", TradeNo: ext.TradeNo, UserId: owner.Id, SiteId: site.Id, Status: model.AdtyManualRefundStatusCompleted, RequestUSD: decimal.NewFromInt(20), RequestQuota: 10000000, CompletedAt: 200}).Error)
	sheet, err := summarizeAdtySiteSettlement(site.Id, "1970-01-01", 0, 1000)
	require.NoError(t, err)
	raw, err := common.Marshal(sheet)
	require.NoError(t, err)
	var values map[string]any
	require.NoError(t, common.Unmarshal(raw, &values))
	require.Equal(t, "100", values["owner_self_use_topup_usd"])
	require.Equal(t, "20", values["owner_self_use_refund_usd"])
	assert.Equal(t, "20", values["owner_self_use_voucher_usd"])
	assert.Equal(t, "30", sheet.TopupFaceUSD.String())
	assert.EqualValues(t, 200, sheet.EventWatermark)
	assert.EqualValues(t, 5, sheet.IncludedCount)
	assert.True(t, sheet.PaidUSD.IsZero(), "vouchers are not external cash receipts")
	var paid map[string]string
	require.NoError(t, common.UnmarshalJsonStr(sheet.PaidByCurrency, &paid))
	require.Equal(t, "100", paid["USDT"], "a later refund cannot erase the original receipt")
	day := time.Now().Format("2006-01-02")
	stats, err := GetAdtySiteStats(site.Id, day, day)
	require.NoError(t, err)
	require.Equal(t, "30", stats.Total.TopUpUSD.String(), "only the customer voucher belongs in customer performance")
}

func TestOwnerTopupTransferLocksUserBeforeSitePG(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "lockowner")
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(10), AdtySiteWalletChange{Type: "earning_settlement", IdempotencyKey: "lock-earning"})
	require.NoError(t, err)
	checked, userLocked := false, false
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("owner-transfer-lock-order", func(tx *gorm.DB) {
		if tx.Statement.Table != "adty_sites" || tx.Statement.Clauses["FOR"].Expression == nil || checked {
			return
		}
		checked = true
		userLocked = db.Transaction(func(other *gorm.DB) error {
			return other.Exec("SELECT id FROM users WHERE id = ? FOR UPDATE NOWAIT", owner.Id).Error
		}) != nil
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove("owner-transfer-lock-order") })
	_, err = TransferAdtyEarningsToPersonal(site.Id, owner.Id, decimal.NewFromInt(1), "lock-order")
	require.NoError(t, err)
	require.True(t, checked)
	require.True(t, userLocked, "transfer must lock the user before the site, as checkout and credit do")
}
