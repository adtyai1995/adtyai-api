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

type ApiResponse<T> = {
  success: boolean
  message?: string
  code?: string
  data: T
}

export type PartnerContactChannel = 'telegram' | 'wechat' | 'email' | 'other'

export interface PartnerPublicConfig {
  enabled: boolean
  default_rate_percent: number
  turnstile_check: boolean
  turnstile_site_key: string
  has_ref_cookie: boolean
}

export interface PartnerLeadPayload {
  contact_name: string
  contact_channel: PartnerContactChannel
  contact_value: string
  estimated_volume: string
  note: string
  referral_code: string
}

export interface PartnerLeadResult {
  id: number
  contact_telegram: string
}

export const partnerQueryKeys = {
  publicConfig: ['adty-partner', 'public'] as const,
}

export async function getPartnerPublicConfig(): Promise<PartnerPublicConfig> {
  const res = await api.get<ApiResponse<PartnerPublicConfig>>(
    '/api/adty/partner/public'
  )
  return requireServerSuccess(res.data).data
}

export async function recordPartnerClick(code: string): Promise<void> {
  const res = await api.post<ApiResponse<{ ok?: boolean }>>(
    '/api/adty/partner/click',
    { code },
    { skipErrorHandler: true }
  )
  requireServerSuccess(res.data)
}

export async function submitPartnerLead(
  payload: PartnerLeadPayload,
  turnstileToken?: string
): Promise<PartnerLeadResult> {
  const trimmedToken = turnstileToken?.trim() ?? ''
  const url = trimmedToken
    ? `/api/adty/partner/apply?turnstile=${encodeURIComponent(trimmedToken)}`
    : '/api/adty/partner/apply'
  const res = await api.post<ApiResponse<PartnerLeadResult>>(url, payload)
  return requireServerSuccess(res.data).data
}
