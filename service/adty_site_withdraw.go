package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	adtySiteWithdrawMinKey    = "adty_site_withdraw.min_usd"
	adtySiteWithdrawFeeKey    = "adty_site_withdraw.fee_usd"
	adtySiteWithdrawAlipayKey = "adty_site_withdraw.alipay_cny_rate"
	adtySiteWithdrawUSDTKey   = "adty_site_withdraw.usdt_usd_rate"
)

var (
	ErrAdtySiteWithdrawUnset     = errors.New("site withdrawal rules are not configured")
	ErrAdtySiteWithdrawLocked    = errors.New("withdrawal can no longer be cancelled")
	adtySiteWithdrawUSDTNetworks = []string{"TRC20", "BEP20", "POLYGON", "XLAYER"}
)

func isAllowedAdtySiteWithdrawNetwork(network string) bool {
	switch network {
	case "TRC20", "BEP20", "POLYGON", "XLAYER":
		return true
	default:
		return false
	}
}

// AdtySiteWithdrawRules is Root configuration. Empty or non-positive values are unset.
type AdtySiteWithdrawRules struct {
	MinUSD        string `json:"min_usd"`
	FeeUSD        string `json:"fee_usd"`
	AlipayCNYRate string `json:"alipay_cny_rate,omitempty"`
	USDTUSDRate   string `json:"usdt_usd_rate"`
}

type AdtySiteWithdrawInput struct {
	ExpectedPayout *decimal.Decimal
	SiteID         int
	OwnerID        int
	AmountUSD      decimal.Decimal
	Method         string
	Network        string
	AccountNo      string
	AccountName    string
	QRImageURL     string
	ExpectedRate   decimal.Decimal
	IdempotencyKey string
}

// CanonicalReferralAssetURL strips signed query parameters and returns the
// canonical /api/referral-assets/<name> path when raw is a valid asset URL.
func CanonicalReferralAssetURL(raw string) (string, bool) {
	name, ok := extractReferralAssetName(raw)
	if !ok {
		return "", false
	}
	return "/api/referral-assets/" + name, true
}

func resignSiteWithdrawalQR(row *model.AdtySiteWithdrawal, now time.Time) {
	if row == nil || strings.TrimSpace(row.QRImageURL) == "" {
		return
	}
	row.QRImageURL = ResignReferralAssetURLForWithdrawal(row.QRImageURL, row.OwnerUserId, model.ReferralAssetPurposeWithdrawalQR, now)
}

type adtySiteWithdrawConfig struct {
	Min           decimal.Decimal
	Fee           decimal.Decimal
	AlipayCNYRate decimal.Decimal
	AlipayCNYRaw  string
	USDTUSDRate   decimal.Decimal
	USDTUSDRaw    string
}

func SaveAdtySiteWithdrawRules(rules AdtySiteWithdrawRules, operatorID int) error {
	_, err := SaveAdtySiteWithdrawRulesWithAudit(rules, operatorID)
	return err
}

