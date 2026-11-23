/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { SiteWallet } from '@/features/adty-ledger/site-wallet-labels'

const TWO_DECIMAL_PATTERN = /^\d+(?:\.\d{1,2})?$/

export type OwnerFundingPreview = {
  valid: boolean
  amount: number
  earningsAvailable: number
  marginFloor: number
  marginExcessAvailable: number
  fromEarnings: number
  fromMargin: number
  shortfall: number
  sufficient: boolean
}

function toCents(raw: string | undefined): number {
  if (!raw) return 0
  const parsed = Number(raw)
  if (!Number.isFinite(parsed)) return 0
  return Math.floor(parsed * 100 + 1e-6)
}

function fromCents(cents: number): number {
  return Math.max(0, Math.round(cents)) / 100
}

export function formatFundingUsd(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0.00'
  return value.toFixed(2)
}

/**
 * Computes the automatic owner funding split for manual customer/team balance
 * credits and site vouchers: available earnings first, then margin strictly
 * above the site level's required deposit floor.
 */
export function computeOwnerFundingPreview(
  wallets: SiteWallet[],
  amountRaw: string
): OwnerFundingPreview {
  const earnings = wallets.find((item) => item.kind === 'earnings')
  const margin = wallets.find((item) => item.kind === 'margin')

  const earningsAvailCents = Math.max(
    0,
    toCents(earnings?.balance) - toCents(earnings?.frozen_usd)
  )
  const marginFloorCents = Math.max(0, toCents(margin?.required_balance))
  const marginExcessCents = Math.max(
    0,
    toCents(margin?.balance) - toCents(margin?.frozen_usd) - marginFloorCents
  )

  const trimmed = amountRaw.trim()
  if (!TWO_DECIMAL_PATTERN.test(trimmed)) {
    return {
      valid: false,
      amount: 0,
      earningsAvailable: fromCents(earningsAvailCents),
      marginFloor: fromCents(marginFloorCents),
      marginExcessAvailable: fromCents(marginExcessCents),
      fromEarnings: 0,
      fromMargin: 0,
      shortfall: 0,
      sufficient: false,
    }
  }
  const amountCents = Math.round(Number(trimmed) * 100)
  if (!Number.isFinite(amountCents) || amountCents <= 0) {
    return {
      valid: false,
      amount: 0,
      earningsAvailable: fromCents(earningsAvailCents),
      marginFloor: fromCents(marginFloorCents),
      marginExcessAvailable: fromCents(marginExcessCents),
      fromEarnings: 0,
      fromMargin: 0,
      shortfall: 0,
      sufficient: false,
    }
  }

  const fromEarningsCents = Math.min(earningsAvailCents, amountCents)
  const neededMarginCents = amountCents - fromEarningsCents
  const fromMarginCents = Math.min(marginExcessCents, neededMarginCents)
  const shortfallCents = Math.max(0, neededMarginCents - marginExcessCents)

  return {
    valid: true,
    amount: fromCents(amountCents),
    earningsAvailable: fromCents(earningsAvailCents),
    marginFloor: fromCents(marginFloorCents),
    marginExcessAvailable: fromCents(marginExcessCents),
    fromEarnings: fromCents(fromEarningsCents),
    fromMargin: fromCents(fromMarginCents),
    shortfall: fromCents(shortfallCents),
    sufficient: shortfallCents === 0,
  }
}
