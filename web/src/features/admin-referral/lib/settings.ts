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
import { USDT_NETWORKS, type UsdtNetwork } from '@/features/referral/lib/address'

import type { ReferralSettings } from '../api'

export const CASH_WITHDRAW_METHODS = ['usdt', 'alipay'] as const
export type CashWithdrawMethod = (typeof CASH_WITHDRAW_METHODS)[number]

export function canonicalWithdrawMethods(
  list?: string[]
): CashWithdrawMethod[] {
  if (!Array.isArray(list)) return []
  const set = new Set(list.map((v) => v.trim().toLowerCase()))
  return CASH_WITHDRAW_METHODS.filter((m) => set.has(m))
}

export function canonicalWithdrawNetworks(list?: string[]): UsdtNetwork[] {
  if (!Array.isArray(list)) return []
  const set = new Set(list.map((v) => v.trim().toUpperCase()))
  return USDT_NETWORKS.filter((n) => set.has(n))
}

export type MoneyKey =
  | 'default_rate'
  | 'min_withdraw_usd'
  | 'usdt_withdraw_fee'
export type RuleKey =
  | 'cookie_ttl_days'
  | 'freeze_days'
  | 'cancel_window_minutes'
  | 'self_invite_window_days'
  | 'batch_register_threshold'
  | 'batch_register_window_hours'

export const MONEY_KEYS: MoneyKey[] = [
  'default_rate',
  'min_withdraw_usd',
  'usdt_withdraw_fee',
]
export const RULE_KEYS: RuleKey[] = [
  'cookie_ttl_days',
  'freeze_days',
  'cancel_window_minutes',
  'self_invite_window_days',
  'batch_register_threshold',
  'batch_register_window_hours',
]

export type Drafts = Record<MoneyKey | RuleKey, string> & { cny_rate: string }

export function toDrafts(settings: ReferralSettings): Drafts {
  const drafts = { cny_rate: settings.withdraw_cny_rate } as Drafts
  for (const key of MONEY_KEYS) {
    const value = settings[key]
    drafts[key] = value === null || value === undefined ? '' : String(value)
  }
  for (const key of RULE_KEYS) drafts[key] = String(settings[key])
  return drafts
}
