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
import i18next from 'i18next'

import type { SiteWalletLedgerPage } from '@/features/adty-ledger/site-wallet-labels'
import type {
  VoucherIssued,
  VoucherPage,
} from '@/features/adty-ledger/voucher-labels'
import type {
  SettlementSheet,
  SettlementPage,
} from '@/features/adty-site-owner/api'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  AdminSite,
  AdminSiteBrand,
  AdminSiteTierPrincipalAdjustResult,
  AdminSiteTierPrincipalView,
  ResellerEdgeDNSSync,
  ResellerEdgeProbe,
  ResellerEdgeStatus,
  ResellerSettings,
  ResellerUsage,
  ResellerSettingsUpdate,
  FlaggedCustomer,
  SiteAudit,
  SiteCustomer,
  SitePage,
  SiteReport,
  SiteStatsResponse,
  SiteStatusChange,
  SitesOverview,
  TierPrincipalAdjustMode,
} from './types'

type ApiResponse<T> = { success: boolean; message?: string; data: T }

const BASE = '/api/adty/admin'

/**
 * 服务端把空集合序列化成 null 或对象时，`data ?? []` 会把这个非数组原样返回，
 * 调用方一旦 `.map` 就整块白屏。列表只有数组才算数，其它形状一律按空列表处理。
 */
function listOrEmpty<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : []
}

export const SITE_ADMIN_PAGE_SIZE = 20

export const siteAdminKeys = {
  all: ['adty-site-admin'] as const,
  settings: ['adty-site-admin', 'settings'] as const,
  edgeStatus: ['adty-site-admin', 'edge-status'] as const,
  site: (id: number) => ['adty-site-admin', 'site', id] as const,
  list: (name: string, params: Record<string, unknown>) =>
    ['adty-site-admin', name, params] as const,
}

export async function getSites(params: {
  p: number
  status?: string
  keyword?: string
  page_size?: number
  sort?: string
  order?: 'asc' | 'desc'
}): Promise<SitePage<AdminSite>> {
  const res = await api.get<ApiResponse<SitePage<AdminSite>>>(`${BASE}/sites`, {
    params: { page_size: SITE_ADMIN_PAGE_SIZE, ...params },
  })
  return requireServerSuccess(res.data).data
}

export async function getSitesOverview(): Promise<SitesOverview> {
  const res = await api.get<ApiResponse<SitesOverview>>(
    `${BASE}/sites/overview`
  )
  return requireServerSuccess(res.data).data
}

export async function getSiteStats(id: number): Promise<SiteStatsResponse> {
  const res = await api.get<ApiResponse<SiteStatsResponse>>(
    `${BASE}/sites/${id}/stats`
  )
  return requireServerSuccess(res.data).data
}

export async function getAdminWithdrawRules() {
  const res = await api.get<
    ApiResponse<
      Partial<
        Record<
          'min_usd' | 'fee_usd' | 'alipay_cny_rate' | 'usdt_usd_rate',
          string
        >
      >
    >
  >(`${BASE}/site-withdrawals/rules`)
  return requireServerSuccess(res.data).data
}

export async function saveAdminWithdrawRules(body: {
  min_usd: string
  fee_usd: string
  alipay_cny_rate?: string
  usdt_usd_rate: string
}): Promise<void> {
  const res = await api.put<ApiResponse<{ saved: boolean }>>(
    `${BASE}/site-withdrawals/rules`,
    body
  )
  requireServerSuccess(res.data)
}

export type AdminVoucherIssued = VoucherIssued & {
  status?: string
  target_site_id?: number
  designated_owner_id?: number
  intent_key?: string
  replayed?: boolean
}

type PendingVoucherIntent = {
  intentKey: string
  voucherId?: number
  reconciled: boolean
}

let voucherIntentSeq = 0
const pendingVoucherIntents = new Map<string, PendingVoucherIntent>()

export function createVoucherIssuanceIntentKey(): string {
  voucherIntentSeq += 1
  const rand = Math.random().toString(36).slice(2, 10)
  return `vch-intent-${Date.now().toString(36)}-${voucherIntentSeq}-${rand}`
}

function voucherDraftSignature(input: {
  purpose: string
  site_id?: number
  designated_owner_id?: number
  amount_usd: string
  reason: string
}): string {
  return [
    input.purpose.trim(),
    input.site_id ?? 0,
    input.designated_owner_id ?? 0,
    input.amount_usd.trim(),
    input.reason.trim(),
  ].join('|')
}

