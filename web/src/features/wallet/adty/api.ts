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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  AdtyOrder,
  AdtyPage,
  AdtyQuote,
  AdtyTopupBusiness,
  AdtyTopupHistoryFilters,
  AdtyTopupInfo as BaseAdtyTopupInfo,
} from './types'

export type AdtyTopupInfo = BaseAdtyTopupInfo & {
  /** Active recharge bonus rates keyed by canonical method (USDT only: { usdt: "0.025" }). */
  method_bonus_rates?: Record<string, string>
}

/**
 * Formats a decimal fraction bonus rate (0..1, e.g. "0.05" -> "5", "0.005" -> "0.5")
 * into a clean percentage number string, or null when inactive.
 */
export function formatBonusRateFraction(raw?: string | number): string | null {
  if (raw === undefined || raw === null) return null
  const num = Number(String(raw).trim())
  if (!Number.isFinite(num) || num <= 0) return null
  const rounded = Number((num * 100).toFixed(2))
  return rounded > 0 ? String(rounded) : null
}

/**
 * Formats a percentage bonus rate (0..100, e.g. "0.5" -> "0.5", "1" -> "1", "2.5" -> "2.5")
 * into a clean percentage number string without scaling, or null when inactive.
 */
export function formatBonusRatePercent(raw?: string | number): string | null {
  if (raw === undefined || raw === null) return null
  const num = Number(String(raw).trim())
  if (!Number.isFinite(num) || num <= 0) return null
  const rounded = Number(num.toFixed(2))
  return rounded > 0 ? String(rounded) : null
}

/** Resolves the active recharge bonus percent for a USDT payment method id (maps usdt.* -> usdt). */
export function resolveMethodBonusPercent(
  methodId: string,
  methodBonusRates?: Record<string, string>
): string | null {
  if (!methodBonusRates) return null
  const normalized = methodId.toLowerCase().trim()
  if (normalized !== 'usdt' && !normalized.startsWith('usdt.')) {
    return null
  }
  const raw =
    methodBonusRates[methodId] ??
    methodBonusRates[normalized] ??
    methodBonusRates.usdt
  return formatBonusRateFraction(raw)
}

type ApiResponse<T> = { success: boolean; message?: string; data: T }

export const adtyTopupQueryKeys = {
  info: ['adty-topup', 'info'] as const,
  order: (tradeNo: string) => ['adty-topup', 'order', tradeNo] as const,
  history: (page: number, filters?: AdtyTopupHistoryFilters) =>
    filters && Object.values(filters).some((v) => v !== undefined && v !== '')
      ? (['adty-topup', 'history', page, filters] as const)
      : (['adty-topup', 'history', page] as const),
}

export async function getAdtyTopupInfo(
  business?: AdtyTopupBusiness,
  teamId?: number
): Promise<AdtyTopupInfo> {
  const res = await api.get<ApiResponse<AdtyTopupInfo>>(
    '/api/adty/topup/info',
    { params: { business, team_id: teamId } }
  )
  return requireServerSuccess(res.data).data
}

export async function quoteAdtyTopup(
  amount: number,
  method: string,
  paymentScene = 'wallet_topup',
  channelId?: number,
  business?: AdtyTopupBusiness,
  teamId?: number
): Promise<{ quote: AdtyQuote; available: boolean }> {
  const res = await api.post<
    ApiResponse<{ quote: AdtyQuote; available: boolean }>
  >('/api/adty/topup/quote', {
    amount,
    method,
    channel_id: channelId,
    payment_scene: paymentScene,
    business,
    team_id: teamId,
  })
  return requireServerSuccess(res.data).data
}

export async function getAdtyOrder(tradeNo: string): Promise<AdtyOrder> {
  const res = await api.get<ApiResponse<AdtyOrder>>(
    `/api/adty/topup/orders/${encodeURIComponent(tradeNo)}`
  )
  return requireServerSuccess(res.data).data
}

export async function cancelAdtyOrder(tradeNo: string): Promise<void> {
  const res = await api.post<ApiResponse<null>>(
    `/api/adty/topup/orders/${encodeURIComponent(tradeNo)}/cancel`
  )
  requireServerSuccess(res.data)
}

export async function getAdtyTopupHistory(
  page: number,
  pageSize: number,
  filters?: AdtyTopupHistoryFilters
): Promise<AdtyPage<AdtyOrder>> {
  const params: Record<string, string | number> = {
    p: page,
    page_size: pageSize,
  }
  if (filters?.status && filters.status !== 'all') {
    params.status = filters.status
  }
  if (filters?.business) params.business = filters.business
  if (filters?.payment_method && filters.payment_method !== 'all') {
    params.payment_method = filters.payment_method
  }
  if (filters?.trade_no && filters.trade_no.trim() !== '') {
    params.trade_no = filters.trade_no.trim()
  }
  if (
    typeof filters?.start_timestamp === 'number' &&
    Number.isFinite(filters.start_timestamp) &&
    filters.start_timestamp > 0
  ) {
    params.start_timestamp = filters.start_timestamp
  }
  if (
    typeof filters?.end_timestamp === 'number' &&
    Number.isFinite(filters.end_timestamp) &&
    filters.end_timestamp > 0
  ) {
    params.end_timestamp = filters.end_timestamp
  }
  const res = await api.get<ApiResponse<AdtyPage<AdtyOrder>>>(
    '/api/adty/topup/self',
    { params }
  )
  return requireServerSuccess(res.data).data
}
