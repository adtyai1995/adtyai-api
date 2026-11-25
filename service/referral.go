package service

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 推广返佣（P6）。从旧项目移植并按方案改写：注册即推广员、只按充值面额计佣、全部美元结算、
// 去掉订阅和兑换码计佣、人民币结算和申请审批。金额列沿用 float64，计算用 decimal（执行层决定 19）。

var (
	ErrReferralDisabled        = errors.New("referral disabled")
	ErrReferralCodeInvalid     = errors.New("referral code invalid")
	ErrReferralNotAffiliate    = errors.New("not an affiliate")
	ErrReferralAlreadyBound    = errors.New("invitee already bound")
	ErrReferralSelfOrCycle     = errors.New("self or cyclic invite")
	ErrReferralInsufficient    = errors.New("insufficient available commission")
	ErrReferralWithdrawBlocked = errors.New("withdrawal disabled for this affiliate")
	// ErrReferralWithdrawUnavailable：这种去向要的最低提现额或手续费还没在后台设置（约定 I）。
	ErrReferralWithdrawUnavailable = errors.New("withdrawal method not configured")
	ErrReferralSiteCustomer        = common.NewMessage("Reseller site customers cannot join the referral program")
)

// ReferralService 收拢推广返佣的业务逻辑。
type ReferralService struct{}

// Referral 是全局实例。
var Referral = &ReferralService{}

func referralSetting() *adty_setting.ReferralSetting { return adty_setting.GetReferralSetting() }

// IsEnabled 说明推广返佣是否开启。
func (s *ReferralService) IsEnabled() bool { return referralSetting().Enabled }

func moneyDec(value float64) decimal.Decimal { return decimal.NewFromFloat(value).Round(8) }
func moneyF(value decimal.Decimal) float64 {
	f, _ := value.Round(8).Float64()
	return f
}
func normalizeReferralMoney(value decimal.Decimal) decimal.Decimal {
	return value.Round(8)
}

// ValidateReferralRate 校验通用返佣比例（0–100 百分数）。
func ValidateReferralRate(rate decimal.Decimal) error {
	if rate.LessThan(decimal.Zero) || rate.GreaterThan(decimal.NewFromInt(100)) {
		return common.NewMessage("The commission rate must be between 0 and 100")
	}
	return nil
}

// referralSecret 是 cookie 签名和指纹哈希的密钥：CryptoSecret，没有就用 SessionSecret。
// 部署必须固定 SESSION_SECRET 和 CRYPTO_SECRET，否则重启后 cookie 失效、指纹对不上。
func referralSecret() string {
	if secret := strings.TrimSpace(common.CryptoSecret); secret != "" {
		return secret
	}
	return strings.TrimSpace(common.SessionSecret)
}

// HashReferralRiskValue 用 HMAC-SHA256 哈希 IP、设备、UA，库里只存哈希。
func HashReferralRiskValue(value string) string {
	value = strings.TrimSpace(value)
	secret := referralSecret()
	if value == "" || secret == "" {
		return ""
	}
	return common.HmacSha256(value, secret)
}

// BuildReferralCookie 生成签名 cookie：base64(码.签发时间.签名)。
func BuildReferralCookie(code string, issuedAt time.Time) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	secret := referralSecret()
	if code == "" || secret == "" {
		return "", errors.New("cannot sign referral cookie")
	}
	payload := fmt.Sprintf("%s.%d", code, issuedAt.Unix())
	return base64.StdEncoding.EncodeToString([]byte(payload + "." + common.HmacSha256(payload, secret))), nil
}

