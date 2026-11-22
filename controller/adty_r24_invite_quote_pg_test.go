package controller

import (
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestR24InviteCashQuoteHTTPPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtySite{}, &model.AdtySiteInviteCommission{}, &model.AdtySiteInviteWithdrawal{}, &model.AdtySiteInviteWithdrawalItem{}))
	minimum, fee, rate := decimal.NewFromInt(1), decimal.RequireFromString("0.1"), decimal.NewFromInt(7)
	site := model.AdtySite{OwnerUserId: 9123, Status: "active", Subdomain: "quote", MarketingPolicy: model.AdtySiteMarketingPolicy{MinUSD: &minimum, FeeUSD: &fee, CNYRate: &rate, CancelWindowMinutes: new(int64(1))}}
	require.NoError(t, db.Create(&site).Error)
	user := model.User{SiteId: site.Id, Username: "quote-customer", Password: "hashed-password", Email: "quote@example.com"}
	require.NoError(t, db.Create(&user).Error)
	principal := decimal.NewFromInt(2)
	require.NoError(t, db.Create(&model.AdtySiteInviteCommission{SiteId: site.Id, InviterId: user.Id, TradeNo: "quote-http", Status: "available", AvailableAt: 1, AmountUSD: principal, CoveredUSD: principal, EarningsHoldUSD: principal}).Error)
	path := "/api/adty/site-commissions/withdrawals"
	ctx, out := newAuthenticatedContext(t, http.MethodGet, path+"/quote?amount_usd=1.0000005&method=usdt", nil, user.Id)
	GetAdtySiteInviteWithdrawalQuote(ctx)
	var q struct {
		Success bool
		Data    service.SiteInviteWithdrawalQuote
	}
	require.NoError(t, common.Unmarshal(out.Body.Bytes(), &q))
	require.True(t, q.Success, out.Body.String())
	require.Equal(t, "0.900001", q.Data.PayoutAmount)
	body := map[string]string{"amount_usd": "1.0000005", "method": "usdt", "network": "TRC20", "account_no": "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb"}
	ctx, out = newAuthenticatedContext(t, http.MethodPost, path, body, user.Id)
	ctx.Request.Header.Set("Idempotency-Key", "quote-http")
	PostAdtySiteInviteWithdrawal(ctx)
	require.Contains(t, out.Body.String(), `"code":"quote_changed"`)
	body["expected_quote_id"] = q.Data.QuoteID
	ctx, out = newAuthenticatedContext(t, http.MethodPost, path, body, user.Id)
	ctx.Request.Header.Set("Idempotency-Key", "quote-http")
	PostAdtySiteInviteWithdrawal(ctx)
	require.Contains(t, out.Body.String(), `"success":true`)
	require.Contains(t, out.Body.String(), `"payout_amount":"0.900001"`)
	require.Contains(t, out.Body.String(), `"can_cancel":true`)
	var stored model.AdtySiteInviteWithdrawal
	require.NoError(t, db.Where("idempotency_key = ?", "quote-http").First(&stored).Error)
	require.Equal(t, stored.CreatedAt+60, stored.CancelDeadline)
	require.NoError(t, db.Model(&stored).Update("cancel_deadline", time.Now().Unix()-1).Error)
	ctx, out = newAuthenticatedContext(t, http.MethodGet, path, nil, user.Id)
	GetAdtySiteInviteWithdrawals(ctx)
	require.Contains(t, out.Body.String(), `"can_cancel":false`)
	require.Contains(t, out.Body.String(), `"cancel_reason":"The cancel window has passed"`)
}
