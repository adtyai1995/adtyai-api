package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 补查、连续未付、探活（任务 5.3）。都挂在充值维护任务上，只在主节点跑。

const adtyNotifyTypeEpayMaintenance = "adty_epay_maintenance"

// adtyNotifyRootAdmins 通知超级管理员，测试可以替换。走 notifyAdminsWithEventID，通知方式、语言、限频都按上游处理。
var adtyNotifyRootAdminsWithEventID = func(eventID string, render func(lang string) (string, string)) {
	notifyAdminsWithEventID(eventID, adtyNotifyTypeEpayMaintenance, func(_ int, role int, _ dto.UserSetting) bool {
		return role == common.RoleRootUser
	}, render)
}

var adtyNotifyRootAdmins = func(render func(lang string) (string, string)) {
	adtyNotifyRootAdminsWithEventID("", render)
}

// adtyEpayQuery 和 adtyEpayProbe 可以在测试里替换成假网关。
var (
	adtyEpayQuery = QueryAdtyEpayOrder
	adtyEpayProbe = ProbeAdtyEpayChannel
)

func adtyEpayChannelHTTPSQueryable(channel *model.AdtyEpayChannel) bool {
	if channel == nil || channel.Status == model.AdtyEpayStatusRevoked || strings.TrimSpace(channel.MerchantId) == "" || !channel.HasCredentials() {
		return false
	}
	endpoint, err := url.Parse(strings.TrimSpace(channel.GatewayURL))
	return err == nil && endpoint != nil && endpoint.Scheme == "https" && endpoint.Host != "" && endpoint.User == nil
}

const (
	adtyEpayPendingScanLimit        = 50
	adtyEpayTerminalScanLimit       = 20
	adtyEpayTerminalRecentBackoff   = 600  // 10 min for terminal orders < 1h old
	adtyEpayTerminalExtendedBackoff = 1800 // 30 min for older terminal orders
)

