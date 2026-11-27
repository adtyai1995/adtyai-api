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

import type {
  SiteWallet,
  SiteWalletLedgerPage,
} from '@/features/adty-ledger/site-wallet-labels'
import type {
  VoucherIssued,
  VoucherPage,
} from '@/features/adty-ledger/voucher-labels'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  OwnerCustomer,
  OwnerCustomerDetail,
  OwnerEarnings,
  OwnerLog,
  OwnerPage,
  OwnerSite,
  OwnerTopUp,
  PricingResult,
  SiteAuthConfig,
  SiteAuthConfigView,
  SiteBrand,
  SiteDomain,
  SiteDomainRole,
  SiteLegalDocs,
  SiteMailConfig,
  SiteMailConfigUpdate,
  SiteStats,
} from './types'

type ApiResponse<T> = { success: boolean; message?: string; data: T }

const BASE = '/api/adty/site'
export const OWNER_PAGE_SIZE = 20

export type PriceRevision = {
  id: number
  site_id: number
  user_id: number
  actor_id: number
  actor_username?: string
  group: string
  model: string
  kind: string
  action: string
  version: number
  before: string
  after: string
  effective_at: number
  created_at: number
}

export async function getPricingHistory(
  page: number,
  customerId?: number
): Promise<OwnerPage<PriceRevision>> {
  const path = customerId
    ? `${BASE}/customers/${customerId}/pricing/history`
    : `${BASE}/pricing/history`
  const response = await api.get<ApiResponse<OwnerPage<PriceRevision>>>(path, {
    params: { p: page, page_size: OWNER_PAGE_SIZE },
  })
  return requireServerSuccess(response.data).data
}

export const siteOwnerKeys = {
  all: ['adty-site-owner'] as const,
  site: ['adty-site-owner', 'site'] as const,
  stats: (from: string, to: string) =>
    ['adty-site-owner', 'stats', from, to] as const,
  list: (name: string, params: Record<string, unknown>) =>
    ['adty-site-owner', name, params] as const,
}

/** Header carrying a step-up proof for sensitive owner actions. */
function proofHeaders(proof: string) {
  return {
    headers: { 'X-Security-Proof': proof },
    singleUseAuthorization: true,
  }
}

export async function requestSiteClose(
  reason: string
): Promise<{ id: number; close_requested_at: number }> {
  const res = await api.post<
    ApiResponse<{ id: number; close_requested_at: number }>
  >(`${BASE}/close`, { reason })
  return requireServerSuccess(res.data).data
}

export async function getOwnerSite(): Promise<OwnerSite> {
  const res = await api.get<ApiResponse<OwnerSite>>(`${BASE}/self`)
  return requireServerSuccess(res.data).data
}

/** Brand, custom home page, legal docs, and the SEO switch do not need verification. */
export async function saveOwnerSite(body: {
  brand?: Partial<SiteBrand>
  expected_brand?: Partial<SiteBrand>
  seo_index?: boolean
  home_page_content?: string
  legal_docs?: SiteLegalDocs
}): Promise<OwnerSite> {
  const res = await api.put<ApiResponse<OwnerSite>>(`${BASE}/self`, body)
  return requireServerSuccess(res.data).data
}

/** One payment-method recharge bonus rule for this site. */
export async function saveOwnerSiteMethodBonus(
  method: string,
  enabled: boolean,
  rate: string,
  proof: string
): Promise<void> {
  const res = await api.put<ApiResponse<{ site_id: number }>>(
    `${BASE}/bonus-methods`,
    { method, enabled, rate },
    proofHeaders(proof)
  )
  requireServerSuccess(res.data)
}

export async function getOwnerStats(
  from: string,
  to: string
): Promise<SiteStats> {
  const res = await api.get<ApiResponse<SiteStats>>(`${BASE}/stats`, {
    params: { from: from || undefined, to: to || undefined },
  })
  return requireServerSuccess(res.data).data
}

