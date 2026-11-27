package service

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	ErrAdtyStaffDenied                = common.NewMessage("Staff access is not permitted")
	ErrAdtyStaffInput                 = common.NewMessage("Invalid staff role, permissions or account details")
	ErrAdtyStaffConflict              = common.NewMessage("The staff account changed. Refresh and try again.")
	ErrAdtyStaffCustomerPasswordReset = common.NewMessage("Staff cannot reset a customer's password")
)

const (
	AdtyStaffTicketManage = "tickets.manage"
)

var adtyStaffReadPermissions = []string{"customers.read", "customers.balance", "customers.topups", "customers.usage"}
var adtyStaffPermissions = append(slices.Clone(adtyStaffReadPermissions), "customers.disable", AdtyStaffTicketManage)

type AdtyStaffInput struct {
	Username        string    `json:"username"`
	DisplayName     string    `json:"display_name"`
	Password        string    `json:"password"`
	Role            string    `json:"role"`
	Permissions     *[]string `json:"permissions"`
	Status          string    `json:"status"`
	ExpectedVersion int64     `json:"expected_version"`
}

type AdtyStaffView struct {
	model.AdtySiteStaff
	Permissions []string `json:"permissions"`
}

func AdtyStaffViewOf(staff model.AdtySiteStaff) AdtyStaffView {
	permissions := []string{}
	_ = common.UnmarshalJsonStr(staff.Permissions, &permissions)
	return AdtyStaffView{AdtySiteStaff: staff, Permissions: permissions}
}

// Roles never grant write authority. An explicit empty array grants nothing.
func adtyStaffGrants(role string, custom *[]string) ([]string, error) {
	var grants []string
	switch role {
	case "support":
		grants = []string{"customers.read"}
	case "finance", "manager":
		grants = slices.Clone(adtyStaffReadPermissions)
	case "operations":
		grants = []string{"customers.read", "customers.usage"}
	default:
		return nil, ErrAdtyStaffInput
	}
	if custom != nil {
		grants = slices.Clone(*custom)
	}
	if grants == nil {
		grants = []string{}
	}
	for _, grant := range grants {
		if !slices.Contains(adtyStaffPermissions, grant) {
			return nil, ErrAdtyStaffInput
		}
	}
	slices.Sort(grants)
	return slices.Compact(grants), nil
}

// adtyStaffLockSiteTx locks and validates the site row only so callers that
// subsequently lock adty_site_staff and multiple users can obey the canonical
// global lock order: AdtySite -> AdtySiteStaff -> User (id ASC) (R7-16).
func adtyStaffLockSiteTx(tx *gorm.DB, ownerID, siteID int, allowClosing bool) (*model.AdtySite, error) {
	var site model.AdtySite
	if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
		return nil, err
	}
	if site.OwnerUserId != ownerID || !AdtySiteAgreementAccepted(site.AgreementVer) || (site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused && (!allowClosing || site.Status != model.AdtySiteStatusClosing)) {
		return nil, ErrAdtyStaffDenied
	}
	return &site, nil
}

// Recheck ownership inside the mutation transaction, including account status,
// the current agreement and site state. A cached HTTP precheck is insufficient.
func adtyStaffOwnerTx(tx *gorm.DB, ownerID, siteID int, allowClosing bool) (*model.AdtySite, error) {
	// Canonical lock order: AdtySite -> User.
	site, err := adtyStaffLockSiteTx(tx, ownerID, siteID, allowClosing)
	if err != nil {
		return nil, err
	}
	var owner model.User
	if err := model.AdtyLockForUpdate(tx).First(&owner, ownerID).Error; err != nil {
		return nil, err
	}
	if owner.Status != common.UserStatusEnabled || owner.StaffSiteId != 0 {
		return nil, ErrAdtyStaffDenied
	}
	return site, nil
}

