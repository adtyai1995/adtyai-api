import { skipToken, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import type { OwnerCustomer } from '@/features/adty-site-owner/types'
import {
  ORDER_TRACE_LABELS,
  UNIFIED_ORDER_LABELS,
} from '@/features/recharge-audit/trace-labels'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatCurrencyFromUSD } from '@/lib/currency'
import { formatTimestamp } from '@/lib/format'

import {
  getStaffCustomerOrder,
  getStaffCustomerOrders,
  STAFF_PAGE_SIZE,
  type StaffOrderType,
} from './api'

export function StaffOrdersDialog(props: {
  userId: number
  staffVersion: number
  customer: OwnerCustomer
  orderTypes: StaffOrderType[]
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [orderType, setOrderType] = useState(props.orderTypes[0])
  const [page, setPage] = useState(1)
  const [tradeNo, setTradeNo] = useState<string | null>(null)
  const allowed = props.orderTypes.includes(orderType)
  const list = useQuery({
    queryKey: [
      'adty-staff',
      props.userId,
      props.staffVersion,
      'orders',
      props.customer.id,
      orderType,
      page,
    ],
    queryFn: () => getStaffCustomerOrders(props.customer.id, orderType, page),
    enabled: allowed && tradeNo === null,
    retry: false,
  })
  const detail = useQuery({
    queryKey: [
      'adty-staff',
      props.userId,
      props.staffVersion,
      'order',
      props.customer.id,
      orderType,
      tradeNo,
    ],
    queryFn:
      tradeNo === null
        ? skipToken
        : () => getStaffCustomerOrder(props.customer.id, orderType, tradeNo),
    enabled: allowed && tradeNo !== null,
    retry: false,
  })
  const query = tradeNo === null ? list : detail
  const order = detail.data
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t(tradeNo === null ? 'Orders' : 'Order details')}
      description={props.customer.username}
    >
      {tradeNo === null ? (
        <NativeSelect
          aria-label={t('Order type')}
          value={orderType}
          onChange={(event) => {
            const value = props.orderTypes.find(
              (kind) => kind === event.target.value
            )
            if (value) {
              setOrderType(value)
              setPage(1)
            }
          }}
        >
          {props.orderTypes.map((kind) => (
            <option key={kind} value={kind}>
              {t(UNIFIED_ORDER_LABELS[kind])}
            </option>
          ))}
        </NativeSelect>
      ) : (
        <Button variant='outline' onClick={() => setTradeNo(null)}>
          {t('Back')}
        </Button>
      )}
      {query.isPending && <Skeleton className='h-32' />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {allowed && tradeNo === null && list.isSuccess && (
        <>
          <StaticDataTable
            data={list.data.items ?? []}
            getRowKey={(row) => row.trade_no}
            emptyContent={<EmptyState title={t('No records')} />}
            columns={[
              {
                id: 'number',
                header: t('Order number'),
                cell: (row) => (
                  <Button
                    variant='link'
                    className='h-auto max-w-full break-all whitespace-normal'
                    onClick={() => setTradeNo(row.trade_no)}
                  >
                    {row.trade_no}
                  </Button>
                ),
              },
              {
                id: 'type',
                header: t('Order type'),
                cell: (row) => t(UNIFIED_ORDER_LABELS[row.order_type]),
              },
              {
                id: 'amount',
                header: t('Amount'),
                cell: (row) => formatCurrencyFromUSD(Number(row.face_usd)),
              },
              {
                id: 'status',
                header: t('Status'),
                cell: (row) => t(ORDER_TRACE_LABELS[row.status] ?? row.status),
              },
              {
                id: 'time',
                header: t('Time'),
                cell: (row) => formatTimestamp(row.created_at),
              },
            ]}
          />
          <PageSwitcher
            page={page}
            total={list.data.total}
            pageSize={STAFF_PAGE_SIZE}
            onPageChange={setPage}
          />
        </>
      )}
      {allowed && tradeNo !== null && detail.isSuccess && order && (
        <dl className='grid grid-cols-1 gap-3 text-sm break-words sm:grid-cols-2'>
          {[
            [t('Order number'), order.trade_no],
            [t('Order type'), t(UNIFIED_ORDER_LABELS[order.order_type])],
            [t('Status'), t(ORDER_TRACE_LABELS[order.status] ?? order.status)],
            [t('Amount'), formatCurrencyFromUSD(Number(order.face_usd))],
            [t('Time'), formatTimestamp(order.created_at)],
            [
              t('Completed at'),
              order.completed_at ? formatTimestamp(order.completed_at) : '-',
            ],
          ].map(([label, value]) => (
            <div key={label}>
              <dt className='text-muted-foreground'>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </Dialog>
  )
}
