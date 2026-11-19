package service

import (
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSiteDynamicModelMarkupDisplayPG(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "display-factor")
	customer := siteCustomer(t, db, site, "display-customer")
	sitePrice(t, db, site.Id, 0, "", "1.2")
	require.NoError(t, db.Create(&model.AdtySitePrice{SiteId: site.Id, Model: "dynamic", Markup: decimal.RequireFromString("1.5")}).Error)
	InvalidateAdtySitePriceCache()
	expression := `len > 200000 ? tier("long", p * 4 + c * 12) : tier("short", p * 2 + c * 6)`
	base := []model.Pricing{{ModelName: "dynamic", ModelPrice: 2, BillingMode: "tiered_expr", BillingExpr: expression, BillingPluginVariants: []model.PricingPluginVariant{{PluginKey: "example", BillingMode: "tiered_expr", BillingExpr: `tier("request", fixed(0.02))`}}}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("id", customer.Id)
	adjusted := AdtyAdjustPricingForSiteModels(c, "default", base)
	raw, err := common.Marshal(adjusted[0])
	require.NoError(t, err)
	var payload struct {
		DisplayMultiplier string `json:"billing_display_multiplier"`
	}
	require.NoError(t, common.Unmarshal(raw, &payload))
	require.Equal(t, "1.25", payload.DisplayMultiplier)
	require.Equal(t, expression, adjusted[0].BillingExpr, "display metadata must preserve the authoritative expression")
	require.Equal(t, base[0].BillingPluginVariants[0].BillingExpr, adjusted[0].BillingPluginVariants[0].BillingExpr)
	require.Equal(t, 2.0, base[0].ModelPrice)
	require.Equal(t, 2.5, adjusted[0].ModelPrice)
	_, trace, err := billingexpr.RunExpr(expression, billingexpr.TokenParams{P: 1000, C: 500, Len: 1000})
	require.NoError(t, err)
	factor := decimal.RequireFromString(payload.DisplayMultiplier)
	require.True(t, trace.CostDec.Mul(factor).Mul(decimal.RequireFromString("0.96")).Equal(decimal.RequireFromString("6000")), "1000*2 + 500*6, multiplied by 0.8*1.5, must equal 6000")
}

func TestSitePricingDeleteKeepsCommittedVersionPG(t *testing.T) {
	for _, customerScope := range []bool{false, true} {
		t.Run(map[bool]string{false: "site", true: "customer"}[customerScope], func(t *testing.T) {
			db := prepareSiteDB(t)
			_, site := openActiveSite(t, db, "durable-delete")
			userID := 0
			if customerScope {
				userID = siteCustomer(t, db, site, "durable-customer").Id
			}
			first, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: userID, ModelMarkups: map[string]decimal.Decimal{"model": decimal.RequireFromString("1.2")}, Now: 1700000000})
			require.NoError(t, err)
			deleted, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: userID, ModelMarkups: map[string]decimal.Decimal{"model": decimal.Zero}, ExpectedVersion: first.Version, Now: 1700000001})
			require.NoError(t, err)
			require.Empty(t, deleted.Prices)
			_, err = SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: userID, DefaultMarkup: dec("1.1"), ExpectedVersion: deleted.Version, Now: 1700000002})
			require.NoError(t, err, "returned deletion version must authorize the next save")
			_, err = SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: userID, DefaultMarkup: dec("1.1"), ExpectedVersion: 0, Now: 1700000003})
			require.ErrorIs(t, err, ErrAdtySitePricingConflict)
		})
	}
}

func TestSitePricingWritesRevisionWithBusinessTransactionPG(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "revision-write")
	require.NoError(t, db.AutoMigrate(&model.AdtyPriceRevision{}))
	first, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: dec("1.2"), Now: 1700000000})
	require.NoError(t, err)
	var revisions []model.AdtyPriceRevision
	require.NoError(t, db.Where("site_id = ?", site.Id).Find(&revisions).Error)
	require.Len(t, revisions, 1, "a real successful price write must append history")
	require.Equal(t, owner.Id, revisions[0].ActorId)
	require.Contains(t, revisions[0].Before, "1")
	require.Contains(t, revisions[0].After, "1.2")
	require.Error(t, db.Model(&revisions[0]).Update("after", "9").Error)
	require.Error(t, db.Delete(&revisions[0]).Error)
	// Missing audit storage must fail the business write and leave its version usable.
	require.NoError(t, db.Migrator().DropTable(&model.AdtyPriceRevision{}))
	_, err = SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: dec("1.1"), ExpectedVersion: first.Version, Now: 1700000001})
	require.Error(t, err)
	var price model.AdtySitePrice
	require.NoError(t, db.Where("site_id = ?", site.Id).First(&price).Error)
	require.True(t, price.Markup.Equal(decimal.RequireFromString("1.2")))
	require.Equal(t, first.Version, price.Version)
}

