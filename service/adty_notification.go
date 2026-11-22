package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type AdtyNotificationInbox struct {
	Notice             string           `json:"notice"`
	NoticeKey          string           `json:"notice_key"`
	Announcements      []map[string]any `json:"announcements"`
	ReadKeys           []string         `json:"read_keys"`
	NextTicketBeforeId int64            `json:"next_ticket_before_id"`
	NextBeforeId       int              `json:"next_before_id"`
	UnreadPublications int64            `json:"unread_publications"`
}

type AdtyNotificationInput struct {
	Title        string `json:"title"`
	Content      string `json:"content"`
	Kind         string `json:"kind"`
	Audience     string `json:"audience"`
	TargetSiteId int    `json:"target_site_id"`
	TargetTeamId int64  `json:"target_team_id"`
	TargetUserId int    `json:"target_user_id"`
	Business     string `json:"business"`
	Revision     int64  `json:"revision"`
	// SyncTelegram 勾了才把这条公告排进 TG 队列；TelegramChannel 是目标频道
	// （`@name` 或 `-100…`）。都不填就是只发站内。
	SyncTelegram    bool   `json:"sync_telegram"`
	TelegramChannel string `json:"telegram_channel"`
	// Draft 表示「先存着，别发」。草稿不出现任何读者面前、不推 TG；
	// 编辑草稿时把它改成 false 就是发布。已发布的公告不能再退回草稿
	// （撤回才是那个动作），见下面的判断。
	Draft bool `json:"draft"`
}

const adtyPrivateTelegramMarker = "@private"

