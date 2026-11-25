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
  UptimeGroupResult,
  UptimeHeartbeat,
  UptimePlatform,
  UptimeProbeType,
  UptimeStatusLabel,
} from '@/features/dashboard/types'
import dayjs from '@/lib/dayjs'

export type ChannelPlatformFilter = 'all' | UptimePlatform

export type ChannelSortKey = 'ratio' | 'uptime' | 'avgPingMs' | 'status'

export type ChannelSortDirection = 'asc' | 'desc'

export type AggregateHealthState =
  | 'operational'
  | 'degraded'
  | 'disrupted'
  | 'unknown'

export interface ChannelPlatformMeta {
  id: UptimePlatform
  labelKey: string
  lobeIcon: string | null
}

const FALLBACK_PLATFORM_META: ChannelPlatformMeta = {
  id: 'other',
  labelKey: 'Other',
  lobeIcon: null,
}

export const CHANNEL_PLATFORMS: readonly ChannelPlatformMeta[] = [
  { id: 'anthropic', labelKey: 'Anthropic', lobeIcon: 'Claude.Color' },
  { id: 'openai', labelKey: 'OpenAI', lobeIcon: 'OpenAI' },
  { id: 'gemini', labelKey: 'Gemini', lobeIcon: 'Gemini.Color' },
  { id: 'zhipu', labelKey: 'Zhipu GLM', lobeIcon: 'Zhipu.Color' },
  { id: 'deepseek', labelKey: 'DeepSeek', lobeIcon: 'DeepSeek.Color' },
  { id: 'moonshot', labelKey: 'Moonshot', lobeIcon: 'Moonshot' },
  { id: 'xai', labelKey: 'xAI', lobeIcon: 'XAI' },
  { id: 'azure', labelKey: 'Azure', lobeIcon: 'Azure.Color' },
  FALLBACK_PLATFORM_META,
] as const

const PLATFORM_META_BY_ID = new Map<UptimePlatform, ChannelPlatformMeta>(
  CHANNEL_PLATFORMS.map((platform) => [platform.id, platform])
)

export const AUTO_REFRESH_INTERVAL_OPTIONS = [30, 60, 120, 0] as const

export type AutoRefreshInterval = (typeof AUTO_REFRESH_INTERVAL_OPTIONS)[number]

export const DEFAULT_SORT_DIRECTIONS: Record<
  ChannelSortKey,
  ChannelSortDirection
> = {
  ratio: 'asc',
  uptime: 'desc',
  avgPingMs: 'asc',
  status: 'asc',
}

export interface ChannelStatusCardItem {
  key: string
  index: number
  groupId?: string
  displayName: string
  description?: string
  platform: UptimePlatform
  probeType: UptimeProbeType
  probeModel: string
  groupRatio: number | null
  status: UptimeStatusLabel
  uptime: number | null
  avgPingMs: number | null
  lastCheckedAt: string | number | null
  heartbeats: UptimeHeartbeat[]
}

export function isKnownPlatform(value: unknown): value is UptimePlatform {
  return (
    typeof value === 'string' &&
    PLATFORM_META_BY_ID.has(value as UptimePlatform)
  )
}

export function getPlatformMeta(platform: UptimePlatform): ChannelPlatformMeta {
  return PLATFORM_META_BY_ID.get(platform) ?? FALLBACK_PLATFORM_META
}

export function inferChannelPlatform(
  probeModel: string,
  categoryName: string,
  groupId?: string
): UptimePlatform {
  const haystack =
    `${probeModel} ${categoryName} ${groupId ?? ''}`.toLowerCase()
  if (haystack.includes('azure')) return 'azure'
  if (haystack.includes('claude') || haystack.includes('anthropic')) {
    return 'anthropic'
  }
  if (
    haystack.includes('gemini') ||
    haystack.includes('imagen') ||
    haystack.includes('veo')
  ) {
    return 'gemini'
  }
  if (
    haystack.includes('glm') ||
    haystack.includes('zhipu') ||
    haystack.includes('cogview') ||
    haystack.includes('智谱')
  ) {
    return 'zhipu'
  }
  if (haystack.includes('deepseek')) return 'deepseek'
  if (haystack.includes('moonshot') || haystack.includes('kimi')) {
    return 'moonshot'
  }
  if (haystack.includes('grok') || /\bxai\b/.test(haystack)) return 'xai'
  if (
    haystack.includes('gpt') ||
    haystack.includes('openai') ||
    haystack.includes('chatgpt') ||
    haystack.includes('dall-e') ||
    haystack.includes('sora') ||
    /\bo[134](-|$)/.test(haystack)
  ) {
    return 'openai'
  }
  return 'other'
}

