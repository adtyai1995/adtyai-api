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
import { Loader2 } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
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
import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import {
  formatBonusRateFraction,
  formatBonusRatePercent,
  quoteAdtyTopup,
  type AdtyTopupInfo,
} from '../api'
import { paymentMethodKey } from '../lib/methods'
import { formatPaymentAmount, formatUsd } from '../lib/money'
import type { AdtyMethod, AdtyTopupBusiness } from '../types'
import { PaymentMethodButtons } from './payment-method-buttons'

function formatFixedPayAmount(raw: string): string {
  const num = Number(raw)
  if (!Number.isFinite(num)) return raw
  const trimmed = raw.trim()
  const dotIndex = trimmed.indexOf('.')
  if (dotIndex >= 0 && trimmed.length - dotIndex - 1 > 2) {
    return trimmed
  }
  return num.toFixed(2)
}

type AdtyRechargeCardProps = {
  business?: AdtyTopupBusiness
  info: AdtyTopupInfo | undefined
  loading: boolean
  paying: boolean
  /** Starts the payment for the chosen amount and method (USDT / Epay dialogs). */
  onPay: (amount: number, method: AdtyMethod, paymentScene: string) => void
}

export function AdtyRechargeCard(props: AdtyRechargeCardProps) {
  const { t, i18n } = useTranslation()
  const sceneSelectId = useId()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const tiers = props.info?.tiers ?? []
  const ownerMargin = props.business === 'owner_margin_topup'
  const [sceneKey, setSceneKey] = useState('wallet_topup')
  const scenes = props.info?.scenes ?? []
  const scene = scenes.find((item) => item.key === sceneKey) ?? scenes[0]
  const paymentScene = scene?.key ?? (ownerMargin ? '' : 'wallet_topup')
  const minTopup = Math.max(
    props.info?.min_topup ?? 1,
    Number(scene?.min_usd ?? 0)
  )
  const [selectedTier, setSelectedTier] = useState<number | null>(null)
  const [customAmount, setCustomAmount] = useState('')
  const [methodKey, setMethodKey] = useState<string | null>(null)

  const methods =
    !ownerMargin && props.info?.scenes === undefined
      ? (props.info?.methods ?? [])
      : (scene?.methods ?? [])
  const method =
    methods.find((item) => paymentMethodKey(item) === methodKey) ?? null
  const fixedAmount =
    method?.face_usd === undefined ? undefined : Number(method.face_usd)
  const amount =
    fixedAmount ??
    selectedTier ??
    (customAmount === '' ? 0 : Number(customAmount))
  const validAmount =
    Number.isFinite(amount) &&
    Number.isInteger(amount) &&
    (fixedAmount !== undefined ? amount > 0 : amount >= minTopup) &&
    amount >= Number(scene?.min_usd ?? 0) &&
    (Number(scene?.max_usd ?? 0) <= 0 || amount <= Number(scene?.max_usd))

  const quoteQuery = useQuery({
    queryKey: [
      'adty-topup',
      'quote',
      props.business ?? 'wallet_topup',
      paymentScene,
      amount,
      methodKey,
    ],
    queryFn: () =>
      quoteAdtyTopup(
        amount,
        method?.id ?? '',
        paymentScene,
        method?.channel_id,
        props.business
      ),
    enabled: validAmount && method !== null && method.available,
    meta: { errorToast: false },
  })
  const quote = quoteQuery.data?.quote
  const quoteAvailable = quoteQuery.data?.available ?? false
  const quoteError = quoteQuery.isError
    ? getServerErrorMessage(quoteQuery.error, t('Failed to load data'))
    : ''

  if (props.loading) {
    return (
      <Card>
        <CardHeader>
          <Skeleton className='h-6 w-32' />
        </CardHeader>
        <CardContent className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
          {Array.from({ length: 8 }, (_, index) => (
            <Skeleton key={index} className='h-16' />
          ))}
        </CardContent>
      </Card>
    )
  }

  if (ownerMargin && !scenes.length) {
    return (
      <EmptyState title={t('Online top-up is unavailable for this site.')} />
    )
  }

  const quotedFaceUsd = Number(quote?.face_usd)
  const quotedBonusUsd = Number(quote?.bonus_usd)
  const quotedCreditUsd =
    quote && Number.isFinite(quote.credit_quota) && quote.credit_quota > 0
      ? quote.credit_quota / 500_000
      : Number.NaN
  let principalUsd = validAmount ? amount : 0
  let bonusUsd = 0
  let totalCreditUsd = validAmount ? amount : 0
  if (validAmount && !ownerMargin && quote) {
    if (Number.isFinite(quotedBonusUsd) && quotedBonusUsd > 0) {
      bonusUsd = quotedBonusUsd
    }
    const hasFace = Number.isFinite(quotedFaceUsd) && quotedFaceUsd > 0
    principalUsd = hasFace ? quotedFaceUsd : amount
    totalCreditUsd = principalUsd + bonusUsd
    if (
      Number.isFinite(quotedCreditUsd) &&
      (!hasFace || Math.abs(quotedCreditUsd - totalCreditUsd) < 0.01)
    ) {
      totalCreditUsd = quotedCreditUsd
    }
  }
  let bonusPercent: string | null = null
  if (bonusUsd > 0) {
    bonusPercent = formatBonusRatePercent(quote?.bonus_rate)
    if (!bonusPercent && principalUsd > 0) {
      bonusPercent = formatBonusRateFraction(bonusUsd / principalUsd)
    }
  }
  let bonusBreakdownText = ''
  if (validAmount && bonusUsd > 0) {
    if (bonusPercent) {
      bonusBreakdownText = t(
        'Principal {{principal}} + Bonus {{bonus}} (+{{percent}}%)',
        {
          principal: formatUsd(principalUsd, locale),
          bonus: formatUsd(bonusUsd, locale),
          percent: bonusPercent,
        }
      )
    } else {
      bonusBreakdownText = t('Principal {{principal}} + Bonus {{bonus}}', {
        principal: formatUsd(principalUsd, locale),
        bonus: formatUsd(bonusUsd, locale),
      })
    }
  }

  let isUsdtChannel = false
  if (quote) {
    isUsdtChannel = quote.pay_currency === 'USDT'
  } else if (method) {
    isUsdtChannel =
      method.kind === 'usdt' ||
      method.id.toLowerCase().startsWith('usdt') ||
      method.currency === 'USDT'
  } else {
    isUsdtChannel =
      methods.length > 0 && methods.every((item) => item.kind === 'usdt')
  }

  let payAmountText = isUsdtChannel ? '0.00 USDT' : '¥0.00'
  let payRateHint = ''
  if (quote) {
    if (quote.pay_currency === 'USDT') {
      payAmountText = `${formatFixedPayAmount(quote.pay_amount)} USDT`
      payRateHint = t('1 USDT = 1 USD')
    } else if (quote.pay_currency === 'CNY') {
      payAmountText = `¥${Number(quote.pay_amount).toFixed(2)}`
      payRateHint = t('1 USD = {{rate}} CNY', {
        rate: Number(quote.price_cny_per_usd).toFixed(2),
      })
    } else {
      payAmountText = formatPaymentAmount(
        quote.pay_amount,
        quote.pay_currency,
        locale
      )
    }
  }

  const cardTitle = ownerMargin
    ? t('Owner margin online top-up')
    : t('Add funds')

  return (
    <Card>
      <CardHeader>
        <CardTitle>{cardTitle}</CardTitle>
        {ownerMargin && (
          <CardDescription>
            {t(
              'Paid amount credits the site margin wallet and opens a new 90% margin cycle. No bonus or customer balance applies.'
            )}
          </CardDescription>
        )}
      </CardHeader>
      <CardContent className='flex flex-col gap-6'>
        {scenes.length > 1 && (
          <div className='space-y-2'>
            <Label htmlFor={sceneSelectId}>{t('Payment scene')}</Label>
            <NativeSelect
              id={sceneSelectId}
              value={paymentScene}
              disabled={props.paying}
              onChange={(event) => {
                setSceneKey(event.target.value)
                setMethodKey(null)
              }}
            >
              {scenes.map((item) => (
                <NativeSelectOption key={item.key} value={item.key}>
                  {t(item.name)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
        )}
        <section className='flex flex-col gap-3'>
          <Label className='text-muted-foreground text-xs font-medium'>
            {t('Amount')}
          </Label>
          <div className='grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-5'>
            {tiers.map((tier) => (
              <Button
                key={tier.amount}
                variant='outline'
                aria-pressed={
                  fixedAmount === undefined && selectedTier === tier.amount
                }
                disabled={fixedAmount !== undefined}
                className={cn(
                  'flex h-auto min-h-16 flex-col items-start gap-1 px-3 py-2.5 text-left whitespace-normal',
                  selectedTier === tier.amount
                    ? 'border-foreground bg-foreground/5'
                    : 'border-muted'
                )}
                onClick={() => {
                  setSelectedTier(tier.amount)
                  setCustomAmount('')
                }}
              >
                <span className='text-base font-semibold'>
                  ${formatNumber(tier.amount, locale)}
                </span>
              </Button>
            ))}
          </div>
          <div className='flex flex-col gap-1.5 sm:max-w-xs'>
            <Label htmlFor={`${sceneSelectId}-amount`}>
              {t('Custom amount (USD)')}
            </Label>
            <Input
              id={`${sceneSelectId}-amount`}
              type='number'
              inputMode='numeric'
              min={minTopup}
              step='1'
              value={fixedAmount ?? customAmount}
              disabled={fixedAmount !== undefined}
              placeholder={t('At least ${{amount}}', { amount: minTopup })}
              onChange={(event) => {
                setCustomAmount(event.target.value)
                setSelectedTier(null)
              }}
            />
            {customAmount !== '' && !validAmount && (
              <p className='text-muted-foreground text-xs'>
                {t('At least ${{amount}}', { amount: minTopup })}
              </p>
            )}
          </div>
        </section>

        <section className='flex flex-col gap-3'>
          <Label className='text-muted-foreground text-xs font-medium'>
            {t('Payment method')}
          </Label>
          <PaymentMethodButtons
            methods={methods}
            selected={methodKey}
            onSelect={setMethodKey}
            methodBonusRates={
              ownerMargin ? undefined : props.info?.method_bonus_rates
            }
          />
        </section>

        <section
          className='bg-muted/40 flex flex-col gap-3 rounded-xl border p-4 sm:p-5'
          aria-live='polite'
        >
          <div className='flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between'>
            <div className='grid flex-1 gap-3 sm:grid-cols-2 sm:gap-6'>
              <div className='flex flex-col gap-1'>
                <span className='text-muted-foreground text-xs font-medium'>
                  {t('Estimated Credit')}
                </span>
                <div className='flex flex-wrap items-baseline gap-2'>
                  <span
                    className={cn(
                      'text-xl font-semibold tracking-tight tabular-nums',
                      validAmount ? 'text-foreground' : 'text-muted-foreground'
                    )}
                    data-testid='adty-credit'
                  >
                    {formatUsd(validAmount ? totalCreditUsd : 0, locale)}
                  </span>
                  {bonusBreakdownText && (
                    <span className='bg-emerald-500/12 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300 inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium tabular-nums'>
                      {bonusBreakdownText}
                    </span>
                  )}
                </div>
              </div>
              <div className='flex flex-col gap-1 sm:border-l sm:pl-6'>
                <div className='flex items-center gap-2'>
                  <span className='text-muted-foreground text-xs font-medium'>
                    {t('Amount Due')}
                  </span>
                  {quoteQuery.isFetching && (
                    <span className='text-muted-foreground inline-flex items-center gap-1 text-xs'>
                      <Loader2
                        className='size-3 animate-spin'
                        aria-hidden='true'
                      />
                      {t('Calculating...')}
                    </span>
                  )}
                </div>
                <div className='flex flex-wrap items-baseline gap-2'>
                  <span
                    className={cn(
                      'text-xl font-semibold tracking-tight tabular-nums',
                      quote ? 'text-foreground' : 'text-muted-foreground'
                    )}
                    data-testid='adty-pay'
                  >
                    {payAmountText}
                  </span>
                  {payRateHint && (
                    <span className='text-muted-foreground text-xs tabular-nums'>
                      {payRateHint}
                    </span>
                  )}
                </div>
              </div>
            </div>
            <Button
              variant='brand'
              className='w-full shrink-0 sm:w-auto sm:min-w-28'
              disabled={
                !validAmount ||
                method === null ||
                !quoteAvailable ||
                props.paying ||
                quoteQuery.isFetching ||
                quoteQuery.isError
              }
              onClick={() => method && props.onPay(amount, method, paymentScene)}
            >
              {props.paying ? t('Processing...') : t('Pay now')}
            </Button>
          </div>
          {quoteQuery.data && !quoteAvailable && (
            <p className='text-destructive text-sm'>
              {t(
                'This method cannot take this amount. Choose another method or amount.'
              )}
            </p>
          )}
          {quoteError && (
            <p className='text-destructive text-sm' role='alert'>
              {quoteError}
            </p>
          )}
        </section>
      </CardContent>
    </Card>
  )
}
