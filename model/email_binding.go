package model

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	EmailBindingTTL         = 10 * time.Minute
	EmailBindingResendDelay = time.Minute
	EmailBindingMaxAttempts = 5
)

var (
	ErrEmailBindingCodeInvalid = errors.New("Email verification code is incorrect.")
	ErrEmailBindingLocked      = errors.New("Too many incorrect codes. Start email verification again.")
	ErrEmailBindingResendWait  = errors.New("Please wait before requesting another verification code.")
)

// EmailBindingState is server-owned payload. It deliberately contains only
// salted code hashes and an already-consumed authorization, never usable codes
// or proof tokens. Resending must retain the deadline and failure counter.
// SiteID and OriginHost bind the ceremony to the exact site and initiating host
// required by A.15 (line 1768).
type EmailBindingState struct {
	Authorization  *AuthFlowAuthorization `json:"authorization"`
	SiteID         int                    `json:"site_id"`
	OriginHost     string                 `json:"origin_host,omitempty"`
	CurrentEmail   string                 `json:"current_email"`
	Email          string                 `json:"email"`
	NewCodeHash    string                 `json:"new_code_hash"`
	OldCodeHash    string                 `json:"old_code_hash,omitempty"`
	FailedAttempts int                    `json:"failed_attempts"`
	ResendAt       int64                  `json:"resend_at"`
}

// NormalizeEmailBindingHost normalizes a trusted request Host (lowercase, port
// and trailing dot stripped) and rejects malformed, wildcard, or cookie-widened
// host values.
func NormalizeEmailBindingHost(raw string) string {
	host := strings.TrimSpace(raw)
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		h, _, err := net.SplitHostPort(host)
		if err != nil {
			return ""
		}
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || len(host) > 253 || strings.HasPrefix(host, ".") || strings.Contains(host, "..") {
		return ""
	}
	for _, r := range host {
		if unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune("/*?#@\\\"", r) {
			return ""
		}
	}
	return host
}

func resolveEmailBindingCreateHost(stateHost string, originHost ...string) (string, error) {
	if len(originHost) > 0 {
		h := NormalizeEmailBindingHost(originHost[0])
		if h == "" {
			return "", ErrAuthFlowInvalid
		}
		if stateHost != "" && NormalizeEmailBindingHost(stateHost) != h {
			return "", ErrAuthFlowInvalid
		}
		return h, nil
	}
	if stateHost != "" {
		h := NormalizeEmailBindingHost(stateHost)
		if h == "" {
			return "", ErrAuthFlowInvalid
		}
		return h, nil
	}
	return "", nil
}

func matchEmailBindingOriginHost(expected string, originHost ...string) error {
	expectedNorm := NormalizeEmailBindingHost(expected)
	if expected != "" && expectedNorm == "" {
		return ErrAuthFlowInvalid
	}
	if expectedNorm == "" && len(originHost) == 0 {
		return nil
	}
	if expectedNorm == "" || len(originHost) == 0 {
		return ErrAuthFlowInvalid
	}
	got := NormalizeEmailBindingHost(originHost[0])
	if got == "" || got != expectedNorm {
		return ErrAuthFlowInvalid
	}
	return nil
}

func authFlowProviderForHost(host string) string {
	if len(host) <= 64 {
		return host
	}
	return ""
}