func ListAdtyManagedNotifications(issuerSiteID, start, limit int) ([]model.AdtyNotificationPublication, int64, error) {
	query := model.DB.Model(&model.AdtyNotificationPublication{}).Where("issuer_site_id = ?", issuerSiteID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := []model.AdtyNotificationPublication{}
	err := query.Order("id DESC").Offset(start).Limit(limit).Find(&items).Error
	return items, total, err
}

func SaveAdtyNotificationPublication(actorID int, issuer *model.AdtySite, id int, input AdtyNotificationInput, withdraw bool) (*model.AdtyNotificationPublication, error) {
	issuerID := 0
	if issuer != nil {
		issuerID = issuer.Id
		if issuer.Status == model.AdtySiteStatusClosing {
			return nil, ErrAdtySiteClosing
		}
	}
	row := model.AdtyNotificationPublication{IssuerSiteId: issuerID}
	currentStatus := ""
	if id > 0 {
		if err := model.DB.Where("id = ? AND issuer_site_id = ?", id, issuerID).First(&row).Error; err != nil {
			return nil, err
		}
		currentStatus = row.Status
		if input.Revision <= 0 {
			return nil, common.NewMessage("Invalid parameters")
		}
		if currentStatus == "withdrawn" {
			// 撤回的公告是历史记录，不是可编辑对象。
			return nil, common.NewMessage("Invalid parameters")
		}
		if currentStatus == "published" && input.Draft {
			// 已发布的不能退回草稿：那是「悄无声息地把它从所有人眼前拿走」，
			// 而撤回这个动作是显式且有历史的。
			return nil, common.NewMessage("Published announcements cannot be turned back into drafts")
		}
	} else {
		if withdraw || input.Revision != 0 {
			return nil, common.NewMessage("Invalid parameters")
		}
		var publisher model.User
		if err := model.DB.First(&publisher, actorID).Error; err != nil {
			return nil, err
		}
		row.PublisherId, row.PublisherName = actorID, publisher.Username
		row.Revision = 1
		// 草稿没有「发布时间」：它还没发。0 在界面上就是「还没发」。
		if !input.Draft {
			row.PublishedAt = time.Now().Unix()
		}
	}
	row.UpdatedAt, row.UpdatedBy = time.Now().Unix(), actorID
	if withdraw {
		row.Status = "withdrawn"
		return &row, model.UpdateAdtyNotificationPublication(&row, input.Revision)
	}
	input.Title, input.Content, input.Business = strings.TrimSpace(input.Title), strings.TrimSpace(input.Content), strings.TrimSpace(input.Business)
	sourceContent := normalizeAdtyNotificationSourceContent(row.Id, input.Content)
	// 草稿允许空标题空正文——「先存着、回头再写」正是它存在的理由，要求填满就等于
	// 没有草稿。但长度上限和类别/受众枚举始终要校验：那些是存储约束，不是完整度。
	if (!input.Draft && (input.Title == "" || sourceContent == "")) ||
		utf8.RuneCountInString(input.Title) > 120 || utf8.RuneCountInString(sourceContent) > 10000 || utf8.RuneCountInString(input.Business) > 200 ||
		!slices.Contains([]string{"platform", "system", "maintenance", "rules"}, input.Kind) ||
		!slices.Contains([]string{"all_users", "all_sites", "site", "team", "user"}, input.Audience) {
		return nil, common.NewMessage("Invalid parameters")
	}

	targetSiteID, targetSiteName, err := resolveAdtyNotificationTarget(issuer, issuerID, input)
	if err != nil {
		return nil, err
	}

	row.Title, row.Content, row.Business = input.Title, sourceContent, input.Business
	row.Kind, row.Audience, row.TargetSiteId, row.TargetSiteName = input.Kind, input.Audience, targetSiteID, targetSiteName
	row.Status = "published"
	if input.Draft {
		row.Status = "draft"
	}
	// 第一次从草稿变成发布时，发布时间才是「现在」。之后编辑已发布的公告
	// 不改它——那会把一条老公告在首页顶到最新。
	if row.Status == "published" && row.PublishedAt == 0 {
		row.PublishedAt = time.Now().Unix()
	}
	row.ReadKey = adtyNotificationVersion("publication:" + common.GetUUID())
	// 不勾同步就把频道一起清掉：留着上一轮的频道名，下次勾上会安静地发到
	// 一个没人预期的目标。
	if input.SyncTelegram {
		if row.Audience == "team" || row.Audience == "user" {
			row.TelegramChannel = adtyPrivateTelegramMarker
		} else {
			row.TelegramChannel = strings.TrimSpace(input.TelegramChannel)
		}
	} else {
		row.TelegramChannel = ""
	}
	if id > 0 {
		if err := model.UpdateAdtyNotificationPublication(&row, input.Revision); err != nil {
			return nil, err
		}
	} else if err := model.DB.Create(&row).Error; err != nil {
		return nil, err
	}
	if row.Status == "published" {
		produceAdtySevereTodosForPublished(&row)
		applyAdtyNotificationTranslation(&row, sourceContent, issuerID)
		pushAdtyNotificationToTelegram(&row, issuer)
	}
	return &row, nil
}

func resolveAdtyNotificationTarget(issuer *model.AdtySite, issuerID int, input AdtyNotificationInput) (int, string, error) {
	switch input.Audience {
	case "all_users", "all_sites":
		if issuer != nil || input.TargetSiteId != 0 || input.TargetTeamId != 0 || input.TargetUserId != 0 {
			return 0, "", common.NewMessage("Invalid parameters")
		}
		return 0, "", nil
	case "site":
		if input.TargetSiteId <= 0 || input.TargetTeamId != 0 || input.TargetUserId != 0 {
			return 0, "", common.NewMessage("Invalid parameters")
		}
		if issuer != nil && input.TargetSiteId != issuerID {
			return 0, "", common.NewMessage("Invalid parameters")
		}
		var target model.AdtySite
		if err := model.DB.Where("id = ? AND status IN ?", input.TargetSiteId, []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused}).First(&target).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return 0, "", common.NewMessage("Invalid parameters")
			}
			return 0, "", err
		}
		return input.TargetSiteId, AdtySiteDisplayHost(&target), nil
	case "team":
		if input.TargetTeamId <= 0 || input.TargetUserId != 0 || input.TargetSiteId < 0 {
			return 0, "", common.NewMessage("Invalid parameters")
		}
		if !model.DB.Migrator().HasTable(&model.AdtyTeam{}) {
			return 0, "", common.NewMessage("Invalid parameters")
		}
		var team model.AdtyTeam
		if err := model.DB.Where("id = ? AND status = ?", input.TargetTeamId, model.AdtyTeamStatusActive).First(&team).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return 0, "", common.NewMessage("Invalid parameters")
			}
			return 0, "", err
		}
		if issuer != nil {
			if team.SiteId != issuerID || (input.TargetSiteId != 0 && input.TargetSiteId != issuerID) {
				return 0, "", common.NewMessage("Invalid parameters")
			}
		} else {
			if input.TargetSiteId > 0 && team.SiteId != input.TargetSiteId {
				return 0, "", common.NewMessage("Invalid parameters")
			}
			if team.SiteId > 0 {
				var targetSite model.AdtySite
				if err := model.DB.Where("id = ? AND status IN ?", team.SiteId, []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused}).First(&targetSite).Error; err != nil {
					if err == gorm.ErrRecordNotFound {
						return 0, "", common.NewMessage("Invalid parameters")
					}
					return 0, "", err
				}
			}
		}
		return team.SiteId, fmt.Sprintf("team:%d", team.Id), nil
	case "user":
		if input.TargetUserId <= 0 || input.TargetTeamId != 0 || input.TargetSiteId < 0 {
			return 0, "", common.NewMessage("Invalid parameters")
		}
		var targetUser model.User
		if err := model.DB.Select("id", "username", "site_id", "staff_site_id", "status").
			Where("id = ? AND status = ? AND staff_site_id = 0", input.TargetUserId, common.UserStatusEnabled).
			First(&targetUser).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return 0, "", common.NewMessage("Invalid parameters")
			}
			return 0, "", err
		}
		if model.DB.Migrator().HasTable(&model.AdtySiteStaff{}) {
			var staffCount int64
			if err := model.DB.Model(&model.AdtySiteStaff{}).Where("user_id = ?", targetUser.Id).Count(&staffCount).Error; err != nil {
				return 0, "", err
			}
			if staffCount > 0 {
				return 0, "", common.NewMessage("Invalid parameters")
			}
		}
		if issuer != nil {
			if targetUser.SiteId != issuerID || (input.TargetSiteId != 0 && input.TargetSiteId != issuerID) {
				return 0, "", common.NewMessage("Invalid parameters")
			}
		} else {
			if input.TargetSiteId > 0 && targetUser.SiteId != input.TargetSiteId {
				return 0, "", common.NewMessage("Invalid parameters")
			}
			if targetUser.SiteId > 0 {
				var targetSite model.AdtySite
				if err := model.DB.Where("id = ? AND status IN ?", targetUser.SiteId, []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused}).First(&targetSite).Error; err != nil {
					if err == gorm.ErrRecordNotFound {
						return 0, "", common.NewMessage("Invalid parameters")
					}
					return 0, "", err
				}
			}
		}
		return targetUser.SiteId, fmt.Sprintf("user:%d", targetUser.Id), nil
	default:
		return 0, "", common.NewMessage("Invalid parameters")
	}
}

