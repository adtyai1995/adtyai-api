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
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	defer func() { model.LOG_DB = oldLog }()
	require.NoError(t, db.AutoMigrate(&model.AuditIntent{}, &model.AuditLog{}, &model.Log{}, &model.UserAccessToken{}, &model.UserSession{}, &model.ExternalIdentityClaim{}))
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
		ext := model.AdtyTopupOrder{TradeNo: trade, TeamId: teamID, UserId: leader.Id, FaceUSD: decimal.NewFromInt(int64(n)), Provider: model.PaymentProviderAdtyEpay, Method: "alipay", PayAmount: decimal.NewFromInt(int64(n * 7)), PaidAmount: decimal.NewFromInt(int64(n * 7)), PayCurrency: "CNY", PaidCurrency: "CNY", CreditQuota: int64(n * unit)}
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
	credit("ORDER_T", 100)
	original := "refund-termination"
	_, err = model.ApplyAdtyTopUpManualRefund("ORDER_T", "team refund", root.Id, original, decimal.NewFromInt(40))
	require.NoError(t, err)
	_, err = model.ApproveAdtyTopUpManualRefund("ORDER_T", original, root.Id, "approve-t", "reviewed")
	require.NoError(t, err)
	_, err = model.ReclaimAdtyTopUpManualRefund("ORDER_T", original, root.Id, "reclaim-t")
	require.NoError(t, err)
	_, err = model.RecordAdtyTopUpManualRefundPayout("ORDER_T", original, root.Id, "payout-t", model.AdtyManualRefundPayout{Status: model.AdtyManualRefundPayoutFailed})
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = model.TerminateAdtyTopUpManualRefund("ORDER_T", original, root.Id, "terminate-t", "failed")
		require.NoError(t, err)
	}
	facts, err := model.LoadAdtyRefundPageFacts("ORDER_T", decimal.Zero)
	require.NoError(t, err)
	require.Equal(t, "100", facts.PrincipalRemainingUSD)
	require.NoError(t, db.First(&stored, teamID).Error)
	require.EqualValues(t, 100*unit, stored.SharedQuota)
	var reversals int64
	require.NoError(t, db.Model(&model.AdtyTeamLedger{}).Where("ref_id = ? AND delta_quota = ?", original, 40*unit).Count(&reversals).Error)
	require.EqualValues(t, 1, reversals)
	reserve("request:clear", &token, 100)
	settle("request:clear", 100)
	for _, split := range []bool{false, true} {
		trade := fmt.Sprintf("STREAM_%t", split)
		if split {
			credit(trade, 25)
			credit(trade+"_B", 75)
		} else {
			credit(trade, 100)
		}
		rootKey := "request:" + trade
		reserve(rootKey, &token, 0)
		for i, qty := range []int{18, 12} {
			child := model.BillingOperation{OperationKey: fmt.Sprintf("direct:%s:%d", trade, i), EarningRootKey: rootKey, UserId: token.UserId, TokenId: token.Id, TokenUnlimited: true}
			require.NoError(t, model.QueueDirectBillingDelta(child, qty*unit))
			_, _, err := model.ApplyBillingSettlement(child.OperationKey)
			require.NoError(t, err)
		}
		require.NoError(t, model.QueueBillingSettlement(rootKey, 20*unit, false, 30*unit, 0))
		for i := 0; i < 2; i++ {
			_, _, err := model.ApplyBillingSettlement(rootKey)
			require.NoError(t, err)
		}
		facts, err := model.LoadAdtyRefundPageFacts(trade, decimal.Zero)
		require.NoError(t, err)
		if split {
			require.Equal(t, "5", facts.PrincipalRemainingUSD)
			facts, err = model.LoadAdtyRefundPageFacts(trade+"_B", decimal.Zero)
			require.NoError(t, err)
			require.Equal(t, "75", facts.PrincipalRemainingUSD)
		} else {
			require.Equal(t, "80", facts.PrincipalRemainingUSD)
		}
		require.NoError(t, db.First(&stored, teamID).Error)
		require.EqualValues(t, 80*unit, stored.SharedQuota)
		reserve(rootKey+"-clear", &token, 80)
		settle(rootKey+"-clear", 80)
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
	for _, state := range []string{"disabled", "expired", "deleted"} {
		key := model.Token{UserId: employee.UserId, Key: "state-" + state, Name: "retained-" + state, Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
		require.NoError(t, key.InsertWithLimit(30))
		switch state {
		case "disabled":
			key.Status = common.TokenStatusDisabled
			require.NoError(t, key.SelectUpdate())
		case "expired":
			require.NoError(t, db.Model(&key).Update("expired_time", common.GetTimestamp()-5).Error)
		case "deleted":
			require.NoError(t, key.Delete())
		}
		page, err := GetAdtyTeamMine(leader.Id)
		require.NoError(t, err)
		var matched bool
		for _, row := range page.Keys {
			if row.TokenId == key.Id {
				require.Equal(t, key.Name, row.Name)
				require.Equal(t, state, row.Status)
				matched = true
			}
		}
		require.True(t, matched)
		_, err = model.ValidateUserToken(key.Key)
		require.Error(t, err)
	}
	t.Run("recover-checkout", func(t *testing.T) { testTeamCheckoutRecovery(t, db, teamID, leader.Id) })
	for _, action := range []string{"disable", "leave", "close"} {
		staff, err := CreateAdtyTeamEmployee(leader.Id, teamID, "race"+action, "Somepassword123!", action)
		require.NoError(t, err)
		old := model.Token{Key: "old-" + action, UserId: staff.UserId, UnlimitedQuota: true, ExpiredTime: -1}
		require.NoError(t, old.InsertWithLimit(30))
		start := make(chan struct{})
		result := make(chan error, 8)
		for i := 0; i < 8; i++ {
			go func(n int) {
				<-start
				key := model.Token{Key: fmt.Sprintf("%s-%d", action, n), UserId: staff.UserId, UnlimitedQuota: true, ExpiredTime: -1}
				result <- key.InsertWithLimit(30)
			}(i)
		}
		close(start)
		switch action {
		case "disable":
			err = DisableAdtyTeamMember(leader.Id, teamID, staff.UserId)
		case "leave":
			err = LeaveAdtyTeam(staff.UserId, teamID)
		case "close":
			err = CloseAdtyTeam(leader.Id, teamID)
		}
		require.NoError(t, err)
		for i := 0; i < 8; i++ {
			if err := <-result; err != nil {
				require.ErrorIs(t, err, model.ErrTokenInvalid)
			}
		}
		var keys []model.Token
		require.NoError(t, db.Where("user_id = ?", staff.UserId).Find(&keys).Error)
		for _, key := range keys {
			_, err := model.ValidateUserToken(key.Key)
			require.Error(t, err)
		}
	}
	late := model.AdtyTopupOrder{TradeNo: "LATE", SiteId: site.Id, TeamId: teamID, UserId: leader.Id}
	require.NoError(t, db.Create(&late).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return creditAdtyTeamTopUpTx(tx, &model.TopUp{UserId: leader.Id}, &late, unit)
	}))
	mainOrder := model.AdtyTopupOrder{TradeNo: "LEGACY_MAIN", TeamId: teamID, UserId: leader.Id}
	require.NoError(t, db.Create(&mainOrder).Error)
	require.NoError(t, db.Create(&model.TopUp{TradeNo: mainOrder.TradeNo, UserId: leader.Id, Status: common.TopUpStatusSuccess}).Error)
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	for _, tt := range []struct {
		site        int
		trade, kind string
	}{{site.Id, "LATE", "closed_team_topup"}, {0, "LEGACY_MAIN", "team_principal_review"}} {
		rows, err := ListAdtyAnomalyDispositions(tt.site)
		require.NoError(t, err)
		var found bool
		for _, row := range rows {
			if row.OrderRef == tt.trade {
				require.Equal(t, teamID, row.TeamId)
				require.Equal(t, tt.kind, row.Kind)
				require.Equal(t, "open", row.Status)
				found = true
			}
		}
		require.True(t, found)
	}
	for _, trade := range []string{"ORDER_A", "ORDER_B"} {
		_, err = model.TerminateAdtyTopUpManualRefund(trade, "refund-"+trade, root.Id, "stop-"+trade, "abandoned")
		require.NoError(t, err)
	}
	_, err = model.DeleteUserById(leader.Id)
	require.NoError(t, err)
	preview, err := model.PreviewAdtyTopUpRefund("ORDER_T")
	require.NoError(t, err)
	require.Equal(t, "0", preview.Facts.PrincipalRemainingUSD)
	require.Len(t, preview.Facts.ManualRefunds, 1)
	require.Equal(t, model.AdtyManualRefundStatusTerminated, preview.Facts.ManualRefunds[0].Status)
	require.Error(t, model.ValidateAdtyTeamTokenTx(db, &leaderToken))
}
