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

import type { OrderTraceSection, UnifiedOrderType } from './api'

export const UNIFIED_ORDER_LABELS: Record<UnifiedOrderType, string> = {
  topup: 'Top-up',
  consumption: 'API consumption',
  voucher: 'Voucher top-up',
  refund: 'Refund',
  withdrawal: 'Withdrawal',
  commission: 'Invite commission',
  staff_adjustment: 'Staff balance adjustment',
  other: 'Other financial activity',
}

export const ORDER_TRACE_SECTIONS: Record<OrderTraceSection, string> = {
  business: 'Business record',
  funds: 'Principal and gift batches',
  refunds: 'Manual refund records',
  payments: 'Payment events and queries',
  wallet: 'Wallet credits, adjustments and refunds',
  commissions: 'Commission records',
  commission_movements: 'Commission wallet movements',
  site_wallet: 'Site wallet movements',
  exceptions: 'Payment reconciliation events',
  reviews: 'Financial review history',
}

export const ORDER_TRACE_LABELS: Record<string, string> = {
  callback: 'Payment callback',
  query: 'Payment query',
  manual: 'Manual completion',
  credited: 'Credited',
  replayed: 'Payment replay',
  unpaid: 'Not paid',
  failed: 'Failed',
  rejected: 'Rejected',
  received: 'Received',
  paid: 'Paid',
  void: 'Voided',
  pending: 'Pending',
  success: 'Success',
  refunded: 'Refunded',
  expired: 'Expired',
  canceled: 'Canceled',
  replaced: 'Replaced',
  topup: 'Top-up',
  refund: 'Refund',
  manual_adjust: 'Balance adjustment',
  pending_review: 'Pending review',
  never_credited: 'Never credited',
  dismissed: 'Dismissed',
  available: 'Available',
  frozen: 'Frozen amount',
  settled: 'Settled',
  submitted: 'Submitted',
  approved: 'Approved',
  completed: 'Completed',
  terminated: 'Terminated',
  paying: 'Paying',
  covered: 'Covered',
  pending_coverage: 'Pending coverage',
  principal: 'Principal',
  bonus: 'Permanent gift',
  expiring_bonus: 'Expiring gift',
  active: 'Active',
  exhausted: 'Exhausted',
  voided: 'Voided',
  clawed_back: 'Clawed back',
  commission_accrue: 'Commission accrued',
  commission_settle: 'Commission settled',
  commission_void: 'Commission release',
  commission_clawback: 'Commission clawback',
  commission_clawback_partial: 'Partial commission clawback',
  payment_query: 'Payment query',
  payment_callback: 'Payment callback',
  team_payment_query: 'Team payment query',
  duplicate_payment: 'Duplicate payment',
  reversal: 'External reversal',
  'charge.refunded': 'External refund',
  'charge.dispute.created': 'Chargeback dispute',
  unknown: 'Unknown',
  operating: 'Operating wallet',
  deposit: 'Deposit wallet',
  earnings: 'Earnings wallet',
}

export function paymentProviderLabel(
  t: TFunction,
  provider: string | undefined
): string {
  const raw = (provider ?? '').trim()
  if (!raw) return '-'
  switch (raw.toLowerCase()) {
    case 'epay':
    case 'adty_epay':
      return t('Epay channels')
    case 'bepusdt':
      return t('USDT (BEpusdt)')
    case 'stripe':
      return t('Stripe')
    case 'creem':
      return t('Creem Payment')
    case 'adty_voucher':
    case 'voucher':
      return t('Voucher')
    case 'alipay':
      return t('Alipay')
    case 'wxpay':
    case 'wechat':
      return t('WeChat Pay')
    case 'usdt':
      return t('USDT')
    case 'usdt.trc20':
      return 'USDT · TRC20'
    case 'usdt.erc20':
      return 'USDT · ERC20'
    case 'usdt.bep20':
      return 'USDT · BEP20'
    case 'usdt.polygon':
      return 'USDT · Polygon'
    case 'usdt.xlayer':
      return 'USDT · X Layer'
    case 'wallet':
      return t('Wallet')
    case 'balance':
      return t('Account balance')
    case 'manual_refund':
      return t('Manual refund')
    case 'withdrawal':
      return t('Withdrawal')
    case 'commission':
      return t('Commission')
    default:
      return raw
  }
}

