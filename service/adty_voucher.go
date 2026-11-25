package service

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	adtyVoucherTradeNoAttempts = 8
	adtyVoucherGuessMax        = 8
	adtyVoucherGuessWindow     = 15 * 60
)

const adtyVoucherGuessKeyPrefix = "adty:voucher:guess:"

// adtyVoucherGuessAllowScript counts timestamps in the sliding window
// without recording a new guess, so a later successful redeem is not charged.
const adtyVoucherGuessAllowScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local max = tonumber(ARGV[3])
local items = redis.call('LRANGE', key, 0, -1)
local kept = 0
for i = 1, #items do
  if now - tonumber(items[i]) < window then
    kept = kept + 1
  end
end
if kept < max then
  return 1
end
return 0
`

const adtyVoucherGuessNoteScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local items = redis.call('LRANGE', key, 0, -1)
redis.call('DEL', key)
for i = 1, #items do
  if now - tonumber(items[i]) < window then
    redis.call('RPUSH', key, items[i])
  end
end
redis.call('RPUSH', key, now)
redis.call('EXPIRE', key, window)
return 1
`

var adtyVoucherTradeNoTestHook func(hash string, attempt int) string

var (
	ErrAdtyVoucherInvalid        = common.NewMessage("Voucher code is invalid")
	ErrAdtyVoucherUsed           = common.NewMessage("Voucher code has already been used")
	ErrAdtyVoucherFrozen         = common.NewMessage("Voucher code is frozen")
	ErrAdtyVoucherCancelled      = common.NewMessage("Voucher code is cancelled")
	ErrAdtyVoucherWrongAccount   = common.NewMessage("This voucher cannot be used on this account")
	ErrAdtyVoucherLimit          = common.NewMessage("Issuance limit is not configured")
	ErrAdtyVoucherFunds          = common.NewMessage("Insufficient available funds")
	ErrAdtyVoucherRate           = common.NewMessage("Too many voucher attempts. Try again later")
	ErrAdtyVoucherIntentConflict = common.NewMessage("Voucher issuance intent parameters do not match")
)

type AdtyVoucherIssueInput struct {
	IssuerId          int
	Purpose           string
	SiteId            int
	DesignatedOwnerId int
	OwnerUserId       int
	AmountUSD         decimal.Decimal
	Source            string
	Reason            string
	IntentKey         string
	Count             int
}

