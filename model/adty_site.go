package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 站长分站（P11）的站点、开站邀请码和站点审计。只由 service/adty_site*.go 写。

// 站点状态和开站方式。
const (
	AdtySiteStatusPending  = "pending"
	AdtySiteStatusActive   = "active"
	AdtySiteStatusPaused   = "paused"
	AdtySiteStatusClosing  = "closing"
	AdtySiteStatusClosed   = "closed"
	AdtySiteStatusDeleting = "deleting"

	AdtySiteOpenedByApply  = "apply"
	AdtySiteOpenedByAdmin  = "admin"
	AdtySiteOpenedByInvite = "invite"
)

// AdtySite 是一个分站。一个用户同时最多一个没关闭的站：开站事务里先锁用户行再查，不靠唯一索引（关闭的站保留存档）。
// 子域名全局唯一，关站后保留，用来把老链接跳到客户的新归属。
type AdtySite struct {
	MaxMarkup                  *decimal.Decimal        `json:"-" gorm:"type:decimal(10,4);->"` // historical metadata
	TierMaxMarkup              *decimal.Decimal        `json:"-" gorm:"type:decimal(10,4);->"`
	MarketingPolicy            AdtySiteMarketingPolicy `json:"-" gorm:"embedded"`
	MarketingPolicyInitialized bool                    `json:"-"`
	Id                         int                     `json:"id"`
	OwnerUserId                int                     `json:"owner_user_id" gorm:"index"`
	OwnershipVersion           int64                   `json:"ownership_version" gorm:"not null;default:0"`
	Subdomain                  string                  `json:"subdomain" gorm:"uniqueIndex;type:varchar(63)"`
	RootDomain                 string                  `json:"root_domain" gorm:"type:varchar(253)"`
	Status                     string                  `json:"status" gorm:"type:varchar(16);index"`
	CostFactor                 *decimal.Decimal        `json:"cost_factor" gorm:"type:decimal(10,6)"` // 单站 d；空就用档位或全局
	// 平台提高 d 先记在这里，到点才写入 CostFactor。降低立即写 CostFactor 并清空这两列。
	PendingCostFactor   *decimal.Decimal `json:"pending_cost_factor" gorm:"type:decimal(10,6)"`
	PendingCostFactorAt int64            `json:"pending_cost_factor_at" gorm:"index"`
	TierCostFactor      *decimal.Decimal `json:"tier_cost_factor" gorm:"type:decimal(10,6)"`
	// 业绩档位算出的更高 d 先记在这里，到点才写入 TierCostFactor。降低立即写入并清空这两列。
	PendingTierCostFactor      *decimal.Decimal `json:"pending_tier_cost_factor" gorm:"type:decimal(10,6)"`
	PendingTierCostFactorAt    int64            `json:"pending_tier_cost_factor_at" gorm:"index"`
	TierMonth                  string           `json:"tier_month" gorm:"type:varchar(7)"`
	TierCode                   string           `json:"tier_code" gorm:"type:varchar(32);index"`
	TierMode                   string           `json:"tier_mode" gorm:"type:varchar(16)"`
	TierUpdatedAt              int64            `json:"tier_updated_at"`
	TierPrincipalAdjustmentUSD decimal.Decimal  `json:"tier_principal_adjustment_usd" gorm:"type:decimal(20,8);not null;default:0"`
	Visibility                 string           `json:"visibility" gorm:"type:text"`
	AllowedGroups              string           `json:"allowed_groups" gorm:"type:text"`
	Brand                      string           `json:"brand" gorm:"type:text"`
	HomePageContent            string           `json:"home_page_content" gorm:"type:text"`
	LegalDocs                  string           `json:"-" gorm:"type:text"`
	BonusRates                 string           `json:"bonus_rates" gorm:"type:text"`
	SeoIndex                   bool             `json:"seo_index"`
	ArchivedHiddenAt           int64            `json:"archived_hidden_at" gorm:"index;not null;default:0"`
	AgreementVer               string           `json:"agreement_ver" gorm:"type:varchar(32)"`
	AgreementAt                int64            `json:"agreement_at"`
	ContactInfo                string           `json:"contact_info" gorm:"type:text"`
	OpenedBy                   string           `json:"opened_by" gorm:"type:varchar(16)"`
	OpenKey                    *string          `json:"-" gorm:"uniqueIndex;type:varchar(64)"`
	OpenFingerprint            string           `json:"-" gorm:"type:varchar(64)"`
	ReviewNote                 string           `json:"review_note" gorm:"type:text"`
	// 关站：站长申请时写 CloseRequestedAt；管理员定下客户去向后状态改 closing，到 CloseExecuteAt 由主节点执行。
	CloseRequestedAt int64  `json:"close_requested_at"`
	CloseMailBatchId int64  `json:"close_mail_batch_id,omitempty" gorm:"-"`
	CloseExecuteAt   int64  `json:"close_execute_at" gorm:"index"`
	CloseTransferTo  int    `json:"close_transfer_to"` // 新关站只记 0（由主站继承）；历史已转到其他站的已关闭行可能仍非 0
	CloseClawBack    bool   `json:"close_claw_back"`
	CloseReason      string `json:"close_reason" gorm:"type:text"`
	ClosedAt         int64  `json:"closed_at"`
	// 日结算对不上消费日志时暂停正收益，等管理员核对后恢复。负收益照常结算。
	SettleHoldReason string `json:"settle_hold_reason" gorm:"type:text"`
	// 收益钩子写库失败的次数。只记数，不重试；日结算对账会拦住漏记的正收益。
	EarningHookFailures int64 `json:"earning_hook_failures"`
	CreatedAt           int64 `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt           int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtySite) TableName() string { return "adty_sites" }

// New sites configure their own policies. Upgraded rows are initialized once
// from the settings in force before the site-specific fields existed.
func (s *AdtySite) BeforeCreate(tx *gorm.DB) error {
	s.MarketingPolicyInitialized = true
	return nil
}

type AdtySiteMarketingPolicy struct {
	PendingCoverageCap       *decimal.Decimal `json:"pending_coverage_cap" gorm:"type:decimal(20,8)"`
	CancelWindowMinutes      *int64           `json:"cancel_window_minutes" gorm:"column:invite_cancel_window_minutes"`
	ObservationDays          *int64           `json:"observation_days"`
	CookieTTLDays            *int64           `json:"cookie_ttl_days" gorm:"column:invite_cookie_ttl_days"`
	SelfInviteWindowDays     *int64           `json:"self_invite_window_days" gorm:"column:invite_self_invite_window_days"`
	BatchRegisterThreshold   *int64           `json:"batch_register_threshold" gorm:"column:invite_batch_register_threshold"`
	BatchRegisterWindowHours *int64           `json:"batch_register_window_hours" gorm:"column:invite_batch_register_window_hours"`
	MinUSD                   *decimal.Decimal `json:"min_usd" gorm:"column:invite_withdraw_min;type:decimal(20,8)"`
	FeeUSD                   *decimal.Decimal `json:"fee_usd" gorm:"column:invite_withdraw_fee;type:decimal(20,8)"`
	CNYRate                  *decimal.Decimal `json:"cny_rate" gorm:"column:invite_withdraw_cny_rate;type:decimal(20,8)"`
	WithdrawMethods          *string          `json:"-" gorm:"column:invite_withdraw_methods;type:varchar(64)"`
	WithdrawUSDTNetworks     *string          `json:"-" gorm:"column:invite_withdraw_usdt_networks;type:varchar(128)"`
	TrialGrantMaxUSD         *decimal.Decimal `json:"trial_grant_max_usd" gorm:"type:decimal(20,8)"`
	TrialGrantDailyUSD       *decimal.Decimal `json:"trial_grant_daily_usd" gorm:"type:decimal(20,8)"`
}

const (
	AdtySiteAffiliateStatusApproved = "approved"
	AdtySiteAffiliateStatusDisabled = "disabled"
)

// AdtySiteReferralAffiliate stores per-affiliate controls on a subsite:
// individual commission rate override (0..0.30, nil = site default),
// disable/restore state, and settlement/withdrawal freeze flags.
type AdtySiteReferralAffiliate struct {
	Id                int64            `json:"id" gorm:"primaryKey"`
	SiteId            int              `json:"site_id" gorm:"uniqueIndex:ux_adty_site_referral_aff,priority:1;index"`
	UserId            int              `json:"user_id" gorm:"uniqueIndex:ux_adty_site_referral_aff,priority:2;index"`
	RateOverride      *decimal.Decimal `json:"rate_override" gorm:"type:decimal(10,6)"`
	Status            string           `json:"status" gorm:"type:varchar(24);not null;default:approved;index"`
	SettlementEnabled bool             `json:"settlement_enabled" gorm:"not null;default:true"`
	WithdrawalEnabled bool             `json:"withdrawal_enabled" gorm:"not null;default:true"`
	RiskReason        string           `json:"risk_reason" gorm:"type:varchar(64)"`
	RiskNote          string           `json:"risk_note" gorm:"type:text"`
	DisabledBy        int              `json:"disabled_by"`
	DisabledAt        int64            `json:"disabled_at"`
	CreatedAt         int64            `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt         int64            `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtySiteReferralAffiliate) TableName() string { return "adty_site_referral_affiliates" }

// AdtySiteReferralAuditLog records subsite referral administration actions
// (settings changes, affiliate adjustments, withdrawal reviews/payouts, invite binds).
type AdtySiteReferralAuditLog struct {
	Id           int64  `json:"id" gorm:"primaryKey"`
	SiteId       int    `json:"site_id" gorm:"index"`
	Action       string `json:"action" gorm:"type:varchar(64);index"`
	TargetUserId int    `json:"target_user_id" gorm:"index"`
	OperatorId   int    `json:"operator_id" gorm:"index"`
	Reason       string `json:"reason" gorm:"type:text"`
	Ip           string `json:"ip" gorm:"type:varchar(64)"`
	UserAgent    string `json:"user_agent" gorm:"type:text"`
	OldValue     string `json:"old_value" gorm:"type:text"`
	NewValue     string `json:"new_value" gorm:"type:text"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime;index"`
}

