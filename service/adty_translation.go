package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	AdtyTranslationNotReady      = "英文译文未就绪"
	adtyTranslationLeaseDuration = 5 * time.Minute
)

var (
	ErrAdtyTranslationInvalid = common.NewMessage("Invalid parameters")
	ErrAdtyTranslationSelf    = common.NewMessage("The translator cannot call this relay")
	ErrAdtyTranslationQuota   = common.NewMessage("Daily translation quota reached")

	adtyTranslationFlight       sync.Mutex
	adtyTranslationInflight     int
	adtyTranslationHeadMu       sync.Mutex
	adtyTranslationUsageMu      sync.Mutex
	adtyTranslationSiteUsageDay string
	adtyTranslationSiteUsageDB  *gorm.DB
	adtyTranslationSiteCalls    map[int]int

	adtyAmountToken         = regexp.MustCompile(`\d+(?:\.\d+)?`)
	adtyURLToken            = regexp.MustCompile(`https?://[^\s,，。；;！!？?)"'<>]+`)
	adtyPlaceholderToken    = regexp.MustCompile(`\{\{[^}]+\}\}|\{[0-9]+\}|%[sd]`)
	adtyOrderToken          = regexp.MustCompile(`[A-Za-z]{0,6}\d{6,}`)
	adtyWordRunToken        = regexp.MustCompile(`[A-Za-z]+`)
	adtyCurrencySymbolToken = regexp.MustCompile(`[$¥￥€£]`)
	adtyNegationSource      = regexp.MustCompile(`不|未|禁止|没有|勿`)
	adtyNegationTarget      = regexp.MustCompile(`(?i)\b(not|no|never|without|cannot|can['’]t|do not|don['’]t|doesn['’]t|didn['’]t|isn['’]t|aren['’]t|wasn['’]t|weren['’]t|won['’]t|wouldn['’]t|shouldn['’]t|couldn['’]t|mustn['’]t|haven['’]t|hasn['’]t|hadn['’]t|unable|unavailable|prohibited|forbidden|invalid|denied|blocked|disallowed|refused|none|neither|nor)\b`)
	adtyHTMLTagPattern      = regexp.MustCompile(`(?i)</?(?:p|br|div|span|strong|b|em|i|u|h[1-6]|ul|ol|li|blockquote|font)\b[^>]*>`)

	adtyProtectedCurrencies = map[string]struct{}{
		"USD":  {},
		"USDT": {},
		"USDC": {},
		"EUR":  {},
		"CNY":  {},
		"RMB":  {},
		"HKD":  {},
		"GBP":  {},
		"JPY":  {},
		"CAD":  {},
		"AUD":  {},
		"SGD":  {},
	}
)

// AdtyTranslateFunc performs one system translation. The default calls the Root-configured
// translator over HTTP (see adty_translation_provider.go); it does not debit a customer.
// Tests replace it with a fake to keep the rest of the chain offline.
var AdtyTranslateFunc = adtyTranslateViaProvider

// AdtyTranslationBackoffFunc calculates the retry backoff duration for 1-based retry index.
var AdtyTranslationBackoffFunc = func(retry int) time.Duration {
	if retry <= 0 {
		return 0
	}
	shift := retry - 1
	if shift > 4 {
		shift = 4
	}
	return time.Duration(1<<shift) * 50 * time.Millisecond
}

var adtyTranslationSleep = time.Sleep

type AdtyTranslationView struct {
	Mode         string `json:"mode"`
	Protocol     string `json:"protocol"`
	BaseURL      string `json:"base_url"`
	ModelName    string `json:"model_name"`
	BackupModel  string `json:"backup_model"`
	TokenMask    string `json:"token_mask"`
	TimeoutSec   int    `json:"timeout_sec"`
	Concurrency  int    `json:"concurrency"`
	MaxChars     int    `json:"max_chars"`
	DailyQuota   int    `json:"daily_quota"`
	RetryLimit   int    `json:"retry_limit"`
	Enabled      bool   `json:"enabled"`
	SavedAt      int64  `json:"saved_at"`
	TestedAt     int64  `json:"tested_at"`
	TranslatedAt int64  `json:"translated_at"`
}

type AdtyTranslationInput struct {
	Mode        string `json:"mode"`
	Protocol    string `json:"protocol"`
	BaseURL     string `json:"base_url"`
	ModelName   string `json:"model_name"`
	BackupModel string `json:"backup_model"`
	Token       string `json:"token"`
	TimeoutSec  int    `json:"timeout_sec"`
	Concurrency int    `json:"concurrency"`
	MaxChars    int    `json:"max_chars"`
	DailyQuota  int    `json:"daily_quota"`
	RetryLimit  int    `json:"retry_limit"`
	Enabled     bool   `json:"enabled"`
	RequestHost string `json:"-"`
}

type AdtyTranslationResult struct {
	ObjectKey string `json:"object_key"`
	Source    string `json:"source"`
	English   string `json:"english"`
	Status    string `json:"status"`
	Notice    string `json:"notice"`
	Version   int    `json:"version"`
	Reused    bool   `json:"reused"`
}

type AdtyTranslationTaskView struct {
	Id          int64  `json:"id"`
	JobId       int64  `json:"job_id"`
	ObjectKey   string `json:"object_key"`
	SiteId      int    `json:"site_id"`
	Version     int    `json:"version"`
	SourceHash  string `json:"source_hash"`
	Source      string `json:"source"`
	English     string `json:"english"`
	Status      string `json:"status"`
	HumanEdited bool   `json:"human_edited"`
	ModelName   string `json:"model_name"`
	Notice      string `json:"notice"`
	UpdatedAt   int64  `json:"updated_at"`
	FinishedAt  int64  `json:"finished_at"`
}

func GetAdtyTranslationConfig() (*AdtyTranslationView, error) {
	var row model.AdtyTranslationConfig
	if err := model.DB.First(&row, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &AdtyTranslationView{}, nil
		}
		return nil, err
	}
	return adtyTranslationView(row), nil
}

