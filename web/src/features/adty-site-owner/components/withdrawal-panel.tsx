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
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { siteWithdrawalStatusLabel } from '@/features/adty-ledger/site-wallet-labels'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { useWithdrawalIntent } from '@/features/wallet/adty/use-withdrawal-intent'
import { WithdrawalRecovery } from '@/features/wallet/adty/withdrawal-recovery'
import { handleServerError } from '@/lib/handle-server-error'

import {
  OWNER_PAGE_SIZE,
  getSiteWithdrawalQuote,
  cancelSiteWithdrawal,
  createSiteWithdrawal,
  getSiteWithdrawals,
  getSiteWithdrawRules,
  siteOwnerKeys,
} from '../api'

const NETWORKS = ['TRC20', 'BEP20', 'POLYGON', 'XLAYER'] as const

function withdrawalMethodLabel(
  method: string,
  t: (key: string) => string
): string {
  switch (method) {
    case 'alipay':
      return t('Alipay')
    case 'wxpay':
      return t('WeChat Pay')
    case 'usdt':
      return 'USDT'
    default:
      return method
  }
}

/** Owner request against the site earnings wallet. Amounts come from Root rules, not from this page. */
export function WithdrawalPanel({
  siteId,
  activeSiteId,
}: {
  siteId?: number
  activeSiteId?: number
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const rules = useQuery({
    queryKey: siteOwnerKeys.list('withdraw-rules', { siteId }),
    queryFn: () => getSiteWithdrawRules(siteId),
  })
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [number, setNumber] = useState('')
  const list = useQuery({
    queryKey: siteOwnerKeys.list('withdrawals', {
      siteId,
      page,
      status,
      number,
    }),
    queryFn: () => getSiteWithdrawals(siteId, page, status, number),
  })
  const [amount, setAmount] = useState('')
  const [method, setMethod] = useState<'alipay' | 'usdt'>('alipay')
  const [network, setNetwork] = useState<(typeof NETWORKS)[number]>('TRC20')
  const [accountNo, setAccountNo] = useState('')
  const [accountName, setAccountName] = useState('')
  const [confirmedQuote, setConfirmedQuote] = useState('')
  const originalSite = siteId ?? activeSiteId ?? 0
  const intent = useWithdrawalIntent(
    '/api/adty/site/withdrawals',
    originalSite,
    !!siteId
  )

  const quote = useQuery({
    queryKey: siteOwnerKeys.list('withdrawal-quote', {
      siteId,
      amount,
      method,
      fee: rules.data?.fee_usd,
      rate:
        method === 'usdt'
          ? rules.data?.usdt_usd_rate
          : rules.data?.alipay_cny_rate,
    }),
    enabled: !!rules.data?.configured && Number(amount) > 0,
    retry: false,
    queryFn: () => getSiteWithdrawalQuote(amount, method, siteId),
  })
  const quoteSignature = quote.data
    ? JSON.stringify([
        originalSite,
        amount,
        method,
        rules.data?.fee_usd,
        method === 'usdt'
          ? rules.data?.usdt_usd_rate
          : rules.data?.alipay_cny_rate,
        quote.data,
      ])
    : ''
  const confirmed = !!quoteSignature && confirmedQuote === quoteSignature
  const reset = () => {
    setConfirmedQuote('')
    setAmount('')
    setAccountNo('')
    setAccountName('')
  }
  const refresh = () =>
    void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
  const submit = useMutation({
    mutationFn: async () => {
      const rate =
        method === 'usdt'
          ? rules.data?.usdt_usd_rate
          : rules.data?.alipay_cny_rate
      if (!rate) throw new Error(t('Withdrawal rules are not set'))
      const payload = {
        amount_usd: amount,
        method,
        network: method === 'usdt' ? network : '',
        account_no: accountNo,
        account_name: accountName,
        expected_rate: rate,
        expected_payout: quote.data,
      }
      const key = await intent.prepare({
        amount,
        method,
        quote: quoteSignature,
      })
      if (!key) return
      const row = await createSiteWithdrawal(
        { ...payload, site_id: originalSite, idempotencyKey: key },
        siteId
      )
      intent.accept(key, row.id)
    },
    onSuccess: () => {
      reset()
      refresh()
    },
    onError: (err) => {
      handleServerError(err)
      setConfirmedQuote('')
      void quote.refetch()
      void rules.refetch()
    },
  })
  const cancel = useMutation({
    mutationFn: (id: number) => cancelSiteWithdrawal(id, siteId),
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all }),
    onError: (err) => handleServerError(err),
  })

  const configured = rules.data?.configured === true
  const rate =
    method === 'usdt' ? rules.data?.usdt_usd_rate : rules.data?.alipay_cny_rate

  return (
    <section
      className='flex flex-col gap-4'
      aria-label={t('Earnings wallet withdrawal')}
    >
      <div className='flex flex-col gap-1'>
        <h2 className='text-sm font-medium'>
          {t('Earnings wallet withdrawal')}
        </h2>
        <p className='text-muted-foreground text-sm'>
          {t(
            'This request freezes the site earnings wallet. Referral commission withdrawals stay on the Referral page.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Customer invite commission withdrawals are reviewed on the Invite commission withdrawal tab.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t('Invite commission holds are not available for this withdrawal.')}
        </p>
      </div>
      {rules.isError && (
        <p className='text-destructive text-sm'>
          {t('Could not load withdrawal rules')}
        </p>
      )}
      {rules.data && !configured && (
        <p className='text-muted-foreground text-sm'>
          {t('Root has not set the withdrawal minimum, fee, or exchange rate.')}
        </p>
      )}
      <WithdrawalRecovery
        intent={intent}
        busy={submit.isPending}
        onReset={reset}
        onRecovered={refresh}
      />
      {configured && !intent.saved?.accepted && (
        <form
          className='flex flex-col gap-4'
          onChange={(event) => {
            if (
              !(
                event.target instanceof HTMLInputElement &&
                event.target.type === 'checkbox'
              )
            )
              {setConfirmedQuote('')}
          }}
          onSubmit={(event) => {
            event.preventDefault()
            if (!confirmed || !quote.data) return
            submit.mutate()
          }}
        >
          <p className='text-muted-foreground text-sm tabular-nums'>
            {t('Minimum {{min}} USD. Fee {{fee}} USD. Rate {{rate}}.', {
              min: rules.data?.min_usd,
              fee: rules.data?.fee_usd,
              rate,
            })}
          </p>
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='flex flex-col gap-2'>
              <Label htmlFor='withdraw-amount'>{t('Amount USD')}</Label>
              <Input
                id='withdraw-amount'
                inputMode='decimal'
                value={amount}
                onChange={(event) => setAmount(event.target.value)}
                required
              />
            </div>
            <div className='flex flex-col gap-2'>
              <Label htmlFor='withdraw-method'>{t('Method')}</Label>
              <NativeSelect
                id='withdraw-method'
                value={method}
                onChange={(event) =>
                  setMethod(event.target.value === 'usdt' ? 'usdt' : 'alipay')
                }
              >
                <NativeSelectOption value='alipay'>
                  {t('Alipay')}
                </NativeSelectOption>
                <NativeSelectOption value='usdt'>USDT</NativeSelectOption>
              </NativeSelect>
            </div>
            {method === 'usdt' && (
              <div className='flex flex-col gap-2'>
                <Label htmlFor='withdraw-network'>{t('Network')}</Label>
                <NativeSelect
                  id='withdraw-network'
                  value={network}
                  onChange={(event) =>
                    setNetwork(event.target.value as (typeof NETWORKS)[number])
                  }
                >
                  {NETWORKS.map((item) => (
                    <NativeSelectOption key={item} value={item}>
                      {item === 'XLAYER' ? 'X Layer' : item}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
            )}
            <div className='flex flex-col gap-2'>
              <Label htmlFor='withdraw-account'>{t('Account')}</Label>
              <Input
                id='withdraw-account'
                value={accountNo}
                onChange={(event) => setAccountNo(event.target.value)}
                required
              />
            </div>
            <div className='flex flex-col gap-2'>
              <Label htmlFor='withdraw-name'>{t('Payee name')}</Label>
              <Input
                id='withdraw-name'
                value={accountName}
                onChange={(event) => setAccountName(event.target.value)}
                required={method === 'alipay'}
              />
            </div>
          </div>
          {quote.data && (
            <p>
              {t('Payout amount')} · {quote.data}{' '}
              {method === 'alipay' ? 'CNY' : 'USDT'}
            </p>
          )}
          {quote.isError && <ErrorState onRetry={() => void quote.refetch()} />}
          <label className='flex items-start gap-2 text-sm'>
            <input
              type='checkbox'
              className='mt-1'
              checked={confirmed}
              onChange={(event) =>
                setConfirmedQuote(event.target.checked ? quoteSignature : '')
              }
            />
            <span>
              {t('I confirm the payee details before this request is frozen.')}
            </span>
          </label>
          <div>
            <Button
              variant='brand'
              type='submit'
              disabled={!confirmed || !quote.data || submit.isPending}
            >
              {t('Submit withdrawal')}
            </Button>
          </div>
        </form>
      )}
      <section
        className='flex flex-col gap-2'
        aria-label={t('Withdrawal history')}
      >
        <Label htmlFor='withdraw-number'>{t('Order number')}</Label>
        <Input
          id='withdraw-number'
          value={number}
          onChange={(e) => {
            setNumber(e.target.value)
            setPage(1)
          }}
        />
        <NativeSelect
          aria-label={t('Status')}
          value={status}
          onChange={(e) => {
            setStatus(e.target.value)
            setPage(1)
          }}
        >
          <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
          {['pending', 'paying', 'paid', 'rejected', 'cancelled'].map((x) => (
            <NativeSelectOption key={x} value={x}>
              {siteWithdrawalStatusLabel(t, x)}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        {list.isPending && <LoadingState />}
        {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
        {list.isSuccess && (list.data?.items ?? []).length === 0 && (
          <EmptyState className='min-h-24' title={t('No withdrawals yet')} />
        )}
        {list.isSuccess && (list.data?.items ?? []).length > 0 && (
          <ul className='flex flex-col gap-2'>
            {(list.data?.items ?? []).map((row) => (
              <li
                key={row.id}
                className='flex flex-wrap items-center justify-between gap-2 text-sm tabular-nums'
              >
                <span>
                  WD-SITE-{row.id} · {row.amount_usd} USD ·{' '}
                  {withdrawalMethodLabel(row.method, t)} ·{' '}
                  {siteWithdrawalStatusLabel(t, row.status)}
                </span>
                {row.status === 'pending' && !row.attempt_open && (
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={cancel.isPending}
                    onClick={() => cancel.mutate(row.id)}
                  >
                    {t('Cancel')}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
        {list.data && (
          <PageSwitcher
            page={page}
            pageSize={OWNER_PAGE_SIZE}
            total={list.data.total}
            onPageChange={setPage}
          />
        )}
      </section>
    </section>
  )
}
