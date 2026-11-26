package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type TokenDetails struct {
	TextTokens  int
	AudioTokens int
}

type QuotaInfo struct {
	InputDetails                   TokenDetails
	OutputDetails                  TokenDetails
	ModelName                      string
	UsePrice                       bool
	ModelPrice                     float64
	ModelRatio                     float64
	GroupRatio                     float64
	GroupRatioDecimal              decimal.Decimal
	hasGroupRatioDecimal           bool
	ModelRatioDecimal              decimal.Decimal
	hasModelRatioDecimal           bool
	ModelPriceDecimal              decimal.Decimal
	hasModelPriceDecimal           bool
	CompletionRatioDecimal         decimal.Decimal
	hasCompletionRatioDecimal      bool
	AudioRatioDecimal              decimal.Decimal
	hasAudioRatioDecimal           bool
	AudioCompletionRatioDecimal    decimal.Decimal
	hasAudioCompletionRatioDecimal bool
}

func quotaInfoGroupRatio(info QuotaInfo) decimal.Decimal {
	if info.hasGroupRatioDecimal {
		return info.GroupRatioDecimal
	}
	return decimal.NewFromFloat(info.GroupRatio)
}

func withBilledGroupRatio(info QuotaInfo, billed decimal.Decimal) QuotaInfo {
	info.GroupRatioDecimal = billed
	info.hasGroupRatioDecimal = true
	if info.GroupRatio == 0 {
		info.GroupRatio = billed.InexactFloat64()
	}
	return info
}

func quotaInfoFromPriceData(info QuotaInfo, relayInfo *relaycommon.RelayInfo) QuotaInfo {
	if relayInfo == nil {
		return info
	}
	info = withBilledGroupRatio(info, relayInfo.PriceData.GroupRatioInfo.BilledGroupRatio())
	price := relayInfo.PriceData
	if price.HasBilledModelRatio() {
		info.ModelRatioDecimal = price.BilledModelRatio()
		info.hasModelRatioDecimal = true
		if info.ModelRatio == 0 {
			info.ModelRatio = info.ModelRatioDecimal.InexactFloat64()
		}
	}
	if price.HasBilledModelPrice() {
		info.ModelPriceDecimal = price.BilledModelPrice()
		info.hasModelPriceDecimal = true
		if info.ModelPrice == 0 {
			info.ModelPrice = info.ModelPriceDecimal.InexactFloat64()
		}
	}
	if price.HasBilledCompletionRatio() {
		info.CompletionRatioDecimal = price.BilledCompletionRatio()
		info.hasCompletionRatioDecimal = true
	}
	if price.HasBilledAudioRatio() {
		info.AudioRatioDecimal = price.BilledAudioRatio()
		info.hasAudioRatioDecimal = true
	}
	if price.HasBilledAudioCompletionRatio() {
		info.AudioCompletionRatioDecimal = price.BilledAudioCompletionRatio()
		info.hasAudioCompletionRatioDecimal = true
	}
	return info
}

func hasCustomModelRatio(modelName string, currentRatio float64) bool {
	defaultRatio, exists := ratio_setting.GetDefaultModelRatioMap()[modelName]
	if !exists {
		return true
	}
	return currentRatio != defaultRatio
}

func calculateAudioQuotaDecimal(info QuotaInfo, toolSurcharge decimal.Decimal) decimal.Decimal {
	if info.UsePrice {
		modelPrice := decimal.NewFromFloat(info.ModelPrice)
		if info.hasModelPriceDecimal {
			modelPrice = info.ModelPriceDecimal
		}
		quotaPerUnit := common.QuotaPerUnitDecimal()
		groupRatio := quotaInfoGroupRatio(info)

		return modelPrice.Mul(quotaPerUnit).Mul(groupRatio).Add(toolSurcharge)
	}

	completionRatio := ratio_setting.GetCompletionRatioDecimal(info.ModelName)
	if info.hasCompletionRatioDecimal {
		completionRatio = info.CompletionRatioDecimal
	}
	audioRatio := ratio_setting.GetAudioRatioDecimal(info.ModelName)
	if info.hasAudioRatioDecimal {
		audioRatio = info.AudioRatioDecimal
	}
	audioCompletionRatio := ratio_setting.GetAudioCompletionRatioDecimal(info.ModelName)
	if info.hasAudioCompletionRatioDecimal {
		audioCompletionRatio = info.AudioCompletionRatioDecimal
	}

	groupRatio := quotaInfoGroupRatio(info)
	modelRatio := decimal.NewFromFloat(info.ModelRatio)
	if info.hasModelRatioDecimal {
		modelRatio = info.ModelRatioDecimal
	}
	ratio := groupRatio.Mul(modelRatio)

	inputTextTokens := decimal.NewFromInt(int64(max(info.InputDetails.TextTokens, 0)))
	outputTextTokens := decimal.NewFromInt(int64(max(info.OutputDetails.TextTokens, 0)))
	inputAudioTokens := decimal.NewFromInt(int64(max(info.InputDetails.AudioTokens, 0)))
	outputAudioTokens := decimal.NewFromInt(int64(max(info.OutputDetails.AudioTokens, 0)))

	quota := decimal.Zero
	quota = quota.Add(inputTextTokens)
	quota = quota.Add(outputTextTokens.Mul(completionRatio))
	quota = quota.Add(inputAudioTokens.Mul(audioRatio))
	quota = quota.Add(outputAudioTokens.Mul(audioRatio).Mul(audioCompletionRatio))

	quota = quota.Mul(ratio).Add(toolSurcharge)

	// If ratio is not zero and quota is less than or equal to zero, set quota to 1
	if !ratio.IsZero() && quota.LessThanOrEqual(decimal.Zero) {
		quota = decimal.NewFromInt(1)
	}

	return quota
}

