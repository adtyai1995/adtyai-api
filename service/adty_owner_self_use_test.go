package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestOwnerSelfUseHistoryReportsDollarUnitsPG(t *testing.T) {
	f := newOperatingSettleFixture(t)
	require.NoError(t, f.db.AutoMigrate(&model.Log{}))
	for _, tc := range []struct {
		name, unit, frozenPrice, wantUnit, wantPrice string
	}{
		{"token", model.BillingUnitPerToken, "1.2", "per_million_input_tokens", "2.4"},
		{"call", model.BillingUnitPerCall, "0.8", model.BillingUnitPerCall, "0.8"},
		{"expression", model.BillingUnitTieredExpr, "1.2", model.BillingUnitTieredExpr, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := model.EncodeRequestPriceSnapshot(&model.RequestPriceSnapshot{SchemaVersion: model.RequestPriceSnapshotVersion, ModelBasePrice: "1", GroupRatio: "1", SiteCostFactor: "0.8", SiteMarkupRatio: "0", Currency: "USD", AcceptedAt: 1700000000, RuleVersion: "owner-test", BillingUnit: tc.unit, FinalPrice: tc.frozenPrice})
			require.NoError(t, err)
			op := model.BillingOperation{OperationKey: "request:owner-history-" + tc.name, UserId: f.site.OwnerUserId, SiteId: f.site.Id, PayerKind: model.BillingPayerOwner, PriceSnapshot: snapshot}
			require.NoError(t, f.db.Create(&op).Error)
			require.NoError(t, f.db.Create(&model.AdtySiteWalletLedger{SiteId: f.site.Id, WalletKind: model.AdtySiteWalletOperating, Type: "principal_consume", RefType: model.BillingPayerOwner, RefId: op.OperationKey, IdempotencyKey: op.OperationKey, Delta: decimal.RequireFromString("-0.8")}).Error)
			rows, _, err := ListAdtyOwnerSelfUseUsage(f.site.Id, f.site.OwnerUserId, 0, 20)
			require.NoError(t, err)
			require.NotEmpty(t, rows)
			require.Equal(t, op.OperationKey, rows[0].OperationKey)
			require.Equal(t, tc.wantUnit, rows[0].BillingUnit)
			require.Equal(t, tc.wantPrice, rows[0].SupplyUnitPrice)
		})
	}
}
