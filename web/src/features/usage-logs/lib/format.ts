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
import type { StatusBadgeProps } from '@/components/status-badge'
import {
  BILLING_PRICING_VARS,
  normalizeTierLabel,
  parseTiersFromExpr,
  splitBillingExprAndRequestRules,
  type ParsedTier,
} from '@/features/pricing/lib/billing-expr'
import { translateServerText } from '@/lib/server-error-message'

import type { UsageLog } from '../data/schema'
import type { LogOtherData } from '../types'
import { buildQuotaAuditOperation } from './quota-audit-operation'

export { normalizeTierLabel }

const PARAM_OVERRIDE_ACTION_MAP: Record<string, string> = {
  set: 'Set',
  delete: 'Delete',
  copy: 'Copy',
  move: 'Move',
  append: 'Append',
  prepend: 'Prepend',
  trim_prefix: 'Trim Prefix',
  trim_suffix: 'Trim Suffix',
  ensure_prefix: 'Ensure Prefix',
  ensure_suffix: 'Ensure Suffix',
  trim_space: 'Trim Space',
  to_lower: 'To Lower',
  to_upper: 'To Upper',
  replace: 'Replace',
  regex_replace: 'Regex Replace',
  set_header: 'Set Header',
  delete_header: 'Delete Header',
  copy_header: 'Copy Header',
  move_header: 'Move Header',
  pass_headers: 'Pass Headers',
  sync_fields: 'Sync Fields',
  return_error: 'Return Error',
}

/**
 * Get localized label for a param override action
 */
export function getParamOverrideActionLabel(
  action: string,
  t: (key: string) => string
): string {
  const key = PARAM_OVERRIDE_ACTION_MAP[action.toLowerCase()]
  return key ? t(key) : action
}

/**
 * Parse a param override audit line into action and content
 */
export function parseAuditLine(
  line: string
): { action: string; content: string } | null {
  if (typeof line !== 'string') return null
  const firstSpace = line.indexOf(' ')
  if (firstSpace <= 0) return { action: line, content: line }
  return {
    action: line.slice(0, firstSpace),
    content: line.slice(firstSpace + 1),
  }
}

/**
 * Check if the log is a violation fee log
 */
export function isViolationFeeLog(other: LogOtherData | null): boolean {
  if (!other) return false
  return (
    other.violation_fee === true ||
    Boolean(other.violation_fee_code) ||
    Boolean(other.violation_fee_marker)
  )
}

function isPositiveFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
}

function hasLegacySearchSurcharge(
  enabled: boolean | undefined,
  count: number | undefined,
  price: number | undefined
): boolean {
  return (
    enabled === true &&
    isPositiveFiniteNumber(count) &&
    isPositiveFiniteNumber(price)
  )
}

/**
 * Check whether a consume log includes an actual tool-call surcharge.
 * Structured surcharge items cover current logs, while the legacy fields keep
 * historical Web Search, File Search, and Image Generation logs visible.
 */
export function hasToolSurcharge(other: LogOtherData | null): boolean {
  if (!other) return false

  const hasStructuredSurcharge =
    Array.isArray(other.tool_surcharges) &&
    other.tool_surcharges.some(
      (item) =>
        typeof item?.name === 'string' &&
        item.name.trim() !== '' &&
        isPositiveFiniteNumber(item.count) &&
        isPositiveFiniteNumber(item.price)
    )
  if (hasStructuredSurcharge) return true

  if (
    hasLegacySearchSurcharge(
      other.web_search,
      other.web_search_call_count,
      other.web_search_price
    )
  ) {
    return true
  }

  if (
    hasLegacySearchSurcharge(
      other.file_search,
      other.file_search_call_count,
      other.file_search_price
    )
  ) {
    return true
  }

  return (
    other.image_generation_call === true &&
    isPositiveFiniteNumber(other.image_generation_call_price)
  )
}

/**
 * Parse the 'other' field from JSON string to object
 */
export function parseLogOther(other: string): LogOtherData | null {
  if (!other) return null
  try {
    return JSON.parse(other) as LogOtherData
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to parse log other field:', error)
    return null
  }
}

