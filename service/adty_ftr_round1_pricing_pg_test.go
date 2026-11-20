package service

import (
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY1102SiteCustomerPricePG(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	_, site := openActiveSite(t, db, "ftr1102")
	customer := siteCustomer(t, db, site, "ftr1102-customer")
	other := siteCustomer(t, db, site, "ftr1102-other")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Update("quota", 500000).Error)

	const now int64 = 1700001102
	saved, err := SaveAdtySitePricing(AdtySitePricingInput{
		SiteId:       site.Id,
		UserId:       customer.Id,
		ModelMarkups: map[string]decimal.Decimal{"ftr-1102-model": decimal.RequireFromString("1.4")},
		Now:          now,
	})
	require.NoError(t, err)
	require.Positive(t, saved.Version)
	require.Contains(t, saved.ModelPending, "ftr-1102-model")

	var row model.AdtySitePrice
	require.NoError(t, db.Where("site_id = ? AND user_id = ? AND model = ?", site.Id, customer.Id, "ftr-1102-model").First(&row).Error)
	require.Equal(t, customer.Id, row.UserId)
	delay := int64(adty_setting.GetResellerSetting().MarkupIncreaseDelayHours) * 3600
	require.True(t, row.Markup.Equal(decimal.NewFromInt(1)))
	require.True(t, row.PendingMarkup.Equal(decimal.RequireFromString("1.4")))
	require.Equal(t, now+delay, row.PendingAt)

	prices, version, err := LoadAdtySitePricing(site.Id, customer.Id)
	require.NoError(t, err)
	require.Equal(t, saved.Version, version)
	require.NotEmpty(t, prices)

	applied, err := ApplyDueAdtySiteMarkups(now + delay)
	require.NoError(t, err)
	require.EqualValues(t, 1, applied)
	require.NoError(t, db.First(&row, row.Id).Error)
	require.True(t, row.Markup.Equal(decimal.RequireFromString("1.4")))
	require.True(t, row.PendingMarkup.IsZero())
	require.Zero(t, row.PendingAt)

	var stored model.User
	require.NoError(t, db.Select("quota").First(&stored, customer.Id).Error)
	require.Equal(t, 500000, stored.Quota)

	var otherCount int64
	require.NoError(t, db.Model(&model.AdtySitePrice{}).Where("site_id = ? AND user_id = ?", site.Id, other.Id).Count(&otherCount).Error)
	require.Zero(t, otherCount)
}

func TestFTRADTY1108PriceRevisionHistoryPG(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	owner, site := openActiveSite(t, db, "ftr1108")
	require.NoError(t, db.AutoMigrate(&model.AdtyPriceRevision{}))

	saved, err := SaveAdtySitePricing(AdtySitePricingInput{
		SiteId:        site.Id,
		DefaultMarkup: dec("1.2"),
		Now:           1700001108,
	})
	require.NoError(t, err)

	items, total, err := ListAdtySitePricingHistory(site.Id, 0, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, owner.Id, items[0].ActorId)
	require.Equal(t, site.Id, items[0].SiteId)
	require.Contains(t, items[0].After, "1.2")
	require.Error(t, db.Model(&items[0]).Update("after", "9").Error)
	require.Error(t, db.Delete(&items[0]).Error)

	var price model.AdtySitePrice
	require.NoError(t, db.Where("site_id = ? AND user_id = 0", site.Id).First(&price).Error)
	require.True(t, price.Markup.Equal(decimal.RequireFromString("1.2")))
	require.Equal(t, saved.Version, price.Version)
}
