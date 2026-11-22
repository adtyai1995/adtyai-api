package model

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

// adtyExit 留给测试替换。生产环境直接退出，不能改用 FatalLog，否则测试挡不住。
var adtyExit = func(code int) {
	os.Exit(code)
}

// adtyDSNIsPostgreSQL 和 chooseDB 用同一个判断。关键字形式也是 PostgreSQL。
func adtyDSNIsPostgreSQL(dsn string) bool {
	return IsPostgresDSN(dsn)
}

// CheckAdtyPostgreSQL 在连库之前检查 SQL_DSN 和 LOG_SQL_DSN：不是 PostgreSQL 就写明是哪一个并退出。
// LOG_SQL_DSN 为空时日志和主库同库，不单独检查。
func CheckAdtyPostgreSQL() {
	var problems []string
	if !adtyDSNIsPostgreSQL(os.Getenv("SQL_DSN")) {
		problems = append(problems, "SQL_DSN")
	}
	if logDSN := os.Getenv("LOG_SQL_DSN"); logDSN != "" && !adtyDSNIsPostgreSQL(logDSN) {
		problems = append(problems, "LOG_SQL_DSN")
	}
	if len(problems) == 0 {
		return
	}
	common.SysError("ADTY requires PostgreSQL; check " + strings.Join(problems, " and "))
	adtyExit(1)
}

// MigrateAdtyTables 用包级 DB 建二开的表。上游的 migrateDB 只把它挂在主节点上；
// 上游的 SQLite 测试也会走到这里，那里同样只用 AutoMigrate。
func MigrateAdtyTables() error {
	return MigrateAdtyTablesOn(DB)
}

