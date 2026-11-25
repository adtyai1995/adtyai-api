package controller

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

var sendAccountDeletionEmail = service.AdtySendSiteEmail

func accountDeletionCodeKey(userID int, email string) string {
	return fmt.Sprintf("%d:%s", userID, model.NormalizeEmail(email))
}

// SendAccountDeletionCode emails a single-use code bound to this account.
// A send failure leaves no usable code and does not delete the account.
func SendAccountDeletionCode(c *gin.Context) {
	setAuthNoStore(c)
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user.Role == common.RoleRootUser {
		common.ApiErrorT(c, "Cannot delete super administrator account")
		return
	}
	if user.Status != common.UserStatusEnabled {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("This account cannot request deletion"))
		return
	}
	if user.StaffSiteId > 0 {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Site staff accounts cannot self-delete"))
		return
	}
	isSiteOwner, ownerErr := service.AdtyIsSiteOwnerStrict(user.Id)
	if ownerErr != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Service temporarily unavailable"))
		return
	}
	if isSiteOwner {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Site owner accounts cannot self-delete while owning a site"))
		return
	}
	email := model.NormalizeEmail(user.Email)
	if email == "" || !strings.Contains(email, "@") {
		common.ApiErrorT(c, "Add an email address before deleting this account")
		return
	}
	if err := common.VerificationStoreReady(); err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Please try again later"))
		return
	}
	code := common.GenerateVerificationCode(6)
	key := accountDeletionCodeKey(user.Id, email)
	if err := common.RegisterVerificationCodeWithKey(key, code, common.AccountDeletionPurpose); err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Please try again later"))
		return
	}
	site := service.AdtySiteForRequest(c)
	subject := service.AdtyBrandFor(site).Name + " account deletion confirmation"
	content := service.AdtyRenderMail(site, service.AdtyMailMessage{
		Title:    "Confirm account deletion",
		Intro:    "Enter this code only if you want to delete your account.",
		Code:     code,
		Footnote: fmt.Sprintf("This code expires in %d minutes and can be used once. If you did not request account deletion, ignore this email.", common.VerificationValidMinutes),
	})
	if err := sendAccountDeletionEmail(site, subject, email, content); err != nil {
		_, _ = common.ConsumeVerificationCodeWithKey(key, code, common.AccountDeletionPurpose)
		common.ApiErrorT(c, "Failed to send email")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{}})
}

func consumeAccountDeletionCode(c *gin.Context, userID int, email string) (string, time.Time, bool) {
	var body struct {
		Code string `json:"verification_code"`
	}
	if c.Request.Body != nil {
		_ = common.DecodeJson(c.Request.Body, &body)
	}
	code := strings.TrimSpace(body.Code)
	if code == "" || strings.TrimSpace(email) == "" {
		common.ApiErrorT(c, "Email confirmation code is invalid")
		return "", time.Time{}, false
	}
	issued, status := common.ConsumeVerificationCodeWithKeyStatus(accountDeletionCodeKey(userID, email), code, common.AccountDeletionPurpose)
	if status != common.VerificationOK {
		if status == common.VerificationLocked {
			common.ApiErrorT(c, service.ErrVerificationCodeLocked.Error())
		} else {
			common.ApiErrorT(c, "Email confirmation code is invalid")
		}
		return "", time.Time{}, false
	}
	return code, issued, true
}
