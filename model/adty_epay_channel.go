package model

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ErrAdtyEpayDailyLimit means this order would push the method over its daily cap.
var ErrAdtyEpayDailyLimit = errors.New("epay daily limit reached")

// 易支付多通道（任务 5.1）。

const (
	AdtyEpayStatusEnabled     = "enabled"
	AdtyEpayStatusMaintenance = "maintenance"
	AdtyEpayStatusDisabled    = "disabled"
	AdtyEpayStatusRevoked     = "revoked"

	AdtyEpayReasonManual = "manual"
	AdtyEpayReasonUnpaid = "unpaid"
	AdtyEpayReasonProbe  = "probe"

	AdtyEpayOrderModeAPI    = "api"
	AdtyEpayOrderModeSubmit = "submit"

	AdtyEpaySignTypeMD5 = "MD5"
	AdtyEpaySignTypeRSA = "RSA"

	adtyEpayMerchantKeyPurpose        = "adty_epay_channel:merchant_key"
	adtyEpayMerchantPrivateKeyPurpose = "adty_epay_channel:merchant_private_key"
)

var ErrAdtyEpayChannelNotFound = errors.New("adty epay channel not found")

// AdtyEpayChannel 是一个易支付商户。商户密钥与商户私钥不序列化给前端。
type AdtyEpayChannel struct {
	Id                   int                     `json:"id"`
	Name                 string                  `json:"name" gorm:"type:varchar(128)"`
	GatewayURL           string                  `json:"gateway_url" gorm:"column:gateway_url;type:varchar(512)"`
	MerchantId           string                  `json:"merchant_id" gorm:"type:varchar(128)"`
	MerchantKey          string                  `json:"-" gorm:"column:merchant_key;type:text"`
	SignType             string                  `json:"sign_type" gorm:"column:sign_type;type:varchar(16);not null;default:'MD5'"`
	MerchantPrivateKey   string                  `json:"-" gorm:"column:merchant_private_key;type:text"`
	PlatformPublicKey    string                  `json:"platform_public_key" gorm:"column:platform_public_key;type:text"`
	Priority             int                     `json:"priority"`
	Weight               int                     `json:"weight"`
	OrderMode            string                  `json:"order_mode" gorm:"type:varchar(16)"`
	ProbeMode            string                  `json:"probe_mode" gorm:"type:varchar(16)"`
	Status               string                  `json:"status" gorm:"type:varchar(16);index"`
	StatusReason         string                  `json:"status_reason" gorm:"type:varchar(16)"`
	UnpaidStreak         int                     `json:"unpaid_streak"`
	UnpaidThreshold      int                     `json:"unpaid_threshold"`
	ProbeFailCount       int                     `json:"probe_fail_count"`
	ProbeOkCount         int                     `json:"probe_ok_count"`
	LastProbeAt          int64                   `json:"last_probe_at"`
	CreatedAt            int64                   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt            int64                   `json:"updated_at" gorm:"autoUpdateTime"`
	Methods              []AdtyEpayChannelMethod `json:"methods" gorm:"foreignKey:ChannelId"`
	CredentialDecryptErr bool                    `json:"credential_decrypt_error,omitempty" gorm:"-"`
}

// EffectiveSignType returns the canonical signature type ("MD5" or "RSA"),
// treating empty values as "MD5" for backward compatibility.
func (channel *AdtyEpayChannel) EffectiveSignType() string {
	if channel != nil && strings.EqualFold(strings.TrimSpace(channel.SignType), AdtyEpaySignTypeRSA) {
		return AdtyEpaySignTypeRSA
	}
	return AdtyEpaySignTypeMD5
}

// IsRSA reports whether this channel uses Epay v2 RSA signing.
func (channel *AdtyEpayChannel) IsRSA() bool {
	return channel.EffectiveSignType() == AdtyEpaySignTypeRSA
}

// HasCredentials reports whether the signing credentials required by SignType are present.
func (channel *AdtyEpayChannel) HasCredentials() bool {
	if channel == nil || channel.CredentialDecryptErr {
		return false
	}
	if channel.IsRSA() {
		return strings.TrimSpace(channel.MerchantPrivateKey) != "" &&
			strings.TrimSpace(channel.PlatformPublicKey) != ""
	}
	return strings.TrimSpace(channel.MerchantKey) != ""
}

func (AdtyEpayChannel) TableName() string {
	return "adty_epay_channels"
}

