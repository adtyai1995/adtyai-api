package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// PostAdtyRootExpiringGift has been retired (R-44): expiring trial gifts are no
// longer issued; existing historical lots remain read-only/auditable.
func PostAdtyRootExpiringGift(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorT(c, "You do not have permission to perform this action.")
		return
	}
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Expiring trial gifts have been retired"))
}

// QueryAdtyTopUp may credit a verified payment, so its route requires the same
// explicit write permission as manual completion, plus a one-operation step-up
// proof. Provider errors may contain credential-bearing URLs and must not reach
// API responses or audit metadata.
func QueryAdtyTopUp(c *gin.Context) {
	tradeNo := c.Param("trade_no")
	if middleware.RequireSecurityProof(c, service.AdtyTopUpQueryOperation(tradeNo)) == nil {
		return
	}
	var topUp model.TopUp
	if err := model.DB.Where("trade_no = ?", tradeNo).First(&topUp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = model.ErrTopUpNotFound
		}
		writeAdtyFinanceError(c, err)
		return
	}
	ext, err := model.GetAdtyTopupOrder(tradeNo)
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	if ext == nil || !model.IsAdtyPaymentProvider(topUp.PaymentProvider) || topUp.PaymentProvider != ext.Provider || topUp.PaymentMethod != ext.Method || topUp.UserId != ext.UserId {
		writeAdtyFinanceError(c, model.ErrPaymentMethodMismatch)
		return
	}
	var targetUser model.User
	if err := model.DB.Select("id", "role").First(&targetUser, topUp.UserId).Error; err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	if !canManageTargetRole(c.GetInt("role"), targetUser.Role) {
		writeAdtyFinanceError(c, model.ErrUserQuotaPermission)
		return
	}
	if isAccessTokenRequest(c) && targetUser.Role >= common.RoleAdminUser {
		writeAdtyFinanceError(c, model.ErrAdminTargetSessionRequired)
		return
	}
	if _, ok := service.AdtyPaymentProviderByName(ext.Provider).(service.AdtyPaymentOrderQuerier); !ok {
		common.ApiErrorT(c, "This payment provider does not support order queries")
		return
	}
	switch topUp.Status {
	case common.TopUpStatusSuccess:
		// A repeated request observes the committed credit without querying or
		// applying another payment to an already completed order.
	case common.TopUpStatusPending, common.TopUpStatusExpired, common.TopUpStatusFailed, model.TopUpStatusCanceled:
		if err := model.UpdateAdtyTopupOrderFields(tradeNo, map[string]any{"last_query_at": common.GetTimestamp()}); err != nil {
			common.ApiError(c, err)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		err := service.RecoverAdtyHostedPaymentForActor(ctx, ext, c.GetInt("id"), c.GetInt("role"), isAccessTokenRequest(c))
		cancel()
		if errors.Is(err, model.ErrAdtyOwnerTopupReview) || errors.Is(err, model.ErrTopUpStatusInvalid) {
			common.ApiErrorT(c, "Payment verified and queued for review. No balance was credited.")
			return
		}
		if errors.Is(err, model.ErrUserQuotaPermission) || errors.Is(err, model.ErrAdminTargetSessionRequired) {
			writeAdtyFinanceError(c, err)
			return
		}
		if err != nil {
			common.ApiErrorT(c, "Payment query failed. Check the provider console or try again.")
			return
		}
	default:
		writeAdtyFinanceError(c, model.ErrTopUpStatusInvalid)
		return
	}
	if err := model.DB.Select("status").Where("id = ?", topUp.Id).First(&topUp).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, topUp.UserId, "adty.topup.query", map[string]any{
		"trade_no": tradeNo, "provider": ext.Provider, "status": topUp.Status,
	})
	common.ApiSuccess(c, gin.H{"status": topUp.Status})
}

type adtyPaymentOrphanReviewView struct {
	*model.PaymentOrphanReview
	OperatorUsername string `json:"operator_username,omitempty"`
}