export function resetVoucherIssuanceIntent(
  intentKeyOrVoucherId?: string | number
): void {
  if (intentKeyOrVoucherId === undefined) {
    pendingVoucherIntents.clear()
    return
  }
  for (const [sig, entry] of pendingVoucherIntents.entries()) {
    if (
      entry.intentKey === intentKeyOrVoucherId ||
      (typeof intentKeyOrVoucherId === 'number' &&
        entry.voucherId === intentKeyOrVoucherId)
    ) {
      pendingVoucherIntents.delete(sig)
    }
  }
}

async function postAdminVoucherWithIntent(input: {
  purpose: 'main_user' | 'site_margin'
  site_id?: number
  designated_owner_id?: number
  amount_usd: string
  reason: string
  intent_key?: string
}): Promise<AdminVoucherIssued> {
  const sig = voucherDraftSignature(input)
  const existing = pendingVoucherIntents.get(sig)
  const intentKey =
    input.intent_key?.trim() ||
    existing?.intentKey ||
    createVoucherIssuanceIntentKey()
  pendingVoucherIntents.set(sig, {
    intentKey,
    voucherId: existing?.voucherId,
    reconciled: existing?.reconciled ?? false,
  })
  const payload: Record<string, unknown> = {
    purpose: input.purpose,
    amount_usd: input.amount_usd,
    reason: input.reason,
    intent_key: intentKey,
  }
  if (input.site_id !== undefined) {
    payload.site_id = input.site_id
  }
  if (input.designated_owner_id !== undefined) {
    payload.designated_owner_id = input.designated_owner_id
  }
  const res = await api.post<ApiResponse<AdminVoucherIssued>>(
    `${BASE}/vouchers`,
    payload
  )
  const data = requireServerSuccess(res.data).data
  if (!data.replayed && Boolean(data.code)) {
    pendingVoucherIntents.delete(sig)
  } else {
    pendingVoucherIntents.set(sig, {
      intentKey: data.intent_key || intentKey,
      voucherId: data.id,
      reconciled: true,
    })
  }
  return data
}

export async function issueAdminMarginVoucher(body: {
  site_id: number
  designated_owner_id?: number
  amount_usd: string
  reason: string
  intent_key?: string
}): Promise<AdminVoucherIssued> {
  return postAdminVoucherWithIntent({
    purpose: 'site_margin',
    ...body,
  })
}

export type AdminWallet = {
  id: number
  kind: string
  balance: string
  frozen_usd?: string
  excess_frozen_usd?: string
  cycle_tier_usd: string
  cycle_used_usd: string
}

export type AnomalyDisposition = {
  id: number
  kind: string
  ref: string
  status: string
  note: string
  operator_id: number
  site_id?: number
  team_id?: number
  account_id?: number
  order_ref?: string
  difference?: string
  evidence?: string
  created_at?: number
}

export async function getAdminSiteWallets(
  siteId: number
): Promise<AdminWallet[]> {
  const res = await api.get<ApiResponse<AdminWallet[]>>(
    `${BASE}/sites/${siteId}/wallets`
  )
  return listOrEmpty(requireServerSuccess(res.data).data)
}

export async function creditAdminSiteMargin(
  siteId: number,
  body: { amount_usd: string; reason: string },
  idempotencyKey: string
): Promise<{
  credited: boolean
  ledger_id: number
  balance: string
  balance_before: string
  balance_after: string
}> {
  const res = await api.post<
    ApiResponse<{
      credited: boolean
      ledger_id: number
      balance: string
      balance_before: string
      balance_after: string
    }>
  >(`${BASE}/sites/${siteId}/margin`, body, {
    headers: { 'Idempotency-Key': idempotencyKey },
  })
  return requireServerSuccess(res.data).data
}

/** Root only: every wallet kind of one site, newest first. */
export async function getAdminSiteWalletLedger(
  siteId: number,
  kind: string,
  page: number,
  pageSize: number
): Promise<SiteWalletLedgerPage> {
  const res = await api.get<ApiResponse<SiteWalletLedgerPage>>(
    `${BASE}/sites/${siteId}/wallet-ledgers`,
    { params: { kind: kind || undefined, p: page, page_size: pageSize } }
  )
  return requireServerSuccess(res.data).data
}

