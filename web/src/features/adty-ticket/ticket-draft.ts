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
