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
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { formatLogQuota } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getLogStats, getUserLogStats } from '../api'
import { DEFAULT_LOG_STATS } from '../constants'
import { buildApiParams } from '../lib/utils'
import { useLogsViewScope, useUsageLogsContext } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/usage-logs/$section')

function StatBadge(props: { label: string; value: string | number }) {
  return (
    <span className='inline-flex h-7 items-center gap-2 px-2.5'>
      <span className='text-faint text-[11.5px] leading-none'>
        {props.label}
      </span>
      <span className='text-foreground font-mono text-xs font-semibold tabular-nums'>
        {props.value}
      </span>
    </span>
  )
}

export function CommonLogsStats() {
  const { t } = useTranslation()
  const { isAdminView: isAdmin } = useLogsViewScope()
  const searchParams = route.useSearch()
  const { sensitiveVisible } = useUsageLogsContext()

  const {
    data: stats,
    isLoading,
    isFetching,
    isError,
    refetch,
  } = useQuery({
    queryKey: ['usage-logs-stats', isAdmin, searchParams],
    queryFn: async () => {
      const params = buildApiParams({
        page: 1,
        pageSize: 1,
        searchParams,
        columnFilters: [],
        isAdmin,
      })

      const result = isAdmin
        ? requireServerSuccess(await getLogStats(params))
        : requireServerSuccess(await getUserLogStats(params))

      return result.data || DEFAULT_LOG_STATS
    },
    placeholderData: (previousData, previousQuery) => {
      if (previousQuery?.queryKey[1] === isAdmin) {
        return previousData
      }
      return undefined
    },
  })

  if (isLoading || (isFetching && !stats && !isError)) {
    return (
      <div className='border-border flex overflow-hidden rounded-[8px] border'>
        {['usage', 'rpm', 'tpm'].map((key) => (
          <div
            key={key}
            className='border-border flex h-7 items-center gap-2 border-r px-2.5 last:border-r-0'
          >
            <Skeleton className='h-2.5 w-8' />
            <Skeleton className='h-3 w-12' />
          </div>
        ))}
      </div>
    )
  }

  if (isError || !stats) {
    return (
      <div
        role='alert'
        className='border-destructive/40 bg-destructive/5 text-destructive inline-flex h-7 max-w-full items-center gap-2 rounded-[8px] border px-2.5 text-xs'
      >
        <span>{t('Failed to load')}</span>
        <Button
          type='button'
          variant='ghost'
          size='xs'
          className='h-5 px-1.5 text-xs'
          disabled={isFetching}
          onClick={() => void refetch()}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  }

  // One quiet strip of cells separated by hairlines (design system §4).
  return (
    <div className='border-border divide-border inline-flex max-w-full items-stretch divide-x overflow-hidden rounded-[8px] border'>
      <StatBadge
        label={t('Usage')}
        value={sensitiveVisible ? formatLogQuota(stats.quota || 0) : '••••'}
      />
      <StatBadge label={t('RPM')} value={stats.rpm || 0} />
      <StatBadge label={t('TPM')} value={stats.tpm || 0} />
    </div>
  )
}
