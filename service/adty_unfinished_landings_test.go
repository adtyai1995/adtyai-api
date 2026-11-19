package service

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwnerEarningLineShowsModelAndGroupWithoutChannel(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, RecordAdtySiteEarningLine(7, 9, "gpt-test", "vip", "req-1", 500000, 1.25, 0.8))
	rows, err := ListAdtyOwnerEarningLines(7, 0, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "gpt-test", rows[0].ModelName)
	assert.Equal(t, "vip", rows[0].Group)
	assert.Equal(t, "req-1", rows[0].RequestId)
	body, err := common.Marshal(rows[0])
	require.NoError(t, err)
	assert.NotContains(t, string(body), "channel")
	assert.NotContains(t, string(body), "upstream")
	var stored int64
	require.NoError(t, db.Model(&model.AdtySiteEarningLine{}).Count(&stored).Error)
	assert.EqualValues(t, 1, stored)
}

func TestAnomalyReviewNotifiesTheSite(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	const quota = 500000
	root := referralUser(t, db, "anomaly-note-root")
	require.NoError(t, db.Model(root).Update("role", common.RoleRootUser).Error)
	owner, site := openActiveSite(t, db, "anomaly-note")
	customer := siteCustomer(t, db, site, "anomaly-note-customer")
	require.NoError(t, db.Model(&model.User{}).Where("id IN ?", []int{root.Id, owner.Id, customer.Id}).Update("quota", quota).Error)

	var walletsBefore []model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ?", site.Id).Order("id").Find(&walletsBefore).Error)
	disp := model.AdtyAnomalyDisposition{
		Kind: AdtyAnomalyOrderSuccessUnbooked, Ref: "ORD-NOTE-1", OrderRef: "ORD-NOTE-1",
		Status: "open", SiteId: site.Id, AccountId: customer.Id, Difference: "10", Note: "detected",
	}
	require.NoError(t, db.Create(&disp).Error)
	require.NoError(t, ReviewAdtyAnomalyDisposition(disp.Id, root.Id, "closed", "checked the ledger"))

	var siteWide int64
	require.NoError(t, db.Model(&model.AdtyNotificationPublication{}).Where("audience = ?", "site").Count(&siteWide).Error)
	assert.Zero(t, siteWide, "closing a fund anomaly must not broadcast to the whole site")
	var notices []model.AdtyNotificationPublication
	require.NoError(t, db.Where("audience = ? AND business = ?", "user", "fund_anomaly").Find(&notices).Error)
	require.Len(t, notices, 1)
	assert.Equal(t, "user", notices[0].Audience)
	assert.Equal(t, owner.SiteId, notices[0].TargetSiteId)
	assert.Equal(t, "user:"+strconv.Itoa(owner.Id), notices[0].TargetSiteName)
	assert.Contains(t, notices[0].Content, "checked the ledger")
	assert.Contains(t, notices[0].Content, "资金异常处置已记录")
	assert.NotContains(t, notices[0].Content, "channel")
	assert.True(t, adtyUserCanReadPublication(owner, &notices[0]), "the site owner receives the private review notice")
	assert.False(t, adtyUserCanReadPublication(customer, &notices[0]), "a site customer must not read the internal review notice")

	for _, id := range []int{root.Id, owner.Id, customer.Id} {
		var stored int
		require.NoError(t, db.Model(&model.User{}).Where("id = ?", id).Select("quota").Scan(&stored).Error)
		assert.Equal(t, quota, stored)
	}
	var walletsAfter []model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ?", site.Id).Order("id").Find(&walletsAfter).Error)
	require.Equal(t, len(walletsBefore), len(walletsAfter))
	for i := range walletsAfter {
		assert.True(t, walletsBefore[i].Balance.Equal(walletsAfter[i].Balance), walletsAfter[i].Kind)
	}
}
