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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { uploadReferralAsset } from '@/features/referral/api'
import {
  isValidUsdtAddress,
  USDT_NETWORKS,
  usdtNetworkLabel,
  type UsdtNetwork,
} from '@/features/referral/lib/address'
import {
  WITHDRAWAL_STATUS_LABELS,
  WITHDRAWAL_TARGET_LABELS,
} from '@/features/referral/lib/labels'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { SiteCommission } from './site-commission-panel'
import { useWithdrawalIntent } from './use-withdrawal-intent'
import { WithdrawalCancellation } from './withdrawal-cancellation'
import { WithdrawalRecovery } from './withdrawal-recovery'

type WithdrawRules = {
  configured: boolean
  min_usd?: string
  fee_usd?: string
  cny_rate?: string
  methods: string[]
}

type SiteInviteWithdrawal = {
  site_id?: number
  id: number
  amount_usd: string
  fee_usd: string
  net_usd: string
  method: string
  network: string
  account_no: string
  account_name: string
  status: string
  can_cancel: boolean
  cancel_deadline: number
  cancel_reason: string
  payout_amount: string
  payout_currency: string
  reject_reason: string
}

type ApiResponse<T> = {
  success: boolean
  message?: string
  code?: string
  data: T
}

type Quote = {
  quote_id: string
  rate: string
  fee_usd: string
  payout_amount: string
  payout_currency: string
}

const METHODS = ['balance', 'usdt', 'alipay'] as const
type Method = (typeof METHODS)[number]

async function loadRules(siteId: number): Promise<WithdrawRules> {
  const res = await api.get<ApiResponse<WithdrawRules>>(
    '/api/adty/site-commissions/withdrawals/rules',
    { params: { site_id: siteId } }
  )
  return requireServerSuccess(res.data).data
}

async function loadWithdrawals(): Promise<SiteInviteWithdrawal[]> {
  const res = await api.get<ApiResponse<{ items?: SiteInviteWithdrawal[] }>>(
    '/api/adty/site-commissions/withdrawals'
  )
  return requireServerSuccess(res.data).data.items ?? []
}

async function createWithdrawal(body: {
  siteId: number
  amount: string
  method: Method
  network: string
  accountNo: string
  accountName: string
  qrUrl: string
  expectedQuoteId: string
  idempotencyKey: string
}): Promise<{ kind: 'ok' } | { kind: 'quote_changed'; quote: Quote }> {
  const res = await api.post<ApiResponse<Quote>>(
    '/api/adty/site-commissions/withdrawals',
    {
      amount_usd: body.amount,
      site_id: body.siteId,
      method: body.method,
      network: body.method === 'usdt' ? body.network : '',
      account_no: body.method === 'usdt' ? body.accountNo : '',
      account_name: body.method === 'alipay' ? body.accountName : '',
      qr_image_url: body.method === 'alipay' ? body.qrUrl : '',
      expected_quote_id: body.expectedQuoteId,
    },
    { headers: { 'Idempotency-Key': body.idempotencyKey } }
  )
  if (res.data.success === false && res.data.code === 'quote_changed') {
    return { kind: 'quote_changed', quote: res.data.data }
  }
  requireServerSuccess(res.data)
  return { kind: 'ok' }
}

async function cancelWithdrawal(id: number): Promise<void> {
  const res = await api.post<ApiResponse<{ canceled: boolean }>>(
    `/api/adty/site-commissions/withdrawals/${id}/cancel`
  )
  requireServerSuccess(res.data)
}

function freeAmount(row: SiteCommission, nowSeconds: number): number {
  if (
    row.status !== 'available' ||
    !row.available_at ||
    row.available_at > nowSeconds
  ) {
    return 0
  }
  const hold =
    Number(row.earnings_hold_usd || 0) + Number(row.margin_hold_usd || 0)
  const free = hold - Number(row.frozen_usd || 0)
  return free > 0 ? free : 0
}

function methodLabel(method: string): string {
  if (method in WITHDRAWAL_TARGET_LABELS) {
    return WITHDRAWAL_TARGET_LABELS[
      method as keyof typeof WITHDRAWAL_TARGET_LABELS
    ]
  }
  return method
}

function statusLabel(status: string): string {
  if (status in WITHDRAWAL_STATUS_LABELS) {
    return WITHDRAWAL_STATUS_LABELS[
      status as keyof typeof WITHDRAWAL_STATUS_LABELS
    ]
  }
  return status
}