func (AdtySiteReferralAuditLog) TableName() string { return "adty_site_referral_audit_logs" }

func (a *AdtySiteReferralAuditLog) BeforeCreate(*gorm.DB) error {
	a.Reason = RedactAuditText(a.Reason)
	a.UserAgent = RedactAuditText(a.UserAgent)
	a.OldValue = RedactAuditText(a.OldValue)
	a.NewValue = RedactAuditText(a.NewValue)
	return nil
}

// AdtySiteInviteCode 是开站邀请码：有效就直接开成 active，并用码上预设的供货倍率。
type AdtySiteInviteCode struct {
	MaxMarkup  *decimal.Decimal `json:"-" gorm:"type:decimal(10,4);->"` // historical metadata
	Id         int              `json:"id"`
	Code       string           `json:"code" gorm:"uniqueIndex;type:varchar(32)"`
	MaxUses    int              `json:"max_uses"` // 0 不限
	UsedCount  int              `json:"used_count"`
	ExpireAt   int64            `json:"expire_at"` // 0 不过期
	CostFactor *decimal.Decimal `json:"cost_factor" gorm:"type:decimal(10,6)"`
	Note       string           `json:"note" gorm:"type:text"`
	CreatedBy  int              `json:"created_by"`
	CreatedAt  int64            `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtySiteInviteCode) TableName() string { return "adty_site_invite_codes" }

// AdtySiteAudit 是分站的操作记录：开站、审核、改 d 和 k、暂停、关站、客户转移。
type AdtySiteAudit struct {
	Id         int64  `json:"id"`
	SiteId     int    `json:"site_id" gorm:"index"`
	Action     string `json:"action" gorm:"type:varchar(32);index"`
	OperatorId int    `json:"operator_id" gorm:"index"`
	OldValue   string `json:"old_value" gorm:"type:text"`
	NewValue   string `json:"new_value" gorm:"type:text"`
	Reason     string `json:"reason" gorm:"type:text"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime;index"`
}

