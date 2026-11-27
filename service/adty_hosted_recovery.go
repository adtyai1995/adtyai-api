package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type AdtyPaymentOrderQuerier interface {
	Query(context.Context, *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error)
}

type adtyVerifiedPaymentClientCtxKey struct{}

func withAdtyVerifiedPaymentClient(ctx context.Context, client *http.Client) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, adtyVerifiedPaymentClientCtxKey{}, client)
}

func adtyVerifiedPaymentClientFromContext(ctx context.Context) *http.Client {
	if ctx == nil {
		return nil
	}
	client, _ := ctx.Value(adtyVerifiedPaymentClientCtxKey{}).(*http.Client)
	return client
}

var (
	adtyHostedRecoveryProviderByName = func(name string) any {
		return AdtyPaymentProviderByName(name)
	}
	recordAdtyHostedPaymentAttempt = func(tradeNo, provider, source, status string, operatorID int) error {
		if model.DB == nil {
			return nil
		}
		return model.RecordAdtyPaymentAttempt(tradeNo, provider, source, status, operatorID)
	}
)

func queryAdtyHostedPaymentFacts(ctx context.Context, provider AdtyPaymentOrderQuerier, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
	verifiedClient, err := NewAdtyEpayVerifiedHTTPClient(0)
	if err != nil {
		return nil, err
	}
	verifiedTransport, ok := verifiedClient.Transport.(*http.Transport)
	if !ok || verifiedTransport == nil || verifiedTransport.TLSClientConfig == nil || verifiedTransport.TLSClientConfig.InsecureSkipVerify {
		return nil, ErrAdtyUnverifiedPaymentTLS
	}
	defer verifiedTransport.CloseIdleConnections()

	return provider.Query(withAdtyVerifiedPaymentClient(ctx, verifiedClient), ext)
}

// RecoverAdtyHostedPayment shares the callback's transaction and immutable
// credit ledger. A successful retry also closes its pending query review.
func RecoverAdtyHostedPayment(ctx context.Context, ext *model.AdtyTopupOrder) error {
	return RecoverAdtyHostedPaymentAs(ctx, ext, 0)
}

// RecoverAdtyHostedPaymentAs records the authenticated administrator for manual
// queries; background recovery retains actor zero.
func RecoverAdtyHostedPaymentAs(ctx context.Context, ext *model.AdtyTopupOrder, operatorID int) error {
	return RecoverAdtyHostedPaymentForActor(ctx, ext, operatorID, 0, false)
}

// RecoverAdtyHostedPaymentForActor enforces target role hierarchy and PAT
// admin-target restrictions inside the credit transaction (R11-003).
func isPermanentAdtyTopUpCreditReject(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, model.ErrPaymentAmountMismatch) ||
		errors.Is(err, model.ErrPaymentMethodMismatch) ||
		errors.Is(err, model.ErrTopUpStatusInvalid) ||
		errors.Is(err, model.ErrAdtyDuplicatePayment) ||
		errors.Is(err, model.ErrAdtyOwnerTopupReview) ||
		errors.Is(err, model.ErrPaymentOrphanNotCredit) ||
		errors.Is(err, model.ErrTopUpQuotaLimitExceeded) ||
		errors.Is(err, model.ErrInvalidTopUpQuota) ||
		errors.Is(err, model.ErrTopUpNotFound) ||
		errors.Is(err, ErrAdtyTeamInvalid) ||
		errors.Is(err, gorm.ErrRecordNotFound)
}

