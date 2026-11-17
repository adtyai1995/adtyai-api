package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
)

// Margin holds already include order reservations. Store F+R without guessing
// a split or claiming R=0; the two constraints both subtract that same total.
// CycleNumber=0 and a nil initial balance preserve unknown legacy history.
type adtyMarginCycleSnapshot struct {
	Version           int              `json:"version"`
	CycleNumber       int64            `json:"cycle_number"`
	StartedAt         int64            `json:"started_at"`
	InitialBalanceUSD *decimal.Decimal `json:"initial_balance_usd"`
	TierUSD           decimal.Decimal  `json:"tier_usd"`
	LimitUSD          decimal.Decimal  `json:"limit_usd"`
	RetainedUSD       decimal.Decimal  `json:"retained_usd"`
	BalanceUSD        decimal.Decimal  `json:"balance_usd"`
	UsedUSD           decimal.Decimal  `json:"used_usd"`
	FrozenReservedUSD decimal.Decimal  `json:"frozen_and_reserved_usd"`
	CycleRoomUSD      decimal.Decimal  `json:"cycle_room_usd"`
	CashRoomUSD       decimal.Decimal  `json:"cash_room_usd"`
	NewDrawUSD        decimal.Decimal  `json:"new_draw_usd"`
}

func snapshotAdtyMarginCycle(wallet model.AdtySiteWallet) (*string, error) {
	limit := AdtyMarginCycleLimit(wallet.CycleTierUSD)
	retained := wallet.CycleTierUSD.Sub(limit)
	cycleRoom := decimal.Max(decimal.Zero, limit.Sub(wallet.CycleUsedUSD).Sub(wallet.FrozenUSD))
	cashRoom := decimal.Max(decimal.Zero, wallet.Balance.Sub(wallet.FrozenUSD).Sub(retained))
	data, err := common.Marshal(adtyMarginCycleSnapshot{
		Version: 1, CycleNumber: wallet.CycleNumber, StartedAt: wallet.CycleStartedAt,
		InitialBalanceUSD: wallet.CycleInitialUSD, TierUSD: wallet.CycleTierUSD,
		LimitUSD: limit, RetainedUSD: retained, BalanceUSD: wallet.Balance,
		UsedUSD: wallet.CycleUsedUSD, FrozenReservedUSD: wallet.FrozenUSD,
		CycleRoomUSD: cycleRoom, CashRoomUSD: cashRoom, NewDrawUSD: decimal.Min(cycleRoom, cashRoom),
	})
	if err != nil {
		return nil, err
	}
	text := string(data)
	return &text, nil
}

// AdtyMarginDrawInput is one margin cycle. TierUSD is the frozen tier T.
// Used, Frozen and Reserved occupy the 90% cap. Balance is the real margin cash.
type AdtyMarginDrawInput struct {
	TierUSD     decimal.Decimal
	Balance     decimal.Decimal
	Used        decimal.Decimal
	Frozen      decimal.Decimal
	Reserved    decimal.Decimal
	CycleOpened bool
}

// AdtyMarginCycleLimit is 90% of the frozen tier. The other 10% stays retained.
func AdtyMarginCycleLimit(tier decimal.Decimal) decimal.Decimal {
	if !tier.IsPositive() {
		return decimal.Zero
	}
	return tier.Mul(decimal.RequireFromString("0.9"))
}

// AdtyMarginNewDraw is the additional amount this cycle may still use.
// A closed cycle that has already reached the cap returns zero until a real
// top-up opens a new cycle (CycleOpened with Used reset by the caller).
func AdtyMarginNewDraw(in AdtyMarginDrawInput) decimal.Decimal {
	limit := AdtyMarginCycleLimit(in.TierUSD)
	if !limit.IsPositive() {
		return decimal.Zero
	}
	occupied := in.Used.Add(in.Frozen).Add(in.Reserved)
	if occupied.GreaterThanOrEqual(limit) && !in.CycleOpened {
		return decimal.Zero
	}
	if in.CycleOpened {
		occupied = in.Used.Add(in.Frozen).Add(in.Reserved)
	}
	capRoom := limit.Sub(occupied)
	retained := in.TierUSD.Sub(limit)
	cashRoom := in.Balance.Sub(in.Frozen).Sub(in.Reserved).Sub(retained)
	room := decimal.Min(capRoom, cashRoom)
	if room.IsNegative() {
		return decimal.Zero
	}
	return room
}