func calculateAudioQuota(info QuotaInfo) (int, *common.QuotaClamp) {
	return common.QuotaFromDecimalChecked(calculateAudioQuotaDecimal(info, decimal.Zero))
}

func normalizeAudioUsageDetails(promptTokens, completionTokens, rawTotal, rawTextIn, rawAudioIn, rawTextOut, rawAudioOut int) (textIn, audioIn, textOut, audioOut, total int) {
	prompt := max(promptTokens, 0)
	completion := max(completionTokens, 0)
	textIn = max(rawTextIn, 0)
	audioIn = max(rawAudioIn, 0)
	textOut = max(rawTextOut, 0)
	audioOut = max(rawAudioOut, 0)
	if textIn == 0 && prompt > audioIn {
		textIn = prompt - audioIn
	}
	if textOut == 0 && completion > audioOut {
		textOut = completion - audioOut
	}
	total = max(max(rawTotal, 0), prompt+completion, textIn+textOut+audioIn+audioOut)
	if textIn == 0 && audioIn == 0 && textOut == 0 && audioOut == 0 && total > 0 {
		textIn = total
	}
	return
}

func realtimeUsageToDTOUsage(usage *dto.RealtimeUsage) *dto.Usage {
	if usage == nil {
		return &dto.Usage{}
	}
	return &dto.Usage{
		PromptTokens:           usage.InputTokens,
		CompletionTokens:       usage.OutputTokens,
		TotalTokens:            usage.TotalTokens,
		PromptTokensDetails:    usage.InputTokenDetails.Clone(),
		CompletionTokenDetails: usage.OutputTokenDetails,
		InputTokens:            usage.InputTokens,
		OutputTokens:           usage.OutputTokens,
	}
}

func buildRealtimeTieredParams(relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage) billingexpr.TokenParams {
	var usedVars map[string]bool
	if relayInfo != nil && relayInfo.TieredBillingSnapshot != nil {
		usedVars = billingexpr.UsedVarsByHash(relayInfo.TieredBillingSnapshot.ExprString, relayInfo.TieredBillingSnapshot.ExprHash)
	}
	return BuildTieredTokenParams(realtimeUsageToDTOUsage(usage), false, usedVars)
}

func PreWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage) error {
	if relayInfo.PriceData.UsePrice {
		return nil
	}
	userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
	if err != nil {
		return err
	}

	token, err := model.GetTokenByKey(strings.TrimPrefix(relayInfo.TokenKey, "sk-"), false)
	if err != nil {
		return err
	}

	var quota int
	if tieredOk, tieredQuota, _ := TryTieredSettle(relayInfo, buildRealtimeTieredParams(relayInfo, usage)); tieredOk {
		quota = tieredQuota
	} else {
		modelName := relayInfo.GetBillingModelName()
		if modelName == "" {
			modelName = relayInfo.OriginModelName
		}
		var textInputTokens, audioInputTokens, textOutTokens, audioOutTokens int
		if usage != nil {
			textInputTokens, audioInputTokens, textOutTokens, audioOutTokens, _ = normalizeAudioUsageDetails(
				usage.InputTokens, usage.OutputTokens, usage.TotalTokens,
				usage.InputTokenDetails.TextTokens, usage.InputTokenDetails.AudioTokens,
				usage.OutputTokenDetails.TextTokens, usage.OutputTokenDetails.AudioTokens,
			)
		}
		quotaInfo := QuotaInfo{
			InputDetails: TokenDetails{
				TextTokens:  textInputTokens,
				AudioTokens: audioInputTokens,
			},
			OutputDetails: TokenDetails{
				TextTokens:  textOutTokens,
				AudioTokens: audioOutTokens,
			},
			ModelName:  modelName,
			UsePrice:   relayInfo.PriceData.UsePrice,
			ModelPrice: relayInfo.PriceData.ModelPrice,
			ModelRatio: relayInfo.PriceData.ModelRatio,
			GroupRatio: relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		}
		quotaInfo = quotaInfoFromPriceData(quotaInfo, relayInfo)
		if snap := AdtyFrozenPriceSnapshot(ctx); snap != nil {
			quotaInfo = applySnapshotToQuotaInfo(quotaInfo, snap)
		} else {
			if !quotaInfo.hasModelRatioDecimal {
				modelRatioDec, _, _ := ratio_setting.GetModelRatioDecimal(modelName)
				quotaInfo.ModelRatioDecimal = modelRatioDec
				quotaInfo.hasModelRatioDecimal = true
				quotaInfo.ModelRatio = modelRatioDec.InexactFloat64()
			}
			if !quotaInfo.hasGroupRatioDecimal || quotaInfo.GroupRatioDecimal.IsZero() && !relayInfo.PriceData.GroupRatioInfo.HasBilledGroupRatio() {
				autoGroup, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroup)
				if exists {
					relayInfo.UsingGroup = autoGroup.(string)
				}
				actualGroupRatioDec := ratio_setting.GetGroupRatioDecimal(relayInfo.UsingGroup)
				actualGroupRatioDec, _ = AdtyApplySiteMarkupDecimal(ctx, relayInfo.UserId, relayInfo.UsingGroup, actualGroupRatioDec, modelName) // 二开：任务 11.4
				quotaInfo = withBilledGroupRatio(quotaInfo, actualGroupRatioDec)
			}
		}

		var clamp *common.QuotaClamp
		quota, clamp = calculateAudioQuota(quotaInfo)
		noteQuotaClamp(relayInfo, clamp)
	}

	if relayInfo.Billing == nil && quota > 0 {
		if apiErr := PreConsumeBilling(ctx, 0, relayInfo); apiErr != nil {
			return apiErr
		}
	}

	availableQuota := int64(userQuota)
	isTeamKey := false
	var op *model.BillingOperation
	if session, ok := relayInfo.Billing.(*BillingSession); ok && session.operationKey != "" {
		if loadedOp, opErr := model.GetBillingOperation(session.operationKey); opErr == nil && loadedOp != nil {
			op = loadedOp
			if op.PayerKind == model.BillingPayerTeam && op.PayerTeamId > 0 {
				var team model.AdtyTeam
				if err := model.DB.Select("id", "shared_quota").First(&team, op.PayerTeamId).Error; err != nil {
					return err
				}
				availableQuota = team.SharedQuota
				isTeamKey = true
			}
		}
	}
	if !isTeamKey && relayInfo.TokenId > 0 && !relayInfo.IsPlayground {
		teamQuota, teamBound, teamErr := adtyTeamSharedQuotaForToken(relayInfo.TokenId)
		if teamErr != nil {
			return teamErr
		}
		if teamBound {
			availableQuota = teamQuota
			isTeamKey = true
		}
	}
	if !isTeamKey {
		if op != nil {
			scoped, scopeErr := model.AdtyOwnerScopedWallet(model.DB, op, userQuota)
			if scopeErr != nil {
				return scopeErr
			}
			availableQuota = int64(scoped)
		} else if value, ok := ctx.Get(AdtyOwnerSelfUseContextKey); ok {
			site, valid := value.(*model.AdtySite)
			if !valid || site == nil || site.Id <= 0 || site.OwnerUserId != relayInfo.UserId || site.Status != model.AdtySiteStatusActive {
				return model.ErrBillingOperationConflict
			}
			principal, pErr := model.AdtyOwnerPrincipalAvailable(model.DB, relayInfo.UserId, site.Id)
			if pErr != nil {
				return pErr
			}
			availableQuota = min(availableQuota, principal)
		} else {
			scoped, scopeErr := model.AdtyOwnerScopedWallet(model.DB, &model.BillingOperation{UserId: relayInfo.UserId}, userQuota)
			if scopeErr != nil {
				return scopeErr
			}
			availableQuota = int64(scoped)
		}
	}

	if availableQuota < int64(quota) {
		return fmt.Errorf("user quota is not enough, user quota: %s, need quota: %s", logger.FormatQuota(int(min(availableQuota, int64(math.MaxInt)))), logger.FormatQuota(quota))
	}

	tokenRemainQuota, _ := model.TokenQuotaForBilling(token)
	if !token.UnlimitedQuota && tokenRemainQuota < quota {
		return fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuota(tokenRemainQuota), logger.FormatQuota(quota))
	}

	shortfallBefore := relayInfo.BillingShortfall
	result, err := postConsumeQuotaWithResult(relayInfo, quota, 0, false, ctx)
	holdErr := adtyNoteBillingShortfall(ctx, relayInfo, shortfallBefore)
	if err != nil {
		return err
	}
	charged := quota - result.Shortfall
	if session, ok := relayInfo.Billing.(*BillingSession); ok && session.operationKey != "" {
		session.mu.Lock()
		relayInfo.RealtimeAlreadyCharged += max(0, charged)
		relayInfo.RealtimeTokenAlreadyCharged += result.TokenCharged
		session.realtimeShortfall += result.Shortfall
		session.mu.Unlock()
	} else if charged > 0 {
		relayInfo.RealtimeAlreadyCharged += charged
	}
	logger.LogInfo(ctx, common.LogText("realtime streaming consume quota success, quota: %d", quota))
	return holdErr
}

func PostWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelName string,
	usage *dto.RealtimeUsage, extraContent []*common.Message) error {
	if usage == nil {
		usage = &dto.RealtimeUsage{}
	}

	var tieredResult *billingexpr.TieredResult
	tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, buildRealtimeTieredParams(relayInfo, usage))
	if tieredOk {
		tieredResult = tieredRes
	}

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	var textInputTokens, audioInputTokens, textOutTokens, audioOutTokens int
	if usage != nil {
		textInputTokens, audioInputTokens, textOutTokens, audioOutTokens, _ = normalizeAudioUsageDetails(
			usage.InputTokens, usage.OutputTokens, usage.TotalTokens,
			usage.InputTokenDetails.TextTokens, usage.InputTokenDetails.AudioTokens,
			usage.OutputTokenDetails.TextTokens, usage.OutputTokenDetails.AudioTokens,
		)
	}

	tokenName := ctx.GetString("token_name")
	// 结算与预扣必须用同一个计价身份。modelName 是渠道映射后的上游名，用它查倍率会让同一次
	// 结算横跨两张价目表（ISS-R4-0096）；与 PostAudioConsumeQuota 一样统一取 billing 身份。
	billingModelName := relayInfo.GetBillingModelName()

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  billingModelName,
		UsePrice:   usePrice,
		ModelPrice: modelPrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}
	quotaInfo = quotaInfoFromPriceData(quotaInfo, relayInfo)
	if snap := AdtyFrozenPriceSnapshot(ctx); snap != nil {
		quotaInfo = applySnapshotToQuotaInfo(quotaInfo, snap)
	}
	completionRatio := quotaInfo.CompletionRatioDecimal
	if !quotaInfo.hasCompletionRatioDecimal {
		completionRatio = ratio_setting.GetCompletionRatioDecimal(billingModelName)
	}
	audioRatio := quotaInfo.AudioRatioDecimal
	if !quotaInfo.hasAudioRatioDecimal {
		audioRatio = ratio_setting.GetAudioRatioDecimal(billingModelName)
	}
	audioCompletionRatio := quotaInfo.AudioCompletionRatioDecimal
	if !quotaInfo.hasAudioCompletionRatioDecimal {
		audioCompletionRatio = ratio_setting.GetAudioCompletionRatioDecimal(billingModelName)
	}
	modelRatio = quotaInfo.ModelRatio
	groupRatio = quotaInfoGroupRatio(quotaInfo).InexactFloat64()
	modelPrice = quotaInfo.ModelPrice
	usePrice = quotaInfo.UsePrice

	quota, clamp := calculateAudioQuota(quotaInfo)
	noteQuotaClamp(relayInfo, clamp)
	if tieredOk {
		quota = tieredQuota
	}

	totalTokens := max(usage.TotalTokens, usage.InputTokens+usage.OutputTokens, textInputTokens+textOutTokens+audioInputTokens+audioOutTokens)
	fixedPriceBilling := tieredOk && isFixedPriceSettlement(relayInfo, tieredResult)
	usePriceBilling := usePrice && modelPrice > 0
	var logContent []*common.Message
	if !usePrice {
		logContent = append(logContent, common.NewMessage("Model ratio {{model_ratio}}, completion ratio {{completion_ratio}}, audio ratio {{audio_ratio}}, audio completion ratio {{audio_completion_ratio}}, group ratio {{group_ratio}}", map[string]any{
			"model_ratio":            fmt.Sprintf("%.2f", modelRatio),
			"completion_ratio":       fmt.Sprintf("%.2f", completionRatio.InexactFloat64()),
			"audio_ratio":            fmt.Sprintf("%.2f", audioRatio.InexactFloat64()),
			"audio_completion_ratio": fmt.Sprintf("%.2f", audioCompletionRatio.InexactFloat64()),
			"group_ratio":            fmt.Sprintf("%.2f", groupRatio),
		}))
	} else {
		logContent = append(logContent, common.NewMessage("Model price {{model_price}}, group ratio {{group_ratio}}", map[string]any{
			"model_price": fmt.Sprintf("%.2f", modelPrice),
			"group_ratio": fmt.Sprintf("%.2f", groupRatio),
		}))
	}

	// record all the consume log even if quota is 0
	if totalTokens == 0 && !fixedPriceBilling && !usePriceBilling {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent = append(logContent, common.NewMessage("Possibly an upstream timeout"))
		logger.LogError(ctx, common.LogText("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d",
			relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	}

	if err := SettleBilling(ctx, relayInfo, quota); err != nil {
		logger.LogError(ctx, common.LogText("error settling billing: %s", err.Error()))
		if unsettledBilling(relayInfo) {
			return err
		}
	}
	if totalTokens != 0 || fixedPriceBilling || usePriceBilling {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}

	logModel := relayInfo.OriginModelName
	if logModel == "" {
		logModel = modelName
	}
	logContent = append(logContent, extraContent...)
	other := GenerateWssOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
	return nil
}

func CalcOpenRouterCacheCreateTokens(usage dto.Usage, priceData types.PriceData) int {
	if priceData.CacheCreationRatio == 1 {
		return 0
	}
	// Model and completion ratios use the PriceData decimal authority when
	// present. Cache ratios still have no Get*Decimal authority, so they
	// convert from the float snapshot once at this boundary.
	modelRatio := priceData.BilledModelRatio()
	cacheCreationRatio := decimal.NewFromFloat(priceData.CacheCreationRatio)
	cacheRatio := decimal.NewFromFloat(priceData.CacheRatio)
	completionRatio := priceData.BilledCompletionRatio()
	quotaPrice := modelRatio.Div(common.QuotaPerUnitDecimal())
	promptCacheCreatePrice := quotaPrice.Mul(cacheCreationRatio)
	promptCacheReadPrice := quotaPrice.Mul(cacheRatio)
	completionPrice := quotaPrice.Mul(completionRatio)

	cost, ok := openRouterUsageCostDecimal(usage.Cost)
	if !ok {
		return -1
	}
	totalPromptTokens := decimal.NewFromInt(int64(usage.PromptTokens))
	completionTokens := decimal.NewFromInt(int64(usage.CompletionTokens))
	promptCacheReadTokens := decimal.NewFromInt(int64(usage.PromptTokensDetails.CachedTokens))

	denom := promptCacheCreatePrice.Sub(quotaPrice)
	if denom.IsZero() {
		return -1
	}
	value := cost.
		Sub(totalPromptTokens.Mul(quotaPrice)).
		Add(promptCacheReadTokens.Mul(quotaPrice.Sub(promptCacheReadPrice))).
		Sub(completionTokens.Mul(completionPrice)).
		Div(denom)
	quota, clamp := common.QuotaFromDecimalChecked(value)
	if clamp != nil {
		return -1
	}
	return quota
}

func openRouterUsageCostDecimal(cost any) (decimal.Decimal, bool) {
	v, ok := cost.(float64)
	if !ok {
		// Preserve the previous type assertion: a non-float64 cost is treated as 0.
		return decimal.Zero, true
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return decimal.Zero, false
	}
	return decimal.NewFromFloat(v), true
}

func PostAudioConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent []*common.Message) error {
	originUsage := usage
	billingUsage := effectiveBillingUsage(usage)
	if billingUsage == nil {
		est := max(relayInfo.GetEstimatePromptTokens(), 0)
		billingUsage = &dto.Usage{PromptTokens: est, TotalTokens: est}
	}
	if originUsage != nil {
		ObserveChannelAffinityUsageCacheByRelayFormat(ctx, billingUsage, relayInfo.GetFinalRequestRelayFormat())
	}

	var tieredUsedVars map[string]bool
	if snap := relayInfo.TieredBillingSnapshot; snap != nil {
		tieredUsedVars = billingexpr.UsedVarsByHash(snap.ExprString, snap.ExprHash)
	}
	var tieredResult *billingexpr.TieredResult
	tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, BuildTieredTokenParams(billingUsage, false, tieredUsedVars))
	if tieredOk {
		tieredResult = tieredRes
	}
	fixedPriceBilling := tieredOk && isFixedPriceSettlement(relayInfo, tieredRes)

	promptTokens := max(billingUsage.PromptTokens, 0)
	if promptTokens == 0 && billingUsage.InputTokens > 0 {
		promptTokens = billingUsage.InputTokens
	}
	completionTokens := max(billingUsage.CompletionTokens, 0)
	if completionTokens == 0 && billingUsage.OutputTokens > 0 {
		completionTokens = billingUsage.OutputTokens
	}
	rawTextIn := billingUsage.PromptTokensDetails.TextTokens
	rawAudioIn := billingUsage.PromptTokensDetails.AudioTokens
	if billingUsage.InputTokensDetails != nil {
		if rawTextIn == 0 {
			rawTextIn = billingUsage.InputTokensDetails.TextTokens
		}
		if rawAudioIn == 0 {
			rawAudioIn = billingUsage.InputTokensDetails.AudioTokens
		}
	}
	rawTextOut := billingUsage.CompletionTokenDetails.TextTokens
	rawAudioOut := billingUsage.CompletionTokenDetails.AudioTokens
	if billingUsage.OutputTokensDetails != nil {
		if rawTextOut == 0 {
			rawTextOut = billingUsage.OutputTokensDetails.TextTokens
		}
		if rawAudioOut == 0 {
			rawAudioOut = billingUsage.OutputTokensDetails.AudioTokens
		}
	}

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	textInputTokens, audioInputTokens, textOutTokens, audioOutTokens, totalTokens := normalizeAudioUsageDetails(
		promptTokens, completionTokens, billingUsage.TotalTokens,
		rawTextIn, rawAudioIn,
		rawTextOut, rawAudioOut,
	)

	tokenName := ctx.GetString("token_name")
	billingModelName := relayInfo.GetBillingModelName()

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  billingModelName,
		UsePrice:   usePrice,
		ModelPrice: modelPrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}
	quotaInfo = quotaInfoFromPriceData(quotaInfo, relayInfo)
	if snap := AdtyFrozenPriceSnapshot(ctx); snap != nil {
		quotaInfo = applySnapshotToQuotaInfo(quotaInfo, snap)
	}
	completionRatio := quotaInfo.CompletionRatioDecimal
	if !quotaInfo.hasCompletionRatioDecimal {
		completionRatio = ratio_setting.GetCompletionRatioDecimal(billingModelName)
	}
	audioRatio := quotaInfo.AudioRatioDecimal
	if !quotaInfo.hasAudioRatioDecimal {
		audioRatio = ratio_setting.GetAudioRatioDecimal(billingModelName)
	}
	audioCompletionRatio := quotaInfo.AudioCompletionRatioDecimal
	if !quotaInfo.hasAudioCompletionRatioDecimal {
		audioCompletionRatio = ratio_setting.GetAudioCompletionRatioDecimal(billingModelName)
	}
	modelRatio = quotaInfo.ModelRatio
	groupRatio = quotaInfoGroupRatio(quotaInfo).InexactFloat64()
	modelPrice = quotaInfo.ModelPrice
	usePrice = quotaInfo.UsePrice

	toolSummary := textQuotaSummary{
		ModelName:  billingModelName,
		GroupRatio: groupRatio,
	}
	toolSummary.ToolCallSurchargeQuota = calculateTextToolCallSurcharge(ctx, relayInfo, &toolSummary)

	var quota int
	if tieredOk {
		quota = composeTieredTextQuota(relayInfo, toolSummary, tieredQuota, tieredRes)
	} else {
		quotaDecimal := calculateAudioQuotaDecimal(quotaInfo, toolSummary.ToolCallSurchargeQuota)
		var clamp *common.QuotaClamp
		quota, clamp = common.QuotaFromDecimalChecked(quotaDecimal)
		noteQuotaClamp(relayInfo, clamp)
	}

	var logContent []*common.Message
	if !usePrice {
		logContent = append(logContent, common.NewMessage("Model ratio {{model_ratio}}, completion ratio {{completion_ratio}}, audio ratio {{audio_ratio}}, audio completion ratio {{audio_completion_ratio}}, group ratio {{group_ratio}}", map[string]any{
			"model_ratio":            fmt.Sprintf("%.2f", modelRatio),
			"completion_ratio":       fmt.Sprintf("%.2f", completionRatio.InexactFloat64()),
			"audio_ratio":            fmt.Sprintf("%.2f", audioRatio.InexactFloat64()),
			"audio_completion_ratio": fmt.Sprintf("%.2f", audioCompletionRatio.InexactFloat64()),
			"group_ratio":            fmt.Sprintf("%.2f", groupRatio),
		}))
	} else {
		logContent = append(logContent, common.NewMessage("Model price {{model_price}}, group ratio {{group_ratio}}", map[string]any{
			"model_price": fmt.Sprintf("%.2f", modelPrice),
			"group_ratio": fmt.Sprintf("%.2f", groupRatio),
		}))
	}
	for _, item := range toolSummary.ToolSurchargeItems {
		q := decimal.NewFromFloat(item.Price).
			Mul(decimal.NewFromInt(int64(item.Count))).
			Div(decimal.NewFromInt(1000)).
			Mul(textBilledGroupRatio(relayInfo, groupRatio)).
			Mul(common.QuotaPerUnitDecimal())
		logContent = append(logContent, common.NewMessage("{{name}} called {{count}} times, cost {{quota}}", map[string]any{
			"name":  item.Name,
			"count": item.Count,
			"quota": logger.FormatQuota(common.QuotaFromDecimal(q)),
		}))
	}

	// record all the consume log even if quota is 0
	usePriceBilling := relayInfo.PriceData.UsePrice && relayInfo.PriceData.BilledModelPrice().IsPositive()
	hasToolSurcharge := !toolSummary.ToolCallSurchargeQuota.IsZero()
	billable := totalTokens != 0 || fixedPriceBilling || usePriceBilling || hasToolSurcharge || (tieredOk && quota > 0)
	if !billable {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent = append(logContent, common.NewMessage("Possibly an upstream timeout"))
		logger.LogError(ctx, common.LogText("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d",
			relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, billingModelName, relayInfo.FinalPreConsumedQuota))
	}

	if err := SettleBilling(ctx, relayInfo, quota); err != nil {
		logger.LogError(ctx, common.LogText("error settling billing: %s", err.Error()))
		if unsettledBilling(relayInfo) {
			return err
		}
	}
	if billable {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}

	logModel := billingModelName
	logContent = append(logContent, extraContent...)
	logUsage := *billingUsage
	logUsage.PromptTokensDetails.TextTokens = textInputTokens
	logUsage.PromptTokensDetails.AudioTokens = audioInputTokens
	logUsage.CompletionTokenDetails.TextTokens = textOutTokens
	logUsage.CompletionTokenDetails.AudioTokens = audioOutTokens
	other := GenerateAudioOtherInfo(ctx, relayInfo, &logUsage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	appendUsageBillingPathForLog(other, common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens), originUsage)
	appendToolSurchargeLogInfo(other, toolSummary.ToolSurchargeItems)
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
	relayInfo.PerformanceOutputTokens = int64(completionTokens)
	return nil
}

