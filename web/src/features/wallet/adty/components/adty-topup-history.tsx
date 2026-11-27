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
import { Link } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { sceneBusinessLabels } from '@/features/adty-payment-scenes/schema'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  adtyTopupQueryKeys,
  cancelAdtyOrder,
  getAdtyTopupHistory,
} from '../api'
import { useNowSeconds } from '../lib/countdown'
import {
  customerMethodLabel,
  METHOD_LABELS,
  methodLabel,
  ORDER_STATUS_LABELS,
  ORDER_STATUS_VARIANTS,
} from '../lib/methods'
import { formatUsd, giftFulfillmentStatusLabelKey } from '../lib/money'
import { clearPendingPayment, loadPendingPayment } from '../lib/pending-payment'
import type {
  AdtyOrder,
  AdtyOrderStatus,
  AdtyTopupBusiness,
  AdtyTopupHistoryFilters,
} from '../types'

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
export function AdtyTopupHistory(props: {
  business?: AdtyTopupBusiness
  onResumeOrder?: (order: AdtyOrder) => void
  onOrderChanged?: () => void
}) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const fieldId = useId()
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
    business: props.business ?? 'wallet_topup',
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
  const hasPendingRows = items.some((row) => row.status === 'pending')
  const nowSec = useNowSeconds(hasPendingRows)

  const cancelMutation = useMutation({
    mutationFn: (orderTradeNo: string) => cancelAdtyOrder(orderTradeNo),
    onSuccess: (_data, orderTradeNo) => {
      try {
        if (loadPendingPayment(window.localStorage)?.tradeNo === orderTradeNo) {
          clearPendingPayment(window.localStorage)
        }
      } catch {
        // Ignore storage errors in restricted contexts.
      }
      toast.success(t('Order canceled'))
      void queryClient.invalidateQueries({ queryKey: ['adty-topup'] })
      props.onOrderChanged?.()
    },
    onError: (error) => handleServerError(error),
  })

  if (query.isError && !query.data && !hasActiveFilters) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }

  let historyTitle = t('Top-up history')
  if (props.business === 'owner_margin_topup') {
    historyTitle = t('Owner margin top-up history')
  } else if (props.business === 'owner_self_use_topup') {
    historyTitle = t('Owner self-use top-up history')
  }

  return (
    <div
      id={
        props.business === 'owner_margin_topup'
          ? 'margin-topup-history'
          : 'wallet-history'
      }
      className='scroll-mt-4'
    >
      <ContentSection title={historyTitle}>
        <div className='flex flex-wrap items-end gap-2'>
          <div className='flex flex-col gap-1'>
            <Label htmlFor={`${fieldId}-status`}>{t('Status')}</Label>
            <NativeSelect
              id={`${fieldId}-status`}
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
            <Label htmlFor={`${fieldId}-method`}>{t('Payment method')}</Label>
            <NativeSelect
              id={`${fieldId}-method`}
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
            <Label htmlFor={`${fieldId}-trade-no`}>{t('Order number')}</Label>
            <Input
              id={`${fieldId}-trade-no`}
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
            <Label htmlFor={`${fieldId}-start-date`}>
              {t('Start date', { defaultValue: 'Start date' })}
            </Label>
            <Input
              id={`${fieldId}-start-date`}
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
            <Label htmlFor={`${fieldId}-end-date`}>
              {t('End date', { defaultValue: 'End date' })}
            </Label>
            <Input
              id={`${fieldId}-end-date`}
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
                row.ext?.method_label ||
                t(
                  props.business === 'owner_margin_topup'
                    ? methodLabel(row.payment_method)
                    : customerMethodLabel(row.payment_method)
                ),
            },
            {
              id: 'scene',
              header: t('Payment scene'),
              cell: (row) => {
                const sceneKey = row.ext?.payment_scene
                if (!sceneKey) return '—'
                const rawName = row.ext?.payment_scene_name?.trim()
                const labelKey =
                  (rawName && sceneBusinessLabels[rawName]) ||
                  rawName ||
                  sceneBusinessLabels[sceneKey] ||
                  sceneKey
                return `${t(labelKey)} (${sceneKey}) · v${row.ext?.payment_scene_version}`
              },
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
                const bonusAmount = formatUsd(
                  Number(row.ext.bonus_usd),
                  locale
                )
                const labelKey = giftFulfillmentStatusLabelKey(
                  row.ext.gift_fulfillment_status
                )
                if (!labelKey || Number(row.ext.bonus_usd) <= 0) {
                  return bonusAmount
                }
                return (
                  <div className='flex flex-col items-end gap-0.5'>
                    <span>{bonusAmount}</span>
                    <span className='text-muted-foreground text-xs'>
                      {t(labelKey)}
                    </span>
                  </div>
                )
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
              cell: (row) => {
                const effectiveStatus =
                  row.status === 'pending' &&
                  row.ext?.expire_at &&
                  row.ext.expire_at <= nowSec
                    ? 'expired'
                    : row.status
                return (
                  <StatusBadge
                    label={t(
                      ORDER_STATUS_LABELS[effectiveStatus] ?? effectiveStatus
                    )}
                    variant={ORDER_STATUS_VARIANTS[effectiveStatus] ?? 'neutral'}
                    copyable={false}
                  />
                )
              },
            },
            {
              id: 'trade_no',
              header: t('Order number'),
              cell: (row) => (
                <span className='flex items-center gap-2'>
                  <code className='font-mono text-xs'>{row.trade_no}</code>
                  <Link
                    to='/support'
                    hash={`ticket:${row.ext?.team_id ? 'team_order' : 'topup'}:${encodeURIComponent(row.trade_no)}`}
                    className='text-muted-foreground text-xs'
                  >
                    {t('Submit ticket')}
                  </Link>
                  {(row.status === 'refunded' ||
                    (row.ext?.refunded_at ?? 0) > 0) && (
                    <Link
                      to='/support'
                      hash={`ticket:${row.ext?.team_id ? 'team_refund' : 'refund'}:${encodeURIComponent(row.trade_no)}`}
                      className='text-muted-foreground text-xs'
                    >
                      {t('Refund inquiry')}
                    </Link>
                  )}
                </span>
              ),
            },
            {
              id: 'actions',
              header: t('Actions'),
              className: 'text-right',
              cellClassName: 'text-right',
              cell: (row) => {
                const actionable =
                  row.status === 'pending' &&
                  (!row.ext?.expire_at || row.ext.expire_at > nowSec)
                if (!actionable) return null
                return (
                  <div className='flex items-center justify-end gap-1.5 whitespace-nowrap'>
                    {props.onResumeOrder && (
                      <Button
                        type='button'
                        size='sm'
                        disabled={cancelMutation.isPending}
                        onClick={() => props.onResumeOrder?.(row)}
                      >
                        {t('Continue payment')}
                      </Button>
                    )}
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      disabled={cancelMutation.isPending}
                      onClick={() => cancelMutation.mutate(row.trade_no)}
                    >
                      {t('Cancel')}
                    </Button>
                  </div>
                )
              },
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
      </ContentSection>
    </div>
  )
}
