package service

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setAdtyMarginTargetForTest(t *testing.T, siteID int, amount decimal.Decimal) {
	t.Helper()
	code := "margin-target-" + strconv.Itoa(siteID)
	require.NoError(t, SaveAdtySiteTiers([]model.AdtySiteTier{{Code: code, Name: "Margin target", DepositUSD: amount, Enabled: true}}))
	_, err := SetAdtySiteTier(siteID, code, AdtySiteTierModeManual, 100)
	require.NoError(t, err)
}

func TestAdtyMarginSupplementStartsCurrentTierCycle(t *testing.T) {
	for _, entry := range []string{"voucher", "earning"} {
		for _, state := range []string{"exhausted_partial", "active_with_hold", "no_cycle"} {
			t.Run(entry+"/"+state, func(t *testing.T) {
				db := prepareReferralDialectDB(t, "postgres")
				pgtest.RecordServerVersion(t, db)
				ResetAdtyVoucherGuesses()
				t.Cleanup(ResetAdtyVoucherGuesses)
				root := referralUser(t, db, "margin-cycle-root")
				require.NoError(t, db.Model(root).Update("role", common.RoleRootUser).Error)
				owner := referralUser(t, db, "margin-cycle-owner")
				site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "margin-cycle-supplement", Status: model.AdtySiteStatusActive}
				require.NoError(t, db.Create(&site).Error)
				require.NoError(t, EnsureAdtySiteWallets(site.Id))
				if state == "no_cycle" {
					_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(100), AdtySiteWalletChange{Type: "legacy_credit", IdempotencyKey: "legacy-margin"})
					require.NoError(t, err)
				} else {
					require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(100), "initial-margin-cycle"))
					used := int64(90)
					if state == "active_with_hold" {
						used = 20
					}
					_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(-used), AdtySiteWalletChange{Type: "margin_draw", RefType: "business", RefID: "old-source", IdempotencyKey: "old-margin-use"})
					require.NoError(t, err)
					if state == "active_with_hold" {
						_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(200), AdtySiteWalletChange{Type: "legacy_credit", IdempotencyKey: "legacy-margin"})
						require.NoError(t, err)
					}
				}
				if state != "exhausted_partial" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := applyAdtySiteWalletChangeTx(tx, site.Id, model.AdtySiteWalletMargin, decimal.Zero, AdtySiteWalletChange{Type: "gift_reserve", RefType: "topup", RefID: "old-pending-order", IdempotencyKey: "old-margin-hold", HoldDelta: decimal.NewFromInt(5)})
						return err
					}))
				}
				var before model.AdtySiteWallet
				require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&before).Error)
				require.NoError(t, SaveAdtySiteTiers([]model.AdtySiteTier{{Code: "cycle-current", Name: "Cycle current", DepositUSD: decimal.NewFromInt(120), Enabled: true}}))
				_, err := SetAdtySiteTier(site.Id, "cycle-current", AdtySiteTierModeManual, 100)
				require.NoError(t, err)
				var afterTier model.AdtySiteWallet
				require.NoError(t, db.First(&afterTier, before.Id).Error)
				assert.True(t, afterTier.CycleTierUSD.Equal(before.CycleTierUSD), "tier assignment must not expand an existing cycle")
				assert.True(t, afterTier.CycleUsedUSD.Equal(before.CycleUsedUSD))
				var redeem func() error
				if entry == "voucher" {
					require.NoError(t, SetAdtyVoucherIssuanceLimit(root.Id, decimal.NewFromInt(30)))
					issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeSiteMargin, SiteId: site.Id, AmountUSD: decimal.NewFromInt(10), Reason: "new margin cycle"})
					require.NoError(t, err)
					redeem = func() error { _, err := RedeemAdtyVoucher(owner.Id, issued.Code); return err }
				} else {
					_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(30), AdtySiteWalletChange{Type: "earning_settlement", IdempotencyKey: "cycle-earnings"})
					require.NoError(t, err)
					redeem = func() error {
						return TransferAdtyEarningToMargin(site.Id, owner.Id, decimal.NewFromInt(10), "cycle-supplement")
					}
				}
				require.NoError(t, redeem())
				var after model.AdtySiteWallet
				require.NoError(t, db.First(&after, before.Id).Error)
				assert.True(t, after.Balance.Equal(before.Balance.Add(decimal.NewFromInt(10))))
				assert.True(t, after.CycleTierUSD.Equal(decimal.NewFromInt(120)), "supplement uses the target effective in its transaction, not stale caller data")
				assert.True(t, after.CycleUsedUSD.IsZero(), "every real supplement resets the new cycle usage")
				assert.True(t, after.FrozenUSD.Equal(before.FrozenUSD), "old pending-order responsibility survives the new cycle")
				var history model.AdtySiteWalletLedger
				typ := "voucher_margin"
				if entry == "earning" {
					typ = "earning_to_margin"
				}
				require.NoError(t, db.Where("site_id = ? AND wallet_kind = ? AND type = ?", site.Id, model.AdtySiteWalletMargin, typ).First(&history).Error)
				raw, err := common.Marshal(history)
				require.NoError(t, err)
				var stored struct {
					Before *string `json:"margin_cycle_before"`
					After  *string `json:"margin_cycle_after"`
				}
				require.NoError(t, common.Unmarshal(raw, &stored))
				require.NotNil(t, stored.Before, "old cycle inputs must remain in the immutable ledger")
				require.NotNil(t, stored.After, "the committed cycle and its formula must survive reconnect")
				var snapshot struct {
					Version        int    `json:"version"`
					CycleNumber    int64  `json:"cycle_number"`
					StartedAt      int64  `json:"started_at"`
					InitialBalance string `json:"initial_balance_usd"`
					Tier           string `json:"tier_usd"`
					Limit          string `json:"limit_usd"`
					Retained       string `json:"retained_usd"`
					Used           string `json:"used_usd"`
					Holds          string `json:"frozen_and_reserved_usd"`
					NewDraw        string `json:"new_draw_usd"`
				}
				require.NoError(t, common.UnmarshalJsonStr(*stored.After, &snapshot))
				assert.Equal(t, 1, snapshot.Version)
				assert.Positive(t, snapshot.CycleNumber)
				assert.Positive(t, snapshot.StartedAt)
				assert.Equal(t, "120", snapshot.Tier)
				assert.Equal(t, "108", snapshot.Limit)
				assert.Equal(t, "12", snapshot.Retained)
				assert.Equal(t, "0", snapshot.Used)
				assert.Equal(t, map[string]string{"exhausted_partial": "20", "active_with_hold": "290", "no_cycle": "110"}[state], snapshot.InitialBalance)
				assert.Equal(t, map[string]string{"exhausted_partial": "8", "active_with_hold": "103", "no_cycle": "93"}[state], snapshot.NewDraw)
				assert.Equal(t, before.FrozenUSD.String(), snapshot.Holds, "wallet holds already include the outstanding order reservations")
				if state == "active_with_hold" {
					_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(-1), AdtySiteWalletChange{Type: "margin_draw", RefType: "business", RefID: "new-source", IdempotencyKey: "new-cycle-use"})
					require.NoError(t, err)
				}
				require.NoError(t, db.First(&after, before.Id).Error)
				require.NoError(t, redeem())
				var replay model.AdtySiteWallet
				require.NoError(t, db.First(&replay, before.Id).Error)
				assert.True(t, replay.Balance.Equal(after.Balance))
				assert.True(t, replay.CycleUsedUSD.Equal(after.CycleUsedUSD), "replay must not reset usage again")
				assert.True(t, replay.FrozenUSD.Equal(after.FrozenUSD))
				_, err = SetAdtySiteTier(site.Id, "", AdtySiteTierModeAuto, 101)
				require.NoError(t, err)
				require.NoError(t, redeem(), "a completed supplement replays even after the tier is removed")
				var removedTierReplay model.AdtySiteWallet
				require.NoError(t, db.First(&removedTierReplay, before.Id).Error)
				assert.Equal(t, replay.CycleNumber, removedTierReplay.CycleNumber)
				assert.True(t, replay.CycleUsedUSD.Equal(removedTierReplay.CycleUsedUSD))
			})
		}
	}
}

