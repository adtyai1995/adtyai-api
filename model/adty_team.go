package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	AdtyTeamStatusActive   = "active"
	AdtyTeamStatusClosed   = "closed"
	AdtyTeamRoleLeader     = "leader"
	AdtyTeamRoleMember     = "member"
	AdtyTeamMemberActive   = "active"
	AdtyTeamMemberLeft     = "left"
	AdtyTeamMemberDisabled = "disabled"
	AdtyTeamMemberDeleted  = "deleted"
)

// AdtyTeam is one shared-balance team on the main site or a subsite.
type AdtyTeam struct {
	CreateKey         *string         `json:"-" gorm:"uniqueIndex;type:varchar(64)"`
	CreateFingerprint string          `json:"-" gorm:"type:varchar(64)"`
	Id                int64           `json:"id"`
	SiteId            int             `json:"site_id" gorm:"index"`
	SourceSiteId      int             `json:"source_site_id" gorm:"index;not null;default:0;column:source_site_id"` // 关站迁入主站时记下原分站；0 表示不是迁入团队或历史未回填。历史主站团队仅在来源已独立记录时由 BackfillAdtyTeamSourceSiteID 补写
	LeaderUserId      int             `json:"leader_user_id" gorm:"index"`
	Name              string          `json:"name" gorm:"type:varchar(64);not null"`
	Discount          decimal.Decimal `json:"discount" gorm:"type:decimal(10,4);not null"`
	Status            string          `json:"status" gorm:"type:varchar(16);index;not null"`
	SharedQuota       int64           `json:"shared_quota"`
	CreatedAt         int64           `json:"created_at" gorm:"autoCreateTime"`
	ClosedAt          int64           `json:"closed_at"`
}

func (AdtyTeam) TableName() string { return "adty_teams" }

// AdtyTeamMember binds one user to one team. A second active membership is rejected.
// idx_adty_team_member uniquely binds (team_id, user_id). A partial unique index
// ux_adty_team_members_one_active_user additionally guarantees at most one active
// row per user across teams. Left history is outside that index.
type AdtyTeamMember struct {
	Id                  int64  `json:"id"`
	TeamId              int64  `json:"team_id" gorm:"uniqueIndex:idx_adty_team_member,priority:1"`
	UserId              int    `json:"user_id" gorm:"uniqueIndex:idx_adty_team_member,priority:2;index"`
	Role                string `json:"role" gorm:"type:varchar(16);not null"`
	Status              string `json:"status" gorm:"type:varchar(16);index;not null"`
	IndependentEmployee bool   `json:"independent_employee" gorm:"not null;default:false"`
	DisabledAuthVersion int64  `json:"-" gorm:"not null;default:0"`
	DeletedAt           int64  `json:"deleted_at,omitempty" gorm:"not null;default:0"`
	CreatedAt           int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyTeamMember) TableName() string { return "adty_team_members" }