func SaveAdtySiteWithdrawRulesWithAudit(rules AdtySiteWithdrawRules, operatorID int) (string, error) {
	minDec, minRaw, err := requiredPositiveDecimal(rules.MinUSD)
	if err != nil {
		return "", common.NewMessage("Invalid parameters")
	}
	feeDec, feeRaw, err := requiredPositiveDecimal(rules.FeeUSD)
	if err != nil {
		return "", common.NewMessage("Invalid parameters")
	}
	if !feeDec.LessThan(minDec) {
		return "", common.NewMessage("Withdrawal fee must be less than minimum withdrawal amount")
	}
	_, usdtRaw, err := requiredPositiveDecimal(rules.USDTUSDRate)
	if err != nil {
		return "", common.NewMessage("Invalid parameters")
	}
	values := map[string]string{
		adtySiteWithdrawMinKey:  minRaw,
		adtySiteWithdrawFeeKey:  feeRaw,
		adtySiteWithdrawUSDTKey: usdtRaw,
	}
	after := map[string]any{
		"configured":    true,
		"min_usd":       minRaw,
		"fee_usd":       feeRaw,
		"usdt_usd_rate": usdtRaw,
	}
	if alipayTrimmed := strings.TrimSpace(rules.AlipayCNYRate); alipayTrimmed != "" {
		if _, alipayRaw, alipayErr := requiredPositiveDecimal(alipayTrimmed); alipayErr == nil {
			values[adtySiteWithdrawAlipayKey] = alipayRaw
			after["alipay_cny_rate"] = alipayRaw
		}
	}
	var auditEvent *model.AuditLog
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		before := map[string]any{"configured": false}
		if prev, cfgErr := loadAdtySiteWithdrawConfigTx(tx, true); cfgErr == nil {
			before = map[string]any{
				"configured":    true,
				"min_usd":       prev.Min.String(),
				"fee_usd":       prev.Fee.String(),
				"usdt_usd_rate": prev.USDTUSDRaw,
			}
			if prev.AlipayCNYRaw != "" {
				before["alipay_cny_rate"] = prev.AlipayCNYRaw
				if _, ok := after["alipay_cny_rate"]; !ok {
					after["alipay_cny_rate"] = prev.AlipayCNYRaw
				}
			}
		} else if !errors.Is(cfgErr, ErrAdtySiteWithdrawUnset) {
			return cfgErr
		}
		if err := model.SaveOptionsTx(tx, values); err != nil {
			return err
		}
		var err error
		auditEvent, err = appendAdtyOfficialAuditTx(tx, operatorID, 0, "site_withdraw_rules", "global", "adty.site_withdraw.rules.update", "", "", before, after, model.AuditResultSuccess)
		return err
	}); err != nil {
		return "", err
	}
	if err := model.ApplyOptionValues(values); err != nil {
		return "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return eventID, nil
}

func siteWithdrawalAuditSnapshot(row *model.AdtySiteWithdrawal) map[string]any {
	if row == nil {
		return nil
	}
	snap := map[string]any{
		"withdrawal_id":   row.Id,
		"site_id":         row.SiteId,
		"owner_user_id":   row.OwnerUserId,
		"amount_usd":      row.AmountUSD.String(),
		"fee_usd":         row.FeeUSD.String(),
		"net_usd":         row.NetUSD.String(),
		"method":          row.Method,
		"payout_amount":   row.PayoutAmount,
		"payout_currency": row.PayoutCurrency,
		"status":          row.Status,
		"attempt_number":  row.AttemptNumber,
		"attempt_open":    row.AttemptOpen,
	}
	if row.Network != "" {
		snap["network"] = row.Network
	}
	if row.TxID != "" {
		snap["tx_id"] = row.TxID
	}
	if row.QRImageURL != "" {
		snap["qr_image_url"] = row.QRImageURL
	}
	return snap
}

func requiredPositiveDecimal(raw string) (decimal.Decimal, string, error) {
	trimmed := strings.TrimSpace(raw)
	value, err := common.ParseBoundedDecimalString(trimmed, 12, 8)
	if err != nil || !value.IsPositive() {
		return decimal.Zero, "", ErrAdtySiteWithdrawUnset
	}
	return value, trimmed, nil
}

