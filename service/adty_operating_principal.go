package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	model.AdtyAfterPrincipalSpentTx = adtyDebitOperatingPrincipalTx
	model.AdtyAfterPrincipalRefundedTx = adtyRefundOperatingPrincipalTx
	model.AdtyAfterPrincipalReducedTx = adtyReduceOperatingPrincipalTx
	model.AdtyAfterPrincipalDeltaTx = adtyRecordPrincipalConsumeEventsTx
}

func adtyReduceOperatingPrincipalTx(tx *gorm.DB, siteID int, quota int64, refType, refID, idemKey string) error {
	if tx == nil || siteID <= 0 || quota <= 0 {
		return nil
	}
	return adtyMoveOperatingPrincipalTx(tx, siteID, -quota, "principal_adjust_debit", refType, refID, idemKey, false)
}

func adtyCreditOperatingPrincipalTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
	if tx == nil || ext == nil || ext.SiteId <= 0 || ext.TeamId > 0 {
		return nil
	}
	// R10-020: Never re-credit a closed subsite's drained operating wallet on late settlement.
	hasSiteTable, err := model.AdtyTableExists(tx, &model.AdtySite{})
	if err != nil {
		return err
	}
	if hasSiteTable {
		var status string
		if err := tx.Model(&model.AdtySite{}).Select("status").Where("id = ?", ext.SiteId).Limit(1).Scan(&status).Error; err != nil {
			return err
		}
		if status == model.AdtySiteStatusClosed {
			return nil
		}
	}
	return adtyMoveOperatingPrincipalTx(tx, ext.SiteId, ext.FaceQuota(), "principal_credit", "topup", ext.TradeNo, "operating:topup:"+ext.TradeNo, false)
}

func adtyBatchSiteIDTx(tx *gorm.DB, batch *model.AdtyFundBatch) (int, error) {
	if batch == nil {
		return 0, nil
	}
	// Explicit site_id (including 0 for main-site batches) is authoritative.
	if batch.SiteId != nil {
		if *batch.SiteId > 0 {
			return *batch.SiteId, nil
		}
		return 0, nil
	}
	if batch.Source == "" {
		return 0, nil
	}
	var orders []model.AdtyTopupOrder
	if err := tx.Select("site_id").Where("trade_no = ? AND site_id > 0", batch.Source).Limit(1).Find(&orders).Error; err != nil {
		return 0, err
	}
	if len(orders) > 0 && orders[0].SiteId > 0 {
		return orders[0].SiteId, nil
	}
	return 0, nil
}

// adtyOperatingPrincipalAllocBySiteTx attributes principal consumption for
// operationKey across actual consumed AdtyFundBatch rows (keyed by operation_id,
// never source — R1-009). Returns found=true whenever allocation rows exist
// (even if all principal belonged to main-site site_id=0 batches, so callers
// never erroneously fall back to InheritedFromSiteId).
func adtyOperatingPrincipalAllocBySiteTx(tx *gorm.DB, userID int, operationKey string, limitQuota int64) (bySite map[int]int64, found bool, err error) {
	if tx == nil || userID <= 0 || operationKey == "" {
		return nil, false, nil
	}
	hasAllocTable, err := model.AdtyTableExists(tx, &model.AdtyFundAllocation{})
	if err != nil {
		return nil, false, err
	}
	if !hasAllocTable {
		return nil, false, nil
	}
	// R3-005: Always attribute strictly by this operation's own allocation rows.
	// Every debit operation (including task:<id>:adjust:* and direct:*) writes its
	// own AdtyFundAllocation rows under operation_key; never inherit EarningRootKey.
	var allocs []model.AdtyFundAllocation
	if err := tx.Select("batch_id", "kind", "source", "held", "settled").
		Where("user_id = ? AND operation_id = ? AND kind = ? AND source <> ?", userID, operationKey, "principal", "legacy").
		Order("id ASC").Find(&allocs).Error; err != nil {
		return nil, false, err
	}
	if len(allocs) == 0 {
		return nil, false, nil
	}
	bySite = map[int]int64{}
	remaining := limitQuota
	for _, alloc := range allocs {
		if alloc.BatchId <= 0 {
			continue
		}
		share := alloc.Settled
		if share <= 0 {
			share = alloc.Held
		}
		if share <= 0 {
			continue
		}
		var batches []model.AdtyFundBatch
		if err := tx.Select("id", "site_id", "source").Where("id = ?", alloc.BatchId).Limit(1).Find(&batches).Error; err != nil {
			return nil, false, err
		}
		if len(batches) == 0 {
			continue
		}
		found = true
		siteID, err := adtyBatchSiteIDTx(tx, &batches[0])
		if err != nil {
			return nil, false, err
		}
		take := share
		if limitQuota > 0 {
			if remaining <= 0 {
				break
			}
			if take > remaining {
				take = remaining
			}
			remaining -= take
		}
		if siteID > 0 && take > 0 {
			bySite[siteID] += take
		}
	}
	return bySite, found, nil
}

