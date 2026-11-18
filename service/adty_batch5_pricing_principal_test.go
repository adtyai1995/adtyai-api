package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreeModelSnapshotAndTaskTokenSettlementKeepZeroRatioAfterLivePriceIncrease(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)

	const modelName = "free-async-video-model"
	ratio_setting.UpdateModelRatioByJSONString(`{"` + modelName + `": 0}`)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	price := types.PriceData{
		FreeModel:  true,
		UsePrice:   false,
		ModelRatio: 0,
	}
	price.SetBilledModelRatio(decimal.Zero)
	price.GroupRatioInfo.GroupRatio = 1
	price.GroupRatioInfo.SetBilledGroupRatio(decimal.NewFromInt(1))

	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		UsingGroup:      "default",
		UserGroup:       "default",
		StartTime:       time.Now(),
		PriceData:       price,
	}
	AdtyFreezeRequestPriceSnapshot(c, info)
	snap := AdtyFrozenPriceSnapshot(c)
	require.NotNil(t, snap, "free model must freeze a valid official RequestPriceSnapshot")
	assert.True(t, snap.FreeModel)
	assert.Equal(t, "0", snap.ModelBasePrice)

	// Admin later increases the live model ratio while the task is still running.
	ratio_setting.UpdateModelRatioByJSONString(`{"` + modelName + `": 15}`)

	const userID, tokenID, channelID = 610, 610, 610
	seedUser(t, userID, 50000)
	seedToken(t, tokenID, userID, "sk-free-task", 50000)
	seedChannel(t, channelID)

	task := &model.Task{
		TaskID:    "task-free-model-0036",
		UserId:    userID,
		ChannelId: channelID,
		Group:     "default",
		Quota:     0,
		Status:    model.TaskStatusSuccess,
		Properties: model.Properties{
			OriginModelName: modelName,
		},
		PrivateData: model.TaskPrivateData{
			TokenId:       tokenID,
			BillingSource: BillingSourceWallet,
			BillingContext: &model.TaskBillingContext{
				OriginModelName: modelName,
				GroupRatio:      1,
				ModelRatio:      0,
				PriceSnapshot:   snap,
			},
		},
	}
	require.NoError(t, model.DB.Create(task).Error)

	recalculated := RecalculateTaskQuotaByTokens(context.Background(), task, 2000)
	assert.False(t, recalculated, "free model task with frozen 0 ratio must not be repriced using live ratio")
	assert.Equal(t, 50000, getUserQuota(t, userID))
	assert.Equal(t, 50000, getTokenRemainQuota(t, tokenID))
}

func TestMidjourneyRequestContextReservesBeforeUpstreamAndReleasesOnReject(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)

	const userID, tokenID, channelID = 611, 611, 611
	const initialWallet, initialToken, mjQuota = 10000, 4000, 3000
	seedUser(t, userID, initialWallet)
	seedToken(t, tokenID, userID, "sk-mj-preconsume", initialToken)
	seedChannel(t, channelID)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{
		UserId:    userID,
		TokenId:   tokenID,
		TokenKey:  "sk-mj-preconsume",
		UserQuota: initialWallet,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: channelID,
		},
	}
	task := &model.Midjourney{UserId: userID, ChannelId: channelID, Action: "IMAGINE"}

	prepared, err := PrepareMidjourneyTaskBilling(info, task, mjQuota, true, c)
	require.NoError(t, err)
	require.True(t, prepared)
	assert.Equal(t, initialWallet-mjQuota, getUserQuota(t, userID), "wallet quota must be reserved before upstream request")
	assert.Equal(t, initialToken-mjQuota, getTokenRemainQuota(t, tokenID), "token quota must be reserved before upstream request")

	// A second concurrent request requiring 2000 must fail token quota check (only 1000 remaining).
	task2 := &model.Midjourney{UserId: userID, ChannelId: channelID, Action: "IMAGINE"}
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	prepared2, err2 := PrepareMidjourneyTaskBilling(info, task2, 2000, true, c2)
	require.ErrorIs(t, err2, model.ErrBillingTokenInsufficient)
	assert.False(t, prepared2)

	// Upstream rejects the first task -> ReleaseMidjourneyTaskBilling refunds both wallet and token.
	ReleaseMidjourneyTaskBilling(task)
	assert.Equal(t, initialWallet, getUserQuota(t, userID))
	assert.Equal(t, initialToken, getTokenRemainQuota(t, tokenID))
}

func TestOperatingPrincipalResolvesSiteAfterUserMigratedToMainSite(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.BillingOperation{}, &model.AdtySite{}, &model.AdtySiteWallet{}, &model.AdtySiteWalletLedger{}, &model.AdtyTopupOrder{}, &model.AdtyFundBatch{}, &model.AdtyFundAllocation{}))
	require.NoError(t, model.DB.Exec("DELETE FROM adty_sites").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM adty_site_wallets").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM adty_site_wallet_ledgers").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM adty_fund_allocations").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM adty_fund_batches").Error)

	const siteID, userID = 77, 612
	require.NoError(t, model.DB.Create(&model.AdtySite{
		Id:         siteID,
		Subdomain:  "site77",
		RootDomain: "example.com",
		Status:     model.AdtySiteStatusActive,
	}).Error)
	seedUser(t, userID, 500000)
	// Simulate user already migrated to main site (site_id = 0, inherited_from_site_id = siteID).
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"site_id":                0,
		"inherited_from_site_id": siteID,
	}).Error)
	_, err := applyAdtySiteWalletChangeTx(model.DB, siteID, model.AdtySiteWalletOperating, decimal.NewFromInt(10), AdtySiteWalletChange{
		Type:           "principal_credit",
		RefType:        "test",
		RefID:          "seed-operating-77",
		IdempotencyKey: "seed-operating-77",
	})
	require.NoError(t, err)

	opKey := "request:migrated-site-op"
	require.NoError(t, model.DB.Create(&model.BillingOperation{
		OperationKey: opKey,
		UserId:       userID,
		Kind:         "request",
		State:        model.BillingSettled,
		SiteId:       siteID,
	}).Error)

	require.NoError(t, adtyDebitOperatingPrincipalTx(model.DB, userID, opKey, 250000))
	var wallet model.AdtySiteWallet
	require.NoError(t, model.DB.Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletOperating).First(&wallet).Error)
	assert.True(t, decimal.RequireFromString("9.5").Equal(wallet.Balance), "operating wallet must be debited by 0.5 USD even after user.site_id became 0")

	require.NoError(t, adtyRefundOperatingPrincipalTx(model.DB, userID, "task:99:refund", 250000))
	require.NoError(t, model.DB.Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletOperating).First(&wallet).Error)
	assert.True(t, decimal.NewFromInt(10).Equal(wallet.Balance), "operating wallet must be refunded by 0.5 USD via inherited_from_site_id")
}