export type SiteWithdrawRules = {
  configured: boolean
  min_usd?: string
  fee_usd?: string
  alipay_cny_rate?: string
  usdt_usd_rate?: string
  methods: string[]
}

export type SiteWithdrawal = {
  attempt_open?: boolean
  id: number
  amount_usd: string
  fee_usd: string
  method: string
  network: string
  account_no?: string
  account_name?: string
  qr_image_url?: string
  status: string
  rate: string
  payout_amount: string
  payout_currency: string
  created_at?: number
}

function archivedQuery(siteId?: number) {
  return { archived_site_id: siteId }
}

export async function getArchivedWebsiteDomains(siteId: number) {
  const res = await api.get<ApiResponse<{id:number; hostname:string}[]>>(`${BASE}/archived-sites/${siteId}/domains`)
  return requireServerSuccess(res.data).data ?? []
}

export async function removeArchivedDomain(siteId: number,id: number,proof: string) {
  const res = await api.delete<ApiResponse<unknown>>(`${BASE}/archived-sites/${siteId}/domains/${id}`, proofHeaders(proof))
  requireServerSuccess(res.data)
}

export async function getArchivedSites() {
  const res = await api.get<
    ApiResponse<
      {
        id: number
        subdomain: string
        root_domain: string
        closed_at: number
      }[]
    >
  >(`${BASE}/archived-sites`)
  return requireServerSuccess(res.data).data ?? []
}

export async function getSiteWithdrawRules(
  siteId?: number
): Promise<SiteWithdrawRules> {
  const res = await api.get<ApiResponse<SiteWithdrawRules>>(
    `${BASE}/withdrawals/rules`,
    { params: archivedQuery(siteId) }
  )
  return requireServerSuccess(res.data).data
}

export async function getSiteWithdrawalQuote(
  amount: string,
  method: string,
  siteId?: number
) {
  const res = await api.get<ApiResponse<{ payout_amount: string }>>(
    `${BASE}/withdrawals/rules`,
    { params: { ...archivedQuery(siteId), amount_usd: amount, method } }
  )
  return requireServerSuccess(res.data).data.payout_amount
}

export async function getSiteWithdrawals(
  siteId?: number,
  page = 1,
  status = '',
  number = ''
) {
  const res = await api.get<
    ApiResponse<{ items: SiteWithdrawal[]; total: number }>
  >(`${BASE}/withdrawals`, {
    params: {
      ...archivedQuery(siteId),
      p: page,
      page_size: OWNER_PAGE_SIZE,
      status,
      number,
    },
  })
  return requireServerSuccess(res.data).data
}

/** Uploads an Alipay payout QR or payout proof image (PNG, JPEG or WebP, up to 5 MB) and returns a signed URL. */
export async function uploadOwnerWithdrawalQR(
  file: File,
  purpose?: 'withdrawal_qr' | 'payment_proof'
): Promise<string> {
  const form = new FormData()
  form.append('file', file)
  if (purpose) {
    form.append('purpose', purpose)
  }
  const res = await api.post<ApiResponse<{ url: string }>>(
    `${BASE}/withdrawals/qr`,
    form
  )
  return requireServerSuccess(res.data).data.url
}

export async function createSiteWithdrawal(
  body: {
    site_id: number
    amount_usd: string
    method: string
    network: string
    account_no: string
    account_name: string
    qr_image_url?: string
    expected_payout?: string
    expected_rate: string
    idempotencyKey: string
  },
  siteId?: number,
  proof?: string
): Promise<SiteWithdrawal> {
  const res = await api.post<ApiResponse<SiteWithdrawal>>(
    `${BASE}/withdrawals`,
    {
      site_id: body.site_id,
      amount_usd: body.amount_usd,
      method: body.method,
      network: body.network,
      account_no: body.account_no,
      account_name: body.account_name,
      qr_image_url: body.qr_image_url ?? '',
      expected_rate: body.expected_rate,
      expected_payout: body.expected_payout,
    },
    {
      params: archivedQuery(siteId),
      headers: {
        'Idempotency-Key': body.idempotencyKey,
        ...(proof ? { 'X-Security-Proof': proof } : {}),
      },
      ...(proof ? { singleUseAuthorization: true } : {}),
    }
  )
  return requireServerSuccess(res.data).data
}

