package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-12-09: the site owner moves earnings into margin through POST /api/adty/site/wallets/earning-to-margin.
func TestFTRADTY1209OwnerTransfersEarningToMarginPG(t *testing.T) {
	db := ftrAccountDB(t)
	owner := model.User{
		Username: "ftr1209-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1209", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, service.EnsureAdtySiteWallets(site.Id))
	_, err := service.ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(30), service.AdtySiteWalletChange{
		Type: "ftr_seed", Reason: "FTR 1209 earnings", IdempotencyKey: "ftr1209-seed",
	})
	require.NoError(t, err)
	require.NoError(t, service.SaveAdtySiteTiers([]model.AdtySiteTier{{
		Code: "ftr1209", Name: "FTR 1209", DepositUSD: decimal.NewFromInt(100), Enabled: true,
	}}))
	_, err = service.SetAdtySiteTier(site.Id, "ftr1209", service.AdtySiteTierModeManual, 100)
	require.NoError(t, err)

	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/site/wallets/earning-to-margin", map[string]any{
		"amount_usd": "10",
	}, owner.Id)
	ctx.Request.Header.Set("Idempotency-Key", "ftr1209")
	ctx.Set("role", owner.Role)
	PostAdtyEarningToMargin(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"transferred":true`)

	var earnings, margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earnings).Error)
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	assert.True(t, earnings.Balance.Equal(decimal.NewFromInt(20)))
	assert.True(t, margin.Balance.Equal(decimal.NewFromInt(10)))
	assert.True(t, margin.CycleUsedUSD.IsZero())
	assert.Equal(t, int64(1), margin.CycleNumber)
	prefix := "site:" + strconv.Itoa(site.Id) + ":earn-margin:ftr1209:"
	var earnKey, marginKey int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", prefix+"earn").Count(&earnKey).Error)
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", prefix+"margin").Count(&marginKey).Error)
	assert.Equal(t, int64(1), earnKey)
	assert.Equal(t, int64(1), marginKey)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