func loadAdtySiteWithdrawConfigTx(tx *gorm.DB, lock bool) (adtySiteWithdrawConfig, error) {
	var cfg adtySiteWithdrawConfig
	var err error
	// Lock/read keys in strict lexicographical order matching SaveOptionsTx
	// (alipay_cny_rate < fee_usd < min_usd < usdt_usd_rate) to avoid deadlocks.
	if alipayRate, alipayRaw, alipayErr := readPositiveOptionTx(tx, adtySiteWithdrawAlipayKey, lock); alipayErr == nil {
		cfg.AlipayCNYRate = alipayRate
		cfg.AlipayCNYRaw = alipayRaw
	} else if !errors.Is(alipayErr, ErrAdtySiteWithdrawUnset) {
		return cfg, alipayErr
	}
	if cfg.Fee, _, err = readPositiveOptionTx(tx, adtySiteWithdrawFeeKey, lock); err != nil {
		return cfg, err
	}
	if cfg.Min, _, err = readPositiveOptionTx(tx, adtySiteWithdrawMinKey, lock); err != nil {
		return cfg, err
	}
	if cfg.USDTUSDRate, cfg.USDTUSDRaw, err = readPositiveOptionTx(tx, adtySiteWithdrawUSDTKey, lock); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func readPositiveOptionTx(tx *gorm.DB, key string, lock bool) (decimal.Decimal, string, error) {
	var (
		raw string
		ok  bool
		err error
	)
	if lock {
		raw, ok, err = model.ReadOptionForUpdate(tx, key)
	} else {
		raw, ok, err = model.ReadOptionInTx(tx, key)
	}
	if err != nil {
		return decimal.Zero, "", err
	}
	if !ok {
		return decimal.Zero, "", ErrAdtySiteWithdrawUnset
	}
	return requiredPositiveDecimal(raw)
}

// AdtySiteWithdrawRulesView is what the owner page may show. Unset rules stay blank.
type AdtySiteWithdrawRulesView struct {
	Configured    bool     `json:"configured"`
	MinUSD        string   `json:"min_usd,omitempty"`
	FeeUSD        string   `json:"fee_usd,omitempty"`
	AlipayCNYRate string   `json:"alipay_cny_rate,omitempty"`
	USDTUSDRate   string   `json:"usdt_usd_rate,omitempty"`
	Methods       []string `json:"methods"`
	Networks      []string `json:"networks"`
}

func LoadAdtySiteWithdrawRules() (AdtySiteWithdrawRulesView, error) {
	view := AdtySiteWithdrawRulesView{
		Methods:  []string{"usdt"},
		Networks: append([]string(nil), adtySiteWithdrawUSDTNetworks...),
	}
	cfg, err := loadAdtySiteWithdrawConfigTx(model.DB, false)
	if errors.Is(err, ErrAdtySiteWithdrawUnset) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	view.Configured = true
	view.MinUSD = cfg.Min.String()
	view.FeeUSD = cfg.Fee.String()
	view.AlipayCNYRate = cfg.AlipayCNYRaw
	view.USDTUSDRate = cfg.USDTUSDRaw
	return view, nil
}

func ListAdtySiteWithdrawals(siteID, ownerID int) ([]model.AdtySiteWithdrawal, error) {
	var rows []model.AdtySiteWithdrawal
	err := model.DB.Scopes(model.AdtyWithdrawalOwnerScope(siteID, ownerID)).Order("id desc").Limit(20).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for i := range rows {
		resignSiteWithdrawalQR(&rows[i], now)
	}
	return rows, nil
}

func PageAdtySiteWithdrawals(siteID, ownerID int, status, number string, offset, limit int) ([]model.AdtySiteWithdrawal, int64, error) {
	q := model.DB.Model(&model.AdtySiteWithdrawal{}).Scopes(model.AdtyWithdrawalOwnerScope(siteID, ownerID))
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if number != "" {
		id, err := strconv.ParseInt(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(number)), "WD-SITE-"), 10, 64)
		if err != nil || id <= 0 {
			return nil, 0, common.NewMessage("Invalid parameters")
		}
		q = q.Where("id = ?", id)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.AdtySiteWithdrawal
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	for i := range rows {
		resignSiteWithdrawalQR(&rows[i], now)
	}
	return rows, total, nil
}

// AdtySiteWithdrawalAdminRow is one withdrawal in Root's review list. The
// subdomain lets the reviewer tell sites apart without opening each one.
type AdtySiteWithdrawalAdminRow struct {
	model.AdtySiteWithdrawal
	Subdomain string `json:"subdomain"`
}

