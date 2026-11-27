package adty_setting

import (
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 这四个键必须与实际写入 options 表的键一致，否则重启后配置回退。
const (
	TopupSettingKeyMethodBonus         = "adty_topup_setting.method_bonus"
	TopupSettingKeySiteMethodAuth      = "adty_topup_setting.site_method_auth"
	TopupSettingKeySiteMethodBonus     = "adty_topup_setting.site_method_bonus"
	TopupSettingKeySiteBonusMarginFill = "adty_topup_setting.site_bonus_margin_fill"
)

var (
	authoritativeBonusDBMu sync.RWMutex
	authoritativeBonusDB   *gorm.DB
)

// SetAuthoritativeBonusDB binds the SQL database handle used to read
// authoritative bonus policy rows when callers do not pass an explicit tx.
func SetAuthoritativeBonusDB(db *gorm.DB) {
	authoritativeBonusDBMu.Lock()
	authoritativeBonusDB = db
	authoritativeBonusDBMu.Unlock()
}

func getAuthoritativeBonusDB() *gorm.DB {
	authoritativeBonusDBMu.RLock()
	defer authoritativeBonusDBMu.RUnlock()
	return authoritativeBonusDB
}

func bonusTableExists(db *gorm.DB, tableName string) (bool, error) {
	if db == nil || strings.TrimSpace(tableName) == "" {
		return false, nil
	}
	var count int64
	var err error
	dialector := ""
	if db.Dialector != nil {
		dialector = db.Dialector.Name()
	}
	switch dialector {
	case "postgres":
		err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = CURRENT_SCHEMA() AND table_name = ?", tableName).Scan(&count).Error
	case "mysql":
		err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", tableName).Scan(&count).Error
	default:
		err = db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", tableName).Scan(&count).Error
	}
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func readOptionFromDBStrict(db *gorm.DB, key string) (string, bool, error) {
	if db == nil {
		return "", false, nil
	}
	exists, err := bonusTableExists(db, "options")
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, nil
	}
	var row struct {
		Value string `gorm:"column:value"`
	}
	res := db.Table("options").Select("value").Where(map[string]any{"key": key}).Limit(1).Find(&row)
	if res.Error != nil {
		return "", false, res.Error
	}
	if res.RowsAffected == 0 {
		return "", false, nil
	}
	return row.Value, true, nil
}

func readOptionFromDB(db *gorm.DB, key string) (string, bool) {
	val, ok, err := readOptionFromDBStrict(db, key)
	if err != nil || !ok {
		return "", false
	}
	return val, true
}

// MethodBonusRule is one site's gift switch and rate for a payment method.
// Rate is a decimal fraction in [0, 1], not a percent and not an amount tier.
type MethodBonusRule struct {
	Enabled bool   `json:"enabled"`
	Rate    string `json:"rate"`
}

// BonusPolicySnapshot captures the authoritative bonus authorization and rate
// evaluated inside the order-creation transaction (G.4.2 / G.4.11 / G.4.14).
type BonusPolicySnapshot struct {
	SiteID            int             `json:"site_id"`
	IsTeam            bool            `json:"is_team"`
	PaymentMethod     string          `json:"payment_method"`
	CanonicalMethod   string          `json:"canonical_method"`
	MainAuthorized    bool            `json:"main_authorized"`
	SiteAuthorized    bool            `json:"site_authorized"`
	Enabled           bool            `json:"enabled"`
	Rate              string          `json:"rate"`
	BonusRate         decimal.Decimal `json:"bonus_rate"`
	FaceUSD           decimal.Decimal `json:"face_usd"`
	BonusUSD          decimal.Decimal `json:"bonus_usd"`
	BonusQuota        int64           `json:"bonus_quota"`
	MarginFillAllowed bool            `json:"margin_fill_allowed"`
}

// ParseMainMethodBonus parses the main-site bonus rule for method from raw JSON.
func ParseMainMethodBonus(rawMethodBonus, method string) (MethodBonusRule, bool, error) {
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return MethodBonusRule{}, false, err
	}
	var rules map[string]MethodBonusRule
	if err := unmarshalBonusSetting(rawMethodBonus, &rules); err != nil {
		return MethodBonusRule{}, false, err
	}
	if rules == nil {
		return MethodBonusRule{}, false, nil
	}
	rule, ok := rules[key]
	return rule, ok, nil
}

