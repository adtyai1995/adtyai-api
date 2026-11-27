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
	ConfigVersion        int64                   `json:"-" gorm:"not null;default:0"`
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

// AdtyEpayOrderMatchesChannelMerchantGateway reports whether an order's frozen
// merchant ID and gateway URL still match the channel's current configuration.
// Legacy orders without those frozen columns remain compatible until the
// channel's identity is edited (which backfills the pre-edit identity onto them).
func AdtyEpayOrderMatchesChannelMerchantGateway(ext *AdtyTopupOrder, channel *AdtyEpayChannel) bool {
	if ext == nil || channel == nil || ext.ChannelId != channel.Id {
		return false
	}
	if merchant := strings.TrimSpace(ext.ProviderMerchantId); merchant != "" && merchant != strings.TrimSpace(channel.MerchantId) {
		return false
	}
	if gateway := strings.TrimRight(strings.TrimSpace(ext.CheckoutGateway), "/"); gateway != "" &&
		gateway != strings.TrimRight(strings.TrimSpace(channel.GatewayURL), "/") {
		return false
	}
	return true
}

// AdtyEpayOrderMatchesChannelIdentity reports whether an active pending order
// still matches the channel's current payment configuration version, merchant
// ID, and gateway URL, preventing reuse of stale checkout links after edits.
func AdtyEpayOrderMatchesChannelIdentity(ext *AdtyTopupOrder, channel *AdtyEpayChannel) bool {
	if !AdtyEpayOrderMatchesChannelMerchantGateway(ext, channel) {
		return false
	}
	return ext.ChannelConfigVersion == channel.ConfigVersion
}

func backfillAdtyEpayOrderSnapshotsTx(tx *gorm.DB, channel *AdtyEpayChannel, includeIdentity bool) error {
	if channel == nil || channel.Id <= 0 {
		return nil
	}
	hasOrders, err := AdtyTableExists(tx, &AdtyTopupOrder{})
	if err != nil || !hasOrders {
		return err
	}
	if name := strings.TrimSpace(channel.Name); name != "" {
		if err := tx.Model(&AdtyTopupOrder{}).
			Where("channel_id = ? AND channel_name = ?", channel.Id, "").
			Update("channel_name", name).Error; err != nil {
			return err
		}
	}
	if !includeIdentity {
		return nil
	}
	if merchant := strings.TrimSpace(channel.MerchantId); merchant != "" {
		if err := tx.Model(&AdtyTopupOrder{}).
			Where("channel_id = ? AND provider_merchant_id = ?", channel.Id, "").
			Update("provider_merchant_id", merchant).Error; err != nil {
			return err
		}
	}
	if gateway := strings.TrimRight(strings.TrimSpace(channel.GatewayURL), "/"); gateway != "" {
		if err := tx.Model(&AdtyTopupOrder{}).
			Where("channel_id = ? AND checkout_gateway = ?", channel.Id, "").
			Update("checkout_gateway", gateway).Error; err != nil {
			return err
		}
	}
	return nil
}

func updatePaymentScenesForChannelTx(tx *gorm.DB, oldID, replacementID, actorID int) (updatedScenes, disabledScenes int, err error) {
	hasSceneCfgTable, err := AdtyTableExists(tx, &AdtyPaymentSceneConfig{})
	if err != nil || !hasSceneCfgTable {
		return 0, 0, err
	}
	sceneCfg, err := GetAdtyPaymentSceneConfig(tx, true)
	if err != nil {
		return 0, 0, err
	}
	var scenes []AdtyPaymentScene
	if strings.TrimSpace(sceneCfg.Payload) != "" {
		if err := common.Unmarshal([]byte(sceneCfg.Payload), &scenes); err != nil {
			return 0, 0, err
		}
	}
	for i := range scenes {
		if !slices.Contains(scenes[i].ChannelIDs, oldID) {
			continue
		}
		nextIDs := make([]int, 0, len(scenes[i].ChannelIDs))
		for _, cid := range scenes[i].ChannelIDs {
			if cid != oldID {
				nextIDs = append(nextIDs, cid)
			}
		}
		if replacementID > 0 {
			if !slices.Contains(nextIDs, replacementID) {
				nextIDs = append(nextIDs, replacementID)
			}
			scenes[i].ChannelIDs = nextIDs
		} else if len(nextIDs) > 0 {
			scenes[i].ChannelIDs = nextIDs
		} else {
			// R8-15: Removing the sole restricted Epay channel must never silently
			// widen the scene to all Epay channels (empty channel_ids means unrestricted).
			scenes[i].ChannelIDs = []int{}
			otherProviders := make([]string, 0, len(scenes[i].Providers))
			for _, p := range scenes[i].Providers {
				if p != PaymentProviderAdtyEpay {
					otherProviders = append(otherProviders, p)
				}
			}
			if len(otherProviders) > 0 {
				scenes[i].Providers = otherProviders
			} else {
				scenes[i].Enabled = false
				disabledScenes++
			}
		}
		updatedScenes++
	}
	if updatedScenes == 0 {
		return 0, 0, nil
	}
	payload, err := common.Marshal(scenes)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Model(sceneCfg).Where("id = ?", sceneCfg.ID).Updates(map[string]any{
		"version":    sceneCfg.Version + 1,
		"payload":    string(payload),
		"updated_by": actorID,
		"updated_at": time.Now().Unix(),
	}).Error; err != nil {
		return 0, 0, err
	}
	return updatedScenes, disabledScenes, nil
}