func adtyNotificationTranslationKey(id int) string {
	return fmt.Sprintf("notification:%d", id)
}

func adtyContainsChinese(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func normalizeAdtyNotificationSourceContent(pubID int, content string) string {
	return normalizeAdtyNotificationSourceContentWithDB(model.DB, pubID, content)
}

func normalizeAdtyNotificationSourceContentWithDB(db *gorm.DB, pubID int, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if idx := strings.LastIndex(content, "\n\n"+AdtyTranslationNotReady); idx >= 0 {
		content = strings.TrimSpace(content[:idx])
	}
	if pubID > 0 && db != nil && db.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		var head model.AdtyTranslationHead
		if db.Where("object_key = ?", adtyNotificationTranslationKey(pubID)).Limit(1).Find(&head).RowsAffected > 0 {
			if head.English != "" {
				suffix := "\n\n" + strings.TrimSpace(head.English)
				if strings.HasSuffix(content, suffix) {
					content = strings.TrimSpace(strings.TrimSuffix(content, suffix))
				}
			}
		}
	}
	return content
}

func ensureAdtyTranslationTables() {
	if model.DB == nil {
		return
	}
	if !model.DB.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		_ = model.DB.AutoMigrate(
			&model.AdtyTranslationConfig{},
			&model.AdtyTranslationHead{},
			&model.AdtyTranslationJob{},
			&model.AdtyTranslationUsage{},
			&model.AdtyGlossaryTerm{},
		)
	}
}

func applyAdtyNotificationTranslation(row *model.AdtyNotificationPublication, sourceContent string, siteID int) {
	if row == nil || row.Id <= 0 || model.DB == nil {
		return
	}
	sourceText := strings.TrimSpace(sourceContent)
	if !adtyContainsChinese(sourceText) && !adtyContainsChinese(row.Title) {
		return
	}
	if !adtyContainsChinese(sourceText) {
		sourceText = strings.TrimSpace(row.Title)
	}
	ensureAdtyTranslationTables()
	objectKey := adtyNotificationTranslationKey(row.Id)
	res, err := TranslateAdtyDynamic(objectKey, sourceText, siteID)
	if err != nil {
		common.SysError(common.LogText("translate notification %d failed: %s", row.Id, err.Error()))
	}
	bilingual := sourceContent
	if res != nil && res.Status == model.AdtyTranslationReady && strings.TrimSpace(res.English) != "" {
		eng := strings.TrimSpace(res.English)
		if !strings.Contains(bilingual, eng) {
			bilingual = sourceContent + "\n\n" + eng
		}
	} else {
		if !strings.Contains(bilingual, AdtyTranslationNotReady) {
			bilingual = sourceContent + "\n\n" + AdtyTranslationNotReady
		}
	}
	if bilingual != row.Content {
		if err := model.DB.Model(&model.AdtyNotificationPublication{}).Where("id = ?", row.Id).Update("content", bilingual).Error; err == nil {
			row.Content = bilingual
		}
	}
}

// 公告入队的 EventKey 绑公告 id，不绑正文。正文一改哈希就变，
// 「队列里有没有这条公告」会被误判成没有，编辑就再推一条。
func adtyPublicNotificationEventKey(id int) string {
	return fmt.Sprintf("pub:%d:public", id)
}

func adtyNotificationPrivateRecipientIDs(tx *gorm.DB, row *model.AdtyNotificationPublication) ([]int, error) {
	if row == nil {
		return nil, nil
	}
	switch row.Audience {
	case "user":
		uidStr, ok := strings.CutPrefix(row.TargetSiteName, "user:")
		if !ok {
			return nil, nil
		}
		uid, err := strconv.Atoi(uidStr)
		if err != nil || uid <= 0 {
			return nil, nil
		}
		return []int{uid}, nil
	case "team":
		tidStr, ok := strings.CutPrefix(row.TargetSiteName, "team:")
		if !ok {
			return nil, nil
		}
		tid, err := strconv.ParseInt(tidStr, 10, 64)
		if err != nil || tid <= 0 {
			return nil, nil
		}
		if !tx.Migrator().HasTable(&model.AdtyTeamMember{}) {
			return nil, nil
		}
		var userIDs []int
		if err := tx.Model(&model.AdtyTeamMember{}).
			Where("team_id = ? AND status = ?", tid, model.AdtyTeamMemberActive).
			Order("user_id").Pluck("user_id", &userIDs).Error; err != nil {
			return nil, err
		}
		return userIDs, nil
	default:
		return nil, nil
	}
}

