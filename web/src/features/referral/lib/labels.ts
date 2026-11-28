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

import type { StatusVariant } from '@/components/status-badge'

import type {
  ActiveWithdrawalAccountType,
  ReferralCommissionStatus,
  ReferralProfile,
  ReferralWithdrawalStatus,
  WithdrawalAccountType,
} from '../types'
import { USDT_NETWORKS, type UsdtNetwork } from './address'

// Values are i18n keys; render them with t().

export const COMMISSION_STATUS_LABELS: Record<
  ReferralCommissionStatus,
  string
> = {
  pending: 'Settling',
  risk_held: 'Risk held',
  available: 'Withdrawable',
  frozen: 'Withdrawing',
  paid: 'Withdrawn',
  void: 'Voided',
  clawed_back: 'Clawed back',
}

export const COMMISSION_STATUS_VARIANTS: Record<
  ReferralCommissionStatus,
  StatusVariant
> = {
  pending: 'warning',
  risk_held: 'danger',
  available: 'success',
  frozen: 'info',
  paid: 'neutral',
  void: 'neutral',
  clawed_back: 'danger',
}

/** Literal t() calls so the i18n scanner sees every commission status label. */
export function commissionStatusLabel(t: TFunction, status: string): string {
  switch (status) {
    case 'pending':
      return t('Settling')
    case 'risk_held':
      return t('Risk held')
    case 'available':
      return t('Withdrawable')
    case 'frozen':
      return t('Withdrawing')
    case 'paid':
      return t('Withdrawn')
    case 'void':
      return t('Voided')
    case 'clawed_back':
      return t('Clawed back')
    default:
      return status || '-'
  }
}

/** Literal t() calls for commission source kinds. */
export function commissionSourceLabel(
  t: TFunction,
  sourceType?: string
): string {
  switch (sourceType) {
    case 'consume_hourly':
      return t('Hourly consumption')
    case 'topup':
      return t('Top-up')
    default:
      return sourceType || '-'
  }
}

export const WITHDRAWAL_STATUS_LABELS: Record<
  ReferralWithdrawalStatus,
  string
> = {
  pending: 'Under review',
  approved: 'Approved, awaiting payout',
  paid: 'Paid',
  rejected: 'Rejected',
  canceled: 'Canceled',
}

export const WITHDRAWAL_STATUS_VARIANTS: Record<
  ReferralWithdrawalStatus,
  StatusVariant
> = {
  pending: 'warning',
  approved: 'info',
  paid: 'success',
  rejected: 'danger',
  canceled: 'neutral',
}

export const WITHDRAWAL_TARGET_LABELS: Record<WithdrawalAccountType, string> = {
  balance: 'Account balance',
  usdt: 'USDT',
  alipay: 'Alipay',
  wechat: 'WeChat Pay',
}

/** A new idempotency key for one withdrawal attempt. */
export function newIdempotencyKey(): string {
  return crypto.randomUUID()
}

/** Enabled USDT networks in canonical order (defaults to all 4 when unspecified). */
export function availableUsdtNetworks(networks?: string[]): UsdtNetwork[] {
  if (!Array.isArray(networks)) return [...USDT_NETWORKS]
  return USDT_NETWORKS.filter((item) => networks.includes(item))
}

/** Which targets the admin has enabled and configured (adty convention I: no built-in minimum, fee or rate). */
export function withdrawTargetAvailable(
  profile: ReferralProfile,
  target: WithdrawalAccountType
): boolean {
  if (target === 'balance') return true
  if (target === 'wechat') return false
  if (
    Array.isArray(profile.withdraw_methods) &&
    !profile.withdraw_methods.includes(target)
  ) {
    return false
  }
  if (profile.min_withdraw_usd === null) return false
  if (target === 'usdt') {
    if (profile.usdt_withdraw_fee === null) return false
    return availableUsdtNetworks(profile.withdraw_usdt_networks).length > 0
  }
  return profile.cny_rate !== ''
}

/** Active targets shown to the promoter (balance is always first; external methods only if enabled). */
export function availableWithdrawalTargets(
  profile: ReferralProfile
): ActiveWithdrawalAccountType[] {
  const targets: ActiveWithdrawalAccountType[] = ['balance']
  if (withdrawTargetAvailable(profile, 'usdt')) targets.push('usdt')
  if (withdrawTargetAvailable(profile, 'alipay')) targets.push('alipay')
  return targets
}
