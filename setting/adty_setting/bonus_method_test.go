package adty_setting

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type testBonusOptionRow struct {
	Key   string `gorm:"primaryKey;column:key"`
	Value string `gorm:"column:value"`
}

func (testBonusOptionRow) TableName() string { return "options" }

type testBonusSiteRow struct {
	Id     int `gorm:"primaryKey;column:id"`
	Status int `gorm:"column:status"`
}

func (testBonusSiteRow) TableName() string { return "adty_sites" }

type testBonusOrderRow struct {
	TradeNo     string          `gorm:"primaryKey;column:trade_no"`
	SiteId      int             `gorm:"column:site_id"`
	BonusUSD    decimal.Decimal `gorm:"type:decimal(18,6);column:bonus_usd"`
	BonusMethod string          `gorm:"column:bonus_method"`
}

func (testBonusOrderRow) TableName() string { return "adty_topup_orders" }

func openBonusSettingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	var (
		db  *gorm.DB
		err error
	)
	if dsn := strings.TrimSpace(os.Getenv("ADTY_TEST_PG_DSN")); dsn != "" {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
		schema := fmt.Sprintf("adty_bonus_%d", os.Getpid())
		require.NoError(t, db.Exec("DROP SCHEMA IF EXISTS "+schema+" CASCADE").Error)
		require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
		require.NoError(t, db.Exec("SET search_path TO "+schema).Error)
		t.Cleanup(func() {
			_ = db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error
		})
	} else {
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
	}
	require.NoError(t, db.AutoMigrate(&testBonusOptionRow{}, &testBonusSiteRow{}, &testBonusOrderRow{}))
	SetAuthoritativeBonusDB(db)
	t.Cleanup(func() {
		SetAuthoritativeBonusDB(nil)
		topupSetting = TopupSetting{}
	})
	return db
}

// 三个入口都只「算」不「写」：以调用方给的当前值（生产里是事务内加锁读到的
// options 行）为基准合并，返回要落库的 JSON，内存由配置层在提交后从落库值同步。
func TestMergeMainAndSiteMethodBonus(t *testing.T) {
	topupSetting = TopupSetting{}
	value, err := MergeMainMethodBonus("", "usdt.trc20", true, "0.02")
	require.NoError(t, err)
	require.Contains(t, value, `"usdt"`)
	require.NoError(t, ApplyMethodBonusOption(TopupSettingKeyMethodBonus, value))
	rule, ok := MainMethodBonus("usdt")
	require.True(t, ok)
	require.True(t, rule.Enabled)
	require.Equal(t, "0.02", rule.Rate)

	_, err = MergeMainMethodBonus("", "paypal", true, "0.1")
	require.Error(t, err, "unsupported payment method")
	_, err = MergeSiteMethodBonus("", 9, "alipay", true, "0.1")
	require.Error(t, err, "a site cannot configure a method Root did not authorize")

	authValue, err := MergeSiteMethodAuth("", 9, "alipay", true)
	require.NoError(t, err)
	require.NoError(t, ApplyMethodBonusOption(TopupSettingKeySiteMethodAuth, authValue))
	require.True(t, SiteMethodBonusAllowed(9, "alipay"))

	siteValue, err := MergeSiteMethodBonus("", 9, "alipay", true, "0.1")
	require.NoError(t, err)
	require.NoError(t, ApplyMethodBonusOption(TopupSettingKeySiteMethodBonus, siteValue))
	saved, ok := SiteMethodBonus(9, "alipay")
	require.True(t, ok)
	require.Equal(t, "0.1", saved.Rate)
}

// ISS-R4-0099：合并的基准是库里读到的当前值，不是本进程的内存快照。另一个
// 副本刚写进去的规则必须原样留下，不能被自己这一项的整串覆盖抹掉。
func TestMergeKeepsRulesWrittenByAnotherNode(t *testing.T) {
	topupSetting = TopupSetting{}
	first, err := MergeMainMethodBonus("", "alipay", true, "0.02")
	require.NoError(t, err)
	second, err := MergeMainMethodBonus(first, "wxpay", true, "0.03")
	require.NoError(t, err)
	require.Contains(t, second, `"alipay"`, "另一节点写入的主站规则不能被覆盖")
	require.Contains(t, second, `"wxpay"`)

	auth, err := MergeSiteMethodAuth("", 7, "usdt", true)
	require.NoError(t, err)
	auth, err = MergeSiteMethodAuth(auth, 8, "usdt", true)
	require.NoError(t, err)
	require.NoError(t, ApplyMethodBonusOption(TopupSettingKeySiteMethodAuth, auth))
	require.True(t, SiteMethodBonusAllowed(7, "usdt"), "另一节点授权的站点不能被覆盖")
	require.True(t, SiteMethodBonusAllowed(8, "usdt"))

	site, err := MergeSiteMethodBonus("", 7, "usdt", true, "0.1")
	require.NoError(t, err)
	site, err = MergeSiteMethodBonus(site, 8, "usdt", true, "0.2")
	require.NoError(t, err)
	require.NoError(t, ApplyMethodBonusOption(TopupSettingKeySiteMethodBonus, site))
	rule7, ok := SiteMethodBonus(7, "usdt")
	require.True(t, ok, "另一节点写入的站点规则不能被覆盖")
	require.Equal(t, "0.1", rule7.Rate)
	rule8, ok := SiteMethodBonus(8, "usdt")
	require.True(t, ok)
	require.Equal(t, "0.2", rule8.Rate)
}

