package service

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const ownerTransfer = "owner_self_use_transfer"

type AdtyOwnerPrincipalTransfer struct {
	TransferID     string          `json:"transfer_id"`
	CreatedAt      int64           `json:"created_at"`
	AmountUSD      decimal.Decimal `json:"amount_usd"`
	EarningsBefore decimal.Decimal `json:"earnings_before"`
	EarningsAfter  decimal.Decimal `json:"earnings_after"`
	PersonalBefore decimal.Decimal `json:"personal_before"`
	PersonalAfter  decimal.Decimal `json:"personal_after"`
}

// Join immutable ledgers by the transfer identity; never reconstruct past balances.
func ListAdtyOwnerPrincipalTransfers(siteID, ownerID, offset, limit int) ([]AdtyOwnerPrincipalTransfer, int64, error) {
	var site model.AdtySite
	if err := model.DB.First(&site, siteID).Error; err != nil {
		return nil, 0, err
	}
	if site.OwnerUserId != ownerID {
		return nil, 0, ErrAdtySiteNotOwner
	}
	if err := ReviewAdtyPooledSiteFinance(model.DB, siteID); err != nil {
		return nil, 0, err
	}
	q := model.DB.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND wallet_kind = ? AND type = ? AND operator_id = ?", siteID, model.AdtySiteWalletEarnings, ownerTransfer, ownerID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var ledgers []model.AdtySiteWalletLedger
	if err := q.Order("id DESC").Offset(max(0, offset)).Limit(min(max(1, limit), 100)).Find(&ledgers).Error; err != nil {
		return nil, 0, err
	}
	items := make([]AdtyOwnerPrincipalTransfer, 0, len(ledgers))
	for _, ledger := range ledgers {
		var credit model.AdtyBalanceLedger
		if err := model.DB.Where("user_id = ? AND type = ? AND ref_id = ?", ownerID, ownerTransfer, ledger.RefId).First(&credit).Error; err != nil {
			return nil, 0, err
		}
		items = append(items, AdtyOwnerPrincipalTransfer{TransferID: ledger.RefId, CreatedAt: ledger.CreatedAt, AmountUSD: ledger.Delta.Neg(), EarningsBefore: ledger.BalanceBefore, EarningsAfter: ledger.BalanceAfter, PersonalBefore: QuotaToUSD(credit.BalanceBefore), PersonalAfter: QuotaToUSD(credit.BalanceAfter)})
	}
	return items, total, nil
}

type AdtyOwnerSelfUseUsage struct {
	OperationKey     string             `json:"operation_key"`
	RequestID        string             `json:"request_id"`
	CreatedAt        int64              `json:"created_at"`
	ModelName        string             `json:"model_name"`
	BillingGroup     string             `json:"billing_group"`
	BillingUnit      string             `json:"billing_unit"`
	SupplyUnitPrice  string             `json:"supply_unit_price"`
	ChargedUSD       decimal.Decimal    `json:"charged_usd"`
	PromptTokens     int                `json:"prompt_tokens"`
	CompletionTokens int                `json:"completion_tokens"`
	UsageFacts       map[string]float64 `json:"usage_facts,omitempty"`
}