func adtySortedUniqueIDs(ids ...int) []int {
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func CreateAdtyStaff(ownerID, siteID int, input AdtyStaffInput) (*AdtyStaffView, error) {
	input.Username, input.DisplayName = strings.TrimSpace(input.Username), strings.TrimSpace(input.DisplayName)
	if input.Username == "" || !model.ValidateUsernameNoControl(input.Username) || input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > 20 || utf8.RuneCountInString(input.Password) < 15 {
		return nil, ErrAdtyStaffInput
	}
	if err := common.Validate.StructPartial(&model.User{Username: input.Username}, "Username"); err != nil {
		return nil, ErrAdtyStaffInput
	}
	grants, err := adtyStaffGrants(input.Role, input.Permissions)
	if err != nil {
		return nil, err
	}
	hash, err := common.HashAccountPassword(input.Password)
	if err != nil {
		return nil, err
	}
	var staff model.AdtySiteStaff
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := adtyStaffOwnerTx(tx, ownerID, siteID, false); err != nil {
			return err
		}
		// Global login names cannot be reassigned, including deleted accounts
		// and case-variant spellings of an existing name.
		if err := model.EnsureUsernameAvailableWithTx(tx, input.Username, 0); err != nil {
			if errors.Is(err, model.ErrUsernameAlreadyTaken) {
				return ErrAdtyStaffInput
			}
			return err
		}
		user := model.User{Username: input.Username, DisplayName: input.DisplayName, Password: hash, Role: common.RoleCommonUser,
			Status: common.UserStatusEnabled, Group: "", SiteId: siteID, StaffSiteId: siteID, AuthVersion: 1}
		if err := tx.Session(&gorm.Session{Logger: logger.Discard}).Create(&user).Error; err != nil {
			return ErrAdtyStaffInput
		}
		staff = model.AdtySiteStaff{SiteId: siteID, OwnerUserId: ownerID, UserId: user.Id, Username: user.Username,
			DisplayName: input.DisplayName, Role: input.Role, Permissions: common.GetJsonString(grants), Status: "enabled", Version: 1}
		if err := tx.Create(&staff).Error; err != nil {
			return err
		}
		return recordSiteAuditTx(tx, siteID, "staff.create", ownerID, nil, AdtyStaffViewOf(staff), "")
	})
	if err != nil {
		return nil, err
	}
	view := AdtyStaffViewOf(staff)
	return &view, nil
}

