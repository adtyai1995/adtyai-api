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
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'

import { sceneBusinessLabels } from '@/features/adty-payment-scenes/schema'
import {
  officialCommissionStateLabel,
} from '@/features/wallet/adty/commission-states'

import { WithdrawalCancellation } from '@/features/wallet/adty/withdrawal-cancellation'
import type { OrderTrace } from '../api'
import {
  ORDER_TRACE_LABELS,
  paymentProviderLabel,
  UNIFIED_ORDER_LABELS,
} from '../trace-labels'

export function OrderDetailSummary(props: { detail: OrderTrace }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const order = props.detail.order
  const payable = order.has_ext
    ? `${order.pay_amount} ${order.pay_currency} / ${order.paid_amount} ${order.paid_currency}`
    : t('Missing order details')
  let businessLabel = t(UNIFIED_ORDER_LABELS[order.order_type] ?? order.order_type)
  if (order.business === 'owner_self_use_topup') {
    businessLabel = t('Owner self-use top-up')
  } else if (order.business === 'owner_margin_topup') {
    businessLabel = t('Owner margin top-up')
  }
  const refund = props.detail.refund
  const isSubsiteOrder = (order.site_id ?? 0) > 0
  const issuedCommissionUsd = positiveUsd(refund.commission?.issued_usd)
  const recoveredUsd = isSubsiteOrder
    ? positiveUsd(
        refund.commission?.recovered_usd,
        refund.commission_recovered_usd
      )
    : undefined
  const convertedClawedUsd = positiveUsd(refund.commission?.converted_clawed_usd)
  const clawedUsd = positiveUsd(
    refund.commission_clawed_usd,
    refund.commission?.clawed_usd
  )
  const recoveryUsd = positiveUsd(
    refund.commission_recovery_usd,
    refund.commission?.recovery_usd
  )
  const withdrawnUsd = positiveUsd(
    refund.commission_withdrawn_usd,
    refund.commission?.withdrawn_usd
  )
  const hasWithdrawnState = Boolean(
    refund.commission?.states?.some(
      (item) =>
        item.state === 'withdrawn_released' && positiveUsd(item.amount_usd)
    )
  )
  return (
    <>
      <dl className='grid grid-cols-1 gap-3 text-sm break-words sm:grid-cols-3'>
        {[
          [t('User'), formatUserIdentity(order.user_id, order.username)],
          [
            t('Site'),
            `${order.site_name || t('Unknown site')} · ${order.site_id ?? '-'}`,
          ],
          [t('Status'), t(ORDER_TRACE_LABELS[order.status] ?? order.status)],
          [t('Provider'), paymentProviderLabel(t, order.payment_provider)],
          [t('Business'), businessLabel],
          [t('Original order'), order.original_trade_no || '-'],
          [
            t('Payment scene'),
            order.payment_scene
              ? (() => {
                  const rawName = order.payment_scene_name?.trim()
                  const labelKey =
                    (rawName && sceneBusinessLabels[rawName]) ||
                    rawName ||
                    sceneBusinessLabels[order.payment_scene] ||
                    order.payment_scene
                  return `${t(labelKey)} (${order.payment_scene}) · v${order.payment_scene_version}`
                })()
              : '-',
          ],
          [
            t('Payable / paid'),
            props.detail.cancellation && order.paid_currency
              ? `${order.paid_amount} ${order.paid_currency}`
              : payable,
          ],
          [
            t('Order bonus snapshot'),
            order.has_ext ? `${order.bonus_usd} USD` : '-',
          ],
          [t('Created at'), formatTimestamp(order.create_time)],
          [
            t('Completed at'),
            order.complete_time > 0
              ? formatTimestamp(order.complete_time)
              : '-',
          ],
        ].map(([label, value]) => (
          <div key={label}>
            <dt className='text-muted-foreground'>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      {props.detail.cancellation && <WithdrawalCancellation state={props.detail.cancellation} />}
      {order.paid_tx_hash && (
        <p className='text-sm break-all'>
          {t('Payment proof')}: {order.paid_tx_hash}
        </p>
      )}
      {refund.refunded_at > 0 && (
        <div className='rounded-md border p-3 text-sm'>
          <p>
            {t('Refund')}: {refund.refund_amount} {refund.currency} ·{' '}
            {formatTimestamp(refund.refunded_at)}
          </p>
          <p>
            {t('Refund principal quota')}:{' '}
            {formatNumber(refund.refund_quota, locale)}
          </p>
          {refund.reason && <p className='break-words'>{refund.reason}</p>}
          {issuedCommissionUsd && (
            <p className='tabular-nums'>
              {t('Issued commission')}: {issuedCommissionUsd} USD
            </p>
          )}
          {refund.commission?.states?.map((item) => (
            <p key={item.state} className='tabular-nums'>
              {t(officialCommissionStateLabel(item.state))}: {item.amount_usd}{' '}
              USD
            </p>
          ))}
          {recoveredUsd && (
            <p className='tabular-nums'>
              {t('Returned to site wallets')}: {recoveredUsd} USD
            </p>
          )}
          {convertedClawedUsd && (
            <p className='tabular-nums'>
              {t('Clawback from balance')}: {convertedClawedUsd} USD
            </p>
          )}
          {clawedUsd &&
            (!isSubsiteOrder || (!recoveredUsd && !convertedClawedUsd)) && (
              <p className='tabular-nums'>
                {t('Commission clawback')}: {clawedUsd} USD
              </p>
            )}
          {recoveryUsd && (
            <p className='tabular-nums'>
              {t('Recovery liability')}: {recoveryUsd} USD
            </p>
          )}
          {withdrawnUsd && !hasWithdrawnState && (
            <p className='tabular-nums'>
              {t('Withdrawn commission')}: {withdrawnUsd} USD
            </p>
          )}
          <p className='text-muted-foreground'>
            {t(
              'This records the wallet refund. Return the payment offline through the original payment provider.'
            )}
          </p>
        </div>
      )}
    </>
  )
}

function positiveUsd(...values: (string | undefined)[]) {
  for (const value of values) {
    const trimmed = value?.trim()
    if (!trimmed) continue
    const num = Number(trimmed)
    if (Number.isFinite(num) ? num > 0 : trimmed !== '0') {
      return trimmed
    }
  }
  return undefined
}