export async function getAdminAnomalyDispositions(
  siteId: number,
  filters: {
    status: string
    kind: string
    order_ref: string
    p: number
    page_size: number
  }
): Promise<{ items: AnomalyDisposition[]; total: number }> {
  const res = await api.get<
    ApiResponse<{ items: AnomalyDisposition[]; total: number }>
  >(`${BASE}/fund-anomalies/dispositions`, {
    params: { site_id: siteId, ...filters },
  })
  return requireServerSuccess(res.data).data
}

export async function reviewAdminAnomalyDisposition(
  id: number,
  status: 'reviewing' | 'closed',
  note: string
): Promise<void> {
  const res = await api.post<ApiResponse<{ reviewed: boolean }>>(
    `${BASE}/fund-anomalies/dispositions/${id}/review`,
    { status, note }
  )
  requireServerSuccess(res.data)
}

export async function getAdminFundAnomalies(siteId: number): Promise<string[]> {
  const res = await api.get<ApiResponse<{ kinds?: string[] }>>(
    `${BASE}/fund-anomalies`,
    { params: { site_id: siteId } }
  )
  return requireServerSuccess(res.data).data.kinds ?? []
}

export async function getAdminBusinessTimezone(): Promise<{
  configured: boolean
  timezone: string
}> {
  const res = await api.get<
    ApiResponse<{ configured: boolean; timezone: string }>
  >(`${BASE}/business-timezone`)
  return requireServerSuccess(res.data).data
}

export async function saveAdminBusinessTimezone(
  timezone: string
): Promise<void> {
  const res = await api.put<ApiResponse<{ saved: boolean }>>(
    `${BASE}/business-timezone`,
    { timezone }
  )
  requireServerSuccess(res.data)
}

export async function getAdminSettlementSheets(
  p: number,
  day: string,
  siteId?: number
): Promise<SettlementPage> {
  const res = await api.get<ApiResponse<SettlementPage>>(
    `${BASE}/settlement-sheets`,
    {
      params: {
        p,
        page_size: 20,
        business_day: day || undefined,
        site_id: siteId,
      },
    }
  )
  return requireServerSuccess(res.data).data ?? { items: [], total: 0 }
}
export async function getAdminSettlementDetail(
  id: number,
  siteId?: number
): Promise<SettlementSheet> {
  const res = await api.get<ApiResponse<SettlementSheet>>(
    `${BASE}/settlement-sheets`,
    { params: { sheet_id: id, site_id: siteId } }
  )
  const sheet = requireServerSuccess(res.data).data
  if (!sheet) throw new Error(i18next.t('Settlement sheet is missing'))
  return sheet
}

export async function getAdminSiteInviteBatchPolicy(): Promise<{
  configured: boolean
  threshold?: string
  window_hours?: string
}> {
  const res = await api.get<
    ApiResponse<{
      configured: boolean
      threshold?: string
      window_hours?: string
    }>
  >(`${BASE}/site-referral/batch-policy`)
  return requireServerSuccess(res.data).data
}

export async function saveAdminSiteInviteBatchPolicy(
  threshold: string,
  windowHours: string
): Promise<void> {
  const res = await api.put<ApiResponse<{ saved: boolean }>>(
    `${BASE}/site-referral/batch-policy`,
    { threshold, window_hours: windowHours }
  )
  requireServerSuccess(res.data)
}

export async function getAdminSiteCommissionMargin(
  siteId: number
): Promise<{ allowed: boolean }> {
  const res = await api.get<ApiResponse<{ site_id: number; allowed: boolean }>>(
    `${BASE}/sites/${siteId}/commission-margin`
  )
  return requireServerSuccess(res.data).data
}

export async function saveAdminSiteCommissionMargin(
  siteId: number,
  allowed: boolean
): Promise<void> {
  const res = await api.put<ApiResponse<{ site_id: number; allowed: boolean }>>(
    `${BASE}/sites/${siteId}/commission-margin`,
    { allowed }
  )
  requireServerSuccess(res.data)
}

export async function getFlaggedCustomers(
  p: number
): Promise<SitePage<FlaggedCustomer>> {
  const res = await api.get<ApiResponse<SitePage<FlaggedCustomer>>>(
    `${BASE}/sites/flagged-customers`,
    { params: { p, page_size: SITE_ADMIN_PAGE_SIZE } }
  )
  return requireServerSuccess(res.data).data
}