// runAdtyEpayFollowUpQueries 找下单超过 query_after_seconds 的易支付订单去查单。
// 未过期的 pending 订单独占优先通道每分钟补查；终态订单与超期团队订单按退避频率在次级批次补查，
// 并在 SQL 层排除已有非 dismissed 异常单与无有效网关的通道，避免队头饿死在途 pending 订单。
func runAdtyEpayFollowUpQueries(now int64) {
	after := int64(adty_setting.GetEpaySetting().QueryAfterSeconds)
	if after <= 0 {
		after = 120
	}
	grace := int64(adty_setting.GetEpaySetting().ExpiredQuerySeconds)
	if grace < 0 {
		grace = 0
	}
	canceledFailedWindow := max(grace, int64(3600))
	teamLookbackWindow := max(canceledFailedWindow, int64(7*24*60*60))
	hasOrphanTable := model.DB.Migrator().HasTable(&model.PaymentOrphanEvent{})
	hasChannelTable := model.DB.Migrator().HasTable(&model.AdtyEpayChannel{})
	applyCommonFilters := func(q *gorm.DB) *gorm.DB {
		if hasChannelTable {
			q = q.Joins("JOIN adty_epay_channels c ON c.id = a.channel_id AND TRIM(c.gateway_url) <> ''")
		}
		if hasOrphanTable {
			q = q.Where("NOT EXISTS (SELECT 1 FROM adty_payment_orphan_events o WHERE o.reference_id = a.trade_no AND o.provider = a.provider AND o.status <> ?)", model.PaymentOrphanStatusDismissed)
		}
		return q
	}

	var pendingExts []model.AdtyTopupOrder
	pendingQ := model.DB.Table("adty_topup_orders AS a").Select("a.*").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no").
		Where("a.provider = ? AND a.channel_id > 0 AND a.created_at <= ? AND a.last_query_at <= ? AND t.status = ? AND (a.expire_at = 0 OR a.expire_at > ?)",
			model.PaymentProviderAdtyEpay, now-after, now-60, common.TopUpStatusPending, now)
	if err := applyCommonFilters(pendingQ).Order("a.last_query_at, a.id").Limit(adtyEpayPendingScanLimit).Scan(&pendingExts).Error; err != nil {
		common.SysError(common.LogText("epay follow-up pending query scan: %s", err.Error()))
		return
	}

	var terminalExts []model.AdtyTopupOrder
	terminalQ := model.DB.Table("adty_topup_orders AS a").Select("a.*").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no").
		Where(`a.provider = ? AND a.channel_id > 0 AND a.created_at <= ? AND (
			(t.status = ? AND a.expire_at > 0 AND a.expire_at <= ? AND a.expire_at > ?) OR
			(t.status = ? AND a.expire_at <= ? AND a.expire_at > ?) OR
			(t.status IN ? AND a.created_at >= ?) OR
			(a.team_id > 0 AND a.created_at >= ? AND t.status IN ? AND (a.expire_at = 0 OR (a.expire_at <= ? AND a.last_query_at < a.expire_at)))
		) AND (
			a.last_query_at = 0 OR
			(a.team_id > 0 AND a.expire_at > 0 AND a.expire_at <= ? AND a.last_query_at < a.expire_at) OR
			(a.created_at >= ? AND a.last_query_at <= ?) OR
			a.last_query_at <= ?
		)`,
			model.PaymentProviderAdtyEpay, now-after,
			common.TopUpStatusPending, now, now-grace,
			common.TopUpStatusExpired, now, now-grace,
			[]string{model.TopUpStatusCanceled, common.TopUpStatusFailed}, now-canceledFailedWindow,
			now-teamLookbackWindow, []string{common.TopUpStatusPending, common.TopUpStatusExpired}, now,
			now,
			now-3600, now-adtyEpayTerminalRecentBackoff,
			now-adtyEpayTerminalExtendedBackoff)
	if err := applyCommonFilters(terminalQ).Order("a.last_query_at, a.id").Limit(adtyEpayTerminalScanLimit).Scan(&terminalExts).Error; err != nil {
		common.SysError(common.LogText("epay follow-up terminal query scan: %s", err.Error()))
	}

	exts := make([]model.AdtyTopupOrder, 0, len(pendingExts)+len(terminalExts))
	seen := make(map[string]struct{}, len(pendingExts)+len(terminalExts))
	for _, ext := range pendingExts {
		seen[ext.TradeNo] = struct{}{}
		exts = append(exts, ext)
	}
	for _, ext := range terminalExts {
		if _, dup := seen[ext.TradeNo]; dup {
			continue
		}
		exts = append(exts, ext)
	}
	for _, ext := range exts {
		if ext.ChannelId <= 0 {
			continue
		}
		// Atomically advance last_query_at up front so transient credit errors,
		// restricted fund writes, or orphan conflicts back off cleanly instead
		// of re-pinning the head of the scan queue on every tick (R13-P2-03).
		claimed := model.DB.Model(&model.AdtyTopupOrder{}).
			Where("id = ? AND last_query_at = ?", ext.Id, ext.LastQueryAt).
			Update("last_query_at", now)
		if claimed.Error != nil {
			common.SysError(common.LogText("epay follow-up claim failed trade_no=%s: %s", ext.TradeNo, claimed.Error.Error()))
			continue
		}
		if claimed.RowsAffected == 0 {
			continue
		}
		channel, chErr := model.GetAdtyEpayChannel(ext.ChannelId)
		if chErr != nil || channel == nil || strings.TrimSpace(channel.GatewayURL) == "" {
			continue
		}
		httpsQueryable := adtyEpayChannelHTTPSQueryable(channel)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		facts, err := (adtyEpayProvider{}).Query(ctx, &ext)
		cancel()
		legacy := ext.TeamId > 0 && ext.ExpireAt == 0
		deadline := ext.ExpireAt
		if legacy {
			deadline = AdtyEpayExpiry(ext.CreatedAt)
		}
		if errors.Is(err, errEpayOrderAbsent) {
			err = nil
		}
		finalTeamCheck := ext.TeamId > 0 && (legacy || now >= deadline)
		if err != nil && finalTeamCheck && httpsQueryable {
			if reviewErr := model.RecordPaymentOrphanEvent(&model.PaymentOrphanEvent{
				Provider: ext.Provider, EventID: "team-query:" + ext.TradeNo, EventType: "team_payment_query",
				ReferenceID: ext.TradeNo, SessionID: ext.GatewayTradeId,
				Reason: "Team payment could not be verified", Error: err.Error(),
			}); reviewErr != nil {
				continue
			}
		}
		if err != nil || facts == nil {
			outcome := "unpaid"
			if err != nil {
				outcome = "failed"
				if !finalTeamCheck && (errors.Is(err, model.ErrPaymentMethodMismatch) || errors.Is(err, model.ErrPaymentAmountMismatch)) {
					if reviewErr := adtyQueuePaymentReview(ext.TradeNo, ext.GatewayTradeId, err.Error(), ""); reviewErr != nil {
						continue
					}
					outcome = "rejected"
				}
			}
			if recordErr := model.RecordAdtyPaymentAttempt(ext.TradeNo, ext.Provider, model.AdtyPaymentSourceQuery, outcome, 0); recordErr != nil {
				common.SysError("payment attempt history could not be recorded")
				continue
			}
		} else {
			var topUp *model.TopUp
			var frozen *model.AdtyTopupOrder
			var credited int
			var alreadyDone bool
			creditErr := model.DB.Transaction(func(tx *gorm.DB) error {
				lockedOrphans, _, lockErr := model.LockOrderPaymentOrphansForCreditTx(tx, ext.TradeNo, ext.Provider,
					[]string{"hosted-query:" + ext.TradeNo, "epay-query:" + ext.TradeNo, "team-query:" + ext.TradeNo})
				if lockErr != nil {
					return lockErr
				}
				var txErr error
				alreadyDone, credited, topUp, frozen, txErr = model.CompleteAdtyTopUpTx(tx, ext.TradeNo, *facts)
				if txErr != nil {
					return txErr
				}
				if len(lockedOrphans) == 0 {
					return nil
				}
				orphanIDs := make([]int64, 0, len(lockedOrphans))
				for _, row := range lockedOrphans {
					orphanIDs = append(orphanIDs, row.ID)
				}
				nowTs := common.GetTimestamp()
				return tx.Model(&model.PaymentOrphanEvent{}).
					Where("id IN ? AND status = ?", orphanIDs, model.PaymentOrphanStatusPendingReview).
					Updates(map[string]any{
						"status":          model.PaymentOrphanStatusCredited,
						"resolution":      model.PaymentOrphanStatusCredited,
						"resolution_note": "Recovered by epay order query",
						"resolved_at":     nowTs,
						"update_time":     nowTs,
					}).Error
			})
			if creditErr != nil {
				common.SysLog(common.LogText("epay follow-up credit failed trade_no=%s: %s", ext.TradeNo, creditErr.Error()))
				if isPermanentAdtyTopUpCreditReject(creditErr) {
					encodedFacts, _ := common.Marshal(facts)
					if reviewErr := adtyQueuePaymentReview(ext.TradeNo, facts.GatewayTradeId, creditErr.Error(), string(encodedFacts)); reviewErr != nil {
						continue
					}
					if recordErr := model.RecordAdtyPaymentAttempt(ext.TradeNo, ext.Provider, model.AdtyPaymentSourceQuery, "rejected", 0); recordErr != nil {
						common.SysError("payment attempt history could not be recorded")
						continue
					}
				} else {
					continue
				}
			} else if !alreadyDone {
				model.FinishAdtyTopUpCredit(topUp, frozen, credited, "")
			}
		}
		if legacy {
			if err := model.UpdateAdtyTopupOrderFields(ext.TradeNo, map[string]any{"expire_at": deadline}); err != nil {
				common.SysError(common.LogText("epay follow-up checkpoint failed trade_no=%s: %s", ext.TradeNo, err.Error()))
			}
		}
	}
}