// SaveAdtyEpayChannel 新建或改一个通道，方式整组替换。keepKey 为真时不改商户密钥；编辑时留空的 RSA 密钥也会保留原值。
// 允许修改已有订单或已撤销通道的全部配置（名称、地址、商户 ID、密钥、签名方式、下单方式、支付方式等）：
// 修改支付凭据或网关身份前会先把旧通道名/商户号/网关快照到历史订单，并递增配置版本、重置故障计数。
func SaveAdtyEpayChannel(channel *AdtyEpayChannel, keepKey bool) error {
	if channel != nil && channel.Id != 0 {
		var pre AdtyEpayChannel
		if err := DB.First(&pre, channel.Id).Error; err == nil {
			if err := backfillAdtyEpayOrderSnapshotsTx(DB, &pre, true); err != nil {
				return err
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		channel.SignType = channel.EffectiveSignType()
		methods := channel.Methods
		defer func() { channel.Methods = methods }()
		keepPublicKey := false
		keepCorruptedKey := false
		keepCorruptedPrivateKey := false
		identityChanged := false
		if channel.Id != 0 {
			var existing AdtyEpayChannel
			if err := AdtyLockForUpdate(tx).Preload("Methods").First(&existing, channel.Id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtyEpayChannelNotFound
				}
				return err
			}
			if channel.IsRSA() {
				// Clear inactive MD5 key so stale/corrupted columns never poison an RSA channel.
				channel.MerchantKey = ""
				if channel.MerchantPrivateKey == "" {
					if existing.Status == AdtyEpayStatusRevoked {
						return common.NewMessage("The Epay merchant private key is missing or invalid")
					}
					if existing.CredentialDecryptErr && existing.IsRSA() && existing.MerchantPrivateKey == "" {
						keepCorruptedPrivateKey = true
					}
					channel.MerchantPrivateKey = existing.MerchantPrivateKey
				}
				if channel.PlatformPublicKey == "" {
					if existing.Status == AdtyEpayStatusRevoked {
						return common.NewMessage("The Epay platform public key is missing or invalid")
					}
					keepPublicKey = true
					channel.PlatformPublicKey = existing.PlatformPublicKey
				}
				if !keepCorruptedPrivateKey && !channel.HasCredentials() {
					if strings.TrimSpace(channel.MerchantPrivateKey) == "" {
						return common.NewMessage("The Epay merchant private key is missing or invalid")
					}
					return common.NewMessage("The Epay platform public key is missing or invalid")
				}
			} else {
				// Clear inactive RSA columns so stale/corrupted columns never poison an MD5 channel.
				channel.MerchantPrivateKey = ""
				channel.PlatformPublicKey = ""
				if keepKey {
					if existing.Status == AdtyEpayStatusRevoked {
						return common.NewMessage("The merchant key is required")
					}
					if existing.CredentialDecryptErr && !existing.IsRSA() && existing.MerchantKey == "" {
						keepCorruptedKey = true
					}
					channel.MerchantKey = existing.MerchantKey
				}
				if !keepCorruptedKey && !channel.HasCredentials() {
					return common.NewMessage("The merchant key is required")
				}
			}
			identityChanged = existing.Status == AdtyEpayStatusRevoked || !existing.SamePaymentIdentity(channel)
			if identityChanged {
				channel.ConfigVersion = existing.ConfigVersion + 1
				channel.UnpaidStreak = 0
				channel.ProbeFailCount = 0
				channel.ProbeOkCount = 0
				channel.LastProbeAt = 0
				for i := range methods {
					methods[i].ReturnType = ""
				}
			} else {
				channel.ConfigVersion = existing.ConfigVersion
			}
			// Status writes use their own operation; do not restore a stale status read
			// before acquiring the row lock. Re-configuring a revoked channel restores it
			// to disabled so Root can test and enable it again.
			if existing.Status == AdtyEpayStatusRevoked {
				channel.Status = AdtyEpayStatusDisabled
				channel.StatusReason = AdtyEpayReasonManual
			} else {
				channel.Status, channel.StatusReason = existing.Status, existing.StatusReason
			}
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
			omit := []string{"created_at"}
			if !identityChanged {
				omit = append(omit, "unpaid_streak", "probe_fail_count", "probe_ok_count", "last_probe_at")
			}
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

// AdtyEpayChannelDeleteResult summarizes side effects of deleting a payment channel.
type AdtyEpayChannelDeleteResult struct {
	Id             int    `json:"id"`
	Name           string `json:"name"`
	UpdatedScenes  int    `json:"updated_scenes"`
	DisabledScenes int    `json:"disabled_scenes"`
}

// DeleteAdtyEpayChannel deletes any Epay payment channel while preserving
// historical order channel names and keeping payment scene restrictions safe.
func DeleteAdtyEpayChannel(id int) error {
	_, err := DeleteAdtyEpayChannelWithActor(id, 0)
	return err
}

// DeleteAdtyEpayChannelWithActor deletes a payment channel, snapshotting its
// name onto historical orders and cleaning up payment scene references in one
// transaction (canonical lock order: adty_payment_scene_configs -> adty_epay_channels).
func DeleteAdtyEpayChannelWithActor(id, actorID int) (*AdtyEpayChannelDeleteResult, error) {
	var pre AdtyEpayChannel
	if err := DB.First(&pre, id).Error; err == nil {
		if err := backfillAdtyEpayOrderSnapshotsTx(DB, &pre, true); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var res *AdtyEpayChannelDeleteResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		updatedScenes, disabledScenes, err := updatePaymentScenesForChannelTx(tx, id, 0, actorID)
		if err != nil {
			return err
		}
		var channel AdtyEpayChannel
		if err := AdtyLockForUpdate(tx).First(&channel, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtyEpayChannelNotFound
			}
			return err
		}
		if err := tx.Where("channel_id = ?", id).Delete(&AdtyEpayChannelMethod{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", id).Delete(&AdtyEpayChannel{}).Error; err != nil {
			return err
		}
		res = &AdtyEpayChannelDeleteResult{
			Id:             id,
			Name:           channel.Name,
			UpdatedScenes:  updatedScenes,
			DisabledScenes: disabledScenes,
		}
		return nil
	})
	return res, err
}

// AdtyEpayChannelReplaceOptions describes how to replace an existing payment channel.
type AdtyEpayChannelReplaceOptions struct {
	TargetChannelID int
	NewChannel      *AdtyEpayChannel
	DeleteOld       bool
	EnableTarget    bool
	ActorID         int
}

// AdtyEpayChannelReplaceResult summarizes a payment channel replacement.
type AdtyEpayChannelReplaceResult struct {
	OldID         int    `json:"old_id"`
	OldName       string `json:"old_name"`
	NewID         int    `json:"new_id"`
	NewName       string `json:"new_name"`
	DeletedOld    bool   `json:"deleted_old"`
	UpdatedScenes int    `json:"updated_scenes"`
}

// ReplaceAdtyEpayChannel replaces oldID with either an existing channel
// (TargetChannelID) or a newly created channel (NewChannel), atomically
// migrating all payment scene references and disabling or deleting the old
// channel while preserving historical order snapshots.
func ReplaceAdtyEpayChannel(oldID int, opts AdtyEpayChannelReplaceOptions) (*AdtyEpayChannelReplaceResult, error) {
	hasTarget := opts.TargetChannelID > 0
	hasNew := opts.NewChannel != nil
	if hasTarget == hasNew {
		return nil, common.NewMessage("Invalid parameters")
	}
	if hasTarget && opts.TargetChannelID == oldID {
		return nil, common.NewMessage("Cannot replace a payment channel with itself")
	}
	if opts.DeleteOld {
		var pre AdtyEpayChannel
		if err := DB.First(&pre, oldID).Error; err == nil {
			if err := backfillAdtyEpayOrderSnapshotsTx(DB, &pre, true); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	var result *AdtyEpayChannelReplaceResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Acquire adty_payment_scene_configs lock before adty_epay_channels to match
		// the canonical lock order used by DeleteAdtyEpayChannel and SaveAdtyPaymentScenes.
		hasSceneCfgTable, err := AdtyTableExists(tx, &AdtyPaymentSceneConfig{})
		if err != nil {
			return err
		}
		if hasSceneCfgTable {
			if _, err := GetAdtyPaymentSceneConfig(tx, true); err != nil {
				return err
			}
		}

		var oldChannel AdtyEpayChannel
		var targetChannel AdtyEpayChannel
		if hasTarget {
			firstID, secondID := min(oldID, opts.TargetChannelID), max(oldID, opts.TargetChannelID)
			var first, second AdtyEpayChannel
			if err := AdtyLockForUpdate(tx).First(&first, firstID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtyEpayChannelNotFound
				}
				return err
			}
			if err := AdtyLockForUpdate(tx).First(&second, secondID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtyEpayChannelNotFound
				}
				return err
			}
			if first.Id == oldID {
				oldChannel, targetChannel = first, second
			} else {
				oldChannel, targetChannel = second, first
			}
			if targetChannel.Status == AdtyEpayStatusRevoked || !targetChannel.HasCredentials() {
				return common.NewMessage("The replacement payment channel is revoked or missing credentials")
			}
			if opts.EnableTarget {
				if targetChannel.CredentialDecryptErr {
					return common.NewMessage("Cannot enable this payment channel because its stored credentials cannot be decrypted. Re-enter the credentials first")
				}
				if err := setAdtyEpayChannelStatus(tx, targetChannel.Id, AdtyEpayStatusEnabled, AdtyEpayReasonManual); err != nil {
					return err
				}
			}
		} else {
			if err := AdtyLockForUpdate(tx).First(&oldChannel, oldID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtyEpayChannelNotFound
				}
				return err
			}
			newCh := *opts.NewChannel
			newCh.Id = 0
			newCh.SignType = newCh.EffectiveSignType()
			methods := append([]AdtyEpayChannelMethod(nil), newCh.Methods...)
			newCh.Methods = nil
			if opts.EnableTarget {
				newCh.Status = AdtyEpayStatusEnabled
				newCh.StatusReason = ""
			} else {
				newCh.Status = AdtyEpayStatusDisabled
				newCh.StatusReason = AdtyEpayReasonManual
			}
			if err := sealAdtyEpayChannelCredentials(&newCh, false, false); err != nil {
				return err
			}
			if err := tx.Create(&newCh).Error; err != nil {
				return err
			}
			for i := range methods {
				methods[i].Id = 0
				methods[i].ChannelId = newCh.Id
				if methods[i].TypeCode == "" {
					methods[i].TypeCode = methods[i].Method
				}
			}
			if len(methods) > 0 {
				if err := tx.Create(&methods).Error; err != nil {
					return err
				}
			}
			targetChannel = newCh
			opts.NewChannel.Id = newCh.Id
		}

		updatedScenes, _, err := updatePaymentScenesForChannelTx(tx, oldID, targetChannel.Id, opts.ActorID)
		if err != nil {
			return err
		}

		if opts.DeleteOld {
			if err := tx.Where("channel_id = ?", oldID).Delete(&AdtyEpayChannelMethod{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id = ?", oldID).Delete(&AdtyEpayChannel{}).Error; err != nil {
				return err
			}
		} else if oldChannel.Status != AdtyEpayStatusRevoked {
			if err := setAdtyEpayChannelStatus(tx, oldID, AdtyEpayStatusDisabled, AdtyEpayReasonManual); err != nil {
				return err
			}
		}

		result = &AdtyEpayChannelReplaceResult{
			OldID:         oldChannel.Id,
			OldName:       oldChannel.Name,
			NewID:         targetChannel.Id,
			NewName:       targetChannel.Name,
			DeletedOld:    opts.DeleteOld,
			UpdatedScenes: updatedScenes,
		}
		return nil
	})
	return result, err
}

// CountAdtyEpayOrdersByChannel returns historical order counts grouped by channel_id.
func CountAdtyEpayOrdersByChannel() (map[int]int64, error) {
	counts := map[int]int64{}
	hasOrders, err := AdtyTableExists(DB, &AdtyTopupOrder{})
	if err != nil || !hasOrders {
		return counts, err
	}
	var rows []struct {
		ChannelId int
		Count     int64
	}
	if err := DB.Model(&AdtyTopupOrder{}).
		Select("channel_id, COUNT(*) AS count").
		Where("channel_id > 0").
		Group("channel_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.ChannelId] = row.Count
	}
	return counts, nil
}

// SetAdtyEpayChannelStatus 改通道状态和原因；启用时清零连续未付和探活计数。
func SetAdtyEpayChannelStatus(id int, status, reason string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		return setAdtyEpayChannelStatus(tx, id, status, reason)
	})
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
	var current AdtyEpayChannel
	if err := AdtyLockForUpdate(tx).First(&current, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAdtyEpayChannelNotFound
		}
		return err
	}
	if current.Status == AdtyEpayStatusRevoked {
		if status == AdtyEpayStatusRevoked {
			return nil
		}
		return common.NewMessage("This payment channel's credentials have been revoked. Re-enter valid credentials to restore it")
	}
	if status == AdtyEpayStatusEnabled {
		if current.CredentialDecryptErr {
			return common.NewMessage("Cannot enable this payment channel because its stored credentials cannot be decrypted. Re-enter the credentials first")
		}
		if !current.HasCredentials() {
			return common.NewMessage("Cannot enable this payment channel because its credentials are incomplete")
		}
	}
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
	return tx.Model(&AdtyEpayChannel{}).Where("id = ?", id).Updates(updates).Error
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

