package service

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ISS-R1-0053: Idempotency keys on CreateAdtyTicket are scoped by site and actor,
// and stored responses are authorized before returning.
func TestAdtyTicketCreateIdempotencyScopedByActorAndSite(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")

	ownerA := referralUser(t, db, "idem-owner-a")
	siteA := model.AdtySite{OwnerUserId: ownerA.Id, Subdomain: "idem-site-a", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&siteA).Error)
	userA := referralUser(t, db, "idem-user-a")
	require.NoError(t, db.Model(userA).Update("site_id", siteA.Id).Error)

	ownerB := referralUser(t, db, "idem-owner-b")
	siteB := model.AdtySite{OwnerUserId: ownerB.Id, Subdomain: "idem-site-b", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&siteB).Error)
	userB := referralUser(t, db, "idem-user-b")
	require.NoError(t, db.Model(userB).Update("site_id", siteB.Id).Error)

	ticketA, err := CreateAdtyTicket(userA.Id, AdtyTicketInput{
		Type:        "billing",
		Title:       "Site A private title",
		Body:        "Site A body",
		RelatedKind: "topup",
		RelatedRef:  "ORDER-A-SECRET",
		Idempotency: "shared-1",
	})
	require.NoError(t, err)
	require.Equal(t, userA.Id, ticketA.UserId)
	require.Equal(t, siteA.Id, ticketA.SiteId)

	// Same user + same site + same key replays the authorized ticket.
	replayA, err := CreateAdtyTicket(userA.Id, AdtyTicketInput{
		Type:        "billing",
		Title:       "Site A retry",
		Body:        "Site A retry body",
		Idempotency: "shared-1",
	})
	require.NoError(t, err)
	assert.Equal(t, ticketA.Id, replayA.Id)

	// User B on Site B using the same client key gets their own ticket, never User A's.
	ticketB, err := CreateAdtyTicket(userB.Id, AdtyTicketInput{
		Type:        "api",
		Title:       "Site B title",
		Body:        "Site B body",
		RelatedKind: "api",
		RelatedRef:  "REF-B",
		Idempotency: "shared-1",
	})
	require.NoError(t, err)
	assert.NotEqual(t, ticketA.Id, ticketB.Id)
	assert.Equal(t, userB.Id, ticketB.UserId)
	assert.Equal(t, siteB.Id, ticketB.SiteId)
	assert.Equal(t, "Site B title", ticketB.Title)
	assert.Equal(t, "REF-B", ticketB.RelatedRef)

	// Even if a stored dedupe row points to another user's ticket, replay fails closed.
	tamperedDedupe := ticketCreateDedupe(siteB.Id, userB.Id, "tampered-key")
	require.NoError(t, db.Create(&model.AdtyTicketMessage{
		TicketId:   ticketA.Id,
		AuthorId:   userA.Id,
		Visibility: model.AdtyTicketVisibilityPublic,
		Body:       "tampered",
		Dedupe:     tamperedDedupe,
	}).Error)
	_, err = CreateAdtyTicket(userB.Id, AdtyTicketInput{
		Type:        "other",
		Title:       "Attempt replay",
		Body:        "body",
		Idempotency: "tampered-key",
	})
	require.ErrorIs(t, err, ErrAdtyTicketForbidden)
}

