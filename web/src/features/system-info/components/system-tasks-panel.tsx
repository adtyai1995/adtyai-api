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
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { listSystemTasks } from '@/features/system-settings/api'
import { createServerError } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { SystemTaskHistory } from './system-task-history'
import { SystemTasksTable } from './system-tasks-table'

const ACTIVE_POLL_INTERVAL_MS = 8000

export function SystemTasksPanel() {
  const { t } = useTranslation()
  const tasksQuery = useQuery({
    queryKey: ['system-info', 'system-tasks', 'active'],
    queryFn: async () => {
      const res = await listSystemTasks(100, { scope: 'active' })
      if (!res.success || !Array.isArray(res.data)) {
        throw createServerError(res, t('We could not load system tasks.'))
      }
      return res.data
    },
    staleTime: 30 * 1000,
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.length ? ACTIVE_POLL_INTERVAL_MS : false,
  })

  const activeTasks = tasksQuery.data ?? []
  const loading = tasksQuery.isLoading
  const refreshing = tasksQuery.isFetching && !loading
  const hasActiveTasks = activeTasks.length > 0

  return (
    <div className='flex flex-col gap-8'>
      <ContentSection
        title={
          <span className='inline-flex items-center gap-2'>
            <span>{t('Active Tasks')}</span>
            <Badge variant='outline' className='tabular-nums'>
              {activeTasks.length}
            </Badge>
          </span>
        }
        description={t('Tasks currently pending or running.')}
        actions={
          <>
            <span
              className='text-dim inline-flex items-center gap-1.5 text-xs'
              aria-live='polite'
            >
              <span
                className={cn(
                  'size-1.5 rounded-full',
                  hasActiveTasks ? 'bg-success' : 'bg-muted-foreground/40'
                )}
                aria-hidden='true'
              />
              {hasActiveTasks
                ? t('Auto-refreshing every {{seconds}}s', {
                    seconds: ACTIVE_POLL_INTERVAL_MS / 1000,
                  })
                : t('Live refresh pauses when no task is running')}
            </span>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => void tasksQuery.refetch()}
              disabled={tasksQuery.isFetching}
              aria-label={t('Refresh')}
            >
              <RefreshCw
                data-icon='inline-start'
                className={cn('size-3.5', refreshing && 'animate-spin')}
                aria-hidden='true'
              />
              {refreshing ? t('Refreshing...') : t('Refresh')}
            </Button>
          </>
        }
      >
        <div aria-busy={tasksQuery.isFetching}>
          {loading && (
            <div className='space-y-2'>
              <Skeleton className='h-9 w-full rounded-md' />
              <Skeleton className='h-9 w-full rounded-md' />
              <Skeleton className='h-9 w-full rounded-md' />
            </div>
          )}
          {!loading && tasksQuery.isError && (
            <ErrorState
              title={t('We could not load system tasks.')}
              description={
                tasksQuery.error instanceof Error
                  ? tasksQuery.error.message
                  : undefined
              }
              onRetry={() => {
                void tasksQuery.refetch()
              }}
              className='min-h-[200px]'
            />
          )}
          {!loading &&
            !tasksQuery.isError &&
            (activeTasks.length > 0 ? (
              <SystemTasksTable tasks={activeTasks} />
            ) : (
              <div className='border-border text-dim rounded-[14px] border border-dashed px-4 py-6 text-center text-sm'>
                {t('No active system tasks.')}
              </div>
            ))}
        </div>
      </ContentSection>

      <SystemTaskHistory activeRefreshAt={tasksQuery.dataUpdatedAt} />
    </div>
  )
}
