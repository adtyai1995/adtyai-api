package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccessTokenRetiredCatalogAndLegacyRoutesCleaned(t *testing.T) {
	dictionary := AccessTokenScopeDictionary()
	var walletEditDesc, optionReadDesc, optionWriteDesc string
	for _, res := range dictionary {
		switch res.Resource {
		case "wallet":
			for _, act := range res.Actions {
				if act.Action == "write" {
					walletEditDesc = act.DescriptionKey
				}
			}
		case "option":
			for _, act := range res.Actions {
				switch act.Action {
				case "read":
					optionReadDesc = act.DescriptionKey
				case "write":
					optionWriteDesc = act.DescriptionKey
				}
			}
		}
	}

	assert.Equal(t, "Top up.", walletEditDesc)
	assert.NotContains(t, strings.ToLower(walletEditDesc), "check in")
	assert.Equal(t, "View system settings and price sync sources.", optionReadDesc)
	assert.NotContains(t, strings.ToLower(optionReadDesc), "custom oauth")
	assert.Equal(t, "Change system settings and sync prices.", optionWriteDesc)
	assert.NotContains(t, strings.ToLower(optionWriteDesc), "custom oauth")

	for _, retiredRoute := range []string{
		"GET /api/user/:id/oauth/bindings",
		"DELETE /api/user/:id/oauth/bindings/:provider_id",
		"DELETE /api/user/:id/bindings/:binding_type",
	} {
		_, exists := accessTokenLegacyActionScopes[retiredRoute]
		assert.False(t, exists, "retired route %s must not remain in accessTokenLegacyActionScopes", retiredRoute)
	}

	assert.False(t, AccessTokenScopeAllowsRoute([]string{"user:read"}, 0, common.RoleAdminUser, "GET", "/api/user/:id/oauth/bindings", "adty_user:read"))
	assert.False(t, AccessTokenScopeAllowsRoute([]string{"user:write"}, 0, common.RoleAdminUser, "DELETE", "/api/user/:id/oauth/bindings/:provider_id", "adty_user:write"))
	assert.False(t, AccessTokenScopeAllowsRoute([]string{"user:write"}, 0, common.RoleAdminUser, "DELETE", "/api/user/:id/bindings/:binding_type", "adty_user:write"))
}

func TestAccessTokenCatalogAndOwnerScopeIsolation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE adty_sites (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		owner_user_id INTEGER NOT NULL,
		status TEXT NOT NULL
	)`).Error)

	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })

	const userID = 101

	// 1. Before becoming a site owner: no "owner" group in catalog, and site_owner_* cannot be granted.
	catalogBefore := AccessTokenCatalog(userID, common.RoleCommonUser)
	for _, group := range catalogBefore {
		assert.NotEqual(t, AccessTokenGroupOwner, group.Group, "non-owner must not see site owner scope group")
	}
	_, err = NormalizeAccessTokenScopes(userID, common.RoleCommonUser, []string{"site_owner_customer:read"})
	assert.ErrorIs(t, err, ErrAccessTokenScopeForbidden)

	// Personal PAT issued with wallet:read and wallet:write before becoming a site owner.
	personalWalletScopes, err := NormalizeAccessTokenScopes(userID, common.RoleCommonUser, []string{"wallet:read", "wallet:write"})
	require.NoError(t, err)

	// 2. User becomes an active site owner.
	require.NoError(t, db.Exec(`INSERT INTO adty_sites (owner_user_id, status) VALUES (?, ?)`, userID, "active").Error)

	// Personal wallet:read / wallet:write PAT must NEVER authorize site-owner routes,
	// even if the router passes wallet:read or wallet:write as requiredScope.
	for _, tc := range []struct {
		method        string
		path          string
		requiredScope string
	}{
		{"GET", "/api/adty/owner/customers", "wallet:read"},
		{"PUT", "/api/adty/owner/customers/12", "wallet:write"},
		{"GET", "/api/adty/owner/orders", "wallet:read"},
		{"POST", "/api/adty/owner/orders/refund", "wallet:write"},
		{"GET", "/api/adty/owner/logs", "wallet:read"},
		{"GET", "/api/adty/owner/settings", "wallet:read"},
		{"PUT", "/api/adty/owner/settings", "wallet:write"},
	} {
		allowed := AccessTokenScopeAllowsRoute(
			personalWalletScopes,
			model.AccessTokenScopeVersion,
			common.RoleCommonUser,
			tc.method,
			tc.path,
			tc.requiredScope,
		)
		assert.False(t, allowed, "personal wallet PAT must not access owner route %s %s", tc.method, tc.path)
	}

	// 3. Site owner sees the "owner" group in AccessTokenCatalog and can grant domain-specific owner scopes.
	catalogAfter := AccessTokenCatalog(userID, common.RoleCommonUser)
	var ownerGroup *AccessTokenCatalogGroup
	for i := range catalogAfter {
		if catalogAfter[i].Group == AccessTokenGroupOwner {
			ownerGroup = &catalogAfter[i]
			break
		}
	}
	require.NotNil(t, ownerGroup, "active site owner must see owner scope group")
	resourceNames := make([]string, 0, len(ownerGroup.Resources))
	for _, r := range ownerGroup.Resources {
		resourceNames = append(resourceNames, r.Resource)
	}
	assert.True(t, slices.Contains(resourceNames, AccessTokenResourceSiteOwnerCustomer))
	assert.True(t, slices.Contains(resourceNames, AccessTokenResourceSiteOwnerOrder))
	assert.True(t, slices.Contains(resourceNames, AccessTokenResourceSiteOwnerLog))
	assert.True(t, slices.Contains(resourceNames, AccessTokenResourceSiteOwnerSetting))

	ownerCustomerReadOnly, err := NormalizeAccessTokenScopes(userID, common.RoleCommonUser, []string{"site_owner_customer:read"})
	require.NoError(t, err)

	assert.True(t, AccessTokenScopeAllowsRoute(ownerCustomerReadOnly, model.AccessTokenScopeVersion, common.RoleCommonUser, "GET", "/api/adty/owner/customers", "wallet:read"))
	assert.False(t, AccessTokenScopeAllowsRoute(ownerCustomerReadOnly, model.AccessTokenScopeVersion, common.RoleCommonUser, "PUT", "/api/adty/owner/customers/12", "wallet:write"))
	assert.False(t, AccessTokenScopeAllowsRoute(ownerCustomerReadOnly, model.AccessTokenScopeVersion, common.RoleCommonUser, "GET", "/api/adty/owner/orders", "wallet:read"))
	assert.False(t, AccessTokenScopeAllowsRoute(ownerCustomerReadOnly, model.AccessTokenScopeVersion, common.RoleCommonUser, "GET", "/api/adty/owner/logs", "wallet:read"))
	assert.False(t, AccessTokenScopeAllowsRoute(ownerCustomerReadOnly, model.AccessTokenScopeVersion, common.RoleCommonUser, "GET", "/api/adty/owner/settings", "wallet:read"))
}