// ISS-R1-0054: Reply and internal note idempotency keys are separated by actor
// and visibility, and replay checks visibility and author authorization.
func TestAdtyTicketMessageIdempotencySeparatesActorAndVisibility(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")

	customer := referralUser(t, db, "msg-idem-customer")
	owner := referralUser(t, db, "msg-idem-owner")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "msg-idem-site", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(customer).Update("site_id", site.Id).Error)
	staff := referralUser(t, db, "msg-idem-staff")
	require.NoError(t, db.Create(&model.AdtySiteStaff{
		SiteId: site.Id, OwnerUserId: owner.Id, UserId: staff.Id,
		Role: "support", Status: "enabled", Permissions: `["customers.read"]`,
	}).Error)

	ticket, err := CreateAdtyTicket(customer.Id, AdtyTicketInput{
		Type: "billing", Title: "Need help", Body: "initial question", Idempotency: "init-1",
	})
	require.NoError(t, err)

	// Staff posts an internal note with key "note-1".
	internalNote, err := NoteAdtyTicket(staff.Id, ticket.Id, "internal investigation details", "note-1")
	require.NoError(t, err)
	require.Equal(t, model.AdtyTicketVisibilityNote, internalNote.Visibility)

	// Customer posts a public reply with the same key "note-1" — must not return internal note.
	customerReply, err := ReplyAdtyTicket(int64(customer.Id), ticket.Id, "customer public follow-up", "note-1")
	require.NoError(t, err)
	assert.NotEqual(t, internalNote.Id, customerReply.Id)
	assert.Equal(t, model.AdtyTicketVisibilityPublic, customerReply.Visibility)
	assert.Equal(t, customer.Id, customerReply.AuthorId)
	assert.Equal(t, "customer public follow-up", customerReply.Body)
	assert.NotContains(t, customerReply.Body, "internal investigation")

	// Even if a stored row under customer's reply dedupe has internal visibility, replay is forbidden.
	forcedDedupe := ticketMessageDedupe(ticket.Id, customer.Id, model.AdtyTicketVisibilityPublic, "forced-internal")
	require.NoError(t, db.Create(&model.AdtyTicketMessage{
		TicketId:   ticket.Id,
		AuthorId:   staff.Id,
		Visibility: model.AdtyTicketVisibilityNote,
		Body:       "hidden internal row",
		Dedupe:     forcedDedupe,
	}).Error)
	_, err = ReplyAdtyTicket(int64(customer.Id), ticket.Id, "try replay", "forced-internal")
	require.ErrorIs(t, err, ErrAdtyTicketForbidden)
}

// ISS-R1-0122: OpenAdtyTicketImage must fail closed if writing the image_view audit event fails.
func TestAdtyTicketOpenImageFailsClosedWhenAuditInsertFails(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	restore := SetAdtyTicketImageDirForTest(t.TempDir())
	t.Cleanup(restore)

	customer := referralUser(t, db, "img-audit-customer")
	ticket, err := CreateAdtyTicket(customer.Id, AdtyTicketInput{
		Type: "other", Title: "Screenshot", Body: "attached", Idempotency: "img-audit-1",
	})
	require.NoError(t, err)

	img, err := AddAdtyTicketImage(customer.Id, ticket.Id, model.AdtyTicketVisibilityPublic, tinyPNG(t))
	require.NoError(t, err)

	path, mime, err := OpenAdtyTicketImage(customer.Id, ticket.Id, img.Id)
	require.NoError(t, err)
	assert.NotEmpty(t, path)
	assert.Equal(t, "image/png", mime)

	const cbName = "test:fail_image_view_event"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(cbName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "adty_ticket_events" {
			if ev, ok := tx.Statement.Dest.(*model.AdtyTicketEvent); ok && ev.Action == "image_view" {
				_ = tx.AddError(errors.New("audit event store unavailable"))
			}
		}
	}))
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(cbName)
	})

	failedPath, failedMime, err := OpenAdtyTicketImage(customer.Id, ticket.Id, img.Id)
	require.Error(t, err)
	assert.Empty(t, failedPath)
	assert.Empty(t, failedMime)
}

