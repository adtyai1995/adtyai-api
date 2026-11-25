package service

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 站长分站的开站、审核、调整和关站（任务 11.1）。

var (
	ErrAdtySiteDisabled      = common.NewMessage("Reseller sites are not available")
	ErrAdtySiteNotFound      = common.NewMessage("Site not found")
	ErrAdtySiteCustomer      = common.NewMessage("Customers of a reseller site cannot open a site")
	ErrAdtySiteAlreadyOwner  = common.NewMessage("You already have a site")
	ErrAdtySiteSubdomainBad  = common.NewMessage("The subdomain may only contain lowercase letters, digits and hyphens, and cannot start or end with a hyphen")
	ErrAdtySiteSubdomainUsed = common.NewMessage("This subdomain is not available")
	ErrAdtySiteAgreement     = common.NewMessage("Accept the current site agreement first")
	ErrAdtySiteInviteInvalid = common.NewMessage("The invite code is invalid or used up")
	ErrAdtySiteInviteOff     = common.NewMessage("Invite codes are not enabled")
	// ErrAdtySiteSelfServeRetired is the official closed-door message for the
	// retired self-serve / invite-code opening path (A.10-01 / SITE-002). Same
	// wording as the HTTP POST /api/adty/site/apply rejection entry.
	ErrAdtySiteSelfServeRetired = common.NewMessage("Unauthorized, insufficient privileges")
	ErrAdtySiteStatus           = common.NewMessage("The site cannot change to this status")
	ErrAdtySiteTransfer         = common.NewMessage("Closed-site customers can only be inherited by the main site")
	ErrAdtySitePauseRetired     = common.NewMessage("Independent site pause and resume is not supported")
	ErrAdtySitePausedRepair     = common.NewMessage("Only leftover paused sites can be repaired this way")
)

// Outcomes for the Root-only leftover paused-site repair (DEC-019 / ISS-R2-0022).
// Pause is not an operating mode and is not reintroduced as a write path.
const (
	AdtyPausedRepairOutcomeActive = "active"
	AdtyPausedRepairOutcomeClose  = "close"
)

// AdtyNewOwnerInput creates a dedicated standalone pure-admin owner account
// atomically when opening a new reseller site (R-32 #25).
type AdtyNewOwnerInput struct {
	Username    string
	Password    string
	Email       string
	DisplayName string
}

// AdtySitePreset 是开站时预设的单站 d、等级和元数据（管理员直接开），空就用全局。
type AdtySitePreset struct {
	MaxMarkup      *decimal.Decimal // Deprecated: owners control retail pricing.
	CostFactor     *decimal.Decimal
	TierCode       string
	TierMode       string
	ContactInfo    string
	AgreementVer   string
	InviteCodeId   int
	OperatorId     int
	ExplicitRoot   string // 管理员开站可以指定根域名，空就用 root_domains 的第一个
	InitialStatus  string // 空则按开站方式决定
	IdempotencyKey string
	NewOwner       *AdtyNewOwnerInput
}

func resellerSetting() *adty_setting.ResellerSetting { return adty_setting.GetResellerSetting() }

var adtyDefaultAPILabel = regexp.MustCompile(`^api-s[1-9][0-9]*(-[1-9][0-9]*)?$`)

// AdtyNormalizeSubdomain 去空白转小写后按 DNS 标签规则、保留名校验。
// xn-- 是 punycode，不能当站名。CNAME 目标和回源主机名在根域名下的那一段也自动保留。
func AdtyNormalizeSubdomain(raw string) (string, error) {
	label := strings.ToLower(strings.TrimSpace(raw))
	if !adty_setting.ResellerLabelPattern.MatchString(label) || strings.HasPrefix(label, "xn--") {
		return "", ErrAdtySiteSubdomainBad
	}
	cfg := resellerSetting()
	if adtyDefaultAPILabel.MatchString(label) || slices.Contains(cfg.ReservedSubdomains, label) || slices.Contains(adtyInfrastructureLabels(cfg), label) {
		return "", ErrAdtySiteSubdomainUsed
	}
	return label, nil
}

// adtyInfrastructureLabels 取出 cname_target、fallback_origin 紧挨根域名的那一段标签。
func adtyInfrastructureLabels(cfg *adty_setting.ResellerSetting) []string {
	if cfg == nil {
		return nil
	}
	labels := make([]string, 0, 2)
	for _, host := range []string{cfg.CnameTarget, cfg.FallbackOrigin} {
		if label := adtyLabelUnderRoot(host, cfg.RootDomains); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func adtyLabelUnderRoot(host string, roots []string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return ""
	}
	for _, root := range roots {
		root = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(root)), ".")
		if root == "" || !strings.HasSuffix(host, "."+root) {
			continue
		}
		prefix := strings.TrimSuffix(host, "."+root)
		parts := strings.Split(prefix, ".")
		label := parts[len(parts)-1]
		if adty_setting.ResellerLabelPattern.MatchString(label) {
			return label
		}
	}
	return ""
}

// AdtyCurrentSiteAgreementVersion 是开站协议当前的版本号（字符串存进站点）。
func AdtyCurrentSiteAgreementVersion() string {
	return strconv.Itoa(adty_setting.GetLegalSetting().SiteAgreementVersion)
}

// AdtySiteAgreementAccepted always returns true because site opening agreements
// are not required for site owners.
func AdtySiteAgreementAccepted(agreementVer string) bool {
	return true
}

// AdtyCurrentSiteCustomerTermsVersion returns "0" because subsite customer
// terms were retired under R-32 (#9).
func AdtyCurrentSiteCustomerTermsVersion() string {
	return "0"
}

// AdtySiteTermsStatus 说明登录后要不要重新勾选。站长无需签署开站协议；分站客户条款已退役。
func AdtySiteTermsStatus(userId int) (required bool, version string) {
	req, ver, err := AdtySiteTermsStatusWithError(userId)
	if err != nil {
		return true, ver
	}
	return req, ver
}

// AdtySiteTermsStatusWithError always returns required=false after R-32 (#9)
// cancelled subsite agreements and subsite customer terms.
func AdtySiteTermsStatusWithError(userId int) (required bool, version string, err error) {
	return false, "", nil
}

// AcceptAdtySiteTerms rejects writes because subsite customer terms have been
// retired under R-32 (#9) / R5-26.
func AcceptAdtySiteTerms(userId int, version string) error {
	return ErrAdtySiteAgreement
}

// validateSitePreset checks an optional per-site supply discount override.
func validateSitePreset(costFactor *decimal.Decimal) error {
	if costFactor != nil {
		if !costFactor.IsPositive() || costFactor.GreaterThanOrEqual(decimal.New(1, 4)) || !costFactor.Equal(costFactor.Truncate(6)) {
			return common.NewMessage("The cost factor must be greater than 0")
		}
	}
	return nil
}

func recordSiteCompatibilityAuditTx(tx *gorm.DB, siteId int, action string, operatorId int, oldValue, newValue any, reason string) error {
	reason = model.RedactAuditText(truncateText(reason, 1000))
	return tx.Create(&model.AdtySiteAudit{
		SiteId: siteId, Action: action, OperatorId: operatorId,
		OldValue: model.RedactAuditJSON(oldValue), NewValue: model.RedactAuditJSON(newValue), Reason: reason,
	}).Error
}

func recordSiteAuditWithIntentTx(tx *gorm.DB, siteId int, action string, operatorId int, oldValue, newValue any, reason string) (*model.AuditLog, error) {
	reason = truncateText(reason, 1000)
	if err := recordSiteCompatibilityAuditTx(tx, siteId, action, operatorId, oldValue, newValue, reason); err != nil {
		return nil, err
	}
	officialAction := action
	if !strings.Contains(action, ".") {
		officialAction = "adty.site." + action
	}
	// R6-037: Persist official audit atomically via AuditIntent inside tx instead of cross-pool LOG_DB write.
	return appendAdtyOfficialAuditTx(tx, operatorId, siteId, "site", strconv.Itoa(siteId), officialAction, reason, "", oldValue, newValue, model.AuditResultSuccess)
}

