package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	ContentFormat   string   `json:"content_format"`
	Kind            string   `json:"kind"`
	Audience        string   `json:"audience"`
	TargetAudiences []string `json:"target_audiences"`
	TargetSiteIDs   []int    `json:"target_site_ids"`
	TargetSiteId    int      `json:"target_site_id"`
	TargetTeamId    int64    `json:"target_team_id"`
	TargetUserId    int      `json:"target_user_id"`
	Business        string   `json:"business"`
	Revision        int64    `json:"revision"`
	// SyncTelegram 勾了才把这条公告排进 TG 队列；TelegramChannel 是目标频道
	// （`@name` 或 `-100…`）。都不填就是只发站内。
	SyncTelegram    bool   `json:"sync_telegram"`
	TelegramChannel string `json:"telegram_channel"`
	// Draft 表示「先存着，别发」。草稿不出现任何读者面前、不推 TG；
	// 编辑草稿时把它改成 false 就是发布。已发布的公告不能再退回草稿
	// （撤回才是那个动作），见下面的判断。
	Draft bool `json:"draft"`
}

const (
	adtyPrivateTelegramMarker    = "@private"
	adtyNotificationMaxRunes     = 2000
	adtyNotificationMaxRawBytes  = 32 * 1024
	adtyNotificationMaxHTMLDepth = 64
)

var (
	adtyOpenSiteStatuses = []string{
		model.AdtySiteStatusActive,
		model.AdtySiteStatusPaused,
		model.AdtySiteStatusClosing,
	}
	adtyRootTargetAudiences    = []string{"main_users", "all_site_owners", "site_users", "main_teams"}
	adtySubsiteTargetAudiences = []string{"site_users", "site_teams"}
	adtyNewTargetAudiences     = []string{"main_users", "all_site_owners", "site_users", "main_teams", "site_teams"}

	adtyNotificationFontSizeRegex = regexp.MustCompile(`^(\d+(?:\.\d+)?)(px|rem|em)$`)
	adtyNotificationHexColorRegex = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	adtyNotificationRGBColorRegex = regexp.MustCompile(`(?i)^rgb\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*\)$`)
	adtyNotificationMultiNewlines = regexp.MustCompile(`\n{3,}`)
)

func isAdtyNotificationAllowedHTMLTag(tag string) bool {
	switch tag {
	case "p", "br", "div", "span", "strong", "b", "em", "i", "u", "h1", "h2", "h3", "ul", "ol", "li", "blockquote":
		return true
	default:
		return false
	}
}

func isAdtyNotificationDropSubtreeTag(tag string) bool {
	switch tag {
	case "script", "style", "iframe", "object", "embed", "applet", "noscript", "template", "svg", "math", "head", "title", "textarea", "xmp":
		return true
	default:
		return false
	}
}

func sanitizeAdtyNotificationStyle(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	order := make([]string, 0, 3)
	values := make(map[string]string, 3)
	for decl := range strings.SplitSeq(raw, ";") {
		prop, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		prop = strings.ToLower(strings.TrimSpace(prop))
		val = strings.TrimSpace(val)
		if prop == "" || val == "" {
			continue
		}
		var cleaned string
		switch prop {
		case "font-size":
			lowerVal := strings.ToLower(val)
			m := adtyNotificationFontSizeRegex.FindStringSubmatch(lowerVal)
			if m == nil {
				continue
			}
			num, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				continue
			}
			unit := m[2]
			px := num
			if unit == "rem" || unit == "em" {
				px = num * 16
			}
			if !(px >= 10 && px <= 32) {
				continue
			}
			cleaned = m[1] + unit
		case "color":
			if adtyNotificationHexColorRegex.MatchString(val) {
				cleaned = strings.ToLower(val)
			} else if m := adtyNotificationRGBColorRegex.FindStringSubmatch(val); m != nil {
				r, errR := strconv.Atoi(m[1])
				g, errG := strconv.Atoi(m[2])
				b, errB := strconv.Atoi(m[3])
				if errR != nil || errG != nil || errB != nil || r < 0 || r > 255 || g < 0 || g > 255 || b < 0 || b > 255 {
					continue
				}
				cleaned = fmt.Sprintf("rgb(%d, %d, %d)", r, g, b)
			} else {
				continue
			}
		case "font-weight":
			lowerVal := strings.ToLower(val)
			switch lowerVal {
			case "normal", "bold", "400", "500", "600", "700":
				cleaned = lowerVal
			default:
				continue
			}
		default:
			continue
		}
		if _, exists := values[prop]; !exists {
			order = append(order, prop)
		}
		values[prop] = cleaned
	}
	if len(order) == 0 {
		return ""
	}
	var b strings.Builder
	for i, prop := range order {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(prop)
		b.WriteString(": ")
		b.WriteString(values[prop])
	}
	return b.String()
}

// SanitizeAdtyNotificationHTML enforces a strict tag and inline-style whitelist
// for rich-text notifications, stripping scripts, event handlers, links, and
// any unknown tags or attributes.
func SanitizeAdtyNotificationHTML(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	ctxNode := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	nodes, err := html.ParseFragment(strings.NewReader(raw), ctxNode)
	if err != nil {
		return html.EscapeString(strings.TrimSpace(raw))
	}
	var b strings.Builder
	for _, n := range nodes {
		writeSanitizedAdtyHTMLNode(&b, n, 0)
	}
	return strings.TrimSpace(b.String())
}

func writeSanitizedAdtyHTMLNode(b *strings.Builder, n *html.Node, depth int) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.TextNode:
		b.WriteString(html.EscapeString(n.Data))
	case html.ElementNode:
		tag := strings.ToLower(strings.TrimSpace(n.Data))
		if n.Namespace != "" || isAdtyNotificationDropSubtreeTag(tag) {
			return
		}
		if !isAdtyNotificationAllowedHTMLTag(tag) || depth >= adtyNotificationMaxHTMLDepth {
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				writeSanitizedAdtyHTMLNode(b, child, depth+1)
			}
			return
		}
		var safeStyle string
		for _, attr := range n.Attr {
			if attr.Namespace == "" && strings.EqualFold(attr.Key, "style") {
				safeStyle = sanitizeAdtyNotificationStyle(attr.Val)
				break
			}
		}
		b.WriteByte('<')
		b.WriteString(tag)
		if safeStyle != "" {
			b.WriteString(` style="`)
			b.WriteString(html.EscapeString(safeStyle))
			b.WriteByte('"')
		}
		b.WriteByte('>')
		if tag == "br" {
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			writeSanitizedAdtyHTMLNode(b, child, depth+1)
		}
		b.WriteString("</")
		b.WriteString(tag)
		b.WriteByte('>')
	}
}

// AdtyNotificationHTMLToPlainText extracts visible plain text from rich-text
// HTML for visible rune-count enforcement (<= 2000 runes).
func AdtyNotificationHTMLToPlainText(raw string) string {
	return extractAdtyNotificationHTMLText(raw, false)
}

func adtyNotificationHTMLToTelegramText(raw string) string {
	return extractAdtyNotificationHTMLText(raw, true)
}

func extractAdtyNotificationHTMLText(raw string, formatListItems bool) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	ctxNode := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	nodes, err := html.ParseFragment(strings.NewReader(raw), ctxNode)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	var b strings.Builder
	for _, n := range nodes {
		writeAdtyHTMLPlainTextNode(&b, n, formatListItems, 0)
	}
	out := strings.ReplaceAll(b.String(), "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "\n")
	out = strings.ReplaceAll(out, "\u00a0", " ")
	out = adtyNotificationMultiNewlines.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

func writeAdtyHTMLPlainTextNode(b *strings.Builder, n *html.Node, formatListItems bool, depth int) {
	if n == nil || depth > adtyNotificationMaxHTMLDepth*2 {
		return
	}
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
	case html.ElementNode:
		tag := strings.ToLower(strings.TrimSpace(n.Data))
		if n.Namespace != "" || isAdtyNotificationDropSubtreeTag(tag) {
			return
		}
		if tag == "br" {
			b.WriteByte('\n')
			return
		}
		isBlock := false
		switch tag {
		case "p", "div", "h1", "h2", "h3", "blockquote", "ul", "ol":
			isBlock = true
		}
		if isBlock || tag == "li" {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			}
			if formatListItems && tag == "li" {
				b.WriteString("- ")
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			writeAdtyHTMLPlainTextNode(b, child, formatListItems, depth+1)
		}
		if isBlock || tag == "li" {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			}
			if formatListItems && (tag == "p" || tag == "h1" || tag == "h2" || tag == "h3" || tag == "blockquote" || tag == "ul" || tag == "ol") {
				if !strings.HasSuffix(b.String(), "\n\n") {
					b.WriteByte('\n')
				}
			}
		}
	}
}

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

func adtyNotificationAuditSnapshot(row model.AdtyNotificationPublication) map[string]any {
	return map[string]any{
		"id":               row.Id,
		"issuer_site_id":   row.IssuerSiteId,
		"audience":         row.Audience,
		"target_audiences": row.TargetAudiences,
		"target_site_ids":  row.TargetSiteIDs,
		"target_site_id":   row.TargetSiteId,
		"target_site_name": row.TargetSiteName,
		"kind":             row.Kind,
		"title":            row.Title,
		"content_format":   row.ContentFormat,
		"business":         row.Business,
		"status":           row.Status,
		"revision":         row.Revision,
		"telegram_channel": row.TelegramChannel,
	}
}

func SaveAdtyNotificationPublication(actorID int, issuer *model.AdtySite, id int, input AdtyNotificationInput, withdraw bool) (*model.AdtyNotificationPublication, error) {
	row, _, err := SaveAdtyNotificationPublicationWithAudit(actorID, issuer, id, input, withdraw)
	return row, err
}

