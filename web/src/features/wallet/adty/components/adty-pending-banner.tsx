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
import { useMutation } from '@tanstack/react-query'
import { Clock } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

import { cancelAdtyOrder } from '../api'
import { methodLabel } from '../lib/methods'
import { clearPendingPayment, loadPendingPayment } from '../lib/pending-payment'
import type { AdtyOrder } from '../types'

type AdtyPendingBannerProps = {
  order?: AdtyOrder | null
  orders?: AdtyOrder[]
  onResume: (order: AdtyOrder) => void
  onOrderChanged?: () => void
}

/** Shown while one or more unpaid orders are still open; allows resuming or cancelling any of them. */
export function AdtyPendingBanner(props: AdtyPendingBannerProps) {
  const { t } = useTranslation()
  const cancel = useMutation({
    mutationFn: (tradeNo: string) => cancelAdtyOrder(tradeNo),
    onSuccess: (_data, tradeNo) => {
      try {
        if (loadPendingPayment(window.localStorage)?.tradeNo === tradeNo) {
          clearPendingPayment(window.localStorage)
        }
      } catch {
        // Ignore storage errors in restricted contexts.
      }
      toast.success(t('Order canceled'))
      props.onOrderChanged?.()
    },
    onError: (error) => handleServerError(error),
  })

  const rawOrders = props.orders ?? (props.order ? [props.order] : [])
  const list = rawOrders.filter(
    (item): item is AdtyOrder & { ext: NonNullable<AdtyOrder['ext']> } =>
      Boolean(item?.ext)
  )
  if (list.length === 0) return null

  const single = list.length === 1 ? list[0] : null
  if (single) {
    return (
      <Alert>
        <Clock aria-hidden='true' />
        <AlertTitle>{t('You have an order waiting for payment')}</AlertTitle>
        <AlertDescription className='flex flex-wrap items-center justify-between gap-3'>
          <span>
            {t('{{method}}, ${{amount}}, order {{tradeNo}}', {
              method:
                single.ext.method_label || t(methodLabel(single.payment_method)),
              amount: single.ext.face_usd,
              tradeNo: single.trade_no,
            })}
          </span>
          <div className='flex flex-wrap items-center gap-2'>
            <Button
              size='sm'
              disabled={cancel.isPending}
              onClick={() => props.onResume(single)}
            >
              {t('Continue payment')}
            </Button>
            <Button
              size='sm'
              variant='outline'
              disabled={cancel.isPending}
              onClick={() => cancel.mutate(single.trade_no)}
            >
              {t('Cancel order')}
            </Button>
          </div>
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <Alert>
      <Clock aria-hidden='true' />
      <AlertTitle>
        {t('You have {{count}} orders waiting for payment', {
          count: list.length,
        })}
      </AlertTitle>
      <AlertDescription className='mt-2 flex flex-col gap-2'>
        {list.map((order) => (
          <div
            key={order.trade_no}
            className='flex flex-wrap items-center justify-between gap-2 border-t pt-2 first:border-t-0 first:pt-0'
          >
            <span className='text-sm'>
              {t('{{method}}, ${{amount}}, order {{tradeNo}}', {
                method:
                  order.ext.method_label ||
                  t(methodLabel(order.payment_method)),
                amount: order.ext.face_usd,
                tradeNo: order.trade_no,
              })}
            </span>
            <div className='flex flex-wrap items-center gap-2'>
              <Button
                size='sm'
                disabled={cancel.isPending}
                onClick={() => props.onResume(order)}
              >
                {t('Continue payment')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={cancel.isPending}
                onClick={() => cancel.mutate(order.trade_no)}
              >
                {t('Cancel order')}
              </Button>
            </div>
          </div>
        ))}
      </AlertDescription>
    </Alert>
  )
}
