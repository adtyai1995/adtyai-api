package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY1904TranslationConnectivityPG(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(
		&model.Channel{}, &model.Ability{},
		&model.AdtyTranslationConfig{}, &model.AdtyTranslationHead{},
		&model.AdtyTranslationJob{}, &model.AdtyTranslationUsage{},
	))

	var gotPath, gotAPIKey, gotModel string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		var body struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"connected"}]}`))
	}))
	t.Cleanup(server.Close)

	oldClient := adtyTranslationHTTPClient
	adtyTranslationHTTPClient = func(time.Duration) *http.Client {
		return adtyTestTranslationClient(t, server)
	}
	t.Cleanup(func() { adtyTranslationHTTPClient = oldClient })

	baseURL := "https://ftr1904.example"
	mapping := `{"ftr-1904-model":"claude-haiku"}`
	ch := &model.Channel{
		Name:         "ftr-1904",
		Type:         constant.ChannelTypeAnthropic,
		Key:          "sk-ant-ftr-1904",
		Status:       common.ChannelStatusEnabled,
		BaseURL:      &baseURL,
		Models:       "ftr-1904-model",
		ModelMapping: &mapping,
		Group:        "default",
	}
	require.NoError(t, db.Create(ch).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "ftr-1904-model", ChannelId: ch.Id, Enabled: true,
	}).Error)

	_, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:      model.AdtyTranslationModeSet,
		ModelName: "ftr-1904-model",
		Enabled:   true,
	})
	require.NoError(t, err)
	require.NoError(t, CheckAdtyTranslationProvider())

	tested, err := TestAdtyTranslation("console.adty.test")
	require.NoError(t, err)
	require.NotZero(t, tested.TestedAt)
	require.Equal(t, "/v1/messages", gotPath)
	require.Equal(t, "sk-ant-ftr-1904", gotAPIKey)
	require.Equal(t, "claude-haiku", gotModel)
}
