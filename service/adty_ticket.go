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

type AdtyTicketDetail struct {
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
	Pending           int64 `json:"pending"`
}

type AdtyTicketList struct {
	Tickets   []model.AdtyTicket `json:"tickets"`
	CanNote   bool               `json:"can_note"`
	CanManage bool               `json:"can_manage"`
	Badges    AdtyTicketBadges   `json:"badges"`
}

type adtyTicketActor struct {
	User        model.User
	Staff       *model.AdtySiteStaff
	OwnerSiteId int
	TeamId      int64
	TeamRole    string
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
	actor, err := loadAdtyTicketActor(userId)
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
	actor, err := loadAdtyTicketActor(userId)
	if err != nil {
		return nil, err
	}
	scope := scopedAdtyTickets(actor)
	var rows []model.AdtyTicket
	query, err := applyAdtyTicketQueueFilter(scope.Session(&gorm.Session{}).Order("id DESC").Limit(100), queue)
	if err != nil {
		return nil, err
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Status = model.NormalizeAdtyTicketStatus(rows[i].Status, rows[i].AssigneeId)
	}
	badges, err := countAdtyTicketBadges(scope.Session(&gorm.Session{}))
	if err != nil {
		return nil, err
	}
	probe := model.AdtyTicket{SiteId: actor.scopeSite()}
	return &AdtyTicketList{Tickets: rows, CanNote: actor.canNote(probe), CanManage: actor.canManage(probe), Badges: badges}, nil
}

func scopedAdtyTickets(actor adtyTicketActor) *gorm.DB {
	query := model.DB.Model(&model.AdtyTicket{})
	switch {
	case actor.User.Role >= common.RoleRootUser:
	case actor.User.Role >= common.RoleAdminUser:
		query = query.Where("site_id = ?", 0)
	case actor.OwnerSiteId > 0:
		query = query.Where("site_id = ?", actor.OwnerSiteId)
	case actor.Staff != nil && actor.Staff.Status == "enabled":
		query = query.Where("site_id = ?", actor.Staff.SiteId)
	case actor.TeamRole == model.AdtyTeamRoleLeader && actor.TeamId > 0:
		query = query.Where("user_id = ? OR team_id = ?", actor.User.Id, actor.TeamId)
	default:
		query = query.Where("user_id = ?", actor.User.Id)
	}
	return query
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
	badges.Pending = badges.New + badges.PendingAssignment
	return badges, err
}