func adtyQueuePaymentReview(tradeNo, sessionID, cause, payload string) error {
	if err := model.RecordPaymentOrphanEvent(&model.PaymentOrphanEvent{
		Provider:    model.PaymentProviderAdtyEpay,
		EventID:     "hosted-query:" + tradeNo,
		EventType:   "payment_query",
		ReferenceID: tradeNo,
		SessionID:   sessionID,
		Reason:      "payment requires manual review after payment succeeded",
		Error:       cause,
		Payload:     payload,
	}); err != nil {
		common.SysError(common.LogText("record payment review failed trade_no=%s: %s", tradeNo, err.Error()))
		return err
	}
	return nil
}

// adtyEpayUnpaidThreshold 是通道的阈值；通道上为 0 时用全局值，全局也为 0 时不转维护。
func adtyEpayUnpaidThreshold(channel *model.AdtyEpayChannel) int {
	if channel.UnpaidThreshold > 0 {
		return channel.UnpaidThreshold
	}
	return adty_setting.GetEpaySetting().UnpaidThreshold
}

// countAdtyEpayUnpaid 把过期或取消的易支付订单计入通道的连续未付，每笔只计一次。达到阈值就转维护并通知。
func countAdtyEpayUnpaid(tradeNos []string) {
	if len(tradeNos) == 0 {
		return
	}
	var exts []model.AdtyTopupOrder
	// 过期之后又入账或已核实付款进入孤儿审核的订单不再算连续未付，避免 R-41 #1 迟付过期单误触健康通道维护。
	q := model.DB.Table("adty_topup_orders AS a").Select("a.*").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no").
		Where("a.trade_no IN ? AND a.provider = ? AND a.unpaid_counted = ? AND t.status NOT IN ?",
			tradeNos, model.PaymentProviderAdtyEpay, false, []string{common.TopUpStatusSuccess, common.TopUpStatusRefunded})
	if model.DB.Migrator().HasTable(&model.PaymentOrphanEvent{}) {
		q = q.Where("NOT EXISTS (SELECT 1 FROM adty_payment_orphan_events o WHERE o.provider = a.provider AND o.reference_id = a.trade_no AND o.event_type IN ('payment_callback','payment_query') AND o.event_id NOT LIKE 'reversal:%' AND o.status <> ?)",
			model.PaymentOrphanStatusDismissed)
	}
	if err := q.Find(&exts).Error; err != nil {
		common.SysError(common.LogText("count unpaid epay orders: %s", err.Error()))
		return
	}
	for _, ext := range exts {
		result := model.DB.Model(&model.AdtyTopupOrder{}).Where("id = ? AND unpaid_counted = ?", ext.Id, false).Update("unpaid_counted", true)
		if result.Error != nil || result.RowsAffected == 0 {
			continue
		}
		if err := model.DB.Model(&model.AdtyEpayChannel{}).Where("id = ?", ext.ChannelId).
			Update("unpaid_streak", model.DB.Raw("unpaid_streak + 1")).Error; err != nil {
			continue
		}
		channel, err := model.GetAdtyEpayChannel(ext.ChannelId)
		if err != nil {
			continue
		}
		threshold := adtyEpayUnpaidThreshold(channel)
		if threshold <= 0 || channel.UnpaidStreak < threshold || channel.Status != model.AdtyEpayStatusEnabled {
			continue
		}
		if err := model.SetAdtyEpayChannelStatus(channel.Id, model.AdtyEpayStatusMaintenance, model.AdtyEpayReasonUnpaid); err != nil {
			continue
		}
		model.RecordOperationAuditLog(0, common.RoleRootUser, "Payment channel "+channel.Name+" moved to maintenance after unpaid orders", "", "adty.epay_channel.maintenance",
			map[string]any{"id": channel.Id, "reason": model.AdtyEpayReasonUnpaid, "streak": channel.UnpaidStreak}, nil, nil)
		name := channel.Name
		streak := channel.UnpaidStreak
		eventID := fmt.Sprintf("epay_maintenance:%d:%d", channel.Id, time.Now().UnixNano())
		adtyNotifyRootAdminsWithEventID(eventID, func(lang string) (string, string) {
			args := map[string]any{"Name": name, "Count": streak}
			return i18n.Translate(lang, i18n.MsgAdtyEpayMaintenanceSubject, args), i18n.Translate(lang, i18n.MsgAdtyEpayMaintenanceBody, args)
		})
	}
}