// AfterFind transparently decrypts sealed payment credentials while remaining
// backward-compatible with legacy plaintext rows until they are next saved.
// Decryption failure isolates only the affected channel (failing closed via
// CredentialDecryptErr + blank in-memory keys) so healthy channels remain
// available and Root can re-key, revoke, or delete the broken row via API.
func (channel *AdtyEpayChannel) AfterFind(_ *gorm.DB) error {
	if channel == nil {
		return nil
	}
	isRSA := channel.IsRSA()
	if strings.HasPrefix(channel.MerchantKey, adtySecretPrefix) {
		plain, err := OpenAdtyPurposeSecret(adtyEpayMerchantKeyPurpose, channel.MerchantKey)
		if err != nil {
			channel.MerchantKey = ""
			if !isRSA {
				channel.CredentialDecryptErr = true
			}
			common.SysError(common.LogText("adty epay channel %d merchant_key decrypt failed: %s", channel.Id, err.Error()))
		} else {
			channel.MerchantKey = plain
		}
	}
	if strings.HasPrefix(channel.MerchantPrivateKey, adtySecretPrefix) {
		plain, err := OpenAdtyPurposeSecret(adtyEpayMerchantPrivateKeyPurpose, channel.MerchantPrivateKey)
		if err != nil {
			channel.MerchantPrivateKey = ""
			if isRSA {
				channel.CredentialDecryptErr = true
			}
			common.SysError(common.LogText("adty epay channel %d merchant_private_key decrypt failed: %s", channel.Id, err.Error()))
		} else {
			channel.MerchantPrivateKey = plain
		}
	}
	return nil
}

func sealAdtyEpayChannelCredentials(channel *AdtyEpayChannel, omitKey, omitPrivateKey bool) error {
	if channel == nil {
		return nil
	}
	if !omitKey && channel.MerchantKey != "" {
		sealed, err := SealAdtyPurposeSecret(adtyEpayMerchantKeyPurpose, channel.MerchantKey)
		if err != nil {
			return err
		}
		channel.MerchantKey = sealed
	}
	if !omitPrivateKey && channel.MerchantPrivateKey != "" {
		sealed, err := SealAdtyPurposeSecret(adtyEpayMerchantPrivateKeyPurpose, channel.MerchantPrivateKey)
		if err != nil {
			return err
		}
		channel.MerchantPrivateKey = sealed
	}
	return nil
}

// AdtyEpayChannelMethod 是一个通道上的一种支付方式和它的限额。限额 0 表示不限。
type AdtyEpayChannelMethod struct {
	Id            int             `json:"id"`
	ChannelId     int             `json:"channel_id" gorm:"index"`
	Method        string          `json:"method" gorm:"type:varchar(16)"`
	TypeCode      string          `json:"type_code" gorm:"type:varchar(32)"`
	MinCNY        decimal.Decimal `json:"min_cny" gorm:"column:min_cny;type:decimal(20,2);not null;default:0"`
	MaxCNY        decimal.Decimal `json:"max_cny" gorm:"column:max_cny;type:decimal(20,2);not null;default:0"`
	DailyLimitCNY decimal.Decimal `json:"daily_limit_cny" gorm:"column:daily_limit_cny;type:decimal(20,2);not null;default:0"`
	ReturnType    string          `json:"return_type" gorm:"type:varchar(16)"`
	Enabled       bool            `json:"enabled"`
	// DisplayName 是钱包里给客户看的名字，比如「支付宝1」；空着就按方式显示。
	DisplayName string `json:"display_name" gorm:"type:varchar(64);not null;default:''"`
}

func (AdtyEpayChannelMethod) TableName() string {
	return "adty_epay_channel_methods"
}

// MethodFor 返回这个通道上已启用的某种方式。
func (channel *AdtyEpayChannel) MethodFor(method string) *AdtyEpayChannelMethod {
	for i := range channel.Methods {
		if channel.Methods[i].Method == method && channel.Methods[i].Enabled {
			return &channel.Methods[i]
		}
	}
	return nil
}

// MethodByTypeCode 按回调里的 type 找方式（个别易支付程序的代码和方式名不同）。
func (channel *AdtyEpayChannel) MethodByTypeCode(typeCode string) *AdtyEpayChannelMethod {
	for i := range channel.Methods {
		code := channel.Methods[i].TypeCode
		if code == "" {
			code = channel.Methods[i].Method
		}
		if code == typeCode {
			return &channel.Methods[i]
		}
	}
	return nil
}

// ListAdtyEpayChannels 列出全部通道和它们的方式。
func ListAdtyEpayChannels() ([]AdtyEpayChannel, error) {
	var channels []AdtyEpayChannel
	err := DB.Preload("Methods").Order("priority DESC, id").Find(&channels).Error
	return channels, err
}

