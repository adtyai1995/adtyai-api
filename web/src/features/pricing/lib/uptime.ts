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
import type { StatusVariant } from '@/components/status-badge'
import type {
  UptimeGroupResult,
  UptimeHeartbeat,
} from '@/features/dashboard/types'

import { FILTER_ALL } from '../constants'
import type { PricingModel } from '../types'

export type ConnectivityState =
  | 'online'
  | 'degraded'
  | 'offline'
  | 'maintenance'
  | 'unknown'

export interface ResolvedModelUptime {
  model: string
  groupId?: string
  categoryName: string
  monitorName?: string
  status: number
  state: ConnectivityState
  uptime: number | null
  avgPingMs?: number | null
  lastCheckedAt?: string | number | null
  lastMessage?: string | null
  heartbeats: UptimeHeartbeat[]
}

const DEGRADED_UPTIME_THRESHOLD = 0.95

export function classifyUptimeState(
  status: number,
  uptime: number | null | undefined
): ConnectivityState {
  if (status === 0) return 'offline'
  if (status === 2) return 'degraded'
  if (status === 3) return 'maintenance'
  if (status === 1) {
    if (
      uptime != null &&
      Number.isFinite(uptime) &&
      uptime < DEGRADED_UPTIME_THRESHOLD
    ) {
      return 'degraded'
    }
    return 'online'
  }
  return 'unknown'
}

export function formatUptimePercentage(
  uptime: number | null | undefined
): string {
  if (uptime == null || !Number.isFinite(uptime)) return '—'
  const clamped = Math.min(1, Math.max(0, uptime))
  return `${(clamped * 100).toFixed(2)}%`
}

export function computeAveragePing(
  heartbeats: UptimeHeartbeat[]
): number | null {
  let sum = 0
  let count = 0
  for (const hb of heartbeats) {
    if (hb.ping != null && Number.isFinite(hb.ping) && hb.ping >= 0) {
      sum += hb.ping
      count++
    }
  }
  if (count === 0) return null
  return Math.round(sum / count)
}

export function resolveModelPingMs(
  item: Pick<ResolvedModelUptime, 'avgPingMs' | 'heartbeats'>
): number | null {
  if (
    item.avgPingMs != null &&
    Number.isFinite(item.avgPingMs) &&
    item.avgPingMs >= 0
  ) {
    return Math.round(item.avgPingMs)
  }
  return computeAveragePing(item.heartbeats)
}

export function getConnectivityStatusLabelKey(
  state: ConnectivityState
): string {
  switch (state) {
    case 'online':
      return 'Online'
    case 'degraded':
      return 'Degraded'
    case 'offline':
      return 'Offline'
    case 'maintenance':
      return 'Maintenance'
    default:
      return 'No data'
  }
}

export function getConnectivityBadgeVariant(
  state: ConnectivityState
): StatusVariant {
  switch (state) {
    case 'online':
      return 'success'
    case 'degraded':
      return 'warning'
    case 'offline':
      return 'danger'
    case 'maintenance':
      return 'info'
    default:
      return 'neutral'
  }
}

export function getConnectivityTextColorClass(
  state: ConnectivityState
): string {
  switch (state) {
    case 'online':
      return 'text-success'
    case 'degraded':
      return 'text-warning'
    case 'offline':
      return 'text-destructive'
    case 'maintenance':
      return 'text-info'
    default:
      return 'text-muted-foreground'
  }
}

export function getHeartbeatBarColorClass(status: number): string {
  if (status === 1) return 'bg-success'
  if (status === 0) return 'bg-destructive'
  if (status === 2) return 'bg-warning'
  if (status === 3) return 'bg-info'
  return 'bg-muted-foreground/30'
}

export function getHeartbeatStatusLabelKey(status: number): string {
  if (status === 1) return 'Online'
  if (status === 0) return 'Offline'
  if (status === 2) return 'Degraded'
  if (status === 3) return 'Maintenance'
  return 'No data'
}

export function extractGroupModelStatuses(
  group: UptimeGroupResult
): ResolvedModelUptime[] {
  if (group.models && group.models.length > 0) {
    return group.models.map((item) => ({
      model: item.model,
      groupId: group.groupId,
      categoryName: group.categoryName,
      monitorName: item.monitorName,
      status: item.status,
      state: classifyUptimeState(item.status, item.uptime),
      uptime: item.uptime,
      avgPingMs: item.avgPingMs,
      lastCheckedAt: item.lastCheckedAt,
      lastMessage: item.lastMessage,
      heartbeats: item.heartbeats ?? [],
    }))
  }
  return (group.monitors ?? []).map((monitor) => {
    const uptime = Number.isFinite(monitor.uptime) ? monitor.uptime : null
    return {
      model: monitor.name,
      groupId: group.groupId,
      categoryName: group.categoryName,
      monitorName: monitor.name,
      status: monitor.status,
      state: classifyUptimeState(monitor.status, uptime),
      uptime,
      heartbeats: monitor.heartbeats ?? [],
    }
  })
}

function findModelInGroup(
  group: UptimeGroupResult,
  modelName: string
): ResolvedModelUptime | null {
  const lowerTarget = modelName.trim().toLowerCase()
  if (!lowerTarget) return null

  const items = extractGroupModelStatuses(group)
  const exact = items.find((item) => item.model === modelName)
  if (exact) return exact
  const caseInsensitive = items.find(
    (item) => item.model.trim().toLowerCase() === lowerTarget
  )
  return caseInsensitive ?? null
}

function groupMatchesId(
  group: UptimeGroupResult,
  targetGroup: string
): boolean {
  if (group.groupId && group.groupId === targetGroup) return true
  if (
    !group.groupId &&
    group.categoryName.trim().toLowerCase() === targetGroup.trim().toLowerCase()
  ) {
    return true
  }
  return false
}

function statePriority(state: ConnectivityState): number {
  switch (state) {
    case 'online':
      return 4
    case 'degraded':
      return 3
    case 'maintenance':
      return 2
    case 'offline':
      return 1
    default:
      return 0
  }
}

export function resolveAllGroupUptimesForModel(
  uptimeGroups: UptimeGroupResult[],
  model: PricingModel
): ResolvedModelUptime[] {
  if (!uptimeGroups.length || !model.model_name) return []
  const enabledSet = new Set(model.enable_groups ?? [])
  const matched: ResolvedModelUptime[] = []

  for (const group of uptimeGroups) {
    if (
      group.groupId &&
      enabledSet.size > 0 &&
      !enabledSet.has(group.groupId)
    ) {
      continue
    }
    const found = findModelInGroup(group, model.model_name)
    if (found) matched.push(found)
  }

  return matched
}

export function resolveModelUptimeForPricing(
  uptimeGroups: UptimeGroupResult[],
  model: PricingModel,
  selectedGroup?: string
): ResolvedModelUptime | null {
  if (!uptimeGroups.length || !model.model_name) return null

  if (selectedGroup && selectedGroup !== FILTER_ALL) {
    for (const group of uptimeGroups) {
      if (!groupMatchesId(group, selectedGroup)) continue
      const found = findModelInGroup(group, model.model_name)
      if (found) return found
    }
    return null
  }

  const allMatches = resolveAllGroupUptimesForModel(uptimeGroups, model)
  if (allMatches.length === 0) return null
  if (allMatches.length === 1) return allMatches[0]

  return [...allMatches].sort((a, b) => {
    const priorityDiff = statePriority(b.state) - statePriority(a.state)
    if (priorityDiff !== 0) return priorityDiff
    return (b.uptime ?? -1) - (a.uptime ?? -1)
  })[0]
}
