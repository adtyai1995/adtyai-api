/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package model

import (
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type siteOwnershipLegacyDaily struct {
	Id        int64
	SiteId    int    `gorm:"uniqueIndex:idx_site_earning"`
	UserId    int    `gorm:"uniqueIndex:idx_site_earning"`
	Day       string `gorm:"uniqueIndex:idx_site_earning;type:varchar(16)"`
	Kind      string `gorm:"uniqueIndex:idx_site_earning;type:varchar(16)"`
	Quota     int64
	Revenue   decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0"`
	Platform  decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0"`
	Earning   decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0"`
	Status    string          `gorm:"type:varchar(16);index"`
	SettledAt int64
}

func (siteOwnershipLegacyDaily) TableName() string { return "adty_site_earning_daily" }

func TestSiteOwnershipMigrationPreservesHistoryAndOwnerUniqueness(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, upgrade := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/upgrade=%t", dialect, upgrade), func(t *testing.T) {
				db := isolatedMigrationTestDatabase(t, dialect)
				if upgrade {
					require.NoError(t, db.AutoMigrate(&siteOwnershipLegacyDaily{}))
					require.NoError(t, db.Create(&siteOwnershipLegacyDaily{SiteId: 11, UserId: 22, Day: "2026-11-01", Kind: "usage", Quota: 500000,
						Revenue: decimal.NewFromInt(1), Platform: decimal.RequireFromString("0.4"), Earning: decimal.RequireFromString("0.6"), Status: "available", SettledAt: 1234}).Error)
				}
				for range 2 {
					require.NoError(t, migrateDB())
				}
				for _, entity := range []any{&AdtySiteEarningDaily{}, &AdtySiteEarningEvent{}, &BillingOperation{}, &AdtyTopupOrder{}} {
					require.True(t, db.Migrator().HasColumn(entity, "site_owner_user_id"), "financial ownership must survive delayed settlement")
				}
				require.True(t, db.Migrator().HasColumn(&AdtySite{}, "ownership_version"))
				for _, ownerID := range []int{31, 32} {
					require.NoError(t, db.Model(&AdtySiteEarningDaily{}).Create(map[string]any{"site_id": 11, "site_owner_user_id": ownerID, "user_id": 22,
						"day": "2026-11-01", "kind": "usage", "quota": 100, "revenue": 1, "platform": 0.4, "earning": 0.6, "status": "pending"}).Error)
				}
				assert.Error(t, db.Model(&AdtySiteEarningDaily{}).Create(map[string]any{"site_id": 11, "site_owner_user_id": 31, "user_id": 22,
					"day": "2026-11-01", "kind": "usage"}).Error, "the same owner's daily slot must remain unique")
				for range 2 {
					require.NoError(t, migrateDB())
				}
				if upgrade {
					var history AdtySiteEarningDaily
					require.NoError(t, db.Where("id = ?", 1).First(&history).Error)
					assert.Equal(t, "0.6", history.Earning.String())
					assert.EqualValues(t, 500000, history.Quota)
					assert.EqualValues(t, 1234, history.SettledAt)
					assert.Equal(t, "available", history.Status)
				}
			})
		}
	}
}

func TestAdtyDSNAcceptsKeywordPostgreSQL(t *testing.T) {
	keyword := "host=127.0.0.1 port=5432 user=postgres dbname=adty sslmode=disable"
	if !adtyDSNIsPostgreSQL(keyword) {
		t.Fatal("keyword PostgreSQL DSN was rejected")
	}
	if !adtyDSNIsPostgreSQL("postgresql://adty@127.0.0.1:5432/adty?sslmode=disable") {
		t.Fatal("URL PostgreSQL DSN was rejected")
	}
	if adtyDSNIsPostgreSQL("root:pass@tcp(127.0.0.1:3306)/new-api") {
		t.Fatal("MySQL DSN was accepted as PostgreSQL")
	}
}