export async function getSiteReports(p: number): Promise<SitePage<SiteReport>> {
  const res = await api.get<ApiResponse<SitePage<SiteReport>>>(
    `${BASE}/sites/reports`,
    { params: { p, page_size: SITE_ADMIN_PAGE_SIZE, status: 'pending' } }
  )
  return requireServerSuccess(res.data).data
}

export async function reviewFlaggedCustomer(
  userId: number,
  convertSkipped: boolean
): Promise<{ user_id: number; reviewed: boolean }> {
  const res = await api.post<
    ApiResponse<{ user_id: number; reviewed: boolean }>
  >(`${BASE}/sites/customers/${userId}/review`, {
    convert_skipped: convertSkipped,
  })
  return requireServerSuccess(res.data).data
}

export async function handleSiteReport(
  id: number,
  body: { status: 'resolved' | 'dismissed'; note: string }
): Promise<SiteReport> {
  const res = await api.post<ApiResponse<SiteReport>>(
    `${BASE}/sites/reports/${id}/handle`,
    body
  )
  return requireServerSuccess(res.data).data
}

export type AdminSiteDomain = {
  close_action?: 'removed' | 'redirect_only'
  role?: string
  managed_by?: string
  automation_state?: string
  ssl_status?: string
  last_error?: string
  edge_acme_alias?: string
  edge_cert_status?: string
  edge_cert_error?: string
  edge_cert_not_after?: number
  id: number
  hostname: string
  status: string
}

export async function checkDomainProvider(
  provider: 'cloudflare' | 'aliyun'
): Promise<void> {
  const res = await api.post(`${BASE}/reseller/domains/check/${provider}`)
  requireServerSuccess(res.data)
}

export async function retryDomainAutomation(id: number): Promise<void> {
  const res = await api.post(`${BASE}/sites/domains/${id}/retry`)
  requireServerSuccess(res.data)
}

export async function getResellerEdgeStatus(): Promise<ResellerEdgeStatus> {
  const res = await api.get<ApiResponse<ResellerEdgeStatus>>(
    `${BASE}/reseller/edge/status`
  )
  return requireServerSuccess(res.data).data
}

export async function setResellerEdgeFailover(
  failoverToCF: boolean
): Promise<{ failover_to_cf: boolean; dns_sync: ResellerEdgeDNSSync }> {
  const res = await api.post<
    ApiResponse<{ failover_to_cf: boolean; dns_sync: ResellerEdgeDNSSync }>
  >(`${BASE}/reseller/edge/failover`, { failover_to_cf: failoverToCF })
  return requireServerSuccess(res.data).data
}

export async function syncResellerEdgeDNS(): Promise<ResellerEdgeDNSSync> {
  const res = await api.post<ApiResponse<ResellerEdgeDNSSync>>(
    `${BASE}/reseller/edge/sync-dns`
  )
  return requireServerSuccess(res.data).data
}

export async function probeResellerEdge(): Promise<ResellerEdgeProbe> {
  const res = await api.post<ApiResponse<ResellerEdgeProbe>>(
    `${BASE}/reseller/edge/probe`
  )
  return requireServerSuccess(res.data).data
}

export async function retryResellerEdgeCert(certName = ''): Promise<void> {
  const res = await api.post<ApiResponse<{ queued: boolean }>>(
    `${BASE}/reseller/edge/cert/retry`,
    certName ? { cert_name: certName } : {}
  )
  requireServerSuccess(res.data)
}

export type AdminSenderDomain = {
  cleanup_error?: string
  retry_at?: number
  site_id: number
  domain: string
  status: string
}

export async function getSite(id: number): Promise<{
  site: AdminSite
  audits: SiteAudit[]
  domains: AdminSiteDomain[]
  sender_domains: AdminSenderDomain[]
}> {
  const res = await api.get<
    ApiResponse<{
      site: AdminSite
      audits: SiteAudit[]
      domains: AdminSiteDomain[]
      sender_domains: AdminSenderDomain[]
    }>
  >(`${BASE}/sites/${id}`)
  return requireServerSuccess(res.data).data
}

export async function disableSiteDomain(id: number): Promise<void> {
  const res = await api.delete<ApiResponse<unknown>>(
    `${BASE}/sites/domains/${id}`
  )
  requireServerSuccess(res.data)
}

