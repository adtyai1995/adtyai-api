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

func readOptionFromDB(db *gorm.DB, key string) (string, bool) {
	if db == nil || !db.Migrator().HasTable("options") {
		return "", false
	}
	var row struct {
		Value string `gorm:"column:value"`
	}
	res := db.Table("options").Select("value").Where("key = ?", key).Limit(1).Find(&row)
	if res.Error != nil || res.RowsAffected == 0 {
		return "", false
	}
	return row.Value, true
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

// ParseSiteMethodBonusAllowed parses whether Root authorized method for siteID from raw JSON.
func ParseSiteMethodBonusAllowed(rawSiteMethodAuth string, siteID int, method string) (bool, error) {
	if siteID <= 0 {
		return false, nil
	}
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return false, err
	}
	var auth map[int][]string
	if err := unmarshalBonusSetting(rawSiteMethodAuth, &auth); err != nil {
		return false, err
	}
	for _, item := range auth[siteID] {
		if item == key {
			return true, nil
		}
	}
	return false, nil
}

// ParseSiteMethodBonus parses a site-owner rule only if Root currently authorizes the method.
func ParseSiteMethodBonus(rawSiteMethodAuth, rawSiteMethodBonus string, siteID int, method string) (MethodBonusRule, bool, error) {
	allowed, err := ParseSiteMethodBonusAllowed(rawSiteMethodAuth, siteID, method)
	if err != nil || !allowed {
		return MethodBonusRule{}, false, err
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

// MainMethodBonus is the main-site rule. An absent rule is off.
func MainMethodBonus(method string) (MethodBonusRule, bool) {
	key, err := canonicalBonusMethod(method)
	if err == nil {
		method = key
	}
	if raw, ok := readOptionFromDB(getAuthoritativeBonusDB(), TopupSettingKeyMethodBonus); ok {
		if rule, found, err := ParseMainMethodBonus(raw, method); err == nil {
			return rule, found
		}
	}
	if topupSetting.MethodBonus == nil {
		return MethodBonusRule{}, false
	}
	rule, ok := topupSetting.MethodBonus[method]
	return rule, ok
}

// SiteMethodBonusAllowed reports whether Root authorized this method for a site.
func SiteMethodBonusAllowed(siteID int, method string) bool {
	key, err := canonicalBonusMethod(method)
	if err == nil {
		method = key
	}
	if raw, ok := readOptionFromDB(getAuthoritativeBonusDB(), TopupSettingKeySiteMethodAuth); ok {
		if allowed, err := ParseSiteMethodBonusAllowed(raw, siteID, method); err == nil {
			return allowed
		}
	}
	if topupSetting.SiteMethodAuth == nil {
		return false
	}
	for _, item := range topupSetting.SiteMethodAuth[siteID] {
		if item == method {
			return true
		}
	}
	return false
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
	if next == nil {
		next = map[string]MethodBonusRule{}
	}
	next[key] = MethodBonusRule{Enabled: enabled, Rate: parsed}
	return marshalBonusSetting(next)
}

// MergeSiteMethodAuth 校验一次站点授权变更，并把它合并进 current。理由同上：
// 基准取库里加锁读到的当前值，不取内存快照。
func MergeSiteMethodAuth(current string, siteID int, method string, allowed bool) (string, error) {
	if siteID <= 0 {
		return "", errBonusMethod("site is required")
	}
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return "", err
	}
	var next map[int][]string
	if err := unmarshalBonusSetting(current, &next); err != nil {
		return "", err
	}
	if next == nil {
		next = map[int][]string{}
	}
	authorized := next[siteID]
	updated := make([]string, 0, len(authorized)+1)
	for _, item := range authorized {
		if item != key {
			updated = append(updated, item)
		}
	}
	if allowed {
		updated = append(updated, key)
	}
	next[siteID] = updated
	return marshalBonusSetting(next)
}

// MergeSiteMethodBonusWithAuth validates a site rule against the authoritative
// currentAuth JSON read inside the same SQL transaction, and merges it into currentBonus.
func MergeSiteMethodBonusWithAuth(currentBonus, currentAuth string, siteID int, method string, enabled bool, rate string) (string, error) {
	if siteID <= 0 {
		return "", errBonusMethod("site is required")
	}
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return "", err
	}
	allowed, err := ParseSiteMethodBonusAllowed(currentAuth, siteID, key)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", errBonusMethod("payment method is not authorized for this site")
	}
	return mergeAuthorizedSiteMethodBonus(currentBonus, siteID, key, enabled, rate)
}