func mainConfiguredHosts(tx *gorm.DB) []string {
	keys := []string{"ServerAddress", "adty_site_setting.api_base_url"}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		common.OptionMapRWMutex.RLock()
		raw := common.OptionMap[key]
		common.OptionMapRWMutex.RUnlock()
		if raw == "" && tx != nil && tx.Migrator().HasTable(&Option{}) {
			var opt Option
			if tx.Where("key = ?", key).Limit(1).Find(&opt).RowsAffected > 0 {
				raw = opt.Value
			}
		}
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		if h := NormalizeEmailBindingHost(parsed.Host); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func validateEmailBindingOriginWithTx(tx *gorm.DB, siteID int, host string) error {
	if siteID < 0 {
		return ErrAuthFlowInvalid
	}
	hasSitesTable := tx != nil && tx.Migrator().HasTable(&AdtySite{})
	if siteID > 0 && hasSitesTable {
		var site AdtySite
		if err := tx.Where("id = ?", siteID).Limit(1).Find(&site).Error; err != nil {
			return err
		}
		if site.Id != 0 && (site.Status == AdtySiteStatusClosed || site.Status == AdtySiteStatusPending) {
			return ErrAuthFlowInvalid
		}
	}
	if host == "" {
		return nil
	}
	for _, mainHost := range mainConfiguredHosts(tx) {
		if host == mainHost {
			if siteID > 0 {
				return ErrAuthFlowInvalid
			}
			// R1-011: An exact configured main-site host (website or API host) is
			// always valid for main-site (siteID == 0) email bindings even when a
			// reseller site shares the same parent root domain.
			return nil
		}
	}
	if hasSitesTable {
		var sites []AdtySite
		if err := tx.Select("id", "subdomain", "root_domain", "status").Find(&sites).Error; err != nil {
			return err
		}
		for _, s := range sites {
			rootDom := strings.ToLower(strings.TrimSpace(s.RootDomain))
			if s.Subdomain != "" && rootDom != "" {
				subHost := strings.ToLower(strings.TrimSpace(s.Subdomain)) + "." + rootDom
				if host == subHost {
					if s.Id != siteID || s.Status == AdtySiteStatusClosed || s.Status == AdtySiteStatusPending {
						return ErrAuthFlowInvalid
					}
				}
			}
			if rootDom != "" && host == rootDom {
				// Never allow bare reseller root domain to act as a site or main host.
				return ErrAuthFlowInvalid
			}
			if siteID == 0 && rootDom != "" && strings.HasSuffix(host, "."+rootDom) {
				return ErrAuthFlowInvalid
			}
		}
	}
	if tx != nil && tx.Migrator().HasTable(&AdtySiteDomain{}) {
		var domains []AdtySiteDomain
		if err := tx.Where("LOWER(hostname) = ?", host).Find(&domains).Error; err != nil {
			return err
		}
		if len(domains) > 0 {
			activeMatch := false
			for _, d := range domains {
				if d.SiteId == siteID && d.Status == AdtySiteDomainActive && d.DesiredState != "removed" {
					activeMatch = true
				} else {
					return ErrAuthFlowInvalid
				}
			}
			if !activeMatch {
				return ErrAuthFlowInvalid
			}
		}
	}
	return nil
}

func isSiteOwnerForEmailBindingWithTx(tx *gorm.DB, userID, siteID int) (bool, error) {
	if tx == nil {
		tx = DB
	}
	if userID <= 0 || siteID <= 0 || !tx.Migrator().HasTable(&AdtySite{}) {
		return false, nil
	}
	var count int64
	if err := tx.Model(&AdtySite{}).
		Where("id = ? AND owner_user_id = ? AND status <> ?", siteID, userID, AdtySiteStatusClosed).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func resolveEmailBindingOriginSiteWithTx(tx *gorm.DB, identity AuthSessionIdentity, userSiteID int, originHost string) (int, error) {
	issuedSite, ok := identity.IssuedSite()
	if ok {
		if issuedSite == userSiteID {
			return userSiteID, nil
		}
		if userSiteID == 0 && issuedSite > 0 {
			isOwner, err := isSiteOwnerForEmailBindingWithTx(tx, identity.UserID, issuedSite)
			if err != nil {
				return 0, err
			}
			if isOwner {
				return issuedSite, nil
			}
		}
		return 0, ErrAuthFlowInvalid
	}
	if userSiteID == 0 && originHost != "" {
		if _, isPAT := ParseAccessTokenSessionID(identity.SessionID); isPAT {
			if siteID, err := resolveOwnerSubsiteIDForHostWithTx(tx, identity.UserID, originHost); err != nil {
				return 0, err
			} else if siteID > 0 {
				return siteID, nil
			}
		}
	}
	return userSiteID, nil
}

func resolveOwnerSubsiteIDForHostWithTx(tx *gorm.DB, ownerUserID int, host string) (int, error) {
	if tx == nil {
		tx = DB
	}
	host = NormalizeEmailBindingHost(host)
	if ownerUserID <= 0 || host == "" || !tx.Migrator().HasTable(&AdtySite{}) {
		return 0, nil
	}
	for _, mainHost := range mainConfiguredHosts(tx) {
		if host == mainHost {
			return 0, nil
		}
	}
	var sites []AdtySite
	if err := tx.Select("id", "subdomain", "root_domain", "status").
		Where("owner_user_id = ? AND status NOT IN ?", ownerUserID, []string{AdtySiteStatusClosed, AdtySiteStatusPending}).
		Find(&sites).Error; err != nil {
		return 0, err
	}
	if len(sites) == 0 {
		return 0, nil
	}
	ownedIDs := make([]int, 0, len(sites))
	for _, s := range sites {
		ownedIDs = append(ownedIDs, s.Id)
		rootDom := strings.ToLower(strings.TrimSpace(s.RootDomain))
		if s.Subdomain != "" && rootDom != "" && host == strings.ToLower(strings.TrimSpace(s.Subdomain))+"."+rootDom {
			return s.Id, nil
		}
	}
	if tx.Migrator().HasTable(&AdtySiteDomain{}) {
		var domain AdtySiteDomain
		res := tx.Where("LOWER(hostname) = ? AND site_id IN ? AND status = ? AND desired_state <> ?",
			host, ownedIDs, AdtySiteDomainActive, "removed").Limit(1).Find(&domain)
		if res.Error != nil {
			return 0, res.Error
		}
		if res.RowsAffected > 0 {
			return domain.SiteId, nil
		}
	}
	return 0, nil
}

func CreateEmailBinding(identity AuthSessionIdentity, state EmailBindingState, originHost ...string) (string, *AuthFlow, error) {
	var token string
	var flow *AuthFlow
	err := DB.Transaction(func(tx *gorm.DB) error {
		host, err := resolveEmailBindingCreateHost(state.OriginHost, originHost...)
		if err != nil {
			return err
		}
		state.OriginHost = host
		siteID, err := siteIDForUser(tx, identity.UserID)
		if err != nil {
			return err
		}
		originSiteID, err := resolveEmailBindingOriginSiteWithTx(tx, identity, siteID, state.OriginHost)
		if err != nil {
			return err
		}
		if state.SiteID != 0 && state.SiteID != siteID {
			return ErrAuthFlowInvalid
		}
		state.SiteID = siteID
		if err := validateEmailBindingOriginWithTx(tx, originSiteID, state.OriginHost); err != nil {
			return err
		}
		if err := validateEmailBindingAccountWithTx(tx, identity, &state); err != nil {
			return err
		}
		if err := ensureEmailAvailableWithTx(tx, state.Email, siteID, identity.UserID); err != nil {
			return err
		}
		payload, err := common.Marshal(state)
		if err != nil {
			return err
		}
		token, flow, err = createAuthFlowWithTx(tx, AuthFlowCreate{
			Purpose: AuthFlowPurposeEmailBinding, Provider: authFlowProviderForHost(state.OriginHost),
			UserId: identity.UserID, SessionId: identity.SessionID,
			Payload: string(payload), ExpiresAt: time.Now().Add(EmailBindingTTL),
		})
		return err
	})
	return token, flow, err
}

func GetEmailBinding(identity AuthSessionIdentity, token string, originHost ...string) (*AuthFlow, *EmailBindingState, error) {
	match := AuthFlowMatch{Purpose: AuthFlowPurposeEmailBinding, UserId: identity.UserID, SessionId: identity.SessionID}
	if len(originHost) > 0 {
		h := NormalizeEmailBindingHost(originHost[0])
		if h == "" {
			return nil, nil, ErrAuthFlowInvalid
		}
		match.Provider = authFlowProviderForHost(h)
	}
	flow, err := GetAuthFlow(token, match)
	if err != nil {
		return nil, nil, err
	}
	var state EmailBindingState
	if err := common.UnmarshalJsonStr(flow.Payload, &state); err != nil {
		return nil, nil, ErrAuthFlowInvalid
	}
	if err := matchEmailBindingOriginHost(state.OriginHost, originHost...); err != nil {
		return nil, nil, err
	}
	originSiteID, err := resolveEmailBindingOriginSiteWithTx(DB, identity, state.SiteID, state.OriginHost)
	if err != nil {
		return nil, nil, err
	}
	if err := validateEmailBindingOriginWithTx(DB, originSiteID, state.OriginHost); err != nil {
		return nil, nil, err
	}
	return flow, &state, nil
}

func ResendEmailBinding(identity AuthSessionIdentity, token, newCodeHash, oldCodeHash string, originHost ...string) (*AuthFlow, *EmailBindingState, error) {
	var flow *AuthFlow
	var state *EmailBindingState
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		flow, state, err = lockEmailBindingWithTx(tx, identity, token, originHost...)
		if err != nil {
			return err
		}
		now := time.Now()
		if state.ResendAt > now.Unix() {
			return ErrEmailBindingResendWait
		}
		if newCodeHash == "" || (state.OldCodeHash != "") != (oldCodeHash != "") {
			return ErrAuthFlowInvalid
		}
		state.NewCodeHash, state.OldCodeHash = newCodeHash, oldCodeHash
		state.ResendAt = now.Add(EmailBindingResendDelay).Unix()
		payload, err := common.Marshal(state)
		if err != nil {
			return err
		}
		return tx.Model(flow).Update("payload", string(payload)).Error
	})
	return flow, state, err
}