func (AdtySiteAudit) TableName() string { return "adty_site_audits" }

func (a *AdtySiteAudit) BeforeCreate(*gorm.DB) error {
	a.Reason = RedactAuditText(a.Reason)
	a.OldValue = RedactAuditText(a.OldValue)
	a.NewValue = RedactAuditText(a.NewValue)
	return nil
}

// 分站客户成员状态。关站继承到主站时归档原站成员关系，不删除行。
const (
	AdtySiteCustomerActive   = "active"
	AdtySiteCustomerArchived = "archived"
)

// AdtySiteCustomer 是分站客户，和 users.site_id 同步写（11.2 注册、11.1 关站继承）。
// 风控标记和复核在 11.5，条款勾选在 11.7。关站迁入主站时归档（保留原 site_id 和 user_id），不删除。
type AdtySiteCustomer struct {
	UserId              int     `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	SiteId              int     `json:"site_id" gorm:"index"`
	Status              string  `json:"status" gorm:"type:varchar(16);index;not null;default:active"`
	ArchivedAt          int64   `json:"archived_at"`
	RiskFlag            string  `json:"risk_flag" gorm:"type:varchar(16)"` // 空 | self_use | migration
	RiskReason          string  `json:"-" gorm:"type:text"`                // 只给后台看
	Reviewed            bool    `json:"reviewed"`
	EarningRiskReleased bool    `json:"-"` // only an explicit review converting skipped earnings releases old charges
	TermsVersion        string  `json:"terms_version" gorm:"type:varchar(32)"`
	TermsAt             int64   `json:"terms_at"`
	JoinedAt            int64   `json:"joined_at"`
	InviteCode          *string `json:"invite_code,omitempty" gorm:"uniqueIndex;type:varchar(24)"`
}

func (AdtySiteCustomer) TableName() string { return "adty_site_customers" }

func (c *AdtySiteCustomer) BeforeCreate(tx *gorm.DB) error {
	if c.Status == "" {
		c.Status = AdtySiteCustomerActive
	}
	return nil
}

// AdtyLiveSiteCustomer 只保留未归档的成员行，供运营查询使用；归档行仍留在表里供追溯。
func AdtyLiveSiteCustomer(db *gorm.DB) *gorm.DB {
	return db.Where("archived_at = 0 AND status <> ?", AdtySiteCustomerArchived)
}

// AdtyUserSiteMigration 是关站迁入主站的用户级迁移身份映射（A.16.3 / D.4）。
// 按 user_id + 原 site_id + 目标站（主站为 0）只记一次，重试保持幂等。Id 即 migration_id。
// 已迁入主站且用户行上已有 inherited_from_site_id 的历史账号会补发映射行；不猜测来源。
type AdtyUserSiteMigration struct {
	Id           int64 `json:"id" gorm:"primaryKey"`
	UserId       int   `json:"user_id" gorm:"uniqueIndex:ux_adty_user_site_migration;index"`
	SourceSiteId int   `json:"source_site_id" gorm:"uniqueIndex:ux_adty_user_site_migration"`
	TargetSiteId int   `json:"target_site_id" gorm:"uniqueIndex:ux_adty_user_site_migration"`
	CreatedAt    int64 `json:"created_at"`
}

func (AdtyUserSiteMigration) TableName() string { return "adty_user_site_migrations" }

// AdtySitePrice 是站长定价（11.4 / A.10）：同一站内按「客户 + 分组」取最具体的一条，
// 并在同一客户/分组层级上允许单模型独立加价。Model 为空是该层级对所有模型的默认；
// 非空则只覆盖那个模型。已有行的空模型保持全站/该层级默认。
type AdtySitePrice struct {
	Id            int             `json:"id"`
	SiteId        int             `json:"site_id" gorm:"uniqueIndex:idx_site_price_model"`
	UserId        int             `json:"user_id" gorm:"uniqueIndex:idx_site_price_model"`                                     // 0 = 全站默认
	Group         string          `json:"group" gorm:"uniqueIndex:idx_site_price_model;type:varchar(64)"`                      // 空 = 所有分组
	Model         string          `json:"model" gorm:"uniqueIndex:idx_site_price_model;type:varchar(191);not null;default:''"` // 空 = 所有模型的默认
	Markup        decimal.Decimal `json:"markup" gorm:"type:decimal(10,4);not null;default:1"`
	PendingMarkup decimal.Decimal `json:"pending_markup" gorm:"type:decimal(10,4);not null;default:0"` // 0 = 没有待生效的涨价
	PendingAt     int64           `json:"pending_at"`
	Version       int64           `json:"version"`
	UpdatedAt     int64           `json:"updated_at"`
}

func (AdtySitePrice) TableName() string { return "adty_site_prices" }

// AdtySitePriceScope 持久化每个定价作用域（site_id + user_id）的单调递增版本号，
// 即使该作用域下的分组或单模型加价行被全部删除，已提交的版本号也不会回退到 0（ISS-R1-0162）。
type AdtySitePriceScope struct {
	Id        int   `json:"id"`
	SiteId    int   `json:"site_id" gorm:"uniqueIndex:ux_site_price_scope"`
	UserId    int   `json:"user_id" gorm:"uniqueIndex:ux_site_price_scope"`
	Version   int64 `json:"version"`
	UpdatedAt int64 `json:"updated_at"`
}

func (AdtySitePriceScope) TableName() string { return "adty_site_price_scopes" }

// BackfillAdtySitePriceScopes preserves versions from pre-scope price rows.
// Repeated migration never lowers an existing scope version.
func BackfillAdtySitePriceScopes(db *gorm.DB) error {
	var scopes []AdtySitePriceScope
	if err := db.Model(&AdtySitePrice{}).Select("site_id, user_id, MAX(version) AS version").Group("site_id, user_id").Order("site_id, user_id").Scan(&scopes).Error; err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, scope := range scopes {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&scope).Error; err != nil {
				return err
			}
			if err := tx.Model(&AdtySitePriceScope{}).Where("site_id = ? AND user_id = ? AND version < ?", scope.SiteId, scope.UserId, scope.Version).Update("version", scope.Version).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// 站长收益日汇总的类型和状态（11.5）。
const (
	AdtySiteEarningUsage         = "usage"
	AdtySiteEarningRefund        = "refund"
	AdtySiteEarningBonus         = "bonus"
	AdtySiteEarningBonusReversal = "bonus_reversal"
	AdtySiteEarningTrial         = "trial"
	AdtySiteEarningSkipped       = "skipped"

	AdtySiteEarningPending   = "pending"
	AdtySiteEarningAvailable = "available"
	AdtySiteEarningVoid      = "void"

	AdtySiteRiskSelfUse   = "self_use"
	AdtySiteRiskMigration = "migration"
)

// AdtySiteEarningDaily 按「站、客户、日期、类型」汇总站长收益，一天一行（11.5）。
// 只用 OnConflict DoUpdate 累加，状态在结算时改成 available。结算后同一天、同一类型再有消费，
// 记进 Day 加 "+n" 的新行（见 service 的记账），不改已经结算的行。
type AdtySiteEarningDaily struct {
	Id              int64           `json:"id"`
	SiteId          int             `json:"site_id" gorm:"uniqueIndex:idx_site_earning_owner"`
	SiteOwnerUserId int             `json:"site_owner_user_id" gorm:"uniqueIndex:idx_site_earning_owner;not null;default:0"`
	UserId          int             `json:"user_id" gorm:"uniqueIndex:idx_site_earning_owner"`
	Day             string          `json:"day" gorm:"uniqueIndex:idx_site_earning_owner;type:varchar(16)"`
	Kind            string          `json:"kind" gorm:"uniqueIndex:idx_site_earning_owner;type:varchar(16)"`
	Quota           int64           `json:"quota"`
	Revenue         decimal.Decimal `json:"revenue" gorm:"type:decimal(20,8);not null;default:0"`
	Platform        decimal.Decimal `json:"platform" gorm:"type:decimal(20,8);not null;default:0"`
	Earning         decimal.Decimal `json:"earning" gorm:"type:decimal(20,8);not null;default:0"`
	Reason          string          `json:"reason" gorm:"type:varchar(16)"` // skipped 行的原因类别：self_use | migration
	Status          string          `json:"status" gorm:"type:varchar(16);index"`
	SettledAt       int64           `json:"settled_at"`
	UpdatedAt       int64           `json:"updated_at"`
}

func (AdtySiteEarningDaily) TableName() string { return "adty_site_earning_daily" }

// AdtySiteEarningFact keeps the occurrence time of each legacy aggregate update.
// Daily rows remain projections; a later update cannot move earlier money to another day.
type AdtySiteEarningFact struct {
	Id         int64           `gorm:"primaryKey"`
	SiteId     int             `gorm:"index"`
	DailyId    int64           `gorm:"index"`
	OccurredAt int64           `gorm:"index"`
	Revenue    decimal.Decimal `gorm:"type:decimal(20,8);not null"`
	Platform   decimal.Decimal `gorm:"type:decimal(20,8);not null"`
	Earning    decimal.Decimal `gorm:"type:decimal(20,8);not null"`
}

func (AdtySiteEarningFact) TableName() string { return "adty_site_earning_facts" }

// AdtySiteTrialGrant 是站长送给客户的试用额度，按幂等键去重，也用来算每天的总额（11.5）。
type AdtySiteTrialGrant struct {
	Id              int64           `json:"id"`
	SiteId          int             `json:"site_id" gorm:"index"`
	SiteOwnerUserId int             `json:"site_owner_user_id" gorm:"not null;default:0"`
	UserId          int             `json:"user_id" gorm:"index"`
	AmountUSD       decimal.Decimal `json:"amount_usd" gorm:"type:decimal(20,8);not null;default:0"`
	Quota           int64           `json:"quota"`
	Day             string          `json:"day" gorm:"type:varchar(10);index"`
	IdempotencyKey  string          `json:"-" gorm:"uniqueIndex;type:varchar(128)"`
	CreatedAt       int64           `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtySiteTrialGrant) TableName() string { return "adty_site_trial_grants" }

