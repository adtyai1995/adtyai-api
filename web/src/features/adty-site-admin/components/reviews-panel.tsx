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

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
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

  return (
    <div className='flex flex-col gap-6'>
      {(flagged.isError || reports.isError) && <ErrorState onRetry={retry} />}
      <section className='flex flex-col gap-2'>
        <h3 className='text-sm font-medium'>{t('Risk')}</h3>
        {flaggedRows.length === 0 ? (
          <EmptyState className='min-h-24' title={t('No data')} />
        ) : (
          <ul>
            {flaggedRows.map((row) => (
              <li
                key={`${row.site_id}-${row.user_id}`}
                className='flex items-center justify-between gap-2'
              >
                <span>{row.risk_reason || row.risk_flag}</span>
                <Button
                  size='sm'
                  onClick={() => review.mutate(row.user_id)}
                  disabled={row.reviewed || review.isPending}
                >
                  {t('Review this customer')}
                </Button>
              </li>
            ))}
          </ul>
        )}
        <PageSwitcher
          page={flaggedPage}
          total={flagged.data?.total ?? 0}
          pageSize={SITE_ADMIN_PAGE_SIZE}
          onPageChange={setFlaggedPage}
        />
      </section>
      <section className='flex flex-col gap-2'>
        <h3 className='text-sm font-medium'>{t('Reports')}</h3>
        {reportRows.length === 0 ? (
          <EmptyState className='min-h-24' title={t('No data')} />
        ) : (
          <ul className='flex flex-col gap-2'>
            {reportRows.map((row) => (
              <li
                key={row.id}
                className='flex items-center justify-between gap-2'
              >
                <span>{row.reason}</span>
                <Button
                  size='sm'
                  onClick={() => resolve.mutate(row.id)}
                  disabled={resolve.isPending}
                >
                  {t('Resolve report')}
                </Button>
              </li>
            ))}
          </ul>
        )}
        <PageSwitcher
          page={reportsPage}
          total={reports.data?.total ?? 0}
          pageSize={SITE_ADMIN_PAGE_SIZE}
          onPageChange={setReportsPage}
        />
      </section>
    </div>
  )
}
