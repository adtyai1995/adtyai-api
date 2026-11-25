package service

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Confirmed sidebar badge kinds registered in docs/adty/sidebar-badge-inventory.md
// part A. Unconfirmed kinds in part B must not appear in the API.
const (
	AdtyBadgeNotificationUnread                   = "notification_unread"
	AdtyBadgeReferralWithdrawalsPending           = "referral_withdrawals_pending"
	AdtyBadgeReferralWithdrawalsApproved          = "referral_withdrawals_approved"
	AdtyBadgeSiteWithdrawalsPending               = "site_withdrawals_pending"
	AdtyBadgeInviteWithdrawalsPending             = "invite_withdrawals_pending"
	AdtyBadgeSiteInviteCommissionsPendingCoverage = "site_invite_commissions_pending_coverage"
	AdtyBadgeReferralCommissionsFrozen            = "referral_commissions_frozen"
	AdtyBadgeFundAnomaliesOpen                    = "fund_anomalies_open"
	AdtyBadgeTicketsPending                       = "tickets_pending"
	AdtyBadgePlatformTicketsPending               = "platform_tickets_pending"
	AdtyBadgeOwnerTicketsPending                  = "owner_tickets_pending"
	AdtyBadgeTicketsWaitingUser                   = "tickets_waiting_user"
	AdtyBadgePaymentOrphansPendingReview          = "payment_orphans_pending_review"
)

// AdtySidebarBadgeConfirmedKinds is the closed set of kinds the badge API may
// return. Tests use it to reject invented or still-unconfirmed counts.
func AdtySidebarBadgeConfirmedKinds() []string {
	return []string{
		AdtyBadgeNotificationUnread,
		AdtyBadgeReferralWithdrawalsPending,
		AdtyBadgeReferralWithdrawalsApproved,
		AdtyBadgeSiteWithdrawalsPending,
		AdtyBadgeInviteWithdrawalsPending,
		AdtyBadgeSiteInviteCommissionsPendingCoverage,
		AdtyBadgeReferralCommissionsFrozen,
		AdtyBadgeFundAnomaliesOpen,
		AdtyBadgeTicketsPending,
		AdtyBadgePlatformTicketsPending,
		AdtyBadgeOwnerTicketsPending,
		AdtyBadgeTicketsWaitingUser,
		AdtyBadgePaymentOrphansPendingReview,
	}
}

// AdtySidebarBadgeUnconfirmedKinds are registered in the inventory part B and
// must not be computed or returned.
func AdtySidebarBadgeUnconfirmedKinds() []string {
	return []string{
		"topup_orders_pending",
		"payment_exceptions_other",
		"team_notification_unread",
		"site_applications_pending",
		"site_invite_commissions_frozen",
		"site_withdrawals_paying",
		"manual_refunds_submitted",
	}
}

type AdtyReferralAdminBadges struct {
	Partners            int64
	PendingWithdrawals  int64
	ApprovedWithdrawals int64
	FrozenCommissions   int64
}