export function getReasoningEffortVariant(
  effort: string | undefined
): StatusBadgeProps['variant'] {
  switch (effort?.trim().toLowerCase()) {
    case 'max':
    case 'xhigh':
    case 'high':
      return 'orange'
    case 'medium':
      return 'yellow'
    case 'low':
    case 'minimal':
      return 'green'
    case 'none':
    default:
      return 'grey'
  }
}

/**
 * Get time color based on duration (in seconds)
 */
export function getTimeColor(
  seconds: number
): 'success' | 'warning' | 'danger' {
  if (seconds < 10) return 'success'
  if (seconds < 30) return 'warning'
  return 'danger'
}

/**
 * Get first-response-token color based on latency (in seconds)
 */
export function getFirstResponseTimeColor(
  seconds: number
): 'success' | 'warning' | 'danger' {
  if (seconds < 5) return 'success'
  if (seconds < 10) return 'warning'
  return 'danger'
}

/**
 * Get throughput color based on generated tokens per second
 */
export function getThroughputColor(
  tokensPerSecond: number
): 'success' | 'warning' | 'danger' {
  if (tokensPerSecond >= 30) return 'success'
  if (tokensPerSecond >= 15) return 'warning'
  return 'danger'
}

/**
 * Get response color using throughput only when enough output tokens exist.
 */
export function getResponseTimeColor(
  seconds: number,
  completionTokens: number
): 'success' | 'warning' | 'danger' {
  if (completionTokens < 100 || seconds <= 0) return getTimeColor(seconds)
  return getThroughputColor(completionTokens / seconds)
}

/**
 * Format model name with mapping indicator
 */
export function formatModelName(log: UsageLog): {
  name: string
  isMapped: boolean
  actualModel?: string
  responseModel?: LogOtherData['response_model']
} {
  const other = parseLogOther(log.other)
  const isMapped = !!(
    other?.is_model_mapped &&
    other?.upstream_model_name &&
    other.upstream_model_name !== ''
  )

  return {
    name: log.model_name,
    isMapped,
    actualModel: isMapped ? other.upstream_model_name : undefined,
    responseModel: other?.response_model,
  }
}

/**
 * Decode a base64-encoded billing expression. Safely returns an empty string
 * when the input is missing or malformed (e.g. legacy logs without expr_b64).
 */
export function decodeBillingExprB64(exprB64: string | undefined): string {
  if (!exprB64) return ''
  try {
    const binaryString =
      typeof window !== 'undefined'
        ? window.atob(exprB64)
        : Buffer.from(exprB64, 'base64').toString('binary')
    const bytes = new Uint8Array(binaryString.length)

    for (let i = 0; i < binaryString.length; i++) {
      bytes[i] = binaryString.charCodeAt(i)
    }

    if (typeof TextDecoder !== 'undefined') {
      return new TextDecoder().decode(bytes)
    }

    return decodeURIComponent(
      Array.prototype.map
        .call(bytes, (byte: number) => `%${byte.toString(16).padStart(2, '0')}`)
        .join('')
    )
  } catch {
    return ''
  }
}

/**
 * Resolve which parsed tier corresponds to the matched_tier label in a log
 * entry. Missing or unknown labels do not fall back to another tier because
 * that would display guessed unit prices.
 */
export function resolveMatchedTier(
  tiers: ParsedTier[],
  matchedLabel: string | undefined
): ParsedTier | null {
  if (tiers.length === 0) return null
  if (!matchedLabel) return null
  const found = tiers.find((tier) => {
    const l1 = normalizeTierLabel(tier.label)
    const l2 = normalizeTierLabel(matchedLabel)
    return l1 === l2 && l1 !== ''
  })
  return found || null
}

/**
 * Tiered pricing summary derived from an `other` log payload using the
 * billing-expression library. Returns null when the entry is not a tiered
 * billing log or the expression failed to parse.
 */
export interface TieredBillingSummary {
  tiers: ParsedTier[]
  tier: ParsedTier
  priceEntries: Array<{
    field: string
    shortLabel: string
    price: number
    unit?: 'request' | 'image'
  }>
}

/**
 * Whether the request payload reports any cache-related token usage. Used to
 * suppress cache pricing rows from the tiered breakdown when the request did
 * not exercise the cache path.
 */