// ISS-R1-0131: Assignee must be an active, authorized support/manager account in the ticket's scope.
func TestAdtyTicketAssigneeMustBeActiveAuthorizedSupportInScope(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")

	ownerA := referralUser(t, db, "assign-owner-a")
	siteA := model.AdtySite{OwnerUserId: ownerA.Id, Subdomain: "assign-site-a", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&siteA).Error)
	customerA := referralUser(t, db, "assign-customer-a")
	require.NoError(t, db.Model(customerA).Update("site_id", siteA.Id).Error)
	staffA := referralUser(t, db, "assign-staff-a")
	require.NoError(t, db.Create(&model.AdtySiteStaff{
		SiteId: siteA.Id, OwnerUserId: ownerA.Id, UserId: staffA.Id,
		Role: "support", Status: "enabled", Permissions: `["customers.read"]`,
	}).Error)
	disabledStaffA := referralUser(t, db, "assign-disabled-staff-a")
	require.NoError(t, db.Create(&model.AdtySiteStaff{
		SiteId: siteA.Id, OwnerUserId: ownerA.Id, UserId: disabledStaffA.Id,
		Role: "support", Status: "disabled", Permissions: `["customers.read"]`,
	}).Error)

	ownerB := referralUser(t, db, "assign-owner-b")
	siteB := model.AdtySite{OwnerUserId: ownerB.Id, Subdomain: "assign-site-b", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&siteB).Error)
	customerB := referralUser(t, db, "assign-customer-b")
	require.NoError(t, db.Model(customerB).Update("site_id", siteB.Id).Error)
	staffB := referralUser(t, db, "assign-staff-b")
	require.NoError(t, db.Create(&model.AdtySiteStaff{
		SiteId: siteB.Id, OwnerUserId: ownerB.Id, UserId: staffB.Id,
		Role: "support", Status: "enabled", Permissions: `["customers.read"]`,
	}).Error)

	ticket, err := CreateAdtyTicket(customerA.Id, AdtyTicketInput{
		Type: "billing", Title: "Assign check", Body: "help", Idempotency: "assign-1",
	})
	require.NoError(t, err)

	// Nonexistent user ID, ordinary customer, other-site owner/staff, or disabled staff must be rejected.
	require.ErrorIs(t, SetAdtyTicketStatus(ownerA.Id, ticket.Id, "", "", 999999), ErrAdtyTicketInvalid)
	require.ErrorIs(t, SetAdtyTicketStatus(ownerA.Id, ticket.Id, "", "", customerA.Id), ErrAdtyTicketInvalid)
	require.ErrorIs(t, SetAdtyTicketStatus(ownerA.Id, ticket.Id, "", "", customerB.Id), ErrAdtyTicketInvalid)
	require.ErrorIs(t, SetAdtyTicketStatus(ownerA.Id, ticket.Id, "", "", ownerB.Id), ErrAdtyTicketInvalid)
	require.ErrorIs(t, SetAdtyTicketStatus(ownerA.Id, ticket.Id, "", "", staffB.Id), ErrAdtyTicketInvalid)
	require.ErrorIs(t, SetAdtyTicketStatus(ownerA.Id, ticket.Id, "", "", disabledStaffA.Id), ErrAdtyTicketInvalid)

	var unchanged model.AdtyTicket
	require.NoError(t, db.First(&unchanged, ticket.Id).Error)
	assert.Zero(t, unchanged.AssigneeId)
	assert.Equal(t, model.AdtyTicketNew, unchanged.Status)

	// Assigning to active authorized staff on Site A succeeds.
	require.NoError(t, SetAdtyTicketStatus(ownerA.Id, ticket.Id, "", "", staffA.Id))
	var assigned model.AdtyTicket
	require.NoError(t, db.First(&assigned, ticket.Id).Error)
	assert.Equal(t, staffA.Id, assigned.AssigneeId)
	assert.Equal(t, model.AdtyTicketInProgress, assigned.Status)

	// If the assignee is later removed from staff, customer replies fall back to site owner rather than notifying removed staff.
	require.NoError(t, db.Model(&model.AdtySiteStaff{}).Where("user_id = ?", staffA.Id).Update("status", "deleted").Error)
	reply, err := ReplyAdtyTicket(int64(customerA.Id), ticket.Id, "follow up after staff removal", "assign-reply-1")
	require.NoError(t, err)
	var delivery model.AdtyTelegramDelivery
	require.NoError(t, db.Where("event_key = ?", "ticket:"+strconv.FormatInt(ticket.Id, 10)+":msg:"+strconv.FormatInt(reply.Id, 10)).First(&delivery).Error)
	assert.Equal(t, ownerA.Id, delivery.UserId)
}

