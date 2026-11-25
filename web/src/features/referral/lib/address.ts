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
// Same patterns as service/referral_withdraw.go: TRON base58 for TRC20,
// 0x + 40 hex characters for BEP20, Polygon and X Layer.
export const USDT_NETWORKS = ['TRC20', 'BEP20', 'POLYGON', 'XLAYER'] as const
export type UsdtNetwork = (typeof USDT_NETWORKS)[number]

const PATTERNS: Record<UsdtNetwork, RegExp> = {
  TRC20: /^T[1-9A-HJ-NP-Za-km-z]{33}$/,
  BEP20: /^0x[0-9a-fA-F]{40}$/,
  POLYGON: /^0x[0-9a-fA-F]{40}$/,
  XLAYER: /^0x[0-9a-fA-F]{40}$/,
}

export const ADDRESS_HINTS: Record<UsdtNetwork, string> = {
  TRC20: 'Starts with T, 34 characters',
  BEP20: 'Starts with 0x, 42 characters',
  POLYGON: 'Starts with 0x, 42 characters',
  XLAYER: 'Starts with 0x, 42 characters',
}

export function usdtNetworkLabel(network: string): string {
  if (network === 'XLAYER') return 'X Layer'
  if (network === 'POLYGON') return 'Polygon'
  return network
}

export function isValidUsdtAddress(
  network: UsdtNetwork,
  address: string
): boolean {
  return PATTERNS[network].test(address.trim())
}

/** USDT received after the fixed network fee, never below zero, aligned to 6-decimal USDT precision. */
export function usdtNetAmount(amount: number, fee: number): number {
  return Math.max(0, Math.round((amount - fee) * 1_000_000) / 1_000_000)
}

/**
 * Formats a USDT amount keeping at least 2 and up to 6 fractional digits so
 * 6-decimal USDT values are never truncated by `.toFixed(2)`.
 */
export function formatUsdtAmount(value: number | string): string {
  const num =
    typeof value === 'number' ? value : Number(String(value).trim())
  if (!Number.isFinite(num)) return String(value)
  const fixed = num.toFixed(6)
  const [whole, frac = ''] = fixed.split('.')
  const trimmedFrac = frac.replace(/0+$/, '')
  const displayFrac = trimmedFrac.length < 2 ? frac.slice(0, 2) : trimmedFrac
  return `${whole}.${displayFrac}`
}