export function hasAnyCacheTokens(
  other: LogOtherData | null | undefined
): boolean {
  if (!other) return false
  return (
    (other.cache_tokens || 0) > 0 ||
    (other.image_cache_tokens || 0) > 0 ||
    (other.cache_creation_tokens || 0) > 0 ||
    (other.cache_creation_tokens_5m || 0) > 0 ||
    (other.cache_creation_tokens_1h || 0) > 0 ||
    (other.cache_creation_tokens_12h || 0) > 0
  )
}

export function getTieredBillingSummary(
  other: LogOtherData | null
): TieredBillingSummary | null {
  if (!other || other.billing_mode !== 'tiered_expr') return null
  const exprStr = decodeBillingExprB64(other.expr_b64)
  if (!exprStr) return null
  const tiers = parseTiersFromExpr(
    splitBillingExprAndRequestRules(exprStr).billingExpr
  )
  const tier = resolveMatchedTier(tiers, other.matched_tier)
  if (
    other.billing_unit === 'request' &&
    typeof other.fixed_price === 'number' &&
    Number.isFinite(other.fixed_price) &&
    other.fixed_price >= 0
  ) {
    const fixedPrice = other.fixed_price
    const actualTier = tiers.find(
      (entry) =>
        normalizeTierLabel(entry.label) ===
          normalizeTierLabel(other.matched_tier) &&
        entry.billingUnit === 'request' &&
        entry.fixedPrice === fixedPrice
    ) ?? {
      label: other.matched_tier || '',
      conditions: [],
      billingUnit: 'request' as const,
      fixedPrice,
    }
    return {
      tiers,
      tier: actualTier,
      priceEntries: [
        {
          field: 'fixedPrice',
          shortLabel:
            other.image_count !== undefined ? 'Per image' : 'Per-call',
          price: fixedPrice,
          unit: other.image_count !== undefined ? 'image' : 'request',
        },
      ],
    }
  }
  if (!tier) return null
  if (tier.billingUnit === 'request' && typeof tier.fixedPrice === 'number') {
    return {
      tiers,
      tier,
      priceEntries: [
        {
          field: 'fixedPrice',
          shortLabel: tier.imageCount ? 'Per image' : 'Per-call',
          price: tier.fixedPrice,
          unit: tier.imageCount ? 'image' : 'request',
        },
      ],
    }
  }

  const cacheTokensPresent = hasAnyCacheTokens(other)

  const priceEntries: TieredBillingSummary['priceEntries'] = []
  for (const v of BILLING_PRICING_VARS) {
    if (!v.field) continue
    if (v.group === 'cache' && !cacheTokensPresent) continue
    const raw = tier[v.field as keyof ParsedTier]
    const price = Number(raw)
    if (Number.isFinite(price) && price >= 0) {
      priceEntries.push({
        field: v.field,
        shortLabel: v.shortLabel,
        price,
      })
    }
  }
  return { tiers, tier, priceEntries }
}

/**
 * Calculate duration and return formatted result with color variant
 * @param submitTime - Submit timestamp
 * @param finishTime - Finish timestamp
 * @param unit - Unit of the timestamps ('seconds' or 'milliseconds')
 */
export function formatDuration(
  submitTime?: number,
  finishTime?: number,
  unit: 'seconds' | 'milliseconds' = 'milliseconds'
): { durationSec: number; variant: StatusBadgeProps['variant'] } | null {
  if (!submitTime || !finishTime) return null

  const durationSec =
    unit === 'milliseconds'
      ? (finishTime - submitTime) / 1000
      : finishTime - submitTime

  return { durationSec, variant: durationSec > 60 ? 'red' : 'green' }
}

/**
 * Maps a language-independent audit/login operation `action` to an i18n
 * template string (the template itself is the i18n key, with {{placeholders}}).
 *
 * The backend stores only `action` + structured `params` in `other.op`; the UI
 * renders localized content at display time so audit/login logs are fully
 * translatable instead of being frozen to whatever language was written to DB.
 */