func TestAdtyMarginSupplementRollsBackCycleWithFailedLedger(t *testing.T) {
	for _, entry := range []string{"voucher", "earning"} {
		t.Run(entry, func(t *testing.T) {
			db := prepareReferralDialectDB(t, "postgres")
			pgtest.RecordServerVersion(t, db)
			ResetAdtyVoucherGuesses()
			t.Cleanup(ResetAdtyVoucherGuesses)
			root := referralUser(t, db, "cycle-rollback-root")
			require.NoError(t, db.Model(root).Update("role", common.RoleRootUser).Error)
			owner := referralUser(t, db, "cycle-rollback-owner")
			site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "cycle-rollback", Status: model.AdtySiteStatusActive}
			require.NoError(t, db.Create(&site).Error)
			require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(100), "rollback-initial"))
			_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(-10), AdtySiteWalletChange{Type: "margin_draw", IdempotencyKey: "rollback-use"})
			require.NoError(t, err)
			setAdtyMarginTargetForTest(t, site.Id, decimal.NewFromInt(120))
			_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(30), AdtySiteWalletChange{Type: "earning_settlement", IdempotencyKey: "rollback-earn"})
			require.NoError(t, err)
			var apply func() error
			var voucherID int64
			if entry == "voucher" {
				require.NoError(t, SetAdtyVoucherIssuanceLimit(root.Id, decimal.NewFromInt(30)))
				issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeSiteMargin, SiteId: site.Id, AmountUSD: decimal.NewFromInt(10), Reason: "cycle rollback"})
				require.NoError(t, err)
				voucherID = issued.Id
				apply = func() error { _, err := RedeemAdtyVoucher(owner.Id, issued.Code); return err }
			} else {
				apply = func() error {
					return TransferAdtyEarningToMargin(site.Id, owner.Id, decimal.NewFromInt(10), "rollback-supplement")
				}
			}
			before := mustAdtyVoucherWallet(t, db, site.Id, model.AdtySiteWalletMargin)
			injected := errors.New("margin cycle ledger unavailable")
			const callback = "test:margin-cycle-ledger-failure"
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if ledger, ok := tx.Statement.Dest.(*model.AdtySiteWalletLedger); ok && ledger.WalletKind == model.AdtySiteWalletMargin && (ledger.Type == "voucher_margin" || ledger.Type == "earning_to_margin") {
					tx.AddError(injected)
				}
			}))
			t.Cleanup(func() { db.Callback().Create().Remove(callback) })
			require.ErrorIs(t, apply(), injected)
			after := mustAdtyVoucherWallet(t, db, site.Id, model.AdtySiteWalletMargin)
			assert.Equal(t, before, after, "failed receipt must not change cash, usage or cycle metadata")
			earning := mustAdtyVoucherWallet(t, db, site.Id, model.AdtySiteWalletEarnings)
			assert.True(t, earning.Balance.Equal(decimal.NewFromInt(30)))
			if entry == "voucher" {
				var pending model.AdtyVoucher
				require.NoError(t, db.First(&pending, voucherID).Error)
				assert.Equal(t, model.AdtyVoucherStatusPending, pending.Status)
			}
			require.NoError(t, db.Callback().Create().Remove(callback))
			require.NoError(t, apply())
			after = mustAdtyVoucherWallet(t, db, site.Id, model.AdtySiteWalletMargin)
			assert.Equal(t, before.CycleNumber+1, after.CycleNumber)
			assert.True(t, after.Balance.Equal(decimal.NewFromInt(100)))
			assert.True(t, after.CycleTierUSD.Equal(decimal.NewFromInt(120)))
			assert.True(t, after.CycleUsedUSD.IsZero())
		})
	}
}