// resetAdtyEpayUnpaid 在易支付订单入账后把通道的连续未付清零。
func resetAdtyEpayUnpaid(topUp *model.TopUp, ext *model.AdtyTopupOrder) {
	if ext == nil || ext.Provider != model.PaymentProviderAdtyEpay || ext.ChannelId == 0 {
		return
	}
	_ = model.DB.Model(&model.AdtyEpayChannel{}).Where("id = ?", ext.ChannelId).Update("unpaid_streak", 0).Error
}

// runAdtyEpayProbes 探活（默认关）：失败标维护（原因 probe）；原因是 probe 的通道连续 3 次正常就恢复。
// 手动设的维护不自动恢复。
func runAdtyEpayProbes(now int64) {
	setting := adty_setting.GetEpaySetting()
	if !setting.ProbeEnabled {
		return
	}
	interval := int64(max(setting.ProbeIntervalMinutes, 1)) * 60
	channels, err := model.ListAdtyEpayChannels()
	if err != nil {
		return
	}
	for i := range channels {
		channel := &channels[i]
		if channel.ProbeMode == "off" || channel.ProbeMode == "" || now-channel.LastProbeAt < interval {
			continue
		}
		probeable := channel.Status == model.AdtyEpayStatusEnabled ||
			(channel.Status == model.AdtyEpayStatusMaintenance && channel.StatusReason == model.AdtyEpayReasonProbe)
		if !probeable {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		err := adtyEpayProbe(ctx, channel)
		cancel()
		updates := map[string]any{"last_probe_at": now}
		if err != nil {
			updates["probe_fail_count"] = channel.ProbeFailCount + 1
			updates["probe_ok_count"] = 0
			_ = model.DB.Model(&model.AdtyEpayChannel{}).Where("id = ?", channel.Id).Updates(updates).Error
			if channel.Status == model.AdtyEpayStatusEnabled {
				_ = model.SetAdtyEpayChannelStatus(channel.Id, model.AdtyEpayStatusMaintenance, model.AdtyEpayReasonProbe)
			}
			continue
		}
		okCount := channel.ProbeOkCount + 1
		updates["probe_ok_count"] = okCount
		updates["probe_fail_count"] = 0
		_ = model.DB.Model(&model.AdtyEpayChannel{}).Where("id = ?", channel.Id).Updates(updates).Error
		if channel.Status == model.AdtyEpayStatusMaintenance && channel.StatusReason == model.AdtyEpayReasonProbe && okCount >= 3 {
			_ = model.SetAdtyEpayChannelStatus(channel.Id, model.AdtyEpayStatusEnabled, "")
		}
	}
}

// adtyEpayCanTake 说明有没有通道能接这种方式和这个金额（钱包页的可用性）。
func adtyEpayCanTake(method string, payCNY decimal.Decimal) bool {
	_, _, err := PickAdtyEpayChannel(method, payCNY, "pc", nil, time.Now().Unix())
	return err == nil
}

type adtyEpayProvider struct{}

func (adtyEpayProvider) Name() string { return AdtyProviderEpay }

func (adtyEpayProvider) Methods() []AdtyMethodInfo {
	channels, err := model.ListAdtyEpayChannels()
	available := map[string]bool{}
	if err == nil {
		for i := range channels {
			if channels[i].Status != model.AdtyEpayStatusEnabled || !channels[i].HasCredentials() {
				continue
			}
			for _, m := range channels[i].Methods {
				if m.Enabled {
					available[m.Method] = true
				}
			}
		}
	}
	out := make([]AdtyMethodInfo, 0, len(AdtyEpayMethods))
	for _, method := range AdtyEpayMethods {
		out = append(out, AdtyMethodInfo{Id: method, Kind: "epay", Currency: "CNY", Available: available[method]})
	}
	return out
}

func (adtyEpayProvider) AvailableFor(method string, quote *AdtyQuote) bool {
	return quote != nil && adtyEpayCanTake(method, quote.PayAmount)
}

// Cancel 取消易支付订单前，如果该通道具备 HTTPS 主动查单能力，先确认网关确实未付款（ISS-R1-0010）。
// 网关返回已支付时返回 ErrAdtyPaymentDetected；网关失败或不可达时返回错误以中止本地取消，防止网关已付款却被本地错置为取消。
func (adtyEpayProvider) Cancel(ext *model.AdtyTopupOrder) error {
	if ext == nil || ext.Provider != model.PaymentProviderAdtyEpay || ext.ChannelId <= 0 {
		return nil
	}
	channel, err := model.GetAdtyEpayChannel(ext.ChannelId)
	if err != nil {
		if errors.Is(err, model.ErrAdtyEpayChannelNotFound) {
			return nil
		}
		return err
	}
	if channel.Status == model.AdtyEpayStatusRevoked {
		return nil
	}
	endpoint, err := url.Parse(strings.TrimSpace(channel.GatewayURL))
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || channel.MerchantId == "" || !channel.HasCredentials() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	facts, err := (adtyEpayProvider{}).Query(ctx, ext)
	if err != nil {
		common.SysLog(common.LogText("epay cancel query failed trade_no=%s channel_id=%d: %s", ext.TradeNo, ext.ChannelId, err.Error()))
		return err
	}
	if facts != nil {
		return ErrAdtyPaymentDetected
	}
	return nil
}

func (adtyEpayProvider) Query(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
	if ext == nil || ext.Provider != model.PaymentProviderAdtyEpay {
		return nil, model.ErrPaymentMethodMismatch
	}
	channel, err := model.GetAdtyEpayChannel(ext.ChannelId)
	if err != nil {
		return nil, err
	}
	if channel.Status == model.AdtyEpayStatusRevoked {
		return nil, common.NewMessage("This payment channel has been permanently revoked")
	}
	endpoint, err := url.Parse(strings.TrimSpace(channel.GatewayURL))
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || channel.MerchantId == "" || !channel.HasCredentials() {
		return nil, errors.New("authenticated HTTPS Epay query is unavailable")
	}
	result, err := adtyEpayQuery(ctx, channel, ext.TradeNo)
	if err != nil {
		if errors.Is(err, errEpayOrderAbsent) {
			return nil, nil
		}
		return nil, err
	}
	if result == nil || result.OutTradeNo != ext.TradeNo {
		return nil, model.ErrPaymentMethodMismatch
	}
	// Enforce merchant ID match whenever the gateway includes pid in query responses;
	// many real Epay v1/v2 query endpoints omit pid on the response payload.
	if merchantID := strings.TrimSpace(result.MerchantId); merchantID != "" && merchantID != strings.TrimSpace(channel.MerchantId) {
		return nil, model.ErrPaymentMethodMismatch
	}
	if !result.Paid {
		return nil, nil
	}
	method := channel.MethodByTypeCode(result.TypeCode)
	if method == nil || method.Method != ext.Method || strings.TrimSpace(result.GatewayTradeNo) == "" || ext.GatewayTradeId != "" && ext.GatewayTradeId != result.GatewayTradeNo {
		return nil, model.ErrPaymentMethodMismatch
	}
	return &model.AdtyPaymentFacts{
		Provider: ext.Provider, Method: ext.Method, PaidAmount: result.MoneyCNY, PaidCurrency: "CNY",
		PaidAt: result.PaidAt, PaidTimeVerified: result.PaidAt > 0, GatewayTradeId: result.GatewayTradeNo, TxHash: result.GatewayTradeNo, Source: model.AdtyPaymentSourceQuery,
	}, nil
}

func init() {
	RegisterAdtyPaymentProvider(adtyEpayProvider{})
	RegisterAdtyMaintenanceHook("epay-follow-up-query", runAdtyEpayFollowUpQueries)
	RegisterAdtyMaintenanceHook("epay-probe", runAdtyEpayProbes)
	AdtyExpiredHook = func(expired []model.ExpiredAdtyTopUp) {
		tradeNos := make([]string, 0, len(expired))
		for _, item := range expired {
			if item.Provider == model.PaymentProviderAdtyEpay {
				tradeNos = append(tradeNos, item.TradeNo)
			}
		}
		countAdtyEpayUnpaid(tradeNos)
	}
	// Do NOT increment unpaid_streak on user-initiated cancellations (AdtyCanceledHook)
	// so a single user cannot push healthy channels into maintenance via repeated cancel DoS.
	model.AdtyAfterTopUpCompleted = func(topUp *model.TopUp, ext *model.AdtyTopupOrder) {
		resetAdtyEpayUnpaid(topUp, ext)
		if ext != nil && ext.IsOwnerMargin() && ext.SiteId > 0 {
			if _, err := ResumePendingAdtyGiftFulfillments(ext.SiteId); err != nil {
				common.SysLog(common.LogText("gift fulfillment: resume after online margin top-up site %d: %s", ext.SiteId, err.Error()))
			}
			if err := ResumeAdtySiteInviteCoverage(ext.SiteId, time.Now()); err != nil {
				common.SysLog(common.LogText("site invite coverage: resume after online margin top-up site %d: %s", ext.SiteId, err.Error()))
			}
		}
	}
}
