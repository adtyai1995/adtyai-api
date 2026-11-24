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
import { useNavigate } from '@tanstack/react-router'
import { Activity, ChevronDown, ChevronUp } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { useGroupDisplay } from '@/features/adty-groups/hooks/use-group-display'
import type { UptimePlatform } from '@/features/dashboard/types'
import { useUptimeStatus } from '@/features/pricing/hooks/use-uptime-status'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getLobeIcon } from '@/lib/lobe-icon'
import { ROLE } from '@/lib/roles'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { AutoRefreshControls } from './components/auto-refresh-controls'
import { ChannelStatusCard } from './components/channel-status-card'
import {
  CHANNEL_PLATFORMS,
  computeAggregateHealth,
  DEFAULT_SORT_DIRECTIONS,
  resolveChannelCard,
  sortChannelCards,
  type AutoRefreshInterval,
  type ChannelPlatformFilter,
  type ChannelSortDirection,
  type ChannelSortKey,
} from './lib/channel-status'

interface SortOptionConfig {
  key: ChannelSortKey
  labelKey: string
  ascLabelKey: string
  descLabelKey: string
}

const SORT_OPTIONS: readonly SortOptionConfig[] = [
  {
    key: 'ratio',
    labelKey: 'Multiplier',
    ascLabelKey: 'Low to high',
    descLabelKey: 'High to low',
  },
  {
    key: 'uptime',
    labelKey: 'Success rate',
    ascLabelKey: 'Low to high',
    descLabelKey: 'High to low',
  },
  {
    key: 'avgPingMs',
    labelKey: 'Average latency',
    ascLabelKey: 'Low to high',
    descLabelKey: 'High to low',
  },
  {
    key: 'status',
    labelKey: 'Status',
    ascLabelKey: 'Healthy first',
    descLabelKey: 'Issues first',
  },
] as const

