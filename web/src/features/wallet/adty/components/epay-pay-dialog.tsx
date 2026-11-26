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
import { ExternalLink } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAdtyUiStore } from '@/stores/adty-ui-store'

import { adtyTopupQueryKeys, cancelAdtyOrder, getAdtyOrder } from '../api'
import { payWithEpay } from '../api-pay'
import { formatCountdown, useNowSeconds } from '../lib/countdown'
import { detectPayDevice } from '../lib/device'
import {
  creditedUsdAmount,
  giftFulfillmentStatusLabelKey,
  isGiftBonusWithheld,
} from '../lib/money'
import {
  directQrCode,
  mobileTarget,
  qrContent,
  safePaymentLink,
} from '../lib/payment-links'
import { clearPendingPayment, savePendingPayment } from '../lib/pending-payment'
import type { AdtyOrder, AdtyTopupBusiness } from '../types'
import { PaymentMethodIcon } from './payment-logos'

type EpayPayDialogProps = {
  gatewayReturn?: boolean
  business?: AdtyTopupBusiness
  paymentScene?: string
  method: string
  /** Channel picked on the wallet; failures do not switch to another one. */
  channelId?: number
  amount: number
  tradeNo?: string
  onClose: () => void
  onSettled: () => Promise<void> | void
  /** Injected in tests; defaults to the browser. */
  device?: 'pc' | 'mobile'
  navigate?: (url: string) => void
}

