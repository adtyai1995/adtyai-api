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
  duplicate_payment: 'Duplicate payment',
  operating: 'Operating wallet',
  deposit: 'Deposit wallet',
  earnings: 'Earnings wallet',
}