// ParseSiteMethodBonusAllowed reports whether siteID and method are valid for
// subsite bonus rules. Per-site main-site authorization (site_method_auth) has
// been removed; main-site enabled payment method checks happen at the service layer.
func ParseSiteMethodBonusAllowed(_ string, siteID int, method string) (bool, error) {
	if siteID <= 0 {
		return false, nil
	}
	if _, err := canonicalBonusMethod(method); err != nil {
		return false, err
	}
	return true, nil
}

// ParseSiteMethodBonusRule parses a site-owner bonus rule for siteID and method
// from raw site_method_bonus JSON without requiring per-site authorization.
func ParseSiteMethodBonusRule(rawSiteMethodBonus string, siteID int, method string) (MethodBonusRule, bool, error) {
	if siteID <= 0 {
		return MethodBonusRule{}, false, nil
	}
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return MethodBonusRule{}, false, err
	}
	var all map[int]map[string]MethodBonusRule
	if err := unmarshalBonusSetting(rawSiteMethodBonus, &all); err != nil {
		return MethodBonusRule{}, false, err
	}
	if all == nil || all[siteID] == nil {
		return MethodBonusRule{}, false, nil
	}
	rule, ok := all[siteID][key]
	return rule, ok, nil
}

// ParseSiteMethodBonus parses a site-owner rule from raw site_method_bonus JSON.
// rawSiteMethodAuth is ignored now that per-site authorization has been removed.
func ParseSiteMethodBonus(_, rawSiteMethodBonus string, siteID int, method string) (MethodBonusRule, bool, error) {
	return ParseSiteMethodBonusRule(rawSiteMethodBonus, siteID, method)
}

// ParseSiteBonusMarginFillAllowed parses whether Root authorized margin fill for siteID.
func ParseSiteBonusMarginFillAllowed(rawMarginFill string, siteID int) (bool, error) {
	if siteID <= 0 {
		return false, nil
	}
	var allowed map[int]bool
	if err := unmarshalBonusSetting(rawMarginFill, &allowed); err != nil {
		return false, err
	}
	return allowed != nil && allowed[siteID], nil
}

// MainMethodBonusMap returns supported main-site bonus rules (USDT only) from
// the authoritative DB when wired, falling back to the in-memory option snapshot.
func MainMethodBonusMap() map[string]MethodBonusRule {
	if raw, ok := readOptionFromDB(getAuthoritativeBonusDB(), TopupSettingKeyMethodBonus); ok {
		var rules map[string]MethodBonusRule
		if err := unmarshalBonusSetting(raw, &rules); err == nil {
			return filterSupportedMethodBonusMap(rules)
		}
	}
	ts := GetTopupSetting()
	return filterSupportedMethodBonusMap(ts.MethodBonus)
}

// SiteMethodBonusMap returns one site's supported bonus rules (USDT only) from
// the authoritative DB when wired, falling back to the in-memory option snapshot.
func SiteMethodBonusMap(siteID int) map[string]MethodBonusRule {
	if siteID <= 0 {
		return map[string]MethodBonusRule{}
	}
	if raw, ok := readOptionFromDB(getAuthoritativeBonusDB(), TopupSettingKeySiteMethodBonus); ok {
		var all map[int]map[string]MethodBonusRule
		if err := unmarshalBonusSetting(raw, &all); err == nil {
			if all == nil {
				return map[string]MethodBonusRule{}
			}
			return filterSupportedMethodBonusMap(all[siteID])
		}
	}
	ts := GetTopupSetting()
	if ts.SiteMethodBonus == nil {
		return map[string]MethodBonusRule{}
	}
	return filterSupportedMethodBonusMap(ts.SiteMethodBonus[siteID])
}