// ListAdtySiteWithdrawalsForAdmin lists every site's withdrawals, newest
// first, optionally narrowed to one status.
func ListAdtySiteWithdrawalsForAdmin(status string, offset, limit int) ([]AdtySiteWithdrawalAdminRow, int64, error) {
	switch status {
	case "", model.AdtySiteWithdrawPending, model.AdtySiteWithdrawPaying, model.AdtySiteWithdrawPaid,
		model.AdtySiteWithdrawRejected, model.AdtySiteWithdrawCancelled:
	default:
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	query := model.DB.Model(&model.AdtySiteWithdrawal{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.AdtySiteWithdrawal
	if err := query.Order("id desc").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	siteIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		siteIDs = append(siteIDs, row.SiteId)
	}
	subdomains := map[int]string{}
	if len(siteIDs) > 0 {
		var sites []model.AdtySite
		if err := model.DB.Select("id", "subdomain").Where("id IN ?", siteIDs).Find(&sites).Error; err != nil {
			return nil, 0, err
		}
		for _, site := range sites {
			subdomains[site.Id] = site.Subdomain
		}
	}
	now := time.Now()
	items := make([]AdtySiteWithdrawalAdminRow, 0, len(rows))
	for _, row := range rows {
		resignSiteWithdrawalQR(&row, now)
		items = append(items, AdtySiteWithdrawalAdminRow{AdtySiteWithdrawal: row, Subdomain: subdomains[row.SiteId]})
	}
	return items, total, nil
}

func siteWithdrawalPayout(net, rate decimal.Decimal, method string) decimal.Decimal {
	precision := int32(2)
	if method == "usdt" {
		precision = 6
	}
	return net.Mul(rate).Round(precision)
}

func QuoteSiteWithdrawal(amount decimal.Decimal, method string) (decimal.Decimal, error) {
	normalized := strings.ToLower(strings.TrimSpace(method))
	if normalized == "" {
		normalized = "usdt"
	}
	if normalized != "usdt" {
		return decimal.Zero, common.NewMessage("Only USDT withdrawal is supported")
	}
	cfg, err := loadAdtySiteWithdrawConfigTx(model.DB, false)
	if err != nil {
		return decimal.Zero, err
	}
	if !amount.Equal(amount.Round(8)) || amount.LessThan(cfg.Min) || !amount.GreaterThan(cfg.Fee) {
		return decimal.Zero, common.NewMessage("Invalid parameters")
	}
	payout := siteWithdrawalPayout(amount.Sub(cfg.Fee), cfg.USDTUSDRate, normalized)
	if !payout.IsPositive() {
		return decimal.Zero, common.NewMessage("The payout amount is too small")
	}
	return payout, nil
}

func adtySiteWithdrawScopedKey(siteID int, rawKey string) string {
	return "s" + strconv.Itoa(siteID) + ":" + common.Sha1([]byte(rawKey))
}

func adtySiteWithdrawLedgerKey(prefix string, siteID int, storedKey string) string {
	full := prefix + storedKey
	if len(full) <= 128 {
		return full
	}
	return prefix + "s" + strconv.Itoa(siteID) + ":" + common.Sha1([]byte(storedKey))
}

func CreateAdtySiteWithdrawal(input AdtySiteWithdrawInput) (*model.AdtySiteWithdrawal, error) {
	method := strings.ToLower(strings.TrimSpace(input.Method))
	if method != "usdt" {
		return nil, common.NewMessage("Only USDT withdrawal is supported")
	}
	amount := input.AmountUSD
	if !amount.IsPositive() || !amount.Equal(amount.Round(8)) {
		return nil, common.NewMessage("Enter a withdrawal amount")
	}
	rawKey := strings.TrimSpace(input.IdempotencyKey)
	if rawKey == "" || len(rawKey) > 128 {
		return nil, common.NewMessage("Invalid idempotency key")
	}
	scopedKey := adtySiteWithdrawScopedKey(input.SiteID, rawKey)
	accountNo := strings.TrimSpace(input.AccountNo)
	accountName := ""
	normalizedQR := ""
	network := strings.ToUpper(strings.TrimSpace(input.Network))
	if !isAllowedAdtySiteWithdrawNetwork(network) {
		return nil, common.NewMessage("Unsupported USDT network")
	}
	if !ValidateUSDTAddress(network, accountNo) {
		return nil, common.NewMessage("The address does not match the {{network}} network", map[string]any{"network": network})
	}
	input.Network = network
	var created *model.AdtySiteWithdrawal
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, input.SiteID).Error; err != nil {
			return err
		}
		if site.OwnerUserId != input.OwnerID {
			var count int64
			if err := ArchivedAdtyOwnerSites(tx, input.OwnerID).Where("id = ?", site.Id).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 || input.OwnerID <= 0 {
				return ErrAdtySiteNotOwner
			}
		}
		var existing []model.AdtySiteWithdrawal
		if err := tx.Where("idempotency_key = ? OR (site_id = ? AND idempotency_key = ?)", scopedKey, input.SiteID, rawKey).Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		if len(existing) > 0 {
			existingOwner, err := model.ResolveAdtySiteFinancialOwnerTx(tx, existing[0].SiteId, existing[0].OwnerUserId)
			if err != nil {
				return err
			}
			existingPayout, payoutErr := decimal.NewFromString(existing[0].PayoutAmount)
			if input.ExpectedPayout != nil && (payoutErr != nil || !input.ExpectedPayout.Equal(existingPayout)) {
				return model.ErrAdtyIdempotencyConflict
			}
			existingRate, rateErr := decimal.NewFromString(existing[0].Rate)
			existingQR := strings.TrimSpace(existing[0].QRImageURL)
			if canonical, ok := CanonicalReferralAssetURL(existingQR); ok {
				existingQR = canonical
			}
			if existing[0].SiteId != input.SiteID || existingOwner != input.OwnerID ||
				!existing[0].AmountUSD.Equal(amount) || existing[0].Method != method ||
				existing[0].Network != input.Network || existing[0].AccountNo != accountNo ||
				existing[0].AccountName != accountName || existingQR != normalizedQR ||
				rateErr != nil || !existingRate.Equal(input.ExpectedRate) {
				return model.ErrAdtyIdempotencyConflict
			}
			created = &existing[0]
			return nil
		}
		currentOwner := site.OwnerUserId == input.OwnerID
		if currentOwner && site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused && site.Status != model.AdtySiteStatusClosing && site.Status != model.AdtySiteStatusClosed {
			return common.NewMessage("No permission to perform this action")
		}
		if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: site.Id, AccountID: input.OwnerID}); err != nil {
			return err
		}
		cfg, err := loadAdtySiteWithdrawConfigTx(tx, true)
		if err != nil {
			return err
		}
		if amount.LessThan(cfg.Min) || !amount.GreaterThan(cfg.Fee) {
			return common.NewMessage("The amount is below the configured withdrawal minimum")
		}
		rate := cfg.USDTUSDRate
		rateRaw := cfg.USDTUSDRaw
		currency := "USDT"
		if !rate.Equal(input.ExpectedRate) {
			return &RateChangedError{Change: ReferralRateChange{Rate: rate.String(), UpdatedAt: time.Now().Unix()}}
		}
		net := amount.Sub(cfg.Fee)
		payout := siteWithdrawalPayout(net, rate, method)
		if !payout.IsPositive() {
			return common.NewMessage("The payout amount is too small")
		}
		if input.ExpectedPayout != nil && !input.ExpectedPayout.Equal(payout) {
			return common.NewMessage("Withdrawal quote changed. Confirm the updated amount.")
		}
		// ISS-R1-0018: freeze earnings on application without decrementing Balance until paid.
		if _, err := applyAdtySiteWalletChangeTx(tx, site.Id, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "earning_withdraw_freeze", HoldDelta: amount, IdempotencyKey: adtySiteWithdrawLedgerKey("site-withdraw:", site.Id, scopedKey), OperatorID: input.OwnerID, RefType: "site_withdraw", RefID: scopedKey,
		}); err != nil {
			return err
		}
		key := scopedKey
		row := &model.AdtySiteWithdrawal{
			SiteId: site.Id, OwnerUserId: input.OwnerID, AmountUSD: amount, FeeUSD: cfg.Fee, NetUSD: net,
			Method: method, Network: input.Network, AccountNo: accountNo, AccountName: accountName,
			QRImageURL: normalizedQR, Rate: rateRaw, PayoutAmount: payout.String(), PayoutCurrency: currency,
			Status: model.AdtySiteWithdrawPending, IdempotencyKey: key,
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		auditEvent, err = appendAdtyOfficialAuditTx(tx, input.OwnerID, site.Id, "site_withdrawal", strconv.FormatInt(row.Id, 10), "adty.site_withdraw.create", "", "", nil, siteWithdrawalAuditSnapshot(row), model.AuditResultSuccess)
		if err != nil {
			return err
		}
		created = row
		return nil
	})
	if err == nil && auditEvent != nil {
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return created, err
}

