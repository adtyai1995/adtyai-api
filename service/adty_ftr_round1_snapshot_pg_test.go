package service

import (
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY1107FreezeRequestPriceSnapshotPG(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	_, site := openActiveSite(t, db, "ftr1107")
	customer := siteCustomer(t, db, site, "ftr1107-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")

	savedRatios := ratio_setting.ModelRatio2JSONString()
	savedGroups := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroups))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"ftr-1107-model":2}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	ctx, info := snapshotRelay(t, customer.Id, "default", hosttypes.PriceData{ModelRatio: 2})
	info.OriginModelName = "ftr-1107-model"
	billed, _ := AdtyApplySiteMarkupDecimal(ctx, customer.Id, "default", decimal.NewFromInt(1), "")
	info.PriceData.GroupRatioInfo.SetBilledGroupRatio(billed)
	AdtyFreezeRequestPriceSnapshot(ctx, info)
	snap := AdtyFrozenPriceSnapshot(ctx)
	require.NotNil(t, snap)
	require.Equal(t, "2", snap.ModelBasePrice)
	require.Equal(t, "0.25", snap.SiteMarkupRatio)
	require.Equal(t, "0.8", snap.SiteCostFactor)
	require.Equal(t, "2", snap.FinalPrice)

	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"ftr-1107-model":9}`))
	require.NoError(t, db.Model(&model.AdtySitePrice{}).Where("site_id = ?", site.Id).Update("markup", decimal.NewFromInt(3)).Error)
	InvalidateAdtySitePriceCache()
	AdtyFreezeRequestPriceSnapshot(ctx, info)
	again := AdtyFrozenPriceSnapshot(ctx)
	require.NotNil(t, again)
	require.Equal(t, snap.ModelBasePrice, again.ModelBasePrice)
	require.Equal(t, snap.SiteMarkupRatio, again.SiteMarkupRatio)
	require.Equal(t, snap.SiteCostFactor, again.SiteCostFactor)
	require.Equal(t, snap.FinalPrice, again.FinalPrice)
}
