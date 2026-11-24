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
import { Activity, Layers, Timer } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  staticDataTableClassNames as tableStyles,
} from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import type { UptimeGroupResult } from '@/features/dashboard/types'
import { cn } from '@/lib/utils'

import { useUptimeStatus } from '../hooks/use-uptime-status'
import {
  formatUptimePercentage,
  getConnectivityBadgeVariant,
  getConnectivityStatusLabelKey,
  getConnectivityTextColorClass,
  resolveAllGroupUptimesForModel,
  resolveModelPingMs,
  resolveModelUptimeForPricing,
  type ResolvedModelUptime,
} from '../lib/uptime'
import type { PricingModel } from '../types'
import { ModelHeartbeatBars } from './model-connectivity-badge'

function StatCard(props: {
  icon: React.ComponentType<{ className?: string }>
  label: string
  value: React.ReactNode
  hint?: string
  valueClassName?: string
}) {
  const Icon = props.icon
  return (
    <div className='bg-background flex flex-col gap-1 rounded-lg border p-3'>
      <span className='text-muted-foreground inline-flex items-center gap-1.5 text-[10px] font-medium tracking-wider uppercase'>
        <Icon className='size-3' />
        {props.label}
      </span>
      <span
        className={cn(
          'text-foreground font-mono text-lg font-semibold tabular-nums',
          props.valueClassName
        )}
      >
        {props.value}
      </span>
      {props.hint && (
        <span className='text-muted-foreground/70 text-[11px]'>
          {props.hint}
        </span>
      )}
    </div>
  )
}

export interface ModelDetailsPerformanceProps {
  model: PricingModel
  selectedGroup?: string
  uptimeGroups?: UptimeGroupResult[]
}

export function ModelDetailsPerformance(props: ModelDetailsPerformanceProps) {
  const { t } = useTranslation()
  const uptimeQuery = useUptimeStatus({
    enabled: props.uptimeGroups === undefined,
  })
  const groups = props.uptimeGroups ?? uptimeQuery.groups

  const allMatches = useMemo(
    () => resolveAllGroupUptimesForModel(groups, props.model),
    [groups, props.model]
  )
  const primary = useMemo(
    () =>
      resolveModelUptimeForPricing(groups, props.model, props.selectedGroup),
    [groups, props.model, props.selectedGroup]
  )

  if (uptimeQuery.isLoading && props.uptimeGroups === undefined) {
    return (
      <div className='text-muted-foreground rounded-lg border p-6 text-center text-sm'>
        {t('Loading...')}
      </div>
    )
  }

  if (allMatches.length === 0 || !primary) {
    return (
      <div className='text-muted-foreground rounded-lg border p-6 text-center text-sm'>
        {t('No 24h connectivity monitoring data available for this model.')}
      </div>
    )
  }

  const avgPing = resolveModelPingMs(primary)

  return (
    <div className='flex flex-col gap-4'>
      <div className='grid grid-cols-1 gap-2 sm:grid-cols-3'>
        <StatCard
          icon={Activity}
          label={t('Status')}
          value={
            <StatusBadge
              variant={getConnectivityBadgeVariant(primary.state)}
              label={t(getConnectivityStatusLabelKey(primary.state))}
              size='md'
              showDot
              copyable={false}
            />
          }
          hint={primary.categoryName}
        />
        <StatCard
          icon={Activity}
          label={t('24h Uptime')}
          value={formatUptimePercentage(primary.uptime)}
          hint={t('Measured over the last 24 hours')}
          valueClassName={getConnectivityTextColorClass(primary.state)}
        />
        <StatCard
          icon={Timer}
          label={t('Average latency')}
          value={avgPing !== null ? `${avgPing} ${t('ms')}` : '—'}
          hint={t('{{count}} monitored groups', { count: allMatches.length })}
        />
      </div>

      {primary.heartbeats.length > 0 && (
        <section className='bg-card rounded-[14px] border p-4'>
          <div className='mb-3 flex items-center justify-between gap-2'>
            <div className='flex items-center gap-2 text-sm font-semibold'>
              <Activity className='text-muted-foreground size-3.5' />
              {t('24h heartbeat history')}
            </div>
            <span className='text-muted-foreground font-mono text-xs'>
              {primary.categoryName}
            </span>
          </div>
          <ModelHeartbeatBars
            heartbeats={primary.heartbeats}
            maxBars={24}
            size='lg'
            fillWidth
          />
          <div className='text-muted-foreground mt-2 flex items-center justify-between text-[11px]'>
            <span>{t('24h ago')}</span>
            <span>{t('Now')}</span>
          </div>
        </section>
      )}

      <section>
        <div className='mb-2 flex items-center gap-2'>
          <Layers className='text-muted-foreground/70 size-3.5 shrink-0' />
          <div>
            <div className='text-foreground text-sm font-semibold'>
              {t('Connectivity by Group')}
            </div>
            <p className='text-muted-foreground/80 text-xs'>
              {t('24h availability and heartbeat timeline per monitored group')}
            </p>
          </div>
        </div>
        <StaticDataTable
          className='rounded-lg'
          tableClassName='text-sm'
          headerRowClassName={tableStyles.compactHeaderRow}
          data={allMatches}
          getRowKey={(row: ResolvedModelUptime) =>
            `${row.groupId ?? row.categoryName}:${row.model}`
          }
          columns={[
            {
              id: 'group',
              header: t('Group'),
              className: tableStyles.compactHeaderCell,
              cellClassName: tableStyles.compactCell,
              cell: (row: ResolvedModelUptime) =>
                row.groupId ? (
                  <GroupBadge group={row.groupId} size='sm' />
                ) : (
                  <span className='font-medium'>{row.categoryName}</span>
                ),
            },
            {
              id: 'status',
              header: t('Status'),
              className: tableStyles.compactHeaderCell,
              cellClassName: tableStyles.compactCell,
              cell: (row: ResolvedModelUptime) => (
                <StatusBadge
                  variant={getConnectivityBadgeVariant(row.state)}
                  label={t(getConnectivityStatusLabelKey(row.state))}
                  size='sm'
                  showDot
                  copyable={false}
                />
              ),
            },
            {
              id: 'uptime',
              header: t('24h Uptime'),
              className: tableStyles.compactHeaderCellRight,
              cellClassName: tableStyles.compactNumericCell,
              cell: (row: ResolvedModelUptime) => (
                <span className={getConnectivityTextColorClass(row.state)}>
                  {formatUptimePercentage(row.uptime)}
                </span>
              ),
            },
            {
              id: 'ping',
              header: t('Average latency'),
              className: tableStyles.compactHeaderCellRight,
              cellClassName: tableStyles.compactMutedNumericCell,
              cell: (row: ResolvedModelUptime) => {
                const ping = resolveModelPingMs(row)
                return ping !== null ? `${ping} ${t('ms')}` : '—'
              },
            },
            {
              id: 'heartbeats',
              header: t('24h heartbeat history'),
              className: cn(tableStyles.compactHeaderCell, 'min-w-[160px]'),
              cellClassName: tableStyles.compactCell,
              cell: (row: ResolvedModelUptime) =>
                row.heartbeats.length > 0 ? (
                  <ModelHeartbeatBars
                    heartbeats={row.heartbeats}
                    maxBars={24}
                    size='md'
                  />
                ) : (
                  <span className='text-muted-foreground text-xs'>—</span>
                ),
            },
          ]}
        />
      </section>
    </div>
  )
}
