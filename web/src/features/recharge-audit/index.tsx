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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import {
  methodLabel,
  ORDER_STATUS_LABELS,
  ORDER_STATUS_VARIANTS,
} from '@/features/wallet/adty/lib/methods'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import type { AdtyOrderStatus } from '@/features/wallet/adty/types'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  getRechargeAudit,
  getRechargeAuditSummary,
  type RechargeAuditFilters,
  type RechargeAuditOrder,
  type UnifiedOrderType,
} from './api'
import { GiftFulfillmentsPanel } from './components/gift-fulfillments-panel'
import {
  ManualCompleteDialog,
  QueryPaymentDialog,
  RefundDialog,
} from './components/order-action-dialogs'
import { OrderDetailDialog } from './components/order-detail-dialog'
import { PaymentOrphanPanel } from './components/payment-orphan-panel'
import { SiteEarningReviewPanel } from './components/site-earning-review-panel'
import { ORDER_TRACE_LABELS, UNIFIED_ORDER_LABELS } from './trace-labels'

const PAGE_SIZE = 20
const STATUSES: AdtyOrderStatus[] = [
  'pending',
  'success',
  'expired',
  'canceled',
  'failed',
  'refunded',
  'replaced',
]

/** Shared order list; the owner center uses site-scoped read-only endpoints. */
export function RechargeAudit(props: {
  ownerMode?: boolean
  ownerSiteId?: number
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const canRead =
    props.ownerMode || canAdmin(user, ADTY_PERMISSIONS.FINANCE_READ)
  const canComplete =
    !props.ownerMode && canAdmin(user, ADTY_PERMISSIONS.FINANCE_MANUAL_CREDIT)
  const canRefund =
    props.ownerMode === true || canAdmin(user, ADTY_PERMISSIONS.FINANCE_REFUND)
  const [keyword, setKeyword] = useState('')
  const [filters, setFilters] = useState<RechargeAuditFilters>({
    page: 1,
    pageSize: PAGE_SIZE,
  })
  const [completing, setCompleting] = useState<RechargeAuditOrder | null>(null)
  const [refunding, setRefunding] = useState<RechargeAuditOrder | null>(null)
  const [querying, setQuerying] = useState<RechargeAuditOrder | null>(null)
  const [viewing, setViewing] = useState<{
    tradeNo: string
    userId?: number
    ownerMode?: boolean
    siteId?: number
    orderType: UnifiedOrderType
    sourceType?: string
  } | null>(null)
  useEffect(() => {
    setViewing(null)
    setRefunding(null)
  }, [user?.id, props.ownerMode, props.ownerSiteId, canRead])
  useEffect(() => {
    if (!canComplete) {
      setCompleting(null)
      setQuerying(null)
    }
    if (!canRefund) {
      setRefunding(null)
    }
  }, [canComplete, canRefund])

  const ordersQuery = useQuery({
    queryKey: [
      'recharge-audit',
      user?.id,
      props.ownerMode === true,
      props.ownerSiteId,
      'orders',
      filters,
    ],
    queryFn: () => getRechargeAudit(filters, props.ownerMode),
  })
  const summaryQuery = useQuery({
    queryKey: [
      'recharge-audit',
      user?.id,
      props.ownerMode === true,
      props.ownerSiteId,
      'summary',
      { ...filters, page: 1 },
    ],
    queryFn: () => getRechargeAuditSummary(filters, props.ownerMode),
    enabled: props.ownerMode !== true,
  })
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['recharge-audit'] })
    void invalidateSidebarBadges(queryClient)
  }
  const total = ordersQuery.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const content = (
    <>
      <div className='flex flex-col gap-4'>
        {props.ownerMode !== true && summaryQuery.isError && (
          <ErrorState onRetry={() => void summaryQuery.refetch()} />
        )}
        {props.ownerMode !== true && summaryQuery.isPending && <LoadingState />}
        {props.ownerMode !== true && summaryQuery.data && (
          <div className='border-border bg-border grid gap-px overflow-hidden rounded-[14px] border sm:grid-cols-3'>
            {[
              {
                label: t('Orders'),
                value: String(summaryQuery.data?.orders ?? 0),
              },
              {
                label: t('Credited face value'),
                value: formatUsd(
                  Number(summaryQuery.data?.credited_usd ?? 0),
                  locale
                ),
              },
              {
                label: t('Bonus given'),
                value: formatUsd(
                  Number(summaryQuery.data?.bonus_usd ?? 0),
                  locale
                ),
              },
            ].map((item) => (
              <div
                key={item.label}
                className='bg-card flex flex-col gap-1 px-5 py-4'
              >
                <span className='text-dim text-[12px] leading-none'>
                  {item.label}
                </span>
                <span className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
                  {item.value}
                </span>
              </div>
            ))}
          </div>
        )}

        <div className='flex flex-col gap-3'>
          <form
            className='flex flex-wrap items-end gap-2'
            onSubmit={(event) => {
              event.preventDefault()
              setFilters((prev) => ({
                ...prev,
                keyword: keyword.trim(),
                page: 1,
              }))
            }}
          >
            <Input
              className='w-64'
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
              placeholder={t('Order number, username or transaction hash')}
              aria-label={t('Search')}
            />
            {!props.ownerMode && (
              <NativeSelect
                aria-label={t('Site')}
                value={
                  filters.site_id === undefined ? '' : String(filters.site_id)
                }
                onChange={(event) =>
                  setFilters((prev) => ({
                    ...prev,
                    site_id:
                      event.target.value === ''
                        ? undefined
                        : Number(event.target.value),
                    page: 1,
                  }))
                }
              >
                <option value=''>{t('All sites')}</option>
                {(summaryQuery.data?.site_options ?? []).map((site) => (
                  <option key={site.site_id} value={site.site_id}>
                    {site.site_name || t('Site')} · {site.site_id}
                  </option>
                ))}
              </NativeSelect>
            )}
            <NativeSelect
              aria-label={t('Order type')}
              value={filters.order_type ?? ''}
              onChange={(event) =>
                setFilters((prev) => ({
                  ...prev,
                  order_type: (event.target.value || undefined) as
                    | UnifiedOrderType
                    | undefined,
                  page: 1,
                }))
              }
            >
              <option value=''>{t('All order types')}</option>
              {Object.entries(UNIFIED_ORDER_LABELS).map(([value, label]) => (
                <option key={value} value={value}>
                  {t(label)}
                </option>
              ))}
            </NativeSelect>
            <NativeSelect
              aria-label={t('Status')}
              value={filters.status ?? ''}
              onChange={(event) =>
                setFilters((prev) => ({
                  ...prev,
                  status: event.target.value,
                  page: 1,
                }))
              }
            >
              <option value=''>{t('All statuses')}</option>
              {[
                ...new Set([
                  ...STATUSES,
                  'settled',
                  'submitted',
                  'approved',
                  'rejected',
                  'paying',
                  'paid',
                  'completed',
                  'terminated',
                  'available',
                  'frozen',
                  'clawed_back',
                ]),
              ].map((status) => (
                <option key={status} value={status}>
                  {t(ORDER_TRACE_LABELS[status] ?? status)}
                </option>
              ))}
            </NativeSelect>
            {!props.ownerMode && (
              <NativeSelect
                aria-label={t('Provider')}
                value={filters.provider ?? ''}
                onChange={(event) =>
                  setFilters((prev) => ({
                    ...prev,
                    provider: event.target.value,
                    page: 1,
                  }))
                }
              >
                <option value=''>{t('All providers')}</option>
                {(summaryQuery.data?.provider_capabilities ?? []).map(
                  (provider) => (
                    <option key={provider.provider} value={provider.provider}>
                      {t(provider.label)}
                    </option>
                  )
                )}
              </NativeSelect>
            )}
            <Button variant='brand' type='submit'>
              {t('Search')}
            </Button>
          </form>

          {ordersQuery.isError && (
            <ErrorState onRetry={() => void ordersQuery.refetch()} />
          )}
          {ordersQuery.isPending ? (
            <LoadingState />
          ) : (
            ordersQuery.data && (
              <StaticDataTable
                tableClassName='min-w-[1400px]'
                tableProps={{ 'aria-label': t('Order management') }}
                data={ordersQuery.data?.items ?? []}
                getRowKey={(row) =>
                  [row.source_type, row.order_type, row.trade_no].join(':')
                }
                emptyContent={
                  <EmptyState className='min-h-32' title={t('No orders')} />
                }
                columns={[
                  {
                    id: 'time',
                    header: t('Time'),
                    cell: (row) => formatTimestamp(row.create_time),
                  },
                  {
                    id: 'user',
                    header: t('User'),
                    cellClassName: 'tabular-nums',
                    cell: (row) =>
                      formatUserIdentity(row.user_id, row.username),
                  },
                  {
                    id: 'site',
                    header: t('Site'),
                    cell: (row) =>
                      row.site_id == null
                        ? t('Unknown site')
                        : row.site_name || t('Site'),
                  },
                  {
                    id: 'site_id',
                    header: t('Site ID'),
                    cell: (row) => row.site_id ?? '-',
                  },
                  {
                    id: 'order_type',
                    header: t('Order type'),
                    cell: (row) => {
                      if (row.business === 'owner_self_use_topup') {
                        return t('Owner self-use top-up')
                      }
                      if (row.business === 'owner_margin_topup') {
                        return t('Owner margin top-up')
                      }
                      return t(
                        UNIFIED_ORDER_LABELS[row.order_type] ?? row.order_type
                      )
                    },
                  },
                  {
                    id: 'complete_time',
                    header: t('Completed at'),
                    cell: (row) =>
                      row.complete_time > 0
                        ? formatTimestamp(row.complete_time)
                        : '-',
                  },
                  {
                    id: 'method',
                    header: t('Payment method'),
                    cell: (row) => (
                      <div className='flex flex-col'>
                        <span>
                          {row.method_label ||
                            t(methodLabel(row.payment_method))}
                        </span>
                        {row.channel_name && (
                          <span className='text-muted-foreground text-xs'>
                            {row.channel_name}
                          </span>
                        )}
                      </div>
                    ),
                  },
                  {
                    id: 'face',
                    header: t('Amount'),
                    className: 'text-right',
                    cellClassName: 'text-right tabular-nums',
                    cell: (row) =>
                      formatUsd(Number(row.face_usd ?? row.amount), locale),
                  },
                  {
                    id: 'bonus',
                    header: t('Bonus'),
                    className: 'text-right',
                    cellClassName: 'text-right tabular-nums',
                    cell: (row) => formatUsd(Number(row.bonus_usd), locale),
                  },
                  {
                    id: 'pay',
                    header: t('Payable / paid'),
                    className: 'text-right',
                    cellClassName: 'text-right tabular-nums',
                    cell: (row) => (
                      <div className='flex flex-col items-end text-xs'>
                        <span>
                          {row.has_ext
                            ? `${row.pay_amount} ${row.pay_currency}`
                            : row.money}
                        </span>
                        {Number(row.paid_amount) > 0 && (
                          <span className='text-muted-foreground'>
                            {row.paid_amount} {row.paid_currency}
                          </span>
                        )}
                        {row.paid_tx_hash && (
                          <code
                            className='text-muted-foreground block max-w-[10rem] truncate font-mono'
                            title={row.paid_tx_hash}
                          >
                            {row.paid_tx_hash}
                          </code>
                        )}
                      </div>
                    ),
                  },
                  {
                    id: 'status',
                    header: t('Status'),
                    cell: (row) => (
                      <div className='flex flex-col items-start gap-1'>
                        <StatusBadge
                          label={t(
                            ORDER_STATUS_LABELS[
                              row.status as AdtyOrderStatus
                            ] ??
                              ORDER_TRACE_LABELS[row.status] ??
                              row.status
                          )}
                          variant={
                            ORDER_STATUS_VARIANTS[
                              row.status as AdtyOrderStatus
                            ] ?? 'neutral'
                          }
                          copyable={false}
                        />
                        {row.anomaly && (
                          <StatusBadge
                            label={
                              row.anomaly === 'missing_ext'
                                ? t('Missing order details')
                                : t('Paid amount differs')
                            }
                            variant='danger'
                            copyable={false}
                          />
                        )}
                      </div>
                    ),
                  },
                  {
                    id: 'trade_no',
                    header: t('Order number'),
                    cell: (row) => (
                      <Button
                        variant='link'
                        className='h-auto p-0 font-mono text-xs'
                        onClick={() =>
                          setViewing({
                            tradeNo: row.trade_no,
                            userId: user?.id,
                            ownerMode: props.ownerMode,
                            siteId: props.ownerSiteId,
                            orderType: row.order_type,
                            sourceType: row.source_type,
                          })
                        }
                      >
                        {row.trade_no}
                      </Button>
                    ),
                  },
                  ...(canComplete || canRefund
                    ? [
                        {
                          id: 'actions',
                          header: t('Actions'),
                          className: 'text-right',
                          cellClassName: 'text-right',
                          cell: (row: RechargeAuditOrder) => {
                            if (row.order_type !== 'topup') return null
                            return (
                              <div className='flex justify-end gap-1'>
                                {canComplete && row.actions?.query && (
                                  <Button
                                    size='sm'
                                    variant='outline'
                                    onClick={() => setQuerying(row)}
                                  >
                                    {t('Query payment')}
                                  </Button>
                                )}
                                {canComplete && row.actions?.complete && (
                                  <Button
                                    size='sm'
                                    variant='outline'
                                    onClick={() => setCompleting(row)}
                                  >
                                    {t('Complete')}
                                  </Button>
                                )}
                                {canRefund && row.actions?.refund && (
                                  <Button
                                    size='sm'
                                    variant='outline'
                                    onClick={() => setRefunding(row)}
                                  >
                                    {t('Refund')}
                                  </Button>
                                )}
                              </div>
                            )
                          },
                        },
                      ]
                    : []),
                ]}
              />
            )
          )}
          {pages > 1 && (
            <div className='flex items-center justify-end gap-2'>
              <Button
                variant='outline'
                size='sm'
                disabled={filters.page <= 1}
                onClick={() =>
                  setFilters((prev) => ({ ...prev, page: prev.page - 1 }))
                }
              >
                {t('Previous')}
              </Button>
              <span className='text-muted-foreground text-sm'>
                {filters.page} / {pages}
              </span>
              <Button
                variant='outline'
                size='sm'
                disabled={filters.page >= pages}
                onClick={() =>
                  setFilters((prev) => ({ ...prev, page: prev.page + 1 }))
                }
              >
                {t('Next')}
              </Button>
            </div>
          )}
        </div>

        {!props.ownerMode && <PaymentOrphanPanel canCredit={canComplete} />}
        {!props.ownerMode && (
          <SiteEarningReviewPanel canResolve={canComplete} />
        )}
        {!props.ownerMode && user?.role === ROLE.SUPER_ADMIN && (
          <GiftFulfillmentsPanel />
        )}
      </div>
      {viewing &&
        user &&
        viewing.userId === user.id &&
        viewing.ownerMode === props.ownerMode &&
        viewing.siteId === props.ownerSiteId &&
        canRead && (
          <OrderDetailDialog
            key={viewing.tradeNo}
            tradeNo={viewing.tradeNo}
            orderType={viewing.orderType}
            sourceType={viewing.sourceType}
            userId={user.id}
            ownerMode={props.ownerMode}
            ownerSiteId={props.ownerSiteId}
            onClose={() => setViewing(null)}
          />
        )}
      {canComplete && (
        <QueryPaymentDialog
          order={querying}
          onOpenChange={(open) => !open && setQuerying(null)}
          onDone={refresh}
        />
      )}
      {canComplete && (
        <ManualCompleteDialog
          order={completing}
          onOpenChange={(open) => !open && setCompleting(null)}
          onDone={refresh}
        />
      )}
      {canRefund && (
        <RefundDialog
          order={refunding}
          ownerMode={props.ownerMode}
          ownerSiteId={props.ownerSiteId}
          onOpenChange={(open) => !open && setRefunding(null)}
          onDone={refresh}
        />
      )}
    </>
  )
  if (props.ownerMode) return content
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Order management')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>{content}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