func RecoverAdtyHostedPaymentForActor(ctx context.Context, ext *model.AdtyTopupOrder, operatorID, actorRole int, denyAdminTarget bool) error {
	if ext == nil {
		return model.ErrTopUpNotFound
	}
	provider, ok := adtyHostedRecoveryProviderByName(ext.Provider).(AdtyPaymentOrderQuerier)
	if !ok {
		return errors.New("payment provider does not support order queries")
	}
	facts, err := queryAdtyHostedPaymentFacts(ctx, provider, ext)
	if err != nil {
		if errors.Is(err, ErrAdtyPaymentConfirming) {
			if recordErr := recordAdtyHostedPaymentAttempt(ext.TradeNo, ext.Provider, model.AdtyPaymentSourceQuery, "confirming", operatorID); recordErr != nil {
				return recordErr
			}
			return ErrAdtyPaymentConfirming
		}
		outcome := "failed"
		if (errors.Is(err, model.ErrPaymentMethodMismatch) || errors.Is(err, model.ErrPaymentAmountMismatch)) && model.DB != nil {
			if reviewErr := model.RecordPaymentOrphanEvent(&model.PaymentOrphanEvent{
				Provider:    ext.Provider,
				EventID:     "hosted-query:" + ext.TradeNo,
				EventType:   "payment_query",
				ReferenceID: ext.TradeNo,
				SessionID:   strings.TrimSpace(ext.GatewayTradeId),
				Reason:      "Payment recovery query detected conflicting gateway facts",
				Error:       err.Error(),
			}); reviewErr != nil {
				return reviewErr
			}
			outcome = "rejected"
		}
		if recordErr := recordAdtyHostedPaymentAttempt(ext.TradeNo, ext.Provider, model.AdtyPaymentSourceQuery, outcome, operatorID); recordErr != nil {
			return recordErr
		}
		return err
	}
	if facts == nil {
		return recordAdtyHostedPaymentAttempt(ext.TradeNo, ext.Provider, model.AdtyPaymentSourceQuery, "unpaid", operatorID)
	}
	facts.Source = model.AdtyPaymentSourceQuery
	facts.OperatorId = operatorID
	facts.ActorRole = actorRole
	facts.DenyAdminTarget = denyAdminTarget
	var topUp *model.TopUp
	var frozen *model.AdtyTopupOrder
	var credited int
	var alreadyDone bool
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		lockedOrphans, _, lockErr := model.LockOrderPaymentOrphansForCreditTx(tx, ext.TradeNo, ext.Provider,
			[]string{"hosted-query:" + ext.TradeNo, "epay-query:" + ext.TradeNo, "team-query:" + ext.TradeNo})
		if lockErr != nil {
			return lockErr
		}
		var err error
		alreadyDone, credited, topUp, frozen, err = model.CompleteAdtyTopUpTx(tx, ext.TradeNo, *facts)
		if err != nil {
			return err
		}
		if len(lockedOrphans) == 0 {
			return nil
		}
		orphanIDs := make([]int64, 0, len(lockedOrphans))
		for _, row := range lockedOrphans {
			orphanIDs = append(orphanIDs, row.ID)
		}
		now := common.GetTimestamp()
		return tx.Model(&model.PaymentOrphanEvent{}).
			Where("id IN ? AND status = ?", orphanIDs, model.PaymentOrphanStatusPendingReview).
			Updates(map[string]any{
				"status": model.PaymentOrphanStatusCredited, "resolution": model.PaymentOrphanStatusCredited,
				"resolution_note": "Recovered by authenticated provider query", "resolved_at": now, "update_time": now,
			}).Error
	})
	if err != nil {
		// R5-23: Only queue a durable PaymentOrphanEvent when payment was actually
		// verified by the gateway and credit failed with a permanent business rejection,
		// carrying the serialized facts payload. Transient query/DB errors stay retriable.
		outcome := "failed"
		if isPermanentAdtyTopUpCreditReject(err) {
			payload, marshalErr := common.Marshal(facts)
			if marshalErr != nil {
				return marshalErr
			}
			sessionID := strings.TrimSpace(facts.GatewayTradeId)
			if sessionID == "" {
				sessionID = ext.GatewayTradeId
			}
			if reviewErr := model.RecordPaymentOrphanEvent(&model.PaymentOrphanEvent{
				Provider: ext.Provider, EventID: "hosted-query:" + ext.TradeNo, EventType: "payment_query", ReferenceID: ext.TradeNo, SessionID: sessionID,
				Reason: "Payment recovery requires reconciliation", Error: err.Error(), Payload: string(payload),
			}); reviewErr != nil {
				return reviewErr
			}
			outcome = "rejected"
		}
		if recordErr := recordAdtyHostedPaymentAttempt(ext.TradeNo, ext.Provider, model.AdtyPaymentSourceQuery, outcome, operatorID); recordErr != nil {
			return recordErr
		}
	}
	if err == nil && !alreadyDone {
		model.FinishAdtyTopUpCredit(topUp, frozen, credited, "")
	}
	return err
}

const (
	adtyHostedPendingScanLimit        = 20
	adtyHostedTerminalScanLimit       = 10
	adtyHostedTerminalRecentBackoff   = 600  // 10 min for terminal orders < 1h old
	adtyHostedTerminalExtendedBackoff = 1800 // 30 min for older terminal orders
)

