package service

import (
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// TestISS0198TerminalRecoverySamplesOncePG runs the recovery assertion on an
// isolated PostgreSQL schema. The package TestMain opens sqlite; this wrapper
// swaps that connection before the same success path runs.
func TestISS0198TerminalRecoverySamplesOncePG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(
		&model.Task{},
		&model.User{},
		&model.Token{},
		&model.Log{},
		&model.Channel{},
		&model.Midjourney{},
		&model.TopUp{},
		&model.SystemTask{},
		&model.SystemTaskLock{},
		&model.BillingOperation{},
		&model.BillingOperationMovement{},
		&model.AdtySiteEarningEvent{},
		&model.AdtyFundBatch{},
		&model.AdtyFundAllocation{},
		&model.AdtyBillingHold{},
		&model.PerfMetric{},
	))
	TestTerminalTaskRecoverySamplesOnceAndSettlesOnce(t)
}