// MergeSiteMethodBonus 校验一条站长规则（必须在 Root 授权的支付方式内），
// 并把它合并进 current。
func MergeSiteMethodBonus(current string, siteID int, method string, enabled bool, rate string) (string, error) {
	if siteID <= 0 {
		return "", errBonusMethod("site is required")
	}
	key, err := canonicalBonusMethod(method)
	if err != nil {
		return "", err
	}
	if !SiteMethodBonusAllowed(siteID, key) {
		return "", errBonusMethod("payment method is not authorized for this site")
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
	if next[siteID] == nil {
		next[siteID] = map[string]MethodBonusRule{}
	}
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

// LockAndMergeSiteMethodBonusTx locks the authoritative site_method_auth and
// site_method_bonus rows in the same SQL transaction, validates that Root
// currently authorizes the method for siteID, and writes the updated rule.
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
		TopupSettingKeySiteMethodAuth,
		TopupSettingKeySiteMethodBonus,
	}, true)
	if err != nil {
		return "", err
	}
	authJSON, hasAuth := rawValues[TopupSettingKeySiteMethodAuth]
	if !hasAuth && topupSetting.SiteMethodAuth != nil {
		authJSON, _ = marshalBonusSetting(topupSetting.SiteMethodAuth)
	}
	bonusJSON, hasBonus := rawValues[TopupSettingKeySiteMethodBonus]
	if !hasBonus && topupSetting.SiteMethodBonus != nil {
		bonusJSON, _ = marshalBonusSetting(topupSetting.SiteMethodBonus)
	}
	merged, err := MergeSiteMethodBonusWithAuth(bonusJSON, authJSON, siteID, method, enabled, rate)
	if err != nil {
		return "", err
	}
	if err := upsertBonusOptionRowTx(tx, TopupSettingKeySiteMethodBonus, merged); err != nil {
		return "", err
	}
	return merged, nil
}

// LoadBonusPolicySnapshotTx evaluates the bonus policy for (siteID, isTeam, method, faceUSD)
// inside tx, optionally locking the option rows so that concurrent Root authorization
// or rate updates are serialized with order creation (G.4.2 / G.4.11 / G.4.14).
func LoadBonusPolicySnapshotTx(tx *gorm.DB, siteID int, isTeam bool, method string, faceUSD decimal.Decimal, lock bool) (BonusPolicySnapshot, error) {
	faceUSD = faceUSD.Round(6)
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
		TopupSettingKeySiteMethodAuth,
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
			rule, ok = MainMethodBonus(key)
		}
		snap.MainAuthorized = ok && rule.Enabled
	} else {
		rawAuth, hasAuth := rawValues[TopupSettingKeySiteMethodAuth]
		if !hasAuth && topupSetting.SiteMethodAuth != nil {
			rawAuth, _ = marshalBonusSetting(topupSetting.SiteMethodAuth)
		}
		allowed, err := ParseSiteMethodBonusAllowed(rawAuth, siteID, key)
		if err != nil {
			return snap, err
		}
		snap.SiteAuthorized = allowed
		if rawFill, hasFill := rawValues[TopupSettingKeySiteBonusMarginFill]; hasFill {
			snap.MarginFillAllowed, _ = ParseSiteBonusMarginFillAllowed(rawFill, siteID)
		} else {
			snap.MarginFillAllowed = SiteBonusMarginFillAllowed(siteID)
		}
		if !allowed {
			return snap, nil
		}
		rawBonus, hasBonus := rawValues[TopupSettingKeySiteMethodBonus]
		if !hasBonus && topupSetting.SiteMethodBonus != nil {
			rawBonus, _ = marshalBonusSetting(topupSetting.SiteMethodBonus)
		}
		rule, ok, err = ParseSiteMethodBonus(rawAuth, rawBonus, siteID, key)
		if err != nil {
			return snap, err
		}
	}
	if !ok || !rule.Enabled {
		return snap, nil
	}
	rateDec, err := decimal.NewFromString(strings.TrimSpace(rule.Rate))
	if err != nil || rateDec.LessThanOrEqual(decimal.Zero) || rateDec.GreaterThan(decimal.NewFromInt(1)) {
		return snap, nil
	}
	bonusUSD := faceUSD.Mul(rateDec).Round(6)
	bonusQuota := bonusUSD.Mul(common.QuotaPerUnitDecimal()).Floor().IntPart()
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
	if tradeNo != "" && tx.Migrator().HasTable("adty_topup_orders") {
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
	return snap, nil
}