func MarkAdtySiteWithdrawalPaying(id int64, reviewerID int, expected ...int) error {
	_, _, err := MarkAdtySiteWithdrawalPayingWithAudit(id, reviewerID, expected...)
	return err
}

func MarkAdtySiteWithdrawalPayingWithAudit(id int64, reviewerID int, expected ...int) (string, bool, error) {
	var auditEvent *model.AuditLog
	replayed := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
			return err
		}
		previous := 0
		if len(expected) > 0 {
			previous = expected[0]
		}
		if row.Status == model.AdtySiteWithdrawPaying && row.AttemptNumber == previous+1 {
			replayed = true
			return nil
		}
		if row.Status != model.AdtySiteWithdrawPending || row.AttemptNumber != previous || row.AttemptOpen {
			return ErrAdtySiteWithdrawLocked
		}
		if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: row.SiteId, AccountID: row.OwnerUserId}); err != nil {
			return err
		}
		before := siteWithdrawalAuditSnapshot(&row)
		now := time.Now().Unix()
		attempt := model.AdtySitePayoutAttempt{WithdrawalId: row.Id, Number: previous + 1, AmountUSD: row.AmountUSD, Status: "unknown", StartedBy: reviewerID, CreatedAt: now}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]any{"status": model.AdtySiteWithdrawPaying, "attempt_number": attempt.Number, "attempt_open": true, "reviewer_id": reviewerID, "reviewed_at": now}).Error; err != nil {
			return err
		}
		row.Status = model.AdtySiteWithdrawPaying
		row.AttemptNumber = attempt.Number
		row.AttemptOpen = true
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, reviewerID, row.SiteId, "site_withdrawal", strconv.FormatInt(row.Id, 10), "adty.site_withdraw.paying", "", "", before, siteWithdrawalAuditSnapshot(&row), model.AuditResultSuccess)
		return auditErr
	})
	eventID := ""
	if err == nil && auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return eventID, replayed, err
}

