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
import { memo, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { UptimeHeartbeat } from '@/features/dashboard/types'
import { cn } from '@/lib/utils'

import {
  formatUptimePercentage,
  getConnectivityBadgeVariant,
  getConnectivityStatusLabelKey,
  getConnectivityTextColorClass,
  getHeartbeatBarColorClass,
  getHeartbeatStatusLabelKey,
  type ResolvedModelUptime,
} from '../lib/uptime'

export interface ModelHeartbeatBarsProps {
  heartbeats: UptimeHeartbeat[]
  maxBars?: number
  size?: 'sm' | 'md' | 'lg'
  fillWidth?: boolean
  className?: string
}

const SIZE_CLASSES: Record<
  'sm' | 'md' | 'lg',
  { container: string; bar: string; gap: string }
> = {
  sm: { container: 'h-3', bar: 'w-[3px] rounded-xs', gap: 'gap-px' },
  md: { container: 'h-4', bar: 'w-1 rounded-xs', gap: 'gap-[2px]' },
  lg: { container: 'h-6', bar: 'min-w-[4px] flex-1 rounded-sm', gap: 'gap-1' },
}

const EMPTY_SLOT_KEYS = [
  's01',
  's02',
  's03',
  's04',
  's05',
  's06',
  's07',
  's08',
  's09',
  's10',
  's11',
  's12',
  's13',
  's14',
  's15',
  's16',
  's17',
  's18',
  's19',
  's20',
  's21',
  's22',
  's23',
  's24',
] as const

export const ModelHeartbeatBars = memo(function ModelHeartbeatBars(
  props: ModelHeartbeatBarsProps
) {
  const { t } = useTranslation()
  const maxBars = props.maxBars ?? 24
  const size = props.size ?? 'sm'
  const sizeStyle = SIZE_CLASSES[size]
  const recent = useMemo(
    () => props.heartbeats.slice(-maxBars),
    [maxBars, props.heartbeats]
  )
  const keyedHeartbeats = useMemo(() => {
    const seen = new Map<string, number>()
    return recent.map((hb) => {
      const base = `${hb.time || 't'}-${hb.status}-${hb.ping ?? 'n'}`
      const count = seen.get(base) ?? 0
      seen.set(base, count + 1)
      return { key: count === 0 ? base : `${base}#${count}`, hb }
    })
  }, [recent])
  const padCount = props.fillWidth ? Math.max(0, maxBars - recent.length) : 0
  const emptyKeys = EMPTY_SLOT_KEYS.slice(0, padCount)

  if (recent.length === 0 && !props.fillWidth) {
    return null
  }

  return (
    <div
      role='img'
      aria-label={t('24h heartbeat history')}
      className={cn(
        'flex items-center',
        sizeStyle.container,
        sizeStyle.gap,
        props.fillWidth && 'w-full',
        props.className
      )}
    >
      {emptyKeys.map((slotKey) => (
        <span
          key={slotKey}
          aria-hidden='true'
          className={cn(
            'bg-muted-foreground/15 h-full shrink-0',
            sizeStyle.bar,
            props.fillWidth && 'min-w-0 flex-1 shrink'
          )}
        />
      ))}
      {keyedHeartbeats.map(({ key, hb }) => (
        <Tooltip key={key}>
          <TooltipTrigger
            render={
              <span
                className={cn(
                  'h-full transition-opacity hover:opacity-75',
                  sizeStyle.bar,
                  props.fillWidth ? 'min-w-0 flex-1' : 'shrink-0',
                  getHeartbeatBarColorClass(hb.status)
                )}
              />
            }
          />
          <TooltipContent side='top' className='font-mono text-xs'>
            <div className='font-medium'>
              {t(getHeartbeatStatusLabelKey(hb.status))}
            </div>
            {hb.time && <div className='text-muted-foreground'>{hb.time}</div>}
            {hb.ping != null && Number.isFinite(hb.ping) && (
              <div>
                {Math.round(hb.ping)} {t('ms')}
              </div>
            )}
          </TooltipContent>
        </Tooltip>
      ))}
    </div>
  )
})

export interface ModelConnectivityBadgeProps {
  uptime: ResolvedModelUptime | null | undefined
  variant?: 'table' | 'card'
  className?: string
  children?: React.ReactNode
}

export const ModelConnectivityBadge = memo(function ModelConnectivityBadge(
  props: ModelConnectivityBadgeProps
) {
  const { t } = useTranslation()
  const variant = props.variant ?? 'table'
  const uptime = props.uptime

  if (variant === 'table') {
    if (
      !uptime ||
      (uptime.state === 'unknown' &&
        uptime.uptime === null &&
        uptime.heartbeats.length === 0)
    ) {
      return (
        <span
          className={cn('text-muted-foreground/50 text-xs', props.className)}
        >
          —
        </span>
      )
    }

    return (
      <div
        className={cn('flex min-w-0 flex-col gap-1', props.className)}
        onClick={(e) => e.stopPropagation()}
      >
        <div className='flex items-center gap-1.5'>
          <StatusBadge
            variant={getConnectivityBadgeVariant(uptime.state)}
            label={t(getConnectivityStatusLabelKey(uptime.state))}
            size='sm'
            showDot
            copyable={false}
          />
          <span
            className={cn(
              'font-mono text-xs font-semibold tabular-nums',
              getConnectivityTextColorClass(uptime.state)
            )}
          >
            {formatUptimePercentage(uptime.uptime)}
          </span>
        </div>
        {uptime.heartbeats.length > 0 && (
          <ModelHeartbeatBars
            heartbeats={uptime.heartbeats}
            maxBars={24}
            size='sm'
          />
        )}
      </div>
    )
  }

  const hasData = Boolean(
    uptime &&
    (uptime.state !== 'unknown' ||
      uptime.uptime !== null ||
      uptime.heartbeats.length > 0)
  )

  return (
    <div
      aria-label={t('Connectivity (24h)')}
      className={cn(
        'flex w-full min-w-0 items-center justify-between gap-3',
        props.className
      )}
    >
      {hasData && uptime ? (
        <div className='flex min-w-0 flex-1 flex-col gap-1'>
          <div className='flex items-center gap-1.5'>
            <StatusBadge
              variant={getConnectivityBadgeVariant(uptime.state)}
              label={t(getConnectivityStatusLabelKey(uptime.state))}
              size='sm'
              showDot
              copyable={false}
            />
            <span
              className={cn(
                'font-mono text-xs font-semibold tabular-nums',
                getConnectivityTextColorClass(uptime.state)
              )}
            >
              {formatUptimePercentage(uptime.uptime)}
            </span>
          </div>
          {uptime.heartbeats.length > 0 && (
            <ModelHeartbeatBars
              heartbeats={uptime.heartbeats}
              maxBars={24}
              size='sm'
            />
          )}
        </div>
      ) : (
        <div className='text-muted-foreground flex min-w-0 items-center gap-1.5 text-xs'>
          <span>{t('Connectivity (24h)')}:</span>
          <span className='font-mono'>—</span>
        </div>
      )}
      {props.children}
    </div>
  )
})