func SaveAdtyTranslationConfig(input AdtyTranslationInput, audits ...model.AuditLog) (*AdtyTranslationView, error) {
	input.Mode = strings.TrimSpace(input.Mode)
	input.Protocol = strings.TrimSpace(input.Protocol)
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	input.ModelName = strings.TrimSpace(input.ModelName)
	input.BackupModel = strings.TrimSpace(input.BackupModel)
	input.Token = strings.TrimSpace(input.Token)
	for _, r := range input.Token {
		if r < 0x20 || r == 0x7f {
			return nil, ErrAdtyTranslationInvalid
		}
	}
	if input.Mode != model.AdtyTranslationModeOwn && input.Mode != model.AdtyTranslationModeSet {
		return nil, ErrAdtyTranslationInvalid
	}
	if input.ModelName == "" || input.MaxChars < 0 || input.DailyQuota < 0 || input.RetryLimit < 0 || input.Concurrency < 0 ||
		utf8.RuneCountInString(input.BaseURL) > 256 || utf8.RuneCountInString(input.ModelName) > 80 || utf8.RuneCountInString(input.BackupModel) > 80 {
		return nil, ErrAdtyTranslationInvalid
	}
	if input.Mode == model.AdtyTranslationModeOwn {
		protocol, err := adtyTranslationProtocolName(input.Protocol)
		if err != nil {
			return nil, err
		}
		input.Protocol = protocol
		if err := rejectAdtyTranslationSelf(input.BaseURL, input.RequestHost, input.Token); err != nil {
			return nil, err
		}
	} else {
		if input.Protocol != "" {
			protocol, err := adtyTranslationProtocolName(input.Protocol)
			if err != nil {
				return nil, err
			}
			input.Protocol = protocol
		}
		for _, candidateModel := range []string{input.ModelName, input.BackupModel} {
			if candidateModel == "" {
				continue
			}
			if _, _, _, _, _, err := resolveAdtyPlatformChannel(candidateModel, input.RequestHost); err != nil {
				if errors.Is(err, ErrAdtyTranslationUnsupportedChannel) || errors.Is(err, ErrAdtyTranslationSelf) {
					return nil, err
				}
			}
		}
	}
	var saved model.AdtyTranslationConfig
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		lookup := model.AdtyLockForUpdate(tx).First(&saved, 1)
		missing := errors.Is(lookup.Error, gorm.ErrRecordNotFound)
		if lookup.Error != nil && !missing {
			return lookup.Error
		}
		if input.Mode == model.AdtyTranslationModeOwn && strings.TrimSpace(input.Token) == "" && strings.TrimSpace(saved.TokenCipher) == "" {
			return common.NewMessage("API key is required for independent translation mode")
		}
		snapshot := func(row model.AdtyTranslationConfig) map[string]any {
			return map[string]any{"mode": row.Mode, "protocol": row.Protocol, "model_name": row.ModelName, "backup_model": row.BackupModel, "timeout_sec": row.TimeoutSec, "concurrency": row.Concurrency, "max_chars": row.MaxChars, "daily_quota": row.DailyQuota, "retry_limit": row.RetryLimit, "enabled": row.Enabled, "configured": row.TokenCipher != ""}
		}
		before := snapshot(saved)
		saved.Id = 1
		saved.Mode = input.Mode
		if input.Mode == model.AdtyTranslationModeOwn {
			saved.Protocol = input.Protocol
			saved.BaseURL = input.BaseURL
		} else {
			if input.Protocol != "" {
				saved.Protocol = input.Protocol
			}
			if input.BaseURL != "" {
				saved.BaseURL = input.BaseURL
			}
		}
		saved.ModelName = input.ModelName
		saved.BackupModel = strings.TrimSpace(input.BackupModel)
		saved.TimeoutSec = input.TimeoutSec
		saved.Concurrency = input.Concurrency
		saved.MaxChars = input.MaxChars
		saved.DailyQuota = input.DailyQuota
		saved.RetryLimit = input.RetryLimit
		saved.Enabled = input.Enabled
		saved.SavedAt = time.Now().Unix()
		if strings.TrimSpace(input.Token) != "" {
			if err := rejectAdtyCustomerTokenWithDB(tx, input.Token); err != nil {
				return err
			}
			cipher, err := model.SealAdtyPurposeSecret("translation", input.Token)
			if err != nil {
				return err
			}
			saved.TokenCipher = cipher
			saved.TokenMask = adtyMaskTail(input.Token)
		}
		if missing {
			if err := tx.Create(&saved).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&saved).Error; err != nil {
			return err
		}
		for _, event := range audits {
			event.BeforeValue = common.GetJsonString(before)
			event.AfterValue = common.GetJsonString(snapshot(saved))
			if err := model.AppendAuditIntentTx(tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return adtyTranslationView(saved), nil
}

func TestAdtyTranslation(requestHost string) (*AdtyTranslationView, error) {
	cfg, err := loadAdtyTranslationConfig()
	if err != nil {
		return nil, err
	}
	switch cfg.Mode {
	case model.AdtyTranslationModeOwn:
		token, _ := model.OpenAdtyPurposeSecret("translation", cfg.TokenCipher)
		if err := rejectAdtyTranslationSelf(cfg.BaseURL, requestHost, token); err != nil {
			return nil, err
		}
	case model.AdtyTranslationModeSet:
		if _, _, _, _, _, err := resolveAdtyPlatformChannel(cfg.ModelName, requestHost); errors.Is(err, ErrAdtyTranslationSelf) || errors.Is(err, ErrAdtyTranslationUnsupportedChannel) {
			return nil, err
		}
	default:
		return nil, ErrAdtyTranslationInvalid
	}
	if _, err := callAdtyTranslatorForTest(cfg.ModelName, "连接测试"); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if err := model.DB.Model(cfg).Update("tested_at", now).Error; err != nil {
		return nil, err
	}
	cfg.TestedAt = now
	return adtyTranslationView(*cfg), nil
}

func adtyTranslationSourceHash(source string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(source)))
	return hex.EncodeToString(sum[:])
}

func adtyTranslationHeadMatchesSource(head model.AdtyTranslationHead, currentSource string) bool {
	norm := strings.TrimSpace(currentSource)
	if norm == "" {
		return false
	}
	if storedHash := strings.TrimSpace(head.SourceHash); storedHash != "" {
		return storedHash == adtyTranslationSourceHash(norm)
	}
	return strings.TrimSpace(head.Source) == norm
}

func invalidateAdtyTranslationHeadOnSecret(objectKey, sourceHash string, siteId, notifPubID int, notifRevision int64) {
	if objectKey == "" || model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		return
	}
	_ = withAdtyTranslationHeadLock(func(tx *gorm.DB) error {
		var head model.AdtyTranslationHead
		lookup := model.AdtyLockForUpdate(tx).Where("object_key = ?", objectKey).Limit(1).Find(&head)
		if lookup.Error != nil || lookup.RowsAffected == 0 {
			return lookup.Error
		}
		if notifPubID > 0 {
			ok, checkErr := adtyNotificationRevisionMatchesTx(tx, notifPubID, notifRevision)
			if checkErr != nil || !ok {
				return checkErr
			}
		}
		return tx.Model(&model.AdtyTranslationHead{}).Where("id = ?", head.Id).Updates(map[string]any{
			"site_id":      siteId,
			"version":      head.Version + 1,
			"source_hash":  sourceHash,
			"source":       "",
			"english":      "",
			"status":       model.AdtyTranslationFailed,
			"human_edited": false,
			"model_name":   "",
			"notice":       AdtyTranslationNotReady,
			"lease_until":  0,
		}).Error
	})
}

func adtyNotificationRevisionMatchesTx(tx *gorm.DB, pubID int, revision int64) (bool, error) {
	if tx == nil || pubID <= 0 || !tx.Migrator().HasTable(&model.AdtyNotificationPublication{}) {
		return true, nil
	}
	var pub model.AdtyNotificationPublication
	lookup := model.AdtyLockForUpdate(tx).Select("id", "status", "revision").Where("id = ?", pubID).Limit(1).Find(&pub)
	if lookup.Error != nil {
		return false, lookup.Error
	}
	if lookup.RowsAffected == 0 || pub.Status != "published" {
		return false, nil
	}
	if revision > 0 && pub.Revision != revision {
		return false, nil
	}
	return true, nil
}

// deleteAdtyNotificationTranslationHeadIfCurrent deletes a notification translation
// head only while holding the head + publication locks and confirming the live
// publication is still published at the expected revision (R10-035).
func deleteAdtyNotificationTranslationHeadIfCurrent(pubID int, revision int64, objectKey string) {
	if objectKey == "" || model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		return
	}
	_ = withAdtyTranslationHeadLock(func(tx *gorm.DB) error {
		var head model.AdtyTranslationHead
		lookup := model.AdtyLockForUpdate(tx).Where("object_key = ?", objectKey).Limit(1).Find(&head)
		if lookup.Error != nil || lookup.RowsAffected == 0 {
			return lookup.Error
		}
		ok, err := adtyNotificationRevisionMatchesTx(tx, pubID, revision)
		if err != nil || !ok {
			return err
		}
		return tx.Where("id = ?", head.Id).Delete(&model.AdtyTranslationHead{}).Error
	})
}

// translateAdtyNotificationDynamic runs TranslateAdtyDynamic while verifying
// under lock that the target notification is still published at revision (R10-035).
func translateAdtyNotificationDynamic(pubID int, revision int64, objectKey, source string, siteId int) (*AdtyTranslationResult, error) {
	return translateAdtyDynamicInternal(objectKey, source, siteId, pubID, revision)
}

func TranslateAdtyDynamic(objectKey, source string, siteId int) (*AdtyTranslationResult, error) {
	return translateAdtyDynamicInternal(objectKey, source, siteId, 0, 0)
}

func nextAdtyTranslationLeaseUntil(prevLeaseUntil int64) int64 {
	return max(time.Now().Add(adtyTranslationLeaseDuration).Unix(), prevLeaseUntil+1)
}

func adtyTranslationSiteDailyQuota(dailyQuota int) int {
	if dailyQuota <= 0 {
		return 0
	}
	return max(1, dailyQuota/4)
}

func adtyTranslationUTCDayBounds(now time.Time) (string, int64, int64) {
	utc := now.UTC()
	start := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	return start.Format("2006-01-02"), start.Unix(), start.Add(24 * time.Hour).Unix()
}