// 写进 options 的键必须和配置注册名一致，否则重启读不回来。
func TestTopupSettingKeysMatchJSONTags(t *testing.T) {
	require.Equal(t, "adty_topup_setting.method_bonus", TopupSettingKeyMethodBonus)
	require.Equal(t, "adty_topup_setting.site_method_auth", TopupSettingKeySiteMethodAuth)
	require.Equal(t, "adty_topup_setting.site_method_bonus", TopupSettingKeySiteMethodBonus)
	require.Equal(t, "adty_topup_setting.site_bonus_margin_fill", TopupSettingKeySiteBonusMarginFill)
}

// ISS-R1-0039：授权/比例快照与新建订单同一事务确认，跨节点撤权后不能凭陈旧内存放行，
// 且历史已创建订单保留原承诺加赠。
func TestBonusPolicySnapshotTransactionAndHistoricalOrderPreservation(t *testing.T) {
	db := openBonusSettingTestDB(t)
	require.NoError(t, db.Create(&testBonusSiteRow{Id: 12, Status: 1}).Error)
	require.NoError(t, db.Create(&testBonusSiteRow{Id: 13, Status: 2}).Error)

	// 1. Root authorizes site 12 for alipay in SQL; site 12 configures 10% bonus in transaction.
	authJSON, err := MergeSiteMethodAuth("", 12, "alipay", true)
	require.NoError(t, err)
	require.NoError(t, db.Create(&testBonusOptionRow{Key: TopupSettingKeySiteMethodAuth, Value: authJSON}).Error)
	require.NoError(t, ApplyMethodBonusOption(TopupSettingKeySiteMethodAuth, authJSON))

	var savedSiteBonus string
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var txErr error
		savedSiteBonus, txErr = LockAndMergeSiteMethodBonusTx(tx, 12, "alipay", true, "0.10")
		return txErr
	}))
	require.NoError(t, ApplyMethodBonusOption(TopupSettingKeySiteMethodBonus, savedSiteBonus))

	// 2. Create order #1 inside the same transaction as policy snapshot confirmation.
	var firstSnap BonusPolicySnapshot
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&testBonusOrderRow{TradeNo: "TRD-OLD-1", SiteId: 12}).Error; err != nil {
			return err
		}
		var txErr error
		firstSnap, txErr = CreateBonusOrderSnapshotTx(tx, "TRD-OLD-1", 12, false, "alipay", decimal.NewFromInt(100))
		return txErr
	}))
	require.True(t, firstSnap.SiteAuthorized)
	require.True(t, firstSnap.Enabled)
	require.True(t, decimal.RequireFromString("10").Equal(firstSnap.BonusUSD))
	require.Equal(t, int64(5_000_000), firstSnap.BonusQuota)

	// 3. Another node revokes Root authorization in SQL, while local process memory is still stale.
	revokedAuth, err := MergeSiteMethodAuth(authJSON, 12, "alipay", false)
	require.NoError(t, err)
	require.NoError(t, db.Model(&testBonusOptionRow{}).
		Where("key = ?", TopupSettingKeySiteMethodAuth).
		Update("value", revokedAuth).Error)
	// Do NOT call ApplyMethodBonusOption here: simulate stale local memory.

	require.False(t, SiteMethodBonusAllowed(12, "alipay"), "authoritative SQL must win over stale local memory")
	_, ok := SiteMethodBonus(12, "alipay")
	require.False(t, ok, "revoked method must not return active site bonus")

	// Site owner attempting to update alipay bonus in a transaction must be rejected.
	err = db.Transaction(func(tx *gorm.DB) error {
		_, txErr := LockAndMergeSiteMethodBonusTx(tx, 12, "alipay", true, "0.20")
		return txErr
	})
	require.Error(t, err)

	// 4. New order #2 created after revocation gets zero bonus in its creation transaction.
	var secondSnap BonusPolicySnapshot
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&testBonusOrderRow{TradeNo: "TRD-NEW-2", SiteId: 12}).Error; err != nil {
			return err
		}
		var txErr error
		secondSnap, txErr = CreateBonusOrderSnapshotTx(tx, "TRD-NEW-2", 12, false, "alipay", decimal.NewFromInt(100))
		return txErr
	}))
	require.False(t, secondSnap.SiteAuthorized)
	require.False(t, secondSnap.Enabled)
	require.True(t, secondSnap.BonusUSD.IsZero())

	// 5. Historical order #1 still keeps its original promised bonus snapshot.
	var oldOrder, newOrder testBonusOrderRow
	require.NoError(t, db.Where("trade_no = ?", "TRD-OLD-1").First(&oldOrder).Error)
	require.NoError(t, db.Where("trade_no = ?", "TRD-NEW-2").First(&newOrder).Error)
	require.True(t, decimal.RequireFromString("10").Equal(oldOrder.BonusUSD))
	require.Equal(t, "alipay", oldOrder.BonusMethod)
	require.True(t, newOrder.BonusUSD.IsZero())
	require.Equal(t, "", newOrder.BonusMethod)

	// 6. Disabled site (status != 1) is rejected when creating a bonus policy snapshot.
	err = db.Transaction(func(tx *gorm.DB) error {
		_, txErr := LoadBonusPolicySnapshotTx(tx, 13, false, "alipay", decimal.NewFromInt(50), true)
		return txErr
	})
	require.Error(t, err, "inactive site must be rejected")
}
