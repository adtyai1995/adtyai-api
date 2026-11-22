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

func reserveTicketTranslationTx(tx *gorm.DB, msg model.AdtyTicketMessage, siteID int) error {
	if msg.Visibility != model.AdtyTicketVisibilityPublic || msg.AuthorRole == "" || msg.AuthorRole == "customer" || !adtyContainsChinese(msg.Body) {
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
	var after int64
	for {
		var ids []int64
		err := model.DB.Model(&model.AdtyTicketMessage{}).
			Where("id > ? AND visibility=? AND author_role IN ?", after, model.AdtyTicketVisibilityPublic, []string{"owner", "staff", "platform", "platform_main"}).
			Where("NOT EXISTS (SELECT 1 FROM adty_translation_heads h WHERE h.object_key='ticket-reply:' || CAST(adty_ticket_messages.id AS TEXT))").
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
		// only when ready to call the provider so later jobs keep their lease.
		claimed := eligible.Session(&gorm.Session{}).Model(&model.AdtyTranslationHead{}).Where("id=? AND version=?", head.Id, head.Version).
			UpdateColumn("lease_until", time.Now().Add(5*time.Minute).Unix())
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
			continue
		}
		_, err := executeAdtyReservedTranslation(head.ObjectKey, head.Source, head.SiteId, head.Version, head.SourceHash)
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