// GetAdtyEpayChannel 取一个通道和它的方式。
func GetAdtyEpayChannel(id int) (*AdtyEpayChannel, error) {
	var channels []AdtyEpayChannel
	if err := DB.Preload("Methods").Where("id = ?", id).Limit(1).Find(&channels).Error; err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, ErrAdtyEpayChannelNotFound
	}
	return &channels[0], nil
}

// SameNonSecretPaymentIdentity compares non-secret payment routing and
// verification fields (used when existing secret decryption failed so Root
// can re-key broken secrets without mutating channel identity on channels with orders).
func (channel *AdtyEpayChannel) SameNonSecretPaymentIdentity(other *AdtyEpayChannel) bool {
	if other == nil || channel.Id != other.Id || channel.MerchantId != other.MerchantId ||
		channel.EffectiveSignType() != other.EffectiveSignType() ||
		channel.PlatformPublicKey != other.PlatformPublicKey ||
		channel.GatewayURL != other.GatewayURL ||
		channel.OrderMode != other.OrderMode || len(channel.Methods) != len(other.Methods) {
		return false
	}
	codes := make(map[string]string, len(channel.Methods))
	for _, method := range channel.Methods {
		code := method.TypeCode
		if code == "" {
			code = method.Method
		}
		codes[method.Method] = code
	}
	for _, method := range other.Methods {
		code := method.TypeCode
		if code == "" {
			code = method.Method
		}
		if original, ok := codes[method.Method]; !ok || original != code {
			return false
		}
		delete(codes, method.Method)
	}
	return true
}

// SamePaymentIdentity compares the credentials and method mapping used for
// checkout, callbacks and queries. Operational limits and labels may change.
func (channel *AdtyEpayChannel) SamePaymentIdentity(other *AdtyEpayChannel) bool {
	if !channel.SameNonSecretPaymentIdentity(other) {
		return false
	}
	if channel.IsRSA() {
		return channel.MerchantPrivateKey == other.MerchantPrivateKey
	}
	return channel.MerchantKey == other.MerchantKey
}

// SaveAdtyEpayChannel 新建或改一个通道，方式整组替换。keepKey 为真时不改商户密钥；编辑时留空的 RSA 密钥也会保留原值。
func SaveAdtyEpayChannel(channel *AdtyEpayChannel, keepKey bool) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		channel.SignType = channel.EffectiveSignType()
		methods := channel.Methods
		defer func() { channel.Methods = methods }()
		keepPublicKey := false
		keepCorruptedKey := false
		keepCorruptedPrivateKey := false
		if channel.Id != 0 {
			var existing AdtyEpayChannel
			if err := AdtyLockForUpdate(tx).Preload("Methods").First(&existing, channel.Id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtyEpayChannelNotFound
				}
				return err
			}
			if existing.Status == AdtyEpayStatusRevoked {
				return common.NewMessage("This payment channel has been permanently revoked")
			}
			if channel.IsRSA() {
				// Clear inactive MD5 key so stale/corrupted columns never poison an RSA channel.
				channel.MerchantKey = ""
				if channel.MerchantPrivateKey == "" {
					if existing.CredentialDecryptErr && existing.IsRSA() && existing.MerchantPrivateKey == "" {
						keepCorruptedPrivateKey = true
					}
					channel.MerchantPrivateKey = existing.MerchantPrivateKey
				}
				if channel.PlatformPublicKey == "" {
					keepPublicKey = true
					channel.PlatformPublicKey = existing.PlatformPublicKey
				}
			} else {
				// Clear inactive RSA columns so stale/corrupted columns never poison an MD5 channel.
				channel.MerchantPrivateKey = ""
				channel.PlatformPublicKey = ""
				if keepKey {
					if existing.CredentialDecryptErr && !existing.IsRSA() && existing.MerchantKey == "" {
						keepCorruptedKey = true
					}
					channel.MerchantKey = existing.MerchantKey
				}
			}
			identityChanged := !existing.SamePaymentIdentity(channel)
			if identityChanged && existing.CredentialDecryptErr {
				// Only exempt the active scheme's undecryptable secret field so Root can
				// re-key it while non-secret routing/verification identity stays locked.
				activeSecretOK := (!existing.IsRSA() && existing.MerchantKey == "") ||
					(existing.IsRSA() && existing.MerchantPrivateKey == "")
				if existing.SameNonSecretPaymentIdentity(channel) && activeSecretOK {
					identityChanged = false
				}
			}
			if identityChanged {
				var count int64
				if err := tx.Model(&AdtyTopupOrder{}).Where("channel_id = ?", channel.Id).Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					return common.NewMessage("This channel has orders. Create a new channel to change payment credentials or method codes")
				}
			}
			// Status writes use their own operation; do not restore a stale
			// status read by the editor before this transaction acquired its lock.
			channel.Status, channel.StatusReason = existing.Status, existing.StatusReason
		}
		channel.Methods = nil
		toSave := *channel
		if err := sealAdtyEpayChannelCredentials(&toSave, keepCorruptedKey, keepCorruptedPrivateKey); err != nil {
			return err
		}
		if channel.Id == 0 {
			if err := tx.Create(&toSave).Error; err != nil {
				return err
			}
			channel.Id = toSave.Id
			channel.CreatedAt = toSave.CreatedAt
			channel.UpdatedAt = toSave.UpdatedAt
		} else {
			omit := []string{"created_at", "unpaid_streak", "probe_fail_count", "probe_ok_count", "last_probe_at"}
			if keepCorruptedKey {
				omit = append(omit, "merchant_key")
			}
			if keepCorruptedPrivateKey {
				omit = append(omit, "merchant_private_key")
			}
			if keepPublicKey {
				omit = append(omit, "platform_public_key")
			}
			result := tx.Model(&AdtyEpayChannel{}).Where("id = ?", channel.Id).Omit(omit...).Select("*").Updates(&toSave)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrAdtyEpayChannelNotFound
			}
			channel.UpdatedAt = toSave.UpdatedAt
		}
		if err := tx.Where("channel_id = ?", channel.Id).Delete(&AdtyEpayChannelMethod{}).Error; err != nil {
			return err
		}
		for i := range methods {
			methods[i].Id = 0
			methods[i].ChannelId = channel.Id
			if methods[i].TypeCode == "" {
				methods[i].TypeCode = methods[i].Method
			}
		}
		if len(methods) > 0 {
			if err := tx.Create(&methods).Error; err != nil {
				return err
			}
		}
		channel.Methods = methods
		return nil
	})
}