/** Site-customer invite commission withdrawal. The site owner reviews cash methods. */
export function SiteInviteWithdrawPanel(props: {
  rows: SiteCommission[]
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const sites = [...new Set(props.rows.map((row) => row.site_id ?? 0))]
  const [selected, setSelected] = useState(sites[0])
  const current = sites.includes(selected) ? selected : sites[0]
  return (
    <>
      {sites.some((id) => id > 0) && (
        <NativeSelect
          aria-label={t('Site')}
          value={current}
          onChange={(e) => setSelected(Number(e.target.value))}
        >
          {sites.map((id) => (
            <NativeSelectOption key={id} value={id}>
              {t('Site')} #{id}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      )}
      <SiteWithdrawForm
        key={current}
        siteId={current ?? 0}
        rows={props.rows.filter((row) => (row.site_id ?? 0) === current)}
        onChanged={props.onChanged}
      />
    </>
  )
}
function SiteWithdrawForm(props: {
  siteId: number
  rows: SiteCommission[]
  onChanged: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const rules = useQuery({
    queryKey: ['adty-site-invite-withdraw-rules', props.siteId],
    queryFn: () => loadRules(props.siteId),
  })
  const history = useQuery({
    queryKey: ['adty-site-invite-withdrawals'],
    queryFn: loadWithdrawals,
  })
  const [method, setMethod] = useState<Method>('balance')
  const [amount, setAmount] = useState('')
  const [network, setNetwork] = useState<UsdtNetwork | ''>('')
  const [address, setAddress] = useState('')
  const [payee, setPayee] = useState('')
  const [qrUrl, setQrUrl] = useState('')
  const [confirmation, setConfirmation] = useState<Quote | null>(null)
  const [quoteChanged, setQuoteChanged] = useState(false)
  const intent = useWithdrawalIntent(
    '/api/adty/site-commissions/withdrawals',
    props.siteId
  )

  const available = props.rows.reduce(
    (sum, row) => sum + freeAmount(row, Math.floor(Date.now() / 1000)),
    0
  )
  const value = Number(amount)
  const minimum = Number(rules.data?.min_usd || 0)
  const isCny = method === 'alipay'
  const cashReady = rules.data?.configured === true

  const validate = (): string => {
    if (amount.trim() === '') return ''
    if (!(value > 0)) return t('Enter a withdrawal amount')
    if (value > available) return t('Not enough withdrawable commission')
    if (method !== 'balance') {
      if (!cashReady) {
        return t(
          'Cash withdrawal stays off until the minimum, fee, and CNY rate are saved.'
        )
      }
      if (value < minimum) {
        return t('The minimum withdrawal is ${{amount}}', {
          amount: rules.data?.min_usd,
        })
      }
    }
    if (method === 'usdt') {
      if (network === '') return t('Choose a network')
      if (!isValidUsdtAddress(network, address)) {
        return t('The address does not match the {{network}} network', {
          network: usdtNetworkLabel(network),
        })
      }
    }
    if (isCny && (qrUrl === '' || payee.trim() === '')) {
      return t('Upload the payment QR code and enter the payee name')
    }
    return ''
  }
  const error = validate()

  const refresh = () => {
    props.onChanged()
    void client.invalidateQueries({
      queryKey: ['adty-site-invite-withdrawals'],
    })
  }
  const reset = () => {
    setAmount('')
    setAddress('')
    setPayee('')
    setQrUrl('')
    setNetwork('')
    setConfirmation(null)
    setQuoteChanged(false)
  }
  const submit = useMutation({
    mutationFn: async (confirmedQuote: Quote | null) => {
      const body = {
        siteId: props.siteId,
        amount: amount.trim(),
        method,
        network: method === 'usdt' ? network : '',
        accountNo: method === 'usdt' ? address.trim() : '',
        accountName: isCny ? payee.trim() : '',
        qrUrl: isCny ? qrUrl : '',
        expectedQuoteId: confirmedQuote?.quote_id ?? '',
      }
      const key = await intent.prepare({
        amount: body.amount,
        method,
        quote: body.expectedQuoteId,
      })
      if (!key) return { kind: 'recovered' as const }
      const result = await createWithdrawal({ ...body, idempotencyKey: key })
      if (result.kind === 'ok') intent.accept(key)
      return result
    },
    onSuccess: (result) => {
      if (result.kind === 'quote_changed') {
        setQuoteChanged(true)
        setConfirmation(result.quote)
        return
      }
      if (result.kind === 'recovered') {
        reset()
        refresh()
        return
      }
      toast.success(
        method === 'balance'
          ? t('Transferred to your balance')
          : t('Withdrawal submitted')
      )
      reset()
      refresh()
    },
    onError: (err) => handleServerError(err),
  })
  const upload = useMutation({
    mutationFn: (file: File) => uploadReferralAsset(file),
    onSuccess: setQrUrl,
    onError: (err) => handleServerError(err),
  })
  const cancel = useMutation({
    mutationFn: cancelWithdrawal,
    onSettled: refresh,
    onError: (err) => handleServerError(err),
  })

  const quote = useMutation({
    mutationFn: async () => {
      if (intent.saved && (await intent.check())) return null
      const res = await api.get<ApiResponse<Quote>>(
        '/api/adty/site-commissions/withdrawals/quote',
        {
          params: { site_id: props.siteId, amount_usd: amount.trim(), method },
        }
      )
      return requireServerSuccess(res.data).data
    },
    onSuccess: (value) => {
      if (!value) {
        reset()
        refresh()
        return
      }
      setQuoteChanged(false)
      setConfirmation(value)
    },
    onError: (err) => handleServerError(err),
  })
  const confirmationTitle =
    method === 'usdt'
      ? t('Confirm the USDT withdrawal')
      : t('Invite commission withdrawal')
  const onSubmit = () => {
    if (error || amount.trim() === '') return
    if (method !== 'balance') {
      quote.mutate()
      return
    }
    submit.mutate(null)
  }

  return (
    <div
      className='flex flex-col gap-4'
      aria-label={t('Invite commission withdrawal')}
    >
      <div className='flex flex-col gap-1'>
        <h3 className='text-sm font-medium'>
          {t('Invite commission withdrawal')}
        </h3>
        <p className='text-muted-foreground text-sm'>
          {t('The site owner reviews cash withdrawals of invite commission.')}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'A transfer to balance deducts the site hold. External payout releases it after payment is complete.'
          )}
        </p>
        <p className='text-sm tabular-nums'>
          {t('Withdrawable: {{amount}}', {
            amount: formatUsd(available, locale),
          })}
        </p>
      </div>
      {rules.isError && <ErrorState onRetry={() => void rules.refetch()} />}
      {rules.data && !cashReady && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Cash withdrawal stays off until the minimum, fee, and CNY rate are saved.'
          )}
        </p>
      )}
      {cashReady && (
        <p className='text-muted-foreground text-sm tabular-nums'>
          {t('Minimum {{min}} USD. Fee {{fee}} USD. Rate {{rate}}.', {
            min: rules.data?.min_usd,
            fee: rules.data?.fee_usd,
            rate: rules.data?.cny_rate,
          })}
        </p>
      )}
      <WithdrawalRecovery
        intent={intent}
        busy={submit.isPending}
        onReset={reset}
        onRecovered={refresh}
      />
      {!intent.saved?.accepted && (
        <form
          className='flex flex-col gap-4'
          onSubmit={(event) => {
            event.preventDefault()
            onSubmit()
          }}
        >
          <fieldset
            disabled={quote.isPending || submit.isPending}
            className='flex flex-col gap-4'
          >
            <fieldset className='flex flex-col gap-2'>
              <legend className='text-sm font-medium'>
                {t('Withdraw to')}
              </legend>
              <div className='grid grid-cols-2 gap-2'>
                {METHODS.map((item) => {
                  const enabled =
                    item === 'balance' ||
                    rules.data?.methods.includes(item) === true
                  return (
                    <Button
                      key={item}
                      type='button'
                      variant='outline'
                      disabled={!enabled}
                      aria-pressed={method === item}
                      className={method === item ? 'border-foreground' : ''}
                      onClick={() => {
                        setMethod(item)
                      }}
                    >
                      {t(methodLabel(item))}
                      {!enabled && (
                        <span className='text-muted-foreground ml-1 text-xs'>
                          {t('Not available yet')}
                        </span>
                      )}
                    </Button>
                  )
                })}
              </div>
            </fieldset>
            <div className='flex flex-col gap-2'>
              <Label htmlFor='site-invite-withdraw-amount'>
                {t('Amount USD')}
              </Label>
              <Input
                id='site-invite-withdraw-amount'
                value={amount}
                inputMode='decimal'
                autoComplete='off'
                onChange={(event) => setAmount(event.target.value)}
              />
              {method === 'balance' && (
                <p className='text-muted-foreground text-sm'>
                  {t('Credited to your balance right away, no fee.')}
                </p>
              )}
            </div>
            {method === 'usdt' && cashReady && (
              <>
                <div className='flex flex-col gap-2'>
                  <Label htmlFor='site-invite-withdraw-network'>
                    {t('Network')}
                  </Label>
                  <NativeSelect
                    id='site-invite-withdraw-network'
                    value={network}
                    onChange={(event) => {
                      setNetwork(event.target.value as UsdtNetwork | '')
                      setAddress('')
                    }}
                  >
                    <NativeSelectOption value=''>
                      {t('Choose a network')}
                    </NativeSelectOption>
                    {USDT_NETWORKS.map((item) => (
                      <NativeSelectOption key={item} value={item}>
                        {usdtNetworkLabel(item)}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </div>
                <div className='flex flex-col gap-2'>
                  <Label htmlFor='site-invite-withdraw-address'>
                    {t('USDT address')}
                  </Label>
                  <Input
                    id='site-invite-withdraw-address'
                    value={address}
                    autoComplete='off'
                    disabled={network === ''}
                    onChange={(event) => setAddress(event.target.value)}
                  />
                </div>
              </>
            )}
            {isCny && cashReady && (
              <>
                <div className='flex flex-col gap-2'>
                  <Label htmlFor='site-invite-withdraw-payee'>
                    {t('Payee name')}
                  </Label>
                  <Input
                    id='site-invite-withdraw-payee'
                    value={payee}
                    autoComplete='name'
                    onChange={(event) => setPayee(event.target.value)}
                  />
                </div>
                <div className='flex flex-col gap-2'>
                  <Label htmlFor='site-invite-withdraw-qr'>
                    {t('Payment QR code')}
                  </Label>
                  <Input
                    id='site-invite-withdraw-qr'
                    type='file'
                    accept='image/png,image/jpeg,image/webp'
                    onChange={(event) => {
                      const file = event.target.files?.[0]
                      if (file) upload.mutate(file)
                    }}
                  />
                  {qrUrl !== '' && (
                    <img
                      src={qrUrl}
                      alt={t('Payment QR code')}
                      className='h-40 w-40 rounded-lg border object-contain'
                    />
                  )}
                </div>
              </>
            )}
            {error !== '' && (
              <p className='text-destructive text-sm' role='alert'>
                {error}
              </p>
            )}
            <div>
              <Button
                type='submit'
                variant='outline'
                disabled={
                  amount.trim() === '' ||
                  error !== '' ||
                  quote.isPending ||
                  submit.isPending ||
                  upload.isPending
                }
              >
                {t('Submit withdrawal')}
              </Button>
            </div>
          </fieldset>
        </form>
      )}
      {history.isError && <ErrorState onRetry={() => void history.refetch()} />}
      <ul className='flex flex-col gap-2'>
        {(history.data ?? []).map((row) => (
          <li
            key={row.id}
            className='flex flex-wrap items-center justify-between gap-2 text-sm tabular-nums'
          >
            <span>
              {row.site_id && (
                <>
                  {t('Site')} #{row.site_id} ·{' '}
                </>
              )}
              {row.amount_usd} USD · {t(methodLabel(row.method))} ·{' '}
              {t(statusLabel(row.status))}
              {row.payout_amount !== '' &&
                ` · ${row.payout_amount} ${row.payout_currency}`}
              {row.reject_reason !== '' && ` · ${row.reject_reason}`}
            </span>
            <WithdrawalCancellation
              state={row}
              busy={cancel.isPending}
              onCancel={() => cancel.mutate(row.id)}
            />
          </li>
        ))}
      </ul>
      <ConfirmDialog
        open={confirmation !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmation(null)
        }}
        title={
          quoteChanged
            ? t('Withdrawal quote changed. Confirm the updated amount.')
            : confirmationTitle
        }
        desc={
          <div className='flex flex-col gap-2 text-sm'>
            {method === 'usdt' ? (
              <>
                <p>
                  {usdtNetworkLabel(network)} ·{' '}
                  <span className='font-mono break-all'>{address}</span>
                </p>
                <p>
                  {t('USDT fee (USD)')} {confirmation?.fee_usd}
                </p>
                <p className='text-destructive'>
                  {t('A wrong network or address cannot be recovered.')}
                </p>
              </>
            ) : (
              <p>
                {payee} ·{' '}
                {t('1 USD = {{rate}} CNY. You receive ¥{{cny}}.', {
                  rate: confirmation?.rate,
                  cny: confirmation?.payout_amount,
                })}
              </p>
            )}
            <p>
              {t('Payout amount')} {confirmation?.payout_amount}{' '}
              {confirmation?.payout_currency}
            </p>
          </div>
        }
        confirmText={t('Confirm')}
        isLoading={submit.isPending}
        handleConfirm={() => {
          if (confirmation) submit.mutate(confirmation)
        }}
      />
    </div>
  )
}
