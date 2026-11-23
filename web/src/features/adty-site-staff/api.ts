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
import type {
  OwnerCustomer,
  OwnerPage,
  OwnerTopUp,
} from '@/features/adty-site-owner/types'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export type StaffRole = 'support' | 'finance' | 'operations' | 'manager'
export type StaffPermission =
  | 'customers.read'
  | 'customers.balance'
  | 'customers.topups'
  | 'customers.usage'
  | 'tickets.manage'
  | 'customers.balance_request'
  | 'customers.disable'
export type StaffCustomerAction = 'disable'
export type StaffBalanceDirection = 'increase' | 'decrease'
export type StaffBalanceRequest = {
  id: number
  site_id: number
  staff_user_id: number
  customer_user_id: number
  direction: StaffBalanceDirection
  amount_usd: string
  reason: string
  order_ref: string
  funding_source: string
  batch_kind: string
  batch_source: string
  before_quota?: number
  after_quota?: number
  ledger_id?: number
  idempotency_key?: string
  status: 'pending' | 'approved' | 'rejected'
  reviewer_id: number
  reviewer_role: string
  created_at: number
  reviewed_at: number
}
export type StaffAccount = {
  id: number
  user_id: number
  site_id: number
  username: string
  display_name: string
  role: StaffRole
  permissions: StaffPermission[]
  status: 'enabled' | 'disabled'
  version: number
}
export type StaffInput = {
  username?: string
  display_name?: string
  password?: string
  role?: StaffRole
  permissions?: StaffPermission[]
  status?: 'enabled' | 'disabled'
  expected_version?: number
}
export type StaffAudit = {
  id: number
  action: string
  operator_id: number
  created_at: number
  old_value: string
  new_value: string
  staff_id: number
  customer_id: number
}
export type StaffUsage = {
  id: number
  created_at: number
  type: number
  model_name: string
  quota: number
  prompt_tokens: number
  completion_tokens: number
}
export type StaffOrderType = 'topup' | 'voucher' | 'consumption'
export type StaffOrder = {
  trade_no: string
  order_type: StaffOrderType
  status: string
  face_usd: string
  created_at: number
  completed_at: number
}
type Response<T> = { success: boolean; message?: string; data: T }
export const STAFF_PAGE_SIZE = 20

export async function getStaffBalanceRequests(): Promise<
  StaffBalanceRequest[]