// CollectAdtySidebarBadges returns confirmed badge counts the caller is
// allowed to see. Unauthorized kinds are omitted (not zero) so a caller
// cannot learn that work exists outside their scope (EMP-017). Individual
// counter query failures are logged and degraded to 0 so one failing subquery
// never takes down the entire sidebar badge response (R-33 #3).
// MarkAdtySidebarBadgesSeen records the current timestamp as seen for the given
// confirmed badge keys so opening a menu immediately clears its badge (R-32 #6).
func MarkAdtySidebarBadgesSeen(userID int, keys []string) error {
	if userID <= 0 || len(keys) == 0 {
		return nil
	}
	allowed := map[string]struct{}{}
	for _, k := range AdtySidebarBadgeConfirmedKinds() {
		allowed[k] = struct{}{}
	}
	filtered := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, ok := allowed[k]; ok {
			filtered = append(filtered, k)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return model.MarkAdtyBadgeSeen(userID, filtered, time.Now().Unix())
}

func badgeAfterSeen(query *gorm.DB, col string, seenAt int64) *gorm.DB {
	if seenAt > 0 {
		return query.Where(col+" > ?", seenAt)
	}
	return query
}

func countBadgeOrZero(kind string, query *gorm.DB) int64 {
	n, err := countScoped(query)
	if err != nil {
		common.SysError(common.LogText("sidebar badge %s query failed: %s", kind, err.Error()))
		return 0
	}
	return n
}

func CollectAdtySidebarBadges(userID int, requestSite *model.AdtySite, tokenScopes ...[]string) (map[string]int64, error) {
	if userID <= 0 {
		return nil, common.NewMessage("Invalid parameters")
	}
	actor, err := loadAdtyTicketActor(userID)
	if err != nil {
		return nil, err
	}
	seenMap, err := model.GetAdtyBadgeSeenMap(userID)
	if err != nil {
		common.SysError(common.LogText("sidebar badge seen map load failed (user %d): %s", userID, err.Error()))
		seenMap = map[string]int64{}
	}
	counts := map[string]int64{}
	unread, err := countAdtyNotificationUnread(userID, requestSite, tokenScopes...)
	if err != nil {
		common.SysError(common.LogText("sidebar badge %s query failed: %s", AdtyBadgeNotificationUnread, err.Error()))
		unread = 0
	}
	counts[AdtyBadgeNotificationUnread] = unread
	isToken := len(tokenScopes) > 0
	tokenAllows := func(scope string) bool {
		return !isToken || AccessTokenScopeGranted(tokenScopes[0], scope)
	}

	if requestSite == nil {
		_ = collectAdtyReferralAdminBadgeCounts(counts, actor, seenMap, true, tokenScopes...)
		if actor.User.Role >= common.RoleRootUser && tokenAllows("option:read") {
			counts[AdtyBadgeSiteWithdrawalsPending] = countBadgeOrZero(
				AdtyBadgeSiteWithdrawalsPending,
				badgeAfterSeen(model.DB.Model(&model.AdtySiteWithdrawal{}).Where("status = ?", model.AdtySiteWithdrawPending), "created_at", seenMap[AdtyBadgeSiteWithdrawalsPending]),
			)
			counts[AdtyBadgeFundAnomaliesOpen] = countBadgeOrZero(
				AdtyBadgeFundAnomaliesOpen,
				badgeAfterSeen(model.DB.Model(&model.AdtyAnomalyDisposition{}).Where("status IN ?", []string{"open", "reviewing"}), "created_at", seenMap[AdtyBadgeFundAnomaliesOpen]),
			)
		}
	}
	if actor.OwnerSiteId > 0 && tokenAllows(AccessTokenResourceSiteOwnerOrder+":read") {
		counts[AdtyBadgeInviteWithdrawalsPending] = countBadgeOrZero(
			AdtyBadgeInviteWithdrawalsPending,
			badgeAfterSeen(model.DB.Model(&model.AdtySiteInviteWithdrawal{}).Where("site_id = ? AND status = ?", actor.OwnerSiteId, model.AdtySiteInviteWithdrawPending), "created_at", seenMap[AdtyBadgeInviteWithdrawalsPending]),
		)
		counts[AdtyBadgeSiteInviteCommissionsPendingCoverage] = countBadgeOrZero(
			AdtyBadgeSiteInviteCommissionsPendingCoverage,
			badgeAfterSeen(model.DB.Model(&model.AdtySiteInviteCommission{}).Where("site_id = ? AND status = ?", actor.OwnerSiteId, model.AdtySiteCommissionPendingCoverage), "created_at", seenMap[AdtyBadgeSiteInviteCommissionsPendingCoverage]),
		)
	}
	// Staff balance requests are retired (R-35 #11); omit staff_balance_pending without querying adty_staff_balance_requests.
	collectAdtyTicketBadgesInto(counts, actor, requestSite == nil, seenMap, tokenScopes...)
	if requestSite == nil && tokenAllows(AccessTokenScopeOf(authz.AdtyFinanceRead)) {
		collectAdtyPaymentOrphanBadge(counts, actor, seenMap)
	}
	return counts, nil
}

// CollectAdtyReferralAdminBadges is the scoped replacement for the previous
// unscoped GetReferralAdminBadges sums. Root sees every site; a non-root
// admin is limited to main-site users. Callers without referral-admin
// standing receive an authorization error rather than a global or leaked
// count.
func CollectAdtyReferralAdminBadges(userID int, tokenScopes ...[]string) (AdtyReferralAdminBadges, error) {
	var out AdtyReferralAdminBadges
	if userID <= 0 {
		return out, common.NewMessage("Invalid parameters")
	}
	actor, err := loadAdtyTicketActor(userID)
	if err != nil {
		return out, err
	}
	_, ok := referralAdminSiteScope(actor)
	if !ok {
		return out, ErrAdtyTicketForbidden
	}
	counts := map[string]int64{}
	if err := collectAdtyReferralAdminBadgeCounts(counts, actor, nil, false, tokenScopes...); err != nil {
		return out, err
	}
	return AdtyReferralAdminBadges{PendingWithdrawals: counts[AdtyBadgeReferralWithdrawalsPending], ApprovedWithdrawals: counts[AdtyBadgeReferralWithdrawalsApproved], FrozenCommissions: counts[AdtyBadgeReferralCommissionsFrozen]}, nil
}

// AdtyReferralOverview is the referral admin overview totals, in the same
// dollar fields GetReferralOverview already returns.
type AdtyReferralOverview struct {
	Affiliates int64
	Bindings   int64
	Pending    decimal.Decimal
	Available  decimal.Decimal
	Frozen     decimal.Decimal
	Withdrawn  decimal.Decimal
}

// CollectAdtyReferralOverview applies the same caller scope as
// CollectAdtyReferralAdminBadges: Root is global; a non-root admin is
// limited to main-site users (users.site_id = 0). The counting formula
// itself is unchanged (affiliate and binding counts plus commission-account
// SUM). Bindings are scoped by inviter_user_id, the affiliate user, matching
// how badge counts and the binding list already identify the partner.
// Callers without referral-admin standing receive an authorization error
// rather than a global or leaked total.
func CollectAdtyReferralOverview(userID int) (AdtyReferralOverview, error) {
	var out AdtyReferralOverview
	if userID <= 0 {
		return out, common.NewMessage("Invalid parameters")
	}
	actor, err := loadAdtyTicketActor(userID)
	if err != nil {
		return out, err
	}
	siteID, ok := referralAdminSiteScope(actor)
	if !ok {
		return out, ErrAdtyTicketForbidden
	}
	return countAdtyReferralOverview(siteID)
}

func countAdtyReferralOverview(siteID *int) (AdtyReferralOverview, error) {
	var out AdtyReferralOverview
	var err error
	out.Affiliates, err = countScoped(scopeReferralUserQuery(model.DB.Model(&model.ReferralAffiliate{}), "user_id", siteID))
	if err != nil {
		return out, err
	}
	out.Bindings, err = countScoped(scopeReferralUserQuery(model.DB.Model(&model.ReferralBinding{}), "inviter_user_id", siteID))
	if err != nil {
		return out, err
	}
	var sums struct {
		Pending, Available, Frozen, Withdrawn decimal.Decimal
	}
	if err := scopeReferralUserQuery(model.DB.Model(&model.ReferralCommissionAccount{}), "user_id", siteID).Select(
		"COALESCE(SUM(pending_amount),0) AS pending, COALESCE(SUM(available_amount),0) AS available, COALESCE(SUM(frozen_amount),0) AS frozen, COALESCE(SUM(withdrawn_amount),0) AS withdrawn",
	).Scan(&sums).Error; err != nil {
		return out, err
	}
	out.Pending, out.Available, out.Frozen, out.Withdrawn = sums.Pending, sums.Available, sums.Frozen, sums.Withdrawn
	return out, nil
}

func collectAdtyReferralAdminBadgeCounts(counts map[string]int64, actor adtyTicketActor, seenMap map[string]int64, softFail bool, tokenScopes ...[]string) error {
	siteID, ok := referralAdminSiteScope(actor)
	can := func(p authz.Permission) bool {
		return AdtyAdminRoleCan(actor.User.Id, actor.User.Role, p) && (len(tokenScopes) == 0 || AccessTokenScopeGranted(tokenScopes[0], AccessTokenScopeOf(p)))
	}
	if !ok || !can(authz.AdtyReferralRead) {
		return nil
	}
	runCount := func(kind string, query *gorm.DB) (int64, error) {
		if softFail {
			return countBadgeOrZero(kind, query), nil
		}
		return countScoped(query)
	}
	for _, duty := range []struct {
		permission    authz.Permission
		status, badge string
	}{
		{authz.AdtyReferralReview, model.ReferralWithdrawalStatusPending, AdtyBadgeReferralWithdrawalsPending},
		{authz.AdtyReferralPayout, model.ReferralWithdrawalStatusApproved, AdtyBadgeReferralWithdrawalsApproved},
	} {
		if !can(duty.permission) {
			continue
		}
		seenAt := seenMap[duty.badge]
		platform, err := runCount(duty.badge, badgeAfterSeen(scopeReferralUserQuery(model.DB.Model(&model.ReferralWithdrawal{}).Where("status = ?", duty.status), "user_id", siteID), "updated_at", seenAt))
		if err != nil {
			return err
		}
		// adty_site_invite_withdrawals has created_at and reviewed_at (no updated_at column).
		closedQuery := closedSiteInviteWithdrawalQuery(duty.status).Where("status = ?", duty.status)
		if seenAt > 0 {
			closedQuery = closedQuery.Where("(created_at > ? OR reviewed_at > ?)", seenAt, seenAt)
		}
		closed, err := runCount(duty.badge, closedQuery)
		if err != nil {
			return err
		}
		counts[duty.badge] = platform + closed
	}
	frozen, err := runCount(AdtyBadgeReferralCommissionsFrozen, badgeAfterSeen(scopeReferralUserQuery(model.DB.Model(&model.ReferralCommission{}).Where("status = ?", model.ReferralCommissionStatusFrozen), "affiliate_user_id", siteID), "updated_at", seenMap[AdtyBadgeReferralCommissionsFrozen]))
	if err != nil {
		return err
	}
	counts[AdtyBadgeReferralCommissionsFrozen] = frozen
	return nil
}

func referralAdminSiteScope(actor adtyTicketActor) (*int, bool) {
	if actor.User.Role >= common.RoleRootUser {
		return nil, true
	}
	if actor.User.Role >= common.RoleAdminUser {
		mainSite := 0
		return &mainSite, true
	}
	return nil, false
}

// ResolveAdtyReferralAdminScope returns the caller's referral-admin site scope:
// nil for Root (global), &0 for non-root Admin (main site only).
func ResolveAdtyReferralAdminScope(userID int) (*int, bool, error) {
	if userID <= 0 {
		return nil, false, common.NewMessage("Invalid parameters")
	}
	actor, err := loadAdtyTicketActor(userID)
	if err != nil {
		return nil, false, err
	}
	siteID, ok := referralAdminSiteScope(actor)
	if !ok {
		return nil, false, ErrAdtyTicketForbidden
	}
	return siteID, true, nil
}

// ScopeAdtyReferralUsersQuery scopes a query by user column matching siteID (no-op for Root).
func ScopeAdtyReferralUsersQuery(query *gorm.DB, userColumn string, siteID *int) *gorm.DB {
	return scopeReferralUserQuery(query, userColumn, siteID)
}

// ScopeAdtyReferralAffiliateIDsQuery scopes a query referencing affiliate_id by affiliate owner siteID.
func ScopeAdtyReferralAffiliateIDsQuery(query *gorm.DB, affiliateColumn string, siteID *int) *gorm.DB {
	if siteID == nil {
		return query
	}
	return query.Where(
		affiliateColumn+" IN (?)",
		model.DB.Model(&model.ReferralAffiliate{}).
			Select("id").
			Where("user_id IN (?)", model.DB.Model(&model.User{}).Select("id").Where("site_id = ?", *siteID)),
	)
}

// EnsureAdtyReferralTargetUserScope enforces that non-root referral admins only inspect/mutate main-site users.
func EnsureAdtyReferralTargetUserScope(siteID *int, targetUserID int) error {
	if siteID == nil {
		return nil
	}
	if targetUserID <= 0 {
		return common.NewMessage("No permission to perform this action")
	}
	var user model.User
	if err := model.DB.Select("id", "site_id").First(&user, targetUserID).Error; err != nil {
		return err
	}
	if user.SiteId != *siteID {
		return common.NewMessage("No permission to perform this action")
	}
	return nil
}

// EnsureAdtyReferralWithdrawalScope enforces target site scope for a referral withdrawal record.
func EnsureAdtyReferralWithdrawalScope(siteID *int, withdrawalID int) error {
	if siteID == nil {
		return nil
	}
	var w model.ReferralWithdrawal
	if err := model.DB.Select("id", "user_id").First(&w, withdrawalID).Error; err != nil {
		return err
	}
	return EnsureAdtyReferralTargetUserScope(siteID, w.UserId)
}

// EnsureAdtyReferralTradeScope enforces target site scope for a commission job retry identified by trade_no.
func EnsureAdtyReferralTradeScope(siteID *int, tradeNo string) error {
	if siteID == nil {
		return nil
	}
	var job model.ReferralCommissionJob
	if err := model.DB.Where("source_type = ? AND source_trade_no = ?", model.ReferralSourceTopup, tradeNo).First(&job).Error; err != nil {
		return err
	}
	var aff model.ReferralAffiliate
	if err := model.DB.Select("id", "user_id").First(&aff, job.AffiliateId).Error; err != nil {
		return err
	}
	return EnsureAdtyReferralTargetUserScope(siteID, aff.UserId)
}

func scopeReferralUserQuery(query *gorm.DB, userColumn string, siteID *int) *gorm.DB {
	if siteID == nil {
		return query
	}
	return query.Where(userColumn+" IN (?)", model.DB.Model(&model.User{}).Select("id").Where("site_id = ?", *siteID))
}

func collectAdtyTicketBadgesInto(counts map[string]int64, actor adtyTicketActor, onMainSite bool, seenMap map[string]int64, tokenScopes ...[]string) {
	resolveScope := func(requested string) string {
		if len(tokenScopes) > 0 {
			return AdtyTicketTokenScope(requested, "GET", tokenScopes[0])
		}
		return requested
	}
	countForScope := func(kind, scope string, seenAt int64) AdtyTicketBadges {
		scoped := actor
		scoped.Scope = scope
		badges, err := countAdtyTicketBadges(badgeAfterSeen(scopedAdtyTickets(model.DB, scoped), "updated_at", seenAt))
		if err != nil {
			common.SysError(common.LogText("sidebar badge %s (%s) query failed: %s", kind, scope, err.Error()))
			return AdtyTicketBadges{}
		}
		return badges
	}

	// 1. Customer-side waiting-user badge (personal + shared team tickets for active team members; all team tickets for team leaders).
	if actor.Staff == nil && actor.User.StaffSiteId == 0 {
		customerScope := "personal"
		if actor.TeamId > 0 {
			customerScope = "participant"
		}
		if resolved := resolveScope(customerScope); resolved != "denied" {
			badges := countForScope(AdtyBadgeTicketsWaitingUser, resolved, seenMap[AdtyBadgeTicketsWaitingUser])
			counts[AdtyBadgeTicketsWaitingUser] = badges.WaitingUser
		}
	}

	// 2. Platform handler pending badge.
	if onMainSite && actor.PlatformMain {
		if resolved := resolveScope("platform"); resolved != "denied" {
			badges := countForScope(AdtyBadgePlatformTicketsPending, resolved, max(seenMap[AdtyBadgePlatformTicketsPending], seenMap[AdtyBadgeTicketsPending]))
			counts[AdtyBadgePlatformTicketsPending] = badges.Pending
			counts[AdtyBadgeTicketsPending] = badges.Pending
		}
	}

	// 3. Site-owner handler pending badge.
	if actor.OwnerTickets {
		if resolved := resolveScope("owner"); resolved != "denied" {
			badges := countForScope(AdtyBadgeOwnerTicketsPending, resolved, max(seenMap[AdtyBadgeOwnerTicketsPending], seenMap[AdtyBadgeTicketsPending]))
			counts[AdtyBadgeOwnerTicketsPending] = badges.Pending
			if _, exists := counts[AdtyBadgeTicketsPending]; !exists {
				counts[AdtyBadgeTicketsPending] = badges.Pending
			}
		}
	}

	// 4. Site-staff handler badges.
	if actor.StaffTickets {
		if resolved := resolveScope("staff"); resolved != "denied" {
			pendingSeen := seenMap[AdtyBadgeTicketsPending]
			waitingSeen := seenMap[AdtyBadgeTicketsWaitingUser]
			pendingBadges := countForScope(AdtyBadgeTicketsPending, resolved, pendingSeen)
			counts[AdtyBadgeTicketsPending] = pendingBadges.Pending
			if waitingSeen == pendingSeen {
				counts[AdtyBadgeTicketsWaitingUser] = pendingBadges.WaitingUser
			} else {
				waitingBadges := countForScope(AdtyBadgeTicketsWaitingUser, resolved, waitingSeen)
				counts[AdtyBadgeTicketsWaitingUser] = waitingBadges.WaitingUser
			}
		}
	}
}

func collectAdtyPaymentOrphanBadge(counts map[string]int64, actor adtyTicketActor, seenMap map[string]int64) {
	if actor.User.Role < common.RoleAdminUser {
		return
	}
	if AdtyAdminPermissionsEnabled() && actor.User.Role < common.RoleRootUser && !AdtyAdminRoleCan(actor.User.Id, actor.User.Role, authz.AdtyFinanceRead) {
		return
	}
	// adty_payment_orphan_events uses create_time (not created_at) (R-33 #3).
	query := badgeAfterSeen(model.DB.Model(&model.PaymentOrphanEvent{}).Where("status = ?", model.PaymentOrphanStatusPendingReview), "create_time", seenMap[AdtyBadgePaymentOrphansPendingReview])
	if actor.User.Role < common.RoleRootUser {
		query = query.Where("EXISTS (SELECT 1 FROM adty_topup_orders a WHERE a.trade_no = adty_payment_orphan_events.reference_id AND a.site_id = 0)")
	}
	counts[AdtyBadgePaymentOrphansPendingReview] = countBadgeOrZero(AdtyBadgePaymentOrphansPendingReview, query)
}

func countAdtyNotificationUnread(userID int, site *model.AdtySite, tokenScopes ...[]string) (int64, error) {
	inbox, err := GetAdtyNotificationInboxWithScopes(userID, site, 0, 0, tokenScopes...)
	if err != nil {
		return 0, err
	}
	return inbox.UnreadPublications, nil
}

func countScoped(query *gorm.DB) (int64, error) {
	var n int64
	if err := query.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
