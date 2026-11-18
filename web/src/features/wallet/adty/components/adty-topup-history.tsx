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
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'

import { adtyTopupQueryKeys, getAdtyTopupHistory } from '../api'
import {
  METHOD_LABELS,
  methodLabel,
  ORDER_STATUS_LABELS,
  ORDER_STATUS_VARIANTS,
} from '../lib/methods'
import { formatUsd } from '../lib/money'
import type { AdtyOrderStatus, AdtyTopupHistoryFilters } from '../types'

const PAGE_SIZE = 10
const STATUS_OPTIONS = Object.keys(ORDER_STATUS_LABELS) as AdtyOrderStatus[]
const METHOD_OPTIONS = Object.keys(METHOD_LABELS)

function dateInputToUnixSeconds(
  value: string,
  endOfDay: boolean
): number | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const suffix = endOfDay ? 'T23:59:59' : 'T00:00:00'
  const parsed = new Date(`${trimmed}${suffix}`)
  const ms = parsed.getTime()
  if (!Number.isFinite(ms)) return undefined
  return Math.floor(ms / 1000)
}

/** The user's own top-up orders with face value, bonus, payment and status. */
export function AdtyTopupHistory() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [paymentMethod, setPaymentMethod] = useState('')
  const [tradeNo, setTradeNo] = useState('')
  const [startDate, setStartDate] = useState('')
  const [endDate, setEndDate] = useState('')

  const startTimestamp = dateInputToUnixSeconds(startDate, false)
  const endTimestamp = dateInputToUnixSeconds(endDate, true)
  const filters: AdtyTopupHistoryFilters = {
    ...(status ? { status } : {}),
    ...(paymentMethod ? { payment_method: paymentMethod } : {}),
    ...(tradeNo.trim() ? { trade_no: tradeNo.trim() } : {}),
    ...(startTimestamp !== undefined
      ? { start_timestamp: startTimestamp }
      : {}),
    ...(endTimestamp !== undefined ? { end_timestamp: endTimestamp } : {}),
  }
  const hasActiveFilters = Boolean(
    status || paymentMethod || tradeNo.trim() || startDate || endDate
  )

  const query = useQuery({
    queryKey: adtyTopupQueryKeys.history(page, filters),
    queryFn: () => getAdtyTopupHistory(page, PAGE_SIZE, filters),
  })
  const items = query.data?.items ?? []
  const total = query.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  if (query.isError && !query.data && !hasActiveFilters) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }

  return (
    <Card id='wallet-history' className='scroll-mt-4'>
      <CardHeader>
        <CardTitle>{t('Top-up history')}</CardTitle>
        <CardDescription>
          {t('Orders paid after they expire are still credited.')}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        <div className='flex flex-wrap items-end gap-2'>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='topup-history-status'>{t('Status')}</Label>
            <NativeSelect
              id='topup-history-status'
              aria-label={t('Status')}
              value={status}
              onChange={(event) => {
                setStatus(event.target.value)
                setPage(1)
              }}
            >
              <NativeSelectOption value=''>
                {t('All statuses', { defaultValue: 'All statuses' })}
              </NativeSelectOption>
              {STATUS_OPTIONS.map((value) => (
                <NativeSelectOption key={value} value={value}>
                  {t(ORDER_STATUS_LABELS[value])}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='topup-history-method'>{t('Payment method')}</Label>
            <NativeSelect
              id='topup-history-method'
              aria-label={t('Payment method')}
              value={paymentMethod}
              onChange={(event) => {
                setPaymentMethod(event.target.value)
                setPage(1)
              }}
            >
              <NativeSelectOption value=''>
                {t('All methods', { defaultValue: 'All methods' })}
              </NativeSelectOption>
              {METHOD_OPTIONS.map((value) => (
                <NativeSelectOption key={value} value={value}>
                  {t(METHOD_LABELS[value])}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='topup-history-trade-no'>{t('Order number')}</Label>
            <Input
              id='topup-history-trade-no'
              aria-label={t('Order number')}
              value={tradeNo}
              placeholder={t('Order number')}
              onChange={(event) => {
                setTradeNo(event.target.value)
                setPage(1)
              }}
            />
          </div>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='topup-history-start-date'>
              {t('Start date', { defaultValue: 'Start date' })}
            </Label>
            <Input
              id='topup-history-start-date'
              aria-label={t('Start date', { defaultValue: 'Start date' })}
              type='date'
              value={startDate}
              onChange={(event) => {
                setStartDate(event.target.value)
                setPage(1)
              }}
            />
          </div>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='topup-history-end-date'>
              {t('End date', { defaultValue: 'End date' })}
            </Label>
            <Input
              id='topup-history-end-date'
              aria-label={t('End date', { defaultValue: 'End date' })}
              type='date'
              value={endDate}
              onChange={(event) => {
                setEndDate(event.target.value)
                setPage(1)
              }}
            />
          </div>
          {hasActiveFilters && (
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => {
                setStatus('')
                setPaymentMethod('')
                setTradeNo('')
                setStartDate('')
                setEndDate('')
                setPage(1)
              }}
            >
              {t('Reset', { defaultValue: 'Reset' })}
            </Button>
          )}
        </div>
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        <StaticDataTable
          tableClassName='min-w-[720px]'
          tableProps={{ 'aria-label': t('Top-up history') }}
          data={items}
          getRowKey={(row) => row.trade_no}
          emptyContent={
            <EmptyState
              className='min-h-32'
              title={query.isLoading ? t('Loading...') : t('No top-ups yet')}
            />
          }
          columns={[
            {
              id: 'time',
              header: t('Time'),
              cell: (row) => formatTimestamp(row.create_time),
            },
            {
              id: 'method',
              header: t('Payment method'),
              cell: (row) =>
                row.ext?.method_label || t(methodLabel(row.payment_method)),
            },
            {
              id: 'scene',
              header: t('Payment scene'),
              cell: (row) =>
                row.ext?.payment_scene
                  ? `${row.ext.payment_scene_name || row.ext.payment_scene} (${row.ext.payment_scene}) · v${row.ext.payment_scene_version}`
                  : '—',
            },
            {
              id: 'face',
              header: t('Amount'),
              className: 'text-right',
              cellClassName: 'text-right tabular-nums',
              cell: (row) =>
                row.ext ? formatUsd(Number(row.ext.face_usd), locale) : '—',
            },
            {
              id: 'bonus',
              header: t('Bonus'),
              className: 'text-right',
              cellClassName: 'text-right tabular-nums',
              cell: (row) => {
                if (!row.ext) return '—'
                return formatUsd(Number(row.ext.bonus_usd), locale)
              },
            },
            {
              id: 'paid',
              header: t('Paid'),
              className: 'text-right',
              cellClassName: 'text-right tabular-nums',
              cell: (row) => {
                if (!row.ext) return '—'
                const paid = Number(row.ext.paid_amount)
                const value =
                  paid > 0 ? row.ext.paid_amount : row.ext.pay_amount
                return `${value} ${row.ext.paid_currency || row.ext.pay_currency}`
              },
            },
            {
              id: 'status',
              header: t('Status'),
              cell: (row) => (
                <StatusBadge
                  label={t(ORDER_STATUS_LABELS[row.status] ?? row.status)}
                  variant={ORDER_STATUS_VARIANTS[row.status] ?? 'neutral'}
                  copyable={false}
                />
              ),
            },
            {
              id: 'trade_no',
              header: t('Order number'),
              cell: (row) => (
                <span className='flex items-center gap-2'>
                  <code className='font-mono text-xs'>{row.trade_no}</code>
                  <Link
                    to='/profile'
                    hash={`ticket-${row.trade_no}`}
                    className='text-muted-foreground text-xs'
                  >
                    {t('Submit ticket')}
                  </Link>
                </span>
              ),
            },
          ]}
        />
        {pages > 1 && (
          <div className='flex items-center justify-end gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={page <= 1}
              onClick={() => setPage((p) => p - 1)}
            >
              {t('Previous')}
            </Button>
            <span className='text-muted-foreground text-sm'>
              {page} / {pages}
            </span>
            <Button
              variant='outline'
              size='sm'
              disabled={page >= pages}
              onClick={() => setPage((p) => p + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