func recordSiteAuditTx(tx *gorm.DB, siteId int, action string, operatorId int, oldValue, newValue any, reason string) error {
	_, err := recordSiteAuditWithIntentTx(tx, siteId, action, operatorId, oldValue, newValue, reason)
	return err
}

// OpenAdtySite is the official Root opening path (AdtySiteOpenedByAdmin).
// Self-serve apply and invite-code opening are retired and rejected even if
// called directly (A.10-01 / SITE-002). Opening as active still writes
// ReferralAffiliate.SiteOwnerSince in the same transaction.
func OpenAdtySite(userId int, subdomain, openedBy string, preset AdtySitePreset) (*model.AdtySite, error) {
	site, _, _, err := OpenAdtySiteWithAudit(userId, subdomain, openedBy, preset)
	return site, err
}

func OpenAdtySiteWithAudit(userId int, subdomain, openedBy string, preset AdtySitePreset) (*model.AdtySite, string, bool, error) {
	if openedBy != model.AdtySiteOpenedByAdmin || preset.InviteCodeId > 0 {
		return nil, "", false, ErrAdtySiteSelfServeRetired
	}
	cfg := resellerSetting()
	if !cfg.Ready() {
		return nil, "", false, ErrAdtySiteDisabled
	}
	idemKey := strings.TrimSpace(preset.IdempotencyKey)
	if len(idemKey) > 64 {
		return nil, "", false, common.NewMessage("Idempotency-Key is too long (maximum 64 characters)")
	}
	tierCode := strings.TrimSpace(preset.TierCode)
	tierMode := strings.TrimSpace(preset.TierMode)
	if tierCode != "" {
		if tierMode == "" {
			tierMode = AdtySiteTierModeAuto
		}
		if tierMode != AdtySiteTierModeAuto && tierMode != AdtySiteTierModeManual {
			return nil, "", false, ErrAdtySiteTierMode
		}
	} else if tierMode != "" && tierMode != AdtySiteTierModeAuto && tierMode != AdtySiteTierModeManual {
		return nil, "", false, ErrAdtySiteTierMode
	}
	rawSubdomain := strings.ToLower(strings.TrimSpace(subdomain))
	if err := validateSitePreset(preset.CostFactor); err != nil {
		return nil, "", false, err
	}
	root := strings.ToLower(strings.TrimSpace(preset.ExplicitRoot))
	if root == "" {
		root = cfg.RootDomains[0]
		if cfg.DomainAutomationEnabled {
			root = cfg.AliyunDomain
		}
	} else if !slices.Contains(cfg.RootDomains, root) && !(cfg.DomainAutomationEnabled && root == cfg.AliyunDomain) {
		return nil, "", false, common.NewMessage("Invalid root domain {{domain}}", map[string]any{"domain": root})
	}
	if cfg.DomainAutomationEnabled && root != cfg.AliyunDomain {
		return nil, "", false, common.NewMessage("Site root is outside the managed DNS domain")
	}
	costStr := ""
	if preset.CostFactor != nil {
		costStr = preset.CostFactor.String()
	}
	contact := truncateText(strings.TrimSpace(preset.ContactInfo), 500)
	var newOwnerUsername, newOwnerPassHash, newOwnerEmail, newOwnerDisplayName string
	if preset.NewOwner != nil {
		newOwnerUsername = strings.TrimSpace(preset.NewOwner.Username)
		if len(newOwnerUsername) < 3 || len(newOwnerUsername) > 20 || strings.Contains(newOwnerUsername, "@") || !model.ValidateUsernameNoControl(newOwnerUsername) {
			return nil, "", false, common.NewMessage("Owner username must be 3 to 20 characters and cannot contain @")
		}
		if err := common.Validate.StructPartial(&model.User{Username: newOwnerUsername}, "Username"); err != nil {
			return nil, "", false, common.NewMessage("Owner username must be 3 to 20 characters and cannot contain @")
		}
		if err := common.ValidateNewAccountPassword(preset.NewOwner.Password); err != nil {
			return nil, "", false, err
		}
		hashed, hashErr := common.HashAccountPassword(preset.NewOwner.Password)
		if hashErr != nil {
			return nil, "", false, hashErr
		}
		newOwnerPassHash = hashed
		newOwnerEmail = model.NormalizeEmail(preset.NewOwner.Email)
		if newOwnerEmail != "" {
			if err := common.Validate.Var(newOwnerEmail, "email"); err != nil {
				return nil, "", false, common.NewMessage("Invalid email address")
			}
		}
		newOwnerDisplayName = truncateText(strings.TrimSpace(preset.NewOwner.DisplayName), 40)
		if newOwnerDisplayName == "" {
			newOwnerDisplayName = newOwnerUsername
		}
	}
	var openKeyDigest *string
	openFingerprint := ""
	if idemKey != "" {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d/%s", preset.OperatorId, idemKey))))
		openKeyDigest = &digest
		openFingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s|%s|%s|%s", userId, newOwnerUsername, newOwnerEmail, newOwnerDisplayName, rawSubdomain, root, costStr, tierCode, tierMode, contact))))
	}
	label := rawSubdomain
	if label == "" {
		allocated := false
		for range 20 {
			n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 48))
			if err != nil {
				return nil, "", false, err
			}
			candidate := "s" + n.Text(36)
			if _, err := AdtyNormalizeSubdomain(candidate); err != nil {
				continue
			}
			var count int64
			if err := model.DB.Model(&model.AdtySite{}).Where("subdomain = ?", candidate).Count(&count).Error; err != nil {
				return nil, "", false, err
			}
			if count == 0 {
				label = candidate
				allocated = true
				break
			}
		}
		if !allocated {
			return nil, "", false, ErrAdtySiteSubdomainUsed
		}
	}
	var err error
	label, err = AdtyNormalizeSubdomain(label)
	if err != nil {
		return nil, "", false, err
	}
	status := preset.InitialStatus
	if status == "" {
		status = model.AdtySiteStatusActive
		if openedBy == model.AdtySiteOpenedByApply {
			status = model.AdtySiteStatusPending
		}
	}
	now := time.Now().Unix()
	site := &model.AdtySite{
		OwnerUserId: userId, Subdomain: label, RootDomain: root, Status: status,
		CostFactor: preset.CostFactor, ContactInfo: contact,
		OpenedBy: openedBy, OpenKey: openKeyDigest, OpenFingerprint: openFingerprint,
		AgreementVer: strings.TrimSpace(preset.AgreementVer),
	}
	if site.AgreementVer != "" {
		site.AgreementAt = now
	}
	var txOptions []*sql.TxOptions
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		// Marker initialization can read before the user lock. Qualifications
		// checked after waiting must include another opening's committed site.
		txOptions = append(txOptions, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	}
	replayed := false
	var auditEvent *model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// Serialize concurrent retries with the same operator/idempotency key before creating a new owner row.
		if openKeyDigest != nil && preset.OperatorId > 0 {
			var op model.User
			if err := model.AdtyLockForUpdate(tx).Select("id").Where("id = ?", preset.OperatorId).Limit(1).Find(&op).Error; err != nil {
				return err
			}
		}
		if openKeyDigest != nil {
			var existing model.AdtySite
			lookup := tx.Where("open_key = ?", *openKeyDigest).Limit(1).Find(&existing)
			if lookup.Error != nil {
				return lookup.Error
			}
			if lookup.RowsAffected > 0 {
				if existing.OpenFingerprint != openFingerprint {
					return model.ErrAdtyIdempotencyConflict
				}
				site = &existing
				replayed = true
				return nil
			}
		}
		if preset.NewOwner != nil {
			if err := model.EnsureUsernameAvailableWithTx(tx, newOwnerUsername, 0); err != nil {
				if errors.Is(err, model.ErrUsernameAlreadyTaken) {
					return common.NewMessage("Username is already taken")
				}
				return err
			}
			if newOwnerEmail != "" {
				var existingEmail int64
				if err := tx.Unscoped().Model(&model.User{}).Where("LOWER(email) = ? AND site_id = 0", newOwnerEmail).Count(&existingEmail).Error; err != nil {
					return err
				}
				if existingEmail > 0 {
					return common.NewMessage("Email is already registered")
				}
			}
			ownerUser := model.User{
				Username:    newOwnerUsername,
				Password:    newOwnerPassHash,
				DisplayName: newOwnerDisplayName,
				Email:       newOwnerEmail,
				Role:        common.RoleCommonUser,
				Status:      common.UserStatusEnabled,
				Group:       "",
				SiteId:      0,
				Quota:       0,
				AuthVersion: 1,
			}
			if err := tx.Create(&ownerUser).Error; err != nil {
				return err
			}
			userId = ownerUser.Id
			site.OwnerUserId = userId
		}
		// Follow canonical User -> ReferralAffiliate -> ReferralCommissionAccount lock order (R4-09).
		lockIDs := []int{userId}
		if openKeyDigest != nil && preset.OperatorId > 0 && preset.OperatorId != userId {
			lockIDs = append(lockIDs, preset.OperatorId)
		}
		var lockedUsers []model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "site_id", "staff_site_id", "status").Where("id IN ?", lockIDs).Order("id ASC").Find(&lockedUsers).Error; err != nil {
			return err
		}
		if openKeyDigest != nil {
			var existing model.AdtySite
			lookup := tx.Where("open_key = ?", *openKeyDigest).Limit(1).Find(&existing)
			if lookup.Error != nil {
				return lookup.Error
			}
			if lookup.RowsAffected > 0 {
				if existing.OpenFingerprint != openFingerprint {
					return model.ErrAdtyIdempotencyConflict
				}
				site = &existing
				replayed = true
				return nil
			}
		}
		var targetUser *model.User
		for i := range lockedUsers {
			if lockedUsers[i].Id == userId {
				targetUser = &lockedUsers[i]
				break
			}
		}
		if targetUser == nil || targetUser.Status != common.UserStatusEnabled {
			return common.NewMessage("User not found")
		}
		if targetUser.StaffSiteId != 0 {
			return common.NewMessage("Site staff accounts cannot own a site")
		}
		if targetUser.SiteId > 0 {
			return ErrAdtySiteCustomer
		}
		if adtyTeamTableReadyFor(tx, &model.AdtyTeamMember{}) {
			var empCount int64
			if err := tx.Model(&model.AdtyTeamMember{}).Where("user_id = ? AND independent_employee = ?", userId, true).Count(&empCount).Error; err != nil {
				return err
			}
			if empCount > 0 {
				return common.NewMessage("Independent team employee accounts cannot own a site")
			}
		}
		var owned int64
		if err := tx.Model(&model.AdtySite{}).Where("owner_user_id = ? AND status <> ?", userId, model.AdtySiteStatusClosed).Count(&owned).Error; err != nil {
			return err
		}
		if owned > 0 {
			return ErrAdtySiteAlreadyOwner
		}
		// R5-27: Mark affiliate as site owner only after idempotency replay and
		// owner eligibility checks pass, while still holding the lockedUsers lock
		// to preserve canonical User -> ReferralAffiliate lock order (R4-09).
		if status == model.AdtySiteStatusActive {
			if err := markAffiliateSiteOwnerTx(tx, userId, now); err != nil {
				return err
			}
		}
		var selectedTier *model.AdtySiteTier
		if tierCode != "" {
			selectedTier = &model.AdtySiteTier{}
			if err := tx.Where("code = ? AND enabled = ?", tierCode, true).First(selectedTier).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtySiteTierNotFound
				}
				return err
			}
			if selectedTier.CostFactor == nil || !selectedTier.CostFactor.IsPositive() {
				return ErrAdtySiteTierInvalid
			}
		} else {
			var tierErr error
			selectedTier, tierErr = defaultAdtySiteTierTx(tx)
			if tierErr != nil {
				return tierErr
			}
			if selectedTier == nil {
				return common.NewMessage("Configure at least one enabled site level with a supply discount before opening a site")
			}
		}
		effectiveTierMode := tierMode
		if effectiveTierMode == "" {
			effectiveTierMode = AdtySiteTierModeAuto
		}
		site.TierCode = selectedTier.Code
		site.TierMode = effectiveTierMode
		site.TierCostFactor = selectedTier.CostFactor
		site.TierUpdatedAt = now
		var taken int64
		if err := tx.Model(&model.AdtySite{}).Where("subdomain = ?", label).Count(&taken).Error; err != nil {
			return err
		}
		if taken > 0 {
			return ErrAdtySiteSubdomainUsed
		}
		if preset.InviteCodeId > 0 {
			// 条件更新防并发超用：max_uses 为 0 不限。
			result := tx.Model(&model.AdtySiteInviteCode{}).
				Where("id = ? AND (max_uses = 0 OR used_count < max_uses) AND (expire_at = 0 OR expire_at > ?)", preset.InviteCodeId, now).
				Update("used_count", gorm.Expr("used_count + 1"))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrAdtySiteInviteInvalid
			}
		}
		if err := tx.Create(site).Error; err != nil {
			return err
		}
		if err := adtyAllocatePlatformDomainsTx(tx, site); err != nil {
			return err
		}
		if err := ensureAdtySiteWalletsTx(tx, site.Id); err != nil {
			return err
		}
		if selectedTier != nil {
			if err := tx.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).
				Update("required_balance", selectedTier.DepositUSD).Error; err != nil {
				return err
			}
		}
		var auditErr error
		auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, site.Id, "open", preset.OperatorId, nil, map[string]any{
			"opened_by": openedBy, "status": status, "subdomain": label, "root_domain": root,
			"cost_factor": site.CostFactor, "tier_code": site.TierCode, "tier_mode": site.TierMode,
			"invite_code_id": preset.InviteCodeId,
		}, "")
		return auditErr
	}, txOptions...)
	if err != nil {
		return nil, "", false, err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	if !replayed {
		InvalidateAdtySiteCache()
	}
	return site, eventID, replayed, nil
}

