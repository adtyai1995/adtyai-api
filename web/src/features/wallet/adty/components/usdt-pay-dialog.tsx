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
import { QRCodeSVG } from 'qrcode.react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAdtyUiStore } from '@/stores/adty-ui-store'

import { adtyTopupQueryKeys, cancelAdtyOrder, getAdtyOrder } from '../api'
import { payWithBEpusdt } from '../api-pay'
import { formatCountdown, useNowSeconds } from '../lib/countdown'
import { CHAIN_NAMES } from '../lib/methods'
import {
  creditedUsdAmount,
  giftFulfillmentStatusLabelKey,
  isGiftBonusWithheld,
} from '../lib/money'
import { clearPendingPayment, savePendingPayment } from '../lib/pending-payment'
import type { AdtyOrder, AdtyTopupBusiness } from '../types'
import { ChainIcon } from './chain-icons'
import { UsdtChainLogo } from './payment-logos'

type UsdtPayDialogProps = {
  business?: AdtyTopupBusiness
  paymentScene?: string
  chain: string
  amount: number
  /** Resume this order instead of creating a new one. */
  tradeNo?: string
  onClose: () => void
  onSettled: () => Promise<void> | void
  /** Goes back to the recharge card so the user can pick another chain. */
  onChangeChain: () => void
}