type adtyPaymentOrphanEventView struct {
	*model.PaymentOrphanEvent
	ResolvedByUsername string                       `json:"resolved_by_username,omitempty"`
	Review             *adtyPaymentOrphanReviewView `json:"review,omitempty"`
}

// GetPaymentOrphans 分页列出对不上的支付回调（任务 3.5）。
func GetPaymentOrphans(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	events, total, err := model.ListPaymentOrphanEventsForRole(c.Query("status"), pageInfo, c.GetInt("role") < common.RoleRootUser)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ids := make([]int, 0, len(events)*2)
	for _, ev := range events {
		if ev == nil {
			continue
		}
		ids = append(ids, ev.ResolvedBy)
		if ev.Review != nil {
			ids = append(ids, ev.Review.OperatorID)
		}
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]adtyPaymentOrphanEventView, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		view := adtyPaymentOrphanEventView{
			PaymentOrphanEvent: ev,
			ResolvedByUsername: names[ev.ResolvedBy],
		}
		if ev.Review != nil {
			view.Review = &adtyPaymentOrphanReviewView{
				PaymentOrphanReview: ev.Review,
				OperatorUsername:    names[ev.Review.OperatorID],
			}
		}
		items = append(items, view)
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// adtyFactsFromOrphan 按支付商解析 orphan 里存的回调参数，字段映射和回调相同。
func adtyFactsFromOrphan(orphan *model.PaymentOrphanEvent, method string) (model.AdtyPaymentFacts, error) {
	payload := map[string]any{}
	if err := common.UnmarshalJsonStr(orphan.Payload, &payload); err != nil {
		return model.AdtyPaymentFacts{}, model.ErrPaymentOrphanPayloadInvalid
	}
	get := func(key string) string { return model.PaymentOrphanPayloadString(payload, key) }
	parse := func(key string) (decimal.Decimal, error) {
		value, err := common.ParseBoundedDecimalString(get(key), 12, 8)
		if err != nil {
			return decimal.Zero, model.ErrPaymentOrphanPayloadInvalid
		}
		return value, nil
	}
	facts := model.AdtyPaymentFacts{Provider: orphan.Provider, Method: method, Payload: orphan.Payload, Source: model.AdtyPaymentSourceCallback}
	switch orphan.Provider {
	case model.PaymentProviderAdtyBEpusdt:
		paid, err := parse("actual_amount")
		if err != nil {
			return facts, err
		}
		fiat, err := parse("amount")
		if err != nil {
			return facts, err
		}
		facts.PaidAmount, facts.FiatAmount, facts.PaidCurrency = paid, fiat, "USDT"
		facts.PayAddress = get("token")
		facts.GatewayTradeId = get("trade_id")
		facts.TxHash = get("block_transaction_id")
	case model.PaymentProviderAdtyEpay:
		paid, err := parse("money")
		if err != nil {
			return facts, err
		}
		facts.PaidAmount, facts.PaidCurrency = paid, "CNY"
		facts.GatewayTradeId = get("trade_no")
		facts.TxHash = get("trade_no")
		paidAt := service.AdtyEpayPaidTime(get("endtime"))
		facts.PaidAt = paidAt
		facts.PaidTimeVerified = paidAt > 0
	default:
		return facts, model.ErrPaymentOrphanNotCredit
	}
	return facts, nil
}

// CreditPaymentOrphan 按 orphan 里的回调参数补入账：金额照常核对，和标记已处理在同一个事务里，
// 加赠和返佣钩子都会执行。金额不符的不能在这里补，走手动补单。
func CreditPaymentOrphan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = common.DecodeJson(c.Request.Body, &body)
	if middleware.RequireSecurityProof(c, service.AdtyPaymentOrphanCreditOperation(id, body.Note)) == nil {
		return
	}
	var topUp *model.TopUp
	var ext *model.AdtyTopupOrder
	var credited int
	var alreadyDone bool
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		orphan, topUpLocked, extLocked, err := model.LockPaymentOrphanOrderTx(tx, id)
		if err != nil {
			return err
		}
		actorID := c.GetInt("id")
		actorRole := c.GetInt("role")
		if actorRole != common.RoleRootUser && (extLocked == nil || extLocked.SiteId != 0) {
			return model.ErrUserQuotaPermission
		}
		if (topUpLocked != nil && topUpLocked.UserId == actorID) || (extLocked != nil && extLocked.UserId == actorID) {
			return model.ErrUserQuotaPermission
		}
		canCredit, canErr := model.CanCreditPaymentOrphanEventTx(tx, orphan, topUpLocked, extLocked)
		if canErr != nil {
			return canErr
		}
		if !canCredit {
			return model.ErrPaymentOrphanNotCredit
		}
		var topUps []model.TopUp
		if err := tx.Select("payment_method").Where("trade_no = ?", orphan.ReferenceID).Limit(1).Find(&topUps).Error; err != nil {
			return err
		}
		if len(topUps) == 0 {
			return model.ErrTopUpNotFound
		}
		facts, err := adtyFactsFromOrphan(orphan, topUps[0].PaymentMethod)
		if err != nil {
			return err
		}
		facts.OperatorId = c.GetInt("id")
		facts.ActorRole = c.GetInt("role")
		facts.DenyAdminTarget = isAccessTokenRequest(c)
		facts.AllowReplaced = true
		alreadyDone, credited, topUp, ext, err = model.CompleteAdtyTopUpTx(tx, orphan.ReferenceID, facts)
		if err != nil {
			return err
		}
		return model.MarkPaymentOrphanCreditedTx(tx, orphan, c.GetInt("id"), body.Note)
	})
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	if !alreadyDone {
		model.FinishAdtyTopUpCredit(topUp, ext, credited, adtyCallerIP(c))
	}
	recordManageAudit(c, "adty.payment_orphan.credit", map[string]any{"id": id, "note": strings.TrimSpace(body.Note)})
	common.ApiSuccess(c, nil)
}

