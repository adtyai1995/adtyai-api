package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// 支付通道后台（任务 5.4）。查看和启停按权限；新增、编辑（含密钥）和删除只有超级管理员。

type adtyEpayChannelView struct {
	model.AdtyEpayChannel
	HasKey                bool              `json:"has_key"`
	HasMerchantPrivateKey bool              `json:"has_merchant_private_key"`
	HasPlatformPublicKey  bool              `json:"has_platform_public_key"`
	TodayCNY              map[string]string `json:"today_cny"`
	OrderCount            int64             `json:"order_count"`
	SceneNames            []string          `json:"scene_names,omitempty"`
}

// GetAdtyEpayChannels 列出通道，带今天每种方式已到账的人民币。商户密钥与商户私钥不返回。
func GetAdtyEpayChannels(c *gin.Context) {
	channels, err := model.ListAdtyEpayChannels()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	orderCounts, err := model.CountAdtyEpayOrdersByChannel()
	if err != nil {
		orderCounts = map[int]int64{}
	}
	sceneNamesByChannel := map[int][]string{}
	if sceneData, sceneErr := service.GetAdtyPaymentScenes(); sceneErr == nil && sceneData != nil {
		for _, scene := range sceneData.Scenes {
			label := strings.TrimSpace(scene.Name)
			if label == "" {
				label = scene.Key
			}
			for _, cid := range scene.ChannelIDs {
				sceneNamesByChannel[cid] = append(sceneNamesByChannel[cid], label)
			}
		}
	}
	loc := model.AdtyBusinessLocation(nil)
	dayStart := time.Now().In(loc)
	start := time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, 0, 0, loc).Unix()
	out := make([]adtyEpayChannelView, 0, len(channels))
	for _, channel := range channels {
		today := map[string]string{}
		for _, m := range channel.Methods {
			sum, err := model.SumAdtyEpayPaidToday(channel.Id, m.Method, start)
			if err == nil {
				today[m.Method] = sum.StringFixed(2)
			}
		}
		channel.SignType = channel.EffectiveSignType()
		out = append(out, adtyEpayChannelView{
			AdtyEpayChannel:       channel,
			HasKey:                channel.MerchantKey != "",
			HasMerchantPrivateKey: channel.MerchantPrivateKey != "",
			HasPlatformPublicKey:  channel.PlatformPublicKey != "",
			TodayCNY:              today,
			OrderCount:            orderCounts[channel.Id],
			SceneNames:            sceneNamesByChannel[channel.Id],
		})
	}
	common.ApiSuccess(c, out)
}

type adtyEpayChannelRequest struct {
	Name               string `json:"name"`
	GatewayURL         string `json:"gateway_url"`
	MerchantId         string `json:"merchant_id"`
	MerchantKey        string `json:"merchant_key"`
	SignType           string `json:"sign_type"`
	MerchantPrivateKey string `json:"merchant_private_key"`
	PlatformPublicKey  string `json:"platform_public_key"`
	Priority           int    `json:"priority"`
	Weight             int    `json:"weight"`
	OrderMode          string `json:"order_mode"`
	ProbeMode          string `json:"probe_mode"`
	UnpaidThreshold    int    `json:"unpaid_threshold"`
	Methods            []struct {
		Method        string                `json:"method"`
		DisplayName   string                `json:"display_name"`
		TypeCode      string                `json:"type_code"`
		MinCNY        common.BoundedDecimal `json:"min_cny"`
		MaxCNY        common.BoundedDecimal `json:"max_cny"`
		DailyLimitCNY common.BoundedDecimal `json:"daily_limit_cny"`
		Enabled       bool                  `json:"enabled"`
		ReturnType    string                `json:"return_type"`
	} `json:"methods"`
}

// adtyEpayDisplayNameMaxRunes 限制钱包按钮上的名字长度。
const adtyEpayDisplayNameMaxRunes = 32

