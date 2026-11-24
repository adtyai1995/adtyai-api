package vertex

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubAccessTokenFetch replaces the Google token exchange for one test and
// clears the token cache before and after it.
func stubAccessTokenFetch(t *testing.T, fetch func(Credentials, string) (string, time.Duration, error)) {
	t.Helper()
	accessTokens.Clear()
	original := fetchAccessToken
	fetchAccessToken = fetch
	t.Cleanup(func() {
		fetchAccessToken = original
		accessTokens.Clear()
	})
}

func newAccessTokenTestInfo(channelID int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channelID}}
}

func newAccessTokenTestAdaptor(keyID string) *Adaptor {
	return &Adaptor{AccountCredentials: Credentials{ClientEmail: "sa@project.iam.gserviceaccount.com", PrivateKeyID: keyID}}
}

func TestGetAccessTokenReusesTokenUntilNearExpiry(t *testing.T) {
	var calls atomic.Int32
	stubAccessTokenFetch(t, func(Credentials, string) (string, time.Duration, error) {
		return fmt.Sprintf("token-%d", calls.Add(1)), time.Hour, nil
	})
	info := newAccessTokenTestInfo(7)
	a := newAccessTokenTestAdaptor("key-1")

	first, err := getAccessToken(a, info)
	require.NoError(t, err)
	second, err := getAccessToken(a, info)
	require.NoError(t, err)
	assert.Equal(t, "token-1", first)
	assert.Equal(t, first, second)
	assert.Equal(t, int32(1), calls.Load())

	// A token inside the refresh margin is replaced.
	value, ok := accessTokens.Load("7")
	require.True(t, ok)
	entry := value.(cachedAccessToken)
	entry.expiresAt = time.Now().Add(-time.Second)
	accessTokens.Store("7", entry)

	third, err := getAccessToken(a, info)
	require.NoError(t, err)
	assert.Equal(t, "token-2", third)
	assert.Equal(t, int32(2), calls.Load())
}

func TestGetAccessTokenSeparatesKeySlotsAndReplacedKeys(t *testing.T) {
	var calls atomic.Int32
	stubAccessTokenFetch(t, func(creds Credentials, _ string) (string, time.Duration, error) {
		calls.Add(1)
		return creds.PrivateKeyID, time.Hour, nil
	})

	multiKey := newAccessTokenTestInfo(9)
	multiKey.ChannelIsMultiKey = true
	multiKey.ChannelMultiKeyIndex = 1
	slotOne, err := getAccessToken(newAccessTokenTestAdaptor("slot-1"), multiKey)
	require.NoError(t, err)
	multiKey.ChannelMultiKeyIndex = 2
	slotTwo, err := getAccessToken(newAccessTokenTestAdaptor("slot-2"), multiKey)
	require.NoError(t, err)
	assert.Equal(t, "slot-1", slotOne)
	assert.Equal(t, "slot-2", slotTwo)

	// Replacing the channel's key fetches a token for the new key.
	single := newAccessTokenTestInfo(10)
	_, err = getAccessToken(newAccessTokenTestAdaptor("old"), single)
	require.NoError(t, err)
	replaced, err := getAccessToken(newAccessTokenTestAdaptor("new"), single)
	require.NoError(t, err)
	assert.Equal(t, "new", replaced)
	assert.Equal(t, int32(4), calls.Load())
}

func TestGetAccessTokenSharesOneExchangeAcrossConcurrentRequests(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	stubAccessTokenFetch(t, func(Credentials, string) (string, time.Duration, error) {
		calls.Add(1)
		<-release
		return "shared", time.Hour, nil
	})
	info := newAccessTokenTestInfo(11)
	a := newAccessTokenTestAdaptor("key-1")

	const workers = 8
	var wg sync.WaitGroup
	results := make([]string, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = getAccessToken(a, info)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.Equal(t, int32(1), calls.Load())
	for _, token := range results {
		assert.Equal(t, "shared", token)
	}
}

func TestGetAccessTokenDoesNotCacheFailures(t *testing.T) {
	var calls atomic.Int32
	stubAccessTokenFetch(t, func(Credentials, string) (string, time.Duration, error) {
		if calls.Add(1) == 1 {
			return "", 0, errors.New("exchange failed")
		}
		return "recovered", time.Hour, nil
	})
	info := newAccessTokenTestInfo(12)
	a := newAccessTokenTestAdaptor("key-1")

	_, err := getAccessToken(a, info)
	require.ErrorContains(t, err, "exchange failed")
	token, err := getAccessToken(a, info)
	require.NoError(t, err)
	assert.Equal(t, "recovered", token)
}

func TestGetRequestURLUsesConfiguredVertexAPIVersion(t *testing.T) {
	serviceAccountJSON := `{"project_id":"demo-proj","private_key_id":"k1","client_email":"sa@demo.iam.gserviceaccount.com"}`
	for _, tc := range []struct {
		name    string
		keyType dto.VertexKeyType
		apiKey  string
		projID  string
		model   string
		version string
		want    string
	}{
		{
			name:    "service account gemini default v1",
			keyType: dto.VertexKeyTypeJSON,
			apiKey:  serviceAccountJSON,
			model:   "gemini-2.5-pro",
			want:    "https://us-central1-aiplatform.googleapis.com/v1/projects/demo-proj/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent",
		},
		{
			name:    "service account gemini v1beta1",
			keyType: dto.VertexKeyTypeJSON,
			apiKey:  serviceAccountJSON,
			model:   "gemini-2.5-pro",
			version: "v1beta1",
			want:    "https://us-central1-aiplatform.googleapis.com/v1beta1/projects/demo-proj/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent",
		},
		{
			name:    "service account claude stays v1",
			keyType: dto.VertexKeyTypeJSON,
			apiKey:  serviceAccountJSON,
			model:   "claude-sonnet-4-5-20250929",
			version: "v1beta1",
			want:    "https://us-central1-aiplatform.googleapis.com/v1/projects/demo-proj/locations/us-central1/publishers/anthropic/models/claude-sonnet-4-5@20250929:rawPredict",
		},
		{
			name:    "api key gemini v1beta1",
			keyType: dto.VertexKeyTypeAPIKey,
			apiKey:  "test-api-key",
			projID:  "demo-proj",
			model:   "gemini-2.5-flash",
			version: "v1beta1",
			want:    "https://us-central1-aiplatform.googleapis.com/v1beta1/projects/demo-proj/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent?key=test-api-key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				OriginModelName: tc.model,
				ChannelMeta: &relaycommon.ChannelMeta{
					ApiKey:            tc.apiKey,
					ApiVersion:        `{"default":"us-central1"}`,
					UpstreamModelName: tc.model,
					ChannelOtherSettings: dto.ChannelOtherSettings{
						VertexKeyType:    tc.keyType,
						VertexProjectID:  tc.projID,
						VertexAPIVersion: tc.version,
					},
				},
			}
			a := &Adaptor{}
			a.Init(info)
			got, err := a.GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
