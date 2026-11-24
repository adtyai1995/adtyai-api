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
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  RotateCw,
  Search,
  X,
  XCircle,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useGroupDisplay } from '@/features/adty-groups/hooks/use-group-display'
import type { UptimeGroupResult } from '@/features/dashboard/types'
import { ModelHeartbeatBars } from '@/features/pricing/components/model-connectivity-badge'
import { useUptimeStatus } from '@/features/pricing/hooks/use-uptime-status'
import {
  extractGroupModelStatuses,
  formatUptimePercentage,
  getConnectivityBadgeVariant,
  getConnectivityStatusLabelKey,
  getConnectivityTextColorClass,
  resolveModelPingMs,
  type ConnectivityState,
  type ResolvedModelUptime,
} from '@/features/pricing/lib/uptime'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getLobeIcon } from '@/lib/lobe-icon'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

const ALL_GROUPS_KEY = '__all__'

type StatusFilter = 'all' | 'online' | 'degraded' | 'offline'

interface DisplayGroupSection {
  key: string
  groupId?: string
  title: string
  description?: string
  items: ResolvedModelUptime[]
}

function formatLastCheckedAt(
  value: string | number | null | undefined,
  locale?: string
): string | null {
  if (value == null || value === '') return null
  let date: Date
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || value <= 0) return null
    date = new Date(value > 1e12 ? value : value * 1000)
  } else {
    date = new Date(value)
  }
  if (Number.isNaN(date.getTime())) {
    return typeof value === 'string' ? value : null
  }
  return new Intl.DateTimeFormat(locale, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date)
}

function getGroupKey(group: UptimeGroupResult, idx: number): string {
  return group.groupId || group.categoryName || `group-${idx}`
}

function matchesStatusFilter(
  state: ConnectivityState,
  filter: StatusFilter
): boolean {
  if (filter === 'all') return true
  if (filter === 'online') return state === 'online'
  if (filter === 'degraded') {
    return state === 'degraded' || state === 'maintenance'
  }
  return state === 'offline'
}

interface SummaryStatCardProps {
  label: string
  count: number
  locale?: string
  icon: React.ComponentType<{ className?: string }>
  toneClass: string
  dotClass: string
  active: boolean
  onClick: () => void
}

function SummaryStatCard(props: SummaryStatCardProps) {
  const Icon = props.icon
  return (
    <button
      type='button'
      onClick={props.onClick}
      aria-pressed={props.active}
      className={cn(
        'bg-card border-border hover:border-foreground/20 flex items-center justify-between gap-3 rounded-[14px] border p-4 text-left transition-colors',
        props.active && 'ring-ring ring-2'
      )}
    >
      <div className='min-w-0'>
        <div className='text-muted-foreground flex items-center gap-2 text-xs font-medium'>
          <span
            className={cn('size-2 rounded-full', props.dotClass)}
            aria-hidden='true'
          />
          <span className='truncate'>{props.label}</span>
        </div>
        <div
          className={cn(
            'mt-2 font-mono text-2xl font-bold tracking-tight tabular-nums',
            props.toneClass
          )}
        >
          {formatNumber(props.count, props.locale)}
        </div>
      </div>
      <div className='bg-muted/60 flex size-10 shrink-0 items-center justify-center rounded-xl'>
        <Icon className={cn('size-5', props.toneClass)} aria-hidden='true' />
      </div>
    </button>
  )
}

