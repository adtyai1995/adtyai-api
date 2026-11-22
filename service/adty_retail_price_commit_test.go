package service

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRetailPriceCommitAndLegacySchedule(t *testing.T) {
	dsn := os.Getenv("ADTY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("ADTY_TEST_PG_DSN requires isolated PostgreSQL 18")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var pgVersion int
	if err := db.Raw("SHOW server_version_num").Scan(&pgVersion).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("server_version_num=%d", pgVersion)
	if pgVersion < 180000 || pgVersion >= 190000 {
		t.Fatal("PostgreSQL 18 required")
	}
	schema := fmt.Sprintf("retail_test_%d", time.Now().UnixNano())
	if err := db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DROP SCHEMA " + schema + " CASCADE")
	err = db.Connection(func(tx *gorm.DB) error {
		tx = tx.Session(&gorm.Session{NewDB: true})
		if err := tx.Exec("SET search_path TO " + schema).Error; err != nil {
			t.Fatal(err)
		}
		if err := tx.AutoMigrate(&model.AdtySite{}, &model.AdtySitePrice{}, &model.AdtySitePriceScope{}, &model.AdtyPriceRevision{}); err != nil {
			t.Fatal(err)
		}
		oldDB, oldHook := model.DB, AdtyOnSiteMarkupIncrease
		model.DB, AdtyOnSiteMarkupIncrease = tx, nil
		defer func() { model.DB, AdtyOnSiteMarkupIncrease = oldDB, oldHook }()
		site := model.AdtySite{Status: model.AdtySiteStatusActive, OwnerUserId: 14, Subdomain: "retail"}
		if err := tx.Create(&site).Error; err != nil {
			t.Fatal(err)
		}
		legacy := model.AdtySitePrice{SiteId: site.Id, Group: "basic", Markup: decimal.NewFromInt(2), PendingMarkup: decimal.NewFromInt(4), PendingAt: 200}
		if err := tx.Create(&legacy).Error; err != nil {
			t.Fatal(err)
		}
		livePrices := func() []model.AdtySitePrice {
			rows, err := adtySitePrices(site.Id, 5)
			if err != nil {
				t.Fatal(err)
			}
			return rows
		}
		markup := decimal.RequireFromString("101.125")
		saved, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: &markup, Now: 100})
		if err != nil {
			t.Fatal(err)
		}
		if k := ResolveAdtySiteMarkup(livePrices(), 5, "", "new"); !k.Equal(markup) {
			t.Fatalf("default retail multiplier = %v", k)
		}
		if n, err := ApplyDueAdtySiteMarkups(150); n != 0 || err != nil {
			t.Fatalf("early transition %d %v", n, err)
		}
		saved, err = SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, GroupMarkups: map[string]decimal.Decimal{"basic": decimal.Zero}, ExpectedVersion: saved.Version, Now: 160})
		if err != nil {
			t.Fatal(err)
		}
		if k := ResolveAdtySiteMarkup(livePrices(), 5, "basic", "new"); !k.Equal(markup) {
			t.Fatalf("deleted override did not inherit immediately: %v", k)
		}
		if n, err := ApplyDueAdtySiteMarkups(201); n != 0 || err != nil {
			t.Fatalf("superseded schedule activated %d %v", n, err)
		}
		if _, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: &markup, ExpectedVersion: 0}); !errors.Is(err, ErrAdtySitePricingConflict) {
			t.Fatalf("stale edit: %v", err)
		}
		var revision model.AdtyPriceRevision
		if err := tx.Where("site_id = ?", site.Id).Last(&revision).Error; err != nil {
			t.Fatal(err)
		}
		if revision.Action != "deleted" || revision.Before != "2" || revision.After != "101.125" || revision.EffectiveAt != 160 {
			t.Fatalf("incorrect deletion history: %+v", revision)
		}
		// A commit from a different writer cannot notify this process's local memory.
		if err := db.Table(schema+".adty_site_prices").Where("site_id = ?", site.Id).Update("markup", decimal.NewFromInt(3)).Error; err != nil {
			t.Fatal(err)
		}
		if k := ResolveAdtySiteMarkup(livePrices(), 5, "", "new"); !k.Equal(decimal.NewFromInt(3)) {
			t.Fatalf("stale price after an external commit: %v", k)
		}
		// Clearing a customer default inherits the current site price.
		customerPrice := decimal.RequireFromString("0.5")
		specific, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: 5, DefaultMarkup: &customerPrice, Now: 210})
		if err != nil {
			t.Fatal(err)
		}
		if k := ResolveAdtySiteMarkup(livePrices(), 5, "", "new"); !k.Equal(customerPrice) {
			t.Fatalf("customer retail multiplier = %v", k)
		}
		if _, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: 5, ClearDefault: true, ExpectedVersion: specific.Version, Now: 211}); err != nil {
			t.Fatal(err)
		}
		if k := ResolveAdtySiteMarkup(livePrices(), 5, "", "new"); k.String() != "3" {
			t.Fatalf("cleared customer override = %v", k)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetailMarkupFitsPriceColumn(t *testing.T) {
	for _, value := range []string{"0.0001", "1", "152.125", "999999.9999"} {
		if !validRetailMarkup(decimal.RequireFromString(value)) {
			t.Errorf("valid retail multiplier rejected: %s", value)
		}
	}
	for _, value := range []string{"0", "-1", "0.00001", "1000000", "1e2147483647", "1e-2147483647"} {
		if validRetailMarkup(decimal.RequireFromString(value)) {
			t.Errorf("unrepresentable multiplier accepted: %s", value)
		}
	}
}

func TestRetailAdmittedStreamAndTaskUseFrozenPrice(t *testing.T) {
	snapshot := &model.RequestPriceSnapshot{
		BillingUnit: model.BillingUnitPerCall, Currency: "USD", ModelBasePrice: "1",
		GroupRatio: "2", SiteCostFactor: "0.5", SiteMarkupRatio: "0.25", StoredMarkupK: "1.25", FinalPrice: "1.25",
		BilledGroupRatio: "1.25", AcceptedAt: 100, RuleVersion: model.RequestPriceSnapshotVersion,
	}
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set(AdtyPriceSnapshotContextKey, snapshot)
	billed, k := AdtyApplySiteMarkupDecimal(ctx, 42, "basic", decimal.NewFromInt(9), "example")
	if billed.String() != "1.25" || k.String() != "1.25" {
		t.Fatalf("accepted stream source changed: ratio=%s k=%s", billed, k)
	}
	var task model.Task
	task.PrivateData.BillingContext = &model.TaskBillingContext{}
	AdtyStampTaskSiteBilling(ctx, task.PrivateData.BillingContext)
	if ratio := adtyTaskRecalcGroupRatioDecimal(&task, "basic", decimal.NewFromInt(9)); ratio.String() != "1.25" {
		t.Fatalf("accepted task repriced to %s", ratio)
	}
}
