package service

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// The recipient is fixed at event time. Reading still requires the account and
// workspace to have access on this host, including after site closure or revocation.
func adtyTicketInboxQuery(userID int, site *model.AdtySite, tokenScopes ...[]string) (*gorm.DB, string, error) {
	return adtyTicketInboxQueryWithMethod(userID, site, http.MethodGet, tokenScopes...)
}

func adtyTicketInboxQueryWithMethod(userID int, site *model.AdtySite, method string, tokenScopes ...[]string) (*gorm.DB, string, error) {
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
	if handlerScope != "" && len(tokenScopes) > 0 {
		resolved := AdtyTicketTokenScope(handlerScope, method, tokenScopes[0])
		if resolved == "denied" {
			handlerScope = ""
		} else {
			handlerScope = resolved
		}
	}
	actor.Scope = "personal"
	customerTickets := scopedAdtyTickets(model.DB, actor).Select("id")
	if actor.TeamId > 0 {
		customerTickets = customerTickets.Or("team_id=? AND team_shared=?", actor.TeamId, true)
	}
	if actor.User.StaffSiteId != 0 || actor.Staff != nil || host != actor.User.SiteId {
		customerTickets = model.DB.Model(&model.AdtyTicket{}).Select("id").Where("1=0")
	}
	// A pre-upgrade notice lacked a purpose. The original recipient's id still
	// limits visibility, and its public wording contains no internal content.
	customer := model.DB.Where("ticket_id IN (?) AND COALESCE(purpose,'') IN ?", customerTickets, []string{"", model.AdtyTelegramPurposeUser})
	if handlerScope != "" {
		actor.Scope = handlerScope
		handled := scopedAdtyTickets(model.DB, actor).Select("id")
		customer = customer.Or("ticket_id IN (?) AND COALESCE(purpose,'') IN ?", handled, []string{"", model.AdtyTelegramPurposeAdmin})
	}
	// Expiry removes the case and every attachment. Snapshot authorization
	// uses the fixed recipient, purpose, handling site and explicit team.
	if host == actor.User.SiteId && actor.User.StaffSiteId == 0 && actor.Staff == nil {
		customer = customer.Or("expired_at > 0 AND purpose=? AND (snapshot_team_id=0 OR snapshot_team_id=?)", model.AdtyTelegramPurposeUser, actor.TeamId)
	}
	if handlerScope != "" {
		archived := model.DB.Where("expired_at > 0 AND purpose=?", model.AdtyTelegramPurposeAdmin)
		switch handlerScope {
		case "owner":
			archived = archived.Where("site_id=?", actor.OwnerSiteId)
		case "staff":
			archived = archived.Where("site_id=?", actor.Staff.SiteId)
		case "platform_main":
			archived = archived.Where("site_id=0")
		case "platform":
			if !actor.PlatformAll {
				archived = archived.Where("site_id=0")
			}
		}
		customer = customer.Or(archived)
	}
	return q.Where(customer), handlerScope, nil
}

const ticketInboxColumns = "adty_ticket_notices.*, (SELECT t.user_id FROM adty_tickets t WHERE t.id=adty_ticket_notices.ticket_id) AS ticket_owner_id, (SELECT t.team_shared FROM adty_tickets t WHERE t.id=adty_ticket_notices.ticket_id) AS ticket_team_shared"

func formatTicketInboxNotice(row model.AdtyTicketNotice, handlerScope string) map[string]any {
	if row.ExpiredAt > 0 {
		return map[string]any{
			"id":          fmt.Sprintf("ticket-notice:%d", row.Id),
			"read_key":    row.ReadKey,
			"source":      "ticket_notice",
			"title":       fmt.Sprintf("工单 #%d / Ticket #%d", row.TicketId, row.TicketId),
			"content":     "工单正文和附件已到期清理。\n\nThe ticket and its attachments have expired.",
			"publishDate": time.Unix(row.CreatedAt, 0).UTC().Format(time.RFC3339),
		}
	}
	path := "/support"
	if row.Purpose == model.AdtyTelegramPurposeAdmin || (row.Purpose == "" && row.UserId != row.TicketOwnerID) {
		switch handlerScope {
		case "platform", "platform_main":
			path = "/admin-tickets"
		case "owner":
			path = "/site-owner/tickets"
		case "staff":
			path = "/site-staff"
		}
	}
	link := path + "#case-" + strconv.FormatInt(row.TicketId, 10)
	if path == "/support" && row.TicketTeamShared {
		link += "-team"
	}
	return map[string]any{
		"id":          fmt.Sprintf("ticket-notice:%d", row.Id),
		"read_key":    row.ReadKey,
		"source":      "ticket_notice",
		"title":       row.Title,
		"content":     row.Content,
		"ticket_path": link,
		"publishDate": time.Unix(row.CreatedAt, 0).UTC().Format(time.RFC3339),
	}
}