// ParseReferralCookie 验签并按当前的 cookie_ttl_days 判断过期，返回邀请码。
func ParseReferralCookie(raw string, now time.Time) (string, error) {
	secret := referralSecret()
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || secret == "" {
		return "", ErrReferralCodeInvalid
	}
	parts := strings.Split(string(decoded), ".")
	if len(parts) != 3 {
		return "", ErrReferralCodeInvalid
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(common.HmacSha256(payload, secret)), []byte(parts[2])) {
		return "", ErrReferralCodeInvalid
	}
	issued, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", ErrReferralCodeInvalid
	}
	issuedAt := time.Unix(issued, 0)
	ttl := max(referralSetting().CookieTTLDays, 1)
	if issuedAt.After(now.Add(5*time.Minute)) || issuedAt.Add(time.Duration(ttl)*24*time.Hour).Before(now) {
		return "", ErrReferralCodeInvalid
	}
	return strings.ToUpper(parts[0]), nil
}

const inviteCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newInviteCode() (string, error) {
	var b strings.Builder
	limit := big.NewInt(int64(len(inviteCodeAlphabet)))
	for range 8 {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		b.WriteByte(inviteCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// EnsureAffiliate 给用户建推广员（已批准，8 位邀请码，用默认比例）；已有就直接返回。
func (s *ReferralService) EnsureAffiliate(userId int) (*model.ReferralAffiliate, error) {
	var users []model.User
	if err := model.DB.Select("id", "site_id", "role").Where("id = ?", userId).Limit(1).Find(&users).Error; err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, common.NewMessage("User not found")
	}
	if users[0].Role >= common.RoleAdminUser {
		return nil, common.NewMessage("Administrators cannot use user referral endpoints")
	}
	if users[0].SiteId > 0 {
		return nil, ErrReferralSiteCustomer
	}
	if isEmp, empErr := AdtyIsIndependentTeamEmployee(userId); empErr != nil {
		return nil, empErr
	} else if isEmp {
		return nil, common.NewMessage("Team employee accounts cannot participate in referral programs")
	}
	var existing []model.ReferralAffiliate
	if err := model.DB.Where("user_id = ?", userId).Limit(1).Find(&existing).Error; err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return &existing[0], nil
	}
	var created *model.ReferralAffiliate
	for range 5 {
		code, err := newInviteCode()
		if err != nil {
			return nil, err
		}
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			affiliate := &model.ReferralAffiliate{
				UserId: userId, InviteCode: code, Status: model.ReferralAffiliateStatusApproved, SourceType: "register",
				AcquisitionEnabled: true, SettlementEnabled: true, WithdrawalEnabled: true, ApprovedAt: time.Now().Unix(),
			}
			if err := tx.Create(affiliate).Error; err != nil {
				return err
			}
			if err := tx.Create(&model.ReferralCommissionAccount{AffiliateId: affiliate.Id, UserId: userId, SettlementCurrency: "USD"}).Error; err != nil {
				return err
			}
			created = affiliate
			return nil
		})
		if err == nil {
			return created, nil
		}
		if !isAdtyUniqueConflictError(err) {
			return nil, err
		}
		// 并发时另一个请求可能已经建好，或者邀请码撞了，再查一次。
		if findErr := model.DB.Where("user_id = ?", userId).Limit(1).Find(&existing).Error; findErr == nil && len(existing) > 0 {
			return &existing[0], nil
		}
	}
	return nil, errors.New("could not create affiliate")
}

// AffiliateByCode 取一个有效的推广员（未停用、允许拉新）。
func (s *ReferralService) AffiliateByCode(code string) (*model.ReferralAffiliate, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, ErrReferralCodeInvalid
	}
	var rows []model.ReferralAffiliate
	if err := model.DB.Where("invite_code = ?", code).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	// 开了分站的推广员不再拉新（执行层决定 27）：链接照常能打开，但点击和绑定都当作码无效。
	if len(rows) == 0 || rows[0].Status != model.ReferralAffiliateStatusApproved || !rows[0].AcquisitionEnabled || rows[0].SiteOwnerSince > 0 {
		return nil, ErrReferralCodeInvalid
	}
	var owners []model.User
	if err := model.DB.Select("id", "site_id", "status", "role").Where("id = ?", rows[0].UserId).Limit(1).Find(&owners).Error; err != nil {
		return nil, err
	}
	if len(owners) == 0 || owners[0].SiteId > 0 || owners[0].Status != common.UserStatusEnabled || owners[0].Role >= common.RoleAdminUser {
		return nil, ErrReferralCodeInvalid
	}
	return &rows[0], nil
}