func ListAdtyOwnerSelfUseUsage(siteID, ownerID, offset, limit int) ([]AdtyOwnerSelfUseUsage, int64, error) {
	var site model.AdtySite
	if err := model.DB.First(&site, siteID).Error; err != nil {
		return nil, 0, err
	}
	if site.OwnerUserId != ownerID {
		return nil, 0, ErrAdtySiteNotOwner
	}
	q := model.DB.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND wallet_kind = ? AND ref_type = ?", siteID, model.AdtySiteWalletOperating, model.BillingPayerOwner).Where("ref_id IN (?)", model.DB.Model(&model.BillingOperation{}).Select("operation_key").Where("site_id = ? AND user_id = ? AND payer_kind = ?", siteID, ownerID, model.BillingPayerOwner))
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var ledgers []model.AdtySiteWalletLedger
	if err := q.Order("id DESC").Offset(max(0, offset)).Limit(min(max(1, limit), 100)).Find(&ledgers).Error; err != nil {
		return nil, 0, err
	}
	items := make([]AdtyOwnerSelfUseUsage, 0, len(ledgers))
	for _, ledger := range ledgers {
		op, err := model.GetBillingOperation(ledger.RefId)
		if err != nil {
			return nil, 0, err
		}
		if op.UserId != ownerID || op.SiteId != siteID || op.PayerKind != model.BillingPayerOwner {
			return nil, 0, model.ErrBillingOperationConflict
		}
		root := op
		if op.EarningRootKey != "" && op.EarningRootKey != op.OperationKey {
			if original, err := model.GetBillingOperation(op.EarningRootKey); err == nil {
				root = original
			}
		}
		item := AdtyOwnerSelfUseUsage{OperationKey: op.OperationKey, RequestID: strings.TrimPrefix(root.OperationKey, "request:"), CreatedAt: ledger.CreatedAt, ChargedUSD: ledger.Delta.Neg()}
		snapshot := model.DecodeRequestPriceSnapshot(op.PriceSnapshot)
		if snapshot == nil {
			snapshot = model.DecodeRequestPriceSnapshot(root.PriceSnapshot)
		}
		if snapshot != nil {
			item.ModelName, item.BillingGroup, item.BillingUnit, item.SupplyUnitPrice = snapshot.ModelName, snapshot.BillingGroup, snapshot.BillingUnit, snapshot.FinalPrice
			switch snapshot.BillingUnit {
			case model.BillingUnitPerToken:
				// Legacy token prices store quota per input token, not USD.
				price, ok := model.ParseSnapshotDecimal(snapshot.FinalPrice)
				item.SupplyUnitPrice = ""
				item.BillingUnit = "per_million_input_tokens"
				if ok {
					item.SupplyUnitPrice = price.Mul(decimal.NewFromInt(1000000)).Div(common.QuotaPerUnitDecimal()).String()
				}
			case model.BillingUnitTieredExpr:
				item.SupplyUnitPrice = ""
			}
		}
		var logs []model.Log
		if err := model.LOG_DB.Select("prompt_tokens", "completion_tokens", "other").Where("user_id = ? AND request_id = ? AND type = ?", ownerID, item.RequestID, model.LogTypeConsume).Order("id DESC").Limit(1).Find(&logs).Error; err != nil {
			return nil, 0, err
		}
		if len(logs) > 0 {
			item.PromptTokens, item.CompletionTokens = logs[0].PromptTokens, logs[0].CompletionTokens
			var usage struct {
				UsageFacts map[string]float64 `json:"usage_facts"`
			}
			if common.UnmarshalJsonStr(logs[0].Other, &usage) == nil {
				item.UsageFacts = usage.UsageFacts
			}
		}
		items = append(items, item)
	}
	return items, total, nil
}

func TransferAdtyEarningsToPersonal(siteID, ownerID int, usd decimal.Decimal, intent string) (string, error) {
	intent = strings.TrimSpace(intent)
	if !usd.IsPositive() || !validAdtySiteWalletAmount(usd) || len(intent) == 0 || len(intent) > 128 {
		return "", ErrAdtySiteWalletInvalidOperation
	}
	units := usd.Mul(common.QuotaPerUnitDecimal())
	if !units.IsInteger() {
		return "", ErrAdtySiteWalletInvalidOperation
	}
	quota, err := common.WalletQuotaFromDecimalStrict(units)
	if err != nil || quota <= 0 {
		return "", ErrAdtySiteWalletInvalidOperation
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", siteID, ownerID, intent)))
	ref := fmt.Sprintf("owner-%x", digest[:24])
	credited := false
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// Checkout and payment credit also lock user before site.
		var owner model.User
		if err := model.AdtyLockForUpdate(tx).First(&owner, ownerID).Error; err != nil {
			return err
		}
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
			return err
		}
		if site.OwnerUserId != ownerID || site.Status != model.AdtySiteStatusActive {
			return ErrAdtySiteNotOwner
		}
		key := ref
		prior, err := model.FindAdtyBalanceLedgerByKeyTx(tx, key)
		if err != nil {
			return err
		}
		if prior != nil {
			if prior.UserId != ownerID || prior.DeltaQuota != int64(quota) {
				return ErrAdtySiteWalletInvalidOperation
			}
			return nil
		}
		before, err := model.AdtyCreditUserQuotaTx(tx, ownerID, int64(quota))
		if err != nil {
			return err
		}
		if _, err = applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletEarnings, usd.Neg(), AdtySiteWalletChange{Type: ownerTransfer, RefType: ownerTransfer, RefID: ref, OperatorID: ownerID, IdempotencyKey: key}); err != nil {
			return err
		}
		if err = model.CreateAdtyFundBatchesWithSiteTx(tx, ownerID, model.AdtyFundBatchSiteID(siteID), ref, int64(quota), 0); err != nil {
			return err
		}
		if err = model.RecordAdtyBalanceLedgerTx(tx, &model.AdtyBalanceLedger{UserId: ownerID, Type: ownerTransfer, DeltaQuota: int64(quota), BalanceBefore: before, BalanceAfter: before + int64(quota), RefType: ownerTransfer, RefId: ref, IdempotencyKey: &key}); err != nil {
			return err
		}
		credited = true
		return adtyMoveOperatingPrincipalTx(tx, siteID, int64(quota), "principal_credit", ownerTransfer, ref, "operating:"+ref, false)
	})
	if err != nil {
		return "", err
	}
	if credited {
		model.AdtySyncCreditUserQuotaCache(ownerID, int64(quota), "owner transfer")
	}
	return ref, nil
}
