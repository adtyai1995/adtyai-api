package service

import (
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// AccountMailLanguage is independent of the interface and request languages.
func AccountMailLanguage(_ *gin.Context, _ int) string {
	return "en"
}

// NotifyAccountSecurityChange never includes credentials or tokens. The caller
// records delivery failure independently from the already-committed change.
func NotifyAccountSecurityChange(_ string, email string, userID int, eventKey string, eventArgs ...map[string]any) error {
	if email == "" {
		return nil
	}
	user, err := model.GetUserById(userID, false)
	if err != nil {
		return err
	}
	site := AdtySiteById(user.SiteId)
	if user.SiteId > 0 && !AdtySiteServesCustomers(site) {
		return ErrAdtySiteNotFound
	}
	subject := AdtyBrandFor(site).Name + " account security notification"
	content := AdtyRenderMail(site, AdtyMailMessage{
		Title:    "Account security notification",
		Intro:    "Your account security settings have changed: " + i18n.Translate("en", eventKey, eventArgs...) + ".",
		Footnote: "If you did not make this change, open your account security settings, revoke other login sessions, and contact your administrator.",
	})
	return AdtySendSiteEmail(site, subject, email, content)
}
