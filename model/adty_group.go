package model

import (
	"crypto/rand"
	"errors"
	"math/big"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	adtyGroupIDMaxLen    = 32
	adtyGroupIDAlphabet  = "abcdefghijklmnopqrstuvwxyz0123456789"
	adtyGroupIDSuffixLen = 4
	// AdtyDefaultGroupID 是 users、channels 的默认分组 ID；当系统仍有其他活跃分组时允许删除。
	AdtyDefaultGroupID = "default"
	// AdtyGroupModelAllowOption 是分组可用模型白名单在 options 表里的键。
	AdtyGroupModelAllowOption = "GroupModelAllow"
)

// AdtyOnSiteGroupConfigChanged 在分组迁移或删除改到分站 allowed_groups 后调用，由 service 挂上清站点缓存。
var AdtyOnSiteGroupConfigChanged func()

// ValidateAdtyGroupOptionTx 由 service/adty_group_guard.go 注册，用于在事务内持锁复验全部 8 个分组配置。
var ValidateAdtyGroupOptionTx func(tx *gorm.DB, key, value string) error

var adtyGroupOptionKeysSorted = []string{
	"AutoGroups",
	"GroupGroupRatio",
	AdtyGroupModelAllowOption,
	"GroupRatio",
	"ModelRequestRateLimitGroup",
	"TopupGroupRatio",
	"UserUsableGroups",
	"group_ratio_setting.group_special_usable_group",
}

func adtyGroupOptionDefaultValue(key string) string {
	switch key {
	case "AutoGroups":
		return setting.AutoGroups2JsonString()
	case "GroupRatio":
		return ratio_setting.GroupRatio2JSONString()
	case "UserUsableGroups":
		return setting.UserUsableGroups2JSONString()
	case "TopupGroupRatio":
		return common.TopupGroupRatio2JSONString()
	case "GroupGroupRatio":
		return ratio_setting.GroupGroupRatio2JSONString()
	case "group_ratio_setting.group_special_usable_group":
		if spec := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup; spec != nil {
			return spec.MarshalJSONString()
		}
		return "{}"
	case "ModelRequestRateLimitGroup":
		if raw := setting.ModelRequestRateLimitGroup2JSONString(); raw != "" {
			return raw
		}
		return "{}"
	case "console_setting.uptime_kuma_groups":
		return "[]"
	default:
		return "{}"
	}
}

