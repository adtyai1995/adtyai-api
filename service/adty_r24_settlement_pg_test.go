package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// A previous release could seal recent days before the earlier gaps were discovered.
func TestR24SettlementHistoryAndIsolatedRecovery(t *testing.T) {
	db := r23SiteDB(t)
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	first := time.Date(2023, 1, 1, 0, 0, 0, 0, loc)
	now := first.AddDate(0, 0, 1096)
	sites := make([]model.AdtySite, 3)
	var rows []model.AdtySiteSettlementSheet
	for i := range sites {
		sites[i] = model.AdtySite{OwnerUserId: i + 11, Subdomain: fmt.Sprintf("history%d", i), CreatedAt: first.Unix()}
		require.NoError(t, db.Create(&sites[i]).Error)
		for day := 0; day < 1096; day++ {
			if day < 2 || (day >= 10 && day <= 12) || day == 1095 {
				continue
			}
			d := first.AddDate(0, 0, day)
			name := d.Format("2006-01-02")
			rows = append(rows, model.AdtySiteSettlementSheet{SiteId: sites[i].Id, BusinessDay: name, DayStart: d.Unix(), DayEnd: d.AddDate(0, 0, 1).Unix(), Timezone: loc.String(), PaidByCurrency: "{}", BatchId: fmt.Sprintf("history:%d:%s", i, name)})
		}
	}
	require.NoError(t, db.CreateInBatches(rows, 100).Error)
	bad := model.AdtySettlementScan{SiteId: sites[0].Id, NextDay: "corrupt"}
	require.NoError(t, db.Create(&bad).Error)
	started := time.Now()
	n, err := CatchUpAdtySiteSettlementSheets(now)
	require.Error(t, err)
	require.Equal(t, 12, n)
	t.Logf("seeded %d sealed sheets across three sites; repaired %d sheets in %v", len(rows), n, time.Since(started))
	require.NoError(t, db.First(&bad, "site_id = ?", bad.SiteId).Error)
	require.NotEmpty(t, bad.LastError)
	require.NoError(t, db.Model(&bad).Update("next_day", first.Format("2006-01-02")).Error)
	_, err = CatchUpAdtySiteSettlementSheets(now)
	require.NoError(t, err)
	page := common.PageInfo{Page: 1, PageSize: 20}
	require.NoError(t, PageSettlementSheets(sites[1].Id, "", &page))
	require.Equal(t, 1096, page.Total)
	require.Len(t, page.Items, 20)
	one := common.PageInfo{Page: 1, PageSize: 20}
	require.NoError(t, PageSettlementSheets(sites[1].Id, "2023-01-12", &one))
	require.Equal(t, 1, one.Total)
	id := one.Items.([]SettlementSheetSummary)[0].Id
	_, err = GetSettlementSheet(sites[0].Id, id)
	require.Error(t, err)
	for k := 0; k < 2; k++ {
		require.NoError(t, db.Create(&model.AdtySettlementCoverageAdjustment{SheetId: id, BeforeUSD: decimal.NewFromInt(int64(k)), AfterUSD: decimal.NewFromInt(int64(k + 1)), CreatedAt: now.Unix() + int64(k)}).Error)
	}
	require.NoError(t, db.Create(&model.AdtySettlementRevision{SheetId: id, Before: []byte("{}"), After: []byte("{}")}).Error)
	detail, err := GetSettlementSheet(sites[1].Id, id)
	require.NoError(t, err)
	require.Len(t, detail.LegacyCoverage, 2)
	require.Len(t, detail.Revisions, 1)
	require.Equal(t, first.AddDate(0, 0, 11).Unix(), detail.DayStart)
}