// 举报状态（11.6 站长举报客户，11.9 后台处理）。
const (
	AdtySiteReportPending   = "pending"
	AdtySiteReportResolved  = "resolved"
	AdtySiteReportDismissed = "dismissed"
)

// AdtySiteReport 是站长对自己站上客户的举报，进入主站分站管理的待处理。
type AdtySiteReport struct {
	Id         int    `json:"id"`
	SiteId     int    `json:"site_id" gorm:"index"`
	UserId     int    `json:"user_id" gorm:"index"`
	ReporterId int    `json:"reporter_id"`
	Reason     string `json:"reason" gorm:"type:text"`
	Status     string `json:"status" gorm:"type:varchar(16);index"`
	HandledBy  int    `json:"handled_by"`
	HandleNote string `json:"handle_note" gorm:"type:text"`
	HandledAt  int64  `json:"handled_at"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime;index"`
}

func (AdtySiteReport) TableName() string { return "adty_site_reports" }

// AdtySiteLookup 记站长每次精确查找客户（11.7）：限流按小时数这张表，同时就是审计。
type AdtySiteLookup struct {
	Id        int64  `json:"id"`
	SiteId    int    `json:"site_id" gorm:"index:idx_site_lookup_time,priority:1"`
	OwnerId   int    `json:"owner_id"`
	Query     string `json:"-" gorm:"type:varchar(255)"` // 只给后台审计看
	Found     bool   `json:"found"`
	CreatedAt int64  `json:"created_at" gorm:"index:idx_site_lookup_time,priority:2"`
}