func queueAdtyPrivatePublicationTelegramTx(tx *gorm.DB, row *model.AdtyNotificationPublication) error {
	userIDs, err := adtyNotificationPrivateRecipientIDs(tx, row)
	if err != nil {
		return err
	}
	body := strings.TrimSpace(redactAdtySecrets(announceTelegramBodyWithDB(tx, *row)))
	if body == "" {
		return nil
	}
	if !strings.Contains(body, "\n\n") {
		body += "\n\nEnglish is not ready"
	}
	purpose := model.AdtyTelegramPurposeUser
	for _, uid := range userIDs {
		eventKey := fmt.Sprintf("pub:%d:user:%d", row.Id, uid)
		var existing model.AdtyTelegramDelivery
		if tx.Where("event_key = ?", eventKey).Limit(1).Find(&existing).RowsAffected > 0 {
			continue
		}
		delivery := model.AdtyTelegramDelivery{
			EventKey: eventKey,
			UserId:   uid,
			Purpose:  purpose,
			Body:     body,
			Status:   model.AdtyTelegramDeliverySkip,
		}
		var user model.User
		_ = tx.Select("id", "site_id", "staff_site_id", "status", "role").Where("id = ?", uid).Limit(1).Find(&user).Error
		var binding model.AdtyTelegramBinding
		found := tx.Where("user_id = ? AND purpose = ? AND status = ?", uid, purpose, model.AdtyTelegramBindActive).Limit(1).Find(&binding)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected > 0 && user.Id > 0 && user.Status == common.UserStatusEnabled && user.SiteId == binding.SiteId && adtySiteIsOpen(tx, binding.SiteId) {
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
		if err := tx.Create(&delivery).Error; err != nil {
			return err
		}
	}
	return nil
}

func adtyNotificationHasTelegramOutbox(tx *gorm.DB, row *model.AdtyNotificationPublication) bool {
	if row == nil || row.Id <= 0 {
		return true
	}
	if row.Audience == "team" || row.Audience == "user" {
		userIDs, err := adtyNotificationPrivateRecipientIDs(tx, row)
		if err != nil || len(userIDs) == 0 {
			return true
		}
		for _, uid := range userIDs {
			var count int64
			eventKey := fmt.Sprintf("pub:%d:user:%d", row.Id, uid)
			if tx.Model(&model.AdtyTelegramDelivery{}).Where("event_key = ?", eventKey).Count(&count).Error == nil && count == 0 {
				return false
			}
		}
		return true
	}
	channel := strings.TrimSpace(row.TelegramChannel)
	if channel == "" {
		return true
	}
	var count int64
	if err := tx.Model(&model.AdtyTelegramDelivery{}).Where("event_key = ?", adtyPublicNotificationEventKey(row.Id)).Count(&count).Error; err != nil {
		return true
	}
	return count > 0
}

// RecoverAdtyNotificationTelegramOutbox inspects published notifications that
// requested Telegram sync and recovers any missing outbox rows if an earlier
// attempt crashed or left telegram_at out of sync with adty_telegram_deliveries.
func RecoverAdtyNotificationTelegramOutbox() {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyNotificationPublication{}) || !model.DB.Migrator().HasTable(&model.AdtyTelegramDelivery{}) {
		return
	}
	var pubs []model.AdtyNotificationPublication
	if err := model.DB.Where("status = ? AND telegram_channel <> ?", "published", "").Order("id").Find(&pubs).Error; err != nil {
		return
	}
	for i := range pubs {
		pub := &pubs[i]
		if adtyNotificationHasTelegramOutbox(model.DB, pub) {
			continue
		}
		var issuer *model.AdtySite
		if pub.IssuerSiteId > 0 {
			var site model.AdtySite
			if model.DB.Where("id = ?", pub.IssuerSiteId).Limit(1).Find(&site).RowsAffected > 0 {
				issuer = &site
			} else {
				issuer = &model.AdtySite{Id: pub.IssuerSiteId}
			}
		}
		enqueueAdtyNotificationToTelegram(pub, issuer, true)
	}
}

// pushAdtyNotificationToTelegram 尝试把公告推一次到频道（或私聊队列），并保证
// `telegram_at` 占位与投递队列写入在同一个数据库事务内原子提交。
func pushAdtyNotificationToTelegram(row *model.AdtyNotificationPublication, issuer *model.AdtySite) {
	enqueueAdtyNotificationToTelegram(row, issuer, false)
}

func enqueueAdtyNotificationToTelegram(row *model.AdtyNotificationPublication, issuer *model.AdtySite, allowRecovery bool) {
	if row == nil || model.DB == nil {
		return
	}
	channel := strings.TrimSpace(row.TelegramChannel)
	if channel == "" {
		return // 没配频道就不发，也不报错
	}
	purpose := model.AdtyTelegramPurposeAdmin
	if issuer != nil || row.IssuerSiteId > 0 {
		purpose = model.AdtyTelegramPurposeUser
	}
	at := time.Now().Unix()
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.AdtyNotificationPublication{}).
			Where("id = ? AND telegram_at = 0", row.Id).
			Update("telegram_at", at)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// The slot is already taken. Later edits must not enqueue another
			// message just because the body changed. Recovery may fill a missing
			// outbox row, and only that.
			if !allowRecovery || adtyNotificationHasTelegramOutbox(tx, row) {
				return nil
			}
		}
		if row.Audience == "team" || row.Audience == "user" {
			return queueAdtyPrivatePublicationTelegramTx(tx, row)
		}
		return queueAdtyPublicTelegramTx(tx, channel, purpose, announceTelegramBodyWithDB(tx, *row), adtyPublicNotificationEventKey(row.Id))
	})
	if err != nil {
		row.TelegramAt = 0
		common.SysError(common.LogText("queue announcement %d for telegram failed: %s", row.Id, err.Error()))
		return
	}
	var refreshed model.AdtyNotificationPublication
	if model.DB.Select("telegram_at").Where("id = ?", row.Id).Limit(1).Find(&refreshed).RowsAffected > 0 {
		row.TelegramAt = refreshed.TelegramAt
	}
}