// MainMethodBonus is the main-site rule. An absent or unsupported method is off.
func MainMethodBonus(method string) (MethodBonusRule, bool) {
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return MethodBonusRule{}, false
	}
	if raw, ok := readOptionFromDB(getAuthoritativeBonusDB(), TopupSettingKeyMethodBonus); ok {
		if rule, found, err := ParseMainMethodBonus(raw, key); err == nil {
			return rule, found
		}
	}
	ts := GetTopupSetting()
	if ts.MethodBonus == nil {
		return MethodBonusRule{}, false
	}
	rule, ok := ts.MethodBonus[key]
	return rule, ok
}

// SiteMethodBonusAllowed reports whether siteID and method are valid for subsite
// bonus configuration. Per-site main-site authorization (site_method_auth) has been removed.
func SiteMethodBonusAllowed(siteID int, method string) bool {
	if siteID <= 0 {
		return false
	}
	_, err := canonicalBonusMethod(method)
	return err == nil
}

// MergeMainMethodBonus 校验一条主站规则，并把它合并进 current。
//
// current 必须是同一个事务里加行锁从 options 表读到的值，不能是本进程的内存
// 快照：这里写回去的是整张表，基准一旦陈旧，另一节点刚保存的规则就会被静默
// 抹掉（ISS-R4-0099）。它只算不写，内存仍由配置层在事务提交之后按落库值同步。
func MergeMainMethodBonus(current string, method string, enabled bool, rate string) (string, error) {
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return "", err
	}
	parsed, err := parseBonusRate(rate, enabled)
	if err != nil {
		return "", err
	}
	var next map[string]MethodBonusRule
	if err := unmarshalBonusSetting(current, &next); err != nil {
		return "", err
	}
	next = filterSupportedMethodBonusMap(next)
	next[key] = MethodBonusRule{Enabled: enabled, Rate: parsed}
	return marshalBonusSetting(next)
}

// MergeSiteMethodBonusWithAuth validates and merges a site bonus rule into currentBonus.
// currentAuth is ignored now that per-site main-site authorization has been removed.
func MergeSiteMethodBonusWithAuth(currentBonus, _ string, siteID int, method string, enabled bool, rate string) (string, error) {
	return MergeSiteMethodBonus(currentBonus, siteID, method, enabled, rate)
}

// MergeSiteMethodBonus 校验一条站长规则，并把它合并进 current。
func MergeSiteMethodBonus(current string, siteID int, method string, enabled bool, rate string) (string, error) {
	if siteID <= 0 {
		return "", errBonusMethod("site is required")
	}
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return "", err
	}
	return mergeAuthorizedSiteMethodBonus(current, siteID, key, enabled, rate)
}

func mergeAuthorizedSiteMethodBonus(current string, siteID int, key string, enabled bool, rate string) (string, error) {
	parsed, err := parseBonusRate(rate, enabled)
	if err != nil {
		return "", err
	}
	var next map[int]map[string]MethodBonusRule
	if err := unmarshalBonusSetting(current, &next); err != nil {
		return "", err
	}
	if next == nil {
		next = map[int]map[string]MethodBonusRule{}
	}
	next[siteID] = filterSupportedMethodBonusMap(next[siteID])
	next[siteID][key] = MethodBonusRule{Enabled: enabled, Rate: parsed}
	return marshalBonusSetting(next)
}

// MergeSiteBonusMarginFill merges a site's margin-fill authorization into current JSON.
func MergeSiteBonusMarginFill(current string, siteID int, allowed bool) (string, error) {
	if siteID <= 0 {
		return "", errBonusMethod("site is required")
	}
	var next map[int]bool
	if err := unmarshalBonusSetting(current, &next); err != nil {
		return "", err
	}
	if next == nil {
		next = map[int]bool{}
	}
	if allowed {
		next[siteID] = true
	} else {
		delete(next, siteID)
	}
	return marshalBonusSetting(next)
}