func SaveAdtyNotificationPublicationWithAudit(actorID int, issuer *model.AdtySite, id int, input AdtyNotificationInput, withdraw bool) (*model.AdtyNotificationPublication, string, error) {
	issuerID := 0
	if issuer != nil {
		issuerID = issuer.Id
		if issuer.Status == model.AdtySiteStatusClosing {
			return nil, "", ErrAdtySiteClosing
		}
	}
	row := model.AdtyNotificationPublication{IssuerSiteId: issuerID}
	var existing *model.AdtyNotificationPublication
	var beforeSnap map[string]any
	currentStatus := ""
	if id > 0 {
		if err := model.DB.Where("id = ? AND issuer_site_id = ?", id, issuerID).First(&row).Error; err != nil {
			return nil, "", err
		}
		existingCopy := row
		existing = &existingCopy
		beforeSnap = adtyNotificationAuditSnapshot(row)
		currentStatus = row.Status
		if input.Revision <= 0 {
			return nil, "", common.NewMessage("Invalid parameters")
		}
		if currentStatus == "withdrawn" {
			// 撤回的公告是历史记录，不是可编辑对象。
			return nil, "", common.NewMessage("Invalid parameters")
		}
		if currentStatus == "published" && input.Draft {
			// 已发布的不能退回草稿：那是「悄无声息地把它从所有人眼前拿走」，
			// 而撤回这个动作是显式且有历史的。
			return nil, "", common.NewMessage("Published announcements cannot be turned back into drafts")
		}
	} else {
		if withdraw || input.Revision != 0 {
			return nil, "", common.NewMessage("Invalid parameters")
		}
		var publisher model.User
		if err := model.DB.First(&publisher, actorID).Error; err != nil {
			return nil, "", err
		}
		row.PublisherId, row.PublisherName = actorID, publisher.Username
		row.Revision = 1
		// 草稿没有「发布时间」：它还没发。0 在界面上就是「还没发」。
		if !input.Draft {
			row.PublishedAt = time.Now().Unix()
		}
	}
	row.UpdatedAt, row.UpdatedBy = time.Now().Unix(), actorID
	verifyIssuerSiteTx := func(tx *gorm.DB) error {
		if issuer == nil {
			return nil
		}
		var current model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Select("id", "owner_user_id", "status").Where("id = ?", issuerID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if current.OwnerUserId != actorID {
			return ErrAdtySiteNotOwner
		}
		if current.Status == model.AdtySiteStatusClosing {
			return ErrAdtySiteClosing
		}
		if current.Status != model.AdtySiteStatusActive && current.Status != model.AdtySiteStatusPaused {
			return ErrAdtySiteStatus
		}
		return nil
	}
	if withdraw {
		row.Status = "withdrawn"
		var auditEntry *model.AuditLog
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := verifyIssuerSiteTx(tx); err != nil {
				return err
			}
			if err := model.UpdateAdtyNotificationPublicationTx(tx, &row, input.Revision); err != nil {
				return err
			}
			var auditErr error
			auditEntry, auditErr = appendAdtyOfficialAuditTx(tx, actorID, issuerID, "notification", strconv.Itoa(row.Id), "adty.notification.withdraw", "", "", beforeSnap, adtyNotificationAuditSnapshot(row), model.AuditResultSuccess)
			return auditErr
		})
		if err != nil {
			return &row, "", err
		}
		eventID := ""
		if auditEntry != nil {
			eventID = auditEntry.EventId
			deliverAdtyCommittedAudit(*auditEntry)
		} else {
			writeAdtyOfficialAudit(actorID, issuerID, "notification", strconv.Itoa(row.Id), "adty.notification.withdraw", "", "", beforeSnap, adtyNotificationAuditSnapshot(row), model.AuditResultSuccess)
		}
		return &row, eventID, nil
	}
	input.Title, input.Content, input.Business = strings.TrimSpace(input.Title), strings.TrimSpace(input.Content), strings.TrimSpace(input.Business)
	contentFormat := strings.ToLower(strings.TrimSpace(input.ContentFormat))
	if contentFormat == "" {
		contentFormat = "markdown"
	}
	if contentFormat != "markdown" && contentFormat != "html" {
		return nil, "", common.NewMessage("Invalid parameters")
	}
	if len(input.Content) > adtyNotificationMaxRawBytes {
		return nil, "", common.NewMessage("Notification content exceeds 32KB limit")
	}
	sourceContent := normalizeAdtyNotificationSourceContent(row.Id, input.Content)
	visibleContent := sourceContent
	if contentFormat == "html" {
		sourceContent = SanitizeAdtyNotificationHTML(sourceContent)
		if len(sourceContent) > adtyNotificationMaxRawBytes {
			return nil, "", common.NewMessage("Notification content exceeds 32KB limit")
		}
		visibleContent = AdtyNotificationHTMLToPlainText(sourceContent)
	}
	if utf8.RuneCountInString(visibleContent) > adtyNotificationMaxRunes {
		return nil, "", common.NewMessage("Notification content must be at most 2000 characters")
	}
	// 草稿允许空标题空正文——「先存着、回头再写」正是它存在的理由，要求填满就等于
	// 没有草稿。但长度上限和类别/受众枚举始终要校验：那些是存储约束，不是完整度。
	if (!input.Draft && (input.Title == "" || strings.TrimSpace(visibleContent) == "")) ||
		utf8.RuneCountInString(input.Title) > 120 || utf8.RuneCountInString(input.Business) > 200 ||
		!slices.Contains([]string{"platform", "system", "maintenance", "rules"}, input.Kind) {
		return nil, "", common.NewMessage("Invalid parameters")
	}

	resolved, err := resolveAdtyNotificationTargets(issuer, issuerID, input, existing)
	if err != nil {
		return nil, "", err
	}

	row.Title, row.Content, row.ContentFormat, row.Business = input.Title, sourceContent, contentFormat, input.Business
	row.Kind, row.Audience, row.TargetSiteId, row.TargetSiteName = input.Kind, resolved.Audience, resolved.TargetSiteId, resolved.TargetSiteName
	row.TargetAudiences, row.TargetSiteIDs = resolved.TargetAudiences, resolved.TargetSiteIDs
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
		ch := strings.TrimSpace(input.TelegramChannel)
		switch {
		case row.Audience == "team" || row.Audience == "user":
			row.TelegramChannel = adtyPrivateTelegramMarker
		case resolved.UsesTargetAudiences && (ch == "" || strings.EqualFold(ch, adtyPrivateTelegramMarker)):
			row.TelegramChannel = adtyPrivateTelegramMarker
		default:
			if issuer != nil || issuerID > 0 {
				return nil, "", common.NewMessage("Subsites cannot push announcements to shared public Telegram channels")
			}
			if !adtyValidPublicTelegramChannel(ch) || utf8.RuneCountInString(ch) > 128 {
				return nil, "", common.NewMessage("Invalid parameters")
			}
			row.TelegramChannel = ch
		}
	} else {
		row.TelegramChannel = ""
	}
	auditAction := "adty.notification.create"
	if id > 0 {
		auditAction = "adty.notification.update"
	}
	var auditEntry *model.AuditLog
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := verifyIssuerSiteTx(tx); err != nil {
			return err
		}
		if id > 0 {
			if err := model.UpdateAdtyNotificationPublicationTx(tx, &row, input.Revision); err != nil {
				return err
			}
		} else if err := tx.Create(&row).Error; err != nil {
			return err
		}
		var auditErr error
		auditEntry, auditErr = appendAdtyOfficialAuditTx(tx, actorID, issuerID, "notification", strconv.Itoa(row.Id), auditAction, "", "", beforeSnap, adtyNotificationAuditSnapshot(row), model.AuditResultSuccess)
		return auditErr
	}); err != nil {
		return nil, "", err
	}
	eventID := ""
	if auditEntry != nil {
		eventID = auditEntry.EventId
		deliverAdtyCommittedAudit(*auditEntry)
	} else {
		writeAdtyOfficialAudit(actorID, issuerID, "notification", strconv.Itoa(row.Id), auditAction, "", "", beforeSnap, adtyNotificationAuditSnapshot(row), model.AuditResultSuccess)
	}
	if row.Status == "published" {
		// R5-058 / R9-044: Apply translation BEFORE producing/updating severe todos
		// and abort downstream fan-out if a newer revision or withdrawal superseded row.
		if applyAdtyNotificationTranslation(&row, sourceContent, issuerID) {
			produceAdtySevereTodosForPublished(&row)
			if err := pushAdtyNotificationToTelegram(&row, issuer); err != nil {
				common.SysError(common.LogText("push notification %d to telegram failed: %s", row.Id, err.Error()))
			}
		}
	}
	return &row, eventID, nil
}

type resolvedAdtyNotificationTargets struct {
	Audience            string
	TargetAudiences     string
	TargetSiteIDs       string
	TargetSiteId        int
	TargetSiteName      string
	UsesTargetAudiences bool
}