func adtyEpayChannelIdentityScope(tx *gorm.DB, channelID int) (merchant, gateway string) {
	if tx == nil || channelID <= 0 {
		return "", ""
	}
	var ch struct {
		MerchantId string
		GatewayURL string
	}
	if err := tx.Table("adty_epay_channels").Select("merchant_id, gateway_url").Where("id = ?", channelID).Take(&ch).Error; err != nil {
		return "", ""
	}
	return strings.TrimSpace(ch.MerchantId), strings.TrimRight(strings.TrimSpace(ch.GatewayURL), "/")
}

func sumAdtyEpayCommittedTodayTx(tx *gorm.DB, channelID int, method string, now int64) (decimal.Decimal, error) {
	dayStart := adtyEpayDayStart(tx, now)
	merchant, gateway := adtyEpayChannelIdentityScope(tx, channelID)
	var total decimal.Decimal
	q := tx.Table("adty_topup_orders AS a").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no").
		Where("a.channel_id = ? AND a.method = ? AND ((t.status = ? AND (t.complete_time >= ? OR t.create_time >= ?)) OR (t.status = ? AND t.create_time >= ? AND (a.expire_at = 0 OR a.expire_at > ?)))",
			channelID, method,
			common.TopUpStatusSuccess, dayStart, dayStart,
			common.TopUpStatusPending, dayStart, now)
	if merchant != "" {
		q = q.Where("(a.provider_merchant_id = '' OR a.provider_merchant_id = ?)", merchant)
	}
	if gateway != "" {
		q = q.Where("(a.checkout_gateway = '' OR a.checkout_gateway = ?)", gateway)
	}
	err := q.Select("COALESCE(SUM(a.pay_amount), 0)").Scan(&total).Error
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
	merchant, gateway := adtyEpayChannelIdentityScope(DB, channelId)
	var total decimal.Decimal
	q := DB.Table("adty_topup_orders AS a").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no").
		Where("a.channel_id = ? AND a.method = ? AND t.status = ? AND t.complete_time >= ?", channelId, method, common.TopUpStatusSuccess, dayStart)
	if merchant != "" {
		q = q.Where("(a.provider_merchant_id = '' OR a.provider_merchant_id = ?)", merchant)
	}
	if gateway != "" {
		q = q.Where("(a.checkout_gateway = '' OR a.checkout_gateway = ?)", gateway)
	}
	err := q.Select("COALESCE(SUM(a.pay_amount), 0)").Scan(&total).Error
	return total, err
}
