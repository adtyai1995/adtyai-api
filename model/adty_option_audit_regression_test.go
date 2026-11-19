package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpecialOptionAuditUsesWriterTransactionPG(t *testing.T) {
	opened, err := OpenAdtyTestDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opened.Close()) })
	db := opened.DB
	require.NoError(t, db.AutoMigrate(&Option{}, &AuditIntent{}))
	for _, key := range []string{"RetryTimes", "ModelPrice"} {
		t.Run(key, func(t *testing.T) {
			value := "2"
			if key == "ModelPrice" {
				value = `{"audit-fixture":0.1}`
			}
			marker := errors.New("audit persistence unavailable")
			called := false
			check := func(tx *gorm.DB) error {
				called = true
				require.NoError(t, tx.Create(&Option{Key: "audit-writer-marker", Value: key}).Error)
				return marker
			}
			if key == "RetryTimes" {
				err = UpdateRequestPolicyOptions(map[string]string{key: value}, check)
			} else {
				err = UpdateModelPricingOptions(map[string]string{key: value}, check)
			}
			require.ErrorIs(t, err, marker)
			require.True(t, called)
			var count int64
			require.NoError(t, db.Model(&Option{}).Where("key IN ?", []string{key, "audit-writer-marker"}).Count(&count).Error)
			require.Zero(t, count, "specialized options and audit must roll back together")
		})
	}
}
