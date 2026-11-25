package service

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// 收款码和打款凭证图片（任务 6.1）。存在 ./uploads/referral-assets（工作目录是 /data，部署要挂持久卷）；
// 访问不需要登录，靠链接里的过期时间和签名。

const (
	referralAssetLinkTTL             = 24 * time.Hour
	maxUnboundReferralAssetsPerUser  = 30
)

var (
	ErrReferralAssetType  = errors.New("only PNG, JPEG or WebP images are accepted")
	ErrReferralAssetLimit = common.NewMessage("Too many unused uploaded images; please submit a request before uploading more")
	// referralAssetDir 是存放目录，测试可以换。
	referralAssetDir = func() string { return filepath.Join("uploads", "referral-assets") }
)

var referralAssetExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}

func countUnboundReferralAssets(ownerUserId int) (int, error) {
	if ownerUserId <= 0 || model.DB == nil || !model.DB.Migrator().HasTable(&model.ReferralAsset{}) {
		return 0, nil
	}
	var assets []model.ReferralAsset
	if err := model.DB.Select("storage_path").Where("owner_user_id = ?", ownerUserId).Find(&assets).Error; err != nil {
		return 0, err
	}
	if len(assets) < maxUnboundReferralAssetsPerUser {
		return len(assets), nil
	}
	used := make(map[string]struct{}, len(assets))
	markUsed := func(raw string) {
		if name, ok := extractReferralAssetName(raw); ok {
			used[name] = struct{}{}
		}
	}
	if model.DB.Migrator().HasTable(&model.ReferralWithdrawal{}) {
		var rows []model.ReferralWithdrawal
		if err := model.DB.Select("qr_image_url", "payment_proof_url", "reject_proof_url").Where("user_id = ?", ownerUserId).Find(&rows).Error; err != nil {
			return 0, err
		}
		for _, r := range rows {
			markUsed(r.QRImageURL)
			markUsed(r.PaymentProofURL)
			markUsed(r.RejectProofURL)
		}
	}
	if model.DB.Migrator().HasTable(&model.AdtySiteWithdrawal{}) {
		var rows []model.AdtySiteWithdrawal
		if err := model.DB.Select("qr_image_url", "proof_note").Where("owner_user_id = ?", ownerUserId).Find(&rows).Error; err != nil {
			return 0, err
		}
		for _, r := range rows {
			markUsed(r.QRImageURL)
			markUsed(r.ProofNote)
		}
	}
	if model.DB.Migrator().HasTable(&model.AdtySiteInviteWithdrawal{}) {
		var ownedSiteIDs []int
		if model.DB.Migrator().HasTable(&model.AdtySite{}) {
			if err := model.DB.Model(&model.AdtySite{}).Where("owner_user_id = ?", ownerUserId).Pluck("id", &ownedSiteIDs).Error; err != nil {
				return 0, err
			}
		}
		var rows []model.AdtySiteInviteWithdrawal
		q := model.DB.Select("qr_image_url", "proof_note")
		if len(ownedSiteIDs) > 0 {
			q = q.Where("user_id = ? OR paid_by = ? OR legacy_decision_by = ? OR site_id IN ?", ownerUserId, ownerUserId, ownerUserId, ownedSiteIDs)
		} else {
			q = q.Where("user_id = ? OR paid_by = ? OR legacy_decision_by = ?", ownerUserId, ownerUserId, ownerUserId)
		}
		if err := q.Find(&rows).Error; err != nil {
			return 0, err
		}
		for _, r := range rows {
			markUsed(r.QRImageURL)
			markUsed(r.ProofNote)
		}
	}
	unbound := 0
	for _, asset := range assets {
		if _, ok := used[asset.StoragePath]; !ok {
			unbound++
		}
	}
	return unbound, nil
}

// SaveReferralAsset 存一张图片，返回带签名的访问链接。
func SaveReferralAsset(data []byte, contentType string, ownerUserId int, purpose, createdBy string) (string, error) {
	ext, ok := referralAssetExt[contentType]
	if !ok {
		return "", ErrReferralAssetType
	}
	if ownerUserId > 0 && createdBy != "admin" {
		unbound, err := countUnboundReferralAssets(ownerUserId)
		if err != nil {
			return "", err
		}
		if unbound >= maxUnboundReferralAssetsPerUser {
			return "", ErrReferralAssetLimit
		}
	}
	dir := referralAssetDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%d-%s%s", strings.ReplaceAll(purpose, "_", "-"), ownerUserId, common.GetUUID(), ext)
	fullPath := filepath.Join(dir, name)
	if err := os.WriteFile(fullPath, data, 0o640); err != nil {
		return "", err
	}
	if err := model.DB.Create(&model.ReferralAsset{
		OwnerUserId: ownerUserId, Purpose: purpose, StoragePath: name, ContentType: contentType, Size: int64(len(data)), CreatedBy: createdBy,
	}).Error; err != nil {
		_ = os.Remove(fullPath)
		return "", err
	}
	return SignReferralAssetURL(name, time.Now()), nil
}