function computeAvgPingFromHeartbeats(
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
  return sum / count
}

function resolveStatusLabel(
  statusLabel: UptimeStatusLabel | undefined,
  statusCode: number | undefined,
  uptime: number | null,
  hasSamples: boolean
): UptimeStatusLabel {
  if (
    statusLabel === 'up' ||
    statusLabel === 'degraded' ||
    statusLabel === 'down' ||
    statusLabel === 'unknown'
  ) {
    if (statusLabel === 'up' && !hasSamples && uptime === null) {
      return 'unknown'
    }
    return statusLabel
  }
  if (statusCode === 0) return 'down'
  if (statusCode === 2) return 'degraded'
  if (statusCode === 1) {
    if (!hasSamples && uptime === null) return 'unknown'
    if (uptime !== null && uptime < 0.95) return 'degraded'
    return 'up'
  }
  return 'unknown'
}

function normalizeFiniteNumber(
  value: number | null | undefined
): number | null {
  if (value == null || !Number.isFinite(value)) return null
  return value
}

export function resolveChannelCard(
  group: UptimeGroupResult,
  index: number,
  resolveGroupName: (id: string) => string
): ChannelStatusCardItem {
  const primaryModel = group.models?.[0]
  const primaryMonitor = group.monitors?.[0]

  const groupId = group.groupId?.trim() || undefined
  const localizedName = groupId ? resolveGroupName(groupId) : ''
  let displayName =
    group.categoryName?.trim() || groupId || `Group ${index + 1}`
  if (groupId && localizedName && localizedName !== groupId) {
    displayName = localizedName
  }

  const probeModel =
    group.probeModel?.trim() ||
    primaryModel?.model?.trim() ||
    primaryMonitor?.name?.trim() ||
    ''

  const platform: UptimePlatform = isKnownPlatform(group.platform)
    ? group.platform
    : inferChannelPlatform(probeModel, displayName, groupId)

  const probeType: UptimeProbeType =
    group.probeType === 'image' ? 'image' : 'text'

  const rawRatio = normalizeFiniteNumber(group.groupRatio)
  const groupRatio = rawRatio !== null && rawRatio >= 0 ? rawRatio : null

  const heartbeats =
    group.heartbeats ??
    primaryModel?.heartbeats ??
    primaryMonitor?.heartbeats ??
    []

  const statusCode =
    group.status ?? primaryModel?.status ?? primaryMonitor?.status
  const hasSamples = heartbeats.length > 0

  const explicitUptime = normalizeFiniteNumber(
    group.uptime ?? primaryModel?.uptime
  )
  const fallbackMonitorUptime =
    explicitUptime === null &&
    hasSamples &&
    statusCode !== -1 &&
    primaryMonitor?.status !== -1 &&
    group.statusLabel !== 'unknown'
      ? normalizeFiniteNumber(primaryMonitor?.uptime)
      : null

  const rawUptime =
    !hasSamples && (statusCode === -1 || group.statusLabel === 'unknown')
      ? null
      : (explicitUptime ?? fallbackMonitorUptime)
  const uptime = rawUptime !== null ? Math.min(1, Math.max(0, rawUptime)) : null

  const rawPing = normalizeFiniteNumber(
    group.avgPingMs ?? primaryModel?.avgPingMs
  )
  const avgPingMs =
    rawPing !== null && rawPing >= 0
      ? rawPing
      : computeAvgPingFromHeartbeats(heartbeats)

  const lastCheckedAt =
    group.lastCheckedAt ??
    primaryModel?.lastCheckedAt ??
    heartbeats.at(-1)?.time ??
    null

  const status = resolveStatusLabel(
    group.statusLabel,
    statusCode,
    uptime,
    hasSamples
  )

  return {
    key: groupId || group.categoryName || `group-${index}`,
    index,
    groupId,
    displayName,
    description: group.description?.trim() || undefined,
    platform,
    probeType,
    probeModel,
    groupRatio,
    status,
    uptime,
    avgPingMs,
    lastCheckedAt,
    heartbeats,
  }
}