/** In-site USDT payment: address QR code, amount, countdown and polling. */
export function UsdtPayDialog(props: UsdtPayDialogProps) {
  const { t } = useTranslation()
  const [tradeNo, setTradeNo] = useState<string | undefined>(props.tradeNo)
  const [notice, setNotice] = useState('')
  const setPaymentDialogOpen = useAdtyUiStore(
    (state) => state.setPaymentDialogOpen
  )
  const settledRef = useRef(false)

  useEffect(() => {
    setPaymentDialogOpen(true)
    return () => setPaymentDialogOpen(false)
  }, [setPaymentDialogOpen])

  const create = useMutation({
    mutationFn: () =>
      payWithBEpusdt(
        props.amount,
        props.chain,
        props.paymentScene,
        props.business
      ),
    onSuccess: (order) => {
      setTradeNo(order.trade_no)
      if (order.ext) {
        savePendingPayment(window.localStorage, {
          tradeNo: order.trade_no,
          method: order.payment_method,
          expireAt: order.ext.expire_at,
        })
      }
    },
    onError: (error) => {
      handleServerError(error)
      props.onClose()
    },
  })
  const { mutate: createOrder } = create
  useEffect(() => {
    if (!props.tradeNo) createOrder()
  }, [props.tradeNo, createOrder])

  const orderQuery = useQuery({
    queryKey: adtyTopupQueryKeys.order(tradeNo ?? ''),
    queryFn: () => getAdtyOrder(tradeNo ?? ''),
    enabled: Boolean(tradeNo),
    refetchInterval: (query) =>
      query.state.data?.status === 'pending' ? 3000 : false,
  })
  const order: AdtyOrder | undefined =
    orderQuery.data?.trade_no === tradeNo ? orderQuery.data : create.data
  const status = order?.status
  const now = useNowSeconds(status === 'pending')

  const { onSettled } = props
  useEffect(() => {
    if (status && status !== 'pending' && !settledRef.current) {
      settledRef.current = true
      clearPendingPayment(window.localStorage)
      void onSettled()
    }
  }, [status, onSettled])

  const cancel = useMutation({
    mutationFn: () => cancelAdtyOrder(tradeNo ?? ''),
  })

  const handleCancel = async (thenChangeChain: boolean) => {
    try {
      await cancel.mutateAsync()
      clearPendingPayment(window.localStorage)
      if (thenChangeChain) {
        setNotice(t('Do not send anything to the old address.'))
        props.onChangeChain()
        return
      }
      props.onClose()
    } catch (error) {
      setNotice(
        getServerErrorMessage(
          error,
          t('Unable to cancel this order right now. Please try again.')
        )
      )
    }
  }

  const ext = order?.ext
  const chainName = CHAIN_NAMES[props.chain] ?? props.chain
  const expired =
    status === 'expired' ||
    (ext ? ext.expire_at > 0 && now >= ext.expire_at && status === 'pending' : false)
  const isActivePending = status === 'pending' && !expired
  const payAddress = ext?.pay_address?.trim() ?? ''
  const hasPayAddress = Boolean(payAddress)

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={
        <span className='inline-flex items-center gap-2.5'>
          <UsdtChainLogo chain={props.chain} className='size-6' />
          <span>{t('Pay with USDT')}</span>
        </span>
      }
      description={t('Only send USDT on the {{chain}} network.', {
        chain: chainName,
      })}
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        isActivePending ? (
          <>
            <Button
              variant='outline'
              disabled={cancel.isPending}
              onClick={() => void handleCancel(true)}
            >
              {t('Change network')}
            </Button>
            <Button
              variant='ghost'
              disabled={cancel.isPending}
              onClick={() => void handleCancel(false)}
            >
              {t('Cancel order')}
            </Button>
          </>
        ) : (
          <Button onClick={props.onClose}>{t('Close')}</Button>
        )
      }
    >
      {notice && (
        <Alert>
          <AlertDescription>{notice}</AlertDescription>
        </Alert>
      )}
      {!ext && !orderQuery.isError && (
        <p className='text-muted-foreground text-sm'>
          {t('Creating the order...')}
        </p>
      )}
      {orderQuery.isError && (
        <Alert variant='destructive'>
          <AlertDescription className='flex flex-wrap items-center justify-between gap-2'>
            <span>
              {getServerErrorMessage(
                orderQuery.error,
                t('Failed to load order status.')
              )}
            </span>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() => void orderQuery.refetch()}
            >
              {t('Retry')}
            </Button>
          </AlertDescription>
        </Alert>
      )}
      {ext && status === 'success' && (
        <div className='space-y-1 text-center' role='status'>
          <p className='text-success text-lg font-semibold'>
            {t('Credited ${{amount}}', {
              amount: creditedUsdAmount(ext),
            })}
          </p>
          {isGiftBonusWithheld(ext.gift_fulfillment_status) && (
            <p className='text-muted-foreground text-xs'>
              {t(
                giftFulfillmentStatusLabelKey(ext.gift_fulfillment_status) ?? ''
              )}
            </p>
          )}
        </div>
      )}
      {ext && expired && status !== 'success' && (
        <p className='text-destructive text-center' role='status'>
          {t('This order has expired. Please create a new one.')}
        </p>
      )}
      {ext && status === 'canceled' && (
        <p className='text-muted-foreground text-center' role='status'>
          {t('This order was canceled.')}
        </p>
      )}
      {ext && status === 'failed' && (
        <p className='text-destructive text-center' role='status'>
          {t('This payment failed. Please create a new order.')}
        </p>
      )}
      {ext && status === 'refunded' && (
        <p className='text-muted-foreground text-center' role='status'>
          {t('This order has been refunded.')}
        </p>
      )}
      {ext && status === 'replaced' && (
        <p className='text-muted-foreground text-center' role='status'>
          {t('This order was replaced by a newer order.')}
        </p>
      )}
      {ext && isActivePending && !hasPayAddress && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'This USDT payment channel or receiving address is currently unavailable. Please cancel this order and create a new one.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {ext && isActivePending && hasPayAddress && (
        <div className='flex flex-col items-center gap-4'>
          <div className='bg-muted/50 inline-flex items-center gap-2.5 rounded-full border px-3.5 py-1.5 text-sm font-medium'>
            <ChainIcon method={props.chain} className='size-6' />
            <span>USDT · {chainName}</span>
          </div>
          <div className='rounded-lg bg-white p-3'>
            <QRCodeSVG value={payAddress} size={180} />
          </div>
          <div className='w-full space-y-1'>
            <p className='text-muted-foreground text-xs'>
              {t('Receiving address')}
            </p>
            <div className='bg-muted flex items-center justify-between gap-2 rounded-md p-2'>
              <code
                className='font-mono text-xs break-all'
                data-testid='usdt-address'
              >
                {payAddress}
              </code>
              <CopyButton
                value={payAddress}
                aria-label={t('Copy address')}
              />
            </div>
          </div>
          <div className='w-full space-y-1'>
            <p className='text-muted-foreground text-xs'>
              {t('Amount to send')}
            </p>
            <div className='bg-muted flex items-center justify-between gap-2 rounded-md p-2'>
              <code
                className='font-mono text-base font-semibold'
                data-testid='usdt-amount'
              >
                {ext.pay_amount} USDT
              </code>
              <CopyButton
                value={ext.pay_amount}
                aria-label={t('Copy amount')}
              />
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Send exactly the amount shown; it may include an extra 0.01.'
              )}
            </p>
          </div>
          <p className='text-sm'>
            {t('Time left')}:{' '}
            <span
              className='font-mono font-semibold'
              data-testid='usdt-countdown'
            >
              {formatCountdown(ext.expire_at, now)}
            </span>
          </p>
          <Alert variant='destructive'>
            <AlertDescription>
              {t(
                'Only the {{chain}} network is accepted. Transfers on other networks cannot be recovered.',
                {
                  chain: chainName,
                }
              )}
            </AlertDescription>
          </Alert>
        </div>
      )}
    </Dialog>
  )
}