func PreConsumeTokenQuota(relayInfo *relaycommon.RelayInfo, quota int) error {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if relayInfo.IsPlayground {
		return nil
	}
	// 原子预扣：检查与扣减在同一操作中完成，并发请求不可能同时通过检查后超扣。
	reserved, err := model.TryReserveTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota, relayInfo.TokenUnlimited)
	if err != nil {
		return err
	}
	if !reserved {
		remainQuota := 0
		if token, tokenErr := model.GetTokenByKey(relayInfo.TokenKey, false); tokenErr == nil && token != nil {
			remainQuota = token.RemainQuota
		}
		return fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuota(remainQuota), logger.FormatQuota(quota))
	}
	return nil
}

type postConsumeQuotaResult struct {
	FundingApplied bool
	TokenApplied   bool
	TokenCharged   int
	// Shortfall 二开（余额下限）：扣费最多扣到 0，没扣到的部分由平台承担。
	Shortfall int
}

func PostConsumeQuota(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool, contexts ...*gin.Context) error {
	_, err := postConsumeQuotaWithResult(relayInfo, quota, preConsumedQuota, sendEmail, contexts...)
	return err
}

func postConsumeViolationFeeQuota(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool, contexts ...*gin.Context) error {
	_, err := postConsumeDirectWithPrefix("violation", relayInfo, quota, preConsumedQuota, sendEmail, contexts...)
	return err
}

