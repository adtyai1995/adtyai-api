package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY2309OptionSecretRoundTripPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")

	require.NotEmpty(t, adtySecretOptionKeys)
	secretKey := adtySecretOptionKeys[0]
	const plaintext = "synthetic-ftr2309-option-secret"

	stored, err := encodeAdtyOptionSecret(secretKey, plaintext)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(stored, adtySecretPrefix))
	assert.NotContains(t, stored, plaintext)

	decoded, err := decodeAdtyOptionSecret(secretKey, stored)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decoded)

	unchanged, err := encodeAdtyOptionSecret("RegisterEnabled", plaintext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, unchanged)

	empty, err := encodeAdtyOptionSecret(secretKey, "")
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = encodeAdtyOptionSecret(secretKey, "adty:secret:"+plaintext)
	require.Error(t, err)

	require.NoError(t, db.AutoMigrate(&Option{}))
	require.NoError(t, db.Create(&Option{Key: secretKey, Value: stored}).Error)
	var row Option
	require.NoError(t, db.Where(&Option{Key: secretKey}).First(&row).Error)
	assert.Equal(t, stored, row.Value)
	assert.NotContains(t, row.Key, plaintext)
	assert.NotContains(t, row.Value, plaintext)
}