// adtyResolveOperatingPrincipalSiteIDTx resolves a single fallback site ID when
// no AdtyFundAllocation rows exist for the operation. It never attributes a
// main-site batch to InheritedFromSiteId.
func adtyResolveOperatingPrincipalSiteIDTx(tx *gorm.DB, userID int, operationKey string) (int, error) {
	var rootKey string
	if operationKey != "" {
		var ops []model.BillingOperation
		if err := tx.Select("operation_key", "site_id", "earning_root_key").Where("operation_key = ?", operationKey).Limit(1).Find(&ops).Error; err != nil {
			return 0, err
		}
		if len(ops) > 0 {
			if ops[0].SiteId > 0 {
				return ops[0].SiteId, nil
			}
			rootKey = ops[0].EarningRootKey
		}
		if rootKey != "" && rootKey != operationKey {
			var rootOps []model.BillingOperation
			if err := tx.Select("operation_key", "site_id").Where("operation_key = ?", rootKey).Limit(1).Find(&rootOps).Error; err != nil {
				return 0, err
			}
			if len(rootOps) > 0 && rootOps[0].SiteId > 0 {
				return rootOps[0].SiteId, nil
			}
		}
		lookupKeys := []string{"operating:consume:" + operationKey}
		if rootKey != "" && rootKey != operationKey {
			lookupKeys = append(lookupKeys, "operating:consume:"+rootKey)
		}
		var ledgers []model.AdtySiteWalletLedger
		if err := tx.Select("site_id").Where("idempotency_key IN ? AND site_id > 0", lookupKeys).Order("id DESC").Limit(1).Find(&ledgers).Error; err != nil {
			return 0, err
		}
		if len(ledgers) > 0 && ledgers[0].SiteId > 0 {
			return ledgers[0].SiteId, nil
		}
		hasOpEventTable, err := model.AdtyTableExists(tx, &model.AdtyOperatingPrincipalEvent{})
		if err != nil {
			return 0, err
		}
		if hasOpEventTable {
			opKeys := []string{operationKey}
			if rootKey != "" && rootKey != operationKey {
				opKeys = append(opKeys, rootKey)
			}
			var opEvents []model.AdtyOperatingPrincipalEvent
			if err := tx.Select("site_id").Where("operation_key IN ? AND site_id > 0", opKeys).Order("id DESC").Limit(1).Find(&opEvents).Error; err != nil {
				return 0, err
			}
			if len(opEvents) > 0 && opEvents[0].SiteId > 0 {
				return opEvents[0].SiteId, nil
			}
		}
	}
	var user model.User
	if err := tx.Select("id", "site_id").First(&user, userID).Error; err != nil {
		return 0, err
	}
	if user.SiteId > 0 {
		return user.SiteId, nil
	}
	return 0, nil
}

func adtyRecordOperatingPrincipalEventTx(tx *gorm.DB, operationKey, direction string, siteID, userID int, quota int64) error {
	if tx == nil || siteID <= 0 || quota == 0 {
		return nil
	}
	opKey := strings.TrimSpace(operationKey)
	if opKey == "" {
		opKey = fmt.Sprintf("direct:%s:%s", direction, common.GetUUID())
	}
	event := model.AdtyOperatingPrincipalEvent{
		OperationKey: opKey,
		Direction:    direction,
		SiteId:       siteID,
		UserId:       userID,
		Quota:        quota,
		Settled:      false,
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "operation_key"}, {Name: "direction"}, {Name: "site_id"}},
		DoNothing: true,
	}).Create(&event).Error
}