// markAffiliateSiteOwnerTx 写 SiteOwnerSince（只在还是 0 时写，关站后重开不覆盖第一次的时间）。
// 还没有推广员行时补建，避免之后打开推广页建成 SiteOwnerSince 为 0 的行再拿邀请返佣。
func markAffiliateSiteOwnerTx(tx *gorm.DB, userId int, now int64) error {
	// Match existing owners too: a no-op conditional UPDATE can release their
	// lock under MySQL READ COMMITTED and allow referral restoration mid-open.
	result := tx.Model(&model.ReferralAffiliate{}).Where("user_id = ?", userId).
		Update("site_owner_since", gorm.Expr("CASE WHEN site_owner_since = 0 THEN ? ELSE site_owner_since END", now))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var affiliates []model.ReferralAffiliate
	if err := model.AdtyLockForUpdate(tx).Where("user_id = ?", userId).Limit(1).Find(&affiliates).Error; err != nil {
		return err
	}
	if len(affiliates) > 0 {
		return tx.Model(&model.ReferralAffiliate{}).Where("id = ? AND site_owner_since = 0", affiliates[0].Id).Update("site_owner_since", now).Error
	}
	code, err := newInviteCode()
	if err != nil {
		return err
	}
	affiliate := model.ReferralAffiliate{
		UserId: userId, InviteCode: code, Status: model.ReferralAffiliateStatusApproved, SourceType: "site",
		AcquisitionEnabled: true, SettlementEnabled: true, WithdrawalEnabled: true, ApprovedAt: now, SiteOwnerSince: now,
	}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoNothing: true}).Create(&affiliate).Error; err != nil {
		return err
	}
	// A referral-page request can create the row between our lookup and insert.
	// Lock the actual row before touching its account, preserving Affiliate→Account.
	if err := model.AdtyLockForUpdate(tx).Where("user_id = ?", userId).First(&affiliate).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.ReferralAffiliate{}).Where("id = ? AND site_owner_since = 0", affiliate.Id).Update("site_owner_since", now).Error; err != nil {
		return err
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ReferralCommissionAccount{
		AffiliateId: affiliate.Id, UserId: userId, SettlementCurrency: "USD",
	}).Error
}

