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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'

import {
  adtyTopupQueryKeys,
  getAdtyTopupHistory,
  getAdtyTopupInfo,
} from '../api'
import { resumePayment, type ActivePayment } from '../lib/active-payment'
import type { AdtyTopupBusiness } from '../types'
import { AdtyPaymentDialogs } from './adty-payment-dialogs'
import { AdtyPendingBanner } from './adty-pending-banner'
import { AdtyRechargeCard } from './adty-recharge-card'
import { AdtyTopupHistory } from './adty-topup-history'

export function AdtyOnlineTopup(props: {
  business?: AdtyTopupBusiness
  returnTradeNo?: string
  onReturnClosed?: () => void
  onSettled: () => Promise<void>
}) {
  const client = useQueryClient()
  const { t } = useTranslation()
  const [payment, setPayment] = useState<ActivePayment | null>(null)
  const [dismissedTradeNo, setDismissedTradeNo] = useState<string>()
  const tradeNo = props.returnTradeNo
  const returning = Boolean(tradeNo && tradeNo !== dismissedTradeNo)
  const filters = { business: props.business, trade_no: tradeNo }
  // Resolve the return through the history scoped to this business and host.
  const returned = useQuery({
    queryKey: ['adty-topup', 'return', filters],
    enabled: returning,
    queryFn: async () => {
      const page = await getAdtyTopupHistory(1, 10, filters)
      return (
        page.items?.find(
          (order) =>
            order.trade_no === tradeNo && order.ext?.business === props.business
        ) ?? null
      )
    },
  })
  const returnedPayment =
    returning && returned.data ? resumePayment(returned.data) : null
  const info = useQuery({
    queryKey: props.business
      ? [...adtyTopupQueryKeys.info, props.business]
      : adtyTopupQueryKeys.info,
    queryFn: () => getAdtyTopupInfo(props.business),
  })
  const pending = info.data?.pending
  const pendingBusiness = pending?.ext?.business || 'wallet_topup'
  const refreshOrders = () =>
    client.invalidateQueries({ queryKey: ['adty-topup'] })

  return (
    <>
      {returning && returned.isError && (
        <ErrorState onRetry={() => void returned.refetch()} />
      )}
      {returning && returned.isSuccess && !returned.data && (
        <p role='status'>{t('This order was not found for this site.')}</p>
      )}
      <AdtyPendingBanner
        order={
          pendingBusiness === (props.business ?? 'wallet_topup')
            ? pending
            : null
        }
        onResume={(order) => setPayment(resumePayment(order))}
      />
      <div id='wallet-add-funds' className='scroll-mt-4'>
        {info.isError && <ErrorState onRetry={() => void info.refetch()} />}
        {!info.isError && (
          <AdtyRechargeCard
            business={props.business}
            info={info.data}
            loading={info.isLoading}
            paying={
              payment !== null ||
              (returning && returned.isLoading) ||
              returnedPayment !== null
            }
            onPay={(amount, method, paymentScene) =>
              setPayment({
                kind: method.kind,
                method: method.id,
                channelId: method.channel_id,
                amount,
                paymentScene,
              })
            }
          />
        )}
      </div>
      <AdtyTopupHistory business={props.business} />
      <AdtyPaymentDialogs
        business={props.business}
        payment={payment ?? returnedPayment}
        gatewayReturn={!payment && !!returnedPayment}
        onClose={() => {
          if (returning) {
            setDismissedTradeNo(tradeNo)
            props.onReturnClosed?.()
          }
          setPayment(null)
          void refreshOrders()
        }}
        onSettled={async () => {
          await Promise.all([props.onSettled(), refreshOrders()])
        }}
      />
    </>
  )
}
