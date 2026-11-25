package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func init() {
	model.AdtyCreditOwnerMarginTopUpTx = creditAdtyOwnerMarginTopUpTx
}

func isOwnerTopupBusiness(business string) bool {
	return business == model.AdtyBusinessOwnerSelfUseTopup || business == model.AdtyBusinessOwnerMarginTopup
}

func creditAdtyOwnerMarginTopUpTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
	if ext == nil || topUp == nil || !ext.IsOwnerMargin() || ext.SiteId <= 0 || !ext.FaceUSD.IsPositive() {
		return model.ErrInvalidTopUpQuota
	}
	if err := ensureAdtySiteWalletsTx(tx, ext.SiteId); err != nil {
		return err
	}
	_, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletMargin, ext.FaceUSD, AdtySiteWalletChange{
		Type:            "margin_topup",
		RefType:         "topup",
		RefID:           ext.TradeNo,
		OperatorID:      topUp.UserId,
		IdempotencyKey:  "margin_topup:" + ext.TradeNo,
		OpenMarginCycle: true,
	})
	return err
}

// SiteID is supplied by trusted host resolution, never a checkout body.
type AdtyPaymentScope struct {
	TeamID   int64
	Business string
	SiteID   int
}

func adtyPaymentScopeIdentity(db *gorm.DB, user *model.User, scopes []AdtyPaymentScope, lock bool) (int, string, error) {
	scope := AdtyPaymentScope{Business: model.AdtyBusinessWalletTopup}
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	if scope.Business == model.AdtyBusinessTeamTopup {
		var team model.AdtyTeam
		q := db
		if lock {
			q = model.AdtyLockForUpdate(db)
		}
		if scope.TeamID <= 0 || (scope.SiteID > 0 && scope.SiteID != user.SiteId) {
			return 0, "", ErrAdtyTeamForbidden
		}
		if err := q.Where("id=? AND site_id=? AND leader_user_id=? AND status=?", scope.TeamID, user.SiteId, user.Id, model.AdtyTeamStatusActive).First(&team).Error; err != nil {
			return 0, "", ErrAdtyTeamForbidden
		}
		return team.SiteId, scope.Business, nil
	}
	if scope.Business == "" || scope.Business == model.AdtyBusinessWalletTopup {
		if scope.SiteID > 0 && scope.SiteID != user.SiteId {
			return 0, "", common.NewMessage("This payment method is not available")
		}
		return user.SiteId, model.AdtyBusinessWalletTopup, nil
	}
	if !isOwnerTopupBusiness(scope.Business) || user.StaffSiteId != 0 || user.Status != common.UserStatusEnabled {
		return 0, "", common.NewMessage("This payment method is not available")
	}
	var site model.AdtySite
	q := db
	if lock {
		q = model.AdtyLockForUpdate(q)
	}
	if scope.SiteID > 0 {
		if err := q.First(&site, scope.SiteID).Error; err != nil {
			return 0, "", err
		}
		if site.OwnerUserId != user.Id || site.Status != model.AdtySiteStatusActive {
			return 0, "", common.NewMessage("This payment method is not available")
		}
		return site.Id, scope.Business, nil
	}
	if scope.Business != model.AdtyBusinessOwnerMarginTopup {
		return 0, "", common.NewMessage("This payment method is not available")
	}
	res := q.Where("owner_user_id = ? AND status = ?", user.Id, model.AdtySiteStatusActive).Order("id ASC").Limit(1).Find(&site)
	if res.Error != nil {
		return 0, "", res.Error
	}
	if res.RowsAffected == 0 || site.Id <= 0 {
		return 0, "", common.NewMessage("This payment method is not available")
	}
	return site.Id, scope.Business, nil
}

// ResolveAdtyOwnerTopupSiteID resolves the active owned site ID for owner
// margin top-up flows when the owner accesses the console from the main host
// (where requestSiteID == 0).
func ResolveAdtyOwnerTopupSiteID(userID, requestSiteID int) int {
	if requestSiteID > 0 {
		return requestSiteID
	}
	if userID <= 0 || model.DB == nil {
		return 0
	}
	var site model.AdtySite
	res := model.DB.Select("id").Where("owner_user_id = ? AND status = ?", userID, model.AdtySiteStatusActive).Order("id ASC").Limit(1).Find(&site)
	if res.Error != nil || res.RowsAffected == 0 {
		return 0
	}
	return site.Id
}

func AdtyQuoteForPayment(userID, requestSiteID int, group, business string, face int64, method string) (*AdtyQuote, error) {
	if strings.TrimSpace(business) == model.AdtyBusinessOwnerSelfUseTopup {
		return nil, common.NewMessage("Site owner self-use top-up has been retired")
	}
	user, _, err := adtyPaymentIdentity(model.DB, userID, false)
	if err != nil {
		return nil, err
	}
	if user.Role < common.RoleAdminUser && strings.TrimSpace(business) != model.AdtyBusinessOwnerMarginTopup {
		isOwner, ownerErr := AdtyIsSiteOwnerStrict(user.Id)
		if ownerErr != nil {
			return nil, ownerErr
		}
		if isOwner {
			return nil, common.NewMessage("Site owner accounts can only top up site margin")
		}
	}
	siteID, business, err := adtyPaymentScopeIdentity(model.DB, user, []AdtyPaymentScope{{Business: business, SiteID: requestSiteID}}, false)
	if err != nil {
		return nil, err
	}
	quote, err := adtyQuoteForSite(AdtyBillingSiteForUser(userID), group, face, method, isOwnerTopupBusiness(business))
	if err != nil {
		return nil, err
	}
	quote.Business, quote.SiteID = business, siteID
	return quote, nil
}

func adtyOwnerQuotaQuote(face int64) (*AdtyQuote, error) {
	usd := decimal.NewFromInt(face)
	quota, err := common.WalletQuotaFromDecimalStrict(usd.Mul(common.QuotaPerUnitDecimal()))
	if err != nil {
		return nil, err
	}
	return &AdtyQuote{FaceUSD: usd, CreditQuota: int64(quota), TopupGroupRatio: decimal.NewFromInt(1)}, nil
}
