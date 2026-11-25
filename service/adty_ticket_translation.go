package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func ticketSupportAuthor(role string) bool {
	switch role {
	case "owner", "staff", "platform", "platform_main", "support":
		return true
	}
	return false
}

func reserveTicketTranslationTx(tx *gorm.DB, msg model.AdtyTicketMessage, siteID int) error {
	if msg.Visibility != model.AdtyTicketVisibilityPublic || !ticketSupportAuthor(msg.AuthorRole) || !adtyContainsChinese(msg.Body) {
		return nil
	}
	sum := sha256.Sum256([]byte(msg.Body))
	head := model.AdtyTranslationHead{ObjectKey: ticketTranslationKey(msg.Id), SiteId: siteID, Source: msg.Body,
		SourceHash: hex.EncodeToString(sum[:]), Version: 1, Status: model.AdtyTranslationPending, Notice: AdtyTranslationNotReady}
	inserted := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "object_key"}}, DoNothing: true}).Create(&head)
	if inserted.Error != nil || inserted.RowsAffected == 0 {
		return inserted.Error
	}
	return upsertAdtyTranslationJobTx(tx, &model.AdtyTranslationJob{ObjectKey: head.ObjectKey, SourceHash: head.SourceHash,
		Version: head.Version, Status: head.Status})
}

// Existing replies from before reservations were transactional can lack a
// translation head. Repair them in bounded pages, once on worker startup.
func backfillTicketReplyTranslations() error {
	// A reply's current author permissions cannot establish their old role.
	// Typed recipient records tie a message to a specific event. Older
	// personal cases also identify the two sides by their creator.
	authorRoleSQL := `UPDATE adty_ticket_messages AS m SET author_role=CASE
		WHEN m.visibility='internal' AND EXISTS (SELECT 1 FROM adty_ticket_events e
		  WHERE e.ticket_id=m.ticket_id AND e.action='note' AND e.actor_id=m.author_id) THEN 'support'
		WHEN m.visibility='public' AND m.id=(SELECT MIN(f.id) FROM adty_ticket_messages f WHERE f.ticket_id=m.ticket_id)
		  AND m.author_id=t.user_id AND EXISTS (SELECT 1 FROM adty_ticket_events e WHERE e.ticket_id=t.id AND e.action='create' AND e.actor_id=t.user_id) THEN 'customer'
		WHEN m.visibility='public' AND EXISTS (SELECT 1 FROM adty_ticket_notices n JOIN adty_ticket_events e ON e.id=n.event_id
		  WHERE e.ticket_id=m.ticket_id AND n.ticket_id=m.ticket_id AND e.action='reply' AND e.actor_id=m.author_id AND n.actor_id=m.author_id
		  AND n.event_key IN ('ticket:'||t.id||':msg:'||m.id, 'ticket:'||t.id||':msg:'||m.id||':to:'||n.user_id)
		  AND (n.purpose='admin' OR (COALESCE(n.purpose,'')='' AND COALESCE(t.team_id,0)=0 AND m.author_id=t.user_id))) THEN 'customer'
		WHEN m.visibility='public' AND EXISTS (SELECT 1 FROM adty_ticket_notices n JOIN adty_ticket_events e ON e.id=n.event_id
		  WHERE e.ticket_id=m.ticket_id AND n.ticket_id=m.ticket_id AND e.action='reply' AND e.actor_id=m.author_id AND n.actor_id=m.author_id
		  AND n.event_key IN ('ticket:'||t.id||':msg:'||m.id, 'ticket:'||t.id||':msg:'||m.id||':to:'||n.user_id)
		  AND (n.purpose='user' OR (COALESCE(n.purpose,'')='' AND COALESCE(t.team_id,0)=0 AND m.author_id<>t.user_id AND n.user_id=t.user_id))) THEN 'support'
		WHEN m.visibility='public' AND COALESCE(t.team_id,0)=0 AND m.author_id<>t.user_id AND EXISTS
		  (SELECT 1 FROM adty_ticket_events e WHERE e.ticket_id=m.ticket_id AND e.action='reply' AND e.actor_id=m.author_id AND e.created_at=m.created_at) THEN 'support'
		ELSE 'unconfirmed' END FROM adty_tickets t WHERE t.id=m.ticket_id AND COALESCE(m.author_role,'')='' `
	if model.DB.Dialector != nil && model.DB.Dialector.Name() == "mysql" {
		authorRoleSQL = `UPDATE adty_ticket_messages m
		JOIN adty_tickets t ON t.id=m.ticket_id
		LEFT JOIN (SELECT ticket_id, MIN(id) AS first_id FROM adty_ticket_messages GROUP BY ticket_id) firsts ON firsts.ticket_id=m.ticket_id
		SET m.author_role=CASE
		WHEN m.visibility='internal' AND EXISTS (SELECT 1 FROM adty_ticket_events e
		  WHERE e.ticket_id=m.ticket_id AND e.action='note' AND e.actor_id=m.author_id) THEN 'support'
		WHEN m.visibility='public' AND m.id=firsts.first_id
		  AND m.author_id=t.user_id AND EXISTS (SELECT 1 FROM adty_ticket_events e WHERE e.ticket_id=t.id AND e.action='create' AND e.actor_id=t.user_id) THEN 'customer'
		WHEN m.visibility='public' AND EXISTS (SELECT 1 FROM adty_ticket_notices n JOIN adty_ticket_events e ON e.id=n.event_id
		  WHERE e.ticket_id=m.ticket_id AND n.ticket_id=m.ticket_id AND e.action='reply' AND e.actor_id=m.author_id AND n.actor_id=m.author_id
		  AND n.event_key IN (CONCAT('ticket:', t.id, ':msg:', m.id), CONCAT('ticket:', t.id, ':msg:', m.id, ':to:', n.user_id))
		  AND (n.purpose='admin' OR (COALESCE(n.purpose,'')='' AND COALESCE(t.team_id,0)=0 AND m.author_id=t.user_id))) THEN 'customer'
		WHEN m.visibility='public' AND EXISTS (SELECT 1 FROM adty_ticket_notices n JOIN adty_ticket_events e ON e.id=n.event_id
		  WHERE e.ticket_id=m.ticket_id AND n.ticket_id=m.ticket_id AND e.action='reply' AND e.actor_id=m.author_id AND n.actor_id=m.author_id
		  AND n.event_key IN (CONCAT('ticket:', t.id, ':msg:', m.id), CONCAT('ticket:', t.id, ':msg:', m.id, ':to:', n.user_id))
		  AND (n.purpose='user' OR (COALESCE(n.purpose,'')='' AND COALESCE(t.team_id,0)=0 AND m.author_id<>t.user_id AND n.user_id=t.user_id))) THEN 'support'
		WHEN m.visibility='public' AND COALESCE(t.team_id,0)=0 AND m.author_id<>t.user_id AND EXISTS
		  (SELECT 1 FROM adty_ticket_events e WHERE e.ticket_id=m.ticket_id AND e.action='reply' AND e.actor_id=m.author_id AND e.created_at=m.created_at) THEN 'support'
		ELSE 'unconfirmed' END
		WHERE COALESCE(m.author_role,'')='' `
	}
	if err := model.DB.Exec(authorRoleSQL).Error; err != nil {
		return err
	}
	missingHeadSQL := "NOT EXISTS (SELECT 1 FROM adty_translation_heads h WHERE h.object_key='ticket-reply:' || CAST(adty_ticket_messages.id AS TEXT))"
	if model.DB.Dialector != nil && model.DB.Dialector.Name() == "mysql" {
		missingHeadSQL = "NOT EXISTS (SELECT 1 FROM adty_translation_heads h WHERE h.object_key=CONCAT('ticket-reply:', adty_ticket_messages.id))"
	}
	var after int64
	for {
		var ids []int64
		err := model.DB.Model(&model.AdtyTicketMessage{}).
			Where("id > ? AND visibility=? AND author_role IN ?", after, model.AdtyTicketVisibilityPublic, []string{"owner", "staff", "platform", "platform_main", "support"}).
			Where(missingHeadSQL).
			Order("id").Limit(200).Pluck("id", &ids).Error
		if err != nil || len(ids) == 0 {
			return err
		}
		for _, id := range ids {
			if err := model.DB.Transaction(func(tx *gorm.DB) error {
				// Lock the ticket first, as message retention does. Recheck the
				// message after acquiring that lock in case retention won.
				var ticket model.AdtyTicket
				found := model.AdtyLockForUpdate(tx).Select("id", "site_id").Where("id IN (?)", tx.Model(&model.AdtyTicketMessage{}).Select("ticket_id").Where("id=?", id)).Limit(1).Find(&ticket)
				if found.Error != nil || found.RowsAffected == 0 {
					return found.Error
				}
				var msg model.AdtyTicketMessage
				found = tx.Where("ticket_id=? AND id=?", ticket.Id, id).Limit(1).Find(&msg)
				if found.Error != nil || found.RowsAffected == 0 {
					return found.Error
				}
				return reserveTicketTranslationTx(tx, msg, ticket.SiteId)
			}); err != nil {
				return err
			}
		}
		after = ids[len(ids)-1]
	}
}

