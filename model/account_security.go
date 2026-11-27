package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrCurrentPasswordInvalid = errors.New("Current password is incorrect.")
	ErrAccountPasswordState   = errors.New("Password settings have changed. Reload the page and try again.")
	ErrLastLoginMethod        = errors.New("Add another login method before unlinking this account.")
	ErrAccountBindingChanged  = errors.New("Account bindings have changed. Start this operation again.")
	ErrCannotDeleteRootUser   = errors.New("The root account cannot be deleted.")
)

// ChangeUserPassword rechecks the authorized session, password state and current
// password under the same user lock as the credential update. Only self-profile
// fields are accepted; a stale snapshot cannot restore roles or account status.
var ErrPasswordVerificationLocked = errors.New("Too many failed attempts. Please try again later.")

func ChangeUserPassword(identity AuthSessionIdentity, update *User, firstPassword bool) error {
	hash, err := common.HashAccountPassword(update.Password)
	if err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			return err
		}
		var current User
		if err := tx.First(&current, identity.UserID).Error; err != nil {
			return err
		}
		if firstPassword != (current.Password == "") {
			return ErrAccountPasswordState
		}
		if !firstPassword {
			if common.LoginLockedUser(current.Id) || common.LoginLocked(current.Username) {
				return ErrPasswordVerificationLocked
			}
			if !common.ValidatePasswordAndHash(update.OriginalPassword, current.Password) {
				if recErr := common.RecordLoginFailureUser(current.Id); recErr != nil {
					common.SysError(common.LogText("password change login failure lock unavailable: %s", recErr.Error()))
					return ErrPasswordVerificationLocked
				}
				return ErrCurrentPasswordInvalid
			}
			common.ClearAccountLoginFailures(current.Id, current.Username, current.Email, current.SiteId)
		}
		if current.Password != "" && common.ValidatePasswordAndHash(update.Password, current.Password) {
			return common.ErrAccountPasswordSame
		}
		changes := map[string]any{"password": hash, "access_token": nil, "access_token_created_at": nil}
		trimmedUsername := strings.TrimSpace(update.Username)
		if trimmedUsername != "" {
			if !ValidateUsernameNoControl(trimmedUsername) {
				return common.NewMessage("Invalid parameters")
			}
			if err := EnsureUsernameAvailableWithTx(tx, trimmedUsername, identity.UserID); err != nil {
				return err
			}
			changes["username"] = trimmedUsername
		}
		if update.DisplayName != "" {
			changes["display_name"] = update.DisplayName
		}
		if _, err := IncrementUserAuthVersionWithTx(tx, identity.UserID); err != nil {
			return err
		}
		if _, err := DeleteUserAccessTokensWithTx(tx, identity.UserID); err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", identity.UserID).Updates(changes).Error; err != nil {
			if isUsernameUniqueViolation(err) {
				return ErrUsernameAlreadyTaken
			}
			return err
		}
		return tx.First(update, identity.UserID).Error
	})
}
