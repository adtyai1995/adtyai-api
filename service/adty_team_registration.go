package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const (
	adtyTeamRegistrationMaxOutstanding = 50
	adtyTeamRegistrationListLimit      = 200
)

var (
	ErrAdtyTeamRegistrationExpired    = common.NewMessage("This team registration link has expired")
	ErrAdtyTeamRegistrationRevoked    = common.NewMessage("This team registration link has been revoked")
	ErrAdtyTeamRegistrationUsed       = common.NewMessage("This team registration link has already been used")
	ErrAdtyTeamRegistrationUnverified = common.NewMessage("This account could not be verified")
)

// AdtyTeamRegistrationIssue is returned once when a create-team link is issued.
// The plaintext code is not stored.
type AdtyTeamRegistrationIssue struct {
	Id        int64  `json:"id"`
	Code      string `json:"code"`
	SiteId    int    `json:"site_id"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedBy int    `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	Status    string `json:"status"`
	Replayed  bool   `json:"replayed,omitempty"`
}

// AdtyTeamRegistrationView is a secret-free list row for issued create-team links.
type AdtyTeamRegistrationView struct {
	Id        int64  `json:"id"`
	SiteId    int    `json:"site_id"`
	CreatedBy int    `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	UsedBy    int    `json:"used_by"`
	UsedAt    int64  `json:"used_at"`
	TeamId    int64  `json:"team_id"`
	RevokedBy int    `json:"revoked_by"`
	RevokedAt int64  `json:"revoked_at"`
	Status    string `json:"status"`
}

func hashAdtyTeamRegistrationCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func newAdtyTeamRegistrationCode() (code, hash string, err error) {
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	code = hex.EncodeToString(raw)
	return code, hashAdtyTeamRegistrationCode(code), nil
}

func adtyTeamRegistrationTTLValid(ttl time.Duration) bool {
	return ttl > 0 && ttl <= adtyTeamInviteMaxTTL
}

// CreateAdtyMainTeamRegistration issues a main-site create-team link. Only Root may call this.
func CreateAdtyMainTeamRegistration(actorId int, ttl time.Duration, keys ...string) (*AdtyTeamRegistrationIssue, error) {
	issued, _, err := CreateAdtyMainTeamRegistrationWithAudit(actorId, ttl, keys...)
	return issued, err
}

func CreateAdtyMainTeamRegistrationWithAudit(actorId int, ttl time.Duration, keys ...string) (*AdtyTeamRegistrationIssue, string, error) {
	if !adtyTeamRegistrationTTLValid(ttl) {
		return nil, "", ErrAdtyTeamInvalid
	}
	var actor model.User
	if err := model.DB.Select("id", "role").First(&actor, actorId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", ErrAdtyTeamForbidden
		}
		return nil, "", err
	}
	if actor.Role != common.RoleRootUser {
		return nil, "", ErrAdtyTeamForbidden
	}
	return issueAdtyTeamRegistrationWithAudit(actorId, 0, ttl, keys...)
}

// CreateAdtyOwnerSiteTeamRegistration issues a one-time create-team link bound
// to the owner's active subsite (R-41 #11).
func CreateAdtyOwnerSiteTeamRegistration(ownerId, siteId int, ttl time.Duration, keys ...string) (*AdtyTeamRegistrationIssue, error) {
	issued, _, err := CreateAdtyOwnerSiteTeamRegistrationWithAudit(ownerId, siteId, ttl, keys...)
	return issued, err
}

func CreateAdtyOwnerSiteTeamRegistrationWithAudit(ownerId, siteId int, ttl time.Duration, keys ...string) (*AdtyTeamRegistrationIssue, string, error) {
	if ownerId <= 0 || siteId <= 0 {
		return nil, "", ErrAdtyTeamForbidden
	}
	if !adtyTeamRegistrationTTLValid(ttl) {
		return nil, "", ErrAdtyTeamInvalid
	}
	return issueAdtyTeamRegistrationWithAudit(ownerId, siteId, ttl, keys...)
}

func issueAdtyTeamRegistrationWithAudit(actorId, siteId int, ttl time.Duration, keys ...string) (*AdtyTeamRegistrationIssue, string, error) {
	key := ""
	if len(keys) > 0 {
		key = strings.TrimSpace(keys[0])
	}
	if len(key) > 64 {
		return nil, "", ErrAdtyTeamIdempotencyKey
	}
	var createKey *string
	var fingerprint string
	if key != "" {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%s", actorId, siteId, key))))
		createKey = &digest
		fingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d/%d", siteId, int64(ttl.Seconds())))))
	}
	code, hash, err := newAdtyTeamRegistrationCode()
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	link := model.AdtyTeamRegistrationLink{
		CreateKey: createKey, CreateFingerprint: fingerprint,
		SiteId: siteId, CodeHash: hash, CreatedBy: actorId, ExpiresAt: now.Add(ttl).Unix(),
	}
	var auditEvent *model.AuditLog
	replayed := false
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if siteId > 0 {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).First(&site, siteId).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtyTeamForbidden
				}
				return err
			}
			if site.OwnerUserId != actorId {
				return ErrAdtyTeamForbidden
			}
			if site.Status == model.AdtySiteStatusClosing {
				return ErrAdtySiteClosing
			}
			if site.Status != model.AdtySiteStatusActive {
				return ErrAdtyTeamForbidden
			}
		} else if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(2026041001)).Error; err != nil {
				return err
			}
		}
		// Canonical lock order: AdtySite -> User. Locking the actor row also
		// serializes concurrent idempotent retries from the same actor on both
		// PostgreSQL and SQLite before checking create_key or the 50-link cap.
		var lockedActor model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "role", "status").First(&lockedActor, actorId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtyTeamForbidden
			}
			return err
		}
		if lockedActor.Status != common.UserStatusEnabled {
			return ErrAdtyTeamForbidden
		}
		if siteId == 0 && lockedActor.Role != common.RoleRootUser {
			return ErrAdtyTeamForbidden
		}
		if createKey != nil {
			var existing model.AdtyTeamRegistrationLink
			if err := tx.Where("create_key = ?", *createKey).Limit(1).Find(&existing).Error; err != nil {
				return err
			}
			if existing.Id > 0 {
				if existing.CreateFingerprint != fingerprint {
					return model.ErrAdtyIdempotencyConflict
				}
				link = existing
				replayed = true
				return nil
			}
		}
		var activeCount int64
		if err := tx.Model(&model.AdtyTeamRegistrationLink{}).
			Where("site_id = ? AND used_at = 0 AND revoked_at = 0 AND expires_at >= ?", siteId, now.Unix()).
			Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount >= adtyTeamRegistrationMaxOutstanding {
			return common.NewMessage("Too many active team registration links")
		}
		if err := tx.Create(&link).Error; err != nil {
			return err
		}
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, actorId, siteId, "team_registration", strconv.FormatInt(link.Id, 10), "adty.team.registration.create", "", "", nil, map[string]any{
			"link_id": link.Id, "site_id": siteId, "created_by": actorId,
			"expires_at": link.ExpiresAt, "result": model.AuditResultSuccess,
		}, model.AuditResultSuccess)
		return auditErr
	})
	if err != nil {
		if createKey != nil && isAdtyUniqueConflictError(err) {
			var existing model.AdtyTeamRegistrationLink
			if lookupErr := model.DB.Where("create_key = ?", *createKey).Limit(1).Find(&existing).Error; lookupErr == nil && existing.Id > 0 {
				if existing.CreateFingerprint != fingerprint {
					return nil, "", model.ErrAdtyIdempotencyConflict
				}
				link = existing
				replayed = true
				err = nil
			}
		}
		if err != nil {
			return nil, "", err
		}
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	issuedCode := code
	status := model.AdtyTeamRegistrationStatusOutstanding
	if replayed {
		issuedCode = ""
		status = model.AdtyTeamRegistrationStatus(link, time.Now().Unix())
	}
	issued := &AdtyTeamRegistrationIssue{
		Id: link.Id, Code: issuedCode, SiteId: link.SiteId, ExpiresAt: link.ExpiresAt,
		CreatedBy: link.CreatedBy, CreatedAt: link.CreatedAt,
		Status: status, Replayed: replayed,
	}
	return issued, eventID, nil
}

// CompleteAdtyTeamRegistration creates the team and leader from a one-time link.
// requestedSiteID, when provided, must match the site frozen at issuance.
func CompleteAdtyTeamRegistration(code, username, password, teamName string, requestedSiteID *int) (*AdtyTeamCreated, error) {
	code = strings.TrimSpace(code)
	username = strings.TrimSpace(username)
	teamName = strings.TrimSpace(teamName)
	if len(code) < 16 || username == "" || strings.Contains(username, "@") || len(username) > 20 || !model.ValidateUsernameNoControl(username) || teamName == "" || password == "" {
		return nil, ErrAdtyTeamInvalid
	}
	hash := hashAdtyTeamRegistrationCode(code)
	var created *AdtyTeamCreated
	var link model.AdtyTeamRegistrationLink
	var newUser *model.User
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// Pre-read site_id unlocked and lock AdtySite before AdtyTeamRegistrationLink
		// to preserve canonical lock order: AdtySite -> AdtyTeamRegistrationLink -> User.
		var peek struct {
			SiteId int
		}
		if err := tx.Model(&model.AdtyTeamRegistrationLink{}).Select("site_id").Where("code_hash = ?", hash).Limit(1).Scan(&peek).Error; err != nil {
			return err
		}
		var lockedSite model.AdtySite
		lockedSiteID := 0
		if peek.SiteId > 0 {
			res := model.AdtyLockForUpdate(tx).Where("id = ?", peek.SiteId).Limit(1).Find(&lockedSite)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected > 0 {
				lockedSiteID = lockedSite.Id
			}
		}
		lookup := model.AdtyLockForUpdate(tx).Where("code_hash = ?", hash).Limit(1).Find(&link)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return ErrAdtyTeamInvalid
		}
		now := time.Now().Unix()
		switch model.AdtyTeamRegistrationStatus(link, now) {
		case model.AdtyTeamRegistrationStatusUsed:
			return ErrAdtyTeamRegistrationUsed
		case model.AdtyTeamRegistrationStatusRevoked:
			return ErrAdtyTeamRegistrationRevoked
		case model.AdtyTeamRegistrationStatusExpired:
			return ErrAdtyTeamRegistrationExpired
		}
		if requestedSiteID != nil && *requestedSiteID != link.SiteId {
			return ErrAdtyTeamForbidden
		}
		var issuer model.User
		if err := tx.Select("id", "role", "status").First(&issuer, link.CreatedBy).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtyTeamForbidden
			}
			return err
		}
		if issuer.Status != common.UserStatusEnabled {
			return ErrAdtyTeamForbidden
		}
		if link.SiteId == 0 {
			if issuer.Role != common.RoleRootUser {
				return ErrAdtyTeamForbidden
			}
		} else {
			site := lockedSite
			if lockedSiteID != link.SiteId {
				if err := model.AdtyLockForUpdate(tx).First(&site, link.SiteId).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return ErrAdtyTeamForbidden
					}
					return err
				}
			}
			if site.Status != model.AdtySiteStatusActive {
				return common.NewMessage("Site is not open for registration")
			}
			if issuer.Role != common.RoleRootUser && site.OwnerUserId != link.CreatedBy {
				return ErrAdtyTeamForbidden
			}
		}
		leaderID, createdUser, err := resolveAdtyTeamRegistrationLeader(tx, link.SiteId, username, password)
		if err != nil {
			return err
		}
		row, err := createAdtyTeamTx(tx, leaderID, link.SiteId, teamName, adtyTeamDefaultPayRatio)
		if err != nil {
			return err
		}
		if err := tx.Model(&link).Updates(map[string]any{
			"used_by": leaderID, "used_at": now, "team_id": row.Team.Id,
		}).Error; err != nil {
			return err
		}
		link.UsedBy, link.UsedAt, link.TeamId = leaderID, now, row.Team.Id
		created = row
		newUser = createdUser
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, link.UsedBy, link.SiteId, "team_registration", strconv.FormatInt(link.Id, 10), "adty.team.registration.complete", "", "", nil, map[string]any{
			"link_id": link.Id, "site_id": link.SiteId, "created_by": link.CreatedBy,
			"used_by": link.UsedBy, "team_id": link.TeamId, "result": model.AuditResultSuccess,
		}, model.AuditResultSuccess)
		return auditErr
	})
	if err != nil {
		return nil, err
	}
	if newUser != nil {
		newUser.FinishInsert(0)
	}
	if auditEvent != nil {
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return created, nil
}

func resolveAdtyTeamRegistrationLeader(tx *gorm.DB, siteId int, username, password string) (int, *model.User, error) {
	var existing model.User
	lookup := tx.Unscoped().Where("LOWER(username) = ?", model.NormalizeUsername(username)).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return 0, nil, lookup.Error
	}
	if lookup.RowsAffected > 0 {
		// An existing ordinary account cannot be reused as the team leader.
		// Do not check the password: this anonymous entrance must not become a password oracle.
		return 0, nil, ErrAdtyTeamInvalid
	}
	if err := common.ValidateNewAccountPassword(password); err != nil {
		return 0, nil, err
	}
	user := &model.User{
		Username:    username,
		Password:    password,
		DisplayName: username,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "",
		AuthVersion: 1,
		SiteId:      siteId,
	}
	if err := user.InsertWithTx(tx, 0); err != nil {
		if errors.Is(err, model.ErrUsernameAlreadyTaken) || errors.Is(err, model.ErrEmailAlreadyTaken) {
			return 0, nil, ErrAdtyTeamInvalid
		}
		if errors.Is(err, model.ErrSiteRegistrationUnavailable) {
			return 0, nil, common.NewMessage("Site is not open for registration")
		}
		return 0, nil, err
	}
	return user.Id, user, nil
}

func ListAdtyMainTeamRegistrations(actorId int) ([]AdtyTeamRegistrationView, error) {
	var actor model.User
	if err := model.DB.Select("id", "role").First(&actor, actorId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtyTeamForbidden
		}
		return nil, err
	}
	if actor.Role != common.RoleRootUser {
		return nil, ErrAdtyTeamForbidden
	}
	return listAdtyTeamRegistrations(0)
}

func ListAdtyOwnerSiteTeamRegistrations(ownerId, siteId int) ([]AdtyTeamRegistrationView, error) {
	if siteId <= 0 {
		return nil, ErrAdtyTeamForbidden
	}
	if _, err := verifyOwnerSiteReadable(siteId, ownerId); err != nil {
		return nil, err
	}
	return listAdtyTeamRegistrations(siteId)
}

func listAdtyTeamRegistrations(siteId int) ([]AdtyTeamRegistrationView, error) {
	var rows []model.AdtyTeamRegistrationLink
	if err := model.DB.Where("site_id = ?", siteId).Order("id DESC").Limit(adtyTeamRegistrationListLimit).Find(&rows).Error; err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	out := make([]AdtyTeamRegistrationView, 0, len(rows))
	for _, row := range rows {
		out = append(out, adtyTeamRegistrationView(row, now))
	}
	return out, nil
}

func RevokeAdtyMainTeamRegistration(actorId int, linkId int64) (*AdtyTeamRegistrationView, error) {
	view, _, err := RevokeAdtyMainTeamRegistrationWithAudit(actorId, linkId)
	return view, err
}

func RevokeAdtyMainTeamRegistrationWithAudit(actorId int, linkId int64) (*AdtyTeamRegistrationView, string, error) {
	var actor model.User
	if err := model.DB.Select("id", "role").First(&actor, actorId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", ErrAdtyTeamForbidden
		}
		return nil, "", err
	}
	if actor.Role != common.RoleRootUser {
		return nil, "", ErrAdtyTeamForbidden
	}
	return revokeAdtyTeamRegistrationWithAudit(actorId, 0, linkId)
}

func RevokeAdtyOwnerSiteTeamRegistration(ownerId, siteId int, linkId int64) (*AdtyTeamRegistrationView, error) {
	if siteId <= 0 {
		return nil, ErrAdtyTeamForbidden
	}
	view, _, err := revokeAdtyTeamRegistrationWithAudit(ownerId, siteId, linkId)
	return view, err
}

func revokeAdtyTeamRegistrationWithAudit(actorId, siteId int, linkId int64) (*AdtyTeamRegistrationView, string, error) {
	if linkId <= 0 {
		return nil, "", ErrAdtyTeamInvalid
	}
	var view AdtyTeamRegistrationView
	var createdBy int
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if siteId > 0 {
			if _, err := verifyOwnerSiteWritableTx(tx, siteId, actorId); err != nil {
				return err
			}
		}
		var link model.AdtyTeamRegistrationLink
		lookup := model.AdtyLockForUpdate(tx).Where("id = ? AND site_id = ?", linkId, siteId).Limit(1).Find(&link)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return ErrAdtyTeamForbidden
		}
		if link.UsedAt != 0 || link.RevokedAt != 0 {
			return ErrAdtyTeamInvalid
		}
		now := time.Now().Unix()
		if err := tx.Model(&link).Updates(map[string]any{"revoked_by": actorId, "revoked_at": now}).Error; err != nil {
			return err
		}
		link.RevokedBy, link.RevokedAt = actorId, now
		createdBy = link.CreatedBy
		view = adtyTeamRegistrationView(link, now)
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, actorId, siteId, "team_registration", strconv.FormatInt(view.Id, 10), "adty.team.registration.revoke", "", "", map[string]any{
			"link_id": view.Id, "site_id": siteId, "status": model.AdtyTeamRegistrationStatusOutstanding,
		}, map[string]any{
			"link_id": view.Id, "site_id": siteId, "created_by": createdBy,
			"revoked_by": actorId, "revoked_at": view.RevokedAt, "result": model.AuditResultSuccess,
			"status": model.AdtyTeamRegistrationStatusRevoked,
		}, model.AuditResultSuccess)
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
	return &view, eventID, nil
}

func adtyTeamRegistrationView(link model.AdtyTeamRegistrationLink, now int64) AdtyTeamRegistrationView {
	return AdtyTeamRegistrationView{
		Id: link.Id, SiteId: link.SiteId, CreatedBy: link.CreatedBy, CreatedAt: link.CreatedAt,
		ExpiresAt: link.ExpiresAt, UsedBy: link.UsedBy, UsedAt: link.UsedAt, TeamId: link.TeamId,
		RevokedBy: link.RevokedBy, RevokedAt: link.RevokedAt,
		Status: model.AdtyTeamRegistrationStatus(link, now),
	}
}
