package model

import (
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestAdtyMigrationRejectsUniqueCollisionsPG(t *testing.T) {
	dsn := requireAdtyOfficialPostgresAcceptanceDSN(t)
	admin := connectAdtyPostgresAcceptanceInstance(t, dsn)
	t.Cleanup(func() { require.NoError(t, closeAdtyGorm(admin)) })
	for _, kind := range []string{"voucher", "membership"} {
		t.Run(kind, func(t *testing.T) {
			db := openAdtyThrowawayMigrateDatabase(t, admin, dsn)
			require.NoError(t, migrateDB())
			want := ErrAdtyVoucherTradeNoCollision
			if kind == "voucher" {
				require.NoError(t, db.Migrator().DropIndex(&AdtyVoucher{}, adtyVoucherTradeNoUniqueIndex))
				for _, hash := range []string{"historical-one", "historical-two"} {
					require.NoError(t, db.Create(&AdtyVoucher{CodeHash: hash, CodeTail: "1234", TradeNo: "legacy-collision", Status: AdtyVoucherStatusRedeemed, AmountUSD: decimal.NewFromInt(5), Quota: 2500000}).Error)
				}
			} else {
				want = ErrActiveTeamMembershipCollision
				require.NoError(t, db.Migrator().DropIndex(&AdtyTeamMember{}, activeTeamMembershipUniqueIndex))
				for _, teamID := range []int64{71, 72} {
					require.NoError(t, db.Create(&AdtyTeamMember{TeamId: teamID, UserId: 81, Role: AdtyTeamRoleMember, Status: AdtyTeamMemberActive}).Error)
				}
			}
			var beforeVouchers []AdtyVoucher
			var beforeMembers []AdtyTeamMember
			require.NoError(t, db.Order("id").Find(&beforeVouchers).Error)
			require.NoError(t, db.Order("id").Find(&beforeMembers).Error)
			for range 2 {
				require.ErrorIs(t, migrateDB(), want, "a missing unique guarantee must prevent successful startup")
				var afterVouchers []AdtyVoucher
				var afterMembers []AdtyTeamMember
				require.NoError(t, db.Order("id").Find(&afterVouchers).Error)
				require.NoError(t, db.Order("id").Find(&afterMembers).Error)
				require.Equal(t, beforeVouchers, afterVouchers, "failed migration cannot rewrite or merge vouchers")
				require.Equal(t, beforeMembers, afterMembers, "failed migration cannot rewrite or merge memberships")
			}
		})
	}
}

func TestAdtyMigrationUniqueConstraintsFreshAndRepeatedPG(t *testing.T) {
	dsn := requireAdtyOfficialPostgresAcceptanceDSN(t)
	admin := connectAdtyPostgresAcceptanceInstance(t, dsn)
	t.Cleanup(func() { require.NoError(t, closeAdtyGorm(admin)) })
	db := openAdtyThrowawayMigrateDatabase(t, admin, dsn)
	for range 2 {
		require.NoError(t, migrateDB())
		current, err := adtyVoucherTradeNoUniqueIndexIsCurrent(db)
		require.NoError(t, err)
		require.True(t, current)
		current, err = activeTeamMembershipUniqueIndexIsCurrent(db)
		require.NoError(t, err)
		require.True(t, current)
	}
	for _, kind := range []string{"voucher", "membership"} {
		t.Run(kind, func(t *testing.T) {
			start := make(chan struct{})
			results := make(chan error, 2)
			var workers sync.WaitGroup
			for i := range 2 {
				workers.Go(func() {
					<-start
					if kind == "voucher" {
						hashes := []string{"concurrent-one", "concurrent-two"}
						results <- db.Create(&AdtyVoucher{CodeHash: hashes[i], CodeTail: "4321", TradeNo: "same-trade", Status: AdtyVoucherStatusRedeemed}).Error
					} else {
						results <- db.Create(&AdtyTeamMember{TeamId: int64(91 + i), UserId: 101, Role: AdtyTeamRoleMember, Status: AdtyTeamMemberActive}).Error
					}
				})
			}
			close(start)
			workers.Wait()
			close(results)
			succeeded, rejected := 0, 0
			for err := range results {
				if err == nil {
					succeeded++
				} else {
					require.True(t, isAdtyUniqueViolation(err), "the loser must be rejected by the database unique constraint: %v", err)
					rejected++
				}
			}
			require.Equal(t, 1, succeeded)
			require.Equal(t, 1, rejected)
		})
	}
	require.NoError(t, migrateDB())
	t.Run("independent_log_database", func(t *testing.T) {
		logDB := openAdtyThrowawayMigrateDatabase(t, admin, dsn)
		DB, LOG_DB = db, logDB
		for range 2 {
			require.NoError(t, migrateLOGDB())
		}
		require.True(t, logDB.Migrator().HasTable(&Log{}))
		require.False(t, logDB.Migrator().HasTable(&AdtyVoucher{}))
		require.False(t, logDB.Migrator().HasTable(&AdtyTeamMember{}))
	})
}