func (req *adtyEpayChannelRequest) toChannel() (*model.AdtyEpayChannel, error) {
	req.GatewayURL = strings.TrimSpace(req.GatewayURL)
	if strings.TrimSpace(req.Name) == "" || req.GatewayURL == "" || strings.TrimSpace(req.MerchantId) == "" {
		return nil, common.NewMessage("Name, gateway address and merchant ID are required")
	}
	if !strings.HasPrefix(req.GatewayURL, "https://") && !strings.HasPrefix(req.GatewayURL, "http://") {
		return nil, common.NewMessage("The gateway address must start with http:// or https://")
	}
	signType := strings.ToUpper(strings.TrimSpace(req.SignType))
	if signType == "" {
		signType = model.AdtyEpaySignTypeMD5
	}
	if signType != model.AdtyEpaySignTypeMD5 && signType != model.AdtyEpaySignTypeRSA {
		return nil, common.NewMessage("Epay signature type must be MD5 or RSA")
	}
	merchantPrivateKey := strings.TrimSpace(req.MerchantPrivateKey)
	if merchantPrivateKey != "" {
		if _, err := service.ParseEpayRSAPrivateKey(merchantPrivateKey); err != nil {
			return nil, common.NewMessage("The Epay merchant private key is missing or invalid")
		}
	}
	platformPublicKey := strings.TrimSpace(req.PlatformPublicKey)
	if platformPublicKey != "" {
		if _, err := service.ParseEpayRSAPublicKey(platformPublicKey); err != nil {
			return nil, common.NewMessage("The Epay platform public key is missing or invalid")
		}
	}
	if req.OrderMode != model.AdtyEpayOrderModeSubmit {
		req.OrderMode = model.AdtyEpayOrderModeAPI
	}
	switch req.ProbeMode {
	case "query", "balance", "order":
	default:
		req.ProbeMode = "off"
	}
	channel := &model.AdtyEpayChannel{
		Name:               strings.TrimSpace(req.Name),
		GatewayURL:         req.GatewayURL,
		MerchantId:         strings.TrimSpace(req.MerchantId),
		MerchantKey:        strings.TrimSpace(req.MerchantKey),
		SignType:           signType,
		MerchantPrivateKey: merchantPrivateKey,
		PlatformPublicKey:  platformPublicKey,
		Priority:           req.Priority,
		Weight:             max(req.Weight, 1),
		OrderMode:          req.OrderMode,
		ProbeMode:          req.ProbeMode,
		UnpaidThreshold:    max(req.UnpaidThreshold, 0),
	}
	enabled := 0
	seenMethods := map[string]struct{}{}
	seenTypeCodes := map[string]struct{}{}
	for _, m := range req.Methods {
		if !service.IsAdtyEpayMethod(m.Method) {
			return nil, common.NewMessage("Unsupported payment method")
		}
		if _, dup := seenMethods[m.Method]; dup {
			return nil, common.NewMessage("Duplicate payment method")
		}
		seenMethods[m.Method] = struct{}{}
		typeCode := strings.TrimSpace(m.TypeCode)
		effectiveTypeCode := typeCode
		if effectiveTypeCode == "" {
			effectiveTypeCode = m.Method
		}
		if _, dup := seenTypeCodes[effectiveTypeCode]; dup {
			return nil, common.NewMessage("Duplicate payment method type code")
		}
		seenTypeCodes[effectiveTypeCode] = struct{}{}
		minCNY := m.MinCNY.Round(2)
		maxCNY := m.MaxCNY.Round(2)
		dailyLimitCNY := m.DailyLimitCNY.Round(2)
		if minCNY.IsNegative() || maxCNY.IsNegative() || dailyLimitCNY.IsNegative() || (maxCNY.IsPositive() && minCNY.GreaterThan(maxCNY)) {
			return nil, common.NewMessage("Invalid parameters")
		}
		if m.Enabled {
			enabled++
		}
		displayName := strings.TrimSpace(m.DisplayName)
		if utf8.RuneCountInString(displayName) > adtyEpayDisplayNameMaxRunes {
			return nil, common.NewMessage("The display name can be at most 32 characters")
		}
		channel.Methods = append(channel.Methods, model.AdtyEpayChannelMethod{
			Method: m.Method, DisplayName: displayName, TypeCode: typeCode,
			MinCNY: minCNY, MaxCNY: maxCNY, DailyLimitCNY: dailyLimitCNY,
			Enabled: m.Enabled, ReturnType: strings.TrimSpace(m.ReturnType),
		})
	}
	if enabled == 0 {
		return nil, common.NewMessage("Enable at least one payment method")
	}
	return channel, nil
}