const AUDIT_TEMPLATES: Record<string, string> = {
  'token.create': 'API token creation',
  'token.update': 'API token configuration update',
  'token.status_update': 'API token status update',
  'token.delete': 'API token deletion',
  'token.delete_batch': 'API token batch deletion',
  'token.key_view': 'API token key access',
  'token.key_view_batch': 'API token batch key access',
  'access_token.generate': 'Created an access token',
  'access_token.revoke': 'Revoked an access token',
  'access_token.rename': 'Renamed an access token',
  'access_token.update': 'Changed access token permissions',
  'user.2fa_setup': 'Started two-factor authentication setup',
  'user.2fa_enable': 'Enabled two-factor authentication',
  'user.2fa_disable_self': 'Disabled two-factor authentication',
  'user.2fa_backup_codes': 'Regenerated two-factor backup codes',
  'user.security_verify': 'Completed security verification',
  'user.security_verify_failed': 'Security verification failed',
  'user.password_change': 'Account password change',
  'user.binding_start': 'Account binding request',
  'user.binding_bind': 'Account binding',
  'user.binding_unbind': 'Account unlinking',
  'user.email_binding_resend': 'Email confirmation code resend',

  login: 'Logged in successfully via {{method}}',
  // User management
  'user.create': 'Created user {{username}} (role {{role}})',
  'user.update': 'Updated user {{username}} (ID: {{id}})',
  'user.delete': 'Deleted user {{username}} (ID: {{id}})',
  'admin.user.password_reset': 'Reset password for user {{username}} (ID: {{id}})',
  'user.account_delete': 'Account deletion',
  'user.manage': 'Performed {{action}} on user {{username}} (ID: {{id}})',
  'user.quota_add': 'Increased user quota by {{quota}}',
  'user.quota_subtract': 'Decreased user quota by {{quota}}',
  'user.quota_override': 'Overrode user quota from {{from}} to {{to}}',
  'user.binding_clear': 'Cleared {{bindingType}} binding for user {{username}}',
  'user.2fa_disable': 'Force-disabled two-factor authentication for the user',
  'user.passkey_register': 'Registered a passkey',
  'user.passkey_delete': 'Deleted a passkey',
  'user.topup_complete': 'Completed top-up order for the user',
  'user.reset_passkey': 'Reset the user passkey',
  'user.oauth_unbind': 'Removed an OAuth binding for the user',
  // System settings
  'option.update': 'Updated system setting {{key}}',
  'option.passkey_domains':
    'Updated Passkey domains: removed {{domains}}; affected {{known}}; unknown {{unknown}}',
  'option.passkey_domains_confirmed':
    'Confirmed removal of Passkey domains: {{domains}}; affected {{known}}; unknown {{unknown}}',
  'option.passkey_domains_blocked':
    'Passkey domain change blocked: {{domains}}; affected {{known}}; unknown {{unknown}}',
  'option.passkey_domains_failed': 'Passkey domain update failed',
  'option.reset_ratio': 'Reset model ratios',
  'option.clear_affinity_cache': 'Cleared channel affinity cache',
  // Custom OAuth
  'custom_oauth.create': 'Created a custom OAuth provider',
  'custom_oauth.update': 'Updated a custom OAuth provider',
  'custom_oauth.delete': 'Deleted a custom OAuth provider',
  // Performance / cache
  'performance.clear_disk_cache': 'Cleared disk cache',
  'performance.gc': 'Triggered garbage collection',
  'performance.clear_logs': 'Cleared log files',
  // Channel
  'channel.create': 'Created channel {{name}} (type {{type}}, count {{count}})',
  'channel.update': 'Updated channel {{name}} (ID: {{id}})',
  'channel.status_update': 'Updated channel status (ID: {{id}})',
  'channel.status_update_batch':
    'Batch updated channel status ({{count}}/{{total}} changed)',
  'channel.delete': 'Deleted channel {{name}} (ID: {{id}})',
  'channel.delete_batch': 'Batch deleted {{count}} channels',
  'channel.delete_disabled': 'Deleted all disabled channels ({{count}})',
  'channel.key_view': 'Viewed channel key {{name}} (ID: {{id}})',
  'channel.tag_disable': 'Disabled channels with tag {{tag}}',
  'channel.tag_enable': 'Enabled channels with tag {{tag}}',
  'channel.tag_edit': 'Edited channels with tag {{tag}}',
  'channel.tag_batch_set': 'Batch set tag for {{count}} channels',
  'channel.copy':
    'Copied channel (source ID: {{sourceId}}) to {{name}} (new ID: {{id}})',
  'channel.multi_key_manage':
    'Multi-key management {{action}} on channel (ID: {{id}})',
  'channel.fetch_models_saved':
    'Saved fetched models for channel {{channel_id}}',
  'channel.upstream_apply':
    'Applied upstream model changes to channel (ID: {{id}})',
  'channel.upstream_apply_all':
    'Applied upstream model changes to {{count}} channels',
  'channel.upstream_detect_all':
    'Detected upstream model updates for all channels',
  // Groups (adty)
  'adty.group.create': 'Created group {{id}}',
  'adty.group.update': 'Updated group {{id}}',
  'adty.group.migrate': 'Migrated group {{id}} to {{to}}',
  'adty.group.delete': 'Deleted group {{id}}',
  'adty.admin_permissions.update': 'Changed admin permissions of user {{id}}',
  'adty.payment_orphan.credit': 'Credited payment event {{id}}',
  'adty.payment_orphan.resolve': 'Resolved payment event {{id}} as {{status}}',
  'adty.topup.manual_complete': 'Completed top-up {{trade_no}} manually',
  'adty.topup.query': 'Queried top-up {{trade_no}}',
  'adty.topup.refund': 'Refunded top-up {{trade_no}}',
  'adty.topup.refund.apply': 'Applied manual refund for top-up {{trade_no}}',
  'adty.topup.refund.approve': 'Approved manual refund for top-up {{trade_no}}',
  'adty.topup.refund.reclaim':
    'Reclaimed principal for top-up refund {{trade_no}}',
  'adty.topup.refund.bonus':
    'Planned bonus adjustment for top-up refund {{trade_no}}',
  'adty.topup.refund.commission':
    'Planned commission clawback for top-up refund {{trade_no}}',
  'adty.topup.refund.payout': 'Recorded payout for top-up refund {{trade_no}}',
  'adty.topup.refund.complete':
    'Completed manual refund for top-up {{trade_no}}',
  'adty.topup.refund.terminate':
    'Terminated manual refund for top-up {{trade_no}}',
  'adty.topup.method_bonus.update': 'Updated recharge bonus rates',
  'adty.topup.site_bonus_margin_fill.update':
    'Updated subsite bonus margin fallback settings',
  'adty.topup.site_method_bonus.update': 'Updated subsite recharge bonus rates',
  'adty.root_gift.grant': 'Granted expiring trial gift',
  'adty.gift.reserve': 'Reserved gift guarantee',
  'adty.gift.fulfill': 'Fulfilled pending gift',
  'adty.gift.release': 'Released gift guarantee',
  'adty.gift.spend': 'Spent gift guarantee',
  'adty.gift.spend_refund': 'Refunded spent gift guarantee',
  'adty.gift.fulfillment.review':
    'Reviewed gift fulfillment {{trade_no}} ({{action}})',
  'adty.team.create': 'Created team',
  'adty.team.close': 'Closed team',
  'adty.team.discount.update': 'Updated team discount',
  'adty.team.member.create': 'Created team member',
  'adty.team.member.disable': 'Disabled team member',
  'adty.team.member.enable': 'Enabled team member',
  'adty.team.member.delete': 'Deleted team member',
  'adty.team.member.password_reset': 'Reset team member password',
  'adty.team.member.leave': 'Team member left team',
  'adty.team.invite.create': 'Created team invite',
  'adty.team.invite.accept': 'Accepted team invite',
  'adty.team.invite.revoke': 'Revoked team invite',
  'adty.team.key.bind': 'Bound team API key',
  'adty.team.key.revoke': 'Revoked team API key',
  'adty.team.registration.create': 'Created team registration link',
  'adty.team.registration.complete': 'Completed team registration',
  'adty.team.registration.revoke': 'Revoked team registration link',
  'adty.epay_channel.create': 'Created payment channel {{name}} (ID: {{id}})',
  'adty.epay_channel.update': 'Updated payment channel {{name}} (ID: {{id}})',
  'adty.epay_channel.delete': 'Deleted payment channel {{id}}',
  'adty.epay_channel.replace':
    'Replaced payment channel {{id}} with channel {{new_id}}',
  'adty.epay_channel.revoke': 'Revoked payment channel {{id}} credentials',
  'adty.epay_channel.status': 'Set payment channel {{id}} to {{status}}',
  'adty.epay_channel.test': 'Tested payment channel {{id}}',
  'adty.epay_channel.maintenance':
    'Payment channel {{id}} moved to maintenance',
  'adty.user.limits':
    'Set request limits: RPM {{rpm}}, TPM {{tpm}}, concurrency {{concurrency}}',
  'adty.user.quota_adjust': 'Adjusted user quota manually',
  'adty.user.password_reset.challenge': 'Requested password reset verification',
  'adty.site.open': 'Opened reseller site',
  'adty.partner_lead.reject': 'Rejected partner site application',
  'adty.site.partner_bind': 'Bound partner referrer to reseller site',
  'adty.site.partner_rate': 'Updated partner referral commission rate',
  'adty.site.partner_stop': 'Stopped partner referral commission',
  'adty.hourly_settlement.run': 'Triggered hourly settlement run',
  'adty.referral.commission_release':
    'Released risk-held referral commission {{id}}',
  'adty.referral.commission_void':
    'Voided risk-held referral commission {{id}}',
  'adty.site.review': 'Reviewed reseller site application',
  'adty.site.approve': 'Approved reseller site application',
  'adty.site.reject': 'Rejected reseller site application',
  'adty.site.adjust': 'Adjusted reseller site',
  'adty.site.status': 'Updated reseller site status',
  'adty.site.paused_repair': 'Repaired paused reseller site',
  'adty.site.close_request': 'Requested reseller site closure',
  'adty.site.close_schedule': 'Scheduled reseller site closure',
  'adty.site.close_reschedule': 'Rescheduled reseller site closure',
  'adty.site.close': 'Closed reseller site',
  'adty.site.archive_hide': 'Hid archived reseller site',
  'adty.site.delete': 'Deleted reseller site',
  'adty.site.settings': 'Updated reseller settings',
  'adty.site.tiers': 'Updated site tiers',
  'adty.site.tier': 'Updated site tier',
  'adty.site.domain_disable': 'Disabled site custom domain {{domain_id}}',
  'adty.site.sender_domain_disable': 'Disabled site sender domain {{site_id}}',
  'adty.site.legal_update': 'Updated site legal documents',
  'adty.site.mail_config_update': 'Updated site email configuration',
  'adty.site.auth_config_update': 'Updated site basic authentication settings',
  'adty.site.close_mail_retry': 'Retried site closure migration email',
  'adty.site.close_mail_delivered':
    'Marked site closure migration email as delivered',
  'site.close_mail.retry': 'Retried site closure migration email batch',
  'adty.site.customer_review': 'Reviewed flagged site customer',
  'adty.site.earning_review': 'Reviewed site earning event',
  'adty.site.settlement_resume': 'Resumed site settlement',
  'adty.site.report': 'Handled site report',
  'adty.site.report_resolved': 'Resolved site report',
  'adty.site.report_dismissed': 'Dismissed site report',
  'adty.site.notice_takedown': 'Took down reseller site notice',
  'adty.site.customer.password_reset': 'Reset site customer password',
  'adty.site.customer_balance_credit': 'Credited site customer balance',
  'adty.site.team_balance_credit': 'Credited site team balance',
  'adty.site.tier_principal_adjust': 'Adjusted site tier principal',
  'adty.site.customer_relation_recovery_needed':
    'Site customer relation recovery required',
  'adty.site.order.refund.apply':
    'Applied manual refund for site order {{trade_no}}',
  'adty.site.order.refund.approve':
    'Approved manual refund for site order {{trade_no}}',
  'adty.site.order.refund.reclaim':
    'Reclaimed principal for site order refund {{trade_no}}',
  'adty.site.order.refund.bonus':
    'Planned bonus adjustment for site order refund {{trade_no}}',
  'adty.site.order.refund.commission':
    'Planned commission clawback for site order refund {{trade_no}}',
  'adty.site.order.refund.payout':
    'Recorded payout for site order refund {{trade_no}}',
  'adty.site.order.refund.complete':
    'Completed manual refund for site order {{trade_no}}',
  'adty.site.order.refund.terminate':
    'Terminated manual refund for site order {{trade_no}}',
  'margin.root_credit': 'Credited site margin by Root',
  'adty.margin.margin_topup': 'Topped up site margin',
  'adty.margin.margin_cycle_open': 'Opened site margin cycle',
  'adty.margin.earning_to_margin': 'Transferred earnings to margin',
  'adty.margin.commission_freeze': 'Froze margin for invite commission',
  'adty.margin.commission_release': 'Released margin for invite commission',
  'adty.margin.commission_deduct': 'Deducted margin for invite commission',
  'adty.margin.commission_closed_payout_deduct':
    'Deducted margin for closed-site invite commission payout',
  'adty.margin.commission_clawback_return':
    'Returned clawed-back invite commission to margin',
  'adty.anomaly_disposition.review': 'Reviewed fund anomaly disposition',
  'adty.site_withdraw.rules.update': 'Updated site withdrawal rules',
  'adty.site_withdraw.create': 'Created site withdrawal request',
  'adty.site_withdraw.paying': 'Marked site withdrawal as paying',
  'adty.site_withdraw.recall': 'Recalled site withdrawal payout',
  'adty.site_withdraw.failed': 'Marked site withdrawal payout as failed',
  'adty.site_withdraw.paid': 'Marked site withdrawal as paid',
  'adty.site_withdraw.reject': 'Rejected site withdrawal request',
  'adty.site_withdraw.cancel': 'Cancelled site withdrawal request',
  'adty.site_invite_withdrawal.approve':
    'Approved site invite commission withdrawal',
  'adty.site_invite_withdrawal.reject':
    'Rejected site invite commission withdrawal',
  'adty.site_invite_withdrawal.failed':
    'Marked site invite commission withdrawal as failed',
  'adty.site_invite_withdrawal.paid':
    'Marked site invite commission withdrawal as paid',
  'adty.voucher.issue': 'Issued recharge voucher',
  'adty.voucher.redeem': 'Redeemed recharge voucher',
  'adty.voucher.freeze': 'Froze recharge voucher',
  'adty.voucher.cancel': 'Cancelled recharge voucher',
  'staff.create': 'Created site staff account',
  'staff.update': 'Updated site staff account',
  'staff.password': 'Reset site staff password',
  'staff.delete': 'Deleted site staff account',
  'staff.customer.disable': 'Disabled site customer by staff',
  'adty.staff.customers.read': 'Viewed site customer list as staff',
  'adty.staff.customer.read': 'Viewed site customer details as staff',
  'adty.staff.topups.read': 'Viewed site customer top-ups as staff',
  'adty.staff.orders.read': 'Viewed site customer orders as staff',
  'adty.staff.usage.read': 'Viewed site customer usage logs as staff',
  'adty.telegram.save': 'Updated Telegram bot ({{purpose}})',
  'adty.telegram.unbind': 'Unbound Telegram notification ({{purpose}})',
  // Legacy alias for historical audit logs
  'telegram.unbind': 'Unbound Telegram notification ({{purpose}})',
  'adty.translation.save': 'Updated translation settings ({{mode}})',
  'adty.translation.retry': 'Retried translation job {{object_key}}',
  'adty.translation.complete': 'Completed translation job {{object_key}}',
  'adty.translation.edit': 'Edited translation entry {{object_key}}',
  'adty.translation.glossary.save': 'Saved translation glossary entry',
  'adty.translation.glossary.delete': 'Deleted translation glossary entry',
  'adty.payment_scenes.update': 'Updated payment scenes',
  'adty.ticket.image_policy.update': 'Updated ticket image policy',
  'adty.ticket.create': 'Created support ticket',
  'adty.ticket.status': 'Updated support ticket status',
  'adty.ticket.reply': 'Replied to support ticket',
  'adty.ticket.image.view': 'Viewed support ticket image',
  'adty.ticket.image.delete': 'Deleted support ticket image',
  'adty.notification.create': 'Created notification publication',
  'adty.notification.update': 'Updated notification publication',
  'adty.notification.withdraw': 'Withdrew notification publication',
  'adty.notification.hide': 'Hid notification',
  'adty.notice_template.save': 'Saved notice template',
  'adty.todo.transition': 'Updated todo item status',
  'adty.todo.complete': 'Completed todo item',
  'data_retention.update': 'Updated data retention policy',
  'adty.referral.settings': 'Updated referral settings',
  'adty.referral.affiliate_update': 'Updated an affiliate ({{action}})',
  'adty.referral.withdrawal_review': 'Reviewed withdrawal {{id}}',
  'adty.referral.withdrawal_pay': 'Marked withdrawal {{id}} as paid',
  // Prefill groups
  'prefill_group.create': 'Created a prefill group',
  'prefill_group.update': 'Updated a prefill group',
  'prefill_group.delete': 'Deleted a prefill group',
  // Vendors
  'vendor.create': 'Created a vendor',
  'vendor.update': 'Updated a vendor',
  'vendor.delete': 'Deleted a vendor',
  'vendor.metadata.save': 'Saved vendor metadata {{name}} (ID: {{vendor_id}})',
  'vendor.metadata.delete': 'Deleted vendor metadata (ID: {{vendor_id}})',
  'vendor.assign': 'Assigned models to vendor {{target_vendor_id}}',
  'vendor.merge': 'Merged vendors into vendor {{target_vendor_id}}',
  // Model metadata
  'model.create': 'Created a model',
  'model.update': 'Updated a model',
  'model.delete': 'Deleted a model',
  'model.delete_batch': 'Batch deleted models',
  'model.metadata.sync': 'Synced model metadata',
  'model.pricing.update': 'Updated model pricing',
  'model.sync_upstream': 'Synced upstream models',
  // System tasks
  'system_task.cancel': 'Cancelled system task {{task_id}}',
  // Deployments
  'deployment.create': 'Created a deployment',
  'deployment.update': 'Updated a deployment',
  'deployment.delete': 'Deleted a deployment',
  // Logs
  'log.clear': 'Cleared historical logs',
  'log.cleanup_start': 'Log cleanup task started.',
  // Generic middleware fallback
  generic: '{{method}} {{route}}',
}