func referralAssetSignature(name string, expires int64) string {
	return common.HmacSha256(fmt.Sprintf("%s.%d", name, expires), referralSecret())
}

// SignReferralAssetURL 生成 /api/referral-assets/<文件名>?expires=&sig= 的链接。
func SignReferralAssetURL(name string, now time.Time) string {
	expires := now.Add(referralAssetLinkTTL).Unix()
	values := url.Values{}
	values.Set("expires", strconv.FormatInt(expires, 10))
	values.Set("sig", referralAssetSignature(name, expires))
	return "/api/referral-assets/" + name + "?" + values.Encode()
}

func extractReferralAssetName(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/api/referral-assets/") || strings.Contains(raw, "://") || strings.ContainsAny(raw, "\\\r\n\t ") {
		return "", false
	}
	name := strings.TrimPrefix(raw, "/api/referral-assets/")
	name, _, _ = strings.Cut(name, "?")
	name = strings.TrimPrefix(name, "/")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", false
	}
	return name, true
}

// IsOwnedReferralAssetTx verifies stored asset existence, purpose match, and optional owner match inside tx.
func IsOwnedReferralAssetTx(tx *gorm.DB, ownerUserId int, purpose, raw string) (string, bool) {
	name, ok := extractReferralAssetName(raw)
	if !ok {
		return "", false
	}
	db := tx
	if db == nil {
		db = model.DB
	}
	if db == nil {
		return "", false
	}
	q := db.Model(&model.ReferralAsset{}).Where("storage_path = ? AND purpose = ?", name, purpose)
	if ownerUserId > 0 {
		q = q.Where("owner_user_id = ?", ownerUserId)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil || count == 0 {
		return "", false
	}
	return "/api/referral-assets/" + name, true
}

// ResignReferralAssetURLForWithdrawal re-signs a withdrawal asset URL only when metadata matches owner/purpose.
// Unauthorized or unregistered asset URLs return "" so foreign attachments cannot be re-signed.
// Plain text notes (e.g. non-URL ProofNote) are preserved intact.
func ResignReferralAssetURLForWithdrawal(stored string, expectedOwnerUserId int, expectedPurpose string, now time.Time) string {
	trimmed := strings.TrimSpace(stored)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "/api/referral-assets/") {
		return trimmed
	}
	name, ok := extractReferralAssetName(trimmed)
	if !ok || model.DB == nil {
		return ""
	}
	q := model.DB.Model(&model.ReferralAsset{}).Where("storage_path = ? AND purpose = ?", name, expectedPurpose)
	if expectedOwnerUserId > 0 {
		q = q.Where("owner_user_id = ?", expectedOwnerUserId)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil || count == 0 {
		return ""
	}
	return SignReferralAssetURL(name, now)
}

// ResignReferralAssetURL 给存下来的链接重新签名（列表里展示时用，旧链接可能已过期）。
// Refuses paths not backed by a persisted ReferralAsset record.
func ResignReferralAssetURL(stored string, now time.Time) string {
	trimmed := strings.TrimSpace(stored)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/api/referral-assets/") {
		return stored
	}
	name, ok := extractReferralAssetName(trimmed)
	if !ok || model.DB == nil {
		return ""
	}
	var count int64
	if err := model.DB.Model(&model.ReferralAsset{}).Where("storage_path = ?", name).Count(&count).Error; err != nil || count == 0 {
		return ""
	}
	return SignReferralAssetURL(name, now)
}

// OpenReferralAsset 验签后返回文件在磁盘上的路径。
func OpenReferralAsset(name, expiresRaw, sig string, now time.Time) (string, error) {
	name = strings.TrimPrefix(name, "/")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", os.ErrNotExist
	}
	expires, err := strconv.ParseInt(expiresRaw, 10, 64)
	if err != nil || expires < now.Unix() {
		return "", os.ErrPermission
	}
	if !hmac.Equal([]byte(referralAssetSignature(name, expires)), []byte(sig)) {
		return "", os.ErrPermission
	}
	path := filepath.Join(referralAssetDir(), name)
	if _, err := os.Stat(path); err != nil {
		return "", os.ErrNotExist
	}
	return path, nil
}