// ResolvePaymentOrphan records verified external facts and an audited disposition.
func ResolvePaymentOrphan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body model.PaymentOrphanReviewRequest
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if middleware.RequireSecurityProof(c, service.AdtyPaymentOrphanResolveOperation(
		id, body.Status, body.TradeNo, body.ExternalAmount, body.ExternalCurrency, body.Proof, body.Note, body.DismissalReason,
	)) == nil {
		return
	}
	review, err := model.ResolvePaymentOrphanReviewForActor(id, c.GetInt("id"), c.GetInt("role"), isAccessTokenRequest(c), body)
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	recordManageAudit(c, "adty.payment_orphan.resolve", map[string]any{"id": id, "review_id": review.ID, "status": review.Status})
	if review != nil {
		names := model.LookupUsernamesByIDs(model.DB, []int{review.OperatorID})
		common.ApiSuccess(c, adtyPaymentOrphanReviewView{
			PaymentOrphanReview: review,
			OperatorUsername:    names[review.OperatorID],
		})
		return
	}
	common.ApiSuccess(c, review)
}

// ManualCompleteAdtyTopUp 手动补单：管理员在支付商后台核实到账后操作，必须填凭证和备注。
// 跳过金额核对，按下单时的面额加赠入账，实付写管理员填的凭证。
func ManualCompleteAdtyTopUp(c *gin.Context) {
	var body struct {
		Proof string `json:"proof"`
		Note  string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	body.Proof = strings.TrimSpace(body.Proof)
	body.Note = strings.TrimSpace(body.Note)
	if body.Proof == "" || body.Note == "" {
		common.ApiErrorT(c, "Proof and note are required")
		return
	}
	tradeNo := c.Param("trade_no")
	if middleware.RequireSecurityProof(c, service.AdtyTopUpManualCompleteOperation(tradeNo, body.Proof, body.Note)) == nil {
		return
	}
	var topUps []model.TopUp
	if err := model.DB.Where("trade_no = ?", tradeNo).Limit(1).Find(&topUps).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if len(topUps) == 0 {
		writeAdtyFinanceError(c, model.ErrTopUpNotFound)
		return
	}
	facts := model.AdtyPaymentFacts{
		Provider:        topUps[0].PaymentProvider,
		Method:          topUps[0].PaymentMethod,
		TxHash:          body.Proof,
		Source:          model.AdtyPaymentSourceManual,
		Payload:         common.GetJsonString(map[string]any{"proof": body.Proof, "note": body.Note, "operator": c.GetInt("id")}),
		OperatorId:      c.GetInt("id"),
		ActorRole:       c.GetInt("role"),
		DenyAdminTarget: isAccessTokenRequest(c),
		AllowReplaced:   true,
	}
	var topUp *model.TopUp
	var ext *model.AdtyTopupOrder
	var credited int
	var alreadyDone bool
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		lockedOrphans, lockedExt, lockErr := model.LockOrderPaymentOrphansForCreditTx(tx, tradeNo, topUps[0].PaymentProvider, nil)
		if lockErr != nil {
			return lockErr
		}
		if c.GetInt("role") != common.RoleRootUser {
			var lockedTopUp model.TopUp
			if qErr := tx.Where("trade_no = ?", tradeNo).First(&lockedTopUp).Error; qErr != nil {
				if errors.Is(qErr, gorm.ErrRecordNotFound) {
					return model.ErrTopUpNotFound
				}
				return qErr
			}
			nowUnix := common.GetTimestamp()
			if lockedTopUp.Status == common.TopUpStatusExpired || model.AdtyTopupOrderIsExpired(lockedExt, nowUnix) {
				return model.ErrUserQuotaPermission
			}
			if lockedExt != nil && lockedExt.SiteId > 0 {
				if len(lockedOrphans) > 0 {
					return model.ErrUserQuotaPermission
				}
				hasOrphanTable, orphanTableErr := model.AdtyTableExists(tx, &model.PaymentOrphanEvent{})
				if orphanTableErr != nil {
					return orphanTableErr
				}
				if hasOrphanTable {
					var nonDismissedCount int64
					if cntErr := tx.Model(&model.PaymentOrphanEvent{}).
						Where("reference_id = ? AND status <> ?", tradeNo, model.PaymentOrphanStatusDismissed).
						Count(&nonDismissedCount).Error; cntErr != nil {
						return cntErr
					}
					if nonDismissedCount > 0 {
						return model.ErrUserQuotaPermission
					}
				}
			}
		}
		var err error
		alreadyDone, credited, topUp, ext, err = model.CompleteAdtyTopUpTx(tx, tradeNo, facts)
		if err != nil {
			return err
		}
		if alreadyDone {
			return nil
		}
		// 实付没有网关数据，记成应付，币种照下单时。
		if err := tx.Model(&model.AdtyTopupOrder{}).Where("id = ?", ext.Id).Updates(map[string]any{
			"paid_amount":   ext.PayAmount,
			"paid_currency": ext.PayCurrency,
		}).Error; err != nil {
			return err
		}
		if ext != nil && len(lockedOrphans) > 0 && (ext.SiteId == 0 || c.GetInt("role") == common.RoleRootUser) {
			orphanIDs := make([]int64, 0, len(lockedOrphans))
			for _, row := range lockedOrphans {
				orphanIDs = append(orphanIDs, row.ID)
			}
			now := common.GetTimestamp()
			if err := tx.Model(&model.PaymentOrphanEvent{}).
				Where("id IN ? AND status = ?", orphanIDs, model.PaymentOrphanStatusPendingReview).
				Updates(map[string]any{
					"status":          model.PaymentOrphanStatusCredited,
					"resolution":      model.PaymentOrphanStatusCredited,
					"resolution_note": body.Note,
					"resolved_by":     c.GetInt("id"),
					"resolved_at":     now,
					"update_time":     now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	if !alreadyDone {
		ext.PaidAmount, ext.PaidCurrency = ext.PayAmount, ext.PayCurrency
		model.FinishAdtyTopUpCredit(topUp, ext, credited, adtyCallerIP(c))
	}
	recordManageAuditFor(c, topUps[0].UserId, "adty.topup.manual_complete", map[string]any{
		"trade_no": tradeNo, "proof": body.Proof, "note": body.Note, "already_done": alreadyDone,
	})
	common.ApiSuccess(c, gin.H{"already_done": alreadyDone})
}

// RefundAdtyTopUp 退款：按当前余额扣回这笔订单还剩的额度，赠金不退，只退没花掉的本金；佣金按比例扣回。
// 返回扣回额度、退的本金额度和应退金额，真正退钱在线下做。
func redactAdtyManualRefundFactsForViewer(c *gin.Context, facts *model.AdtyRefundPageFacts) {
	if c == nil || facts == nil || c.GetInt("role") >= common.RoleRootUser {
		return
	}
	for i := range facts.ManualRefunds {
		if facts.ManualRefunds[i].SiteId != 0 {
			facts.ManualRefunds[i].ReconcileOrphanId = 0
		}
	}
}

func redactAdtyManualRefundResultForViewer(c *gin.Context, result *model.AdtyManualRefundResult) {
	if c == nil || result == nil || c.GetInt("role") >= common.RoleRootUser {
		return
	}
	if result.Refund != nil && result.Refund.SiteId != 0 {
		result.Refund.ReconcileOrphanId = 0
	}
	redactAdtyManualRefundFactsForViewer(c, result.Facts)
}

// authorizeAdtyTopUpReadTarget enforces target role-hierarchy and PAT
// admin-target restrictions on admin refund read endpoints, matching
// QueryAdtyTopUp and lockAdtyRefundOrderForActorTx.
func authorizeAdtyTopUpReadTarget(c *gin.Context, tradeNo string) bool {
	tradeNo = strings.TrimSpace(tradeNo)
	if tradeNo == "" {
		return true
	}
	var userID int
	if topUp, err := model.LookupTopUpByTradeNo(tradeNo); err == nil && topUp != nil {
		userID = topUp.UserId
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		writeAdtyFinanceError(c, err)
		return false
	}
	if userID <= 0 {
		ext, err := model.GetAdtyTopupOrder(tradeNo)
		if err != nil {
			writeAdtyFinanceError(c, err)
			return false
		}
		if ext != nil {
			userID = ext.UserId
		}
	}
	if userID <= 0 {
		return true
	}
	var users []model.User
	if err := model.DB.Unscoped().Select("id", "role").Where("id = ?", userID).Limit(1).Find(&users).Error; err != nil {
		writeAdtyFinanceError(c, err)
		return false
	}
	if len(users) == 0 {
		return true
	}
	targetUser := users[0]
	if !canManageTargetRole(c.GetInt("role"), targetUser.Role) {
		writeAdtyFinanceError(c, model.ErrUserQuotaPermission)
		return false
	}
	if isAccessTokenRequest(c) && targetUser.Role >= common.RoleAdminUser {
		writeAdtyFinanceError(c, model.ErrAdminTargetSessionRequired)
		return false
	}
	return true
}

func PreviewAdtyTopUpRefund(c *gin.Context) {
	tradeNo := c.Param("trade_no")
	if !authorizeAdtyTopUpReadTarget(c, tradeNo) {
		return
	}
	result, err := model.PreviewAdtyTopUpRefund(tradeNo)
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	var request decimal.Decimal
	hasRequest := false
	if raw := strings.TrimSpace(c.Query("refund_usd")); raw != "" {
		parsed, parseErr := common.ParseBoundedDecimalString(raw, 12, 8)
		if parseErr != nil || !parsed.IsPositive() {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		request = parsed
		hasRequest = true
	}
	var reconcileOrphanID int64
	if rawOrphan := strings.TrimSpace(c.Query("reconcile_orphan_id")); rawOrphan != "" {
		parsedID, parseErr := strconv.ParseInt(rawOrphan, 10, 64)
		if parseErr != nil || parsedID < 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		reconcileOrphanID = parsedID
	}
	if reconcileOrphanID > 0 && c.GetInt("role") < common.RoleRootUser && result.Facts != nil && result.Facts.SiteId != 0 {
		writeAdtyFinanceError(c, model.ErrUserQuotaPermission)
		return
	}
	if hasRequest || reconcileOrphanID > 0 {
		facts, factErr := model.LoadAdtyRefundPageFactsWithReconcile(tradeNo, request, reconcileOrphanID)
		if factErr != nil {
			writeAdtyFinanceError(c, factErr)
			return
		}
		result.Facts = facts
		if facts != nil && facts.Commission != nil {
			result.Commission = facts.Commission
			result.CommissionIssuedUSD = facts.Commission.IssuedUSD
			result.CommissionWithdrawnUSD = facts.Commission.WithdrawnUSD
			result.CommissionClawbackUSD = facts.Commission.ExpectedClawbackUSD
		}
	}
	redactAdtyManualRefundFactsForViewer(c, result.Facts)
	common.ApiSuccess(c, presentAdtyTopUpRefundResult(result))
}

func RefundAdtyTopUp(c *gin.Context) {
	var body struct {
		Reason            string `json:"reason"`
		IdempotencyKey    string `json:"idempotency_key"`
		RefundQuota       *int64 `json:"refund_quota"`
		RefundUSD         string `json:"refund_usd"`
		Proof             string `json:"proof"`
		ReconcileOrphanID int64  `json:"reconcile_orphan_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	tradeNo := c.Param("trade_no")
	refundUSD := strings.TrimSpace(body.RefundUSD)
	var refundQuota *int64
	if refundUSD != "" {
		if _, parseErr := common.ParseBoundedDecimalString(refundUSD, 12, 8); parseErr != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	} else if body.RefundQuota != nil {
		refundQuota = body.RefundQuota
	} else {
		common.ApiErrorT(c, "Enter the refund amount in USD")
		return
	}
	if body.ReconcileOrphanID < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if middleware.RequireSecurityProof(c, service.AdtyTopUpRefundOperation(tradeNo, body.Reason, refundUSD, refundQuota, body.ReconcileOrphanID)) == nil {
		return
	}
	var amount decimal.Decimal
	var parseErr error
	if refundUSD != "" {
		amount, parseErr = common.ParseBoundedDecimalString(refundUSD, 12, 8)
		if parseErr != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	} else {
		amount, parseErr = model.RefundQuotaToUSD(*refundQuota)
		if parseErr != nil {
			writeAdtyFinanceError(c, parseErr)
			return
		}
	}
	actor := model.AdtyRefundActor{
		OperatorID:      c.GetInt("id"),
		Role:            c.GetInt("role"),
		DenyAdminTarget: isAccessTokenRequest(c),
	}
	applied, applyErr := model.ApplyAdtyTopUpManualRefundForActor(tradeNo, body.Reason, actor, body.IdempotencyKey, amount, body.ReconcileOrphanID)
	if applyErr != nil {
		writeAdtyFinanceError(c, applyErr)
		return
	}
	auditParams := map[string]any{
		"trade_no": tradeNo, "refund_id": applied.Refund.RefundId,
		"reason":        strings.TrimSpace(body.Reason),
		"request_usd":   applied.Refund.RequestUSD.String(),
		"request_quota": applied.Refund.RequestQuota,
	}
	if body.ReconcileOrphanID > 0 {
		auditParams["reconcile_orphan_id"] = body.ReconcileOrphanID
	}
	recordManageAuditFor(c, applied.Refund.UserId, "adty.topup.refund.apply", auditParams)
	redactAdtyManualRefundResultForViewer(c, applied)
	common.ApiSuccess(c, presentAdtyManualRefundResult(applied))
}

func ListAdtyTopUpManualRefunds(c *gin.Context) {
	tradeNo := c.Param("trade_no")
	if !authorizeAdtyTopUpReadTarget(c, tradeNo) {
		return
	}
	result, err := model.ListAdtyTopUpManualRefunds(tradeNo)
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	redactAdtyManualRefundResultForViewer(c, result)
	common.ApiSuccess(c, presentAdtyManualRefundResult(result))
}

func GetAdtyTopUpManualRefund(c *gin.Context) {
	tradeNo := c.Param("trade_no")
	if !authorizeAdtyTopUpReadTarget(c, tradeNo) {
		return
	}
	result, err := model.GetAdtyTopUpManualRefund(tradeNo, c.Param("refund_id"))
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	redactAdtyManualRefundResultForViewer(c, result)
	common.ApiSuccess(c, presentAdtyManualRefundResult(result))
}

func writeManualRefundStep(c *gin.Context, step string, run func(tradeNo, refundID string, actor model.AdtyRefundActor, idemKey string) (*model.AdtyManualRefundResult, error)) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Note           string `json:"note"`
		Status         string `json:"status"`
		Currency       string `json:"currency"`
		Amount         string `json:"amount"`
		PaidAt         int64  `json:"paid_at"`
		Voucher        string `json:"voucher"`
		Proof          string `json:"proof"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil && !errors.Is(err, io.EOF) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	tradeNo := c.Param("trade_no")
	refundID := c.Param("refund_id")
	payout := model.AdtyManualRefundPayout{
		Status:   body.Status,
		Currency: body.Currency,
		PaidAt:   body.PaidAt,
		Voucher:  strings.TrimSpace(body.Voucher),
	}
	if payout.Voucher == "" {
		payout.Voucher = strings.TrimSpace(body.Proof)
	}
	if strings.TrimSpace(body.Amount) != "" {
		amount, err := common.ParseBoundedDecimalString(body.Amount, 12, 8)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		payout.Amount = amount
	}
	amount := ""
	if strings.TrimSpace(body.Amount) != "" {
		amount = payout.Amount.String()
	}
	op := service.AdtyTopUpManualRefundStepOperation(tradeNo, refundID, step, body.Note, service.AdtyManualRefundStepPayout{
		Status: payout.Status, Currency: payout.Currency, Amount: amount,
		Voucher: payout.Voucher, PaidAt: payout.PaidAt,
	})
	if middleware.RequireSecurityProof(c, op) == nil {
		return
	}
	actor := model.AdtyRefundActor{
		OperatorID:      c.GetInt("id"),
		Role:            c.GetInt("role"),
		DenyAdminTarget: isAccessTokenRequest(c),
	}
	var result *model.AdtyManualRefundResult
	var err error
	switch step {
	case model.AdtyManualRefundStepPayout:
		result, err = model.RecordAdtyTopUpManualRefundPayoutForActor(tradeNo, refundID, actor, body.IdempotencyKey, payout)
	case model.AdtyManualRefundStepApprove:
		result, err = model.ApproveAdtyTopUpManualRefundForActor(tradeNo, refundID, actor, body.IdempotencyKey, body.Note)
	case model.AdtyManualRefundStepTerminate:
		result, err = model.TerminateAdtyTopUpManualRefundForActor(tradeNo, refundID, actor, body.IdempotencyKey, body.Note)
	default:
		result, err = run(tradeNo, refundID, actor, body.IdempotencyKey)
	}
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	audit := map[string]any{"trade_no": tradeNo, "refund_id": refundID, "step": step}
	if result.Refund != nil {
		audit["status"] = result.Refund.Status
		audit["shortfall_quota"] = result.Refund.ShortfallQuota
		audit["payout_status"] = result.Refund.PayoutStatus
	}
	recordManageAuditFor(c, result.Refund.UserId, "adty.topup.refund."+step, audit)
	redactAdtyManualRefundResultForViewer(c, result)
	common.ApiSuccess(c, presentAdtyManualRefundResult(result))
}

func ApproveAdtyTopUpManualRefund(c *gin.Context) {
	writeManualRefundStep(c, model.AdtyManualRefundStepApprove, nil)
}

func ReclaimAdtyTopUpManualRefund(c *gin.Context) {
	writeManualRefundStep(c, model.AdtyManualRefundStepReclaim, model.ReclaimAdtyTopUpManualRefundForActor)
}

func PlanAdtyTopUpManualRefundBonus(c *gin.Context) {
	writeManualRefundStep(c, model.AdtyManualRefundStepBonus, model.PlanAdtyTopUpManualRefundBonusForActor)
}

func PlanAdtyTopUpManualRefundCommission(c *gin.Context) {
	writeManualRefundStep(c, model.AdtyManualRefundStepCommission, model.PlanAdtyTopUpManualRefundCommissionForActor)
}

func RecordAdtyTopUpManualRefundPayout(c *gin.Context) {
	writeManualRefundStep(c, model.AdtyManualRefundStepPayout, nil)
}

func CompleteAdtyTopUpManualRefund(c *gin.Context) {
	writeManualRefundStep(c, model.AdtyManualRefundStepComplete, model.CompleteAdtyTopUpManualRefundForActor)
}

func TerminateAdtyTopUpManualRefund(c *gin.Context) {
	writeManualRefundStep(c, model.AdtyManualRefundStepTerminate, nil)
}

func writeAdtyFinanceError(c *gin.Context, err error) {
	var message *common.Message
	switch {
	case errors.Is(err, model.ErrAdminTargetSessionRequired):
		denyAdtyAdminAction(c)
	case errors.Is(err, model.ErrUserQuotaPermission):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
	case errors.As(err, &message):
		common.ApiError(c, err)
	case errors.Is(err, model.ErrTopUpNotFound), errors.Is(err, model.ErrPaymentOrphanNotFound):
		common.ApiErrorT(c, "Order not found")
	case errors.Is(err, model.ErrPaymentOrphanNotCredit):
		common.ApiErrorT(c, "This event cannot be credited automatically. Check the payment and complete the order manually")
	case errors.Is(err, model.ErrPaymentOrphanPayloadInvalid):
		common.ApiErrorT(c, "The callback data of this event is incomplete")
	case errors.Is(err, model.ErrPaymentAmountMismatch):
		common.ApiErrorT(c, "The paid amount does not match the order")
	case errors.Is(err, model.ErrPaymentMethodMismatch):
		common.ApiErrorT(c, "The payment provider does not match the order")
	case errors.Is(err, model.ErrTopUpStatusInvalid):
		common.ApiErrorT(c, "The order status does not allow this operation")
	case errors.Is(err, model.ErrAdtyIdempotencyConflict):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This request was already used for another operation"))
	case errors.Is(err, model.ErrAdtyManualRefundTableMissing):
		common.ApiErrorT(c, "Manual refund records are unavailable")
	default:
		common.ApiError(c, err)
	}
}

type adtyGiftFulfillmentAdminItem struct {
	model.AdtyGiftFulfillment
	Username string `json:"username"`
}

// GetAdtyGiftFulfillments lists site gift fulfillment records for Root review (R2-039).
func GetAdtyGiftFulfillments(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	siteID := 0
	if raw := strings.TrimSpace(c.Query("site_id")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		siteID = v
	}
	rows, total, err := service.ListAdtyGiftFulfillments(c.Query("status"), siteID, pageInfo)
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]adtyGiftFulfillmentAdminItem, len(rows))
	for i, row := range rows {
		items[i] = adtyGiftFulfillmentAdminItem{
			AdtyGiftFulfillment: row,
			Username:            names[row.UserId],
		}
	}
	common.ApiSuccess(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      pageInfo.Page,
		"page_size": pageInfo.PageSize,
	})
}

// PostAdtyGiftFulfillmentReview resolves a pending_review gift fulfillment record (R2-039).
func PostAdtyGiftFulfillmentReview(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("You do not have permission to perform this action."))
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	normalizedAction := strings.ToLower(strings.TrimSpace(body.Action))
	trimmedNote := strings.TrimSpace(body.Note)
	if normalizedAction != "approve" && normalizedAction != "reject" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if normalizedAction == "reject" && trimmedNote == "" {
		common.ApiErrorT(c, "Please enter a review reason")
		return
	}
	operation := service.AdtyGiftFulfillmentReviewOperation(id, normalizedAction, trimmedNote)
	if _, err := service.BindVerificationOperation(operation); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if middleware.RequireSecurityProof(c, operation) == nil {
		return
	}
	row, fulfilled, eventID, err := service.ReviewAdtyGiftFulfillmentWithAudit(id, normalizedAction, trimmedNote, c.GetInt("id"))
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{
		"item":      row,
		"fulfilled": fulfilled,
	})
}