// ApplyAdtySite is retired. Self-serve and invite-code opening cannot create
// sites even if called directly; toggling InviteCodeEnabled does not turn it
// back on. Historical application rows stay; Root still reviews pending ones.
func ApplyAdtySite(userId int, subdomain, contactInfo, agreeVersion, inviteCode string) (*model.AdtySite, error) {
	return nil, ErrAdtySiteSelfServeRetired
}

// ReviewAdtySiteApplication 通过或驳回开站申请。通过时写 SiteOwnerSince，可以同时设单站 d 和 k 上限。
func ReviewAdtySiteApplication(siteId, adminId int, approve bool, costFactor, maxMarkup *decimal.Decimal, note string) (*model.AdtySite, error) {
	site, _, err := ReviewAdtySiteApplicationWithAudit(siteId, adminId, approve, costFactor, maxMarkup, note)
	return site, err
}

func ReviewAdtySiteApplicationWithAudit(siteId, adminId int, approve bool, costFactor, _ *decimal.Decimal, note string) (*model.AdtySite, string, error) {
	if approve && !resellerSetting().Ready() {
		return nil, "", ErrAdtySiteDisabled
	}
	if approve {
		if err := validateSitePreset(costFactor); err != nil {
			return nil, "", err
		}
	}
	var site model.AdtySite
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteId).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if site.Status != model.AdtySiteStatusPending {
			return ErrAdtySiteStatus
		}
		updates := map[string]any{"review_note": truncateText(note, 1000)}
		action := "reject"
		if approve {
			action = "approve"
			updates["status"] = model.AdtySiteStatusActive
			if costFactor != nil {
				updates["cost_factor"] = *costFactor
			}
			// Follow canonical User -> ReferralAffiliate lock order (R4-09).
			var owner model.User
			if err := model.AdtyLockForUpdate(tx).Select("id").Where("id = ?", site.OwnerUserId).First(&owner).Error; err != nil {
				return err
			}
			if err := markAffiliateSiteOwnerTx(tx, site.OwnerUserId, time.Now().Unix()); err != nil {
				return err
			}
		} else {
			// 驳回的申请直接关闭，子域名留着存档。站长可以换个子域名重新申请。
			updates["status"] = model.AdtySiteStatusClosed
			updates["closed_at"] = time.Now().Unix()
			updates["close_reason"] = "application rejected"
		}
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(updates).Error; err != nil {
			return err
		}
		var auditErr error
		auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, site.Id, action, adminId, map[string]any{"status": site.Status}, updates, note)
		return auditErr
	})
	if err != nil {
		return nil, "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	InvalidateAdtySiteCache()
	updated, err := GetAdtySite(siteId)
	return updated, eventID, err
}

// GetAdtySite 按 id 取站点。
func GetAdtySite(siteId int) (*model.AdtySite, error) {
	var site model.AdtySite
	if err := model.DB.Where("id = ?", siteId).First(&site).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtySiteNotFound
		}
		return nil, err
	}
	return &site, nil
}

// AdtySiteAdjust 是管理员对单站的调整；字段为 nil 表示不改，Clear* 改回「用全局」。
type AdtySiteAdjust struct {
	MaxMarkup          *decimal.Decimal // Deprecated: ignored for retail pricing.
	ClearMaxMarkup     bool             // Deprecated: ignored for retail pricing.
	CostFactor         *decimal.Decimal
	ClearCostFactor    bool
	Visibility         *string
	AllowedGroups      *string
	ClearAllowedGroups bool
	ContactInfo        *string
	Brand              map[string]string
	HomePageContent    *string
	SeoIndex           *bool
	Reason             string
}

// AdjustAdtySite 改单站 d、k 上限、可见范围。改 d 的通知站长由 11.9 的通知接上（AdtyOnSiteCostFactorChanged）。
// checkAdtySiteTargetActorTx enforces anti-self, role hierarchy, and PAT admin-target
// restrictions against site.OwnerUserId using canonical AdtySite -> User lock order (R13-006).
func checkAdtySiteTargetActorTx(tx *gorm.DB, site *model.AdtySite, actorID, actorRole int, denyAdminTarget bool) error {
	if site == nil || site.OwnerUserId <= 0 || (actorRole <= 0 && !denyAdminTarget) {
		return nil
	}
	var owner model.User
	if err := model.AdtyLockForUpdate(tx).Select("id", "role").First(&owner, site.OwnerUserId).Error; err != nil {
		return err
	}
	if actorID > 0 && actorID == owner.Id {
		return model.ErrUserQuotaPermission
	}
	if actorRole > 0 && actorRole != common.RoleRootUser && actorRole <= owner.Role {
		return model.ErrUserQuotaPermission
	}
	if denyAdminTarget && owner.Role >= common.RoleAdminUser {
		return model.ErrAdminTargetSessionRequired
	}
	return nil
}

func AdjustAdtySite(siteId, adminId int, adjust AdtySiteAdjust) (*model.AdtySite, error) {
	site, _, err := AdjustAdtySiteWithAudit(siteId, adminId, adjust)
	return site, err
}

func AdjustAdtySiteWithAudit(siteId, adminId int, adjust AdtySiteAdjust) (*model.AdtySite, string, error) {
	return AdjustAdtySiteWithAuditForActor(siteId, adminId, 0, false, adjust)
}

