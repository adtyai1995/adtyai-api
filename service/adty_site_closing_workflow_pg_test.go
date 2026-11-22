package service

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClosingSiteOperations(t *testing.T) {
	dsn := os.Getenv("ADTY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL 18 via ADTY_TEST_PG_DSN")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pool, err := admin.DB()
	require.NoError(t, err)
	defer pool.Close()
	var version int
	require.NoError(t, admin.Raw("SHOW server_version_num").Scan(&version).Error)
	require.GreaterOrEqual(t, version, 180000)
	require.Less(t, version, 190000)
	t.Logf("server_version_num=%d", version)
	schema := fmt.Sprintf("closing_operations_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		q.Set("client_encoding", "UTF8")
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " client_encoding=UTF8 search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pg, err := db.DB()
	require.NoError(t, err)
	defer pg.Close()

	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = previousRedis }()
	previousDB, previousType := model.DB, common.MainDatabaseType()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	defer func() { model.DB = previousDB; common.SetMainDatabaseType(previousType) }()
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.TopUp{}, &model.Token{}))
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	now := time.Now()
	owner := model.User{Username: "closing-owner", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "closing-ops", RootDomain: "example.test", Status: model.AdtySiteStatusActive, AgreementVer: AdtyCurrentSiteAgreementVersion()}
	require.NoError(t, db.Create(&site).Error)
	customer := model.User{Username: "closure-customer", SiteId: site.Id, Email: "customer@example.test", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&customer).Error)
	oldMail := AdtyDeliverSiteEmail
	delivered := 0
	AdtyDeliverSiteEmail = func(_ *model.AdtySite, subject, email, body string) error {
		require.Equal(t, customer.Email, email)
		require.Contains(t, subject, "主站")
		require.Contains(t, body, "Sign in")
		require.Contains(t, body, customer.Username)
		delivered++
		return nil
	}
	defer func() { AdtyDeliverSiteEmail = oldMail }()
	scheduled, err := ScheduleAdtySiteClose(site.Id, owner.Id, AdtySiteClosePlan{})
	require.NoError(t, err)
	require.Equal(t, model.AdtySiteStatusClosing, scheduled.Status)
	require.Zero(t, delivered)
	var batch model.AdtySiteCloseMailBatch
	require.NoError(t, db.First(&batch, "site_id = ?", site.Id).Error)
	err = db.Transaction(func(tx *gorm.DB) error { _, err := adtyStaffOwnerTx(tx, owner.Id, site.Id, false); return err })
	require.ErrorIs(t, err, ErrAdtyStaffDenied)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := adtyStaffOwnerTx(tx, owner.Id, site.Id, true); return err }))
	staffUser := model.User{Username: "handoff-staff", Status: common.UserStatusEnabled, SiteId: site.Id, StaffSiteId: site.Id}
	require.NoError(t, db.Create(&staffUser).Error)
	member := model.AdtySiteStaff{SiteId: site.Id, OwnerUserId: owner.Id, UserId: staffUser.Id, Username: staffUser.Username, DisplayName: "Handoff", Role: "support", Permissions: `["customers.read","tickets.manage"]`, Status: "enabled", Version: 1}
	require.NoError(t, db.Create(&member).Error)
	grants := []string{"tickets.manage"}
	edit := AdtyStaffInput{DisplayName: member.DisplayName, Role: member.Role, Status: "enabled", Permissions: &grants, ExpectedVersion: 1}
	updated, err := MutateAdtyStaff(owner.Id, site.Id, member.Id, AdtySiteOwnerActionStaffUpdate, edit)
	require.NoError(t, err)
	require.Equal(t, grants, updated.Permissions)
	edit.ExpectedVersion = updated.Version
	grants = append(grants, "customers.disable")
	_, err = MutateAdtyStaff(owner.Id, site.Id, member.Id, AdtySiteOwnerActionStaffUpdate, edit)
	require.ErrorIs(t, err, ErrAdtyStaffDenied)
	grants = []string{"tickets.manage"}
	edit.Status = "disabled"
	updated, err = MutateAdtyStaff(owner.Id, site.Id, member.Id, AdtySiteOwnerActionStaffUpdate, edit)
	require.NoError(t, err)
	edit.Status, edit.ExpectedVersion = "enabled", updated.Version
	_, err = MutateAdtyStaff(owner.Id, site.Id, member.Id, AdtySiteOwnerActionStaffUpdate, edit)
	require.ErrorIs(t, err, ErrAdtyStaffDenied)
	require.NoError(t, RunAdtySiteCloseMailRetryOnce(now))
	require.NoError(t, RunAdtySiteCloseMailRetryOnce(now))
	require.Equal(t, 1, delivered)
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/domains/remote-test", r.URL.Path)
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(404)
		}
		fmt.Fprint(w, `{"message":"test response"}`)
	}))
	defer server.Close()
	previousResend := AdtyResend
	AdtyResend = &AdtyResendClient{BaseURL: server.URL, HTTP: server.Client(), APIKey: func() string { return "test" }}
	defer func() { AdtyResend = previousResend }()
	require.NoError(t, db.Create(&model.AdtySiteSenderDomain{SiteId: site.Id, Domain: "mail.example.test", ResendDomainId: "remote-test", Status: model.AdtySenderDomainVerified}).Error)
	require.Error(t, DisableAdtySiteSenderDomain(context.Background(), site.Id, owner.Id))
	var sender model.AdtySiteSenderDomain
	require.NoError(t, db.First(&sender, "site_id = ?", site.Id).Error)
	require.Equal(t, model.AdtySenderDomainRemoving, sender.Status)
	require.NotEmpty(t, sender.CleanupError)
	require.NoError(t, RunAdtySenderDomainCheckOnce(context.Background(), now.Add(time.Hour)))
	require.NoError(t, db.First(&sender, "site_id = ?", site.Id).Error)
	require.Equal(t, model.AdtySenderDomainDisabled, sender.Status)
	require.Equal(t, 2, attempts)
}
