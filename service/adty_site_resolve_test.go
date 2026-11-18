package service

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdtyOverrideStatusPreservesSubsitePublicAnnouncements(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "sub-ann")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "https://sub-ann.sites.example.com/api/status", nil)
	c.Request.Host = "sub-ann.sites.example.com"
	c.Set(AdtySiteContextKey, site)

	expected := []map[string]any{
		{"id": "publication:1", "title": "Subsite Public Notice", "audience": "site"},
		{"id": "publication:2", "title": "All Sites Public Notice", "audience": "all_sites"},
	}
	data := map[string]any{
		"announcements": expected,
		"chats":         []any{"main-chat"},
		"api_info":      []any{"main-api"},
		"faq":           []any{"main-faq"},
	}
	AdtyOverrideStatus(c, data)

	assert.Equal(t, expected, data["announcements"], "host-filtered subsite announcements must not be wiped by AdtyOverrideStatus")
	assert.Empty(t, data["chats"])
	assert.Empty(t, data["api_info"])
	assert.Empty(t, data["faq"])
}

func TestCustomerTermsRelationFailClosedAndTransactionalRecovery(t *testing.T) {
	db := prepareSiteDB(t)
	adty_setting.GetLegalSetting().SiteCustomerTermsVersion = 2
	_, site := openActiveSite(t, db, "terms-rel")

	user := referralUser(t, db, "terms-rel-customer")
	require.NoError(t, model.AdtySetUserSiteTx(db, user.Id, site.Id))

	required, ver := AdtySiteTermsStatus(user.Id)
	assert.True(t, required, "missing AdtySiteCustomer row must block protected use when customer terms are active")
	assert.Equal(t, "2", ver)

	requiredMissingUser, _ := AdtySiteTermsStatus(user.Id + 999999)
	assert.True(t, requiredMissingUser, "missing user lookup must fail closed")

	const errCallback = "test:customer-terms-query-error"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(errCallback, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "adty_site_customers" {
			tx.AddError(errors.New("simulated db failure"))
		}
	}))
	requiredOnErr, _ := AdtySiteTermsStatus(user.Id)
	require.NoError(t, db.Callback().Query().Remove(errCallback))
	assert.True(t, requiredOnErr, "customer relation lookup error must fail closed")

	require.NoError(t, AcceptAdtySiteTerms(user.Id, "2"))
	requiredAfterAccept, _ := AdtySiteTermsStatus(user.Id)
	assert.False(t, requiredAfterAccept)

	var cust model.AdtySiteCustomer
	require.NoError(t, db.Where("user_id = ? AND site_id = ?", user.Id, site.Id).First(&cust).Error)
	assert.Equal(t, "2", cust.TermsVersion)
	assert.Positive(t, cust.TermsAt)
}