// Mutation and version advancement are atomic with the audit record. Disabled
// and deleted accounts keep their dedicated identity; enabling never revives JWTs.
func MutateAdtyStaff(ownerID, siteID, staffID int, action string, input AdtyStaffInput) (*AdtyStaffView, error) {
	var hash string
	if action == AdtySiteOwnerActionStaffPassword {
		if utf8.RuneCountInString(input.Password) < 15 {
			return nil, ErrAdtyStaffInput
		}
		var err error
		hash, err = common.HashAccountPassword(input.Password)
		if err != nil {
			return nil, err
		}
	}
	var staff model.AdtySiteStaff
	var staffUsername, staffEmail string
	var ticketAudits []model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		ticketAudits = nil
		// R7-16: Strict global lock order AdtySite -> AdtySiteStaff -> User (id ASC).
		site, err := adtyStaffLockSiteTx(tx, ownerID, siteID, action == AdtySiteOwnerActionStaffDelete || action == AdtySiteOwnerActionStaffUpdate)
		if err != nil {
			return err
		}
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND site_id = ? AND owner_user_id = ? AND status <> ?", staffID, siteID, ownerID, "deleted").First(&staff).Error; err != nil {
			return err
		}
		if input.ExpectedVersion <= 0 || staff.Version != input.ExpectedVersion {
			return ErrAdtyStaffConflict
		}
		var lockedUsers []model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "username", "email", "status", "auth_version", "staff_site_id").
			Where("id IN ?", adtySortedUniqueIDs(ownerID, staff.UserId)).Order("id ASC").Find(&lockedUsers).Error; err != nil {
			return err
		}
		usersByID := make(map[int]model.User, len(lockedUsers))
		for _, u := range lockedUsers {
			usersByID[u.Id] = u
		}
		owner, ok := usersByID[ownerID]
		if !ok || owner.Status != common.UserStatusEnabled || owner.StaffSiteId != 0 {
			return ErrAdtyStaffDenied
		}
		targetUser, ok := usersByID[staff.UserId]
		if !ok || targetUser.StaffSiteId != siteID {
			return gorm.ErrRecordNotFound
		}
		staffUsername, staffEmail = targetUser.Username, targetUser.Email
		before := AdtyStaffViewOf(staff)
		previousStaffStatus := staff.Status
		userUpdates := map[string]any{}
		syncDisabledAuthVersion := false
		switch action {
		case AdtySiteOwnerActionStaffUpdate:
			input.DisplayName = strings.TrimSpace(input.DisplayName)
			if input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > 20 || (input.Status != "enabled" && input.Status != "disabled") {
				return ErrAdtyStaffInput
			}
			if input.Permissions == nil && input.Role == staff.Role {
				input.Permissions = &before.Permissions
			}
			grants, err := adtyStaffGrants(input.Role, input.Permissions)
			if err != nil {
				return err
			}
			if site.Status == model.AdtySiteStatusClosing {
				if input.Status == "enabled" && staff.Status != "enabled" {
					return ErrAdtyStaffDenied
				}
				for _, grant := range grants {
					if !slices.Contains(before.Permissions, grant) {
						return ErrAdtyStaffDenied
					}
				}
			}
			// R3-02: Never let a site owner bypass a platform administrator/Root ban.
			if input.Status == "enabled" {
				if previousStaffStatus == "disabled" {
					if targetUser.Status != common.UserStatusEnabled {
						if targetUser.Status != common.UserStatusDisabled || staff.DisabledAuthVersion == 0 || targetUser.AuthVersion != staff.DisabledAuthVersion {
							return ErrAdtyStaffDenied
						}
						userUpdates["status"] = common.UserStatusEnabled
					}
					staff.DisabledAuthVersion = 0
				}
				// When previousStaffStatus == "enabled", keep users.status untouched so editing
				// display name or permissions cannot overwrite an active platform ban.
			} else {
				// input.Status == "disabled"
				userUpdates["status"] = common.UserStatusDisabled
				if targetUser.Status == common.UserStatusEnabled ||
					(previousStaffStatus == "disabled" && staff.DisabledAuthVersion > 0 && targetUser.AuthVersion == staff.DisabledAuthVersion) {
					syncDisabledAuthVersion = true
				} else {
					staff.DisabledAuthVersion = 0
				}
			}
			staff.DisplayName, staff.Role, staff.Permissions, staff.Status = input.DisplayName, input.Role, common.GetJsonString(grants), input.Status
			userUpdates["display_name"] = staff.DisplayName
		case AdtySiteOwnerActionStaffPassword:
			userUpdates["password"] = hash
			if staff.Status == "disabled" && staff.DisabledAuthVersion > 0 && targetUser.AuthVersion == staff.DisabledAuthVersion {
				syncDisabledAuthVersion = true
			}
		case AdtySiteOwnerActionStaffDelete:
			staff.Status = "deleted"
			staff.DisabledAuthVersion = 0
			userUpdates["status"] = common.UserStatusDisabled
		default:
			return ErrAdtyStaffInput
		}
		nextAuthVersion, err := model.IncrementUserAuthVersionWithTx(tx, staff.UserId)
		if err != nil {
			return err
		}
		if syncDisabledAuthVersion {
			staff.DisabledAuthVersion = nextAuthVersion
		}
		if staff.Status != "enabled" || action == AdtySiteOwnerActionStaffPassword || action == AdtySiteOwnerActionStaffDelete {
			if _, err := model.DeleteUserAccessTokensWithTx(tx, staff.UserId); err != nil {
				return err
			}
			userUpdates["access_token"] = nil
			userUpdates["access_token_created_at"] = nil
		}
		if err := tx.Session(&gorm.Session{Logger: logger.Discard}).Model(&model.User{}).Where("id = ? AND staff_site_id = ?", staff.UserId, siteID).Updates(userUpdates).Error; err != nil {
			return ErrAdtyStaffInput
		}
		staff.Version++
		result := tx.Model(&model.AdtySiteStaff{}).Where("id = ? AND version = ?", staffID, input.ExpectedVersion).Updates(map[string]any{
			"display_name": staff.DisplayName, "role": staff.Role, "permissions": staff.Permissions,
			"status": staff.Status, "disabled_auth_version": staff.DisabledAuthVersion, "version": staff.Version,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAdtyStaffConflict
		}
		if staff.Status != "enabled" || !slices.Contains(AdtyStaffViewOf(staff).Permissions, AdtyStaffTicketManage) {
			reason := "staff_ticket_permission_revoked"
			switch {
			case action == AdtySiteOwnerActionStaffDelete || staff.Status == "deleted":
				reason = "staff_delete"
			case staff.Status == "disabled":
				reason = "staff_disable"
			}
			audits, err := releaseStaffTicketsWithAuditsTx(tx, &staff, ownerID, reason)
			if err != nil {
				return err
			}
			ticketAudits = audits
		}
		return recordSiteAuditTx(tx, siteID, action, ownerID, before, AdtyStaffViewOf(staff), "")
	})
	if err != nil {
		return nil, err
	}
	DeliverAdtyCommittedAudits(ticketAudits)
	if err := model.PublishUserAuthCache(staff.UserId); err != nil {
		common.SysError("Failed to publish staff auth cache")
	}
	if _, err := model.RevokeAllUserSessions(staff.UserId, "staff_"+action); err != nil {
		common.SysError("Failed to revoke staff sessions")
	}
	if err := model.InvalidateUserTokensCache(staff.UserId); err != nil {
		common.SysError("Failed to invalidate staff tokens cache")
	}
	if action == AdtySiteOwnerActionStaffPassword {
		common.ClearAccountLoginFailures(staff.UserId, staffUsername, staffEmail, siteID)
	}
	view := AdtyStaffViewOf(staff)
	return &view, nil
}