// MigrateAdtyTablesOn 在指定连接上建二开的表。
//
// 测试必须用这个：`migrateAdtyTestSchema` 的调用方传进来的是它们自己的库，
// 不一定是包级 DB。只写全局版本时函数会「成功」地迁移了另一套表，调用方随后
// 撞「表不存在」，现象和没修一模一样。
func MigrateAdtyTablesOn(db *gorm.DB) error {
	if err := db.AutoMigrate(&AdtyPaymentSceneConfig{}); err != nil {
		return err
	}
	if err := seedAdtyPaymentScenesOn(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(&AdtyGroup{}, &AdtyTopupOrder{}, &AdtyOrderPaymentEvent{}, &PaymentOrphanEvent{}, &PaymentOrphanReview{}, &AdtyBalanceLedger{}, &AdtyFinancialAdjustment{}, &BillingOperation{}, &BillingOperationMovement{}, &AdtySiteEarningEvent{}, &AdtyEpayChannel{}, &AdtyEpayChannelMethod{}, &AdtyUserFingerprint{}, &AdtyUserLimit{}, &AdtyOfficialPrice{}, &AdtyBillingHold{}, &AdtyFundBatch{}, &AdtyFundAllocation{}, &AdtyGiftFulfillment{}, &AdtyManualRefund{}, &AdtyStaffBalanceRequest{}, &AdtyPriceRevision{}, &AdtyNotificationHide{}, &AdtyGlossaryTerm{}, &AdtySiteReferralRate{}, &AdtySiteInvite{}, &AdtySiteInviteCommission{}, &AdtyCommissionRecovery{}, &AdtyCommissionCoverageCancellation{}, &AdtySiteInviteWithdrawal{}, &AdtySiteInviteWithdrawalItem{}, &AdtyAnomalyDisposition{}, &AdtyEmailChange{}, &AdtyAccountClosure{}, &AdtyNoticeTemplate{}, &AdtyKeyLimit{}, &AdtyModelAlias{}, &AdtyGroupConfig{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&AdtyNotificationRead{}, &AdtyNotificationPublication{}, &AdtyVoucher{}, &AdtyVoucherLiability{}, &AdtyVoucherAuthorization{}, &AdtyVoucherCloseGrant{}, &AdtyTeam{}, &AdtyTeamMember{}, &AdtyTeamLedger{}, &AdtyTeamFundLot{}, &AdtyTeamFundAllocation{}, &AdtyTeamInvite{}, &AdtyTeamRegistrationLink{}, &AdtyTeamKey{}); err != nil {
		return err
	}
	// A surviving subsite team has never migrated; its original invite scope
	// is recoverable. For teams already on main, pre-binding credentials
	// without evidence must be reissued.
	if err := db.Exec(`UPDATE adty_team_invites SET site_id=(SELECT site_id FROM adty_teams WHERE id=adty_team_invites.team_id), purpose=?
       WHERE purpose='' AND used_at=0 AND revoked_at=0 AND EXISTS
       (SELECT 1 FROM adty_teams t WHERE t.id=adty_team_invites.team_id AND t.site_id>0 AND t.status=?)`, AdtyTeamEmployeeInvitePurpose, AdtyTeamStatusActive).Error; err != nil {
		return err
	}
	if err := db.Model(&AdtyTeamInvite{}).Where("purpose = '' AND used_at = 0 AND revoked_at = 0").Update("revoked_at", time.Now().Unix()).Error; err != nil {
		return err
	}
	// Repair the scope on previous releases' team anomalies from the original
	// order. Preserve their disposition, operator and review notes.
	if err := db.Exec(`UPDATE adty_anomaly_dispositions SET
		site_id = o.site_id, team_id = o.team_id, order_ref = o.trade_no, account_id = o.user_id
		FROM adty_topup_orders o WHERE adty_anomaly_dispositions.ref = o.trade_no AND o.team_id > 0
		AND adty_anomaly_dispositions.team_id = 0 AND adty_anomaly_dispositions.kind IN ('closed_team_topup', 'team_principal_review')`).Error; err != nil {
		return err
	}
	// Historical team balances cannot identify which recharge survived. Surface
	// each paid legacy order once in the existing reconciliation work queue.
	if db.Migrator().HasTable(&TopUp{}) {
		if err := db.Exec(`INSERT INTO adty_anomaly_dispositions (kind,ref,order_ref,site_id,team_id,account_id,status,note,created_at)
          SELECT 'team_principal_review',o.trade_no,o.trade_no,o.site_id,o.team_id,o.user_id,'open','Historical team principal requires order-level review',?
          FROM adty_topup_orders o JOIN top_ups t ON t.trade_no=o.trade_no
          WHERE o.team_id>0 AND t.status=? AND NOT EXISTS (SELECT 1 FROM adty_team_fund_lots l WHERE l.team_id=o.team_id AND l.kind='topup' AND l.source=o.trade_no)
          AND NOT EXISTS (SELECT 1 FROM adty_anomaly_dispositions a WHERE a.kind='team_principal_review' AND a.ref=o.trade_no)`, time.Now().Unix(), common.TopUpStatusSuccess).Error; err != nil {
			return err
		}
	}
	voucherIndexErr := EnsureAdtyVoucherTradeNoUniqueIndex(db)
	teamMemberIndexErr := EnsureActiveTeamMembershipUniqueIndex(db)
	installAdtyUniqueConstraintWriteGates(db, voucherIndexErr, teamMemberIndexErr)
	if err := db.AutoMigrate(&AdtyTelegramBot{}, &AdtyTelegramChallenge{}, &AdtyTelegramBinding{}, &AdtyTelegramDelivery{}, &AdtyTicket{}, &AdtyTicketMessage{}, &AdtyTicketImage{}, &AdtyTicketEvent{}, &AdtyTicketNotice{}, &AdtyTicketPolicy{}, &AdtyTranslationConfig{}, &AdtyTranslationHead{}, &AdtyTranslationJob{}, &AdtyTranslationUsage{}, &AdtyGroupHealthSample{}, &AdtyGroupHealthState{}, &AdtyGroupHealthAlert{}, &AdtyDataRetentionPolicy{}, &AdtyDataRetentionRun{}, &AdtyDataRetentionRunItem{}); err != nil {
		return errors.Join(voucherIndexErr, teamMemberIndexErr, err)
	}
	if err := RemapAdtyTicketLegacyStatuses(db); err != nil {
		return errors.Join(voucherIndexErr, teamMemberIndexErr, err)
	}
	if err := BackfillAdtyTicketImageVisibility(db); err != nil {
		return errors.Join(voucherIndexErr, teamMemberIndexErr, err)
	}
	if err := migrateAdtyReferralTablesOn(db); err != nil {
		return errors.Join(voucherIndexErr, teamMemberIndexErr, err)
	}
	if err := migrateAdtySiteTablesOn(db); err != nil {
		return errors.Join(voucherIndexErr, teamMemberIndexErr, err)
	}
	return errors.Join(voucherIndexErr, teamMemberIndexErr, TransferAdtyTicketsToMain(db, db.Model(&AdtySite{}).Select("id").Where("status = ?", AdtySiteStatusClosed)))
}

const (
	adtyUniqueConstraintCreateGateCallback = "adty:unique_constraint_write_gate_create"
	adtyUniqueConstraintUpdateGateCallback = "adty:unique_constraint_write_gate_update"
)

func installAdtyUniqueConstraintWriteGates(db *gorm.DB, voucherErr, teamMemberErr error) {
	if db == nil {
		return
	}
	gate := func(tx *gorm.DB) {
		if tx == nil || tx.Statement == nil {
			return
		}
		table := tx.Statement.Table
		if table == "" && tx.Statement.Schema != nil {
			table = tx.Statement.Schema.Table
		}
		switch table {
		case (AdtyVoucher{}).TableName():
			if voucherErr != nil {
				_ = tx.AddError(voucherErr)
			}
		case (AdtyTeamMember{}).TableName():
			if teamMemberErr != nil {
				_ = tx.AddError(teamMemberErr)
			}
		}
	}
	if db.Callback().Create().Get(adtyUniqueConstraintCreateGateCallback) == nil {
		_ = db.Callback().Create().Before("gorm:create").Register(adtyUniqueConstraintCreateGateCallback, gate)
	} else {
		_ = db.Callback().Create().Replace(adtyUniqueConstraintCreateGateCallback, gate)
	}
	if db.Callback().Update().Get(adtyUniqueConstraintUpdateGateCallback) == nil {
		_ = db.Callback().Update().Before("gorm:update").Register(adtyUniqueConstraintUpdateGateCallback, gate)
	} else {
		_ = db.Callback().Update().Replace(adtyUniqueConstraintUpdateGateCallback, gate)
	}
}

// migrateAdtySiteTables 建分站的表（P11）。
func migrateAdtySiteTables() error {
	return migrateAdtySiteTablesOn(DB)
}

func migrateAdtySiteTablesOn(db *gorm.DB) error {
	// AutoMigrate creates the new owner-aware unique index before we remove
	// the old constraint. A failed migration therefore never loses uniqueness.
	if err := db.AutoMigrate(AdtySiteTables()...); err != nil {
		return err
	}
	// The replacement unique index includes the notice revision. Historical rows
 // are revision one and retain their original batch and provider keys.
 if db.Migrator().HasIndex(&AdtySiteCloseMailBatch{},"idx_adty_site_close_mail_batches_site_id"){
 if err:=db.Migrator().DropIndex(&AdtySiteCloseMailBatch{},"idx_adty_site_close_mail_batches_site_id");err!=nil{return err}
 }
 // Pre-attempt recalls left a reviewer and reason on a pending withdrawal.
	// Keep these holds until the original external result is reconciled.
	if err := db.Model(&AdtySiteWithdrawal{}).Where("attempt_number = 0 AND attempt_open = ? AND (status = ? OR (status = ? AND reviewed_at > 0 AND proof_note <> ''))", false, AdtySiteWithdrawPaying, AdtySiteWithdrawPending).Where("NOT EXISTS (?)", db.Model(&AdtySitePayoutAttempt{}).Select("1").Where("withdrawal_id = adty_site_withdrawals.id")).Update("attempt_open", true).Error; err != nil {
		return err
	}
	if err := backfillCommissionCoverageCancellations(db, time.Now().Unix()); err != nil {
		return err
	}
	if err := backfillAdtySiteMarketingPolicies(db); err != nil {
		return err
	}
	if err := EnsureAdtySitePriceUniqueIndex(db); err != nil {
		return err
	}
	if err := BackfillAdtySitePriceScopes(db); err != nil {
		return err
	}
	if err := BackfillAdtyUserSiteMigrationsFromInheritedSiteID(db, time.Now().Unix()); err != nil {
		return err
	}
	if err := BackfillAdtyTeamSourceSiteID(db); err != nil {
		return err
	}
	if db.Migrator().HasIndex(&AdtySiteEarningDaily{}, "idx_site_earning") {
		return db.Migrator().DropIndex(&AdtySiteEarningDaily{}, "idx_site_earning")
	}
	return nil
}

func migrateAdtyReferralTablesOn(db *gorm.DB) error {
	return db.AutoMigrate(ReferralTables()...)
}

// migrateAdtyTables 是挂到上游迁移上的同名入口，测试也直接调用它。
func migrateAdtyTables() error {
	return MigrateAdtyTables()
}

// SeedAdtyGroups 只在表里还没有任何行（含已软删除）时插入，要在设置载入之后调用。
// 已有名字不覆盖，设置里后加的键也不补。
func SeedAdtyGroups() error {
	var count int64
	if err := DB.Unscoped().Model(&AdtyGroup{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	groups, err := adtyGroupSeedSources()
	if err != nil {
		return err
	}
	if _, ok := groups[AdtyDefaultGroupID]; !ok {
		groups[AdtyDefaultGroupID] = ""
	}
	rows := make([]AdtyGroup, 0, len(groups))
	for id, desc := range groups {
		rows = append(rows, AdtyGroup{
			ID:    id,
			Names: adtyFillLangs(id),
			Descs: adtyFillLangs(desc),
		})
	}
	return DB.Create(&rows).Error
}

// ClearAdtyPlaceholderGroupRatio 去掉上游默认 GroupGroupRatio 里的 edit_this。
// 它不是分组，留着的话分组校验上线后这个配置一保存就会被拒。
func ClearAdtyPlaceholderGroupRatio() error {
	nested := map[string]map[string]float64{}
	if err := adtyUnmarshalObject(ratio_setting.GroupGroupRatio2JSONString(), &nested); err != nil {
		return err
	}
	changed := false
	for outer, inner := range nested {
		if _, ok := inner["edit_this"]; !ok {
			continue
		}
		delete(inner, "edit_this")
		changed = true
		if len(inner) == 0 {
			delete(nested, outer)
		}
	}
	if !changed {
		return nil
	}
	raw, err := common.Marshal(nested)
	if err != nil {
		return err
	}
	return UpdateOption("GroupGroupRatio", string(raw))
}

func adtyUnmarshalObject(raw string, dest any) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return common.Unmarshal([]byte(raw), dest)
}

func adtyGroupSeedSources() (map[string]string, error) {
	groups := map[string]string{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if !adtySeedableGroupID(id) {
			return
		}
		if _, ok := groups[id]; !ok {
			groups[id] = ""
		}
	}
	for id := range ratio_setting.GetGroupRatioCopy() {
		add(id)
	}
	for id, desc := range setting.GetUserUsableGroupsCopy() {
		add(id)
		if !adtySeedableGroupID(id) {
			continue
		}
		desc = strings.TrimSpace(desc)
		if desc != "" {
			groups[id] = desc
		}
	}
	topup := map[string]float64{}
	if err := adtyUnmarshalObject(common.TopupGroupRatio2JSONString(), &topup); err != nil {
		return nil, err
	}
	for id := range topup {
		add(id)
	}
	for _, id := range setting.GetAutoGroups() {
		add(id)
	}
	nested := map[string]map[string]float64{}
	if err := adtyUnmarshalObject(ratio_setting.GroupGroupRatio2JSONString(), &nested); err != nil {
		return nil, err
	}
	for outer, inner := range nested {
		add(outer)
		for id := range inner {
			add(id)
		}
	}
	special := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	if special != nil {
		for owner, inner := range special.ReadAll() {
			add(owner)
			for key := range inner {
				add(adtyStripGroupPrefix(key))
			}
		}
	}
	limits := map[string][2]int{}
	if err := adtyUnmarshalObject(setting.ModelRequestRateLimitGroup2JSONString(), &limits); err != nil {
		return nil, err
	}
	for id := range limits {
		add(id)
	}
	return groups, nil
}