export function ModelStatusPage() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const groupDisplay = useGroupDisplay()

  const { groups, isLoading, isFetching, isError, error, refetch } =
    useUptimeStatus({ refetchInterval: 60 * 1000 })

  const [selectedGroupKey, setSelectedGroupKey] =
    useState<string>(ALL_GROUPS_KEY)
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [searchQuery, setSearchQuery] = useState('')

  const sections = useMemo<DisplayGroupSection[]>(
    () =>
      groups.map((group, idx) => ({
        key: getGroupKey(group, idx),
        groupId: group.groupId,
        title: group.groupId
          ? groupDisplay.name(group.groupId)
          : group.categoryName,
        description: group.description,
        items: extractGroupModelStatuses(group),
      })),
    [groupDisplay, groups]
  )

  const activeGroupKey = useMemo(() => {
    if (selectedGroupKey === ALL_GROUPS_KEY) return ALL_GROUPS_KEY
    const exists = sections.some((section) => section.key === selectedGroupKey)
    return exists ? selectedGroupKey : ALL_GROUPS_KEY
  }, [sections, selectedGroupKey])

  const groupScopedSections = useMemo(() => {
    if (activeGroupKey === ALL_GROUPS_KEY) return sections
    return sections.filter((section) => section.key === activeGroupKey)
  }, [activeGroupKey, sections])

  const summary = useMemo(() => {
    let online = 0
    let degraded = 0
    let offline = 0
    let unknown = 0
    for (const section of groupScopedSections) {
      for (const item of section.items) {
        if (item.state === 'online') {
          online++
        } else if (item.state === 'degraded' || item.state === 'maintenance') {
          degraded++
        } else if (item.state === 'offline') {
          offline++
        } else {
          unknown++
        }
      }
    }
    return {
      online,
      degraded,
      offline,
      unknown,
      total: online + degraded + offline + unknown,
    }
  }, [groupScopedSections])

  const filteredSections = useMemo(() => {
    const query = searchQuery.trim().toLowerCase()
    return groupScopedSections
      .map((section) => {
        const items = section.items.filter((item) => {
          if (!matchesStatusFilter(item.state, statusFilter)) return false
          if (!query) return true
          return (
            item.model.toLowerCase().includes(query) ||
            section.title.toLowerCase().includes(query) ||
            (section.groupId?.toLowerCase().includes(query) ?? false)
          )
        })
        return { ...section, items }
      })
      .filter((section) => section.items.length > 0)
  }, [groupScopedSections, searchQuery, statusFilter])

  const toggleStatusFilter = (next: Exclude<StatusFilter, 'all'>) => {
    setStatusFilter((prev) => (prev === next ? 'all' : next))
  }

  let bodyContent: React.ReactNode
  if (isLoading) {
    bodyContent = <LoadingState message={t('Loading model status...')} />
  } else if (isError && groups.length === 0) {
    bodyContent = (
      <ErrorState
        title={t('Failed to load uptime status')}
        description={getServerErrorMessage(
          error,
          t('Failed to load uptime status')
        )}
        onRetry={() => void refetch()}
      />
    )
  } else if (sections.length === 0) {
    bodyContent = (
      <EmptyState
        bordered
        icon={Activity}
        title={t('No model connectivity monitoring configured')}
        description={t(
          'Model connectivity status will appear here once model connectivity monitoring is configured.'
        )}
      />
    )
  } else {
    bodyContent = (
      <div className='flex flex-col gap-4'>
        <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
          <SummaryStatCard
            label={t('Online')}
            count={summary.online}
            locale={locale}
            icon={CheckCircle2}
            toneClass='text-success'
            dotClass='bg-success'
            active={statusFilter === 'online'}
            onClick={() => toggleStatusFilter('online')}
          />
          <SummaryStatCard
            label={t('Degraded')}
            count={summary.degraded}
            locale={locale}
            icon={AlertTriangle}
            toneClass='text-warning'
            dotClass='bg-warning'
            active={statusFilter === 'degraded'}
            onClick={() => toggleStatusFilter('degraded')}
          />
          <SummaryStatCard
            label={t('Offline')}
            count={summary.offline}
            locale={locale}
            icon={XCircle}
            toneClass='text-destructive'
            dotClass='bg-destructive'
            active={statusFilter === 'offline'}
            onClick={() => toggleStatusFilter('offline')}
          />
        </div>

        <div className='bg-card border-border flex flex-col gap-3 rounded-[14px] border p-3 sm:flex-row sm:items-center sm:justify-between'>
          <Tabs value={activeGroupKey} onValueChange={setSelectedGroupKey}>
            <TabsList className='max-w-full flex-wrap justify-start group-data-horizontal/tabs:h-auto'>
              <TabsTrigger value={ALL_GROUPS_KEY} className='gap-1.5'>
                <span>{t('All Groups')}</span>
                <Badge variant='secondary' className='h-4 px-1.5 text-[10px]'>
                  {sections.length}
                </Badge>
              </TabsTrigger>
              {sections.map((section) => (
                <TabsTrigger
                  key={section.key}
                  value={section.key}
                  className='gap-1.5'
                >
                  <span className='max-w-40 truncate'>{section.title}</span>
                  <Badge variant='secondary' className='h-4 px-1.5 text-[10px]'>
                    {section.items.length}
                  </Badge>
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>

          <div className='flex items-center gap-2'>
            <div className='relative w-full sm:w-64'>
              <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2' />
              <Input
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder={t('Search models...')}
                className='h-8 pr-8 pl-8 text-sm'
              />
              {searchQuery && (
                <button
                  type='button'
                  onClick={() => setSearchQuery('')}
                  className='text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2'
                  aria-label={t('Clear')}
                >
                  <X className='size-3.5' />
                </button>
              )}
            </div>
            {statusFilter !== 'all' && (
              <Button
                type='button'
                variant='ghost'
                size='sm'
                onClick={() => setStatusFilter('all')}
                className='h-8 shrink-0 text-xs'
              >
                {t('Reset')}
              </Button>
            )}
          </div>
        </div>

        {filteredSections.length === 0 ? (
          <EmptyState
            bordered
            icon={Search}
            title={t('No Models Found')}
            description={t('No models match your current filters.')}
            action={
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => {
                  setSearchQuery('')
                  setStatusFilter('all')
                  setSelectedGroupKey(ALL_GROUPS_KEY)
                }}
              >
                {t('Clear filters')}
              </Button>
            }
          />
        ) : (
          <div className='flex flex-col gap-4'>
            {filteredSections.map((section) => {
              const onlineCount = section.items.filter(
                (item) => item.state === 'online'
              ).length
              return (
                <Card
                  key={section.key}
                  className='border-border bg-card overflow-hidden rounded-[14px]'
                >
                  <CardHeader className='border-border flex flex-row flex-wrap items-center justify-between gap-2 border-b px-4 py-3 sm:px-5'>
                    <div className='min-w-0'>
                      <div className='flex items-center gap-2'>
                        <h2 className='truncate text-sm font-semibold'>
                          {section.title}
                        </h2>
                        {section.groupId &&
                          section.groupId !== section.title && (
                            <Badge
                              variant='outline'
                              className='font-mono text-[11px]'
                            >
                              {section.groupId}
                            </Badge>
                          )}
                      </div>
                      {section.description && (
                        <p className='text-muted-foreground mt-0.5 text-xs'>
                          {section.description}
                        </p>
                      )}
                    </div>
                    <Badge
                      variant='secondary'
                      className='font-mono text-xs tabular-nums'
                    >
                      {formatNumber(onlineCount, locale)} /{' '}
                      {formatNumber(section.items.length, locale)} {t('Online')}
                    </Badge>
                  </CardHeader>

                  <CardContent className='divide-border divide-y p-0'>
                    {section.items.map((item) => {
                      const icon = getLobeIcon(item.model, 16)
                      const avgPing = resolveModelPingMs(item)
                      const checkedAt = formatLastCheckedAt(
                        item.lastCheckedAt,
                        locale
                      )
                      return (
                        <div
                          key={`${section.key}:${item.model}`}
                          className='hover:bg-muted/30 flex flex-col gap-2.5 px-4 py-3.5 transition-colors sm:px-5'
                        >
                          <div className='flex flex-wrap items-center justify-between gap-2'>
                            <div className='flex min-w-0 items-center gap-2'>
                              {icon}
                              <span
                                className='truncate font-mono text-sm font-medium'
                                title={item.model}
                              >
                                {item.model}
                              </span>
                              <StatusBadge
                                variant={getConnectivityBadgeVariant(
                                  item.state
                                )}
                                label={t(
                                  getConnectivityStatusLabelKey(item.state)
                                )}
                                size='sm'
                                showDot
                                copyable={false}
                              />
                            </div>

                            <div className='flex shrink-0 items-center gap-3 text-xs'>
                              {avgPing !== null && (
                                <span className='text-muted-foreground font-mono tabular-nums'>
                                  {avgPing} {t('ms')}
                                </span>
                              )}
                              <span
                                className={cn(
                                  'font-mono text-sm font-semibold tabular-nums',
                                  getConnectivityTextColorClass(item.state)
                                )}
                              >
                                {formatUptimePercentage(item.uptime)}
                              </span>
                            </div>
                          </div>

                          <div className='space-y-1'>
                            <ModelHeartbeatBars
                              heartbeats={item.heartbeats}
                              maxBars={24}
                              size='lg'
                              fillWidth
                            />
                            <div className='text-muted-foreground/70 flex flex-wrap items-center justify-between gap-2 text-[11px]'>
                              <span>{t('24h ago')}</span>
                              <div className='flex min-w-0 items-center gap-2'>
                                {item.lastMessage && (
                                  <span
                                    className={cn(
                                      'max-w-xs truncate',
                                      item.state === 'offline' &&
                                        'text-destructive/80'
                                    )}
                                    title={item.lastMessage}
                                  >
                                    {item.lastMessage}
                                  </span>
                                )}
                                <span>
                                  {checkedAt
                                    ? t('Checked {{time}}', { time: checkedAt })
                                    : t('Now')}
                                </span>
                              </div>
                            </div>
                          </div>
                        </div>
                      )
                    })}
                  </CardContent>
                </Card>
              )
            })}
          </div>
        )}
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Model Status')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Real-time 24h model availability and heartbeat history')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Actions>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void refetch()}
          disabled={isFetching}
        >
          <RotateCw
            className={cn('size-3.5', isFetching && 'animate-spin')}
            aria-hidden='true'
          />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{bodyContent}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