// announceTelegramBody 把公告压成一条频道消息：标题 + 正文。
// 正文已经在上面的校验里 TrimSpace 过，这里只做拼接，不重新解释内容。
func announceTelegramBody(row model.AdtyNotificationPublication) string {
	return announceTelegramBodyWithDB(model.DB, row)
}

func announceTelegramBodyWithDB(db *gorm.DB, row model.AdtyNotificationPublication) string {
	content := resolveAdtyNotificationDisplayContentWithDB(db, row)
	if row.Title == "" {
		return content
	}
	return row.Title + "\n\n" + content
}

func resolveAdtyNotificationDisplayContent(row model.AdtyNotificationPublication) string {
	return resolveAdtyNotificationDisplayContentWithDB(model.DB, row)
}

func resolveAdtyNotificationDisplayContentWithDB(db *gorm.DB, row model.AdtyNotificationPublication) string {
	content := strings.TrimSpace(row.Content)
	if row.Id > 0 && db != nil && db.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		var head model.AdtyTranslationHead
		if db.Where("object_key = ?", adtyNotificationTranslationKey(row.Id)).Limit(1).Find(&head).RowsAffected > 0 {
			if head.Status == model.AdtyTranslationReady && strings.TrimSpace(head.English) != "" {
				eng := strings.TrimSpace(head.English)
				src := strings.TrimSpace(head.Source)
				if src == "" {
					src = normalizeAdtyNotificationSourceContentWithDB(db, row.Id, content)
				}
				if !strings.Contains(content, eng) {
					return src + "\n\n" + eng
				}
			}
		}
	}
	return content
}

func adtyUserActiveTeamTargets(userID, siteID int) ([]string, error) {
	if userID <= 0 || model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyTeam{}) || !model.DB.Migrator().HasTable(&model.AdtyTeamMember{}) {
		return nil, nil
	}
	var memberTeamIDs []int64
	if err := model.DB.Model(&model.AdtyTeamMember{}).
		Where("user_id = ? AND status = ?", userID, model.AdtyTeamMemberActive).
		Pluck("team_id", &memberTeamIDs).Error; err != nil {
		return nil, err
	}
	if len(memberTeamIDs) == 0 {
		return nil, nil
	}
	var activeTeamIDs []int64
	if err := model.DB.Model(&model.AdtyTeam{}).
		Where("id IN ? AND site_id = ? AND status = ?", memberTeamIDs, siteID, model.AdtyTeamStatusActive).
		Pluck("id", &activeTeamIDs).Error; err != nil {
		return nil, err
	}
	if len(activeTeamIDs) == 0 {
		return nil, nil
	}
	targets := make([]string, 0, len(activeTeamIDs))
	for _, tid := range activeTeamIDs {
		targets = append(targets, fmt.Sprintf("team:%d", tid))
	}
	return targets, nil
}

func adtyUserCanReadPublication(user *model.User, pub *model.AdtyNotificationPublication) bool {
	if user == nil || pub == nil || user.Id <= 0 || user.Status != common.UserStatusEnabled || pub.Status != "published" {
		return false
	}
	switch pub.Audience {
	case "all_sites", "all_users", "all":
		return true
	case "site":
		return user.SiteId == pub.TargetSiteId && pub.TargetSiteId > 0
	case "user":
		return user.StaffSiteId == 0 && user.SiteId == pub.TargetSiteId && pub.TargetSiteName == fmt.Sprintf("user:%d", user.Id)
	case "team":
		if user.StaffSiteId != 0 || user.SiteId != pub.TargetSiteId {
			return false
		}
		targets, err := adtyUserActiveTeamTargets(user.Id, pub.TargetSiteId)
		if err != nil {
			return false
		}
		return slices.Contains(targets, pub.TargetSiteName)
	default:
		return false
	}
}

