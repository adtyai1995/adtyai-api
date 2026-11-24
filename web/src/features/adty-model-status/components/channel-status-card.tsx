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
import { Activity, Clock, KeyRound, Layers } from 'lucide-react'
import { memo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import {
  formatCheckedTimestamp,
  formatGroupRatio,
  formatLatency,
  formatSuccessRate,
  getChannelStatusMeta,
  getPlatformMeta,
  getSuccessRateToneClass,
  type ChannelStatusCardItem,
} from '../lib/channel-status'
import { ChannelHeartbeatBars } from './channel-heartbeat-bars'

export interface ChannelStatusCardProps {
  item: ChannelStatusCardItem
  canCreateApiKey: boolean
  onCreateApiKey: (groupId?: string) => void
}

export const ChannelStatusCard = memo(function ChannelStatusCard(
  props: ChannelStatusCardProps
) {
  const { t } = useTranslation()
  const item = props.item
  const platformMeta = getPlatformMeta(item.platform)
  const statusMeta = getChannelStatusMeta(item.status)
  const checkedAt = formatCheckedTimestamp(item.lastCheckedAt)
  const successRateTone = getSuccessRateToneClass(item.uptime)

  return (
    <Card className='border-border bg-card hover:border-foreground/15 flex flex-col gap-3.5 rounded-2xl p-4 shadow-none transition-colors'>
      {/* Top row: platform icon + title/status + tags */}
      <div className='flex items-start gap-3'>
        <div
          className='border-border/80 bg-muted/35 flex size-10 shrink-0 items-center justify-center rounded-xl border'
          aria-hidden='true'
        >
          {platformMeta.lobeIcon ? (
            getLobeIcon(platformMeta.lobeIcon, 20)
          ) : (
            <Layers className='text-muted-foreground size-5' />
          )}
        </div>

        <div className='min-w-0 flex-1'>
          <div className='flex items-center justify-between gap-2'>
            <h3
              className='text-foreground truncate text-sm font-semibold'
              title={item.displayName}
            >
              {item.displayName}
            </h3>
            <span
              className={cn(
                'inline-flex shrink-0 items-center gap-1.5 text-xs font-medium',
                statusMeta.textClass
              )}
            >
              <span
                className={cn('size-1.5 rounded-full', statusMeta.dotClass)}
                aria-hidden='true'
              />
              <span>{t(statusMeta.labelKey)}</span>
            </span>
          </div>

          <div className='mt-1.5 flex flex-wrap items-center gap-1.5'>
            <span className='bg-info-soft text-info inline-flex items-center rounded-md px-2 py-0.5 text-[11px] font-medium'>
              {t(platformMeta.labelKey)}
            </span>

            {item.groupRatio !== null && (
              <span className='bg-warning-soft text-warning inline-flex items-center rounded-md px-2 py-0.5 text-[11px] font-medium tabular-nums'>
                {t('Multiplier x{{ratio}}', {
                  ratio: formatGroupRatio(item.groupRatio),
                })}
              </span>
            )}

            {item.probeModel && (
              <span
                className='bg-muted text-muted-foreground inline-flex max-w-[136px] items-center truncate rounded-md px-2 py-0.5 font-mono text-[11px]'
                title={item.probeModel}
              >
                {item.probeModel}
              </span>
            )}

            {item.probeType === 'image' && (
              <span className='bg-secondary text-secondary-foreground border-border/60 inline-flex items-center rounded-md border px-1.5 py-0.5 text-[11px] font-medium'>
                {t('Image')}
              </span>
            )}
          </div>
        </div>
      </div>

      {/* 2-Column KPI Strip */}
      <div className='grid grid-cols-2 gap-2'>
        <div className='border-border/70 bg-muted/20 flex min-w-0 flex-col justify-between rounded-xl border px-2.5 py-2.5'>
          <div>
            <div className='text-muted-foreground flex items-center gap-1 text-[11px] font-medium'>
              <Clock className='size-3 shrink-0' aria-hidden='true' />
              <span className='truncate'>{t('Average latency')}</span>
            </div>
            <div className='text-muted-foreground/75 mt-0.5 truncate text-[10px]'>
              {t('Last 24h')}
            </div>
          </div>
          <div
            className={cn(
              'mt-2 truncate font-mono text-[15px] font-bold tracking-tight tabular-nums',
              item.avgPingMs === null
                ? 'text-muted-foreground'
                : 'text-foreground'
            )}
          >
            {formatLatency(item.avgPingMs)}
          </div>
        </div>

        <div className='border-border/70 bg-muted/20 flex min-w-0 flex-col justify-between rounded-xl border px-2.5 py-2.5'>
          <div>
            <div className='text-muted-foreground flex items-center gap-1 text-[11px] font-medium'>
              <Activity className='size-3 shrink-0' aria-hidden='true' />
              <span className='truncate'>{t('Success rate')}</span>
            </div>
            <div className='text-muted-foreground/75 mt-0.5 truncate text-[10px]'>
              {t('Last 24h')}
            </div>
          </div>
          <div
            className={cn(
              'mt-2 truncate font-mono text-[15px] font-bold tracking-tight tabular-nums',
              successRateTone
            )}
          >
            {formatSuccessRate(item.uptime)}
          </div>
        </div>
      </div>

      {/* Footer action row */}
      <div className='flex items-center justify-between gap-2 pt-0.5'>
        <div className='min-w-0'>
          <div className='text-muted-foreground text-[11px] leading-tight'>
            {t('Last 24h')}
          </div>
          <div className='text-muted-foreground/80 mt-0.5 truncate font-mono text-[11px] leading-tight tabular-nums'>
            {checkedAt}
          </div>
        </div>

        {props.canCreateApiKey && (
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => props.onCreateApiKey(item.groupId)}
            className='h-7 shrink-0 gap-1.5 rounded-full px-2.5 text-xs font-medium shadow-none'
          >
            <KeyRound className='size-3' aria-hidden='true' />
            <span>{t('Create API Key')}</span>
          </Button>
        )}
      </div>

      {/* Bottom 24-Bar Heartbeat Timeline */}
      <ChannelHeartbeatBars heartbeats={item.heartbeats} />
    </Card>
  )
})
