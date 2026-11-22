package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// adtyTeamTableReady caches a positive schema check per connection pool.
// A missing team table means no binding can exist, so personal billing continues.
var adtyTeamTableReady sync.Map

func adtyTeamTableReadyFor(tx *gorm.DB, modelValue any) bool {
	if tx == nil {
		return false
	}
	sqlDB, err := tx.DB()
	if err != nil {
		return false
	}
	key := struct {
		db    any
		model reflect.Type
	}{sqlDB, reflect.TypeOf(modelValue)}
	if ready, ok := adtyTeamTableReady.Load(key); ok {
		return ready.(bool)
	}
	ready := tx.Migrator().HasTable(modelValue)
	if ready {
		adtyTeamTableReady.Store(key, true)
	}
	return ready
}

func init() {
	model.AdtyTeamReserveHook = reserveAdtyTeamQuotaHook
	model.AdtyTeamSettleHook = settleAdtyTeamQuotaHook
	model.AdtyBeforeUserDeleteHook = closeAdtyTeamForDeletedLeader
	model.AdtyCreditTeamTopUpTx = creditAdtyTeamTopUpTx
}

func creditAdtyTeamTopUpTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder, principal int64) error {
	if principal <= 0 || topUp == nil || ext == nil || ext.TeamId <= 0 {
		return ErrAdtyTeamInvalid
	}
	var team model.AdtyTeam
	if err := model.AdtyLockForUpdate(tx).First(&team, ext.TeamId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.Join(ErrAdtyTeamForbidden, model.ErrTopUpStatusInvalid)
		}
		return err
	}
	if team.LeaderUserId != topUp.UserId || (team.Status != model.AdtyTeamStatusActive && team.Status != model.AdtyTeamStatusClosed) {
		return errors.Join(ErrAdtyTeamForbidden, model.ErrTopUpStatusInvalid)
	}
	if err := applyAdtyTeamQuotaTx(tx, &team, principal, "topup", ext.TradeNo, topUp.UserId); err != nil {
		return err
	}
	if team.Status == model.AdtyTeamStatusClosed && adtyTeamTableReadyFor(tx, &model.AdtyAnomalyDisposition{}) {
		var existing int64
		if err := tx.Model(&model.AdtyAnomalyDisposition{}).
			Where("kind = ? AND ref = ?", "closed_team_topup", ext.TradeNo).
			Count(&existing).Error; err != nil {
			return err
		}
		if existing == 0 {
			if err := tx.Create(&model.AdtyAnomalyDisposition{
				Kind:   "closed_team_topup",
				Ref:    ext.TradeNo,
				Status: "open",
				Note:   fmt.Sprintf("team %d closed before paid topup callback credited %d quota", team.Id, principal),
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// SaveAdtyMainTeamAgreementPrice sets a main-site team discount. Subsite teams are rejected.
// There is no supply-price floor. The discount must be greater than zero and at most 1.
func SaveAdtyMainTeamAgreementPrice(teamID int64, raw string) error {
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if teamID <= 0 || err != nil || !adtySiteTeamPayRatioConfigValid(value) {
		return ErrAdtyTeamInvalid
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var team model.AdtyTeam
		if err := model.AdtyLockForUpdate(tx).First(&team, teamID).Error; err != nil {
			return err
		}
		if team.SiteId != 0 || team.Status != model.AdtyTeamStatusActive {
			return ErrAdtyTeamForbidden
		}
		return tx.Model(&team).Update("discount", value).Error
	})
}

var (
	ErrAdtyTeamInvalid     = common.NewMessage("Invalid parameters")
	ErrAdtyTeamForbidden   = common.NewMessage("No permission to perform this action")
	ErrAdtyTeamBusy        = common.NewMessage("This account already belongs to a team")
	ErrAdtyTeamFunds       = common.NewMessage("Insufficient available funds")
	ErrAdtyTeamClosed      = common.NewMessage("This team is closed")
	ErrAdtyTeamLeaderFixed = common.NewMessage("The team leader cannot be replaced")
	// 幂等键是客户端自由文本，和仓库内其它入参一样要限长（40/64/100/128 都有先例，
	// 团队充值取 64）。超长必须报出具体原因：只说 Invalid parameters 的话，
	// 调用方会以为是业务冲突而不是入参问题。
	ErrAdtyTeamIdempotencyKey = common.NewMessage("Idempotency-Key is too long (maximum 64 characters)")
	ErrAdtyTeamKeyBound       = common.NewMessage("This API key is already bound to a team")
)

type AdtyTeamCreated struct {
	Team   model.AdtyTeam
	Member model.AdtyTeamMember
}

type AdtyTeamMemberView struct {
	UserId              int    `json:"user_id"`
	Username            string `json:"username"`
	Role                string `json:"role"`
	Status              string `json:"status"`
	IndependentEmployee bool   `json:"independent_employee,omitempty"`
}

type AdtyTeamKeyView struct {
	TokenId int    `json:"token_id"`
	Name    string `json:"name"`
	UserId  int    `json:"user_id"`
	Status  string `json:"status"`
}

type AdtyTeamMine struct {
	TeamId       int64                `json:"team_id"`
	Name         string               `json:"name"`
	SiteId       int                  `json:"site_id"`
	SourceSiteId int                  `json:"source_site_id"`
	Role         string               `json:"role"`
	Discount     decimal.Decimal      `json:"discount"`
	SharedQuota  int64                `json:"shared_quota"`
	Status       string               `json:"status"`
	Members      []AdtyTeamMemberView `json:"members"`
	Keys         []AdtyTeamKeyView    `json:"keys"`
}

// AdtyTeamInviteDefaultTTL is the existing controller default when the caller
// does not set a validity period. It is not a newly signed official number.
const AdtyTeamInviteDefaultTTL = 24 * time.Hour

// adtyTeamInviteMaxTTL is the existing CreateAdtyTeamInvite upper bound.
const adtyTeamInviteMaxTTL = 7 * 24 * time.Hour

// AdtyTeamInviteIssue is returned once when an invite is created. The plaintext
// code is not stored and is omitted from later list views.
type AdtyTeamInviteIssue struct {
	Id        int64  `json:"id"`
	Code      string `json:"code,omitempty"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedBy int    `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	Status    string `json:"status"`
}

// AdtyTeamInviteView is a secret-free list row for a team's invites.
type AdtyTeamInviteView struct {
	Id        int64  `json:"id"`
	TeamId    int64  `json:"team_id"`
	CreatedBy int    `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	UsedBy    int    `json:"used_by"`
	UsedAt    int64  `json:"used_at"`
	RevokedBy int    `json:"revoked_by"`
	RevokedAt int64  `json:"revoked_at"`
	Status    string `json:"status"`
}

func GetAdtyTeamMine(userId int) (*AdtyTeamMine, error) {
	var membership model.AdtyTeamMember
	lookup := model.DB.Where("user_id = ? AND status = ?", userId, model.AdtyTeamMemberActive).Limit(1).Find(&membership)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return nil, nil
	}
	var team model.AdtyTeam
	if err := model.DB.First(&team, membership.TeamId).Error; err != nil {
		return nil, err
	}
	page := &AdtyTeamMine{
		TeamId: team.Id, Name: team.Name, SiteId: team.SiteId, SourceSiteId: team.SourceSiteId, Role: membership.Role,
		Discount: team.Discount, SharedQuota: team.SharedQuota, Status: team.Status,
		Members: []AdtyTeamMemberView{}, Keys: []AdtyTeamKeyView{},
	}
	if err := fillAdtyTeamPage(page, membership); err != nil {
		return nil, err
	}
	return page, nil
}

func fillAdtyTeamPage(page *AdtyTeamMine, membership model.AdtyTeamMember) error {
	memberQuery := model.DB.Where("team_id = ?", page.TeamId)
	keyQuery := model.DB.Where("team_id = ?", page.TeamId)
	if membership.Role != model.AdtyTeamRoleLeader {
		memberQuery = memberQuery.Where("user_id = ?", membership.UserId)
		keyQuery = keyQuery.Where("user_id = ?", membership.UserId)
	}
	var members []model.AdtyTeamMember
	if err := memberQuery.Order("id ASC").Find(&members).Error; err != nil {
		return err
	}
	ids := make([]int, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.UserId)
	}
	names := map[int]string{}
	if len(ids) > 0 {
		var users []model.User
		if err := model.DB.Select("id", "username").Where("id IN ?", ids).Find(&users).Error; err != nil {
			return err
		}
		for _, user := range users {
			names[user.Id] = user.Username
		}
	}
	for _, member := range members {
		page.Members = append(page.Members, AdtyTeamMemberView{
			UserId: member.UserId, Username: names[member.UserId], Role: member.Role,
			Status: member.Status, IndependentEmployee: member.IndependentEmployee,
		})
	}
	var keys []model.AdtyTeamKey
	if err := keyQuery.Order("id ASC").Find(&keys).Error; err != nil {
		return err
	}
	tokenIds := make([]int, 0, len(keys))
	for _, key := range keys {
		tokenIds = append(tokenIds, key.TokenId)
	}
	tokenNames := map[int]string{}
	if len(tokenIds) > 0 {
		var tokens []model.Token
		if err := model.DB.Select("id", "name").Where("id IN ?", tokenIds).Find(&tokens).Error; err != nil {
			return err
		}
		for _, token := range tokens {
			tokenNames[token.Id] = token.Name
		}
	}
	for _, key := range keys {
		page.Keys = append(page.Keys, AdtyTeamKeyView{
			TokenId: key.TokenId, Name: tokenNames[key.TokenId], UserId: key.UserId, Status: key.Status,
		})
	}
	return nil
}

// TransferAdtyTeamLeader is refused. Closing a team does not appoint a new leader.
func TransferAdtyTeamLeader(int, int64, int) error {
	return ErrAdtyTeamLeaderFixed
}

func DisableAdtyTeamMember(leaderId int, teamId int64, memberUserId int) error {
	if memberUserId <= 0 || memberUserId == leaderId {
		return ErrAdtyTeamLeaderFixed
	}
	var event model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		if team.LeaderUserId != leaderId {
			return ErrAdtyTeamForbidden
		}
		var member model.AdtyTeamMember
		lookup := model.AdtyLockForUpdate(tx).Where("team_id = ? AND user_id = ? AND status = ?", team.Id, memberUserId, model.AdtyTeamMemberActive).Limit(1).Find(&member)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 || member.Role == model.AdtyTeamRoleLeader {
			return ErrAdtyTeamForbidden
		}
		status := model.AdtyTeamMemberLeft
		if member.IndependentEmployee {
			status = model.AdtyTeamMemberDisabled
		}
		if err := tx.Model(&member).Update("status", status).Error; err != nil {
			return err
		}
		if member.IndependentEmployee {
			if err := disableIndependentTeamEmployeeUserTx(tx, memberUserId); err != nil {
				return err
			}
			if err := tx.Model(&member).Update("disabled_auth_version", tx.Model(&model.User{}).Select("auth_version").Where("id = ?", memberUserId)).Error; err != nil {
				return err
			}
		}
		if err := disableAdtyTeamMemberKeysTx(tx, team.Id, memberUserId); err != nil {
			return err
		}
		event, err = appendAdtyTeamAuditTx(tx, leaderId, team.SiteId, "team_member", strconv.Itoa(memberUserId), "adty.team.member.disable",
			map[string]any{"team_id": teamId, "user_id": memberUserId, "status": model.AdtyTeamMemberActive},
			map[string]any{"team_id": teamId, "user_id": memberUserId, "status": status})
		return err
	})
	if err != nil {
		return err
	}
	_ = model.InvalidateUserTokensCache(memberUserId)
	deliverAdtyCommittedAudit(event)
	return nil
}

// EnableAdtyTeamMember re-enables a previously disabled/removed team employee
// account on the same team without reviving deleted accounts or revoked keys.
func EnableAdtyTeamMember(leaderId int, teamId int64, memberUserId int) error {
	if memberUserId <= 0 || memberUserId == leaderId {
		return ErrAdtyTeamInvalid
	}
	var event model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		if team.LeaderUserId != leaderId {
			return ErrAdtyTeamForbidden
		}
		var member model.AdtyTeamMember
		lookup := model.AdtyLockForUpdate(tx).Where("team_id = ? AND user_id = ?", team.Id, memberUserId).Limit(1).Find(&member)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 || !member.IndependentEmployee || member.Role == model.AdtyTeamRoleLeader || (member.Status != model.AdtyTeamMemberDisabled && member.Status != model.AdtyTeamMemberActive) || member.DeletedAt != 0 {
			return ErrAdtyTeamForbidden
		}
		if member.Status == model.AdtyTeamMemberActive {
			return nil
		}
		var user model.User
		if err := model.AdtyLockForUpdate(tx).First(&user, memberUserId).Error; err != nil {
			return err
		}
		if user.DeletedAt.Valid || user.SiteId != team.SiteId || user.StaffSiteId != 0 || user.Role != common.RoleCommonUser {
			return ErrAdtyTeamForbidden
		}
		if user.Status != common.UserStatusDisabled || member.DisabledAuthVersion == 0 || user.AuthVersion != member.DisabledAuthVersion {
			return ErrAdtyTeamForbidden
		}
		if err := rejectTeamStaffOrOwnerIdentityTx(tx, user.Id); err != nil {
			return err
		}
		if err := rejectActiveTeamMembership(tx, user.Id, team.Id); err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			if _, err := model.IncrementUserAuthVersionWithTx(tx, user.Id); err != nil {
				return err
			}
			if err := tx.Model(&model.User{}).Where("id = ?", user.Id).Update("status", common.UserStatusEnabled).Error; err != nil {
				return err
			}
		}
		beforeStatus := member.Status
		if err := tx.Model(&member).Updates(map[string]any{"status": model.AdtyTeamMemberActive, "disabled_auth_version": 0}).Error; err != nil {
			return err
		}
		event, err = appendAdtyTeamAuditTx(tx, leaderId, team.SiteId, "team_member", strconv.Itoa(memberUserId), "adty.team.member.enable",
			map[string]any{"team_id": teamId, "user_id": memberUserId, "status": beforeStatus},
			map[string]any{"team_id": teamId, "user_id": memberUserId, "status": model.AdtyTeamMemberActive})
		return err
	})
	if err != nil {
		return err
	}
	_ = model.PublishUserAuthCache(memberUserId)
	if event.EventId != "" {
		deliverAdtyCommittedAudit(event)
	}
	return nil
}

// DeleteAdtyTeamMember permanently deletes a team employee sub-account from the
// active team, revoking its keys and disabling its login identity while keeping
// historical membership and billing records.
func DeleteAdtyTeamMember(leaderId int, teamId int64, memberUserId int) error {
	if memberUserId <= 0 || memberUserId == leaderId {
		return ErrAdtyTeamLeaderFixed
	}
	var event model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		if team.LeaderUserId != leaderId {
			return ErrAdtyTeamForbidden
		}
		var member model.AdtyTeamMember
		lookup := model.AdtyLockForUpdate(tx).Where("team_id = ? AND user_id = ?", team.Id, memberUserId).Limit(1).Find(&member)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 || !member.IndependentEmployee || member.Role == model.AdtyTeamRoleLeader || member.Status == model.AdtyTeamMemberDeleted || member.DeletedAt != 0 {
			return ErrAdtyTeamForbidden
		}
		beforeStatus := member.Status
		now := time.Now().Unix()
		if err := tx.Model(&member).Updates(map[string]any{
			"status":     model.AdtyTeamMemberDeleted,
			"deleted_at": now,
		}).Error; err != nil {
			return err
		}
		if err := disableIndependentTeamEmployeeUserTx(tx, memberUserId); err != nil {
			return err
		}
		if err := disableAdtyTeamMemberKeysTx(tx, team.Id, memberUserId); err != nil {
			return err
		}
		event, err = appendAdtyTeamAuditTx(tx, leaderId, team.SiteId, "team_member", strconv.Itoa(memberUserId), "adty.team.member.delete",
			map[string]any{"team_id": teamId, "user_id": memberUserId, "status": beforeStatus},
			map[string]any{"team_id": teamId, "user_id": memberUserId, "status": model.AdtyTeamMemberDeleted})
		return err
	})
	if err != nil {
		return err
	}
	_ = model.InvalidateUserTokensCache(memberUserId)
	deliverAdtyCommittedAudit(event)
	return nil
}

// CreateAdtyTeamEmployee creates a dedicated team employee sub-account owned by
// the team's leader, isolated from site staff and ordinary reusable accounts.
func CreateAdtyTeamEmployee(leaderId int, teamId int64, username, password, displayName string) (*AdtyTeamMemberView, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = username
	}
	if leaderId <= 0 || teamId <= 0 || username == "" || strings.Contains(username, "@") || len(username) > 20 || utf8.RuneCountInString(displayName) > 20 {
		return nil, ErrAdtyTeamInvalid
	}
	if err := common.Validate.StructPartial(&model.User{Username: username}, "Username"); err != nil {
		return nil, ErrAdtyTeamInvalid
	}
	if err := common.ValidateNewAccountPassword(password); err != nil {
		return nil, err
	}
	var view *AdtyTeamMemberView
	var event model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		if team.LeaderUserId != leaderId {
			return ErrAdtyTeamForbidden
		}
		user, member, err := createIndependentTeamEmployeeTx(tx, team, username, password, displayName)
		if err != nil {
			return err
		}
		view = &AdtyTeamMemberView{
			UserId:              user.Id,
			Username:            user.Username,
			Role:                member.Role,
			Status:              member.Status,
			IndependentEmployee: true,
		}
		event, err = appendAdtyTeamAuditTx(tx, leaderId, team.SiteId, "team_member", strconv.Itoa(user.Id), "adty.team.member.create", nil,
			map[string]any{"team_id": teamId, "user_id": user.Id, "username": user.Username, "independent_employee": true})
		return err
	})
	if err != nil {
		return nil, err
	}
	deliverAdtyCommittedAudit(event)
	return view, nil
}

// RegisterAdtyTeamEmployee consumes a one-time team invite code to register a
// new independent team employee sub-account bound to that team and its site.
func RegisterAdtyTeamEmployee(code, username, password, displayName string, requestedSiteID int) (*AdtyTeamMemberView, error) {
	code = strings.TrimSpace(code)
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = username
	}
	if len(code) < 16 || len(code) > 128 || username == "" || strings.Contains(username, "@") || len(username) > 20 || utf8.RuneCountInString(displayName) > 20 {
		return nil, ErrAdtyTeamInvalid
	}
	if err := common.Validate.StructPartial(&model.User{Username: username}, "Username"); err != nil {
		return nil, ErrAdtyTeamInvalid
	}
	if err := common.ValidateNewAccountPassword(password); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(code))
	hash := hex.EncodeToString(sum[:])
	var view *AdtyTeamMemberView
	var invite model.AdtyTeamInvite
	var event model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		team, lockedInvite, err := lockAdtyTeamInviteTx(tx, hash)
		if err != nil {
			return err
		}
		invite = *lockedInvite
		now := time.Now().Unix()
		if requestedSiteID < 0 || requestedSiteID != team.SiteId {
			return ErrAdtyTeamForbidden
		}
		user, member, err := createIndependentTeamEmployeeTx(tx, team, username, password, displayName)
		if err != nil {
			return err
		}
		if err := tx.Model(&invite).Updates(map[string]any{"used_by": user.Id, "used_at": now}).Error; err != nil {
			return err
		}
		invite.UsedBy, invite.UsedAt = user.Id, now
		view = &AdtyTeamMemberView{
			UserId:              user.Id,
			Username:            user.Username,
			Role:                member.Role,
			Status:              member.Status,
			IndependentEmployee: true,
		}
		event, err = appendAdtyTeamAuditTx(tx, user.Id, team.SiteId, "team_invite", strconv.FormatInt(invite.Id, 10), "adty.team.invite.accept", nil,
			map[string]any{"invite_id": invite.Id, "team_id": invite.TeamId, "created_by": invite.CreatedBy, "used_by": user.Id, "independent_employee": true})
		return err
	})
	if err != nil {
		return nil, err
	}
	deliverAdtyCommittedAudit(event)
	return view, nil
}

func createIndependentTeamEmployeeTx(tx *gorm.DB, team *model.AdtyTeam, username, password, displayName string) (*model.User, *model.AdtyTeamMember, error) {
	hash, err := common.HashAccountPassword(password)
	if err != nil {
		return nil, nil, err
	}
	if err := model.EnsureUsernameAvailableWithTx(tx, username, 0); err != nil {
		if errors.Is(err, model.ErrUsernameAlreadyTaken) {
			return nil, nil, ErrAdtyTeamInvalid
		}
		return nil, nil, err
	}
	user := &model.User{
		Username:    username,
		Password:    hash,
		DisplayName: displayName,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		SiteId:      team.SiteId,
		StaffSiteId: 0,
	}
	// Dedicated employees start with no personal gift, commission or wallet lots.
	if err := tx.Session(&gorm.Session{Logger: logger.Discard}).Create(user).Error; err != nil {
		if model.IsAdtyUniqueViolation(err) {
			return nil, nil, ErrAdtyTeamInvalid
		}
		return nil, nil, err
	}
	member := model.AdtyTeamMember{
		TeamId:              team.Id,
		UserId:              user.Id,
		Role:                model.AdtyTeamRoleMember,
		Status:              model.AdtyTeamMemberActive,
		IndependentEmployee: true,
	}
	if err := tx.Create(&member).Error; err != nil {
		return nil, nil, mapAdtyTeamMemberWriteError(err)
	}
	return user, &member, nil
}

func disableIndependentTeamEmployeeUserTx(tx *gorm.DB, userId int) error {
	if userId <= 0 || !adtyTeamTableReadyFor(tx, &model.User{}) {
		return nil
	}
	if _, err := model.IncrementUserAuthVersionWithTx(tx, userId); err != nil {
		return err
	}
	if err := tx.Model(&model.User{}).Where("id = ?", userId).Update("status", common.UserStatusDisabled).Error; err != nil {
		return err
	}
	return nil
}

func LeaveAdtyTeam(userId int, teamId int64) error {
	var closedByLeader bool
	var siteID int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		siteID = team.SiteId
		if team.LeaderUserId == userId {
			closedByLeader = true
			return closeAdtyTeamTx(tx, team)
		}
		var member model.AdtyTeamMember
		lookup := model.AdtyLockForUpdate(tx).Where("team_id = ? AND user_id = ? AND status = ?", team.Id, userId, model.AdtyTeamMemberActive).Limit(1).Find(&member)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return ErrAdtyTeamForbidden
		}
		if err := tx.Model(&member).Update("status", model.AdtyTeamMemberLeft).Error; err != nil {
			return err
		}
		if member.IndependentEmployee {
			if err := disableIndependentTeamEmployeeUserTx(tx, userId); err != nil {
				return err
			}
			if err := tx.Model(&member).Updates(map[string]any{
				"status":                model.AdtyTeamMemberDisabled,
				"disabled_auth_version": tx.Model(&model.User{}).Select("auth_version").Where("id = ?", userId),
			}).Error; err != nil {
				return err
			}
		}
		return disableAdtyTeamMemberKeysTx(tx, team.Id, userId)
	})
	if err != nil {
		return err
	}
	_ = model.InvalidateUserTokensCache(userId)
	if closedByLeader {
		writeAdtyTeamAudit(userId, siteID, "team", strconv.FormatInt(teamId, 10), "adty.team.close",
			map[string]any{"team_id": teamId, "status": model.AdtyTeamStatusActive},
			map[string]any{"team_id": teamId, "status": model.AdtyTeamStatusClosed, "reason": "leader_leave", "result": model.AuditResultSuccess})
		return nil
	}
	writeAdtyTeamAudit(userId, siteID, "team_member", strconv.Itoa(userId), "adty.team.member.leave",
		map[string]any{"team_id": teamId, "user_id": userId, "status": model.AdtyTeamMemberActive},
		map[string]any{"team_id": teamId, "user_id": userId, "status": model.AdtyTeamMemberLeft, "result": model.AuditResultSuccess})
	return nil
}

func RevokeAdtyTeamKey(actorId int, teamId int64, tokenId int) error {
	var keyOwnerID int
	var siteID int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var team model.AdtyTeam
		if err := model.AdtyLockForUpdate(tx).First(&team, teamId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtyTeamForbidden
			}
			return err
		}
		siteID = team.SiteId
		var key model.AdtyTeamKey
		lookup := model.AdtyLockForUpdate(tx).Where("team_id = ? AND token_id = ?", teamId, tokenId).Limit(1).Find(&key)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return ErrAdtyTeamForbidden
		}
		if actorId != key.UserId && actorId != team.LeaderUserId {
			return ErrAdtyTeamForbidden
		}
		keyOwnerID = key.UserId
		var token model.Token
		if err := model.AdtyLockForUpdate(tx).First(&token, key.TokenId).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Model(&model.Token{}).Where("id = ?", key.TokenId).Update("status", common.TokenStatusDisabled).Error; err != nil {
			return err
		}
		return tx.Model(&key).Update("status", model.AdtyTeamMemberLeft).Error
	})
	if err != nil {
		return err
	}
	if keyOwnerID > 0 {
		_ = model.InvalidateUserTokensCache(keyOwnerID)
	}
	writeAdtyTeamAudit(actorId, siteID, "team_key", strconv.Itoa(tokenId), "adty.team.key.revoke",
		map[string]any{"team_id": teamId, "token_id": tokenId, "status": model.AdtyTeamMemberActive},
		map[string]any{"team_id": teamId, "token_id": tokenId, "status": model.AdtyTeamMemberLeft, "result": model.AuditResultSuccess})
	return nil
}

// CreateAdtyTeamForCurrentUser is the former self-serve create path.
// Ordinary users must not create a team or submit a protocol discount.
func CreateAdtyTeamForCurrentUser(_ int, _ string, _ decimal.Decimal) (*AdtyTeamCreated, error) {
	return nil, ErrAdtyTeamForbidden
}

// adtyTeamDefaultPayRatio is "no discount": the team pays the ordinary price.
var adtyTeamDefaultPayRatio = decimal.NewFromInt(1)

func adtyTeamDiscountOrDefault(raw string) (decimal.Decimal, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return adtyTeamDefaultPayRatio, nil
	}
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, ErrAdtyTeamInvalid
	}
	return value, nil
}

// CreateAdtyMainTeamByRoot creates a main-site team and designates an existing
// main-site user as the unique leader. Only Root may call this.
func CreateAdtyMainTeamByRoot(actorId, leaderId int, name, rawDiscount string) (*AdtyTeamCreated, error) {
	return CreateAdtySiteTeamByRoot(actorId, 0, leaderId, name, rawDiscount)
}

// CreateAdtySiteTeamByRoot creates a team on the specified site (0 for main site,
// >0 for an active subsite) and designates an existing user of that site as the
// unique leader. Only Root may call this (A.9.1 / DEC-012).
func CreateAdtySiteTeamByRoot(actorId, siteId, leaderId int, name, rawDiscount string, keys ...string) (*AdtyTeamCreated, error) {
	if siteId < 0 {
		return nil, ErrAdtyTeamInvalid
	}
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
	discount, err := adtyTeamDiscountOrDefault(rawDiscount)
	if err != nil {
		return nil, err
	}
	var created *AdtyTeamCreated
	var event model.AuditLog
	key := ""
	if len(keys) > 0 {
		key = strings.TrimSpace(keys[0])
	}
	if len(key) > 64 {
		return nil, ErrAdtyTeamIdempotencyKey
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d/%s", actorId, key))))
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%s/%s", siteId, leaderId, discount.String(), strings.TrimSpace(name)))))
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if key != "" {
			if err := model.AdtyLockForUpdate(tx).Select("id", "role").First(&actor, actorId).Error; err != nil {
				return err
			}
			if actor.Role != common.RoleRootUser {
				return ErrAdtyTeamForbidden
			}
			var existing model.AdtyTeam
			if err := tx.Where("create_key = ?", digest).Limit(1).Find(&existing).Error; err != nil {
				return err
			}
			if existing.Id > 0 {
				if existing.CreateFingerprint != fingerprint {
					return model.ErrAdtyIdempotencyConflict
				}
				var member model.AdtyTeamMember
				if err := tx.Where("team_id = ? AND user_id = ?", existing.Id, leaderId).First(&member).Error; err != nil {
					return err
				}
				created = &AdtyTeamCreated{Team: existing, Member: member}
				return nil
			}
		}
		var err error
		created, err = createAdtyTeamTx(tx, leaderId, siteId, name, discount)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAdtyTeamForbidden
		}
		if err != nil {
			return err
		}
		if key != "" {
			if err := tx.Model(&created.Team).Updates(map[string]any{"create_key": digest, "create_fingerprint": fingerprint}).Error; err != nil {
				return err
			}
		}
		event, err = appendAdtyTeamAuditTx(tx, actorId, siteId, "team", strconv.FormatInt(created.Team.Id, 10), "adty.team.create", nil, map[string]any{"team_id": created.Team.Id, "site_id": siteId, "leader_user_id": leaderId, "name": created.Team.Name, "discount": discount.String()})
		return err
	})
	if err != nil {
		return nil, err
	}
	if event.EventId != "" {
		deliverAdtyCommittedAudit(event)
	}
	return created, nil
}

// CreateAdtyOwnerSiteTeam is permanently rejected under A.9.1 / DEC-012:
// only Root may create teams and designate team leaders.
func CreateAdtyOwnerSiteTeam(_ int, _ int, _ int, _, _ string) (*AdtyTeamCreated, error) {
	return nil, ErrAdtyTeamForbidden
}

func CreateAdtyTeam(leaderId, siteId int, name string, discount decimal.Decimal) (*AdtyTeamCreated, error) {
	var created *AdtyTeamCreated
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		row, err := createAdtyTeamTx(tx, leaderId, siteId, name, discount)
		if err != nil {
			return err
		}
		created = row
		return nil
	})
	return created, err
}

func createAdtyTeamTx(tx *gorm.DB, leaderId, siteId int, name string, discount decimal.Decimal) (*AdtyTeamCreated, error) {
	name = strings.TrimSpace(name)
	if leaderId <= 0 || siteId < 0 || name == "" || len(name) > 64 || utf8.RuneCountInString(name) > 64 || !adtySiteTeamPayRatioConfigValid(discount) {
		return nil, ErrAdtyTeamInvalid
	}
	leader, err := lockAdtyTeamUser(tx, leaderId)
	if err != nil {
		return nil, err
	}
	if leader.SiteId != siteId || leader.StaffSiteId != 0 {
		return nil, ErrAdtyTeamForbidden
	}
	if err := rejectTeamStaffIdentityTx(tx, leader.Id); err != nil {
		return nil, err
	}
	if siteId > 0 {
		var site model.AdtySite
		if err := tx.First(&site, siteId).Error; err != nil {
			return nil, err
		}
		if site.Status != model.AdtySiteStatusActive {
			return nil, ErrAdtyTeamForbidden
		}
	}
	if err := rejectActiveTeamMembership(tx, leader.Id); err != nil {
		return nil, err
	}
	team := model.AdtyTeam{SiteId: siteId, LeaderUserId: leader.Id, Name: name, Discount: discount, Status: model.AdtyTeamStatusActive}
	if err := tx.Create(&team).Error; err != nil {
		return nil, err
	}
	member := model.AdtyTeamMember{TeamId: team.Id, UserId: leader.Id, Role: model.AdtyTeamRoleLeader, Status: model.AdtyTeamMemberActive}
	if err := tx.Create(&member).Error; err != nil {
		return nil, mapAdtyTeamMemberWriteError(err)
	}
	return &AdtyTeamCreated{Team: team, Member: member}, nil
}

func JoinAdtyTeam(userId int, teamId int64) (*model.AdtyTeamMember, error) {
	var member *model.AdtyTeamMember
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		joined, err := joinAdtyTeamTx(tx, userId, teamId)
		if err != nil {
			return err
		}
		member = joined
		return nil
	})
	return member, err
}

// adtyTeamTopupKeyMaxLen 是团队充值幂等键的长度上限。
const adtyTeamTopupKeyMaxLen = 64

// adtyTeamTopupTradeNo 由「团队 + 客户端幂等键」派生单号。
//
// 键必须带团队作用域：单号是全局唯一的，若只由客户端自由文本派生，A 团队用过的
// key 会永久占住 B 团队的同一个 key，B 只会拿到 Invalid parameters 而查不出原因
// （ISS-R4-0068）。
func adtyTeamTopupTradeNo(prefix string, teamID int64, key string) string {
	return prefix + common.Sha1([]byte(fmt.Sprintf("%d:%s", teamID, key)))[:20]
}

// adtyTeamTopupLegacyTradeNo 是 ISS-R4-0068 之前的单号规则：只有 key，没有团队。
// 仅用于认出旧规则下已经建过的单，让同一个 key 的重放仍然回到那张单。
func adtyTeamTopupLegacyTradeNo(prefix, key string) string {
	return prefix + common.Sha1([]byte(key))[:20]
}

var adtyTeamTopupKeyLocks sync.Map

type adtyTeamTopupRefLock struct {
	mu   sync.Mutex
	refs int
}

var adtyTeamTopupKeyLockGuard sync.Mutex

func lockAdtyTeamTopupKey(teamID int64, key string) func() {
	mapKey := fmt.Sprintf("%d:%s", teamID, strings.TrimSpace(key))
	adtyTeamTopupKeyLockGuard.Lock()
	var entry *adtyTeamTopupRefLock
	if v, ok := adtyTeamTopupKeyLocks.Load(mapKey); ok {
		entry = v.(*adtyTeamTopupRefLock)
	} else {
		entry = &adtyTeamTopupRefLock{}
		adtyTeamTopupKeyLocks.Store(mapKey, entry)
	}
	entry.refs++
	adtyTeamTopupKeyLockGuard.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		adtyTeamTopupKeyLockGuard.Lock()
		entry.refs--
		if entry.refs == 0 {
			adtyTeamTopupKeyLocks.Delete(mapKey)
		}
		adtyTeamTopupKeyLockGuard.Unlock()
	}
}

// adtyClaimTeamTopupReplay 认领同一个幂等键上一次下过的单，返回 (订单, 是否冲突)。
//
// 先按新规则（团队作用域）在全部支付商前缀（TTEP / TTU / TTEAM）下查：命中且是
// 同一笔单就是重放，不是同一笔就冲突——同一个 key 换了金额、付款方式或支付商都
// 必须拒绝（ISS-R1-0161）。
//
// 再按旧规则（只有 key）的单号查：那是升级前建的单，同团队且 sameOrder 成立算
// 重放，同团队但参数变了同样算冲突，跨团队则忽略。
func adtyClaimTeamTopupReplay(newTradeNo, legacyTradeNo string, teamID int64, sameOrder func(*model.AdtyTopupOrder) bool) (*model.AdtyTopupOrder, bool, error) {
	for _, tradeNo := range model.AdtyTeamTopupSiblingTradeNos(newTradeNo) {
		if tradeNo == "" {
			continue
		}
		var existing model.AdtyTopupOrder
		lookup := model.DB.Where("trade_no = ?", tradeNo).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return nil, false, lookup.Error
		}
		if lookup.RowsAffected != 1 {
			continue
		}
		if sameOrder(&existing) {
			return &existing, false, nil
		}
		return nil, true, nil
	}
	for _, tradeNo := range model.AdtyTeamTopupSiblingTradeNos(legacyTradeNo) {
		if tradeNo == "" {
			continue
		}
		var existing model.AdtyTopupOrder
		lookup := model.DB.Where("trade_no = ?", tradeNo).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return nil, false, lookup.Error
		}
		if lookup.RowsAffected != 1 {
			continue
		}
		if sameOrder(&existing) {
			return &existing, false, nil
		}
		if existing.TeamId == teamID {
			return nil, true, nil
		}
	}
	return nil, false, nil
}

// CreateAdtyTeamTopupOrder records a leader top-up with no payment-method gift.
// It does not credit the team until payment completion.
func CreateAdtyTeamTopupOrder(leaderID int, teamID int64, amountUSD decimal.Decimal, key string) (*model.AdtyTopupOrder, error) {
	key = strings.TrimSpace(key)
	if leaderID <= 0 || teamID <= 0 || !amountUSD.IsPositive() || key == "" {
		return nil, ErrAdtyTeamInvalid
	}
	if len(key) > adtyTeamTopupKeyMaxLen {
		return nil, ErrAdtyTeamIdempotencyKey
	}
	quota, err := common.WalletQuotaFromDecimalStrict(amountUSD.Mul(common.QuotaPerUnitDecimal()).Floor())
	if err != nil || quota <= 0 {
		return nil, ErrAdtyTeamInvalid
	}
	unlock := lockAdtyTeamTopupKey(teamID, key)
	defer unlock()
	tradeNo := adtyTeamTopupTradeNo("TTEAM", teamID, key)
	existing, conflict, err := adtyClaimTeamTopupReplay(tradeNo, adtyTeamTopupLegacyTradeNo("TTEAM", key), teamID, func(o *model.AdtyTopupOrder) bool {
		return o.TeamId == teamID && o.UserId == leaderID && o.FaceUSD.Equal(amountUSD) && o.Method == "team"
	})
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, ErrAdtyTeamInvalid
	}
	if existing != nil {
		return existing, nil
	}
	return createAdtyTeamTopupOrder(leaderID, teamID, amountUSD, int64(quota), tradeNo)
}

func createAdtyTeamTopupOrder(leaderID int, teamID int64, amountUSD decimal.Decimal, quota int64, tradeNo string) (*model.AdtyTopupOrder, error) {
	var created *model.AdtyTopupOrder
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var team model.AdtyTeam
		if err := model.AdtyLockForUpdate(tx).First(&team, teamID).Error; err != nil {
			return err
		}
		if team.Status != model.AdtyTeamStatusActive || team.LeaderUserId != leaderID {
			return ErrAdtyTeamForbidden
		}
		topUp := &model.TopUp{
			UserId: leaderID, Amount: amountUSD.IntPart(), Money: 0, TradeNo: tradeNo,
			PaymentMethod: "team", PaymentProvider: model.PaymentProviderAdtyEpay,
			CreateTime: common.GetTimestamp(), Status: common.TopUpStatusPending,
		}
		if err := tx.Create(topUp).Error; err != nil {
			return err
		}
		ext := &model.AdtyTopupOrder{
			TradeNo: tradeNo, UserId: leaderID, TeamId: teamID, SiteId: team.SiteId,
			Provider: model.PaymentProviderAdtyEpay, Method: "team",
			FaceUSD: amountUSD, BonusRate: decimal.Zero, BonusUSD: decimal.Zero,
			CreditQuota: quota, PayAmount: amountUSD, PayCurrency: "USD",
			TopupGroupRatio: decimal.NewFromInt(1),
		}
		if err := model.ValidateAdtyTeamTopupCreateTx(tx, ext); err != nil {
			if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
				return ErrAdtyTeamInvalid
			}
			return err
		}
		if err := tx.Create(ext).Error; err != nil {
			if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
				return ErrAdtyTeamInvalid
			}
			return err
		}
		created = ext
		return nil
	})
	return created, err
}

func ListAdtyTeamTopups(leaderID int, teamID int64, pendingOnly ...bool) ([]model.AdtyTopupOrder, error) {
	var team model.AdtyTeam
	if err := model.DB.First(&team, teamID).Error; err != nil {
		return nil, err
	}
	if team.LeaderUserId != leaderID {
		return nil, ErrAdtyTeamForbidden
	}
	var rows []model.AdtyTopupOrder
	query := model.DB.Model(&model.AdtyTopupOrder{}).Where("adty_topup_orders.team_id = ?", teamID)
	if len(pendingOnly) > 0 && pendingOnly[0] {
		query = query.Select("adty_topup_orders.*").Joins("JOIN top_ups t ON t.trade_no = adty_topup_orders.trade_no").Where("t.status = ? AND adty_topup_orders.user_id = ?", common.TopUpStatusPending, leaderID)
	}
	err := query.Order("adty_topup_orders.id desc").Limit(20).Find(&rows).Error
	return rows, err
}

func CreditAdtyTeam(leaderId, teamId int64, quota int64, ref string) error {
	if quota <= 0 || strings.TrimSpace(ref) == "" {
		return ErrAdtyTeamInvalid
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		if int64(team.LeaderUserId) != leaderId {
			return ErrAdtyTeamForbidden
		}
		return applyAdtyTeamQuotaTx(tx, team, quota, "credit", ref, int(leaderId))
	})
}

func SpendAdtyTeam(userId int, quota int64, ref string) error {
	if quota <= 0 || strings.TrimSpace(ref) == "" {
		return ErrAdtyTeamInvalid
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var membership model.AdtyTeamMember
		lookup := tx.Where("user_id = ?", userId).Order("id DESC").Limit(1).Find(&membership)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return ErrAdtyTeamForbidden
		}
		var team model.AdtyTeam
		if err := model.AdtyLockForUpdate(tx).First(&team, membership.TeamId).Error; err != nil {
			return err
		}
		if team.Status != model.AdtyTeamStatusActive {
			return ErrAdtyTeamClosed
		}
		if membership.Status != model.AdtyTeamMemberActive {
			return ErrAdtyTeamForbidden
		}
		if team.SharedQuota < quota {
			return ErrAdtyTeamFunds
		}
		return applyAdtyTeamQuotaTx(tx, &team, -quota, "spend", ref, userId)
	})
}

func CloseAdtyTeam(leaderId int, teamId int64) error {
	var siteID int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		if team.LeaderUserId != leaderId {
			return ErrAdtyTeamForbidden
		}
		siteID = team.SiteId
		return closeAdtyTeamTx(tx, team)
	})
	if err != nil {
		return err
	}
	writeAdtyTeamAudit(leaderId, siteID, "team", strconv.FormatInt(teamId, 10), "adty.team.close",
		map[string]any{"team_id": teamId, "status": model.AdtyTeamStatusActive},
		map[string]any{"team_id": teamId, "status": model.AdtyTeamStatusClosed, "result": model.AuditResultSuccess})
	return nil
}

func closeAdtyTeamTx(tx *gorm.DB, team *model.AdtyTeam) error {
	var members []model.AdtyTeamMember
	if err := model.AdtyLockForUpdate(tx).Where("team_id = ? AND status = ?", team.Id, model.AdtyTeamMemberActive).Find(&members).Error; err != nil {
		return err
	}
	for _, member := range members {
		if member.IndependentEmployee && member.Role != model.AdtyTeamRoleLeader {
			if err := disableIndependentTeamEmployeeUserTx(tx, member.UserId); err != nil {
				return err
			}
		}
	}
	if err := tx.Model(&model.AdtyTeamMember{}).Where("team_id = ? AND status = ?", team.Id, model.AdtyTeamMemberActive).
		Update("status", model.AdtyTeamMemberLeft).Error; err != nil {
		return err
	}
	if err := disableAdtyTeamKeysTx(tx, team.Id); err != nil {
		return err
	}
	return tx.Model(team).Updates(map[string]any{"status": model.AdtyTeamStatusClosed, "closed_at": time.Now().Unix()}).Error
}

func lockAdtyTeamUser(tx *gorm.DB, id int) (*model.User, error) {
	var user model.User
	if err := model.AdtyLockForUpdate(tx).First(&user, id).Error; err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled {
		return nil, ErrAdtyTeamForbidden
	}
	return &user, nil
}

func rejectTeamStaffIdentityTx(tx *gorm.DB, userId int) error {
	if userId <= 0 {
		return ErrAdtyTeamForbidden
	}
	if adtyTeamTableReadyFor(tx, &model.AdtySiteStaff{}) {
		var staffCount int64
		if err := tx.Model(&model.AdtySiteStaff{}).Where("user_id = ? AND status <> ?", userId, "deleted").Count(&staffCount).Error; err != nil {
			return err
		}
		if staffCount > 0 {
			return ErrAdtyTeamForbidden
		}
	}
	return nil
}

func rejectTeamStaffOrOwnerIdentityTx(tx *gorm.DB, userId int) error {
	if err := rejectTeamStaffIdentityTx(tx, userId); err != nil {
		return err
	}
	if adtyTeamTableReadyFor(tx, &model.AdtySite{}) {
		var owned int64
		if err := tx.Model(&model.AdtySite{}).Where("owner_user_id = ? AND status <> ?", userId, model.AdtySiteStatusClosed).Count(&owned).Error; err != nil {
			return err
		}
		if owned > 0 {
			return ErrAdtyTeamForbidden
		}
	}
	return nil
}

func rejectActiveTeamMembership(tx *gorm.DB, userId int, allowedTeamIds ...int64) error {
	var count int64
	if err := tx.Model(&model.AdtyTeamMember{}).Where("user_id = ? AND status = ?", userId, model.AdtyTeamMemberActive).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrAdtyTeamBusy
	}
	var allowed int64
	if len(allowedTeamIds) > 0 {
		allowed = allowedTeamIds[0]
	}
	var boundEmployee int64
	query := tx.Model(&model.AdtyTeamMember{}).Where("user_id = ? AND (independent_employee = ? OR status = ? OR deleted_at > 0)", userId, true, model.AdtyTeamMemberDeleted)
	if allowed > 0 {
		query = query.Where("team_id <> ?", allowed)
	}
	if err := query.Count(&boundEmployee).Error; err != nil {
		return err
	}
	if boundEmployee > 0 {
		return ErrAdtyTeamBusy
	}
	return nil
}

func mapAdtyTeamMemberWriteError(err error) error {
	if model.IsActiveTeamMembershipUniqueViolation(err) {
		return ErrAdtyTeamBusy
	}
	return err
}

func lockActiveTeam(tx *gorm.DB, teamId int64) (*model.AdtyTeam, error) {
	var team model.AdtyTeam
	if err := model.AdtyLockForUpdate(tx).First(&team, teamId).Error; err != nil {
		return nil, err
	}
	if team.Status != model.AdtyTeamStatusActive {
		return nil, ErrAdtyTeamClosed
	}
	return &team, nil
}

func canManageAdtyTeamInvites(team *model.AdtyTeam, actorId int) bool {
	return team != nil && team.LeaderUserId == actorId
}

func CreateAdtyTeamInvite(actorId int, teamId int64, ttl time.Duration) (*AdtyTeamInviteIssue, error) {
	if ttl <= 0 || ttl > adtyTeamInviteMaxTTL {
		return nil, ErrAdtyTeamInvalid
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	code := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(code))
	hash := hex.EncodeToString(sum[:])
	var invite model.AdtyTeamInvite
	var siteID int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		if !canManageAdtyTeamInvites(team, actorId) {
			return ErrAdtyTeamForbidden
		}
		invite = model.AdtyTeamInvite{
			TeamId: team.Id, SiteId: team.SiteId, Purpose: model.AdtyTeamEmployeeInvitePurpose, CodeHash: hash, CreatedBy: actorId, ExpiresAt: time.Now().Add(ttl).Unix(),
		}
		if err := tx.Create(&invite).Error; err != nil {
			return err
		}
		siteID = team.SiteId
		return nil
	})
	if err != nil {
		return nil, err
	}
	issued := &AdtyTeamInviteIssue{
		Id: invite.Id, Code: code, ExpiresAt: invite.ExpiresAt,
		CreatedBy: invite.CreatedBy, CreatedAt: invite.CreatedAt,
		Status: model.AdtyTeamInviteStatusOutstanding,
	}
	writeAdtyTeamInviteAudit(actorId, siteID, invite.Id, "adty.team.invite.create", nil, map[string]any{
		"invite_id": invite.Id, "team_id": invite.TeamId, "created_by": actorId,
		"expires_at": invite.ExpiresAt, "result": model.AuditResultSuccess,
	}, model.AuditResultSuccess)
	return issued, nil
}

// AcceptAdtyTeamInvite rejects every logged-in caller. Team membership is only
// created by RegisterAdtyTeamEmployee, which mints a dedicated employee account.
// This entrance must not consume the invite or attach an ordinary account.
func AcceptAdtyTeamInvite(userId int, code string) (*model.AdtyTeamMember, error) {
	if userId <= 0 || strings.TrimSpace(code) == "" {
		return nil, ErrAdtyTeamForbidden
	}
	return nil, ErrAdtyTeamForbidden
}

// Read the immutable team identity first, then lock team before invite, as revoke does.
func lockAdtyTeamInviteTx(tx *gorm.DB, hash string) (*model.AdtyTeam, *model.AdtyTeamInvite, error) {
	var candidate model.AdtyTeamInvite
	if err := tx.Where("code_hash = ?", hash).Limit(1).Find(&candidate).Error; err != nil {
		return nil, nil, err
	}
	if candidate.Id == 0 {
		return nil, nil, ErrAdtyTeamInvalid
	}
	team, err := lockActiveTeam(tx, candidate.TeamId)
	if err != nil {
		return nil, nil, err
	}
	var invite model.AdtyTeamInvite
	if err := model.AdtyLockForUpdate(tx).Where("id = ? AND team_id = ? AND code_hash = ?", candidate.Id, team.Id, hash).Limit(1).Find(&invite).Error; err != nil {
		return nil, nil, err
	}
	if invite.Id == 0 || invite.SiteId != team.SiteId || invite.Purpose != model.AdtyTeamEmployeeInvitePurpose || model.AdtyTeamInviteStatus(invite, time.Now().Unix()) != model.AdtyTeamInviteStatusOutstanding {
		return nil, nil, ErrAdtyTeamInvalid
	}
	return team, &invite, nil
}

func ListAdtyTeamInvites(actorId int, teamId int64) ([]AdtyTeamInviteView, error) {
	var team model.AdtyTeam
	if err := model.DB.First(&team, teamId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtyTeamForbidden
		}
		return nil, err
	}
	if !canManageAdtyTeamInvites(&team, actorId) {
		return nil, ErrAdtyTeamForbidden
	}
	var rows []model.AdtyTeamInvite
	if err := model.DB.Where("team_id = ?", team.Id).Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	out := make([]AdtyTeamInviteView, 0, len(rows))
	for _, row := range rows {
		out = append(out, adtyTeamInviteView(row, now))
	}
	return out, nil
}

func RevokeAdtyTeamInvite(actorId int, teamId, inviteId int64) (*AdtyTeamInviteView, error) {
	if inviteId <= 0 {
		return nil, ErrAdtyTeamInvalid
	}
	var view AdtyTeamInviteView
	var siteID int
	var createdBy int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		if !canManageAdtyTeamInvites(team, actorId) {
			return ErrAdtyTeamForbidden
		}
		var invite model.AdtyTeamInvite
		lookup := model.AdtyLockForUpdate(tx).Where("id = ? AND team_id = ?", inviteId, team.Id).Limit(1).Find(&invite)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return ErrAdtyTeamForbidden
		}
		if invite.UsedAt != 0 || invite.RevokedAt != 0 {
			return ErrAdtyTeamInvalid
		}
		now := time.Now().Unix()
		if err := tx.Model(&invite).Updates(map[string]any{"revoked_by": actorId, "revoked_at": now}).Error; err != nil {
			return err
		}
		invite.RevokedBy, invite.RevokedAt = actorId, now
		siteID = team.SiteId
		createdBy = invite.CreatedBy
		view = adtyTeamInviteView(invite, now)
		return nil
	})
	if err != nil {
		return nil, err
	}
	writeAdtyTeamInviteAudit(actorId, siteID, view.Id, "adty.team.invite.revoke", map[string]any{
		"invite_id": view.Id, "team_id": teamId, "status": model.AdtyTeamInviteStatusOutstanding,
	}, map[string]any{
		"invite_id": view.Id, "team_id": teamId, "created_by": createdBy,
		"revoked_by": actorId, "revoked_at": view.RevokedAt, "result": model.AuditResultSuccess,
		"status": model.AdtyTeamInviteStatusRevoked,
	}, model.AuditResultSuccess)
	return &view, nil
}

func adtyTeamInviteView(invite model.AdtyTeamInvite, now int64) AdtyTeamInviteView {
	return AdtyTeamInviteView{
		Id: invite.Id, TeamId: invite.TeamId, CreatedBy: invite.CreatedBy, CreatedAt: invite.CreatedAt,
		ExpiresAt: invite.ExpiresAt, UsedBy: invite.UsedBy, UsedAt: invite.UsedAt,
		RevokedBy: invite.RevokedBy, RevokedAt: invite.RevokedAt,
		Status: model.AdtyTeamInviteStatus(invite, now),
	}
}

func writeAdtyTeamInviteAudit(actorId, siteId int, inviteId int64, action string, before, after any, result string) {
	writeAdtyTeamObjectAudit(actorId, siteId, "team_invite", strconv.FormatInt(inviteId, 10), action, before, after, result)
}

func appendAdtyTeamAuditTx(tx *gorm.DB, actorID, siteID int, objectType, objectID, action string, before, after any) (model.AuditLog, error) {
	var actor model.User
	if err := tx.Select("id", "username", "role").First(&actor, actorID).Error; err != nil {
		return model.AuditLog{}, err
	}
	entry, err := model.PrepareOfficialAuditLog(nil, model.OfficialAuditWrite{
		UserId: actorID, Username: actor.Username, ActorRole: actor.Role, SiteID: &siteID,
		ObjectType: objectType, ObjectID: objectID, Action: action, Content: action,
		Category: model.AuditCategoryOperation, Before: before, After: after, Result: model.AuditResultSuccess, Success: true,
	})
	if err != nil {
		return model.AuditLog{}, err
	}
	if err := model.AppendAuditIntentTx(tx, entry); err != nil {
		return model.AuditLog{}, err
	}
	return entry, nil
}

func writeAdtyTeamAudit(actorId, siteId int, objectType, objectId, action string, before, after any) {
	writeAdtyTeamObjectAudit(actorId, siteId, objectType, objectId, action, before, after, model.AuditResultSuccess)
}

func writeAdtyTeamObjectAudit(actorId, siteId int, objectType, objectId, action string, before, after any, result string) {
	rec := model.OfficialAuditWrite{
		UserId: actorId, SiteID: model.OfficialAuditSiteIDPointer(siteId),
		ObjectType: objectType, ObjectID: objectId,
		Action: action, Category: model.AuditCategoryOperation, Content: action,
		Before: before, After: after, Result: result, Success: result == model.AuditResultSuccess,
	}
	if actorId > 0 {
		if user, err := model.GetUserById(actorId, false); err == nil && user != nil {
			rec.Username = user.Username
			rec.ActorRole = user.Role
		}
	}
	if err := model.WriteOfficialAuditLog(nil, rec); err != nil {
		common.SysLog(common.LogText("official team audit write failed (%s): %s", action, err.Error()))
	}
}

// BindAdtyTeamKey binds an enabled user token to the member's active team.
// It locks team and membership before token and binding row so concurrent
// member removal or team closure cannot leave a new active binding behind (ISS-R1-0052).
func BindAdtyTeamKey(userId, tokenId int, teamIds ...int64) error {
	if userId <= 0 || tokenId <= 0 {
		return ErrAdtyTeamInvalid
	}
	var targetTeamID int64
	if len(teamIds) > 0 {
		targetTeamID = teamIds[0]
	}
	var boundTeamID int64
	var siteID int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var candidate model.AdtyTeamMember
		lookup := tx.Where("user_id = ? AND status = ?", userId, model.AdtyTeamMemberActive).Limit(1).Find(&candidate)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return ErrAdtyTeamForbidden
		}
		if targetTeamID > 0 && candidate.TeamId != targetTeamID {
			return ErrAdtyTeamForbidden
		}
		var team model.AdtyTeam
		if err := model.AdtyLockForUpdate(tx).First(&team, candidate.TeamId).Error; err != nil {
			return err
		}
		if team.Status != model.AdtyTeamStatusActive {
			return ErrAdtyTeamClosed
		}
		var membership model.AdtyTeamMember
		memLookup := model.AdtyLockForUpdate(tx).
			Where("id = ? AND team_id = ? AND user_id = ? AND status = ?", candidate.Id, team.Id, userId, model.AdtyTeamMemberActive).
			Limit(1).Find(&membership)
		if memLookup.Error != nil {
			return memLookup.Error
		}
		if memLookup.RowsAffected == 0 {
			return ErrAdtyTeamForbidden
		}
		user, err := lockAdtyTeamUser(tx, userId)
		if err != nil {
			return err
		}
		if user.SiteId != team.SiteId || user.StaffSiteId != 0 {
			return ErrAdtyTeamForbidden
		}
		var token model.Token
		if err := model.AdtyLockForUpdate(tx).First(&token, tokenId).Error; err != nil {
			return err
		}
		if token.UserId != userId || token.Status != common.TokenStatusEnabled || token.OwnerSelfUseSiteId != 0 {
			return ErrAdtyTeamForbidden
		}
		boundTeamID = team.Id
		siteID = team.SiteId
		var bound []model.AdtyTeamKey
		lookupKey := model.AdtyLockForUpdate(tx).Where("token_id = ?", tokenId).Limit(1).Find(&bound)
		if lookupKey.Error != nil {
			return lookupKey.Error
		}
		if lookupKey.RowsAffected > 0 {
			return ErrAdtyTeamKeyBound
		}
		row := model.AdtyTeamKey{TokenId: tokenId, TeamId: team.Id, UserId: userId, Status: model.AdtyTeamMemberActive}
		if err := tx.Create(&row).Error; err != nil {
			// 同一个 Key 被并发绑定：另一事务刚插了行，这是可解释的业务拒绝，不是服务端故障。
			if model.IsAdtyUniqueViolation(err) {
				return ErrAdtyTeamKeyBound
			}
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	writeAdtyTeamAudit(userId, siteID, "team_key", strconv.Itoa(tokenId), "adty.team.key.bind", nil,
		map[string]any{"team_id": boundTeamID, "token_id": tokenId, "user_id": userId, "status": model.AdtyTeamMemberActive, "result": model.AuditResultSuccess})
	return nil
}

// InheritAdtyTeamsOnCloseTx 把关闭分站上仍活跃的团队迁入主站。
// team_id、负责人和成员关系保持不变；当前 site_id 改为主站 0，并在 source_site_id 仍为空时记下原分站。
// 已记录的 source_site_id 不会被重试改成 0 或另一个猜测值。已在主站且来源为空的历史团队不会被本函数回填；
// 来源已独立记录的历史团队由 model.BackfillAdtyTeamSourceSiteID 在迁移时补写。
// 关站迁入不会关闭团队；原团队 API Key 仍按迁移安全规则停用。
func InheritAdtyTeamsOnCloseTx(tx *gorm.DB, siteId int) error {
	if !adtyTeamTableReadyFor(tx, &model.AdtyTeam{}) {
		return nil
	}
	if siteId == 0 {
		return nil
	}
	var teams []model.AdtyTeam
	if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND status = ?", siteId, model.AdtyTeamStatusActive).Order("id").Find(&teams).Error; err != nil {
		return err
	}
	for _, team := range teams {
		if err := tx.Model(&model.AdtyTeamInvite{}).Where("team_id = ? AND used_at = 0 AND revoked_at = 0", team.Id).Update("revoked_at", time.Now().Unix()).Error; err != nil {
			return err
		}
		if err := disableAdtyTeamKeysTx(tx, team.Id); err != nil {
			return err
		}
		updates := map[string]any{"site_id": 0}
		// 只在来源尚未记录、且迁移前 site_id 就是正在关闭的分站时写入；已在主站（0）的不猜测来源。
		if team.SourceSiteId == 0 && team.SiteId == siteId {
			updates["source_site_id"] = team.SiteId
		}
		if err := tx.Model(&model.AdtyTeam{}).Where("id = ?", team.Id).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

func reserveAdtyTeamQuotaHook(tx *gorm.DB, teamId int64, userId, delta int, ref string) (bool, error) {
	if teamId <= 0 || delta <= 0 {
		return false, nil
	}
	var team model.AdtyTeam
	if err := model.AdtyLockForUpdate(tx).First(&team, teamId).Error; err != nil {
		return true, err
	}
	priorReserved, err := adtyTeamPriorReservedQuotaTx(tx, team.Id, ref)
	if err != nil {
		return true, err
	}
	if priorReserved == 0 {
		if err := validateNewAdtyTeamReserveTx(tx, &team, userId, ref); err != nil {
			return true, err
		}
	}
	if team.SharedQuota < int64(delta) {
		return false, model.ErrBillingWalletInsufficient
	}
	if err := applyAdtyTeamQuotaTx(tx, &team, -int64(delta), "reserve", ref, userId); err != nil {
		return true, err
	}
	return true, nil
}

func validateNewAdtyTeamReserveTx(tx *gorm.DB, team *model.AdtyTeam, userId int, ref string) error {
	if team == nil || team.Status != model.AdtyTeamStatusActive {
		return ErrAdtyTeamClosed
	}
	if adtyTeamTableReadyFor(tx, &model.AdtyTeamMember{}) {
		var member model.AdtyTeamMember
		lookup := tx.Where("team_id = ? AND user_id = ? AND status = ?", team.Id, userId, model.AdtyTeamMemberActive).Limit(1).Find(&member)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return ErrAdtyTeamForbidden
		}
	}
	if ref != "" && adtyTeamTableReadyFor(tx, &model.BillingOperation{}) {
		var op model.BillingOperation
		opLookup := tx.Select("operation_key", "token_id").Where("operation_key = ?", ref).Limit(1).Find(&op)
		if opLookup.Error != nil {
			return opLookup.Error
		}
		if opLookup.RowsAffected > 0 && op.TokenId > 0 {
			return validateAdtyTeamTokenActiveTx(tx, team.Id, userId, op.TokenId)
		}
	}
	if adtyTeamTableReadyFor(tx, &model.AdtyTeamKey{}) {
		var keys []model.AdtyTeamKey
		if err := tx.Where("team_id = ? AND user_id = ?", team.Id, userId).Find(&keys).Error; err != nil {
			return err
		}
		if len(keys) > 0 {
			for _, key := range keys {
				if key.Status == model.AdtyTeamMemberActive && validateAdtyTeamTokenActiveTx(tx, team.Id, userId, key.TokenId) == nil {
					return nil
				}
			}
			return ErrAdtyTeamForbidden
		}
	}
	return nil
}

func validateAdtyTeamTokenActiveTx(tx *gorm.DB, teamId int64, userId, tokenId int) error {
	if !adtyTeamTableReadyFor(tx, &model.AdtyTeamKey{}) {
		return nil
	}
	var key model.AdtyTeamKey
	lookup := tx.Where("token_id = ?", tokenId).Limit(1).Find(&key)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected == 0 || key.TeamId != teamId || key.UserId != userId || key.Status != model.AdtyTeamMemberActive {
		return ErrAdtyTeamForbidden
	}
	if adtyTeamTableReadyFor(tx, &model.Token{}) {
		var token model.Token
		tokLookup := tx.Select("id", "user_id", "status").Where("id = ?", tokenId).Limit(1).Find(&token)
		if tokLookup.Error != nil {
			return tokLookup.Error
		}
		if tokLookup.RowsAffected == 0 || token.UserId != userId || token.Status != common.TokenStatusEnabled {
			return ErrAdtyTeamForbidden
		}
	}
	return nil
}

// settleAdtyTeamQuotaHook 是 reserveAdtyTeamQuotaHook 的结算侧对称项（`ISS-R4-0017`）。
//
// 预扣已经把 base 从团队余额扣掉了；结算时的差额 delta 必须记在同一条团队账上：
// delta>0 补扣团队，delta<0 退回团队，并写一条 adty_team_ledgers。成员个人钱包
// 全程不参与——这正是原来「补扣扣错人、退还凭空多给」的修法。
//
// 只使用受理时冻结的 teamId，不看当前绑定、不看团队是否已关闭：这笔是原团队付的，
// 成员事后退出团队或团队被关闭都不能把差额改记到个人钱包，也不能让结算永远卡住。
// 团队余额不够补扣时扣到 0 为止，剩下的差额由调用方记 Shortfall。
func settleAdtyTeamQuotaHook(tx *gorm.DB, teamId int64, userId, delta int, ref string) (bool, int, error) {
	if teamId <= 0 {
		return false, 0, nil
	}
	if delta == 0 {
		return true, 0, nil
	}
	var team model.AdtyTeam
	if err := model.AdtyLockForUpdate(tx).First(&team, teamId).Error; err != nil {
		return true, 0, err
	}
	applied, kind := delta, "settle"
	if delta > 0 {
		if team.SharedQuota < int64(delta) {
			applied = int(team.SharedQuota)
		}
		if applied <= 0 {
			// 团队余额已见底：不写 0 额流水，差额整笔记 Shortfall。
			return true, 0, nil
		}
	} else {
		kind = "refund"
	}
	if err := applyAdtyTeamQuotaTx(tx, &team, -int64(applied), kind, ref, userId); err != nil {
		return true, 0, err
	}
	return true, applied, nil
}

// adtyTeamSharedQuotaForToken 返回团队 Key 当前可用的共享余额，不是团队 Key 时 ok=false。
// 任何曾绑定到团队的 Key（含已撤销、成员已移除、团队已关闭的 left 绑定）都属于团队 Key
// （ok=true），失效后返回可用额度 0，绝不回退扣减成员个人钱包（`ISS-R1-0047` / `ISS-R4-0017`）。
func adtyTeamSharedQuotaForToken(tokenId int) (quota int64, ok bool, err error) {
	if tokenId <= 0 || model.DB == nil || !adtyTeamTableReadyFor(model.DB, &model.AdtyTeamKey{}) {
		return 0, false, nil
	}
	var key model.AdtyTeamKey
	lookup := model.DB.Where("token_id = ?", tokenId).Limit(1).Find(&key)
	if lookup.Error != nil {
		return 0, false, lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return 0, false, nil
	}
	if key.Status != model.AdtyTeamMemberActive || key.TeamId <= 0 || key.UserId <= 0 {
		return 0, true, nil
	}
	if adtyTeamTableReadyFor(model.DB, &model.Token{}) {
		var token model.Token
		tokLookup := model.DB.Select("id", "user_id", "status").Where("id = ?", tokenId).Limit(1).Find(&token)
		if tokLookup.Error != nil {
			return 0, true, tokLookup.Error
		}
		if tokLookup.RowsAffected == 0 || token.UserId != key.UserId || token.Status != common.TokenStatusEnabled {
			return 0, true, nil
		}
	}
	if adtyTeamTableReadyFor(model.DB, &model.AdtyTeamMember{}) {
		var member model.AdtyTeamMember
		memLookup := model.DB.Where("team_id = ? AND user_id = ? AND status = ?", key.TeamId, key.UserId, model.AdtyTeamMemberActive).Limit(1).Find(&member)
		if memLookup.Error != nil {
			return 0, true, memLookup.Error
		}
		if memLookup.RowsAffected == 0 {
			return 0, true, nil
		}
	}
	var team model.AdtyTeam
	if err := model.DB.First(&team, key.TeamId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, true, nil
		}
		return 0, true, err
	}
	if team.Status != model.AdtyTeamStatusActive {
		return 0, true, nil
	}
	return team.SharedQuota, true, nil
}

func closeAdtyTeamForDeletedLeader(tx *gorm.DB, userId int) error {
	if !adtyTeamTableReadyFor(tx, &model.AdtyTeam{}) {
		return nil
	}
	var team model.AdtyTeam
	lookup := model.AdtyLockForUpdate(tx).Where("leader_user_id = ? AND status = ?", userId, model.AdtyTeamStatusActive).Limit(1).Find(&team)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return nil
	}
	return closeAdtyTeamTx(tx, &team)
}

func disableAdtyTeamMemberKeysTx(tx *gorm.DB, teamId int64, userId int) error {
	var keys []model.AdtyTeamKey
	if err := model.AdtyLockForUpdate(tx).Where("team_id = ? AND user_id = ? AND status = ?", teamId, userId, model.AdtyTeamMemberActive).Find(&keys).Error; err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	ids := make([]int, 0, len(keys))
	for _, key := range keys {
		ids = append(ids, key.TokenId)
	}
	var lockedTokens []model.Token
	if err := model.AdtyLockForUpdate(tx).Select("id").Where("id IN ?", ids).Find(&lockedTokens).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.Token{}).Where("id IN ?", ids).Update("status", common.TokenStatusDisabled).Error; err != nil {
		return err
	}
	return tx.Model(&model.AdtyTeamKey{}).Where("team_id = ? AND user_id = ?", teamId, userId).Update("status", model.AdtyTeamMemberLeft).Error
}

func disableAdtyTeamKeysTx(tx *gorm.DB, teamId int64) error {
	var keys []model.AdtyTeamKey
	if err := model.AdtyLockForUpdate(tx).Where("team_id = ? AND status = ?", teamId, model.AdtyTeamMemberActive).Find(&keys).Error; err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	ids := make([]int, 0, len(keys))
	for _, key := range keys {
		ids = append(ids, key.TokenId)
	}
	var lockedTokens []model.Token
	if err := model.AdtyLockForUpdate(tx).Select("id").Where("id IN ?", ids).Find(&lockedTokens).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.Token{}).Where("id IN ?", ids).Update("status", common.TokenStatusDisabled).Error; err != nil {
		return err
	}
	return tx.Model(&model.AdtyTeamKey{}).Where("team_id = ?", teamId).Update("status", model.AdtyTeamMemberLeft).Error
}

// joinAdtyTeamTx is closed. Ordinary accounts must not become team members.
func joinAdtyTeamTx(tx *gorm.DB, userId int, teamId int64) (*model.AdtyTeamMember, error) {
	if tx == nil || userId <= 0 || teamId <= 0 {
		return nil, ErrAdtyTeamForbidden
	}
	return nil, ErrAdtyTeamForbidden
}

func adtyTeamPriorReservedQuotaTx(tx *gorm.DB, teamId int64, ref string) (int64, error) {
	if tx == nil || teamId <= 0 || ref == "" || !adtyTeamTableReadyFor(tx, &model.AdtyTeamLedger{}) {
		return 0, nil
	}
	var rows []model.AdtyTeamLedger
	if err := tx.Select("delta_quota").Where("team_id = ? AND type = ? AND ref_id = ?", teamId, "reserve", ref).Find(&rows).Error; err != nil {
		return 0, err
	}
	var sum int64
	for _, row := range rows {
		if row.DeltaQuota < 0 {
			sum += -row.DeltaQuota
		}
	}
	return sum, nil
}

func applyAdtyTeamQuotaTx(tx *gorm.DB, team *model.AdtyTeam, delta int64, kind, ref string, actor int) error {
	before := team.SharedQuota
	after := before + delta
	if after < 0 {
		return ErrAdtyTeamFunds
	}
	discriminator := after
	if kind == "reserve" {
		priorReserved, err := adtyTeamPriorReservedQuotaTx(tx, team.Id, ref)
		if err != nil {
			return err
		}
		discriminator = priorReserved - delta
	}
	if err := model.ApplyAdtyTeamFundMovementTx(tx, team, kind, ref, delta); err != nil {
		return err
	}
	if err := tx.Model(team).Update("shared_quota", after).Error; err != nil {
		return err
	}
	team.SharedQuota = after
	return tx.Create(&model.AdtyTeamLedger{
		TeamId: team.Id, DeltaQuota: delta, BalanceBefore: before, BalanceAfter: after,
		Type: kind, RefId: ref, ActorUserId: actor, IdempotencyKey: adtyTeamLedgerKey(kind, ref, discriminator),
	}).Error
}

// adtyTeamLedgerKey 构造团队流水的幂等键（`ISS-R4-0067` / `ISS-R1-0051`）。
//
// `reserve` 以该 operation 累计预扣目标额度（cumulativeTarget）作为区分后缀：
// 同一 operation 的多次预扣递增拥有不同且稳定的事件身份，且不受并发退款或充值
// 改变团队总余额（BalanceAfter）的影响（`ISS-R1-0051`）。
//
// 其余 kind 保持 `kind:ref`：同一 ref 对应唯一业务事件，由唯一索引阻止重复入账。
func adtyTeamLedgerKey(kind, ref string, cumulativeTarget int64) string {
	if kind == "reserve" {
		return kind + ":" + ref + ":" + strconv.FormatInt(cumulativeTarget, 10)
	}
	return kind + ":" + ref
}
