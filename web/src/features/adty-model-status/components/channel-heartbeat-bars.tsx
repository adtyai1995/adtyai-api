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

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { UptimeHeartbeat } from '@/features/dashboard/types'
import { cn } from '@/lib/utils'

import { formatCheckedTimestamp, formatLatency } from '../lib/channel-status'

const MAX_BARS = 24

const PLACEHOLDER_KEYS = [
  'p01',
  'p02',
  'p03',
  'p04',
  'p05',
  'p06',
  'p07',
  'p08',
  'p09',
  'p10',
  'p11',
  'p12',
  'p13',
  'p14',
  'p15',
  'p16',
  'p17',
  'p18',
  'p19',
  'p20',
  'p21',
  'p22',
  'p23',
  'p24',
] as const

function getBarColorClass(status: number): string {
  if (status === 1) return 'bg-success'
  if (status === 2) return 'bg-warning'
  if (status === 0) return 'bg-destructive'
  return 'bg-muted-foreground/30'
}

function getBarStatusLabelKey(status: number): string {
  if (status === 1) return 'Healthy'
  if (status === 2) return 'Channel degraded'
  if (status === 0) return 'Abnormal'
  return 'No data'
}

export interface ChannelHeartbeatBarsProps {
  heartbeats: UptimeHeartbeat[]
  className?: string
}

export const ChannelHeartbeatBars = memo(function ChannelHeartbeatBars(
  props: ChannelHeartbeatBarsProps
) {
  const { t } = useTranslation()

  const recent = useMemo(
    () => props.heartbeats.slice(-MAX_BARS),
    [props.heartbeats]
  )

  const keyedBars = useMemo(() => {
    const seen = new Map<string, number>()
    return recent.map((hb) => {
      const base = `${hb.time || 't'}-${hb.status}-${hb.ping ?? 'n'}`
      const count = seen.get(base) ?? 0
      seen.set(base, count + 1)
      return {
        key: count === 0 ? base : `${base}#${count}`,
        hb,
      }
    })
  }, [recent])

  const placeholderKeys = PLACEHOLDER_KEYS.slice(
    0,
    Math.max(0, MAX_BARS - recent.length)
  )

  return (
    <div className={cn('space-y-1', props.className)}>
      <div
        role='img'
        aria-label={t('24h heartbeat history')}
        className='flex h-4.5 w-full items-center gap-[3px]'
      >
        {placeholderKeys.map((slotKey) => (
          <span
            key={slotKey}
            aria-hidden='true'
            className='bg-muted-foreground/15 h-full min-w-0 flex-1 rounded-full'
          />
        ))}
        {keyedBars.map(({ key, hb }) => (
          <Tooltip key={key}>
            <TooltipTrigger
              render={
                <span
                  className={cn(
                    'h-full min-w-0 flex-1 rounded-full transition-opacity hover:opacity-75',
                    getBarColorClass(hb.status)
                  )}
                />
              }
            />
            <TooltipContent side='top' className='font-mono text-xs'>
              <div className='font-medium'>
                {t(getBarStatusLabelKey(hb.status))}
              </div>
              {hb.time && (
                <div className='opacity-80'>
                  {formatCheckedTimestamp(hb.time)}
                </div>
              )}
              {hb.ping != null && Number.isFinite(hb.ping) && (
                <div>{formatLatency(hb.ping)}</div>
              )}
            </TooltipContent>
          </Tooltip>
        ))}
      </div>
      <div className='text-muted-foreground/75 flex items-center justify-between text-[11px]'>
        <span>{t('Past')}</span>
        <span>{t('Now')}</span>
      </div>
    </div>
  )
})
