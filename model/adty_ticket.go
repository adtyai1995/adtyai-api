package model

import "gorm.io/gorm"

const (
	AdtyTicketNew               = "new"
	AdtyTicketPendingAssignment = "pending_assignment"
	AdtyTicketInProgress        = "in_progress"
	AdtyTicketWaitingUser       = "waiting_user"
	AdtyTicketWaitingInternal   = "waiting_internal"
	AdtyTicketResolved          = "resolved"
	AdtyTicketClosed            = "closed"
	// AdtyTicketLegacyOpen and AdtyTicketLegacyAssigned are historical stored
	// values. RemapAdtyTicketLegacyStatuses and NormalizeAdtyTicketStatus map
	// them into the official states: assigned → in_progress; unassigned open →
	// new; open with an assignee → in_progress.
	AdtyTicketLegacyOpen       = "open"
	AdtyTicketLegacyAssigned   = "assigned"
	AdtyTicketVisibilityPublic = "public"
	AdtyTicketVisibilityNote   = "internal"
	AdtyTicketPriorityNormal   = "normal"
	AdtyTicketPriorityHigh     = "important"
	AdtyTicketPriorityUrgent   = "urgent"
)

// AdtyTicket is one support case. Related fields are investigation hints, never a full key or upstream secret.
type AdtyTicket struct {
	Id             int64  `json:"id"`
	SiteId         int    `json:"site_id" gorm:"index"`
	HandlingSiteId *int   `json:"handling_site_id" gorm:"index"`
	UserId         int    `json:"user_id" gorm:"index"`
	ContactId      int    `json:"-"`
	RelatedAt      int64  `json:"related_at,omitempty"`
	TeamShared     bool   `json:"team_shared" gorm:"default:false"`
	TeamId         int64  `json:"team_id" gorm:"index"`
	Type           string `json:"type" gorm:"type:varchar(32)"`
	Title          string `json:"title" gorm:"type:varchar(120)"`
	Priority       string `json:"priority" gorm:"type:varchar(16);index"`
	Status         string `json:"status" gorm:"type:varchar(24);index"`
	AssigneeId     int    `json:"assignee_id" gorm:"index"`
	RelatedKind    string `json:"related_kind" gorm:"type:varchar(32)"`
	RelatedRef     string `json:"related_ref" gorm:"type:varchar(64)"`
	RelatedModel   string `json:"related_model" gorm:"type:varchar(80)"`
	RelatedGroup   string `json:"related_group" gorm:"type:varchar(64)"`
	RelatedError   string `json:"related_error" gorm:"type:varchar(64)"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (t AdtyTicket) HandlingSite() int {
	if t.HandlingSiteId != nil {
		return *t.HandlingSiteId
	}
	return t.SiteId
}

// TransferAdtyTicketsToMain moves handling authority while retaining the original site.
func TransferAdtyTicketsToMain(tx *gorm.DB, sites *gorm.DB) error {
	return tx.Model(&AdtyTicket{}).Where("site_id IN (?) AND handling_site_id IS NULL", sites).
		UpdateColumns(map[string]any{"handling_site_id": 0, "assignee_id": 0,
			"status": gorm.Expr("CASE WHEN status IN ? THEN status ELSE ? END",
				[]string{AdtyTicketClosed, AdtyTicketResolved}, AdtyTicketPendingAssignment)}).Error
}

func (AdtyTicket) TableName() string { return "adty_tickets" }

// AdtyTicketMessage is a public reply or an internal note. Customer responses omit internal rows.
type AdtyTicketMessage struct {
	Id                int64  `json:"id"`
	TicketId          int64  `json:"ticket_id" gorm:"index"`
	AuthorId          int    `json:"author_id"`
	AuthorRole        string `json:"author_role" gorm:"type:varchar(24)"`
	English           string `json:"english,omitempty" gorm:"-"`
	TranslationStatus string `json:"translation_status,omitempty" gorm:"-"`
	Visibility        string `json:"visibility" gorm:"type:varchar(16);index"`
	Body              string `json:"body" gorm:"type:text"`
	Dedupe            string `json:"-" gorm:"uniqueIndex;type:varchar(160)"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyTicketMessage) TableName() string { return "adty_ticket_messages" }

// AdtyTicketImage is a private raster attachment. Bytes stay on disk; this row is the authorization record.
// Visibility mirrors AdtyTicketMessage: an image uploaded for an internal note is
// internal and never reaches a customer, in the detail list or by direct id.
type AdtyTicketImage struct {
	DeletePending bool    `json:"-" gorm:"index;default:false"`
	UploadKey     *string `json:"-" gorm:"uniqueIndex;type:char(64)"`
	Id            int64   `json:"id"`
	ObjectId      string  `json:"object_id" gorm:"type:varchar(64);index"`
	TicketId      int64   `json:"ticket_id" gorm:"index"`
	MessageId     int64   `json:"message_id" gorm:"index"`
	Visibility    string  `json:"visibility" gorm:"type:varchar(16);index"`
	Mime          string  `json:"mime" gorm:"type:varchar(32)"`
	Size          int     `json:"size"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	SHA256        string  `json:"sha256" gorm:"type:varchar(64)"`
	Storage       string  `json:"-" gorm:"type:varchar(128)"`
	CreatedAt     int64   `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyTicketImage) TableName() string { return "adty_ticket_images" }