func (AdtySiteLookup) TableName() string { return "adty_site_lookups" }

// AdtySiteLegalDocs stores a subsite's own multilingual legal documents (en, zh-CN)
// and refund policy visibility toggle. Subsites never fall back to main site legal docs.
type AdtySiteLegalDocs struct {
	UserAgreement       map[string]string `json:"user_agreement"`
	PrivacyPolicy       map[string]string `json:"privacy_policy"`
	RefundPolicy        map[string]string `json:"refund_policy"`
	RefundPolicyEnabled bool              `json:"refund_policy_enabled"`
}

// AdtySiteTables 是分站的表，迁移和测试共用。
func AdtySiteTables() []any {
	return []any{&AdtySite{}, &AdtySiteInviteCode{}, &AdtySiteAudit{}, &AdtySiteCustomer{}, &AdtyUserSiteMigration{}, &AdtySitePrice{}, &AdtySitePriceScope{}, &AdtyPriceRevision{}, &AdtySiteDomain{},
		&AdtySiteEarningDaily{}, &AdtySiteEarningLine{}, &AdtySiteEarningFact{}, &AdtySiteTrialGrant{}, &AdtySiteReport{}, &AdtySiteLookup{}, &AdtySiteSenderDomain{}, &AdtySenderDomainCleanup{},
		&AdtySiteTier{}, &AdtySiteTierEvaluation{}, &AdtySiteTierPrincipalAdjustment{}, &AdtySiteWallet{}, &AdtySiteWalletLedger{}, &AdtySiteWithdrawal{}, &AdtySitePayoutAttempt{}, &AdtySiteStaff{}, &AdtySiteOwnership{},
		&AdtySiteSettlementSheet{}, &AdtySettlementCoverageAdjustment{}, &AdtySettlementRevision{}, &AdtySettlementScan{}, &AdtySiteCloseMailBatch{}, &AdtySiteCloseMailRecipient{}, &AdtySiteCloseMailAttempt{}, &AdtyDomainCloudResource{},
		&AdtySiteReferralAffiliate{}, &AdtySiteReferralAuditLog{}, &AdtySiteMailConfig{}}
}