func adtyTranslationSiteCallsTodayWithDB(db *gorm.DB, siteID int, now time.Time) (int, error) {
	if siteID <= 0 || db == nil {
		return 0, nil
	}
	if !db.Migrator().HasTable(&model.AdtyTranslationJob{}) || !db.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		return 0, nil
	}
	_, dayStart, dayEnd := adtyTranslationUTCDayBounds(now)
	var count int64
	err := db.Table("adty_translation_jobs AS j").
		Joins("INNER JOIN adty_translation_heads AS h ON h.object_key = j.object_key").
		Where("h.site_id = ?", siteID).
		Where("((j.finished_at >= ? AND j.finished_at < ?) OR (j.finished_at = 0 AND j.created_at >= ? AND j.created_at < ?))", dayStart, dayEnd, dayStart, dayEnd).
		Where("(j.status = ? OR j.model_name <> '')", model.AdtyTranslationReady).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

func adtyTranslationSiteUsageToday(siteID int, totalCallsToday int) (int, error) {
	if siteID <= 0 {
		return 0, nil
	}
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	dbCalls, err := adtyTranslationSiteCallsTodayWithDB(model.DB, siteID, now)
	if err != nil {
		return 0, err
	}
	adtyTranslationUsageMu.Lock()
	if adtyTranslationSiteUsageDay != day || adtyTranslationSiteUsageDB != model.DB || totalCallsToday == 0 {
		adtyTranslationSiteUsageDay = day
		adtyTranslationSiteUsageDB = model.DB
		adtyTranslationSiteCalls = make(map[int]int)
	}
	memCalls := adtyTranslationSiteCalls[siteID]
	adtyTranslationUsageMu.Unlock()
	return max(dbCalls, memCalls), nil
}

func translateAdtyDynamicInternal(objectKey, source string, siteId, notifPubID int, notifRevision int64) (*AdtyTranslationResult, error) {
	source = strings.TrimSpace(source)
	objectKey = strings.TrimSpace(objectKey)
	if source == "" || objectKey == "" {
		return &AdtyTranslationResult{ObjectKey: objectKey, Source: source, Status: model.AdtyTranslationFailed, Notice: AdtyTranslationNotReady}, nil
	}
	if utf8.RuneCountInString(objectKey) > 160 {
		return &AdtyTranslationResult{ObjectKey: objectKey, Source: source, Status: model.AdtyTranslationFailed, Notice: AdtyTranslationNotReady}, ErrAdtyTranslationInvalid
	}
	hash := adtyTranslationSourceHash(source)
	if adtyTextHasOutboundSecret(source) {
		invalidateAdtyTranslationHeadOnSecret(objectKey, hash, siteId, notifPubID, notifRevision)
		return &AdtyTranslationResult{ObjectKey: objectKey, Source: source, Status: model.AdtyTranslationFailed, Notice: AdtyTranslationNotReady}, nil
	}

	var version int
	var leaseUntil int64
	var reused *AdtyTranslationResult
	var staleNotification bool
	err := withAdtyTranslationHeadLock(func(tx *gorm.DB) error {
		var head model.AdtyTranslationHead
		lookup := model.AdtyLockForUpdate(tx).Where("object_key = ?", objectKey).Limit(1).Find(&head)
		if lookup.Error != nil {
			return lookup.Error
		}
		if notifPubID > 0 {
			ok, checkErr := adtyNotificationRevisionMatchesTx(tx, notifPubID, notifRevision)
			if checkErr != nil {
				return checkErr
			}
			if !ok {
				staleNotification = true
				return nil
			}
		}
		if lookup.RowsAffected > 0 && head.SourceHash == hash && head.Status == model.AdtyTranslationReady && head.English != "" {
			reused = &AdtyTranslationResult{
				ObjectKey: objectKey,
				Source:    source,
				English:   head.English,
				Status:    head.Status,
				Version:   head.Version,
				Reused:    true,
			}
			return nil
		}
		if notifPubID == 0 && lookup.RowsAffected > 0 && head.SourceHash == hash && head.Status == model.AdtyTranslationPending && head.LeaseUntil > time.Now().Unix() {
			reused = &AdtyTranslationResult{
				ObjectKey: head.ObjectKey,
				Source:    head.Source,
				Version:   head.Version,
				Status:    head.Status,
				Notice:    head.Notice,
				Reused:    true,
			}
			return nil
		}
		version = 1
		var prevLease int64
		if lookup.RowsAffected > 0 {
			version = head.Version + 1
			prevLease = head.LeaseUntil
		}
		leaseUntil = nextAdtyTranslationLeaseUntil(prevLease)
		head.ObjectKey = objectKey
		head.SiteId = siteId
		head.Version = version
		head.SourceHash = hash
		head.Source = source
		head.English = ""
		head.Status = model.AdtyTranslationPending
		head.HumanEdited = false
		head.ModelName = ""
		head.Notice = AdtyTranslationNotReady
		head.LeaseUntil = leaseUntil
		if lookup.RowsAffected == 0 {
			if err := tx.Create(&head).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(&head).Updates(map[string]any{
				"site_id":      siteId,
				"version":      version,
				"source_hash":  hash,
				"source":       source,
				"english":      "",
				"status":       model.AdtyTranslationPending,
				"human_edited": false,
				"model_name":   "",
				"notice":       AdtyTranslationNotReady,
				"lease_until":  leaseUntil,
			}).Error; err != nil {
				return err
			}
		}
		return upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
			ObjectKey:  objectKey,
			Version:    version,
			SourceHash: hash,
			Status:     model.AdtyTranslationPending,
		})
	})
	if err != nil {
		return nil, err
	}
	if staleNotification {
		return nil, nil
	}
	if reused != nil {
		return reused, nil
	}

	return executeAdtyReservedTranslationWithGuard(objectKey, source, siteId, version, hash, notifPubID, notifRevision, leaseUntil)
}

func executeAdtyReservedTranslation(objectKey, source string, siteId, version int, hash string, expectedLeaseUntil int64) (*AdtyTranslationResult, error) {
	return executeAdtyReservedTranslationWithGuard(objectKey, source, siteId, version, hash, 0, 0, expectedLeaseUntil)
}