func validateNewAdtyEpayChannelCredentials(channel *model.AdtyEpayChannel) error {
	if channel.IsRSA() {
		if channel.MerchantPrivateKey == "" {
			return common.NewMessage("The Epay merchant private key is missing or invalid")
		}
		if channel.PlatformPublicKey == "" {
			return common.NewMessage("The Epay platform public key is missing or invalid")
		}
	} else if channel.MerchantKey == "" {
		return common.NewMessage("The merchant key is required")
	}
	return nil
}

// CreateAdtyEpayChannel 新建通道（只有超级管理员）。新通道默认停用，测试通过后再启用。
func CreateAdtyEpayChannel(c *gin.Context) {
	var req adtyEpayChannelRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	channel, err := req.toChannel()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateNewAdtyEpayChannelCredentials(channel); err != nil {
		common.ApiError(c, err)
		return
	}
	channel.Status = model.AdtyEpayStatusDisabled
	channel.StatusReason = model.AdtyEpayReasonManual
	if err := model.SaveAdtyEpayChannel(channel, false); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.epay_channel.create", map[string]any{"id": channel.Id, "name": channel.Name, "sign_type": channel.SignType})
	common.ApiSuccess(c, gin.H{"id": channel.Id})
}

// UpdateAdtyEpayChannel 改通道（只有超级管理员）。密钥留空表示不改。
func UpdateAdtyEpayChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	existing, err := model.GetAdtyEpayChannel(id)
	if err != nil {
		writeAdtyEpayAdminError(c, err)
		return
	}
	var req adtyEpayChannelRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	channel, err := req.toChannel()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel.Id = id
	channel.Status = existing.Status
	channel.StatusReason = existing.StatusReason
	keepKey := channel.MerchantKey == ""
	keepPrivateKey := channel.MerchantPrivateKey == ""
	keepPublicKey := channel.PlatformPublicKey == ""
	if channel.IsRSA() {
		effectivePrivateKey := channel.MerchantPrivateKey
		if keepPrivateKey {
			effectivePrivateKey = existing.MerchantPrivateKey
		}
		if _, err := service.ParseEpayRSAPrivateKey(effectivePrivateKey); err != nil {
			common.ApiErrorT(c, "The Epay merchant private key is missing or invalid")
			return
		}
		effectivePublicKey := channel.PlatformPublicKey
		if keepPublicKey {
			effectivePublicKey = existing.PlatformPublicKey
		}
		if _, err := service.ParseEpayRSAPublicKey(effectivePublicKey); err != nil {
			common.ApiErrorT(c, "The Epay platform public key is missing or invalid")
			return
		}
	} else {
		effectiveMerchantKey := channel.MerchantKey
		if keepKey {
			effectiveMerchantKey = existing.MerchantKey
		}
		if effectiveMerchantKey == "" {
			common.ApiErrorT(c, "The merchant key is required")
			return
		}
	}
	if err := model.SaveAdtyEpayChannel(channel, keepKey); err != nil {
		writeAdtyEpayAdminError(c, err)
		return
	}
	keyChanged := !keepKey || !keepPrivateKey || (!keepPublicKey && channel.PlatformPublicKey != existing.PlatformPublicKey)
	recordManageAudit(c, "adty.epay_channel.update", map[string]any{"id": id, "name": channel.Name, "sign_type": channel.SignType, "key_changed": keyChanged})
	common.ApiSuccess(c, nil)
}

