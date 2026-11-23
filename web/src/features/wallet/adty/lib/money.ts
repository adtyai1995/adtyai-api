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
// formatNumber has no fixed-fraction option; USD amounts always show 2 decimals.
export function isGiftBonusWithheld(status?: string): boolean {
  return (
    status === 'pending' || status === 'pending_review' || status === 'canceled'
  )
}

export function giftFulfillmentStatusLabelKey(status?: string): string | null {
  switch (status) {
    case 'pending':
      return 'Bonus pending fulfillment'
    case 'pending_review':
      return 'Bonus pending review'
    case 'canceled':
      return 'Bonus not granted'
    case 'fulfilled':
      return 'Bonus fulfilled'
    default:
      return null
  }
}

/** Face plus bonus actually credited (withheld bonuses are excluded until fulfilled). */
export function creditedUsdAmount(ext: {
  face_usd: string
  bonus_usd: string
  gift_fulfillment_status?: string
}): string {
  const face = Number(ext.face_usd)
  const bonus = isGiftBonusWithheld(ext.gift_fulfillment_status)
    ? 0
    : Number(ext.bonus_usd)
  return (face + bonus).toFixed(2)
}

export function formatUsd(
  value: number,
  locale: string | undefined,
  maximumFractionDigits = 2
): string {
  return `$${new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits }).format(value)}`
}

export function formatPaymentAmount(
  amount: string,
  currency: string,
  locale: string | undefined
): string {
  if (currency === 'USDT') return `${amount} USDT`
  try {
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
    }).format(Number(amount))
  } catch {
    return `${amount} ${currency}`
  }
}