// ISS-R1-0132 & ISS-R1-0209: AddAdtyTicketImage locks the ticket inside the transaction
// before checking count/total limits, and generates a 16-byte random hex ObjectId while
// keeping legacy Storage compatibility.
func TestAdtyTicketImageQuotaSerializationAndRandomObjectId(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	dir := t.TempDir()
	restore := SetAdtyTicketImageDirForTest(dir)
	t.Cleanup(restore)

	require.NoError(t, db.Create(&model.AdtyTicketPolicy{
		Id: 1, MaxBytes: 2 << 20, MaxCount: 2, MaxTotal: 2 << 20, MaxSide: 4096, MaxPixels: 16_000_000,
	}).Error)

	customer := referralUser(t, db, "img-quota-customer")
	ticket, err := CreateAdtyTicket(customer.Id, AdtyTicketInput{
		Type: "api", Title: "Images", Body: "upload test", Idempotency: "img-quota-1",
	})
	require.NoError(t, err)

	sample := tinyPNG(t)
	first, err := AddAdtyTicketImage(customer.Id, ticket.Id, model.AdtyTicketVisibilityPublic, sample)
	require.NoError(t, err)
	second, err := AddAdtyTicketImage(customer.Id, ticket.Id, model.AdtyTicketVisibilityPublic, sample)
	require.NoError(t, err)

	// Both uploads of identical image bytes receive distinct 32-char hex random ObjectIds and Storage paths.
	assert.Len(t, first.ObjectId, 32)
	assert.Len(t, second.ObjectId, 32)
	_, err = hex.DecodeString(first.ObjectId)
	require.NoError(t, err)
	_, err = hex.DecodeString(second.ObjectId)
	require.NoError(t, err)
	assert.NotEqual(t, first.ObjectId, second.ObjectId)
	assert.NotEqual(t, first.Storage, second.Storage)
	assert.NotContains(t, first.Storage, strconv.FormatInt(ticket.Id, 10)+"-")

	// Third upload exceeds MaxCount=2 and is rejected inside the locked transaction.
	_, err = AddAdtyTicketImage(customer.Id, ticket.Id, model.AdtyTicketVisibilityPublic, sample)
	require.ErrorIs(t, err, ErrAdtyTicketImage)

	// Raising MaxCount but setting a tight MaxTotal enforces total byte budget inside the transaction.
	require.NoError(t, db.Model(&model.AdtyTicketPolicy{}).Where("id = 1").Updates(map[string]any{
		"max_count": 6,
		"max_total": first.Size + second.Size + 1,
	}).Error)
	_, err = AddAdtyTicketImage(customer.Id, ticket.Id, model.AdtyTicketVisibilityPublic, sample)
	require.ErrorIs(t, err, ErrAdtyTicketImage)

	detail, err := GetAdtyTicket(customer.Id, ticket.Id)
	require.NoError(t, err)
	require.Len(t, detail.Images, 2)
	assert.Equal(t, first.ObjectId, detail.Images[0].ObjectId)
	assert.Equal(t, second.ObjectId, detail.Images[1].ObjectId)

	// Legacy Storage row without ObjectId still opens cleanly.
	legacyFile := filepath.Join(dir, "legacy-storage.png")
	require.NoError(t, os.WriteFile(legacyFile, sample, 0o640))
	legacyRow := model.AdtyTicketImage{
		TicketId: ticket.Id, Visibility: model.AdtyTicketVisibilityPublic,
		Mime: "image/png", Size: len(sample), SHA256: "legacy", Storage: "legacy-storage.png",
	}
	require.NoError(t, db.Create(&legacyRow).Error)
	openedPath, openedMime, err := OpenAdtyTicketImage(customer.Id, ticket.Id, legacyRow.Id)
	require.NoError(t, err)
	assert.Equal(t, legacyFile, openedPath)
	assert.Equal(t, "image/png", openedMime)
}

