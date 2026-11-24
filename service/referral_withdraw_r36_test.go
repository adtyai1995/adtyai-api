package service

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func setupR36TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	tables := []any{
		&model.Option{},
		&model.User{},
		&model.Log{},
		&model.AdtyBalanceLedger{},
		&model.AdtyFundBatch{},
		&model.AdtyFundAllocation{},
		&model.AdtySite{},
		&model.AdtySiteReferralRate{},
		&model.AdtySiteInviteCommission{},
		&model.AdtySiteInviteWithdrawal{},
		&model.AdtySiteInviteWithdrawalItem{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
	}
	tables = append(tables, model.ReferralTables()...)
	if err := db.AutoMigrate(tables...); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldCrypto := common.CryptoSecret
	oldOptionMap := common.OptionMap
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	common.CryptoSecret = "test-referral-secret"
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.CryptoSecret = oldCrypto
		common.RedisEnabled = oldRedis
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
	})
	return db
}

func strPtr(s string) *string       { return &s }
func strSlicePtr(s []string) *[]string { return &s }

func TestMainSiteReferralWithdrawConfigAndAlipayEndToEnd(t *testing.T) {
	db := setupR36TestDB(t)

	// 1. Save USDT-only without CNY rate -> must succeed; enabling USDT without chains must fail.
	if err := Referral.SaveReferralSettings(map[string]string{
		"adty_referral_setting.min_withdraw_usd":      "10",
		"adty_referral_setting.usdt_withdraw_fee":     "1",
		adty_setting.ReferralKeyWithdrawMethods:       `["usdt"]`,
		adty_setting.ReferralKeyWithdrawUSDTNetworks:  `[]`,
	}, "", 1, "127.0.0.1", "test"); err == nil {
		t.Fatalf("expected error when USDT enabled with 0 chains")
	}

	if err := Referral.SaveReferralSettings(map[string]string{
		"adty_referral_setting.min_withdraw_usd":      "10",
		"adty_referral_setting.usdt_withdraw_fee":     "1",
		adty_setting.ReferralKeyWithdrawMethods:       `["usdt"]`,
		adty_setting.ReferralKeyWithdrawUSDTNetworks:  `["TRC20","POLYGON"]`,
	}, "", 1, "127.0.0.1", "test"); err != nil {
		t.Fatalf("expected USDT-only save without CNY rate to succeed, got: %v", err)
	}

	// Prepare promoter user + commission account.
	user := model.User{Username: "promoter1", DisplayName: "P1", Status: common.UserStatusEnabled, Quota: 0}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	aff := model.ReferralAffiliate{
		UserId: user.Id, InviteCode: "PROMO123", Status: model.ReferralAffiliateStatusApproved,
		AcquisitionEnabled: true, SettlementEnabled: true, WithdrawalEnabled: true,
	}
	if err := db.Create(&aff).Error; err != nil {
		t.Fatalf("create affiliate: %v", err)
	}
	acc := model.ReferralCommissionAccount{
		AffiliateId: aff.Id, UserId: user.Id, SettlementCurrency: "USD", AvailableAmount: 200,
	}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}

	// WeChat must always be rejected.
	if _, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(20), AccountType: model.ReferralWithdrawToWechat,
		AccountName: "Alice", IdempotencyKey: "wd-wechat",
	}); err == nil {
		t.Fatalf("expected wechat withdrawal to be rejected")
	}

	// Alipay disabled when only USDT enabled.
	if _, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(20), AccountType: model.ReferralWithdrawToAlipay,
		AccountName: "Alice", AccountNo: "alice@example.com", ExpectedCNYRate: decimal.RequireFromString("7.2"),
		IdempotencyKey: "wd-alipay-disabled",
	}); err == nil {
		t.Fatalf("expected alipay to be rejected when disabled")
	}

	// Disabled USDT chain BEP20 must be rejected.
	if _, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(20), AccountType: model.ReferralWithdrawToUSDT,
		AccountNetwork: "BEP20", AccountNo: "0x1234567890abcdef1234567890abcdef12345678",
		IdempotencyKey: "wd-usdt-bep20-disabled",
	}); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("expected BEP20 to be rejected as not enabled, got: %v", err)
	}

	// Enabled USDT chain POLYGON must succeed.
	usdtWd, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(20), AccountType: model.ReferralWithdrawToUSDT,
		AccountNetwork: "POLYGON", AccountNo: "0x1234567890abcdef1234567890abcdef12345678",
		IdempotencyKey: "wd-usdt-polygon-ok",
	})
	if err != nil || usdtWd.AccountNetwork != "POLYGON" {
		t.Fatalf("expected POLYGON withdrawal to succeed, got %v, err=%v", usdtWd, err)
	}

	// 2. Switch to Alipay-only without USDT fee or chains.
	if err := Referral.SaveReferralSettings(map[string]string{
		"adty_referral_setting.min_withdraw_usd":     "10",
		"adty_referral_setting.usdt_withdraw_fee":    "null",
		adty_setting.ReferralKeyWithdrawMethods:      `["alipay"]`,
		adty_setting.ReferralKeyWithdrawUSDTNetworks: `[]`,
	}, "7.2", 1, "127.0.0.1", "test"); err != nil {
		t.Fatalf("expected Alipay-only save without USDT fee/chains to succeed, got: %v", err)
	}

	// Alipay without AccountNo must fail.
	if _, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(20), AccountType: model.ReferralWithdrawToAlipay,
		AccountName: "Alice", AccountNo: "", ExpectedCNYRate: decimal.RequireFromString("7.2"),
		IdempotencyKey: "wd-alipay-no-account",
	}); err == nil {
		t.Fatalf("expected alipay without account_no to fail")
	}

	// Alipay with real name + account and NO QR code must succeed.
	alipayWd, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(20), AccountType: model.ReferralWithdrawToAlipay,
		AccountName: "张三", AccountNo: "13800138000", QRImageURL: "",
		ExpectedCNYRate: decimal.RequireFromString("7.2"), IdempotencyKey: "wd-alipay-no-qr",
	})
	if err != nil {
		t.Fatalf("expected alipay without QR code to succeed, got: %v", err)
	}
	if alipayWd.AccountName != "张三" || alipayWd.AccountNo != "13800138000" || alipayWd.QRImageURL != "" {
		t.Fatalf("unexpected alipay fields: %+v", alipayWd)
	}

	// Idempotent replay with same empty QR & account succeeds.
	replayed, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(20), AccountType: model.ReferralWithdrawToAlipay,
		AccountName: "张三", AccountNo: "13800138000", QRImageURL: "",
		ExpectedCNYRate: decimal.RequireFromString("7.2"), IdempotencyKey: "wd-alipay-no-qr",
	})
	if err != nil || replayed.Id != alipayWd.Id {
		t.Fatalf("expected idempotent replay to return #%d, got %v err=%v", alipayWd.Id, replayed, err)
	}

	// Conflicting AccountNo on same idempotency key fails with conflict.
	if _, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(20), AccountType: model.ReferralWithdrawToAlipay,
		AccountName: "张三", AccountNo: "13900139000", QRImageURL: "",
		ExpectedCNYRate: decimal.RequireFromString("7.2"), IdempotencyKey: "wd-alipay-no-qr",
	}); err != model.ErrAdtyIdempotencyConflict {
		t.Fatalf("expected idempotency conflict, got: %v", err)
	}

	// Alipay WITH owned QR code succeeds and stores canonical asset URL.
	asset := model.ReferralAsset{
		OwnerUserId: user.Id, Purpose: model.ReferralAssetPurposeWithdrawalQR,
		StoragePath: "withdrawal-qr-1-ok.png", ContentType: "image/png", Size: 128, CreatedBy: "user",
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("create asset: %v", err)
	}
	signedQR := SignReferralAssetURL(asset.StoragePath, time.Now())
	alipayQRWd, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(15), AccountType: model.ReferralWithdrawToAlipay,
		AccountName: "张三", AccountNo: "alice@alipay.com", QRImageURL: signedQR,
		ExpectedCNYRate: decimal.RequireFromString("7.2"), IdempotencyKey: "wd-alipay-with-qr",
	})
	if err != nil {
		t.Fatalf("expected alipay with owned QR to succeed, got: %v", err)
	}
	if alipayQRWd.QRImageURL != "/api/referral-assets/"+asset.StoragePath {
		t.Fatalf("expected canonical QR path, got: %q", alipayQRWd.QRImageURL)
	}

	// 3. Disable all external methods -> balance transfer STILL works.
	if err := Referral.SaveReferralSettings(map[string]string{
		adty_setting.ReferralKeyWithdrawMethods:      `[]`,
		adty_setting.ReferralKeyWithdrawUSDTNetworks: `[]`,
	}, "", 1, "127.0.0.1", "test"); err != nil {
		t.Fatalf("expected disabling all external methods to succeed, got: %v", err)
	}
	balWd, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{
		UserId: user.Id, Amount: decimal.NewFromInt(5), AccountType: model.ReferralWithdrawToBalance,
		IdempotencyKey: "wd-balance-ok",
	})
	if err != nil || balWd.Status != model.ReferralWithdrawalStatusPaid {
		t.Fatalf("expected balance transfer to succeed when external methods disabled, got %v err=%v", balWd, err)
	}
}