// Recall changes the review state while the outstanding attempt keeps the hold.
func RecallAdtySiteWithdrawalToPending(id int64, reviewerID int, reason string, expected ...int) error {
	_, _, err := RecallAdtySiteWithdrawalToPendingWithAudit(id, reviewerID, reason, expected...)
	return err
}

func RecallAdtySiteWithdrawalToPendingWithAudit(id int64, reviewerID int, reason string, expected ...int) (string, bool, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", false, common.NewMessage("Recall reason is required")
	}
	var auditEvent *model.AuditLog
	replayed := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
			return err
		}
		if len(expected) > 0 && expected[0] != row.AttemptNumber {
			return ErrAdtySiteWithdrawLocked
		}
		if row.Status == model.AdtySiteWithdrawPending && row.AttemptOpen && row.ProofNote == reason {
			replayed = true
			return nil
		}
		if row.Status != model.AdtySiteWithdrawPaying {
			return ErrAdtySiteWithdrawLocked
		}
		before := siteWithdrawalAuditSnapshot(&row)
		now := time.Now().Unix()
		if row.AttemptNumber > 0 {
			if err := tx.Model(&model.AdtySitePayoutAttempt{}).Where("withdrawal_id = ? AND number = ? AND status = ?", row.Id, row.AttemptNumber, "unknown").Updates(map[string]any{"recalled_by": reviewerID, "recalled_at": now, "recall_reason": reason}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&row).Updates(map[string]any{"status": model.AdtySiteWithdrawPending, "attempt_open": true, "reviewer_id": reviewerID, "reviewed_at": now, "proof_note": reason}).Error; err != nil {
			return err
		}
		row.Status = model.AdtySiteWithdrawPending
		row.AttemptOpen = true
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, reviewerID, row.SiteId, "site_withdrawal", strconv.FormatInt(row.Id, 10), "adty.site_withdraw.recall", reason, "", before, siteWithdrawalAuditSnapshot(&row), model.AuditResultSuccess)
		return auditErr
	})
	eventID := ""
	if err == nil && auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return eventID, replayed, err
}

func ConfirmAdtySiteWithdrawalFailed(id int64, reviewerID, attempt int, reason, reference string) error {
	_, err := ConfirmAdtySiteWithdrawalFailedWithAudit(id, reviewerID, attempt, reason, reference)
	return err
}

func ConfirmAdtySiteWithdrawalFailedWithAudit(id int64, reviewerID, attempt int, reason, reference string) (string, error) {
	reason, reference = strings.TrimSpace(reason), strings.TrimSpace(reference)
	if reason == "" || reference == "" {
		return "", common.NewMessage("Enter the failed payout reference")
	}
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
			return err
		}
		if (row.Status != model.AdtySiteWithdrawPaying && !(row.Status == model.AdtySiteWithdrawPending && row.AttemptOpen)) || attempt != row.AttemptNumber {
			return ErrAdtySiteWithdrawLocked
		}
		before := siteWithdrawalAuditSnapshot(&row)
		if err := resolveSitePayoutAttempt(tx, &row, reviewerID, "failed", reference, reason); err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]any{"status": model.AdtySiteWithdrawPending, "attempt_open": false, "reviewer_id": reviewerID, "reviewed_at": time.Now().Unix(), "proof_note": reason}).Error; err != nil {
			return err
		}
		row.Status = model.AdtySiteWithdrawPending
		row.AttemptOpen = false
		after := siteWithdrawalAuditSnapshot(&row)
		after["failed_reference"] = reference
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, reviewerID, row.SiteId, "site_withdrawal", strconv.FormatInt(row.Id, 10), "adty.site_withdraw.failed", reason, "", before, after, model.AuditResultSuccess)
		return auditErr
	})
	eventID := ""
	if err == nil && auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return eventID, err
}