func TestAdtyMarginTransferUsesTierCommittedBeforeWalletLock(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	pgtest.RecordServerVersion(t, db)
	owner := referralUser(t, db, "cycle-race-owner")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "cycle-race", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	setAdtyMarginTargetForTest(t, site.Id, decimal.NewFromInt(100))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(30), AdtySiteWalletChange{Type: "earning_settlement", IdempotencyKey: "race-earn"})
	require.NoError(t, err)
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	t.Cleanup(release)
	var paused atomic.Bool
	const callback = "test:margin-current-tier-race"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*model.AdtySiteWallet); ok && paused.CompareAndSwap(false, true) {
			close(entered)
			<-resume
		}
	}))
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- TransferAdtyEarningToMargin(site.Id, owner.Id, decimal.NewFromInt(10), "tier-race")
		close(finished)
	}()
	t.Cleanup(func() { release(); <-finished; db.Callback().Query().Remove(callback) })
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("transfer did not reach its wallet-lock boundary")
	}
	setAdtyMarginTargetForTest(t, site.Id, decimal.NewFromInt(120))
	release()
	require.NoError(t, <-done)
	require.NoError(t, db.Callback().Query().Remove(callback))
	margin := mustAdtyVoucherWallet(t, db, site.Id, model.AdtySiteWalletMargin)
	assert.True(t, margin.CycleTierUSD.Equal(decimal.NewFromInt(120)))
	assert.EqualValues(t, 1, margin.CycleNumber)
	// Concurrent replays of that real receipt must not create extra cycles.
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			<-start
			results <- TransferAdtyEarningToMargin(site.Id, owner.Id, decimal.NewFromInt(10), "tier-race")
		})
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	margin = mustAdtyVoucherWallet(t, db, site.Id, model.AdtySiteWalletMargin)
	assert.EqualValues(t, 1, margin.CycleNumber)
	assert.True(t, margin.Balance.Equal(decimal.NewFromInt(10)))
	earning := mustAdtyVoucherWallet(t, db, site.Id, model.AdtySiteWalletEarnings)
	assert.True(t, earning.Balance.Equal(decimal.NewFromInt(20)))
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(300), AdtySiteWalletChange{Type: "legacy_credit", IdempotencyKey: "race-existing-cash"})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := applyAdtySiteWalletChangeTx(tx, site.Id, model.AdtySiteWalletMargin, decimal.Zero, AdtySiteWalletChange{Type: "gift_reserve", RefType: "topup", RefID: "race-old-order", HoldDelta: decimal.NewFromInt(3), IdempotencyKey: "race-old-hold"})
		return err
	}))
	start = make(chan struct{})
	results = make(chan error, 2)
	group.Go(func() {
		<-start
		results <- TransferAdtyEarningToMargin(site.Id, owner.Id, decimal.NewFromInt(10), "race-new-supplement")
	})
	group.Go(func() {
		<-start
		_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(-1), AdtySiteWalletChange{Type: "margin_draw", RefType: "business", RefID: "race-new-use", IdempotencyKey: "race-draw"})
		results <- err
	})
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	margin = mustAdtyVoucherWallet(t, db, site.Id, model.AdtySiteWalletMargin)
	assert.EqualValues(t, 2, margin.CycleNumber)
	assert.True(t, margin.Balance.Equal(decimal.NewFromInt(319)))
	assert.True(t, margin.FrozenUSD.Equal(decimal.NewFromInt(3)))
	var last model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND wallet_kind = ?", site.Id, model.AdtySiteWalletMargin).Order("id DESC").First(&last).Error)
	used := decimal.Zero
	if last.Type == "margin_draw" {
		used = decimal.NewFromInt(1)
	}
	assert.True(t, margin.CycleUsedUSD.Equal(used), "usage belongs to the cycle at its serialized commit position")
}