func AdjustAdtySiteWithAuditForActor(siteId, adminId, actorRole int, denyAdminTarget bool, adjust AdtySiteAdjust) (*model.AdtySite, string, error) {
	if err := validateSitePreset(adjust.CostFactor); err != nil {
		return nil, "", err
	}
	if adjust.Visibility != nil && strings.TrimSpace(*adjust.Visibility) != "" {
		values := map[string]string{}
		if err := common.UnmarshalJsonStr(*adjust.Visibility, &values); err != nil {
			return nil, "", common.NewMessage("Invalid parameters")
		}
		probe := adty_setting.ResellerSetting{Visibility: values, UnknownHostMode: adty_setting.ResellerUnknownHostMain, FingerprintWindowDays: 1}
		if err := adty_setting.ValidateResellerSetting(&probe, true); err != nil {
			return nil, "", err
		}
	}
	var allowedGroupIDs []string
	var allowedGroupsEncoded string
	if !adjust.ClearAllowedGroups && adjust.AllowedGroups != nil {
		var err error
		allowedGroupIDs, allowedGroupsEncoded, err = parseAdtyAllowedGroupIDs(*adjust.AllowedGroups)
		if err != nil {
			return nil, "", err
		}
	}
	var site model.AdtySite
	var costChanged bool
	var pendingScheduled bool
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if len(allowedGroupIDs) > 0 {
			if missingID, err := model.LockActiveAdtyGroupsTx(tx, allowedGroupIDs); err != nil {
				if errors.Is(err, model.ErrAdtyGroupNotFound) && missingID != "" {
					return common.NewMessage("Unknown group ID {{id}} in {{key}}", map[string]any{"id": missingID, "key": "allowed_groups"})
				}
				return err
			}
		}
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteId).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if err := checkAdtySiteTargetActorTx(tx, &site, adminId, actorRole, denyAdminTarget); err != nil {
			return err
		}
		if site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting {
			return ErrAdtySiteStatus
		}
		old := map[string]any{"cost_factor": site.CostFactor, "visibility": site.Visibility}
		updates := map[string]any{}
		var pendingNotice *decimal.Decimal
		var pendingAt int64
		switch {
		case adjust.ClearCostFactor:
			updates["cost_factor"] = nil
			updates["pending_cost_factor"] = gorm.Expr("NULL")
			updates["pending_cost_factor_at"] = 0
		case adjust.CostFactor != nil:
			current, hasCurrent := AdtySiteCostFactor(&site)
			delay := resellerSetting().MarkupIncreaseDelayHours
			if hasCurrent && adjust.CostFactor.GreaterThan(current) && delay > 0 {
				pendingAt = time.Now().Unix() + int64(delay)*3600
				updates["pending_cost_factor"] = *adjust.CostFactor
				updates["pending_cost_factor_at"] = pendingAt
				pendingNotice = adjust.CostFactor
			} else {
				updates["cost_factor"] = *adjust.CostFactor
				updates["pending_cost_factor"] = gorm.Expr("NULL")
				updates["pending_cost_factor_at"] = 0
			}
		}
		if adjust.Visibility != nil {
			updates["visibility"] = strings.TrimSpace(*adjust.Visibility)
		}
		switch {
		case adjust.ClearAllowedGroups:
			updates["allowed_groups"] = ""
		case adjust.AllowedGroups != nil:
			updates["allowed_groups"] = allowedGroupsEncoded
		}
		if adjust.ContactInfo != nil {
			updates["contact_info"] = truncateText(strings.TrimSpace(*adjust.ContactInfo), 500)
		}
		if adjust.SeoIndex != nil {
			updates["seo_index"] = *adjust.SeoIndex
		}
		if adjust.HomePageContent != nil {
			content := strings.TrimSpace(*adjust.HomePageContent)
			if err := validateAdtySiteHomePageContent(content); err != nil {
				return err
			}
			updates["home_page_content"] = content
		}
		if len(adjust.Brand) > 0 {
			var brand AdtySiteBrandInput
			if strings.TrimSpace(site.Brand) != "" {
				_ = common.UnmarshalJsonStr(site.Brand, &brand)
			}
			fields := map[string]*string{
				"name": &brand.Name, "title": &brand.Title, "notice": &brand.Notice,
				"support_contact": &brand.SupportContact,
				"tawk_property_id": &brand.TawkPropertyID, "tawk_widget_id": &brand.TawkWidgetID,
				"wallet_notice": &brand.WalletNotice, "voucher_purchase_url": &brand.VoucherPurchaseURL,
			}
			for key, val := range adjust.Brand {
				if key == "tagline" || key == "theme_color" {
					continue
				}
				dest, ok := fields[key]
				if !ok {
					return ErrAdtySiteBrandInvalid
				}
				*dest = val
			}
			normalized, normErr := NormalizeAdtySiteBrand(brand)
			if normErr != nil {
				return normErr
			}
			rawBrand, mErr := common.Marshal(normalized)
			if mErr != nil {
				return mErr
			}
			updates["brand"] = string(rawBrand)
		}
		if len(updates) == 0 {
			return nil
		}
		_, costChanged = updates["cost_factor"]
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(updates).Error; err != nil {
			return err
		}
		if pendingNotice != nil {
			site.PendingCostFactor = pendingNotice
			site.PendingCostFactorAt = pendingAt
			pendingScheduled = true
		}
		var auditErr error
		auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, site.Id, "adjust", adminId, old, updates, adjust.Reason)
		return auditErr
	})
	if err != nil {
		return nil, "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	InvalidateAdtySiteCache()
	updated, err := GetAdtySite(siteId)
	if err == nil && costChanged && AdtyOnSiteCostFactorChanged != nil {
		AdtyOnSiteCostFactorChanged(updated)
	}
	if err == nil && pendingScheduled && updated != nil && updated.PendingCostFactor != nil && updated.PendingCostFactorAt > 0 {
		adtyNotifySiteCostFactorPending(updated)
	}
	return updated, eventID, err
}

// ApplyDueAdtySiteCostFactors writes pending higher cost factors whose time has
// arrived. A later decrease clears the pending columns, so a due row is still
// an increase that was announced in advance.
func ApplyDueAdtySiteCostFactors(now int64) (int64, error) {
	var dueIDs []int
	if err := model.DB.Model(&model.AdtySite{}).
		Where("pending_cost_factor_at > 0 AND pending_cost_factor_at <= ? AND status NOT IN ?", now, []string{model.AdtySiteStatusClosed, model.AdtySiteStatusDeleting}).
		Order("id ASC").
		Pluck("id", &dueIDs).Error; err != nil {
		return 0, err
	}
	var applied int64
	changedSiteIDs := make([]int, 0, len(dueIDs))
	for _, siteID := range dueIDs {
		var rowApplied int64
		var effectiveChanged bool
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting || site.PendingCostFactor == nil || site.PendingCostFactorAt <= 0 || site.PendingCostFactorAt > now {
				return nil
			}
			beforeEff, beforeOK := AdtySiteCostFactor(&site)
			res := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(map[string]any{
				"cost_factor":            *site.PendingCostFactor,
				"pending_cost_factor":    gorm.Expr("NULL"),
				"pending_cost_factor_at": 0,
			})
			if res.Error != nil {
				return res.Error
			}
			rowApplied = res.RowsAffected
			if rowApplied > 0 {
				afterSite := site
				val := *site.PendingCostFactor
				afterSite.CostFactor = &val
				afterSite.PendingCostFactor = nil
				afterSite.PendingCostFactorAt = 0
				afterEff, afterOK := AdtySiteCostFactor(&afterSite)
				effectiveChanged = beforeOK != afterOK || !beforeEff.Equal(afterEff)
			}
			return nil
		})
		if err != nil {
			return applied, err
		}
		applied += rowApplied
		if effectiveChanged {
			changedSiteIDs = append(changedSiteIDs, siteID)
		}
	}
	if applied > 0 {
		InvalidateAdtySiteCache()
		if AdtyOnSiteCostFactorChanged != nil {
			for _, id := range changedSiteIDs {
				if updated, getErr := GetAdtySite(id); getErr == nil && updated != nil {
					AdtyOnSiteCostFactorChanged(updated)
				}
			}
		}
	}
	return applied, nil
}

// ApplyDueAdtySiteTierCostFactors writes a scheduled higher performance-tier d.
func ApplyDueAdtySiteTierCostFactors(now int64) (int64, error) {
	var dueIDs []int
	if err := model.DB.Model(&model.AdtySite{}).
		Where("pending_tier_cost_factor_at > 0 AND pending_tier_cost_factor_at <= ? AND status NOT IN ?", now, []string{model.AdtySiteStatusClosed, model.AdtySiteStatusDeleting}).
		Where("(tier_mode = ? OR tier_mode IS NULL) AND (tier_code = ? OR tier_code IS NULL)", "", "").
		Order("id ASC").
		Pluck("id", &dueIDs).Error; err != nil {
		return 0, err
	}
	var applied int64
	changedSiteIDs := make([]int, 0, len(dueIDs))
	for _, siteID := range dueIDs {
		var rowApplied int64
		var effectiveChanged bool
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting || site.PendingTierCostFactor == nil || site.PendingTierCostFactorAt <= 0 || site.PendingTierCostFactorAt > now || site.TierMode != "" || site.TierCode != "" {
				return nil
			}
			beforeEff, beforeOK := AdtySiteCostFactor(&site)
			res := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(map[string]any{
				"tier_cost_factor":            *site.PendingTierCostFactor,
				"pending_tier_cost_factor":    gorm.Expr("NULL"),
				"pending_tier_cost_factor_at": 0,
			})
			if res.Error != nil {
				return res.Error
			}
			rowApplied = res.RowsAffected
			if rowApplied > 0 {
				afterSite := site
				val := *site.PendingTierCostFactor
				afterSite.TierCostFactor = &val
				afterSite.PendingTierCostFactor = nil
				afterSite.PendingTierCostFactorAt = 0
				afterEff, afterOK := AdtySiteCostFactor(&afterSite)
				effectiveChanged = beforeOK != afterOK || !beforeEff.Equal(afterEff)
			}
			return nil
		})
		if err != nil {
			return applied, err
		}
		applied += rowApplied
		if effectiveChanged {
			changedSiteIDs = append(changedSiteIDs, siteID)
		}
	}
	if applied > 0 {
		InvalidateAdtySiteCache()
		if AdtyOnSiteCostFactorChanged != nil {
			for _, id := range changedSiteIDs {
				if updated, getErr := GetAdtySite(id); getErr == nil && updated != nil {
					AdtyOnSiteCostFactorChanged(updated)
				}
			}
		}
	}
	return applied, nil
}

// AdtyOnSiteCostFactorChanged 在管理员改了单站 d 之后调用（11.9 的站内信和邮件通知挂在这里）。
var AdtyOnSiteCostFactorChanged func(site *model.AdtySite)