// Always read the current membership, user status/version and owner from SQL.
// Authorization cannot be inherited by transferring a site or replaying a JWT.
func AdtyStaffAccess(userID int, authVersion int64, permission string) (*model.AdtySiteStaff, *model.AdtySite, error) {
	var staff model.AdtySiteStaff
	if err := model.DB.Where("user_id = ? AND status = ?", userID, "enabled").First(&staff).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrAdtyStaffDenied
		}
		return nil, nil, err
	}
	var user model.User
	if err := model.DB.Select("id", "staff_site_id", "site_id", "status", "auth_version", "role").First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrAdtyStaffDenied
		}
		return nil, nil, err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != authVersion || user.StaffSiteId != staff.SiteId || user.SiteId != staff.SiteId || user.Role != common.RoleCommonUser {
		return nil, nil, ErrAdtyStaffDenied
	}
	var site model.AdtySite
	if err := model.DB.First(&site, staff.SiteId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrAdtyStaffDenied
		}
		return nil, nil, err
	}
	if site.OwnerUserId != staff.OwnerUserId || !AdtySiteAgreementAccepted(site.AgreementVer) || (site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused && site.Status != model.AdtySiteStatusClosing) {
		return nil, nil, ErrAdtyStaffDenied
	}
	var owner model.User
	if err := model.DB.Select("id", "status", "staff_site_id").First(&owner, site.OwnerUserId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrAdtyStaffDenied
		}
		return nil, nil, err
	}
	if owner.Status != common.UserStatusEnabled || owner.StaffSiteId != 0 {
		return nil, nil, ErrAdtyStaffDenied
	}
	grants := AdtyStaffViewOf(staff).Permissions
	if permission != "" && (!slices.Contains(adtyStaffPermissions, permission) || !slices.Contains(grants, permission)) {
		return nil, nil, ErrAdtyStaffDenied
	}
	return &staff, &site, nil
}