func executeAdtyReservedTranslationWithGuard(objectKey, source string, siteId, version int, hash string, notifPubID int, notifRevision int64, expectedLeaseUntil int64) (*AdtyTranslationResult, error) {
	if adtyTextHasOutboundSecret(source) {
		return fallbackAdtyTranslationWithGuard(objectKey, source, siteId, version, hash, "", notifPubID, notifRevision, expectedLeaseUntil)
	}
	cfg, err := loadAdtyTranslationConfig()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fallbackAdtyTranslationWithGuard(objectKey, source, siteId, version, hash, "", notifPubID, notifRevision, expectedLeaseUntil)
		}
		return nil, err
	}
	if !cfg.Enabled {
		return fallbackAdtyTranslationWithGuard(objectKey, source, siteId, version, hash, "", notifPubID, notifRevision, expectedLeaseUntil)
	}
	if cfg.MaxChars > 0 && len([]rune(source)) > cfg.MaxChars {
		return fallbackAdtyTranslationWithGuard(objectKey, source, siteId, version, hash, "", notifPubID, notifRevision, expectedLeaseUntil)
	}
	if cfg.DailyQuota > 0 {
		usage, err := AdtyTranslationUsageToday()
		if err != nil {
			return nil, err
		}
		if usage.Calls >= cfg.DailyQuota {
			return fallbackAdtyTranslationWithGuard(objectKey, source, siteId, version, hash, "", notifPubID, notifRevision, expectedLeaseUntil)
		}
		if siteId > 0 {
			siteCalls, err := adtyTranslationSiteUsageToday(siteId, usage.Calls)
			if err != nil {
				return nil, err
			}
			if siteCalls >= adtyTranslationSiteDailyQuota(cfg.DailyQuota) {
				return fallbackAdtyTranslationWithGuard(objectKey, source, siteId, version, hash, "", notifPubID, notifRevision, expectedLeaseUntil)
			}
		}
	}

	english, modelName, callErr := translateWithBackupForSite(cfg, source, siteId)
	if callErr == nil && adtyIsHTMLTranslation(model.DB, objectKey, source) {
		english = SanitizeAdtyNotificationHTML(english)
	}
	if callErr != nil || !adtyTranslationKeepsFacts(source, english) {
		return fallbackAdtyTranslationWithGuard(objectKey, source, siteId, version, hash, modelName, notifPubID, notifRevision, expectedLeaseUntil)
	}

	var saved model.AdtyTranslationHead
	var superseded bool
	err = withAdtyTranslationHeadLock(func(tx *gorm.DB) error {
		var current model.AdtyTranslationHead
		found := model.AdtyLockForUpdate(tx).Where("object_key = ?", objectKey).Limit(1).Find(&current)
		if found.Error != nil {
			return found.Error
		}
		if notifPubID > 0 {
			ok, checkErr := adtyNotificationRevisionMatchesTx(tx, notifPubID, notifRevision)
			if checkErr != nil {
				return checkErr
			}
			if !ok {
				if found.RowsAffected > 0 && !current.HumanEdited && current.Version == version &&
					current.Status == model.AdtyTranslationPending &&
					(current.SourceHash == "" || current.SourceHash == hash) &&
					(expectedLeaseUntil <= 0 || current.LeaseUntil == expectedLeaseUntil) {
					if err := tx.Model(&current).Updates(map[string]any{
						"status":      model.AdtyTranslationFailed,
						"notice":      AdtyTranslationNotReady,
						"model_name":  modelName,
						"lease_until": 0,
					}).Error; err != nil {
						return err
					}
					if err := upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
						ObjectKey:  objectKey,
						Version:    version,
						SourceHash: hash,
						Status:     model.AdtyTranslationFailed,
						ModelName:  modelName,
						FinishedAt: time.Now().Unix(),
					}); err != nil {
						return err
					}
					current.Status = model.AdtyTranslationFailed
					current.Notice = AdtyTranslationNotReady
					current.ModelName = modelName
					current.LeaseUntil = 0
				}
				superseded = true
				saved = current
				return nil
			}
		}
		// A retention cleanup or another worker may have completed the same
		// ticket translation while the provider was running.
		if strings.HasPrefix(objectKey, "ticket-reply:") && (found.RowsAffected == 0 || current.Status != model.AdtyTranslationPending) {
			saved = current
			superseded = found.RowsAffected == 0 || current.Status != model.AdtyTranslationReady
			return nil
		}
		if found.RowsAffected > 0 && (current.HumanEdited || current.Status == model.AdtyTranslationReady) && current.Version == version && (current.SourceHash == "" || current.SourceHash == hash) {
			if current.HumanEdited {
				_ = upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
					ObjectKey:  objectKey,
					Version:    version,
					SourceHash: hash,
					Status:     model.AdtyTranslationReady,
					ModelName:  modelName,
					FinishedAt: time.Now().Unix(),
				})
			}
			saved = current
			return nil
		}
		if found.RowsAffected > 0 && expectedLeaseUntil > 0 && current.LeaseUntil != expectedLeaseUntil {
			superseded = true
			saved = current
			return nil
		}
		if found.RowsAffected == 0 || current.HumanEdited || current.Version != version || current.SourceHash != hash || current.Status != model.AdtyTranslationPending {
			_ = upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
				ObjectKey:  objectKey,
				Version:    version,
				SourceHash: hash,
				Status:     model.AdtyTranslationFailed,
				ModelName:  modelName,
				FinishedAt: time.Now().Unix(),
			})
			superseded = true
			saved = current
			return nil
		}
		oldEnglish := strings.TrimSpace(current.English)
		var pub model.AdtyNotificationPublication
		hasPub := false
		if notifPubID == 0 {
			var pubErr error
			pub, hasPub, pubErr = lockAdtyNotificationPublicationForTranslationTx(tx, objectKey)
			if pubErr != nil {
				return pubErr
			}
		}
		commitEnglish := strings.TrimSpace(english)
		if hasPub && strings.EqualFold(strings.TrimSpace(pub.ContentFormat), "html") {
			commitEnglish = SanitizeAdtyNotificationHTML(commitEnglish)
			if strings.TrimSpace(commitEnglish) == "" {
				current.SiteId = siteId
				current.Version = version
				current.SourceHash = hash
				current.Source = source
				current.English = ""
				current.Status = model.AdtyTranslationFailed
				current.ModelName = modelName
				current.Notice = AdtyTranslationNotReady
				current.LeaseUntil = 0
				if err := tx.Save(&current).Error; err != nil {
					return err
				}
				if err := upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
					ObjectKey:  objectKey,
					Version:    version,
					SourceHash: hash,
					Status:     model.AdtyTranslationFailed,
					ModelName:  modelName,
					FinishedAt: time.Now().Unix(),
				}); err != nil {
					return err
				}
				saved = current
				return nil
			}
		}
		current.SiteId = siteId
		current.Version = version
		current.SourceHash = hash
		current.Source = source
		current.English = commitEnglish
		current.Status = model.AdtyTranslationReady
		current.ModelName = modelName
		current.Notice = ""
		current.LeaseUntil = 0
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		if err := upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
			ObjectKey:  objectKey,
			Version:    version,
			SourceHash: hash,
			Status:     model.AdtyTranslationReady,
			ModelName:  modelName,
			FinishedAt: time.Now().Unix(),
		}); err != nil {
			return err
		}
		if hasPub && pub.Status == "published" {
			if err := syncAdtyNotificationBodyWithTranslationTx(tx, &pub, current, oldEnglish, commitEnglish); err != nil {
				return err
			}
		}
		saved = current
		return tx.Model(&model.AdtyTranslationConfig{}).Where("id = ?", 1).Update("translated_at", time.Now().Unix()).Error
	})
	if err != nil {
		return nil, err
	}
	if superseded {
		return &AdtyTranslationResult{
			ObjectKey: objectKey,
			Source:    source,
			Status:    model.AdtyTranslationFailed,
			Notice:    AdtyTranslationNotReady,
			Version:   version,
		}, nil
	}
	return &AdtyTranslationResult{
		ObjectKey: objectKey,
		Source:    saved.Source,
		English:   saved.English,
		Status:    saved.Status,
		Notice:    saved.Notice,
		Version:   saved.Version,
	}, nil
}

// RetryAdtyTranslationJob retries an existing failed translation task for the same object_key and version.
func RetryAdtyTranslationJob(objectKey string, version int) (*AdtyTranslationResult, error) {
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		return nil, ErrAdtyTranslationInvalid
	}
	var head model.AdtyTranslationHead
	var leaseUntil int64
	var reused *AdtyTranslationResult
	var blocked *AdtyTranslationResult
	isTicketReply := strings.HasPrefix(objectKey, "ticket-reply:")
	err := withAdtyTranslationHeadLock(func(tx *gorm.DB) error {
		if err := model.AdtyLockForUpdate(tx).Where("object_key = ?", objectKey).First(&head).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtyTranslationInvalid
			}
			return err
		}
		if version <= 0 {
			version = head.Version
		}
		if head.HumanEdited || head.Version != version {
			return ErrAdtyTranslationInvalid
		}
		now := time.Now().Unix()
		if isTicketReply && head.Status == model.AdtyTranslationPending {
			reused = &AdtyTranslationResult{ObjectKey: head.ObjectKey, Source: head.Source, Version: head.Version, Status: head.Status, Notice: head.Notice, Reused: true}
			return nil
		}
		if !isTicketReply && head.Status == model.AdtyTranslationPending && head.LeaseUntil > now {
			reused = &AdtyTranslationResult{ObjectKey: head.ObjectKey, Source: head.Source, Version: head.Version, Status: head.Status, Notice: head.Notice, Reused: true}
			return nil
		}
		if head.Status == model.AdtyTranslationReady && head.English != "" {
			reused = &AdtyTranslationResult{
				ObjectKey: objectKey,
				Source:    head.Source,
				English:   head.English,
				Status:    head.Status,
				Version:   head.Version,
				Reused:    true,
			}
			return nil
		}
		if head.SourceHash == "" {
			sum := sha256.Sum256([]byte(head.Source))
			head.SourceHash = hex.EncodeToString(sum[:])
		}
		if strings.TrimSpace(head.Source) == "" || adtyTextHasOutboundSecret(head.Source) {
			if err := tx.Model(&head).Updates(map[string]any{
				"source_hash": head.SourceHash,
				"english":     "",
				"status":      model.AdtyTranslationFailed,
				"notice":      AdtyTranslationNotReady,
				"lease_until": 0,
			}).Error; err != nil {
				return err
			}
			if err := upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
				ObjectKey:  objectKey,
				Version:    version,
				SourceHash: head.SourceHash,
				Status:     model.AdtyTranslationFailed,
				ModelName:  head.ModelName,
				FinishedAt: now,
			}); err != nil {
				return err
			}
			blocked = &AdtyTranslationResult{
				ObjectKey: objectKey,
				Source:    head.Source,
				Status:    model.AdtyTranslationFailed,
				Notice:    AdtyTranslationNotReady,
				Version:   version,
			}
			return nil
		}
		leaseUntil = 0
		if !isTicketReply {
			leaseUntil = nextAdtyTranslationLeaseUntil(head.LeaseUntil)
		}
		if err := tx.Model(&head).Updates(map[string]any{
			"source_hash": head.SourceHash,
			"status":      model.AdtyTranslationPending,
			"notice":      AdtyTranslationNotReady,
			"lease_until": leaseUntil,
		}).Error; err != nil {
			return err
		}
		return upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
			ObjectKey:  objectKey,
			Version:    version,
			SourceHash: head.SourceHash,
			Status:     model.AdtyTranslationPending,
			ModelName:  head.ModelName,
		})
	})
	if err != nil {
		return nil, err
	}
	if reused != nil {
		return reused, nil
	}
	if blocked != nil {
		return blocked, nil
	}

	if isTicketReply {
		return &AdtyTranslationResult{ObjectKey: objectKey, Source: head.Source, Version: version, Status: model.AdtyTranslationPending, Notice: AdtyTranslationNotReady}, nil
	}
	return executeAdtyReservedTranslation(objectKey, head.Source, head.SiteId, version, head.SourceHash, leaseUntil)
}