func adtyDebitOperatingPrincipalTx(tx *gorm.DB, userID int, operationKey string, principalQuota int64) error {
	if tx == nil || userID <= 0 || principalQuota <= 0 {
		return nil
	}
	hasEventTable, err := model.AdtyTableExists(tx, &model.AdtyOperatingPrincipalEvent{})
	if err != nil {
		return err
	}
	bySite, found, err := adtyOperatingPrincipalAllocBySiteTx(tx, userID, operationKey, principalQuota)
	if err != nil {
		return err
	}
	if found {
		if len(bySite) == 0 {
			// All consumed principal came from main-site (site_id=0) batches.
			return nil
		}
		siteIDs := make([]int, 0, len(bySite))
		for sid := range bySite {
			siteIDs = append(siteIDs, sid)
		}
		sort.Ints(siteIDs)
		if hasEventTable {
			for _, sid := range siteIDs {
				q := bySite[sid]
				if sid <= 0 || q <= 0 {
					continue
				}
				if err := adtyRecordOperatingPrincipalEventTx(tx, operationKey, "spend", sid, userID, -q); err != nil {
					return err
				}
			}
			return nil
		}
		refType, err := adtyOperatingRefType(tx, operationKey, "consume")
		if err != nil {
			return err
		}
		for _, sid := range siteIDs {
			q := bySite[sid]
			if q <= 0 {
				continue
			}
			key := "operating:consume:" + operationKey
			if len(siteIDs) > 1 {
				key = fmt.Sprintf("operating:consume:%s:s%d", operationKey, sid)
			}
			if err := adtyMoveOperatingPrincipalTx(tx, sid, -q, "principal_consume", refType, operationKey, key, false); err != nil {
				return err
			}
		}
		return nil
	}
	siteID, err := adtyResolveOperatingPrincipalSiteIDTx(tx, userID, operationKey)
	if err != nil {
		return err
	}
	if siteID <= 0 {
		return nil
	}
	if hasEventTable {
		return adtyRecordOperatingPrincipalEventTx(tx, operationKey, "spend", siteID, userID, -principalQuota)
	}
	refType, err := adtyOperatingRefType(tx, operationKey, "consume")
	if err != nil {
		return err
	}
	key := "operating:consume:" + common.GetUUID()
	refID := key
	if operationKey != "" {
		// 一条 operation 只结算一次，所以「一条消费流水」正好对上一个 operation key。
		key = "operating:consume:" + operationKey
		refID = operationKey
	}
	// Insufficient cover leaves settlement pending for an authorized adjustment.
	return adtyMoveOperatingPrincipalTx(tx, siteID, -principalQuota, "principal_consume", refType, refID, key, false)
}

func adtyRefundOperatingPrincipalTx(tx *gorm.DB, userID int, operationKey string, bySite map[int]int64) error {
	if tx == nil || userID <= 0 || len(bySite) == 0 {
		return nil
	}
	siteIDs := make([]int, 0, len(bySite))
	for sid, q := range bySite {
		if sid > 0 && q > 0 {
			siteIDs = append(siteIDs, sid)
		}
	}
	if len(siteIDs) == 0 {
		return nil
	}
	sort.Ints(siteIDs)
	hasEventTable, err := model.AdtyTableExists(tx, &model.AdtyOperatingPrincipalEvent{})
	if err != nil {
		return err
	}
	if hasEventTable {
		for _, sid := range siteIDs {
			q := bySite[sid]
			if err := adtyRecordOperatingPrincipalEventTx(tx, operationKey, "refund", sid, userID, q); err != nil {
				return err
			}
		}
		return nil
	}
	refType, err := adtyOperatingRefType(tx, operationKey, "refund")
	if err != nil {
		return err
	}
	for _, sid := range siteIDs {
		q := bySite[sid]
		key := "operating:refund:" + common.GetUUID()
		refID := key
		if operationKey != "" {
			key = "operating:refund:" + operationKey
			if len(siteIDs) > 1 {
				key = fmt.Sprintf("operating:refund:%s:s%d", operationKey, sid)
			}
			refID = operationKey
		}
		if err := adtyMoveOperatingPrincipalTx(tx, sid, q, "principal_refund", refType, refID, key, false); err != nil {
			return err
		}
	}
	return nil
}

func adtyPrincipalSourceExcludedFromCommission(source string) bool {
	src := strings.TrimSpace(source)
	if src == "" || src == "legacy" {
		return true
	}
	for _, prefix := range []string{"VCH", "adjust:", "site-comm-cvt:", "site-comm-wd:", "referral-withdrawal-"} {
		if strings.HasPrefix(src, prefix) {
			return true
		}
	}
	return false
}