/** Alipay / WeChat payment: QR code on desktop, jump to the app on phones. */
export function EpayPayDialog(props: EpayPayDialogProps) {
  const { t } = useTranslation()
  const device = props.device ?? detectPayDevice()
  const [tradeNo, setTradeNo] = useState<string | undefined>(props.tradeNo)
  const setPaymentDialogOpen = useAdtyUiStore(
    (state) => state.setPaymentDialogOpen
  )
  const settledRef = useRef(false)
  const jumpedRef = useRef<string>('')

  useEffect(() => {
    setPaymentDialogOpen(true)
    return () => setPaymentDialogOpen(false)
  }, [setPaymentDialogOpen])

  const create = useMutation({
    mutationFn: () =>
      payWithEpay(
        props.amount,
        props.method,
        props.channelId,
        device,
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
  const ext = order?.ext
  const now = useNowSeconds(status === 'pending')
  const expired =
    status === 'expired' ||
    (ext ? ext.expire_at > 0 && now >= ext.expire_at && status === 'pending' : false)

  const { refetch } = orderQuery
  useEffect(() => {
    // Back from the payment app: check the order right away.
    const onVisible = () => {
      if (document.visibilityState === 'visible') void refetch()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [refetch])

  const navigate = props.navigate
  useEffect(() => {
    if (
      device !== 'mobile' ||
      props.gatewayReturn ||
      !ext ||
      status !== 'pending' ||
      expired ||
      jumpedRef.current === ext.trade_no
    ) {
      return
    }
    const target = mobileTarget(ext)
    if (!target) {
      return
    }
    jumpedRef.current = ext.trade_no
    if (navigate) {
      navigate(target)
      return
    }
    window.location.assign(target)
  }, [device, ext, status, expired, navigate, props.gatewayReturn])

  const { onSettled } = props
  useEffect(() => {
    if (status && status !== 'pending' && !settledRef.current) {
      settledRef.current = true
      clearPendingPayment(window.localStorage)
      void onSettled()
    }
  }, [status, onSettled])

  const cancel = useMutation({
    mutationFn: (no: string) => cancelAdtyOrder(no),
  })

  const cancelOrder = async () => {
    try {
      if (tradeNo) await cancel.mutateAsync(tradeNo)
      clearPendingPayment(window.localStorage)
      props.onClose()
    } catch (error) {
      handleServerError(error)
    }
  }

  const appName = props.method === 'wxpay' ? t('WeChat') : t('Alipay')
  const directQr = ext ? directQrCode(ext) : ''
  const payLink = ext ? safePaymentLink(ext) : ''
  const qr = ext ? qrContent(ext) : ''
  const target = ext ? mobileTarget(ext) : ''
  const isActivePending = status === 'pending' && !expired
  const hasRawPaymentLink = Boolean(
    ext?.qr_code?.trim() || ext?.pay_url?.trim() || ext?.url_scheme?.trim()
  )
  const hasSafePaymentAction = Boolean(directQr || payLink || qr || target)
  const desktopOpenLinkMode =
    device === 'pc' && !directQr && Boolean(payLink)
  const checkoutUnavailable = Boolean(
    ext && isActivePending && !hasRawPaymentLink
  )
  const unsafeLink = Boolean(
    ext && isActivePending && hasRawPaymentLink && !hasSafePaymentAction
  )

  const openMobilePaymentApp = () => {
    if (!target) return
    if (navigate) {
      navigate(target)
      return
    }
    window.location.assign(target)
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={
        <span className='inline-flex items-center gap-2.5'>
          <PaymentMethodIcon
            method={props.method === 'wxpay' ? 'wxpay' : 'alipay'}
            className='size-6'
          />
          <span>
            {props.method === 'wxpay'
              ? t('Pay with WeChat Pay')
              : t('Pay with Alipay')}
          </span>
        </span>
      }
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        isActivePending ? (
          <>
            <Button
              variant='outline'
              disabled={cancel.isPending || create.isPending}
              onClick={() => void cancelOrder()}
            >
              {t('Cancel order')}
            </Button>
            <Button variant='ghost' onClick={props.onClose}>
              {t('Close')}
            </Button>
          </>
        ) : (
          <Button onClick={props.onClose}>{t('Close')}</Button>
        )
      }
    >
      {tradeNo && (
        <p className='text-sm break-all'>
          {t('Order number')} · {tradeNo}
        </p>
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
      {unsafeLink && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'The payment link is not safe to open. Choose another payment method.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {checkoutUnavailable && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'This payment channel or payment link is currently unavailable. Please cancel this order and choose another payment method to place a new order.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {ext && isActivePending && (
        <div className='flex flex-col items-center gap-3'>
          <p className='text-2xl font-semibold' data-testid='epay-amount'>
            ¥{Number(ext.pay_amount).toFixed(2)}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t('You get ${{amount}}', {
              amount: creditedUsdAmount(ext),
            })}
          </p>
          {desktopOpenLinkMode && (
            <div className='flex flex-col items-center gap-2'>
              <a
                className={buttonVariants({ variant: 'brand' })}
                href={payLink}
                target='_blank'
                rel='noopener noreferrer'
              >
                <ExternalLink className='size-4' aria-hidden='true' />
                {t('Open payment page')}
              </a>
              <p className='text-muted-foreground text-center text-xs'>
                {t('Complete payment in the newly opened page, then return here.')}
              </p>
            </div>
          )}
          {!desktopOpenLinkMode &&
            (device === 'pc' || !target) &&
            qr && (
              <>
                <div
                  className='rounded-lg bg-white p-3'
                  data-testid='epay-qr'
                  data-value={qr}
                >
                  <QRCodeSVG value={qr} size={200} />
                </div>
                <p className='text-sm'>
                  {t('Scan with {{app}}', { app: appName })}
                </p>
              </>
            )}
          {device === 'mobile' && target && (
            <div className='flex flex-col items-center gap-2'>
              <p className='text-center text-sm'>
                {props.gatewayReturn
                  ? t('Returned from payment page. Checking order status...')
                  : t('Opening {{app}}... Come back here after paying.', {
                      app: appName,
                    })}
              </p>
              <Button
                type='button'
                variant='brand'
                onClick={openMobilePaymentApp}
              >
                <ExternalLink className='size-4' aria-hidden='true' />
                {t('Open payment app')}
              </Button>
            </div>
          )}
          <p className='text-sm'>
            {t('Time left')}:{' '}
            <span className='font-mono font-semibold'>
              {formatCountdown(ext.expire_at, now)}
            </span>
          </p>
        </div>
      )}
    </Dialog>
  )
}