// CompleteEmailBinding commits failed attempts even though verification fails.
// Successful confirmation consumes the flow in the same transaction as the
// email update, preserving the existing cross-database email ownership lock.
func CompleteEmailBinding(identity AuthSessionIdentity, token, email, newCode, oldCode string, originHost ...string) (*EmailBindingState, error) {
	var state *EmailBindingState
	var verificationError error
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Acquire ownership protection before the first consistent read. Under
		// MySQL REPEATABLE READ, a snapshot created before this lock could hide
		// an address claimed by a concurrent transaction while we were waiting.
		if email == "" || email != NormalizeEmail(email) {
			return ErrAuthFlowInvalid
		}
		siteID, err := siteIDForUser(tx, identity.UserID)
		if err != nil {
			return err
		}
		if err := lockNormalizedEmail(tx, email, siteID); err != nil {
			return err
		}
		flow, lockedState, err := lockEmailBindingWithTx(tx, identity, token, originHost...)
		if err != nil {
			return err
		}
		state = lockedState
		if state.Email != email || state.SiteID != siteID {
			return ErrAuthFlowInvalid
		}
		newCode, newCodeError := common.ValidateNumericCode(newCode)
		oldCode, oldCodeError := common.ValidateNumericCode(oldCode)
		newValid := newCodeError == nil && common.ValidatePasswordAndHash(newCode, state.NewCodeHash)
		oldValid := state.OldCodeHash == "" || oldCodeError == nil && common.ValidatePasswordAndHash(oldCode, state.OldCodeHash)
		if !newValid || !oldValid {
			state.FailedAttempts++
			verificationError = ErrEmailBindingCodeInvalid
			if state.FailedAttempts >= EmailBindingMaxAttempts {
				verificationError = ErrEmailBindingLocked
			}
			payload, err := common.Marshal(state)
			if err != nil {
				return err
			}
			return tx.Model(flow).Update("payload", string(payload)).Error
		}
		if err := ensureEmailAvailableWithTx(tx, state.Email, siteID, identity.UserID); err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", identity.UserID).Update("email", state.Email).Error; err != nil {
			if isSiteEmailUniqueViolation(err) {
				return ErrEmailAlreadyTaken
			}
			return err
		}
		return tx.Model(flow).Update("consumed_at", time.Now()).Error
	})
	if err != nil {
		return nil, err
	}
	return state, verificationError
}

