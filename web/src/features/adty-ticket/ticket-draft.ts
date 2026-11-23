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
export const ticketRelations: Record<string, string> = {
  topup: 'Top-up order',
  refund: 'Refund',
  voucher: 'Redemption record ID',
  ledger: 'Transaction ID',
  request: 'Request ID',
  team_order: 'Team order',
  team_refund: 'Team refund',
  team_balance: 'Team balance (team ID)',
  team_key: 'Team key ID',
}

export function ticketDraftLink(hash: string) {
  if (hash.startsWith('#ticket-')) return { kind: 'topup', ref: hash.slice(8) }
  const match = /^#ticket:([a-z_]+):(.+)$/.exec(hash)
  if (!match || !Object.hasOwn(ticketRelations, match[1])) return null
  try {
    return { kind: match[1], ref: decodeURIComponent(match[2]) }
  } catch {
    return null
  }
}

export function ticketOperationKey(
  attempts: Map<string, { key: string; value: string }>,
  action: string,
  value: string
) {
  const previous = attempts.get(action)
  if (previous?.value === value) return previous.key
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  const key = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  attempts.set(action, { key, value })
  return key
}
