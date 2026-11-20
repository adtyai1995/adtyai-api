package router

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSitePricingHistoryOwnerScopePG(t *testing.T) {
	f := newOwnerFixture(t)
	customer := f.customer(t, f.site, "pricing-customer", "pricing@example.com")
	other := f.customer(t, f.other, "other-price-customer", "other-price@example.com")
	_, err := service.SaveAdtySitePricing(service.AdtySitePricingInput{SiteId: f.site.Id, DefaultMarkup: new(decimal.RequireFromString("1.2")), Now: 1700000000})
	require.NoError(t, err)
	_, err = service.SaveAdtySitePricing(service.AdtySitePricingInput{SiteId: f.site.Id, UserId: customer.Id, DefaultMarkup: new(decimal.RequireFromString("1.1")), Now: 1700000001})
	require.NoError(t, err)
	_, err = service.SaveAdtySitePricing(service.AdtySitePricingInput{SiteId: f.other.Id, DefaultMarkup: new(decimal.RequireFromString("2.7")), Now: 1700000002})
	require.NoError(t, err)
	response := f.call(t, http.MethodGet, "/api/adty/site/pricing/history?site_id="+fmt.Sprint(f.other.Id), "", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct { Success bool `json:"success"`; Data struct { Total int `json:"total"`; Items []model.AdtyPriceRevision `json:"items"` } `json:"data"` }
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, 1, body.Data.Total)
	require.Len(t, body.Data.Items, 1)
	require.Equal(t, f.site.Id, body.Data.Items[0].SiteId)
	response = f.call(t, http.MethodGet, fmt.Sprintf("/api/adty/site/customers/%d/pricing/history", other.Id), "", "")
	require.Equal(t, http.StatusNotFound, response.Code)
	response = f.call(t, http.MethodGet, fmt.Sprintf("/api/adty/site/customers/%d/pricing/history", customer.Id), "", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	session, err := service.CreateLoginSessionOnSite(customer.Id, f.site.Id, "password", "127.0.0.1", "history-customer")
	require.NoError(t, err)
	response = f.siteJSON(t, http.MethodGet, "/api/adty/site/pricing/history", "", session.AccessToken)
	require.Equal(t, http.StatusForbidden, response.Code)
}

func TestSitePricingDeletedScopeVersionAPIPG(t *testing.T) {
	f := newOwnerFixture(t)
	customer := f.customer(t, f.site, "deleted-price-customer", "deleted-price@example.com")
	for _, userID := range []int{0, customer.Id} {
		first, err := service.SaveAdtySitePricing(service.AdtySitePricingInput{SiteId: f.site.Id, UserId: userID, ModelMarkups: map[string]decimal.Decimal{"model": decimal.RequireFromString("1.2")}, Now: 1700000000})
		require.NoError(t, err)
		deleted, err := service.SaveAdtySitePricing(service.AdtySitePricingInput{SiteId: f.site.Id, UserId: userID, ModelMarkups: map[string]decimal.Decimal{"model": decimal.Zero}, ExpectedVersion: first.Version, Now: 1700000001})
		require.NoError(t, err)
		path := "/api/adty/site/self"
		if userID > 0 { path = fmt.Sprintf("/api/adty/site/customers/%d", userID) }
		response := f.call(t, http.MethodGet, path, "", "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct { Success bool `json:"success"`; Data struct { Version int64 `json:"pricing_version"`; Prices []model.AdtySitePrice `json:"prices"` } `json:"data"` }
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		require.True(t, body.Success, response.Body.String())
		require.Empty(t, body.Data.Prices)
		require.Equal(t, deleted.Version, body.Data.Version)
	}
}
