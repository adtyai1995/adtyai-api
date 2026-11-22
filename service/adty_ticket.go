package service

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	adtyTicketImageDir     = func() string { return filepath.Join("uploads", "adty-ticket-images") }
)

type AdtyTicketInput struct {
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
	Assignees []AdtyTicketAssignee      `json:"assignees,omitempty"`
	Ticket    model.AdtyTicket          `json:"ticket"`
	Messages  []model.AdtyTicketMessage `json:"messages"`
	Images    []model.AdtyTicketImage   `json:"images"`
	Events    []model.AdtyTicketEvent   `json:"events"`
	CanNote   bool                      `json:"can_note"`
	CanManage bool                      `json:"can_manage"`
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
	Total       int64              `json:"total"`
	Page        int                `json:"page"`
	PageSize    int                `json:"page_size"`
	CanViewTeam bool               `json:"can_view_team"`
	Tickets     []model.AdtyTicket `json:"tickets"`
	CanNote     bool               `json:"can_note"`
	CanManage   bool               `json:"can_manage"`
	Badges      AdtyTicketBadges   `json:"badges"`
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
	if userId <= 0 || input.Title == "" || input.Body == "" || len(input.Title) > 120 {
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
	actor, err := loadAdtyTicketWorkspace(model.DB, userId, "personal")
	if err != nil {
		return nil, err
	}
	if err := ensureAdtyTicketNoticeSchema(); err != nil {
		return nil, err
	}
	var created *model.AdtyTicket
	createdNew := false
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		dedupe := ticketCreateDedupe(actor.User.SiteId, actor.User.Id, input.Idempotency)
		if strings.TrimSpace(input.Idempotency) != "" {
			var prior model.AdtyTicketMessage
			lookup := tx.Where("dedupe = ?", dedupe).Limit(1).Find(&prior)
			if lookup.Error != nil {
				return lookup.Error
			}
			if lookup.RowsAffected > 0 {
				var ticket model.AdtyTicket
				if err := tx.First(&ticket, prior.TicketId).Error; err != nil {
					return err
				}
				if prior.AuthorId != actor.User.Id || ticket.UserId != actor.User.Id || ticket.SiteId != actor.User.SiteId || !actor.canRead(ticket) {
					return ErrAdtyTicketForbidden
				}
				ticket.Status = model.NormalizeAdtyTicketStatus(ticket.Status, ticket.AssigneeId)
				created = &ticket
				return nil
			}
		}
		ticket := model.AdtyTicket{
			SiteId: actor.User.SiteId, UserId: actor.User.Id, TeamId: actor.TeamId, Type: input.Type,
			Title: input.Title, Priority: priority, Status: model.AdtyTicketNew,
			RelatedKind: input.RelatedKind, RelatedRef: input.RelatedRef, RelatedModel: input.RelatedModel,
			RelatedGroup: input.RelatedGroup, RelatedError: input.RelatedError,
		}
		if err := tx.Create(&ticket).Error; err != nil {
			return err
		}
		message := model.AdtyTicketMessage{
			TicketId: ticket.Id, AuthorId: actor.User.Id, Visibility: model.AdtyTicketVisibilityPublic,
			Body: input.Body, Dedupe: dedupe,
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
		created = &ticket
		createdNew = true
		return nil
	})
	if err == nil && createdNew && created != nil {
		writeAdtyOfficialAudit(userId, created.SiteId, "ticket", strconv.FormatInt(created.Id, 10), "adty.ticket.create", "", "",
			nil,
			map[string]any{
				"ticket_id": created.Id, "site_id": created.SiteId, "user_id": created.UserId,
				"team_id": created.TeamId, "type": created.Type, "title": created.Title,
				"priority": created.Priority, "status": created.Status, "related_kind": created.RelatedKind,
			},
			model.AuditResultSuccess)
	}
	return created, err
}

