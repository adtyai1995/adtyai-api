package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestFinancialAdjustmentAuditSealsOnlyAfterOperatorLogKeepsRef(t *testing.T) {
	opened, err := OpenAdtyTestDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opened.Close()) })
	require.NoError(t, opened.DB.AutoMigrate(&AuditIntent{}, &AuditLog{}))

	const eventID = "evt-financial-adjust-seal"
	approval := "financial_adjustment:41:approved:7"
	stripped := AuditLog{
		EventId: eventID, CreatedAt: 1_700_000_000, UserId: 7,
		Username: "root", ActorRole: common.RoleRootUser,
		Action: "user.quota_add", Category: AuditCategoryOperation,
		Result: AuditResultSuccess, Success: true,
	}
	require.NoError(t, AppendAuditIntentTx(opened.DB, stripped))

	ctx := context.Background()
	require.Error(t, AcknowledgeAuditIntent(ctx, eventID))
	var pending AuditIntent
	require.NoError(t, opened.DB.Where("event_id = ?", eventID).Take(&pending).Error)
	require.Zero(t, pending.DeliveredAt)
	require.NotContains(t, pending.Payload, "financial_adjustment:")

	require.Error(t, WritePreparedAuditLog(ctx, stripped))
	bare := stripped
	bare.ApprovalRef = "ticket:41"
	require.Error(t, WritePreparedAuditLog(ctx, bare))

	full := stripped
	full.ApprovalRef = approval
	full.Reason = "correct linked order"
	require.NoError(t, WritePreparedAuditLog(ctx, full))
	require.NoError(t, AcknowledgeAuditIntent(ctx, eventID))

	var sealed AuditIntent
	require.NoError(t, opened.DB.Where("event_id = ?", eventID).Take(&sealed).Error)
	require.NotZero(t, sealed.DeliveredAt)
	require.NotContains(t, sealed.Payload, "financial_adjustment:")
	var logged AuditLog
	require.NoError(t, opened.DB.Where("event_id = ?", eventID).Take(&logged).Error)
	require.Equal(t, approval, logged.ApprovalRef)

	require.NoError(t, DeliverAuditIntent(ctx, eventID))
	var replayed AuditLog
	require.NoError(t, opened.DB.Where("event_id = ?", eventID).Take(&replayed).Error)
	require.Equal(t, approval, replayed.ApprovalRef)
	require.NoError(t, AcknowledgeAuditIntent(ctx, eventID))
}