export async function disableSiteSenderDomain(siteId: number): Promise<void> {
  const res = await api.delete<ApiResponse<unknown>>(
    `${BASE}/sites/${siteId}/sender-domain`
  )
  requireServerSuccess(res.data)
}

export async function getSiteCustomers(
  id: number,
  p: number
): Promise<SitePage<SiteCustomer>> {
  const res = await api.get<ApiResponse<SitePage<SiteCustomer>>>(
    `${BASE}/sites/${id}/customers`,
    { params: { p, page_size: SITE_ADMIN_PAGE_SIZE } }
  )
  return requireServerSuccess(res.data).data
}

export async function createSite(
  body: {
    owner_username: string
    owner_password: string
    owner_email?: string
    owner_display_name?: string
    subdomain: string
    root_domain?: string
    cost_factor?: string
    tier_code?: string
    tier_mode?: 'auto' | 'manual'
    contact_info?: string
  },
  idempotencyKey?: string
): Promise<AdminSite> {
  const res = await api.post<ApiResponse<AdminSite>>(`${BASE}/sites`, body, {
    headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
  })
  return requireServerSuccess(res.data).data
}

export type DeleteSiteResult = {
  deleted: boolean
  mode: 'hard_deleted' | 'archived_hidden'
}

export async function deleteSite(
  id: number,
  reason?: string
): Promise<DeleteSiteResult> {
  const res = await api.delete<ApiResponse<DeleteSiteResult>>(
    `${BASE}/sites/${id}`,
    { data: reason ? { reason } : {} }
  )
  return requireServerSuccess(res.data).data
}

export async function reviewSite(
  id: number,
  body: {
    approve: boolean
    cost_factor?: string
    note: string
  }
): Promise<AdminSite> {
  const res = await api.post<ApiResponse<AdminSite>>(
    `${BASE}/sites/${id}/review`,
    body
  )
  return requireServerSuccess(res.data).data
}

export async function adjustSite(
  id: number,
  body: {
    cost_factor?: string
    visibility?: string
    allowed_groups?: string
    contact_info?: string
    brand?: Partial<Record<keyof AdminSiteBrand, string>>
    home_page_content?: string
    seo_index?: boolean
    clear?: Array<'cost_factor' | 'allowed_groups'>
    reason: string
  }
): Promise<AdminSite> {
  const res = await api.put<ApiResponse<AdminSite>>(`${BASE}/sites/${id}`, body)
  return requireServerSuccess(res.data).data
}

export async function resumeSiteSettlement(id: number): Promise<AdminSite> {
  const res = await api.post<ApiResponse<AdminSite>>(
    `${BASE}/sites/${id}/settlement/resume`
  )
  return requireServerSuccess(res.data).data
}

export async function setSiteStatus(
  id: number,
  body: SiteStatusChange
): Promise<AdminSite> {
  const res = await api.put<ApiResponse<AdminSite>>(
    `${BASE}/sites/${id}/status`,
    body
  )
  return requireServerSuccess(res.data).data
}

export async function getPausedSiteRepairs(): Promise<AdminSite[]> {
  const res = await api.get<ApiResponse<AdminSite[]>>(
    `${BASE}/sites/paused-repairs`
  )
  return listOrEmpty(requireServerSuccess(res.data).data)
}

export async function repairPausedSite(
  id: number,
  body: { outcome: 'active' | 'close'; reason: string; immediate?: boolean }
): Promise<AdminSite> {
  const res = await api.post<ApiResponse<AdminSite>>(
    `${BASE}/sites/${id}/paused-repair`,
    body
  )
  return requireServerSuccess(res.data).data
}

/** Takes the site's public notice down (site adjust permission); the rest of the brand stays. */
export async function clearSiteNotice(
  id: number,
  reason: string
): Promise<{ site_id: number; cleared: boolean }> {
  const res = await api.delete<
    ApiResponse<{ site_id: number; cleared: boolean }>
  >(`${BASE}/sites/${id}/notice`, { data: { reason } })
  return requireServerSuccess(res.data).data
}

export async function getResellerUsage(): Promise<ResellerUsage> {
  const res = await api.get<ApiResponse<ResellerUsage>>(
    `${BASE}/reseller/usage`
  )
  return requireServerSuccess(res.data).data
}

