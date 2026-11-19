package service

import (
	"errors"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
)

const (
	// WalletLotProjectionSourceBatches is a remaining-by-kind sum of stored lots.
	WalletLotProjectionSourceBatches = "fund_batches"
	// WalletLotProjectionSourceLegacy is the historical display-only stand-in for a
	// user with no stored lots. Those users now get no projection at all
	// (ISS-R4-0074): the stand-in reported the raw balance, which is not the logical
	// balance the payload's quota field carries once quota_pending is non-zero.
	WalletLotProjectionSourceLegacy = "legacy_principal"
)

// WalletLotProjection splits stored lots into principal, permanent bonus and
// expiring bonus. All USD amounts use the shared wallet conversion (DEC-001).
type WalletLotProjection struct {
	ExpiringBonusQuota int64           `json:"expiring_bonus_quota"`
	ExpiringBonusUSD   decimal.Decimal `json:"expiring_bonus_usd"`
	NextExpiresAt      int64           `json:"next_expires_at,omitempty"`
	PrincipalQuota     int64           `json:"principal_quota"`
	BonusQuota         int64           `json:"bonus_quota"`
	TotalQuota         int64           `json:"total_quota"`
	PrincipalUSD       decimal.Decimal `json:"principal_usd"`
	BonusUSD           decimal.Decimal `json:"bonus_usd"`
	TotalUSD           decimal.Decimal `json:"total_usd"`
	Source             string          `json:"source"`
}

// ProjectWalletLotsForUser returns the lot remaining projection for one account.
// ok is false when the fund-batch table is missing so callers can omit the field
// instead of inventing numbers or failing login. 待履约 (not-yet-granted) bonus
// is not counted because it is not stored as a spendable bonus lot.
func ProjectWalletLotsForUser(user *model.User) (WalletLotProjection, bool, error) {
	if user == nil || user.Id <= 0 {
		return WalletLotProjection{}, false, nil
	}
	projections, err := ProjectWalletLotsForUsers([]model.User{*user})
	if err != nil {
		if errors.Is(err, model.ErrAdtyFundBatchTableMissing) {
			return WalletLotProjection{}, false, nil
		}
		return WalletLotProjection{}, false, err
	}
	proj, ok := projections[user.Id]
	return proj, ok, nil
}

// ProjectWalletLotsForUsers returns remaining-by-kind projections for many accounts.
func ProjectWalletLotsForUsers(users []model.User) (map[int]WalletLotProjection, error) {
	out := map[int]WalletLotProjection{}
	if len(users) == 0 {
		return out, nil
	}
	ids := make([]int, 0, len(users))
	for _, user := range users {
		if user.Id > 0 {
			ids = append(ids, user.Id)
		}
	}
	sums, err := model.SumAdtyFundBatchRemainingByKindForUsers(ids)
	if err != nil {
		return nil, err
	}
	for _, user := range users {
		if user.Id <= 0 {
			continue
		}
		sum := sums[user.Id]
		if !sum.HasLots {
			// No stored lots means no principal/bonus split to report. A stand-in built
			// from the raw balance would disagree with the logical balance the payload's
			// quota field carries whenever quota_pending != 0 (ISS-R4-0074), so omit.
			continue
		}
		projection := walletLotProjectionFromQuota(sum.Principal, sum.Bonus, WalletLotProjectionSourceBatches)
		projection.ExpiringBonusQuota = sum.ExpiringBonus
		projection.ExpiringBonusUSD = WalletUSDPartsFromQuota(0, sum.ExpiringBonus).Bonus
		projection.NextExpiresAt = sum.NextExpiresAt
		projection.TotalQuota += sum.ExpiringBonus
		projection.TotalUSD = WalletUSDPartsFromQuota(sum.Principal, sum.Bonus+sum.ExpiringBonus).Total
		out[user.Id] = projection
	}
	return out, nil
}

func walletLotProjectionFromQuota(principalQuota, bonusQuota int64, source string) WalletLotProjection {
	parts := WalletUSDPartsFromQuota(principalQuota, bonusQuota)
	return WalletLotProjection{
		PrincipalQuota: principalQuota,
		BonusQuota:     bonusQuota,
		TotalQuota:     principalQuota + bonusQuota,
		PrincipalUSD:   parts.Principal,
		BonusUSD:       parts.Bonus,
		TotalUSD:       parts.Total,
		Source:         source,
	}
}