export function ModelStatusPage() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const navigate = useNavigate()
  const groupDisplay = useGroupDisplay()

  const userRole = useAuthStore((s) => s.auth.user?.role ?? ROLE.GUEST)
  const isSiteOwner = useAuthStore((s) => s.auth.user?.adty_site_owner === true)
  const isStaff = useAuthStore((s) => (s.auth.user?.staff_site_id ?? 0) > 0)
  const canCreateApiKey = !isStaff && !(isSiteOwner && userRole < ROLE.ADMIN)

  const [intervalSeconds, setIntervalSeconds] =
    useState<AutoRefreshInterval>(60)
  const [refreshCycleKey, setRefreshCycleKey] = useState(0)
  const [selectedPlatform, setSelectedPlatform] =
    useState<ChannelPlatformFilter>('all')
  const [sortKey, setSortKey] = useState<ChannelSortKey>('ratio')
  const [sortDirection, setSortDirection] = useState<ChannelSortDirection>(
    DEFAULT_SORT_DIRECTIONS.ratio
  )

  const { groups, isLoading, isFetching, isError, error, refetch } =
    useUptimeStatus({ refetchInterval: false })

  const cards = useMemo(
    () =>
      groups.map((group, idx) =>
        resolveChannelCard(group, idx, groupDisplay.name)
      ),
    [groupDisplay.name, groups]
  )

  const platformCounts = useMemo(() => {
    const counts = new Map<UptimePlatform, number>()
    for (const card of cards) {
      counts.set(card.platform, (counts.get(card.platform) ?? 0) + 1)
    }
    return counts
  }, [cards])

  const visiblePlatforms = useMemo(
    () =>
      CHANNEL_PLATFORMS.filter(
        (platform) => (platformCounts.get(platform.id) ?? 0) > 0
      ),
    [platformCounts]
  )

  const activePlatform: ChannelPlatformFilter = useMemo(() => {
    if (selectedPlatform === 'all') return 'all'
    return (platformCounts.get(selectedPlatform) ?? 0) > 0
      ? selectedPlatform
      : 'all'
  }, [platformCounts, selectedPlatform])

  const filteredAndSortedCards = useMemo(() => {
    const filtered =
      activePlatform === 'all'
        ? cards
        : cards.filter((card) => card.platform === activePlatform)
    return sortChannelCards(filtered, sortKey, sortDirection)
  }, [activePlatform, cards, sortDirection, sortKey])

  const aggregateHealth = useMemo(() => computeAggregateHealth(cards), [cards])

  const handleManualRefresh = useCallback(() => {
    setRefreshCycleKey((prev) => prev + 1)
    void refetch()
  }, [refetch])

  const handleSortToggle = useCallback(
    (nextKey: ChannelSortKey) => {
      if (sortKey === nextKey) {
        setSortDirection((prev) => (prev === 'asc' ? 'desc' : 'asc'))
        return
      }
      setSortKey(nextKey)
      setSortDirection(DEFAULT_SORT_DIRECTIONS[nextKey])
    },
    [sortKey]
  )

  const handleCreateApiKey = useCallback(
    (groupId?: string) => {
      void navigate({
        to: '/keys',
        search: {
          create: '1',
          ...(groupId ? { group: groupId } : {}),
        },
      })
    },
    [navigate]
  )

  let bodyContent: React.ReactNode
  if (isLoading) {
    bodyContent = <LoadingState message={t('Loading channel status...')} />
  } else if (isError && groups.length === 0) {
    bodyContent = (
      <ErrorState
        title={t('Failed to load uptime status')}
        description={getServerErrorMessage(
          error,
          t('Failed to load uptime status')
        )}
        onRetry={handleManualRefresh}
      />
    )
  } else if (cards.length === 0) {
    bodyContent = (
      <EmptyState
        bordered
        icon={Activity}
        title={t('No channel status monitoring configured')}
        description={t(
          'Channel health cards will appear here once channel status monitoring is configured.'
        )}
      />
    )
  } else {
    bodyContent = (
      <div className='flex flex-col gap-4'>
        {/* Filter & Sort Toolbar */}
        <div className='flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between'>
          {/* Platform filter pills */}
          <div
            role='group'
            aria-label={t('Platform')}
            className='flex flex-wrap items-center gap-1.5'
          >
            <button
              type='button'
              aria-pressed={activePlatform === 'all'}
              onClick={() => setSelectedPlatform('all')}
              className={cn(
                'inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium transition-colors',
                activePlatform === 'all'
                  ? 'border-brand/50 bg-brand-soft text-brand-ink'
                  : 'border-border bg-card text-foreground hover:bg-muted/60'
              )}
            >
              <span>{t('All platforms')}</span>
              <span
                className={cn(
                  'font-mono text-[11px] tabular-nums',
                  activePlatform === 'all'
                    ? 'text-brand-ink/80'
                    : 'text-muted-foreground'
                )}
              >
                {formatNumber(cards.length, locale)}
              </span>
            </button>

            {visiblePlatforms.map((platform) => {
              const isActive = activePlatform === platform.id
              const count = platformCounts.get(platform.id) ?? 0
              return (
                <button
                  key={platform.id}
                  type='button'
                  aria-pressed={isActive}
                  onClick={() => setSelectedPlatform(platform.id)}
                  className={cn(
                    'inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium transition-colors',
                    isActive
                      ? 'border-brand/50 bg-brand-soft text-brand-ink'
                      : 'border-border bg-card text-foreground hover:bg-muted/60'
                  )}
                >
                  {platform.lobeIcon && (
                    <span
                      className='inline-flex shrink-0 items-center'
                      aria-hidden='true'
                    >
                      {getLobeIcon(platform.lobeIcon, 14)}
                    </span>
                  )}
                  <span>{t(platform.labelKey)}</span>
                  <span
                    className={cn(
                      'font-mono text-[11px] tabular-nums',
                      isActive ? 'text-brand-ink/80' : 'text-muted-foreground'
                    )}
                  >
                    {formatNumber(count, locale)}
                  </span>
                </button>
              )
            })}
          </div>

          {/* Compact pill sort toggles */}
          <div
            role='group'
            aria-label={t('Sort channels')}
            className='flex flex-wrap items-center gap-1.5 lg:justify-end'
          >
            {SORT_OPTIONS.map((option) => {
              const isActive = sortKey === option.key
              const effectiveDir = isActive
                ? sortDirection
                : DEFAULT_SORT_DIRECTIONS[option.key]
              const dirLabel =
                effectiveDir === 'asc'
                  ? t(option.ascLabelKey)
                  : t(option.descLabelKey)
              const SortChevron =
                isActive && effectiveDir === 'asc' ? ChevronUp : ChevronDown

              return (
                <button
                  key={option.key}
                  type='button'
                  aria-pressed={isActive}
                  onClick={() => handleSortToggle(option.key)}
                  className={cn(
                    'inline-flex items-center gap-1 rounded-full border px-2.5 py-1.5 text-xs transition-colors',
                    isActive
                      ? 'border-brand/50 bg-brand-soft text-brand-ink font-medium'
                      : 'border-border bg-card text-muted-foreground hover:bg-muted/60 hover:text-foreground'
                  )}
                >
                  <span>{t(option.labelKey)}</span>
                  <span
                    className={cn(
                      'text-[11px]',
                      isActive
                        ? 'text-brand-ink/85'
                        : 'text-muted-foreground/80'
                    )}
                  >
                    {dirLabel}
                  </span>
                  <SortChevron className='size-3 shrink-0' aria-hidden='true' />
                </button>
              )
            })}
          </div>
        </div>

        {/* 4-Column Group Card Grid */}
        {filteredAndSortedCards.length === 0 ? (
          <EmptyState
            bordered
            icon={Activity}
            title={t('No channels found')}
            description={t('No channels match the selected platform filter.')}
            action={
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => setSelectedPlatform('all')}
              >
                {t('Clear filters')}
              </Button>
            }
          />
        ) : (
          <div className='grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4'>
            {filteredAndSortedCards.map((item) => (
              <ChannelStatusCard
                key={item.key}
                item={item}
                canCreateApiKey={canCreateApiKey}
                onCreateApiKey={handleCreateApiKey}
              />
            ))}
          </div>
        )}
      </div>
    )
  }

  return (
    <SectionPageLayout stackActionsOnMobile>
      <SectionPageLayout.Title>{t('Channel Status')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t(
          'Only shows service health overview; does not expose request counts, per-request logs, or internal channel info.'
        )}
      </SectionPageLayout.Description>
      <SectionPageLayout.Actions>
        <AutoRefreshControls
          aggregateHealth={aggregateHealth}
          intervalSeconds={intervalSeconds}
          isFetching={isFetching}
          refreshCycleKey={refreshCycleKey}
          onIntervalChange={setIntervalSeconds}
          onRefresh={handleManualRefresh}
        />
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{bodyContent}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