func GetAdtyTicket(userId int, ticketId int64) (*AdtyTicketDetail, error) {
	actor, err := loadAdtyTicketActor(userId)
	if err != nil {
		return nil, err
	}
	var ticket model.AdtyTicket
	if err := model.DB.First(&ticket, ticketId).Error; err != nil {
		return nil, err
	}
	if !actor.canRead(ticket) {
		return nil, ErrAdtyTicketForbidden
	}
	visibility := []string{model.AdtyTicketVisibilityPublic}
	if actor.canNote(ticket) {
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
	if err := model.DB.Where("ticket_id = ?", ticket.Id).Order("id ASC").Find(&events).Error; err != nil {
		return nil, err
	}
	ticket.Status = model.NormalizeAdtyTicketStatus(ticket.Status, ticket.AssigneeId)
	return &AdtyTicketDetail{Ticket: ticket, Messages: messages, Images: images, Events: events, CanNote: actor.canNote(ticket), CanManage: actor.canManage(ticket)}, nil
}

func ReplyAdtyTicket(userId, ticketId int64, body, idempotency string) (*model.AdtyTicketMessage, error) {
	return writeAdtyTicketMessage(int(userId), ticketId, body, idempotency, model.AdtyTicketVisibilityPublic)
}

func NoteAdtyTicket(userId int, ticketId int64, body, idempotency string) (*model.AdtyTicketMessage, error) {
	return writeAdtyTicketMessage(userId, ticketId, body, idempotency, model.AdtyTicketVisibilityNote)
}

func SetAdtyTicketStatus(userId int, ticketId int64, status, priority string, assigneeId int) error {
	if assigneeId < 0 {
		return ErrAdtyTicketInvalid
	}
	actor, err := loadAdtyTicketActor(userId)
	if err != nil {
		return err
	}
	if err := ensureAdtyTicketNoticeSchema(); err != nil {
		return err
	}
	var statusAudit map[string]any
	var statusBefore map[string]any
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var ticket model.AdtyTicket
		if err := model.AdtyLockForUpdate(tx).First(&ticket, ticketId).Error; err != nil {
			return err
		}
		if !actor.canRead(ticket) {
			return ErrAdtyTicketForbidden
		}
		current := model.NormalizeAdtyTicketStatus(ticket.Status, ticket.AssigneeId)
		nextAssignee := ticket.AssigneeId
		if assigneeId > 0 {
			if !actor.canManage(ticket) {
				return ErrAdtyTicketForbidden
			}
			if err := validateAdtyTicketAssigneeTx(tx, ticket, assigneeId); err != nil {
				return err
			}
			nextAssignee = assigneeId
		}
		nextPriority := ticket.Priority
		if priority != "" {
			if !actor.canManage(ticket) {
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
		if err := tx.Model(&ticket).Updates(updates).Error; err != nil {
			return err
		}
		statusBefore = map[string]any{
			"ticket_id": ticket.Id, "status": current, "priority": ticket.Priority, "assignee_id": ticket.AssigneeId,
			"site_id": ticket.SiteId, "user_id": ticket.UserId,
		}
		statusAudit = map[string]any{
			"ticket_id": ticket.Id, "status": next, "priority": nextPriority, "assignee_id": nextAssignee,
			"site_id": ticket.SiteId, "user_id": ticket.UserId, "event": action,
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
		if current == model.AdtyTicketClosed {
			return "", ErrAdtyTicketInvalid
		}
		return model.AdtyTicketResolved, nil
	case "reopen":
		if ticket.UserId != actor.User.Id && !actor.canManage(ticket) {
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
		if !actor.canManage(ticket) {
			return "", ErrAdtyTicketForbidden
		}
		return model.AdtyTicketClosed, nil
	case model.AdtyTicketWaitingUser, model.AdtyTicketWaitingInternal:
		if !actor.canManage(ticket) {
			return "", ErrAdtyTicketForbidden
		}
		if !canMoveAdtyTicketToWaiting(current) {
			return "", ErrAdtyTicketInvalid
		}
		return requested, nil
	case model.AdtyTicketInProgress:
		if !actor.canManage(ticket) {
			return "", ErrAdtyTicketForbidden
		}
		if !canAssignAdtyTicket(current) {
			return "", ErrAdtyTicketInvalid
		}
		return model.AdtyTicketInProgress, nil
	case model.AdtyTicketPendingAssignment:
		if !actor.canManage(ticket) {
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
func AddAdtyTicketImage(userId int, ticketId int64, visibility string, data []byte) (*model.AdtyTicketImage, error) {
	if visibility != model.AdtyTicketVisibilityPublic && visibility != model.AdtyTicketVisibilityNote {
		return nil, ErrAdtyTicketInvalid
	}
	actor, err := loadAdtyTicketActor(userId)
	if err != nil {
		return nil, err
	}
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
		var ticket model.AdtyTicket
		if err := model.AdtyLockForUpdate(tx).First(&ticket, ticketId).Error; err != nil {
			return err
		}
		if !actor.canRead(ticket) {
			return ErrAdtyTicketForbidden
		}
		if visibility == model.AdtyTicketVisibilityNote && !actor.canNote(ticket) {
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

func OpenAdtyTicketImage(userId int, ticketId, imageId int64) (string, string, error) {
	actor, err := loadAdtyTicketActor(userId)
	if err != nil {
		return "", "", err
	}
	var ticket model.AdtyTicket
	if err := model.DB.First(&ticket, ticketId).Error; err != nil {
		return "", "", err
	}
	if !actor.canRead(ticket) {
		return "", "", ErrAdtyTicketForbidden
	}
	var imageRow model.AdtyTicketImage
	if err := model.DB.Where("id = ? AND ticket_id = ?", imageId, ticket.Id).First(&imageRow).Error; err != nil {
		return "", "", err
	}
	// Same gate as the message list. Anything that is not explicitly public is
	// treated as internal, so a row that skipped the backfill still fails closed.
	if imageRow.Visibility != model.AdtyTicketVisibilityPublic && !actor.canNote(ticket) {
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

func writeAdtyTicketMessage(userId int, ticketId int64, body, idempotency, visibility string) (*model.AdtyTicketMessage, error) {
	body = strings.TrimSpace(redactAdtySecrets(body))
	if body == "" {
		return nil, ErrAdtyTicketInvalid
	}
	actor, err := loadAdtyTicketActor(userId)
	if err != nil {
		return nil, err
	}
	if err := ensureAdtyTicketNoticeSchema(); err != nil {
		return nil, err
	}
	var saved *model.AdtyTicketMessage
	createdNew := false
	var ticketSiteID int
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var ticket model.AdtyTicket
		if err := model.AdtyLockForUpdate(tx).First(&ticket, ticketId).Error; err != nil {
			return err
		}
		if !actor.canRead(ticket) {
			return ErrAdtyTicketForbidden
		}
		if visibility == model.AdtyTicketVisibilityNote && !actor.canNote(ticket) {
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
				if prior.Visibility != model.AdtyTicketVisibilityPublic && !actor.canNote(ticket) {
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

func validateAdtyTicketAssigneeTx(tx *gorm.DB, ticket model.AdtyTicket, assigneeId int) error {
	if assigneeId <= 0 {
		return ErrAdtyTicketInvalid
	}
	target, err := loadAdtyTicketActorTx(tx, assigneeId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAdtyTicketInvalid
		}
		return err
	}
	if target.User.Status != common.UserStatusEnabled {
		return ErrAdtyTicketInvalid
	}
	if !target.canManage(ticket) {
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
	var staff model.AdtySiteStaff
	lookup := tx.Where("user_id = ?", userId).Limit(1).Find(&staff)
	if lookup.Error != nil {
		return actor, lookup.Error
	}
	if lookup.RowsAffected > 0 {
		actor.Staff = &staff
	}
	var site model.AdtySite
	owned := tx.Where("owner_user_id = ?", userId).Limit(1).Find(&site)
	if owned.Error != nil {
		return actor, owned.Error
	}
	if owned.RowsAffected > 0 {
		actor.OwnerSiteId = site.Id
	}
	var member model.AdtyTeamMember
	team := tx.Where("user_id = ? AND status = ?", userId, model.AdtyTeamMemberActive).Limit(1).Find(&member)
	if team.Error != nil {
		return actor, team.Error
	}
	if team.RowsAffected > 0 {
		actor.TeamId = member.TeamId
		actor.TeamRole = member.Role
	}
	return actor, nil
}

func (actor adtyTicketActor) canRead(ticket model.AdtyTicket) bool {
	if actor.User.Role >= common.RoleRootUser {
		return true
	}
	if actor.User.Role >= common.RoleAdminUser && ticket.SiteId == 0 {
		return true
	}
	if actor.OwnerSiteId > 0 && actor.OwnerSiteId == ticket.SiteId {
		return true
	}
	if actor.Staff != nil && actor.Staff.Status == "enabled" && actor.Staff.SiteId == ticket.SiteId {
		return true
	}
	if ticket.UserId == actor.User.Id {
		return true
	}
	return actor.TeamRole == model.AdtyTeamRoleLeader && actor.TeamId > 0 && actor.TeamId == ticket.TeamId
}

func (actor adtyTicketActor) canNote(ticket model.AdtyTicket) bool {
	if ticket.UserId == actor.User.Id && actor.User.Role < common.RoleAdminUser && (actor.Staff == nil || actor.Staff.Status != "enabled") && actor.OwnerSiteId != ticket.SiteId {
		return false
	}
	return actor.canManage(ticket)
}

func (actor adtyTicketActor) canManage(ticket model.AdtyTicket) bool {
	if actor.User.Role >= common.RoleRootUser {
		return true
	}
	if actor.User.Role >= common.RoleAdminUser && ticket.SiteId == 0 {
		return true
	}
	if actor.OwnerSiteId > 0 && actor.OwnerSiteId == ticket.SiteId {
		return true
	}
	return actor.Staff != nil && actor.Staff.Status == "enabled" && actor.Staff.SiteId == ticket.SiteId
}

func (actor adtyTicketActor) scopeSite() int {
	if actor.OwnerSiteId > 0 {
		return actor.OwnerSiteId
	}
	if actor.Staff != nil {
		return actor.Staff.SiteId
	}
	return actor.User.SiteId
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
