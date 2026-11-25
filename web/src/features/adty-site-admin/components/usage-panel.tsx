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
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

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
    <div className='flex max-w-3xl flex-col gap-4'>
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      <Card>
        <CardHeader>
          <CardTitle>{t('Turnstile widgets')}</CardTitle>
        </CardHeader>
        <CardContent className='grid gap-2 text-sm'>
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
                cell: (row) => `${row.used} / ${row.limit}`,
              },
            ]}
          />
          {usage.unassigned_domains > 0 && (
            <p className='text-muted-foreground text-xs'>
              {t(
                '{{count}} custom domains have no widget; sign-up and sign-in on them are verified on the platform subdomain.',
                { count: usage.unassigned_domains }
              )}
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