export function computeAggregateHealth(
  items: ChannelStatusCardItem[]
): AggregateHealthState {
  if (items.length === 0) return 'unknown'
  let hasDown = false
  let hasDegraded = false
  let hasUp = false
  for (const item of items) {
    if (item.status === 'down') hasDown = true
    else if (item.status === 'degraded') hasDegraded = true
    else if (item.status === 'up') hasUp = true
  }
  if (hasDown) return 'disrupted'
  if (hasDegraded) return 'degraded'
  if (hasUp) return 'operational'
  return 'unknown'
}

export function formatLatency(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return '—'
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)}s`
  }
  return `${Math.round(ms)}ms`
}

export function formatSuccessRate(uptime: number | null | undefined): string {
  if (uptime == null || !Number.isFinite(uptime)) return '—'
  const clamped = Math.min(1, Math.max(0, uptime))
  return `${(clamped * 100).toFixed(2)}%`
}

export function formatGroupRatio(ratio: number): string {
  return String(Number(ratio.toFixed(6)))
}

export function formatCheckedTimestamp(
  value: string | number | null | undefined
): string {
  if (value == null || value === '') return '—'
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || value <= 0) return '—'
    const ms = value > 1e12 ? value : value * 1000
    const parsed = dayjs(ms)
    return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm:ss') : '—'
  }
  const trimmed = value.trim()
  if (!trimmed) return '—'
  const parsed = dayjs(trimmed)
  if (!parsed.isValid()) return trimmed
  return parsed.format('YYYY-MM-DD HH:mm:ss')
}

export function getSuccessRateToneClass(
  uptime: number | null | undefined
): string {
  if (uptime == null || !Number.isFinite(uptime)) {
    return 'text-muted-foreground'
  }
  if (uptime >= 0.95) return 'text-success'
  if (uptime >= 0.8) return 'text-warning'
  return 'text-destructive'
}

export function getChannelStatusMeta(status: UptimeStatusLabel): {
  labelKey: string
  dotClass: string
  textClass: string
} {
  switch (status) {
    case 'up':
      return {
        labelKey: 'Healthy',
        dotClass: 'bg-success',
        textClass: 'text-success',
      }
    case 'degraded':
      return {
        labelKey: 'Channel degraded',
        dotClass: 'bg-warning',
        textClass: 'text-warning',
      }
    case 'down':
      return {
        labelKey: 'Abnormal',
        dotClass: 'bg-destructive',
        textClass: 'text-destructive',
      }
    default:
      return {
        labelKey: 'Unknown',
        dotClass: 'bg-muted-foreground/50',
        textClass: 'text-muted-foreground',
      }
  }
}

function getStatusSortRank(
  status: UptimeStatusLabel,
  direction: ChannelSortDirection
): number {
  if (direction === 'asc') {
    // Healthy first
    switch (status) {
      case 'up':
        return 0
      case 'degraded':
        return 1
      case 'down':
        return 2
      default:
        return 3
    }
  }
  // Issues first
  switch (status) {
    case 'down':
      return 0
    case 'degraded':
      return 1
    case 'unknown':
      return 2
    default:
      return 3
  }
}

function compareNullableNumbers(
  a: number | null,
  b: number | null,
  direction: ChannelSortDirection
): number {
  if (a === null && b === null) return 0
  if (a === null) return 1
  if (b === null) return -1
  return direction === 'asc' ? a - b : b - a
}

export function sortChannelCards(
  items: ChannelStatusCardItem[],
  sortKey: ChannelSortKey,
  direction: ChannelSortDirection
): ChannelStatusCardItem[] {
  return [...items].sort((a, b) => {
    let diff = 0
    switch (sortKey) {
      case 'ratio':
        diff = compareNullableNumbers(a.groupRatio, b.groupRatio, direction)
        break
      case 'uptime':
        diff = compareNullableNumbers(a.uptime, b.uptime, direction)
        break
      case 'avgPingMs':
        diff = compareNullableNumbers(a.avgPingMs, b.avgPingMs, direction)
        break
      case 'status':
        diff =
          getStatusSortRank(a.status, direction) -
          getStatusSortRank(b.status, direction)
        break
    }
    if (diff !== 0) return diff
    return a.index - b.index
  })
}