// DeleteAdtyEpayChannel 删除支付通道（只有超级管理员）。有历史订单时先快照通道名并安全更新关联支付场景。
func DeleteAdtyEpayChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	res, err := model.DeleteAdtyEpayChannelWithActor(id, c.GetInt("id"))
	if err != nil {
		writeAdtyEpayAdminError(c, err)
		return
	}
	recordManageAudit(c, "adty.epay_channel.delete", map[string]any{
		"id":              id,
		"name":            res.Name,
		"updated_scenes":  res.UpdatedScenes,
		"disabled_scenes": res.DisabledScenes,
	})
	common.ApiSuccess(c, res)
}

type adtyEpayChannelReplaceRequest struct {
	TargetChannelID int                     `json:"target_channel_id"`
	NewChannel      *adtyEpayChannelRequest `json:"new_channel"`
	DeleteOld       bool                    `json:"delete_old"`
	EnableTarget    bool                    `json:"enable_target"`
}

// ReplaceAdtyEpayChannel 用已有通道或新建通道替换旧通道（只有超级管理员），同步迁移支付场景绑定并停用或删除旧通道。
func ReplaceAdtyEpayChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var req adtyEpayChannelReplaceRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	opts := model.AdtyEpayChannelReplaceOptions{
		TargetChannelID: req.TargetChannelID,
		DeleteOld:       req.DeleteOld,
		EnableTarget:    req.EnableTarget,
		ActorID:         c.GetInt("id"),
	}
	if req.NewChannel != nil {
		if req.TargetChannelID > 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		channel, convErr := req.NewChannel.toChannel()
		if convErr != nil {
			common.ApiError(c, convErr)
			return
		}
		if credErr := validateNewAdtyEpayChannelCredentials(channel); credErr != nil {
			common.ApiError(c, credErr)
			return
		}
		opts.NewChannel = channel
	} else if req.TargetChannelID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	res, err := model.ReplaceAdtyEpayChannel(id, opts)
	if err != nil {
		writeAdtyEpayAdminError(c, err)
		return
	}
	recordManageAudit(c, "adty.epay_channel.replace", map[string]any{
		"id":             res.OldID,
		"old_name":       res.OldName,
		"new_id":         res.NewID,
		"new_name":       res.NewName,
		"deleted_old":    res.DeletedOld,
		"enable_target":  req.EnableTarget,
		"updated_scenes": res.UpdatedScenes,
	})
	common.ApiSuccess(c, res)
}

// RevokeAdtyEpayChannel permanently removes the historical credential. Old
// orders remain available for evidence-based manual reconciliation.
func RevokeAdtyEpayChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	event := prepareManageAuditChange(c, c.GetInt("id"), "adty.epay_channel.revoke", map[string]any{"id": id}, nil, nil,
		"Root confirmed permanent payment credential revocation", "")
	if err := model.RevokeAdtyEpayChannelWithAudit(id, event); err != nil {
		writeAdtyEpayAdminError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{event})
	common.ApiSuccess(c, nil)
}

// SetAdtyEpayChannelStatus 启用、停用或手动转维护（支付通道·启停）。
func SetAdtyEpayChannelStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	switch body.Status {
	case model.AdtyEpayStatusEnabled, model.AdtyEpayStatusDisabled, model.AdtyEpayStatusMaintenance:
	default:
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if body.Status == model.AdtyEpayStatusEnabled {
		channel, getErr := model.GetAdtyEpayChannel(id)
		if getErr != nil {
			writeAdtyEpayAdminError(c, getErr)
			return
		}
		if channel.CredentialDecryptErr {
			common.ApiErrorT(c, "Cannot enable this payment channel because its stored credentials cannot be decrypted. Re-enter the credentials first")
			return
		}
	}
	if err := model.SetAdtyEpayChannelStatus(id, body.Status, model.AdtyEpayReasonManual); err != nil {
		writeAdtyEpayAdminError(c, err)
		return
	}
	recordManageAudit(c, "adty.epay_channel.status", map[string]any{"id": id, "status": body.Status})
	common.ApiSuccess(c, nil)
}

