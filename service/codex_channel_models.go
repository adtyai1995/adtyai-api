package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

func FetchCodexChannelModels(channel *model.Channel) ([]string, error) {
	if channel == nil || channel.Type != constant.ChannelTypeCodex {
		return nil, fmt.Errorf("channel type is not Codex")
	}
	if channel.ChannelInfo.IsMultiKey {
		return nil, fmt.Errorf("codex channel does not support multi-key model discovery")
	}

	setting := channel.GetSetting()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The GitHub release lookup is not an upstream request; it always verifies
	// certificates, whatever the channel's or global TLS setting says.
	var releaseClient *http.Client
	if strings.TrimSpace(setting.Proxy) == "" {
		releaseClient = NewVerifiedDirectNoRedirectHTTPClient(20 * time.Second)
	} else {
		baseClient, pErr := GetHttpClientWithProxy(setting.Proxy)
		if pErr != nil {
			return nil, pErr
		}
		verifiedTransport, vErr := cloneAdtyVerifiedPaymentTransport(baseClient.Transport)
		if vErr != nil {
			return nil, vErr
		}
		defer verifiedTransport.CloseIdleConnections()
		cloned := *baseClient
		cloned.Transport = verifiedTransport
		cloned.Timeout = 20 * time.Second
		cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		releaseClient = &cloned
	}
	clientVersion, err := GetLatestCodexClientVersion(ctx, releaseClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get Codex client version: %w", err)
	}

	client, err := GetHttpClientWithProxySettings(setting.Proxy, setting)
	if err != nil {
		return nil, err
	}

	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(constant.ChannelTypeCodex)
	}
	return fetchCodexChannelModels(ctx, channel, baseURL, client, clientVersion)
}

func fetchCodexChannelModels(
	ctx context.Context,
	channel *model.Channel,
	baseURL string,
	client *http.Client,
	clientVersion string,
) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
	}
	oauthKey, err := parseCodexOAuthKey(strings.TrimSpace(channel.Key))
	if err != nil {
		return nil, err
	}

	statusCode, models, err := FetchCodexModels(ctx, client, baseURL, oauthKey, clientVersion)
	if err != nil {
		return nil, err
	}
	if statusCode == http.StatusUnauthorized {
		if channel.Id <= 0 {
			return nil, fmt.Errorf("codex channel credential expired; save the channel before retrying model fetch")
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		refreshedKey, refreshedChannel, refreshErr := RefreshCodexChannelCredential(
			refreshCtx,
			channel.Id,
			CodexCredentialRefreshOptions{ResetCaches: true},
		)
		cancel()
		if refreshErr != nil {
			return nil, fmt.Errorf("failed to refresh Codex channel credential: %w", refreshErr)
		}
		if refreshedChannel != nil {
			channel.Key = refreshedChannel.Key
		}
		statusCode, models, err = FetchCodexModels(ctx, client, baseURL, &CodexOAuthKey{
			AccessToken: refreshedKey.AccessToken,
			AccountID:   refreshedKey.AccountID,
		}, clientVersion)
		if err != nil {
			return nil, err
		}
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("upstream status: %d", statusCode)
	}
	return models, nil
}