// adtyRecordPrincipalConsumeEventsTx records immutable consumption/refund deltas strictly for
// real paid personal top-up principal batches (excluding vouchers, adjustments, commission
// transfers, team top-ups, and owner business orders) so hourly referral settlement can credit
// commissions based on actual principal consumption (R-49).
func adtyRecordPrincipalConsumeEventsTx(tx *gorm.DB, userID int, operationKey string, deltas []model.AdtyPrincipalBatchDelta) error {
	if tx == nil || userID <= 0 || len(deltas) == 0 {
		return nil
	}
	hasEventTable, err := model.AdtyTableExists(tx, &model.AdtyPrincipalConsumeEvent{})
	if err != nil || !hasEventTable {
		return err
	}
	hasOrderTable, err := model.AdtyTableExists(tx, &model.AdtyTopupOrder{})
	if err != nil || !hasOrderTable {
		return err
	}
	candidateSet := make(map[string]struct{}, len(deltas))
	candidates := make([]string, 0, len(deltas))
	for _, d := range deltas {
		if d.Quota == 0 {
			continue
		}
		src := strings.TrimSpace(d.Source)
		if adtyPrincipalSourceExcludedFromCommission(src) {
			continue
		}
		if _, exists := candidateSet[src]; !exists {
			candidateSet[src] = struct{}{}
			candidates = append(candidates, src)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	var orders []model.AdtyTopupOrder
	if err := tx.Select("trade_no", "team_id", "provider", "business").
		Where("trade_no IN ?", candidates).
		Find(&orders).Error; err != nil {
		return err
	}
	qualifying := make(map[string]bool, len(orders))
	for i := range orders {
		ord := &orders[i]
		if ord.TeamId == 0 && ord.Business != model.AdtyBusinessTeamTopup &&
			ord.Provider != model.AdtyVoucherProvider && !ord.IsOwnerBusiness() {
			qualifying[ord.TradeNo] = true
		}
	}
	if len(qualifying) == 0 {
		return nil
	}
	type aggKey struct {
		siteID    int
		direction string
	}
	agg := make(map[aggKey]int64)
	for _, d := range deltas {
		if d.Quota == 0 {
			continue
		}
		src := strings.TrimSpace(d.Source)
		if !qualifying[src] {
			continue
		}
		siteID := d.SiteId
		if siteID < 0 {
			siteID = 0
		}
		dir := "spend"
		if d.Quota < 0 {
			dir = "refund"
		}
		agg[aggKey{siteID: siteID, direction: dir}] += d.Quota
	}
	if len(agg) == 0 {
		return nil
	}
	keys := make([]aggKey, 0, len(agg))
	for k := range agg {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].siteID != keys[j].siteID {
			return keys[i].siteID < keys[j].siteID
		}
		return keys[i].direction < keys[j].direction
	})
	opKey := strings.TrimSpace(operationKey)
	if opKey == "" {
		opKey = "direct:" + common.GetUUID()
	}
	perUnit := common.QuotaPerUnitDecimal()
	for _, k := range keys {
		q := agg[k]
		if q == 0 {
			continue
		}
		amountUSD := decimal.NewFromInt(q).Div(perUnit).Round(8)
		row := model.AdtyPrincipalConsumeEvent{
			OperationKey: opKey,
			Direction:    k.direction,
			SiteId:       k.siteID,
			UserId:       userID,
			Quota:        q,
			AmountUSD:    amountUSD,
			Settled:      false,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "operation_key"}, {Name: "direction"}, {Name: "site_id"}},
			DoNothing: true,
		}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func adtyOperatingRefType(tx *gorm.DB, key, fallback string) (string, error) {
	if key == "" {
		return fallback, nil
	}
	var op model.BillingOperation
	if err := tx.Select("payer_kind").Where("operation_key = ?", key).Limit(1).Find(&op).Error; err != nil {
		return "", err
	}
	if op.PayerKind == model.BillingPayerOwner {
		return model.BillingPayerOwner, nil
	}
	return fallback, nil
}

func adtyMoveOperatingPrincipalTx(tx *gorm.DB, siteID int, quota int64, changeType, refType, refID, key string, allowOverdraft bool) error {
	if siteID <= 0 || quota == 0 {
		return nil
	}
	usd := decimal.NewFromInt(quota).Div(common.QuotaPerUnitDecimal())
	if usd.IsZero() {
		return nil
	}
	_, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletOperating, usd, AdtySiteWalletChange{
		Type: changeType, RefType: refType, RefID: refID, IdempotencyKey: key, AllowOverdraft: allowOverdraft,
	})
	return err
}
