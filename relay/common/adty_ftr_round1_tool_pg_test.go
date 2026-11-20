package common

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestFTRADTY0608ToolCallBillingRulesPG(t *testing.T) {
	dsn := os.Getenv("ADTY_TEST_PG_DSN")
	if dsn == "" {
		t.Fatal("ADTY_TEST_PG_DSN is unset")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	pgtest.RecordServerVersion(t, db)

	operation_setting.SetToolPriceForTest("ftr0608_priced", 5)
	operation_setting.SetToolPriceForTest("ftr0608_free", 0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest("ftr0608_priced")
		operation_setting.DeleteToolPriceForTest("ftr0608_free")
	})

	info := &RelayInfo{OriginModelName: "gpt-5.1"}
	info.CountBillableToolCall(dto.BuildInCallFunctionCall, "")
	require.NotNil(t, info.ResponsesUsageInfo)
	assert.Empty(t, info.ResponsesUsageInfo.BuiltInTools)

	info.CountBillableToolCall(dto.BuildInCallFunctionCall, "ftr0608_free")
	if info.ResponsesUsageInfo != nil {
		assert.NotContains(t, info.ResponsesUsageInfo.BuiltInTools, "ftr0608_free")
	}

	info.CountBillableToolCall(dto.BuildInCallFunctionCall, "ftr0608_priced")
	info.CountBillableToolCall(dto.BuildInCallFunctionCall, "ftr0608_priced")
	require.Contains(t, info.ResponsesUsageInfo.BuiltInTools, "ftr0608_priced")
	assert.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools["ftr0608_priced"].CallCount)

	info.SetBillableToolCount("ftr0608_priced", -3)
	assert.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools["ftr0608_priced"].CallCount)
	info.SetBillableToolCount("ftr0608_priced", 0)
	assert.Equal(t, 0, info.ResponsesUsageInfo.BuiltInTools["ftr0608_priced"].CallCount)
	info.SetBillableToolCount("ftr0608_negative_only", -1)
	assert.NotContains(t, info.ResponsesUsageInfo.BuiltInTools, "ftr0608_negative_only")
}
