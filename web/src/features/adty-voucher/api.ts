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
import type { Voucher } from '@/features/adty-ledger/voucher-labels'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

type ApiResponse<T> = { success: boolean; message?: string; data: T }

const ADMIN_BASE = '/api/adty/admin'
const OWNER_BASE = '/api/adty/site'

export type VoucherListItem = Voucher & {
  batch_no?: string
  reason?: string
  designated_owner_id?: number
}

export type VoucherListPage = {
  total: number
  items: VoucherListItem[]
  page: number
  page_size: number
}

export type VoucherIssuance = {
  limit_usd: string
  frozen_usd: string
  spent_usd: string
}

export type VoucherBatchItem = {
  id: number
  code?: string
  code_tail: string
  purpose: string
  status?: string
  amount_usd: string
  quota: number
  batch_no?: string
  reason?: string
  source_kind?: string
  frozen_earnings_usd?: string
  frozen_margin_usd?: string
  target_site_id?: number
  created_at?: number
}

export type VoucherBatchIssued = {
  id: number
  code: string
  code_tail: string
  purpose: string
  status?: string
  amount_usd: string
  quota: number
  batch_no?: string
  count?: number
  total_amount_usd?: string
  source_kind?: string
  frozen_earnings_usd?: string
  frozen_margin_usd?: string
  target_site_id?: number
  designated_owner_id?: number
  intent_key?: string
  replayed?: boolean
  reason?: string
  created_at?: number
  items?: VoucherBatchItem[]
}

export const adtyVoucherKeys = {
  all: ['adty-voucher'] as const,
  adminIssuance: ['adty-voucher', 'admin', 'issuance'] as const,
  adminList: (params: Record<string, unknown>) =>
    ['adty-voucher', 'admin', 'list', params] as const,
  ownerList: (params: Record<string, unknown>) =>
    ['adty-voucher', 'owner', 'list', params] as const,
}

let voucherBatchIntentSeq = 0

export function createVoucherBatchIntentKey(): string {
  voucherBatchIntentSeq += 1
  const rand = Math.random().toString(36).slice(2, 10)
  return `vch-batch-${Date.now().toString(36)}-${voucherBatchIntentSeq}-${rand}`
}

function ownerProofHeaders(proof: string) {
  return {
    headers: { 'X-Security-Proof': proof },
    singleUseAuthorization: true,
  }
}

export async function getAdminVoucherIssuance(): Promise<VoucherIssuance> {
  const res = await api.get<ApiResponse<VoucherIssuance>>(
    `${ADMIN_BASE}/vouchers/limit`
  )
  return requireServerSuccess(res.data).data
}

export async function saveAdminVoucherIssuanceLimit(
  limitUsd: string
): Promise<void> {
  const res = await api.put<ApiResponse<unknown>>(
    `${ADMIN_BASE}/vouchers/limit`,
    { limit_usd: limitUsd }
  )
  requireServerSuccess(res.data)
}

export async function getAdminVouchersList(params: {
  purpose: 'main_user' | 'site_margin'
  status?: string
  site_id?: number
  batch_no?: string
  page: number
  page_size: number
}): Promise<VoucherListPage> {
  const res = await api.get<ApiResponse<VoucherListPage>>(
    `${ADMIN_BASE}/vouchers`,
    {
      params: {
        purpose: params.purpose,
        status: params.status || undefined,
        site_id:
          params.site_id && params.site_id > 0 ? params.site_id : undefined,
        batch_no: params.batch_no?.trim() || undefined,
        p: params.page,
        page: params.page,
        page_size: params.page_size,
      },
    }
  )
  const data = requireServerSuccess(res.data).data
  return {
    ...data,
    items: Array.isArray(data?.items) ? data.items : [],
  }
}

export async function issueAdminVoucherBatch(input: {
  purpose: 'main_user' | 'site_margin'
  site_id?: number
  designated_owner_id?: number
  amount_usd: string
  count: number
  reason: string
  intent_key?: string
}): Promise<VoucherBatchIssued> {
  const payload: Record<string, unknown> = {
    purpose: input.purpose,
    amount_usd: input.amount_usd,
    count: input.count,
    reason: input.reason,
    intent_key: input.intent_key,
  }
  if (input.site_id !== undefined && input.site_id > 0) {
    payload.site_id = input.site_id
  }
  if (
    input.designated_owner_id !== undefined &&
    input.designated_owner_id > 0
  ) {
    payload.designated_owner_id = input.designated_owner_id
  }
  const res = await api.post<ApiResponse<VoucherBatchIssued>>(
    `${ADMIN_BASE}/vouchers`,
    payload
  )
  return requireServerSuccess(res.data).data
}

export async function freezeAdminVoucherById(id: number): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${ADMIN_BASE}/vouchers/${id}/freeze`
  )
  requireServerSuccess(res.data)
}

export async function cancelAdminVoucherById(id: number): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${ADMIN_BASE}/vouchers/${id}/cancel`
  )
  requireServerSuccess(res.data)
}

export async function getOwnerVouchersList(params: {
  status?: string
  batch_no?: string
  page: number
  page_size: number
}): Promise<VoucherListPage> {
  const res = await api.get<ApiResponse<VoucherListPage>>(
    `${OWNER_BASE}/vouchers`,
    {
      params: {
        status: params.status || undefined,
        batch_no: params.batch_no?.trim() || undefined,
        p: params.page,
        page: params.page,
        page_size: params.page_size,
      },
    }
  )
  const data = requireServerSuccess(res.data).data
  return {
    ...data,
    items: Array.isArray(data?.items) ? data.items : [],
  }
}

export async function issueOwnerVoucherBatch(
  body: {
    purpose: 'site_customer'
    amount_usd: string
    count: number
    reason: string
    intent_key?: string
  },
  proof: string
): Promise<VoucherBatchIssued> {
  const res = await api.post<ApiResponse<VoucherBatchIssued>>(
    `${OWNER_BASE}/vouchers`,
    body,
    ownerProofHeaders(proof)
  )
  return requireServerSuccess(res.data).data
}

export async function freezeOwnerVoucherById(
  id: number,
  proof: string
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${OWNER_BASE}/vouchers/${id}/freeze`,
    undefined,
    ownerProofHeaders(proof)
  )
  requireServerSuccess(res.data)
}

export async function cancelOwnerVoucherById(
  id: number,
  proof: string
): Promise<void> {
  const res = await api.post<ApiResponse<unknown>>(
    `${OWNER_BASE}/vouchers/${id}/cancel`,
    undefined,
    ownerProofHeaders(proof)
  )
  requireServerSuccess(res.data)
}
