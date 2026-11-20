package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-16-10: the owner applies for an earnings withdrawal and the rate is snapshotted.
// POST /api/adty/site/withdrawals freezes the amount and leaves the earnings balance in place.
func TestFTRADTY1610OwnerAppliesSiteWithdrawalPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.AdtySite{}, &model.AdtySiteWallet{}, &model.AdtySiteWalletLedger{}, &model.AdtySiteWithdrawal{},
	))
	owner := model.User{
		Username: "ftr1610-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1610", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	ftrPinProcessOptions(t)
	require.NoError(t, service.SaveAdtySiteWithdrawRules(service.AdtySiteWithdrawRules{
		MinUSD: "10", FeeUSD: "1", AlipayCNYRate: "7.20", USDTUSDRate: "1.00",
	}, owner.Id))
	_, err := service.ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("50"), service.AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "ftr1610-earn",
	})
	require.NoError(t, err)

	body := map[string]string{
		"amount_usd": "20", "method": "alipay", "account_name": "Ada",
		"account_no": "ada@example.com", "expected_rate": "7.20",
	}
	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/site/withdrawals", body, owner.Id)
	ctx.Request.Header.Set("Idempotency-Key", "ftr1610-wd")
	ctx.Set("role", owner.Role)
	ctx.Set("username", owner.Username)
	PostAdtySiteWithdrawal(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"success":true`)
	require.Contains(t, rec.Body.String(), `"status":"pending"`)
	require.Contains(t, rec.Body.String(), `"rate":"7.20"`)

	ctx, rec = newAuthenticatedContext(t, http.MethodPost, "/api/adty/site/withdrawals", body, owner.Id)
	ctx.Request.Header.Set("Idempotency-Key", "ftr1610-wd")
	ctx.Set("role", owner.Role)
	PostAdtySiteWithdrawal(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var stored model.AdtySiteWithdrawal
	require.NoError(t, db.Where("idempotency_key = ?", "ftr1610-wd").First(&stored).Error)
	assert.Equal(t, site.Id, stored.SiteId)
	assert.Equal(t, owner.Id, stored.OwnerUserId)
	assert.Equal(t, model.AdtySiteWithdrawPending, stored.Status)
	assert.Equal(t, "alipay", stored.Method)
	assert.Equal(t, "7.20", stored.Rate)
	assert.Equal(t, "CNY", stored.PayoutCurrency)
	assert.True(t, stored.AmountUSD.Equal(decimal.RequireFromString("20")))
	assert.True(t, stored.FeeUSD.Equal(decimal.RequireFromString("1")))
	assert.True(t, stored.NetUSD.Equal(decimal.RequireFromString("19")))
	assert.Equal(t, "136.8", stored.PayoutAmount)
	var rows int64
	require.NoError(t, db.Model(&model.AdtySiteWithdrawal{}).Count(&rows).Error)
	assert.EqualValues(t, 1, rows)

	var wallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&wallet).Error)
	assert.True(t, wallet.Balance.Equal(decimal.RequireFromString("50")))
	assert.True(t, wallet.FrozenUSD.Equal(decimal.RequireFromString("20")))
	var freeze model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND type = ?", site.Id, "earning_withdraw_freeze").First(&freeze).Error)
	assert.True(t, freeze.Delta.IsZero())
	assert.True(t, freeze.HoldDelta.Equal(decimal.RequireFromString("20")))
	assert.True(t, freeze.BalanceAfter.Equal(decimal.RequireFromString("50")))
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
}