func TestSubsiteReferralWithdrawConfigAndAlipayEndToEnd(t *testing.T) {
	db := setupR36TestDB(t)

	owner := model.User{Username: "owner1", DisplayName: "Owner", Status: common.UserStatusEnabled}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "sub1", RootDomain: "example.com", Status: model.AdtySiteStatusActive,
	}
	if err := db.Create(&site).Error; err != nil {
		t.Fatalf("create site: %v", err)
	}
	for _, wallet := range []model.AdtySiteWallet{
		{SiteId: site.Id, Kind: model.AdtySiteWalletEarnings, Currency: "USD", Balance: decimal.Zero, FrozenUSD: decimal.NewFromInt(100)},
		{SiteId: site.Id, Kind: model.AdtySiteWalletMargin, Currency: "USD", Balance: decimal.Zero, FrozenUSD: decimal.Zero},
		{SiteId: site.Id, Kind: model.AdtySiteWalletOperating, Currency: "QUOTA", Balance: decimal.Zero, FrozenUSD: decimal.Zero},
	} {
		if err := db.Create(&wallet).Error; err != nil {
			t.Fatalf("create site wallet %s: %v", wallet.Kind, err)
		}
	}
	customer := model.User{Username: "cust1", DisplayName: "Cust", Status: common.UserStatusEnabled, SiteId: site.Id}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatalf("create customer: %v", err)
	}
	comm := model.AdtySiteInviteCommission{
		SiteId: site.Id, TradeNo: "T100", InviterId: customer.Id, InviteeId: owner.Id,
		FaceUSD: decimal.NewFromInt(1000), Rate: decimal.RequireFromString("0.1"),
		AmountUSD: decimal.NewFromInt(100), CoveredUSD: decimal.NewFromInt(100),
		EarningsHoldUSD: decimal.NewFromInt(100), Status: model.AdtySiteCommissionAvailable,
		AvailableAt: time.Now().Add(-time.Hour).Unix(),
	}
	if err := db.Create(&comm).Error; err != nil {
		t.Fatalf("create commission: %v", err)
	}

	// 1. Legacy compatibility: MinUSD + FeeUSD + CNYRate set without WithdrawMethods => usdt + alipay with all 4 chains.
	if err := SaveAdtyOwnerSiteMarketing(site.Id, owner.Id, AdtySiteMarketingUpdate{
		MinUSD: strPtr("10"), FeeUSD: strPtr("1"), CNYRate: strPtr("7.2"),
	}); err != nil {
		t.Fatalf("legacy save failed: %v", err)
	}
	rules, err := LoadAdtyCustomerSiteInviteWithdrawRules(customer.Id)
	if err != nil {
		t.Fatalf("load legacy rules: %v", err)
	}
	if !rules.Configured || !slices.Equal(rules.Methods, []string{"balance", "usdt", "alipay"}) || len(rules.USDTNetworks) != 4 {
		t.Fatalf("unexpected legacy rules: %+v", rules)
	}

	// 2. Decoupled USDT-only: clear CNYRate, restrict chains to ["TRC20"].
	if err := SaveAdtyOwnerSiteMarketing(site.Id, owner.Id, AdtySiteMarketingUpdate{
		MinUSD:               strPtr("10"),
		FeeUSD:               strPtr("1"),
		CNYRate:              strPtr(""),
		WithdrawMethods:      strSlicePtr([]string{"usdt"}),
		WithdrawUSDTNetworks: strSlicePtr([]string{"TRC20"}),
	}); err != nil {
		t.Fatalf("USDT-only decoupled save failed: %v", err)
	}
	rules, err = LoadAdtyCustomerSiteInviteWithdrawRules(customer.Id)
	if err != nil {
		t.Fatalf("load usdt-only rules: %v", err)
	}
	if !rules.Configured || !slices.Equal(rules.Methods, []string{"balance", "usdt"}) || !slices.Equal(rules.USDTNetworks, []string{"TRC20"}) || rules.CNYRate != "" {
		t.Fatalf("unexpected usdt-only rules: %+v", rules)
	}

	// Quote for alipay must fail; quote for usdt must succeed.
	if _, err := QuoteAdtySiteInviteWithdrawal(customer.Id, site.Id, decimal.NewFromInt(20), "alipay"); err == nil {
		t.Fatalf("expected alipay quote to fail when alipay disabled")
	}
	usdtQuote, err := QuoteAdtySiteInviteWithdrawal(customer.Id, site.Id, decimal.NewFromInt(20), "usdt")
	if err != nil {
		t.Fatalf("expected usdt quote to succeed, got: %v", err)
	}

	// Disabled chain BEP20 must be rejected.
	if _, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: customer.Id, SiteID: site.Id, Amount: decimal.NewFromInt(20), Method: "usdt",
		Network: "BEP20", AccountNo: "0x1234567890abcdef1234567890abcdef12345678",
		ExpectedQuoteID: usdtQuote.QuoteID, RequireQuote: true, IdempotencyKey: "sub-usdt-bep20",
	}); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("expected BEP20 to be rejected, got: %v", err)
	}

	// 3. Decoupled Alipay-only: clear FeeUSD and chains.
	if err := SaveAdtyOwnerSiteMarketing(site.Id, owner.Id, AdtySiteMarketingUpdate{
		MinUSD:               strPtr("10"),
		FeeUSD:               strPtr(""),
		CNYRate:              strPtr("7.1"),
		WithdrawMethods:      strSlicePtr([]string{"alipay"}),
		WithdrawUSDTNetworks: strSlicePtr([]string{}),
	}); err != nil {
		t.Fatalf("Alipay-only decoupled save failed: %v", err)
	}
	rules, err = LoadAdtyCustomerSiteInviteWithdrawRules(customer.Id)
	if err != nil {
		t.Fatalf("load alipay-only rules: %v", err)
	}
	if !rules.Configured || !slices.Equal(rules.Methods, []string{"balance", "alipay"}) || len(rules.USDTNetworks) != 0 || rules.FeeUSD != "" {
		t.Fatalf("unexpected alipay-only rules: %+v", rules)
	}

	alipayQuote, err := QuoteAdtySiteInviteWithdrawal(customer.Id, site.Id, decimal.NewFromInt(20), "alipay")
	if err != nil {
		t.Fatalf("expected alipay quote to succeed, got: %v", err)
	}

	// Alipay without account_no must fail.
	if _, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: customer.Id, SiteID: site.Id, Amount: decimal.NewFromInt(20), Method: "alipay",
		AccountName: "李四", AccountNo: "", QRImageURL: "",
		ExpectedQuoteID: alipayQuote.QuoteID, RequireQuote: true, IdempotencyKey: "sub-alipay-missing-no",
	}); err == nil {
		t.Fatalf("expected subsite alipay without account_no to fail")
	}

	// Alipay with real name + account and NO QR code must succeed.
	subAlipay, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: customer.Id, SiteID: site.Id, Amount: decimal.NewFromInt(20), Method: "alipay",
		AccountName: "李四", AccountNo: "lisi@example.com", QRImageURL: "",
		ExpectedQuoteID: alipayQuote.QuoteID, RequireQuote: true, IdempotencyKey: "sub-alipay-ok",
	})
	if err != nil {
		t.Fatalf("expected subsite alipay without QR to succeed, got: %v", err)
	}
	if subAlipay.AccountName != "李四" || subAlipay.AccountNo != "lisi@example.com" || subAlipay.QRImageURL != "" {
		t.Fatalf("unexpected subsite alipay fields: %+v", subAlipay)
	}

	// Idempotent replay with empty QR succeeds.
	subReplay, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: customer.Id, SiteID: site.Id, Amount: decimal.NewFromInt(20), Method: "alipay",
		AccountName: "李四", AccountNo: "lisi@example.com", QRImageURL: "",
		ExpectedQuoteID: alipayQuote.QuoteID, RequireQuote: true, IdempotencyKey: "sub-alipay-ok",
	})
	if err != nil || subReplay.Id != subAlipay.Id {
		t.Fatalf("expected subsite idempotent replay, got %v err=%v", subReplay, err)
	}

	// Conflicting AccountNo fails with idempotency conflict.
	if _, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: customer.Id, SiteID: site.Id, Amount: decimal.NewFromInt(20), Method: "alipay",
		AccountName: "李四", AccountNo: "other@example.com", QRImageURL: "",
		ExpectedQuoteID: alipayQuote.QuoteID, RequireQuote: true, IdempotencyKey: "sub-alipay-ok",
	}); err != model.ErrAdtyIdempotencyConflict {
		t.Fatalf("expected subsite idempotency conflict, got: %v", err)
	}

	// 4. Disable all external methods -> rules only returns balance, and balance withdrawal works.
	if err := SaveAdtyOwnerSiteMarketing(site.Id, owner.Id, AdtySiteMarketingUpdate{
		WithdrawMethods:      strSlicePtr([]string{}),
		WithdrawUSDTNetworks: strSlicePtr([]string{}),
	}); err != nil {
		t.Fatalf("disable all methods failed: %v", err)
	}
	rules, err = LoadAdtyCustomerSiteInviteWithdrawRules(customer.Id)
	if err != nil {
		t.Fatalf("load disabled rules: %v", err)
	}
	if rules.Configured || !slices.Equal(rules.Methods, []string{"balance"}) {
		t.Fatalf("expected balance-only rules, got: %+v", rules)
	}
	ownerView, err := LoadAdtyOwnerSiteReferral(site.Id, owner.Id)
	if err != nil {
		t.Fatalf("load owner view: %v", err)
	}
	if len(ownerView.WithdrawMethods) != 0 || len(ownerView.WithdrawUSDTNetworks) != 0 {
		t.Fatalf("expected empty methods/nets in owner view, got %v / %v", ownerView.WithdrawMethods, ownerView.WithdrawUSDTNetworks)
	}
	subBal, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: customer.Id, SiteID: site.Id, Amount: decimal.NewFromInt(5), Method: "balance",
		IdempotencyKey: "sub-balance-ok",
	})
	if err != nil || subBal.Status != model.AdtySiteInviteWithdrawPaid {
		t.Fatalf("expected subsite balance transfer to succeed when external methods disabled, got %v err=%v", subBal, err)
	}
}
