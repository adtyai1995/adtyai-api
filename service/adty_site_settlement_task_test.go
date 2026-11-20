package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSiteSettlementTaskImmediateSpreadAndPendingRowEligibility(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "settle-task-imm")
	customer := siteCustomer(t, db, site, "settle-task-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")

	// 1. Confirmed consumption spread settles immediately into site earnings wallet on the same day.
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	saved := adtySiteEarningDayFn
	t.Cleanup(func() { adtySiteEarningDayFn = saved })
	adtySiteEarningDayFn = func() string { return "2026-11-18" }

	consumeOnSite(t, customer, perUnit("125"))
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(usd("25")),
		"confirmed spread must credit site earnings wallet immediately without waiting for yesterday or freeze_days")

	// 2. Any manually-seeded pending row on today (including legacy skipped kind) settles on the same day without freeze delay.
	require.NoError(t, db.Create(&model.AdtySiteEarningDaily{
		SiteId: site.Id, SiteOwnerUserId: owner.Id, UserId: customer.Id, Day: "2026-11-18+1",
		Kind: model.AdtySiteEarningSkipped, Reason: model.AdtySiteRiskSelfUse,
		Quota: perUnit("50"), Revenue: decimal.NewFromInt(50), Platform: decimal.NewFromInt(40),
		Earning: decimal.NewFromInt(10), Status: model.AdtySiteEarningPending,
	}).Error)

	settled, err := RunAdtySiteSettlementOnce(now)
	require.NoError(t, err)
	assert.Equal(t, 1, settled)
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(usd("35")))
}
