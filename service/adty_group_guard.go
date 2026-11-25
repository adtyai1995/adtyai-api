package service

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// adtyGroupSpecialUsableKey 是特殊可用分组在 options 表里的键（带点的 config 键）。
const adtyGroupSpecialUsableKey = "group_ratio_setting.group_special_usable_group"

func init() {
	model.AdtyOnSiteGroupConfigChanged = InvalidateAdtySiteCache
	model.ValidateAdtyGroupOptionTx = ValidateAdtyGroupOptionTx
}

// IsAdtyGroupOption 说明这个配置项的键或值里写的是分组 ID，保存前要校验。
func IsAdtyGroupOption(key string) bool {
	switch key {
	case "GroupRatio", "TopupGroupRatio", "UserUsableGroups", "GroupGroupRatio",
		"AutoGroups", "ModelRequestRateLimitGroup", groupModelAllowOption, adtyGroupSpecialUsableKey:
		return true
	}
	return false
}

// ValidateAdtyGroupOption 检查分组类配置里出现的每个分组 ID 都在分组表里（未删除）。
// auto 分组已退役，禁止在任何分组配置项中出现。
// GroupRatio 还不能漏掉任何未删除的分组：删除分组只能走迁移和删除。
// GroupModelAllow 额外在保存前强校验「分组 ID → 非空模型名数组」结构，其余配置项的格式错误留给上游各自的检查。
func ValidateAdtyGroupOption(key, value string) error {
	return validateAdtyGroupOption(nil, key, value, false)
}

// ValidateAdtyGroupOptionTx locks live adty_groups inside tx (canonical order:
// options -> adty_groups) and validates all 8 group-scoped options against the
// locked group table to eliminate delete-vs-option TOCTOU races (R9-008).
func ValidateAdtyGroupOptionTx(tx *gorm.DB, key, value string) error {
	return validateAdtyGroupOption(tx, key, value, true)
}

func validateAdtyGroupOption(tx *gorm.DB, key, value string, lock bool) error {
	if !IsAdtyGroupOption(key) {
		return nil
	}
	ids, err := groupIDsInOption(key, value)
	if err != nil {
		if key == groupModelAllowOption {
			return err
		}
		return nil
	}
	if key == groupModelAllowOption && len(ids) == 0 {
		return nil
	}
	var active map[string]struct{}
	if tx != nil {
		active, err = model.ActiveAdtyGroupIDsTx(tx, lock)
		if err != nil {
			return err
		}
		if len(active) == 0 {
			return nil
		}
	} else {
		active, err = model.ActiveAdtyGroupIDs()
		if err != nil {
			return err
		}
	}
	for _, ref := range ids {
		if ref.id == "auto" {
			if ref.autoAllowed {
				continue
			}
			return common.NewMessage("Group auto cannot be used in {{key}}", map[string]any{"key": key})
		}
		if _, ok := active[ref.id]; !ok {
			return common.NewMessage("Unknown group ID {{id}} in {{key}}", map[string]any{"id": ref.id, "key": key})
		}
	}
	if key != "GroupRatio" {
		return nil
	}
	present := make(map[string]struct{}, len(ids))
	for _, ref := range ids {
		present[ref.id] = struct{}{}
	}
	missing := make([]string, 0)
	for id := range active {
		if _, ok := present[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return common.NewMessage("Group ratios are missing groups {{ids}}. Migrate and delete a group instead of removing its ratio", map[string]any{"ids": strings.Join(missing, ", ")})
}

type adtyGroupRef struct {
	id          string
	autoAllowed bool
}

func groupIDsInOption(key, value string) ([]adtyGroupRef, error) {
	var refs []adtyGroupRef
	add := func(id string, autoAllowed bool) {
		refs = append(refs, adtyGroupRef{id: strings.TrimSpace(id), autoAllowed: autoAllowed})
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	switch key {
	case groupModelAllowOption:
		parsed, err := model.ValidateAdtyGroupModelAllowSchema(value)
		if err != nil {
			return nil, err
		}
		for id := range parsed {
			add(id, false)
		}
	case "GroupRatio", "TopupGroupRatio", "UserUsableGroups", "ModelRequestRateLimitGroup":
		object := map[string]any{}
		if err := common.UnmarshalJsonStr(value, &object); err != nil {
			return nil, err
		}
		for id := range object {
			add(id, false)
		}
	case "GroupGroupRatio":
		nested := map[string]map[string]any{}
		if err := common.UnmarshalJsonStr(value, &nested); err != nil {
			return nil, err
		}
		for outer, inner := range nested {
			add(outer, false)
			for id := range inner {
				add(id, false)
			}
		}
	case "AutoGroups":
		var list []string
		if err := common.UnmarshalJsonStr(value, &list); err != nil {
			return nil, err
		}
		for _, id := range list {
			add(id, false)
		}
	case adtyGroupSpecialUsableKey:
		nested := map[string]map[string]any{}
		if err := common.UnmarshalJsonStr(value, &nested); err != nil {
			return nil, err
		}
		for owner, inner := range nested {
			add(owner, false)
			for id := range inner {
				if after, ok := strings.CutPrefix(id, "+:"); ok {
					id = after
				} else if after, ok := strings.CutPrefix(id, "-:"); ok {
					id = after
				}
				add(id, false)
			}
		}
	}
	return refs, nil
}