func preLockAdtyGroupOptionsTx(tx *gorm.DB, keys []string) error {
	if tx == nil || len(keys) == 0 {
		return nil
	}
	hasOptionTable, err := AdtyTableExists(tx, &Option{})
	if err != nil {
		return err
	}
	if !hasOptionTable {
		return nil
	}
	sorted := slices.Clone(keys)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	// Seed any missing option rows with valid default JSON BEFORE FOR UPDATE so
	// concurrent UpdateOptionWithAudit cannot form an AB-BA 40P01 deadlock (R10-025).
	for _, key := range sorted {
		seed := Option{Key: key, Value: adtyGroupOptionDefaultValue(key)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
	}
	var rows []Option
	return lockForUpdate(tx).Where(commonKeyCol+" IN ?", sorted).Order(commonKeyCol + " ASC").Find(&rows).Error
}

// adtyGroupConfigMu 让删除分组时的整份回写和单键保存排队。
// 只在内存快照上改完再写回，会盖掉这中间已经保存的倍率。
var adtyGroupConfigMu sync.Mutex

func adtyGroupConfigOption(key string) bool {
	switch key {
	case "GroupRatio", "UserUsableGroups", "TopupGroupRatio", "GroupGroupRatio", "AutoGroups",
		"group_ratio_setting.group_special_usable_group", "ModelRequestRateLimitGroup", AdtyGroupModelAllowOption:
		return true
	default:
		return false
	}
}

func lockAdtyGroupConfig(keys ...string) func() {
	for _, key := range keys {
		if adtyGroupConfigOption(key) {
			adtyGroupConfigMu.Lock()
			return adtyGroupConfigMu.Unlock
		}
	}
	return func() {}
}

// AdtyContentLangs 保留历史内容语言列表供旧数据读取回退；写入链路仅使用正式语言 adtyOfficialContentLangs。
var AdtyContentLangs = []string{"zh-CN", "zh-TW", "en", "fr", "ja", "ko", "ru", "vi"}

// adtyOfficialContentLangs 是正式产品语言（简体中文与英文），创建和重命名分组时仅保留并写入这两项。
var adtyOfficialContentLangs = []string{"zh-CN", "en"}

var (
	ErrAdtyGroupNotFound     = common.NewMessage("Group not found")
	ErrAdtyGroupTextRequired = errors.New("adty group text required")
	errAdtyGroupIDExhausted  = errors.New("adty group id exhausted")
	adtyGroupCreateAttempts  = 5
	adtySkippedSeedGroupIDs  = map[string]struct{}{"auto": {}, "all": {}, "edit_this": {}}
	// adtyReservedGroupIDs 永远不会被生成：auto/all 是保留关键字，default 是列默认值。
	adtyReservedGroupIDs = map[string]struct{}{"auto": {}, "all": {}, AdtyDefaultGroupID: {}}
	// adtyGroupIDTaken 由设置里出现过的分组键组成；生成 ID 时一起避开。测试可以替换。
	adtyGroupIDTaken = adtyGroupSeedSources
)

// IsReservedAdtyGroupID reports whether id is a reserved pseudo-group ("auto" or "all")
// that must never be assigned to channels, users, or tokens.
func IsReservedAdtyGroupID(id string) bool {
	trimmed := strings.TrimSpace(id)
	return strings.EqualFold(trimmed, "auto") || strings.EqualFold(trimmed, "all")
}

// AdtyLangText 按语言存文本。Value 返回字符串，PostgreSQL 简单协议才能写入 json 列。
type AdtyLangText map[string]string

func (text AdtyLangText) Value() (driverValue, error) {
	return adtyLangTextValue(text)
}

func (text *AdtyLangText) Scan(value any) error {
	return adtyLangTextScan(text, value)
}

// AdtyGroup 是分组表。ID 创建后不改；软删除的行仍占用 ID，历史日志靠它显示名字。
type AdtyGroup struct {
	ID        string         `json:"id" gorm:"column:id;type:varchar(32);primaryKey"`
	Names     AdtyLangText   `json:"names" gorm:"column:names;type:json;not null"`
	Descs     AdtyLangText   `json:"descs" gorm:"column:descs;type:json;not null"`
	CreatedAt int64          `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64          `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

func (AdtyGroup) TableName() string {
	return "adty_groups"
}

// Deleted 表示这一行已软删除。
func (group AdtyGroup) Deleted() bool {
	return group.DeletedAt.Valid
}

// DisplayName 按优先语言 → zh-CN → en → 其余内容语言 → ID 取分组显示名。
func (group AdtyGroup) DisplayName(preferredLangs ...string) string {
	for _, lang := range preferredLangs {
		if v := strings.TrimSpace(group.Names[lang]); v != "" {
			return v
		}
	}
	for _, lang := range []string{"zh-CN", "en"} {
		if v := strings.TrimSpace(group.Names[lang]); v != "" {
			return v
		}
	}
	for _, lang := range AdtyContentLangs {
		if v := strings.TrimSpace(group.Names[lang]); v != "" {
			return v
		}
	}
	return group.ID
}

// DisplayDesc 按优先语言取分组说明，全部为空时回退到 DisplayName。
func (group AdtyGroup) DisplayDesc(preferredLangs ...string) string {
	for _, lang := range preferredLangs {
		if v := strings.TrimSpace(group.Descs[lang]); v != "" {
			return v
		}
	}
	for _, lang := range []string{"zh-CN", "en"} {
		if v := strings.TrimSpace(group.Descs[lang]); v != "" {
			return v
		}
	}
	for _, lang := range AdtyContentLangs {
		if v := strings.TrimSpace(group.Descs[lang]); v != "" {
			return v
		}
	}
	return group.DisplayName(preferredLangs...)
}

// AdtyGroupRefs 是分组被引用的数量。一个密钥即使分组和自动分组都命中，也只算一次。
type AdtyGroupRefs struct {
	Users    int64 `json:"users"`
	Tokens   int64 `json:"tokens"`
	Channels int64 `json:"channels"`
	Sites    int64 `json:"sites"`
}

// Total 是四类引用之和。
func (refs AdtyGroupRefs) Total() int64 {
	return refs.Users + refs.Tokens + refs.Channels + refs.Sites
}

// NewAdtyGroupID 生成 "g" + 4 位 [a-z0-9]，避开保留字、表里已有的 ID（含软删除）和设置里出现过的分组键。
func NewAdtyGroupID(tx *gorm.DB) (string, error) {
	var ids []string
	if err := tx.Unscoped().Model(&AdtyGroup{}).Pluck("id", &ids).Error; err != nil {
		return "", err
	}
	used := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		used[id] = struct{}{}
	}
	taken, err := adtyGroupIDTaken()
	if err != nil {
		return "", err
	}
	for id := range taken {
		used[id] = struct{}{}
	}
	for range 100 {
		id, err := randomAdtyGroupID()
		if err != nil {
			return "", err
		}
		if _, reserved := adtyReservedGroupIDs[id]; reserved {
			continue
		}
		if _, exists := used[id]; exists {
			continue
		}
		return id, nil
	}
	return "", errAdtyGroupIDExhausted
}

func randomAdtyGroupID() (string, error) {
	var b strings.Builder
	b.WriteByte('g')
	limit := big.NewInt(int64(len(adtyGroupIDAlphabet)))
	for range adtyGroupIDSuffixLen {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		b.WriteByte(adtyGroupIDAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// CreateAdtyGroup 新建分组。名字必填，说明可空；两者都写进全部语言，之后可按语言改。
// 同一个事务里把新分组写入 options 的 GroupRatio（默认倍率 1）与 UserUsableGroups（描述/名字），
// 提交后立即刷新内存缓存，使新分组无需二次保存即可在渠道、定价和 API Key 中立即可见可用。
func CreateAdtyGroup(name, desc string) (*AdtyGroup, error) {
	name = strings.TrimSpace(name)
	desc = strings.TrimSpace(desc)
	if name == "" {
		return nil, ErrAdtyGroupTextRequired
	}
	adtyGroupConfigMu.Lock()
	defer adtyGroupConfigMu.Unlock()
	var created *AdtyGroup
	var lastErr error
	for range adtyGroupCreateAttempts {
		var optionValues map[string]string
		err := DB.Transaction(func(tx *gorm.DB) error {
			// Preserve canonical options -> adty_groups lock order (R9-008).
			if err := preLockAdtyGroupOptionsTx(tx, []string{"GroupRatio", "UserUsableGroups"}); err != nil {
				return err
			}
			id, err := NewAdtyGroupID(tx)
			if err != nil {
				return err
			}
			row := &AdtyGroup{
				ID:    id,
				Names: adtyFillLangs(name),
				Descs: adtyFillLangs(desc),
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
			var load func(string) (string, error)
			hasOptionTable, err := AdtyTableExists(tx, &Option{})
			if err != nil {
				return err
			}
			if hasOptionTable {
				load = func(key string) (string, error) {
					return loadAdtyGroupOptionTx(tx, key)
				}
			}
			ratioRaw, err := adtyGroupOptionSource(load, "GroupRatio", ratio_setting.GroupRatio2JSONString())
			if err != nil {
				return err
			}
			ratios := map[string]float64{}
			if err := adtyUnmarshalObject(ratioRaw, &ratios); err != nil {
				return err
			}
			if _, exists := ratios[id]; !exists {
				ratios[id] = 1
			}
			ratioBytes, err := common.Marshal(ratios)
			if err != nil {
				return err
			}
			usableRaw, err := adtyGroupOptionSource(load, "UserUsableGroups", setting.UserUsableGroups2JSONString())
			if err != nil {
				return err
			}
			usable := map[string]string{}
			if err := adtyUnmarshalObject(usableRaw, &usable); err != nil {
				return err
			}
			usableDesc := desc
			if usableDesc == "" {
				usableDesc = name
			}
			usable[id] = usableDesc
			usableBytes, err := common.Marshal(usable)
			if err != nil {
				return err
			}
			optionValues = map[string]string{
				"GroupRatio":       string(ratioBytes),
				"UserUsableGroups": string(usableBytes),
			}
			if hasOptionTable {
				if err := SaveOptionsTx(tx, optionValues); err != nil {
					return err
				}
			}
			created = row
			return nil
		})
		if err == nil {
			common.OptionMapRWMutex.Lock()
			if common.OptionMap == nil {
				common.OptionMap = make(map[string]string)
			}
			common.OptionMapRWMutex.Unlock()
			for _, key := range []string{"GroupRatio", "UserUsableGroups"} {
				if value, ok := optionValues[key]; ok {
					if updateErr := updateOptionMap(key, value); updateErr != nil {
						common.SysError(common.LogText("update option map after group create (%s): %s", key, updateErr.Error()))
					}
				}
			}
			ratio_setting.InvalidateExposedDataCache()
			InvalidatePricingCache()
			return created, nil
		}
		if !isAdtyUniqueViolation(err) {
			return nil, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errAdtyGroupIDExhausted
	}
	return nil, lastErr
}

// UpdateAdtyGroupTexts 只改显示名和说明，ID 不变。英文名必填，其余语言可空（显示时回退英文）。
func UpdateAdtyGroupTexts(id string, names, descs AdtyLangText) (*AdtyGroup, error) {
	names, descs, err := normalizeAdtyGroupTexts(names, descs)
	if err != nil {
		return nil, err
	}
	group, err := GetAdtyGroup(id)
	if err != nil {
		return nil, err
	}
	group.Names = names
	group.Descs = descs
	if err := DB.Save(group).Error; err != nil {
		return nil, err
	}
	InvalidatePricingCache()
	return group, nil
}

// ListAdtyGroups 按 ID 排序返回分组；includeDeleted 为真时带上软删除的行。
func ListAdtyGroups(includeDeleted bool) ([]AdtyGroup, error) {
	var groups []AdtyGroup
	query := DB
	if includeDeleted {
		query = query.Unscoped()
	}
	err := query.Order("id").Find(&groups).Error
	return groups, err
}

// GetAdtyGroup 取一个未删除的分组。
func GetAdtyGroup(id string) (*AdtyGroup, error) {
	var groups []AdtyGroup
	if err := DB.Where("id = ?", id).Limit(1).Find(&groups).Error; err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, ErrAdtyGroupNotFound
	}
	return &groups[0], nil
}

// ActiveAdtyGroupIDs 返回未删除分组的 ID 集合，保存设置时按它校验。
func ActiveAdtyGroupIDs() (map[string]struct{}, error) {
	return ActiveAdtyGroupIDsTx(DB, false)
}

// ActiveAdtyGroupIDsTx returns active (non-deleted) group IDs inside tx,
// optionally locking live rows in ascending ID order.
func ActiveAdtyGroupIDsTx(tx *gorm.DB, lock bool) (map[string]struct{}, error) {
	if tx == nil {
		tx = DB
	}
	hasGroupTable, err := AdtyTableExists(tx, &AdtyGroup{})
	if err != nil {
		return nil, err
	}
	if !hasGroupTable {
		return map[string]struct{}{}, nil
	}
	q := tx
	if lock {
		q = lockForUpdate(q)
	}
	var groups []AdtyGroup
	if err := q.Select("id").Order("id ASC").Find(&groups).Error; err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		out[g.ID] = struct{}{}
	}
	return out, nil
}

// CountAdtyGroupRefs 统计还在使用这个分组的用户、密钥、渠道和分站。
func CountAdtyGroupRefs(id string) (AdtyGroupRefs, error) {
	return countAdtyGroupRefs(DB, id)
}

func countAdtyGroupRefs(db *gorm.DB, id string) (AdtyGroupRefs, error) {
	var refs AdtyGroupRefs
	var err error
	refs.Users, err = countAdtyUserRefs(db, id)
	if err != nil {
		return refs, err
	}
	refs.Tokens, err = countAdtyTokenRefs(db, id)
	if err != nil {
		return refs, err
	}
	refs.Channels, err = countAdtyChannelRefs(db, id)
	if err != nil {
		return refs, err
	}
	refs.Sites, err = countAdtySiteRefs(db, id)
	return refs, err
}

func countAdtySiteRefs(db *gorm.DB, id string) (int64, error) {
	if db == nil {
		return 0, nil
	}
	hasSiteTable, err := AdtyTableExists(db, &AdtySite{})
	if err != nil {
		return 0, err
	}
	if !hasSiteTable {
		return 0, nil
	}
	var sites []AdtySite
	if err := db.Select("id", "allowed_groups").
		Where("allowed_groups <> '' AND status NOT IN (?, ?)", AdtySiteStatusClosed, AdtySiteStatusDeleting).
		Find(&sites).Error; err != nil {
		return 0, err
	}
	var count int64
	for _, site := range sites {
		groups, err := parseAdtySiteAllowedGroups(site.AllowedGroups)
		if err != nil {
			continue
		}
		if slices.Contains(groups, id) {
			count++
		}
	}
	return count, nil
}

func parseAdtySiteAllowedGroups(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var groups []string
	if strings.HasPrefix(raw, "[") {
		if err := common.UnmarshalJsonStr(raw, &groups); err != nil {
			return nil, err
		}
	} else if err := common.UnmarshalJsonStr(raw, &groups); err != nil {
		// Fallback for legacy comma-separated allowed_groups values.
		groups = strings.Split(raw, ",")
	}
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" || slices.Contains(out, group) {
			continue
		}
		out = append(out, group)
	}
	return out, nil
}

func replaceAdtyGroupInSlice(groups []string, fromID, toID string) []string {
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		if group == fromID {
			group = toID
		}
		if group == "" || slices.Contains(out, group) {
			continue
		}
		out = append(out, group)
	}
	return out
}

func countAdtyUserRefs(db *gorm.DB, id string) (int64, error) {
	var count int64
	err := db.Model(&User{}).Where(commonGroupCol+" = ?", id).Count(&count).Error
	return count, err
}

func adtyIsPostgresDB(db *gorm.DB) bool {
	return db != nil && db.Dialector != nil && db.Dialector.Name() == "postgres"
}

func countAdtyTokenRefs(db *gorm.DB, id string) (int64, error) {
	if adtyIsPostgresDB(db) {
		var count int64
		// 空字符串不能转 jsonb。分组列和自动分组同时命中时，这一行只计一次。
		err := db.Model(&Token{}).Where(
			`(`+commonGroupCol+` = ? OR (auto_groups <> '' AND jsonb_exists(auto_groups::jsonb, ?)))`,
			id, id,
		).Count(&count).Error
		return count, err
	}
	var tokens []Token
	if err := db.Select("id", commonGroupCol, "auto_groups").
		Where(commonGroupCol+" = ? OR auto_groups <> ''", id).Find(&tokens).Error; err != nil {
		return 0, err
	}
	var count int64
	for _, token := range tokens {
		if token.Group == id {
			count++
			continue
		}
		groups, err := token.GetAutoGroups()
		if err == nil && slices.Contains(groups, id) {
			count++
		}
	}
	return count, nil
}

func countAdtyChannelRefs(db *gorm.DB, id string) (int64, error) {
	if adtyIsPostgresDB(db) {
		var count int64
		// 和 Channel.GetGroups 一样：先去掉首尾逗号，再按逗号拆开后精确匹配。group_2 不能命中 group_20。
		query := `EXISTS (
		SELECT 1
		FROM unnest(string_to_array(trim(both ',' from channels.` + commonGroupCol + `), ',')) AS part
		WHERE btrim(part) = ?
	)`
		err := db.Model(&Channel{}).Where(query, id).Count(&count).Error
		return count, err
	}
	var channels []Channel
	if err := db.Select("id", commonGroupCol).Find(&channels).Error; err != nil {
		return 0, err
	}
	var count int64
	for _, ch := range channels {
		if slices.Contains(ch.GetGroups(), id) {
			count++
		}
	}
	return count, nil
}

// lockLiveAdtyGroupsTx 按 ID 顺序锁住还在的分组行，避免删除和迁移互相死锁。
func lockLiveAdtyGroupsTx(tx *gorm.DB, ids ...string) error {
	sorted := append([]string(nil), ids...)
	slices.Sort(sorted)
	seen := map[string]struct{}{}
	for _, id := range sorted {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		var rows []AdtyGroup
		if err := lockForUpdate(tx).Where("id = ?", id).Limit(1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return ErrAdtyGroupNotFound
		}
	}
	return nil
}

// lockAssignableAdtyGroupsTx 在把分组写到用户、密钥或渠道上时调用。
// 显式拒绝保留分组 "auto" 与 "all"；当 adty_groups 表存在且已有活跃分组时，
// 查不到活跃记录的分组 ID 返回 ErrAdtyGroupNotFound。已经软删除的分组同样拒绝。
func lockAssignableAdtyGroupsTx(tx *gorm.DB, ids []string) error {
	sorted := append([]string(nil), ids...)
	slices.Sort(sorted)
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(sorted))
	for _, id := range sorted {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if IsReservedAdtyGroupID(id) {
			return common.NewMessage("Group {{group}} is reserved and cannot be used", map[string]any{"group": strings.ToLower(id)})
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	// 还没建分组表的库没有可锁定的行，旧名字照旧放行。
	if len(unique) == 0 || tx == nil {
		return nil
	}
	hasGroupTable, err := AdtyTableExists(tx, &AdtyGroup{})
	if err != nil {
		return err
	}
	if !hasGroupTable {
		return nil
	}
	var activeCount int64
	if err := tx.Model(&AdtyGroup{}).Count(&activeCount).Error; err != nil {
		return err
	}
	hasActiveGroups := activeCount > 0
	for _, id := range unique {
		var rows []AdtyGroup
		if err := lockForUpdate(tx.Unscoped()).Where("id = ?", id).Limit(1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			if hasActiveGroups {
				return ErrAdtyGroupNotFound
			}
			continue
		}
		if rows[0].DeletedAt.Valid {
			return ErrAdtyGroupNotFound
		}
	}
	return nil
}

// PreLockAdtyGroupRowAllowDeletedTx acquires the FOR UPDATE lock on one
// adty_groups row before locking users, reporting whether the row exists and
// is soft-deleted so callers can allow keeping an unchanged legacy group while
// rejecting transitions onto a soft-deleted group without reversing lock order.
func PreLockAdtyGroupRowAllowDeletedTx(tx *gorm.DB, id string) (bool, error) {
	return preLockAdtyGroupRowAllowDeletedTx(tx, id)
}

func preLockAdtyGroupRowAllowDeletedTx(tx *gorm.DB, id string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || tx == nil {
		return false, nil
	}
	hasGroupTable, err := AdtyTableExists(tx, &AdtyGroup{})
	if err != nil {
		return false, err
	}
	if !hasGroupTable {
		return false, nil
	}
	var rows []AdtyGroup
	if err := lockForUpdate(tx.Unscoped()).Where("id = ?", id).Limit(1).Find(&rows).Error; err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	return rows[0].DeletedAt.Valid, nil
}

// PreLockAdtyGroupsTx locks assignable adty_groups rows in sorted order so callers
// that subsequently lock users/channels preserve the canonical adty_groups -> users lock order.
func PreLockAdtyGroupsTx(tx *gorm.DB, ids ...string) error {
	return lockAssignableAdtyGroupsTx(tx, ids)
}

// LockActiveAdtyGroupsTx locks every requested adty_groups row in sorted order and
// requires each group ID to exist and remain active (not soft-deleted).
func LockActiveAdtyGroupsTx(tx *gorm.DB, ids []string) (string, error) {
	if tx == nil || len(ids) == 0 {
		return "", nil
	}
	sorted := append([]string(nil), ids...)
	slices.Sort(sorted)
	seen := map[string]struct{}{}
	for _, id := range sorted {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		var rows []AdtyGroup
		if err := lockForUpdate(tx.Unscoped()).Where("id = ?", id).Limit(1).Find(&rows).Error; err != nil {
			return "", err
		}
		if len(rows) == 0 || rows[0].DeletedAt.Valid {
			return id, ErrAdtyGroupNotFound
		}
	}
	return "", nil
}

func adtyFillLangs(text string) AdtyLangText {
	out := make(AdtyLangText, len(adtyOfficialContentLangs))
	for _, lang := range adtyOfficialContentLangs {
		out[lang] = text
	}
	return out
}

// ValidateAdtyGroupTexts 在读库前检查英文名不为空。
func ValidateAdtyGroupTexts(names, descs AdtyLangText) error {
	_, _, err := normalizeAdtyGroupTexts(names, descs)
	return err
}

// normalizeAdtyGroupTexts 去掉首尾空白，仅保留正式语言（zh-CN 与 en）；英文名必须有。
func normalizeAdtyGroupTexts(names, descs AdtyLangText) (AdtyLangText, AdtyLangText, error) {
	outNames := make(AdtyLangText, len(adtyOfficialContentLangs))
	outDescs := make(AdtyLangText, len(adtyOfficialContentLangs))
	for _, lang := range adtyOfficialContentLangs {
		outNames[lang] = strings.TrimSpace(names[lang])
		outDescs[lang] = strings.TrimSpace(descs[lang])
	}
	if outNames["en"] == "" {
		return nil, nil, ErrAdtyGroupTextRequired
	}
	return outNames, outDescs, nil
}

func adtySeedableGroupID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || utf8.RuneCountInString(id) > adtyGroupIDMaxLen {
		return false
	}
	_, skip := adtySkippedSeedGroupIDs[id]
	return !skip
}

func adtyStripGroupPrefix(key string) string {
	key = strings.TrimSpace(key)
	if after, ok := strings.CutPrefix(key, "+:"); ok {
		return strings.TrimSpace(after)
	}
	if after, ok := strings.CutPrefix(key, "-:"); ok {
		return strings.TrimSpace(after)
	}
	return key
}

// IsAdtyUniqueViolation reports a unique-index conflict from a supported database.
func IsAdtyUniqueViolation(err error) bool {
	return isAdtyUniqueViolation(err)
}

func isAdtyUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "UNIQUE constraint failed") || strings.Contains(text, "duplicate key")
}