// ListAdtyTranslationTasks lists current translation heads with their job metadata.
func ListAdtyTranslationTasks(status string) ([]AdtyTranslationTaskView, error) {
	if model.DB == nil {
		return nil, ErrAdtyTranslationInvalid
	}
	query := model.DB.Model(&model.AdtyTranslationHead{})
	if st := strings.TrimSpace(status); st != "" {
		query = query.Where("status = ?", st)
	}
	var heads []model.AdtyTranslationHead
	if err := query.Order("updated_at DESC, id DESC").Limit(100).Find(&heads).Error; err != nil {
		return nil, err
	}
	out := make([]AdtyTranslationTaskView, 0, len(heads))
	if len(heads) == 0 {
		return out, nil
	}
	type jobLookupKey struct {
		objectKey string
		version   int
	}
	objectKeys := make([]string, 0, len(heads))
	seenKeys := make(map[string]struct{}, len(heads))
	for _, h := range heads {
		if _, ok := seenKeys[h.ObjectKey]; !ok {
			seenKeys[h.ObjectKey] = struct{}{}
			objectKeys = append(objectKeys, h.ObjectKey)
		}
	}
	jobsByKey := make(map[jobLookupKey]model.AdtyTranslationJob, len(heads))
	if len(objectKeys) > 0 {
		var jobs []model.AdtyTranslationJob
		if err := model.DB.Where("object_key IN ?", objectKeys).Order("id DESC").Find(&jobs).Error; err == nil {
			for _, job := range jobs {
				if job.Id <= 0 {
					continue
				}
				k := jobLookupKey{objectKey: job.ObjectKey, version: job.Version}
				if _, exists := jobsByKey[k]; !exists {
					jobsByKey[k] = job
				}
			}
		}
	}
	for _, h := range heads {
		view := AdtyTranslationTaskView{
			Id:          h.Id,
			ObjectKey:   h.ObjectKey,
			SiteId:      h.SiteId,
			Version:     h.Version,
			SourceHash:  h.SourceHash,
			Source:      h.Source,
			English:     h.English,
			Status:      h.Status,
			HumanEdited: h.HumanEdited,
			ModelName:   h.ModelName,
			Notice:      h.Notice,
			UpdatedAt:   h.UpdatedAt,
		}
		if job, ok := jobsByKey[jobLookupKey{objectKey: h.ObjectKey, version: h.Version}]; ok && job.Id > 0 {
			view.JobId = job.Id
			view.FinishedAt = job.FinishedAt
			if view.ModelName == "" {
				view.ModelName = job.ModelName
			}
		}
		out = append(out, view)
	}
	return out, nil
}

func lockAdtyNotificationPublicationForTranslationTx(tx *gorm.DB, objectKey string) (model.AdtyNotificationPublication, bool, error) {
	var pub model.AdtyNotificationPublication
	suffix, ok := strings.CutPrefix(strings.TrimSpace(objectKey), "notification:")
	if !ok || strings.Contains(suffix, ":") {
		return pub, false, nil
	}
	pubID, err := strconv.Atoi(suffix)
	if err != nil || pubID <= 0 || tx == nil || !tx.Migrator().HasTable(&model.AdtyNotificationPublication{}) {
		return pub, false, nil
	}
	lookup := model.AdtyLockForUpdate(tx).Where("id = ?", pubID).Limit(1).Find(&pub)
	if lookup.Error != nil {
		return pub, false, lookup.Error
	}
	return pub, lookup.RowsAffected > 0, nil
}

func syncAdtyNotificationBodyWithTranslationTx(tx *gorm.DB, pub *model.AdtyNotificationPublication, head model.AdtyTranslationHead, oldEnglish, english string) error {
	if tx == nil || pub == nil || pub.Status != "published" {
		return nil
	}
	liveSource := stripAdtyNotificationTranslationSuffix(pub.Content, oldEnglish)
	// R10-035: Only rewrite publication body when the translation head
	// still matches the live published source text.
	if liveSource == "" || !adtyTranslationHeadMatchesSource(head, liveSource) {
		return nil
	}
	nextEnglish := strings.TrimSpace(english)
	if strings.EqualFold(strings.TrimSpace(pub.ContentFormat), "html") {
		nextEnglish = SanitizeAdtyNotificationHTML(nextEnglish)
	}
	nextContent := liveSource
	if nextEnglish != "" && !strings.Contains(liveSource, nextEnglish) {
		nextContent = liveSource + "\n\n" + nextEnglish
	}
	if nextContent == pub.Content {
		return nil
	}
	return tx.Model(pub).Update("content", nextContent).Error
}

func CompleteAdtyTranslationJob(objectKey string, version int, english string) error {
	objectKey = strings.TrimSpace(objectKey)
	english = strings.TrimSpace(english)
	if objectKey == "" || english == "" {
		return ErrAdtyTranslationInvalid
	}
	return withAdtyTranslationHeadLock(func(tx *gorm.DB) error {
		var head model.AdtyTranslationHead
		if err := model.AdtyLockForUpdate(tx).Where("object_key = ?", objectKey).First(&head).Error; err != nil {
			return err
		}
		if head.HumanEdited || head.Version != version || head.Status != model.AdtyTranslationPending {
			return ErrAdtyTranslationInvalid
		}
		var job model.AdtyTranslationJob
		if err := tx.Where("object_key = ? AND version = ?", objectKey, version).Limit(1).Find(&job).Error; err == nil && job.Id > 0 {
			if job.SourceHash != "" && head.SourceHash != "" && job.SourceHash != head.SourceHash {
				return ErrAdtyTranslationInvalid
			}
		}
		oldEnglish := strings.TrimSpace(head.English)
		pub, hasPub, err := lockAdtyNotificationPublicationForTranslationTx(tx, objectKey)
		if err != nil {
			return err
		}
		isHTML := (hasPub && strings.EqualFold(strings.TrimSpace(pub.ContentFormat), "html")) || adtyLooksLikeHTML(head.Source)
		if isHTML {
			english = SanitizeAdtyNotificationHTML(english)
			if strings.TrimSpace(english) == "" {
				return ErrAdtyTranslationInvalid
			}
		}
		if !adtyTranslationKeepsFactsWithDB(tx, head.Source, english) {
			return ErrAdtyTranslationInvalid
		}
		if err := tx.Model(&head).Updates(map[string]any{
			"english":     english,
			"status":      model.AdtyTranslationReady,
			"notice":      "",
			"lease_until": 0,
		}).Error; err != nil {
			return err
		}
		if err := upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
			ObjectKey:  objectKey,
			Version:    version,
			SourceHash: head.SourceHash,
			Status:     model.AdtyTranslationReady,
			ModelName:  head.ModelName,
			FinishedAt: time.Now().Unix(),
		}); err != nil {
			return err
		}
		if hasPub && pub.Status == "published" {
			if err := syncAdtyNotificationBodyWithTranslationTx(tx, &pub, head, oldEnglish, english); err != nil {
				return err
			}
		}
		return nil
	})
}