// AdtySetUsersSiteTx 把 fromSite 的客户全部改到 toSite（0 是主站），同事务撤销旧Key。
// 转到主站时同时写下 inherited_from_site_id，并记一条迁移身份映射；不合并、不覆盖同邮箱的主站已有账号，也不改 user_id。
// users.site_id 只由分站服务通过这里和 AdtySetUserSiteTx 改（UpdateWithTx 不会改它）。
// 客户的资金批次也要一起搬（ISS-R4-0047）：批次自带 site_id 之后，不搬就是
// 「客户在新站、本金挂旧站」，两边的本金负债同时算错。
func AdtySetUsersSiteTx(tx *gorm.DB, fromSite, toSite int) ([]int, error) {
	ids, _, err := AdtySetUsersSiteWithMovedPrincipalTx(tx, fromSite, toSite)
	return ids, err
}

// AdtySetUsersSiteWithMovedPrincipalTx migrates all users from fromSite to toSite,
// rewrites their fund batches' site_id, and returns both moved user IDs and the total
// AdtySetUsersSiteWithMovedPrincipalTx moves all users on fromSite to toSite and returns
// the moved user IDs and total non-legacy principal quota remaining that left fromSite (R3-015).
func AdtySetUsersSiteWithMovedPrincipalTx(tx *gorm.DB, fromSite, toSite int) ([]int, int64, error) {
	return AdtyCloseSiteUsersAndOwnerSelfUseTx(tx, fromSite, 0, toSite)
}