var ErrAdtyGroupSameTarget = errors.New("adty group migrate target equals source")

// MigrateAdtyGroup 在一个事务里把用户、密钥（分组和自动分组）、渠道、分站 allowed_groups 和 GroupModelAllow 从 fromID 改到 toID，
// 渠道的 abilities 跟着重建。软删除的用户和密钥也一起改，免得恢复后指向不存在的分组。
// 历史计费快照（RequestPriceSnapshot 等）保持原样不改。
// 提交后刷新受影响用户的分组缓存、密钥缓存、渠道缓存、分站缓存与内存配置。
func MigrateAdtyGroup(fromID, toID string) (AdtyGroupRefs, error) {
	var moved AdtyGroupRefs
	if fromID == toID {
		return moved, ErrAdtyGroupSameTarget
	}
	if _, err := GetAdtyGroup(fromID); err != nil {
		return moved, err
	}
	if _, err := GetAdtyGroup(toID); err != nil {
		return moved, err
	}
	adtyGroupConfigMu.Lock()
	defer adtyGroupConfigMu.Unlock()
	affectedUsers := map[int]struct{}{}
	var optionValues map[string]string
	var siteChanged bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		// R9-008: Pre-lock options (sorted) BEFORE adty_groups -> adty_sites to match
		// UpdateOptionWithAudit canonical lock order and prevent 40P01 deadlocks.
		if err := preLockAdtyGroupOptionsTx(tx, []string{AdtyGroupModelAllowOption}); err != nil {
			return err
		}
		if err := lockLiveAdtyGroupsTx(tx, fromID, toID); err != nil {
			return err
		}
		hasSiteTable, err := AdtyTableExists(tx, &AdtySite{})
		if err != nil {
			return err
		}
		if hasSiteTable {
			var sites []AdtySite
			if err := lockForUpdate(tx).Select("id", "status", "allowed_groups").
				Where("allowed_groups <> '' AND status <> ?", AdtySiteStatusDeleting).
				Order("id").Find(&sites).Error; err != nil {
				return err
			}
			for _, site := range sites {
				groups, err := parseAdtySiteAllowedGroups(site.AllowedGroups)
				if err != nil || !slices.Contains(groups, fromID) {
					continue
				}
				next := replaceAdtyGroupInSlice(groups, fromID, toID)
				raw, err := common.Marshal(next)
				if err != nil {
					return err
				}
				if err := tx.Model(&AdtySite{}).Where("id = ?", site.Id).Update("allowed_groups", string(raw)).Error; err != nil {
					return err
				}
				siteChanged = true
				if site.Status != AdtySiteStatusClosed && site.Status != AdtySiteStatusDeleting {
					moved.Sites++
				}
			}
		}
		var users []User
		if err := lockForUpdate(tx.Unscoped()).Select("id").Where(commonGroupCol+" = ?", fromID).Order("id").Find(&users).Error; err != nil {
			return err
		}
		if len(users) > 0 {
			ids := make([]int, 0, len(users))
			for _, user := range users {
				ids = append(ids, user.Id)
				affectedUsers[user.Id] = struct{}{}
			}
			if err := tx.Unscoped().Model(&User{}).Where("id IN ?", ids).Update(commonGroupCol, toID).Error; err != nil {
				return err
			}
			moved.Users = int64(len(ids))
		}

		var tokens []Token
		tokenQuery := lockForUpdate(tx.Unscoped()).Select("id", "user_id", commonKeyCol, commonGroupCol, "auto_groups")
		if adtyIsPostgresDB(tx) {
			tokenQuery = tokenQuery.Where(`(`+commonGroupCol+` = ? OR (auto_groups <> '' AND jsonb_exists(auto_groups::jsonb, ?)))`, fromID, fromID)
		} else {
			tokenQuery = tokenQuery.Where(commonGroupCol+` = ? OR auto_groups <> ''`, fromID)
		}
		if err := tokenQuery.Order("id").Find(&tokens).Error; err != nil {
			return err
		}
		if err := invalidateTokensCache(tokens); err != nil {
			return err
		}
		for _, token := range tokens {
			updates := map[string]any{}
			if token.Group == fromID {
				updates[commonGroupCol] = toID
			}
			groups, err := token.GetAutoGroups()
			if err != nil {
				return err
			}
			if slices.Contains(groups, fromID) {
				next := replaceAdtyGroupInSlice(groups, fromID, toID)
				if err := token.SetAutoGroups(next); err != nil {
					return err
				}
				updates["auto_groups"] = token.AutoGroups
			}
			if len(updates) == 0 {
				continue
			}
			if err := tx.Unscoped().Model(&Token{}).Where("id = ?", token.Id).Updates(updates).Error; err != nil {
				return err
			}
			affectedUsers[token.UserId] = struct{}{}
			moved.Tokens++
		}

		var channels []Channel
		if err := lockForUpdate(tx).Select("id", "models", "status", commonGroupCol, "priority", "weight", "tag").Order("id").Find(&channels).Error; err != nil {
			return err
		}
		for _, channel := range channels {
			groups := channel.GetGroups()
			if !slices.Contains(groups, fromID) {
				continue
			}
			next := replaceAdtyGroupInSlice(groups, fromID, toID)
			channel.Group = strings.Join(next, ",")
			if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Update(commonGroupCol, channel.Group).Error; err != nil {
				return err
			}
			if err := channel.UpdateAbilities(tx); err != nil {
				return err
			}
			moved.Channels++
		}

		hasOptionTable, err := AdtyTableExists(tx, &Option{})
		if err != nil {
			return err
		}
		if hasOptionTable {
			var err error
			optionValues, err = adtyGroupOptionsMigrated(fromID, toID, func(key string) (string, error) {
				return loadAdtyGroupOptionTx(tx, key)
			})
			if err != nil {
				return err
			}
			if err := SaveOptionsTx(tx, optionValues); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AdtyGroupRefs{}, err
	}
	for key, value := range optionValues {
		if err := updateOptionMap(key, value); err != nil {
			common.SysError(common.LogText("update option map after group migrate (%s): %s", key, err.Error()))
		}
	}
	if siteChanged && AdtyOnSiteGroupConfigChanged != nil {
		AdtyOnSiteGroupConfigChanged()
	}
	for userID := range affectedUsers {
		if err := RefreshUserGroupCache(userID); err != nil {
			common.SysError(common.LogText("refresh user group cache after group migrate: %s", err.Error()))
		}
		if err := InvalidateUserTokensCache(userID); err != nil {
			common.SysError(common.LogText("invalidate token cache after group migrate: %s", err.Error()))
		}
	}
	if moved.Channels > 0 {
		_ = RefreshChannelCache()
	}
	InvalidatePricingCache()
	return moved, nil
}

