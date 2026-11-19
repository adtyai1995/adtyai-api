package model

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSitePriceScopeMigrationPG(t *testing.T) {
	dsn := requireAdtyOfficialPostgresAcceptanceDSN(t)
	admin := connectAdtyPostgresAcceptanceInstance(t, dsn)
	t.Cleanup(func() { require.NoError(t, closeAdtyGorm(admin)) })
	db := openAdtyThrowawayMigrateDatabase(t, admin, dsn)
	require.NoError(t, migrateDB())
	require.True(t, db.Migrator().HasTable(&AdtySitePriceScope{}))
	require.True(t, db.Migrator().HasTable(&AdtyPriceRevision{}))
	require.NoError(t, db.Migrator().DropTable(&AdtySitePriceScope{}))
	for _, row := range []AdtySitePrice{
		{SiteId: 71, Markup: decimal.NewFromInt(1), Version: 7},
		{SiteId: 71, Model: "model", Markup: decimal.NewFromInt(1), Version: 9},
		{SiteId: 71, UserId: 81, Markup: decimal.NewFromInt(1), Version: 3},
	} {
		require.NoError(t, db.Create(&row).Error)
	}
	require.NoError(t, migrateDB())
	var scopes []AdtySitePriceScope
	require.NoError(t, db.Order("user_id").Find(&scopes).Error)
	require.Len(t, scopes, 2)
	require.EqualValues(t, 9, scopes[0].Version)
	require.EqualValues(t, 3, scopes[1].Version)
	require.NoError(t, db.Model(&AdtySitePriceScope{}).Where("site_id = ? AND user_id = 0", 71).Update("version", 12).Error)
	for range 2 {
		require.NoError(t, migrateDB())
	}
	var durable AdtySitePriceScope
	require.NoError(t, db.Where("site_id = ? AND user_id = 0", 71).First(&durable).Error)
	require.EqualValues(t, 12, durable.Version)
	duplicate := AdtySitePriceScope{SiteId: 71, Version: 1}
	require.Error(t, db.Create(&duplicate).Error)
	t.Run("independent_log_database", func(t *testing.T) {
		logDB := openAdtyThrowawayMigrateDatabase(t, admin, dsn)
		DB, LOG_DB = db, logDB
		require.NoError(t, migrateLOGDB())
		require.True(t, logDB.Migrator().HasTable(&Log{}))
		require.False(t, logDB.Migrator().HasTable(&AdtySitePriceScope{}))
		require.False(t, logDB.Migrator().HasTable(&AdtyPriceRevision{}))
	})
}
