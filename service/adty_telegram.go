package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"gorm.io/gorm"
)

const adtyTelegramReply = "此机器人仅用于通知，请前往网站操作\n\nThis bot only sends notifications. Please use the website."
const adtyTelegramStaged = "已收到绑定请求，请回到网站确认。\n\nBinding request received. Confirm it on the website."
const adtyTranslationNotReadyEn = "English is not ready"

func adtyContainsASCIILatin(text string) bool {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	return false
}

func adtyIsURLToken(token string) bool {
	trimmed := strings.Trim(token, "()[]{}<>\"'`,.;:!?")
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "www.") ||
		strings.HasPrefix(lower, "ftp://") ||
		strings.HasPrefix(lower, "mailto:")
}

func adtyIsASCIIEnglishWord(token string) bool {
	if token == "" || adtyIsURLToken(token) {
		return false
	}
	hasLetter := false
	for i := 0; i < len(token); i++ {
		c := token[i]
		if c > 127 {
			return false
		}
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			hasLetter = true
		}
	}
	return hasLetter
}

func adtyHasStandaloneEnglishParagraph(body string) bool {
	normalized := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	for _, para := range strings.Split(normalized, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" || adtyContainsChinese(para) || isAdtyTranslationNotReadyText(para) {
			continue
		}
		englishWords := 0
		for _, token := range strings.Fields(para) {
			if adtyIsASCIIEnglishWord(token) {
				englishWords++
				if englishWords >= 2 {
					return true
				}
			}
		}
	}
	return false
}

func adtyEnsureTelegramBilingualFallback(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if !adtyContainsChinese(body) && !strings.Contains(body, AdtyTranslationNotReady) {
		return body
	}
	if strings.Contains(strings.ToLower(body), strings.ToLower(adtyTranslationNotReadyEn)) {
		return body
	}
	if strings.Contains(body, AdtyTranslationNotReady) || !adtyHasStandaloneEnglishParagraph(body) {
		return body + "\n\n" + adtyTranslationNotReadyEn
	}
	return body
}

var (
	ErrAdtyTelegramInvalid    = common.NewMessage("Invalid parameters")
	ErrAdtyTelegramForbidden  = common.NewMessage("No permission to perform this action")
	ErrAdtyTelegramUnverified = common.NewMessage("Bot identity is not verified")
	ErrAdtyTelegramPrivate    = common.NewMessage("Private notices stay in a private chat")
	adtyTelegramTokenPattern  = regexp.MustCompile(`^(\d{6,12}):[A-Za-z0-9_-]{20,}$`)
	adtyTelegramPrivateWords  = regexp.MustCompile(`(?i)(订单|退款|保证金|兑换码|\b(?:refund|deposit|voucher|order)\b)`)
)

// AdtyTelegramGetMe verifies a bot token. The default does not dial Telegram.
var AdtyTelegramGetMe = func(string) (int64, string, error) {
	return 0, "", ErrAdtyTelegramUnverified
}

// adtyTelegramHTTP 是所有出站 Telegram 调用共用的 client，**必须带 Timeout**。
//
// 投递任务是一条单线程串行的 goroutine（ticker 每分钟一次，逐条发 20 行）。
// 用 `http.DefaultClient`（无 timeout）时，一次 TCP 卡住（对端不响应 / 丢包 /
// DNS 卡住）就会让它永久停在那行，而且：
//   - 队列只增不减；
//   - 没有任何错误日志（函数没返回，SysError 不执行）；
//   - ticker 也不再触发（goroutine 卡在上一次循环里）。
//
// 「没有新消息发出」的外观和「没有消息要发」一模一样，不会自愈。
//
// 所以超时不是「让失败更快」，是让整条重试机制保持活着。
var adtyTelegramHTTP = NewVerifiedDirectNoRedirectHTTPClient(15 * time.Second)

// adtyTelegramInteractiveHTTP 用于**交互式**调用（保存 bot token 时验证身份）。
//
// 它和投递那个 15 秒的 client 分开，是因为等待的人不同：投递是后台 goroutine，
// 15 秒没人等；而 `getMe` 是用户点了「保存」之后同步等结果——**15 秒会让人以为
// 卡死，然后重复点，产生第二个保存请求。**
var adtyTelegramInteractiveHTTP = NewVerifiedDirectNoRedirectHTTPClient(5 * time.Second)

// adtyTelegramAPIBase 是出站 API 的根地址，**留作变量给测试替换**。
//
// 写死字面量时，验超时的用例只能自己拼一个 URL 去调 client——那样验的是
// `http.Client` 的通用行为，**不是「这两处调用真的走这个 client」**。
// 同仓库的 `adtyModelsDevURL`（`service/adty_official_price.go`）就是这个形态。
var adtyTelegramAPIBase = "https://api.telegram.org"

// AdtyTelegramSender delivers a notice. Tests leave it unset so no message leaves the process.
//
// chat 是字符串而不是 int64：公告要发到分站自己的频道，站长手里只有 `@their_channel`
// 这类名字，让他们自己去翻数字 id 会让每个分站都来问一遍。数字 id（`-100…`）一样
// 能放进字符串，两种写法都只是 Telegram API 的 chat_id 参数。
var AdtyTelegramSender func(botToken string, chat string, text string) error

func init() {
	if os.Getenv("ADTY_TELEGRAM_ALLOW_NETWORK") == "1" {
		AdtyTelegramGetMe = adtyTelegramGetMeHTTP
		// 之前这里是空的：`AdtyTelegramSender` 全仓没有任何赋值，于是
		// RetryAdtyTelegram 里的发送分支永远进不去，队列只会一路 attempts+1
		// 直到 dead。开关和 getMe 用的是同一个，避免两个「是否允许联网」的口径。
		AdtyTelegramSender = adtyTelegramSendMessageHTTP
	}
}

type AdtyTelegramBotView struct {
	Purpose           string `json:"purpose"`
	BotId             int64  `json:"bot_id"`
	Username          string `json:"username"`
	TokenMask         string `json:"token_mask"`
	Enabled           bool   `json:"enabled"`
	IdentityVerified  bool   `json:"identity_verified"`
	SavedAt           int64  `json:"saved_at"`
	TestedAt          int64  `json:"tested_at"`
	Pending           int64  `json:"pending"`
	WebhookConfigured bool   `json:"webhook_configured"`
}

type AdtyTelegramSaved struct {
	View          AdtyTelegramBotView `json:"bot"`
	WebhookSecret string              `json:"webhook_secret,omitempty"`
}