func EditAdtyTranslation(objectKey, english string) error {
	objectKey = strings.TrimSpace(objectKey)
	english = strings.TrimSpace(english)
	if objectKey == "" || english == "" {
		return ErrAdtyTranslationInvalid
	}
	return withAdtyTranslationHeadLock(func(tx *gorm.DB) error {
		var head model.AdtyTranslationHead
		if err := model.AdtyLockForUpdate(tx).Where("object_key = ?", objectKey).First(&head).Error; err != nil {
			return err
		}
		// R5-057: Capture prior English BEFORE updating head.english so double human edits
		// on secret-bearing ZH announcements strip the prior English cleanly instead of
		// appending duplicates and breaking adtyTranslationHeadMatchesSource.
		oldEnglish := strings.TrimSpace(head.English)
		pub, hasPub, err := lockAdtyNotificationPublicationForTranslationTx(tx, objectKey)
		if err != nil {
			return err
		}
		isHTML := (hasPub && strings.EqualFold(strings.TrimSpace(pub.ContentFormat), "html")) || adtyLooksLikeHTML(head.Source)
		if isHTML {
			english = SanitizeAdtyNotificationHTML(english)
			if strings.TrimSpace(english) == "" {
				return ErrAdtyTranslationInvalid
			}
		}
		if err := tx.Model(&head).Updates(map[string]any{
			"english":      english,
			"human_edited": true,
			"status":       model.AdtyTranslationReady,
			"notice":       "",
			"lease_until":  0,
		}).Error; err != nil {
			return err
		}
		if hasPub && pub.Status == "published" {
			if err := syncAdtyNotificationBodyWithTranslationTx(tx, &pub, head, oldEnglish, english); err != nil {
				return err
			}
		}
		return nil
	})
}

func AdtyTranslationUsageToday() (*model.AdtyTranslationUsage, error) {
	var row model.AdtyTranslationUsage
	day := time.Now().UTC().Format("2006-01-02")
	err := model.DB.Where("day = ?", day).Limit(1).Find(&row).Error
	if row.Day == "" {
		row.Day = day
	}
	if row.CostUSD == "" {
		row.CostUSD = "0"
	}
	return &row, err
}

func adtyTranslationView(row model.AdtyTranslationConfig) *AdtyTranslationView {
	return &AdtyTranslationView{
		Mode: row.Mode, Protocol: row.Protocol, BaseURL: row.BaseURL, ModelName: row.ModelName, BackupModel: row.BackupModel,
		TokenMask: row.TokenMask, TimeoutSec: row.TimeoutSec, Concurrency: row.Concurrency, MaxChars: row.MaxChars,
		DailyQuota: row.DailyQuota, RetryLimit: row.RetryLimit, Enabled: row.Enabled, SavedAt: row.SavedAt, TestedAt: row.TestedAt, TranslatedAt: row.TranslatedAt,
	}
}

func loadAdtyTranslationConfig() (*model.AdtyTranslationConfig, error) {
	var row model.AdtyTranslationConfig
	if err := model.DB.First(&row, 1).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func rejectAdtyTranslationSelf(baseURL, requestHost, token string) error {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return ErrAdtyTranslationSelf
	}
	host := strings.Trim(AdtyNormalizeHost(parsed.Host), "[]")
	reqHost := strings.Trim(AdtyNormalizeHost(requestHost), "[]")
	if host == "" || (reqHost != "" && host == reqHost) || adtyTranslationTargetIsSelf(parsed.Host) {
		return ErrAdtyTranslationSelf
	}
	if err := validateURLWithCurrentFetchSetting(parsed.String(), false); err != nil {
		return ErrAdtyTranslationSelf
	}
	return rejectAdtyCustomerToken(token)
}

func rejectAdtyCustomerToken(token string) error {
	return rejectAdtyCustomerTokenWithDB(model.DB, token)
}

func normalizeAdtyCustomerTokenCandidates(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	candidates := make([]string, 0, 4)
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		for _, existing := range candidates {
			if existing == v {
				return
			}
		}
		candidates = append(candidates, v)
	}
	stripChannelSuffix := func(v string) string {
		idx := strings.LastIndex(v, "-")
		if idx <= 0 || idx >= len(v)-1 {
			return v
		}
		suffix := v[idx+1:]
		for i := 0; i < len(suffix); i++ {
			if suffix[i] < '0' || suffix[i] > '9' {
				return v
			}
		}
		return v[:idx]
	}
	add(trimmed)
	add(stripChannelSuffix(trimmed))
	if len(trimmed) > 3 && strings.EqualFold(trimmed[:3], "sk-") {
		stripped := trimmed[3:]
		add(stripped)
		add(stripChannelSuffix(stripped))
	}
	return candidates
}

func rejectAdtyCustomerTokenWithDB(db *gorm.DB, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	if (len(token) >= 4 && strings.EqualFold(token[:4], "pat-")) ||
		(len(token) >= len(model.AccessTokenPrefix) && strings.EqualFold(token[:len(model.AccessTokenPrefix)], model.AccessTokenPrefix)) {
		return ErrAdtyTranslationSelf
	}
	if db == nil {
		return nil
	}
	hasTokenTable, err := model.AdtyTableExists(db, &model.Token{})
	if err != nil {
		return err
	}
	if !hasTokenTable {
		return nil
	}
	candidates := normalizeAdtyCustomerTokenCandidates(token)
	if len(candidates) == 0 {
		return nil
	}
	var count int64
	if err := db.Unscoped().Model(&model.Token{}).Where(map[string]any{"key": candidates}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrAdtyTranslationSelf
	}
	return nil
}

func translateWithBackup(cfg *model.AdtyTranslationConfig, source string) (string, string, error) {
	return translateWithBackupForSite(cfg, source, 0)
}

func translateWithBackupForSite(cfg *model.AdtyTranslationConfig, source string, siteID int) (string, string, error) {
	primary := strings.TrimSpace(cfg.ModelName)
	backup := strings.TrimSpace(cfg.BackupModel)
	if backup == "" {
		backup = primary
	}

	english, err := callAdtyTranslatorForSite(primary, source, siteID)
	if err == nil {
		return english, primary, nil
	}
	lastModel := primary
	for retry := 1; retry <= cfg.RetryLimit; retry++ {
		if errors.Is(err, ErrAdtyTranslationQuota) || errors.Is(err, ErrAdtyTranslationDisabled) || errors.Is(err, ErrAdtyTranslationSelf) {
			break
		}
		if delay := AdtyTranslationBackoffFunc(retry); delay > 0 && adtyTranslationSleep != nil {
			adtyTranslationSleep(delay)
		}
		targetModel := backup
		if targetModel == "" {
			targetModel = primary
		}
		lastModel = targetModel
		english, err = callAdtyTranslatorForSite(targetModel, source, siteID)
		if err == nil {
			return english, targetModel, nil
		}
	}
	return "", lastModel, err
}

func callAdtyTranslator(modelName, text string) (string, error) {
	return callAdtyTranslatorWithOptions(modelName, text, false, 0)
}

func callAdtyTranslatorForSite(modelName, text string, siteID int) (string, error) {
	return callAdtyTranslatorWithOptions(modelName, text, false, siteID)
}

func callAdtyTranslatorForTest(modelName, text string) (string, error) {
	return callAdtyTranslatorWithOptions(modelName, text, true, 0)
}