// A matching Host alone does not entitle an unrelated account to site content.
// Account membership is read from SQL rather than client parameters or caches.
func adtyVisibleNotificationPublications(userID int, site *model.AdtySite) (*gorm.DB, error) {
	var user model.User
	if err := model.DB.First(&user, userID).Error; err != nil {
		return nil, err
	}
	query := model.DB.Model(&model.AdtyNotificationPublication{}).Where("status = ?", "published")
	userTarget := fmt.Sprintf("user:%d", userID)
	// 可见性规则（A.18.2 / features.md:16）：
	//
	//	audience    主站可见  分站可见
	//	all_users   ✅        ✅  ← Root 发布的全部用户通知全平台可见
	//	all_sites   ✅        ✅  平台级公告，全平台可见
	//	site+target 仅 target 是自己时可见
	//	team+target 仅同站活跃团队成员可见
	//	user+target 仅同站目标用户本人可见
	platformAudiences := []string{"all_users", "all_sites", "all"}
	if site == nil {
		if user.SiteId == 0 && user.StaffSiteId == 0 {
			teamTargets, err := adtyUserActiveTeamTargets(userID, 0)
			if err != nil {
				return nil, err
			}
			if len(teamTargets) > 0 {
				return query.Where(
					"audience IN ? OR (audience = ? AND target_site_id = 0 AND target_site_name = ?) OR (audience = ? AND target_site_id = 0 AND target_site_name IN ?)",
					platformAudiences, "user", userTarget, "team", teamTargets,
				), nil
			}
			return query.Where(
				"audience IN ? OR (audience = ? AND target_site_id = 0 AND target_site_name = ?)",
				platformAudiences, "user", userTarget,
			), nil
		}
		return query.Where("audience IN ?", platformAudiences), nil
	}
	var current model.AdtySite
	if err := model.DB.First(&current, site.Id).Error; err != nil {
		return nil, err
	}
	if user.SiteId != site.Id && current.OwnerUserId != userID {
		// 只是访问了这个域名、并不属于这个站：平台级公告照常，站内定向内容不给。
		return query.Where("audience IN ?", platformAudiences), nil
	}
	if user.SiteId == site.Id && user.StaffSiteId == 0 {
		teamTargets, err := adtyUserActiveTeamTargets(userID, site.Id)
		if err != nil {
			return nil, err
		}
		if len(teamTargets) > 0 {
			return query.Where(
				"audience IN ? OR (audience = ? AND target_site_id = ?) OR (audience = ? AND target_site_id = ? AND target_site_name = ?) OR (audience = ? AND target_site_id = ? AND target_site_name IN ?)",
				platformAudiences, "site", site.Id, "user", site.Id, userTarget, "team", site.Id, teamTargets,
			), nil
		}
		return query.Where(
			"audience IN ? OR (audience = ? AND target_site_id = ?) OR (audience = ? AND target_site_id = ? AND target_site_name = ?)",
			platformAudiences, "site", site.Id, "user", site.Id, userTarget,
		), nil
	}
	return query.Where("audience IN ? OR (audience = ? AND target_site_id = ?)", platformAudiences, "site", site.Id), nil
}

func adtyTodoSiteID(userID int, site *model.AdtySite) int {
	if userID > 0 && model.DB != nil {
		var user model.User
		if model.DB.Select("id", "site_id").Where("id = ?", userID).Limit(1).Find(&user).RowsAffected > 0 {
			return user.SiteId
		}
	}
	if site != nil {
		return site.Id
	}
	return 0
}

func adtyNotificationVersion(source string) string {
	hash := sha256.Sum256([]byte(source))
	return hex.EncodeToString(hash[:])
}

// adtyCurrentNotificationInbox uses the same Host boundary as public Notice and
// status. Version keys bind reads to the actual content shown by this response.
func adtyCurrentNotificationInbox(site *model.AdtySite) (AdtyNotificationInbox, []string, error) {
	inbox := AdtyNotificationInbox{Announcements: []map[string]any{}, ReadKeys: []string{}}
	keys := []string{}
	if site != nil {
		return inbox, keys, nil
	}
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	inbox.Notice = strings.TrimSpace(common.OptionMap["Notice"])
	if inbox.Notice != "" {
		inbox.NoticeKey = adtyNotificationVersion("notice:" + inbox.Notice)
		keys = append(keys, inbox.NoticeKey)
	}
	return inbox, keys, nil
}

func formatAdtyNotificationItem(row model.AdtyNotificationPublication) map[string]any {
	content := resolveAdtyNotificationDisplayContent(row)
	var targetTeamID int64
	var targetUserID int
	if row.Audience == "team" {
		if raw, ok := strings.CutPrefix(row.TargetSiteName, "team:"); ok {
			targetTeamID, _ = strconv.ParseInt(raw, 10, 64)
		}
	} else if row.Audience == "user" {
		if raw, ok := strings.CutPrefix(row.TargetSiteName, "user:"); ok {
			targetUserID, _ = strconv.Atoi(raw)
		}
	}
	item := map[string]any{
		"id":               fmt.Sprintf("publication:%d", row.Id),
		"read_key":         row.ReadKey,
		"title":            row.Title,
		"content":          content,
		"type":             "default",
		"publishDate":      time.Unix(row.PublishedAt, 0).UTC().Format(time.RFC3339),
		"publisher_name":   row.PublisherName,
		"publisher_id":     row.PublisherId,
		"issuer_site_id":   row.IssuerSiteId,
		"audience":         row.Audience,
		"target_site_id":   row.TargetSiteId,
		"target_site_name": row.TargetSiteName,
		"target_team_id":   targetTeamID,
		"target_user_id":   targetUserID,
		"kind":             row.Kind,
		"business":         row.Business,
	}
	if row.Id > 0 && model.DB != nil && model.DB.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		var head model.AdtyTranslationHead
		if model.DB.Where("object_key = ?", adtyNotificationTranslationKey(row.Id)).Limit(1).Find(&head).RowsAffected > 0 {
			item["english"] = head.English
			item["content_en"] = head.English
			item["source"] = head.Source
			item["translation_status"] = head.Status
			item["translation_notice"] = head.Notice
			item["translation_version"] = head.Version
			item["object_key"] = head.ObjectKey
		}
	}
	return item
}

