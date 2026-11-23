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
import { useQuery } from '@tanstack/react-query'

import { useStatus } from '@/hooks/use-status'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { subsiteDocument } from '@/lib/subsite-document'

/** Public site settings from GET /api/adty/site (adty task 8.3). */
export type AdtySite = {
  api_base_url: string
  support_email: string
  support_contact?: string
  telegram_url: string
  support_hours: string
  tawk_property_id: string
  tawk_widget_id: string
  refund_policy_enabled: boolean
  /** Minimum top-up in USD (Top-up settings); missing on older servers. */
  min_topup?: number
}

export const adtySiteQueryKey = ['adty', 'site'] as const

export async function getAdtySite(): Promise<AdtySite> {
  const res = await api.get<{
    success: boolean
    message?: string
    data: AdtySite
  }>('/api/adty/site')
  return requireServerSuccess(res.data).data
}

/** Shared /api/adty/site query. Public pages read it, so failures stay silent. */
export function useAdtySite() {
  return useQuery({
    queryKey: adtySiteQueryKey,
    queryFn: getAdtySite,
    staleTime: 5 * 60 * 1000,
    meta: { errorToast: false },
  })
}

/**
 * The dedicated API calling address customers configure: never falls back to
 * the website/home domain origin because website domains reject relay calls.
 * No trailing slash.
 */
export function useApiBaseUrl(): string {
  const site = useAdtySite()
  const { status } = useStatus()
  const tenant = subsiteDocument()
  const serverAddress =
    typeof status?.server_address === 'string' ? status.server_address : ''
  const isSubsite = Boolean(status?.adty_site ?? status?.data?.adty_site)
  if (tenant || isSubsite) {
    return ((isSubsite && serverAddress.trim()) || tenant?.api || '').replace(
      /\/+$/,
      ''
    )
  }
  const candidates = [site.data?.api_base_url ?? '', serverAddress]
  const configured = candidates.map((value) => value.trim()).find(Boolean) ?? ''
  return configured.replace(/\/+$/, '')
}