// RemoveSiteMethodBonus deletes all per-method bonus rules stored for siteID in current JSON.
func RemoveSiteMethodBonus(current string, siteID int) (string, bool, error) {
	if siteID <= 0 || strings.TrimSpace(current) == "" {
		return current, false, nil
	}
	var next map[int]map[string]MethodBonusRule
	if err := unmarshalBonusSetting(current, &next); err != nil {
		return "", false, err
	}
	if _, exists := next[siteID]; !exists {
		return current, false, nil
	}
	delete(next, siteID)
	encoded, err := marshalBonusSetting(next)
	if err != nil {
		return "", false, err
	}
	return encoded, true, nil
}

// LockAndMergeSiteMethodBonusTx locks the authoritative site_method_bonus row in
// the SQL transaction, validates the site and rule, and writes the updated rule.
func LockAndMergeSiteMethodBonusTx(tx *gorm.DB, siteID int, method string, enabled bool, rate string) (string, error) {
	if tx == nil {
		return "", errBonusMethod("transaction is required")
	}
	if siteID <= 0 {
		return "", errBonusMethod("site is required")
	}
	if err := verifyTrustedBonusSiteTx(tx, siteID); err != nil {
		return "", err
	}
	rawValues, err := loadBonusOptionsTx(tx, []string{
		TopupSettingKeySiteMethodBonus,
	}, true)
	if err != nil {
		return "", err
	}
	bonusJSON, hasBonus := rawValues[TopupSettingKeySiteMethodBonus]
	if !hasBonus {
		ts := GetTopupSetting()
		if ts.SiteMethodBonus != nil {
			bonusJSON, _ = marshalBonusSetting(ts.SiteMethodBonus)
		}
	}
	merged, err := MergeSiteMethodBonus(bonusJSON, siteID, method, enabled, rate)
	if err != nil {
		return "", err
	}
	if err := upsertBonusOptionRowTx(tx, TopupSettingKeySiteMethodBonus, merged); err != nil {
		return "", err
	}
	return merged, nil
}