// Customer writes serialize with owner grant changes and site closure. The
// returned user is internal mail input and must never be serialized to staff.
func ManageAdtyStaffCustomer(userID int, authVersion int64, customerID int, action string) (*model.User, error) {
	if action == AdtyStaffActionCustomerPasswordReset {
		return nil, ErrAdtyStaffCustomerPasswordReset
	}
	if action != AdtyStaffActionCustomerDisable {
		return nil, ErrAdtyStaffInput
	}
	permission := "customers.disable"
	var membership model.AdtySiteStaff
	if err := model.DB.Where("user_id = ? AND status = ?", userID, "enabled").First(&membership).Error; err != nil {
		return nil, ErrAdtyStaffDenied
	}
	var customer model.User
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// R7-16: Strict global lock order AdtySite -> AdtySiteStaff -> User (id ASC)
		// without nested FOR UPDATE subqueries across adty_sites/referral_affiliates.
		site, err := adtyStaffLockSiteTx(tx, membership.OwnerUserId, membership.SiteId, false)
		if err != nil {
			return err
		}
		var staff model.AdtySiteStaff
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND owner_user_id = ? AND status = ?", membership.Id, site.OwnerUserId, "enabled").First(&staff).Error; err != nil {
			return ErrAdtyStaffDenied
		}
		if staff.SiteId != site.Id {
			return ErrAdtyStaffDenied
		}
		var lockedUsers []model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "username", "email", "setting", "status", "auth_version", "site_id", "staff_site_id", "role").
			Where("id IN ?", adtySortedUniqueIDs(site.OwnerUserId, userID, customerID)).Order("id ASC").Find(&lockedUsers).Error; err != nil {
			return err
		}
		usersByID := make(map[int]model.User, len(lockedUsers))
		for _, u := range lockedUsers {
			usersByID[u.Id] = u
		}
		owner, ok := usersByID[site.OwnerUserId]
		if !ok || owner.Status != common.UserStatusEnabled || owner.StaffSiteId != 0 {
			return ErrAdtyStaffDenied
		}
		employee, ok := usersByID[userID]
		if !ok {
			return gorm.ErrRecordNotFound
		}
		grants := AdtyStaffViewOf(staff).Permissions
		if employee.AuthVersion != authVersion || employee.Status != common.UserStatusEnabled || employee.Role != common.RoleCommonUser ||
			employee.SiteId != site.Id || employee.StaffSiteId != site.Id ||
			!slices.Contains(grants, "customers.read") || !slices.Contains(grants, permission) {
			return ErrAdtyStaffDenied
		}
		foundCustomer, ok := usersByID[customerID]
		if !ok || foundCustomer.SiteId != site.Id || foundCustomer.StaffSiteId != 0 ||
			foundCustomer.Role != common.RoleCommonUser || foundCustomer.Id == site.OwnerUserId {
			return gorm.ErrRecordNotFound
		}
		var ownedSites int64
		if err := tx.Model(&model.AdtySite{}).Where("owner_user_id = ? AND status <> ?", customerID, model.AdtySiteStatusClosed).Count(&ownedSites).Error; err != nil {
			return err
		}
		if ownedSites > 0 {
			return gorm.ErrRecordNotFound
		}
		var ownerAffiliates int64
		if err := tx.Model(&model.ReferralAffiliate{}).Where("user_id = ? AND site_owner_since > 0", customerID).Count(&ownerAffiliates).Error; err != nil {
			return err
		}
		if ownerAffiliates > 0 {
			return gorm.ErrRecordNotFound
		}
		if isIndep, err := adtyIsIndependentTeamEmployeeTx(tx, customerID); err != nil {
			return err
		} else if isIndep {
			return gorm.ErrRecordNotFound
		}
		customer = foundCustomer
		var joined model.AdtySiteCustomer
		if err := model.AdtyLockForUpdate(tx).Where("user_id = ? AND site_id = ?", customerID, site.Id).First(&joined).Error; err != nil {
			return err
		}
		previousStatus := customer.Status
		if _, err := model.IncrementUserAuthVersionWithTx(tx, customerID); err != nil {
			return err
		}
		if _, err := model.DeleteUserAccessTokensWithTx(tx, customerID); err != nil {
			return err
		}
		if err := tx.Model(&model.User{}).Where("id = ?", customerID).Updates(map[string]any{
			"status":                  common.UserStatusDisabled,
			"access_token":            nil,
			"access_token_created_at": nil,
		}).Error; err != nil {
			return err
		}
		customer.Status = common.UserStatusDisabled
		return recordSiteAuditTx(tx, site.Id, "staff.customer.disable", userID, map[string]any{"status": previousStatus},
			map[string]any{"staff_id": staff.Id, "customer_id": customerID, "status": customer.Status}, "")
	})
	if err != nil {
		return nil, err
	}
	if action == AdtyStaffActionCustomerDisable {
		if err := model.PublishUserAuthCache(customerID); err != nil {
			common.SysError("Failed to publish disabled customer auth cache")
		}
		if _, err := model.RevokeAllUserSessions(customerID, "staff_customer_disabled"); err != nil {
			common.SysError("Failed to revoke disabled customer sessions")
		}
		if err := model.InvalidateUserTokensCache(customerID); err != nil {
			common.SysError("Failed to invalidate disabled customer tokens cache")
		}
	}
	return &customer, nil
}

// Staff visibility is the intersection of platform visibility and explicit
// grants. Emails are always masked and customer API keys have no staff endpoint.
func AdtyStaffCustomerSite(site *model.AdtySite, staff *model.AdtySiteStaff) *model.AdtySite {
	copy := *site
	visibility := AdtySiteVisibility(site)
	visibility["customer_email"] = "masked"
	visibility["customer_tokens"] = "off"
	for grant, key := range map[string]string{"customers.balance": "customer_balance", "customers.usage": "customer_usage", "customers.topups": "customer_topups"} {
		if !slices.Contains(AdtyStaffViewOf(*staff).Permissions, grant) {
			visibility[key] = "off"
		}
	}
	copy.Visibility = common.GetJsonString(visibility)
	return &copy
}

func AdtyStaffErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrAdtyStaffDenied), errors.Is(err, ErrAdtyStaffCustomerPasswordReset):
		return 403
	case errors.Is(err, gorm.ErrRecordNotFound):
		return 404
	case errors.Is(err, ErrAdtyStaffConflict):
		return 409
	case errors.Is(err, ErrAdtyStaffInput), errors.Is(err, common.ErrAccountPasswordLength), errors.Is(err, common.ErrPasswordLegacyLimit):
		return 400
	default:
		return 500
	}
}