// AdtyTicketNotice is an authoritative in-site notification record produced from
// a ticket event (create, public reply, or status/reopen transition).
type AdtyTicketNotice struct {
	ExpiredAt        int64  `json:"-" gorm:"default:0"`
	SnapshotTeamId   int64  `json:"-" gorm:"default:0"`
	TelegramPending  bool   `json:"-" gorm:"index;default:false"`
	TicketTeamShared bool   `json:"-" gorm:"column:ticket_team_shared;->;-:migration"`
	TicketOwnerID    int    `json:"-" gorm:"column:ticket_owner_id;->;-:migration"`
	Id               int64  `json:"id"`
	Purpose          string `json:"-" gorm:"type:varchar(16)"`
	EventKey         string `json:"event_key" gorm:"uniqueIndex;type:varchar(128);not null"`
	TicketId         int64  `json:"ticket_id" gorm:"index;not null"`
	EventId          int64  `json:"event_id" gorm:"index"`
	SiteId           int    `json:"site_id" gorm:"index"`
	UserId           int    `json:"user_id" gorm:"index;not null"`
	ActorId          int    `json:"actor_id"`
	Action           string `json:"action" gorm:"type:varchar(32);index"`
	Title            string `json:"title" gorm:"type:varchar(120)"`
	Content          string `json:"content" gorm:"type:text"`
	ReadKey          string `json:"read_key" gorm:"uniqueIndex;type:char(64);not null"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyTicketNotice) TableName() string { return "adty_ticket_notices" }

// AdtyTicketEvent is an append-only handling record. It does not grant money or key permissions.
type AdtyTicketEvent struct {
	OperationKey   *string `json:"-" gorm:"uniqueIndex;type:char(64)"`
	ClosureEventId int64   `json:"-"`
	Id             int64   `json:"id"`
	TicketId       int64   `json:"ticket_id" gorm:"index"`
	ActorId        int     `json:"actor_id"`
	Action         string  `json:"action" gorm:"type:varchar(32)"`
	Detail         string  `json:"detail" gorm:"type:varchar(160)"`
	CreatedAt      int64   `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyTicketEvent) TableName() string { return "adty_ticket_events" }

// AdtyTicketPolicy is the Root image limit. Zero values mean the service defaults.
type AdtyTicketPolicy struct {
	Id        int   `json:"id" gorm:"primaryKey"`
	MaxBytes  int   `json:"max_bytes"`
	MaxCount  int   `json:"max_count"`
	MaxTotal  int   `json:"max_total"`
	MaxSide   int   `json:"max_side"`
	MaxPixels int   `json:"max_pixels"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtyTicketPolicy) TableName() string { return "adty_ticket_policies" }

// NormalizeAdtyTicketStatus maps a stored status, including historical leftovers,
// onto one official G.12.7 state. assigned → in_progress; unassigned open → new;
// open with an assignee → in_progress. Official values are returned unchanged.
func NormalizeAdtyTicketStatus(status string, assigneeId int) string {
	switch status {
	case AdtyTicketLegacyAssigned:
		return AdtyTicketInProgress
	case AdtyTicketLegacyOpen:
		if assigneeId > 0 {
			return AdtyTicketInProgress
		}
		return AdtyTicketNew
	default:
		return status
	}
}

// RemapAdtyTicketLegacyStatuses rewrites leftover open/assigned rows into official
// states. Tickets are never deleted. The mapping is idempotent.
func RemapAdtyTicketLegacyStatuses(db *gorm.DB) error {
	if db == nil {
		db = DB
	}
	if err := db.AutoMigrate(&AdtyTicketImage{}, &AdtyTicketNotice{}); err != nil {
		return err
	}
	if err := db.Model(&AdtyTicket{}).Where("status = ?", AdtyTicketLegacyAssigned).Update("status", AdtyTicketInProgress).Error; err != nil {
		return err
	}
	if err := db.Model(&AdtyTicket{}).Where("status = ? AND assignee_id > 0", AdtyTicketLegacyOpen).Update("status", AdtyTicketInProgress).Error; err != nil {
		return err
	}
	return db.Model(&AdtyTicket{}).Where("status = ? AND assignee_id = 0", AdtyTicketLegacyOpen).Update("status", AdtyTicketNew).Error
}

// BackfillAdtyTicketImageVisibility hides every image written before the
// visibility column existed. Stored rows cannot prove their message was a
// public reply, and the two mistakes are not symmetric: hiding a customer's
// public image is a visible, appealable defect, while exposing an internal
// screenshot cannot be undone. Legacy rows therefore land on internal.
func BackfillAdtyTicketImageVisibility(db *gorm.DB) error {
	if db == nil {
		db = DB
	}
	return db.Model(&AdtyTicketImage{}).
		Where("visibility IS NULL OR visibility = ?", "").
		Update("visibility", AdtyTicketVisibilityNote).Error
}