func ListAdtyTickets(userId int, queue string) (*AdtyTicketList, error) {
	return ListAdtyTicketPage(userId, queue, 1, 100)
}

func ListAdtyTicketPage(userId int, queue string, page, pageSize int, workspace ...string) (*AdtyTicketList, error) {
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
	return &AdtyTicketList{Tickets: rows, Total: total, Page: page, PageSize: pageSize, CanViewTeam: actor.TeamRole == model.AdtyTeamRoleLeader, CanNote: manages, CanManage: manages, Badges: badges}, nil
}

// Every operation resolves the requested workspace before applying this same
// row filter. A handler visiting the account page sees only their public case.
func scopedAdtyTickets(tx *gorm.DB, actor adtyTicketActor) *gorm.DB {
	query := tx.Model(&model.AdtyTicket{})
	switch actor.Scope {
	case "platform_main":
		return query.Where("site_id = ?", 0)
	case "platform":
		if actor.PlatformAll {
			return query
		}
		return query.Where("site_id = ?", 0)
	case "owner":
		return query.Where("site_id = ?", actor.OwnerSiteId)
	case "staff":
		return query.Where("site_id = ?", actor.Staff.SiteId)
	case "participant":
		return query.Where("user_id = ? OR (team_id = ? AND site_id = ?)", actor.User.Id, actor.TeamId, actor.User.SiteId)
	case "team":
		return query.Where("site_id = ? AND team_id = ?", actor.User.SiteId, actor.TeamId)
	default:
		return query.Where("user_id = ?", actor.User.Id)
	}
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
	var images []model.AdtyTicketImage
	if err := model.DB.Select("id", "object_id", "ticket_id", "message_id", "visibility", "mime", "size", "width", "height", "sha256", "created_at").Where("ticket_id = ? AND visibility IN ?", ticket.Id, visibility).Order("id ASC").Find(&images).Error; err != nil {
		return nil, err
	}
	var events []model.AdtyTicketEvent
	eventQuery := model.DB.Where("ticket_id = ?", ticket.Id)
	if !actor.isHandling() {
		eventQuery = eventQuery.Where("action IN ?", []string{"create", "reply", "status", "reopen", "priority", "assign", "transfer"})
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
	return &AdtyTicketDetail{Assignees: assignees, Ticket: ticket, Messages: messages, Images: images, Events: events, CanNote: actor.isHandling(), CanManage: actor.isHandling()}, nil
}

func ReplyAdtyTicket(userId, ticketId int64, body, idempotency string, workspace ...string) (*model.AdtyTicketMessage, error) {
	return writeAdtyTicketMessage(int(userId), ticketId, body, idempotency, model.AdtyTicketVisibilityPublic, workspace...)
}

func NoteAdtyTicket(userId int, ticketId int64, body, idempotency string, workspace ...string) (*model.AdtyTicketMessage, error) {
	return writeAdtyTicketMessage(userId, ticketId, body, idempotency, model.AdtyTicketVisibilityNote, workspace...)
}

func SetAdtyTicketStatus(userId int, ticketId int64, status, priority string, assigneeId int, workspace ...string) error {
	if assigneeId < 0 {
		return ErrAdtyTicketInvalid
	}
	var err error
	if err := ensureAdtyTicketNoticeSchema(); err != nil {
		return err
	}
	var statusAudit map[string]any
	var statusBefore map[string]any
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		actor, err := loadAdtyTicketWorkspace(tx, userId, workspace...)
		if err != nil {
			return err
		}
		var ticket model.AdtyTicket
		if err := scopedAdtyTickets(model.AdtyLockForUpdate(tx), actor).First(&ticket, ticketId).Error; err != nil {
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
		eventRow := model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: actor.User.Id, Action: eventAction, Detail: eventDetail}
		if err := tx.Create(&eventRow).Error; err != nil {
			return err
		}
		return emitAdtyTicketStatusNoticesTx(tx, ticket, eventRow, actor.User.Id, next, nextAssignee)
	})
	if err == nil && statusAudit != nil {
		siteID := 0
		if value, ok := statusAudit["site_id"].(int); ok {
			siteID = value
		}
		writeAdtyOfficialAudit(userId, siteID, "ticket", strconv.FormatInt(ticketId, 10), "adty.ticket.status", "", "",
			statusBefore, statusAudit, model.AuditResultSuccess)
	}
	return err
}