func TestAdtyMarginCycleMigrationPreservesUnknownLegacyHistory(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	pgtest.RecordServerVersion(t, db)
	// The fixture above exercises the actual migration on an empty schema.
	// Reconstruct only the pre-snapshot columns for a representative upgrade.
	for _, field := range []string{"CycleNumber", "CycleStartedAt", "CycleInitialUSD"} {
		require.NoError(t, db.Migrator().DropColumn(&model.AdtySiteWallet{}, field))
	}
	for _, field := range []string{"MarginCycleBefore", "MarginCycleAfter"} {
		require.NoError(t, db.Migrator().DropColumn(&model.AdtySiteWalletLedger{}, field))
	}
	old := model.AdtySiteWallet{SiteId: 41, Kind: model.AdtySiteWalletMargin, Currency: "USD", Balance: decimal.NewFromInt(123), FrozenUSD: decimal.NewFromInt(7), RequiredBalance: decimal.NewFromInt(100), CycleTierUSD: decimal.NewFromInt(100), CycleUsedUSD: decimal.NewFromInt(9)}
	require.NoError(t, db.Omit("CycleNumber", "CycleStartedAt", "CycleInitialUSD").Create(&old).Error)
	legacy := model.AdtySiteWalletLedger{SiteId: 41, WalletId: old.Id, WalletKind: model.AdtySiteWalletMargin, Delta: decimal.NewFromInt(123), HoldDelta: decimal.NewFromInt(7), BalanceAfter: decimal.NewFromInt(123), Type: "legacy_credit", RefType: "legacy", RefId: "known-old-source", IdempotencyKey: "migration-legacy-margin"}
	require.NoError(t, db.Omit("MarginCycleBefore", "MarginCycleAfter").Create(&legacy).Error)
	for range 2 {
		require.NoError(t, model.MigrateAdtyTablesOn(db))
	}
	var after model.AdtySiteWallet
	require.NoError(t, db.First(&after, old.Id).Error)
	oldJSON, err := common.Marshal(old)
	require.NoError(t, err)
	afterJSON, err := common.Marshal(after)
	require.NoError(t, err)
	assert.JSONEq(t, string(oldJSON), string(afterJSON))
	assert.Zero(t, after.CycleNumber)
	assert.Nil(t, after.CycleInitialUSD, "migration must not invent a legacy cycle's opening balance")
	var history model.AdtySiteWalletLedger
	require.NoError(t, db.First(&history, legacy.Id).Error)
	legacyJSON, err := common.Marshal(legacy)
	require.NoError(t, err)
	historyJSON, err := common.Marshal(history)
	require.NoError(t, err)
	assert.JSONEq(t, string(legacyJSON), string(historyJSON))
	assert.Nil(t, history.MarginCycleBefore)
	assert.Nil(t, history.MarginCycleAfter)
	duplicate := legacy
	duplicate.Id = 0
	require.Error(t, db.Create(&duplicate).Error, "migration must preserve global financial idempotency uniqueness")
}