// LoadBonusPolicySnapshotTx evaluates the bonus policy for (siteID, isTeam, method, faceUSD)
// inside tx, optionally locking the option rows so that concurrent rate updates
// are serialized with order creation (G.4.2 / G.4.11 / G.4.14).
func LoadBonusPolicySnapshotTx(tx *gorm.DB, siteID int, isTeam bool, method string, faceUSD decimal.Decimal, lock bool) (BonusPolicySnapshot, error) {
	faceUSD = faceUSD.Round(8)
	snap := BonusPolicySnapshot{
		SiteID:        siteID,
		IsTeam:        isTeam,
		PaymentMethod: strings.TrimSpace(method),
		BonusRate:     decimal.Zero,
		FaceUSD:       faceUSD,
		BonusUSD:      decimal.Zero,
	}
	if siteID < 0 {
		return snap, errBonusMethod("invalid site")
	}
	if siteID > 0 && tx != nil {
		if err := verifyTrustedBonusSiteTx(tx, siteID); err != nil {
			return snap, err
		}
	}
	if isTeam || snap.PaymentMethod == "" || faceUSD.LessThanOrEqual(decimal.Zero) {
		return snap, nil
	}
	key, err := canonicalBonusMethod(snap.PaymentMethod)
	if err != nil {
		return snap, nil
	}
	snap.CanonicalMethod = key

	keys := []string{
		TopupSettingKeyMethodBonus,
		TopupSettingKeySiteBonusMarginFill,
		TopupSettingKeySiteMethodBonus,
	}
	rawValues, err := loadBonusOptionsTx(tx, keys, lock)
	if err != nil {
		return snap, err
	}

	var rule MethodBonusRule
	var ok bool
	if siteID == 0 {
		if raw, hasRow := rawValues[TopupSettingKeyMethodBonus]; hasRow {
			rule, ok, err = ParseMainMethodBonus(raw, key)
			if err != nil {
				return snap, err
			}
		} else {
			ts := GetTopupSetting()
			if ts.MethodBonus != nil {
				rule, ok = ts.MethodBonus[key]
			}
		}
		snap.MainAuthorized = ok && rule.Enabled
	} else {
		snap.SiteAuthorized = true
		if rawFill, hasFill := rawValues[TopupSettingKeySiteBonusMarginFill]; hasFill {
			snap.MarginFillAllowed, _ = ParseSiteBonusMarginFillAllowed(rawFill, siteID)
		} else {
			ts := GetTopupSetting()
			snap.MarginFillAllowed = ts.SiteBonusMarginFill != nil && ts.SiteBonusMarginFill[siteID]
		}
		rawBonus, hasBonus := rawValues[TopupSettingKeySiteMethodBonus]
		if !hasBonus {
			if ts := GetTopupSetting(); ts.SiteMethodBonus != nil {
				rawBonus, _ = marshalBonusSetting(ts.SiteMethodBonus)
			}
		}
		rule, ok, err = ParseSiteMethodBonusRule(rawBonus, siteID, key)
		if err != nil {
			return snap, err
		}
	}
	if !ok || !rule.Enabled {
		return snap, nil
	}
	rateDec, err := common.ParseBoundedDecimalString(rule.Rate, 4, 8)
	if err != nil || rateDec.LessThanOrEqual(decimal.Zero) || rateDec.GreaterThan(decimal.NewFromInt(1)) {
		return snap, nil
	}
	bonusUSD := faceUSD.Mul(rateDec).Round(8)
	bonusQuotaInt, err := common.WalletQuotaFromDecimalStrict(bonusUSD.Mul(common.QuotaPerUnitDecimal()).Floor())
	if err != nil {
		return snap, err
	}
	bonusQuota := int64(bonusQuotaInt)
	if !bonusUSD.IsPositive() || bonusQuota <= 0 {
		return snap, nil
	}
	snap.Enabled = true
	snap.Rate = rateDec.String()
	snap.BonusRate = rateDec
	snap.BonusUSD = bonusUSD
	snap.BonusQuota = bonusQuota
	return snap, nil
}

// CreateBonusOrderSnapshotTx confirms the current bonus authorization and rate
// inside the same transaction as new order creation and writes the frozen
// snapshot onto the newly created adty_topup_orders row if present. Existing
// historical orders are never mutated by later policy updates (G.4.6 / G.4.14).
func CreateBonusOrderSnapshotTx(tx *gorm.DB, tradeNo string, siteID int, isTeam bool, method string, faceUSD decimal.Decimal) (BonusPolicySnapshot, error) {
	if tx == nil {
		return BonusPolicySnapshot{}, errBonusMethod("transaction is required")
	}
	snap, err := LoadBonusPolicySnapshotTx(tx, siteID, isTeam, method, faceUSD, true)
	if err != nil {
		return snap, err
	}
	tradeNo = strings.TrimSpace(tradeNo)
	if tradeNo != "" {
		hasOrdersTable, tableErr := bonusTableExists(tx, "adty_topup_orders")
		if tableErr != nil {
			return snap, tableErr
		}
		if hasOrdersTable {
			bonusMethod := ""
			if snap.BonusUSD.IsPositive() {
				bonusMethod = snap.CanonicalMethod
			}
			if err := tx.Table("adty_topup_orders").
				Where("trade_no = ?", tradeNo).
				Updates(map[string]any{
					"bonus_usd":    snap.BonusUSD,
					"bonus_method": bonusMethod,
				}).Error; err != nil {
				return snap, err
			}
		}
	}
	return snap, nil
}

