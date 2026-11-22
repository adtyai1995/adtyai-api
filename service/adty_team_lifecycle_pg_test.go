package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTeamLifecycleAndOriginalPrincipal(t *testing.T) {
	dsn := os.Getenv("ADTY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL 18 via ADTY_TEST_PG_DSN")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pool, err := admin.DB()
	require.NoError(t, err)
	defer pool.Close()
	var version int
	require.NoError(t, admin.Raw("SHOW server_version_num").Scan(&version).Error)
	require.GreaterOrEqual(t, version, 180000)
	require.Less(t, version, 190000)
	t.Logf("server_version_num=%d", version)
	schema := fmt.Sprintf("team_lifecycle_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		q.Set("client_encoding", "UTF8")
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " client_encoding=UTF8 search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pg, err := db.DB()
	require.NoError(t, err)
	defer pg.Close()

	previousDB, previousType := model.DB, common.MainDatabaseType()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	defer func() { model.DB = previousDB; common.SetMainDatabaseType(previousType) }()
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.TopUp{}, &model.Token{}))
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = oldRedis }()
	oldLog := model.LOG_DB
	model.LOG_DB = db
	defer func() { model.LOG_DB = oldLog }()
	require.NoError(t, db.AutoMigrate(&model.AuditIntent{}, &model.AuditLog{}))
	root := model.User{Username: "root_test", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	owner := model.User{Username: "owner_test", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "teamreg", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	leader := model.User{Username: "leader_test", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: site.Id}
	require.NoError(t, db.Create(&leader).Error)
	created, err := CreateAdtySiteTeamByRoot(root.Id, site.Id, leader.Id, "early team", "1", "create1")
	require.NoError(t, err)
	teamID := created.Team.Id
	replay, err := CreateAdtySiteTeamByRoot(root.Id, site.Id, leader.Id, "early team", "1", "create1")
	require.NoError(t, err)
	require.Equal(t, teamID, replay.Team.Id)
	_, err = CreateAdtySiteTeamByRoot(root.Id, 0, leader.Id, "wrong site", "1", "create2")
	require.Error(t, err)
	for i := 0; i < 50; i++ {
		require.NoError(t, db.Create(&model.AdtyTeam{SiteId: site.Id, Name: fmt.Sprintf("later %d", i), Discount: decimal.NewFromInt(1), Status: model.AdtyTeamStatusActive}).Error)
	}
	teams, total, err := ListAdtyOwnerSiteTeams(site.Id, owner.Id, 50, 10, "")
	require.NoError(t, err)
	require.EqualValues(t, 51, total)
	require.Equal(t, teamID, teams[0].Id)
	teams, total, err = ListAdtyOwnerSiteTeams(site.Id, owner.Id, 0, 10, "early team")
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, teamID, teams[0].Id)
	require.Error(t, SaveAdtyOwnerSiteTeamDiscount(site.Id, owner.Id, teamID, "0.00001"))
	require.NoError(t, SaveAdtyOwnerSiteTeamDiscount(site.Id, owner.Id, teamID, "0.0001"))
	var stored model.AdtyTeam
	require.NoError(t, db.First(&stored, teamID).Error)
	require.Equal(t, "0.0001", stored.Discount.String())
	pricingToken := model.Token{UserId: leader.Id, Key: "initial-site-price", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
	require.NoError(t, pricingToken.InsertWithLimit(10))
	require.NoError(t, BindAdtyTeamKey(leader.Id, pricingToken.Id, teamID))
	require.NoError(t, db.Model(&site).Update("cost_factor", "0.7").Error)
	priceCtx := &gin.Context{}
	common.SetContextKey(priceCtx, constant.ContextKeyTokenId, pricingToken.Id)
	billed, _ := adtyApplyTeamProtocolPriceDecimal(priceCtx, leader.Id, decimal.NewFromInt(1), decimal.NewFromInt(2), decimal.NewFromInt(2))
	require.Equal(t, "0.7", billed.String())
	require.NoError(t, db.Model(&stored).Update("discount", 0).Error)
	adtyApplyTeamProtocolPriceDecimal(priceCtx, leader.Id, decimal.NewFromInt(1), decimal.NewFromInt(2), decimal.NewFromInt(2))
	require.True(t, priceCtx.GetBool(AdtyTeamPriceBlockedKey))
	require.NoError(t, SaveAdtyOwnerSiteTeamDiscount(site.Id, owner.Id, teamID, "1"))
	invite, err := CreateAdtyTeamInvite(leader.Id, teamID, time.Hour)
	require.NoError(t, err)
	employee, err := CreateAdtyTeamEmployee(leader.Id, teamID, "teamemployee", "Somepassword123!", "Staff")
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return InheritAdtyTeamsOnCloseTx(tx, site.Id) }))
	require.NoError(t, db.Model(&model.User{}).Where("site_id = ?", site.Id).Update("site_id", 0).Error)
	_, err = RegisterAdtyTeamEmployee(invite.Code, "forbiddenstaff", "Somepassword123!", "Staff", 0)
	require.Error(t, err)
	invite, err = CreateAdtyTeamInvite(leader.Id, teamID, time.Hour)
	require.NoError(t, err)
	_, err = RegisterAdtyTeamEmployee(invite.Code, "freshstaff", "Somepassword123!", "Staff", 0)
	require.NoError(t, err)
	token := model.Token{UserId: employee.UserId, Key: "team_employee_key", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
	require.NoError(t, token.InsertWithLimit(10))
	snap, err := model.CaptureBillingPayer(token.UserId, token.Id)
	require.NoError(t, err)
	require.Equal(t, teamID, snap.PayerTeamId)
	legacy := model.Token{UserId: employee.UserId, Key: "unbound_employee_key", Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, db.Create(&legacy).Error)
	require.Error(t, model.ValidateAdtyTeamTokenTx(db, &legacy))
	_, err = model.CaptureBillingPayer(legacy.UserId, legacy.Id)
	require.Error(t, err)
	leaderToken := model.Token{UserId: leader.Id, Key: "leader_key", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
	require.NoError(t, leaderToken.InsertWithLimit(10))
	personal, err := model.CaptureBillingPayer(leader.Id, leaderToken.Id)
	require.NoError(t, err)
	require.Equal(t, model.BillingPayerPersonal, personal.PayerKind)
	require.NoError(t, BindAdtyTeamKey(leader.Id, leaderToken.Id, teamID))
	const unit = 500000
	credit := func(trade string, n int) {
		order := model.TopUp{TradeNo: trade, UserId: leader.Id, Status: common.TopUpStatusSuccess}
		ext := model.AdtyTopupOrder{TradeNo: trade, TeamId: teamID, UserId: leader.Id, FaceUSD: decimal.NewFromInt(int64(n)), Provider: model.PaymentProviderAdtyEpay, CreditQuota: int64(n * unit)}
		require.NoError(t, db.Create(&order).Error)
		require.NoError(t, db.Create(&ext).Error)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return creditAdtyTeamTopUpTx(tx, &order, &ext, int64(n*unit)) }))
	}
	reserve := func(key string, tok *model.Token, n int) {
		op, err := model.ReserveBillingOperation(model.BillingOperation{OperationKey: key, UserId: tok.UserId, TokenId: tok.Id, TokenUnlimited: tok.UnlimitedQuota}, n*unit)
		require.NoError(t, err)
		require.Equal(t, teamID, op.PayerTeamId)
	}
	settle := func(key string, target int) *model.BillingOperation {
		require.NoError(t, model.QueueBillingSettlement(key, target*unit, false))
		op, _, err := model.ApplyBillingSettlement(key)
		require.NoError(t, err)
		return op
	}
	credit("ORDER_A", 100)
	reserve("request:first", &token, 100)
	settled := settle("request:first", 110)
	require.Equal(t, 10*unit, settled.Shortfall)
	credit("ORDER_B", 100)
	reserve("request:second", &leaderToken, 30)
	credit("ORDER_C", 100)
	reserve("request:third", &token, 90)
	settle("request:third", 75)
	settle("request:second", 10)
	refund := func(trade string, n int, want int) {
		id := "refund-" + trade
		row := model.AdtyManualRefund{TradeNo: trade, RefundId: id, UserId: leader.Id, Status: model.AdtyManualRefundStatusApproved, ApprovedAt: 1, RequestQuota: int64(n * unit), RequestUSD: decimal.NewFromInt(int64(n))}
		require.NoError(t, db.Create(&row).Error)
		result, err := model.ReclaimAdtyTopUpManualRefund(trade, id, root.Id, "reclaim-"+trade)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NoError(t, db.First(&row, row.Id).Error)
		require.EqualValues(t, want*unit, row.DeductedPrincipalQuota)
		require.EqualValues(t, (n-want)*unit, row.ShortfallQuota)
	}
	refund("ORDER_A", 100, 0)
	refund("ORDER_B", 100, 20)
	var lot model.AdtyTeamFundLot
	require.NoError(t, db.Where("source = ?", "ORDER_C").First(&lot).Error)
	require.EqualValues(t, 95*unit, lot.Available)
	require.NoError(t, RevokeAdtyTeamKey(leader.Id, teamID, token.Id))
	require.NoError(t, db.Model(&token).Update("status", common.TokenStatusEnabled).Error)
	require.Error(t, BindAdtyTeamKey(token.UserId, token.Id, teamID))
	require.Error(t, model.ValidateAdtyTeamTokenTx(db, &token))
	require.NoError(t, CloseAdtyTeam(leader.Id, teamID))
	require.Error(t, model.ValidateAdtyTeamTokenTx(db, &leaderToken))
}