// AdtyCloseSiteUsersAndOwnerSelfUseTx migrates all customers on fromSite and, when
// closing to main site (toSite == 0 && ownerUserID > 0), also revokes the owner's
// self-use API keys for fromSite and migrates unencumbered owner self-use principal
// batches to site_id = 0 under strict canonical lock order (User ASC -> Token ASC).
func AdtyCloseSiteUsersAndOwnerSelfUseTx(tx *gorm.DB, fromSite, ownerUserID, toSite int) ([]int, int64, error) {
	if fromSite == toSite {
		return nil, 0, nil
	}
	migrateOwnerSelfUse := fromSite > 0 && toSite == 0 && ownerUserID > 0
	// R5-12 / R5-13: Preserve canonical AdtySite -> AdtyTeam -> AdtySiteStaff / AdtyTeamMember -> User
	// lock order before locking User rows so concurrent admin operations (PreLockScopedEmployeeRowsTx)
	// cannot AB-BA deadlock with site closure.
	if tx.Migrator().HasTable(&AdtySiteStaff{}) {
		var preLockedStaff []AdtySiteStaff
		if err := lockForUpdate(tx).Select("id").Where("site_id = ?", fromSite).Order("id ASC").Find(&preLockedStaff).Error; err != nil {
			return nil, 0, err
		}
	}
	if tx.Migrator().HasTable(&AdtyTeamMember{}) {
		siteCustomerIDs := tx.Model(&User{}).Select("id").Where("site_id = ? AND staff_site_id = 0", fromSite)
		var preLockedMembers []AdtyTeamMember
		if err := lockForUpdate(tx).Select("id").
			Where("independent_employee = ? AND status = ? AND user_id IN (?)", true, AdtyTeamMemberDisabled, siteCustomerIDs).
			Order("id ASC").Find(&preLockedMembers).Error; err != nil {
			return nil, 0, err
		}
	}
	// InsertWithLimit locks the owner too: a token created before this lock is
	// revoked with the migration; one created afterwards belongs to the new site.
	var users []User
	userQ := lockForUpdate(tx).Select("id", "site_id", "staff_site_id", "auth_version")
	if migrateOwnerSelfUse {
		userQ = userQ.Where("site_id = ? OR staff_site_id = ? OR id = ?", fromSite, fromSite, ownerUserID)
	} else {
		userQ = userQ.Where("site_id = ? OR staff_site_id = ?", fromSite, fromSite)
	}
	if err := userQ.Order("id").Find(&users).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]int, 0, len(users))
	staffIDs := make([]int, 0)
	prevAuthVersionByID := make(map[int]int64, len(users))
	ownerLocked := false
	for i := range users {
		prevAuthVersionByID[users[i].Id] = users[i].AuthVersion
		if users[i].StaffSiteId == fromSite {
			staffIDs = append(staffIDs, users[i].Id)
			continue
		}
		if users[i].SiteId == fromSite && users[i].StaffSiteId == 0 {
			ids = append(ids, users[i].Id)
		}
		if migrateOwnerSelfUse && users[i].Id == ownerUserID {
			ownerLocked = true
		}
	}
	if len(ids) == 0 && len(staffIDs) == 0 && !ownerLocked {
		return nil, 0, nil
	}
	tokenTargetIDs := make([]int, 0, len(ids)+len(staffIDs))
	tokenTargetIDs = append(tokenTargetIDs, ids...)
	tokenTargetIDs = append(tokenTargetIDs, staffIDs...)
	var tokens []Token
	tokenQ := lockForUpdate(tx).Select("id", commonKeyCol)
	switch {
	case len(tokenTargetIDs) > 0 && ownerLocked:
		tokenQ = tokenQ.Where("user_id IN ? OR (user_id = ? AND owner_self_use_site_id = ?)", tokenTargetIDs, ownerUserID, fromSite)
	case len(tokenTargetIDs) > 0:
		tokenQ = tokenQ.Where("user_id IN ?", tokenTargetIDs)
	default:
		tokenQ = tokenQ.Where("user_id = ? AND owner_self_use_site_id = ?", ownerUserID, fromSite)
	}
	if err := tokenQ.Order("id").Find(&tokens).Error; err != nil {
		return nil, 0, err
	}
	if len(tokens) > 0 {
		if err := invalidateTokensCache(tokens); err != nil {
			return nil, 0, err
		}
		tokenIDs := make([]int, len(tokens))
		for i := range tokens {
			tokenIDs[i] = tokens[i].Id
		}
		// Soft deletion keeps historical identifiers and quota facts, while normal
		// token edits cannot reactivate the original secret at the destination.
		if err := tx.Where("id IN ?", tokenIDs).Delete(&Token{}).Error; err != nil {
			return nil, 0, err
		}
	}
	// Subsite staff belong strictly to the closing subsite and must never migrate
	// to the main site as customers: disable their accounts, revoke PATs, and bump auth_version.
	if len(staffIDs) > 0 {
		for _, sid := range staffIDs {
			if _, err := IncrementUserAuthVersionWithTx(tx, sid); err != nil {
				return nil, 0, err
			}
		}
		if err := tx.Model(&User{}).Where("id IN ?", staffIDs).Updates(map[string]any{
			"status":                  common.UserStatusDisabled,
			"access_token":            nil,
			"access_token_created_at": nil,
		}).Error; err != nil {
			return nil, 0, err
		}
		if tx.Migrator().HasTable(&UserAccessToken{}) {
			if err := tx.Where("user_id IN ?", staffIDs).Delete(&UserAccessToken{}).Error; err != nil {
				return nil, 0, err
			}
		}
		if tx.Migrator().HasTable(&AdtySiteStaff{}) {
			if err := tx.Model(&AdtySiteStaff{}).Where("site_id = ? AND user_id IN ?", fromSite, staffIDs).
				Updates(map[string]any{"status": "disabled", "disabled_auth_version": 0}).Error; err != nil {
				return nil, 0, err
			}
		}
	}
	var movedPrincipal int64
	if len(ids) > 0 {
		// Site membership is part of authorization. Fence old user metadata and
		// browser sessions before the new membership can commit.
		for _, id := range ids {
			nextAuthVer, err := IncrementUserAuthVersionWithTx(tx, id)
			if err != nil {
				return nil, 0, err
			}
			if err := SyncStaffAndTeamDisabledAuthVersionTx(tx, id, prevAuthVersionByID[id], nextAuthVer); err != nil {
				return nil, 0, err
			}
		}
		updates := map[string]any{"site_id": toSite}
		if toSite == 0 {
			// Stamp the origin only when it is still unset so a later membership
			// write cannot rewrite a previous close-inherit mapping.
			updates["inherited_from_site_id"] = gorm.Expr(
				"CASE WHEN inherited_from_site_id = 0 THEN ? ELSE inherited_from_site_id END", fromSite)
		}
		if err := tx.Model(&User{}).Where("id IN ?", ids).Updates(updates).Error; err != nil {
			return nil, 0, err
		}
		if fromSite > 0 && tx.Migrator().HasTable(&AdtyFundBatch{}) {
			if err := tx.Model(&AdtyFundBatch{}).
				Select("COALESCE(SUM(remaining), 0)").
				Where("user_id IN ? AND site_id = ? AND kind = ? AND source <> ? AND remaining > 0",
					ids, fromSite, AdtyFundBatchKindPrincipal, adtyLegacyBatchSource).
				Scan(&movedPrincipal).Error; err != nil {
				return nil, 0, err
			}
		}
		if err := adtyMoveFundBatchesToSiteTx(tx, ids, fromSite, toSite); err != nil {
			return nil, 0, err
		}
		if toSite == 0 {
			if err := RecordAdtyUserSiteMigrationsTx(tx, ids, fromSite, toSite, time.Now().Unix()); err != nil {
				return nil, 0, err
			}
		}
	}
	if ownerLocked && tx.Migrator().HasTable(&AdtyFundBatch{}) {
		var ownerBatches []AdtyFundBatch
		if err := lockForUpdate(tx).
			Where("user_id = ? AND site_id = ? AND kind = ?", ownerUserID, fromSite, AdtyFundBatchKindPrincipal).
			Order("id ASC").
			Find(&ownerBatches).Error; err != nil {
			return nil, 0, err
		}
		if len(ownerBatches) > 0 {
			encumbered := map[int64]bool{}
			// R10-019: Exclude ALL batches with any active allocation (in-flight API
			// precharges as well as owner-refund holds) AND any batch whose source
			// top-up still has a non-final manual refund (submitted/approved/reclaimed).
			if tx.Migrator().HasTable(&AdtyFundAllocation{}) {
				var heldBatchIDs []int64
				if err := tx.Model(&AdtyFundAllocation{}).
					Distinct("batch_id").
					Where("user_id = ? AND held > settled", ownerUserID).
					Pluck("batch_id", &heldBatchIDs).Error; err != nil {
					return nil, 0, err
				}
				for _, bid := range heldBatchIDs {
					encumbered[bid] = true
				}
			}
			encumberedSources := map[string]bool{}
			if tx.Migrator().HasTable(&AdtyManualRefund{}) {
				var refundSources []string
				if err := tx.Model(&AdtyManualRefund{}).
					Distinct("trade_no").
					Where("user_id = ? AND site_id = ? AND status NOT IN ?", ownerUserID, fromSite, []string{AdtyManualRefundStatusCompleted, AdtyManualRefundStatusTerminated}).
					Pluck("trade_no", &refundSources).Error; err != nil {
					return nil, 0, err
				}
				for _, src := range refundSources {
					encumberedSources[src] = true
				}
			}
			moveBatchIDs := make([]int64, 0, len(ownerBatches))
			for _, batch := range ownerBatches {
				if encumbered[batch.Id] || encumberedSources[batch.Source] {
					continue
				}
				moveBatchIDs = append(moveBatchIDs, batch.Id)
				if batch.Remaining > 0 && batch.Source != adtyLegacyBatchSource {
					movedPrincipal += batch.Remaining
				}
			}
			if len(moveBatchIDs) > 0 {
				if err := tx.Model(&AdtyFundBatch{}).
					Where("id IN ?", moveBatchIDs).
					Update("site_id", toSite).Error; err != nil {
					return nil, 0, err
				}
			}
		}
	}
	return ids, movedPrincipal, nil
}