func TestSitePricingActivationAuditAndRollbackPG(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "raise", true: "remove_override"}[remove], func(t *testing.T) {
			db := prepareSiteDB(t)
			_, site := openActiveSite(t, db, "activation-audit")
			adty_setting.GetResellerSetting().MarkupIncreaseDelayHours = 1
			initial, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: dec("1.4"), GroupMarkups: map[string]decimal.Decimal{"vip": decimal.RequireFromString("1.1")}, Now: 1700000000})
			require.NoError(t, err)
			siteCustomer(t, db, site, "activation-customer")
			next := decimal.RequireFromString("1.3")
			if remove {
				next = decimal.Zero
			}
			scheduled, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, GroupMarkups: map[string]decimal.Decimal{"vip": next}, ExpectedVersion: initial.Version, Now: 1700000001})
			require.NoError(t, err)
			require.EqualValues(t, 1700003601, scheduled.Pending["vip"])
			changed, err := ApplyDueAdtySiteMarkups(1700003600)
			require.NoError(t, err)
			require.Zero(t, changed)
			require.NoError(t, db.Migrator().DropTable(&model.AdtyPriceRevision{}))
			changed, err = ApplyDueAdtySiteMarkups(1700003601)
			require.Error(t, err)
			require.Zero(t, changed)
			var unchanged model.AdtySitePrice
			require.NoError(t, db.Where("site_id = ? AND "+model.AdtyGroupColumn()+" = ?", site.Id, "vip").First(&unchanged).Error)
			require.Equal(t, "1.1", unchanged.Markup.String())
			require.Equal(t, scheduled.Version, unchanged.Version)
			require.NoError(t, db.AutoMigrate(&model.AdtyPriceRevision{}))
			changed, err = ApplyDueAdtySiteMarkups(1700003601)
			require.NoError(t, err)
			require.EqualValues(t, 1, changed)
			var revision model.AdtyPriceRevision
			require.NoError(t, db.First(&revision).Error)
			require.Zero(t, revision.ActorId)
			require.Equal(t, "1.1", revision.Before)
			expected, action := "1.3", "activated"
			if remove {
				expected, action = "1.4", "deletion_activated"
			}
			require.Equal(t, expected, revision.After)
			require.Equal(t, action, revision.Action)
			var before model.AdtySitePrice
			require.NoError(t, common.Unmarshal([]byte(revision.BeforeState), &before))
			require.Equal(t, "1.1", before.Markup.String())
			require.EqualValues(t, 1700003601, before.PendingAt)
			_, version, err := LoadAdtySitePricing(site.Id, 0)
			require.NoError(t, err)
			require.Equal(t, scheduled.Version+1, version)
			changed, err = ApplyDueAdtySiteMarkups(1700003602)
			require.NoError(t, err)
			require.Zero(t, changed)
		})
	}
}

func TestSitePricingCancelledRaiseAndConcurrentSavePG(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "cancel-price")
	initial, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: dec("1.2"), Now: 1700000000})
	require.NoError(t, err)
	siteCustomer(t, db, site, "cancel-customer")
	adty_setting.GetResellerSetting().MarkupIncreaseDelayHours = 1
	scheduled, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: dec("1.5"), ExpectedVersion: initial.Version, Now: 1700000001})
	require.NoError(t, err)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: dec("1.1"), ExpectedVersion: scheduled.Version, Now: 1700000002})
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	passed, conflicted := 0, 0
	for err := range errors {
		if err == nil {
			passed++
		} else {
			require.ErrorIs(t, err, ErrAdtySitePricingConflict)
			conflicted++
		}
	}
	require.Equal(t, 1, passed)
	require.Equal(t, 1, conflicted)
	changed, err := ApplyDueAdtySiteMarkups(1700003602)
	require.NoError(t, err)
	require.Zero(t, changed)
	prices, version, err := LoadAdtySitePricing(site.Id, 0)
	require.NoError(t, err)
	require.Len(t, prices, 1)
	require.Equal(t, "1.1", prices[0].Markup.String())
	require.Equal(t, scheduled.Version+1, version)
	history, total, err := ListAdtySitePricingHistory(site.Id, 0, 0, 1)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, history, 1)
	require.Equal(t, "cancelled", history[0].Action)
	require.NoError(t, db.Migrator().DropTable(&model.AdtySitePriceScope{}))
	_, _, err = LoadAdtySitePricing(site.Id, 0)
	require.Error(t, err, "a failed version read must not be returned as version zero")
}