// AdtyTeamLedger records shared-balance credits and spends.
type AdtyTeamLedger struct {
	Id             int64  `json:"id"`
	TeamId         int64  `json:"team_id" gorm:"index"`
	DeltaQuota     int64  `json:"delta_quota"`
	BalanceBefore  int64  `json:"balance_before"`
	BalanceAfter   int64  `json:"balance_after"`
	Type           string `json:"type" gorm:"type:varchar(32)"`
	RefId          string `json:"ref_id" gorm:"type:varchar(64)"`
	ActorUserId    int    `json:"actor_user_id"`
	IdempotencyKey string `json:"-" gorm:"uniqueIndex;type:varchar(128)"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyTeamLedger) TableName() string { return "adty_team_ledgers" }

const (
	AdtyTeamInviteStatusOutstanding = "outstanding"
	AdtyTeamInviteStatusExpired     = "expired"
	AdtyTeamInviteStatusUsed        = "used"
	AdtyTeamInviteStatusRevoked     = "revoked"
)

// AdtyTeamInvite is a one-time, expiring join credential. The plaintext is not stored.
const AdtyTeamEmployeeInvitePurpose = "employee_registration"

type AdtyTeamInvite struct {
	SiteId    int    `json:"site_id" gorm:"not null;default:0"`
	Purpose   string `json:"purpose" gorm:"type:varchar(32);not null;default:''"`
	Id        int64  `json:"id"`
	TeamId    int64  `json:"team_id" gorm:"index"`
	CodeHash  string `json:"-" gorm:"uniqueIndex;type:varchar(64);not null"`
	CreatedBy int    `json:"created_by"`
	ExpiresAt int64  `json:"expires_at" gorm:"index"`
	UsedBy    int    `json:"used_by"`
	UsedAt    int64  `json:"used_at"`
	RevokedBy int    `json:"revoked_by"`
	RevokedAt int64  `json:"revoked_at"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

// AdtyTeamInviteStatus returns outstanding, expired, used, or revoked.
// Revoked is preferred over expiry; a used invite stays used even if later marked revoked.
func AdtyTeamInviteStatus(invite AdtyTeamInvite, now int64) string {
	if invite.UsedAt != 0 {
		return AdtyTeamInviteStatusUsed
	}
	if invite.RevokedAt != 0 {
		return AdtyTeamInviteStatusRevoked
	}
	if invite.ExpiresAt < now {
		return AdtyTeamInviteStatusExpired
	}
	return AdtyTeamInviteStatusOutstanding
}

func (AdtyTeamInvite) TableName() string { return "adty_team_invites" }

const (
	AdtyTeamRegistrationStatusOutstanding = "outstanding"
	AdtyTeamRegistrationStatusExpired     = "expired"
	AdtyTeamRegistrationStatusUsed        = "used"
	AdtyTeamRegistrationStatusRevoked     = "revoked"
)

// AdtyTeamRegistrationLink is a one-time, expiring credential that creates a
// team and its unique leader. SiteId is fixed at issuance. The plaintext code
// is not stored.
type AdtyTeamRegistrationLink struct {
	CreateKey         *string `json:"-" gorm:"uniqueIndex;type:varchar(64)"`
	CreateFingerprint string  `json:"-" gorm:"type:varchar(64)"`
	Id                int64   `json:"id"`
	SiteId            int     `json:"site_id" gorm:"index;not null"`
	CodeHash  string `json:"-" gorm:"uniqueIndex;type:varchar(64);not null"`
	CreatedBy int    `json:"created_by"`
	ExpiresAt int64  `json:"expires_at" gorm:"index"`
	UsedBy    int    `json:"used_by"`
	UsedAt    int64  `json:"used_at"`
	TeamId    int64  `json:"team_id"`
	RevokedBy int    `json:"revoked_by"`
	RevokedAt int64  `json:"revoked_at"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

// AdtyTeamRegistrationStatus returns outstanding, expired, used, or revoked.
func AdtyTeamRegistrationStatus(link AdtyTeamRegistrationLink, now int64) string {
	if link.UsedAt != 0 {
		return AdtyTeamRegistrationStatusUsed
	}
	if link.RevokedAt != 0 {
		return AdtyTeamRegistrationStatusRevoked
	}
	if link.ExpiresAt < now {
		return AdtyTeamRegistrationStatusExpired
	}
	return AdtyTeamRegistrationStatusOutstanding
}

func (AdtyTeamRegistrationLink) TableName() string { return "adty_team_registration_links" }

// AdtyTeamKey binds an API token to the member and team that the server stored.
type AdtyTeamKey struct {
	Id        int64  `json:"id"`
	TokenId   int    `json:"token_id" gorm:"uniqueIndex"`
	TeamId    int64  `json:"team_id" gorm:"index"`
	UserId    int    `json:"user_id" gorm:"index"`
	Status    string `json:"status" gorm:"type:varchar(16);index"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyTeamKey) TableName() string { return "adty_team_keys" }

// AdtyTeamReserveHook spends a team key from the shared balance inside the billing transaction.
// teamId is the accepted operation's frozen payer, never a mutable token binding.
var AdtyTeamReserveHook func(tx *gorm.DB, teamId int64, userId, delta int, ref string) (handled bool, err error)

// AdtyTeamSettleHook 是 AdtyTeamReserveHook 的结算侧对称项（`ISS-R4-0017`）：
// 团队 Key 的结算差额记到团队共享余额，成员个人钱包不再参与。
//
//   - delta > 0：从团队余额补扣，applied 为团队真正付得起的那部分（余额见底时
//     小于 delta，差额由调用方记 Shortfall，绝不回落成员钱包）；
//   - delta < 0：把未用的预扣退回团队，applied = delta；
//   - delta == 0：不动账，也不写流水；
//   - handled 为 false 表示冻结的 teamId 无效，调用方拒绝结算，不回退个人钱包。
//
// 扣减在结算事务里完成，只使用持久付款快照，不重新读取 Key 绑定。
var AdtyTeamSettleHook func(tx *gorm.DB, teamId int64, userId, delta int, ref string) (handled bool, applied int, err error)

// AdtyTokenIsTeamKeyTx reports whether this token is bound to a team. A team key
// spends the shared team balance, so the member's personal wallet — and their
// principal lots — must not be charged for it.
//
// 判断只看绑定关系，不看团队余额：reserveAdtyTeamQuotaHook 在团队余额不足时
// 直接报错，而不是回落到个人钱包，所以「有绑定」等价于「这笔是团队付的」。
func AdtyTokenIsTeamKeyTx(tx *gorm.DB, tokenId int) (bool, error) {
	if tx == nil || tokenId <= 0 {
		return false, nil
	}
	hasTeamKeyTable, err := AdtyTableExists(tx, &AdtyTeamKey{})
	if err != nil {
		return false, err
	}
	if !hasTeamKeyTable {
		// 没有绑表就等于没有团队 Key，个人计费照旧。
		return false, nil
	}
	var count int64
	if err := tx.Model(&AdtyTeamKey{}).Where("token_id = ?", tokenId).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ValidateAdtyTeamTokenTx reads current lifecycle state even when token metadata
// came from a different node's cache. Historical bindings cannot become personal.
func ValidateAdtyTeamTokenTx(tx *gorm.DB, token *Token) error {
	hasTeamKeyTable, err := AdtyTableExists(tx, &AdtyTeamKey{})
	if err != nil {
		return err
	}
	if !hasTeamKeyTable {
		return nil
	}
	var key AdtyTeamKey
	if err := tx.Where("token_id = ?", token.Id).Limit(1).Find(&key).Error; err != nil {
		return err
	}
	if key.Id == 0 {
		return validateUnboundTeamEmployeeTx(tx, token.UserId)
	}
	if key.Status != AdtyTeamMemberActive || key.UserId != token.UserId || key.TeamId <= 0 {
		return ErrTokenInvalid
	}
	var count int64
	if err := tx.Model(&AdtyTeam{}).Where("id = ? AND status = ?", key.TeamId, AdtyTeamStatusActive).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrTokenInvalid
	}
	if err := tx.Model(&AdtyTeamMember{}).Where("team_id = ? AND user_id = ? AND status = ?", key.TeamId, token.UserId, AdtyTeamMemberActive).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrTokenInvalid
	}
	return nil
}