export async function transferEarningToMargin(
  amountUsd: string,
  idempotencyKey: string = crypto.randomUUID()
): Promise<void> {
  const res = await api.post<ApiResponse<{ transferred: boolean }>>(
    `${BASE}/wallets/earning-to-margin`,
    { amount_usd: amountUsd },
    { headers: { 'Idempotency-Key': idempotencyKey } }
  )
  requireServerSuccess(res.data)
}

/** Earnings and margin wallets; the server never returns the operating wallet here. */
export async function getOwnerWallets(): Promise<SiteWallet[]> {
  const res = await api.get<ApiResponse<SiteWallet[]>>(`${BASE}/wallets`)
  const data = requireServerSuccess(res.data).data
  return Array.isArray(data) ? data : []
}

export async function getOwnerWalletLedger(
  kind: string,
  page: number,
  pageSize: number
): Promise<SiteWalletLedgerPage> {
  const res = await api.get<ApiResponse<SiteWalletLedgerPage>>(
    `${BASE}/wallet-ledgers`,
    { params: { kind: kind || undefined, p: page, page_size: pageSize } }
  )
  return requireServerSuccess(res.data).data
}

/** Codes this owner issued from the site's wallets. */
export async function getOwnerVouchers(
  page: number,
  pageSize: number
): Promise<VoucherPage> {
  const res = await api.get<ApiResponse<VoucherPage>>(`${BASE}/vouchers`, {
    params: { p: page, page_size: pageSize },
  })
  return requireServerSuccess(res.data).data
}

export async function issueOwnerVoucher(
  body: {
    purpose: string
    amount_usd: string
    source?: string
    reason: string
    intent_key?: string
  },
  proof: string
): Promise<VoucherIssued> {
  const res = await api.post<ApiResponse<VoucherIssued>>(
    `${BASE}/vouchers`,
    body,
    proofHeaders(proof)
  )
  return requireServerSuccess(res.data).data
}

export async function freezeOwnerVoucher(
  id: number,
  proof: string
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${BASE}/vouchers/${id}/freeze`,
    undefined,
    proofHeaders(proof)
  )
  requireServerSuccess(res.data)
}

export async function cancelOwnerVoucher(
  id: number,
  proof: string
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${BASE}/vouchers/${id}/cancel`,
    undefined,
    proofHeaders(proof)
  )
  requireServerSuccess(res.data)
}