// DeleteAdtyEpayChannel 删除通道。旧订单的回调仍按 channelId 路由，所以有订单的通道只能停用；
// 仍被支付场景引用的通道也必须先从场景中移除，避免删除后静默放开或卡死配置保存（R8-15）。
func DeleteAdtyEpayChannel(id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// R8-15: Follow canonical lock order adty_payment_scene_configs -> adty_epay_channels.
		if tx.Migrator().HasTable(&AdtyPaymentSceneConfig{}) {
			sceneCfg, cfgErr := GetAdtyPaymentSceneConfig(tx, true)
			if cfgErr != nil {
				return cfgErr
			}
			var scenes []AdtyPaymentScene
			if strings.TrimSpace(sceneCfg.Payload) != "" {
				if err := common.Unmarshal([]byte(sceneCfg.Payload), &scenes); err != nil {
					return err
				}
			}
			for _, scene := range scenes {
				if slices.Contains(scene.ChannelIDs, id) {
					return common.NewMessage("Remove this payment channel from payment scenes before deleting it")
				}
			}
		}
		var channel AdtyEpayChannel
		if err := AdtyLockForUpdate(tx).First(&channel, id).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&AdtyTopupOrder{}).Where("channel_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return common.NewMessage("This channel has orders. Disable it instead of deleting it")
		}
		if err := tx.Where("channel_id = ?", id).Delete(&AdtyEpayChannelMethod{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&AdtyEpayChannel{}).Error
	})
}

// SetAdtyEpayChannelStatus 改通道状态和原因；启用时清零连续未付和探活计数。
func SetAdtyEpayChannelStatus(id int, status, reason string) error {
	return setAdtyEpayChannelStatus(DB, id, status, reason)
}

// RevokeAdtyEpayChannelWithAudit commits permanent credential removal together
// with recoverable evidence. A log-store outage cannot lose the operation.
func RevokeAdtyEpayChannelWithAudit(id int, event AuditLog) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var channel AdtyEpayChannel
		if err := AdtyLockForUpdate(tx).First(&channel, id).Error; err != nil {
			return err
		}
		before, err := encodeOfficialAuditValue(map[string]any{
			"status": channel.Status, "status_reason": channel.StatusReason, "credential_present": channel.HasCredentials(),
		})
		if err != nil {
			return err
		}
		after, err := encodeOfficialAuditValue(map[string]any{
			"status": AdtyEpayStatusRevoked, "status_reason": AdtyEpayReasonManual, "credential_present": false,
		})
		if err != nil {
			return err
		}
		event.BeforeValue, event.AfterValue = before, after
		if err := setAdtyEpayChannelStatus(tx, id, AdtyEpayStatusRevoked, AdtyEpayReasonManual); err != nil {
			return err
		}
		return AppendAuditIntentTx(tx, event)
	})
}

