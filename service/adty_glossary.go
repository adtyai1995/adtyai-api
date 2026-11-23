package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// AdtyOfficialGlossaryPair is one A.20.2 term in the official languages.
// English is the official counterpart already used in the repo or the plan.
type AdtyOfficialGlossaryPair struct {
	ZH string
	EN string
}

// AdtyOfficialGlossary is the protected A.20.2 terminology for zh-CN and en.
// These pairs are seeded into AdtyGlossaryTerm so dynamic translation cannot
// rewrite the business terms.
var AdtyOfficialGlossary = []AdtyOfficialGlossaryPair{
	{ZH: "本金", EN: "principal"},
	{ZH: "永久赠金", EN: "permanent bonus"},
	{ZH: "冻结金额", EN: "frozen amount"},
	{ZH: "可用余额", EN: "available balance"},
	{ZH: "收益", EN: "earnings"},
	{ZH: "保证金", EN: "deposit"},
	{ZH: "人工退款", EN: "manual refund"},
	{ZH: "佣金释放", EN: "commission release"},
	{ZH: "冲回", EN: "clawback"},
	{ZH: "追偿", EN: "recovery"},
}

const (
	AdtyOfficialGlossaryLocaleEN   = "en"
	AdtyOfficialGlossaryLocaleZhCN = "zh-CN"
)

// SeedAdtyOfficialGlossary upserts the official A.20.2 pairs for zh-CN and en.
// Existing rows for the same source and locale are updated to the official target.
func SeedAdtyOfficialGlossary() error {
	for _, term := range AdtyOfficialGlossary {
		if err := SaveAdtyGlossaryTerm(term.ZH, AdtyOfficialGlossaryLocaleEN, term.EN); err != nil {
			return err
		}
		if err := SaveAdtyGlossaryTerm(term.EN, AdtyOfficialGlossaryLocaleZhCN, term.ZH); err != nil {
			return err
		}
	}
	return nil
}

// ListAdtyGlossaryTerms returns configured glossary entries for Root administration.
func ListAdtyGlossaryTerms(locale string) ([]model.AdtyGlossaryTerm, error) {
	if model.DB == nil {
		return nil, ErrAdtyTranslationInvalid
	}
	query := model.DB.Model(&model.AdtyGlossaryTerm{})
	if loc := strings.TrimSpace(locale); loc != "" {
		query = query.Where("locale = ?", loc)
	}
	var terms []model.AdtyGlossaryTerm
	if err := query.Order("locale ASC, source ASC, id ASC").Find(&terms).Error; err != nil {
		return nil, err
	}
	return terms, nil
}

// UpsertAdtyGlossaryTerm creates or updates one shared Chinese/English glossary entry.
func UpsertAdtyGlossaryTerm(id int64, source, locale, target string) (*model.AdtyGlossaryTerm, error) {
	if model.DB == nil {
		return nil, ErrAdtyTranslationInvalid
	}
	source = strings.TrimSpace(source)
	locale = strings.TrimSpace(locale)
	target = strings.TrimSpace(target)
	if locale == "" {
		locale = AdtyOfficialGlossaryLocaleEN
	}
	if source == "" || target == "" || (locale != AdtyOfficialGlossaryLocaleEN && locale != AdtyOfficialGlossaryLocaleZhCN) {
		return nil, ErrAdtyTranslationInvalid
	}
	if id > 0 {
		var row model.AdtyGlossaryTerm
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtyTranslationInvalid
				}
				return err
			}
			var conflict model.AdtyGlossaryTerm
			lookup := tx.Where("source = ? AND locale = ? AND id <> ?", source, locale, id).Limit(1).Find(&conflict)
			if lookup.Error != nil {
				return lookup.Error
			}
			if lookup.RowsAffected > 0 {
				return ErrAdtyTranslationInvalid
			}
			row.Source = source
			row.Locale = locale
			row.Target = target
			return tx.Save(&row).Error
		})
		if err != nil {
			return nil, err
		}
		return &row, nil
	}
	if err := SaveAdtyGlossaryTerm(source, locale, target); err != nil {
		return nil, err
	}
	var saved model.AdtyGlossaryTerm
	if err := model.DB.Where("source = ? AND locale = ?", source, locale).First(&saved).Error; err != nil {
		return nil, err
	}
	return &saved, nil
}

// DeleteAdtyGlossaryTerm removes one glossary entry by ID.
func DeleteAdtyGlossaryTerm(id int64) error {
	if model.DB == nil || id <= 0 {
		return ErrAdtyTranslationInvalid
	}
	res := model.DB.Delete(&model.AdtyGlossaryTerm{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAdtyTranslationInvalid
	}
	return nil
}

func adtyMatchedGlossaryTerms(db *gorm.DB, source string) []model.AdtyGlossaryTerm {
	if db == nil || strings.TrimSpace(source) == "" || !db.Migrator().HasTable(&model.AdtyGlossaryTerm{}) {
		return nil
	}
	var terms []model.AdtyGlossaryTerm
	if err := db.Where("locale = ?", AdtyOfficialGlossaryLocaleEN).Order("id ASC").Find(&terms).Error; err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(terms))
	matched := make([]model.AdtyGlossaryTerm, 0, len(terms))
	for _, term := range terms {
		src := strings.TrimSpace(term.Source)
		dst := strings.TrimSpace(term.Target)
		if src == "" || dst == "" {
			continue
		}
		if _, dup := seen[src]; dup {
			continue
		}
		if strings.Contains(source, src) {
			seen[src] = struct{}{}
			term.Source = src
			term.Target = dst
			matched = append(matched, term)
		}
	}
	return matched
}

func buildAdtyTranslationSystemPrompt(source string) string {
	terms := adtyMatchedGlossaryTerms(model.DB, source)
	if len(terms) == 0 {
		return adtyTranslationSystemPrompt
	}
	var b strings.Builder
	b.WriteString(adtyTranslationSystemPrompt)
	b.WriteString("\nYou must use these exact English glossary translations for the matching Chinese terms: ")
	for i, term := range terms {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteByte('"')
		b.WriteString(term.Source)
		b.WriteString(`" -> "`)
		b.WriteString(term.Target)
		b.WriteByte('"')
	}
	b.WriteByte('.')
	return b.String()
}

func adtyTranslationKeepsGlossary(source, english string) bool {
	return adtyTranslationKeepsGlossaryWithDB(model.DB, source, english)
}

func adtyTranslationKeepsGlossaryWithDB(db *gorm.DB, source, english string) bool {
	if db == nil {
		return false
	}
	var terms []model.AdtyGlossaryTerm
	if err := db.Where("locale = ?", AdtyOfficialGlossaryLocaleEN).Find(&terms).Error; err != nil {
		return false
	}
	lower := strings.ToLower(english)
	for _, term := range terms {
		if term.Source == "" || term.Target == "" {
			continue
		}
		if strings.Contains(source, term.Source) && !strings.Contains(lower, strings.ToLower(term.Target)) {
			return false
		}
	}
	return true
}
