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
import { Activity, RotateCw } from 'lucide-react'
import { memo, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useGroupDisplay } from '@/features/adty-groups/hooks/use-group-display'
import {
  getUptimeStatus,
  UPTIME_STATUS_QUERY_KEY,
} from '@/features/dashboard/api'
import type {
  UptimeGroupResult,
  UptimeHeartbeat,
} from '@/features/dashboard/types'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { PanelWrapper } from '../ui/panel-wrapper'

const STATUS_COLOR_MAP: Record<number, string> = {
  1: 'bg-success',
  0: 'bg-destructive',
  2: 'bg-warning',
  3: 'bg-info',
}
const DEFAULT_STATUS_COLOR = 'bg-muted-foreground/40'

const StatusDot = memo(function StatusDot(props: { status: number }) {
  const color = STATUS_COLOR_MAP[props.status] ?? DEFAULT_STATUS_COLOR
  return (
    <span
      className={cn('inline-block size-2 shrink-0 rounded-full', color)}
      aria-hidden='true'
    />
  )
})

const MiniHeartbeatStrip = memo(function MiniHeartbeatStrip(props: {
  heartbeats: UptimeHeartbeat[]
}) {
  const items = useMemo(() => {
    const recent = props.heartbeats.slice(-12)
    const seen = new Map<string, number>()
    return recent.map((hb) => {
      const base = `${hb.time || 't'}-${hb.status}-${hb.ping ?? 'n'}`
      const count = seen.get(base) ?? 0
      seen.set(base, count + 1)
      return { key: count === 0 ? base : `${base}#${count}`, hb }
    })
  }, [props.heartbeats])

  if (items.length === 0) return null
  return (
    <span
      className='hidden items-center gap-px sm:inline-flex'
      aria-hidden='true'
    >
      {items.map(({ key, hb }) => (
        <span
          key={key}
          className={cn(
            'h-3 w-[3px] rounded-xs',
            STATUS_COLOR_MAP[hb.status] ?? DEFAULT_STATUS_COLOR
          )}
        />
      ))}
    </span>
  )
})

interface UptimePanelRow {
  key: string
  name: string
  status: number
  uptime: number | null
  pingMs: number | null
  lastMessage?: string | null
  heartbeats: UptimeHeartbeat[]
  group?: string
}