function readResellerSettings(
  response: ApiResponse<ResellerSettings>,
  status: number
) {
  if (
    typeof response !== 'object' ||
    !response ||
    typeof response.success !== 'boolean'
  ) {
    throw new Error(
      i18next.t('Settings response could not be read (HTTP {{status}})', {
        status,
      })
    )
  }
  return requireServerSuccess(response).data
}

export async function getResellerSettings(): Promise<ResellerSettings> {
  const res = await api.get<ApiResponse<ResellerSettings>>(
    `${BASE}/reseller/settings`
  )
  return readResellerSettings(res.data, res.status)
}

export async function saveResellerSettings(
  body: ResellerSettingsUpdate
): Promise<ResellerSettings> {
  const res = await api.put<ApiResponse<ResellerSettings>>(
    `${BASE}/reseller/settings`,
    body
  )
  return readResellerSettings(res.data, res.status)
}

/** One site earnings withdrawal in Root's review list. Amounts are USD. */
export type AdminSiteWithdrawal = {
  attempt_number: number
  attempt_open: boolean
  id: number
  site_id: number
  owner_user_id: number
  owner_username?: string
  subdomain: string
  amount_usd: string
  fee_usd: string
  net_usd: string
  method: string
  network: string
  account_no: string
  account_name: string
  qr_image_url?: string
  rate: string
  payout_amount: string
  payout_currency: string
  status: string
  tx_id: string
  proof_note: string
  reviewer_id: number
  reviewed_at: number
  paid_at: number
  created_at: number
}

export async function getAdminSiteWithdrawals(
  status: string,
  page: number,
  pageSize: number
): Promise<{ total: number; items: AdminSiteWithdrawal[] }> {
  const res = await api.get<
    ApiResponse<{ total: number; items: AdminSiteWithdrawal[] }>
  >(`${BASE}/site-withdrawals`, {
    params: { status: status || undefined, p: page, page_size: pageSize },
  })
  return requireServerSuccess(res.data).data
}

export async function markSiteWithdrawalPaying(
  id: number,
  attempt: number
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${BASE}/site-withdrawals/${id}/paying`,
    { attempt_number: attempt }
  )
  requireServerSuccess(res.data)
}

export async function recallSiteWithdrawal(
  id: number,
  reason: string,
  attempt: number,
  reference = ''
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${BASE}/site-withdrawals/${id}/recall`,
    { reason, attempt_number: attempt, tx_id: reference }
  )
  requireServerSuccess(res.data)
}

export async function markSiteWithdrawalPaid(
  id: number,
  body: { tx_id: string; proof: string; attempt_number: number }
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${BASE}/site-withdrawals/${id}/paid`,
    body
  )
  requireServerSuccess(res.data)
}

export async function rejectSiteWithdrawal(
  id: number,
  reason: string
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${BASE}/site-withdrawals/${id}/reject`,
    { reason }
  )
  requireServerSuccess(res.data)
}

export type VoucherIssuance = {
  limit_usd: string
  frozen_usd: string
  spent_usd: string
}

export async function getAdminVouchers(
  filters: { status: string; purpose: string },
  page: number,
  pageSize: number
): Promise<VoucherPage> {
  const res = await api.get<ApiResponse<VoucherPage>>(`${BASE}/vouchers`, {
    params: {
      status: filters.status || undefined,
      purpose: filters.purpose || undefined,
      p: page,
      page_size: pageSize,
    },
  })
  return requireServerSuccess(res.data).data
}

export async function getVoucherIssuance(): Promise<VoucherIssuance> {
  const res = await api.get<ApiResponse<VoucherIssuance>>(
    `${BASE}/vouchers/limit`
  )
  return requireServerSuccess(res.data).data
}

export async function saveVoucherIssuanceLimit(
  limitUsd: string
): Promise<void> {
  const res = await api.put<ApiResponse<unknown>>(`${BASE}/vouchers/limit`, {
    limit_usd: limitUsd,
  })
  requireServerSuccess(res.data)
}

export async function issueMainUserVoucher(body: {
  amount_usd: string
  reason: string
  intent_key?: string
}): Promise<AdminVoucherIssued> {
  return postAdminVoucherWithIntent({
    purpose: 'main_user',
    ...body,
  })
}

export async function freezeAdminVoucher(id: number): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${BASE}/vouchers/${id}/freeze`
  )
  requireServerSuccess(res.data)
}

export async function cancelAdminVoucher(id: number): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${BASE}/vouchers/${id}/cancel`
  )
  requireServerSuccess(res.data)
  resetVoucherIssuanceIntent(id)
}