func TestCollectAdtyFundAnomaliesFlagsDuplicateCreditAndRefund(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "dup-fund")
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "dup-credit", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "credited"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "dup-credit", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "credited"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "once", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "credited"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "once", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "replayed"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "dup-refund", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "received", EventType: "refund.succeeded"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "dup-refund", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "received", EventType: "refund.succeeded"}).Error)

	kinds, err := CollectAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	require.Contains(t, kinds, "duplicate_credit")
	require.Contains(t, kinds, "duplicate_refund")
}

func TestReviewAdtyAnomalyDispositionDoesNotMoveWallets(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "anomaly-review")
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, recordAdtyAnomalyDisposition("balance_ledger_gap", strconv.Itoa(site.Id), "open", "detected", 0))
	rows, err := ListAdtyAnomalyDispositions(site.Id)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Error(t, ReviewAdtyAnomalyDisposition(rows[0].Id, 9, "closed", ""))
	require.NoError(t, ReviewAdtyAnomalyDisposition(rows[0].Id, 9, "closed", "checked"))
	require.Error(t, ReviewAdtyAnomalyDisposition(rows[0].Id, 9, "closed", "again"))
	var wallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).First(&wallet).Error)
	require.True(t, wallet.Balance.IsZero())
}

func TestCollectAdtyFundAnomaliesFlagsMarginOverCap(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "fund-anomaly")
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.RequireFromString("100"), "anomaly-cycle"))
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).Update("cycle_used_usd", "91").Error)
	kinds, err := CollectAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	require.Contains(t, kinds, "margin_anomaly")
}

func TestMarginDebitStopsAtNinetyPercentOfOpenCycle(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "margin-cycle")
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.RequireFromString("10000"), "cycle-open"))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.RequireFromString("-9000"), AdtySiteWalletChange{
		Type: "margin_draw", IdempotencyKey: "draw-9000",
	})
	require.NoError(t, err)
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.RequireFromString("-1"), AdtySiteWalletChange{
		Type: "margin_draw", IdempotencyKey: "draw-over",
	})
	require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)
}

func TestTransferEarningToMarginOpensNewCycle(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "margin-transfer")
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("1000"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-1000",
	})
	require.NoError(t, err)
	setAdtyMarginTargetForTest(t, site.Id, decimal.NewFromInt(1000))
	require.NoError(t, TransferAdtyEarningToMargin(site.Id, 1, decimal.RequireFromString("1000"), "xfer-1"))
	var margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.Balance.Equal(decimal.RequireFromString("1000")))
	require.True(t, margin.CycleTierUSD.Equal(decimal.RequireFromString("1000")))
	require.True(t, margin.CycleUsedUSD.IsZero())
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.RequireFromString("-901"), AdtySiteWalletChange{
		Type: "margin_draw", IdempotencyKey: "draw-over-transfer",
	})
	require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)
}
