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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getFlaggedCustomers,
  getSiteReports,
  handleSiteReport,
  reviewFlaggedCustomer,
  SITE_ADMIN_PAGE_SIZE,
  siteAdminKeys,
} from '../api'

/** Flagged customers and owner reports. Resolving a report calls the review API. */
export function ReviewsPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [flaggedPage, setFlaggedPage] = useState(1)
  const [reportsPage, setReportsPage] = useState(1)
  const flagged = useQuery({
    queryKey: siteAdminKeys.list('flagged', { p: flaggedPage }),
    queryFn: () => getFlaggedCustomers(flaggedPage),
  })
  const reports = useQuery({
    queryKey: siteAdminKeys.list('reports', { p: reportsPage }),
    queryFn: () => getSiteReports(reportsPage),
  })
  useEffect(() => {
    if (!flagged.data) return
    const maxPage = Math.max(
      1,
      Math.ceil((flagged.data.total ?? 0) / SITE_ADMIN_PAGE_SIZE)
    )
    if (flaggedPage > maxPage) {
      setFlaggedPage(maxPage)
    }
  }, [flagged.data, flaggedPage])
  useEffect(() => {
    if (!reports.data) return
    const maxPage = Math.max(
      1,
      Math.ceil((reports.data.total ?? 0) / SITE_ADMIN_PAGE_SIZE)
    )
    if (reportsPage > maxPage) {
      setReportsPage(maxPage)
    }
  }, [reports.data, reportsPage])
  const review = useMutation({
    mutationFn: (userId: number) => reviewFlaggedCustomer(userId, true),
    onSuccess: () => {
      toast.success(t('Review this customer'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  const resolve = useMutation({
    mutationFn: (id: number) =>
      handleSiteReport(id, { status: 'resolved', note: '' }),
    onSuccess: () => {
      toast.success(t('Resolve report'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  const flaggedRows = Array.isArray(flagged.data?.items)
    ? flagged.data.items
    : []
  const reportRows = Array.isArray(reports.data?.items)
    ? reports.data.items
    : []
  const retry = () => {
    void flagged.refetch()
    void reports.refetch()
  }
  if (
    (flagged.isError && !flagged.data) ||
    (reports.isError && !reports.data)
  ) {
    return <ErrorState onRetry={retry} />
  }

  const riskLabel = (flag: string) => {
    if (flag === 'self_use') return t('Possible self-use')
    if (flag === 'migration') return t('Possible main site migration')
    return flag
  }

  return (
    <div className='flex flex-col gap-8'>
      {(flagged.isError || reports.isError) && <ErrorState onRetry={retry} />}
      <ContentSection title={t('Risk')}>
        <StaticDataTable
          tableProps={{ 'aria-label': t('Risk') }}
          data={flaggedRows}
          getRowKey={(row) => `${row.site_id}-${row.user_id}`}
          emptyContent={
            <EmptyState className='min-h-24' title={t('No data')} />
          }
          columns={[
            {
              id: 'user',
              header: t('User'),
              cellClassName: 'tabular-nums font-medium',
              cell: (row) => formatUserIdentity(row.user_id, row.username),
            },
            {
              id: 'reason',
              header: t('Reason'),
              cell: (row) => (
                <span className='text-dim text-[13px]'>
                  {row.risk_reason || riskLabel(row.risk_flag)}
                </span>
              ),
            },
            {
              id: 'actions',
              header: t('Actions'),
              className: 'text-right',
              cellClassName: 'text-right',
              cell: (row) => (
                <Button
                  size='sm'
                  variant='brand'
                  onClick={() => review.mutate(row.user_id)}
                  disabled={row.reviewed || review.isPending}
                >
                  {t('Review this customer')}
                </Button>
              ),
            },
          ]}
        />
        <PageSwitcher
          page={flaggedPage}
          total={flagged.data?.total ?? 0}
          pageSize={SITE_ADMIN_PAGE_SIZE}
          onPageChange={setFlaggedPage}
        />
      </ContentSection>
      <ContentSection title={t('Reports')}>
        <StaticDataTable
          tableProps={{ 'aria-label': t('Reports') }}
          data={reportRows}
          getRowKey={(row) => String(row.id)}
          emptyContent={
            <EmptyState className='min-h-24' title={t('No data')} />
          }
          columns={[
            {
              id: 'user',
              header: t('User'),
              cellClassName: 'tabular-nums font-medium',
              cell: (row) => formatUserIdentity(row.user_id, row.username),
            },
            {
              id: 'reason',
              header: t('Reason'),
              cell: (row) => (
                <span className='text-dim text-[13px]'>{row.reason}</span>
              ),
            },
            {
              id: 'actions',
              header: t('Actions'),
              className: 'text-right',
              cellClassName: 'text-right',
              cell: (row) => (
                <Button
                  size='sm'
                  variant='brand'
                  onClick={() => resolve.mutate(row.id)}
                  disabled={resolve.isPending}
                >
                  {t('Resolve report')}
                </Button>
              ),
            },
          ]}
        />
        <PageSwitcher
          page={reportsPage}
          total={reports.data?.total ?? 0}
          pageSize={SITE_ADMIN_PAGE_SIZE}
          onPageChange={setReportsPage}
        />
      </ContentSection>
    </div>
  )
}
