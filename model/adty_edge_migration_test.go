package model

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TestAdtyEdgeMigrationPostgres18 verifies fresh and repeated startup migration
// on a real PostgreSQL 18 instance when ADTY_TEST_PG18_DSN is configured.
func TestAdtyEdgeMigrationPostgres18(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADTY_TEST_PG18_DSN"))
	if dsn == "" {
		t.Skip("set ADTY_TEST_PG18_DSN to run PostgreSQL 18 schema/migration verification")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)

	var versionNum int
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&versionNum).Error)
	t.Logf("PostgreSQL server_version_num=%d", versionNum)
	require.GreaterOrEqual(t, versionNum, 180000, "verification must run on PostgreSQL 18")
	require.Less(t, versionNum, 190000, "verification must run on PostgreSQL 18")

	// Fresh DB startup migration.
	require.NoError(t, db.AutoMigrate(&Option{}, &User{}, &Token{}, &TopUp{}))
	require.NoError(t, MigrateAdtyTablesOn(db))

	for _, table := range []string{"adty_edge_certs", "adty_edge_acme_accounts", "adty_edge_states", "adty_site_domains"} {
		require.True(t, db.Migrator().HasTable(table), "expected table %s", table)
	}
	for _, col := range []string{"edge_acme_alias", "edge_cert_status", "edge_cert_error", "edge_cert_not_after"} {
		require.True(t, db.Migrator().HasColumn(&AdtySiteDomain{}, col), "expected adty_site_domains.%s", col)
	}

	// Verify unique constraints, upserts, and round-trip persistence on PostgreSQL 18.
	now := time.Now().Unix()
	cert := AdtyEdgeCert{
		CertName:    "*.adtyai-fz.top",
		Kind:        AdtyEdgeCertKindWildcard,
		AliasFQDN:   "wild.acme.adtyaicf.top",
		Environment: AdtyEdgeACMEEnvProduction,
		Status:      AdtyEdgeCertStatusReady,
		NotBefore:   now,
		NotAfter:    now + 90*86400,
	}
	require.NoError(t, db.Create(&cert).Error)

	acct := AdtyEdgeACMEAccount{
		Environment:  AdtyEdgeACMEEnvProduction,
		AccountURI:   "https://acme-v02.api.letsencrypt.org/acme/acct/1",
		KeyEncrypted: "adty:secret:v1:placeholder",
	}
	require.NoError(t, db.Create(&acct).Error)

	state := AdtyEdgeState{StateKey: AdtyEdgeStateKeyManifestPull, Value: `{"version":"v1"}`, UpdatedAt: now}
	require.NoError(t, db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "state_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&state).Error)
	state.Value = `{"version":"v2"}`
	require.NoError(t, db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "state_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&state).Error)

	// Repeated startup migration (idempotency check).
	require.NoError(t, MigrateAdtyTablesOn(db))

	var reloadedCert AdtyEdgeCert
	require.NoError(t, db.Where("cert_name = ?", "*.adtyai-fz.top").First(&reloadedCert).Error)
	assert.Equal(t, AdtyEdgeCertStatusReady, reloadedCert.Status)

	var reloadedState AdtyEdgeState
	require.NoError(t, db.Where("state_key = ?", AdtyEdgeStateKeyManifestPull).First(&reloadedState).Error)
	assert.Equal(t, `{"version":"v2"}`, reloadedState.Value)
}