func resolveSitePayoutAttempt(tx *gorm.DB, row *model.AdtySiteWithdrawal, reviewerID int, status, reference, proof string) error {
	now := time.Now().Unix()
	if row.AttemptNumber == 0 {
		// An old paying withdrawal predates attempt numbering.
		return tx.Create(&model.AdtySitePayoutAttempt{WithdrawalId: row.Id, Number: 0, AmountUSD: row.AmountUSD, Status: status, StartedBy: row.ReviewerId, CreatedAt: row.ReviewedAt, ResolvedBy: reviewerID, ResolvedAt: now, Reference: reference, Proof: proof}).Error
	}
	res := tx.Model(&model.AdtySitePayoutAttempt{}).Where("withdrawal_id = ? AND number = ? AND status = ?", row.Id, row.AttemptNumber, "unknown").Updates(map[string]any{"status": status, "resolved_by": reviewerID, "resolved_at": now, "reference": reference, "proof": proof})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrAdtySiteWithdrawLocked
	}
	return nil
}

func MarkAdtySiteWithdrawalPaid(id int64, reviewerID int, txID, proof string, attempt ...int) error {
	_, _, err := MarkAdtySiteWithdrawalPaidWithAudit(id, reviewerID, txID, proof, attempt...)
	return err
}

func MarkAdtySiteWithdrawalPaidWithAudit(id int64, reviewerID int, txID, proof string, attempt ...int) (string, bool, error) {
	txID = strings.TrimSpace(txID)
	proof = strings.TrimSpace(proof)
	if txID == "" && proof == "" {
		return "", false, common.NewMessage("Enter the payout reference")
	}
	var auditEvent *model.AuditLog
	replayed := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
			return err
		}
		number := 0
		if len(attempt) > 0 {
			number = attempt[0]
		}
		if number != row.AttemptNumber {
			return ErrAdtySiteWithdrawLocked
		}
		if row.Status == model.AdtySiteWithdrawPaid && row.TxID == txID && row.ProofNote == proof {
			replayed = true
			return nil
		}
		if row.Status != model.AdtySiteWithdrawPaying && !(row.Status == model.AdtySiteWithdrawPending && row.AttemptOpen) {
			return ErrAdtySiteWithdrawLocked
		}
		if row.Method == "usdt" && txID == "" {
			return common.NewMessage("Enter the payout reference")
		}
		if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: row.SiteId, AccountID: row.OwnerUserId}); err != nil {
			return err
		}
		reference := txID
		if reference == "" {
			if canonical, ok := CanonicalReferralAssetURL(proof); ok {
				reference = canonical
			} else {
				reference = proof
			}
		}
		rk := fmt.Sprintf("%x", sha256.Sum256([]byte(row.Method+"|"+row.Network+"|"+reference)))
		var dupCount int64
		if err := tx.Model(&model.AdtySiteWithdrawal{}).Where("receipt_key = ? AND id <> ?", rk, row.Id).Count(&dupCount).Error; err != nil {
			return err
		}
		if dupCount > 0 {
			return common.NewMessage("This payout reference was already used for another withdrawal")
		}
		before := siteWithdrawalAuditSnapshot(&row)
		if err := resolveSitePayoutAttempt(tx, &row, reviewerID, "paid", txID, proof); err != nil {
			return err
		}
		if _, err := applyAdtySiteWalletChangeTx(tx, row.SiteId, model.AdtySiteWalletEarnings, row.AmountUSD.Neg(), AdtySiteWalletChange{
			Type: "earning_withdraw_paid", HoldDelta: row.AmountUSD.Neg(), IdempotencyKey: adtySiteWithdrawLedgerKey("site-withdraw-paid:", row.SiteId, row.IdempotencyKey),
			OperatorID: reviewerID, Reason: proof, RefType: "site_withdraw", RefID: row.IdempotencyKey,
		}); err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]any{
			"status": model.AdtySiteWithdrawPaid, "attempt_open": false, "reviewer_id": reviewerID, "tx_id": txID, "proof_note": proof, "receipt_key": &rk, "paid_at": time.Now().Unix(),
		}).Error; err != nil {
			if isAdtyUniqueConflictError(err) {
				return common.NewMessage("This payout reference was already used for another withdrawal")
			}
			return err
		}
		row.Status = model.AdtySiteWithdrawPaid
		row.AttemptOpen = false
		row.TxID = txID
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, reviewerID, row.SiteId, "site_withdrawal", strconv.FormatInt(row.Id, 10), "adty.site_withdraw.paid", proof, "", before, siteWithdrawalAuditSnapshot(&row), model.AuditResultSuccess)
		return auditErr
	})
	eventID := ""
	if err == nil && auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return eventID, replayed, err
}