func postConsumeQuotaWithResult(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool, contexts ...*gin.Context) (result postConsumeQuotaResult, err error) {
	return postConsumeDirectWithPrefix("direct", relayInfo, quota, preConsumedQuota, sendEmail, contexts...)
}

func postConsumeDirectWithPrefix(prefix string, relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool, contexts ...*gin.Context) (result postConsumeQuotaResult, err error) {
	if relayInfo == nil {
		return result, model.ErrBillingOperationConflict
	}
	if relayInfo.RequestId == "" {
		relayInfo.RequestId = common.NewRequestId()
	}
	if prefix == "" {
		prefix = "direct"
	}
	key := fmt.Sprintf("%s:%s:%d", prefix, relayInfo.RequestId, relayInfo.BillingDirectSequence)
	spec := model.BillingOperation{OperationKey: key, UserId: relayInfo.UserId, TokenId: relayInfo.TokenId, TokenUnlimited: relayInfo.TokenUnlimited,
		EarningRootKey: "request:" + relayInfo.RequestId}
	if relayInfo.IsPlayground {
		spec.TokenId = 0
	}
	if len(contexts) > 0 && contexts[0] != nil {
		c := contexts[0]
		if k, d, ok := AdtySiteMarkupFromContext(c); ok {
			spec.SiteId, spec.SiteMarkup, spec.SiteCostFactor = c.GetInt(AdtySiteIdContextKey), k, d
		}
		applySnapshotToBillingOperation(&spec, AdtyFrozenPriceSnapshot(c))
		c.Set(adtyDurableBillingContextKey, true)
		c.Set(adtyBillingOperationContextKey, key)
	}
	if err = model.QueueDirectBillingDelta(spec, quota); err != nil {
		return result, err
	}
	// R4-020: Advance sequence immediately once the operation row commits so a
	// transient ApplyBillingSettlement error never collides with subsequent chunks.
	relayInfo.BillingDirectSequence++
	op, _, applyErr := model.ApplyBillingSettlement(key)
	if applyErr != nil {
		return result, applyErr
	}
	if len(contexts) > 0 && contexts[0] != nil {
		contexts[0].Set(usageBillingContextKey, op)
	}
	if op.PayerKind != "" {
		relayInfo.BillingPayerKind = op.PayerKind
	}
	result.FundingApplied, result.TokenApplied, result.Shortfall = true, spec.TokenId > 0, op.Shortfall
	result.TokenCharged = op.TokenAppliedQuota
	relayInfo.BillingShortfall += op.Shortfall
	projectBillingEarnings(key)

	if sendEmail && op.PayerKind != model.BillingPayerTeam && relayInfo.BillingPayerKind != model.BillingPayerTeam {
		if (quota + preConsumedQuota) != 0 {
			checkAndSendQuotaNotify(relayInfo, quota, preConsumedQuota)
		}
	}

	return result, nil
}