/**
 * Render a log's `other.content_parts`, which the backend stores as English
 * source keys plus params so the text follows the viewer's language. Returns
 * null for logs written before structured content, letting callers fall back
 * to the raw `content` field.
 */
export function renderLogContent(
  other: LogOtherData | null | undefined,
  t: (key: string, opts?: Record<string, unknown>) => string
): string | null {
  const parts = other?.content_parts
  if (!Array.isArray(parts) || parts.length === 0) return null
  return parts.map((part) => t(part.key, part.params)).join(', ')
}

/**
 * The token name to show for a log. A log written without a token, such as a
 * channel test, carries a name the gateway stored as an English source key.
 */
export function logTokenName(
  log: Pick<UsageLog, 'token_id' | 'token_name'>,
  t: (key: string, opts?: Record<string, unknown>) => string
): string {
  return log.token_id === 0
    ? translateServerText(t, log.token_name)
    : log.token_name
}

const AUDIT_TEMPLATE_PLACEHOLDER_RE = /\{\{\s*(\w+)\s*\}\}/g

function hasNonEmptyAuditParam(value: unknown): boolean {
  if (value === undefined || value === null) return false
  if (typeof value === 'string') return value.trim().length > 0
  if (Array.isArray(value)) return value.length > 0
  return true
}