// RecordAdtyUserSiteMigrationsTx 为迁入主站的账号记下 user_id、原站和目标站。已有同样键的行保持原样（幂等）。
func RecordAdtyUserSiteMigrationsTx(tx *gorm.DB, userIds []int, sourceSite, targetSite int, now int64) error {
	if len(userIds) == 0 || sourceSite == targetSite {
		return nil
	}
	rows := make([]AdtyUserSiteMigration, len(userIds))
	for i, userId := range userIds {
		rows[i] = AdtyUserSiteMigration{
			UserId:       userId,
			SourceSiteId: sourceSite,
			TargetSiteId: targetSite,
			CreatedAt:    now,
		}
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "source_site_id"}, {Name: "target_site_id"}},
		DoNothing: true,
	}).Create(&rows).Error
}

// BackfillAdtyUserSiteMigrationsFromInheritedSiteID 为已迁入主站、且用户行上已记下
// inherited_from_site_id 的历史账号补发映射行和 migration_id（A.16.3 / D.4）。
// 来源必须已经记在用户行上，不静默猜测。已有同样键的行保持原样（幂等）。
func BackfillAdtyUserSiteMigrationsFromInheritedSiteID(db *gorm.DB, now int64) error {
	if db == nil {
		db = DB
	}
	if db == nil {
		return nil
	}
	if !db.Migrator().HasTable(&User{}) || !db.Migrator().HasColumn(&User{}, "inherited_from_site_id") {
		return nil
	}
	if !db.Migrator().HasTable(&AdtyUserSiteMigration{}) {
		return nil
	}
	var users []User
	if err := db.Select("id", "inherited_from_site_id").
		Where("site_id = ? AND inherited_from_site_id <> ?", 0, 0).
		Order("id").
		Find(&users).Error; err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	bySource := make(map[int][]int)
	for i := range users {
		source := users[i].InheritedFromSiteId
		bySource[source] = append(bySource[source], users[i].Id)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for source, ids := range bySource {
			if err := RecordAdtyUserSiteMigrationsTx(tx, ids, source, 0, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ArchiveAdtySiteCustomersOnCloseTx 归档原站成员关系，保留原来的 site_id 和 user_id。已归档的行不会被改回或删掉。
func ArchiveAdtySiteCustomersOnCloseTx(tx *gorm.DB, userIds []int, siteId int, now int64) error {
	if len(userIds) == 0 {
		return nil
	}
	return tx.Model(&AdtySiteCustomer{}).
		Where("user_id IN ? AND site_id = ? AND archived_at = 0 AND status <> ?", userIds, siteId, AdtySiteCustomerArchived).
		Updates(map[string]any{"status": AdtySiteCustomerArchived, "archived_at": now}).Error
}

// AdtySetUserSiteTx 设置一个用户的所属分站（注册时用）。
func AdtySetUserSiteTx(tx *gorm.DB, userId, siteId int) error {
	return tx.Model(&User{}).Where("id = ?", userId).Update("site_id", siteId).Error
}

// AdtyInvalidateUserCaches 在改了 site_id 的事务提交后清这些用户的缓存，下次读取从库里重建。
func AdtyInvalidateUserCaches(ids []int) {
	for _, id := range ids {
		if err := InvalidateUserTokensCache(id); err != nil {
			common.SysError(common.LogText("invalidate token cache after site migration user=%d: %s", id, err.Error()))
		}
		if err := invalidateUserCache(id); err != nil {
			common.SysError(common.LogText("invalidate user cache failed user=%d: %s", id, err.Error()))
		}
		if err := PublishUserAuthCache(id); err != nil {
			common.SysError(common.LogText("publish user cache after site migration user=%d: %s", id, err.Error()))
		}
	}
}

// AdtyOnConsumeLog 由分站注册（任务 11.4、11.5），RecordConsumeLog 在消费日志开关之前调用：
// 把这次请求的分站倍率写进 params.Other 的 admin_info，并给站长记收益。
var AdtyOnConsumeLog func(c *gin.Context, userId int, params *RecordConsumeLogParams)

// AdtyOnTaskBillingLog 由分站注册（任务 11.5），RecordTaskBillingLog 在消费日志开关之前调用。
var AdtyOnTaskBillingLog func(params *RecordTaskBillingLogParams)

// AdtyGroupColumn 是带引号的 group 列名（保留字），service 包写原生 Select 时用。
func AdtyGroupColumn() string {
	return commonGroupCol
}