type AdtyTelegramMessage struct {
	Text          string `json:"text"`
	ForwardFrom   any    `json:"forward_from"`
	ForwardOrigin any    `json:"forward_origin"`
	From          struct {
		Id       int64  `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
	Chat struct {
		Id   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
}

type AdtyTelegramUpdate struct {
	UpdateId      int64                `json:"update_id"`
	Message       *AdtyTelegramMessage `json:"message"`
	CallbackQuery any                  `json:"callback_query"`
}

type AdtyTelegramBindView struct {
	Status         string `json:"status"`
	TelegramUserId int64  `json:"telegram_user_id"`
	Username       string `json:"username"`
	Purpose        string `json:"purpose"`
}

func ListAdtyTelegramBots() ([]AdtyTelegramBotView, error) {
	out := make([]AdtyTelegramBotView, 0, 2)
	for _, purpose := range []string{model.AdtyTelegramPurposeUser, model.AdtyTelegramPurposeAdmin} {
		view, err := adtyTelegramView(purpose)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

func adtyValidTelegramPurpose(purpose string) bool {
	return purpose == model.AdtyTelegramPurposeUser || purpose == model.AdtyTelegramPurposeAdmin
}

func adtySiteIsOpen(tx *gorm.DB, siteID int) bool {
	ok, _ := adtyTelegramSiteAccess(tx, siteID)
	return ok
}
func adtyTelegramSiteAccess(tx *gorm.DB, siteID int) (bool, error) {
	if siteID <= 0 {
		return true, nil
	}
	var count int64
	err := tx.Model(&model.AdtySite{}).Where("id=? AND status IN ?", siteID, []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}).Count(&count).Error
	return count > 0, err
}
func adtyCanUseAdminTelegram(tx *gorm.DB, user *model.User) bool {
	ok, _ := adtyTelegramAdminAccess(tx, user)
	return ok
}

// CheckAdtyTelegramAdminAccess verifies whether userID (Root, platform ticket
// admin, site owner, or ticket-handling site staff) may bind the Admin Bot (R2-054).
func CheckAdtyTelegramAdminAccess(userID int) error {
	if model.DB == nil || userID <= 0 {
		return ErrAdtyTelegramForbidden
	}
	var user model.User
	if err := model.DB.First(&user, userID).Error; err != nil {
		return ErrAdtyTelegramForbidden
	}
	ok, err := adtyTelegramAdminAccess(model.DB, &user)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAdtyTelegramForbidden
	}
	return nil
}

func adtyTelegramAdminAccess(tx *gorm.DB, user *model.User) (bool, error) {
	if user == nil || user.Id <= 0 || user.Status != common.UserStatusEnabled {
		return false, nil
	}
	// 1. 现役有效 Root 或具备平台工单处理权限的平台管理员（且未被撤权）
	if user.SiteId == 0 && user.StaffSiteId == 0 && user.Role >= common.RoleAdminUser {
		if user.Role >= common.RoleRootUser {
			return true, nil
		}
		hasCasbinTable, err := model.AdtyTableExists(tx, &model.CasbinRule{})
		if err != nil {
			return false, err
		}
		if hasCasbinTable {
			check, err := authz.PermissionCheckerInTx(tx.Where("v1 = ?", authz.ResourceAdtyTicket))
			if err != nil {
				return false, err
			}
			if check(user.Id, user.Role, authz.AdtyTicketManageAll) || check(user.Id, user.Role, authz.AdtyTicketManage) {
				return true, nil
			}
		}
	}
	// 2. 现役活跃分站站长
	activeStatuses := []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}
	hasSiteTable, err := model.AdtyTableExists(tx, &model.AdtySite{})
	if err != nil {
		return false, err
	}
	if hasSiteTable {
		var site model.AdtySite
		owned := tx.Where("owner_user_id = ? AND status IN ?", user.Id, activeStatuses).Limit(1).Find(&site)
		if owned.Error != nil {
			return false, owned.Error
		}
		if owned.RowsAffected > 0 && AdtySiteAgreementAccepted(site.AgreementVer) {
			return true, nil
		}
		// 3. 具备分站工单权限的现役员工
		hasStaffTable, err := model.AdtyTableExists(tx, &model.AdtySiteStaff{})
		if err != nil {
			return false, err
		}
		if hasStaffTable {
			var staff model.AdtySiteStaff
			lookup := tx.Where("user_id = ? AND status = ?", user.Id, "enabled").Limit(1).Find(&staff)
			if lookup.Error != nil {
				return false, lookup.Error
			}
			if lookup.RowsAffected > 0 && staff.SiteId > 0 &&
				user.StaffSiteId == staff.SiteId && user.SiteId == staff.SiteId &&
				user.Role == common.RoleCommonUser &&
				slices.Contains(AdtyStaffViewOf(staff).Permissions, AdtyStaffTicketManage) {
				var staffSite model.AdtySite
				res := tx.Where("id = ? AND owner_user_id = ? AND status IN ?", staff.SiteId, staff.OwnerUserId, activeStatuses).Limit(1).Find(&staffSite)
				if res.Error != nil {
					return false, res.Error
				}
				if res.RowsAffected == 1 && AdtySiteAgreementAccepted(staffSite.AgreementVer) {
					var owner model.User
					ownerRes := tx.Select("id", "status", "staff_site_id").Where("id = ?", staffSite.OwnerUserId).Limit(1).Find(&owner)
					if ownerRes.Error != nil {
						return false, ownerRes.Error
					}
					if ownerRes.RowsAffected == 1 && owner.Status == common.UserStatusEnabled && owner.StaffSiteId == 0 {
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}

func SaveAdtyTelegramBot(purpose, token string, enabled bool, audits ...model.AuditLog) (*AdtyTelegramSaved, error) {
	if !adtyValidTelegramPurpose(purpose) {
		return nil, ErrAdtyTelegramInvalid
	}
	token = strings.TrimSpace(token)
	var (
		botId        int64
		username     string
		verified     bool
		cipher       string
		secret       string
		webhookHash  string
		identityHash string
	)
	if token != "" {
		match := adtyTelegramTokenPattern.FindStringSubmatch(token)
		if match == nil {
			return nil, ErrAdtyTelegramInvalid
		}
		botId, _ = strconv.ParseInt(match[1], 10, 64)
		if id, name, err := AdtyTelegramGetMe(token); err == nil && id > 0 {
			botId, username, verified = id, name, true
		}
		var err error
		cipher, err = model.SealAdtyPurposeSecret("telegram:"+purpose, token)
		if err != nil {
			return nil, err
		}
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		secret = hex.EncodeToString(raw)
		sum := sha256.Sum256([]byte(secret))
		identity := sha256.Sum256([]byte(strconv.FormatInt(botId, 10)))
		webhookHash = hex.EncodeToString(sum[:])
		identityHash = hex.EncodeToString(identity[:])
	}
	var saved *AdtyTelegramSaved
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtyTelegramBot
		lookup := model.AdtyLockForUpdate(tx).Where("purpose = ?", purpose).Limit(1).Find(&row)
		if lookup.Error != nil {
			return lookup.Error
		}
		before := map[string]any{"enabled": row.Enabled, "bot_id": row.BotId, "configured": row.TokenCipher != ""}
		if token != "" {
			row.Purpose = purpose
			row.BotId = botId
			row.Username = username
			row.TokenCipher = cipher
			row.TokenMask = adtyMaskTail(token)
			row.WebhookHash = webhookHash
			row.IdentityHash = identityHash
			row.IdentityVerified = verified
			row.Enabled = enabled
			row.SavedAt = time.Now().Unix()
			if lookup.RowsAffected == 0 {
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			} else if err := tx.Save(&row).Error; err != nil {
				return err
			}
			var staleBindingIDs []int64
			if err := tx.Model(&model.AdtyTelegramBinding{}).
				Where("purpose = ? AND bot_identity <> ? AND status = ?", purpose, row.IdentityHash, model.AdtyTelegramBindActive).
				Pluck("id", &staleBindingIDs).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.AdtyTelegramBinding{}).
				Where("purpose = ? AND bot_identity <> ? AND status = ?", purpose, row.IdentityHash, model.AdtyTelegramBindActive).
				Update("status", model.AdtyTelegramBindConfirm).Error; err != nil {
				return err
			}
			if len(staleBindingIDs) > 0 {
				if err := tx.Model(&model.AdtyTelegramDelivery{}).
					Where("purpose = ? AND binding_id IN ? AND status IN ?",
						purpose, staleBindingIDs, []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail}).
					Updates(map[string]any{
						"status":     model.AdtyTelegramDeliverySkip,
						"last_error": "bot identity changed",
					}).Error; err != nil {
					return err
				}
			}
		} else if lookup.RowsAffected == 0 {
			return ErrAdtyTelegramInvalid
		} else if err := tx.Model(&row).Update("enabled", enabled).Error; err != nil {
			return err
		}
		view, err := adtyTelegramViewTx(tx, purpose)
		if err != nil {
			return err
		}
		saved = &AdtyTelegramSaved{View: view, WebhookSecret: secret}
		for _, event := range audits {
			event.BeforeValue = common.GetJsonString(before)
			event.AfterValue = common.GetJsonString(map[string]any{"enabled": enabled, "bot_id": row.BotId, "configured": row.TokenCipher != ""})
			if err := model.AppendAuditIntentTx(tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	return saved, err
}

func TestAdtyTelegramBot(purpose string, userId int) error {
	if !adtyValidTelegramPurpose(purpose) || userId <= 0 {
		return ErrAdtyTelegramInvalid
	}
	var row model.AdtyTelegramBot
	if err := model.DB.Where("purpose = ?", purpose).Limit(1).Find(&row).Error; err != nil {
		return err
	}
	if row.Id == 0 || row.TokenCipher == "" {
		return ErrAdtyTelegramInvalid
	}
	if !row.Enabled {
		return common.NewMessage("Telegram bot is disabled")
	}
	token := mustOpenTelegramToken(row)
	if token == "" {
		return ErrAdtyTelegramInvalid
	}
	var user model.User
	if err := model.DB.Select("id", "site_id", "staff_site_id", "status", "role").First(&user, userId).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled {
		return ErrAdtyTelegramForbidden
	}
	if siteOpen, err := adtyTelegramSiteAccess(model.DB, user.SiteId); err != nil {
		return err
	} else if !siteOpen {
		return ErrAdtyTelegramForbidden
	}
	if purpose == model.AdtyTelegramPurposeAdmin {
		adminOK, err := adtyTelegramAdminAccess(model.DB, &user)
		if err != nil {
			return err
		}
		if !adminOK {
			return ErrAdtyTelegramForbidden
		}
	}
	var binding model.AdtyTelegramBinding
	found := model.DB.Where("user_id = ? AND purpose = ? AND status = ?", userId, purpose, model.AdtyTelegramBindActive).Limit(1).Find(&binding)
	if found.Error != nil {
		return found.Error
	}
	if found.RowsAffected == 0 || binding.BotIdentity != row.IdentityHash || binding.TelegramUserId <= 0 || binding.SiteId != user.SiteId {
		return common.NewMessage("Bind your Telegram account first before sending a test message")
	}
	if AdtyTelegramSender == nil {
		return common.NewMessage("Telegram outbound sending is disabled")
	}
	botID, username, err := AdtyTelegramGetMe(token)
	if err != nil {
		return err
	}
	if botID <= 0 {
		return ErrAdtyTelegramUnverified
	}
	if err := AdtyTelegramSender(token, strconv.FormatInt(binding.TelegramUserId, 10), adtyTelegramSavedOr("telegram_test_notice", "测试通知\n\nTest notice")); err != nil {
		return err
	}
	updates := map[string]any{
		"tested_at":         time.Now().Unix(),
		"identity_verified": true,
	}
	if username != "" {
		updates["username"] = username
	}
	return model.DB.Model(&row).Updates(updates).Error
}

func StartAdtyTelegramBind(userId int, purpose string) (string, string, int64, error) {
	if !adtyValidTelegramPurpose(purpose) {
		return "", "", 0, ErrAdtyTelegramInvalid
	}
	var user model.User
	if err := model.DB.Select("id", "site_id", "staff_site_id", "status", "role").First(&user, userId).Error; err != nil {
		return "", "", 0, err
	}
	if user.Status != common.UserStatusEnabled {
		return "", "", 0, ErrAdtyTelegramForbidden
	}
	if user.StaffSiteId > 0 && purpose != model.AdtyTelegramPurposeAdmin {
		return "", "", 0, ErrAdtyTelegramForbidden
	}
	siteForAccess := user.SiteId
	if user.StaffSiteId > 0 {
		siteForAccess = user.StaffSiteId
	}
	if siteOpen, err := adtyTelegramSiteAccess(model.DB, siteForAccess); err != nil {
		return "", "", 0, err
	} else if !siteOpen {
		return "", "", 0, ErrAdtyTelegramForbidden
	}
	if purpose == model.AdtyTelegramPurposeAdmin {
		if adminOK, err := adtyTelegramAdminAccess(model.DB, &user); err != nil {
			return "", "", 0, err
		} else if !adminOK {
			return "", "", 0, ErrAdtyTelegramForbidden
		}
	}
	var bot model.AdtyTelegramBot
	if err := model.DB.Where("purpose = ? AND enabled = ?", purpose, true).Limit(1).Find(&bot).Error; err != nil {
		return "", "", 0, err
	}
	if bot.Id == 0 {
		return "", "", 0, ErrAdtyTelegramInvalid
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", "", 0, err
	}
	code := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(code))
	now := time.Now().Unix()
	expires := now + int64((15 * time.Minute).Seconds())
	row := model.AdtyTelegramChallenge{
		UserId: user.Id, SiteId: user.SiteId, Purpose: purpose,
		CodeHash: hex.EncodeToString(sum[:]), BotIdentity: bot.IdentityHash, ExpiresAt: expires,
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		// R8-20: Expire prior unused challenges for the same (user_id, purpose)
		// so repeated bind starts cannot accumulate unbounded active rows.
		if err := tx.Model(&model.AdtyTelegramChallenge{}).
			Where("user_id = ? AND purpose = ? AND used_at = 0 AND expires_at > ?", user.Id, purpose, now).
			Update("expires_at", now).Error; err != nil {
			return err
		}
		return tx.Create(&row).Error
	}); err != nil {
		return "", "", 0, err
	}
	return code, bot.Username, expires, nil
}

func VerifyAdtyTelegramWebhook(purpose, secret string) error {
	_, err := verifyAdtyTelegramWebhookBot(purpose, secret)
	return err
}

func verifyAdtyTelegramWebhookBot(purpose, secret string) (*model.AdtyTelegramBot, error) {
	if !adtyValidTelegramPurpose(purpose) {
		return nil, ErrAdtyTelegramInvalid
	}
	var bot model.AdtyTelegramBot
	if err := model.DB.Where("purpose = ?", purpose).Limit(1).Find(&bot).Error; err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(secret))
	if bot.Id == 0 || subtle.ConstantTimeCompare([]byte(bot.WebhookHash), []byte(hex.EncodeToString(sum[:]))) != 1 {
		return nil, ErrAdtyTelegramForbidden
	}
	return &bot, nil
}

func HandleAdtyTelegramUpdate(purpose, secret string, update AdtyTelegramUpdate) (string, error) {
	bot, err := verifyAdtyTelegramWebhookBot(purpose, secret)
	if err != nil {
		return "", err
	}
	if update.Message == nil || update.Message.Chat.Type != "private" || update.Message.From.Id <= 0 {
		return adtyTelegramSavedOr("telegram_bot_reply", adtyTelegramReply), nil
	}
	text := strings.TrimSpace(update.Message.Text)
	code, ok := strings.CutPrefix(text, "/start ")
	forwarded := update.Message.ForwardFrom != nil || update.Message.ForwardOrigin != nil || update.CallbackQuery != nil
	if !ok || forwarded || strings.TrimSpace(code) == "" {
		return adtyTelegramSavedOr("telegram_bot_reply", adtyTelegramReply), nil
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(code)))
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var challenge model.AdtyTelegramChallenge
		lookup := model.AdtyLockForUpdate(tx).Where("code_hash = ? AND purpose = ?", hex.EncodeToString(hash[:]), purpose).Limit(1).Find(&challenge)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 || challenge.UsedAt != 0 || challenge.SeenAt != 0 || challenge.ExpiresAt < time.Now().Unix() || challenge.BotIdentity != bot.IdentityHash {
			return ErrAdtyTelegramInvalid
		}
		var user model.User
		if err := tx.Select("id", "site_id", "staff_site_id", "status", "role").Where("id = ?", challenge.UserId).Limit(1).Find(&user).Error; err != nil {
			return err
		}
		if user.Id == 0 || user.Status != common.UserStatusEnabled || user.SiteId != challenge.SiteId {
			return ErrAdtyTelegramForbidden
		}
		if siteOpen, err := adtyTelegramSiteAccess(tx, user.SiteId); err != nil {
			return err
		} else if !siteOpen {
			return ErrAdtyTelegramForbidden
		}
		if purpose == model.AdtyTelegramPurposeAdmin {
			if adminOK, err := adtyTelegramAdminAccess(tx, &user); err != nil {
				return err
			} else if !adminOK {
				return ErrAdtyTelegramForbidden
			}
		}
		return tx.Model(&challenge).Updates(map[string]any{
			"telegram_user_id": update.Message.From.Id, "telegram_username": update.Message.From.Username, "seen_at": time.Now().Unix(),
		}).Error
	})
	if err != nil {
		return adtyTelegramSavedOr("telegram_bot_reply", adtyTelegramReply), nil
	}
	return adtyTelegramSavedOr("telegram_bind_staged", adtyTelegramStaged), nil
}

func adtyTelegramSavedOr(key, fallback string) string {
	if text, ok := SavedAdtyBilingualNoticeText(key); ok {
		return text
	}
	return fallback
}

func adtyTelegramSiteDisplayName(siteID int) string {
	if siteID > 0 {
		if site, err := GetAdtySite(siteID); err == nil && site != nil {
			if name := strings.TrimSpace(AdtyBrandFor(site).Name); name != "" {
				return name
			}
			if sub := strings.TrimSpace(site.Subdomain); sub != "" {
				return sub
			}
		}
	}
	if name := strings.TrimSpace(common.GetSystemName()); name != "" {
		return name
	}
	return common.AdtyBrandName
}

func ConfirmAdtyTelegramBind(userId int, purpose, code string, telegramUserId int64) (*model.AdtyTelegramBinding, error) {
	if !adtyValidTelegramPurpose(purpose) {
		return nil, ErrAdtyTelegramInvalid
	}
	code = strings.TrimSpace(code)
	if telegramUserId <= 0 || len(code) < 16 {
		return nil, ErrAdtyTelegramInvalid
	}
	sum := sha256.Sum256([]byte(code))
	var binding *model.AdtyTelegramBinding
	var boundSiteID int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := model.AdtyLockForUpdate(tx).First(&user, userId).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return ErrAdtyTelegramForbidden
		}
		if user.StaffSiteId > 0 && purpose != model.AdtyTelegramPurposeAdmin {
			return ErrAdtyTelegramForbidden
		}
		siteForAccess := user.SiteId
		if user.StaffSiteId > 0 {
			siteForAccess = user.StaffSiteId
		}
		boundSiteID = siteForAccess
		if siteOpen, err := adtyTelegramSiteAccess(tx, siteForAccess); err != nil {
			return err
		} else if !siteOpen {
			return ErrAdtyTelegramForbidden
		}
		if purpose == model.AdtyTelegramPurposeAdmin {
			if adminOK, err := adtyTelegramAdminAccess(tx, &user); err != nil {
				return err
			} else if !adminOK {
				return ErrAdtyTelegramForbidden
			}
		}
		var challenge model.AdtyTelegramChallenge
		lookup := model.AdtyLockForUpdate(tx).Where("code_hash = ?", hex.EncodeToString(sum[:])).Limit(1).Find(&challenge)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 || challenge.UsedAt != 0 || challenge.ExpiresAt < time.Now().Unix() || challenge.SeenAt == 0 {
			return ErrAdtyTelegramInvalid
		}
		if challenge.UserId != user.Id || challenge.SiteId != user.SiteId || challenge.Purpose != purpose || challenge.TelegramUserId != telegramUserId {
			return ErrAdtyTelegramForbidden
		}
		var bot model.AdtyTelegramBot
		if err := tx.Where("purpose = ? AND enabled = ?", purpose, true).Limit(1).Find(&bot).Error; err != nil {
			return err
		}
		if bot.Id == 0 || bot.IdentityHash != challenge.BotIdentity {
			return ErrAdtyTelegramInvalid
		}
		row := model.AdtyTelegramBinding{
			UserId: user.Id, Purpose: purpose, SiteId: user.SiteId,
			TelegramUserId: telegramUserId, BotIdentity: challenge.BotIdentity, Status: model.AdtyTelegramBindActive,
		}
		var existing model.AdtyTelegramBinding
		found := model.AdtyLockForUpdate(tx).Where("user_id = ? AND purpose = ?", user.Id, purpose).Limit(1).Find(&existing)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected == 0 {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			binding = &row
		} else if existing.TelegramUserId != telegramUserId || existing.SiteId != user.SiteId || existing.BotIdentity != challenge.BotIdentity {
			// Prevent already-queued deliveries bound to the previous Telegram identity
			// from being rerouted to the newly bound Telegram account.
			if err := tx.Model(&model.AdtyTelegramDelivery{}).
				Where("binding_id = ? AND status IN ?", existing.Id, []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail}).
				Updates(map[string]any{
					"status":     model.AdtyTelegramDeliverySkip,
					"last_error": "binding replaced",
				}).Error; err != nil {
				return err
			}
			if err := tx.Delete(&existing).Error; err != nil {
				return err
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			binding = &row
		} else {
			existing.SiteId = user.SiteId
			existing.TelegramUserId = telegramUserId
			existing.BotIdentity = challenge.BotIdentity
			existing.Status = model.AdtyTelegramBindActive
			if err := tx.Save(&existing).Error; err != nil {
				return err
			}
			binding = &existing
		}
		return tx.Model(&challenge).Update("used_at", time.Now().Unix()).Error
	})
	if err == nil && binding != nil {
		siteName := adtyTelegramSiteDisplayName(boundSiteID)
		if _, body, renderErr := RenderAdtyBilingualNotice("telegram_bind_confirm", nil, siteName); renderErr == nil && strings.TrimSpace(body) != "" {
			_ = QueueAdtyTelegramNotice(userId, purpose, body, fmt.Sprintf("tg-bind:%d:%s:%d", userId, purpose, time.Now().UnixNano()))
		}
	}
	return binding, err
}

func GetAdtyTelegramBind(userId int, purpose string) (*AdtyTelegramBindView, error) {
	if !adtyValidTelegramPurpose(purpose) {
		return nil, ErrAdtyTelegramInvalid
	}
	var u model.User
	if userId > 0 && model.DB != nil {
		if err := model.DB.Select("id", "site_id", "staff_site_id").Where("id = ?", userId).Limit(1).Find(&u).Error; err != nil {
			return nil, err
		}
		if u.StaffSiteId > 0 && purpose != model.AdtyTelegramPurposeAdmin {
			return nil, ErrAdtyTelegramForbidden
		}
	}
	view := &AdtyTelegramBindView{Status: "unbound", Purpose: purpose}
	var row model.AdtyTelegramBinding
	lookup := model.DB.Where("user_id = ? AND purpose = ?", userId, purpose).Limit(1).Find(&row)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return view, nil
	}
	view.Status = row.Status
	// R14-031: If the user's current site or active bot identity no longer matches
	// the binding, report needs_reconfirm so the UI exposes the rebind entry point.
	if row.Status == model.AdtyTelegramBindActive {
		if u.Id > 0 && row.SiteId != u.SiteId {
			view.Status = model.AdtyTelegramBindConfirm
		} else {
			var bot model.AdtyTelegramBot
			if err := model.DB.Where("purpose = ? AND enabled = ?", purpose, true).Limit(1).Find(&bot).Error; err == nil && bot.Id != 0 && bot.IdentityHash != row.BotIdentity {
				view.Status = model.AdtyTelegramBindConfirm
			}
		}
	}
	if row.Status != "revoked" {
		view.TelegramUserId = row.TelegramUserId
	}
	return view, nil
}

func QueueAdtyTelegramNotice(userId int, purpose, body, eventKey string) error {
	if !adtyValidTelegramPurpose(purpose) {
		return ErrAdtyTelegramInvalid
	}
	body = strings.TrimSpace(body)
	if userId <= 0 || strings.TrimSpace(eventKey) == "" || body == "" || adtyTextHasOutboundSecret(body) {
		return ErrAdtyTelegramInvalid
	}
	body = adtyEnsureTelegramBilingualFallback(body)
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var existing model.AdtyTelegramDelivery
		lookup := tx.Where("event_key = ?", eventKey).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			return nil
		}
		delivery := model.AdtyTelegramDelivery{
			EventKey: eventKey, UserId: userId, Purpose: purpose, Body: body, Status: model.AdtyTelegramDeliverySkip,
		}
		var user model.User
		if err := tx.Select("id", "site_id", "staff_site_id", "status", "role").Where("id = ?", userId).Limit(1).Find(&user).Error; err != nil {
			return err
		}
		if purpose == model.AdtyTelegramPurposeUser && user.StaffSiteId > 0 {
			delivery.LastError = "staff cannot use user bot"
			return tx.Create(&delivery).Error
		}
		var binding model.AdtyTelegramBinding
		found := tx.Where("user_id = ? AND purpose = ? AND status = ?", userId, purpose, model.AdtyTelegramBindActive).Limit(1).Find(&binding)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected > 0 && user.Id > 0 && user.Status == common.UserStatusEnabled && user.SiteId == binding.SiteId {
			siteOpen, err := adtyTelegramSiteAccess(tx, binding.SiteId)
			if err != nil {
				return err
			}
			adminOK := true
			if siteOpen && purpose == model.AdtyTelegramPurposeAdmin {
				adminOK, err = adtyTelegramAdminAccess(tx, &user)
				if err != nil {
					return err
				}
			}
			if siteOpen && adminOK {
				var bot model.AdtyTelegramBot
				if err := tx.Where("purpose = ? AND enabled = ?", purpose, true).Limit(1).Find(&bot).Error; err != nil {
					return err
				}
				if bot.Id != 0 && bot.IdentityHash == binding.BotIdentity && binding.TelegramUserId > 0 {
					delivery.Status = model.AdtyTelegramDeliveryWait
					delivery.BindingId = binding.Id
					delivery.SiteId = binding.SiteId
				}
			}
		}
		return tx.Create(&delivery).Error
	})
}

// QueueAdtyPublicTelegram 把一条对外公告排进公开频道队列。
//
// channel 是目标频道（`@name` 或 `-100…`）。公开频道公告仅支持主站通过 admin bot 发送，
// admin bot 必须是目标频道的管理员；分站公告不允许推送共享公开频道，非 admin purpose 会被拒绝。
//
// 正文先脱敏再入队，不因命中敏感串就拒——和工单那四个字段同一条口径：拒单既不
// 告诉管理员哪句有问题，也挡住了正常发布。私有词（订单/退款/保证金…）仍然拒，
// 因为那些内容本来就不该出现在公开频道，脱敏解决不了「不该发」。
func QueueAdtyPublicTelegram(channel, purpose, body string) error {
	return queueAdtyPublicTelegramTx(model.DB, channel, purpose, body, "")
}

var (
	adtyPublicTelegramChannelPattern = regexp.MustCompile(`^@[A-Za-z][A-Za-z0-9_]{3,31}$`)
	adtyPublicTelegramChatIDPattern  = regexp.MustCompile(`^-100\d{5,}$`)
)

func adtyValidPublicTelegramChannel(channel string) bool {
	channel = strings.TrimSpace(channel)
	if channel == "" || strings.EqualFold(channel, adtyPrivateTelegramMarker) {
		return false
	}
	return adtyPublicTelegramChannelPattern.MatchString(channel) || adtyPublicTelegramChatIDPattern.MatchString(channel)
}

func queueAdtyPublicTelegramTx(tx *gorm.DB, channel, purpose, body, eventKey string) error {
	channel, body = strings.TrimSpace(channel), strings.TrimSpace(body)
	if !adtyValidPublicTelegramChannel(channel) || utf8.RuneCountInString(channel) > 128 || body == "" || purpose != model.AdtyTelegramPurposeAdmin {
		return ErrAdtyTelegramInvalid
	}
	if adtyTelegramPrivateWords.MatchString(body) {
		return ErrAdtyTelegramPrivate
	}
	body = adtyEnsureTelegramBilingualFallback(redactAdtySecrets(body))
	if strings.TrimSpace(eventKey) == "" {
		// EventKey 必须把 channel 算进去：同一段文字发到两个频道是两条投递，
		// 只哈希 body 会让第二条被唯一索引当成重复丢掉。
		sum := sha256.Sum256([]byte(channel + "\n" + body))
		eventKey = "public:" + hex.EncodeToString(sum[:16])
	}
	return tx.Create(&model.AdtyTelegramDelivery{
		EventKey: eventKey,
		Purpose:  purpose, Body: body, Channel: channel,
		Status: model.AdtyTelegramDeliveryWait, Public: true,
	}).Error
}

func adtyAuthorizeTelegramEventAccess(row *model.AdtyTelegramDelivery, user *model.User) (bool, error) {
	if row == nil || user == nil {
		return false, nil
	}
	key := strings.TrimSpace(row.EventKey)
	if strings.HasPrefix(key, "ticket:") {
		parts := strings.Split(key, ":")
		if len(parts) < 2 {
			return false, nil
		}
		ticketID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || ticketID <= 0 {
			return false, nil
		}
		var ticket model.AdtyTicket
		if err := model.DB.Where("id = ?", ticketID).Limit(1).Find(&ticket).Error; err != nil {
			return false, err
		}
		if ticket.Id == 0 {
			return false, nil
		}
		actor, err := loadAdtyTicketActor(user.Id)
		if err == ErrAdtyTicketForbidden || err == gorm.ErrRecordNotFound {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !actor.canRead(ticket) {
			return false, nil
		}
		if row.Purpose == model.AdtyTelegramPurposeAdmin {
			if !actor.canManage(ticket) {
				return false, nil
			}
		} else if !actor.canReadOwnTicket(ticket) && !(ticket.TeamShared && actor.TeamId == ticket.TeamId && actor.TeamRole == model.AdtyTeamRoleLeader && actor.Staff == nil && actor.User.StaffSiteId == 0) {
			return false, nil
		}
		if len(parts) >= 4 && parts[2] == "msg" {
			msgID, err := strconv.ParseInt(parts[3], 10, 64)
			if err != nil || msgID <= 0 {
				return false, nil
			}
			var msg model.AdtyTicketMessage
			if err := model.DB.Where("id = ? AND ticket_id = ?", msgID, ticket.Id).Limit(1).Find(&msg).Error; err != nil {
				return false, err
			}
			if msg.Id == 0 {
				return false, nil
			}
			if row.Purpose == model.AdtyTelegramPurposeUser && msg.Visibility != model.AdtyTicketVisibilityPublic {
				return false, nil
			}
		}
		return true, nil
	}
	if strings.HasPrefix(key, "pub:") || strings.HasPrefix(key, "notification:") {
		parts := strings.Split(key, ":")
		if len(parts) < 2 {
			return false, nil
		}
		pubID, err := strconv.Atoi(parts[1])
		if err != nil || pubID <= 0 {
			return false, nil
		}
		var pub model.AdtyNotificationPublication
		if err := model.DB.Where("id = ?", pubID).Limit(1).Find(&pub).Error; err != nil {
			return false, err
		}
		if pub.Id == 0 {
			return false, nil
		}
		if pub.Status != "published" {
			return false, nil
		}
		return adtyUserCanReadPublicationWithErr(user, &pub)
	}
	return true, nil
}

const telegramAuthorizationUnavailable = "delivery authorization unavailable"

func authorizeAndResolveAdtyTelegramDelivery(row *model.AdtyTelegramDelivery) (token string, chat string, skipReason string, failReason string) {
	if !adtyValidTelegramPurpose(row.Purpose) {
		return "", "", "invalid purpose", ""
	}
	var bot model.AdtyTelegramBot
	if err := model.DB.Where("purpose = ?", row.Purpose).Limit(1).Find(&bot).Error; err != nil {
		return "", "", "", telegramAuthorizationUnavailable
	}
	if bot.Id == 0 || !bot.Enabled {
		return "", "", "bot disabled", ""
	}
	plainToken, err := model.OpenAdtyPurposeSecret("telegram:"+row.Purpose, bot.TokenCipher)
	if err != nil || strings.TrimSpace(plainToken) == "" {
		return "", "", "", "bot token unavailable"
	}
	if row.Public {
		channel := strings.TrimSpace(row.Channel)
		if channel == "" {
			return "", "", "", "channel missing"
		}
		if row.Purpose != model.AdtyTelegramPurposeAdmin || !adtyValidPublicTelegramChannel(channel) {
			return "", "", "invalid public channel", ""
		}
		if strings.HasPrefix(row.EventKey, "pub:") {
			parts := strings.Split(row.EventKey, ":")
			if len(parts) >= 2 {
				if pubID, err := strconv.Atoi(parts[1]); err == nil && pubID > 0 {
					var pub model.AdtyNotificationPublication
					if err := model.DB.Where("id = ?", pubID).Limit(1).Find(&pub).Error; err != nil {
						return "", "", "", telegramAuthorizationUnavailable
					}
					if pub.Id == 0 || pub.Status != "published" {
						return "", "", "publication withdrawn", ""
					}
					if pub.IssuerSiteId > 0 {
						return "", "", "subsite public channel unsupported", ""
					}
					if pub.Audience == "user" || pub.Audience == "team" {
						return "", "", "publication audience restricted", ""
					}
					if strings.TrimSpace(pub.TelegramChannel) != channel {
						return "", "", "publication channel changed", ""
					}
				}
			}
		}
		return plainToken, channel, "", ""
	}
	if row.BindingId <= 0 || row.UserId <= 0 {
		return "", "", "binding missing", ""
	}
	var binding model.AdtyTelegramBinding
	if err := model.DB.Where("id = ?", row.BindingId).Limit(1).Find(&binding).Error; err != nil {
		return "", "", "", telegramAuthorizationUnavailable
	}
	if binding.Id == 0 {
		return "", "", "binding not found", ""
	}
	if binding.Status != model.AdtyTelegramBindActive ||
		binding.UserId != row.UserId ||
		binding.Purpose != row.Purpose ||
		binding.SiteId != row.SiteId ||
		binding.BotIdentity == "" ||
		binding.BotIdentity != bot.IdentityHash ||
		binding.TelegramUserId <= 0 {
		return "", "", "binding inactive or mismatched", ""
	}
	var user model.User
	if err := model.DB.Select("id", "site_id", "staff_site_id", "status", "role").Where("id = ?", row.UserId).Limit(1).Find(&user).Error; err != nil {
		return "", "", "", telegramAuthorizationUnavailable
	}
	if user.Id == 0 {
		return "", "", "user not found", ""
	}
	if user.Status != common.UserStatusEnabled {
		return "", "", "user disabled or site changed", ""
	}
	if row.Purpose == model.AdtyTelegramPurposeUser && user.StaffSiteId > 0 {
		return "", "", "staff cannot use user bot", ""
	}
	if user.SiteId != binding.SiteId || user.SiteId != row.SiteId {
		return "", "", "user disabled or site changed", ""
	}
	if open, err := adtyTelegramSiteAccess(model.DB, binding.SiteId); err != nil {
		return "", "", "", telegramAuthorizationUnavailable
	} else if !open {
		return "", "", "site not active", ""
	}
	if row.Purpose == model.AdtyTelegramPurposeAdmin {
		ok, err := adtyTelegramAdminAccess(model.DB, &user)
		if err != nil {
			return "", "", "", telegramAuthorizationUnavailable
		}
		if !ok {
			return "", "", "admin permission revoked", ""
		}
	}
	if ok, err := adtyAuthorizeTelegramEventAccess(row, &user); err != nil {
		return "", "", "", telegramAuthorizationUnavailable
	} else if !ok {
		return "", "", "business access revoked", ""
	}
	return plainToken, strconv.FormatInt(binding.TelegramUserId, 10), "", ""
}

func RetryAdtyTelegram() (int, error) {
	RecoverAdtyNotificationTelegramOutbox()
	now := time.Now().Unix()
	var rows []model.AdtyTelegramDelivery
	if err := model.DB.
		Where("status IN ? AND (next_retry_at = 0 OR next_retry_at <= ?)", []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail}, now).
		Order("id ASC").
		Limit(20).
		Find(&rows).Error; err != nil {
		return 0, err
	}
	n := 0
	for _, row := range rows {
		// R6-032: Atomically claim a 120s lease before invoking the external Telegram API
		// so multi-replica retries never double-send the same delivery row.
		claimNow := time.Now().Unix()
		claim := model.DB.Model(&model.AdtyTelegramDelivery{}).
			Where("id = ? AND status = ? AND attempts = ? AND (next_retry_at = 0 OR next_retry_at <= ?)",
				row.Id, row.Status, row.Attempts, claimNow).
			Update("next_retry_at", claimNow+120)
		if claim.Error != nil {
			return n, claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}

		status := model.AdtyTelegramDeliveryFail
		last := "sender unavailable"
		if AdtyTelegramSender != nil {
			token, chat, skipReason, failReason := authorizeAndResolveAdtyTelegramDelivery(&row)
			switch {
			case skipReason != "":
				status = model.AdtyTelegramDeliverySkip
				last = skipReason
			case failReason != "":
				status = model.AdtyTelegramDeliveryFail
				last = failReason
			default:
				if err := AdtyTelegramSender(token, chat, row.Body); err == nil {
					status, last = model.AdtyTelegramDeliverySent, ""
				} else if row.Public {
					last = "channel send failed"
				} else {
					last = "private send failed"
				}
			}
		}
		attempts := row.Attempts + 1
		nextRetryAt := int64(0)
		if last == telegramAuthorizationUnavailable {
			attempts = row.Attempts
			nextRetryAt = time.Now().Unix() + 60
		} else if status != model.AdtyTelegramDeliverySent && status != model.AdtyTelegramDeliverySkip {
			if attempts >= 5 {
				status = model.AdtyTelegramDeliveryDead
			} else {
				shift := attempts - 1
				if shift < 0 {
					shift = 0
				}
				if shift > 4 {
					shift = 4
				}
				nextRetryAt = time.Now().Unix() + int64(60<<shift)
			}
		}
		upd := model.DB.Model(&model.AdtyTelegramDelivery{}).Where("id = ?", row.Id)
		if status != model.AdtyTelegramDeliverySent {
			upd = upd.Where("status IN ?", []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail})
		}
		if err := upd.Updates(map[string]any{
			"status":        status,
			"attempts":      attempts,
			"last_error":    last,
			"next_retry_at": nextRetryAt,
		}).Error; err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func adtyTelegramView(purpose string) (AdtyTelegramBotView, error) {
	return adtyTelegramViewTx(model.DB, purpose)
}

func adtyTelegramViewTx(tx *gorm.DB, purpose string) (AdtyTelegramBotView, error) {
	view := AdtyTelegramBotView{Purpose: purpose}
	var row model.AdtyTelegramBot
	if err := tx.Where("purpose = ?", purpose).Limit(1).Find(&row).Error; err != nil {
		return view, err
	}
	if row.Id == 0 {
		return view, nil
	}
	var pending int64
	if err := tx.Model(&model.AdtyTelegramDelivery{}).Where("purpose = ? AND status IN ?", purpose, []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail}).Count(&pending).Error; err != nil {
		return view, err
	}
	view.BotId = row.BotId
	view.Username = row.Username
	view.TokenMask = row.TokenMask
	view.Enabled = row.Enabled
	view.IdentityVerified = row.IdentityVerified
	view.SavedAt = row.SavedAt
	view.TestedAt = row.TestedAt
	view.Pending = pending
	view.WebhookConfigured = row.WebhookHash != ""
	return view, nil
}

func adtyMaskTail(value string) string {
	runes := []rune(value)
	if len(runes) <= 4 {
		return "••••"
	}
	return "••••" + string(runes[len(runes)-4:])
}

func mustOpenTelegramToken(row model.AdtyTelegramBot) string {
	plain, err := model.OpenAdtyPurposeSecret("telegram:"+row.Purpose, row.TokenCipher)
	if err != nil {
		return ""
	}
	return plain
}

const adtyTelegramMaxMessageUnits = 3800

func adtyTelegramRuneUnits(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

func adtyTelegramTextUnits(text string) int {
	units := 0
	for _, r := range text {
		units += adtyTelegramRuneUnits(r)
	}
	return units
}

// splitAdtyTelegramMessage splits text into sequential chunks whose UTF-16 code unit
// count stays within maxUnits (safely below Telegram's 4096-character sendMessage limit),
// preferring paragraph breaks ("\n\n"), then line breaks ("\n"), and finally rune boundaries.
func splitAdtyTelegramMessage(text string, maxUnits int) []string {
	if text == "" {
		return nil
	}
	if maxUnits <= 0 {
		maxUnits = adtyTelegramMaxMessageUnits
	}
	if adtyTelegramTextUnits(text) <= maxUnits {
		return []string{text}
	}
	chunks := make([]string, 0, 2)
	rem := text
	for rem != "" {
		if adtyTelegramTextUnits(rem) <= maxUnits {
			if strings.TrimSpace(rem) != "" {
				chunks = append(chunks, rem)
			}
			break
		}
		limitByte := 0
		units := 0
		for limitByte < len(rem) {
			r, size := utf8.DecodeRuneInString(rem[limitByte:])
			u := adtyTelegramRuneUnits(r)
			if limitByte > 0 && units+u > maxUnits {
				break
			}
			units += u
			limitByte += size
		}
		if limitByte <= 0 || limitByte >= len(rem) {
			if strings.TrimSpace(rem) != "" {
				chunks = append(chunks, rem)
			}
			break
		}
		window := rem[:limitByte]
		rest := rem[limitByte:]
		var chunk string
		var next string

		switch {
		case strings.HasPrefix(rest, "\n\n") && strings.TrimSpace(window) != "":
			chunk = strings.TrimRight(window, "\n")
			next = strings.TrimLeft(rest[2:], "\n")
		case strings.HasSuffix(window, "\n") && strings.HasPrefix(rest, "\n") && strings.TrimSpace(window[:len(window)-1]) != "":
			chunk = strings.TrimRight(window[:len(window)-1], "\n")
			next = strings.TrimLeft(rest[1:], "\n")
		default:
			if pos := strings.LastIndex(window, "\n\n"); pos > 0 && strings.TrimSpace(window[:pos]) != "" {
				chunk = strings.TrimRight(window[:pos], "\n")
				next = strings.TrimLeft(rem[pos+2:], "\n")
			} else if strings.HasPrefix(rest, "\n") && strings.TrimSpace(window) != "" {
				chunk = window
				next = rest[1:]
			} else if pos := strings.LastIndex(window, "\n"); pos > 0 && strings.TrimSpace(window[:pos]) != "" {
				chunk = window[:pos]
				next = rem[pos+1:]
			} else {
				chunk = window
				next = rest
			}
		}

		if strings.TrimSpace(chunk) != "" {
			chunks = append(chunks, chunk)
		}
		rem = next
	}
	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

// adtyTelegramSendMessageHTTP 发一条消息到 chat（`@name` 或数字 id）。
// 只在 ADTY_TELEGRAM_ALLOW_NETWORK=1 时被装上，测试里永远是 nil。
// 超过 Telegram 4096 字符安全边界时自动按段落、换行或 rune 边界拆分为多段顺序发送。
func adtyTelegramSendMessageHTTP(token, chat, text string) error {
	chunks := splitAdtyTelegramMessage(text, adtyTelegramMaxMessageUnits)
	if len(chunks) == 0 {
		return adtyTelegramSendSingleMessageHTTP(token, chat, text)
	}
	for _, chunk := range chunks {
		if err := adtyTelegramSendSingleMessageHTTP(token, chat, chunk); err != nil {
			return err
		}
	}
	return nil
}

func adtyTelegramSendSingleMessageHTTP(token, chat, text string) error {
	payload := url.Values{}
	payload.Set("chat_id", chat)
	payload.Set("text", text)
	payload.Set("disable_web_page_preview", "true")
	endpoint := adtyTelegramAPIBase + "/bot" + token + "/sendMessage"
	response, err := adtyTelegramHTTP.PostForm(endpoint, payload)
	if err != nil {
		// R2-026: Never return raw *url.Error which embeds /bot<TOKEN>/sendMessage.
		return common.NewMessage("Telegram send failed")
	}
	defer response.Body.Close()
	var body struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if response.StatusCode != http.StatusOK {
		return common.NewMessage("Telegram API returned HTTP {{status}}", map[string]any{"status": response.StatusCode})
	}
	if err := common.DecodeJson(io.LimitReader(response.Body, 1<<20), &body); err != nil {
		return common.NewMessage("Invalid Telegram API response")
	}
	if !body.Ok {
		if strings.TrimSpace(body.Description) != "" {
			return common.NewMessage("Telegram sendMessage rejected: {{error}}", map[string]any{"error": body.Description})
		}
		return common.NewMessage("Telegram send failed")
	}
	return nil
}

// startAdtyTelegramDeliveryTask 定期把待发/失败的投递重试出去。
//
// `RetryAdtyTelegram` 在此之前零生产调用方：入队的行没有任何东西去消费，
// 队列只增不减。这条任务就是它的出口。
var (
	adtyTelegramDeliveryMu   sync.Mutex
	adtyTelegramDeliveryStop chan struct{}
	adtyTelegramDeliveryDone chan struct{}
)

func startAdtyTelegramDeliveryTask() {
	// 没有发送实现时，这条任务只会把队列一路推到 dead，而且全程没有错误。
	// 「开关没打开」和「没有消息要发」在外部看起来完全一样，所以必须喊一声。
	//
	// **判据不能只看 sender 为 nil。** 一个本来就不用 Telegram 的部署（本地开发、
	// 不带通知的单体部署、CI）永远是 nil，那样每次启动都会打一条 ERROR——
	// 后果不是多一行日志，是**这条日志从此不被读**。
	// 所以只在「库里真的配了 bot」时才喊：那才是「配了但发不出去」。
	if AdtyTelegramSender == nil && adtyTelegramHasConfiguredBots() {
		common.SysError(common.LogText(
			"telegram delivery is disabled but a bot is configured: ADTY_TELEGRAM_ALLOW_NETWORK is not \"1\", so no message can leave this process; queued rows will be marked failed and eventually dead"))
	}
	adtyTelegramDeliveryMu.Lock()
	defer adtyTelegramDeliveryMu.Unlock()
	if adtyTelegramDeliveryStop != nil {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	adtyTelegramDeliveryStop = stop
	adtyTelegramDeliveryDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if _, err := RetryAdtyTelegram(); err != nil {
					common.SysError(common.LogText("telegram delivery task: %s", err.Error()))
				}
			}
		}
	}()
}

// stopAdtyTelegramDeliveryTask stops the retry loop started by tests.
// Production leaves the task running for the process lifetime.
func stopAdtyTelegramDeliveryTask() {
	adtyTelegramDeliveryMu.Lock()
	stop := adtyTelegramDeliveryStop
	done := adtyTelegramDeliveryDone
	adtyTelegramDeliveryStop = nil
	adtyTelegramDeliveryDone = nil
	adtyTelegramDeliveryMu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtyTelegramDeliveryTask)
}

func adtyTelegramGetMeHTTP(token string) (int64, string, error) {
	request, err := http.NewRequest(http.MethodGet, adtyTelegramAPIBase+"/bot"+token+"/getMe", nil)
	if err != nil {
		return 0, "", ErrAdtyTelegramUnverified
	}
	response, err := adtyTelegramInteractiveHTTP.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil {
			_ = response.Body.Close()
		}
		return 0, "", ErrAdtyTelegramUnverified
	}
	defer response.Body.Close()
	var body struct {
		Ok     bool `json:"ok"`
		Result struct {
			Id       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"result"`
	}
	if common.DecodeJson(io.LimitReader(response.Body, 1<<20), &body) != nil || !body.Ok || body.Result.Id <= 0 {
		return 0, "", ErrAdtyTelegramUnverified
	}
	return body.Result.Id, body.Result.Username, nil
}

// adtyTelegramHasConfiguredBots 说明库里是否真的配过「启用且带 token」的 bot。
//
// 它只回答一个问题：**这个部署本来是想发 Telegram 的吗？** 不是的话，
// 「sender 为 nil」是正常状态，不该报警。
//
// 库不可用时返回 false —— 启动阶段库还没好是常态，那种情况下打一条
// 「Telegram 没配」是噪音，而真正的连接问题有它自己的日志。
func adtyTelegramHasConfiguredBots() bool {
	if model.DB == nil {
		return false
	}
	var count int64
	err := model.DB.Model(&model.AdtyTelegramBot{}).
		Where("enabled = ? AND token_cipher <> ?", true, "").
		Count(&count).Error
	if err != nil {
		return false
	}
	return count > 0
}
