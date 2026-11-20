package model

import (
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFTRADTY0705ReservePriceSnapshotImmutablePG(t *testing.T) {
	db := prepareAdtyTopUpDB(t)
	pgtest.RecordServerVersion(t, db)
	user := seedAdtyTopUpUser(t, db, "ftr0705", 10_000_000)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CreateAdtyFundBatchesTx(tx, user.Id, "ftr0705-seed", 10_000_000, 0)
	}))

	empty := BillingOperation{OperationKey: "request:ftr0705-fill", UserId: user.Id}
	_, err := ReserveBillingOperation(empty, 1000)
	require.NoError(t, err)
	stored, err := GetBillingOperation("request:ftr0705-fill")
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Empty(t, stored.PriceSnapshot)

	filled := empty
	filled.PriceSnapshot = "snap-first"
	_, err = ReserveBillingOperation(filled, 1000)
	require.NoError(t, err)
	stored, err = GetBillingOperation("request:ftr0705-fill")
	require.NoError(t, err)
	require.Equal(t, "snap-first", stored.PriceSnapshot)

	replaced := empty
	replaced.PriceSnapshot = "snap-second"
	_, err = ReserveBillingOperation(replaced, 1000)
	require.NoError(t, err)
	stored, err = GetBillingOperation("request:ftr0705-fill")
	require.NoError(t, err)
	require.Equal(t, "snap-first", stored.PriceSnapshot)

	seeded := BillingOperation{OperationKey: "request:ftr0705-keep", UserId: user.Id, PriceSnapshot: "snap-kept"}
	_, err = ReserveBillingOperation(seeded, 500)
	require.NoError(t, err)
	seeded.PriceSnapshot = "snap-other"
	_, err = ReserveBillingOperation(seeded, 500)
	require.NoError(t, err)
	kept, err := GetBillingOperation("request:ftr0705-keep")
	require.NoError(t, err)
	require.Equal(t, "snap-kept", kept.PriceSnapshot)

	require.Equal(t, 10_000_000-1500, userQuota(t, db, user.Id))
}