type adtyEpayTestResult struct {
	Method     string `json:"method"`
	OK         bool   `json:"ok"`
	ReturnType string `json:"return_type"`
	Disabled   bool   `json:"disabled"`
	Message    string `json:"message"`
}

// TestAdtyEpayChannel 每种勾选的方式按最小金额下一笔测试单（订单号 TEST…，不写 top_ups，不计连续未付）。
// 通道回复没开通的方式，自动取消勾选。只对 API 下单的通道有意义。
func TestAdtyEpayChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	channel, err := model.GetAdtyEpayChannel(id)
	if err != nil {
		writeAdtyEpayAdminError(c, err)
		return
	}
	if channel.OrderMode != model.AdtyEpayOrderModeAPI {
		common.ApiErrorT(c, "Only channels that order through the API can be tested")
		return
	}
	callback := strings.TrimRight(service.GetCallbackAddress(), "/")
	results := make([]adtyEpayTestResult, 0, len(channel.Methods))
	for i := range channel.Methods {
		method := &channel.Methods[i]
		if !method.Enabled {
			continue
		}
		money := method.MinCNY
		if !money.IsPositive() {
			money = decimal.NewFromInt(1)
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		result, err := service.CreateAdtyEpayOrder(ctx, channel, method, service.AdtyEpayCreateRequest{
			TradeNo:   fmt.Sprintf("TEST%d%d", channel.Id, time.Now().UnixNano()%1_000_000_000),
			Name:      "Channel test",
			ClientIP:  c.ClientIP(),
			Device:    "pc",
			MoneyCNY:  money,
			NotifyURL: callback + "/api/user/epay/notify/" + strconv.Itoa(channel.Id),
			ReturnURL: callback + "/wallet",
		})
		cancel()
		item := adtyEpayTestResult{Method: method.Method}
		if err != nil {
			safeErr := common.SanitizeOutboundError(err, channel.MerchantKey, channel.MerchantPrivateKey)
			common.SysError(common.LogText("epay channel %d method %s test failed: %s", channel.Id, method.Method, safeErr.Error()))
			item.Message = safeErr.Error()
			if adtyEpayMethodNotOpen(safeErr) {
				_ = model.SetAdtyEpayMethodEnabled(method.Id, false)
				item.Disabled = true
			}
		} else {
			item.OK = true
			item.ReturnType = result.Mode
			_ = model.UpdateAdtyEpayMethodReturnType(method.Id, result.Mode)
		}
		results = append(results, item)
	}
	recordManageAudit(c, "adty.epay_channel.test", map[string]any{"id": id})
	common.ApiSuccess(c, results)
}

// adtyEpayMethodNotOpen 判断网关的报错是不是「这种方式没开通」。各家措辞不同，只匹配明确的未开通/不支持表述，避免误关方式。
func adtyEpayMethodNotOpen(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, word := range []string{
		"不支持该支付方式",
		"支付方式未开启",
		"支付方式未开通",
		"支付方式不存在",
		"通道未开启",
		"通道未开通",
		"未开通",
		"未开启",
		"不支持",
		"not open",
		"not enabled",
		"not supported",
		"unsupported payment",
	} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func writeAdtyEpayAdminError(c *gin.Context, err error) {
	var message *common.Message
	switch {
	case errors.As(err, &message):
		common.ApiError(c, err)
	case errors.Is(err, model.ErrAdtyEpayChannelNotFound):
		common.ApiErrorT(c, "Payment channel not found")
	default:
		common.ApiError(c, err)
	}
}

func init() {
	adtyEpayChannelNames = func() map[int]string {
		channels, err := model.ListAdtyEpayChannels()
		names := map[int]string{}
		if err != nil {
			return names
		}
		for _, channel := range channels {
			names[channel.Id] = channel.Name
		}
		return names
	}
}