/**
 * Render the localized content of an operation log from its structured
 * `other.op` descriptor. Returns null when the log has no recognized action,
 * letting callers fall back to the raw `content` field.
 */
export function renderAuditContent(
  other: LogOtherData | null | undefined,
  t: (key: string, opts?: Record<string, unknown>) => string
): string | null {
  const op = other?.op
  if (!op?.action) return null
  const template = AUDIT_TEMPLATES[op.action]
  if (!template) return null
  const quotaOperation = buildQuotaAuditOperation(
    op.action,
    op.params ?? {},
    other?.audit_info?.success !== false,
    t
  )
  if (quotaOperation) {
    return `${quotaOperation.summary} · ${quotaOperation.description}`
  }
  const params = { ...op.params }
  if (op.action === 'adty.gift.fulfillment.review') {
    const placeholders = Array.from(
      template.matchAll(AUDIT_TEMPLATE_PLACEHOLDER_RE),
      (match) => match[1]
    ).filter((key): key is string => Boolean(key))
    const paramRecord = params as Record<string, unknown>
    if (
      placeholders.length > 0 &&
      placeholders.every((key) => !hasNonEmptyAuditParam(paramRecord[key]))
    ) {
      return t('Reviewed gift fulfillment')
    }
  }
  return t(template, params)
}