// The scan covers providers that can be queried (BEpusdt), prioritizing live
// unexpired pending orders (every 60s) in a dedicated pass so terminal orders
// or already-queued orphans can never cause head-of-line starvation.
func recoverAdtyHostedPayments(now int64) {
	hasOrphanTable := model.DB.Migrator().HasTable(&model.PaymentOrphanEvent{})
	applyOrphanFilter := func(q *gorm.DB) *gorm.DB {
		if !hasOrphanTable {
			return q
		}
		return q.Where("NOT EXISTS (SELECT 1 FROM adty_payment_orphan_events o WHERE o.reference_id = a.trade_no AND o.provider = a.provider AND o.status <> ?)", model.PaymentOrphanStatusDismissed)
	}

	var pendingOrders []model.AdtyTopupOrder
	pendingQ := model.DB.Table("adty_topup_orders AS a").Select("a.*").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no").
		Where("a.provider IN ? AND a.gateway_trade_id <> '' AND a.pay_address <> '' AND a.pay_url <> '' AND a.created_at <= ? AND a.created_at >= ? AND a.last_query_at <= ? AND t.status = ? AND (a.expire_at = 0 OR a.expire_at > ?)",
			[]string{model.PaymentProviderAdtyBEpusdt},
			now-120, now-7*24*60*60, now-60, common.TopUpStatusPending, now)
	if err := applyOrphanFilter(pendingQ).Order("a.last_query_at, a.id").Limit(adtyHostedPendingScanLimit).Scan(&pendingOrders).Error; err != nil {
		common.SysError(common.LogText("hosted payment recovery pending scan failed: %s", err.Error()))
		return
	}

	var terminalOrders []model.AdtyTopupOrder
	terminalQ := model.DB.Table("adty_topup_orders AS a").Select("a.*").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no").
		Where(`a.provider IN ? AND a.gateway_trade_id <> '' AND a.pay_address <> '' AND a.pay_url <> '' AND a.created_at <= ? AND a.created_at >= ? AND
			((t.status = ? AND a.expire_at > 0 AND a.expire_at <= ?) OR t.status IN ?) AND
			(a.last_query_at = 0 OR (a.created_at >= ? AND a.last_query_at <= ?) OR a.last_query_at <= ?)`,
			[]string{model.PaymentProviderAdtyBEpusdt},
			now-120, now-7*24*60*60,
			common.TopUpStatusPending, now,
			[]string{common.TopUpStatusExpired, common.TopUpStatusFailed, model.TopUpStatusCanceled},
			now-3600, now-adtyHostedTerminalRecentBackoff, now-adtyHostedTerminalExtendedBackoff)
	if err := applyOrphanFilter(terminalQ).Order("a.last_query_at, a.id").Limit(adtyHostedTerminalScanLimit).Scan(&terminalOrders).Error; err != nil {
		common.SysError(common.LogText("hosted payment recovery terminal scan failed: %s", err.Error()))
	}

	runBatch := func(orders []model.AdtyTopupOrder, claimCutoff int64) {
		for _, ext := range orders {
			claim := model.DB.Model(&model.AdtyTopupOrder{}).Where("id = ? AND last_query_at <= ?", ext.Id, claimCutoff).Update("last_query_at", now)
			if claim.Error != nil || claim.RowsAffected != 1 {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			err := RecoverAdtyHostedPayment(ctx, &ext)
			cancel()
			if err != nil {
				if errors.Is(err, ErrAdtyPaymentConfirming) {
					common.SysLog(common.LogText("hosted payment recovery confirming on-chain trade_no=%s", ext.TradeNo))
				} else if isPermanentAdtyTopUpCreditReject(err) {
					common.SysLog(common.LogText("hosted payment recovery conflict queued for review trade_no=%s: %s", ext.TradeNo, err.Error()))
				} else {
					common.SysLog(common.LogText("hosted payment recovery transient error trade_no=%s: %s", ext.TradeNo, err.Error()))
				}
			}
		}
	}
	runBatch(pendingOrders, now-60)
	runBatch(terminalOrders, now-adtyHostedTerminalRecentBackoff)
}

func init() {
	RegisterAdtyMaintenanceHook("hosted-payment-recovery", recoverAdtyHostedPayments)
}
