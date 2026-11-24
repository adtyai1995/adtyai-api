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

import type {
  FlowQuotaDataItem,
  QuotaDataItem,
  UptimeGroupResult,
  UptimeHeartbeat,
  UptimeKumaGroupConfig,
  UptimeModelStatus,
  UptimePlatform,
  UptimeProbeRequest,
  UptimeProbeResponseData,
  UptimeProbeType,
  UptimeStatusLabel,
} from './types'

export type {
  UptimeGroupResult,
  UptimeHeartbeat,
  UptimeKumaGroupConfig,
  UptimeModelStatus,
  UptimePlatform,
  UptimeProbeRequest,
  UptimeProbeResponseData,
  UptimeProbeType,
  UptimeStatusLabel,
}

// ============================================================================
// Dashboard APIs
// ============================================================================

// ----------------------------------------------------------------------------
// Quota & Usage Data
// ----------------------------------------------------------------------------

// Get user quota data within a time range
// Admin users get all users' data by default.
export async function getUserQuotaDates(
  params: {
    start_timestamp: number
    end_timestamp: number
    default_time?: string
    username?: string
  },
  isAdmin = false
) {
  const endpoint = isAdmin ? '/api/data' : '/api/data/self'
  const res = await api.get<{
    success: boolean
    data?: QuotaDataItem[]
    message?: string
    message_key?: string
    message_params?: Record<string, unknown>
  }>(endpoint, { params })
  return res.data
}

// ----------------------------------------------------------------------------
// System Monitoring
// ----------------------------------------------------------------------------

export async function getUserQuotaDataByUsers(params: {
  start_timestamp: number
  end_timestamp: number
}) {
  const res = await api.get<{
    success: boolean
    data?: QuotaDataItem[]
    message?: string
    message_key?: string
    message_params?: Record<string, unknown>
  }>('/api/data/users', { params })
  return res.data
}

export async function getFlowQuotaDates(
  params: {
    start_timestamp: number
    end_timestamp: number
    default_time?: string
    username?: string
  },
  isAdmin = false
) {
  const endpoint = isAdmin ? '/api/data/flow' : '/api/data/flow/self'
  const res = await api.get<{
    success: boolean
    data?: FlowQuotaDataItem[]
    message?: string
  }>(endpoint, { params })
  return res.data
}

export const UPTIME_STATUS_QUERY_KEY = ['uptime', 'status'] as const

// Get uptime monitoring status for all services
export async function getUptimeStatus() {
  const res = await api.get<{
    success: boolean
    data?: UptimeGroupResult[]
    message?: string
    message_key?: string
    message_params?: Record<string, unknown>
  }>('/api/uptime/status')
  return res.data
}

// Trigger an immediate built-in connectivity probe (admin only)
export async function triggerUptimeProbe(params?: UptimeProbeRequest) {
  const body: UptimeProbeRequest = {}
  if (params?.group_id?.trim()) {
    body.group_id = params.group_id.trim()
  }
  if (params?.model?.trim()) {
    body.model = params.model.trim()
  }
  const res = await api.post<{
    success: boolean
    data?: UptimeProbeResponseData
    message?: string
    message_key?: string
    message_params?: Record<string, unknown>
  }>('/api/uptime/probe', body)
  return res.data
}
