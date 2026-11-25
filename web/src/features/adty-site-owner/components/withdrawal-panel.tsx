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

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { siteWithdrawalStatusLabel } from '@/features/adty-ledger/site-wallet-labels'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import {
  ADDRESS_HINTS,
  isValidUsdtAddress,
  USDT_NETWORKS,
  usdtNetworkLabel,
  type UsdtNetwork,
} from '@/features/referral/lib/address'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { useWithdrawalIntent } from '@/features/wallet/adty/use-withdrawal-intent'
import { WithdrawalRecovery } from '@/features/wallet/adty/withdrawal-recovery'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'

import {
  cancelSiteWithdrawal,
  createSiteWithdrawal,
  getSiteWithdrawalQuote,
  getSiteWithdrawals,
  getSiteWithdrawRules,
  OWNER_PAGE_SIZE,
  siteOwnerKeys,
  type SiteWithdrawal,
} from '../api'
import { useOwnerVerification } from './owner-verification'

const WITHDRAWAL_METHOD = 'usdt' as const

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
  const owner = useOwnerVerification()
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
  const [network, setNetwork] = useState<UsdtNetwork>('TRC20')
  const [accountNo, setAccountNo] = useState('')
  const [confirmedQuote, setConfirmedQuote] = useState('')
  const originalSite = siteId ?? activeSiteId ?? 0
  const intent = useWithdrawalIntent(
    '/api/adty/site/withdrawals',
    originalSite,
    Boolean(siteId)
  )

  const quote = useQuery({
    queryKey: siteOwnerKeys.list('withdrawal-quote', {
      siteId,
      amount,
      method: WITHDRAWAL_METHOD,
      fee: rules.data?.fee_usd,
      rate: rules.data?.usdt_usd_rate,
    }),
    enabled: Boolean(rules.data?.configured) && Number(amount) > 0,
    retry: false,
    queryFn: () => getSiteWithdrawalQuote(amount, WITHDRAWAL_METHOD, siteId),
  })
  const quoteSignature = quote.data
    ? JSON.stringify([
        originalSite,
        amount,
        WITHDRAWAL_METHOD,
        rules.data?.fee_usd,
        rules.data?.usdt_usd_rate,
        quote.data,
      ])
    : ''
  const confirmed = Boolean(quoteSignature) && confirmedQuote === quoteSignature
  const reset = () => {
    setConfirmedQuote('')
    setAmount('')
    setAccountNo('')
  }
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
    void invalidateSidebarBadges(queryClient)
  }

  const submit = useMutation({
    mutationFn: async () => {
      const rate = rules.data?.usdt_usd_rate
      if (!rate) throw new Error(t('Withdrawal rules are not set'))
      const payload = {
        amount_usd: amount,
        method: WITHDRAWAL_METHOD,
        network,
        account_no: accountNo.trim(),
        account_name: '',
        qr_image_url: '',
        expected_rate: rate,
        expected_payout: quote.data,
      }
      const hadSaved = Boolean(intent.saved)
      const key = await intent.prepare({
        amount,
        method: WITHDRAWAL_METHOD,
        quote: quoteSignature,
      })
      if (!key) return false
      const proof = await owner.verify('withdrawal.create', originalSite, {
        amount_usd: payload.amount_usd,
        method: payload.method,
        network: payload.network,
        account_no: payload.account_no,
        account_name: payload.account_name,
        qr_image_url: '',
        expected_rate: payload.expected_rate,
        idempotency_key: key,
      })
      if (!proof) {
        if (!hadSaved) intent.clear(key)
        return false
      }
      const row = await createSiteWithdrawal(
        { ...payload, site_id: originalSite, idempotencyKey: key },
        siteId,
        proof
      )
      intent.accept(key, row.id)
      return true
    },
    onSuccess: (submitted) => {
      if (!submitted) return
      toast.success(t('Withdrawal submitted'))
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
    onSuccess: () => {
      toast.success(t('Withdrawal cancelled'))
      refresh()
    },
    onError: (err) => handleServerError(err),
  })

  const configured = rules.data?.configured === true
  const rate = rules.data?.usdt_usd_rate
  const trimmedAddress = accountNo.trim()
  const addressValid =
    trimmedAddress !== '' && isValidUsdtAddress(network, trimmedAddress)
  const addressInvalid = trimmedAddress !== '' && !addressValid
  const payeeReady = addressValid
  const historyRows = list.data?.items ?? []

  return (
    <div className='flex flex-col gap-8'>
      <SettingsSection
        title={t('Earnings wallet withdrawal')}
        description={[
          t(
            'This request freezes the site earnings wallet. Referral commission withdrawals stay on the Referral page.'
          ),
          t(
            'Customer invite commission withdrawals are reviewed on the Invite commission withdrawal tab.'
          ),
          t('Invite commission holds are not available for this withdrawal.'),
        ].join(' ')}
        bodyClassName='flex flex-col gap-4'
      >
        {rules.isError && (
          <p className='text-destructive text-sm'>
            {t('Could not load withdrawal rules')}
          </p>
        )}
        {rules.data && !configured && (
          <p className='text-muted-foreground text-sm'>
            {t(
              'Root has not set the withdrawal minimum, fee, or exchange rate.'
            )}
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
              ) {
                setConfirmedQuote('')
              }
            }}
            onSubmit={(event) => {
              event.preventDefault()
              if (!confirmed || !quote.data || !payeeReady) return
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
            <div className='grid max-w-2xl gap-4 sm:grid-cols-2'>
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
                <Label htmlFor='withdraw-network'>{t('Network')}</Label>
                <NativeSelect
                  id='withdraw-network'
                  value={network}
                  onChange={(event) =>
                    setNetwork(event.target.value as UsdtNetwork)
                  }
                >
                  {USDT_NETWORKS.map((item) => (
                    <NativeSelectOption key={item} value={item}>
                      {`USDT (${usdtNetworkLabel(item)})`}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className='flex flex-col gap-2 sm:col-span-2'>
                <Label htmlFor='withdraw-account'>{t('USDT address')}</Label>
                <Input
                  id='withdraw-account'
                  className='font-mono'
                  value={accountNo}
                  placeholder={t(ADDRESS_HINTS[network])}
                  aria-invalid={addressInvalid}
                  onChange={(event) => setAccountNo(event.target.value)}
                  required
                />
                {addressInvalid ? (
                  <p className='text-destructive text-xs' role='alert'>
                    {t('The address does not match the {{network}} network', {
                      network: usdtNetworkLabel(network),
                    })}
                  </p>
                ) : (
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'TRC20 addresses start with T; BEP20, Polygon, and X Layer addresses start with 0x.'
                    )}
                  </p>
                )}
              </div>
            </div>
            {quote.data && (
              <p className='text-sm tabular-nums'>
                <span className='text-dim'>{t('Payout amount')}</span>{' '}
                <span className='font-display text-lg font-extrabold tracking-[-0.02em]'>
                  {quote.data} USDT
                </span>
              </p>
            )}
            {quote.isError && (
              <ErrorState onRetry={() => void quote.refetch()} />
            )}
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
                {t(
                  'I confirm the payee details before this request is frozen.'
                )}{' '}
                <span className='text-muted-foreground'>
                  {t('A wrong network or address cannot be recovered.')}
                </span>
              </span>
            </label>
            <div>
              <Button
                variant='brand'
                type='submit'
                disabled={
                  !confirmed || !quote.data || !payeeReady || submit.isPending
                }
              >
                {t('Submit withdrawal')}
              </Button>
            </div>
          </form>
        )}
      </SettingsSection>
      <ContentSection
        title={t('Withdrawal history')}
        actions={
          <>
            <Input
              id='withdraw-number'
              aria-label={t('Order number')}
              placeholder={t('Order number')}
              className='w-52'
              value={number}
              onChange={(event) => {
                setNumber(event.target.value)
                setPage(1)
              }}
            />
            <NativeSelect
              id='withdraw-status-filter'
              aria-label={t('Status')}
              value={status}
              onChange={(event) => {
                setStatus(event.target.value)
                setPage(1)
              }}
            >
              <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
              {['pending', 'paying', 'paid', 'rejected', 'cancelled'].map(
                (item) => (
                  <NativeSelectOption key={item} value={item}>
                    {siteWithdrawalStatusLabel(t, item)}
                  </NativeSelectOption>
                )
              )}
            </NativeSelect>
          </>
        }
      >
        {list.isPending && <LoadingState />}
        {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
        {list.isSuccess && (
          <StaticDataTable
            tableClassName='min-w-[700px]'
            tableProps={{ 'aria-label': t('Withdrawal history') }}
            data={historyRows}
            getRowKey={(row: SiteWithdrawal) => String(row.id)}
            emptyContent={
              <EmptyState
                className='min-h-24'
                title={t('No withdrawals yet')}
              />
            }
            columns={[
              {
                id: 'order',
                header: t('Order number'),
                cell: (row: SiteWithdrawal) => (
                  <span className='font-mono text-xs'>WD-SITE-{row.id}</span>
                ),
              },
              {
                id: 'amount',
                header: t('Amount'),
                cell: (row: SiteWithdrawal) => (
                  <div className='flex flex-col tabular-nums'>
                    <span>{row.amount_usd} USD</span>
                    <span className='text-muted-foreground text-xs'>
                      {row.payout_amount} {row.payout_currency}
                    </span>
                  </div>
                ),
              },
              {
                id: 'payee',
                header: t('Payee'),
                cell: (row: SiteWithdrawal) => (
                  <div className='flex items-center gap-2'>
                    {row.qr_image_url && (
                      <a
                        href={row.qr_image_url}
                        target='_blank'
                        rel='noopener noreferrer'
                        className='shrink-0'
                      >
                        <img
                          src={row.qr_image_url}
                          alt={t('Payment QR code')}
                          className='h-12 w-12 rounded border object-contain'
                        />
                      </a>
                    )}
                    <div className='flex flex-col text-xs'>
                      <span className='font-medium'>
                        {withdrawalMethodLabel(row.method, t)}
                        {row.method === 'usdt' && row.network
                          ? ` (${usdtNetworkLabel(row.network)})`
                          : ''}
                      </span>
                      {row.account_no && (
                        <span className='font-mono break-all'>
                          {row.account_no}
                        </span>
                      )}
                      {row.account_name && (
                        <span className='text-muted-foreground'>
                          {row.account_name}
                        </span>
                      )}
                    </div>
                  </div>
                ),
              },
              {
                id: 'status',
                header: t('Status'),
                cell: (row: SiteWithdrawal) =>
                  siteWithdrawalStatusLabel(t, row.status),
              },
              {
                id: 'actions',
                header: t('Actions'),
                className: 'text-right',
                cellClassName: 'text-right',
                cell: (row: SiteWithdrawal) =>
                  row.status === 'pending' && !row.attempt_open ? (
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      disabled={cancel.isPending}
                      onClick={() => cancel.mutate(row.id)}
                    >
                      {t('Cancel')}
                    </Button>
                  ) : (
                    '—'
                  ),
              },
            ]}
          />
        )}
        {list.data && (
          <PageSwitcher
            page={page}
            pageSize={OWNER_PAGE_SIZE}
            total={list.data.total}
            onPageChange={setPage}
          />
        )}
      </ContentSection>
    </div>
  )
}
