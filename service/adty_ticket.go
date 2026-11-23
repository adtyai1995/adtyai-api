package service

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"math"
	"slices"
	// WebP 只用于解码：注册 "webp" 格式并让 image.Decode 能识别。
	// 这里显式引入，避免依赖同包其它文件的间接 import。
	_ "golang.org/x/image/webp"
	"gorm.io/gorm"
)

var (
	ErrAdtyTicketInvalid   = common.NewMessage("Invalid parameters")
	ErrAdtyTicketForbidden = common.NewMessage("No permission to perform this action")
	ErrAdtyTicketImage     = common.NewMessage("Only images are accepted")
	ErrAdtyTicketImageSize = common.NewMessage("Image exceeds the size limit")
	adtyTicketImageDir     = func() string { return filepath.Join("uploads", "adty-ticket-images") }
)

type AdtyTicketInput struct {
	Audience     string `json:"audience"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Priority     string `json:"priority"`
	RelatedKind  string `json:"related_kind"`
	RelatedRef   string `json:"related_ref"`
	RelatedModel string `json:"related_model"`
	RelatedGroup string `json:"related_group"`
	RelatedError string `json:"related_error"`
	Idempotency  string `json:"-"`
}

type AdtyTicketAssignee struct {
	Id          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type AdtyTicketDetail struct {
	Actions   []string                  `json:"actions"`
	Assignees []AdtyTicketAssignee      `json:"assignees,omitempty"`
	Ticket    model.AdtyTicket          `json:"ticket"`
	Messages  []model.AdtyTicketMessage `json:"messages"`
	Images    []model.AdtyTicketImage   `json:"images"`
	Events     []model.AdtyTicketEvent   `json:"events"`
	CanNote    bool                      `json:"can_note"`
	CanManage  bool                      `json:"can_manage"`
	CanControl bool                      `json:"can_control"`
}

type AdtyTicketBadges struct {
	New               int64 `json:"new"`
	PendingAssignment int64 `json:"pending_assignment"`
	InProgress        int64 `json:"in_progress"`
	WaitingUser       int64 `json:"waiting_user"`
	WaitingInternal   int64 `json:"waiting_internal"`
	Urgent            int64 `json:"urgent"`
	Resolved          int64 `json:"resolved"`
	Closed            int64 `json:"closed"`
	Pending           int64 `json:"pending"`
}

type AdtyTicketList struct {
	Total       int64                  `json:"total"`
	Page        int                    `json:"page"`
	PageSize    int                    `json:"page_size"`
	ImagePolicy model.AdtyTicketPolicy `json:"image_policy"`
	CanViewTeam bool                   `json:"can_view_team"`
	Tickets     []model.AdtyTicket     `json:"tickets"`
	CanNote     bool                   `json:"can_note"`
	CanManage   bool                   `json:"can_manage"`
	Badges      AdtyTicketBadges       `json:"badges"`
}

type adtyTicketActor struct {
	Scope        string
	PlatformMain bool
	PlatformAll  bool
	StaffTickets bool
	User         model.User
	Staff        *model.AdtySiteStaff
	OwnerSiteId  int
	OwnerTickets bool
	TeamId       int64
	TeamRole     string
}

func SetAdtyTicketImageDirForTest(dir string) func() {
	old := adtyTicketImageDir
	adtyTicketImageDir = func() string { return dir }
	return func() { adtyTicketImageDir = old }
}

func adtyTicketImageFilePath(storage string) (string, error) {
	storage = strings.TrimSpace(storage)
	if storage == "" {
		return "", nil
	}
	if strings.Contains(storage, "..") || strings.ContainsAny(storage, `/\`) {
		return "", ErrAdtyTicketInvalid
	}
	return filepath.Join(adtyTicketImageDir(), storage), nil
}

func adtyTicketImageStoragePath(imageRow model.AdtyTicketImage) (string, error) {
	storage := strings.TrimSpace(imageRow.Storage)
	if storage == "" && strings.TrimSpace(imageRow.ObjectId) != "" {
		storage = strings.TrimSpace(imageRow.ObjectId) + adtyTicketExt(imageRow.Mime)
	}
	if storage == "" {
		return "", os.ErrNotExist
	}
	path, err := adtyTicketImageFilePath(storage)
	if err != nil || path == "" {
		return "", os.ErrNotExist
	}
	return path, nil
}

func newAdtyTicketImageObjectID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func CreateAdtyTicket(userId int, input AdtyTicketInput) (*model.AdtyTicket, error) {
	input.Title = strings.TrimSpace(redactAdtySecrets(input.Title))
	input.Body = strings.TrimSpace(redactAdtySecrets(input.Body))
	// 关联字段同样是用户手输的自由文本：related_error 最常见的就是一段带请求头或
	// token 的报错原文，related_ref / model / group 也可能粘进 key。四个字段一律
	// 先脱敏再落库，和 Title/Body 走同一条路径，而不是拿原始文本拒单——拒单会让
	// 用户知道「哪句话里有敏感串」，却拿不到单。
	input.RelatedRef = strings.TrimSpace(redactAdtySecrets(input.RelatedRef))
	input.RelatedModel = strings.TrimSpace(redactAdtySecrets(input.RelatedModel))
	input.RelatedGroup = strings.TrimSpace(redactAdtySecrets(input.RelatedGroup))
	input.RelatedError = strings.TrimSpace(redactAdtySecrets(input.RelatedError))
	if userId <= 0 || input.Title == "" || input.Body == "" || utf8.RuneCountInString(input.Title) > 120 {
		return nil, ErrAdtyTicketInvalid
	}
	if input.Type == "" {
		input.Type = "other"
	}
	switch input.Type {
	case "billing", "api", "account", "other":
	default:
		return nil, ErrAdtyTicketInvalid
	}
	priority := model.AdtyTicketPriorityNormal
	switch input.Priority {
	case "", model.AdtyTicketPriorityNormal, model.AdtyTicketPriorityHigh, model.AdtyTicketPriorityUrgent:
		if input.Priority != "" {
			priority = input.Priority
		}
	default:
		return nil, ErrAdtyTicketInvalid
	}
	if input.Audience != "" && input.Audience != "personal" && input.Audience != "team" {
		return nil, ErrAdtyTicketInvalid
	}
	if input.Type == "account" && input.Audience == "team" {
		return nil, ErrAdtyTicketInvalid
	}
	if strings.HasPrefix(input.RelatedKind, "team_") && input.Audience != "team" {
		return nil, ErrAdtyTicketInvalid
	}
	if err := ensureAdtyTicketNoticeSchema(); err != nil {
		return nil, err
	}
	var created *model.AdtyTicket
	createdNew := false
	var createAudit *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := model.AdtyLockForUpdate(tx).Select("id").First(&user, userId).Error; err != nil {
			return err
		}
		actor, err := loadAdtyTicketWorkspace(tx, userId, "personal")
		if err != nil {
			return err
		}
		var teamID int64
		if input.Audience == "team" {
			if actor.TeamId <= 0 {
				return ErrAdtyTicketForbidden
			}
			teamID = actor.TeamId
		}
		dedupe := ticketCreateDedupe(-1, actor.User.Id, input.Idempotency)
		if strings.TrimSpace(input.Idempotency) != "" {
			var prior model.AdtyTicketMessage
			// Include site-scoped keys used before cross-site replay was supported.
			var priorSites []int
			if err := tx.Model(&model.AdtyTicket{}).Where("user_id=?", actor.User.Id).Distinct().Pluck("site_id", &priorSites).Error; err != nil {
				return err
			}
			keys := []string{dedupe}
			for _, siteID := range priorSites {
				keys = append(keys, ticketCreateDedupe(siteID, actor.User.Id, input.Idempotency))
			}
			lookup := tx.Where("dedupe IN ?", keys).Order("id").Limit(1).Find(&prior)
			if lookup.Error != nil {
				return lookup.Error
			}
			if lookup.RowsAffected > 0 {
				var ticket model.AdtyTicket
				if err := tx.First(&ticket, prior.TicketId).Error; err != nil {
					return err
				}
				if prior.AuthorId != actor.User.Id || ticket.UserId != actor.User.Id || !actor.canReadOwnTicket(ticket) {
					return ErrAdtyTicketForbidden
				}
				ticket.Status = model.NormalizeAdtyTicketStatus(ticket.Status, ticket.AssigneeId)
				ticket.InitialMessageId = prior.Id
				created = &ticket
				return nil
			}
		}

		ticket := model.AdtyTicket{
			SiteId: actor.User.SiteId, UserId: actor.User.Id, TeamId: teamID, Type: input.Type,
			TeamShared: teamID > 0, Title: input.Title, Priority: priority, Status: model.AdtyTicketNew,
			RelatedKind: input.RelatedKind, RelatedRef: input.RelatedRef, RelatedModel: input.RelatedModel,
			RelatedGroup: input.RelatedGroup, RelatedError: input.RelatedError,
		}
		if input.RelatedKind == "request" {
			payer, err := ticketRequestSnapshot(tx, actor, input.RelatedRef, &ticket)
			if err != nil {
				return err
			}
			if payer != teamID {
				return ErrAdtyTicketInvalid
			}
		} else if err := validateTicketReference(tx, actor, teamID, input.RelatedKind, input.RelatedRef); err != nil {
			return err
		}
		if ticket.TeamShared {
			ticket.ContactId = userId
		}
		if err := tx.Create(&ticket).Error; err != nil {
			return err
		}
		message := model.AdtyTicketMessage{
			TicketId: ticket.Id, AuthorId: actor.User.Id, Visibility: model.AdtyTicketVisibilityPublic,
			Body: input.Body, Dedupe: dedupe, AuthorRole: "customer",
		}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		createEvent := model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: actor.User.Id, Action: "create", Detail: input.RelatedKind}
		if err := tx.Create(&createEvent).Error; err != nil {
			return err
		}
		if err := emitAdtyTicketCreateNoticesTx(tx, ticket, createEvent.Id, actor.User.Id); err != nil {
			return err
		}
		ticket.InitialMessageId = message.Id
		created = &ticket
		createdNew = true
		var auditErr error
		createAudit, auditErr = appendAdtyOfficialAuditTx(tx, userId, ticket.SiteId, "ticket", strconv.FormatInt(ticket.Id, 10), "adty.ticket.create", "", "",
			nil,
			map[string]any{
				"ticket_id": ticket.Id, "site_id": ticket.SiteId, "user_id": ticket.UserId,
				"team_id": ticket.TeamId, "type": ticket.Type, "title": ticket.Title,
				"priority": ticket.Priority, "status": ticket.Status, "related_kind": ticket.RelatedKind,
			},
			model.AuditResultSuccess)
		return auditErr
	})
	if err == nil && createdNew && created != nil {
		if createAudit != nil {
			deliverAdtyCommittedAudit(*createAudit)
		} else {
			writeAdtyOfficialAudit(userId, created.SiteId, "ticket", strconv.FormatInt(created.Id, 10), "adty.ticket.create", "", "",
				nil,
				map[string]any{
					"ticket_id": created.Id, "site_id": created.SiteId, "user_id": created.UserId,
					"team_id": created.TeamId, "type": created.Type, "title": created.Title,
					"priority": created.Priority, "status": created.Status, "related_kind": created.RelatedKind,
				},
				model.AuditResultSuccess)
		}
	}
	return created, err
}

func ListAdtyTickets(userId int, queue string) (*AdtyTicketList, error) {
	return ListAdtyTicketPage(userId, queue, 1, 100)
}

func ListAdtyTicketPage(userId int, queue string, page, pageSize int, workspace ...string) (*AdtyTicketList, error) {
	return SearchAdtyTicketPage(userId, queue, "", 0, page, pageSize, workspace...)
}

func SearchAdtyTicketPage(userId int, queue, search string, before int64, page, pageSize int, workspace ...string) (*AdtyTicketList, error) {
	actor, err := loadAdtyTicketWorkspace(model.DB, userId, workspace...)
	if err != nil {
		return nil, err
	}
	if page < 1 || pageSize < 1 || pageSize > 100 || page > math.MaxInt/pageSize {
		return nil, ErrAdtyTicketInvalid
	}
	scope := scopedAdtyTickets(model.DB, actor)
	query, err := applyAdtyTicketQueueFilter(scope.Session(&gorm.Session{}), queue)
	if err != nil {
		return nil, err
	}
	search = strings.TrimSpace(search)
	if len(search) > 128 || before < 0 {
		return nil, ErrAdtyTicketInvalid
	}
	if search != "" {
		ticketID, _ := strconv.ParseInt(strings.TrimPrefix(search, "#"), 10, 64)
		query = query.Where("id = ? OR related_ref = ?", ticketID, search)
	}
	if before > 0 {
		query = query.Where("id < ?", before)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []model.AdtyTicket
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Status = model.NormalizeAdtyTicketStatus(rows[i].Status, rows[i].AssigneeId)
	}
	badges, err := countAdtyTicketBadges(scope)
	if err != nil {
		return nil, err
	}
	manages := actor.isHandling()
	return &AdtyTicketList{Tickets: rows, Total: total, Page: page, PageSize: pageSize, ImagePolicy: adtyTicketPolicy(), CanViewTeam: actor.TeamId > 0, CanNote: manages, CanManage: manages, Badges: badges}, nil
}

// Every operation resolves the requested workspace before applying this same
// row filter. A handler visiting the account page sees only their public case.
func scopedAdtyTickets(tx *gorm.DB, actor adtyTicketActor) *gorm.DB {
	query := tx.Model(&model.AdtyTicket{})
	switch actor.Scope {
	case "platform_main":
		return query.Where("COALESCE(handling_site_id,site_id) = ?", 0)
	case "platform":
		if actor.PlatformAll {
			return query
		}
		return query.Where("COALESCE(handling_site_id,site_id) = ?", 0)
	case "owner":
		return query.Where("COALESCE(handling_site_id,site_id) = ?", actor.OwnerSiteId)
	case "staff":
		return query.Where("COALESCE(handling_site_id,site_id) = ?", actor.Staff.SiteId)
	case "participant":
		return query.Where("(user_id = ? AND COALESCE(team_shared,false)=false) OR team_id = ?", actor.User.Id, actor.TeamId)
	case "team":
		query = query.Where("team_id = ?", actor.TeamId)
		if actor.TeamRole != model.AdtyTeamRoleLeader {
			query = query.Where("team_shared = ? OR user_id = ?", true, actor.User.Id)
		}
		return query
	default:
		return query.Where("user_id = ? AND (COALESCE(team_shared,false)=false OR (team_id > 0 AND team_id = ?))", actor.User.Id, actor.TeamId)
	}
}

// A duplicate hint is restricted to the customer's authorized workspace and
// exact business reference. Customers can still open a separate issue.
func RelatedOpenAdtyTickets(userID int, scope, kind, ref string) ([]model.AdtyTicket, error) {
	if (scope != "personal" && scope != "team") || ref == "" || len(ref) > 64 || len(kind) > 32 {
		return nil, ErrAdtyTicketInvalid
	}
	actor, err := loadAdtyTicketWorkspace(model.DB, userID, scope)
	if err != nil {
		return nil, err
	}
	var tickets []model.AdtyTicket
	kinds := []string{kind}
	switch kind {
	case "topup", "refund":
		kinds = []string{"topup", "refund"}
	case "team_order", "team_refund":
		kinds = []string{"team_order", "team_refund"}
	}
	q := scopedAdtyTickets(model.DB, actor).Where("related_kind IN ? AND related_ref=? AND status NOT IN ?", kinds, ref, []string{model.AdtyTicketClosed, model.AdtyTicketResolved})
	err = q.Select("id", "title", "status", "team_id", "team_shared").Order("id DESC").Limit(20).Find(&tickets).Error
	return tickets, err
}

func applyAdtyTicketQueueFilter(query *gorm.DB, queue string) (*gorm.DB, error) {
	switch strings.TrimSpace(queue) {
	case "", "all":
		return query, nil
	case "pending":
		return query.Where("status = ? OR status = ? OR (status = ? AND assignee_id = 0)",
			model.AdtyTicketNew, model.AdtyTicketPendingAssignment, model.AdtyTicketLegacyOpen), nil
	case "waiting_user":
		return query.Where("status = ?", model.AdtyTicketWaitingUser), nil
	case "in_progress":
		return query.Where("status = ? OR status = ? OR (status = ? AND assignee_id > 0)",
			model.AdtyTicketInProgress, model.AdtyTicketLegacyAssigned, model.AdtyTicketLegacyOpen), nil
	case "waiting_internal", "resolved", "closed":
		return query.Where("status = ?", queue), nil
	case "urgent":
		return query.Where("priority = ? AND status NOT IN ?", model.AdtyTicketPriorityUrgent, []string{model.AdtyTicketResolved, model.AdtyTicketClosed}), nil
	default:
		return nil, ErrAdtyTicketInvalid
	}
}

func countAdtyTicketBadges(scope *gorm.DB) (AdtyTicketBadges, error) {
	var badges AdtyTicketBadges
	var err error
	count := func(dest *int64, apply func(*gorm.DB) *gorm.DB) {
		if err != nil {
			return
		}
		err = apply(scope.Session(&gorm.Session{})).Count(dest).Error
	}
	count(&badges.New, func(q *gorm.DB) *gorm.DB {
		return q.Where("status = ? OR (status = ? AND assignee_id = 0)", model.AdtyTicketNew, model.AdtyTicketLegacyOpen)
	})
	count(&badges.PendingAssignment, func(q *gorm.DB) *gorm.DB {
		return q.Where("status = ?", model.AdtyTicketPendingAssignment)
	})
	count(&badges.InProgress, func(q *gorm.DB) *gorm.DB {
		return q.Where("status = ? OR status = ? OR (status = ? AND assignee_id > 0)",
			model.AdtyTicketInProgress, model.AdtyTicketLegacyAssigned, model.AdtyTicketLegacyOpen)
	})
	count(&badges.WaitingUser, func(q *gorm.DB) *gorm.DB {
		return q.Where("status = ?", model.AdtyTicketWaitingUser)
	})
	count(&badges.WaitingInternal, func(q *gorm.DB) *gorm.DB {
		return q.Where("status = ?", model.AdtyTicketWaitingInternal)
	})
	count(&badges.Urgent, func(q *gorm.DB) *gorm.DB {
		return q.Where("priority = ? AND status NOT IN ?", model.AdtyTicketPriorityUrgent, []string{model.AdtyTicketResolved, model.AdtyTicketClosed})
	})
	count(&badges.Resolved, func(q *gorm.DB) *gorm.DB { return q.Where("status = ?", model.AdtyTicketResolved) })
	count(&badges.Closed, func(q *gorm.DB) *gorm.DB { return q.Where("status = ?", model.AdtyTicketClosed) })
	badges.Pending = badges.New + badges.PendingAssignment
	return badges, err
}

func GetAdtyTicket(userId int, ticketId int64, workspace ...string) (*AdtyTicketDetail, error) {
	actor, err := loadAdtyTicketWorkspace(model.DB, userId, workspace...)
	if err != nil {
		return nil, err
	}
	var ticket model.AdtyTicket
	if err := scopedAdtyTickets(model.DB, actor).First(&ticket, ticketId).Error; err != nil {
		return nil, err
	}
	visibility := []string{model.AdtyTicketVisibilityPublic}
	if actor.isHandling() {
		visibility = append(visibility, model.AdtyTicketVisibilityNote)
	}
	var messages []model.AdtyTicketMessage
	if err := model.DB.Where("ticket_id = ? AND visibility IN ?", ticket.Id, visibility).Order("id ASC").Find(&messages).Error; err != nil {
		return nil, err
	}
	keys := map[string]int{}
	var translationKeys []string
	for i, m := range messages {
		if ticketSupportAuthor(m.AuthorRole) && m.Visibility == model.AdtyTicketVisibilityPublic && adtyContainsChinese(m.Body) {
			key := ticketTranslationKey(m.Id)
			keys[key] = i
			translationKeys = append(translationKeys, key)
			messages[i].TranslationStatus = model.AdtyTranslationPending
		}
	}
	if len(translationKeys) > 0 {
		var heads []model.AdtyTranslationHead
		if err := model.DB.Where("object_key IN ?", translationKeys).Find(&heads).Error; err != nil {
			return nil, err
		}
		for _, head := range heads {
			m := &messages[keys[head.ObjectKey]]
			m.TranslationStatus, m.English = head.Status, head.English
		}
	}
	var images []model.AdtyTicketImage
	if err := model.DB.Select("id", "object_id", "ticket_id", "message_id", "visibility", "mime", "size", "width", "height", "sha256", "created_at").Where("ticket_id = ? AND visibility IN ? AND COALESCE(delete_pending, false) = false", ticket.Id, visibility).Order("id ASC").Find(&images).Error; err != nil {
		return nil, err
	}
	var events []model.AdtyTicketEvent
	eventQuery := model.DB.Where("ticket_id = ?", ticket.Id)
	if !actor.isHandling() {
		eventQuery = eventQuery.Where("action IN ?", []string{"create", "reply", "status", "reopen", "priority", "assign", "transfer", "site_handoff"})
	}
	if err := eventQuery.Order("id ASC").Find(&events).Error; err != nil {
		return nil, err
	}
	ticket.Status = model.NormalizeAdtyTicketStatus(ticket.Status, ticket.AssigneeId)
	var assignees []AdtyTicketAssignee
	if actor.isHandling() {
		assignees, err = adtyTicketAssigneesTx(model.DB, ticket, actor.Scope == "platform" || actor.Scope == "platform_main", 0)
		if err != nil {
			return nil, err
		}
	}
	canControl := actor.isHandling() || isAdtyTicketCustomerController(actor, ticket)
	return &AdtyTicketDetail{Actions: allowedTicketActions(actor, ticket), Assignees: assignees, Ticket: ticket, Messages: messages, Images: images, Events: events, CanNote: actor.isHandling(), CanManage: actor.isHandling(), CanControl: canControl}, nil
}

func isAdtyTicketCustomerController(actor adtyTicketActor, ticket model.AdtyTicket) bool {
	if actor.Staff != nil || actor.User.StaffSiteId > 0 {
		return false
	}
	if actor.User.Id == ticket.UserId {
		return !ticket.TeamShared || (actor.TeamId > 0 && actor.TeamId == ticket.TeamId)
	}
	return ticket.TeamShared && actor.TeamId > 0 && actor.TeamId == ticket.TeamId && actor.TeamRole == model.AdtyTeamRoleLeader
}

func allowedTicketActions(actor adtyTicketActor, ticket model.AdtyTicket) []string {
	var actions []string
	status := model.NormalizeAdtyTicketStatus(ticket.Status, ticket.AssigneeId)
	active := canAssignAdtyTicket(status)
	handler := actor.isHandling()
	if active {
		actions = append(actions, "reply")
	}
	if handler && active {
		actions = append(actions, "assign", "note")
		if status == model.AdtyTicketNew && ticket.AssigneeId == 0 {
			actions = append(actions, model.AdtyTicketPendingAssignment)
		}
		if ticket.AssigneeId > 0 && status != model.AdtyTicketInProgress {
			actions = append(actions, model.AdtyTicketInProgress)
		}
		if canMoveAdtyTicketToWaiting(status) {
			for _, next := range []string{model.AdtyTicketWaitingUser, model.AdtyTicketWaitingInternal} {
				if next != status {
					actions = append(actions, next)
				}
			}
		}
	}
	if handler && status != model.AdtyTicketClosed {
		actions = append(actions, "close")
	}
	if handler || isAdtyTicketCustomerController(actor, ticket) {
		if active {
			actions = append(actions, "resolve")
		} else {
			actions = append(actions, "reopen")
		}
	}
	return actions
}

func ReplyAdtyTicket(userId, ticketId int64, body, idempotency string, workspace ...string) (*model.AdtyTicketMessage, error) {
	return writeAdtyTicketMessage(int(userId), ticketId, body, idempotency, model.AdtyTicketVisibilityPublic, workspace...)
}

func NoteAdtyTicket(userId int, ticketId int64, body, idempotency string, workspace ...string) (*model.AdtyTicketMessage, error) {
	return writeAdtyTicketMessage(userId, ticketId, body, idempotency, model.AdtyTicketVisibilityNote, workspace...)
}

func SetAdtyTicketStatus(userId int, ticketId int64, status, priority string, assigneeId int, workspace ...string) error {
	return SetAdtyTicketStatusWithKey(userId, ticketId, status, priority, assigneeId, "", 0, workspace...)
}

func SetAdtyTicketStatusWithKey(userId int, ticketId int64, status, priority string, assigneeId int, key string, closureID int64, workspace ...string) error {
	if assigneeId < 0 {
		return ErrAdtyTicketInvalid
	}
	var err error
	if err := ensureAdtyTicketNoticeSchema(); err != nil {
		return err
	}
	var statusAudit map[string]any
	var statusBefore map[string]any
	var statusAuditEntry *model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if assigneeId > 0 {
			var membership model.AdtySiteStaff
			if err := model.AdtyLockForUpdate(tx).Select("id").Where("user_id=?", assigneeId).Limit(1).Find(&membership).Error; err != nil {
				return err
			}
		}
		actor, err := loadAdtyTicketWorkspace(tx, userId, workspace...)
		if err != nil {
			return err
		}
		var ticket model.AdtyTicket
		if err := scopedAdtyTickets(model.AdtyLockForUpdate(tx), actor).First(&ticket, ticketId).Error; err != nil {
			return err
		}
		var operationKey *string
		if status == "reopen" && key != "" {
			if !isAdtyTicketCustomerController(actor, ticket) && !actor.isHandling() {
				return ErrAdtyTicketForbidden
			}
			digest := sha256.Sum256([]byte(strconv.FormatInt(ticket.Id, 10) + ":" + strconv.Itoa(userId) + ":reopen:" + key))
			encoded := hex.EncodeToString(digest[:])
			operationKey = &encoded
			var existing model.AdtyTicketEvent
			found := tx.Where("operation_key=?", encoded).Limit(1).Find(&existing)
			if found.Error != nil || found.RowsAffected > 0 {
				return found.Error
			}
			var last model.AdtyTicketEvent
			if err := tx.Where("ticket_id=? AND action='status' AND detail IN ?", ticket.Id, []string{model.AdtyTicketClosed, model.AdtyTicketResolved}).Order("id DESC").Limit(1).Find(&last).Error; err != nil {
				return err
			}
			if last.Id != closureID || (ticket.Status != model.AdtyTicketClosed && ticket.Status != model.AdtyTicketResolved) {
				return ErrAdtyTicketInvalid
			}
		}
		if err := releaseInvalidTicketAssigneeTx(tx, &ticket, userId); err != nil {
			return err
		}
		current := model.NormalizeAdtyTicketStatus(ticket.Status, ticket.AssigneeId)
		nextAssignee := ticket.AssigneeId
		if assigneeId > 0 {
			if !actor.isHandling() {
				return ErrAdtyTicketForbidden
			}
			eligible, err := adtyTicketAssigneesTx(tx, ticket, actor.Scope == "platform" || actor.Scope == "platform_main", assigneeId)
			if err != nil {
				return err
			}
			if len(eligible) == 0 {
				return ErrAdtyTicketInvalid
			}
			nextAssignee = assigneeId
		}
		nextPriority := ticket.Priority
		if priority != "" {
			if !actor.isHandling() {
				return ErrAdtyTicketForbidden
			}
			switch priority {
			case model.AdtyTicketPriorityNormal, model.AdtyTicketPriorityHigh, model.AdtyTicketPriorityUrgent:
				nextPriority = priority
			default:
				return ErrAdtyTicketInvalid
			}
		}
		action := "status"
		next, err := nextAdtyTicketStatus(tx, actor, ticket, current, strings.TrimSpace(status), nextAssignee)
		if err != nil {
			return err
		}
		if next == "reopen_idempotent" {
			return nil
		}
		if strings.HasPrefix(next, "reopen:") {
			action = "reopen"
			next = strings.TrimPrefix(next, "reopen:")
		}
		if next == model.AdtyTicketInProgress && nextAssignee <= 0 {
			return ErrAdtyTicketInvalid
		}
		if (next == model.AdtyTicketNew || next == model.AdtyTicketPendingAssignment) && nextAssignee > 0 {
			return ErrAdtyTicketInvalid
		}
		if next == current && nextAssignee == ticket.AssigneeId && nextPriority == ticket.Priority {
			return nil
		}
		updates := map[string]any{"status": next}
		if nextPriority != ticket.Priority {
			updates["priority"] = nextPriority
		}
		if nextAssignee != ticket.AssigneeId {
			updates["assignee_id"] = nextAssignee
		}
		statusBefore = map[string]any{
			"ticket_id": ticket.Id, "status": current, "priority": ticket.Priority, "assignee_id": ticket.AssigneeId,
			"site_id": ticket.SiteId, "user_id": ticket.UserId,
		}
		statusAudit = map[string]any{
			"ticket_id": ticket.Id, "status": next, "priority": nextPriority, "assignee_id": nextAssignee,
			"site_id": ticket.SiteId, "user_id": ticket.UserId, "event": action,
		}
		if err := tx.Model(&model.AdtyTicket{}).Where("id = ?", ticket.Id).Updates(updates).Error; err != nil {
			return err
		}
		if nextAssignee != ticket.AssigneeId {
			assignment := "assign"
			if ticket.AssigneeId > 0 {
				assignment = "transfer"
			}
			change := strconv.Itoa(ticket.AssigneeId) + " → " + strconv.Itoa(nextAssignee)
			if err := tx.Create(&model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: actor.User.Id, Action: assignment, Detail: change}).Error; err != nil {
				return err
			}
		}
		eventAction := action
		eventDetail := next
		if action != "reopen" {
			if next == current && nextAssignee != ticket.AssigneeId {
				eventAction = "status"
				eventDetail = next
			} else if next == current && nextPriority != ticket.Priority {
				eventAction = "priority"
				eventDetail = nextPriority
			}
		}
		eventRow := model.AdtyTicketEvent{OperationKey: operationKey, ClosureEventId: closureID, TicketId: ticket.Id, ActorId: actor.User.Id, Action: eventAction, Detail: eventDetail}
		if err := tx.Create(&eventRow).Error; err != nil {
			return err
		}
		if err := emitAdtyTicketStatusNoticesTx(tx, ticket, eventRow, actor.User.Id, next, nextAssignee); err != nil {
			return err
		}
		var auditErr error
		statusAuditEntry, auditErr = appendAdtyOfficialAuditTx(tx, userId, ticket.SiteId, "ticket", strconv.FormatInt(ticketId, 10), "adty.ticket.status", "", "",
			statusBefore, statusAudit, model.AuditResultSuccess)
		return auditErr
	})
	if err == nil && statusAudit != nil {
		if statusAuditEntry != nil {
			deliverAdtyCommittedAudit(*statusAuditEntry)
		} else {
			siteID := 0
			if value, ok := statusAudit["site_id"].(int); ok {
				siteID = value
			}
			writeAdtyOfficialAudit(userId, siteID, "ticket", strconv.FormatInt(ticketId, 10), "adty.ticket.status", "", "",
				statusBefore, statusAudit, model.AuditResultSuccess)
		}
	}
	return err
}

func nextAdtyTicketStatus(tx *gorm.DB, actor adtyTicketActor, ticket model.AdtyTicket, current, requested string, nextAssignee int) (string, error) {
	landing := reopenAdtyTicketLanding(nextAssignee)
	switch requested {
	case "resolve":
		if !isAdtyTicketCustomerController(actor, ticket) && !actor.isHandling() {
			return "", ErrAdtyTicketForbidden
		}
		if current == model.AdtyTicketClosed {
			return "", ErrAdtyTicketInvalid
		}
		return model.AdtyTicketResolved, nil
	case "reopen":
		if !isAdtyTicketCustomerController(actor, ticket) && !actor.isHandling() {
			return "", ErrAdtyTicketForbidden
		}
		if current == model.AdtyTicketResolved || current == model.AdtyTicketClosed {
			return "reopen:" + landing, nil
		}
		if idempotentAdtyTicketReopen(tx, ticket, current, ticket.AssigneeId) {
			return "reopen_idempotent", nil
		}
		return "", ErrAdtyTicketInvalid
	case "close":
		if !actor.isHandling() {
			return "", ErrAdtyTicketForbidden
		}
		return model.AdtyTicketClosed, nil
	case model.AdtyTicketWaitingUser, model.AdtyTicketWaitingInternal:
		if !actor.isHandling() {
			return "", ErrAdtyTicketForbidden
		}
		if !canMoveAdtyTicketToWaiting(current) {
			return "", ErrAdtyTicketInvalid
		}
		return requested, nil
	case model.AdtyTicketInProgress:
		if !actor.isHandling() {
			return "", ErrAdtyTicketForbidden
		}
		if !canAssignAdtyTicket(current) {
			return "", ErrAdtyTicketInvalid
		}
		return model.AdtyTicketInProgress, nil
	case model.AdtyTicketPendingAssignment:
		if !actor.isHandling() {
			return "", ErrAdtyTicketForbidden
		}
		if nextAssignee > 0 {
			return "", ErrAdtyTicketInvalid
		}
		if current != model.AdtyTicketNew && current != model.AdtyTicketPendingAssignment {
			return "", ErrAdtyTicketInvalid
		}
		return model.AdtyTicketPendingAssignment, nil
	case "":
		if nextAssignee != ticket.AssigneeId {
			if !canAssignAdtyTicket(current) {
				return "", ErrAdtyTicketInvalid
			}
			return model.AdtyTicketInProgress, nil
		}
		return current, nil
	default:
		return "", ErrAdtyTicketInvalid
	}
}

func canAssignAdtyTicket(current string) bool {
	switch current {
	case model.AdtyTicketNew, model.AdtyTicketPendingAssignment, model.AdtyTicketInProgress, model.AdtyTicketWaitingUser, model.AdtyTicketWaitingInternal:
		return true
	default:
		return false
	}
}

func canMoveAdtyTicketToWaiting(current string) bool {
	switch current {
	case model.AdtyTicketInProgress, model.AdtyTicketWaitingUser, model.AdtyTicketWaitingInternal:
		return true
	default:
		return false
	}
}

func reopenAdtyTicketLanding(assigneeId int) string {
	if assigneeId > 0 {
		return model.AdtyTicketInProgress
	}
	return model.AdtyTicketPendingAssignment
}

func idempotentAdtyTicketReopen(tx *gorm.DB, ticket model.AdtyTicket, current string, assigneeId int) bool {
	if current != reopenAdtyTicketLanding(assigneeId) {
		return false
	}
	var event model.AdtyTicketEvent
	lookup := tx.Where("ticket_id = ? AND action IN ?", ticket.Id, []string{"reopen", "status"}).Order("id DESC").Limit(1).Find(&event)
	return lookup.Error == nil && lookup.RowsAffected > 0 && event.Action == "reopen"
}

func releaseTicketAssigneeTx(tx *gorm.DB, ticket *model.AdtyTicket, actorID int) error {
	previous := ticket.AssigneeId
	ticket.AssigneeId = 0
	if canAssignAdtyTicket(model.NormalizeAdtyTicketStatus(ticket.Status, previous)) {
		ticket.Status = model.AdtyTicketPendingAssignment
	}
	if err := tx.Model(ticket).UpdateColumns(map[string]any{"assignee_id": 0, "status": ticket.Status}).Error; err != nil {
		return err
	}
	return tx.Create(&model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: actorID, Action: "transfer", Detail: strconv.Itoa(previous) + " → 0"}).Error
}

func releaseInvalidTicketAssigneeTx(tx *gorm.DB, ticket *model.AdtyTicket, actorID int) error {
	if ticket.AssigneeId == 0 {
		return nil
	}
	eligible, err := adtyTicketAssigneesTx(tx, *ticket, true, ticket.AssigneeId)
	if err != nil || len(eligible) > 0 {
		return err
	}
	return releaseTicketAssigneeTx(tx, ticket, actorID)
}

func releaseStaffTicketsTx(tx *gorm.DB, staff model.AdtySiteStaff, ownerID int) error {
	var tickets []model.AdtyTicket
	if err := model.AdtyLockForUpdate(tx).Where("assignee_id=? AND COALESCE(handling_site_id,site_id)=? AND status NOT IN ?", staff.UserId, staff.SiteId, []string{model.AdtyTicketClosed, model.AdtyTicketResolved}).Order("id").Find(&tickets).Error; err != nil {
		return err
	}
	for i := range tickets {
		if err := releaseTicketAssigneeTx(tx, &tickets[i], ownerID); err != nil {
			return err
		}
	}
	return nil
}

// AddAdtyTicketImage stores an attachment under the same visibility as the
// message it belongs to. The caller must declare visibility: there is no
// default, because a default would read as "optional" and silently decide a
// security question. Internal images require note permission.
func AddAdtyTicketImage(userId int, ticketId int64, visibility string, data []byte, workspace ...string) (*model.AdtyTicketImage, error) {
	return AttachAdtyTicketImage(userId, ticketId, 0, visibility, data, workspace...)
}

func AttachAdtyTicketImage(userId int, ticketId, messageID int64, visibility string, data []byte, workspace ...string) (*model.AdtyTicketImage, error) {
	return AttachAdtyTicketImageWithKey(userId, ticketId, messageID, visibility, data, "", workspace...)
}

func AttachAdtyTicketImageWithKey(userId int, ticketId, messageID int64, visibility string, data []byte, key string, workspace ...string) (*model.AdtyTicketImage, error) {
	if visibility != model.AdtyTicketVisibilityPublic && visibility != model.AdtyTicketVisibilityNote {
		return nil, ErrAdtyTicketInvalid
	}
	var err error
	policy := adtyTicketPolicy()
	var stripped []byte
	var mime string
	var width, height int
	var sanitizeErr error = ErrAdtyTicketImage
	if len(data) > policy.MaxBytes {
		sanitizeErr = ErrAdtyTicketImageSize
	} else if len(data) > 0 {
		stripped, mime, width, height, sanitizeErr = sanitizeAdtyTicketImage(data, policy)
	}
	var sum [32]byte
	var objectID string
	var name string
	var dir string
	if sanitizeErr == nil {
		sum = sha256.Sum256(stripped)
		objectID, err = newAdtyTicketImageObjectID()
		if err != nil {
			return nil, err
		}
		dir = adtyTicketImageDir()
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
		name = objectID + adtyTicketExt(mime)
	}
	var row model.AdtyTicketImage
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		actor, err := loadAdtyTicketWorkspace(tx, userId, workspace...)
		if err != nil {
			return err
		}
		var ticket model.AdtyTicket
		if err := scopedAdtyTickets(model.AdtyLockForUpdate(tx), actor).First(&ticket, ticketId).Error; err != nil {
			return err
		}
		if visibility == model.AdtyTicketVisibilityNote && !actor.isHandling() {
			return ErrAdtyTicketForbidden
		}
		if messageID != 0 {
			var message model.AdtyTicketMessage
			if err := tx.Where("id=? AND ticket_id=? AND author_id=? AND visibility=?", messageID, ticket.Id, actor.User.Id, visibility).First(&message).Error; err != nil {
				return err
			}
		}
		if sanitizeErr != nil {
			return sanitizeErr
		}
		if key == "" {
			// Legacy clients do not send a key. Reattaching the same image
			// to the same message after a lost response reuses that object.
			key = "content:" + strconv.FormatInt(messageID, 10) + ":" + visibility + ":" + hex.EncodeToString(sum[:])
		}
		digest := sha256.Sum256([]byte(strconv.FormatInt(ticketId, 10) + ":" + strconv.Itoa(userId) + ":" + key))
		uploadKey := hex.EncodeToString(digest[:])
		prior := tx.Where("upload_key=?", uploadKey).Limit(1).Find(&row)
		if prior.Error != nil {
			return prior.Error
		}
		if prior.RowsAffected > 0 {
			if row.MessageId != messageID || row.Visibility != visibility || row.SHA256 != hex.EncodeToString(sum[:]) {
				return ErrAdtyTicketInvalid
			}
			return nil
		}
		if ticket.Status == model.AdtyTicketClosed || ticket.Status == model.AdtyTicketResolved {
			return ErrAdtyTicketInvalid
		}
		var count int64
		var total int64
		if err := tx.Model(&model.AdtyTicketImage{}).Where("ticket_id = ? AND COALESCE(delete_pending,false)=false", ticket.Id).Count(&count).Error; err != nil {
			return err
		}
		if int(count) >= policy.MaxCount {
			return ErrAdtyTicketImage
		}
		if err := tx.Model(&model.AdtyTicketImage{}).Where("ticket_id = ? AND COALESCE(delete_pending,false)=false", ticket.Id).Select("COALESCE(SUM(size),0)").Scan(&total).Error; err != nil {
			return err
		}
		if int(total)+len(stripped) > policy.MaxTotal {
			return ErrAdtyTicketImage
		}
		filePath := filepath.Join(dir, name)
		if err := os.WriteFile(filePath, stripped, 0o640); err != nil {
			return err
		}
		row = model.AdtyTicketImage{
			UploadKey: &uploadKey, ObjectId: objectID, TicketId: ticket.Id, MessageId: messageID, Visibility: visibility, Mime: mime,
			Size: len(stripped), Width: width, Height: height, SHA256: hex.EncodeToString(sum[:]), Storage: name,
		}
		if err := tx.Create(&row).Error; err != nil {
			_ = os.Remove(filePath)
			return err
		}
		event := model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: actor.User.Id, Action: "image_upload", Detail: mime}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		if visibility != model.AdtyTicketVisibilityPublic || actor.isHandling() {
			return nil
		}
		if err := releaseInvalidTicketAssigneeTx(tx, &ticket, userId); err != nil {
			return err
		}
		if ticket.Status == model.AdtyTicketWaitingUser {
			next := reopenAdtyTicketLanding(ticket.AssigneeId)
			if err := tx.Model(&ticket).Update("status", next).Error; err != nil {
				return err
			}
			if err := tx.Create(&model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: userId, Action: "status", Detail: next}).Error; err != nil {
				return err
			}
		}
		id := strconv.FormatInt(ticketId, 10)
		return notifyTicketHandlersTx(tx, ticket, event.Id, userId, event.Action, "ticket:"+id+":image:"+strconv.FormatInt(row.Id, 10),
			"工单 #"+id+" 已补图 / Ticket #"+id+" attachment", "客户已补充图片，可在工单中查看。\n\nThe customer added an attachment.")
	})
	if err != nil {
		if row.ObjectId == objectID && name != "" {
			_ = os.Remove(filepath.Join(dir, name))
		}
		return nil, err
	}
	return &row, nil
}

func AuthorizedAdtyTicketImage(userId int, ticketId, imageId int64, workspace ...string) (*model.AdtyTicketImage, error) {
	actor, err := loadAdtyTicketWorkspace(model.DB, userId, workspace...)
	if err != nil {
		return nil, err
	}
	var ticket model.AdtyTicket
	if err := scopedAdtyTickets(model.DB, actor).First(&ticket, ticketId).Error; err != nil {
		return nil, err
	}
	var imageRow model.AdtyTicketImage
	if err := model.DB.Where("id = ? AND ticket_id = ? AND COALESCE(delete_pending,false)=false", imageId, ticket.Id).First(&imageRow).Error; err != nil {
		return nil, err
	}
	// Same gate as the message list. Anything that is not explicitly public is
	// treated as internal, so a row that skipped the backfill still fails closed.
	if imageRow.Visibility != model.AdtyTicketVisibilityPublic && !actor.isHandling() {
		return nil, ErrAdtyTicketForbidden
	}
	return &imageRow, nil
}

func OpenAdtyTicketImage(userId int, ticketId, imageId int64, workspace ...string) (string, string, error) {
	imageRow, err := AuthorizedAdtyTicketImage(userId, ticketId, imageId, workspace...)
	if err != nil {
		return "", "", err
	}
	path, err := adtyTicketImageStoragePath(*imageRow)
	if err != nil {
		return "", "", os.ErrNotExist
	}
	if err := model.DB.Create(&model.AdtyTicketEvent{TicketId: ticketId, ActorId: userId, Action: "image_view", Detail: strconv.FormatInt(imageRow.Id, 10)}).Error; err != nil {
		return "", "", err
	}
	return path, imageRow.Mime, nil
}

// DeleteAdtyTicketImage hides an image atomically inside a transaction, records
// both a ticket event and an official audit log, and removes the backing file
// after commit so a crash or disk failure never leaves a dangling visible image.
func DeleteAdtyTicketImage(userId int, ticketId, imageId int64, workspace ...string) error {
	var target model.AdtyTicketImage
	var ticketSiteID int
	var deleteAudit *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		actor, err := loadAdtyTicketWorkspace(tx, userId, workspace...)
		if err != nil {
			return err
		}
		var ticket model.AdtyTicket
		if err := scopedAdtyTickets(model.AdtyLockForUpdate(tx), actor).First(&ticket, ticketId).Error; err != nil {
			return err
		}
		if ticket.Status == model.AdtyTicketClosed || ticket.Status == model.AdtyTicketResolved {
			return ErrAdtyTicketInvalid
		}
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND ticket_id = ? AND COALESCE(delete_pending,false)=false", imageId, ticket.Id).First(&target).Error; err != nil {
			return err
		}
		if target.Visibility != model.AdtyTicketVisibilityPublic {
			if !actor.isHandling() {
				return ErrAdtyTicketForbidden
			}
		} else if !actor.isHandling() {
			allowed := isAdtyTicketCustomerController(actor, ticket)
			if !allowed && target.MessageId > 0 {
				var msg model.AdtyTicketMessage
				if err := tx.Select("author_id").Where("id = ? AND ticket_id = ?", target.MessageId, ticket.Id).First(&msg).Error; err == nil && msg.AuthorId == actor.User.Id {
					allowed = true
				}
			}
			if !allowed {
				return ErrAdtyTicketForbidden
			}
		}
		if err := tx.Model(&target).Update("delete_pending", true).Error; err != nil {
			return err
		}
		ticketSiteID = ticket.SiteId
		if err := tx.Create(&model.AdtyTicketEvent{
			TicketId: ticket.Id, ActorId: actor.User.Id, Action: "image_delete", Detail: strconv.FormatInt(target.Id, 10),
		}).Error; err != nil {
			return err
		}
		var auditErr error
		deleteAudit, auditErr = appendAdtyOfficialAuditTx(tx, userId, ticketSiteID, "ticket_image", strconv.FormatInt(target.Id, 10), "adty.ticket.image.delete", "", "",
			map[string]any{"ticket_id": ticketId, "image_id": target.Id, "object_id": target.ObjectId, "visibility": target.Visibility},
			map[string]any{"deleted": true},
			model.AuditResultSuccess)
		return auditErr
	})
	if err != nil {
		return err
	}
	if path, pathErr := adtyTicketImageStoragePath(target); pathErr == nil {
		if rmErr := os.Remove(path); rmErr == nil || errors.Is(rmErr, os.ErrNotExist) {
			_ = model.DB.Where("id = ? AND COALESCE(delete_pending,false)=true", target.Id).Delete(&model.AdtyTicketImage{}).Error
		}
	}
	if deleteAudit != nil {
		deliverAdtyCommittedAudit(*deleteAudit)
	} else {
		writeAdtyOfficialAudit(userId, ticketSiteID, "ticket_image", strconv.FormatInt(target.Id, 10), "adty.ticket.image.delete", "", "",
			map[string]any{"ticket_id": ticketId, "image_id": target.Id, "object_id": target.ObjectId, "visibility": target.Visibility},
			map[string]any{"deleted": true},
			model.AuditResultSuccess)
	}
	return nil
}

func writeAdtyTicketMessage(userId int, ticketId int64, body, idempotency, visibility string, workspace ...string) (*model.AdtyTicketMessage, error) {
	body = strings.TrimSpace(redactAdtySecrets(body))
	if body == "" {
		return nil, ErrAdtyTicketInvalid
	}
	var err error
	if err := ensureAdtyTicketNoticeSchema(); err != nil {
		return nil, err
	}
	var saved *model.AdtyTicketMessage
	createdNew := false
	var ticketSiteID int
	var replyAudit *model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		actor, err := loadAdtyTicketWorkspace(tx, userId, workspace...)
		if err != nil {
			return err
		}
		var ticket model.AdtyTicket
		if err := scopedAdtyTickets(model.AdtyLockForUpdate(tx), actor).First(&ticket, ticketId).Error; err != nil {
			return err
		}
		if visibility == model.AdtyTicketVisibilityNote && !actor.isHandling() {
			return ErrAdtyTicketForbidden
		}
		dedupe := ticketMessageDedupe(ticket.Id, actor.User.Id, visibility, idempotency)
		if strings.TrimSpace(idempotency) != "" {
			var prior model.AdtyTicketMessage
			lookup := tx.Where("dedupe = ?", dedupe).Limit(1).Find(&prior)
			if lookup.Error != nil {
				return lookup.Error
			}
			if lookup.RowsAffected > 0 {
				if prior.TicketId != ticket.Id || prior.AuthorId != actor.User.Id || prior.Visibility != visibility {
					return ErrAdtyTicketForbidden
				}
				if prior.Visibility != model.AdtyTicketVisibilityPublic && !actor.isHandling() {
					return ErrAdtyTicketForbidden
				}
				saved = &prior
				return nil
			}
		}
		if err := releaseInvalidTicketAssigneeTx(tx, &ticket, userId); err != nil {
			return err
		}
		current := model.NormalizeAdtyTicketStatus(ticket.Status, ticket.AssigneeId)
		if current == model.AdtyTicketClosed || current == model.AdtyTicketResolved {
			return ErrAdtyTicketInvalid
		}
		authorRole := "customer"
		if actor.isHandling() {
			authorRole = actor.Scope
		}
		row := model.AdtyTicketMessage{TicketId: ticket.Id, AuthorId: actor.User.Id, AuthorRole: authorRole, Visibility: visibility, Body: body, Dedupe: dedupe}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if err := reserveTicketTranslationTx(tx, row, ticket.SiteId); err != nil {
			return err
		}
		action := "reply"
		if visibility == model.AdtyTicketVisibilityNote {
			action = "note"
		}
		eventRow := model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: actor.User.Id, Action: action}
		if err := tx.Create(&eventRow).Error; err != nil {
			return err
		}
		saved = &row
		createdNew = true
		ticketSiteID = ticket.SiteId
		if visibility == model.AdtyTicketVisibilityPublic {
			next := current
			if actor.isHandling() {
				next = model.AdtyTicketWaitingUser
			} else if current == model.AdtyTicketWaitingUser {
				next = reopenAdtyTicketLanding(ticket.AssigneeId)
			}
			if next != current {
				if err := tx.Model(&ticket).Update("status", next).Error; err != nil {
					return err
				}
				if err := tx.Create(&model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: actor.User.Id, Action: "status", Detail: next}).Error; err != nil {
					return err
				}
			}
			if err := emitAdtyTicketReplyNoticesTx(tx, ticket, row, eventRow.Id, actor.User.Id); err != nil {
				return err
			}
			var auditErr error
			replyAudit, auditErr = appendAdtyOfficialAuditTx(tx, userId, ticketSiteID, "ticket", strconv.FormatInt(ticketId, 10), "adty.ticket.reply", "", "",
				nil,
				map[string]any{
					"ticket_id": ticketId, "message_id": row.Id, "author_id": row.AuthorId,
					"visibility": row.Visibility, "site_id": ticketSiteID,
				},
				model.AuditResultSuccess)
			if auditErr != nil {
				return auditErr
			}
		}
		return nil
	})
	if err == nil && createdNew && saved != nil && visibility == model.AdtyTicketVisibilityPublic {
		if replyAudit != nil {
			deliverAdtyCommittedAudit(*replyAudit)
		} else {
			writeAdtyOfficialAudit(userId, ticketSiteID, "ticket", strconv.FormatInt(ticketId, 10), "adty.ticket.reply", "", "",
				nil,
				map[string]any{
					"ticket_id": ticketId, "message_id": saved.Id, "author_id": saved.AuthorId,
					"visibility": saved.Visibility, "site_id": ticketSiteID,
				},
				model.AuditResultSuccess)
		}
	}
	return saved, err
}

// An assigned case goes to its active handler. The site owner covers an unassigned
// local case, and authorized main-site handlers share the main-site duty queue.
func adtyTicketHandlersTx(tx *gorm.DB, ticket model.AdtyTicket) ([]int, error) {
	candidates, err := adtyTicketAssigneesTx(tx, ticket, true, 0)
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		if c.Id == ticket.AssigneeId {
			return []int{c.Id}, nil
		}
	}
	if ticket.HandlingSite() > 0 {
		var site model.AdtySite
		if err := tx.Where("id = ?", ticket.HandlingSite()).Limit(1).Find(&site).Error; err != nil {
			return nil, err
		}
		for _, c := range candidates {
			if c.Id == site.OwnerUserId {
				return []int{c.Id}, nil
			}
		}
		return nil, nil
	}
	ids := make([]int, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.Id)
	}
	return ids, nil
}

// Candidate names are restricted to the handling workspace. The mutation uses
// the same candidate query with an explicit ID, so grants are rechecked on transfer.
func adtyTicketAssigneesTx(tx *gorm.DB, ticket model.AdtyTicket, includePlatform bool, id int) ([]AdtyTicketAssignee, error) {
	userQuery := tx.Model(&model.User{}).Select("id, username, display_name, role, site_id, staff_site_id").Where("status = ?", common.UserStatusEnabled)
	if id > 0 {
		userQuery = userQuery.Where("id = ?", id)
	}
	var staff []model.AdtySiteStaff
	var ownerID int
	if ticket.HandlingSite() > 0 {
		var site model.AdtySite
		if err := tx.Where("id = ? AND status IN ?", ticket.HandlingSite(), []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}).Limit(1).Find(&site).Error; err != nil {
			return nil, err
		}
		if site.Id > 0 && AdtySiteAgreementAccepted(site.AgreementVer) {
			ownerID = site.OwnerUserId
			q := tx.Where("site_id = ? AND owner_user_id = ? AND status = ?", ticket.HandlingSite(), ownerID, "enabled")
			if id > 0 {
				q = q.Where("user_id = ?", id)
			}
			if err := q.Find(&staff).Error; err != nil {
				return nil, err
			}
		}
	}
	localIDs := []int{ownerID}
	for _, member := range staff {
		if slices.Contains(AdtyStaffViewOf(member).Permissions, AdtyStaffTicketManage) {
			localIDs = append(localIDs, member.UserId)
		}
	}
	if includePlatform {
		userQuery = userQuery.Where("id IN ? OR (role >= ? AND site_id = 0 AND staff_site_id = 0)", localIDs, common.RoleAdminUser)
	} else {
		userQuery = userQuery.Where("id IN ?", localIDs)
	}
	var users []model.User
	if err := userQuery.Order("username ASC, id ASC").Find(&users).Error; err != nil {
		return nil, err
	}
	var platformCan func(int, int, authz.Permission) bool
	if includePlatform && slices.ContainsFunc(users, func(user model.User) bool { return user.Role == common.RoleAdminUser }) && tx.Migrator().HasTable(&model.CasbinRule{}) {
		var err error
		platformCan, err = authz.PermissionCheckerInTx(tx.Where("v1 = ?", authz.ResourceAdtyTicket))
		if err != nil {
			return nil, err
		}
	}
	result := []AdtyTicketAssignee{}
	for _, user := range users {
		local := user.Id == ownerID || (user.Role == common.RoleCommonUser && ticket.HandlingSite() > 0 && user.StaffSiteId == ticket.HandlingSite() && user.SiteId == ticket.HandlingSite())
		platform := includePlatform && user.Role >= common.RoleRootUser
		if platformCan != nil && user.Role == common.RoleAdminUser {
			platform = platformCan(user.Id, user.Role, authz.AdtyTicketManageAll) || (ticket.HandlingSite() == 0 && platformCan(user.Id, user.Role, authz.AdtyTicketManage))
		}
		if !local && !platform {
			continue
		}
		result = append(result, AdtyTicketAssignee{Id: user.Id, Username: user.Username, DisplayName: user.DisplayName})
	}
	return result, nil
}

func validateAdtyTicketAssigneeTx(tx *gorm.DB, ticket model.AdtyTicket, id int) error {
	eligible, err := adtyTicketAssigneesTx(tx, ticket, true, id)
	if err != nil {
		return err
	}
	if id <= 0 || len(eligible) == 0 {
		return ErrAdtyTicketInvalid
	}
	return nil
}

func CheckAdtyPlatformTicketAccess(userID int) error {
	_, err := loadAdtyTicketWorkspace(model.DB, userID, "platform")
	return err
}

func loadAdtyTicketActor(userId int) (adtyTicketActor, error) {
	return loadAdtyTicketActorTx(model.DB, userId)
}

func loadAdtyTicketActorTx(tx *gorm.DB, userId int) (adtyTicketActor, error) {
	var actor adtyTicketActor
	if err := tx.First(&actor.User, userId).Error; err != nil {
		return actor, err
	}
	if actor.User.Status != common.UserStatusEnabled {
		return actor, ErrAdtyTicketForbidden
	}
	if actor.User.SiteId == 0 && actor.User.StaffSiteId == 0 && actor.User.Role >= common.RoleAdminUser {
		if actor.User.Role >= common.RoleRootUser {
			actor.PlatformAll, actor.PlatformMain = true, true
		} else if tx.Migrator().HasTable(&model.CasbinRule{}) {
			check, err := authz.PermissionCheckerInTx(tx.Where("v1 = ?", authz.ResourceAdtyTicket))
			if err != nil {
				return actor, err
			}
			actor.PlatformAll = check(userId, actor.User.Role, authz.AdtyTicketManageAll)
			actor.PlatformMain = actor.PlatformAll || check(userId, actor.User.Role, authz.AdtyTicketManage)
		}
	}
	hasSiteTable := tx.Migrator().HasTable(&model.AdtySite{})
	if tx.Migrator().HasTable(&model.AdtySiteStaff{}) {
		var staff model.AdtySiteStaff
		lookup := tx.Where("user_id = ?", userId).Limit(1).Find(&staff)
		if lookup.Error != nil {
			return actor, lookup.Error
		}
		if lookup.RowsAffected > 0 {
			actor.Staff = &staff
			if hasSiteTable && staff.SiteId > 0 && actor.User.StaffSiteId == staff.SiteId && actor.User.SiteId == staff.SiteId && actor.User.Role == common.RoleCommonUser && staff.Status == "enabled" && slices.Contains(AdtyStaffViewOf(staff).Permissions, AdtyStaffTicketManage) {
				var staffSite model.AdtySite
				res := tx.Model(&model.AdtySite{}).Where("id = ? AND owner_user_id = ? AND status IN ?", staff.SiteId, staff.OwnerUserId, []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}).Limit(1).Find(&staffSite)
				if res.Error != nil {
					return actor, res.Error
				}
				actor.StaffTickets = res.RowsAffected == 1 && AdtySiteAgreementAccepted(staffSite.AgreementVer)
			}
		}
	}
	if hasSiteTable {
		var site model.AdtySite
		owned := tx.Where("owner_user_id = ? AND status IN ?", userId, []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}).Limit(1).Find(&site)
		if owned.Error != nil {
			return actor, owned.Error
		}
		if owned.RowsAffected > 0 {
			actor.OwnerSiteId = site.Id
			actor.OwnerTickets = AdtySiteAgreementAccepted(site.AgreementVer)
		}
	}
	if tx.Migrator().HasTable(&model.AdtyTeamMember{}) && tx.Migrator().HasTable(&model.AdtyTeam{}) {
		var member model.AdtyTeamMember
		team := tx.Where("user_id = ? AND status = ?", userId, model.AdtyTeamMemberActive).Limit(1).Find(&member)
		if team.Error != nil {
			return actor, team.Error
		}
		if team.RowsAffected > 0 {
			var active int64
			if err := tx.Model(&model.AdtyTeam{}).Where("id = ? AND site_id = ? AND status = ?", member.TeamId, actor.User.SiteId, model.AdtyTeamStatusActive).Count(&active).Error; err != nil {
				return actor, err
			}
			if active == 1 {
				actor.TeamId = member.TeamId
				actor.TeamRole = member.Role
			}
		}
	}
	return actor, nil
}

// A PAT may only narrow the workspace available to its actual account.
func AdtyTicketTokenScope(workspace, method string, granted []string) string {
	switch workspace {
	case "platform", "platform_main":
		if AccessTokenScopeGranted(granted, AccessTokenScopeOf(authz.AdtyTicketManageAll)) {
			return workspace
		}
		if AccessTokenScopeGranted(granted, AccessTokenScopeOf(authz.AdtyTicketManage)) {
			return "platform_main"
		}
	case "owner":
		// The owner workspace uses the existing site-owner PAT scope.
		scope := "wallet:write"
		if method == http.MethodGet {
			scope = "wallet:read"
		}
		if AccessTokenScopeGranted(granted, scope) {
			return workspace
		}
	case "staff":
		// Dedicated staff workspaces require a live browser session.
	default:
		return workspace
	}
	return "denied"
}

func loadAdtyTicketWorkspace(tx *gorm.DB, userId int, requested ...string) (adtyTicketActor, error) {
	if len(requested) > 1 {
		return adtyTicketActor{}, ErrAdtyTicketInvalid
	}
	workspace := ""
	if len(requested) == 1 {
		workspace = requested[0]
	}
	if (workspace == "staff" || len(requested) == 0) && tx != model.DB {
		// A staff grant update locks this row before replacing permissions.
		var membership model.AdtySiteStaff
		if err := model.AdtyLockForUpdate(tx).Select("id").Where("user_id = ?", userId).Limit(1).Find(&membership).Error; err != nil {
			return adtyTicketActor{}, err
		}
	}
	actor, err := loadAdtyTicketActorTx(tx, userId)
	if err != nil {
		return actor, err
	}
	// In-process callers predating workspaces use their authorized handling
	// scope. HTTP always supplies a scope, with an empty value meaning personal.
	if len(requested) == 0 {
		switch {
		case actor.PlatformMain:
			workspace = "platform"
		case actor.OwnerTickets:
			workspace = "owner"
		case actor.StaffTickets:
			workspace = "staff"
		case actor.TeamRole == model.AdtyTeamRoleLeader:
			workspace = "participant"
		}
	}
	actor.Scope = workspace
	permitted := false
	switch workspace {
	case "denied":
		return actor, ErrAdtyTicketForbidden
	case "", "personal":
		actor.Scope = "personal"
		permitted = actor.User.StaffSiteId == 0 && actor.Staff == nil
	case "participant":
		permitted = len(requested) == 0 && actor.Staff == nil && actor.TeamId > 0 && actor.TeamRole == model.AdtyTeamRoleLeader
	case "team":
		permitted = actor.Staff == nil && actor.TeamId > 0
	case "platform", "platform_main":
		permitted = actor.PlatformMain
	case "owner":
		permitted = actor.OwnerTickets
	case "staff":
		permitted = actor.StaffTickets
	default:
		return actor, ErrAdtyTicketInvalid
	}
	if !permitted {
		return actor, ErrAdtyTicketForbidden
	}
	return actor, nil
}

func (actor adtyTicketActor) isHandling() bool {
	return actor.Scope == "platform" || actor.Scope == "platform_main" || actor.Scope == "owner" || actor.Scope == "staff"
}

func (actor adtyTicketActor) canManage(ticket model.AdtyTicket) bool {
	return actor.PlatformAll || (actor.PlatformMain && ticket.HandlingSite() == 0) ||
		(actor.OwnerTickets && actor.OwnerSiteId == ticket.HandlingSite()) || (actor.StaffTickets && actor.Staff.SiteId == ticket.HandlingSite())
}

func (actor adtyTicketActor) canRead(ticket model.AdtyTicket) bool {
	if actor.Staff != nil || actor.User.StaffSiteId > 0 {
		return actor.canManage(ticket)
	}
	return actor.canReadOwnTicket(ticket) || actor.canManage(ticket) ||
		((actor.TeamRole == model.AdtyTeamRoleLeader || ticket.TeamShared) && actor.TeamId > 0 && actor.TeamId == ticket.TeamId)
}

func (actor adtyTicketActor) canReadOwnTicket(ticket model.AdtyTicket) bool {
	return ticket.UserId == actor.User.Id && (!ticket.TeamShared || (actor.TeamId > 0 && actor.TeamId == ticket.TeamId))
}

func ticketCreateDedupe(siteId, userId int, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ticketDedupe(0, "")
	}
	prefix := "create:" + strconv.Itoa(siteId) + ":" + strconv.Itoa(userId) + ":"
	if siteId < 0 {
		prefix = "create-user:" + strconv.Itoa(userId) + ":"
	}
	raw := prefix + key
	if len(raw) > 160 {
		sum := sha256.Sum256([]byte(raw))
		return prefix + "sha256:" + hex.EncodeToString(sum[:])
	}
	return raw
}

func ticketMessageDedupe(ticketId int64, userId int, visibility, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ticketDedupe(ticketId, "")
	}
	action := "reply"
	if visibility == model.AdtyTicketVisibilityNote {
		action = "note"
	}
	prefix := strconv.FormatInt(ticketId, 10) + ":" + strconv.Itoa(userId) + ":" + action + ":"
	raw := prefix + key
	if len(raw) > 160 {
		sum := sha256.Sum256([]byte(raw))
		return prefix + "sha256:" + hex.EncodeToString(sum[:])
	}
	return raw
}

func ticketDedupe(ticketId int64, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		var rnd [16]byte
		_, _ = rand.Read(rnd[:])
		raw := strconv.FormatInt(time.Now().UnixNano(), 10) + ":" + strconv.FormatInt(ticketId, 10) + ":" + hex.EncodeToString(rnd[:])
		sum := sha256.Sum256([]byte(raw))
		return "auto:" + hex.EncodeToString(sum[:])
	}
	return strconv.FormatInt(ticketId, 10) + ":" + key
}

func ensureAdtyTicketNoticeSchema() error {
	if model.DB == nil {
		return nil
	}
	if !model.DB.Migrator().HasTable(&model.AdtyTicketNotice{}) {
		if err := model.DB.AutoMigrate(&model.AdtyTicketNotice{}); err != nil {
			return err
		}
	}
	if !model.DB.Migrator().HasTable(&model.AdtyTelegramDelivery{}) {
		if err := model.DB.AutoMigrate(&model.AdtyTelegramBot{}, &model.AdtyTelegramBinding{}, &model.AdtyTelegramDelivery{}); err != nil {
			return err
		}
	}
	return nil
}

func saveAdtyTicketNoticeTx(tx *gorm.DB, row model.AdtyTicketNotice) error {
	row.EventKey = strings.TrimSpace(row.EventKey)
	if row.UserId <= 0 || row.TicketId <= 0 || row.EventKey == "" {
		return ErrAdtyTicketInvalid
	}
	sum := sha256.Sum256([]byte("ticket-notice:" + row.EventKey + ":" + strconv.Itoa(row.UserId)))
	row.ReadKey = hex.EncodeToString(sum[:])
	var existing model.AdtyTicketNotice
	lookup := tx.Where("event_key = ?", row.EventKey).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected > 0 {
		return nil
	}
	return tx.Create(&row).Error
}

func queueAdtyTicketTelegramNoticeTx(tx *gorm.DB, userId int, purpose, body, eventKey string, createdAt int64) error {
	body = strings.TrimSpace(redactAdtySecrets(body))
	eventKey = strings.TrimSpace(eventKey)
	if userId <= 0 || eventKey == "" || body == "" || adtyTextHasSecret(body) {
		return ErrAdtyTelegramInvalid
	}
	if !strings.Contains(body, "\n\n") {
		body += "\n\nEnglish is not ready"
	}
	var existing model.AdtyTelegramDelivery
	lookup := tx.Where("event_key = ?", eventKey).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected > 0 {
		return nil
	}
	delivery := model.AdtyTelegramDelivery{
		EventKey: eventKey, UserId: userId, Purpose: purpose, Body: body, CreatedAt: createdAt, Status: model.AdtyTelegramDeliverySkip,
	}
	var binding model.AdtyTelegramBinding
	found := tx.Where("user_id = ? AND purpose = ? AND status = ?", userId, purpose, model.AdtyTelegramBindActive).Limit(1).Find(&binding)
	if found.Error != nil {
		return found.Error
	}
	if found.RowsAffected > 0 {
		var bot model.AdtyTelegramBot
		if err := tx.Where("purpose = ? AND enabled = ?", purpose, true).Limit(1).Find(&bot).Error; err != nil {
			return err
		}
		if bot.Id != 0 && bot.IdentityHash == binding.BotIdentity {
			delivery.Status = model.AdtyTelegramDeliveryWait
			delivery.BindingId = binding.Id
			delivery.SiteId = binding.SiteId
		}
	}
	return tx.Create(&delivery).Error
}

func activeTicketContact(tx *gorm.DB, ticket model.AdtyTicket, id int) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	actor, err := loadAdtyTicketActorTx(tx, id)
	if err == ErrAdtyTicketForbidden || err == gorm.ErrRecordNotFound {
		return false, nil
	}
	return err == nil && actor.TeamId == ticket.TeamId && actor.TeamId > 0 && actor.Staff == nil && actor.User.StaffSiteId == 0, err
}

// Both inbox and Telegram rows are recorded with the event, including the
// intended recipient. Retries never infer recipients from a later assignment.
func recordTicketNoticeTx(tx *gorm.DB, ticket model.AdtyTicket, eventID int64, actorID, userID int, purpose, action, key, title, body string) error {
	if userID <= 0 {
		return nil
	}
	if purpose == model.AdtyTelegramPurposeUser && ticket.TeamShared {
		contact := ticket.ContactId
		if contact <= 0 {
			contact = ticket.UserId
		}
		valid, err := activeTicketContact(tx, ticket, contact)
		if err != nil {
			return err
		}
		if !valid {
			var team model.AdtyTeam
			if err := tx.Select("leader_user_id").Where("id=? AND status=?", ticket.TeamId, model.AdtyTeamStatusActive).Limit(1).Find(&team).Error; err != nil {
				return err
			}
			contact = team.LeaderUserId
			valid, err = activeTicketContact(tx, ticket, contact)
			if err != nil || !valid {
				return err
			}
		}
		if contact != ticket.ContactId {
			if err := tx.Model(&ticket).UpdateColumn("contact_id", contact).Error; err != nil {
				return err
			}
		}
		userID = contact
		if action != "create" && userID == actorID {
			return nil
		}
	}
	key += ":to:" + strconv.Itoa(userID)
	createdAt := time.Now().Unix()
	if err := saveAdtyTicketNoticeTx(tx, model.AdtyTicketNotice{
		EventKey: key, TicketId: ticket.Id, EventId: eventID, SiteId: ticket.HandlingSite(),
		UserId: userID, ActorId: actorID, Purpose: purpose, Action: action, Title: title, Content: body, CreatedAt: createdAt, TelegramPending: true,
	}); err != nil {
		return err
	}
	// A Telegram lookup failure may be retried without rolling back the reply.
	// A savepoint keeps the outer PostgreSQL transaction usable on failure.
	if err := tx.Transaction(func(outbox *gorm.DB) error {
		return queueAdtyTicketTelegramNoticeTx(outbox, userID, purpose, body, key, createdAt)
	}); err != nil {
		return nil
	}
	return tx.Model(&model.AdtyTicketNotice{}).Where("event_key=?", key).Update("telegram_pending", false).Error
}

func notifyTicketHandlersTx(tx *gorm.DB, ticket model.AdtyTicket, eventID int64, actorID int, action, key, title, body string) error {
	handlers, err := adtyTicketHandlersTx(tx, ticket)
	if err != nil {
		return err
	}
	for _, id := range handlers {
		if id == actorID || id == ticket.UserId {
			continue
		}
		if err := recordTicketNoticeTx(tx, ticket, eventID, actorID, id, model.AdtyTelegramPurposeAdmin, action, key, title, body); err != nil {
			return err
		}
	}
	return nil
}

func emitAdtyTicketCreateNoticesTx(tx *gorm.DB, ticket model.AdtyTicket, eventID int64, actorID int) error {
	id := strconv.FormatInt(ticket.Id, 10)
	key := "ticket:" + id + ":create"
	title := "工单 #" + id + " 已创建 / Ticket #" + id + " created"
	if err := recordTicketNoticeTx(tx, ticket, eventID, actorID, ticket.UserId, model.AdtyTelegramPurposeUser, "create", key, title,
		"工单已创建，可在网站查看。\n\nYour ticket has been created. Open it on the website."); err != nil {
		return err
	}
	return notifyTicketHandlersTx(tx, ticket, eventID, actorID, "create", key, title, "新工单待处理。\n\nA new ticket is waiting for support.")
}

func emitAdtyTicketReplyNoticesTx(tx *gorm.DB, ticket model.AdtyTicket, message model.AdtyTicketMessage, eventID int64, actorID int) error {
	id := strconv.FormatInt(ticket.Id, 10)
	key := "ticket:" + id + ":msg:" + strconv.FormatInt(message.Id, 10)
	title := "工单 #" + id + " 新回复 / Ticket #" + id + " new reply"
	body := "工单收到新回复，可在网站查看。\n\nYour ticket has a new reply. Open it on the website."
	if message.AuthorRole == "customer" {
		return notifyTicketHandlersTx(tx, ticket, eventID, actorID, "reply", key, title, body)
	}
	if ticket.UserId == actorID {
		return nil
	}
	return recordTicketNoticeTx(tx, ticket, eventID, actorID, ticket.UserId, model.AdtyTelegramPurposeUser, "reply", key, title, body)
}

func emitAdtyTicketStatusNoticesTx(tx *gorm.DB, ticket model.AdtyTicket, event model.AdtyTicketEvent, actorID int, nextStatus string, nextAssignee int) error {
	ticket.Status, ticket.AssigneeId = nextStatus, nextAssignee
	id := strconv.FormatInt(ticket.Id, 10)
	key := "ticket:" + id + ":event:" + strconv.FormatInt(event.Id, 10)
	title := "工单 #" + id + " 已更新 / Ticket #" + id + " updated"
	body := "工单处理进度已更新，可在网站查看。\n\nThe progress of your ticket has changed. Open it on the website."
	if ticket.UserId != actorID || ticket.TeamShared {
		if err := recordTicketNoticeTx(tx, ticket, event.Id, actorID, ticket.UserId, model.AdtyTelegramPurposeUser, event.Action, key, title, body); err != nil {
			return err
		}
	}
	return notifyTicketHandlersTx(tx, ticket, event.Id, actorID, event.Action, key, title, body)
}

// Retry only the indexed pending notices. Their recipient, purpose, content
// and timestamp were frozen in the original business transaction.
func SyncAdtyTicketEventNotices() (int, error) {
	if model.DB == nil {
		return 0, nil
	}
	var ids []int64
	if err := model.DB.Model(&model.AdtyTicketNotice{}).Where("telegram_pending = ?", true).Order("id").Limit(100).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	recovered := 0
	for _, id := range ids {
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			var row model.AdtyTicketNotice
			found := model.AdtyLockForUpdate(tx).Where("id=? AND telegram_pending=?", id, true).Limit(1).Find(&row)
			if found.Error != nil || found.RowsAffected == 0 {
				return found.Error
			}
			if err := queueAdtyTicketTelegramNoticeTx(tx, row.UserId, row.Purpose, row.Content, row.EventKey, row.CreatedAt); err != nil {
				return err
			}
			if err := tx.Model(&row).Update("telegram_pending", false).Error; err != nil {
				return err
			}
			recovered++
			return nil
		})
		if err != nil {
			return recovered, err
		}
	}
	return recovered, nil
}

// The site-close transaction freezes recipients while it moves the queue.
// Already-migrated cases keep any subsequent main-site assignment and status.
func transferSiteTicketsToMainTx(tx *gorm.DB, siteID int) error {
	var tickets []model.AdtyTicket
	pending := tx.Where("site_id=? AND (handling_site_id IS NULL OR handling_site_id=0)", siteID).
		Where("NOT EXISTS (SELECT 1 FROM adty_ticket_events e WHERE e.ticket_id=adty_tickets.id AND e.action='site_handoff')")
	if err := model.AdtyLockForUpdate(pending).Order("id").Find(&tickets).Error; err != nil {
		return err
	}
	for _, ticket := range tickets {
		prior := "site=" + strconv.Itoa(ticket.SiteId)
		if ticket.HandlingSiteId == nil {
			prior += " status=" + ticket.Status + " assignee=" + strconv.Itoa(ticket.AssigneeId)
			main := 0
			ticket.HandlingSiteId, ticket.AssigneeId = &main, 0
			if ticket.Status != model.AdtyTicketClosed && ticket.Status != model.AdtyTicketResolved {
				ticket.Status = model.AdtyTicketPendingAssignment
			}
			if err := tx.Model(&ticket).UpdateColumns(map[string]any{"handling_site_id": 0, "assignee_id": 0, "status": ticket.Status}).Error; err != nil {
				return err
			}
		}
		event := model.AdtyTicketEvent{TicketId: ticket.Id, Action: "site_handoff", Detail: prior}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		id := strconv.FormatInt(ticket.Id, 10)
		if err := notifyTicketHandlersTx(tx, ticket, event.Id, 0, event.Action, "ticket:"+id+":site_handoff",
			"工单 #"+id+" 已移交 / Ticket #"+id+" transferred", "分站已关闭，工单由主站承接。\n\nThe site has closed. Main-site support now handles this ticket."); err != nil {
			return err
		}
	}
	return nil
}

func backfillClosedSiteTicketHandoffs() error {
	var sites []int
	if err := model.DB.Model(&model.AdtySite{}).Where("status=?", model.AdtySiteStatusClosed).Pluck("id", &sites).Error; err != nil {
		return err
	}
	for _, id := range sites {
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Select("id").First(&site, id).Error; err != nil {
				return err
			}
			return transferSiteTicketsToMainTx(tx, id)
		}); err != nil {
			return err
		}
	}
	return nil
}

func startAdtyTicketNoticeSyncTask() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		repaired := false
		for {
			if !repaired {
				if err := backfillClosedSiteTicketHandoffs(); err != nil {
					common.SysError(common.LogText("ticket handoff retry: %s", err.Error()))
				} else {
					repaired = true
				}
			}
			if _, err := SyncAdtyTicketEventNotices(); err != nil {
				common.SysError(common.LogText("ticket notice delivery retry: %s", err.Error()))
			}
			<-ticker.C
		}
	}()
}
func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtyTicketNoticeSyncTask, startTicketTranslationWorker, StartAdtyPendingTicketAttachmentCleanupTask)
}

// ListAdtyTicketNoticesForUser returns persisted in-site ticket notices for the
// given user and site scope, newest first.
func ListAdtyTicketNoticesForUser(userId, siteId, limit int) ([]model.AdtyTicketNotice, error) {
	if userId <= 0 || model.DB == nil {
		return []model.AdtyTicketNotice{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	var rows []model.AdtyTicketNotice
	err := model.DB.Where("user_id = ? AND site_id = ?", userId, siteId).Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func ticketTranslationKey(messageID int64) string {
	return "ticket-reply:" + strconv.FormatInt(messageID, 10)
}

const adtyTicketMaxFileBytes = 16 << 20

func SaveAdtyTicketImagePolicy(policy model.AdtyTicketPolicy, audits ...model.AuditLog) error {
	if policy.MaxBytes < 1 || policy.MaxBytes > adtyTicketMaxFileBytes || policy.MaxCount < 1 || policy.MaxCount > 100 ||
		policy.MaxTotal < policy.MaxBytes || policy.MaxTotal > 256<<20 || policy.MaxSide < 1 || policy.MaxSide > 16000 ||
		policy.MaxPixels < 1 || policy.MaxPixels > 32000000 {
		return ErrAdtyTicketInvalid
	}
	policy.Id = 1
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var existing model.AdtyTicketPolicy
		found := model.AdtyLockForUpdate(tx).Where("id = ?", 1).Limit(1).Find(&existing)
		if found.Error != nil {
			return found.Error
		}
		before := map[string]any{
			"max_bytes":  existing.MaxBytes,
			"max_count":  existing.MaxCount,
			"max_total":  existing.MaxTotal,
			"max_side":   existing.MaxSide,
			"max_pixels": existing.MaxPixels,
		}
		after := map[string]any{
			"max_bytes":  policy.MaxBytes,
			"max_count":  policy.MaxCount,
			"max_total":  policy.MaxTotal,
			"max_side":   policy.MaxSide,
			"max_pixels": policy.MaxPixels,
		}
		if err := tx.Save(&policy).Error; err != nil {
			return err
		}
		if len(audits) > 0 && tx.Migrator().HasTable(&model.AuditIntent{}) {
			for _, event := range audits {
				event.BeforeValue = common.GetJsonString(before)
				event.AfterValue = common.GetJsonString(after)
				if err := model.AppendAuditIntentTx(tx, event); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func AdtyTicketImageMaxBytes() int { return adtyTicketPolicy().MaxBytes }

func adtyTicketPolicy() model.AdtyTicketPolicy {
	policy := model.AdtyTicketPolicy{MaxBytes: 2 << 20, MaxCount: 6, MaxTotal: 8 << 20, MaxSide: 8000, MaxPixels: 16_000_000}
	var row model.AdtyTicketPolicy
	if err := model.DB.Where("id = ?", 1).Limit(1).Find(&row).Error; err == nil {
		if row.MaxBytes > 0 {
			policy.MaxBytes = min(row.MaxBytes, adtyTicketMaxFileBytes)
		}
		if row.MaxCount > 0 {
			policy.MaxCount = row.MaxCount
		}
		if row.MaxTotal > 0 {
			policy.MaxTotal = row.MaxTotal
		}
		if row.MaxSide > 0 {
			policy.MaxSide = row.MaxSide
		}
		if row.MaxPixels > 0 {
			policy.MaxPixels = row.MaxPixels
		}
	}
	return policy
}

func sanitizeAdtyTicketImage(data []byte, policy model.AdtyTicketPolicy) ([]byte, string, int, int, error) {
	kind := http.DetectContentType(data)
	if bytes.HasPrefix(data, []byte("%PDF")) || bytes.HasPrefix(data, []byte("GIF8")) || strings.EqualFold(kind, "image/gif") || strings.Contains(kind, "pdf") || strings.Contains(kind, "zip") {
		return nil, "", 0, 0, ErrAdtyTicketImage
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > policy.MaxSide || config.Height > policy.MaxSide || config.Width*config.Height > policy.MaxPixels {
		return nil, "", 0, 0, ErrAdtyTicketImage
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", 0, 0, ErrAdtyTicketImage
	}
	var buf bytes.Buffer
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		if err := jpeg.Encode(&buf, decoded, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", 0, 0, ErrAdtyTicketImage
		}
		if buf.Len() > policy.MaxBytes {
			return nil, "", 0, 0, ErrAdtyTicketImageSize
		}
		return buf.Bytes(), "image/jpeg", config.Width, config.Height, nil
	case "png":
		if err := png.Encode(&buf, decoded); err != nil {
			return nil, "", 0, 0, ErrAdtyTicketImage
		}
		if buf.Len() > policy.MaxBytes {
			return nil, "", 0, 0, ErrAdtyTicketImageSize
		}
		return buf.Bytes(), "image/png", config.Width, config.Height, nil
	case "webp":
		// Go 只有 WebP 解码器、没有编码器，先尝试重编码为 PNG；若 PNG 超过单图上限，
		// 回退为 JPEG（Quality: 85），仍超限时明确返回尺寸超限错误。
		if err := png.Encode(&buf, decoded); err != nil {
			return nil, "", 0, 0, ErrAdtyTicketImage
		}
		if buf.Len() <= policy.MaxBytes {
			return buf.Bytes(), "image/png", config.Width, config.Height, nil
		}
		buf.Reset()
		if err := jpeg.Encode(&buf, decoded, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", 0, 0, ErrAdtyTicketImage
		}
		if buf.Len() > policy.MaxBytes {
			return nil, "", 0, 0, ErrAdtyTicketImageSize
		}
		return buf.Bytes(), "image/jpeg", config.Width, config.Height, nil
	default:
		return nil, "", 0, 0, ErrAdtyTicketImage
	}
}

func adtyTicketExt(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}