> {
  const response = await api.get<Response<{ items?: StaffBalanceRequest[] }>>(
    '/api/adty/staff/balance-requests'
  )
  return requireServerSuccess(response.data).data.items ?? []
}
export async function createStaffBalanceRequest(input: {
  customer_id: number
  direction: StaffBalanceDirection
  amount_usd: string
  idempotency_key: string
  reason: string
  order_ref: string
  funding_source: string
  batch_kind: string
  batch_source: string
}): Promise<StaffBalanceRequest> {
  const response = await api.post<Response<StaffBalanceRequest>>(
    '/api/adty/staff/balance-requests',
    input
  )
  return requireServerSuccess(response.data).data
}
export async function getOwnerBalanceRequests(p: number, status: string) {
  const response = await api.get<
    Response<{ items: StaffBalanceRequest[]; total: number }>
  >('/api/adty/site/balance-requests', {
    params: { p, status, page_size: STAFF_PAGE_SIZE },
  })
  return requireServerSuccess(response.data).data
}
export async function reviewOwnerBalanceRequest(
  id: number,
  approve: boolean
): Promise<void> {
  const response = await api.post<Response<{ reviewed: boolean }>>(
    `/api/adty/site/balance-requests/${id}/review`,
    { approve }
  )
  requireServerSuccess(response.data)
}
export async function getStaffAccounts(
  page: number
): Promise<OwnerPage<StaffAccount>> {
  const response = await api.get<Response<OwnerPage<StaffAccount>>>(
    '/api/adty/site/staff',
    { params: { p: page, page_size: STAFF_PAGE_SIZE } }
  )
  return requireServerSuccess(response.data).data
}
export async function getStaffAudits(
  page: number
): Promise<OwnerPage<StaffAudit>> {
  const response = await api.get<Response<OwnerPage<StaffAudit>>>(
    '/api/adty/site/staff/audits',
    { params: { p: page, page_size: STAFF_PAGE_SIZE } }
  )
  return requireServerSuccess(response.data).data
}
export async function saveStaffAccount(
  id: number | null,
  input: StaffInput,
  proof: string
): Promise<StaffAccount> {
  const path = `/api/adty/site/staff${id === null ? '' : `/${id}`}`
  const config = { headers: { 'X-Security-Proof': proof } }
  const response =
    id === null
      ? await api.post<Response<StaffAccount>>(path, input, config)
      : await api.put<Response<StaffAccount>>(path, input, config)
  return requireServerSuccess(response.data).data
}
export async function submitStaffRequest(action: string, target: number, input: StaffInput, proof: string) {
  if (action === 'staff.create' || action === 'staff.update') {
    return saveStaffAccount(action === 'staff.create' ? null : target,input,proof)
  }
  const headers={ 'X-Security-Proof': proof }
  const path=`/api/adty/site/staff/${target}`
  const response=action==='staff.password'
    ? await api.put<Response<StaffAccount>>(`${path}/password`,input,{headers})
    : await api.delete<Response<StaffAccount>>(path,{data:input,headers})
  return requireServerSuccess(response.data).data
}
export async function getStaffSelf(): Promise<{
  staff: StaffAccount
  site_name: string
}> {
  const response = await api.get<
    Response<{ staff: StaffAccount; site_name: string }>
  >('/api/adty/staff/self')
  return requireServerSuccess(response.data).data
}
export async function getStaffCustomers(
  page: number
): Promise<OwnerPage<OwnerCustomer>> {
  const response = await api.get<Response<OwnerPage<OwnerCustomer>>>(
    '/api/adty/staff/customers',
    { params: { p: page, page_size: STAFF_PAGE_SIZE } }
  )
  return requireServerSuccess(response.data).data
}
export async function manageStaffCustomer(
  customerId: number,
  action: StaffCustomerAction,
  proof: string
): Promise<void> {
  const response = await api.post<Response<null>>(
    `/api/adty/staff/customers/${customerId}/${action}`,
    {},
    { headers: { 'X-Security-Proof': proof } }
  )
  requireServerSuccess(response.data)
}
export async function getStaffCustomerRecords(
  kind: 'topups',
  customerId: number,
  page: number
): Promise<OwnerPage<OwnerTopUp>>
export async function getStaffCustomerRecords(
  kind: 'logs',
  customerId: number,
  page: number
): Promise<OwnerPage<StaffUsage>>
export async function getStaffCustomerRecords(
  kind: 'topups' | 'logs',
  customerId: number,
  page: number
): Promise<OwnerPage<OwnerTopUp> | OwnerPage<StaffUsage>> {
  const response = await api.get<
    Response<OwnerPage<OwnerTopUp> | OwnerPage<StaffUsage>>
  >(`/api/adty/staff/customers/${customerId}/${kind}`, {
    params: { p: page, page_size: STAFF_PAGE_SIZE },
  })
  return requireServerSuccess(response.data).data
}

export async function getStaffCustomerOrders(
  customerId: number,
  orderType: StaffOrderType,
  page: number
): Promise<OwnerPage<StaffOrder>> {
  const response = await api.get<Response<OwnerPage<StaffOrder>>>(
    `/api/adty/staff/customers/${customerId}/orders`,
    { params: { order_type: orderType, p: page, page_size: STAFF_PAGE_SIZE } }
  )
  return requireServerSuccess(response.data).data
}

export async function getStaffCustomerOrder(
  customerId: number,
  orderType: StaffOrderType,
  tradeNo: string
): Promise<StaffOrder> {
  const response = await api.get<Response<{ order: StaffOrder }>>(
    `/api/adty/staff/customers/${customerId}/orders/${encodeURIComponent(tradeNo)}`,
    { params: { order_type: orderType } }
  )
  return requireServerSuccess(response.data).data.order
}