// Independent staff accounts retain their team identity after disablement or closure.
func independentTeamEmployeeTx(tx *gorm.DB, userID int) (*AdtyTeamMember, error) {
	hasMemberTable, err := AdtyTableExists(tx, &AdtyTeamMember{})
	if err != nil {
		return nil, err
	}
	if !hasMemberTable {
		return nil, nil
	}
	var member AdtyTeamMember
	result := tx.Where("user_id = ? AND independent_employee = ?", userID, true).Order("id desc").Limit(1).Find(&member)
	if result.Error != nil || result.RowsAffected == 0 {
		return nil, result.Error
	}
	return &member, nil
}

func validateUnboundTeamEmployeeTx(tx *gorm.DB, userID int) error {
	member, err := independentTeamEmployeeTx(tx, userID)
	if err != nil {
		return err
	}
	if member != nil {
		return ErrTokenInvalid
	}
	return nil
}

// Lifecycle mutations take team, membership, then user locks. Key issuance
// uses the same order before checking the per-user key count.
func lockAdtyTokenMembershipTx(tx *gorm.DB, token *Token) (*AdtyTeamMember, error) {
	member, err := independentTeamEmployeeTx(tx, token.UserId)
	if err != nil || member == nil {
		return member, err
	}
	var team AdtyTeam
	if err := lockForUpdate(tx).First(&team, member.TeamId).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).First(member, member.Id).Error; err != nil {
		return nil, err
	}
	if team.Status != AdtyTeamStatusActive || member.Status != AdtyTeamMemberActive || token.OwnerSelfUseSiteId != 0 {
		return nil, ErrTokenInvalid
	}
	return member, nil
}

func insertAdtyEmployeeTokenTx(tx *gorm.DB, token *Token, member *AdtyTeamMember) error {
	if err := tx.Create(token).Error; err != nil {
		return err
	}
	if member == nil {
		return nil
	}
	return tx.Create(&AdtyTeamKey{TokenId: token.Id, TeamId: member.TeamId, UserId: token.UserId, Status: AdtyTeamMemberActive}).Error
}

var adtyTeamTopupPrefixes = []string{"TTEP", "TTU", "TTEAM"}

// AdtyTeamTopupSiblingTradeNos returns all provider-prefix variants sharing the
// same team+key digest so one endpoint idempotency key cannot create separate
// orders across Epay, BEpusdt, and direct team top-up paths (ISS-R1-0161).
func AdtyTeamTopupSiblingTradeNos(tradeNo string) []string {
	tradeNo = strings.TrimSpace(tradeNo)
	for _, prefix := range adtyTeamTopupPrefixes {
		if strings.HasPrefix(tradeNo, prefix) {
			suffix := strings.TrimPrefix(tradeNo, prefix)
			if len(suffix) == 20 {
				return []string{"TTEP" + suffix, "TTU" + suffix, "TTEAM" + suffix}
			}
		}
	}
	if tradeNo == "" {
		return nil
	}
	return []string{tradeNo}
}