// DeleteAdtyGroup 软删除任意未删除分组（含 default），只要系统里还剩下至少 1 个其他未删除分组。
// 同一个事务里清理渠道（若分组清空则手动禁用并删空 abilities）、分站 allowed_groups、用户默认分组、
// 全部 8 个分组配置项并软删除分组行；不改 tokens（避免静默重绑到不同倍率分组）。
// 提交后同步内存配置并刷新用户、渠道、定价与分站缓存。
func DeleteAdtyGroup(id string) error {
	if _, err := GetAdtyGroup(id); err != nil {
		return err
	}
	adtyGroupConfigMu.Lock()
	defer adtyGroupConfigMu.Unlock()
	var values map[string]string
	var channelsChanged bool
	affectedUsers := map[int]struct{}{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		// R9-008 / R2-44: Pre-lock all group option keys plus console_setting.uptime_kuma_groups
		// in sorted order BEFORE adty_groups -> adty_sites -> users -> channels.
		lockKeys := append(slices.Clone(adtyGroupOptionKeysSorted), "console_setting.uptime_kuma_groups")
		slices.Sort(lockKeys)
		if err := preLockAdtyGroupOptionsTx(tx, lockKeys); err != nil {
			return err
		}
		activeIDs, err := ActiveAdtyGroupIDsTx(tx, true)
		if err != nil {
			return err
		}
		if _, exists := activeIDs[id]; !exists {
			return ErrAdtyGroupNotFound
		}
		if len(activeIDs) <= 1 {
			return common.NewMessage("At least one active group must remain")
		}
		hasSiteTable, err := AdtyTableExists(tx, &AdtySite{})
		if err != nil {
			return err
		}
		if hasSiteTable {
			var sites []AdtySite
			if err := lockForUpdate(tx).Select("id", "status", "allowed_groups").
				Where("allowed_groups <> ''").
				Order("id").Find(&sites).Error; err != nil {
				return err
			}
			type siteAllowUpdate struct {
				id      int
				encoded string
			}
			pendingUpdates := make([]siteAllowUpdate, 0)
			for _, site := range sites {
				groups, err := parseAdtySiteAllowedGroups(site.AllowedGroups)
				if err != nil || !slices.Contains(groups, id) {
					continue
				}
				next := slices.DeleteFunc(slices.Clone(groups), func(group string) bool { return group == id })
				if len(next) == 0 {
					if site.Status != AdtySiteStatusClosed && site.Status != AdtySiteStatusDeleting {
						return common.NewMessage("This group is the only allowed group for an active reseller site. Update the site's allowed groups first")
					}
					pendingUpdates = append(pendingUpdates, siteAllowUpdate{id: site.Id, encoded: "[]"})
					continue
				}
				raw, err := common.Marshal(next)
				if err != nil {
					return err
				}
				pendingUpdates = append(pendingUpdates, siteAllowUpdate{id: site.Id, encoded: string(raw)})
			}
			for _, upd := range pendingUpdates {
				if err := tx.Model(&AdtySite{}).Where("id = ?", upd.id).Update("allowed_groups", upd.encoded).Error; err != nil {
					return err
				}
			}
		}
		hasUserTable, err := AdtyTableExists(tx, &User{})
		if err != nil {
			return err
		}
		if hasUserTable {
			var users []User
			if err := lockForUpdate(tx.Unscoped()).Select("id").Where(AdtyGroupColumn()+" = ?", id).Order("id").Find(&users).Error; err != nil {
				return err
			}
			if len(users) > 0 {
				ids := make([]int, 0, len(users))
				for _, user := range users {
					ids = append(ids, user.Id)
					affectedUsers[user.Id] = struct{}{}
				}
				if err := tx.Unscoped().Model(&User{}).Where("id IN ?", ids).Update(AdtyGroupColumn(), "").Error; err != nil {
					return err
				}
			}
		}
		// Keep tokens untouched so existing API keys are never silently rebound to another group.
		hasChannelTable, err := AdtyTableExists(tx, &Channel{})
		if err != nil {
			return err
		}
		if hasChannelTable {
			hasAbilityTable, err := AdtyTableExists(tx, &Ability{})
			if err != nil {
				return err
			}
			var channels []Channel
			if err := lockForUpdate(tx).Select("id", "models", "status", commonGroupCol, "priority", "weight", "tag").Order("id").Find(&channels).Error; err != nil {
				return err
			}
			for _, channel := range channels {
				groups := channel.GetGroups()
				if !slices.Contains(groups, id) {
					continue
				}
				next := make([]string, 0, len(groups))
				for _, g := range groups {
					g = strings.TrimSpace(g)
					if g == "" || g == id || slices.Contains(next, g) {
						continue
					}
					next = append(next, g)
				}
				if len(next) == 0 {
					if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
						commonGroupCol: "",
						"status":       common.ChannelStatusManuallyDisabled,
					}).Error; err != nil {
						return err
					}
					if hasAbilityTable {
						if err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error; err != nil {
							return err
						}
					}
				} else {
					channel.Group = strings.Join(next, ",")
					if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Update(commonGroupCol, channel.Group).Error; err != nil {
						return err
					}
					if hasAbilityTable {
						if err := channel.UpdateAbilities(tx); err != nil {
							return err
						}
					}
				}
				channelsChanged = true
			}
		}
		values, err = adtyGroupOptionsWithout(id, func(key string) (string, error) {
			return loadAdtyGroupOptionTx(tx, key)
		})
		if err != nil {
			return err
		}
		hasOptionTable, err := AdtyTableExists(tx, &Option{})
		if err != nil {
			return err
		}
		if hasOptionTable {
			var uptimeOpt Option
			if findErr := lockForUpdate(tx).Where(commonKeyCol+" = ?", "console_setting.uptime_kuma_groups").Limit(1).Find(&uptimeOpt).Error; findErr != nil {
				return findErr
			}
			if trimmed := strings.TrimSpace(uptimeOpt.Value); trimmed != "" {
				var rawGroups []map[string]any
				if unmarshalErr := common.UnmarshalJsonStr(trimmed, &rawGroups); unmarshalErr == nil {
					filtered := make([]map[string]any, 0, len(rawGroups))
					changed := false
					for _, entry := range rawGroups {
						gid, _ := entry["groupId"].(string)
						cat, _ := entry["categoryName"].(string)
						trimmedGID := strings.TrimSpace(gid)
						if trimmedGID == id || (trimmedGID == "" && strings.TrimSpace(cat) == id) {
							changed = true
							continue
						}
						filtered = append(filtered, entry)
					}
					if changed {
						if encoded, marshalErr := common.Marshal(filtered); marshalErr == nil {
							values["console_setting.uptime_kuma_groups"] = string(encoded)
						}
					}
				}
			}
		}
		hasModelProbeTable, err := AdtyTableExists(tx, &AdtyModelProbe{})
		if err != nil {
			return err
		}
		if hasModelProbeTable {
			if err := tx.Where("group_id = ?", id).Delete(&AdtyModelProbe{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("id = ?", id).Delete(&AdtyGroup{}).Error; err != nil {
			return err
		}
		if hasOptionTable {
			if err := SaveOptionsTx(tx, values); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()
	for key, value := range values {
		if err := updateOptionMap(key, value); err != nil {
			common.SysError(common.LogText("update option map after group delete (%s): %s", key, err.Error()))
		}
	}
	ratio_setting.InvalidateExposedDataCache()
	for userID := range affectedUsers {
		if err := RefreshUserGroupCache(userID); err != nil {
			common.SysError(common.LogText("refresh user group cache after group delete: %s", err.Error()))
		}
	}
	if channelsChanged {
		_ = RefreshChannelCache()
	}
	InvalidatePricingCache()
	if AdtyOnSiteGroupConfigChanged != nil {
		AdtyOnSiteGroupConfigChanged()
	}
	return nil
}

// SyncActiveAdtyGroupsToOptions 幂等同步所有未删除的 adty_groups 到 options.GroupRatio 与
// options.UserUsableGroups（缺项补默认倍率 1 与分组名），同时清除已软删除的分组键，
// 持久化后刷新内存设置与定价缓存。
func SyncActiveAdtyGroupsToOptions() error {
	if DB == nil {
		return nil
	}
	hasGroupTable, err := AdtyTableExists(DB, &AdtyGroup{})
	if err != nil {
		return err
	}
	if !hasGroupTable {
		return nil
	}
	adtyGroupConfigMu.Lock()
	defer adtyGroupConfigMu.Unlock()
	var optionValues map[string]string
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := preLockAdtyGroupOptionsTx(tx, []string{"GroupRatio", "UserUsableGroups"}); err != nil {
			return err
		}
		var allGroups []AdtyGroup
		if err := tx.Unscoped().Order("id ASC").Find(&allGroups).Error; err != nil {
			return err
		}
		if len(allGroups) == 0 {
			return nil
		}
		activeGroups := make([]AdtyGroup, 0, len(allGroups))
		deletedSet := make(map[string]struct{})
		for _, g := range allGroups {
			id := strings.TrimSpace(g.ID)
			if id == "" || id == "auto" {
				continue
			}
			if g.Deleted() {
				deletedSet[id] = struct{}{}
				continue
			}
			activeGroups = append(activeGroups, g)
		}
		var load func(string) (string, error)
		hasOptionTable, err := AdtyTableExists(tx, &Option{})
		if err != nil {
			return err
		}
		if hasOptionTable {
			load = func(key string) (string, error) {
				return loadAdtyGroupOptionTx(tx, key)
			}
		}
		ratioRaw, err := adtyGroupOptionSource(load, "GroupRatio", ratio_setting.GroupRatio2JSONString())
		if err != nil {
			return err
		}
		ratios := map[string]float64{}
		if err := adtyUnmarshalObject(ratioRaw, &ratios); err != nil {
			return err
		}
		ratioChanged := false
		for _, g := range activeGroups {
			id := strings.TrimSpace(g.ID)
			if _, exists := ratios[id]; !exists {
				ratios[id] = 1
				ratioChanged = true
			}
		}
		for id := range deletedSet {
			if _, exists := ratios[id]; exists {
				delete(ratios, id)
				ratioChanged = true
			}
		}

		usableRaw, err := adtyGroupOptionSource(load, "UserUsableGroups", setting.UserUsableGroups2JSONString())
		if err != nil {
			return err
		}
		usable := map[string]string{}
		if err := adtyUnmarshalObject(usableRaw, &usable); err != nil {
			return err
		}
		usableChanged := false
		for _, g := range activeGroups {
			id := strings.TrimSpace(g.ID)
			if existing, exists := usable[id]; !exists || strings.TrimSpace(existing) == "" {
				usable[id] = g.DisplayName()
				usableChanged = true
			}
		}
		for id := range deletedSet {
			if _, exists := usable[id]; exists {
				delete(usable, id)
				usableChanged = true
			}
		}

		if !ratioChanged && !usableChanged {
			return nil
		}
		optionValues = map[string]string{}
		if ratioChanged {
			ratioBytes, err := common.Marshal(ratios)
			if err != nil {
				return err
			}
			optionValues["GroupRatio"] = string(ratioBytes)
		}
		if usableChanged {
			usableBytes, err := common.Marshal(usable)
			if err != nil {
				return err
			}
			optionValues["UserUsableGroups"] = string(usableBytes)
		}
		if hasOptionTable && len(optionValues) > 0 {
			if err := SaveOptionsTx(tx, optionValues); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(optionValues) == 0 {
		return nil
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()
	for _, key := range []string{"GroupRatio", "UserUsableGroups"} {
		if value, ok := optionValues[key]; ok {
			if updateErr := updateOptionMap(key, value); updateErr != nil {
				common.SysError(common.LogText("update option map after sync active adty groups (%s): %s", key, updateErr.Error()))
			}
		}
	}
	ratio_setting.InvalidateExposedDataCache()
	InvalidatePricingCache()
	return nil
}

// loadAdtyGroupOptionTx 锁住这一行配置再读。没有这一行时返回空串，调用方改用当前内存。
func loadAdtyGroupOptionTx(tx *gorm.DB, key string) (string, error) {
	var rows []Option
	if err := lockForUpdate(tx).Where(commonKeyCol+" = ?", key).Limit(1).Find(&rows).Error; err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].Value, nil
}

func adtyGroupOptionSource(load func(string) (string, error), key, memory string) (string, error) {
	if load == nil {
		return memory, nil
	}
	raw, err := load(key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(raw) == "" {
		return memory, nil
	}
	return raw, nil
}

// adtyGroupOptionsWithout 返回去掉这个分组以后、内容有变化的分组配置（键 → 新的 JSON）。
// load 返回数据库里锁住的当前值。有行就以它为准，避免用删除开始前的内存快照盖掉后写入的倍率。
func adtyGroupOptionsWithout(id string, load func(string) (string, error)) (map[string]string, error) {
	values := map[string]string{}
	put := func(key string, changed bool, value any) error {
		if !changed {
			return nil
		}
		raw, err := common.Marshal(value)
		if err != nil {
			return err
		}
		values[key] = string(raw)
		return nil
	}

	ratioRaw, err := adtyGroupOptionSource(load, "GroupRatio", ratio_setting.GroupRatio2JSONString())
	if err != nil {
		return nil, err
	}
	ratios := map[string]float64{}
	if err := adtyUnmarshalObject(ratioRaw, &ratios); err != nil {
		return nil, err
	}
	_, had := ratios[id]
	delete(ratios, id)
	if err := put("GroupRatio", had, ratios); err != nil {
		return nil, err
	}

	usableRaw, err := adtyGroupOptionSource(load, "UserUsableGroups", setting.UserUsableGroups2JSONString())
	if err != nil {
		return nil, err
	}
	usable := map[string]string{}
	if err := adtyUnmarshalObject(usableRaw, &usable); err != nil {
		return nil, err
	}
	_, had = usable[id]
	delete(usable, id)
	if err := put("UserUsableGroups", had, usable); err != nil {
		return nil, err
	}

	topupRaw, err := adtyGroupOptionSource(load, "TopupGroupRatio", common.TopupGroupRatio2JSONString())
	if err != nil {
		return nil, err
	}
	topup := map[string]float64{}
	if err := adtyUnmarshalObject(topupRaw, &topup); err != nil {
		return nil, err
	}
	_, had = topup[id]
	delete(topup, id)
	if err := put("TopupGroupRatio", had, topup); err != nil {
		return nil, err
	}

	groupRaw, err := adtyGroupOptionSource(load, "GroupGroupRatio", ratio_setting.GroupGroupRatio2JSONString())
	if err != nil {
		return nil, err
	}
	nested := map[string]map[string]float64{}
	if err := adtyUnmarshalObject(groupRaw, &nested); err != nil {
		return nil, err
	}
	_, had = nested[id]
	delete(nested, id)
	for _, inner := range nested {
		if _, ok := inner[id]; ok {
			delete(inner, id)
			had = true
		}
	}
	if err := put("GroupGroupRatio", had, nested); err != nil {
		return nil, err
	}

	autoRaw, err := adtyGroupOptionSource(load, "AutoGroups", setting.AutoGroups2JsonString())
	if err != nil {
		return nil, err
	}
	auto := []string{}
	if err := adtyUnmarshalObject(autoRaw, &auto); err != nil {
		return nil, err
	}
	if err := put("AutoGroups", slices.Contains(auto, id), slices.DeleteFunc(slices.Clone(auto), func(group string) bool { return group == id })); err != nil {
		return nil, err
	}

	specialMemory := ""
	if spec := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup; spec != nil {
		specialMemory = spec.MarshalJSONString()
	}
	specialRaw, err := adtyGroupOptionSource(load, "group_ratio_setting.group_special_usable_group", specialMemory)
	if err != nil {
		return nil, err
	}
	special := map[string]map[string]string{}
	if err := adtyUnmarshalObject(specialRaw, &special); err != nil {
		return nil, err
	}
	_, had = special[id]
	delete(special, id)
	for owner, inner := range special {
		next := map[string]string{}
		for key, desc := range inner {
			if adtyStripGroupPrefix(key) == id {
				had = true
				continue
			}
			next[key] = desc
		}
		special[owner] = next
	}
	if err := put("group_ratio_setting.group_special_usable_group", had, special); err != nil {
		return nil, err
	}

	limitRaw, err := adtyGroupOptionSource(load, "ModelRequestRateLimitGroup", setting.ModelRequestRateLimitGroup2JSONString())
	if err != nil {
		return nil, err
	}
	limits := map[string][2]int{}
	if err := adtyUnmarshalObject(limitRaw, &limits); err != nil {
		return nil, err
	}
	_, had = limits[id]
	delete(limits, id)
	if err := put("ModelRequestRateLimitGroup", had, limits); err != nil {
		return nil, err
	}

	common.OptionMapRWMutex.RLock()
	gmaMemory := common.OptionMap[AdtyGroupModelAllowOption]
	common.OptionMapRWMutex.RUnlock()
	gmaRaw, err := adtyGroupOptionSource(load, AdtyGroupModelAllowOption, gmaMemory)
	if err != nil {
		return nil, err
	}
	gma := map[string][]string{}
	if err := adtyUnmarshalObject(gmaRaw, &gma); err != nil {
		return nil, err
	}
	_, had = gma[id]
	delete(gma, id)
	if err := put(AdtyGroupModelAllowOption, had, gma); err != nil {
		return nil, err
	}
	return values, nil
}

// adtyGroupOptionsMigrated 在分组迁移时把 GroupModelAllow 里的源分组模型白名单并入目标分组并删掉源分组。
func adtyGroupOptionsMigrated(fromID, toID string, load func(string) (string, error)) (map[string]string, error) {
	values := map[string]string{}
	common.OptionMapRWMutex.RLock()
	gmaMemory := common.OptionMap[AdtyGroupModelAllowOption]
	common.OptionMapRWMutex.RUnlock()
	gmaRaw, err := adtyGroupOptionSource(load, AdtyGroupModelAllowOption, gmaMemory)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(gmaRaw) == "" {
		return values, nil
	}
	gma := map[string][]string{}
	if err := adtyUnmarshalObject(gmaRaw, &gma); err != nil {
		return nil, err
	}
	fromModels, ok := gma[fromID]
	if !ok {
		return values, nil
	}
	// R9-033: Empty slice toID: [] means unrestricted; treat identically to !hasTo.
	if toModels, hasTo := gma[toID]; hasTo && len(toModels) > 0 {
		merged := make([]string, 0, len(toModels)+len(fromModels))
		for _, item := range toModels {
			item = strings.TrimSpace(item)
			if item != "" && !slices.Contains(merged, item) {
				merged = append(merged, item)
			}
		}
		for _, item := range fromModels {
			item = strings.TrimSpace(item)
			if item != "" && !slices.Contains(merged, item) {
				merged = append(merged, item)
			}
		}
		gma[toID] = merged
	}
	delete(gma, fromID)
	raw, err := common.Marshal(gma)
	if err != nil {
		return nil, err
	}
	values[AdtyGroupModelAllowOption] = string(raw)
	return values, nil
}

// ValidateAdtyGroupModelAllowSchema 校验 GroupModelAllow 的值结构：空串表示清空，
// 非空必须是「分组 ID → 非空模型名数组」的 JSON 对象，拒绝非对象、非数组、空数组元素或非字符串元素。
func ValidateAdtyGroupModelAllowSchema(raw string) (map[string][]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var generic any
	if err := common.UnmarshalJsonStr(raw, &generic); err != nil {
		return nil, common.NewMessage("{{key}} must be a JSON object", map[string]any{"key": AdtyGroupModelAllowOption})
	}
	object, ok := generic.(map[string]any)
	if !ok || object == nil {
		return nil, common.NewMessage("{{key}} must be a JSON object", map[string]any{"key": AdtyGroupModelAllowOption})
	}
	out := make(map[string][]string, len(object))
	for groupID, rawModels := range object {
		trimmedID := strings.TrimSpace(groupID)
		if trimmedID == "" || trimmedID != groupID {
			return nil, common.NewMessage("{{key}} must be a JSON object", map[string]any{"key": AdtyGroupModelAllowOption})
		}
		items, ok := rawModels.([]any)
		if !ok || rawModels == nil {
			return nil, common.NewMessage("{{key}} must be a JSON object", map[string]any{"key": AdtyGroupModelAllowOption})
		}
		models := make([]string, 0, len(items))
		for _, item := range items {
			name, ok := item.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return nil, common.NewMessage("{{key}} must be a JSON object", map[string]any{"key": AdtyGroupModelAllowOption})
			}
			models = append(models, strings.TrimSpace(name))
		}
		out[trimmedID] = models
	}
	return out, nil
}

// BeforeSave 在持久化 options 表前守住 GroupModelAllow 的结构与活动分组引用，防止非法值覆盖已有配置。
func (option *Option) BeforeSave(tx *gorm.DB) error {
	if option == nil || option.Key != AdtyGroupModelAllowOption {
		return nil
	}
	parsed, err := ValidateAdtyGroupModelAllowSchema(option.Value)
	if err != nil {
		return err
	}
	if len(parsed) == 0 {
		return nil
	}
	for id := range parsed {
		if id == "auto" {
			return common.NewMessage("Group auto cannot be used in {{key}}", map[string]any{"key": AdtyGroupModelAllowOption})
		}
	}
	if tx == nil {
		return nil
	}
	hasGroupTable, err := AdtyTableExists(tx, &AdtyGroup{})
	if err != nil {
		return err
	}
	if !hasGroupTable {
		return nil
	}
	var total int64
	if err := tx.Model(&AdtyGroup{}).Count(&total).Error; err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	var ids []string
	if err := tx.Model(&AdtyGroup{}).Pluck("id", &ids).Error; err != nil {
		return err
	}
	active := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		active[id] = struct{}{}
	}
	for id := range parsed {
		if _, ok := active[id]; !ok {
			return common.NewMessage("Unknown group ID {{id}} in {{key}}", map[string]any{"id": id, "key": AdtyGroupModelAllowOption})
		}
	}
	return nil
}