func verifyTrustedBonusSiteTx(tx *gorm.DB, siteID int) error {
	if siteID <= 0 || tx == nil {
		return nil
	}
	hasSitesTable, err := bonusTableExists(tx, "adty_sites")
	if err != nil {
		return err
	}
	if !hasSitesTable {
		return nil
	}
	var row struct {
		Id     int    `gorm:"column:id"`
		Status string `gorm:"column:status"`
	}
	res := tx.Table("adty_sites").Select("id, status").Where("id = ?", siteID).Limit(1).Find(&row)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 || row.Id <= 0 {
		return errBonusMethod("site does not exist")
	}
	if strings.TrimSpace(row.Status) != "active" {
		return errBonusMethod("site is not active")
	}
	return nil
}

var bonusLockForUpdateFn func(*gorm.DB) *gorm.DB

// RegisterBonusLockForUpdate injects model.AdtyLockForUpdate without introducing
// a package import cycle between model and setting/adty_setting (R1-120).
func RegisterBonusLockForUpdate(fn func(*gorm.DB) *gorm.DB) {
	bonusLockForUpdateFn = fn
}

func loadBonusOptionsTx(tx *gorm.DB, keys []string, lock bool) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	db := tx
	if db == nil {
		db = getAuthoritativeBonusDB()
	}
	if db == nil {
		return out, nil
	}
	hasOptionsTable, err := bonusTableExists(db, "options")
	if err != nil {
		return nil, err
	}
	if !hasOptionsTable {
		return out, nil
	}
	if lock && tx != nil {
		for _, key := range keys {
			if err := ensureBonusOptionRowTx(tx, key); err != nil {
				return nil, err
			}
		}
	}
	q := db.Table("options").Select([]string{"key", "value"}).Where(map[string]any{"key": keys}).Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}})
	if lock && tx != nil && bonusLockForUpdateFn != nil {
		q = bonusLockForUpdateFn(q)
	}
	var rows []struct {
		Key   string `gorm:"column:key"`
		Value string `gorm:"column:value"`
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

func ensureBonusOptionRowTx(tx *gorm.DB, key string) error {
	return tx.Transaction(func(sp *gorm.DB) error {
		return sp.Table("options").Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoNothing: true,
		}).Create(map[string]any{
			"key":   key,
			"value": "",
		}).Error
	})
}

func upsertBonusOptionRowTx(tx *gorm.DB, key, value string) error {
	hasOptionsTable, err := bonusTableExists(tx, "options")
	if err != nil {
		return err
	}
	if !hasOptionsTable {
		return errBonusMethod("options table is missing")
	}
	if err := ensureBonusOptionRowTx(tx, key); err != nil {
		return err
	}
	return tx.Table("options").Where(map[string]any{"key": key}).Update("value", value).Error
}

// ApplyMethodBonusOption 把一条已校验的加赠配置同步进内存。
//
// 生产路径是「先落库、提交后由配置层调用它」，测试用它预置内存配置，
// 于是不再存在「只改内存、不写库」的入口。
func ApplyMethodBonusOption(key, value string) error {
	field, ok := strings.CutPrefix(key, "adty_topup_setting.")
	if !ok || field == "" {
		return errBonusMethod("unknown bonus setting key")
	}
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	return config.UpdateConfigFromMap(&topupSetting, map[string]string{field: value})
}

// unmarshalBonusSetting 解析 options 里读到的一串配置；空串表示这一行还没有。
func unmarshalBonusSetting(current string, value any) error {
	if strings.TrimSpace(current) == "" {
		return nil
	}
	return common.UnmarshalJsonStr(current, value)
}

func marshalBonusSetting(value any) (string, error) {
	encoded, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// IsBonusMethodValidationError reports whether err is a domain validation error
// (HTTP 400/403) rather than a database persistence error (HTTP 500) (R1-037).
func IsBonusMethodValidationError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*bonusMethodError)
	return ok
}

// AuthorizeSiteBonusMarginFill lets an authorized site cover a gift shortfall from margin.
func AuthorizeSiteBonusMarginFill(siteID int, allowed bool) {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if topupSetting.SiteBonusMarginFill == nil {
		topupSetting.SiteBonusMarginFill = map[int]bool{}
	}
	topupSetting.SiteBonusMarginFill[siteID] = allowed
}