func verifyTrustedBonusSiteTx(tx *gorm.DB, siteID int) error {
	if siteID <= 0 || tx == nil || !tx.Migrator().HasTable("adty_sites") {
		return nil
	}
	var row struct {
		Id     int `gorm:"column:id"`
		Status int `gorm:"column:status"`
	}
	res := tx.Table("adty_sites").Select("id, status").Where("id = ?", siteID).Limit(1).Find(&row)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 || row.Id <= 0 {
		return errBonusMethod("site does not exist")
	}
	if row.Status != 1 {
		return errBonusMethod("site is not active")
	}
	return nil
}

func loadBonusOptionsTx(tx *gorm.DB, keys []string, lock bool) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	db := tx
	if db == nil {
		db = getAuthoritativeBonusDB()
	}
	if db == nil || !db.Migrator().HasTable("options") {
		return out, nil
	}
	if lock && tx != nil {
		for _, key := range keys {
			if err := ensureBonusOptionRowTx(tx, key); err != nil {
				return nil, err
			}
		}
	}
	q := db.Table("options").Select("key, value").Where("key IN ?", keys).Order("key ASC")
	if lock && tx != nil {
		name := tx.Dialector.Name()
		if name == "postgres" || name == "mysql" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
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
	if !tx.Migrator().HasTable("options") {
		return errBonusMethod("options table is missing")
	}
	if err := ensureBonusOptionRowTx(tx, key); err != nil {
		return err
	}
	return tx.Table("options").Where("key = ?", key).Update("value", value).Error
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

// AuthorizeSiteBonusMarginFill lets an authorized site cover a gift shortfall from margin.
func AuthorizeSiteBonusMarginFill(siteID int, allowed bool) {
	if topupSetting.SiteBonusMarginFill == nil {
		topupSetting.SiteBonusMarginFill = map[int]bool{}
	}
	topupSetting.SiteBonusMarginFill[siteID] = allowed
}

// SiteBonusMarginFillAllowed reports the Root authorization for margin fill.
func SiteBonusMarginFillAllowed(siteID int) bool {
	if raw, ok := readOptionFromDB(getAuthoritativeBonusDB(), TopupSettingKeySiteBonusMarginFill); ok {
		if allowed, err := ParseSiteBonusMarginFillAllowed(raw, siteID); err == nil {
			return allowed
		}
	}
	return topupSetting.SiteBonusMarginFill != nil && topupSetting.SiteBonusMarginFill[siteID]
}

func canonicalBonusMethod(method string) (string, error) {
	method = strings.ToLower(strings.TrimSpace(method))
	if strings.HasPrefix(method, "usdt") {
		return "usdt", nil
	}
	if method == "alipay" || method == "wxpay" {
		return method, nil
	}
	return "", errBonusMethod("unsupported payment method")
}

func parseBonusRate(rate string, enabled bool) (string, error) {
	if !enabled && strings.TrimSpace(rate) == "" {
		return "0", nil
	}
	parsed, err := decimal.NewFromString(strings.TrimSpace(rate))
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

// SiteMethodBonus is the site-owner rule inside an authorized method.
func SiteMethodBonus(siteID int, method string) (MethodBonusRule, bool) {
	key, err := canonicalBonusMethod(method)
	if err == nil {
		method = key
	}
	db := getAuthoritativeBonusDB()
	if db != nil {
		rawAuth, hasAuth := readOptionFromDB(db, TopupSettingKeySiteMethodAuth)
		rawBonus, hasBonus := readOptionFromDB(db, TopupSettingKeySiteMethodBonus)
		if hasAuth || hasBonus {
			if !hasAuth && topupSetting.SiteMethodAuth != nil {
				rawAuth, _ = marshalBonusSetting(topupSetting.SiteMethodAuth)
			}
			if !hasBonus && topupSetting.SiteMethodBonus != nil {
				rawBonus, _ = marshalBonusSetting(topupSetting.SiteMethodBonus)
			}
			if rule, ok, err := ParseSiteMethodBonus(rawAuth, rawBonus, siteID, method); err == nil {
				return rule, ok
			}
		}
	}
	if topupSetting.SiteMethodBonus == nil {
		return MethodBonusRule{}, false
	}
	if !SiteMethodBonusAllowed(siteID, method) {
		return MethodBonusRule{}, false
	}
	rules := topupSetting.SiteMethodBonus[siteID]
	rule, ok := rules[method]
	return rule, ok
}
