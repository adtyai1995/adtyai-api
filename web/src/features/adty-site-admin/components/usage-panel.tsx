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
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'

import { getResellerUsage, siteAdminKeys } from '../api'

/** Turnstile widget slots (task 11.8). Read-only. */
export function UsagePanel() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: siteAdminKeys.list('usage', {}),
    queryFn: getResellerUsage,
  })
  const usage = query.data
  if (query.isError && !usage) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  if (!usage) return null

  return (
    <ContentSection
      title={t('Turnstile widgets')}
      description={
        usage.unassigned_domains > 0
          ? t(
              '{{count}} custom domains have no widget; sign-up and sign-in on them are verified on the platform subdomain.',
              { count: usage.unassigned_domains }
            )
          : undefined
      }
    >
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {usage.widget_slots_low && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'Only {{count}} Turnstile domain slots are left. Add another widget before they run out.',
              { count: usage.widget_slots_left }
            )}
          </AlertDescription>
        </Alert>
      )}
      <div className='max-w-3xl'>
        <StaticDataTable
          tableProps={{ 'aria-label': t('Turnstile widgets') }}
          data={usage.widgets}
          getRowKey={(row) => row.id}
          emptyContent={
            <EmptyState
              className='min-h-16'
              title={t('No widgets configured')}
            />
          }
          columns={[
            {
              id: 'id',
              header: t('Widget'),
              cell: (row) => (
                <span className='font-mono text-xs'>{row.id}</span>
              ),
            },
            {
              id: 'used',
              header: t('Domains'),
              cellClassName: 'tabular-nums',
              cell: (row) => `${row.used} / ${row.limit}`,
            },
          ]}
        />
      </div>
    </ContentSection>
  )
}