export function orphanStatusLabel(
  t: TFunction,
  status: string | undefined
): string {
  const raw = (status ?? '').trim()
  if (!raw) return '-'
  switch (raw.toLowerCase()) {
    case 'pending_review':
      return t('Pending review')
    case 'credited':
      return t('Credited')
    case 'refunded':
      return t('Refunded')
    case 'dismissed':
      return t('Dismissed')
    case 'never_credited':
      return t('Never credited')
    default:
      return t(ORDER_TRACE_LABELS[raw.toLowerCase()] ?? raw)
  }
}

export function orphanStatusVariant(
  status: string | undefined
): 'warning' | 'success' | 'info' | 'neutral' {
  switch ((status ?? '').trim().toLowerCase()) {
    case 'pending_review':
      return 'warning'
    case 'credited':
      return 'success'
    case 'refunded':
      return 'info'
    default:
      return 'neutral'
  }
}

export function orphanReasonLabel(
  t: TFunction,
  reason: string | undefined
): string {
  const raw = (reason ?? '').trim()
  if (!raw) return '-'
  switch (raw.toLowerCase()) {
    case 'payment requires manual review after payment succeeded':
      return t('Payment requires manual review after payment succeeded')
    case 'a completed order received a different payment voucher':
      return t('Completed order received a different payment voucher')
    case 'team payment could not be verified':
      return t('Team payment could not be verified')
    case 'payment recovery requires reconciliation':
      return t('Payment recovery requires reconciliation')
    case 'topup status invalid':
      return t('Top-up status is invalid')
    case 'payment recovery query detected conflicting gateway facts':
      return t('Payment recovery query detected conflicting gateway facts')
    case 'order_not_found':
      return t('Order not found')
    case 'amount_mismatch':
    case 'paid_mismatch':
      return t('Paid amount differs')
    case 'duplicate_payment':
      return t('Duplicate payment')
    case 'reversal':
      return t('External reversal')
    case 'charge.refunded':
      return t('External refund')
    case 'charge.dispute.created':
      return t('Chargeback dispute')
    case 'payment_callback':
      return t('Payment callback')
    case 'payment_query':
      return t('Payment query')
    case 'team_payment_query':
      return t('Team payment query')
    default:
      return raw
  }
}

export function manualRefundStatusLabel(
  t: TFunction,
  status: string | undefined
): string {
  const raw = (status ?? '').trim()
  if (!raw) return '-'
  switch (raw.toLowerCase()) {
    case 'submitted':
      return t('Submitted')
    case 'approved':
      return t('Approved')
    case 'completed':
      return t('Completed')
    case 'terminated':
      return t('Terminated')
    default:
      return t(ORDER_TRACE_LABELS[raw.toLowerCase()] ?? raw)
  }
}

export function manualRefundPayoutStatusLabel(
  t: TFunction,
  status: string | undefined
): string {
  const raw = (status ?? '').trim().toLowerCase()
  switch (raw) {
    case '':
    case 'pending':
      return t('Pending')
    case 'success':
      return t('Success')
    case 'failed':
      return t('Failed')
    case 'unknown':
      return t('Unknown')
    default:
      return t(ORDER_TRACE_LABELS[raw] ?? raw)
  }
}

export function orderTraceSourceLabel(
  t: TFunction,
  value: string | undefined
): string {
  const raw = (value ?? '').trim()
  if (!raw) return '-'
  const normalized = raw.toLowerCase()
  if (normalized in ORDER_TRACE_LABELS) {
    return t(ORDER_TRACE_LABELS[normalized])
  }
  return orphanReasonLabel(t, raw)
}