// RecordClick 记一次点击（IP 和 UA 只存哈希），返回推广员。
func (s *ReferralService) RecordClick(code, landingPath, referer, ip, userAgent string) (*model.ReferralAffiliate, error) {
	if !s.IsEnabled() {
		return nil, ErrReferralDisabled
	}
	affiliate, err := s.AffiliateByCode(code)
	if err != nil {
		return nil, err
	}
	click := &model.ReferralClick{
		AffiliateId: affiliate.Id, InviteCode: affiliate.InviteCode, Referer: truncateText(referer, 512),
		LandingPath: truncateText(landingPath, 512), IpHash: HashReferralRiskValue(ip), UserAgentHash: HashReferralRiskValue(userAgent),
	}
	if err := model.DB.Create(click).Error; err != nil {
		return nil, err
	}
	return affiliate, nil
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	// R3-33: Truncate strictly on UTF-8 rune boundaries so PostgreSQL never sees
	// an invalid split multi-byte sequence.
	cut := 0
	for cut < len(value) {
		_, size := utf8.DecodeRuneInString(value[cut:])
		if size <= 0 || cut+size > limit {
			break
		}
		cut += size
	}
	return value[:cut]
}

// BindInvitee 注册后绑定邀请关系。拒绝自己邀请自己和循环邀请；绑定之后不能改。
func (s *ReferralService) BindInvitee(inviteeId int, code, ipHash string) (*model.ReferralBinding, error) {
	affiliate, err := s.AffiliateByCode(code)
	if err != nil {
		return nil, err
	}
	if affiliate.UserId == inviteeId {
		return nil, ErrReferralSelfOrCycle
	}
	var cycle int64
	if err := model.DB.Model(&model.ReferralBinding{}).Where("invitee_user_id = ? AND inviter_user_id = ?", affiliate.UserId, inviteeId).Count(&cycle).Error; err != nil {
		return nil, err
	}
	if cycle > 0 {
		return nil, ErrReferralSelfOrCycle
	}
	binding := &model.ReferralBinding{
		InviteeUserId: inviteeId, InviterUserId: affiliate.UserId, AffiliateId: affiliate.Id,
		BindSource: "cookie", BindCode: affiliate.InviteCode, BindIpHash: ipHash, BoundAt: time.Now().Unix(),
	}
	if err := model.DB.Create(binding).Error; err != nil {
		var existing int64
		if model.DB.Model(&model.ReferralBinding{}).Where("invitee_user_id = ?", inviteeId).Count(&existing); existing > 0 {
			return nil, ErrReferralAlreadyBound
		}
		return nil, err
	}
	return binding, nil
}

// CheckReferralAdminTargetUserTx locks the target user row BEFORE any referral
// affiliate/commission/withdrawal/account locks (canonical User -> Affiliate ->
// Commission -> Withdrawal -> Account order, R3-08) and enforces role hierarchy,
// anti-self-operation, and PAT admin-target restrictions (R11-004).
func CheckReferralAdminTargetUserTx(tx *gorm.DB, targetUserID, adminID, actorRole int, denyAdminTarget bool) error {
	if tx == nil || (actorRole <= 0 && !denyAdminTarget) {
		return nil
	}
	var target model.User
	if err := model.AdtyLockForUpdate(tx).Select("id", "role").First(&target, targetUserID).Error; err != nil {
		return err
	}
	if adminID > 0 && adminID == target.Id {
		return model.ErrUserQuotaPermission
	}
	if actorRole > 0 && actorRole != common.RoleRootUser && actorRole <= target.Role {
		return model.ErrUserQuotaPermission
	}
	if denyAdminTarget && target.Role >= common.RoleAdminUser {
		return model.ErrAdminTargetSessionRequired
	}
	return nil
}