func TestMigrateAdtyTablesFailsAndGatesWritesOnUniqueConstraintCollisions(t *testing.T) {
	db := isolatedMigrationTestDatabase(t, "sqlite")
	require.NoError(t, db.AutoMigrate(&User{}, &Log{}, &TopUp{}, &AdtyVoucher{}, &AdtyTeam{}, &AdtyTeamMember{}))

	v1 := AdtyVoucher{
		CodeHash:   "hist-v1",
		CodeTail:   "1111",
		Purpose:    AdtyVoucherPurposeMainUser,
		Status:     AdtyVoucherStatusRedeemed,
		AmountUSD:  decimal.NewFromInt(5),
		Quota:      2500000,
		SourceKind: AdtyVoucherSourcePlatform,
		TradeNo:    "VCHCOLLISION001",
	}
	v2 := AdtyVoucher{
		CodeHash:   "hist-v2",
		CodeTail:   "2222",
		Purpose:    AdtyVoucherPurposeMainUser,
		Status:     AdtyVoucherStatusRedeemed,
		AmountUSD:  decimal.NewFromInt(5),
		Quota:      2500000,
		SourceKind: AdtyVoucherSourcePlatform,
		TradeNo:    "VCHCOLLISION001",
	}
	require.NoError(t, db.Create(&v1).Error)
	require.NoError(t, db.Create(&v2).Error)

	teamA := AdtyTeam{Name: "hist-team-a", Status: AdtyTeamStatusActive, Discount: decimal.NewFromInt(1)}
	teamB := AdtyTeam{Name: "hist-team-b", Status: AdtyTeamStatusActive, Discount: decimal.NewFromInt(1)}
	require.NoError(t, db.Create(&teamA).Error)
	require.NoError(t, db.Create(&teamB).Error)
	m1 := AdtyTeamMember{TeamId: teamA.Id, UserId: 77, Role: AdtyTeamRoleLeader, Status: AdtyTeamMemberActive}
	m2 := AdtyTeamMember{TeamId: teamB.Id, UserId: 77, Role: AdtyTeamRoleMember, Status: AdtyTeamMemberActive}
	require.NoError(t, db.Create(&m1).Error)
	require.NoError(t, db.Create(&m2).Error)

	err := MigrateAdtyTablesOn(db)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAdtyVoucherTradeNoCollision)
	assert.ErrorIs(t, err, ErrActiveTeamMembershipCollision)

	// Historical rows must remain intact and readable.
	var vouchers []AdtyVoucher
	require.NoError(t, db.Order("id ASC").Find(&vouchers).Error)
	require.Len(t, vouchers, 2)
	assert.Equal(t, "VCHCOLLISION001", vouchers[0].TradeNo)
	assert.Equal(t, "VCHCOLLISION001", vouchers[1].TradeNo)

	var members []AdtyTeamMember
	require.NoError(t, db.Order("id ASC").Find(&members).Error)
	require.Len(t, members, 2)
	assert.Equal(t, AdtyTeamMemberActive, members[0].Status)
	assert.Equal(t, AdtyTeamMemberActive, members[1].Status)

	// Write paths to tables with missing unique constraints must be gated.
	writeVoucherErr := db.Create(&AdtyVoucher{
		CodeHash:   "blocked-v3",
		CodeTail:   "3333",
		Purpose:    AdtyVoucherPurposeMainUser,
		Status:     AdtyVoucherStatusPending,
		AmountUSD:  decimal.NewFromInt(1),
		Quota:      500000,
		SourceKind: AdtyVoucherSourcePlatform,
	}).Error
	require.ErrorIs(t, writeVoucherErr, ErrAdtyVoucherTradeNoCollision)

	updateVoucherErr := db.Model(&AdtyVoucher{}).Where("id = ?", v1.Id).Update("status", AdtyVoucherStatusCancelled).Error
	require.ErrorIs(t, updateVoucherErr, ErrAdtyVoucherTradeNoCollision)

	writeMemberErr := db.Create(&AdtyTeamMember{
		TeamId: teamA.Id,
		UserId: 88,
		Role:   AdtyTeamRoleMember,
		Status: AdtyTeamMemberActive,
	}).Error
	require.ErrorIs(t, writeMemberErr, ErrActiveTeamMembershipCollision)

	updateMemberErr := db.Model(&AdtyTeamMember{}).Where("id = ?", m1.Id).Update("role", AdtyTeamRoleMember).Error
	require.ErrorIs(t, updateMemberErr, ErrActiveTeamMembershipCollision)

	// After historical collisions are remediated, re-running migration installs the unique indexes and lifts the write gate.
	require.NoError(t, db.Delete(&AdtyVoucher{}, v2.Id).Error)
	require.NoError(t, db.Delete(&AdtyTeamMember{}, m2.Id).Error)
	require.NoError(t, MigrateAdtyTablesOn(db))

	require.NoError(t, db.Create(&AdtyVoucher{
		CodeHash:   "allowed-v3",
		CodeTail:   "3333",
		Purpose:    AdtyVoucherPurposeMainUser,
		Status:     AdtyVoucherStatusRedeemed,
		AmountUSD:  decimal.NewFromInt(1),
		Quota:      500000,
		SourceKind: AdtyVoucherSourcePlatform,
		TradeNo:    "VCHUNIQUE002",
	}).Error)
	require.NoError(t, db.Create(&AdtyTeamMember{
		TeamId: teamB.Id,
		UserId: 88,
		Role:   AdtyTeamRoleMember,
		Status: AdtyTeamMemberActive,
	}).Error)
}
