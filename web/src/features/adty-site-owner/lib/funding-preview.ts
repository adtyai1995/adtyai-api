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
const SCALE_DIGITS = 8
const SCALE = 100_000_000n
const MAX_SINGLE_WALLET_USD_UNITS = 100_000n * SCALE

export type OwnerFundingPreview = {
  valid: boolean
  exceedsMax: boolean
  amount: bigint
  earningsAvailable: bigint
  marginFloor: bigint
  marginExcessAvailable: bigint
  fromEarnings: bigint
  fromMargin: bigint
  shortfall: bigint
  sufficient: boolean
}

function parseDecimalUnits(raw: string | undefined): bigint | null {
  if (!raw) return 0n
  const trimmed = raw.trim()
  if (!trimmed) return 0n
  const negative = trimmed.startsWith('-')
  const unsigned = negative ? trimmed.slice(1) : trimmed
  if (!/^\d+(?:\.\d+)?$/.test(unsigned)) return null
  const [wholePart, fracPart = ''] = unsigned.split('.')
  const paddedFrac = fracPart.slice(0, SCALE_DIGITS).padEnd(SCALE_DIGITS, '0')
  const units = BigInt(wholePart) * SCALE + BigInt(paddedFrac)
  return negative ? -units : units
}

function toUnits(raw: string | undefined): bigint {
  return parseDecimalUnits(raw) ?? 0n
}

function maxBigInt(a: bigint, b: bigint): bigint {
  return a > b ? a : b
}

function minBigInt(a: bigint, b: bigint): bigint {
  return a < b ? a : b
}

function totalFrozenUnits(wallet: SiteWallet | undefined): bigint {
  if (!wallet) return 0n
  if (wallet.frozen_balance !== undefined && wallet.frozen_balance !== '') {
    return maxBigInt(0n, toUnits(wallet.frozen_balance))
  }
  return (
    maxBigInt(0n, toUnits(wallet.frozen_usd)) +
    maxBigInt(0n, toUnits(wallet.excess_frozen_usd))
  )
}

export function formatFundingUsd(value: bigint | number | string): string {
  let units: bigint
  if (typeof value === 'bigint') {
    units = value
  } else if (typeof value === 'number') {
    if (!Number.isFinite(value) || value <= 0) return '0.00'
    units = toUnits(value.toFixed(SCALE_DIGITS))
  } else {
    units = toUnits(value)
  }
  if (units <= 0n) return '0.00'
  const whole = units / SCALE
  const frac = (units % SCALE).toString().padStart(SCALE_DIGITS, '0')
  const trimmedFrac = frac.replace(/0+$/, '')
  const displayFrac = trimmedFrac.length < 2 ? frac.slice(0, 2) : trimmedFrac
  return `${whole.toString()}.${displayFrac}`
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

  const earningsAvailUnits = maxBigInt(
    0n,
    toUnits(earnings?.balance) - totalFrozenUnits(earnings)
  )
  const marginFloorUnits = maxBigInt(0n, toUnits(margin?.required_balance))
  const marginExcessUnits = maxBigInt(
    0n,
    toUnits(margin?.balance) - totalFrozenUnits(margin) - marginFloorUnits
  )

  const trimmed = amountRaw.trim()
  if (!TWO_DECIMAL_PATTERN.test(trimmed)) {
    return {
      valid: false,
      exceedsMax: false,
      amount: 0n,
      earningsAvailable: earningsAvailUnits,
      marginFloor: marginFloorUnits,
      marginExcessAvailable: marginExcessUnits,
      fromEarnings: 0n,
      fromMargin: 0n,
      shortfall: 0n,
      sufficient: false,
    }
  }
  const amountUnits = parseDecimalUnits(trimmed)
  if (amountUnits === null || amountUnits <= 0n) {
    return {
      valid: false,
      exceedsMax: false,
      amount: 0n,
      earningsAvailable: earningsAvailUnits,
      marginFloor: marginFloorUnits,
      marginExcessAvailable: marginExcessUnits,
      fromEarnings: 0n,
      fromMargin: 0n,
      shortfall: 0n,
      sufficient: false,
    }
  }
  if (amountUnits > MAX_SINGLE_WALLET_USD_UNITS) {
    return {
      valid: false,
      exceedsMax: true,
      amount: amountUnits,
      earningsAvailable: earningsAvailUnits,
      marginFloor: marginFloorUnits,
      marginExcessAvailable: marginExcessUnits,
      fromEarnings: 0n,
      fromMargin: 0n,
      shortfall: 0n,
      sufficient: false,
    }
  }

  const fromEarningsUnits = minBigInt(earningsAvailUnits, amountUnits)
  const neededMarginUnits = amountUnits - fromEarningsUnits
  const fromMarginUnits = minBigInt(marginExcessUnits, neededMarginUnits)
  const shortfallUnits = maxBigInt(0n, neededMarginUnits - marginExcessUnits)

  return {
    valid: true,
    exceedsMax: false,
    amount: amountUnits,
    earningsAvailable: earningsAvailUnits,
    marginFloor: marginFloorUnits,
    marginExcessAvailable: marginExcessUnits,
    fromEarnings: fromEarningsUnits,
    fromMargin: fromMarginUnits,
    shortfall: shortfallUnits,
    sufficient: shortfallUnits === 0n,
  }
}