func nextAdtyTicketStatus(tx *gorm.DB, actor adtyTicketActor, ticket model.AdtyTicket, current, requested string, nextAssignee int) (string, error) {
	landing := reopenAdtyTicketLanding(nextAssignee)
	switch requested {
	case "resolve":
		if actor.User.Id != ticket.UserId && !actor.isHandling() {
			return "", ErrAdtyTicketForbidden
		}
		if current == model.AdtyTicketClosed {
			return "", ErrAdtyTicketInvalid
		}
		return model.AdtyTicketResolved, nil
	case "reopen":
		if ticket.UserId != actor.User.Id && !actor.isHandling() {
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
	lookup := tx.Where("ticket_id = ?", ticket.Id).Order("id DESC").Limit(1).Find(&event)
	return lookup.Error == nil && lookup.RowsAffected > 0 && event.Action == "reopen"
}

// AddAdtyTicketImage stores an attachment under the same visibility as the
// message it belongs to. The caller must declare visibility: there is no
// default, because a default would read as "optional" and silently decide a
// security question. Internal images require note permission.
func AddAdtyTicketImage(userId int, ticketId int64, visibility string, data []byte, workspace ...string) (*model.AdtyTicketImage, error) {
	if visibility != model.AdtyTicketVisibilityPublic && visibility != model.AdtyTicketVisibilityNote {
		return nil, ErrAdtyTicketInvalid
	}
	var err error
	policy := adtyTicketPolicy()
	var stripped []byte
	var mime string
	var width, height int
	var sanitizeErr error = ErrAdtyTicketImage
	if len(data) > 0 && len(data) <= policy.MaxBytes {
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
		if sanitizeErr != nil {
			return sanitizeErr
		}
		var count int64
		var total int64
		if err := tx.Model(&model.AdtyTicketImage{}).Where("ticket_id = ?", ticket.Id).Count(&count).Error; err != nil {
			return err
		}
		if int(count) >= policy.MaxCount {
			return ErrAdtyTicketImage
		}
		if err := tx.Model(&model.AdtyTicketImage{}).Where("ticket_id = ?", ticket.Id).Select("COALESCE(SUM(size),0)").Scan(&total).Error; err != nil {
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
			ObjectId: objectID, TicketId: ticket.Id, Visibility: visibility, Mime: mime,
			Size: len(stripped), Width: width, Height: height, SHA256: hex.EncodeToString(sum[:]), Storage: name,
		}
		if err := tx.Create(&row).Error; err != nil {
			_ = os.Remove(filePath)
			return err
		}
		if err := tx.Create(&model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: actor.User.Id, Action: "image_upload", Detail: mime}).Error; err != nil {
			_ = os.Remove(filePath)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func OpenAdtyTicketImage(userId int, ticketId, imageId int64, workspace ...string) (string, string, error) {
	actor, err := loadAdtyTicketWorkspace(model.DB, userId, workspace...)
	if err != nil {
		return "", "", err
	}
	var ticket model.AdtyTicket
	if err := scopedAdtyTickets(model.DB, actor).First(&ticket, ticketId).Error; err != nil {
		return "", "", err
	}
	var imageRow model.AdtyTicketImage
	if err := model.DB.Where("id = ? AND ticket_id = ?", imageId, ticket.Id).First(&imageRow).Error; err != nil {
		return "", "", err
	}
	// Same gate as the message list. Anything that is not explicitly public is
	// treated as internal, so a row that skipped the backfill still fails closed.
	if imageRow.Visibility != model.AdtyTicketVisibilityPublic && !actor.isHandling() {
		return "", "", ErrAdtyTicketForbidden
	}
	path, err := adtyTicketImageStoragePath(imageRow)
	if err != nil {
		return "", "", os.ErrNotExist
	}
	if err := model.DB.Create(&model.AdtyTicketEvent{TicketId: ticket.Id, ActorId: actor.User.Id, Action: "image_view", Detail: strconv.FormatInt(imageRow.Id, 10)}).Error; err != nil {
		return "", "", err
	}
	return path, imageRow.Mime, nil
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
		row := model.AdtyTicketMessage{TicketId: ticket.Id, AuthorId: actor.User.Id, Visibility: visibility, Body: body, Dedupe: dedupe}
		if err := tx.Create(&row).Error; err != nil {
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
		// 推送按「回复者的对端」分流，作者本人永不进队列。
		//
		// 对端以 actor.User.Id 对 ticket.UserId 判定，不看角色——作者是客户就通知
		// 处理人，其他人回帖（客服、站长、团队长）一律通知客户。与消息和事件处于
		// 同一事务，避免消息已提交而通知出箱落库失败被吞掉。
		if visibility == model.AdtyTicketVisibilityPublic {
			if err := emitAdtyTicketReplyNoticesTx(tx, ticket, row, eventRow.Id, actor.User.Id); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && createdNew && saved != nil && visibility == model.AdtyTicketVisibilityPublic {
		writeAdtyOfficialAudit(userId, ticketSiteID, "ticket", strconv.FormatInt(ticketId, 10), "adty.ticket.reply", "", "",
			nil,
			map[string]any{
				"ticket_id": ticketId, "message_id": saved.Id, "author_id": saved.AuthorId,
				"visibility": saved.Visibility, "site_id": ticketSiteID,
			},
			model.AuditResultSuccess)
	}
	return saved, err
}

// adtyTicketHandlerTx 回答「客户回帖该通知谁」：优先处理人（需仍在工单作用域内有效），未分配则本站站长。
//
// 主站工单（site_id=0）在未分配时没有单一的处理人，这里返回 0 而不是挑一个——
// 猜错人会变成骚扰，少一条提醒只是漏，两者代价不对等。
func adtyTicketHandlerTx(tx *gorm.DB, ticket model.AdtyTicket) int {
	if ticket.AssigneeId > 0 {
		if validateAdtyTicketAssigneeTx(tx, ticket, ticket.AssigneeId) == nil {
			return ticket.AssigneeId
		}
	}
	if ticket.SiteId <= 0 {
		return 0
	}
	var site model.AdtySite
	if err := tx.Where("id = ?", ticket.SiteId).Limit(1).Find(&site).Error; err != nil {
		return 0
	}
	if site.OwnerUserId > 0 && validateAdtyTicketAssigneeTx(tx, ticket, site.OwnerUserId) == nil {
		return site.OwnerUserId
	}
	return 0
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
	if ticket.SiteId > 0 {
		var site model.AdtySite
		if err := tx.Where("id = ? AND status IN ? AND agreement_ver = ?", ticket.SiteId, []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}, AdtyCurrentSiteAgreementVersion()).Limit(1).Find(&site).Error; err != nil {
			return nil, err
		}
		if site.Id > 0 {
			ownerID = site.OwnerUserId
			q := tx.Where("site_id = ? AND owner_user_id = ? AND status = ?", ticket.SiteId, ownerID, "enabled")
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
	if includePlatform && slices.ContainsFunc(users, func(user model.User) bool { return user.Role == common.RoleAdminUser }) {
		var err error
		platformCan, err = authz.PermissionCheckerInTx(tx.Where("v1 = ?", authz.ResourceAdtyTicket))
		if err != nil {
			return nil, err
		}
	}
	result := []AdtyTicketAssignee{}
	for _, user := range users {
		local := user.Id == ownerID || (user.Role == common.RoleCommonUser && ticket.SiteId > 0 && user.StaffSiteId == ticket.SiteId && user.SiteId == ticket.SiteId)
		platform := includePlatform && user.Role >= common.RoleRootUser
		if platformCan != nil && user.Role == common.RoleAdminUser {
			platform = platformCan(user.Id, user.Role, authz.AdtyTicketManageAll) || (ticket.SiteId == 0 && platformCan(user.Id, user.Role, authz.AdtyTicketManage))
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
		} else {
			check, err := authz.PermissionCheckerInTx(tx.Where("v1 = ?", authz.ResourceAdtyTicket))
			if err != nil {
				return actor, err
			}
			actor.PlatformAll = check(userId, actor.User.Role, authz.AdtyTicketManageAll)
			actor.PlatformMain = actor.PlatformAll || check(userId, actor.User.Role, authz.AdtyTicketManage)
		}
	}
	var staff model.AdtySiteStaff
	lookup := tx.Where("user_id = ?", userId).Limit(1).Find(&staff)
	if lookup.Error != nil {
		return actor, lookup.Error
	}
	if lookup.RowsAffected > 0 {
		actor.Staff = &staff
		if staff.SiteId > 0 && actor.User.StaffSiteId == staff.SiteId && actor.User.SiteId == staff.SiteId && actor.User.Role == common.RoleCommonUser && staff.Status == "enabled" && slices.Contains(AdtyStaffViewOf(staff).Permissions, AdtyStaffTicketManage) {
			var n int64
			err := tx.Model(&model.AdtySite{}).Where("id = ? AND owner_user_id = ? AND status IN ? AND agreement_ver = ?", staff.SiteId, staff.OwnerUserId, []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}, AdtyCurrentSiteAgreementVersion()).Count(&n).Error
			if err != nil {
				return actor, err
			}
			actor.StaffTickets = n == 1
		}
	}
	var site model.AdtySite
	owned := tx.Where("owner_user_id = ? AND status IN ?", userId, []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}).Limit(1).Find(&site)
	if owned.Error != nil {
		return actor, owned.Error
	}
	if owned.RowsAffected > 0 {
		actor.OwnerSiteId = site.Id
		actor.OwnerTickets = site.AgreementVer == AdtyCurrentSiteAgreementVersion()
	}
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
		permitted = actor.Staff == nil && actor.TeamId > 0 && actor.TeamRole == model.AdtyTeamRoleLeader
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
	return actor.PlatformAll || (actor.PlatformMain && ticket.SiteId == 0) ||
		(actor.OwnerTickets && actor.OwnerSiteId == ticket.SiteId) || (actor.StaffTickets && actor.Staff.SiteId == ticket.SiteId)
}

func (actor adtyTicketActor) canRead(ticket model.AdtyTicket) bool {
	if actor.Staff != nil || actor.User.StaffSiteId > 0 {
		return actor.canManage(ticket)
	}
	return ticket.UserId == actor.User.Id || actor.canManage(ticket) ||
		(actor.TeamRole == model.AdtyTeamRoleLeader && actor.TeamId > 0 && actor.TeamId == ticket.TeamId && actor.User.SiteId == ticket.SiteId)
}

func ticketCreateDedupe(siteId, userId int, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ticketDedupe(0, "")
	}
	prefix := "create:" + strconv.Itoa(siteId) + ":" + strconv.Itoa(userId) + ":"
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

func queueAdtyTicketTelegramNoticeTx(tx *gorm.DB, userId int, purpose, body, eventKey string) error {
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
		EventKey: eventKey, UserId: userId, Purpose: purpose, Body: body, Status: model.AdtyTelegramDeliverySkip,
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

func emitAdtyTicketCreateNoticesTx(tx *gorm.DB, ticket model.AdtyTicket, eventId int64, actorUserId int) error {
	if ticket.UserId > 0 {
		userKey := "ticket:" + strconv.FormatInt(ticket.Id, 10) + ":create:user"
		title := "工单 #" + strconv.FormatInt(ticket.Id, 10) + " 已创建 / Ticket #" + strconv.FormatInt(ticket.Id, 10) + " created"
		body := "工单 " + strconv.FormatInt(ticket.Id, 10) + " 已创建。请在网站查看。\n\nTicket " + strconv.FormatInt(ticket.Id, 10) + " has been created. Open the website."
		if err := saveAdtyTicketNoticeTx(tx, model.AdtyTicketNotice{
			EventKey: userKey, TicketId: ticket.Id, EventId: eventId, SiteId: ticket.SiteId,
			UserId: ticket.UserId, ActorId: actorUserId, Action: "create", Title: title, Content: body,
		}); err != nil {
			return err
		}
		if err := queueAdtyTicketTelegramNoticeTx(tx, ticket.UserId, model.AdtyTelegramPurposeUser, body, userKey); err != nil {
			return err
		}
	}
	handlerId := adtyTicketHandlerTx(tx, ticket)
	if handlerId > 0 && handlerId != actorUserId {
		adminKey := "ticket:" + strconv.FormatInt(ticket.Id, 10) + ":create"
		title := "新工单 #" + strconv.FormatInt(ticket.Id, 10) + " / New ticket #" + strconv.FormatInt(ticket.Id, 10)
		body := "工单 " + strconv.FormatInt(ticket.Id, 10) + " 待处理。请在网站查看。\n\nTicket " + strconv.FormatInt(ticket.Id, 10) + " is waiting to be handled. Open the website."
		if err := saveAdtyTicketNoticeTx(tx, model.AdtyTicketNotice{
			EventKey: adminKey, TicketId: ticket.Id, EventId: eventId, SiteId: ticket.SiteId,
			UserId: handlerId, ActorId: actorUserId, Action: "create", Title: title, Content: body,
		}); err != nil {
			return err
		}
		if err := queueAdtyTicketTelegramNoticeTx(tx, handlerId, model.AdtyTelegramPurposeAdmin, body, adminKey); err != nil {
			return err
		}
	}
	return nil
}

func emitAdtyTicketReplyNoticesTx(tx *gorm.DB, ticket model.AdtyTicket, message model.AdtyTicketMessage, eventId int64, actorUserId int) error {
	notifyUser := 0
	notifyPurpose := ""
	if actorUserId == ticket.UserId {
		notifyUser = adtyTicketHandlerTx(tx, ticket)
		notifyPurpose = model.AdtyTelegramPurposeAdmin
	} else {
		notifyUser = ticket.UserId
		notifyPurpose = model.AdtyTelegramPurposeUser
	}
	if notifyUser == actorUserId {
		notifyUser = 0
	}
	if notifyUser <= 0 || notifyPurpose == "" {
		return nil
	}
	eventKey := "ticket:" + strconv.FormatInt(ticket.Id, 10) + ":msg:" + strconv.FormatInt(message.Id, 10)
	title := "工单 #" + strconv.FormatInt(ticket.Id, 10) + " 新回复 / Ticket #" + strconv.FormatInt(ticket.Id, 10) + " new reply"
	body := "工单 " + strconv.FormatInt(ticket.Id, 10) + " 有新回复。请在网站查看。\n\nTicket " + strconv.FormatInt(ticket.Id, 10) + " has a new reply. Open the website."
	if err := saveAdtyTicketNoticeTx(tx, model.AdtyTicketNotice{
		EventKey: eventKey, TicketId: ticket.Id, EventId: eventId, SiteId: ticket.SiteId,
		UserId: notifyUser, ActorId: actorUserId, Action: "reply", Title: title, Content: body,
	}); err != nil {
		return err
	}
	return queueAdtyTicketTelegramNoticeTx(tx, notifyUser, notifyPurpose, body, eventKey)
}

func emitAdtyTicketStatusNoticesTx(tx *gorm.DB, ticket model.AdtyTicket, event model.AdtyTicketEvent, actorUserId int, nextStatus string, nextAssignee int) error {
	updated := ticket
	if nextStatus != "" {
		updated.Status = nextStatus
	}
	if nextAssignee > 0 {
		updated.AssigneeId = nextAssignee
	}
	detail := event.Detail
	if detail == "" {
		detail = updated.Status
	}
	title := "工单 #" + strconv.FormatInt(ticket.Id, 10) + " 状态更新 / Ticket #" + strconv.FormatInt(ticket.Id, 10) + " status updated"
	body := "工单 " + strconv.FormatInt(ticket.Id, 10) + " 状态已更新为 " + detail + "。请在网站查看。\n\nTicket " + strconv.FormatInt(ticket.Id, 10) + " status updated to " + detail + ". Open the website."
	baseKey := "ticket:" + strconv.FormatInt(ticket.Id, 10) + ":event:" + strconv.FormatInt(event.Id, 10)
	notifiedCustomer := false
	if ticket.UserId > 0 && ticket.UserId != actorUserId {
		notifiedCustomer = true
		if err := saveAdtyTicketNoticeTx(tx, model.AdtyTicketNotice{
			EventKey: baseKey, TicketId: ticket.Id, EventId: event.Id, SiteId: ticket.SiteId,
			UserId: ticket.UserId, ActorId: actorUserId, Action: event.Action, Title: title, Content: body,
		}); err != nil {
			return err
		}
		if err := queueAdtyTicketTelegramNoticeTx(tx, ticket.UserId, model.AdtyTelegramPurposeUser, body, baseKey); err != nil {
			return err
		}
	}
	handlerId := adtyTicketHandlerTx(tx, updated)
	if handlerId > 0 && handlerId != actorUserId && handlerId != ticket.UserId {
		adminKey := baseKey
		if notifiedCustomer {
			adminKey = baseKey + ":admin"
		}
		if err := saveAdtyTicketNoticeTx(tx, model.AdtyTicketNotice{
			EventKey: adminKey, TicketId: ticket.Id, EventId: event.Id, SiteId: ticket.SiteId,
			UserId: handlerId, ActorId: actorUserId, Action: event.Action, Title: title, Content: body,
		}); err != nil {
			return err
		}
		if err := queueAdtyTicketTelegramNoticeTx(tx, handlerId, model.AdtyTelegramPurposeAdmin, body, adminKey); err != nil {
			return err
		}
	}
	return nil
}

// SyncAdtyTicketEventNotices scans persisted ticket events and idempotently
// recovers any missing in-site ticket notices or retriable Telegram outbox rows.
func SyncAdtyTicketEventNotices() (int, error) {
	if model.DB == nil {
		return 0, nil
	}
	if err := ensureAdtyTicketNoticeSchema(); err != nil {
		return 0, err
	}
	var events []model.AdtyTicketEvent
	if err := model.DB.Where("action IN ?", []string{"create", "reply", "status", "reopen", "priority"}).Order("id ASC").Find(&events).Error; err != nil {
		return 0, err
	}
	var beforeNotices, beforeDeliveries int64
	_ = model.DB.Model(&model.AdtyTicketNotice{}).Count(&beforeNotices).Error
	_ = model.DB.Model(&model.AdtyTelegramDelivery{}).Count(&beforeDeliveries).Error

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		tickets := map[int64]model.AdtyTicket{}
		repliedTickets := map[int64]bool{}
		for _, event := range events {
			ticket, ok := tickets[event.TicketId]
			if !ok {
				if err := tx.First(&ticket, event.TicketId).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						continue
					}
					return err
				}
				tickets[event.TicketId] = ticket
			}
			switch event.Action {
			case "create":
				if err := emitAdtyTicketCreateNoticesTx(tx, ticket, event.Id, event.ActorId); err != nil {
					return err
				}
			case "reply":
				if repliedTickets[ticket.Id] {
					continue
				}
				repliedTickets[ticket.Id] = true
				var messages []model.AdtyTicketMessage
				if err := tx.Where("ticket_id = ? AND visibility = ?", ticket.Id, model.AdtyTicketVisibilityPublic).Order("id ASC").Find(&messages).Error; err != nil {
					return err
				}
				for i, msg := range messages {
					if i == 0 && strings.HasPrefix(msg.Dedupe, "create:") {
						continue
					}
					if i == 0 && len(messages) >= 1 && msg.AuthorId == ticket.UserId && event.ActorId != msg.AuthorId {
						continue
					}
					if i == 0 {
						// First public message created with CreateAdtyTicket has its own create event.
						var createCount int64
						_ = tx.Model(&model.AdtyTicketEvent{}).Where("ticket_id = ? AND action = ? AND id <= ?", ticket.Id, "create", event.Id).Count(&createCount).Error
						if createCount > 0 {
							continue
						}
					}
					if err := emitAdtyTicketReplyNoticesTx(tx, ticket, msg, event.Id, msg.AuthorId); err != nil {
						return err
					}
				}
			case "status", "reopen", "priority":
				if err := emitAdtyTicketStatusNoticesTx(tx, ticket, event, event.ActorId, event.Detail, ticket.AssigneeId); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	var afterNotices, afterDeliveries int64
	_ = model.DB.Model(&model.AdtyTicketNotice{}).Count(&afterNotices).Error
	_ = model.DB.Model(&model.AdtyTelegramDelivery{}).Count(&afterDeliveries).Error
	recovered := int((afterNotices - beforeNotices) + (afterDeliveries - beforeDeliveries))
	if recovered < 0 {
		recovered = 0
	}
	return recovered, nil
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

func startAdtyTicketNoticeSyncTask() {
	if _, err := SyncAdtyTicketEventNotices(); err != nil {
		common.SysError(common.LogText("ticket notice sync on startup: %s", err.Error()))
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := SyncAdtyTicketEventNotices(); err != nil {
				common.SysError(common.LogText("ticket notice sync task: %s", err.Error()))
			}
		}
	}()
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtyTicketNoticeSyncTask)
}

func adtyTicketPolicy() model.AdtyTicketPolicy {
	policy := model.AdtyTicketPolicy{MaxBytes: 2 << 20, MaxCount: 6, MaxTotal: 8 << 20, MaxSide: 8000, MaxPixels: 16_000_000}
	var row model.AdtyTicketPolicy
	if err := model.DB.First(&row, 1).Error; err == nil {
		if row.MaxBytes > 0 {
			policy.MaxBytes = row.MaxBytes
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
	if bytes.HasPrefix(data, []byte("%PDF")) || strings.Contains(kind, "pdf") || strings.Contains(kind, "zip") {
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
		return buf.Bytes(), "image/jpeg", config.Width, config.Height, nil
	case "png":
		if err := png.Encode(&buf, decoded); err != nil {
			return nil, "", 0, 0, ErrAdtyTicketImage
		}
		return buf.Bytes(), "image/png", config.Width, config.Height, nil
	case "gif":
		if err := gif.Encode(&buf, decoded, nil); err != nil {
			return nil, "", 0, 0, ErrAdtyTicketImage
		}
		return buf.Bytes(), "image/gif", config.Width, config.Height, nil
	case "webp":
		// Go 只有 WebP 解码器、没有编码器，所以这里存成 PNG。
		// 重编码必然丢掉所有 chunk，元数据清理因此和 JPEG/PNG 一样由解码-编码完成，
		// 不再是「扫一下 EXIF 字样就原样返回」那种黑名单检查——换块名或大小写就能绕过。
		if err := png.Encode(&buf, decoded); err != nil {
			return nil, "", 0, 0, ErrAdtyTicketImage
		}
		return buf.Bytes(), "image/png", config.Width, config.Height, nil
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