export async function getAdminSiteBonusMarginFill(
  siteId: number
): Promise<{ site_id: number; enabled: boolean }> {
  const res = await api.get<ApiResponse<{ site_id: number; enabled: boolean }>>(
    `${BASE}/sites/${siteId}/bonus-margin-fill`
  )
  return requireServerSuccess(res.data).data
}

export async function saveAdminSiteBonusMarginFill(
  siteId: number,
  enabled: boolean
): Promise<void> {
  const res = await api.put<ApiResponse<unknown>>(
    `${BASE}/sites/${siteId}/bonus-margin-fill`,
    { enabled }
  )
  requireServerSuccess(res.data)
}

/** One site level (model.AdtySiteTier). Decimals are strings; an empty cost factor uses the default. */
export type SiteTier = {
  id?: number
  code: string
  name: string
  min_effective_users: number
  min_principal_usd: string
  min_consumption_usd: string
  cost_factor: string | null
  deposit_usd: string
  enabled: boolean
  sort_order: number
}

export async function getSiteTiers(): Promise<SiteTier[]> {
  const res = await api.get<ApiResponse<SiteTier[]>>(`${BASE}/reseller/tiers`)
  return listOrEmpty(requireServerSuccess(res.data).data)
}

/** Create and edit have separate conflict and missing-level semantics. */
export async function saveSiteTier(
  tier: SiteTier,
  create: boolean
): Promise<SiteTier[]> {
  const res = await api[create ? 'post' : 'patch']<ApiResponse<SiteTier[]>>(
    `${BASE}/reseller/tiers`,
    {
      tiers: [tier],
    }
  )
  return listOrEmpty(requireServerSuccess(res.data).data)
}

export async function setSiteTierAssignment(
  siteId: number,
  code: string,
  mode: 'auto' | 'manual'
): Promise<void> {
  const res = await api.put<ApiResponse<unknown>>(
    `${BASE}/sites/${siteId}/tier`,
    { code, mode }
  )
  requireServerSuccess(res.data)
}

export async function getAdminSiteTierPrincipal(
  siteId: number,
  page = 1,
  pageSize = 10
): Promise<AdminSiteTierPrincipalView> {
  const res = await api.get<ApiResponse<AdminSiteTierPrincipalView>>(
    `${BASE}/sites/${siteId}/tier-principal`,
    { params: { p: page, page_size: pageSize } }
  )
  const data = requireServerSuccess(res.data).data
  return { ...data, items: listOrEmpty(data.items) }
}

export async function adjustAdminSiteTierPrincipal(
  siteId: number,
  body: {
    mode: TierPrincipalAdjustMode
    amount_usd: string
    reason: string
  },
  idempotencyKey: string
): Promise<AdminSiteTierPrincipalAdjustResult> {
  const res = await api.post<ApiResponse<AdminSiteTierPrincipalAdjustResult>>(
    `${BASE}/sites/${siteId}/tier-principal`,
    { ...body, idempotency_key: idempotencyKey },
    { headers: { 'Idempotency-Key': idempotencyKey } }
  )
  return requireServerSuccess(res.data).data
}

export type CloseMailRecipient = {
  attempts?: {number:number; provider:string; outcome:string; started_at:number;provider_message_id?:string}[]
  id: number
  email: string
  status: string
  attempt_count: number
  last_error: string
}
type CloseMailBatch = {
  id: number
  revision: number
  execute_at: number
  retry_status: string
}
export async function getCloseMail(
  siteId: number,
  batch: number,
  page: number
) {
  const res = await api.get<
    ApiResponse<{
      batches: CloseMailBatch[]
      recipients: CloseMailRecipient[]
      batch_id: number
      total: number
    }>
  >(`${BASE}/sites/${siteId}/close-mail`, {
    params: { batch: batch || undefined, p: page, page_size: 20 },
  })
  return requireServerSuccess(res.data).data
}
export async function resolveCloseMail(
  siteId: number,
  row: CloseMailRecipient,
  decision: string,
  reason: string
) {
  const res = await api.post<ApiResponse<null>>(
    `${BASE}/sites/${siteId}/close-mail`,
    { recipient_id: row.id, attempt: row.attempt_count, decision, reason }
  )
  requireServerSuccess(res.data)
}