func resolveAdtyNotificationTargets(issuer *model.AdtySite, issuerID int, input AdtyNotificationInput, existing *model.AdtyNotificationPublication) (resolvedAdtyNotificationTargets, error) {
	rawAudience := strings.TrimSpace(input.Audience)
	if existing != nil && (existing.Audience == "user" || existing.Audience == "team") && len(existing.ParsedTargetAudiences()) == 0 {
		hasTargetAudiences := false
		for _, item := range input.TargetAudiences {
			if strings.TrimSpace(item) != "" {
				hasTargetAudiences = true
				break
			}
		}
		if hasTargetAudiences || len(input.TargetSiteIDs) > 0 {
			return resolvedAdtyNotificationTargets{}, common.NewMessage("Targeted user or team notifications cannot be broadened to group broadcasts")
		}
		if len(input.TargetAudiences) > 0 {
			return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
		}
		if rawAudience != "" && rawAudience != existing.Audience {
			switch rawAudience {
			case "all", "all_users", "all_sites", "site", "multi",
				"main_users", "main_teams", "site_users", "site_teams",
				"subsite_users", "all_site_owners":
				return resolvedAdtyNotificationTargets{}, common.NewMessage("Targeted user or team notifications cannot be broadened to group broadcasts")
			case "team":
				if existing.Audience == "user" {
					return resolvedAdtyNotificationTargets{}, common.NewMessage("Targeted user or team notifications cannot be broadened to group broadcasts")
				}
				return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
			default:
				return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
			}
		}
		return resolvedAdtyNotificationTargets{
			Audience:            existing.Audience,
			TargetAudiences:     existing.TargetAudiences,
			TargetSiteIDs:       existing.TargetSiteIDs,
			TargetSiteId:        existing.TargetSiteId,
			TargetSiteName:      existing.TargetSiteName,
			UsesTargetAudiences: false,
		}, nil
	}
	useMulti := len(input.TargetAudiences) > 0 || slices.Contains(adtyNewTargetAudiences, rawAudience)
	if !useMulti {
		if !slices.Contains([]string{"all_users", "all_sites", "site", "team", "user"}, rawAudience) {
			return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
		}
		input.Audience = rawAudience
		targetSiteID, targetSiteName, err := resolveAdtyNotificationTarget(issuer, issuerID, input)
		if err != nil {
			return resolvedAdtyNotificationTargets{}, err
		}
		return resolvedAdtyNotificationTargets{
			Audience:       rawAudience,
			TargetSiteId:   targetSiteID,
			TargetSiteName: targetSiteName,
		}, nil
	}

	rawList := input.TargetAudiences
	if len(rawList) == 0 && rawAudience != "" {
		rawList = []string{rawAudience}
	}
	allowed := adtyRootTargetAudiences
	if issuer != nil {
		allowed = adtySubsiteTargetAudiences
	}
	audiences := make([]string, 0, len(rawList))
	for _, item := range rawList {
		aud := strings.TrimSpace(item)
		if !slices.Contains(allowed, aud) {
			return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
		}
		if !slices.Contains(audiences, aud) {
			audiences = append(audiences, aud)
		}
	}
	if len(audiences) == 0 || input.TargetTeamId != 0 || input.TargetUserId != 0 {
		return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
	}
	slices.Sort(audiences)
	audiencesJSON, err := common.Marshal(audiences)
	if err != nil {
		return resolvedAdtyNotificationTargets{}, err
	}

	summaryAudience := "multi"
	if len(audiences) == 1 {
		summaryAudience = audiences[0]
	}

	if issuer != nil {
		if input.TargetSiteId != 0 && input.TargetSiteId != issuerID {
			return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
		}
		for _, sid := range input.TargetSiteIDs {
			if sid != issuerID {
				return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
			}
		}
		siteIDsJSON, err := common.Marshal([]int{issuerID})
		if err != nil {
			return resolvedAdtyNotificationTargets{}, err
		}
		return resolvedAdtyNotificationTargets{
			Audience:            summaryAudience,
			TargetAudiences:     string(audiencesJSON),
			TargetSiteIDs:       string(siteIDsJSON),
			TargetSiteId:        issuerID,
			TargetSiteName:      AdtySiteDisplayHost(issuer),
			UsesTargetAudiences: true,
		}, nil
	}

	// Main-site Root (issuer == nil)
	hasSiteUsers := slices.Contains(audiences, "site_users")
	if !hasSiteUsers {
		if len(input.TargetSiteIDs) > 0 || input.TargetSiteId != 0 {
			return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
		}
		return resolvedAdtyNotificationTargets{
			Audience:            summaryAudience,
			TargetAudiences:     string(audiencesJSON),
			TargetSiteIDs:       "[]",
			UsesTargetAudiences: true,
		}, nil
	}

	rawSiteIDs := input.TargetSiteIDs
	if len(rawSiteIDs) == 0 && input.TargetSiteId > 0 {
		rawSiteIDs = []int{input.TargetSiteId}
	}
	siteIDs := make([]int, 0, len(rawSiteIDs))
	for _, sid := range rawSiteIDs {
		if sid <= 0 {
			return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
		}
		if !slices.Contains(siteIDs, sid) {
			siteIDs = append(siteIDs, sid)
		}
	}
	if len(siteIDs) == 0 {
		return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
	}
	slices.Sort(siteIDs)

	var sites []model.AdtySite
	siteQuery := model.DB.Where("id IN ? AND status IN ?", siteIDs, adtyOpenSiteStatuses)
	if model.DB.Migrator().HasColumn(&model.AdtySite{}, "archived_hidden_at") {
		siteQuery = siteQuery.Where("archived_hidden_at = 0")
	}
	if err := siteQuery.Order("id ASC").Find(&sites).Error; err != nil {
		return resolvedAdtyNotificationTargets{}, err
	}
	if len(sites) != len(siteIDs) {
		return resolvedAdtyNotificationTargets{}, common.NewMessage("Invalid parameters")
	}
	hosts := make([]string, 0, len(sites))
	for i := range sites {
		hosts = append(hosts, AdtySiteDisplayHost(&sites[i]))
	}
	siteIDsJSON, err := common.Marshal(siteIDs)
	if err != nil {
		return resolvedAdtyNotificationTargets{}, err
	}
	targetSiteID := 0
	if len(siteIDs) == 1 {
		targetSiteID = siteIDs[0]
	}
	return resolvedAdtyNotificationTargets{
		Audience:            summaryAudience,
		TargetAudiences:     string(audiencesJSON),
		TargetSiteIDs:       string(siteIDsJSON),
		TargetSiteId:        targetSiteID,
		TargetSiteName:      truncateAdtyRunes(strings.Join(hosts, ", "), 255),
		UsesTargetAudiences: true,
	}, nil
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

func adtyNotificationTitleTranslationKey(id int) string {
	return fmt.Sprintf("notification-title:%d", id)
}

func adtyContainsChinese(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func NormalizeAdtyNotificationSourceContent(pubID int, content string) string {
	return normalizeAdtyNotificationSourceContentWithDB(model.DB, pubID, content)
}

func NormalizeAdtyNotificationSourceContents(rows []model.AdtyNotificationPublication) map[int]string {
	out := make(map[int]string, len(rows))
	if len(rows) == 0 {
		return out
	}
	engByPubID := map[int]string{}
	if model.DB != nil && model.DB.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		keys := make([]string, 0, len(rows))
		pubByKey := make(map[string]int, len(rows))
		for _, row := range rows {
			if row.Id > 0 {
				key := adtyNotificationTranslationKey(row.Id)
				keys = append(keys, key)
				pubByKey[key] = row.Id
			}
		}
		if len(keys) > 0 {
			var heads []model.AdtyTranslationHead
			if err := model.DB.Select("object_key", "english").Where("object_key IN ?", keys).Find(&heads).Error; err == nil {
				for _, head := range heads {
					if pubID, ok := pubByKey[head.ObjectKey]; ok {
						engByPubID[pubID] = head.English
					}
				}
			}
		}
	}
	for _, row := range rows {
		out[row.Id] = stripAdtyNotificationTranslationSuffix(row.Content, engByPubID[row.Id])
	}
	return out
}

func cutAdtySuffixFold(s, suffix string) (string, bool) {
	if len(s) < len(suffix) {
		return s, false
	}
	if !strings.EqualFold(s[len(s)-len(suffix):], suffix) {
		return s, false
	}
	return s[:len(s)-len(suffix)], true
}

func isAdtyTranslationNotReadyText(s string) bool {
	s = strings.TrimSpace(s)
	return strings.EqualFold(s, AdtyTranslationNotReady) || strings.EqualFold(s, adtyTranslationNotReadyEn)
}

func adtyNotReadyTranslationStatus(bodyNeedsEn bool, bodyHead *model.AdtyTranslationHead, bodyMatched bool, titleNeedsEn bool, titleHead *model.AdtyTranslationHead, titleMatched bool, fallback string) string {
	status := ""
	if bodyNeedsEn && bodyHead != nil && (bodyMatched || (strings.TrimSpace(bodyHead.SourceHash) == "" && strings.TrimSpace(bodyHead.Source) == "")) {
		if s := strings.TrimSpace(bodyHead.Status); s != "" && s != model.AdtyTranslationReady {
			status = s
		}
	}
	if titleNeedsEn && titleHead != nil && (titleMatched || (strings.TrimSpace(titleHead.SourceHash) == "" && strings.TrimSpace(titleHead.Source) == "")) {
		if s := strings.TrimSpace(titleHead.Status); s != "" && s != model.AdtyTranslationReady {
			if status == "" || (status == model.AdtyTranslationPending && s == model.AdtyTranslationFailed) {
				status = s
			}
		}
	}
	if status == "" {
		return fallback
	}
	return status
}

func stripAdtyNotificationTranslationSuffix(content, english string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	notReadyMarkers := []string{AdtyTranslationNotReady, adtyTranslationNotReadyEn}
	var englishCandidates []string
	if eng := strings.TrimSpace(english); eng != "" {
		englishCandidates = append(englishCandidates, eng)
		if sanitized := SanitizeAdtyNotificationHTML(eng); sanitized != "" && sanitized != eng {
			englishCandidates = append(englishCandidates, sanitized)
		}
	}
	for {
		trimmed := false
		for _, sep := range []string{"\n\n", "\n"} {
			for _, marker := range notReadyMarkers {
				if before, ok := cutAdtySuffixFold(content, sep+marker); ok {
					content = strings.TrimSpace(before)
					trimmed = true
				}
			}
		}
		for _, candidate := range englishCandidates {
			if before, ok := strings.CutSuffix(content, "\n\n"+candidate); ok {
				content = strings.TrimSpace(before)
				trimmed = true
			}
		}
		if !trimmed {
			break
		}
	}
	return content
}

func normalizeAdtyNotificationSourceContent(pubID int, content string) string {
	return normalizeAdtyNotificationSourceContentWithDB(model.DB, pubID, content)
}

func normalizeAdtyNotificationSourceContentWithDB(db *gorm.DB, pubID int, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	english := ""
	if pubID > 0 && db != nil && db.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		var head model.AdtyTranslationHead
		if db.Where("object_key = ?", adtyNotificationTranslationKey(pubID)).Limit(1).Find(&head).RowsAffected > 0 {
			english = head.English
		}
	}
	return stripAdtyNotificationTranslationSuffix(content, english)
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

func adtyNotificationRevisionIsCurrent(id int, revision int64) bool {
	if id <= 0 || model.DB == nil {
		return false
	}
	var current struct {
		Revision int64
		Status   string
	}
	res := model.DB.Model(&model.AdtyNotificationPublication{}).
		Select("revision", "status").
		Where("id = ?", id).
		Limit(1).
		Find(&current)
	if res.Error != nil || res.RowsAffected == 0 {
		return false
	}
	if current.Status != "published" {
		return false
	}
	if revision > 0 && current.Revision != revision {
		return false
	}
	return true
}

func applyAdtyNotificationTranslation(row *model.AdtyNotificationPublication, sourceContent string, siteID int) bool {
	if row == nil || row.Id <= 0 || model.DB == nil {
		return false
	}
	if !adtyNotificationRevisionIsCurrent(row.Id, row.Revision) {
		return false
	}
	titleText := strings.TrimSpace(row.Title)
	bodyText := strings.TrimSpace(sourceContent)
	titleHasZH := adtyContainsChinese(titleText)
	bodyHasZH := adtyContainsChinese(bodyText)
	if !titleHasZH {
		deleteAdtyNotificationTranslationHeadIfCurrent(row.Id, row.Revision, adtyNotificationTitleTranslationKey(row.Id))
	}
	if !bodyHasZH {
		deleteAdtyNotificationTranslationHeadIfCurrent(row.Id, row.Revision, adtyNotificationTranslationKey(row.Id))
	}
	if !titleHasZH && !bodyHasZH {
		return adtyNotificationRevisionIsCurrent(row.Id, row.Revision)
	}
	ensureAdtyTranslationTables()
	if titleHasZH {
		if _, err := translateAdtyNotificationDynamic(row.Id, row.Revision, adtyNotificationTitleTranslationKey(row.Id), titleText, siteID); err != nil {
			common.SysError(common.LogText("translate notification %d title failed: %s", row.Id, err.Error()))
		}
	}
	if !bodyHasZH {
		return adtyNotificationRevisionIsCurrent(row.Id, row.Revision)
	}
	objectKey := adtyNotificationTranslationKey(row.Id)
	if _, err := translateAdtyNotificationDynamic(row.Id, row.Revision, objectKey, bodyText, siteID); err != nil {
		common.SysError(common.LogText("translate notification %d failed: %s", row.Id, err.Error()))
	}
	// R12-012: Write bilingual pub.Content under the same head -> pub lock order
	// as EditAdtyTranslation using the live locked head so concurrent human edits
	// (which do not bump pub.Revision) are never overwritten by stale results.
	current := false
	if err := withAdtyTranslationHeadLock(func(tx *gorm.DB) error {
		var head model.AdtyTranslationHead
		hasHead := model.AdtyLockForUpdate(tx).Where("object_key = ?", objectKey).Limit(1).Find(&head).RowsAffected > 0
		var pub model.AdtyNotificationPublication
		if model.AdtyLockForUpdate(tx).Where("id = ?", row.Id).Limit(1).Find(&pub).RowsAffected == 0 {
			return nil
		}
		if pub.Status != "published" || pub.Revision != row.Revision {
			return nil
		}
		bilingual := sourceContent
		if hasHead && head.Status == model.AdtyTranslationReady && strings.TrimSpace(head.English) != "" && adtyTranslationHeadMatchesSource(head, bodyText) {
			eng := strings.TrimSpace(head.English)
			if strings.EqualFold(strings.TrimSpace(pub.ContentFormat), "html") {
				eng = SanitizeAdtyNotificationHTML(eng)
			}
			if eng != "" && !strings.Contains(bilingual, eng) {
				bilingual = sourceContent + "\n\n" + eng
			}
		} else {
			if !strings.Contains(bilingual, AdtyTranslationNotReady) {
				bilingual = sourceContent + "\n\n" + AdtyTranslationNotReady
			}
			if !strings.Contains(strings.ToLower(bilingual), strings.ToLower(adtyTranslationNotReadyEn)) {
				bilingual += "\n\n" + adtyTranslationNotReadyEn
			}
		}
		if bilingual != pub.Content {
			if err := tx.Model(&pub).Update("content", bilingual).Error; err != nil {
				return err
			}
			row.Content = bilingual
		} else {
			row.Content = pub.Content
		}
		current = true
		return nil
	}); err != nil {
		common.SysError(common.LogText("persist notification %d bilingual content failed: %s", row.Id, err.Error()))
		return false
	}
	return current
}

// 公告入队的 EventKey 绑公告 id，不绑正文。正文一改哈希就变，
// 「队列里有没有这条公告」会被误判成没有，编辑就再推一条。
func adtyPublicNotificationEventKey(id int) string {
	return fmt.Sprintf("pub:%d:public", id)
}

func adtyNotificationIsPrivateTelegram(row *model.AdtyNotificationPublication) bool {
	if row == nil {
		return false
	}
	if row.Audience == "team" || row.Audience == "user" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(row.TelegramChannel), adtyPrivateTelegramMarker)
}

