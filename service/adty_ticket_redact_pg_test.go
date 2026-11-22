package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Free-text diagnostics are redacted before storage.
func TestAdtyTicketRedactsRelatedFields(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AdtySite{}, &model.AdtySiteStaff{}, &model.AdtyTeam{}, &model.AdtyTeamMember{}))
	require.NoError(t, model.MigrateAdtyTables())

	customer := ticketTestUser(t, db, "redact-customer", common.RoleCommonUser)

	// related_error 是最常夹带 key 的字段：用户贴报错时会把请求体或 header 一起带上。
	created, err := CreateAdtyTicket(customer.Id, AdtyTicketInput{
		Type:         "api",
		Title:        "Upstream rejects the call",
		Body:         "See the attached error text",
		RelatedError: "401 upstream: api_key=sk-abcdefgh12345678 rejected",
		RelatedModel: "gpt-4o sk-abcdefgh12345678",
		RelatedGroup: "default password=hunter2",
		Idempotency:  "redact-1",
	})
	require.NoError(t, err)

	var stored model.AdtyTicket
	require.NoError(t, db.First(&stored, created.Id).Error)
	assert.NotContains(t, stored.RelatedError, "sk-abcdefgh12345678")
	assert.Contains(t, stored.RelatedError, "[redacted]")
	assert.NotContains(t, stored.RelatedModel, "sk-abcdefgh12345678")
	assert.NotContains(t, stored.RelatedGroup, "hunter2")
}

// An unverified identifier cannot create a business link. The customer can
// still report it in the description, where secrets are redacted.
func TestAdtyTicketRejectsUnverifiedReferenceAndRedactsItsDescription(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AdtySite{}, &model.AdtySiteStaff{}, &model.AdtyTeam{}, &model.AdtyTeamMember{}))
	require.NoError(t, model.MigrateAdtyTables())

	customer := ticketTestUser(t, db, "redact-ref-customer", common.RoleCommonUser)
	input := AdtyTicketInput{Type: "billing", Title: "Order not credited", Body: "Paid but quota missing", RelatedKind: "topup", RelatedRef: "sk-abcdefgh12345678", Idempotency: "redact-ref-1"}
	_, err := CreateAdtyTicket(customer.Id, input)
	require.ErrorIs(t, err, ErrAdtyTicketInvalid)
	input.Body += " " + input.RelatedRef
	input.RelatedRef, input.RelatedKind = "", ""
	created, err := CreateAdtyTicket(customer.Id, input)
	require.NoError(t, err)
	var message model.AdtyTicketMessage
	require.NoError(t, db.Where("ticket_id=?", created.Id).First(&message).Error)
	require.NotContains(t, message.Body, "sk-abcdefgh12345678")
	require.Contains(t, message.Body, "[redacted]")
}