func checkAndSendQuotaNotify(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int) {
	if relayInfo == nil || relayInfo.BillingPayerKind == model.BillingPayerTeam {
		return
	}
	gopool.Go(func() {
		userSetting := relayInfo.UserSetting
		// UserQuota is the balance before this request; quota plus
		// preConsumedQuota is what the request finally cost.
		balanceAfter := relayInfo.UserQuota - (quota + preConsumedQuota)
		if !quotaReminderDue(userSetting, int64(relayInfo.UserQuota), int64(balanceAfter)) {
			return
		}
		lang := notifyDeliveryLanguage(userSetting)
		prompt := i18n.Translate(lang, i18n.MsgNotifyQuotaLowTitle)
		// 二开：充值链接指向用户所属站点（任务 11.3）
		content, values := quotaNotifyContent(lang, userSetting.NotifyType, prompt, logger.FormatQuota(balanceAfter), AdtySitePublicBaseURL(AdtySiteForUser(relayInfo.UserId)))
		// R3-058: Apply Root-customized quota_exceed template when configured.
		threshold := common.GetQuotaRemindThreshold()
		if userSetting.QuotaWarningThreshold != 0 {
			threshold = int(userSetting.QuotaWarningThreshold)
		}
		customTitle, customContent := ResolveAdtyNoticeTemplateForNotify(
			dto.NotifyTypeQuotaExceed, lang, prompt, "", []any{logger.FormatQuota(balanceAfter), logger.FormatQuota(threshold)},
		)
		if customContent != "" {
			// R4-048: Keep values intact so sendEmailNotify preserves the top-up CTA
			// button (values[2]) and SendWebhookNotify preserves structured data.Values.
			prompt, content = customTitle, customContent
		}
		err := NotifyUser(relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values))
		if err != nil {
			common.SysError(common.LogText("failed to send quota notify to user %d: %s", relayInfo.UserId, err.Error()))
		}
	})
}