func recordReferralAuditTx(tx *gorm.DB, action string, targetUserId, affiliateId, adminId int, reason, ip, ua string, oldValue, newValue any) error {
	return tx.Create(&model.ReferralAdminAuditLog{
		Action: action, TargetUserId: targetUserId, AffiliateId: affiliateId, AdminUserId: adminId,
		Reason: truncateText(reason, 1000), Ip: ip, UserAgent: truncateText(ua, 512),
		OldValue: common.GetJsonString(oldValue), NewValue: common.GetJsonString(newValue),
	}).Error
}

// AffiliateRate 是推广员当前的比例（百分数）：单设了比例（合作伙伴或后台调整）就用它，否则用默认比例。
// 默认比例没设时 ok 为 false，这时不计佣。
func AffiliateRate(affiliate *model.ReferralAffiliate) (rate decimal.Decimal, ok bool) {
	if affiliate.RateOverride != nil {
		return decimal.NewFromFloat(*affiliate.RateOverride), true
	}
	if def := referralSetting().DefaultRate; def != nil {
		return decimal.NewFromFloat(*def), true
	}
	return decimal.Zero, false
}

// lockReferralAccountTx 锁佣金账户行。
func lockReferralAccountTx(tx *gorm.DB, affiliateId int) (*model.ReferralCommissionAccount, error) {
	var account model.ReferralCommissionAccount
	if err := model.AdtyLockForUpdate(tx).Where("affiliate_id = ?", affiliateId).First(&account).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

// addReferralLedgerTx 写一条返佣流水，同一个外部引用只写一次（先查后写）。
func addReferralLedgerTx(tx *gorm.DB, entry *model.ReferralCommissionLedger) error {
	if entry.ExternalRefId != "" {
		var count int64
		if err := tx.Model(&model.ReferralCommissionLedger{}).Where("external_ref_id = ?", entry.ExternalRefId).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
	} else {
		entry.ExternalRefId = fmt.Sprintf("ledger-%d-%d", entry.AffiliateId, time.Now().UnixNano())
	}
	if entry.SettlementCurrency == "" {
		entry.SettlementCurrency = "USD"
	}
	return tx.Create(entry).Error
}

// ReferralCommissionAccountAmounts 是佣金账户四个余额的 decimal 权威读数。
type ReferralCommissionAccountAmounts struct {
	PendingAmount   decimal.Decimal `gorm:"column:pending_amount"`
	AvailableAmount decimal.Decimal `gorm:"column:available_amount"`
	FrozenAmount    decimal.Decimal `gorm:"column:frozen_amount"`
	WithdrawnAmount decimal.Decimal `gorm:"column:withdrawn_amount"`
}

// ReferralCommissionLedgerDeltas 是佣金流水四个增量的 decimal 权威读数。
type ReferralCommissionLedgerDeltas struct {
	DeltaPending   decimal.Decimal `gorm:"column:delta_pending"`
	DeltaAvailable decimal.Decimal `gorm:"column:delta_available"`
	DeltaFrozen    decimal.Decimal `gorm:"column:delta_frozen"`
	DeltaWithdrawn decimal.Decimal `gorm:"column:delta_withdrawn"`
}

// GetReferralAccountAmountsTx 直接按 decimal 扫描佣金账户余额，避免 float64 扫描丢位。
func GetReferralAccountAmountsTx(tx *gorm.DB, affiliateId int) (ReferralCommissionAccountAmounts, error) {
	var amounts ReferralCommissionAccountAmounts
	if tx == nil {
		tx = model.DB
	}
	if err := tx.Model(&model.ReferralCommissionAccount{}).
		Select("pending_amount", "available_amount", "frozen_amount", "withdrawn_amount").
		Where("affiliate_id = ?", affiliateId).
		Take(&amounts).Error; err != nil {
		return ReferralCommissionAccountAmounts{}, err
	}
	amounts.PendingAmount = normalizeReferralMoney(amounts.PendingAmount)
	amounts.AvailableAmount = normalizeReferralMoney(amounts.AvailableAmount)
	amounts.FrozenAmount = normalizeReferralMoney(amounts.FrozenAmount)
	amounts.WithdrawnAmount = normalizeReferralMoney(amounts.WithdrawnAmount)
	return amounts, nil
}

// GetReferralLedgerDeltasTx 直接按 decimal 扫描佣金流水增量，避免 float64 扫描丢位。
func GetReferralLedgerDeltasTx(tx *gorm.DB, ledgerId int) (ReferralCommissionLedgerDeltas, error) {
	var deltas ReferralCommissionLedgerDeltas
	if tx == nil {
		tx = model.DB
	}
	if err := tx.Model(&model.ReferralCommissionLedger{}).
		Select("delta_pending", "delta_available", "delta_frozen", "delta_withdrawn").
		Where("id = ?", ledgerId).
		Take(&deltas).Error; err != nil {
		return ReferralCommissionLedgerDeltas{}, err
	}
	deltas.DeltaPending = normalizeReferralMoney(deltas.DeltaPending)
	deltas.DeltaAvailable = normalizeReferralMoney(deltas.DeltaAvailable)
	deltas.DeltaFrozen = normalizeReferralMoney(deltas.DeltaFrozen)
	deltas.DeltaWithdrawn = normalizeReferralMoney(deltas.DeltaWithdrawn)
	return deltas, nil
}

// GetReferralCommissionAmountTx 直接按 decimal 扫描单笔佣金金额，避免 float64 扫描丢位。
func GetReferralCommissionAmountTx(tx *gorm.DB, commissionId int) (decimal.Decimal, error) {
	var row struct {
		CommissionAmount decimal.Decimal `gorm:"column:commission_amount"`
	}
	if tx == nil {
		tx = model.DB
	}
	if err := tx.Model(&model.ReferralCommission{}).
		Select("commission_amount").
		Where("id = ?", commissionId).
		Take(&row).Error; err != nil {
		return decimal.Zero, err
	}
	return normalizeReferralMoney(row.CommissionAmount), nil
}

// AdjustAccountTx 按增量改佣金账户四个余额（可以为负，扣回用），并写流水。
func AdjustAccountTx(tx *gorm.DB, account *model.ReferralCommissionAccount, pending, available, frozen, withdrawn decimal.Decimal, entry model.ReferralCommissionLedger) error {
	pendingDec := normalizeReferralMoney(pending)
	availableDec := normalizeReferralMoney(available)
	frozenDec := normalizeReferralMoney(frozen)
	withdrawnDec := normalizeReferralMoney(withdrawn)
	if err := tx.Model(&model.ReferralCommissionAccount{}).Where("id = ?", account.Id).Updates(map[string]any{
		"pending_amount":   gorm.Expr("pending_amount + ?", pendingDec),
		"available_amount": gorm.Expr("available_amount + ?", availableDec),
		"frozen_amount":    gorm.Expr("frozen_amount + ?", frozenDec),
		"withdrawn_amount": gorm.Expr("withdrawn_amount + ?", withdrawnDec),
	}).Error; err != nil {
		return err
	}
	if entry.CommissionId > 0 && pendingDec.IsPositive() && availableDec.IsZero() && frozenDec.IsZero() && withdrawnDec.IsZero() {
		if err := tx.Model(&model.ReferralCommission{}).Where("id = ?", entry.CommissionId).Update("commission_amount", pendingDec).Error; err != nil {
			return err
		}
	}
	entry.AffiliateId = account.AffiliateId
	entry.UserId = account.UserId
	entry.DeltaPending = moneyF(pendingDec)
	entry.DeltaAvailable = moneyF(availableDec)
	entry.DeltaFrozen = moneyF(frozenDec)
	entry.DeltaWithdrawn = moneyF(withdrawnDec)
	if err := addReferralLedgerTx(tx, &entry); err != nil {
		return err
	}
	if entry.Id > 0 {
		if err := tx.Model(&model.ReferralCommissionLedger{}).Where("id = ?", entry.Id).Updates(map[string]any{
			"delta_pending":   pendingDec,
			"delta_available": availableDec,
			"delta_frozen":    frozenDec,
			"delta_withdrawn": withdrawnDec,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}
