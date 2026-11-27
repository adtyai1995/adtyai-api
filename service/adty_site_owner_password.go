package service

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// ResetAdtySiteCustomerPassword lets a site owner change or randomly reset the
// password of a common customer belonging strictly to their open (non-closed)
// site. Existing sessions, PATs, and legacy access tokens are revoked immediately.
func ResetAdtySiteCustomerPassword(siteID, ownerUserID, customerID int, manualPassword string, random bool, length int) (targetUsername string, generatedPassword string, err error) {
	if siteID <= 0 || ownerUserID <= 0 || customerID <= 0 {
		return "", "", common.NewMessage("Invalid parameters")
	}
	if customerID == ownerUserID {
		return "", "", ErrAdtySiteCustomerNotFound
	}
	var newPassword string
	if random {
		newPassword, err = common.GenerateRandomAccountPassword(length)
		if err != nil {
			return "", "", err
		}
		generatedPassword = newPassword
	} else {
		if err := common.ValidateNewAccountPassword(manualPassword); err != nil {
			return "", "", err
		}
		newPassword = manualPassword
	}
	hash, err := common.HashAccountPassword(newPassword)
	if err != nil {
		return "", "", err
	}

	var targetEmail string
	var auditEvent *model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// Canonical lock order: AdtySite -> User.
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotOwner
			}
			return err
		}
		if site.OwnerUserId != ownerUserID {
			return ErrAdtySiteNotOwner
		}
		if !AdtySiteAgreementAccepted(site.AgreementVer) {
			return ErrAdtySiteAgreement
		}
		switch site.Status {
		case model.AdtySiteStatusActive, model.AdtySiteStatusPaused:
		case model.AdtySiteStatusClosing:
			return ErrAdtySiteClosing
		default:
			return ErrAdtySiteNotOwner
		}

		var customer model.User
		if err := model.AdtyLockForUpdate(tx).First(&customer, customerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteCustomerNotFound
			}
			return err
		}
		if customer.SiteId != siteID ||
			customer.Role != common.RoleCommonUser ||
			customer.Id == ownerUserID ||
			customer.StaffSiteId != 0 ||
			!customer.DeletedAt.Time.IsZero() ||
			customer.DeletedAt.Valid {
			return ErrAdtySiteCustomerNotFound
		}
		if isEmp, empErr := adtyIsIndependentTeamEmployeeTx(tx, customerID); empErr != nil {
			return empErr
		} else if isEmp {
			return ErrAdtySiteCustomerNotFound
		}

		if _, err := model.IncrementUserAuthVersionWithTx(tx, customer.Id); err != nil {
			return err
		}
		if _, err := model.DeleteUserAccessTokensWithTx(tx, customer.Id); err != nil {
			return err
		}
		if err := tx.Model(&model.User{}).
			Where("id = ? AND site_id = ? AND staff_site_id = 0 AND role = ?", customer.Id, siteID, common.RoleCommonUser).
			Updates(map[string]any{
				"password":                hash,
				"access_token":            nil,
				"access_token_created_at": nil,
			}).Error; err != nil {
			return err
		}

		targetUsername = customer.Username
		targetEmail = customer.Email
		auditBefore := map[string]any{
			"site_id":     siteID,
			"customer_id": customer.Id,
		}
		auditAfter := map[string]any{
			"site_id":          siteID,
			"customer_id":      customer.Id,
			"username":         customer.Username,
			"random":           random,
			"sessions_revoked": true,
		}
		if err := recordSiteCompatibilityAuditTx(tx, siteID, "customer.password_reset", ownerUserID, auditBefore, auditAfter, ""); err != nil {
			return err
		}
		auditEvent, err = appendAdtyOfficialAuditTx(tx, ownerUserID, siteID, "site_customer", strconv.Itoa(customer.Id),
			"adty.site.customer.password_reset", "", "", auditBefore, auditAfter, model.AuditResultSuccess)
		return err
	})
	if err != nil {
		return "", "", err
	}

	if err := model.PublishUserAuthCache(customerID); err != nil {
		common.SysError(common.LogText("failed to publish customer auth cache after password reset: user=%d err=%v", customerID, err))
	}
	if _, err := model.RevokeAllUserSessions(customerID, "site_owner_customer_password_reset"); err != nil {
		common.SysError(common.LogText("failed to revoke customer sessions after password reset: user=%d err=%v", customerID, err))
	}
	common.ClearAccountLoginFailures(customerID, targetUsername, targetEmail, siteID)
	if auditEvent != nil && auditEvent.EventId != "" {
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return targetUsername, generatedPassword, nil
}