// SetAdtySiteStatus 不再暂停或恢复分站。独立暂停/恢复已退役（DEC-019 / SITE-008）；
// 关站走 ScheduleAdtySiteClose。任何把站点改成或改出 paused 的请求都返回明确错误，不会静默成功。
// 遗留 status=paused 行只能由 Root 通过 RepairLeftoverPausedAdtySite 人工逐站修复，不会在此自动翻转。
func SetAdtySiteStatus(siteId, adminId int, status, reason string) (*model.AdtySite, error) {
	return nil, ErrAdtySitePauseRetired
}

// ListLeftoverPausedAdtySites returns leftover status=paused rows for Root review.
// It does not change any site status.
func ListLeftoverPausedAdtySites() ([]model.AdtySite, error) {
	var sites []model.AdtySite
	if err := model.DB.Where("status = ?", model.AdtySiteStatusPaused).Order("id ASC").Find(&sites).Error; err != nil {
		return nil, err
	}
	return sites, nil
}

// RepairLeftoverPausedAdtySite is the Root-only one-time repair for a leftover
// paused row. Root chooses, per site, either repair to active or enter the
// existing close/handover flow (ScheduleAdtySiteClose, customers inherited by
// the main site). Other statuses are rejected. Pause/resume write paths stay retired.
func RepairLeftoverPausedAdtySite(siteId, adminId int, outcome string, plan AdtySiteClosePlan) (*model.AdtySite, error) {
	site, _, _, err := RepairLeftoverPausedAdtySiteWithAudit(siteId, adminId, outcome, plan)
	return site, err
}

func RepairLeftoverPausedAdtySiteWithAudit(siteId, adminId int, outcome string, plan AdtySiteClosePlan) (*model.AdtySite, string, bool, error) {
	switch strings.TrimSpace(outcome) {
	case AdtyPausedRepairOutcomeActive:
		site, eventID, err := repairLeftoverPausedAdtySiteToActiveWithAudit(siteId, adminId, plan.Reason)
		return site, eventID, false, err
	case AdtyPausedRepairOutcomeClose:
		plan.TransferTo = 0
		plan.ClawBack = false
		return scheduleAdtySiteCloseWithAudit(siteId, adminId, plan, model.AdtySiteStatusPaused)
	default:
		return nil, "", false, ErrAdtySiteStatus
	}
}

func repairLeftoverPausedAdtySiteToActiveWithAudit(siteId, adminId int, reason string) (*model.AdtySite, string, error) {
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteId).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if site.Status != model.AdtySiteStatusPaused {
			return ErrAdtySitePausedRepair
		}
		updates := map[string]any{"status": model.AdtySiteStatusActive}
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(updates).Error; err != nil {
			return err
		}
		var auditErr error
		auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, site.Id, "paused_repair", adminId,
			map[string]any{"status": site.Status},
			map[string]any{"status": model.AdtySiteStatusActive, "outcome": AdtyPausedRepairOutcomeActive},
			reason)
		return auditErr
	})
	if err != nil {
		return nil, "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	InvalidateAdtySiteCache()
	site, err := GetAdtySite(siteId)
	return site, eventID, err
}

// RequestAdtySiteClose 是站长申请关站：只记下申请时间。关闭仍由 Root 在主站后台确认，客户一律由主站继承。
func RequestAdtySiteClose(ownerUserId int, reason string) (*model.AdtySite, error) {
	var site model.AdtySite
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.AdtyLockForUpdate(tx).Where("owner_user_id = ? AND status IN ?", ownerUserId,
			[]string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusPending}).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if site.CloseRequestedAt > 0 {
			return nil
		}
		now := time.Now().Unix()
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(map[string]any{
			"close_requested_at": now, "close_reason": truncateText(reason, 1000),
		}).Error; err != nil {
			return err
		}
		site.CloseRequestedAt = now
		return recordSiteAuditTx(tx, site.Id, "close_request", ownerUserId, nil, map[string]any{"close_requested_at": now}, reason)
	})
	if err != nil {
		return nil, err
	}
	return &site, nil
}

// AdtySiteClosePlan 是管理员定下的关站方式。
type AdtySiteClosePlan struct {
	ExecuteAt         int64 // Explicit revised deadline; zero requests the original notice.
	ExpectedExecuteAt int64
	TransferTo        int  // 只能是 0（主站）；非 0 会被拒绝，不会改写后继续关站
	ClawBack          bool // 兼容字段：G.7 / SITE-010 不允许因关站作废或扣回收益，传入也会被忽略
	Immediate         bool // 不等 close_notice_days，立即执行（例如违规）
	Reason            string
}

// ScheduleAdtySiteClose 把站点改成 closing，close_transfer_to 只记 0（由主站继承），
// close_notice_days 天后由主节点执行；Immediate 时立即执行。站点 pending（还没开）时直接关闭，没有客户。
func ScheduleAdtySiteClose(siteId, adminId int, plan AdtySiteClosePlan) (*model.AdtySite, error) {
	site, _, _, err := scheduleAdtySiteCloseWithAudit(siteId, adminId, plan, "")
	return site, err
}

func ScheduleAdtySiteCloseWithAudit(siteId, adminId int, plan AdtySiteClosePlan) (*model.AdtySite, string, bool, error) {
	return scheduleAdtySiteCloseWithAudit(siteId, adminId, plan, "")
}

func scheduleAdtySiteCloseWithAudit(siteId, adminId int, plan AdtySiteClosePlan, requiredStatus string) (*model.AdtySite, string, bool, error) {
	now := time.Now().Unix()
	immediate := plan.Immediate
	wasPending := false
	replay := false
	var receipt model.AdtySiteCloseMailBatch
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteId).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if requiredStatus != "" && site.Status != requiredStatus {
			return ErrAdtySitePausedRepair
		}
		if site.Status == model.AdtySiteStatusDeleting {
			return ErrAdtySiteStatus
		}
		if plan.TransferTo != 0 {
			return ErrAdtySiteTransfer
		}
		if site.Status == model.AdtySiteStatusClosing || site.Status == model.AdtySiteStatusClosed {
			q := tx.Where("site_id = ?", siteId)
			// Before the first reschedule the locked site still contains the original
			// deadline. Later revisions carry the prior deadline explicitly.
			if err := model.BackfillAdtyCloseMailDeadline(tx, site.Id); err != nil {
				return err
			}
			if plan.ExecuteAt == 0 {
				q = q.Where("revision = 1")
			} else {
				q = q.Where("execute_at = ? AND previous_execute_at = ?", plan.ExecuteAt, plan.ExpectedExecuteAt)
			}
			if err := q.Limit(1).Find(&receipt).Error; err != nil {
				return err
			}
			if plan.ExecuteAt == 0 || receipt.Id != 0 {
				replay = true
				return nil
			}
			if site.Status == model.AdtySiteStatusClosed || plan.ExpectedExecuteAt != site.CloseExecuteAt || plan.ExecuteAt <= now || strings.TrimSpace(plan.Reason) == "" || immediate {
				return ErrAdtySiteStatus
			}
		} else if plan.ExecuteAt != 0 {
			return ErrAdtySiteStatus
		}
		if site.Status == model.AdtySiteStatusPending {
			wasPending = true
			immediate = true
		}
		executeAt := now + int64(max(resellerSetting().CloseNoticeDays, 0))*86400
		if plan.ExecuteAt > 0 {
			executeAt = plan.ExecuteAt
		}
		if immediate {
			executeAt = now
		}
		updates := map[string]any{
			"status": model.AdtySiteStatusClosing, "close_execute_at": executeAt, "close_transfer_to": 0,
			"close_claw_back": false, "close_reason": truncateText(plan.Reason, 1000),
		}
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(updates).Error; err != nil {
			return err
		}
		if !wasPending {
			var last model.AdtySiteCloseMailBatch
			if err := tx.Where("site_id = ?", site.Id).Order("revision DESC").Limit(1).Find(&last).Error; err != nil {
				return err
			}
			batch := model.AdtySiteCloseMailBatch{SiteId: site.Id, Revision: last.Revision + 1, PreviousExecuteAt: site.CloseExecuteAt, ExecuteAt: executeAt, RetryStatus: model.AdtySiteCloseMailRetryNone, CreatedAt: now, UpdatedAt: now}
			if last.Id != 0 {
				if err := tx.Model(&last).Updates(map[string]any{"retry_status": "superseded"}).Error; err != nil {
					return err
				}
			}
			if err := tx.Create(&batch).Error; err != nil {
				return err
			}
			receipt = batch
		}
		action := "close_schedule"
		if plan.ExecuteAt > 0 {
			action = "close_reschedule"
		}
		afterSnap := make(map[string]any, len(updates)+2)
		for k, v := range updates {
			afterSnap[k] = v
		}
		if requiredStatus == model.AdtySiteStatusPaused {
			action = "paused_repair"
			afterSnap["outcome"] = AdtyPausedRepairOutcomeClose
			afterSnap["immediate"] = immediate
		}
		var auditErr error
		auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, site.Id, action, adminId, map[string]any{"status": site.Status, "close_execute_at": site.CloseExecuteAt}, afterSnap, plan.Reason)
		return auditErr
	})
	if err != nil {
		return nil, "", false, err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	result := func() (*model.AdtySite, string, bool, error) {
		site, err := GetAdtySite(siteId)
		if err == nil {
			site.CloseMailBatchId = receipt.Id
			if receipt.Id > 0 {
				site.CloseExecuteAt = receipt.ExecuteAt
			}
		}
		return site, eventID, replay, err
	}
	if replay {
		return result()
	}
	InvalidateAdtySiteCache()
	if AdtyOnSiteClosing != nil && !wasPending {
		if site, err := GetAdtySite(siteId); err == nil {
			AdtyOnSiteClosing(site)
		}
	}
	if immediate {
		if err := ExecuteAdtySiteClose(siteId, now); err != nil {
			return nil, eventID, false, err
		}
	}
	return result()
}