func lockEmailBindingWithTx(tx *gorm.DB, identity AuthSessionIdentity, token string, originHost ...string) (*AuthFlow, *EmailBindingState, error) {
	match := AuthFlowMatch{Purpose: AuthFlowPurposeEmailBinding, UserId: identity.UserID, SessionId: identity.SessionID}
	if len(originHost) > 0 {
		h := NormalizeEmailBindingHost(originHost[0])
		if h == "" {
			return nil, nil, ErrAuthFlowInvalid
		}
		match.Provider = authFlowProviderForHost(h)
	}
	// Make the first statement a write so SQLite does not have to upgrade a
	// deferred read transaction. A no-op update also locks the row on MySQL and
	// PostgreSQL; do not interpret dialect-dependent RowsAffected as success.
	if err := applyAuthFlowMatch(tx.Model(&AuthFlow{}), token, match).
		Where("consumed_at IS NULL AND expires_at > ?", time.Now()).
		UpdateColumn("payload", gorm.Expr("payload")).Error; err != nil {
		return nil, nil, err
	}
	var flow AuthFlow
	if err := applyAuthFlowMatch(tx, token, match).First(&flow).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrAuthFlowInvalid
		}
		return nil, nil, err
	}
	if flow.ConsumedAt != nil {
		return nil, nil, ErrAuthFlowConsumed
	}
	if !flow.ExpiresAt.After(time.Now()) {
		return nil, nil, ErrAuthFlowExpired
	}
	var state EmailBindingState
	if err := common.UnmarshalJsonStr(flow.Payload, &state); err != nil {
		return nil, nil, ErrAuthFlowInvalid
	}
	if err := matchEmailBindingOriginHost(state.OriginHost, originHost...); err != nil {
		return nil, nil, err
	}
	if err := validateEmailBindingAccountWithTx(tx, identity, &state); err != nil {
		return nil, nil, err
	}
	if state.FailedAttempts >= EmailBindingMaxAttempts {
		return nil, nil, ErrEmailBindingLocked
	}
	return &flow, &state, nil
}

