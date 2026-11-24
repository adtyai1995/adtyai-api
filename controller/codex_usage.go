package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func GetCodexChannelUsage(c *gin.Context) {
	fetchCodexChannelWhamData(
		c,
		adaptCodexWhamFetch(service.FetchCodexWhamUsage),
		common.LogText("failed to fetch codex usage"),
		"Failed to fetch usage. Please try again later",
		false,
	)
}

func GetCodexChannelRateLimitResetCredits(c *gin.Context) {
	fetchCodexChannelWhamData(
		c,
		adaptCodexWhamFetch(service.FetchCodexWhamRateLimitResetCredits),
		common.LogText("failed to fetch codex reset credits"),
		"Failed to fetch reset credit details. Please try again later",
		false,
	)
}

func ResetCodexChannelUsage(c *gin.Context) {
	fetchCodexChannelWhamData(
		c,
		service.ConsumeCodexWhamRateLimitResetCredit,
		common.LogText("failed to reset codex usage"),
		"Failed to reset usage. Please try again later",
		true,
	)
}

type codexWhamFetchFunc func(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	accountID string,
	redeemRequestID string,
) (statusCode int, body []byte, err error)

func adaptCodexWhamFetch(
	fetch func(ctx context.Context, client *http.Client, baseURL string, accessToken string, accountID string) (int, []byte, error),
) codexWhamFetchFunc {
	return func(ctx context.Context, client *http.Client, baseURL string, accessToken string, accountID string, _ string) (int, []byte, error) {
		return fetch(ctx, client, baseURL, accessToken, accountID)
	}
}

func fetchCodexChannelWhamData(
	c *gin.Context,
	fetch codexWhamFetchFunc,
	logPrefix string,
	userMessage string,
	claimRedeem bool,
) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid channel ID")
		return
	}

	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if ch == nil {
		common.ApiErrorT(c, "Channel not found")
		return
	}
	if ch.Type != constant.ChannelTypeCodex {
		common.ApiErrorT(c, "Channel type is not Codex")
		return
	}
	if ch.ChannelInfo.IsMultiKey {
		common.ApiErrorT(c, "Multi-key channel is not supported")
		return
	}

	oauthKey, err := codex.ParseOAuthKey(strings.TrimSpace(ch.Key))
	if err != nil {
		common.SysError(common.LogText("failed to parse oauth key: %s", err.Error()))
		common.ApiErrorT(c, "Failed to parse credentials. Please check the channel configuration")
		return
	}
	accessToken := strings.TrimSpace(oauthKey.AccessToken)
	accountID := strings.TrimSpace(oauthKey.AccountID)
	if accessToken == "" {
		common.ApiErrorT(c, "Codex channel requires access_token")
		return
	}
	if accountID == "" {
		common.ApiErrorT(c, "Codex channel requires account_id")
		return
	}

	setting := ch.GetSetting()
	client, err := service.GetHttpClientWithProxySettings(setting.Proxy, setting)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	redeemID := ""
	if claimRedeem {
		var claimErr error
		redeemID, claimErr = service.ClaimCodexChannelResetRedeemID(ch.Id)
		if claimErr != nil {
			common.SysError(common.LogText("%s claim redeem id: %s", logPrefix, claimErr.Error()))
			common.ApiErrorT(c, userMessage)
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	statusCode, body, err := fetch(ctx, client, ch.GetBaseURL(), accessToken, accountID, redeemID)
	if err != nil {
		common.SysError(common.LogText("%s: %s", logPrefix, common.SanitizeOutboundError(err, accessToken).Error()))
		common.ApiErrorT(c, userMessage)
		return
	}

	if (statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden) && strings.TrimSpace(oauthKey.RefreshToken) != "" {
		refreshCtx, refreshCancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer refreshCancel()

		refreshed, _, refreshErr := service.RefreshCodexChannelCredential(refreshCtx, ch.Id, service.CodexCredentialRefreshOptions{ResetCaches: true})
		if refreshErr == nil {
			accessToken = strings.TrimSpace(refreshed.AccessToken)
			if strings.TrimSpace(refreshed.AccountID) != "" {
				accountID = strings.TrimSpace(refreshed.AccountID)
			}

			ctx2, cancel2 := context.WithTimeout(c.Request.Context(), 15*time.Second)
			defer cancel2()
			statusCode, body, err = fetch(ctx2, client, ch.GetBaseURL(), accessToken, accountID, redeemID)
			if err != nil {
				common.SysError(common.LogText("%s after refresh: %s", logPrefix, common.SanitizeOutboundError(err, accessToken).Error()))
				common.ApiErrorT(c, userMessage)
				return
			}
		}
	}

	var payload any
	if common.Unmarshal(body, &payload) != nil {
		payload = string(body)
	}

	ok := statusCode >= 200 && statusCode < 300
	if claimRedeem && ok {
		if relErr := service.ReleaseCodexChannelResetRedeemID(ch.Id, redeemID); relErr != nil {
			common.SysError(common.LogText("%s release redeem id: %s", logPrefix, relErr.Error()))
		}
	}
	resp := gin.H{
		"success":         ok,
		"message":         "",
		"upstream_status": statusCode,
		"data":            payload,
	}
	if !ok {
		resp["message"] = fmt.Sprintf("Upstream status: %d", statusCode)
		resp["message_key"] = "Upstream status: {{status}}"
		resp["message_params"] = gin.H{"status": statusCode}
	}
	c.JSON(http.StatusOK, resp)
}