// ValidateAdtyTeamTopupCreateTx serializes new team checkout requests. Historical
// orders retain their original financial identity even after the team closes.
func ValidateAdtyTeamTopupCreateTx(tx *gorm.DB, ext *AdtyTopupOrder) error {
	if ext == nil || ext.TeamId <= 0 || tx == nil {
		return nil
	}
	hasTeamTable, err := AdtyTableExists(tx, &AdtyTeam{})
	if err != nil {
		return err
	}
	if hasTeamTable {
		var team AdtyTeam
		if err := AdtyLockForUpdate(tx).First(&team, ext.TeamId).Error; err != nil {
			return err
		}
		if team.Status != AdtyTeamStatusActive || team.LeaderUserId != ext.UserId {
			return common.NewMessage("No permission to perform this action")
		}
	}
	if siblings := AdtyTeamTopupSiblingTradeNos(ext.TradeNo); len(siblings) > 0 {
		var count int64
		if err := tx.Model(&AdtyTopupOrder{}).Where("trade_no IN ?", siblings).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrAdtyIdempotencyConflict
		}
	}
	return nil
}

// AdtyPreLockUserDeleteHook acquires AdtyTeam and AdtyTeamMember row locks
// BEFORE user deletion locks the users row, preventing AB-BA deadlocks (R5-008).
var AdtyPreLockUserDeleteHook func(tx *gorm.DB, userId int) error

// AdtyUserDeletePostEffects separates post-commit revocation effects produced
// by AdtyBeforeUserDeleteHook so independent employees disabled during leader
// team closure receive full session/auth revocation while ordinary members
// whose team keys were disabled only have their token caches invalidated.
type AdtyUserDeletePostEffects struct {
	DisabledEmployeeIDs []int
	KeyOwnerIDs         []int
	AuthzCleared        bool
	TicketAudits        []AuditLog
}

// AdtyBeforeUserDeleteHook closes a team when its leader is deleted and returns
// separated post-commit cache/session invalidation targets (R2-010 / R5-15).
var AdtyBeforeUserDeleteHook func(tx *gorm.DB, userId, actorId int, reason string) (AdtyUserDeletePostEffects, error)

// AdtyAfterUserDeleteHook delivers service-level post-commit effects (such as
// committed ticket release audits and Casbin policy reload) after user deletion.
var AdtyAfterUserDeleteHook func(deletedUserID int, effects AdtyUserDeletePostEffects)

// BackfillAdtyTeamSourceSiteID 为已迁入主站（site_id=0）且 source_site_id 仍为空的历史团队补写来源站（A.16.4 / G.7.2）。
// 只使用已核销的团队注册链接上独立记下的非零 SiteId 和 TeamId；同一团队若出现多个不同来源则视为不明确，不回填。
// 不从负责人和成员的 inherited_from_site_id、用户迁移映射或订单/工单上的客户站点推断来源，以免把原生主站团队误标成迁入团队。
// 已有非零 source_site_id 不覆盖；仍在分站（site_id!=0）或没有已知来源的主站团队保持 0。可重复执行。
func BackfillAdtyTeamSourceSiteID(db *gorm.DB) error {
	if db == nil {
		db = DB
	}
	if db == nil {
		return nil
	}
	hasTeamTable, err := AdtyTableExists(db, &AdtyTeam{})
	if err != nil {
		return err
	}
	if !hasTeamTable {
		return nil
	}
	hasSourceSiteCol, err := AdtyColumnExists(db, &AdtyTeam{}, "source_site_id")
	if err != nil {
		return err
	}
	if !hasSourceSiteCol {
		return nil
	}
	hasRegLinkTable, err := AdtyTableExists(db, &AdtyTeamRegistrationLink{})
	if err != nil {
		return err
	}
	if !hasRegLinkTable {
		return nil
	}

	var rows []struct {
		TeamId int64 `gorm:"column:team_id"`
		SiteId int   `gorm:"column:site_id"`
	}
	if err := db.Model(&AdtyTeamRegistrationLink{}).
		Select("team_id, MAX(site_id) AS site_id").
		Where("team_id > ? AND site_id > ? AND used_at <> ?", 0, 0, 0).
		Group("team_id").
		Having("COUNT(DISTINCT site_id) = ?", 1).
		Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			if row.TeamId <= 0 || row.SiteId <= 0 {
				continue
			}
			if err := tx.Model(&AdtyTeam{}).
				Where("id = ? AND site_id = ? AND source_site_id = ?", row.TeamId, 0, 0).
				Update("source_site_id", row.SiteId).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