type AdtyVoucherIssued struct {
	Id                int64           `json:"id"`
	Code              string          `json:"code"`
	CodeTail          string          `json:"code_tail"`
	Purpose           string          `json:"purpose"`
	Status            string          `json:"status,omitempty"`
	SourceKind        string          `json:"source_kind,omitempty"`
	FrozenEarningsUSD decimal.Decimal `json:"frozen_earnings_usd"`
	FrozenMarginUSD   decimal.Decimal `json:"frozen_margin_usd"`
	TargetSiteId      int             `json:"target_site_id,omitempty"`
	DesignatedOwnerId int             `json:"designated_owner_id,omitempty"`
	AmountUSD         decimal.Decimal `json:"amount_usd"`
	Quota             int64           `json:"quota"`
	IntentKey         string          `json:"intent_key,omitempty"`
	BatchNo           string          `json:"batch_no,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	CreatedAt         int64           `json:"created_at,omitempty"`
	Replayed          bool            `json:"replayed,omitempty"`
	AuditEventID      string          `json:"-"`
}

type AdtyVoucherBatchIssued struct {
	Id                int64               `json:"id"`
	Code              string              `json:"code"`
	CodeTail          string              `json:"code_tail"`
	Purpose           string              `json:"purpose"`
	Status            string              `json:"status,omitempty"`
	SourceKind        string              `json:"source_kind,omitempty"`
	FrozenEarningsUSD decimal.Decimal     `json:"frozen_earnings_usd"`
	FrozenMarginUSD   decimal.Decimal     `json:"frozen_margin_usd"`
	TargetSiteId      int                 `json:"target_site_id,omitempty"`
	DesignatedOwnerId int                 `json:"designated_owner_id,omitempty"`
	AmountUSD         decimal.Decimal     `json:"amount_usd"`
	Quota             int64               `json:"quota"`
	IntentKey         string              `json:"intent_key,omitempty"`
	BatchNo           string              `json:"batch_no,omitempty"`
	Reason            string              `json:"reason,omitempty"`
	CreatedAt         int64               `json:"created_at,omitempty"`
	Replayed          bool                `json:"replayed,omitempty"`
	Count             int                 `json:"count"`
	TotalAmountUSD    decimal.Decimal     `json:"total_amount_usd"`
	Items             []AdtyVoucherIssued `json:"items"`
	AuditEventID      string              `json:"-"`
}

type AdtyVoucherRedeemed struct {
	TradeNo   string          `json:"trade_no"`
	Purpose   string          `json:"purpose"`
	AmountUSD decimal.Decimal `json:"amount_usd"`
	Quota     int64           `json:"quota"`
	Replayed  bool            `json:"replayed"`
}

var voucherGuess struct {
	mu    sync.Mutex
	fails map[int][]int64
}

func ResetAdtyVoucherGuesses() {
	voucherGuess.mu.Lock()
	voucherGuess.fails = nil
	voucherGuess.mu.Unlock()
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx := context.Background()
	var cursor uint64
	pattern := adtyVoucherGuessKeyPrefix + "*"
	for {
		keys, next, err := common.RDB.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			_ = common.RDB.Del(ctx, keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func adtyVoucherGuessKey(userId int) string {
	return fmt.Sprintf("%s%d", adtyVoucherGuessKeyPrefix, userId)
}

var ErrAdtyVoucherForbidden = common.NewMessage("No permission to perform this action")

func IssueAdtyVoucher(input AdtyVoucherIssueInput) (*AdtyVoucherIssued, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	input.Purpose = strings.TrimSpace(input.Purpose)
	input.Source = strings.TrimSpace(input.Source)
	input.IntentKey = strings.TrimSpace(input.IntentKey)
	if input.DesignatedOwnerId < 0 || input.OwnerUserId < 0 || (input.DesignatedOwnerId > 0 && input.OwnerUserId > 0 && input.DesignatedOwnerId != input.OwnerUserId) {
		return nil, common.NewMessage("Invalid parameters")
	}
	if input.DesignatedOwnerId == 0 {
		input.DesignatedOwnerId = input.OwnerUserId
	}
	quota, err := adtyVoucherQuota(input.AmountUSD)
	if err != nil || input.IssuerId <= 0 || input.Reason == "" || len(input.Reason) > 200 || len(input.IntentKey) > 128 {
		return nil, common.NewMessage("Invalid parameters")
	}
	code, err := newAdtyVoucherCode()
	if err != nil {
		return nil, err
	}
	hash := model.AdtyVoucherCodeHash(code)
	tail := code[len(code)-4:]
	var issued *AdtyVoucherIssued
	var auditEvent *model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// Canonical lock order: AdtySite -> User(id ASC) -> AdtySiteWallet.
		var lockedSite *model.AdtySite
		if input.SiteId > 0 && (input.Purpose == model.AdtyVoucherPurposeSiteMargin || input.Purpose == model.AdtyVoucherPurposeSiteCustomer || input.Purpose == model.AdtyVoucherPurposeSiteOwner) {
			site, err := lockAdtyVoucherSite(tx, input.SiteId)
			if err != nil {
				return err
			}
			lockedSite = site
		}
		var issuer *model.User
		if input.Purpose == model.AdtyVoucherPurposeSiteMargin && lockedSite != nil && lockedSite.OwnerUserId > 0 && lockedSite.OwnerUserId < input.IssuerId {
			if _, err := lockAdtyVoucherUser(tx, lockedSite.OwnerUserId); err != nil {
				return err
			}
		}
		issuer, err = lockAdtyVoucherUser(tx, input.IssuerId)
		if err != nil {
			return err
		}
		if input.IntentKey != "" {
			replay, err := findAdtyVoucherIssueReplayTx(tx, issuer.Id, input, quota)
			if err != nil {
				return err
			}
			if replay != nil {
				issued = replay
				return nil
			}
		}
		var intentKeyPtr *string
		if input.IntentKey != "" {
			k := input.IntentKey
			intentKeyPtr = &k
		}
		row := model.AdtyVoucher{
			CodeHash: hash, CodeTail: tail, Purpose: input.Purpose, Status: model.AdtyVoucherStatusPending,
			AmountUSD: input.AmountUSD, Quota: quota, IssuerId: issuer.Id, Reason: input.Reason, IntentKey: intentKeyPtr,
		}
		switch input.Purpose {
		case model.AdtyVoucherPurposeMainUser:
			if issuer.Role != common.RoleRootUser {
				return ErrAdtyVoucherForbidden
			}
			if input.SiteId != 0 || input.DesignatedOwnerId != 0 || input.Source != "" {
				return common.NewMessage("Invalid parameters")
			}
			row.SourceKind = model.AdtyVoucherSourcePlatform
			if err := freezeAdtyVoucherLiabilityTx(tx, input.AmountUSD); err != nil {
				return err
			}
		case model.AdtyVoucherPurposeSiteMargin:
			if issuer.Role != common.RoleRootUser || input.SiteId <= 0 || lockedSite == nil {
				return common.NewMessage("Invalid parameters")
			}
			site := lockedSite
			if site.Status != model.AdtySiteStatusActive || site.OwnerUserId <= 0 {
				return common.NewMessage("Invalid parameters")
			}
			designatedOwnerID := input.DesignatedOwnerId
			if designatedOwnerID == 0 {
				designatedOwnerID = site.OwnerUserId
			} else if designatedOwnerID != site.OwnerUserId {
				return common.NewMessage("Invalid parameters")
			}
			if designatedOwnerID > input.IssuerId {
				if _, err := lockAdtyVoucherUser(tx, designatedOwnerID); err != nil {
					return err
				}
			}
			row.SourceKind = model.AdtyVoucherSourcePlatform
			row.TargetSiteId = site.Id
			row.DesignatedOwnerId = designatedOwnerID
			if err := freezeAdtyVoucherLiabilityTx(tx, input.AmountUSD); err != nil {
				return err
			}
		case model.AdtyVoucherPurposeSiteCustomer:
			if lockedSite == nil {
				return common.NewMessage("Invalid parameters")
			}
			site := lockedSite
			if site.OwnerUserId != issuer.Id || site.Status != model.AdtySiteStatusActive {
				return ErrAdtyVoucherForbidden
			}
			if input.DesignatedOwnerId != 0 && input.DesignatedOwnerId != site.OwnerUserId {
				return common.NewMessage("Invalid parameters")
			}
			switch input.Source {
			case "", "auto", model.AdtyVoucherSourceEarnings, model.AdtyVoucherSourceMargin:
			default:
				return common.NewMessage("Invalid parameters")
			}
			split, err := adtyFreezeOwnerAutoFundingTx(tx, site.Id, issuer.Id, input.AmountUSD, hash[:16], "voucher issuance freeze", "voucher-freeze:"+hash, "voucher-freeze:"+hash+":margin")
			if err != nil {
				if errors.Is(err, ErrAdtySiteWalletInsufficient) {
					return ErrAdtyVoucherFunds
				}
				return err
			}
			row.SourceKind = split.SourceKind()
			row.FrozenEarningsUSD = split.EarningsUSD
			row.FrozenMarginUSD = split.MarginUSD
			row.SourceSiteId = site.Id
			row.TargetSiteId = site.Id
		case model.AdtyVoucherPurposeSiteOwner:
			return ErrAdtyVoucherForbidden
		default:
			return common.NewMessage("Invalid parameters")
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		auditSiteID := row.TargetSiteId
		if auditSiteID == 0 {
			auditSiteID = input.SiteId
		}
		auditEvent, err = appendAdtyOfficialAuditTx(tx, input.IssuerId, auditSiteID, "voucher", strconv.FormatInt(row.Id, 10), "adty.voucher.issue", input.Reason, "",
			nil,
			map[string]any{
				"voucher_id": row.Id, "purpose": row.Purpose, "amount_usd": row.AmountUSD.String(),
				"quota": row.Quota, "status": model.AdtyVoucherStatusPending, "source": row.SourceKind,
				"frozen_earnings_usd": row.FrozenEarningsUSD.String(), "frozen_margin_usd": row.FrozenMarginUSD.String(),
				"site_id": auditSiteID, "target_site_id": row.TargetSiteId, "designated_owner_id": row.DesignatedOwnerId,
				"code_tail": tail, "intent_key": input.IntentKey,
			},
			model.AuditResultSuccess)
		if err != nil {
			return err
		}
		issued = &AdtyVoucherIssued{
			Id: row.Id, Code: code, CodeTail: tail, Purpose: row.Purpose, Status: row.Status,
			SourceKind: row.SourceKind, FrozenEarningsUSD: row.FrozenEarningsUSD, FrozenMarginUSD: row.FrozenMarginUSD,
			TargetSiteId: row.TargetSiteId, DesignatedOwnerId: row.DesignatedOwnerId,
			AmountUSD: row.AmountUSD, Quota: row.Quota, IntentKey: input.IntentKey,
			BatchNo: row.BatchNo, Reason: row.Reason, CreatedAt: row.CreatedAt,
			AuditEventID: auditEvent.EventId,
		}
		return nil
	})
	if err != nil && input.IntentKey != "" {
		if replay, lookErr := findAdtyVoucherIssueReplayTx(model.DB, input.IssuerId, input, quota); lookErr != nil {
			return nil, lookErr
		} else if replay != nil {
			return replay, nil
		}
	}
	if err == nil && issued != nil && !issued.Replayed && auditEvent != nil {
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return issued, err
}

func adtyVoucherBatchFromSingle(issued *AdtyVoucherIssued) *AdtyVoucherBatchIssued {
	if issued == nil {
		return nil
	}
	item := *issued
	return &AdtyVoucherBatchIssued{
		Id:                item.Id,
		Code:              item.Code,
		CodeTail:          item.CodeTail,
		Purpose:           item.Purpose,
		Status:            item.Status,
		SourceKind:        item.SourceKind,
		FrozenEarningsUSD: item.FrozenEarningsUSD,
		FrozenMarginUSD:   item.FrozenMarginUSD,
		TargetSiteId:      item.TargetSiteId,
		DesignatedOwnerId: item.DesignatedOwnerId,
		AmountUSD:         item.AmountUSD,
		Quota:             item.Quota,
		IntentKey:         item.IntentKey,
		BatchNo:           item.BatchNo,
		Reason:            item.Reason,
		CreatedAt:         item.CreatedAt,
		Replayed:          item.Replayed,
		Count:             1,
		TotalAmountUSD:    item.AmountUSD,
		Items:             []AdtyVoucherIssued{item},
		AuditEventID:      item.AuditEventID,
	}
}

func IssueAdtyVoucherBatch(input AdtyVoucherIssueInput) (*AdtyVoucherBatchIssued, error) {
	if input.Count < 0 {
		return nil, common.NewMessage("Invalid parameters")
	}
	if input.Count == 0 {
		input.Count = 1
	}
	if input.Count > 100 {
		return nil, common.NewMessage("Invalid parameters")
	}
	if input.Count == 1 {
		issued, err := IssueAdtyVoucher(input)
		if err != nil {
			return nil, err
		}
		return adtyVoucherBatchFromSingle(issued), nil
	}

	count := input.Count
	input.Reason = strings.TrimSpace(input.Reason)
	input.Purpose = strings.TrimSpace(input.Purpose)
	input.Source = strings.TrimSpace(input.Source)
	input.IntentKey = strings.TrimSpace(input.IntentKey)
	if input.DesignatedOwnerId < 0 || input.OwnerUserId < 0 || (input.DesignatedOwnerId > 0 && input.OwnerUserId > 0 && input.DesignatedOwnerId != input.OwnerUserId) {
		return nil, common.NewMessage("Invalid parameters")
	}
	if input.DesignatedOwnerId == 0 {
		input.DesignatedOwnerId = input.OwnerUserId
	}
	quota, err := adtyVoucherQuota(input.AmountUSD)
	if err != nil || input.IssuerId <= 0 || input.Reason == "" || len(input.Reason) > 200 || len(input.IntentKey) > 128 {
		return nil, common.NewMessage("Invalid parameters")
	}

	batchNo, err := newAdtyVoucherBatchNo()
	if err != nil {
		return nil, err
	}
	codes := make([]string, count)
	hashes := make([]string, count)
	tails := make([]string, count)
	seenHash := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		for {
			code, genErr := newAdtyVoucherCode()
			if genErr != nil {
				return nil, genErr
			}
			hash := model.AdtyVoucherCodeHash(code)
			if _, dup := seenHash[hash]; dup {
				continue
			}
			seenHash[hash] = struct{}{}
			codes[i] = code
			hashes[i] = hash
			tails[i] = code[len(code)-4:]
			break
		}
	}

	totalAmount := input.AmountUSD.Mul(decimal.NewFromInt(int64(count)))
	var batch *AdtyVoucherBatchIssued
	var auditEvent *model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// Canonical lock order: AdtySite -> User(id ASC) -> AdtySiteWallet.
		var lockedSite *model.AdtySite
		if input.SiteId > 0 && (input.Purpose == model.AdtyVoucherPurposeSiteMargin || input.Purpose == model.AdtyVoucherPurposeSiteCustomer || input.Purpose == model.AdtyVoucherPurposeSiteOwner) {
			site, err := lockAdtyVoucherSite(tx, input.SiteId)
			if err != nil {
				return err
			}
			lockedSite = site
		}
		var issuer *model.User
		if input.Purpose == model.AdtyVoucherPurposeSiteMargin && lockedSite != nil && lockedSite.OwnerUserId > 0 && lockedSite.OwnerUserId < input.IssuerId {
			if _, err := lockAdtyVoucherUser(tx, lockedSite.OwnerUserId); err != nil {
				return err
			}
		}
		issuer, err = lockAdtyVoucherUser(tx, input.IssuerId)
		if err != nil {
			return err
		}
		if input.IntentKey != "" {
			replay, err := findAdtyVoucherBatchReplayTx(tx, issuer.Id, input, quota, count)
			if err != nil {
				return err
			}
			if replay != nil {
				batch = replay
				return nil
			}
		}

		var intentKeyPtr *string
		if input.IntentKey != "" {
			k := input.IntentKey
			intentKeyPtr = &k
		}
		rows := make([]model.AdtyVoucher, count)
		for i := 0; i < count; i++ {
			rows[i] = model.AdtyVoucher{
				CodeHash:  hashes[i],
				CodeTail:  tails[i],
				Purpose:   input.Purpose,
				Status:    model.AdtyVoucherStatusPending,
				AmountUSD: input.AmountUSD,
				Quota:     quota,
				IssuerId:  issuer.Id,
				Reason:    input.Reason,
				BatchNo:   batchNo,
			}
			if i == 0 {
				rows[i].IntentKey = intentKeyPtr
			}
		}

		switch input.Purpose {
		case model.AdtyVoucherPurposeMainUser:
			if issuer.Role != common.RoleRootUser {
				return ErrAdtyVoucherForbidden
			}
			if input.SiteId != 0 || input.DesignatedOwnerId != 0 || input.Source != "" {
				return common.NewMessage("Invalid parameters")
			}
			if err := freezeAdtyVoucherLiabilityTx(tx, totalAmount); err != nil {
				return err
			}
			for i := range rows {
				rows[i].SourceKind = model.AdtyVoucherSourcePlatform
			}
		case model.AdtyVoucherPurposeSiteMargin:
			if issuer.Role != common.RoleRootUser || input.SiteId <= 0 || lockedSite == nil {
				return common.NewMessage("Invalid parameters")
			}
			site := lockedSite
			if site.Status != model.AdtySiteStatusActive || site.OwnerUserId <= 0 {
				return common.NewMessage("Invalid parameters")
			}
			designatedOwnerID := input.DesignatedOwnerId
			if designatedOwnerID == 0 {
				designatedOwnerID = site.OwnerUserId
			} else if designatedOwnerID != site.OwnerUserId {
				return common.NewMessage("Invalid parameters")
			}
			if designatedOwnerID > input.IssuerId {
				if _, err := lockAdtyVoucherUser(tx, designatedOwnerID); err != nil {
					return err
				}
			}
			if err := freezeAdtyVoucherLiabilityTx(tx, totalAmount); err != nil {
				return err
			}
			for i := range rows {
				rows[i].SourceKind = model.AdtyVoucherSourcePlatform
				rows[i].TargetSiteId = site.Id
				rows[i].DesignatedOwnerId = designatedOwnerID
			}
		case model.AdtyVoucherPurposeSiteCustomer:
			if lockedSite == nil {
				return common.NewMessage("Invalid parameters")
			}
			site := lockedSite
			if site.OwnerUserId != issuer.Id || site.Status != model.AdtySiteStatusActive {
				return ErrAdtyVoucherForbidden
			}
			if input.DesignatedOwnerId != 0 && input.DesignatedOwnerId != site.OwnerUserId {
				return common.NewMessage("Invalid parameters")
			}
			switch input.Source {
			case "", "auto", model.AdtyVoucherSourceEarnings, model.AdtyVoucherSourceMargin:
			default:
				return common.NewMessage("Invalid parameters")
			}
			for i := range rows {
				split, freezeErr := adtyFreezeOwnerAutoFundingTx(tx, site.Id, issuer.Id, input.AmountUSD, hashes[i][:16], "voucher issuance freeze", "voucher-freeze:"+hashes[i], "voucher-freeze:"+hashes[i]+":margin")
				if freezeErr != nil {
					if errors.Is(freezeErr, ErrAdtySiteWalletInsufficient) {
						return ErrAdtyVoucherFunds
					}
					return freezeErr
				}
				rows[i].SourceKind = split.SourceKind()
				rows[i].FrozenEarningsUSD = split.EarningsUSD
				rows[i].FrozenMarginUSD = split.MarginUSD
				rows[i].SourceSiteId = site.Id
				rows[i].TargetSiteId = site.Id
				// R10-06: Persist each voucher row immediately after freezing its funding
				// so subsequent iterations in a batch see prior frozen earnings via ownerEarningsWalletTx.
				if err := tx.Create(&rows[i]).Error; err != nil {
					return err
				}
			}
		case model.AdtyVoucherPurposeSiteOwner:
			return ErrAdtyVoucherForbidden
		default:
			return common.NewMessage("Invalid parameters")
		}

		voucherIDs := make([]int64, count)
		totalFrozenEarnings := decimal.Zero
		totalFrozenMargin := decimal.Zero
		for i := range rows {
			if rows[i].Id == 0 {
				if err := tx.Create(&rows[i]).Error; err != nil {
					return err
				}
			}
			voucherIDs[i] = rows[i].Id
			totalFrozenEarnings = totalFrozenEarnings.Add(rows[i].FrozenEarningsUSD)
			totalFrozenMargin = totalFrozenMargin.Add(rows[i].FrozenMarginUSD)
		}
		batchSourceKind := rows[0].SourceKind
		if input.Purpose == model.AdtyVoucherPurposeSiteCustomer {
			batchSourceKind = AdtyOwnerFundingSplit{
				EarningsUSD: totalFrozenEarnings,
				MarginUSD:   totalFrozenMargin,
			}.SourceKind()
		}

		auditSiteID := rows[0].TargetSiteId
		if auditSiteID == 0 {
			auditSiteID = input.SiteId
		}
		auditEvent, err = appendAdtyOfficialAuditTx(tx, input.IssuerId, auditSiteID, "voucher", batchNo, "adty.voucher.issue", input.Reason, "",
			nil,
			map[string]any{
				"batch_no": batchNo, "count": count, "voucher_id": rows[0].Id, "voucher_ids": voucherIDs,
				"purpose": rows[0].Purpose, "amount_usd": input.AmountUSD.String(), "total_amount_usd": totalAmount.String(),
				"quota": quota, "status": model.AdtyVoucherStatusPending, "source": batchSourceKind,
				"frozen_earnings_usd": totalFrozenEarnings.String(), "frozen_margin_usd": totalFrozenMargin.String(),
				"site_id": auditSiteID, "target_site_id": rows[0].TargetSiteId,
				"designated_owner_id": rows[0].DesignatedOwnerId, "intent_key": input.IntentKey,
			},
			model.AuditResultSuccess)
		if err != nil {
			return err
		}

		items := make([]AdtyVoucherIssued, count)
		for i, row := range rows {
			itemIntentKey := ""
			if i == 0 {
				itemIntentKey = input.IntentKey
			}
			items[i] = AdtyVoucherIssued{
				Id:                row.Id,
				Code:              codes[i],
				CodeTail:          row.CodeTail,
				Purpose:           row.Purpose,
				Status:            row.Status,
				SourceKind:        row.SourceKind,
				FrozenEarningsUSD: row.FrozenEarningsUSD,
				FrozenMarginUSD:   row.FrozenMarginUSD,
				TargetSiteId:      row.TargetSiteId,
				DesignatedOwnerId: row.DesignatedOwnerId,
				AmountUSD:         row.AmountUSD,
				Quota:             row.Quota,
				IntentKey:         itemIntentKey,
				BatchNo:           row.BatchNo,
				Reason:            row.Reason,
				CreatedAt:         row.CreatedAt,
				AuditEventID:      auditEvent.EventId,
			}
		}
		first := items[0]
		batch = &AdtyVoucherBatchIssued{
			Id:                first.Id,
			Code:              first.Code,
			CodeTail:          first.CodeTail,
			Purpose:           first.Purpose,
			Status:            first.Status,
			SourceKind:        batchSourceKind,
			FrozenEarningsUSD: totalFrozenEarnings,
			FrozenMarginUSD:   totalFrozenMargin,
			TargetSiteId:      first.TargetSiteId,
			DesignatedOwnerId: first.DesignatedOwnerId,
			AmountUSD:         first.AmountUSD,
			Quota:             first.Quota,
			IntentKey:         input.IntentKey,
			BatchNo:           batchNo,
			Reason:            first.Reason,
			CreatedAt:         first.CreatedAt,
			Count:             count,
			TotalAmountUSD:    totalAmount,
			Items:             items,
			AuditEventID:      auditEvent.EventId,
		}
		return nil
	})
	if err != nil && input.IntentKey != "" {
		if replay, lookErr := findAdtyVoucherBatchReplayTx(model.DB, input.IssuerId, input, quota, count); lookErr != nil {
			return nil, lookErr
		} else if replay != nil {
			return replay, nil
		}
	}
	if err == nil && batch != nil && !batch.Replayed && auditEvent != nil {
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return batch, err
}

func findAdtyVoucherBatchReplayTx(tx *gorm.DB, issuerId int, input AdtyVoucherIssueInput, quota int64, count int) (*AdtyVoucherBatchIssued, error) {
	if tx == nil || issuerId <= 0 || input.IntentKey == "" {
		return nil, nil
	}
	var existing model.AdtyVoucher
	lookup := tx.Where("issuer_id = ? AND intent_key = ?", issuerId, input.IntentKey).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return nil, nil
	}
	if !adtyVoucherIntentMatches(existing, input, quota) {
		return nil, ErrAdtyVoucherIntentConflict
	}
	var rows []model.AdtyVoucher
	if existing.BatchNo != "" {
		if err := tx.Where("issuer_id = ? AND batch_no = ?", issuerId, existing.BatchNo).Order("id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
	} else {
		rows = []model.AdtyVoucher{existing}
	}
	if len(rows) != count {
		return nil, ErrAdtyVoucherIntentConflict
	}
	items := make([]AdtyVoucherIssued, len(rows))
	totalAmount := decimal.Zero
	totalFrozenEarnings := decimal.Zero
	totalFrozenMargin := decimal.Zero
	for i, row := range rows {
		totalAmount = totalAmount.Add(row.AmountUSD)
		totalFrozenEarnings = totalFrozenEarnings.Add(row.FrozenEarningsUSD)
		totalFrozenMargin = totalFrozenMargin.Add(row.FrozenMarginUSD)
		itemIntent := ""
		if row.IntentKey != nil {
			itemIntent = *row.IntentKey
		}
		items[i] = AdtyVoucherIssued{
			Id:                row.Id,
			Code:              "",
			CodeTail:          row.CodeTail,
			Purpose:           row.Purpose,
			Status:            row.Status,
			SourceKind:        row.SourceKind,
			FrozenEarningsUSD: row.FrozenEarningsUSD,
			FrozenMarginUSD:   row.FrozenMarginUSD,
			TargetSiteId:      row.TargetSiteId,
			DesignatedOwnerId: row.DesignatedOwnerId,
			AmountUSD:         row.AmountUSD,
			Quota:             row.Quota,
			IntentKey:         itemIntent,
			BatchNo:           row.BatchNo,
			Reason:            row.Reason,
			CreatedAt:         row.CreatedAt,
			Replayed:          true,
		}
	}
	first := items[0]
	batchSourceKind := first.SourceKind
	if first.Purpose == model.AdtyVoucherPurposeSiteCustomer {
		batchSourceKind = AdtyOwnerFundingSplit{
			EarningsUSD: totalFrozenEarnings,
			MarginUSD:   totalFrozenMargin,
		}.SourceKind()
	}
	return &AdtyVoucherBatchIssued{
		Id:                first.Id,
		Code:              "",
		CodeTail:          first.CodeTail,
		Purpose:           first.Purpose,
		Status:            first.Status,
		SourceKind:        batchSourceKind,
		FrozenEarningsUSD: totalFrozenEarnings,
		FrozenMarginUSD:   totalFrozenMargin,
		TargetSiteId:      first.TargetSiteId,
		DesignatedOwnerId: first.DesignatedOwnerId,
		AmountUSD:         first.AmountUSD,
		Quota:             first.Quota,
		IntentKey:         input.IntentKey,
		BatchNo:           first.BatchNo,
		Reason:            first.Reason,
		CreatedAt:         first.CreatedAt,
		Replayed:          true,
		Count:             len(items),
		TotalAmountUSD:    totalAmount,
		Items:             items,
	}, nil
}

func findAdtyVoucherIssueReplayTx(tx *gorm.DB, issuerId int, input AdtyVoucherIssueInput, quota int64) (*AdtyVoucherIssued, error) {
	if tx == nil || issuerId <= 0 || input.IntentKey == "" {
		return nil, nil
	}
	var existing model.AdtyVoucher
	lookup := tx.Where("issuer_id = ? AND intent_key = ?", issuerId, input.IntentKey).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return nil, nil
	}
	if !adtyVoucherIntentMatches(existing, input, quota) {
		return nil, ErrAdtyVoucherIntentConflict
	}
	if existing.BatchNo != "" {
		var batchCount int64
		if err := tx.Model(&model.AdtyVoucher{}).Where("issuer_id = ? AND batch_no = ?", issuerId, existing.BatchNo).Count(&batchCount).Error; err != nil {
			return nil, err
		}
		if batchCount > 1 {
			return nil, ErrAdtyVoucherIntentConflict
		}
	}
	return &AdtyVoucherIssued{
		Id:                existing.Id,
		Code:              "",
		CodeTail:          existing.CodeTail,
		Purpose:           existing.Purpose,
		Status:            existing.Status,
		SourceKind:        existing.SourceKind,
		FrozenEarningsUSD: existing.FrozenEarningsUSD,
		FrozenMarginUSD:   existing.FrozenMarginUSD,
		TargetSiteId:      existing.TargetSiteId,
		DesignatedOwnerId: existing.DesignatedOwnerId,
		AmountUSD:         existing.AmountUSD,
		Quota:             existing.Quota,
		IntentKey:         input.IntentKey,
		BatchNo:           existing.BatchNo,
		Reason:            existing.Reason,
		CreatedAt:         existing.CreatedAt,
		Replayed:          true,
	}, nil
}

func adtyVoucherIntentMatches(existing model.AdtyVoucher, input AdtyVoucherIssueInput, quota int64) bool {
	if existing.Purpose != input.Purpose || !existing.AmountUSD.Equal(input.AmountUSD) || existing.Quota != quota {
		return false
	}
	if existing.Reason != "" && existing.Reason != input.Reason {
		return false
	}
	switch input.Purpose {
	case model.AdtyVoucherPurposeMainUser:
		return input.SiteId == 0 && input.DesignatedOwnerId == 0 && input.Source == "" && existing.TargetSiteId == 0
	case model.AdtyVoucherPurposeSiteMargin:
		if input.SiteId <= 0 || existing.TargetSiteId != input.SiteId {
			return false
		}
		if input.DesignatedOwnerId > 0 && existing.DesignatedOwnerId != input.DesignatedOwnerId {
			return false
		}
		return true
	case model.AdtyVoucherPurposeSiteCustomer, model.AdtyVoucherPurposeSiteOwner:
		if input.SiteId <= 0 || existing.TargetSiteId != input.SiteId || existing.SourceSiteId != input.SiteId {
			return false
		}
		switch input.Source {
		case "", "auto", model.AdtyVoucherSourceEarnings, model.AdtyVoucherSourceMargin:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func RedeemAdtyVoucher(userId int, code string) (*AdtyVoucherRedeemed, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if userId <= 0 || len(code) < 8 || len(code) > 64 {
		return nil, ErrAdtyVoucherInvalid
	}
	if !allowAdtyVoucherGuess(userId, time.Now().Unix()) {
		return nil, ErrAdtyVoucherRate
	}
	hash := model.AdtyVoucherCodeHash(code)
	var result *AdtyVoucherRedeemed
	var credit int64
	var marginResumedSiteID int
	var err error
	for attempt := 0; attempt < adtyVoucherTradeNoAttempts; attempt++ {
		result, credit, marginResumedSiteID, err = redeemAdtyVoucherOnce(userId, hash, attempt)
		if err == nil {
			break
		}
		if !model.IsAdtyVoucherTradeNoUniqueViolation(err) {
			noteAdtyVoucherGuess(userId, time.Now().Unix())
			return nil, err
		}
		replay, lookErr := loadAdtyVoucherRedeemReplay(userId, hash)
		if lookErr != nil {
			noteAdtyVoucherGuess(userId, time.Now().Unix())
			return nil, lookErr
		}
		if replay != nil {
			return replay, nil
		}
		// Still pending: the generated trade number collided with another
		// formal order. Retry with a new number so this code is not credited
		// twice and a unique violation is not returned as a raw 500 after a
		// second order was inserted.
	}
	if err != nil {
		noteAdtyVoucherGuess(userId, time.Now().Unix())
		return nil, err
	}
	if credit > 0 {
		model.AdtySyncCreditUserQuotaCache(userId, credit, "voucher redemption")
	}
	if marginResumedSiteID > 0 && result != nil && !result.Replayed {
		if _, resumeErr := ResumePendingAdtyGiftFulfillments(marginResumedSiteID); resumeErr != nil {
			common.SysError(common.LogText("resume pending gifts after margin voucher redeem failed for site %d: %s", marginResumedSiteID, resumeErr.Error()))
		}
		if covErr := ResumeAdtySiteInviteCoverage(marginResumedSiteID, time.Now()); covErr != nil {
			common.SysError(common.LogText("resume site invite coverage after margin voucher redeem failed for site %d: %s", marginResumedSiteID, covErr.Error()))
		}
	}
	return result, nil
}

func redeemAdtyVoucherOnce(userId int, hash string, attempt int) (*AdtyVoucherRedeemed, int64, int, error) {
	var result *AdtyVoucherRedeemed
	var credit int64
	var voucherHint model.AdtyVoucher
	_ = model.DB.Select("target_site_id", "source_site_id").Where("code_hash = ?", hash).Limit(1).Find(&voucherHint).Error
	preLockSiteID := voucherHint.TargetSiteId
	if preLockSiteID == 0 {
		preLockSiteID = voucherHint.SourceSiteId
	}
	var marginResumedSiteID int
	var redeemAudit map[string]any
	var redeemAuditEntry *model.AuditLog
	var redeemVoucherID int64
	var redeemSiteID int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var lockedSite *model.AdtySite
		if preLockSiteID > 0 {
			var s model.AdtySite
			if err := model.AdtyLockForUpdate(tx).First(&s, preLockSiteID).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			} else {
				lockedSite = &s
			}
		}
		user, err := lockAdtyVoucherUser(tx, userId)
		if err != nil {
			return err
		}
		var voucher model.AdtyVoucher
		lookup := model.AdtyLockForUpdate(tx).Where("code_hash = ?", hash).Limit(1).Find(&voucher)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return ErrAdtyVoucherInvalid
		}
		actualSiteID := voucher.TargetSiteId
		if actualSiteID == 0 {
			actualSiteID = voucher.SourceSiteId
		}
		if actualSiteID > 0 && (lockedSite == nil || lockedSite.Id != actualSiteID) {
			return ErrAdtyVoucherInvalid
		}
		if voucher.Status == model.AdtyVoucherStatusRedeemed {
			if voucher.RedeemerId == user.Id {
				result = &AdtyVoucherRedeemed{TradeNo: voucher.TradeNo, Purpose: voucher.Purpose, AmountUSD: voucher.AmountUSD, Quota: voucher.Quota, Replayed: true}
				return nil
			}
			return ErrAdtyVoucherUsed
		}
		if voucher.Status == model.AdtyVoucherStatusFrozen {
			return ErrAdtyVoucherFrozen
		}
		if voucher.Status == model.AdtyVoucherStatusCancelled {
			return ErrAdtyVoucherCancelled
		}
		if voucher.Status != model.AdtyVoucherStatusPending {
			return ErrAdtyVoucherInvalid
		}
		if err := adtyVoucherRedeemAllowed(tx, user, &voucher, lockedSite); err != nil {
			return err
		}
		trade, err := allocateAdtyVoucherTradeNo(tx, hash, attempt)
		if err != nil {
			return err
		}
		now := time.Now().Unix()
		updated := tx.Model(&model.AdtyVoucher{}).Where("id = ? AND status = ?", voucher.Id, model.AdtyVoucherStatusPending).
			Updates(map[string]any{"status": model.AdtyVoucherStatusRedeemed, "redeemer_id": user.Id, "trade_no": trade, "redeemed_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrAdtyVoucherUsed
		}
		if err := spendAdtyVoucherSourceTx(tx, &voucher); err != nil {
			return err
		}
		if voucher.Purpose == model.AdtyVoucherPurposeSiteMargin {
			if err := creditAdtyVoucherMarginTx(tx, voucher.TargetSiteId, voucher.AmountUSD, trade); err != nil {
				return err
			}
			marginResumedSiteID = voucher.TargetSiteId
		} else {
			before, err := model.AdtyCreditUserQuotaTx(tx, user.Id, voucher.Quota)
			if err != nil {
				return err
			}
			key := "voucher:" + trade
			if err := model.InsertAdtyLedgerTx(tx, &model.AdtyBalanceLedger{
				UserId: user.Id, DeltaQuota: voucher.Quota, BalanceBefore: before, BalanceAfter: before + voucher.Quota,
				Type: model.AdtyLedgerTypeVoucher, RefType: "voucher", RefId: trade, IdempotencyKey: &key,
			}); err != nil {
				return err
			}
			// 币值批次的站点取券的目标站，没有目标站时退回来源站（与下面审计行
			// 的口径一致）。主站承接的关站兑换码归入主站（0），且绝不入账原已关站 operating 钱包。
			batchSite := voucher.TargetSiteId
			if batchSite == 0 {
				batchSite = voucher.SourceSiteId
			}
			if voucher.InheritedByMain {
				batchSite = 0
			}
			if err := model.CreateAdtyFundBatchesWithSiteTx(tx, user.Id, model.AdtyFundBatchSiteID(batchSite), trade, voucher.Quota, 0); err != nil {
				return err
			}
			if !voucher.InheritedByMain && (voucher.Purpose == model.AdtyVoucherPurposeSiteCustomer || voucher.Purpose == model.AdtyVoucherPurposeSiteOwner) {
				if err := adtyMoveOperatingPrincipalTx(tx, batchSite, voucher.Quota, "principal_credit", "voucher", trade, "operating:voucher:"+trade, false); err != nil {
					return err
				}
			}
			credit = voucher.Quota
		}
		if err := insertAdtyVoucherOrderTx(tx, user.Id, trade, &voucher, now); err != nil {
			return err
		}
		result = &AdtyVoucherRedeemed{TradeNo: trade, Purpose: voucher.Purpose, AmountUSD: voucher.AmountUSD, Quota: voucher.Quota}
		redeemVoucherID = voucher.Id
		redeemSiteID = voucher.TargetSiteId
		if redeemSiteID == 0 {
			redeemSiteID = voucher.SourceSiteId
		}
		redeemAudit = map[string]any{
			"voucher_id": voucher.Id, "purpose": voucher.Purpose, "amount_usd": voucher.AmountUSD.String(),
			"quota": voucher.Quota, "status": model.AdtyVoucherStatusRedeemed, "trade_no": trade,
			"redeemer_id": user.Id, "source_kind": voucher.SourceKind, "source_site_id": voucher.SourceSiteId,
			"target_site_id": voucher.TargetSiteId,
		}
		var auditErr error
		redeemAuditEntry, auditErr = appendAdtyOfficialAuditTx(tx, userId, redeemSiteID, "voucher", strconv.FormatInt(redeemVoucherID, 10), "adty.voucher.redeem", "", "",
			map[string]any{"status": model.AdtyVoucherStatusPending},
			redeemAudit,
			model.AuditResultSuccess)
		if auditErr != nil {
			return auditErr
		}
		return nil
	})
	if err == nil && result != nil && !result.Replayed && redeemAuditEntry != nil {
		deliverAdtyCommittedAudit(*redeemAuditEntry)
	}
	return result, credit, marginResumedSiteID, err
}

func loadAdtyVoucherRedeemReplay(userId int, hash string) (*AdtyVoucherRedeemed, error) {
	var voucher model.AdtyVoucher
	lookup := model.DB.Where("code_hash = ?", hash).Limit(1).Find(&voucher)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected == 0 || voucher.Status != model.AdtyVoucherStatusRedeemed {
		return nil, nil
	}
	if voucher.RedeemerId == userId {
		return &AdtyVoucherRedeemed{TradeNo: voucher.TradeNo, Purpose: voucher.Purpose, AmountUSD: voucher.AmountUSD, Quota: voucher.Quota, Replayed: true}, nil
	}
	return nil, ErrAdtyVoucherUsed
}

func allocateAdtyVoucherTradeNo(tx *gorm.DB, hash string, attemptSeed int) (string, error) {
	for attempt := 0; attempt < adtyVoucherTradeNoAttempts; attempt++ {
		trade := generateAdtyVoucherTradeNo(hash, attemptSeed*adtyVoucherTradeNoAttempts+attempt)
		if trade == "" {
			continue
		}
		taken, err := model.AdtyVoucherTradeNoTaken(tx, trade)
		if err != nil {
			return "", err
		}
		if !taken {
			return trade, nil
		}
	}
	return "", fmt.Errorf("allocate voucher trade_no: no unused order number available")
}

func generateAdtyVoucherTradeNo(hash string, attempt int) string {
	if adtyVoucherTradeNoTestHook != nil {
		if trade := strings.TrimSpace(adtyVoucherTradeNoTestHook(hash, attempt)); trade != "" {
			return trade
		}
	}
	nonce := fmt.Sprintf("%s:%s:%d", hash, time.Now().Format(time.RFC3339Nano), attempt)
	sum := strings.ToUpper(common.Sha1([]byte(nonce)))
	if len(sum) < 20 {
		return ""
	}
	return "VCH" + sum[:20]
}

// AdtyVoucherListFilter narrows a voucher list; empty fields do not filter.
type AdtyVoucherListFilter struct {
	Status       string
	Purpose      string
	IssuerId     int
	SiteId       int    // codes funded by this site's wallets
	TargetSiteId int    // codes targeted at this site
	BatchNo      string // filter by batch number
}

// ListAdtyVouchers lists codes newest first. Only the tail of each code is
// stored, so the full code can never be shown again after issuance.
func ListAdtyVouchers(filter AdtyVoucherListFilter, offset, limit int) ([]model.AdtyVoucher, int64, error) {
	switch filter.Status {
	case "", model.AdtyVoucherStatusPending, model.AdtyVoucherStatusFrozen, model.AdtyVoucherStatusRedeemed, model.AdtyVoucherStatusCancelled:
	default:
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	switch filter.Purpose {
	case "", model.AdtyVoucherPurposeMainUser, model.AdtyVoucherPurposeSiteMargin, model.AdtyVoucherPurposeSiteCustomer, model.AdtyVoucherPurposeSiteOwner:
	default:
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	if filter.TargetSiteId < 0 {
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	query := model.DB.Model(&model.AdtyVoucher{})
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Purpose != "" {
		query = query.Where("purpose = ?", filter.Purpose)
	}
	if filter.IssuerId > 0 {
		query = query.Where("issuer_id = ?", filter.IssuerId)
	}
	if filter.SiteId > 0 {
		query = query.Where("source_site_id = ?", filter.SiteId)
	}
	if filter.TargetSiteId > 0 {
		query = query.Where("target_site_id = ?", filter.TargetSiteId)
	}
	if batchNo := strings.TrimSpace(filter.BatchNo); batchNo != "" {
		query = query.Where("batch_no = ?", batchNo)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.AdtyVoucher, 0)
	err := query.Order("id desc").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

// AdtyVoucherIssuance is the main-site issuance cap and the part already
// frozen by unredeemed codes or spent by redeemed ones.
type AdtyVoucherIssuance struct {
	LimitUSD  string `json:"limit_usd"`
	FrozenUSD string `json:"frozen_usd"`
	SpentUSD  string `json:"spent_usd"`
}

func LoadAdtyVoucherIssuance() (AdtyVoucherIssuance, error) {
	var row model.AdtyVoucherLiability
	if err := model.DB.Where("scope = ?", "main").Limit(1).Find(&row).Error; err != nil {
		return AdtyVoucherIssuance{}, err
	}
	return AdtyVoucherIssuance{LimitUSD: row.LimitUSD.String(), FrozenUSD: row.FrozenUSD.String(), SpentUSD: row.SpentUSD.String()}, nil
}

func adtyVoucherManageAllowedTx(tx *gorm.DB, actor *model.User, voucher *model.AdtyVoucher, lockedSite *model.AdtySite, ownerOnly bool, expectedSiteID int) error {
	if !ownerOnly && actor.Role == common.RoleRootUser {
		return nil
	}
	if voucher.IssuerId != actor.Id || voucher.InheritedByMain {
		return ErrAdtyVoucherForbidden
	}
	if ownerOnly && (voucher.SourceKind == model.AdtyVoucherSourcePlatform || voucher.SourceSiteId <= 0 || expectedSiteID <= 0 || voucher.SourceSiteId != expectedSiteID) {
		return ErrAdtyVoucherForbidden
	}
	siteID := voucher.SourceSiteId
	if siteID <= 0 {
		siteID = voucher.TargetSiteId
	}
	if siteID > 0 {
		var site *model.AdtySite
		if lockedSite != nil && lockedSite.Id == siteID {
			site = lockedSite
		} else {
			var loaded model.AdtySite
			if err := tx.First(&loaded, siteID).Error; err != nil {
				return err
			}
			site = &loaded
		}
		if site.OwnerUserId != actor.Id || site.Status == model.AdtySiteStatusClosing || site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting {
			return ErrAdtyVoucherForbidden
		}
	}
	return nil
}

func FreezeAdtyVoucher(actorId int, voucherId int64) error {
	_, err := FreezeAdtyVoucherWithAudit(actorId, voucherId)
	return err
}

func FreezeAdtyVoucherWithAudit(actorId int, voucherId int64) (string, error) {
	return freezeAdtyVoucherWithScope(actorId, voucherId, false, 0)
}

func FreezeAdtySiteVoucherWithAudit(actorId, siteID int, voucherId int64) (string, error) {
	return freezeAdtyVoucherWithScope(actorId, voucherId, true, siteID)
}

func freezeAdtyVoucherWithScope(actorId int, voucherId int64, ownerOnly bool, expectedSiteID int) (string, error) {
	var event model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var stub struct {
			SourceSiteId int
			TargetSiteId int
		}
		if err := tx.Model(&model.AdtyVoucher{}).Select("source_site_id", "target_site_id").First(&stub, voucherId).Error; err != nil {
			return err
		}
		siteID := stub.SourceSiteId
		if siteID <= 0 {
			siteID = stub.TargetSiteId
		}
		var lockedSite *model.AdtySite
		if siteID > 0 {
			var err error
			lockedSite, err = lockAdtyVoucherSite(tx, siteID)
			if err != nil {
				return err
			}
		}
		actor, err := lockAdtyVoucherUser(tx, actorId)
		if err != nil {
			return err
		}
		var voucher model.AdtyVoucher
		if err := model.AdtyLockForUpdate(tx).First(&voucher, voucherId).Error; err != nil {
			return err
		}
		if err := adtyVoucherManageAllowedTx(tx, actor, &voucher, lockedSite, ownerOnly, expectedSiteID); err != nil {
			return err
		}
		if voucher.Status != model.AdtyVoucherStatusPending {
			return common.NewMessage("Invalid parameters")
		}
		updated := tx.Model(&voucher).Where("id = ? AND status = ?", voucher.Id, model.AdtyVoucherStatusPending).
			Update("status", model.AdtyVoucherStatusFrozen)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return common.NewMessage("Invalid parameters")
		}
		event, err = prepareAdtyVoucherStatusAudit(*actor, voucher, "adty.voucher.freeze", "", model.AdtyVoucherStatusPending, model.AdtyVoucherStatusFrozen)
		if err != nil {
			return err
		}
		return model.AppendAuditIntentTx(tx, event)
	})
	if err == nil && event.EventId != "" {
		deliverAdtyCommittedAudit(event)
	}
	return event.EventId, err
}

// InheritAdtyVouchersOnCloseTx keeps unredeemed customer codes under the original
// audience after the site closes. Unused site-owner and site-margin codes are
// revoked and their source holds are released; it does not widen use.
func InheritAdtyVouchersOnCloseTx(tx *gorm.DB, siteId int, movedUserIDs []int) error {
	if err := revokeUnusedOwnerAndMarginVouchersOnCloseTx(tx, siteId); err != nil {
		return err
	}
	if err := tx.Model(&model.AdtyVoucher{}).
		Where("(target_site_id = ? OR source_site_id = ?) AND status IN ?", siteId, siteId, []string{model.AdtyVoucherStatusPending, model.AdtyVoucherStatusFrozen}).
		Update("inherited_by_main", true).Error; err != nil {
		return err
	}
	for _, userId := range movedUserIDs {
		var existing int64
		if err := tx.Model(&model.AdtyVoucherCloseGrant{}).Where("site_id = ? AND user_id = ?", siteId, userId).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			continue
		}
		if err := tx.Create(&model.AdtyVoucherCloseGrant{SiteId: siteId, UserId: userId}).Error; err != nil {
			return err
		}
	}
	return nil
}

func CancelAdtyVoucher(actorId int, voucherId int64) error {
	_, err := CancelAdtyVoucherWithAudit(actorId, voucherId)
	return err
}

func CancelAdtyVoucherWithAudit(actorId int, voucherId int64) (string, error) {
	return cancelAdtyVoucherWithScope(actorId, voucherId, false, 0)
}

func CancelAdtySiteVoucherWithAudit(actorId, siteID int, voucherId int64) (string, error) {
	return cancelAdtyVoucherWithScope(actorId, voucherId, true, siteID)
}

func cancelAdtyVoucherWithScope(actorId int, voucherId int64, ownerOnly bool, expectedSiteID int) (string, error) {
	var event model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var stub struct {
			SourceSiteId int
			TargetSiteId int
		}
		if err := tx.Model(&model.AdtyVoucher{}).Select("source_site_id", "target_site_id").First(&stub, voucherId).Error; err != nil {
			return err
		}
		siteID := stub.SourceSiteId
		if siteID <= 0 {
			siteID = stub.TargetSiteId
		}
		var lockedSite *model.AdtySite
		if siteID > 0 {
			var err error
			lockedSite, err = lockAdtyVoucherSite(tx, siteID)
			if err != nil {
				return err
			}
		}
		actor, err := lockAdtyVoucherUser(tx, actorId)
		if err != nil {
			return err
		}
		var voucher model.AdtyVoucher
		if err := model.AdtyLockForUpdate(tx).First(&voucher, voucherId).Error; err != nil {
			return err
		}
		if err := adtyVoucherManageAllowedTx(tx, actor, &voucher, lockedSite, ownerOnly, expectedSiteID); err != nil {
			return err
		}
		beforeStatus := voucher.Status
		if err := cancelAdtyVoucherTx(tx, &voucher); err != nil {
			return err
		}
		event, err = prepareAdtyVoucherStatusAudit(*actor, voucher, "adty.voucher.cancel", "", beforeStatus, model.AdtyVoucherStatusCancelled)
		if err != nil {
			return err
		}
		return model.AppendAuditIntentTx(tx, event)
	})
	if err == nil && event.EventId != "" {
		deliverAdtyCommittedAudit(event)
	}
	return event.EventId, err
}

func revokeUnusedOwnerAndMarginVouchersOnCloseTx(tx *gorm.DB, siteId int) error {
	var vouchers []model.AdtyVoucher
	if err := model.AdtyLockForUpdate(tx).
		Where("(target_site_id = ? OR source_site_id = ?) AND purpose IN ? AND status IN ?", siteId, siteId, []string{model.AdtyVoucherPurposeSiteOwner, model.AdtyVoucherPurposeSiteMargin}, []string{model.AdtyVoucherStatusPending, model.AdtyVoucherStatusFrozen}).
		Order("id ASC").
		Find(&vouchers).Error; err != nil {
		return err
	}
	for i := range vouchers {
		before := vouchers[i]
		if err := cancelAdtyVoucherTx(tx, &vouchers[i]); err != nil {
			return err
		}
		event, err := prepareAdtyVoucherStatusAudit(model.User{}, before, "adty.voucher.cancel", "unused owner or margin voucher revoked on site close", before.Status, model.AdtyVoucherStatusCancelled)
		if err != nil {
			return err
		}
		if err := model.AppendAuditIntentTx(tx, event); err != nil {
			return err
		}
	}
	return nil
}

// RevokeUnusedOwnerAndMarginVouchersOnClosedSites cancels leftover unused
// site-owner and site-margin codes on sites that are already closed, and
// releases their source holds. Later closes are handled by
// InheritAdtyVouchersOnCloseTx; this backfills historical pending or
// inherited_by_main rows that were never revoked.
func RevokeUnusedOwnerAndMarginVouchersOnClosedSites() error {
	if model.DB == nil {
		return nil
	}
	var leftovers []model.AdtyVoucher
	if err := model.DB.Select("id", "target_site_id", "source_site_id").
		Where("purpose IN ? AND status IN ?", []string{model.AdtyVoucherPurposeSiteOwner, model.AdtyVoucherPurposeSiteMargin}, []string{model.AdtyVoucherStatusPending, model.AdtyVoucherStatusFrozen}).
		Find(&leftovers).Error; err != nil {
		return err
	}
	siteIDs := map[int]struct{}{}
	for _, voucher := range leftovers {
		if voucher.TargetSiteId > 0 {
			siteIDs[voucher.TargetSiteId] = struct{}{}
		}
		if voucher.SourceSiteId > 0 {
			siteIDs[voucher.SourceSiteId] = struct{}{}
		}
	}
	sortedSiteIDs := make([]int, 0, len(siteIDs))
	for siteID := range siteIDs {
		sortedSiteIDs = append(sortedSiteIDs, siteID)
	}
	slices.Sort(sortedSiteIDs)
	// R14-027: Run each closed site in its own transaction so a reviewing restriction
	// or transient error on one site never rolls back or starves other closed sites.
	var firstErr error
	for _, siteID := range sortedSiteIDs {
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Select("id", "status").Where("id = ?", siteID).Limit(1).Find(&site).Error; err != nil {
				return err
			}
			if site.Id == 0 || site.Status != model.AdtySiteStatusClosed {
				return nil
			}
			return revokeUnusedOwnerAndMarginVouchersOnCloseTx(tx, siteID)
		})
		if err != nil {
			common.SysError(common.LogText("revoke unused closed-site vouchers failed site=%d: %s", siteID, err.Error()))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func cancelAdtyVoucherTx(tx *gorm.DB, voucher *model.AdtyVoucher) error {
	if voucher.Status != model.AdtyVoucherStatusPending && voucher.Status != model.AdtyVoucherStatusFrozen {
		return common.NewMessage("Invalid parameters")
	}
	updated := tx.Model(voucher).Where("id = ? AND status IN ?", voucher.Id, []string{model.AdtyVoucherStatusPending, model.AdtyVoucherStatusFrozen}).
		Update("status", model.AdtyVoucherStatusCancelled)
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return common.NewMessage("Invalid parameters")
	}
	return releaseAdtyVoucherSourceTx(tx, voucher)
}

func SetAdtyVoucherIssuanceLimit(actorId int, limit decimal.Decimal) error {
	if !limit.IsPositive() {
		return common.NewMessage("Invalid parameters")
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		actor, err := lockAdtyVoucherUser(tx, actorId)
		if err != nil {
			return err
		}
		if actor.Role != common.RoleRootUser {
			return common.NewMessage("No permission to perform this action")
		}
		row, err := lockAdtyVoucherLiabilityTx(tx)
		if err != nil {
			return err
		}
		if limit.LessThan(row.FrozenUSD.Add(row.SpentUSD)) {
			return ErrAdtyVoucherLimit
		}
		return tx.Model(row).Update("limit_usd", limit).Error
	})
}

func adtyVoucherQuota(amount decimal.Decimal) (int64, error) {
	if !amount.IsPositive() || amount.Exponent() < -2 || amount.GreaterThan(decimal.NewFromInt(100000)) {
		return 0, common.NewMessage("Invalid parameters")
	}
	computed := amount.Mul(common.QuotaPerUnitDecimal())
	if !computed.Equal(computed.Truncate(0)) {
		return 0, common.NewMessage("Invalid parameters")
	}
	quota, err := common.WalletQuotaFromDecimalStrict(computed)
	if err != nil || quota <= 0 {
		return 0, common.NewMessage("Invalid parameters")
	}
	return int64(quota), nil
}

func newAdtyVoucherCode() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), "="), nil
}

func newAdtyVoucherBatchNo() (string, error) {
	raw := make([]byte, 3)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("VB%d-%s", time.Now().Unix(), strings.ToUpper(hex.EncodeToString(raw))), nil
}

func lockAdtyVoucherUser(tx *gorm.DB, id int) (*model.User, error) {
	var user model.User
	if err := model.AdtyLockForUpdate(tx).First(&user, id).Error; err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled {
		return nil, common.NewMessage("No permission to perform this action")
	}
	return &user, nil
}

func lockAdtyVoucherSite(tx *gorm.DB, id int) (*model.AdtySite, error) {
	var site model.AdtySite
	if err := model.AdtyLockForUpdate(tx).First(&site, id).Error; err != nil {
		return nil, err
	}
	return &site, nil
}

func lockAdtyVoucherLiabilityTx(tx *gorm.DB) (*model.AdtyVoucherLiability, error) {
	var row model.AdtyVoucherLiability
	lookup := model.AdtyLockForUpdate(tx).Where("scope = ?", "main").Limit(1).Find(&row)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected == 0 {
		row = model.AdtyVoucherLiability{Scope: "main"}
		if err := tx.Create(&row).Error; err != nil {
			return nil, err
		}
	}
	return &row, nil
}

func freezeAdtyVoucherLiabilityTx(tx *gorm.DB, amount decimal.Decimal) error {
	row, err := lockAdtyVoucherLiabilityTx(tx)
	if err != nil {
		return err
	}
	if row.LimitUSD.LessThanOrEqual(decimal.Zero) || row.FrozenUSD.Add(row.SpentUSD).Add(amount).GreaterThan(row.LimitUSD) {
		return ErrAdtyVoucherLimit
	}
	return tx.Model(row).Update("frozen_usd", row.FrozenUSD.Add(amount)).Error
}

func spendAdtyVoucherLiabilityTx(tx *gorm.DB, amount decimal.Decimal) error {
	row, err := lockAdtyVoucherLiabilityTx(tx)
	if err != nil {
		return err
	}
	if row.FrozenUSD.LessThan(amount) {
		return ErrAdtyVoucherFunds
	}
	return tx.Model(row).Updates(map[string]any{
		"frozen_usd": row.FrozenUSD.Sub(amount),
		"spent_usd":  row.SpentUSD.Add(amount),
	}).Error
}

func releaseAdtyVoucherLiabilityTx(tx *gorm.DB, amount decimal.Decimal) error {
	row, err := lockAdtyVoucherLiabilityTx(tx)
	if err != nil {
		return err
	}
	if row.FrozenUSD.LessThan(amount) {
		return ErrAdtyVoucherFunds
	}
	return tx.Model(row).Update("frozen_usd", row.FrozenUSD.Sub(amount)).Error
}

func adtyVoucherFrozenSplit(voucher *model.AdtyVoucher) (earnUSD, marginUSD decimal.Decimal, legacySingleMargin bool) {
	if voucher.FrozenEarningsUSD.IsPositive() || voucher.FrozenMarginUSD.IsPositive() {
		return voucher.FrozenEarningsUSD, voucher.FrozenMarginUSD, false
	}
	if voucher.SourceKind == model.AdtyVoucherSourceMargin {
		return decimal.Zero, voucher.AmountUSD, true
	}
	return voucher.AmountUSD, decimal.Zero, false
}

func releaseAdtyVoucherSourceTx(tx *gorm.DB, voucher *model.AdtyVoucher) error {
	if voucher.SourceKind == model.AdtyVoucherSourcePlatform {
		return releaseAdtyVoucherLiabilityTx(tx, voucher.AmountUSD)
	}
	earnUSD, marginUSD, legacySingleMargin := adtyVoucherFrozenSplit(voucher)
	if marginUSD.IsPositive() && !earnUSD.IsPositive() && voucher.SourceSiteId > 0 {
		if err := preLockAdtySiteWalletsTx(tx, voucher.SourceSiteId, model.AdtySiteWalletEarnings); err != nil {
			return err
		}
	}
	if earnUSD.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, voucher.SourceSiteId, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "voucher_release", RefType: "voucher", RefID: voucher.CodeHash[:16], Reason: "voucher cancellation release", IdempotencyKey: "voucher-release:" + voucher.CodeHash,
			HoldDelta: earnUSD.Neg(),
		}); err != nil {
			if errors.Is(err, ErrAdtySiteWalletInsufficient) {
				return ErrAdtyVoucherFunds
			}
			return err
		}
	}
	if marginUSD.IsPositive() {
		marginKey := "voucher-release:" + voucher.CodeHash + ":margin"
		if legacySingleMargin {
			marginKey = "voucher-release:" + voucher.CodeHash
		}
		if _, err := applyAdtySiteWalletChangeTx(tx, voucher.SourceSiteId, model.AdtySiteWalletMargin, decimal.Zero, AdtySiteWalletChange{
			Type: "voucher_release", RefType: "voucher", RefID: voucher.CodeHash[:16], Reason: "voucher cancellation release", IdempotencyKey: marginKey,
			HoldDelta: marginUSD.Neg(), MarginExcessOnly: !legacySingleMargin,
		}); err != nil {
			if errors.Is(err, ErrAdtySiteWalletInsufficient) {
				return ErrAdtyVoucherFunds
			}
			return err
		}
	}
	return nil
}

func spendAdtyVoucherSourceTx(tx *gorm.DB, voucher *model.AdtyVoucher) error {
	if voucher.SourceKind == model.AdtyVoucherSourcePlatform {
		return spendAdtyVoucherLiabilityTx(tx, voucher.AmountUSD)
	}
	earnUSD, marginUSD, legacySingleMargin := adtyVoucherFrozenSplit(voucher)
	if marginUSD.IsPositive() && !earnUSD.IsPositive() && voucher.SourceSiteId > 0 {
		if err := preLockAdtySiteWalletsTx(tx, voucher.SourceSiteId, model.AdtySiteWalletEarnings); err != nil {
			return err
		}
	}
	if earnUSD.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, voucher.SourceSiteId, model.AdtySiteWalletEarnings, earnUSD.Neg(), AdtySiteWalletChange{
			Type: "voucher_spend", RefType: "voucher", RefID: voucher.CodeHash[:16], Reason: "voucher redeem spend", IdempotencyKey: "voucher-spend:" + voucher.CodeHash,
			HoldDelta: earnUSD.Neg(),
		}); err != nil {
			if errors.Is(err, ErrAdtySiteWalletInsufficient) {
				return ErrAdtyVoucherFunds
			}
			return err
		}
	}
	if marginUSD.IsPositive() {
		marginKey := "voucher-spend:" + voucher.CodeHash + ":margin"
		if legacySingleMargin {
			marginKey = "voucher-spend:" + voucher.CodeHash
		}
		if _, err := applyAdtySiteWalletChangeTx(tx, voucher.SourceSiteId, model.AdtySiteWalletMargin, marginUSD.Neg(), AdtySiteWalletChange{
			Type: "voucher_spend", RefType: "voucher", RefID: voucher.CodeHash[:16], Reason: "voucher redeem spend", IdempotencyKey: marginKey,
			HoldDelta: marginUSD.Neg(), MarginExcessOnly: !legacySingleMargin,
		}); err != nil {
			if errors.Is(err, ErrAdtySiteWalletInsufficient) {
				return ErrAdtyVoucherFunds
			}
			return err
		}
	}
	return nil
}

func creditAdtyVoucherMarginTx(tx *gorm.DB, siteId int, amount decimal.Decimal, trade string) error {
	if err := ensureAdtySiteWalletsTx(tx, siteId); err != nil {
		return err
	}
	_, err := applyAdtySiteWalletChangeTx(tx, siteId, model.AdtySiteWalletMargin, amount, AdtySiteWalletChange{
		Type: "voucher_margin", RefType: "voucher", RefID: trade, Reason: "voucher margin credit", IdempotencyKey: "voucher-margin:" + trade,
		OpenMarginCycle: true,
	})
	return err
}

func adtyVoucherRedeemAllowed(tx *gorm.DB, user *model.User, voucher *model.AdtyVoucher, lockedSite *model.AdtySite) error {
	if isEmp, empErr := adtyIsIndependentTeamEmployeeTx(tx, user.Id); empErr != nil {
		return empErr
	} else if isEmp {
		return ErrAdtyVoucherWrongAccount
	}
	switch voucher.Purpose {
	case model.AdtyVoucherPurposeMainUser:
		if user.SiteId != 0 || user.StaffSiteId != 0 || user.Role != common.RoleCommonUser {
			return ErrAdtyVoucherWrongAccount
		}
		isOwner, ownerErr := AdtyIsSiteOwnerStrictTx(tx, user.Id)
		if ownerErr != nil {
			return ownerErr
		}
		if isOwner {
			return ErrAdtyVoucherWrongAccount
		}
	case model.AdtyVoucherPurposeSiteMargin:
		if voucher.DesignatedOwnerId <= 0 || user.Id != voucher.DesignatedOwnerId {
			return ErrAdtyVoucherWrongAccount
		}
		if lockedSite == nil || lockedSite.Id != voucher.TargetSiteId || lockedSite.Status != model.AdtySiteStatusActive {
			return ErrAdtyVoucherWrongAccount
		}
	case model.AdtyVoucherPurposeSiteCustomer:
		if user.StaffSiteId != 0 {
			return ErrAdtyVoucherWrongAccount
		}
		if voucher.InheritedByMain {
			var grants int64
			if err := tx.Model(&model.AdtyVoucherCloseGrant{}).Where("site_id = ? AND user_id = ?", voucher.TargetSiteId, user.Id).Count(&grants).Error; err != nil {
				return err
			}
			if grants == 0 {
				return ErrAdtyVoucherWrongAccount
			}
			return nil
		}
		if user.SiteId != voucher.TargetSiteId {
			return ErrAdtyVoucherWrongAccount
		}
	case model.AdtyVoucherPurposeSiteOwner:
		return ErrAdtyVoucherWrongAccount
	default:
		return ErrAdtyVoucherInvalid
	}
	return nil
}

func insertAdtyVoucherOrderTx(tx *gorm.DB, userId int, trade string, voucher *model.AdtyVoucher, now int64) error {
	siteOwnerUserID := voucher.IssuerId
	if voucher.Purpose == model.AdtyVoucherPurposeSiteMargin && voucher.DesignatedOwnerId > 0 {
		siteOwnerUserID = voucher.DesignatedOwnerId
	}
	// Official decimal amounts and the already-determined credit are the
	// source of truth. Wallet and ledger credit use voucher.Quota / AmountUSD
	// directly; this formal order does not go through float or IntPart.
	if err := tx.Create(&model.AdtyTopupOrder{
		TradeNo: trade, UserId: userId, Provider: model.AdtyVoucherProvider, Method: voucher.Purpose,
		FaceUSD: voucher.AmountUSD, CreditQuota: voucher.Quota, PayAmount: voucher.AmountUSD, PayCurrency: "USD",
		PaidAmount: voucher.AmountUSD, PaidCurrency: "USD", PaidSource: "voucher", SiteId: voucher.TargetSiteId,
		SiteOwnerUserId: siteOwnerUserID, TopupGroupRatio: decimal.NewFromInt(1),
	}).Error; err != nil {
		return err
	}
	// TopUp is only a compatibility projection for the unified order list.
	// MoneyExact stores the exact face amount so this row does not treat
	// float64 or IntPart as the authoritative face. Money is still filled
	// as a legacy display projection for the existing list JSON field and
	// is not used to credit the wallet or ledger. Amount is the already-
	// determined exact credit quota and is likewise not the credited source.
	// Exact values used for crediting live on AdtyTopupOrder and the voucher.
	topUp := model.TopUp{
		UserId: userId, Amount: voucher.Quota, MoneyExact: voucher.AmountUSD,
		TradeNo: trade, PaymentMethod: voucher.Purpose, PaymentProvider: model.AdtyVoucherProvider,
		CreateTime: now, CompleteTime: now, Status: common.TopUpStatusSuccess,
	}
	if f, exact := voucher.AmountUSD.Float64(); exact {
		topUp.Money = f
	} else {
		topUp.Money = voucher.AmountUSD.InexactFloat64()
	}
	return tx.Create(&topUp).Error
}

func allowAdtyVoucherGuess(userId int, now int64) bool {
	if common.RedisEnabled {
		ok, err := allowAdtyVoucherGuessRedis(userId, now)
		if err != nil {
			return false
		}
		return ok
	}
	voucherGuess.mu.Lock()
	defer voucherGuess.mu.Unlock()
	return len(recentAdtyVoucherGuesses(userId, now)) < adtyVoucherGuessMax
}

func noteAdtyVoucherGuess(userId int, now int64) {
	if common.RedisEnabled {
		if err := noteAdtyVoucherGuessRedis(userId, now); err != nil {
			common.SysLog(common.LogText("official voucher guess limiter write failed: %s", err.Error()))
		}
		return
	}
	voucherGuess.mu.Lock()
	defer voucherGuess.mu.Unlock()
	if voucherGuess.fails == nil {
		voucherGuess.fails = map[int][]int64{}
	}
	voucherGuess.fails[userId] = append(recentAdtyVoucherGuesses(userId, now), now)
}

func recentAdtyVoucherGuesses(userId int, now int64) []int64 {
	var kept []int64
	for _, at := range voucherGuess.fails[userId] {
		if now-at < adtyVoucherGuessWindow {
			kept = append(kept, at)
		}
	}
	return kept
}

func allowAdtyVoucherGuessRedis(userId int, now int64) (bool, error) {
	if common.RDB == nil {
		return false, errors.New("Redis client is not initialized")
	}
	result, err := common.RDB.Eval(
		context.Background(),
		adtyVoucherGuessAllowScript,
		[]string{adtyVoucherGuessKey(userId)},
		now,
		adtyVoucherGuessWindow,
		adtyVoucherGuessMax,
	).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func noteAdtyVoucherGuessRedis(userId int, now int64) error {
	if common.RDB == nil {
		return errors.New("Redis client is not initialized")
	}
	return common.RDB.Eval(
		context.Background(),
		adtyVoucherGuessNoteScript,
		[]string{adtyVoucherGuessKey(userId)},
		now,
		adtyVoucherGuessWindow,
	).Err()
}

func prepareAdtyVoucherStatusAudit(actor model.User, voucher model.AdtyVoucher, action, reason, beforeStatus, afterStatus string) (model.AuditLog, error) {
	siteID := voucher.TargetSiteId
	if siteID == 0 {
		siteID = voucher.SourceSiteId
	}
	return model.PrepareOfficialAuditLog(nil, model.OfficialAuditWrite{
		UserId: actor.Id, Username: actor.Username, ActorRole: actor.Role,
		SiteID: model.OfficialAuditSiteIDPointer(siteID), ObjectType: "voucher", ObjectID: strconv.FormatInt(voucher.Id, 10),
		Action: action, Content: action, Reason: reason, Result: model.AuditResultSuccess, Success: true,
		Before: map[string]any{
			"voucher_id": voucher.Id, "purpose": voucher.Purpose, "amount_usd": voucher.AmountUSD.String(),
			"quota": voucher.Quota, "status": beforeStatus, "source_kind": voucher.SourceKind,
			"source_site_id": voucher.SourceSiteId, "target_site_id": voucher.TargetSiteId,
		},
		After: map[string]any{
			"voucher_id": voucher.Id, "purpose": voucher.Purpose, "amount_usd": voucher.AmountUSD.String(),
			"quota": voucher.Quota, "status": afterStatus, "source_kind": voucher.SourceKind,
			"source_site_id": voucher.SourceSiteId, "target_site_id": voucher.TargetSiteId,
		},
	})
}