// GetAdtyNotificationDetail resolves a single announcement or notice across the
// caller's full visible history (not just the first inbox page of 20 items),
// enforcing site/audience visibility and user hide state.
func GetAdtyNotificationDetail(userID int, site *model.AdtySite, key string) (map[string]any, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false, nil
	}
	inbox, _, err := adtyCurrentNotificationInbox(site)
	if err != nil {
		return nil, false, err
	}
	if inbox.Notice != "" && (key == inbox.NoticeKey || key == "notice") {
		if adtyIsNotificationKeyHidden(userID, inbox.NoticeKey) {
			return nil, false, nil
		}
		return map[string]any{
			"id":       inbox.NoticeKey,
			"read_key": inbox.NoticeKey,
			"title":    "Notice",
			"content":  inbox.Notice,
			"type":     "notice",
		}, true, nil
	}
	query, err := adtyVisibleNotificationPublications(userID, site)
	if err != nil {
		return nil, false, err
	}
	var row model.AdtyNotificationPublication
	if idStr, ok := strings.CutPrefix(key, "publication:"); ok {
		if pubID, parseErr := strconv.Atoi(strings.TrimSpace(idStr)); parseErr == nil && pubID > 0 {
			err = query.Where("id = ? OR read_key = ?", pubID, key).Order("id DESC").Limit(1).Find(&row).Error
		} else {
			err = query.Where("read_key = ?", key).Order("id DESC").Limit(1).Find(&row).Error
		}
	} else if pubID, parseErr := strconv.Atoi(key); parseErr == nil && pubID > 0 {
		err = query.Where("id = ? OR read_key = ?", pubID, key).Order("id DESC").Limit(1).Find(&row).Error
	} else {
		err = query.Where("read_key = ?", key).Order("id DESC").Limit(1).Find(&row).Error
	}
	if err != nil {
		return nil, false, err
	}
	if row.Id == 0 {
		ticketQuery, scope, err := adtyTicketInboxQuery(userID, site)
		if err != nil {
			return nil, false, err
		}
		var notice model.AdtyTicketNotice
		if err := ticketQuery.Select(ticketInboxColumns).Where("read_key = ?", key).Limit(1).Find(&notice).Error; err != nil {
			return nil, false, err
		}
		if notice.Id > 0 && !adtyIsNotificationKeyHidden(userID, notice.ReadKey) {
			return formatTicketInboxNotice(notice, scope), true, nil
		}
		if item, ok := adtyWatcherNoticeDetail(userID, key); ok {
			return item, true, nil
		}
		return nil, false, nil
	}
	if adtyIsNotificationKeyHidden(userID, row.ReadKey) || adtyIsNotificationKeyHidden(userID, fmt.Sprintf("publication:%d", row.Id)) {
		return nil, false, nil
	}
	if _, err := EnsureAdtySevereTodoForVisiblePublication(userID, adtyTodoSiteID(userID, site), &row); err != nil {
		common.SysLog(common.LogText("severe todo was not created for visible notice %s: %s", row.ReadKey, err.Error()))
	}
	item := formatAdtyNotificationItem(row)
	RenderAdtyTodoInboxItems(userID, []map[string]any{item})
	return item, true, nil
}

func adtyIsNotificationKeyHidden(userID int, key string) bool {
	if userID <= 0 || key == "" || model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyNotificationHide{}) {
		return false
	}
	var count int64
	if err := model.DB.Model(&model.AdtyNotificationHide{}).Where("user_id = ? AND key = ?", userID, key).Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

