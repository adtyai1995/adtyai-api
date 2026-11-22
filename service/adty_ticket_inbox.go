package service

import (
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// The recipient is fixed at event time. Reading still requires the account and
// workspace to have access on this host, including after site closure or revocation.
func adtyTicketInboxQuery(userID int, site *model.AdtySite) (*gorm.DB, string, error) {
	q := model.DB.Model(&model.AdtyTicketNotice{}).Where("adty_ticket_notices.user_id = ?", userID)
	actor, err := loadAdtyTicketActor(userID)
	if err != nil {
		return nil, "", err
	}
	host := 0
	if site != nil {
		host = site.Id
	}
	var handlerScope string
	switch {
	case host == 0 && actor.PlatformMain:
		handlerScope = "platform"
	case actor.OwnerTickets && (host == actor.OwnerSiteId || host == actor.User.SiteId):
		handlerScope = "owner"
	case actor.StaffTickets && host == actor.Staff.SiteId:
		handlerScope = "staff"
	}
	customerTickets := model.DB.Model(&model.AdtyTicket{}).Select("id").Where("user_id = ?", userID)
	if actor.User.StaffSiteId != 0 || actor.Staff != nil || host != actor.User.SiteId {
		customerTickets = customerTickets.Where("1=0")
	}
	// A pre-upgrade notice lacked a purpose. The original recipient's id still
	// limits visibility, and its public wording contains no internal content.
	customer := model.DB.Where("ticket_id IN (?) AND COALESCE(purpose,'') IN ?", customerTickets, []string{"", model.AdtyTelegramPurposeUser})
	if handlerScope != "" {
		actor.Scope = handlerScope
		handled := scopedAdtyTickets(model.DB, actor).Select("id")
		customer = customer.Or("ticket_id IN (?) AND COALESCE(purpose,'') IN ?", handled, []string{"", model.AdtyTelegramPurposeAdmin})
	}
	return q.Where(customer), handlerScope, nil
}

const ticketInboxColumns = "adty_ticket_notices.*, (SELECT t.user_id FROM adty_tickets t WHERE t.id=adty_ticket_notices.ticket_id) AS ticket_owner_id"

func formatTicketInboxNotice(row model.AdtyTicketNotice, handlerScope string) map[string]any {
	path := "/profile"
	if row.Purpose == model.AdtyTelegramPurposeAdmin || (row.Purpose == "" && row.UserId != row.TicketOwnerID) {
		switch handlerScope {
		case "platform":
			path = "/site-admin/tickets"
		case "owner":
			path = "/site-owner/tickets"
		case "staff":
			path = "/site-staff"
		}
	}
	return map[string]any{"id": fmt.Sprintf("ticket-notice:%d", row.Id), "read_key": row.ReadKey, "title": row.Title, "content": row.Content,
		"ticket_path": path + "#case-" + strconv.FormatInt(row.TicketId, 10), "publishDate": time.Unix(row.CreatedAt, 0).UTC().Format(time.RFC3339)}
}