// SiteBonusMarginFillAllowed reports the Root authorization for margin fill.
func SiteBonusMarginFillAllowed(siteID int) bool {
	return SiteBonusMarginFillAllowedTx(nil, siteID)
}

// SiteBonusMarginFillAllowedStrictTx reports the Root authorization for margin
// fill using tx when provided, propagating database and JSON parse errors
// instead of falling back to stale in-memory cache.
func SiteBonusMarginFillAllowedStrictTx(tx *gorm.DB, siteID int) (bool, error) {
	db := tx
	if db == nil {
		db = getAuthoritativeBonusDB()
	}
	raw, ok, err := readOptionFromDBStrict(db, TopupSettingKeySiteBonusMarginFill)
	if err != nil {
		return false, err
	}
	if ok {
		return ParseSiteBonusMarginFillAllowed(raw, siteID)
	}
	ts := GetTopupSetting()
	return ts.SiteBonusMarginFill != nil && ts.SiteBonusMarginFill[siteID], nil
}

// SiteBonusMarginFillAllowedTx reports the Root authorization for margin fill
// using tx when provided so transactional callers never open an outer DB query.
func SiteBonusMarginFillAllowedTx(tx *gorm.DB, siteID int) bool {
	if allowed, err := SiteBonusMarginFillAllowedStrictTx(tx, siteID); err == nil {
		return allowed
	}
	ts := GetTopupSetting()
	return ts.SiteBonusMarginFill != nil && ts.SiteBonusMarginFill[siteID]
}

// CanonicalBonusMethod validates and normalizes a recharge bonus payment method.
// Recharge bonus is only supported for USDT (all USDT networks share the single "usdt" rule).
func CanonicalBonusMethod(method string) (string, error) {
	return canonicalBonusMethod(method)
}

func canonicalBonusMethod(method string) (string, error) {
	method = strings.ToLower(strings.TrimSpace(method))
	if method == "usdt" || strings.HasPrefix(method, "usdt.") {
		return "usdt", nil
	}
	return "", errBonusMethod("Recharge bonus is only supported for USDT")
}

func filterSupportedMethodBonusMap(in map[string]MethodBonusRule) map[string]MethodBonusRule {
	out := map[string]MethodBonusRule{}
	if in == nil {
		return out
	}
	if rule, ok := in["usdt"]; ok {
		out["usdt"] = rule
	}
	return out
}

func parseBonusRate(rate string, enabled bool) (string, error) {
	if !enabled && strings.TrimSpace(rate) == "" {
		return "0", nil
	}
	parsed, err := common.ParseBoundedDecimalString(rate, 4, 8)
	if err != nil || parsed.IsNegative() || parsed.GreaterThan(decimal.NewFromInt(1)) {
		return "", errBonusMethod("bonus rate must be a decimal fraction from 0 to 1")
	}
	return parsed.String(), nil
}

func errBonusMethod(text string) error {
	return &bonusMethodError{text}
}

type bonusMethodError struct{ text string }

func (e *bonusMethodError) Error() string { return e.text }

// SiteMethodBonus returns the site-owner bonus rule for siteID and method
// from the authoritative DB when wired, falling back to the in-memory option snapshot.
func SiteMethodBonus(siteID int, method string) (MethodBonusRule, bool) {
	if siteID <= 0 {
		return MethodBonusRule{}, false
	}
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return MethodBonusRule{}, false
	}
	if rawBonus, hasBonus := readOptionFromDB(getAuthoritativeBonusDB(), TopupSettingKeySiteMethodBonus); hasBonus {
		if rule, ok, err := ParseSiteMethodBonusRule(rawBonus, siteID, key); err == nil {
			return rule, ok
		}
	}
	ts := GetTopupSetting()
	if ts.SiteMethodBonus == nil {
		return MethodBonusRule{}, false
	}
	rules := ts.SiteMethodBonus[siteID]
	rule, ok := rules[key]
	return rule, ok
}