func GetAdtyNotificationInbox(userID int, site *model.AdtySite, before int, ticketCursors ...int64) (AdtyNotificationInbox, error) {
	inbox, keys, err := adtyCurrentNotificationInbox(site)
	if err != nil {
		return inbox, err
	}
	query, err := adtyVisibleNotificationPublications(userID, site)
	if err != nil {
		return inbox, err
	}
	siteID := 0
	if site != nil {
		siteID = site.Id
	}
	receipts := model.DB.Model(&model.AdtyNotificationRead{}).Select("read_key").Where("user_id = ? AND site_id = ?", userID, siteID)
	var unread int64
	if err := query.Session(&gorm.Session{}).Where("read_key NOT IN (?)", receipts).Count(&unread).Error; err != nil {
		return inbox, err
	}
	inbox.UnreadPublications = unread
	var ticketBefore int64
	if len(ticketCursors) > 0 {
		ticketBefore = ticketCursors[0]
	} else if before != 0 {
		ticketBefore = -1
	}
	firstPage := before == 0 && ticketBefore == 0
	if before != 0 {
		query = query.Where("id < ?", before)
		inbox = AdtyNotificationInbox{Announcements: []map[string]any{}, ReadKeys: []string{}, UnreadPublications: unread}
		keys = []string{}
	}
	var rows []model.AdtyNotificationPublication
	if err := query.Order("id DESC").Limit(21).Find(&rows).Error; err != nil {
		return inbox, err
	}
	if len(rows) > 20 {
		rows = rows[:20]
		inbox.NextBeforeId = rows[19].Id
	}
	todoSiteID := adtyTodoSiteID(userID, site)
	for i := range rows {
		row := rows[i]
		if _, err := EnsureAdtySevereTodoForVisiblePublication(userID, todoSiteID, &row); err != nil {
			common.SysLog(common.LogText("severe todo was not created for visible notice %s: %s", row.ReadKey, err.Error()))
		}
		key := row.ReadKey
		inbox.Announcements = append(inbox.Announcements, formatAdtyNotificationItem(row))
		keys = append(keys, key)
	}
	ticketQuery, handlerScope, err := adtyTicketInboxQuery(userID, site)
	if err != nil {
		return inbox, err
	}
	var unreadTickets int64
	var visibleTicketKeys []string
	ticketReceipts := model.DB.Model(&model.AdtyNotificationRead{}).Where("user_id=?", userID)
	hidden := model.DB.Model(&model.AdtyNotificationHide{}).Select("key").Where("user_id = ?", userID)
	ticketQuery = ticketQuery.Where("read_key NOT IN (?)", hidden)
	if err := ticketQuery.Session(&gorm.Session{}).Where("read_key NOT IN (?)", ticketReceipts.Session(&gorm.Session{}).Select("read_key")).Count(&unreadTickets).Error; err != nil {
		return inbox, err
	}
	inbox.UnreadPublications += unreadTickets
	if ticketBefore >= 0 {
		if ticketBefore > 0 {
			ticketQuery = ticketQuery.Where("id < ?", ticketBefore)
		}
		var notices []model.AdtyTicketNotice
		if err := ticketQuery.Select(ticketInboxColumns).Order("id DESC").Limit(21).Find(&notices).Error; err != nil {
			return inbox, err
		}
		if len(notices) > 20 {
			notices = notices[:20]
			inbox.NextTicketBeforeId = notices[19].Id
		}
		for _, n := range notices {
			visibleTicketKeys = append(visibleTicketKeys, n.ReadKey)
			inbox.Announcements = append(inbox.Announcements, formatTicketInboxNotice(n, handlerScope))
		}
	}

	if firstPage {
		for _, item := range adtyWatcherNoticeInboxItems(userID) {
			inbox.Announcements = append(inbox.Announcements, item)
			if key, _ := item["read_key"].(string); key != "" {
				keys = append(keys, key)
			}
		}
	}
	inbox.UnreadPublications += adtyUnreadWatcherNoticeCount(userID, siteID)
	inbox.ReadKeys, err = model.GetAdtyNotificationReadKeys(userID, siteID, keys)
	if err != nil {
		return inbox, err
	}
	if len(visibleTicketKeys) > 0 {
		var ticketRead []string
		if err := ticketReceipts.Where("read_key IN ?", visibleTicketKeys).Distinct().Pluck("read_key", &ticketRead).Error; err != nil {
			return inbox, err
		}
		inbox.ReadKeys = append(inbox.ReadKeys, ticketRead...)
	}
	inbox.Announcements = RenderAdtyTodoInboxItems(userID, inbox.Announcements)
	return hideAdtyNotifications(userID, inbox)
}

func hideAdtyNotifications(userID int, inbox AdtyNotificationInbox) (AdtyNotificationInbox, error) {
	if userID <= 0 || model.DB == nil || len(inbox.Announcements) == 0 {
		return inbox, nil
	}
	keys := make([]string, 0, len(inbox.Announcements))
	for _, item := range inbox.Announcements {
		if key, ok := item["read_key"].(string); ok && key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return inbox, nil
	}
	var hidden []string
	if err := model.DB.Model(&model.AdtyNotificationHide{}).Where("user_id = ? AND key IN ?", userID, keys).Pluck("key", &hidden).Error; err != nil {
		return inbox, err
	}
	if len(hidden) == 0 {
		return inbox, nil
	}
	skip := map[string]bool{}
	for _, key := range hidden {
		skip[key] = true
	}
	visible := inbox.Announcements[:0]
	for _, item := range inbox.Announcements {
		key, _ := item["read_key"].(string)
		if !skip[key] {
			visible = append(visible, item)
		}
	}
	inbox.Announcements = visible
	return inbox, nil
}

func MarkAdtyNotificationInboxRead(userID int, site *model.AdtySite, keys []string) error {
	if len(keys) == 0 || len(keys) > 41 {
		return common.NewMessage("Invalid parameters")
	}
	_, visible, err := adtyCurrentNotificationInbox(site)
	if err != nil {
		return err
	}
	allowed := make(map[string]bool, len(visible))
	for _, key := range visible {
		allowed[key] = true
	}
	query, err := adtyVisibleNotificationPublications(userID, site)
	if err != nil {
		return err
	}
	// Version keys select candidates; the visibility predicate must also hold,
	// including for publications on later pages.
	var publicationKeys []string
	if err := query.Where("read_key IN ?", keys).Pluck("read_key", &publicationKeys).Error; err != nil {
		return err
	}
	for _, key := range publicationKeys {
		allowed[key] = true
	}
	ticketQuery, _, err := adtyTicketInboxQuery(userID, site)
	if err != nil {
		return err
	}
	var ticketKeys []string
	if err := ticketQuery.Where("read_key IN ?", keys).Pluck("read_key", &ticketKeys).Error; err != nil {
		return err
	}
	for _, key := range ticketKeys {
		allowed[key] = true
	}
	for key := range adtyOwnedWatcherReadKeys(userID, keys) {
		allowed[key] = true
	}
	unique := make(map[string]bool, len(keys))
	for _, key := range keys {
		if !allowed[key] {
			return common.NewMessage("Invalid parameters")
		}
		unique[key] = true
	}
	keys = make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	siteID := 0
	if site != nil {
		siteID = site.Id
	}
	return model.MarkAdtyNotificationsRead(userID, siteID, keys, time.Now().Unix())
}
