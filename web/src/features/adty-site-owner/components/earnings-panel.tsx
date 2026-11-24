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
import { useMutation, useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import {
  downloadExport,
  getOwnerEarningLines,
  siteOwnerKeys,
  type ExportKind,
  type OwnerEarningLine,
} from '../api'
import { isNegativeAmount } from '../lib/site-owner'

const EARNING_LINES_PAGE_SIZE = 50
const EMPTY_EARNING_LINES: OwnerEarningLine[] = []

/**
 * Single-request earning details (R-33 #1): shows per-request earning lines
 * with model/request/group filters and CSV exports.
 */
export function EarningsPanel() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const [searchText, setSearchText] = useState('')
  const [groupFilter, setGroupFilter] = useState('')
  const lines = useQuery({
    queryKey: siteOwnerKeys.list('earning-lines', {
      page,
      pageSize: EARNING_LINES_PAGE_SIZE,
    }),
    queryFn: () => getOwnerEarningLines(page, EARNING_LINES_PAGE_SIZE),
  })
  const exporter = useMutation({
    mutationFn: (exportKind: ExportKind) => downloadExport(exportKind),
    onError: (err) => handleServerError(err),
  })

  const rawRows = lines.data ?? EMPTY_EARNING_LINES
  const groupOptions = useMemo(() => {
    const groups = new Set<string>()
    for (const row of rawRows) {
      if (row.group) groups.add(row.group)
    }
    return [...groups].sort((a, b) => a.localeCompare(b))
  }, [rawRows])

  const filteredRows = useMemo(() => {
    const needle = searchText.trim().toLowerCase()
    return rawRows.filter((row) => {
      if (groupFilter && row.group !== groupFilter) {
        return false
      }
      if (!needle) {
        return true
      }
      return (
        row.model_name.toLowerCase().includes(needle) ||
        row.request_id.toLowerCase().includes(needle) ||
        String(row.user_id ?? '').includes(needle)
      )
    })
  }, [rawRows, searchText, groupFilter])

  const hasFilters = searchText.trim() !== '' || groupFilter !== ''
  const money = (value: string | undefined) =>
    value == null || value === '' ? '—' : formatUsd(Number(value), locale)
  const amountCell = (value: string | undefined) => (
    <span
      data-negative={isNegativeAmount(value) ? 'true' : undefined}
      className={cn(isNegativeAmount(value) && 'text-destructive')}
    >
      {money(value)}
    </span>
  )

  if (lines.isError && !lines.data) {
    return <ErrorState onRetry={() => void lines.refetch()} />
  }
  if (lines.isPending) {
    return <LoadingState />
  }

  return (
    <div className='flex flex-col gap-4'>
      {lines.isError && <ErrorState onRetry={() => void lines.refetch()} />}
      <div className='flex flex-col gap-1'>
        <h2 className='text-sm font-medium'>{t('Request earnings')}</h2>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Each request shows the model and billing group. Upstream channel names are not shown.'
          )}
        </p>
      </div>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex flex-wrap items-center gap-2'>
          <Input
            aria-label={t('Filter by model or request ID')}
            placeholder={t('Filter by model or request ID')}
            className='w-64'
            value={searchText}
            onChange={(event) => setSearchText(event.target.value)}
          />
          <NativeSelect
            aria-label={t('Filter by group')}
            value={groupFilter}
            onChange={(event) => setGroupFilter(event.target.value)}
          >
            <NativeSelectOption value=''>{t('All groups')}</NativeSelectOption>
            {groupOptions.map((group) => (
              <NativeSelectOption key={group} value={group}>
                {group}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          {hasFilters && (
            <Button
              type='button'
              size='sm'
              variant='ghost'
              onClick={() => {
                setSearchText('')
                setGroupFilter('')
              }}
            >
              {t('Clear')}
            </Button>
          )}
        </div>
        <div className='flex flex-wrap items-center gap-2'>
          {(
            [
              ['earnings', t('Export earnings')],
              ['stats', t('Export report')],
              ['customers', t('Export customers')],
            ] as const
          ).map(([exportKind, label]) => (
            <Button
              key={exportKind}
              type='button'
              size='sm'
              variant='outline'
              disabled={exporter.isPending}
              onClick={() => exporter.mutate(exportKind)}
            >
              {label}
            </Button>
          ))}
        </div>
      </div>
      <StaticDataTable
        tableClassName='min-w-[720px]'
        tableProps={{ 'aria-label': t('Request earnings') }}
        data={filteredRows}
        getRowKey={(row) => row.id}
        emptyContent={
          <EmptyState className='min-h-32' title={t('No earnings yet')} />
        }
        columns={[
          {
            id: 'time',
            header: t('Time'),
            cell: (row) =>
              row.created_at > 0 ? formatTimestamp(row.created_at) : '—',
          },
          {
            id: 'user',
            header: t('Customer ID'),
            cell: (row) => (row.user_id ? row.user_id : '—'),
          },
          { id: 'model', header: t('Model'), cell: (row) => row.model_name },
          { id: 'group', header: t('Group'), cell: (row) => row.group || '—' },
          {
            id: 'request',
            header: t('Request'),
            cell: (row) => (
              <span className='font-mono text-xs'>{row.request_id || '—'}</span>
            ),
          },
          {
            id: 'revenue',
            header: t('Customer spend'),
            cell: (row) => amountCell(row.revenue_usd),
          },
          {
            id: 'earning',
            header: t('Your earnings'),
            cell: (row) => amountCell(row.earning_usd),
          },
        ]}
      />
      {(page > 1 || rawRows.length >= EARNING_LINES_PAGE_SIZE) && (
        <div className='flex items-center gap-2'>
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={page <= 1 || lines.isFetching}
            onClick={() => setPage((current) => Math.max(1, current - 1))}
          >
            {t('Previous page')}
          </Button>
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={
              rawRows.length < EARNING_LINES_PAGE_SIZE || lines.isFetching
            }
            onClick={() => setPage((current) => current + 1)}
          >
            {t('Next page')}
          </Button>
        </div>
      )}
    </div>
  )
}