func adtyActiveTeamUserIDsForSitesTx(tx *gorm.DB, siteIDs []int, staffSet map[int]bool) ([]int, error) {
	if tx == nil || len(siteIDs) == 0 || !tx.Migrator().HasTable(&model.AdtyTeam{}) {
		return nil, nil
	}
	var teams []model.AdtyTeam
	if err := tx.Select("id", "leader_user_id").
		Where("site_id IN ? AND status = ?", siteIDs, model.AdtyTeamStatusActive).
		Find(&teams).Error; err != nil {
		return nil, err
	}
	if len(teams) == 0 {
		return nil, nil
	}
	teamIDs := make([]int64, 0, len(teams))
	candidates := map[int]bool{}
	for _, team := range teams {
		teamIDs = append(teamIDs, team.Id)
		if team.LeaderUserId > 0 {
			candidates[team.LeaderUserId] = true
		}
	}
	if tx.Migrator().HasTable(&model.AdtyTeamMember{}) {
		var memberIDs []int
		if err := tx.Model(&model.AdtyTeamMember{}).
			Where("team_id IN ? AND status = ?", teamIDs, model.AdtyTeamMemberActive).
			Pluck("user_id", &memberIDs).Error; err != nil {
			return nil, err
		}
		for _, uid := range memberIDs {
			if uid > 0 {
				candidates[uid] = true
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	candList := make([]int, 0, len(candidates))
	for uid := range candidates {
		if !staffSet[uid] {
			candList = append(candList, uid)
		}
	}
	if len(candList) == 0 {
		return nil, nil
	}
	var validIDs []int
	if err := tx.Model(&model.User{}).
		Where("id IN ? AND status = ? AND site_id IN ? AND staff_site_id = 0", candList, common.UserStatusEnabled, siteIDs).
		Order("id ASC").
		Pluck("id", &validIDs).Error; err != nil {
		return nil, err
	}
	return validIDs, nil
}

func adtyNotificationTargetedRecipientSets(tx *gorm.DB, row *model.AdtyNotificationPublication, audiences []string) ([]int, []int, error) {
	staffSet := map[int]bool{}
	if tx.Migrator().HasTable(&model.AdtySiteStaff{}) {
		var staffIDs []int
		if err := tx.Model(&model.AdtySiteStaff{}).Distinct("user_id").Pluck("user_id", &staffIDs).Error; err != nil {
			return nil, nil, err
		}
		for _, uid := range staffIDs {
			if uid > 0 {
				staffSet[uid] = true
			}
		}
	}

	ownerSet := map[int]bool{}
	if (slices.Contains(audiences, "main_users") || slices.Contains(audiences, "all_site_owners")) && tx.Migrator().HasTable(&model.AdtySite{}) {
		var ownerIDs []int
		if err := tx.Model(&model.AdtySite{}).
			Where("status IN ? AND owner_user_id > 0", adtyOpenSiteStatuses).
			Distinct("owner_user_id").
			Pluck("owner_user_id", &ownerIDs).Error; err != nil {
			return nil, nil, err
		}
		for _, uid := range ownerIDs {
			if uid > 0 {
				ownerSet[uid] = true
			}
		}
	}

	userSet := map[int]bool{}
	adminSet := map[int]bool{}
	for _, aud := range audiences {
		switch aud {
		case "main_users":
			var ids []int
			if err := tx.Model(&model.User{}).
				Where("status = ? AND site_id = 0 AND staff_site_id = 0", common.UserStatusEnabled).
				Pluck("id", &ids).Error; err != nil {
				return nil, nil, err
			}
			for _, uid := range ids {
				if uid > 0 && !staffSet[uid] && !ownerSet[uid] {
					userSet[uid] = true
				}
			}
		case "all_site_owners":
			if len(ownerSet) == 0 {
				continue
			}
			ownerList := make([]int, 0, len(ownerSet))
			for uid := range ownerSet {
				ownerList = append(ownerList, uid)
			}
			var enabledOwners []int
			if err := tx.Model(&model.User{}).
				Where("id IN ? AND status = ?", ownerList, common.UserStatusEnabled).
				Pluck("id", &enabledOwners).Error; err != nil {
				return nil, nil, err
			}
			for _, uid := range enabledOwners {
				if uid > 0 {
					adminSet[uid] = true
				}
			}
		case "site_users":
			siteIDs := row.EffectiveTargetSiteIDs()
			if len(siteIDs) == 0 {
				continue
			}
			var ids []int
			if err := tx.Model(&model.User{}).
				Where("status = ? AND site_id IN ? AND staff_site_id = 0", common.UserStatusEnabled, siteIDs).
				Pluck("id", &ids).Error; err != nil {
				return nil, nil, err
			}
			for _, uid := range ids {
				if uid > 0 && !staffSet[uid] {
					userSet[uid] = true
				}
			}
		case "main_teams":
			ids, err := adtyActiveTeamUserIDsForSitesTx(tx, []int{0}, staffSet)
			if err != nil {
				return nil, nil, err
			}
			for _, uid := range ids {
				if uid > 0 {
					userSet[uid] = true
				}
			}
		case "site_teams":
			siteIDs := row.EffectiveTargetSiteIDs()
			if len(siteIDs) == 0 {
				continue
			}
			ids, err := adtyActiveTeamUserIDsForSitesTx(tx, siteIDs, staffSet)
			if err != nil {
				return nil, nil, err
			}
			for _, uid := range ids {
				if uid > 0 {
					userSet[uid] = true
				}
			}
		}
	}

	userIDs := make([]int, 0, len(userSet))
	for uid := range userSet {
		userIDs = append(userIDs, uid)
	}
	slices.Sort(userIDs)

	adminIDs := make([]int, 0, len(adminSet))
	for uid := range adminSet {
		adminIDs = append(adminIDs, uid)
	}
	slices.Sort(adminIDs)
	return userIDs, adminIDs, nil
}

func adtyNotificationPrivateRecipientSets(tx *gorm.DB, row *model.AdtyNotificationPublication) ([]int, []int, error) {
	if tx == nil || row == nil {
		return nil, nil, nil
	}
	if audiences := row.ParsedTargetAudiences(); len(audiences) > 0 {
		return adtyNotificationTargetedRecipientSets(tx, row, audiences)
	}
	if row.IssuerSiteId > 0 && row.TargetSiteId != row.IssuerSiteId {
		return nil, nil, nil
	}
	switch row.Audience {
	case "user":
		uidStr, ok := strings.CutPrefix(row.TargetSiteName, "user:")
		if !ok {
			return nil, nil, nil
		}
		uid, err := strconv.Atoi(uidStr)
		if err != nil || uid <= 0 {
			return nil, nil, nil
		}
		var user model.User
		lookup := tx.Select("id", "site_id", "staff_site_id", "status").
			Where("id = ? AND status = ? AND staff_site_id = 0", uid, common.UserStatusEnabled).
			Limit(1).Find(&user)
		if lookup.Error != nil {
			return nil, nil, lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return nil, nil, nil
		}
		if user.SiteId != row.TargetSiteId {
			// Site owner accounts always have users.site_id = 0; allow Root-issued
			// historical user notices that recorded the owned open site ID.
			ownerMatch := false
			if row.IssuerSiteId == 0 && row.TargetSiteId > 0 && user.SiteId == 0 && tx.Migrator().HasTable(&model.AdtySite{}) {
				var ownedCount int64
				if err := tx.Model(&model.AdtySite{}).
					Where("id = ? AND owner_user_id = ? AND status IN ?", row.TargetSiteId, user.Id, adtyOpenSiteStatuses).
					Count(&ownedCount).Error; err != nil {
					return nil, nil, err
				}
				ownerMatch = ownedCount > 0
			}
			if !ownerMatch {
				return nil, nil, nil
			}
		}
		isStaff, err := adtyUserIsStaffWithDB(tx, &user)
		if err != nil {
			return nil, nil, err
		}
		if isStaff {
			return nil, nil, nil
		}
		return []int{user.Id}, nil, nil
	case "team":
		tidStr, ok := strings.CutPrefix(row.TargetSiteName, "team:")
		if !ok {
			return nil, nil, nil
		}
		tid, err := strconv.ParseInt(tidStr, 10, 64)
		if err != nil || tid <= 0 {
			return nil, nil, nil
		}
		if !tx.Migrator().HasTable(&model.AdtyTeam{}) || !tx.Migrator().HasTable(&model.AdtyTeamMember{}) {
			return nil, nil, nil
		}
		var team model.AdtyTeam
		teamLookup := tx.Select("id").
			Where("id = ? AND status = ? AND site_id = ?", tid, model.AdtyTeamStatusActive, row.TargetSiteId).
			Limit(1).Find(&team)
		if teamLookup.Error != nil {
			return nil, nil, teamLookup.Error
		}
		if teamLookup.RowsAffected == 0 {
			return nil, nil, nil
		}
		var memberIDs []int
		if err := tx.Model(&model.AdtyTeamMember{}).
			Where("team_id = ? AND status = ?", tid, model.AdtyTeamMemberActive).
			Order("user_id ASC").Pluck("user_id", &memberIDs).Error; err != nil {
			return nil, nil, err
		}
		if len(memberIDs) == 0 {
			return nil, nil, nil
		}
		staffSet := map[int]bool{}
		if tx.Migrator().HasTable(&model.AdtySiteStaff{}) {
			var staffIDs []int
			if err := tx.Model(&model.AdtySiteStaff{}).
				Where("user_id IN ?", memberIDs).
				Distinct("user_id").Pluck("user_id", &staffIDs).Error; err != nil {
				return nil, nil, err
			}
			for _, sid := range staffIDs {
				if sid > 0 {
					staffSet[sid] = true
				}
			}
		}
		candidates := make([]int, 0, len(memberIDs))
		for _, uid := range memberIDs {
			if uid > 0 && !staffSet[uid] {
				candidates = append(candidates, uid)
			}
		}
		if len(candidates) == 0 {
			return nil, nil, nil
		}
		var userIDs []int
		if err := tx.Model(&model.User{}).
			Where("id IN ? AND status = ? AND site_id = ? AND staff_site_id = 0", candidates, common.UserStatusEnabled, row.TargetSiteId).
			Order("id ASC").Pluck("id", &userIDs).Error; err != nil {
			return nil, nil, err
		}
		return userIDs, nil, nil
	default:
		return nil, nil, nil
	}
}

func queueAdtyPrivatePublicationTelegramTx(tx *gorm.DB, row *model.AdtyNotificationPublication) error {
	userIDs, adminIDs, err := adtyNotificationPrivateRecipientSets(tx, row)
	if err != nil {
		return err
	}
	body := adtyEnsureTelegramBilingualFallback(redactAdtySecrets(announceTelegramBodyWithDB(tx, *row)))
	if body == "" {
		return nil
	}
	enqueueGroup := func(purpose string, eventPrefix string, recipientIDs []int) error {
		for _, uid := range recipientIDs {
			eventKey := fmt.Sprintf("pub:%d:%s:%d", row.Id, eventPrefix, uid)
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
			if err := tx.Select("id", "site_id", "staff_site_id", "status", "role").Where("id = ?", uid).Limit(1).Find(&user).Error; err != nil {
				return err
			}
			var binding model.AdtyTelegramBinding
			found := tx.Where("user_id = ? AND purpose = ? AND status = ?", uid, purpose, model.AdtyTelegramBindActive).Limit(1).Find(&binding)
			if found.Error != nil {
				return found.Error
			}
			eligible := found.RowsAffected > 0 && user.Id > 0 && user.Status == common.UserStatusEnabled && user.SiteId == binding.SiteId
			if purpose == model.AdtyTelegramPurposeUser && user.StaffSiteId > 0 {
				eligible = false
				delivery.LastError = "staff cannot use user bot"
			}
			if eligible && purpose == model.AdtyTelegramPurposeAdmin {
				adminOK, err := adtyTelegramAdminAccess(tx, &user)
				if err != nil {
					return err
				}
				eligible = adminOK
			}
			if eligible {
				siteOpen, err := adtyTelegramSiteAccess(tx, binding.SiteId)
				if err != nil {
					return err
				}
				if siteOpen {
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
			if err := tx.Create(&delivery).Error; err != nil {
				return err
			}
		}
		return nil
	}
	if err := enqueueGroup(model.AdtyTelegramPurposeUser, "user", userIDs); err != nil {
		return err
	}
	return enqueueGroup(model.AdtyTelegramPurposeAdmin, "admin", adminIDs)
}

func adtyNotificationHasTelegramOutbox(tx *gorm.DB, row *model.AdtyNotificationPublication) bool {
	if row == nil || row.Id <= 0 {
		return true
	}
	if adtyNotificationIsPrivateTelegram(row) {
		userIDs, adminIDs, err := adtyNotificationPrivateRecipientSets(tx, row)
		if err != nil || (len(userIDs) == 0 && len(adminIDs) == 0) {
			return true
		}
		for _, uid := range userIDs {
			var count int64
			eventKey := fmt.Sprintf("pub:%d:user:%d", row.Id, uid)
			if tx.Model(&model.AdtyTelegramDelivery{}).Where("event_key = ?", eventKey).Count(&count).Error == nil && count == 0 {
				return false
			}
		}
		for _, uid := range adminIDs {
			var count int64
			eventKey := fmt.Sprintf("pub:%d:admin:%d", row.Id, uid)
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

// RecoverAdtyNotificationTelegramOutbox inspects recent published notifications
// that requested Telegram sync and have not yet finished outbox seeding, using
// a bounded 7-day window and cursor pagination so it neither full-scans old
// history nor retroactively pushes historical team notices to new members.
func RecoverAdtyNotificationTelegramOutbox() {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyNotificationPublication{}) || !model.DB.Migrator().HasTable(&model.AdtyTelegramDelivery{}) {
		return
	}
	hasSeededCol := model.DB.Migrator().HasColumn(&model.AdtyNotificationPublication{}, "telegram_outbox_seeded_at")
	hasTranslationTable := model.DB.Migrator().HasTable(&model.AdtyTranslationHead{})
	now := time.Now()
	cutoff := now.Add(-7 * 24 * time.Hour).Unix()
	graceCutoff := now.Add(-2 * time.Minute).Unix()
	const batchSize = 50
	const maxPerTick = 200
	after := 0
	processed := 0
	for processed < maxPerTick {
		q := model.DB.Where("status = ? AND telegram_channel <> ? AND published_at >= ? AND updated_at <= ? AND id > ?", "published", "", cutoff, graceCutoff, after)
		if hasSeededCol {
			q = q.Where("telegram_outbox_seeded_at IS NULL")
		}
		var pubs []model.AdtyNotificationPublication
		if err := q.Order("id ASC").Limit(batchSize).Find(&pubs).Error; err != nil || len(pubs) == 0 {
			return
		}
		for i := range pubs {
			pub := &pubs[i]
			after = pub.Id
			processed++
			if adtyNotificationHasTelegramOutbox(model.DB, pub) {
				if hasSeededCol {
					seedNow := time.Now().Unix()
					_ = model.DB.Model(&model.AdtyNotificationPublication{}).
						Where("id = ? AND telegram_outbox_seeded_at IS NULL", pub.Id).
						Update("telegram_outbox_seeded_at", seedNow).Error
				}
				continue
			}
			// R12-037: Skip publications whose initial translation is actively leased so
			// recovery does not race ahead and enqueue untranslated Chinese content.
			if hasTranslationTable && (adtyContainsChinese(pub.Content) || adtyContainsChinese(pub.Title)) {
				var pendingCount int64
				keys := []string{adtyNotificationTranslationKey(pub.Id), adtyNotificationTitleTranslationKey(pub.Id)}
				if model.DB.Model(&model.AdtyTranslationHead{}).
					Where("object_key IN ? AND status = ? AND lease_until > ?", keys, model.AdtyTranslationPending, time.Now().Unix()).
					Count(&pendingCount).Error == nil && pendingCount > 0 {
					continue
				}
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
			if err := enqueueAdtyNotificationToTelegram(pub, issuer, true); err != nil {
				common.SysError(common.LogText("recover notification %d telegram outbox failed: %s", pub.Id, err.Error()))
			}
			if processed >= maxPerTick {
				return
			}
		}
		if len(pubs) < batchSize {
			return
		}
	}
}

// pushAdtyNotificationToTelegram 尝试把公告推一次到频道（或私聊队列），并保证
// `telegram_at` 占位、投递队列写入与 `telegram_outbox_seeded_at` 置位在同一个数据库事务内原子提交。
func pushAdtyNotificationToTelegram(row *model.AdtyNotificationPublication, issuer *model.AdtySite) error {
	return enqueueAdtyNotificationToTelegram(row, issuer, false)
}

func enqueueAdtyNotificationToTelegram(row *model.AdtyNotificationPublication, issuer *model.AdtySite, allowRecovery bool) error {
	if row == nil || model.DB == nil {
		return nil
	}
	at := time.Now().Unix()
	hasSeededCol := model.DB.Migrator().HasColumn(&model.AdtyNotificationPublication{}, "telegram_outbox_seeded_at")
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var current model.AdtyNotificationPublication
		q := model.AdtyLockForUpdate(tx)
		if !hasSeededCol {
			q = q.Omit("telegram_outbox_seeded_at")
		}
		if err := q.Where("id = ?", row.Id).First(&current).Error; err != nil {
			return err
		}
		if current.Status != "published" {
			return nil
		}
		if !allowRecovery && row.Revision > 0 && current.Revision != row.Revision {
			return nil
		}
		if hasSeededCol && current.TelegramOutboxSeededAt != nil {
			return nil
		}
		channel := strings.TrimSpace(current.TelegramChannel)
		if channel == "" {
			return nil
		}
		purpose := model.AdtyTelegramPurposeAdmin
		if current.IssuerSiteId > 0 {
			purpose = model.AdtyTelegramPurposeUser
		}
		res := tx.Model(&model.AdtyNotificationPublication{}).
			Where("id = ? AND telegram_at = 0 AND status = ?", current.Id, "published").
			Update("telegram_at", at)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// The slot is already taken. Later edits must not enqueue another
			// message just because the body changed. Recovery may fill a missing
			// outbox row, and only that.
			if !allowRecovery || adtyNotificationHasTelegramOutbox(tx, &current) {
				if hasSeededCol {
					_ = tx.Model(&model.AdtyNotificationPublication{}).
						Where("id = ? AND telegram_outbox_seeded_at IS NULL", current.Id).
						Update("telegram_outbox_seeded_at", at).Error
				}
				return nil
			}
		}
		if adtyNotificationIsPrivateTelegram(&current) {
			if err := queueAdtyPrivatePublicationTelegramTx(tx, &current); err != nil {
				return err
			}
		} else {
			if err := queueAdtyPublicTelegramTx(tx, channel, purpose, announceTelegramBodyWithDB(tx, current), adtyPublicNotificationEventKey(current.Id)); err != nil {
				// R2-055: Permanent content/configuration rejections must persist a skipped
				// outbox row and mark telegram_outbox_seeded_at instead of retrying every minute for 7 days.
				if errors.Is(err, ErrAdtyTelegramPrivate) || errors.Is(err, ErrAdtyTelegramInvalid) {
					lastErr := truncateAdtyRunes(err.Error(), 128)
					skipRow := model.AdtyTelegramDelivery{
						Public:    true,
						Purpose:   purpose,
						Channel:   truncateAdtyRunes(channel, 128),
						EventKey:  adtyPublicNotificationEventKey(current.Id),
						Body:      "",
						Status:    model.AdtyTelegramDeliverySkip,
						LastError: lastErr,
					}
					_ = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&skipRow).Error
				} else {
					return err
				}
			}
		}
		if hasSeededCol {
			if err := tx.Model(&model.AdtyNotificationPublication{}).
				Where("id = ? AND telegram_outbox_seeded_at IS NULL", current.Id).
				Update("telegram_outbox_seeded_at", at).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		row.TelegramAt = 0
		common.SysError(common.LogText("queue announcement %d for telegram failed: %s", row.Id, err.Error()))
		return err
	}
	var refreshed model.AdtyNotificationPublication
	if model.DB.Select("telegram_at").Where("id = ?", row.Id).Limit(1).Find(&refreshed).RowsAffected > 0 {
		row.TelegramAt = refreshed.TelegramAt
	}
	return nil
}

// announceTelegramBody 把公告压成一条频道消息：标题 + 正文。
// 正文已经在上面的校验里 TrimSpace 过，这里只做拼接，不重新解释内容。
func announceTelegramBody(row model.AdtyNotificationPublication) string {
	return announceTelegramBodyWithDB(model.DB, row)
}

func adtyTitleHasBilingualEnglish(title string) bool {
	_, after, ok := strings.Cut(title, " / ")
	if !ok {
		return false
	}
	after = strings.TrimSpace(after)
	return after != "" && !adtyContainsChinese(after) && adtyContainsASCIILatin(after)
}

func announceTelegramBodyWithDB(db *gorm.DB, row model.AdtyNotificationPublication) string {
	var bodyHead *model.AdtyTranslationHead
	var titleHead *model.AdtyTranslationHead
	if row.Id > 0 && db != nil && db.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		keys := []string{adtyNotificationTranslationKey(row.Id), adtyNotificationTitleTranslationKey(row.Id)}
		var heads []model.AdtyTranslationHead
		if db.Where("object_key IN ?", keys).Find(&heads).Error == nil {
			for i := range heads {
				switch heads[i].ObjectKey {
				case keys[0]:
					h := heads[i]
					bodyHead = &h
				case keys[1]:
					h := heads[i]
					titleHead = &h
				}
			}
		}
	}
	rawContent := strings.TrimSpace(row.Content)
	bodySrc := stripAdtyNotificationTranslationSuffix(rawContent, "")
	bodyReady := false
	isHTML := strings.EqualFold(strings.TrimSpace(row.ContentFormat), "html")
	if bodyHead != nil {
		candidateSrc := stripAdtyNotificationTranslationSuffix(rawContent, bodyHead.English)
		if adtyTranslationHeadMatchesSource(*bodyHead, candidateSrc) {
			bodySrc = candidateSrc
			eng := strings.TrimSpace(bodyHead.English)
			if isHTML {
				eng = strings.TrimSpace(adtyNotificationHTMLToTelegramText(SanitizeAdtyNotificationHTML(eng)))
			}
			if bodyHead.Status == model.AdtyTranslationReady && eng != "" && !isAdtyTranslationNotReadyText(eng) {
				bodyReady = true
			}
		}
	}
	content := resolveAdtyNotificationDisplayContentWithHead(row, bodyHead)
	if isHTML {
		content = adtyNotificationHTMLToTelegramText(content)
	}
	content = adtyEnsureTelegramBilingualFallback(content)
	bodyNeedsNotReady := adtyContainsChinese(bodySrc) && !bodyReady
	rawTitle := strings.TrimSpace(row.Title)
	if rawTitle == "" {
		if content != "" && bodyNeedsNotReady && !strings.Contains(strings.ToLower(content), strings.ToLower(adtyTranslationNotReadyEn)) {
			content += "\n\n" + adtyTranslationNotReadyEn
		}
		return content
	}
	title := rawTitle
	titleReady := false
	if titleHead != nil && titleHead.Status == model.AdtyTranslationReady && strings.TrimSpace(titleHead.English) != "" && adtyTranslationHeadMatchesSource(*titleHead, rawTitle) {
		titleEn := strings.TrimSpace(titleHead.English)
		if !isAdtyTranslationNotReadyText(titleEn) {
			titleReady = true
			if titleEn != rawTitle {
				title = rawTitle + " / " + titleEn
			}
		}
	}
	body := adtyEnsureTelegramBilingualFallback(title + "\n\n" + content)
	titleNeedsNotReady := adtyContainsChinese(rawTitle) && !titleReady && !adtyTitleHasBilingualEnglish(rawTitle)
	if body != "" && (bodyNeedsNotReady || titleNeedsNotReady) {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(adtyTranslationNotReadyEn)) {
			body += "\n\n" + adtyTranslationNotReadyEn
		}
	}
	return body
}

func resolveAdtyNotificationDisplayContent(row model.AdtyNotificationPublication) string {
	return resolveAdtyNotificationDisplayContentWithDB(model.DB, row)
}

func resolveAdtyNotificationDisplayContentWithDB(db *gorm.DB, row model.AdtyNotificationPublication) string {
	var headPtr *model.AdtyTranslationHead
	if row.Id > 0 && db != nil && adtyContainsChinese(strings.TrimSpace(row.Content)) && db.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		var head model.AdtyTranslationHead
		if db.Where("object_key = ?", adtyNotificationTranslationKey(row.Id)).Limit(1).Find(&head).RowsAffected > 0 {
			headPtr = &head
		}
	}
	return resolveAdtyNotificationDisplayContentWithHead(row, headPtr)
}

func resolveAdtyNotificationDisplayContentWithHead(row model.AdtyNotificationPublication, head *model.AdtyTranslationHead) string {
	content := strings.TrimSpace(row.Content)
	isHTML := strings.EqualFold(strings.TrimSpace(row.ContentFormat), "html")
	if row.Id > 0 && head != nil && adtyContainsChinese(content) {
		currentSrc := stripAdtyNotificationTranslationSuffix(content, head.English)
		if head.Status == model.AdtyTranslationReady && strings.TrimSpace(head.English) != "" && !isAdtyTranslationNotReadyText(head.English) && adtyTranslationHeadMatchesSource(*head, currentSrc) {
			eng := strings.TrimSpace(head.English)
			if isHTML {
				eng = SanitizeAdtyNotificationHTML(eng)
			}
			src := strings.TrimSpace(head.Source)
			// Guard against legacy rows where title was stored in notification:<id>
			// while the body itself was pure English.
			if src != "" && src == strings.TrimSpace(row.Title) && !adtyContainsChinese(currentSrc) {
				if isHTML {
					return SanitizeAdtyNotificationHTML(content)
				}
				return content
			}
			if src == "" {
				src = currentSrc
			}
			if isHTML {
				src = SanitizeAdtyNotificationHTML(src)
			}
			if eng != "" && !strings.Contains(src, eng) {
				return src + "\n\n" + eng
			}
			if src != "" {
				return src
			}
		}
	}
	if isHTML {
		return SanitizeAdtyNotificationHTML(content)
	}
	return content
}

func adtyUserActiveTeamTargets(userID, siteID int) ([]string, error) {
	return adtyUserActiveTeamTargetsWithDB(model.DB, userID, siteID)
}

func adtyUserActiveTeamTargetsWithDB(db *gorm.DB, userID, siteID int) ([]string, error) {
	if db == nil {
		db = model.DB
	}
	if userID <= 0 || db == nil || !db.Migrator().HasTable(&model.AdtyTeam{}) || !db.Migrator().HasTable(&model.AdtyTeamMember{}) {
		return nil, nil
	}
	var memberTeamIDs []int64
	if err := db.Model(&model.AdtyTeamMember{}).
		Where("user_id = ? AND status = ?", userID, model.AdtyTeamMemberActive).
		Pluck("team_id", &memberTeamIDs).Error; err != nil {
		return nil, err
	}
	if len(memberTeamIDs) == 0 {
		return nil, nil
	}
	var activeTeamIDs []int64
	if err := db.Model(&model.AdtyTeam{}).
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

func adtyUserIsStaffWithDB(db *gorm.DB, user *model.User) (bool, error) {
	if user == nil || user.Id <= 0 {
		return false, nil
	}
	if user.StaffSiteId > 0 {
		return true, nil
	}
	if db == nil {
		db = model.DB
	}
	if db == nil || !db.Migrator().HasTable(&model.AdtySiteStaff{}) {
		return false, nil
	}
	var count int64
	if err := db.Model(&model.AdtySiteStaff{}).Where("user_id = ?", user.Id).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func adtyUserOwnsOpenSiteWithDB(db *gorm.DB, userID int) (bool, error) {
	if db == nil {
		db = model.DB
	}
	if db == nil || userID <= 0 || !db.Migrator().HasTable(&model.AdtySite{}) {
		return false, nil
	}
	var count int64
	if err := db.Model(&model.AdtySite{}).
		Where("owner_user_id = ? AND status IN ?", userID, adtyOpenSiteStatuses).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func adtyUserOwnedOpenSiteIDsWithDB(db *gorm.DB, userID int) ([]int, error) {
	if db == nil {
		db = model.DB
	}
	if db == nil || userID <= 0 || !db.Migrator().HasTable(&model.AdtySite{}) {
		return nil, nil
	}
	var ids []int
	if err := db.Model(&model.AdtySite{}).
		Where("id > 0 AND owner_user_id = ? AND status IN ?", userID, adtyOpenSiteStatuses).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func adtyUserOwnsSpecificOpenSiteWithDB(db *gorm.DB, userID, siteID int) (bool, error) {
	if db == nil {
		db = model.DB
	}
	if db == nil || userID <= 0 || siteID <= 0 || !db.Migrator().HasTable(&model.AdtySite{}) {
		return false, nil
	}
	var count int64
	if err := db.Model(&model.AdtySite{}).
		Where("id = ? AND owner_user_id = ? AND status IN ?", siteID, userID, adtyOpenSiteStatuses).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func adtyUserInActiveTeamOnSiteWithDB(db *gorm.DB, userID, siteID int) (bool, error) {
	if db == nil {
		db = model.DB
	}
	if db == nil || userID <= 0 || !db.Migrator().HasTable(&model.AdtyTeam{}) {
		return false, nil
	}
	targets, err := adtyUserActiveTeamTargetsWithDB(db, userID, siteID)
	if err != nil {
		return false, err
	}
	if len(targets) > 0 {
		return true, nil
	}
	var leaderCount int64
	if err := db.Model(&model.AdtyTeam{}).
		Where("site_id = ? AND leader_user_id = ? AND status = ?", siteID, userID, model.AdtyTeamStatusActive).
		Count(&leaderCount).Error; err != nil {
		return false, err
	}
	return leaderCount > 0, nil
}

func adtyUserCanReadPublication(user *model.User, pub *model.AdtyNotificationPublication) bool {
	ok, err := adtyUserCanReadPublicationWithErr(user, pub)
	return err == nil && ok
}

func adtyUserCanReadPublicationWithErr(user *model.User, pub *model.AdtyNotificationPublication) (bool, error) {
	if user == nil || pub == nil || user.Id <= 0 || user.Status != common.UserStatusEnabled || pub.Status != "published" {
		return false, nil
	}
	if audiences := pub.ParsedTargetAudiences(); len(audiences) > 0 {
		isStaff, err := adtyUserIsStaffWithDB(model.DB, user)
		if err != nil {
			return false, err
		}
		isSiteOwner, err := adtyUserOwnsOpenSiteWithDB(model.DB, user.Id)
		if err != nil {
			return false, err
		}
		var firstErr error
		for _, aud := range audiences {
			switch aud {
			case "main_users":
				if user.SiteId == 0 && !isSiteOwner && !isStaff {
					return true, nil
				}
			case "all_site_owners":
				if isSiteOwner {
					return true, nil
				}
			case "site_users":
				if !isStaff && user.SiteId > 0 && slices.Contains(pub.EffectiveTargetSiteIDs(), user.SiteId) {
					return true, nil
				}
			case "main_teams":
				if !isStaff && user.SiteId == 0 {
					inTeam, err := adtyUserInActiveTeamOnSiteWithDB(model.DB, user.Id, 0)
					if err != nil {
						if firstErr == nil {
							firstErr = err
						}
						continue
					}
					if inTeam {
						return true, nil
					}
				}
			case "site_teams":
				if !isStaff && user.SiteId > 0 && slices.Contains(pub.EffectiveTargetSiteIDs(), user.SiteId) {
					inTeam, err := adtyUserInActiveTeamOnSiteWithDB(model.DB, user.Id, user.SiteId)
					if err != nil {
						if firstErr == nil {
							firstErr = err
						}
						continue
					}
					if inTeam {
						return true, nil
					}
				}
			}
		}
		if firstErr != nil {
			return false, firstErr
		}
		return false, nil
	}
	switch pub.Audience {
	case "all_sites", "all_users", "all":
		return true, nil
	case "site":
		return user.SiteId == pub.TargetSiteId && pub.TargetSiteId > 0, nil
	case "user":
		if user.StaffSiteId != 0 || pub.TargetSiteName != fmt.Sprintf("user:%d", user.Id) {
			return false, nil
		}
		if pub.IssuerSiteId > 0 && pub.TargetSiteId != pub.IssuerSiteId {
			return false, nil
		}
		isStaff, err := adtyUserIsStaffWithDB(model.DB, user)
		if err != nil {
			return false, err
		}
		if isStaff {
			return false, nil
		}
		if user.SiteId == pub.TargetSiteId {
			return true, nil
		}
		if pub.IssuerSiteId == 0 && pub.TargetSiteId > 0 && user.SiteId == 0 {
			owned, err := adtyUserOwnsSpecificOpenSiteWithDB(model.DB, user.Id, pub.TargetSiteId)
			if err != nil {
				return false, err
			}
			return owned, nil
		}
		return false, nil
	case "team":
		if user.StaffSiteId != 0 || user.SiteId != pub.TargetSiteId {
			return false, nil
		}
		targets, err := adtyUserActiveTeamTargets(user.Id, pub.TargetSiteId)
		if err != nil {
			return false, err
		}
		return slices.Contains(targets, pub.TargetSiteName), nil
	default:
		return false, nil
	}
}

type adtyNotificationViewerContext struct {
	UserID         int
	UserSiteID     int
	IsStaff        bool
	IsSiteOwner    bool
	InMainTeam     bool
	ViewerSite     *model.AdtySite
	OwnsViewerSite bool
}

type adtyNotificationPreload struct {
	HasTranslationTable bool
	TranslationHeads    map[string]model.AdtyTranslationHead
	HasSiteTable        bool
	IssuerSites         map[int]model.AdtySite
	HasDispositionTable bool
	OpenAnomalyRefs     map[string]bool
	Viewer              *adtyNotificationViewerContext
}

func adtyQuoteColumn(db *gorm.DB, name string) string {
	if db != nil && db.Dialector != nil {
		var sb strings.Builder
		db.Dialector.QuoteTo(&sb, name)
		return sb.String()
	}
	if db != nil && db.Statement != nil {
		return db.Statement.Quote(name)
	}
	return name
}

func adtyPublicationNotHiddenSQL(db *gorm.DB) string {
	keyCol := adtyQuoteColumn(db, "key")
	pubIDExpr := "'publication:' || CAST(adty_notification_publications.id AS TEXT)"
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "mysql" {
		pubIDExpr = "CONCAT('publication:', adty_notification_publications.id)"
	}
	return "NOT EXISTS (SELECT 1 FROM adty_notification_hides h WHERE h.user_id = ? AND (h." + keyCol + " = adty_notification_publications.read_key OR h." + keyCol + " = " + pubIDExpr + "))"
}

func adtyTicketNoticeNotHiddenSQL(db *gorm.DB) string {
	keyCol := adtyQuoteColumn(db, "key")
	return "NOT EXISTS (SELECT 1 FROM adty_notification_hides h WHERE h.user_id = ? AND h." + keyCol + " = adty_ticket_notices.read_key)"
}

func preloadAdtyNotificationBatch(db *gorm.DB, rows []model.AdtyNotificationPublication, viewer *adtyNotificationViewerContext) *adtyNotificationPreload {
	preloaded := &adtyNotificationPreload{
		TranslationHeads: map[string]model.AdtyTranslationHead{},
		IssuerSites:      map[int]model.AdtySite{},
		OpenAnomalyRefs:  map[string]bool{},
		Viewer:           viewer,
	}
	if db == nil || len(rows) == 0 {
		return preloaded
	}
	transKeys := make([]string, 0, len(rows)*2)
	transSeen := make(map[int]struct{}, len(rows))
	issuerSiteIDs := make([]int, 0)
	issuerSeen := make(map[int]struct{})
	pubPtrs := make([]*model.AdtyNotificationPublication, 0, len(rows))
	for i := range rows {
		pubPtrs = append(pubPtrs, &rows[i])
		if rows[i].Id > 0 {
			if _, ok := transSeen[rows[i].Id]; !ok {
				transSeen[rows[i].Id] = struct{}{}
				transKeys = append(transKeys,
					adtyNotificationTranslationKey(rows[i].Id),
					adtyNotificationTitleTranslationKey(rows[i].Id),
				)
			}
		}
		if rows[i].IssuerSiteId > 0 {
			if _, ok := issuerSeen[rows[i].IssuerSiteId]; !ok {
				issuerSeen[rows[i].IssuerSiteId] = struct{}{}
				issuerSiteIDs = append(issuerSiteIDs, rows[i].IssuerSiteId)
			}
		}
	}
	if len(transKeys) > 0 && db.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		preloaded.HasTranslationTable = true
		var heads []model.AdtyTranslationHead
		if err := db.Where("object_key IN ?", transKeys).Find(&heads).Error; err == nil {
			for _, head := range heads {
				preloaded.TranslationHeads[head.ObjectKey] = head
			}
		}
	}
	if len(issuerSiteIDs) > 0 && db.Migrator().HasTable(&model.AdtySite{}) {
		preloaded.HasSiteTable = true
		var sites []model.AdtySite
		if err := db.Select("id", "subdomain", "root_domain", "brand").Where("id IN ?", issuerSiteIDs).Find(&sites).Error; err == nil {
			for _, site := range sites {
				preloaded.IssuerSites[site.Id] = site
			}
		}
	}
	openRefs, hasDisp := adtyBatchOpenAnomalyRefs(db, pubPtrs)
	preloaded.HasDispositionTable = hasDisp
	if openRefs != nil {
		preloaded.OpenAnomalyRefs = openRefs
	}
	return preloaded
}

// A matching Host alone does not entitle an unrelated account to site content.
// Account membership is read from SQL rather than client parameters or caches.
func adtyVisibleNotificationPublications(userID int, site *model.AdtySite) (*gorm.DB, error) {
	return adtyVisibleNotificationPublicationsWithDB(model.DB, userID, site)
}

func adtyVisibleNotificationPublicationsWithDB(db *gorm.DB, userID int, site *model.AdtySite) (*gorm.DB, error) {
	query, _, err := adtyVisibleNotificationPublicationsWithViewer(db, userID, site)
	return query, err
}

func adtyVisibleNotificationPublicationsWithViewer(db *gorm.DB, userID int, site *model.AdtySite) (*gorm.DB, adtyNotificationViewerContext, error) {
	var viewer adtyNotificationViewerContext
	if db == nil {
		db = model.DB
	}
	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		return nil, viewer, err
	}
	query := db.Model(&model.AdtyNotificationPublication{}).Where("status = ?", "published")
	userTarget := fmt.Sprintf("user:%d", userID)
	// 可见性规则（A.18.2 / R-32 #12）：
	// 旧记录（target_audiences 为空）：
	//   all_users / all_sites / all -> 全平台可见
	//   site + target_site_id       -> 目标分站成员与站长可见
	//   team / user                 -> 同站对应活跃团队成员或本人可见
	// 新记录（target_audiences 非空，多选并集）：
	//   main_users                  -> 仅主站 site_id=0 且非站长且非员工用户可见，绝不出现在分站
	//   all_site_owners             -> 仅活跃/暂停/关站中分站的站长可见
	//   site_users + target_site_ids-> 仅所选分站（或分站自发）的非员工用户（含团队成员/负责人）可见
	//   main_teams                  -> 仅主站 site_id=0 活跃团队成员与负责人可见
	//   site_teams                  -> 仅该分站活跃团队成员与负责人可见
	platformAudiences := []string{"all_users", "all_sites", "all"}
	hasTargetAudiences := db.Migrator().HasColumn(&model.AdtyNotificationPublication{}, "target_audiences")
	hasTargetSiteIDs := db.Migrator().HasColumn(&model.AdtyNotificationPublication{}, "target_site_ids")

	isStaff, err := adtyUserIsStaffWithDB(db, &user)
	if err != nil {
		return nil, viewer, err
	}
	var ownedOpenSiteIDs []int
	if user.SiteId == 0 && user.StaffSiteId == 0 {
		ownedOpenSiteIDs, err = adtyUserOwnedOpenSiteIDsWithDB(db, userID)
		if err != nil {
			return nil, viewer, err
		}
	}
	isSiteOwner := len(ownedOpenSiteIDs) > 0
	if !isSiteOwner && (user.SiteId != 0 || user.StaffSiteId != 0) {
		isSiteOwner, err = adtyUserOwnsOpenSiteWithDB(db, userID)
		if err != nil {
			return nil, viewer, err
		}
	}
	viewer = adtyNotificationViewerContext{
		UserID:      user.Id,
		UserSiteID:  user.SiteId,
		IsStaff:     isStaff,
		IsSiteOwner: isSiteOwner,
	}

	clauses := make([]string, 0, 8)
	args := make([]any, 0, 16)
	addClause := func(sql string, clauseArgs ...any) {
		clauses = append(clauses, "("+sql+")")
		args = append(args, clauseArgs...)
	}

	if hasTargetAudiences {
		addClause("COALESCE(target_audiences, '') IN ('', '[]') AND audience IN ?", platformAudiences)
	} else {
		addClause("audience IN ?", platformAudiences)
	}

	if site == nil {
		if user.SiteId == 0 && !isStaff {
			addClause("audience = ? AND target_site_id = 0 AND target_site_name = ?", "user", userTarget)
			if user.StaffSiteId == 0 && len(ownedOpenSiteIDs) > 0 {
				if hasTargetAudiences {
					addClause("COALESCE(target_audiences, '') IN ('', '[]') AND audience = ? AND issuer_site_id = 0 AND target_site_id IN ? AND target_site_name = ?", "user", ownedOpenSiteIDs, userTarget)
				} else {
					addClause("audience = ? AND issuer_site_id = 0 AND target_site_id IN ? AND target_site_name = ?", "user", ownedOpenSiteIDs, userTarget)
				}
			}
			teamTargets, err := adtyUserActiveTeamTargetsWithDB(db, userID, 0)
			if err != nil {
				return nil, viewer, err
			}
			if len(teamTargets) > 0 {
				addClause("audience = ? AND target_site_id = 0 AND target_site_name IN ?", "team", teamTargets)
			}
			if hasTargetAudiences {
				if !isSiteOwner {
					addClause("target_audiences LIKE ?", `%"main_users"%`)
				}
				inMainTeam := len(teamTargets) > 0
				if !inMainTeam {
					inMainTeam, err = adtyUserInActiveTeamOnSiteWithDB(db, userID, 0)
					if err != nil {
						return nil, viewer, err
					}
				}
				viewer.InMainTeam = inMainTeam
				if inMainTeam {
					addClause("target_audiences LIKE ?", `%"main_teams"%`)
				}
			}
		}
		if hasTargetAudiences && isSiteOwner {
			addClause("target_audiences LIKE ?", `%"all_site_owners"%`)
		}
		return query.Where(strings.Join(clauses, " OR "), args...), viewer, nil
	}

	var current model.AdtySite
	if err := db.First(&current, site.Id).Error; err != nil {
		return nil, viewer, err
	}
	viewer.ViewerSite = &current
	viewer.OwnsViewerSite = isSiteOwner && current.OwnerUserId == userID
	if user.SiteId != site.Id && current.OwnerUserId != userID {
		// 只是访问了这个域名、并不属于这个站：平台级公告照常，站内定向内容不给。
		return query.Where(strings.Join(clauses, " OR "), args...), viewer, nil
	}

	if hasTargetAudiences {
		addClause("COALESCE(target_audiences, '') IN ('', '[]') AND audience = ? AND target_site_id = ?", "site", site.Id)
	} else {
		addClause("audience = ? AND target_site_id = ?", "site", site.Id)
	}
	if hasTargetAudiences && isSiteOwner && current.OwnerUserId == userID {
		addClause("target_audiences LIKE ?", `%"all_site_owners"%`)
	}

	if user.SiteId == site.Id && !isStaff {
		addClause("audience = ? AND target_site_id = ? AND target_site_name = ?", "user", site.Id, userTarget)
		teamTargets, err := adtyUserActiveTeamTargetsWithDB(db, userID, site.Id)
		if err != nil {
			return nil, viewer, err
		}
		if len(teamTargets) > 0 {
			addClause("audience = ? AND target_site_id = ? AND target_site_name IN ?", "team", site.Id, teamTargets)
		}
		if hasTargetAudiences {
			siteMatchSQL, siteMatchArgs := model.AdtyNotificationTargetSiteMatchSQL(site.Id, hasTargetSiteIDs)
			siteArgs := make([]any, 0, 1+len(siteMatchArgs))
			siteArgs = append(siteArgs, `%"site_users"%`)
			siteArgs = append(siteArgs, siteMatchArgs...)
			addClause("target_audiences LIKE ? AND ("+siteMatchSQL+")", siteArgs...)

			inSiteTeam := len(teamTargets) > 0
			if !inSiteTeam {
				inSiteTeam, err = adtyUserInActiveTeamOnSiteWithDB(db, userID, site.Id)
				if err != nil {
					return nil, viewer, err
				}
			}
			if inSiteTeam {
				teamSiteSQL, teamSiteArgs := model.AdtyNotificationTargetSiteMatchSQL(site.Id, hasTargetSiteIDs)
				teamArgs := make([]any, 0, 1+len(teamSiteArgs))
				teamArgs = append(teamArgs, `%"site_teams"%`)
				teamArgs = append(teamArgs, teamSiteArgs...)
				addClause("target_audiences LIKE ? AND ("+teamSiteSQL+")", teamArgs...)
			}
		}
	} else if !isStaff && user.SiteId == 0 && user.StaffSiteId == 0 && current.OwnerUserId == userID && slices.Contains(ownedOpenSiteIDs, site.Id) {
		if hasTargetAudiences {
			addClause("COALESCE(target_audiences, '') IN ('', '[]') AND audience = ? AND issuer_site_id = 0 AND target_site_id = ? AND target_site_name = ?", "user", site.Id, userTarget)
		} else {
			addClause("audience = ? AND issuer_site_id = 0 AND target_site_id = ? AND target_site_name = ?", "user", site.Id, userTarget)
		}
	}

	return query.Where(strings.Join(clauses, " OR "), args...), viewer, nil
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
	return formatAdtyNotificationItemForViewer(row, nil, nil)
}

func formatAdtyNotificationItemForViewer(row model.AdtyNotificationPublication, viewerSite *model.AdtySite, preloaded *adtyNotificationPreload) map[string]any {
	var bodyHead *model.AdtyTranslationHead
	var titleHead *model.AdtyTranslationHead
	if preloaded != nil {
		if row.Id > 0 && preloaded.HasTranslationTable {
			if head, ok := preloaded.TranslationHeads[adtyNotificationTranslationKey(row.Id)]; ok {
				h := head
				bodyHead = &h
			}
			if head, ok := preloaded.TranslationHeads[adtyNotificationTitleTranslationKey(row.Id)]; ok {
				h := head
				titleHead = &h
			}
		}
	} else if row.Id > 0 && model.DB != nil && model.DB.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		var heads []model.AdtyTranslationHead
		keys := []string{adtyNotificationTranslationKey(row.Id), adtyNotificationTitleTranslationKey(row.Id)}
		if err := model.DB.Where("object_key IN ?", keys).Find(&heads).Error; err == nil {
			for i := range heads {
				switch heads[i].ObjectKey {
				case keys[0]:
					h := heads[i]
					bodyHead = &h
				case keys[1]:
					h := heads[i]
					titleHead = &h
				}
			}
		}
	}
	content := resolveAdtyNotificationDisplayContentWithHead(row, bodyHead)
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
	publisherLabel := strings.TrimSpace(common.GetSystemName())
	if row.IssuerSiteId > 0 {
		publisherLabel = ""
		if preloaded != nil {
			if issuerSite, ok := preloaded.IssuerSites[row.IssuerSiteId]; ok {
				publisherLabel = strings.TrimSpace(AdtyBrandFor(&issuerSite).Name)
			}
		} else if model.DB != nil && model.DB.Migrator().HasTable(&model.AdtySite{}) {
			var issuerSite model.AdtySite
			if model.DB.Select("id", "subdomain", "root_domain", "brand").Where("id = ?", row.IssuerSiteId).Limit(1).Find(&issuerSite).RowsAffected > 0 {
				publisherLabel = strings.TrimSpace(AdtyBrandFor(&issuerSite).Name)
			}
		}
	}
	contentFormat := strings.ToLower(strings.TrimSpace(row.ContentFormat))
	if contentFormat == "" {
		contentFormat = "markdown"
	}
	rawTargetAudiences := row.ParsedTargetAudiences()
	targetAudiences := []string{}
	targetSiteIDs := []int{}
	targetSiteID := row.TargetSiteId
	targetSiteName := row.TargetSiteName

	var viewer *adtyNotificationViewerContext
	if preloaded != nil {
		viewer = preloaded.Viewer
	}
	isLegacyDirect := len(rawTargetAudiences) == 0 && (row.Audience == "team" || row.Audience == "user")
	if viewerSite != nil {
		resolvedSite := viewerSite
		if viewer != nil && viewer.ViewerSite != nil && viewer.ViewerSite.Id == viewerSite.Id {
			resolvedSite = viewer.ViewerSite
		}
		ownsViewerSite := viewer != nil && viewer.OwnsViewerSite
		if isLegacyDirect {
			if row.TargetSiteId != viewerSite.Id {
				targetSiteID = 0
				targetSiteName = ""
			}
		} else {
			targeted := viewerSite.Id > 0 && slices.Contains(row.EffectiveTargetSiteIDs(), viewerSite.Id)
			if targeted {
				targetSiteIDs = []int{viewerSite.Id}
				targetSiteID = viewerSite.Id
				targetSiteName = AdtySiteDisplayHost(resolvedSite)
			} else {
				targetSiteIDs = []int{}
				targetSiteID = 0
				targetSiteName = ""
			}
			for _, aud := range rawTargetAudiences {
				switch aud {
				case "site_users", "site_teams":
					if targeted {
						targetAudiences = append(targetAudiences, aud)
					}
				case "all_site_owners":
					if ownsViewerSite {
						targetAudiences = append(targetAudiences, aud)
					}
				}
			}
		}
	} else if !isLegacyDirect {
		// On main site, omit subsite target IDs/hostnames so main-site users and
		// unrelated site owners never receive other subsites' hostnames or IDs.
		targetSiteIDs = []int{}
		targetSiteID = 0
		targetSiteName = ""
		for _, aud := range rawTargetAudiences {
			switch aud {
			case "main_users":
				if viewer == nil || (viewer.UserSiteID == 0 && !viewer.IsStaff && !viewer.IsSiteOwner) {
					targetAudiences = append(targetAudiences, aud)
				}
			case "main_teams":
				if viewer == nil || (viewer.UserSiteID == 0 && !viewer.IsStaff && viewer.InMainTeam) {
					targetAudiences = append(targetAudiences, aud)
				}
			case "all_site_owners":
				if viewer == nil || viewer.IsSiteOwner {
					targetAudiences = append(targetAudiences, aud)
				}
			}
		}
	}

	item := map[string]any{
		"id":               fmt.Sprintf("publication:%d", row.Id),
		"read_key":         row.ReadKey,
		"title":            row.Title,
		"content":          content,
		"content_format":   contentFormat,
		"type":             "default",
		"publishDate":      time.Unix(row.PublishedAt, 0).UTC().Format(time.RFC3339),
		"publisher_name":   publisherLabel,
		"issuer_site_id":   row.IssuerSiteId,
		"audience":         row.Audience,
		"target_audiences": targetAudiences,
		"target_site_ids":  targetSiteIDs,
		"target_site_id":   targetSiteID,
		"target_site_name": targetSiteName,
		"target_team_id":   targetTeamID,
		"target_user_id":   targetUserID,
		"kind":             row.Kind,
		"business":         row.Business,
	}
	cleanBodySrc := stripAdtyNotificationTranslationSuffix(row.Content, "")
	bodySrc := cleanBodySrc
	bodyReady := false
	if bodyHead != nil {
		candidateSrc := stripAdtyNotificationTranslationSuffix(row.Content, bodyHead.English)
		if adtyTranslationHeadMatchesSource(*bodyHead, candidateSrc) {
			bodySrc = candidateSrc
			eng := strings.TrimSpace(bodyHead.English)
			if contentFormat == "html" {
				eng = SanitizeAdtyNotificationHTML(eng)
			}
			if isAdtyTranslationNotReadyText(eng) {
				eng = ""
			}
			status := strings.TrimSpace(bodyHead.Status)
			notice := strings.TrimSpace(bodyHead.Notice)
			if status == model.AdtyTranslationReady && eng != "" {
				bodyReady = true
			} else {
				if status == "" || status == model.AdtyTranslationReady {
					status = model.AdtyTranslationPending
				}
				if notice == "" {
					notice = AdtyTranslationNotReady
				}
			}
			item["english"] = eng
			item["content_en"] = eng
			item["source"] = bodyHead.Source
			item["translation_status"] = status
			item["translation_notice"] = notice
			item["translation_version"] = bodyHead.Version
			item["object_key"] = bodyHead.ObjectKey
		}
	}
	if adtyContainsChinese(bodySrc) && !bodyReady {
		if curStatus, ok := item["translation_status"].(string); !ok || strings.TrimSpace(curStatus) == "" {
			fallbackStatus := model.AdtyTranslationPending
			if bodyHead != nil && strings.TrimSpace(bodyHead.SourceHash) == "" && strings.TrimSpace(bodyHead.Source) == "" {
				if s := strings.TrimSpace(bodyHead.Status); s != "" && s != model.AdtyTranslationReady {
					fallbackStatus = s
				}
			}
			item["translation_status"] = fallbackStatus
		}
		if curNotice, ok := item["translation_notice"].(string); !ok || strings.TrimSpace(curNotice) == "" {
			item["translation_notice"] = AdtyTranslationNotReady
		}
	}
	title := strings.TrimSpace(row.Title)
	titleMatched := titleHead != nil && adtyTranslationHeadMatchesSource(*titleHead, title)
	titleReady := false
	if titleMatched && titleHead.Status == model.AdtyTranslationReady && strings.TrimSpace(titleHead.English) != "" {
		engTitle := strings.TrimSpace(titleHead.English)
		if !isAdtyTranslationNotReadyText(engTitle) {
			item["title_en"] = engTitle
			titleReady = true
		}
	}
	if adtyContainsChinese(title) && !titleReady {
		titleStatus := model.AdtyTranslationPending
		if titleHead != nil && (titleMatched || (strings.TrimSpace(titleHead.SourceHash) == "" && strings.TrimSpace(titleHead.Source) == "")) {
			if s := strings.TrimSpace(titleHead.Status); s != "" && s != model.AdtyTranslationReady {
				titleStatus = s
			}
		}
		item["title_translation_status"] = titleStatus
		curStatus, _ := item["translation_status"].(string)
		curStatus = strings.TrimSpace(curStatus)
		if curStatus == "" || curStatus == model.AdtyTranslationReady || (curStatus == model.AdtyTranslationPending && titleStatus == model.AdtyTranslationFailed) {
			item["translation_status"] = titleStatus
			item["translation_notice"] = AdtyTranslationNotReady
		}
	}
	return item
}

// GetAdtyNotificationDetail resolves a single announcement or notice across the
// caller's full visible history (not just the first inbox page of 20 items),
// enforcing site/audience visibility and user hide state.
func GetAdtyNotificationDetail(userID int, site *model.AdtySite, key string, tokenScopes ...[]string) (map[string]any, bool, error) {
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
	query, viewer, err := adtyVisibleNotificationPublicationsWithViewer(model.DB, userID, site)
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
		if adtyTicketInboxTablesReady() {
			ticketQuery, scope, err := adtyTicketInboxQuery(userID, site, tokenScopes...)
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
		}
		if item, ok := adtyWatcherNoticeDetail(userID, site, key, tokenScopes...); ok {
			return item, true, nil
		}
		return nil, false, nil
	}
	if adtyIsNotificationKeyHidden(userID, row.ReadKey) || adtyIsNotificationKeyHidden(userID, fmt.Sprintf("publication:%d", row.Id)) {
		return nil, false, nil
	}
	preloaded := preloadAdtyNotificationBatch(model.DB, []model.AdtyNotificationPublication{row}, &viewer)
	if adtyPublicationIsSevereWithRefs(&row, preloaded.OpenAnomalyRefs) && userID > 0 && strings.TrimSpace(row.ReadKey) != "" {
		if _, err := UpsertAdtySevereTodo(userID, viewer.UserSiteID, row.ReadKey, row.Title, row.Content, row.Business, row.Id); err != nil {
			common.SysLog(common.LogText("severe todo was not created for visible notice %s: %s", row.ReadKey, err.Error()))
		}
	}
	item := formatAdtyNotificationItemForViewer(row, site, preloaded)
	renderAdtyTodoInboxItemsWithPreload(userID, []map[string]any{item}, map[int]model.AdtyNotificationPublication{row.Id: row}, preloaded.OpenAnomalyRefs)
	return item, true, nil
}

// resolveAdtyNotificationCanonicalKey resolves any supported notification key or alias
// ("notice", "publication:<id>", "<id>", "watcher:<read_key>", "watcher:<event_id>", read_key)
// to its canonical read_key while verifying audience/permission visibility, WITHOUT rejecting
// keys that are already hidden so HideAdtyNotificationForUserFromRequest stays idempotent (R5-060).
func resolveAdtyNotificationCanonicalKey(userID int, site *model.AdtySite, key string, tokenScopes ...[]string) (string, bool, error) {
	key = strings.TrimSpace(key)
	if userID <= 0 || key == "" {
		return "", false, nil
	}
	inbox, _, err := adtyCurrentNotificationInbox(site)
	if err != nil {
		return "", false, err
	}
	if inbox.Notice != "" && (key == inbox.NoticeKey || key == "notice") {
		return inbox.NoticeKey, true, nil
	}
	query, err := adtyVisibleNotificationPublications(userID, site)
	if err != nil {
		return "", false, err
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
		return "", false, err
	}
	if row.Id > 0 {
		return row.ReadKey, true, nil
	}
	if adtyTicketInboxTablesReady() {
		ticketQuery, _, err := adtyTicketInboxQueryWithMethod(userID, site, http.MethodPost, tokenScopes...)
		if err != nil {
			return "", false, err
		}
		var notice model.AdtyTicketNotice
		if err := ticketQuery.Select("id", "read_key").Where("read_key = ?", key).Limit(1).Find(&notice).Error; err != nil {
			return "", false, err
		}
		if notice.Id > 0 {
			return notice.ReadKey, true, nil
		}
	}
	if notice, ok := adtyWatcherNoticeLookup(userID, site, key, tokenScopes...); ok {
		return notice.ReadKey, true, nil
	}
	return "", false, nil
}

func adtyTicketInboxTablesReady() bool {
	return model.DB != nil &&
		model.DB.Migrator().HasTable(&model.AdtyTicketNotice{}) &&
		model.DB.Migrator().HasTable(&model.AdtyTicket{})
}

func adtyIsNotificationKeyHidden(userID int, key string) bool {
	if userID <= 0 || key == "" || model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyNotificationHide{}) {
		return false
	}
	var count int64
	if err := model.DB.Model(&model.AdtyNotificationHide{}).Where("user_id = ? AND "+adtyQuoteColumn(model.DB, "key")+" = ?", userID, key).Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

// CountAdtyNotificationUnread returns the total unread notification count
// (publications + ticket notices + watcher notices) without fetching or
// formatting inbox rows, resolving translations, or upserting todos.
func CountAdtyNotificationUnread(userID int, site *model.AdtySite, tokenScopes ...string) (int, error) {
	var scoped [][]string
	if len(tokenScopes) > 0 {
		scoped = [][]string{tokenScopes}
	}
	n, err := countAdtyNotificationUnreadWithScopes(userID, site, scoped...)
	return int(n), err
}

func countAdtyNotificationUnreadWithScopes(userID int, site *model.AdtySite, tokenScopes ...[]string) (int64, error) {
	if model.DB == nil {
		return 0, nil
	}
	query, err := adtyVisibleNotificationPublications(userID, site)
	if err != nil {
		return 0, err
	}
	siteID := 0
	if site != nil {
		siteID = site.Id
	}
	hasHideTable := model.DB.Migrator().HasTable(&model.AdtyNotificationHide{})
	if hasHideTable {
		query = query.Where(adtyPublicationNotHiddenSQL(model.DB), userID)
	}
	var unread int64
	if err := query.Where(
		"NOT EXISTS (SELECT 1 FROM adty_notification_reads r WHERE r.user_id = ? AND r.site_id = ? AND r.read_key = adty_notification_publications.read_key)",
		userID, siteID,
	).Count(&unread).Error; err != nil {
		return 0, err
	}
	if adtyTicketInboxTablesReady() {
		ticketQuery, _, err := adtyTicketInboxQuery(userID, site, tokenScopes...)
		if err != nil {
			return 0, err
		}
		if hasHideTable {
			ticketQuery = ticketQuery.Where(adtyTicketNoticeNotHiddenSQL(model.DB), userID)
		}
		var unreadTickets int64
		if err := ticketQuery.Where(
			"NOT EXISTS (SELECT 1 FROM adty_notification_reads r WHERE r.user_id = ? AND r.read_key = adty_ticket_notices.read_key)",
			userID,
		).Count(&unreadTickets).Error; err != nil {
			return 0, err
		}
		unread += unreadTickets
	}
	unread += adtyUnreadWatcherNoticeCount(userID, site, tokenScopes...)
	return unread, nil
}

func GetAdtyNotificationInbox(userID int, site *model.AdtySite, before int, ticketCursors ...int64) (AdtyNotificationInbox, error) {
	var ticketBefore int64
	if len(ticketCursors) > 0 {
		ticketBefore = ticketCursors[0]
	} else if before != 0 {
		ticketBefore = -1
	}
	return GetAdtyNotificationInboxWithScopes(userID, site, before, ticketBefore)
}

func GetAdtyNotificationInboxWithScopes(userID int, site *model.AdtySite, before int, ticketBefore int64, tokenScopes ...[]string) (AdtyNotificationInbox, error) {
	inbox, keys, err := adtyCurrentNotificationInbox(site)
	if err != nil {
		return inbox, err
	}
	query, viewer, err := adtyVisibleNotificationPublicationsWithViewer(model.DB, userID, site)
	if err != nil {
		return inbox, err
	}
	siteID := 0
	if site != nil {
		siteID = site.Id
	}
	hasHideTable := model.DB.Migrator().HasTable(&model.AdtyNotificationHide{})
	if hasHideTable {
		// R2-052: Apply hide exclusion BEFORE Limit(21) so hidden items never cause short/empty pages.
		query = query.Where(adtyPublicationNotHiddenSQL(model.DB), userID)
	}
	unreadPubQuery := query.Session(&gorm.Session{}).Where(
		"NOT EXISTS (SELECT 1 FROM adty_notification_reads r WHERE r.user_id = ? AND r.site_id = ? AND r.read_key = adty_notification_publications.read_key)",
		userID, siteID,
	)
	var unread int64
	if err := unreadPubQuery.Count(&unread).Error; err != nil {
		return inbox, err
	}
	inbox.UnreadPublications = unread
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
	preloaded := preloadAdtyNotificationBatch(model.DB, rows, &viewer)
	preloadedPubs := make(map[int]model.AdtyNotificationPublication, len(rows))
	todoSiteID := viewer.UserSiteID
	for i := range rows {
		row := rows[i]
		preloadedPubs[row.Id] = row
		if adtyPublicationIsSevereWithRefs(&row, preloaded.OpenAnomalyRefs) && userID > 0 && strings.TrimSpace(row.ReadKey) != "" {
			if _, err := UpsertAdtySevereTodo(userID, todoSiteID, row.ReadKey, row.Title, row.Content, row.Business, row.Id); err != nil {
				common.SysLog(common.LogText("severe todo was not created for visible notice %s: %s", row.ReadKey, err.Error()))
			}
		}
		key := row.ReadKey
		inbox.Announcements = append(inbox.Announcements, formatAdtyNotificationItemForViewer(row, site, preloaded))
		keys = append(keys, key)
	}
	var visibleTicketKeys []string
	ticketReceipts := model.DB.Model(&model.AdtyNotificationRead{}).Where("user_id=?", userID)
	if adtyTicketInboxTablesReady() {
		ticketQuery, handlerScope, err := adtyTicketInboxQuery(userID, site, tokenScopes...)
		if err != nil {
			return inbox, err
		}
		var unreadTickets int64
		if hasHideTable {
			ticketQuery = ticketQuery.Where(adtyTicketNoticeNotHiddenSQL(model.DB), userID)
		}
		if err := ticketQuery.Session(&gorm.Session{}).Where(
			"NOT EXISTS (SELECT 1 FROM adty_notification_reads r WHERE r.user_id = ? AND r.read_key = adty_ticket_notices.read_key)",
			userID,
		).Count(&unreadTickets).Error; err != nil {
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
	}

	if firstPage {
		for _, item := range adtyWatcherNoticeInboxItems(userID, site, tokenScopes...) {
			inbox.Announcements = append(inbox.Announcements, item)
			if key, _ := item["read_key"].(string); key != "" {
				keys = append(keys, key)
			}
		}
	}
	inbox.UnreadPublications += adtyUnreadWatcherNoticeCount(userID, site, tokenScopes...)
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
	inbox.Announcements = renderAdtyTodoInboxItemsWithPreload(userID, inbox.Announcements, preloadedPubs, preloaded.OpenAnomalyRefs)
	return hideAdtyNotifications(userID, inbox)
}

func hideAdtyNotifications(userID int, inbox AdtyNotificationInbox) (AdtyNotificationInbox, error) {
	if userID <= 0 || model.DB == nil || (len(inbox.Announcements) == 0 && strings.TrimSpace(inbox.NoticeKey) == "") {
		return inbox, nil
	}
	keys := make([]string, 0, len(inbox.Announcements)*2+1)
	if noticeKey := strings.TrimSpace(inbox.NoticeKey); noticeKey != "" {
		keys = append(keys, noticeKey)
	}
	for _, item := range inbox.Announcements {
		if key, ok := item["read_key"].(string); ok && key != "" {
			keys = append(keys, key)
		}
		if idKey, ok := item["id"].(string); ok && idKey != "" {
			keys = append(keys, idKey)
		}
	}
	if len(keys) == 0 {
		return inbox, nil
	}
	var hidden []string
	if err := model.DB.Model(&model.AdtyNotificationHide{}).Where("user_id = ? AND "+adtyQuoteColumn(model.DB, "key")+" IN ?", userID, keys).Pluck("key", &hidden).Error; err != nil {
		return inbox, err
	}
	if len(hidden) == 0 {
		return inbox, nil
	}
	skip := map[string]bool{}
	for _, key := range hidden {
		skip[key] = true
	}
	if noticeKey := strings.TrimSpace(inbox.NoticeKey); noticeKey != "" && skip[noticeKey] {
		inbox.Notice = ""
		inbox.NoticeKey = ""
	}
	visible := inbox.Announcements[:0]
	for _, item := range inbox.Announcements {
		key, _ := item["read_key"].(string)
		idKey, _ := item["id"].(string)
		if !skip[key] && !skip[idKey] {
			visible = append(visible, item)
		}
	}
	inbox.Announcements = visible
	return inbox, nil
}

func MarkAdtyNotificationInboxRead(userID int, site *model.AdtySite, keys []string, tokenScopes ...[]string) error {
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
	if adtyTicketInboxTablesReady() {
		ticketQuery, _, err := adtyTicketInboxQueryWithMethod(userID, site, http.MethodPost, tokenScopes...)
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
	}
	for key := range adtyOwnedWatcherReadKeys(userID, site, keys, tokenScopes...) {
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
