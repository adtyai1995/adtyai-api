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
import type { TFunction } from 'i18next'

import type { SiteWallet } from '@/features/adty-ledger/site-wallet-labels'

import type { EarningKind, SenderDnsRecord, SitePrice } from '../types'

/** Literal t() calls so the key scanner sees every label. */
export function earningKindLabel(t: TFunction, kind: EarningKind): string {
  switch (kind) {
    case 'usage':
      return t('Usage')
    case 'refund':
      return t('Refund')
    case 'bonus':
      return t('Bonus you paid')
    case 'bonus_reversal':
      return t('Bonus returned')
    case 'trial':
      return t('Trial credit')
    case 'skipped':
      return t('Not counted')
  }
}

const USD_SCALE = 8
const USD_SCALE_FACTOR = 10n ** BigInt(USD_SCALE)

function parseScaledUsd(raw: string | undefined): bigint {
  const s = (raw ?? '').trim()
  if (!s) return 0n
  const neg = s.startsWith('-')
  const clean = neg || s.startsWith('+') ? s.slice(1) : s
  const [intPart = '0', fracPart = ''] = clean.split('.')
  if (!/^\d+$/.test(intPart || '0') || !/^\d*$/.test(fracPart)) return 0n
  const paddedFrac = (fracPart + '0'.repeat(USD_SCALE)).slice(0, USD_SCALE)
  const val =
    BigInt(intPart || '0') * USD_SCALE_FACTOR + BigInt(paddedFrac || '0')
  return neg ? -val : val
}

function formatScaledUsd(val: bigint): string {
  if (val === 0n) return '0'
  const neg = val < 0n
  const abs = neg ? -val : val
  const intPart = abs / USD_SCALE_FACTOR
  const fracPart = (abs % USD_SCALE_FACTOR)
    .toString()
    .padStart(USD_SCALE, '0')
    .replace(/0+$/, '')
  const formatted = fracPart ? `${intPart}.${fracPart}` : `${intPart}`
  return neg ? `-${formatted}` : formatted
}

export type MarginCycleSummary = {
  tierUsd: string
  usedUsd: string
  limit90Usd: string
  availableUsd: string
}

export function ownerTransferPreview(
  wallet: SiteWallet | undefined,
  amount: string
) {
  if (!wallet || !/^\d{1,20}(\.\d{1,8})?$/.test(amount)) return null
  const available =
    parseScaledUsd(wallet.balance) - parseScaledUsd(wallet.frozen_usd)
  const debit = parseScaledUsd(amount)
  if (debit <= 0n || debit > available || debit % 200n !== 0n) return null
  return formatScaledUsd(available - debit)
}

/**
 * Computes the margin cycle summary (tier, used, 90% limit, and currently
 * available draw) using scaled BigInt arithmetic.
 */
export function marginCycleSummary(
  wallet: Pick<
    SiteWallet,
    | 'balance'
    | 'frozen_usd'
    | 'excess_frozen_usd'
    | 'cycle_tier_usd'
    | 'cycle_used_usd'
  >
): MarginCycleSummary {
  const tier = parseScaledUsd(wallet.cycle_tier_usd)
  const used = parseScaledUsd(wallet.cycle_used_usd)
  const frozen = parseScaledUsd(wallet.frozen_usd)
  const excessFrozen = parseScaledUsd(wallet.excess_frozen_usd)
  const balance = parseScaledUsd(wallet.balance)

  if (tier <= 0n) {
    return {
      tierUsd: formatScaledUsd(tier > 0n ? tier : 0n),
      usedUsd: formatScaledUsd(used > 0n ? used : 0n),
      limit90Usd: '0',
      availableUsd: '0',
    }
  }

  const limit90 = (tier * 9n) / 10n
  const floor10 = tier - limit90
  const capRoom = limit90 - used - frozen
  const cashRoom = balance - frozen - excessFrozen - floor10
  const minRoom = capRoom < cashRoom ? capRoom : cashRoom
  const available = minRoom > 0n ? minRoom : 0n

  return {
    tierUsd: formatScaledUsd(tier),
    usedUsd: formatScaledUsd(used),
    limit90Usd: formatScaledUsd(limit90),
    availableUsd: formatScaledUsd(available),
  }
}

/** True for a decimal string below zero ("-0.00" counts as zero). */
export function isNegativeAmount(value: string | undefined): boolean {
  return Number(value ?? 0) < 0
}

/** A raise that is saved but not in effect yet. */
export type PendingIncrease = {
  group: string
  model?: string
  current: string
  next: string
  at: number
}

/** Pending increases among the site-wide prices, default first then by group. */
export function pendingIncreases(
  prices: SitePrice[] | null | undefined
): PendingIncrease[] {
  return (prices ?? [])
    .filter((price) => price.user_id === 0 && price.pending_at > 0)
    .map((price) => ({
      group: price.group,
      model: price.model || undefined,
      current: price.markup,
      next: price.pending_markup,
      at: price.pending_at,
    }))
    .sort((a, b) => {
      const modelA = a.model ?? ''
      const modelB = b.model ?? ''
      if (modelA !== modelB) {
        if (modelA === '') return -1
        if (modelB === '') return 1
        return modelA.localeCompare(modelB)
      }
      if (a.group === b.group) return 0
      if (a.group === '') return -1
      if (b.group === '') return 1
      return a.group.localeCompare(b.group)
    })
}

/** The site-wide default markup and per-group markups as editable strings. */
export function siteMarkups(prices: SitePrice[] | null | undefined): {
  defaultMarkup: string
  groups: Record<string, string>
  models: Record<string, string>
} {
  let defaultMarkup = ''
  const groups: Record<string, string> = Object.create(null)
  const models: Record<string, string> = Object.create(null)
  for (const price of prices ?? []) {
    if (price.user_id !== 0) continue
    const model = (price.model ?? '').trim()
    if (model) {
      if (price.group === '') models[model] = price.markup
      continue
    }
    if (price.group === '') defaultMarkup = price.markup
    else groups[price.group] = price.markup
  }
  return { defaultMarkup, groups, models }
}

/** Only send changed overrides. Missing saved keys explicitly resume inheritance. */
export function retailOverrideChanges(
  saved: Record<string, string>,
  rows: [string, string][]
): Record<string, string> {
  const current = new Map(
    rows.map(([key, value]) => [key.trim(), value] as const)
  )
  const changes = new Map<string, string>()
  for (const key of Object.keys(saved)) {
    if (!current.has(key)) changes.set(key, '0')
  }
  for (const [key, value] of current) {
    if (key && saved[key] !== value) changes.set(key, value)
  }
  return Object.fromEntries(changes)
}

export function validRetailMarkup(value: string): boolean {
  return (
    /^(?:\d{1,6}(?:\.\d{0,4})?|\.\d{1,4})$/.test(value) && Number(value) > 0
  )
}

/** A fresh idempotency key for one trial grant. */
export function newIdempotencyKey(): string {
  return crypto.randomUUID()
}

let rowSeq = 0

/** A key for an editable list row; stays the same while the row is edited. */
export function newRowKey(): string {
  rowSeq += 1
  return `row-${rowSeq}`
}

/** Parses the DNS records stored with a sender domain; bad or empty JSON gives no rows. */
export function parseSenderRecords(raw: string | undefined): SenderDnsRecord[] {
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed) ? (parsed as SenderDnsRecord[]) : []
  } catch {
    return []
  }
}
