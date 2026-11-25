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
import { LoadingState } from '@/components/loading-state'

import { getSitesOverview, siteAdminKeys } from '../api'

/** Totals for every reseller site, including hostname cost and settlement holds. */
export function OverviewPanel() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: siteAdminKeys.list('overview', {}),
    queryFn: getSitesOverview,
  })
  if (query.isLoading) return <LoadingState />
  if (query.isError && !query.data) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  const data = query.data
  if (!data) {
    return <EmptyState className='min-h-48' title={t('No data')} />
  }
  const stats = [
    { label: t('Revenue'), value: data.site_revenue_usd },
    { label: t('Earnings'), value: data.site_earning_usd },
    { label: t('Top-up'), value: data.site_topup_usd },
    { label: t('Custom hostnames'), value: String(data.custom_hostnames) },
    {
      label: t('Hostname monthly fee'),
      value: data.hostname_monthly_fee_usd,
    },
    {
      label: t('Earning hook failures'),
      value: String(data.earning_hook_failures),
    },
  ]
  return (
    <div className='flex flex-col gap-8'>
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      <dl
        aria-label={t('Overview')}
        className='border-border bg-border grid grid-cols-2 gap-px overflow-hidden rounded-[14px] border lg:grid-cols-3'
      >
        {stats.map((item) => (
          <div
            key={item.label}
            className='bg-card flex flex-col gap-2 px-5 py-4'
          >
            <dt className='text-dim text-[12px] leading-none'>{item.label}</dt>
            <dd className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
              {item.value}
            </dd>
          </div>
        ))}
      </dl>
      <ContentSection title={t('Settlement holds')}>
        <StaticDataTable
          tableProps={{ 'aria-label': t('Settlement holds') }}
          data={data.settlement_holds}
          getRowKey={(hold) => String(hold.site_id)}
          emptyContent={
            <EmptyState className='min-h-24' title={t('No data')} />
          }
          columns={[
            {
              id: 'site',
              header: t('Site'),
              cell: (hold) => (
                <span className='font-mono text-sm font-medium'>
                  {hold.subdomain}
                </span>
              ),
            },
            {
              id: 'reason',
              header: t('Reason'),
              cell: (hold) => (
                <span className='text-dim text-[13px]'>
                  {hold.settle_hold_reason}
                </span>
              ),
            },
          ]}
        />
      </ContentSection>
    </div>
  )
}