// AdtyOnSiteClosing 在站点进入 closing 后调用，11.3 用它给客户发关站通知邮件（按站点品牌）。
var AdtyOnSiteClosing func(site *model.AdtySite)

// AdtyOnSiteClosed 在关站事务提交后调用，11.3、11.8 用它删除发件域名和自定义域名。
var AdtyOnSiteClosed func(site *model.AdtySite)

// AdtySiteClawBackTx 曾由 11.5 注册，强制关站时作废 pending 收益并扣回已结算佣金。
// G.7 / SITE-010 要求关站后收益与佣金按原来源承接，此钩子不再被调用；即使被调用也不会改动资金。
var AdtySiteClawBackTx func(tx *gorm.DB, site *model.AdtySite, operatorId int) error

// ExecuteAdtySiteClose 执行到期的关站：客户、团队一律由主站继承，站点改 closed。可重复调用。
// 已排期但 close_transfer_to 仍指向其他站的 in-flight 关站同样只继承到主站，不会再分流。
// 迁入账号保留独立 user_id、inherited_from_site_id、迁移身份映射，并把原站 AdtySiteCustomer 归档而不是删除。
// 关站不会作废未结收益，也不会扣回已结算佣金；这些金额继续按原来源持有，后续结算、转余额、提现或退款走原规则。
func ExecuteAdtySiteClose(siteId int, now int64) error {
	var moved []int
	var disabledStaff []int
	var closed model.AdtySite
	var txOpts []*sql.TxOptions
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		// R9-08: Avoid REPEATABLE READ snapshot skew against concurrent
		// commission-to-balance conversions while migrating customer batches.
		txOpts = append(txOpts, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteId).First(&site).Error; err != nil {
			return err
		}
		if site.Status != model.AdtySiteStatusClosing || site.CloseExecuteAt > now {
			return nil
		}
		const target = 0
		// R4-007 / R5-004: Strictly follow canonical lock order:
		// AdtySite -> AdtyTeam -> User -> Token -> AdtyVoucher -> AdtySiteWallet(earnings -> margin -> operating).
		inheritedTeamIDs, err := inheritAdtyTeamsBeforeUsersOnCloseTx(tx, site.Id)
		if err != nil {
			return err
		}
		var movedPrincipal int64
		moved, movedPrincipal, err = model.AdtyCloseSiteUsersAndOwnerSelfUseTx(tx, site.Id, site.OwnerUserId, target)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.User{}).Where("staff_site_id = ?", site.Id).Pluck("id", &disabledStaff).Error; err != nil {
			return err
		}
		if err := disableAdtyTeamsKeysOnCloseTx(tx, inheritedTeamIDs); err != nil {
			return err
		}
		if err := InheritAdtyVouchersOnCloseTx(tx, site.Id, moved); err != nil {
			return err
		}
		if movedPrincipal > 0 && tx.Migrator().HasTable(&model.AdtySiteWallet{}) {
			// R3-015: Debit the migrated customer principal from the closing site's operating
			// wallet so no ghost operating balance remains after principal lots move to site_id=0.
			debitUSD := decimal.NewFromInt(movedPrincipal).Div(common.QuotaPerUnitDecimal())
			var opWallet model.AdtySiteWallet
			res := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).Limit(1).Find(&opWallet)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected > 0 && opWallet.Balance.IsPositive() {
				if debitUSD.GreaterThan(opWallet.Balance) {
					debitUSD = opWallet.Balance
				}
				if debitUSD.IsPositive() {
					if _, err := applyAdtySiteWalletChangeTx(tx, site.Id, model.AdtySiteWalletOperating, debitUSD.Neg(), AdtySiteWalletChange{
						Type:           "principal_site_close_migrate",
						RefType:        "site_close",
						RefID:          fmt.Sprintf("site:%d", site.Id),
						IdempotencyKey: fmt.Sprintf("operating:close-migrate:%d", site.Id),
					}); err != nil {
						return err
					}
				}
			}
		}
		if err := transferSiteTicketsToMainTx(tx, site.Id); err != nil {
			return err
		}
		// 主站没有在用的分站客户行；原站成员关系归档保留，不删除，以便追溯原始站点。
		if err := model.ArchiveAdtySiteCustomersOnCloseTx(tx, moved, site.Id, now); err != nil {
			return err
		}
		// R14-031: Require reconfirmation of closed-site Telegram bindings after users
		// inherit to main site, and skip unsent closed-site deliveries cleanly.
		if tx.Migrator().HasTable(&model.AdtyTelegramBinding{}) {
			if err := tx.Model(&model.AdtyTelegramBinding{}).
				Where("site_id = ? AND status = ?", site.Id, model.AdtyTelegramBindActive).
				Update("status", model.AdtyTelegramBindConfirm).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&model.AdtyTelegramDelivery{}) {
			if err := tx.Model(&model.AdtyTelegramDelivery{}).
				Where("site_id = ? AND status IN ?", site.Id, []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail}).
				Updates(map[string]any{"status": model.AdtyTelegramDeliverySkip, "last_error": "site closed"}).Error; err != nil {
				return err
			}
		}
		// R7-27 & R8-16: Stop pending/failed/in-flight subsite system email retries once the site closes.
		if tx.Migrator().HasTable(&AdtySystemEmailDelivery{}) {
			if err := tx.Model(&AdtySystemEmailDelivery{}).
				Where("site_id = ? AND status IN ?", site.Id, []string{AdtySystemEmailStatusPending, AdtySystemEmailStatusFailed, AdtySystemEmailStatusSending}).
				Updates(map[string]any{"status": AdtySystemEmailStatusDead, "body_cipher": "", "last_error": "site closed", "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(map[string]any{
			"status":                      model.AdtySiteStatusClosed,
			"closed_at":                   now,
			"close_transfer_to":           target,
			"pending_cost_factor":         gorm.Expr("NULL"),
			"pending_cost_factor_at":      0,
			"pending_tier_cost_factor":    gorm.Expr("NULL"),
			"pending_tier_cost_factor_at": 0,
		}).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&model.AdtySitePrice{}) {
			if err := tx.Model(&model.AdtySitePrice{}).
				Where("site_id = ? AND (pending_at > 0 OR pending_markup <> 0)", site.Id).
				Updates(map[string]any{"pending_markup": 0, "pending_at": 0}).Error; err != nil {
				return err
			}
		}
		site.Status, site.ClosedAt, site.CloseTransferTo = model.AdtySiteStatusClosed, now, target
		closed = site
		if err := adtyCloseDomainIntentsTx(tx, site.Id); err != nil {
			return err
		}
		return recordSiteAuditTx(tx, site.Id, "close", 0, nil, map[string]any{
			"transfer_to": target, "customers": len(moved), "archived_memberships": len(moved), "claw_back": false,
		}, "")
	}, txOpts...)
	if err != nil {
		return err
	}
	invalidateIDs := append([]int(nil), moved...)
	for _, sid := range disabledStaff {
		if sid > 0 && !slices.Contains(invalidateIDs, sid) {
			invalidateIDs = append(invalidateIDs, sid)
		}
	}
	if closed.OwnerUserId > 0 && !slices.Contains(invalidateIDs, closed.OwnerUserId) {
		invalidateIDs = append(invalidateIDs, closed.OwnerUserId)
	}
	model.AdtyInvalidateUserCaches(invalidateIDs)
	for _, sid := range disabledStaff {
		if sid > 0 {
			_, _ = model.RevokeAllUserSessions(sid, "site_closed")
			model.InvalidateUserTokensCache(sid)
		}
	}
	InvalidateAdtySiteCache()
	if closed.Id > 0 && AdtyOnSiteClosed != nil {
		AdtyOnSiteClosed(&closed)
	}
	return nil
}