func callAdtyTranslatorWithOptions(modelName, text string, allowDisabled bool, siteID int) (string, error) {
	adtyTranslationFlight.Lock()
	cfgConcurrency := 0
	dailyQuota := 0
	var cfg model.AdtyTranslationConfig
	if err := model.DB.Select("concurrency, daily_quota").First(&cfg, 1).Error; err == nil {
		cfgConcurrency = cfg.Concurrency
		dailyQuota = cfg.DailyQuota
	}
	if cfgConcurrency > 0 && adtyTranslationInflight >= cfgConcurrency {
		adtyTranslationFlight.Unlock()
		return "", ErrAdtyTranslationInvalid
	}
	adtyTranslationInflight++
	adtyTranslationFlight.Unlock()
	defer func() {
		adtyTranslationFlight.Lock()
		adtyTranslationInflight--
		adtyTranslationFlight.Unlock()
	}()

	day := time.Now().UTC().Format("2006-01-02")
	if err := reserveAdtyTranslationCallForSite(day, dailyQuota, siteID); err != nil {
		return "", err
	}

	started := time.Now()
	var (
		english string
		callErr error
	)
	if allowDisabled && AdtyTranslateFunc != nil && reflect.ValueOf(AdtyTranslateFunc).Pointer() == reflect.ValueOf(adtyTranslateViaProvider).Pointer() {
		english, callErr = adtyTranslateViaProviderWithOptions(modelName, text, true)
	} else {
		english, callErr = AdtyTranslateFunc(modelName, text)
	}
	latency := time.Since(started).Milliseconds()
	cost := calculateAdtyTranslationCostUSD(modelName, text, english)
	if usageErr := finishAdtyTranslationCall(day, callErr != nil, latency, cost); usageErr != nil && callErr == nil {
		return "", usageErr
	}
	return english, callErr
}

func reserveAdtyTranslationCall(day string, dailyQuota int) error {
	return reserveAdtyTranslationCallForSite(day, dailyQuota, 0)
}

func reserveAdtyTranslationCallForSite(day string, dailyQuota, siteID int) error {
	adtyTranslationUsageMu.Lock()
	defer adtyTranslationUsageMu.Unlock()

	now := time.Now().UTC()
	if day == "" {
		day = now.Format("2006-01-02")
	}
	siteQuota := adtyTranslationSiteDailyQuota(dailyQuota)
	var nextSiteCalls int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtyTranslationUsage
		lookup := model.AdtyLockForUpdate(tx).Where("day = ?", day).Limit(1).Find(&row)
		if lookup.Error != nil {
			return lookup.Error
		}
		if adtyTranslationSiteUsageDay != day || adtyTranslationSiteUsageDB != model.DB || lookup.RowsAffected == 0 || row.Calls == 0 {
			adtyTranslationSiteUsageDay = day
			adtyTranslationSiteUsageDB = model.DB
			adtyTranslationSiteCalls = make(map[int]int)
		}
		if dailyQuota > 0 && row.Calls >= dailyQuota {
			return ErrAdtyTranslationQuota
		}
		if siteID > 0 && siteQuota > 0 {
			dbCalls, err := adtyTranslationSiteCallsTodayWithDB(tx, siteID, now)
			if err != nil {
				return err
			}
			used := max(dbCalls, adtyTranslationSiteCalls[siteID])
			if used >= siteQuota {
				return ErrAdtyTranslationQuota
			}
			nextSiteCalls = used + 1
		}
		row.Day = day
		row.Calls++
		if strings.TrimSpace(row.CostUSD) == "" {
			row.CostUSD = "0.000000"
		}
		if lookup.RowsAffected == 0 {
			return tx.Create(&row).Error
		}
		return tx.Save(&row).Error
	})
	if err != nil {
		return err
	}
	if siteID > 0 && siteQuota > 0 {
		adtyTranslationSiteCalls[siteID] = nextSiteCalls
	}
	return nil
}

func finishAdtyTranslationCall(day string, failed bool, latency int64, callCost decimal.Decimal) error {
	adtyTranslationUsageMu.Lock()
	defer adtyTranslationUsageMu.Unlock()

	return model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtyTranslationUsage
		lookup := model.AdtyLockForUpdate(tx).Where("day = ?", day).Limit(1).Find(&row)
		if lookup.Error != nil {
			return lookup.Error
		}
		row.Day = day
		if lookup.RowsAffected == 0 {
			row.Calls = 1
		}
		row.LatencyMs += latency
		if failed {
			row.Failures++
		}
		prevCost, err := decimal.NewFromString(strings.TrimSpace(row.CostUSD))
		if err != nil {
			prevCost = decimal.Zero
		}
		if callCost.GreaterThan(decimal.Zero) {
			prevCost = prevCost.Add(callCost)
		}
		row.CostUSD = prevCost.StringFixed(6)
		if lookup.RowsAffected == 0 {
			return tx.Create(&row).Error
		}
		return tx.Save(&row).Error
	})
}

func noteAdtyTranslationUsage(failed bool, latency int64) error {
	day := time.Now().UTC().Format("2006-01-02")
	if err := reserveAdtyTranslationCall(day, 0); err != nil {
		return err
	}
	cost := calculateAdtyTranslationCostUSD("translator", "usage", "")
	return finishAdtyTranslationCall(day, failed, latency, cost)
}

func calculateAdtyTranslationCostUSD(modelName, source, english string) decimal.Decimal {
	inTokens := (len([]rune(adtyTranslationSystemPrompt)) + len([]rune(source)) + 3) / 4
	if inTokens <= 0 {
		inTokens = 1
	}
	outTokens := 0
	if strings.TrimSpace(english) != "" {
		outTokens = (len([]rune(english)) + 3) / 4
		if outTokens <= 0 {
			outTokens = 1
		}
	}

	if modelPrice, ok := ratio_setting.GetModelPriceDecimal(modelName, false); ok && modelPrice.GreaterThan(decimal.Zero) {
		return modelPrice
	}
	modelRatio, _, _ := ratio_setting.GetModelRatioDecimal(modelName)
	if modelRatio.LessThanOrEqual(decimal.Zero) {
		modelRatio = decimal.NewFromInt(1)
	}
	compRatio := ratio_setting.GetCompletionRatioDecimal(modelName)
	if compRatio.LessThanOrEqual(decimal.Zero) {
		compRatio = decimal.NewFromInt(1)
	}
	perUnit := common.QuotaPerUnitDecimal()
	if perUnit.LessThanOrEqual(decimal.Zero) {
		perUnit = decimal.NewFromInt(500000)
	}
	quotaUnits := decimal.NewFromInt(int64(inTokens)).Add(decimal.NewFromInt(int64(outTokens)).Mul(compRatio)).Mul(modelRatio)
	cost := quotaUnits.Div(perUnit)
	if cost.LessThan(decimal.RequireFromString("0.000001")) {
		cost = decimal.RequireFromString("0.000001")
	}
	return cost
}

func withAdtyTranslationHeadLock(fn func(tx *gorm.DB) error) error {
	adtyTranslationHeadMu.Lock()
	defer adtyTranslationHeadMu.Unlock()
	return model.DB.Transaction(fn)
}

func upsertAdtyTranslationJobTx(tx *gorm.DB, job *model.AdtyTranslationJob) error {
	var existing model.AdtyTranslationJob
	lookup := tx.Where("object_key = ? AND version = ?", job.ObjectKey, job.Version).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return tx.Create(job).Error
	}
	updates := map[string]any{
		"source_hash": job.SourceHash,
		"status":      job.Status,
		"model_name":  job.ModelName,
		"finished_at": job.FinishedAt,
	}
	return tx.Model(&existing).Updates(updates).Error
}

func fallbackAdtyTranslation(objectKey, source string, siteId, version int, hash, modelName string, expectedLeaseUntil int64) (*AdtyTranslationResult, error) {
	return fallbackAdtyTranslationWithGuard(objectKey, source, siteId, version, hash, modelName, 0, 0, expectedLeaseUntil)
}