func setAdtyEpayChannelStatus(tx *gorm.DB, id int, status, reason string) error {
	updates := map[string]any{"status": status, "status_reason": reason}
	if status == AdtyEpayStatusRevoked {
		updates["merchant_key"] = ""
		updates["merchant_private_key"] = ""
		updates["platform_public_key"] = ""
		updates["status_reason"] = AdtyEpayReasonManual
	}
	if status == AdtyEpayStatusEnabled {
		updates["status_reason"] = ""
		updates["unpaid_streak"] = 0
		updates["probe_fail_count"] = 0
		updates["probe_ok_count"] = 0
	}
	result := tx.Model(&AdtyEpayChannel{}).Where("id = ? AND status <> ?", id, AdtyEpayStatusRevoked).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var channel AdtyEpayChannel
		if err := tx.First(&channel, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtyEpayChannelNotFound
			}
			return err
		}
		if status == AdtyEpayStatusRevoked && channel.Status == AdtyEpayStatusRevoked {
			return nil
		}
		return common.NewMessage("This payment channel has been permanently revoked")
	}
	return nil
}

// UpdateAdtyEpayMethodReturnType 记下这种方式实际返回的是二维码、链接还是 scheme。
func UpdateAdtyEpayMethodReturnType(methodID int, returnType string) error {
	return DB.Model(&AdtyEpayChannelMethod{}).Where("id = ?", methodID).Update("return_type", returnType).Error
}

// SetAdtyEpayMethodEnabled 打开或关闭一种方式（测试下单发现没开通时关掉）。
func SetAdtyEpayMethodEnabled(methodID int, enabled bool) error {
	return DB.Model(&AdtyEpayChannelMethod{}).Where("id = ?", methodID).Update("enabled", enabled).Error
}

func adtyEpayDayStart(tx *gorm.DB, now int64) int64 {
	loc := AdtyBusinessLocation(tx)
	t := time.Unix(now, 0).In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Unix()
}

func sumAdtyEpayCommittedTodayTx(tx *gorm.DB, channelID int, method string, now int64) (decimal.Decimal, error) {
	dayStart := adtyEpayDayStart(tx, now)
	var total decimal.Decimal
	err := tx.Table("adty_topup_orders AS a").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no").
		Where("a.channel_id = ? AND a.method = ? AND ((t.status = ? AND (t.complete_time >= ? OR t.create_time >= ?)) OR (t.status = ? AND t.create_time >= ? AND (a.expire_at = 0 OR a.expire_at > ?)))",
			channelID, method,
			common.TopUpStatusSuccess, dayStart, dayStart,
			common.TopUpStatusPending, dayStart, now).
		Select("COALESCE(SUM(a.pay_amount), 0)").Scan(&total).Error
	return total, err
}

// lockAdtyEpayDailyRoom locks the method row and rejects the order when paid
// plus currently unexpired pending amounts for today, plus this order, exceed
// the daily cap.
func lockAdtyEpayDailyRoom(tx *gorm.DB, channelId int, method string, amount decimal.Decimal, now int64) error {
	var row AdtyEpayChannelMethod
	if err := AdtyLockForUpdate(tx).Where("channel_id = ? AND method = ?", channelId, method).First(&row).Error; err != nil {
		return err
	}
	if !row.DailyLimitCNY.IsPositive() {
		return nil
	}
	total, err := sumAdtyEpayCommittedTodayTx(tx, channelId, method, now)
	if err != nil {
		return err
	}
	if total.Add(amount).GreaterThan(row.DailyLimitCNY) {
		return ErrAdtyEpayDailyLimit
	}
	return nil
}

// SumAdtyEpayCommittedToday returns today's paid amount plus currently unexpired
// pending reservations for a channel method, matching lockAdtyEpayDailyRoom.
func SumAdtyEpayCommittedToday(channelID int, method string, now int64) (decimal.Decimal, error) {
	return sumAdtyEpayCommittedTodayTx(DB, channelID, method, now)
}

// SumAdtyEpayPaidToday 统计一个通道某种方式当天已到账的人民币（每日限额用）。
func SumAdtyEpayPaidToday(channelId int, method string, dayStart int64) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := DB.Table("adty_topup_orders AS a").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no").
		Where("a.channel_id = ? AND a.method = ? AND t.status = ? AND t.complete_time >= ?", channelId, method, common.TopUpStatusSuccess, dayStart).
		Select("COALESCE(SUM(a.pay_amount), 0)").Scan(&total).Error
	return total, err
}
