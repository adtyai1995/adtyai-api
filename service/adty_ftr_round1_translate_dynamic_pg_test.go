package service

import (
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY1905TranslateChineseDynamicPG(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	require.NoError(t, db.AutoMigrate(
		&model.AdtyNotificationPublication{},
		&model.AdtyTranslationConfig{},
		&model.AdtyTranslationHead{},
		&model.AdtyTranslationJob{},
		&model.AdtyTranslationUsage{},
	))
	actor := referralUser(t, db, "ftr1905")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", actor.Id).Update("quota", 500000).Error)

	_, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:        model.AdtyTranslationModeOwn,
		Protocol:    "openai",
		BaseURL:     "https://translator.ftr1905.example/v1",
		Token:       "ext-secret-token-value-1234",
		ModelName:   "ftr-1905-model",
		TimeoutSec:  5,
		Concurrency: 1,
		MaxChars:    2000,
		DailyQuota:  10,
		RetryLimit:  0,
		Enabled:     true,
		RequestHost: "console.adty.test",
	})
	require.NoError(t, err)

	calls := 0
	old := AdtyTranslateFunc
	AdtyTranslateFunc = func(_, source string) (string, error) {
		calls++
		require.Contains(t, source, "今晚维护")
		return "Maintenance tonight", nil
	}
	t.Cleanup(func() { AdtyTranslateFunc = old })

	published, err := SaveAdtyNotificationPublication(actor.Id, nil, 0, AdtyNotificationInput{
		Title:    "维护",
		Content:  "今晚维护",
		Kind:     "maintenance",
		Audience: "all_users",
	}, false)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Contains(t, published.Content, "今晚维护")
	require.Contains(t, published.Content, "Maintenance tonight")

	var head model.AdtyTranslationHead
	require.NoError(t, db.Where("object_key = ?", adtyNotificationTranslationKey(published.Id)).First(&head).Error)
	require.Equal(t, model.AdtyTranslationReady, head.Status)
	require.Equal(t, "Maintenance tonight", head.English)
	require.Equal(t, "今晚维护", head.Source)

	var stored model.User
	require.NoError(t, db.Select("quota").First(&stored, actor.Id).Error)
	require.Equal(t, 500000, stored.Quota)
}

func TestFTRADTY1906ReuseSavedTranslationPG(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	require.NoError(t, db.AutoMigrate(
		&model.AdtyNotificationPublication{},
		&model.AdtyTranslationConfig{},
		&model.AdtyTranslationHead{},
		&model.AdtyTranslationJob{},
		&model.AdtyTranslationUsage{},
	))
	actor := referralUser(t, db, "ftr1906")

	_, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:        model.AdtyTranslationModeOwn,
		Protocol:    "openai",
		BaseURL:     "https://translator.ftr1906.example/v1",
		Token:       "ext-secret-token-value-1234",
		ModelName:   "ftr-1906-model",
		TimeoutSec:  5,
		Concurrency: 1,
		MaxChars:    2000,
		DailyQuota:  10,
		RetryLimit:  0,
		Enabled:     true,
		RequestHost: "console.adty.test",
	})
	require.NoError(t, err)

	calls := 0
	old := AdtyTranslateFunc
	AdtyTranslateFunc = func(_, _ string) (string, error) {
		calls++
		return "Saved English", nil
	}
	t.Cleanup(func() { AdtyTranslateFunc = old })

	first, err := SaveAdtyNotificationPublication(actor.Id, nil, 0, AdtyNotificationInput{
		Title:    "公告",
		Content:  "中文公告",
		Kind:     "maintenance",
		Audience: "all_users",
	}, false)
	require.NoError(t, err)
	require.Equal(t, 1, calls)

	second, err := SaveAdtyNotificationPublication(actor.Id, nil, first.Id, AdtyNotificationInput{
		Title:    "公告",
		Content:  "中文公告",
		Kind:     "maintenance",
		Audience: "all_users",
		Revision: first.Revision,
	}, false)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Contains(t, second.Content, "Saved English")

	var jobs int64
	require.NoError(t, db.Model(&model.AdtyTranslationJob{}).Where("object_key = ?", adtyNotificationTranslationKey(first.Id)).Count(&jobs).Error)
	require.EqualValues(t, 1, jobs)
}
