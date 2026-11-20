package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-08-02: the USD scale stays 500000 even when the float QuotaPerUnit is mutated.
func TestFTRADTY0802FixedQuotaScalePG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = previous })

	assert.Equal(t, int64(500000), common.FixedQuotaPerUnit)
	assert.True(t, common.QuotaPerUnitDecimal().Equal(decimal.NewFromInt(500000)))
	oneAndQuarter, err := common.WalletQuotaFromDecimalStrict(decimal.RequireFromString("1.25").Mul(common.QuotaPerUnitDecimal()).Floor())
	require.NoError(t, err)
	assert.Equal(t, 625000, oneAndQuarter)
	two, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(2).Mul(common.QuotaPerUnitDecimal()).Floor())
	require.NoError(t, err)
	assert.Equal(t, 1000000, two)
	require.NoError(t, rejectChangedQuotaPerUnit("500000"))
	require.Error(t, rejectChangedQuotaPerUnit("250000"))
	assert.True(t, common.QuotaPerUnitDecimal().Equal(decimal.NewFromInt(500000)))
	assert.Equal(t, 1.0, common.QuotaPerUnit)
}