func fallbackAdtyTranslationWithGuard(objectKey, source string, siteId, version int, hash, modelName string, notifPubID int, notifRevision int64, expectedLeaseUntil int64) (*AdtyTranslationResult, error) {
	head := model.AdtyTranslationHead{
		ObjectKey: objectKey, SiteId: siteId, Version: version, SourceHash: hash, Source: source,
		Status: model.AdtyTranslationFailed, Notice: AdtyTranslationNotReady, ModelName: modelName,
	}
	preserved := false
	err := withAdtyTranslationHeadLock(func(tx *gorm.DB) error {
		var current model.AdtyTranslationHead
		found := model.AdtyLockForUpdate(tx).Where("object_key = ?", objectKey).Limit(1).Find(&current)
		if found.Error != nil {
			return found.Error
		}
		if notifPubID > 0 {
			ok, checkErr := adtyNotificationRevisionMatchesTx(tx, notifPubID, notifRevision)
			if checkErr != nil {
				return checkErr
			}
			if !ok {
				if found.RowsAffected > 0 && !current.HumanEdited && current.Version == version &&
					current.Status == model.AdtyTranslationPending &&
					(current.SourceHash == "" || current.SourceHash == hash) &&
					(expectedLeaseUntil <= 0 || current.LeaseUntil == expectedLeaseUntil) {
					if err := upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
						ObjectKey:  objectKey,
						Version:    version,
						SourceHash: hash,
						Status:     model.AdtyTranslationFailed,
						ModelName:  modelName,
						FinishedAt: time.Now().Unix(),
					}); err != nil {
						return err
					}
					if err := tx.Model(&current).Updates(map[string]any{
						"status":      model.AdtyTranslationFailed,
						"notice":      AdtyTranslationNotReady,
						"model_name":  modelName,
						"lease_until": 0,
					}).Error; err != nil {
						return err
					}
					current.Status = model.AdtyTranslationFailed
					current.Notice = AdtyTranslationNotReady
					current.ModelName = modelName
					current.LeaseUntil = 0
				}
				head = current
				preserved = found.RowsAffected > 0
				return nil
			}
		}
		if found.RowsAffected > 0 && expectedLeaseUntil > 0 && current.LeaseUntil != expectedLeaseUntil {
			head = current
			preserved = true
			return nil
		}
		if (strings.HasPrefix(objectKey, "ticket-reply:") || strings.HasPrefix(objectKey, "notification:") || strings.HasPrefix(objectKey, "notification-title:")) &&
			(found.RowsAffected == 0 || current.Status != model.AdtyTranslationPending || current.Version != version || current.SourceHash != hash) {
			head = current
			preserved = found.RowsAffected > 0
			return nil
		}
		if found.RowsAffected > 0 && (current.Status != model.AdtyTranslationPending || current.HumanEdited || current.Version != version || (current.SourceHash != "" && current.SourceHash != hash)) {
			head = current
			preserved = true
			return nil
		}
		if err := upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{
			ObjectKey:  objectKey,
			Version:    version,
			SourceHash: hash,
			Status:     model.AdtyTranslationFailed,
			ModelName:  modelName,
			FinishedAt: time.Now().Unix(),
		}); err != nil {
			return err
		}
		head.Id = current.Id
		if found.RowsAffected == 0 {
			return tx.Create(&head).Error
		}
		return tx.Model(&current).Updates(map[string]any{
			"version":     version,
			"source_hash": hash,
			"source":      source,
			"english":     "",
			"status":      model.AdtyTranslationFailed,
			"notice":      AdtyTranslationNotReady,
			"model_name":  modelName,
			"lease_until": 0,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	if preserved && head.Status == model.AdtyTranslationReady && strings.TrimSpace(head.English) != "" &&
		(head.SourceHash == "" || head.SourceHash == hash) &&
		(head.HumanEdited || head.Version == version) {
		return &AdtyTranslationResult{
			ObjectKey: objectKey,
			Source:    head.Source,
			English:   head.English,
			Status:    model.AdtyTranslationReady,
			Notice:    head.Notice,
			Version:   head.Version,
		}, nil
	}
	return &AdtyTranslationResult{
		ObjectKey: objectKey,
		Source:    source,
		Status:    model.AdtyTranslationFailed,
		Notice:    AdtyTranslationNotReady,
		Version:   version,
	}, nil
}

func adtyLooksLikeHTML(text string) bool {
	return strings.TrimSpace(text) != "" && adtyHTMLTagPattern.MatchString(text)
}

func adtyIsHTMLTranslation(db *gorm.DB, objectKey, source string) bool {
	if adtyLooksLikeHTML(source) {
		return true
	}
	if suffix, ok := strings.CutPrefix(strings.TrimSpace(objectKey), "notification:"); ok && !strings.Contains(suffix, ":") {
		if pubID, err := strconv.Atoi(suffix); err == nil && pubID > 0 && db != nil && db.Migrator().HasTable(&model.AdtyNotificationPublication{}) {
			var pub model.AdtyNotificationPublication
			if db.Select("content_format").Where("id = ?", pubID).Limit(1).Find(&pub).RowsAffected > 0 {
				return strings.EqualFold(strings.TrimSpace(pub.ContentFormat), "html")
			}
		}
	}
	return false
}

func adtyTranslationKeepsFacts(source, english string) bool {
	return adtyTranslationKeepsFactsWithDB(model.DB, source, english)
}

func adtyTranslationKeepsFactsWithDB(db *gorm.DB, source, english string) bool {
	if strings.TrimSpace(english) == "" {
		return false
	}
	sourceCheck := source
	englishCheck := english
	if adtyLooksLikeHTML(source) {
		sourceCheck = AdtyNotificationHTMLToPlainText(source)
	}
	if adtyLooksLikeHTML(english) {
		englishCheck = AdtyNotificationHTMLToPlainText(english)
	}
	if strings.TrimSpace(englishCheck) == "" {
		return false
	}
	if !adtyEqualTokenCounts(adtyURLToken.FindAllString(sourceCheck, -1), adtyURLToken.FindAllString(englishCheck, -1)) {
		return false
	}
	if !adtyEqualTokenCounts(adtyPlaceholderToken.FindAllString(sourceCheck, -1), adtyPlaceholderToken.FindAllString(englishCheck, -1)) {
		return false
	}

	cleanSource := adtyStripProtectedSpans(sourceCheck, adtyURLToken, adtyPlaceholderToken)
	cleanEnglish := adtyStripProtectedSpans(englishCheck, adtyURLToken, adtyPlaceholderToken)

	if !adtyEqualTokenCounts(adtyOrderToken.FindAllString(cleanSource, -1), adtyOrderToken.FindAllString(cleanEnglish, -1)) {
		return false
	}

	numSource := adtyStripProtectedSpans(cleanSource, adtyOrderToken)
	numEnglish := adtyStripProtectedSpans(cleanEnglish, adtyOrderToken)

	if !adtyEqualTokenCounts(adtyExtractNumberPercentTokens(numSource), adtyExtractNumberPercentTokens(numEnglish)) {
		return false
	}
	if !adtyEqualTokenCounts(adtyExtractCurrencyTokens(numSource), adtyExtractCurrencyTokens(numEnglish)) {
		return false
	}

	if adtyNegationSource.MatchString(sourceCheck) && !adtyNegationTarget.MatchString(englishCheck) {
		return false
	}
	return adtyTranslationKeepsGlossaryWithDB(db, sourceCheck, englishCheck)
}

func adtyStripProtectedSpans(input string, patterns ...*regexp.Regexp) string {
	out := input
	for _, p := range patterns {
		out = p.ReplaceAllString(out, " ")
	}
	return out
}

func adtyExtractNumberPercentTokens(s string) []string {
	indices := adtyAmountToken.FindAllStringIndex(s, -1)
	if len(indices) == 0 {
		return nil
	}
	out := make([]string, 0, len(indices))
	for _, loc := range indices {
		start, end := loc[0], loc[1]
		sign := ""
		if start > 0 && (s[start-1] == '-' || s[start-1] == '+') {
			if start == 1 || !adtyIsASCIIAlphaNum(s[start-2]) {
				if s[start-1] == '-' {
					sign = "-"
				} else {
					sign = "+"
				}
			}
		}
		suffix := ""
		if end < len(s) && s[end] == '%' {
			suffix = "%"
		}
		out = append(out, sign+s[start:end]+suffix)
	}
	return out
}

func adtyIsASCIIAlphaNum(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func adtyExtractCurrencyTokens(s string) []string {
	var out []string
	for _, sym := range adtyCurrencySymbolToken.FindAllString(s, -1) {
		if sym == "￥" {
			sym = "¥"
		}
		out = append(out, sym)
	}
	for _, word := range adtyWordRunToken.FindAllString(s, -1) {
		upper := strings.ToUpper(word)
		if _, ok := adtyProtectedCurrencies[upper]; ok {
			out = append(out, upper)
		}
	}
	return out
}

func adtyEqualTokenCounts(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	counts := make(map[string]int, len(a))
	for _, item := range a {
		counts[item]++
	}
	for _, item := range b {
		counts[item]--
		if counts[item] < 0 {
			return false
		}
	}
	for _, v := range counts {
		if v != 0 {
			return false
		}
	}
	return true
}