function resolveRowPingMs(
  avgPingMs: number | null | undefined,
  heartbeats: UptimeHeartbeat[]
): number | null {
  if (avgPingMs != null && Number.isFinite(avgPingMs) && avgPingMs >= 0) {
    return Math.round(avgPingMs)
  }
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

function formatPingLabel(pingMs: number, msLabel: string): string {
  if (pingMs >= 1000) {
    return `${(pingMs / 1000).toFixed(2)}s`
  }
  return `${pingMs} ${msLabel}`
}

function getGroupRows(group: UptimeGroupResult): UptimePanelRow[] {
  if (group.models && group.models.length > 0) {
    return group.models.map((item) => {
      const heartbeats = item.heartbeats ?? group.heartbeats ?? []
      return {
        key: `model:${item.model}`,
        name: item.model,
        status: item.status,
        uptime: item.uptime,
        pingMs: resolveRowPingMs(item.avgPingMs ?? group.avgPingMs, heartbeats),
        lastMessage: item.lastMessage,
        heartbeats,
      }
    })
  }
  if (group.monitors && group.monitors.length > 0) {
    return group.monitors.map((monitor) => {
      const heartbeats = monitor.heartbeats ?? group.heartbeats ?? []
      return {
        key: `monitor:${monitor.name}`,
        name: monitor.name,
        status: monitor.status,
        uptime: Number.isFinite(monitor.uptime) ? monitor.uptime : null,
        pingMs: resolveRowPingMs(group.avgPingMs, heartbeats),
        heartbeats,
        group: monitor.group,
      }
    })
  }
  if (
    group.probeModel ||
    group.status !== undefined ||
    group.uptime !== undefined ||
    (group.heartbeats && group.heartbeats.length > 0)
  ) {
    const heartbeats = group.heartbeats ?? []
    const modelName =
      group.probeModel?.trim() || group.categoryName || group.groupId || '—'
    const status = group.status ?? -1
    const uptime =
      group.uptime != null && Number.isFinite(group.uptime)
        ? group.uptime
        : null
    return [
      {
        key: `group:${group.groupId || group.categoryName || modelName}`,
        name: modelName,
        status,
        uptime,
        pingMs: resolveRowPingMs(group.avgPingMs, heartbeats),
        heartbeats,
      },
    ]
  }
  return []
}

export function UptimePanel() {
  const { t } = useTranslation()
  const groupDisplay = useGroupDisplay()

  const uptimeQuery = useQuery({
    queryKey: UPTIME_STATUS_QUERY_KEY,
    queryFn: async () => {
      const res = requireServerSuccess(await getUptimeStatus())
      return res.data ?? []
    },
    staleTime: 30 * 1000,
    retry: false,
    meta: { errorToast: false },
  })

  const groups = useMemo(() => uptimeQuery.data ?? [], [uptimeQuery.data])
  const errorMessage = uptimeQuery.isError
    ? getServerErrorMessage(
        uptimeQuery.error,
        t('Failed to load uptime status')
      )
    : null

  const handleRefresh = () => {
    void uptimeQuery.refetch()
  }

  return (
    <PanelWrapper
      title={
        <span className='flex items-center gap-2'>
          <IconBadge tone='success' size='sm'>
            <Activity />
          </IconBadge>
          {t('Uptime')}
        </span>
      }
      description={t('24h model connectivity status')}
      loading={uptimeQuery.isLoading}
      error={errorMessage}
      onRetry={handleRefresh}
      empty={!groups.length}
      emptyMessage={t('No model connectivity monitoring configured')}
      height='h-80'
      contentClassName='p-0'
      headerActions={
        <Button
          variant='ghost'
          size='sm'
          onClick={handleRefresh}
          disabled={uptimeQuery.isFetching}
          className='size-7 p-0'
          aria-label={t('Refresh')}
        >
          <RotateCw
            className={cn('size-3.5', uptimeQuery.isFetching && 'animate-spin')}
            aria-hidden='true'
          />
        </Button>
      }
    >
      <ScrollArea className='h-80'>
        <div>
          {groups.map((group, groupIdx) => {
            const rows = getGroupRows(group)
            const heading = group.groupId
              ? groupDisplay.name(group.groupId)
              : group.categoryName

            return (
              <div key={group.groupId || group.categoryName}>
                <div className='bg-canvas border-b px-3 py-2 sm:px-5'>
                  <div className='flex items-center gap-2'>
                    <h4 className='text-faint text-[11px] font-semibold tracking-[0.08em] uppercase'>
                      {heading}
                    </h4>
                    <span className='text-muted-foreground/40 font-mono text-xs tabular-nums'>
                      {rows.length}
                    </span>
                  </div>
                </div>

                {rows.map((row, rowIdx) => (
                  <div
                    key={row.key}
                    className={cn(
                      'hover:bg-muted/40 flex items-center justify-between gap-2 px-3 py-2 transition-colors sm:px-5 sm:py-2.5',
                      rowIdx < rows.length - 1 && 'border-border/40 border-b',
                      groupIdx < groups.length - 1 &&
                        rowIdx === rows.length - 1 &&
                        'border-border/60 border-b'
                    )}
                  >
                    <div
                      className='flex min-w-0 items-center gap-2.5'
                      title={row.lastMessage || undefined}
                    >
                      <StatusDot status={row.status} />
                      <span className='truncate font-mono text-xs sm:text-sm'>
                        {row.name}
                      </span>
                      {row.group && (
                        <span className='text-muted-foreground/40 shrink-0 text-xs'>
                          ({row.group})
                        </span>
                      )}
                    </div>
                    <div className='flex shrink-0 items-center gap-2.5'>
                      <MiniHeartbeatStrip heartbeats={row.heartbeats} />
                      {row.pingMs !== null && (
                        <span className='text-muted-foreground hidden w-16 text-right font-mono text-xs tabular-nums sm:inline'>
                          {formatPingLabel(row.pingMs, t('ms'))}
                        </span>
                      )}
                      <span className='text-foreground w-16 text-right font-mono text-xs font-semibold tabular-nums sm:text-sm'>
                        {row.uptime !== null
                          ? `${(row.uptime * 100).toFixed(2)}%`
                          : '—'}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            )
          })}
        </div>
      </ScrollArea>
    </PanelWrapper>
  )
}
