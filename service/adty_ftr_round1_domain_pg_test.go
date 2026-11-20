package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY1003BindSiteDomainPG(t *testing.T) {
	db, fake := prepareDomainDB(t)
	pgtest.RecordServerVersion(t, db)
	owner, site := openActiveSite(t, db, "ftr1003")

	domain, err := AddAdtySiteDomain(context.Background(), owner.Id, "shop.ftr1003.example")
	require.NoError(t, err)
	require.Equal(t, site.Id, domain.SiteId)
	require.Equal(t, "shop.ftr1003.example", domain.Hostname)
	require.Equal(t, model.AdtySiteDomainRoleSite, domain.Role)
	require.NotEmpty(t, domain.OwnershipToken)
	require.NotEmpty(t, domain.CFHostnameId)
	require.Equal(t, model.AdtySiteDomainPending, domain.Status)
	require.Contains(t, domain.VerifyRecords, "customers.sites.example.com")
	require.Contains(t, domain.VerifyRecords, "own-cf-1")
	require.Contains(t, domain.VerifyRecords, "acme-cf-1")
	require.Len(t, fake.hostnames, 1)

	var stored model.AdtySiteDomain
	require.NoError(t, db.First(&stored, domain.Id).Error)
	require.Equal(t, site.Id, stored.SiteId)
	require.Equal(t, domain.Hostname, stored.Hostname)
	require.Equal(t, domain.OwnershipToken, stored.OwnershipToken)
	require.Equal(t, domain.CFHostnameId, stored.CFHostnameId)

	var audits int64
	require.NoError(t, db.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND new_value = ?", site.Id, domain.Hostname).Count(&audits).Error)
	require.EqualValues(t, 1, audits)
}

func TestFTRADTY1004VerifyAndUnbindSiteDomainPG(t *testing.T) {
	db, fake := prepareDomainDB(t)
	pgtest.RecordServerVersion(t, db)
	owner, site := openActiveSite(t, db, "ftr1004")

	domain, err := AddAdtySiteDomain(context.Background(), owner.Id, "shop.ftr1004.example")
	require.NoError(t, err)
	require.NotEmpty(t, domain.CFHostnameId)
	fake.setStatus(domain.CFHostnameId, "active", "active")

	require.NoError(t, RunAdtySiteDomainCheckOnce(context.Background(), time.Now()))
	var verified model.AdtySiteDomain
	require.NoError(t, db.First(&verified, domain.Id).Error)
	require.Equal(t, model.AdtySiteDomainActive, verified.Status)
	require.Equal(t, site.Id, verified.SiteId)

	require.NoError(t, RemoveAdtySiteDomain(context.Background(), owner.Id, domain.Id))
	var removed model.AdtySiteDomain
	require.NoError(t, db.First(&removed, domain.Id).Error)
	require.Equal(t, model.AdtySiteDomainRemoved, removed.Status)
	require.Equal(t, site.Id, removed.SiteId)
	hosts, err := adtyLoadActiveCustomDomains()
	require.NoError(t, err)
	require.NotContains(t, hosts, domain.Hostname)
}