func processPendingTicketTranslations(now time.Time) error {
	var heads []model.AdtyTranslationHead
	eligible := model.DB.Where("status=? AND lease_until <= ? AND object_key LIKE ?", model.AdtyTranslationPending, now.Unix(), "ticket-reply:%")
	if err := eligible.Session(&gorm.Session{}).Order("id").Limit(32).Find(&heads).Error; err != nil {
		return err
	}
	var batchError error
	for _, head := range heads {
		// A crashed worker gives up its lease automatically. Claim each row
		// atomically with a strictly monotonic fencing lease_until token.
		nextLease := nextAdtyTranslationLeaseUntil(head.LeaseUntil)
		claimed := eligible.Session(&gorm.Session{}).Model(&model.AdtyTranslationHead{}).
			Where("id=? AND version=? AND lease_until=?", head.Id, head.Version, head.LeaseUntil).
			UpdateColumn("lease_until", nextLease)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
			continue
		}
		_, err := executeAdtyReservedTranslation(head.ObjectKey, head.Source, head.SiteId, head.Version, head.SourceHash, nextLease)
		batchError = errors.Join(batchError, err)
	}
	return batchError
}

func startTicketTranslationWorker() {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		backfilled := false
		for {
			if !backfilled {
				if err := backfillTicketReplyTranslations(); err != nil {
					common.SysError(common.LogText("ticket translation backfill: %s", err.Error()))
				} else {
					backfilled = true
				}
			}
			if err := processPendingTicketTranslations(time.Now()); err != nil {
				common.SysError(common.LogText("ticket translation worker: %s", err.Error()))
			}
			<-ticker.C
		}
	}()
}