export async function cancelSiteWithdrawal(
  id: number,
  siteId?: number
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${BASE}/withdrawals/${id}/cancel`,
    undefined,
    { params: archivedQuery(siteId) }
  )
  requireServerSuccess(res.data)
}

export type SiteInviteCommissionWithdrawal = {
  site_id?: number
  id: number
  user_id: number
  username?: string
  created_at?: number
  legacy_decision_by?: number
  legacy_decision_at?: number
  reviewed_by?: number
  reviewed_at?: number
  paid_by?: number
  paid_at?: number
  failure_by?: number
  failure_at?: number
  can_cancel: boolean
  cancel_deadline: number
  cancel_reason: string
  amount_usd: string
  fee_usd: string
  net_usd: string
  method: string
  network: string
  account_no: string
  account_name: string
  qr_image_url: string
  status: string
  payout_amount: string
  payout_currency: string
  tx_id: string
  proof_note: string
  reject_reason: string
}

function commissionPath(closed: boolean) {
  return closed
    ? '/api/user/admin/referral/closed-site-withdrawals'
    : `${BASE}/commission-withdrawals`
}

export async function getOwnerCommissionWithdrawals(
  page: number,
  status: string,
  closed = false
) {
  const res = await api.get<
    ApiResponse<{ items: SiteInviteCommissionWithdrawal[]; total: number }>
  >(`${commissionPath(closed)}`, {
    params: { p: page, page_size: OWNER_PAGE_SIZE, status },
  })
  return requireServerSuccess(res.data).data
}

export async function reviewOwnerCommissionWithdrawal(
  id: number,
  approve: boolean,
  reason: string,
  closed = false,
  failedPayout = false
): Promise<void> {
  const res = await api.post<ApiResponse<{ reviewed: boolean }>>(
    `${commissionPath(closed)}/${id}/${failedPayout ? 'failed' : 'review'}`,
    { approve, reason }
  )
  requireServerSuccess(res.data)
}

export async function markOwnerCommissionWithdrawalPaid(
  id: number,
  txId: string,
  proof: string,
  closed = false
): Promise<void> {
  const res = await api.post<ApiResponse<{ paid: boolean }>>(
    `${commissionPath(closed)}/${id}/paid`,
    { tx_id: txId, proof }
  )
  requireServerSuccess(res.data)
}

export type OwnerEarningLine = {
  id: number
  user_id?: number
  username?: string
  model_name: string
  group: string
  request_id: string
  revenue_usd?: string
  platform_usd?: string
  markup?: string
  earning_usd: string
  created_at: number
}

export async function getOwnerEarningLines(
  page = 1,
  pageSize = OWNER_PAGE_SIZE
): Promise<OwnerEarningLine[]> {
  const res = await api.get<ApiResponse<OwnerEarningLine[]>>(
    `${BASE}/earnings/lines`,
    { params: { p: page, page_size: pageSize } }
  )
  return requireServerSuccess(res.data).data ?? []
}

export type RedeemedOwnerVoucher = {
  trade_no: string
  purpose: string
  amount_usd: string
  quota: number
  replayed?: boolean
}

/** Redeems a platform-issued margin recharge code into the site's margin wallet. */
export async function redeemOwnerMarginVoucher(
  code: string
): Promise<RedeemedOwnerVoucher> {
  const res = await api.post<ApiResponse<RedeemedOwnerVoucher>>(
    '/api/adty/site/margin/vouchers/redeem',
    { code: code.trim() }
  )
  return requireServerSuccess(res.data).data
}

export type SettlementSummary = Pick<SettlementSheet,'id'|'site_id'|'business_day'>
export type SettlementPage = {items:SettlementSummary[];total:number}
export type SettlementSheet = {
  generated_at?: number
  legacy_coverage?: {id:number;created_at:number;before_usd:string;after_usd:string}[]
  revisions?: {
    id: number
    before: SettlementSheet
    after: SettlementSheet
    sources?: string[]
    created_at: number
  }[]
  owner_self_use_topup_usd?: string
  owner_self_use_voucher_usd?: string
  owner_self_use_refund_usd?: string
  owner_transfer_usd?: string
  owner_self_use_usd?: string
  owner_self_use_cost_usd?: string
  id: number
  site_id: number
  business_day: string
  timezone: string
  day_start: number
  day_end: number
  event_watermark?: number
  batch_id?: string
  topup_face_usd: string
  paid_usd: string
  paid_by_currency: string
  consumption_usd: string
  supply_cost_usd: string
  spread_usd: string
  gift_reserve_usd: string
  gift_funding_usd: string
  gift_release_usd: string
  voucher_funding_usd?: string
  earning_to_margin_usd?: string
  margin_topup_usd?: string
  commission_paid_usd?: string
  commission_clawback_return_usd?: string
  trial_expense_usd: string
  commission_usd: string
  pending_coverage_unverified?: boolean
  pending_coverage_usd: string
  margin_delta_usd: string
  earnings_delta_usd: string
  earnings_available_delta_usd: string
  earnings_frozen_delta_usd: string
  withdrawal_usd: string
  withdrawal_fee_usd: string
  adjustment_usd: string
  anomaly_count: number
}

export async function getOwnerSettlementSheets(p:number, day:string,siteId?:number):Promise<SettlementPage> {
 const res=await api.get<ApiResponse<SettlementPage>>(`${BASE}/settlement-sheets`,{params:{p,page_size:20,business_day:day||undefined,...archivedQuery(siteId)}})
 return requireServerSuccess(res.data).data ?? {items:[],total:0}
}
export async function getOwnerSettlementDetail(id:number,siteId?:number):Promise<SettlementSheet> {
 const res=await api.get<ApiResponse<SettlementSheet>>(`${BASE}/settlement-sheets`,{params:{sheet_id:id,...archivedQuery(siteId)}})
 const sheet = requireServerSuccess(res.data).data
  if (!sheet) throw new Error(i18next.t('Settlement sheet is missing'))
  return sheet
}

export async function getOwnerEarnings(
  p: number,
  kind: string,
  siteId?: number
): Promise<OwnerEarnings> {
  const res = await api.get<ApiResponse<OwnerEarnings>>(`${BASE}/earnings`, {
    params: {
      ...archivedQuery(siteId),
      p,
      page_size: OWNER_PAGE_SIZE,
      kind: kind || undefined,
    },
  })
  return requireServerSuccess(res.data).data
}

export type SiteReferralView = {
  pending_coverage_cap: string | null
  cancel_window_minutes?: number | null
  observation_days: number | null
  cookie_ttl_days?: number | null
  self_invite_window_days?: number | null
  batch_register_threshold?: number | null
  batch_register_window_hours?: number | null
  min_usd: string | null
  fee_usd: string | null
  cny_rate: string | null
  withdraw_methods?: string[]
  withdraw_usdt_networks?: string[]
  rate_configured: boolean
  rate?: string
  enabled?: boolean
}

export type SiteInvite = {
  id: number
  site_id: number
  inviter_id: number
  inviter_username?: string
  invitee_id: number
  invitee_username?: string
  rate: string
  risk_flag?: string
  created_at: number
}

export type SiteReferralOverview = {
  affiliates: number
  bindings: number
  pending: string
  available: string
  frozen: string
  withdrawn: string
  pending_coverage_count: number
  pending_withdrawals: number
}

export type SiteReferralAffiliateAction =
  | 'rate'
  | 'disable'
  | 'restore'
  | 'freeze_settlement'
  | 'restore_settlement'
  | 'freeze_withdrawal'
  | 'restore_withdrawal'

export type SiteReferralAffiliateItem = {
  user_id: number
  username: string
  display_name?: string
  invite_code?: string
  rate_override?: number | null
  status?: string
  settlement_enabled?: boolean
  withdrawal_enabled?: boolean
  risk_reason?: string
  risk_note?: string
  invite_count: number
  pending: string
  available: string
  frozen: string
  withdrawn: string
  total_earned: string
}

export type SiteReferralAuditLogItem = {
  id: number
  site_id: number
  action: string
  operator_id: number
  operator_username?: string
  target_user_id: number
  target_username?: string
  reason: string
  old_value: string
  new_value: string
  ip: string
  created_at: number
}

export type SiteReferralCommissionItem = {
  id: number
  order_id: number
  trade_no: string
  inviter_id: number
  inviter_username: string
  invitee_id: number
  invitee_username: string
  order_amount_usd: string
  rate: string
  amount_usd: string
  covered_usd: string
  remaining_usd: string
  status: string
  coverage_status: string
  settle_at: number
  created_at: number
}

export type SiteReferralPage<T> = {
  items: T[]
  total: number
  page: number
  page_size: number
}

export async function getOwnerSiteReferralOverview(): Promise<SiteReferralOverview> {
  const res = await api.get<ApiResponse<SiteReferralOverview>>(
    `${BASE}/referral/overview`
  )
  return requireServerSuccess(res.data).data
}

export async function getOwnerSiteReferralAffiliates(
  page = 1,
  pageSize = OWNER_PAGE_SIZE,
  q = ''
): Promise<SiteReferralPage<SiteReferralAffiliateItem>> {
  const res = await api.get<
    ApiResponse<SiteReferralPage<SiteReferralAffiliateItem>>
  >(`${BASE}/referral/affiliates`, {
    params: {
      page,
      p: page,
      page_size: pageSize,
      q: q.trim() || undefined,
    },
  })
  const data = requireServerSuccess(res.data).data
  return {
    items: Array.isArray(data?.items) ? data.items : [],
    total: Number(data?.total) || 0,
    page: Number(data?.page) || page,
    page_size: Number(data?.page_size) || pageSize,
  }
}

export async function getOwnerSiteReferralCommissions(
  page = 1,
  pageSize = OWNER_PAGE_SIZE,
  status = '',
  q = '',
  inviterUserId?: number
): Promise<SiteReferralPage<SiteReferralCommissionItem>> {
  const res = await api.get<
    ApiResponse<SiteReferralPage<SiteReferralCommissionItem>>
  >(`${BASE}/referral/commissions`, {
    params: {
      page,
      p: page,
      page_size: pageSize,
      status: status.trim() || undefined,
      q: q.trim() || undefined,
      inviter_user_id:
        inviterUserId && inviterUserId > 0 ? inviterUserId : undefined,
    },
  })
  const data = requireServerSuccess(res.data).data
  return {
    items: Array.isArray(data?.items) ? data.items : [],
    total: Number(data?.total) || 0,
    page: Number(data?.page) || page,
    page_size: Number(data?.page_size) || pageSize,
  }
}

export async function updateOwnerSiteReferralAffiliate(
  userId: number,
  body: {
    action: SiteReferralAffiliateAction
    rate?: number | null
    reason?: string
  }
): Promise<void> {
  const res = await api.post<ApiResponse<{ updated: boolean }>>(
    `${BASE}/referral/affiliates/${userId}/update`,
    body
  )
  requireServerSuccess(res.data)
}

export async function getOwnerSiteReferralAuditLogs(
  page = 1,
  pageSize = OWNER_PAGE_SIZE
): Promise<SiteReferralPage<SiteReferralAuditLogItem>> {
  const res = await api.get<
    ApiResponse<SiteReferralPage<SiteReferralAuditLogItem>>
  >(`${BASE}/referral/audit-logs`, {
    params: { page, p: page, page_size: pageSize },
  })
  const data = requireServerSuccess(res.data).data
  return {
    items: Array.isArray(data?.items) ? data.items : [],
    total: Number(data?.total) || 0,
    page: Number(data?.page) || page,
    page_size: Number(data?.page_size) || pageSize,
  }
}

export async function getOwnerSiteReferral(): Promise<SiteReferralView> {
  const res = await api.get<ApiResponse<SiteReferralView>>(`${BASE}/referral`)
  return requireServerSuccess(res.data).data
}

export async function saveOwnerSiteReferralEnabled(
  enabled: boolean
): Promise<void> {
  const res = await api.put<ApiResponse<{ enabled: boolean }>>(
    `${BASE}/referral/enabled`,
    { enabled }
  )
  requireServerSuccess(res.data)
}

export type SiteMarketingField = Exclude<
  keyof SiteReferralView,
  'rate_configured' | 'enabled' | 'withdraw_methods' | 'withdraw_usdt_networks'
>

export type SiteMarketingUpdate = Partial<Record<SiteMarketingField, string>> & {
  enabled?: boolean
  withdraw_methods?: string[]
  withdraw_usdt_networks?: string[]
}

export async function saveOwnerSiteReferral(
  fields: SiteMarketingUpdate
): Promise<void> {
  const res = await api.put<ApiResponse<{ saved: boolean }>>(
    `${BASE}/referral`,
    fields
  )
  requireServerSuccess(res.data)
}

export async function getOwnerSiteInvites(): Promise<SiteInvite[]> {
  const res = await api.get<ApiResponse<{ items?: SiteInvite[] }>>(
    `${BASE}/referral/invites`
  )
  return requireServerSuccess(res.data).data.items ?? []
}

export async function resumeOwnerSiteCoverage(): Promise<void> {
  const res = await api.post<ApiResponse<{ resumed: boolean }>>(
    `${BASE}/referral/cover-pending`
  )
  requireServerSuccess(res.data)
}

export async function bindOwnerSiteInvite(
  inviterId: number,
  inviteeId: number
): Promise<void> {
  const res = await api.post<ApiResponse<{ bound: boolean }>>(
    `${BASE}/referral/invites`,
    { inviter_id: inviterId, invitee_id: inviteeId }
  )
  requireServerSuccess(res.data)
}

export async function getOwnerCustomers(
  p: number
): Promise<OwnerPage<OwnerCustomer>> {
  const res = await api.get<ApiResponse<OwnerPage<OwnerCustomer>>>(
    `${BASE}/customers`,
    { params: { p, page_size: OWNER_PAGE_SIZE } }
  )
  return requireServerSuccess(res.data).data
}

/** Exact match on the full username or email; rate limited per hour. */
export async function lookupOwnerCustomer(q: string): Promise<OwnerCustomer> {
  const res = await api.get<ApiResponse<OwnerCustomer>>(
    `${BASE}/customers/lookup`,
    { params: { q } }
  )
  return requireServerSuccess(res.data).data
}

export async function getOwnerCustomer(
  id: number
): Promise<OwnerCustomerDetail> {
  const res = await api.get<ApiResponse<OwnerCustomerDetail>>(
    `${BASE}/customers/${id}`
  )
  return requireServerSuccess(res.data).data
}

export async function getOwnerCustomerLogs(
  id: number,
  p: number
): Promise<OwnerPage<OwnerLog>> {
  const res = await api.get<ApiResponse<OwnerPage<OwnerLog>>>(
    `${BASE}/customers/${id}/logs`,
    { params: { p, page_size: OWNER_PAGE_SIZE } }
  )
  return requireServerSuccess(res.data).data
}

export async function getOwnerCustomerTopUps(
  id: number,
  p: number
): Promise<OwnerPage<OwnerTopUp>> {
  const res = await api.get<ApiResponse<OwnerPage<OwnerTopUp>>>(
    `${BASE}/customers/${id}/topups`,
    { params: { p, page_size: OWNER_PAGE_SIZE } }
  )
  return requireServerSuccess(res.data).data
}

export type OwnerCustomerPasswordResult = {
  user_id: number
  username: string
  generated_password?: string
}

/** Proof: action `customer.password`, target = customer id. */
export async function resetOwnerCustomerPassword(
  customerId: number,
  payload: { password?: string; random?: boolean; length?: number },
  proofToken: string
): Promise<OwnerCustomerPasswordResult> {
  const res = await api.post<ApiResponse<OwnerCustomerPasswordResult>>(
    `${BASE}/customers/${customerId}/password`,
    payload,
    proofHeaders(proofToken)
  )
  return requireServerSuccess(res.data).data
}

export async function reportCustomer(
  userId: number,
  reason: string
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(`${BASE}/report`, {
    user_id: userId,
    reason,
  })
  requireServerSuccess(res.data)
}

/** Proof: action `pricing`, target 0 (site-wide) or the customer id. */
export async function savePricing(
  body: {
    default_markup?: string
    clear_default?: boolean
    group_markups?: Record<string, string>
    model_markups?: Record<string, string>
    expected_version: number
  },
  proof: string,
  customerId?: number
): Promise<PricingResult> {
  const path = customerId
    ? `${BASE}/customers/${customerId}/pricing`
    : `${BASE}/pricing`
  const res = await api.put<ApiResponse<PricingResult>>(
    path,
    body,
    proofHeaders(proof)
  )
  return requireServerSuccess(res.data).data
}

/**
 * Binds a custom domain. `role` says what the domain is for:
 * `site` serves the whole site (pages, sign-in, top-up); `base_url` serves only
 * the relay (`/v1` …) so the address a customer pastes into a client is a pure API.
 */
export async function addDomain(
  hostname: string,
  role: SiteDomainRole = 'site'
): Promise<SiteDomain> {
  const res = await api.post<ApiResponse<SiteDomain>>(`${BASE}/domains`, {
    hostname,
    role,
  })
  return requireServerSuccess(res.data).data
}

/** Atomically binds both custom domains (site + base_url) in one submission. */
export async function addDomainPair(
  siteHostname: string,
  apiHostname: string
): Promise<SiteDomain[]> {
  const res = await api.post<ApiResponse<SiteDomain[]>>(
    `${BASE}/domains/pair`,
    {
      site_hostname: siteHostname,
      api_hostname: apiHostname,
    }
  )
  return requireServerSuccess(res.data).data ?? []
}

export async function getOwnerSiteMailConfig(): Promise<SiteMailConfig> {
  const res = await api.get<ApiResponse<SiteMailConfig>>(`${BASE}/mail-config`)
  return requireServerSuccess(res.data).data
}

/** Proof: action `mail.config`, target = site id. */
export async function saveOwnerSiteMailConfig(
  body: SiteMailConfigUpdate,
  proof?: string
): Promise<SiteMailConfig> {
  const res = await api.put<ApiResponse<SiteMailConfig>>(
    `${BASE}/mail-config`,
    body,
    proof ? proofHeaders(proof) : undefined
  )
  return requireServerSuccess(res.data).data
}

export async function testOwnerSiteMailConfig(to: string): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(`${BASE}/mail-config/test`, {
    to,
  })
  requireServerSuccess(res.data)
}

export async function getOwnerSiteAuthConfig(): Promise<SiteAuthConfigView> {
  const res = await api.get<ApiResponse<SiteAuthConfigView>>(
    `${BASE}/auth-config`
  )
  return requireServerSuccess(res.data).data
}

/** Proof: action `auth.config`, target = site id. */
export async function saveOwnerSiteAuthConfig(
  body: SiteAuthConfig,
  proof?: string
): Promise<SiteAuthConfigView> {
  const res = await api.put<ApiResponse<SiteAuthConfigView>>(
    `${BASE}/auth-config`,
    body,
    proof ? proofHeaders(proof) : undefined
  )
  return requireServerSuccess(res.data).data
}

/** Triggers an immediate DNS & verification check for one pending custom domain. */
export async function checkDomainNow(id: number): Promise<SiteDomain> {
  const res = await api.post<ApiResponse<SiteDomain>>(
    `${BASE}/domains/${id}/check`
  )
  return requireServerSuccess(res.data).data
}

/** Proof: action `domain.delete`, target = domain id. */
export async function removeDomain(id: number, proof: string): Promise<void> {
  const res = await api.delete<ApiResponse<unknown>>(
    `${BASE}/domains/${id}`,
    proofHeaders(proof)
  )
  requireServerSuccess(res.data)
}

/** Uploads a brand image (PNG, JPEG or WebP, up to 200 KB) and returns its address. */
export async function uploadLogo(file: File): Promise<string> {
  const form = new FormData()
  form.append('file', file)
  const res = await api.post<ApiResponse<{ url: string }>>(`${BASE}/logo`, form)
  return requireServerSuccess(res.data).data.url
}

export type ExportKind = 'earnings' | 'stats' | 'customers'

/** Fetches a CSV export (dashboard auth is a bearer token, so a plain link would not be signed in). */
export async function downloadExport(kind: ExportKind): Promise<void> {
  const res = await api.get<Blob>(`${BASE}/export`, {
    params: { kind },
    responseType: 'blob',
  })
  const url = URL.createObjectURL(res.data)
  const link = document.createElement('a')
  link.href = url
  link.download = `site-${kind}.csv`
  link.click()
  URL.revokeObjectURL(url)
}