func validateEmailBindingAccountWithTx(tx *gorm.DB, identity AuthSessionIdentity, state *EmailBindingState) error {
	if state.Authorization == nil || state.Authorization.ProofID <= 0 || state.Authorization.AuthSessionIdentity != identity ||
		state.Authorization.Scope != "account.binding.bind" || state.Authorization.ContextHash == "" ||
		state.Email == "" || state.Email != NormalizeEmail(state.Email) || state.NewCodeHash == "" {
		return ErrAuthFlowInvalid
	}
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return err
	}
	siteID, err := siteIDForUser(tx, identity.UserID)
	if err != nil {
		return err
	}
	if state.SiteID != siteID {
		return ErrAuthFlowInvalid
	}
	originSiteID, err := resolveEmailBindingOriginSiteWithTx(tx, identity, siteID, state.OriginHost)
	if err != nil {
		return err
	}
	if err := validateEmailBindingOriginWithTx(tx, originSiteID, state.OriginHost); err != nil {
		return err
	}
	var user User
	if err := tx.Select("email").First(&user, identity.UserID).Error; err != nil {
		return err
	}
	if NormalizeEmail(user.Email) != state.CurrentEmail || state.CurrentEmail == state.Email {
		return ErrAccountBindingChanged
	}
	return nil
}