// ISS-R1-0133: Create, reply, and status changes persist in-site notices and retriable
// Telegram outbox rows atomically, and SyncAdtyTicketEventNotices recovers missing rows.
func TestAdtyTicketEventsPersistNoticesAndOutboxAtomicallyAndRecover(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")

	owner := referralUser(t, db, "outbox-owner")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "outbox-site", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	customer := referralUser(t, db, "outbox-customer")
	require.NoError(t, db.Model(customer).Update("site_id", site.Id).Error)
	staff := referralUser(t, db, "outbox-staff")
	require.NoError(t, db.Create(&model.AdtySiteStaff{
		SiteId: site.Id, OwnerUserId: owner.Id, UserId: staff.Id,
		Role: "support", Status: "enabled", Permissions: `["customers.read"]`,
	}).Error)

	// 1. Create produces in-site notices and Telegram outbox rows for customer and site owner.
	ticket, err := CreateAdtyTicket(customer.Id, AdtyTicketInput{
		Type: "billing", Title: "Outbox test", Body: "initial body", Idempotency: "outbox-create-1",
	})
	require.NoError(t, err)

	createUserKey := "ticket:" + strconv.FormatInt(ticket.Id, 10) + ":create:user"
	createAdminKey := "ticket:" + strconv.FormatInt(ticket.Id, 10) + ":create"
	var createUserNotice model.AdtyTicketNotice
	require.NoError(t, db.Where("event_key = ?", createUserKey).First(&createUserNotice).Error)
	assert.Equal(t, customer.Id, createUserNotice.UserId)
	var createAdminNotice model.AdtyTicketNotice
	require.NoError(t, db.Where("event_key = ?", createAdminKey).First(&createAdminNotice).Error)
	assert.Equal(t, owner.Id, createAdminNotice.UserId)

	var createDelivery model.AdtyTelegramDelivery
	require.NoError(t, db.Where("event_key = ?", createAdminKey).First(&createDelivery).Error)
	assert.Equal(t, owner.Id, createDelivery.UserId)

	// 2. Status/assignment change produces in-site notice and Telegram outbox row.
	require.NoError(t, SetAdtyTicketStatus(owner.Id, ticket.Id, "", "", staff.Id))
	var statusEvent model.AdtyTicketEvent
	require.NoError(t, db.Where("ticket_id = ? AND action = ?", ticket.Id, "status").Order("id DESC").First(&statusEvent).Error)
	statusCustomerKey := "ticket:" + strconv.FormatInt(ticket.Id, 10) + ":event:" + strconv.FormatInt(statusEvent.Id, 10)
	statusAdminKey := statusCustomerKey + ":admin"

	var statusCustomerNotice model.AdtyTicketNotice
	require.NoError(t, db.Where("event_key = ?", statusCustomerKey).First(&statusCustomerNotice).Error)
	assert.Equal(t, customer.Id, statusCustomerNotice.UserId)
	var statusStaffNotice model.AdtyTicketNotice
	require.NoError(t, db.Where("event_key = ?", statusAdminKey).First(&statusStaffNotice).Error)
	assert.Equal(t, staff.Id, statusStaffNotice.UserId)

	// 3. If Telegram outbox DB write fails during ReplyAdtyTicket, the reply transaction rolls back.
	const failOutboxCB = "test:fail_telegram_delivery_insert"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(failOutboxCB, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "adty_telegram_deliveries" {
			_ = tx.AddError(errors.New("telegram outbox write failed"))
		}
	}))
	_, err = ReplyAdtyTicket(int64(customer.Id), ticket.Id, "should rollback on outbox failure", "outbox-reply-fail")
	require.Error(t, err)
	require.NoError(t, db.Callback().Create().Remove(failOutboxCB))

	var rolledBackCount int64
	require.NoError(t, db.Model(&model.AdtyTicketMessage{}).Where("ticket_id = ? AND body LIKE ?", ticket.Id, "%should rollback%").Count(&rolledBackCount).Error)
	assert.Zero(t, rolledBackCount, "reply message must not commit when outbox write fails")

	// Normal reply succeeds and writes both in-site notice and Telegram outbox.
	reply, err := ReplyAdtyTicket(int64(customer.Id), ticket.Id, "normal reply", "outbox-reply-ok")
	require.NoError(t, err)
	replyKey := "ticket:" + strconv.FormatInt(ticket.Id, 10) + ":msg:" + strconv.FormatInt(reply.Id, 10)

	// 4. Simulate missing notices/outbox rows and verify SyncAdtyTicketEventNotices recovers them.
	require.NoError(t, db.Where("ticket_id = ?", ticket.Id).Delete(&model.AdtyTicketNotice{}).Error)
	require.NoError(t, db.Where("event_key LIKE ?", "ticket:"+strconv.FormatInt(ticket.Id, 10)+":%").Delete(&model.AdtyTelegramDelivery{}).Error)

	recovered, err := SyncAdtyTicketEventNotices()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, recovered, 6)

	var recoveredReplyDelivery model.AdtyTelegramDelivery
	require.NoError(t, db.Where("event_key = ?", replyKey).First(&recoveredReplyDelivery).Error)
	assert.Equal(t, staff.Id, recoveredReplyDelivery.UserId)

	customerNotices, err := ListAdtyTicketNoticesForUser(customer.Id, site.Id, 10)
	require.NoError(t, err)
	assert.NotEmpty(t, customerNotices)
}