// quotaReminderDue reports whether a charge that moved a balance (wallet or
// subscription) from before to after earns a low-balance reminder. The site
// and the user must both have reminders on, and the charge must cross the
// user's threshold, or the site default when the user set none. A balance
// already below the threshold earns nothing more until a top-up lifts it back
// above, so a user gets one reminder per drop.
func quotaReminderDue(userSetting dto.UserSetting, before int64, after int64) bool {
	if !common.GetQuotaRemindEnabled() {
		return false
	}
	if userSetting.QuotaWarningEnabled != nil && !*userSetting.QuotaWarningEnabled {
		return false
	}
	threshold := float64(common.GetQuotaRemindThreshold())
	if userSetting.QuotaWarningThreshold != 0 {
		threshold = userSetting.QuotaWarningThreshold
	}
	return float64(before) >= threshold && float64(after) < threshold
}

// quotaNotifyContent renders a low-quota notice in the user's saved language.
// Bark and Gotify get plain text; email and webhook get HTML with the top-up
// link. values stay in the notify payload for webhook receivers.
func quotaNotifyContent(lang string, notifyType string, prompt string, remaining string, baseURL string) (string, []any) {
	switch notifyType {
	case "", dto.NotifyTypeEmail:
		topUpLink := strings.TrimRight(baseURL, "/") + "/wallet"
		return i18n.Translate("en", i18n.MsgNotifyQuotaLowGotify, map[string]any{"Prompt": prompt, "Quota": remaining}), []any{prompt, remaining, topUpLink}
	case dto.NotifyTypeBark:
		// Bark推送使用简短文本，不支持HTML
		return i18n.Translate(lang, i18n.MsgNotifyQuotaLowBark, map[string]any{"Prompt": prompt, "Quota": remaining}), []any{prompt, remaining}
	case dto.NotifyTypeGotify:
		return i18n.Translate(lang, i18n.MsgNotifyQuotaLowGotify, map[string]any{"Prompt": prompt, "Quota": remaining}), []any{prompt, remaining}
	default:
		topUpLink := strings.TrimRight(baseURL, "/") + "/wallet"
		return i18n.Translate(lang, i18n.MsgNotifyQuotaLowEmail, map[string]any{"Prompt": prompt, "Quota": remaining, "Link": topUpLink}), []any{prompt, remaining, topUpLink, topUpLink}
	}
}
