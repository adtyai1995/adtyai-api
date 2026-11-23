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
import type { StatusVariant } from '@/components/status-badge'

/** i18n keys for team, member and key states. */
export const TEAM_STATUS_LABELS: Record<string, string> = {
  active: 'Active',
  closed: 'Closed',
}

export const MEMBER_STATUS_LABELS: Record<string, string> = {
  active: 'Active',
  disabled: 'Disabled',
  left: 'Left the team',
  deleted: 'Deleted',
}

export const KEY_STATUS_LABELS: Record<string, string> = {
  active: 'Active',
  disabled: 'Disabled',
  expired: 'Expired',
  exhausted: 'Exhausted',
  deleted: 'Deleted',
  left: 'Revoked',
}

export const LEDGER_TYPE_LABELS: Record<string, string> = {
  topup: 'Team top-up',
  credit: 'Credit',
  spend: 'API usage',
  reserve: 'Pre-deduction',
  settle: 'Settlement adjustment',
  refund: 'Refund to team',
  manual_refund: 'Manual refund reclaim',
  refund_release: 'Refund termination restore',
}

export function teamStatusVariant(status: string): StatusVariant {
  return status === 'active' ? 'success' : 'neutral'
}

export function labelOf(
  t: (key: string) => string,
  map: Record<string, string>,
  value: string
): string {
  const key = map[value]
  return key ? t(key) : value
}