func CancelAdtySiteWithdrawal(siteID, ownerID int, id int64) error {
	return releaseAdtySiteWithdrawal(id, siteID, ownerID, model.AdtySiteWithdrawCancelled, true)
}

func RejectAdtySiteWithdrawal(id int64, reviewerID int, reason string) error {
	_, err := RejectAdtySiteWithdrawalWithAudit(id, reviewerID, reason)
	return err
}

func RejectAdtySiteWithdrawalWithAudit(id int64, reviewerID int, reason string) (string, error) {
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
			return err
		}
		if row.Status != model.AdtySiteWithdrawPending || row.AttemptOpen {
			return ErrAdtySiteWithdrawLocked
		}
		before := siteWithdrawalAuditSnapshot(&row)
		if _, err := applyAdtySiteWalletChangeTx(tx, row.SiteId, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "earning_withdraw_release", HoldDelta: row.AmountUSD.Neg(), IdempotencyKey: adtySiteWithdrawLedgerKey("site-withdraw-release:", row.SiteId, row.IdempotencyKey), OperatorID: reviewerID, Reason: reason, RefType: "site_withdraw", RefID: row.IdempotencyKey,
		}); err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]any{
			"status": model.AdtySiteWithdrawRejected, "reviewer_id": reviewerID, "reviewed_at": time.Now().Unix(), "proof_note": reason,
		}).Error; err != nil {
			return err
		}
		row.Status = model.AdtySiteWithdrawRejected
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, reviewerID, row.SiteId, "site_withdrawal", strconv.FormatInt(row.Id, 10), "adty.site_withdraw.reject", reason, "", before, siteWithdrawalAuditSnapshot(&row), model.AuditResultSuccess)
		return auditErr
	})
	eventID := ""
	if err == nil && auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return eventID, err
}

func releaseAdtySiteWithdrawal(id int64, siteID, ownerID int, status string, ownerOnly bool) error {
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
			return err
		}
		frozenOwner, err := model.ResolveAdtySiteFinancialOwnerTx(tx, row.SiteId, row.OwnerUserId)
		if err != nil {
			return err
		}
		if row.SiteId != siteID || (ownerOnly && frozenOwner != ownerID) {
			return common.NewMessage("No permission to perform this action")
		}
		if row.Status != model.AdtySiteWithdrawPending || row.AttemptOpen {
			return ErrAdtySiteWithdrawLocked
		}
		before := siteWithdrawalAuditSnapshot(&row)
		if _, err := applyAdtySiteWalletChangeTx(tx, row.SiteId, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "earning_withdraw_release", HoldDelta: row.AmountUSD.Neg(), IdempotencyKey: adtySiteWithdrawLedgerKey("site-withdraw-release:", row.SiteId, row.IdempotencyKey), OperatorID: ownerID, RefType: "site_withdraw", RefID: row.IdempotencyKey,
		}); err != nil {
			return err
		}
		if err := tx.Model(&row).Update("status", status).Error; err != nil {
			return err
		}
		row.Status = status
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, ownerID, row.SiteId, "site_withdrawal", strconv.FormatInt(row.Id, 10), "adty.site_withdraw.cancel", "", "", before, siteWithdrawalAuditSnapshot(&row), model.AuditResultSuccess)
		return auditErr
	})
	if err == nil && auditEvent != nil {
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return err
}