// ResolveAdtySiteMarkup 在一个站的定价里按官方「全局 + 单模型独立加价」取存储 k（11.4）。
// 若该模型存在适用的单模型独立加价，取其中最具体的一行（客户+分组 → 客户 → 全站+分组 → 全站）。
// 否则取最具体的空模型/全局行，新模型继承该全局加价。正数 k（含小于 1）都会采用。
// 因此更具体的单模型仍覆盖更不具体的单模型；没有单模型时，更具体的空模型仍覆盖更不具体的空模型；
// 站点级（或更不具体的）单模型不会被更具体但空模型的客户/分组行盖掉。
// modelName 为空时只使用空模型默认行，不计单模型行。
func ResolveAdtySiteMarkup(prices []model.AdtySitePrice, userId int, group, modelName string) decimal.Decimal {
	return resolveAdtySitePrice(prices, userId, group, modelName).Markup
}

func resolveAdtySitePrice(prices []model.AdtySitePrice, userId int, group, modelName string) model.AdtySitePrice {
	modelName = adtyNormalizeSitePriceModel(modelName)
	bestModelRank, bestEmptyRank := 0, 0
	var bestModel, bestEmpty model.AdtySitePrice
	for _, price := range prices {
		if !price.Markup.IsPositive() {
			continue
		}
		rank := 0
		switch {
		case price.UserId == userId && userId != 0 && price.Group == group && group != "":
			rank = 4
		case price.UserId == userId && userId != 0 && price.Group == "":
			rank = 3
		case price.UserId == 0 && price.Group == group && group != "":
			rank = 2
		case price.UserId == 0 && price.Group == "":
			rank = 1
		}
		if rank == 0 {
			continue
		}
		rowModel := adtyNormalizeSitePriceModel(price.Model)
		if rowModel != "" && rowModel != modelName {
			continue
		}
		if rowModel != "" {
			if rank >= bestModelRank {
				bestModelRank = rank
				bestModel = price
			}
			continue
		}
		if rank >= bestEmptyRank {
			bestEmptyRank = rank
			bestEmpty = price
		}
	}
	if bestModelRank > 0 {
		return bestModel
	}
	if bestEmptyRank > 0 {
		return bestEmpty
	}
	return model.AdtySitePrice{Markup: decimal.NewFromInt(1)}
}

// RunAdtySiteCloseOnce 执行所有到期的关站，并由主节点任务回补已关站、仍占用冻结的站长自用/保证金未兑码。
func RunAdtySiteCloseOnce(now int64) error {
	var ids []int
	if err := model.DB.Model(&model.AdtySite{}).Where("status = ? AND close_execute_at <= ?", model.AdtySiteStatusClosing, now).Pluck("id", &ids).Error; err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		if err := ExecuteAdtySiteClose(id, now); err != nil {
			common.SysError(common.LogText("execute site close failed site=%d: %s", id, err.Error()))
			if first == nil {
				first = err
			}
		}
	}
	if err := RevokeUnusedOwnerAndMarginVouchersOnClosedSites(); err != nil {
		common.SysError(common.LogText("revoke leftover owner and margin vouchers on closed sites: %s", err.Error()))
		if first == nil {
			first = err
		}
	}
	if err := model.MigrateUnencumberedOwnerLotsOnClosedSites(); err != nil {
		common.SysError(common.LogText("migrate leftover owner principal lots on closed sites: %s", err.Error()))
		if first == nil {
			first = err
		}
	}
	return first
}

const adtySiteInviteAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewAdtySiteInviteCode 生成一个 12 位的开站邀请码（去掉容易看错的 0、O、1、I）。
func NewAdtySiteInviteCode() (string, error) {
	var b strings.Builder
	for range 12 {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(adtySiteInviteAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(adtySiteInviteAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// SaveAdtySiteInviteCode is retired. Invite-code minting is not an official
// opening path (A.10-01 / SITE-002). Historical invite-code rows stay and
// must be seeded by direct database writes in tests.
func SaveAdtySiteInviteCode(code *model.AdtySiteInviteCode) error {
	return ErrAdtySiteSelfServeRetired
}

// AdtySiteDisplayHost 是站点的平台子域名主机名。
func AdtySiteDisplayHost(site *model.AdtySite) string {
	if site == nil {
		return ""
	}
	return fmt.Sprintf("%s.%s", site.Subdomain, site.RootDomain)
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtySiteCloseTask)
}

// startAdtySiteCloseTask 在主节点每 10 分钟执行一次到期的关站。
func startAdtySiteCloseTask() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := RunAdtySiteCloseOnce(time.Now().Unix()); err != nil {
				common.SysError(common.LogText("site close task: %s", err.Error()))
			}
		}
	}()
}

// ClearAdtySiteNotice 是管理员一键下线分站公告（分站·调整，任务 11.3、11.9）：清空 Brand.notice，其余品牌字段不动，写审计。
// 返回下线前的公告（审计里也有）。没有公告时什么都不改。
func ClearAdtySiteNotice(siteId, adminId int, reason string) (string, error) {
	old, _, err := ClearAdtySiteNoticeWithAudit(siteId, adminId, reason)
	return old, err
}

func ClearAdtySiteNoticeWithAudit(siteId, adminId int, reason string) (string, string, error) {
	return ClearAdtySiteNoticeWithAuditForActor(siteId, adminId, 0, false, reason)
}

func ClearAdtySiteNoticeWithAuditForActor(siteId, adminId, actorRole int, denyAdminTarget bool, reason string) (string, string, error) {
	var old string
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteId).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if err := checkAdtySiteTargetActorTx(tx, &site, adminId, actorRole, denyAdminTarget); err != nil {
			return err
		}
		if site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting {
			return ErrAdtySiteStatus
		}
		brand := map[string]any{}
		if strings.TrimSpace(site.Brand) != "" {
			if err := common.UnmarshalJsonStr(site.Brand, &brand); err != nil {
				return err
			}
		}
		old, _ = brand["notice"].(string)
		if old == "" {
			return nil
		}
		brand["notice"] = ""
		encoded, err := common.Marshal(brand)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("brand", string(encoded)).Error; err != nil {
			return err
		}
		var auditErr error
		auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, site.Id, "notice_takedown", adminId, map[string]any{"notice": old}, map[string]any{"notice": ""}, reason)
		return auditErr
	})
	if err != nil {
		return "", "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	InvalidateAdtySiteCache()
	return old, eventID, nil
}
