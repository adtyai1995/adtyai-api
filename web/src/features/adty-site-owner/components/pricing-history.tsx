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
import { ChevronLeft, ChevronRight, History } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { getPricingHistory, OWNER_PAGE_SIZE, siteOwnerKeys } from '../api'

export function PricingHistory(props: { customerId?: number }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: siteOwnerKeys.list('pricing-history', { customerId: props.customerId ?? 0, page }),
    queryFn: () => getPricingHistory(page, props.customerId),
    enabled: open,
    meta: { errorToast: false },
  })
  const labels: Record<string, string> = { immediate: t('Effective immediately'), scheduled: t('Scheduled'), cancelled: t('Cancelled'), deleted: t('Removed'), activated: t('Price activated'), deletion_activated: t('Override removal activated') }
  return (
    <section className='space-y-3'>
      <Button variant='outline' type='button' aria-expanded={open} onClick={() => setOpen(!open)}><History aria-hidden='true' />{t('Price change history')}</Button>
      {open && query.isPending && <LoadingState />}
      {open && query.isError && <ErrorState description={getServerErrorMessage(query.error, t('Failed to load data'))} onRetry={() => void query.refetch()} />}
      {open && query.isSuccess && <>
        <StaticDataTable data={query.data.items ?? []} getRowKey={(row) => row.id} columns={[
          { id: 'time', header: t('Time'), cell: (row) => formatTimestamp(row.created_at) },
          { id: 'actor', header: t('Operator'), cell: (row) => row.actor_id ? formatUserIdentity(row.actor_id, row.actor_username) : t('System') },
          { id: 'scope', header: t('Scope'), cell: (row) => row.model || row.group || t('Default') },
          { id: 'value', header: t('Price change'), cell: (row) => `${row.before} → ${row.after}` },
          { id: 'action', header: t('Status'), cell: (row) => labels[row.action] ?? row.action },
          { id: 'effective', header: t('Effective time'), cell: (row) => row.effective_at ? formatTimestamp(row.effective_at) : '-' },
        ]} />
        <div className='flex items-center justify-end gap-2'>
          <Button size='icon' variant='outline' aria-label={t('Previous page')} disabled={page === 1} onClick={() => setPage(page - 1)}><ChevronLeft /></Button>
          <span className='text-muted-foreground text-sm'>{page}</span>
          <Button size='icon' variant='outline' aria-label={t('Next page')} disabled={page * OWNER_PAGE_SIZE >= query.data.total} onClick={() => setPage(page + 1)}><ChevronRight /></Button>
        </div>
      </>}
    </section>
  )
}
