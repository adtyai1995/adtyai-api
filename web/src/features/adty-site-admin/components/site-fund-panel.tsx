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
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { siteWalletKindLabel } from '@/features/adty-ledger/site-wallet-labels'
import { SiteWalletLedger } from '@/features/adty-ledger/site-wallet-ledger'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  creditAdminSiteMargin,
  getAdminAnomalyDispositions,
  getAdminFundAnomalies,
  getAdminSiteWalletLedger,
  getAdminSiteWallets,
  reviewAdminAnomalyDisposition,
  siteAdminKeys,
} from '../api'

function fundAnomalyKindLabel(
  kind: string,
  t: (key: string) => string
): string {
  switch (kind) {
    case 'balance_ledger_gap':
      return t('Balance and ledger mismatch')
    case 'open_shortfall_hold':
      return t('Open shortfall hold')
    case 'margin_anomaly':
      return t('Margin cycle anomaly')
    case 'duplicate_credit':
      return t('Duplicate credit')
    case 'duplicate_refund':
      return t('Duplicate refund')
    case 'order_success_unbooked':
      return t('Successful order unbooked')
    case 'gift_guarantee_short':
      return t('Insufficient gift guarantee')
    case 'earning_supply_mismatch':
      return t('Earnings and supply cost mismatch')
    case 'refund_amount_mismatch':
      return t('Refund amount mismatch')
    case 'closed_team_topup':
      return t('Closed team top-up')
    case 'team_principal_review':
      return t('Team principal review required')
    default:
      return kind
  }
}

function dispositionStatusLabel(
  status: string,
  t: (key: string) => string
): string {
  switch (status) {
    case 'open':
      return t('Open')
    case 'reviewing':
      return t('Reviewing')
    case 'closed':
      return t('Closed')
    default:
      return status
  }
}

/** Root sees site wallets, direct margin credit, wallet ledger, anomalies, and review history. */
export function SiteFundPanel(props: { siteId: number }) {
  const { t } = useTranslation()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const client = useQueryClient()
  const [directAmount, setDirectAmount] = useState('')
  const [directReason, setDirectReason] = useState('')
  const directIntentRef = useRef<{ payload: string; key: string } | null>(null)
  const directCredit = useMutation({
    mutationFn: () => {
      const payloadObj = {
        amount_usd: directAmount.trim(),
        reason: directReason.trim(),
      }
      const serialized = JSON.stringify(payloadObj)
      if (directIntentRef.current?.payload !== serialized) {
        const key =
          typeof crypto !== 'undefined' && 'randomUUID' in crypto
            ? crypto.randomUUID()
            : `margin-${Date.now()}-${Math.random().toString(36).slice(2)}`
        directIntentRef.current = { payload: serialized, key }
      }
      return creditAdminSiteMargin(
        props.siteId,
        payloadObj,
        directIntentRef.current.key
      )
    },
    onSuccess: (res) => {
      directIntentRef.current = null
      setDirectAmount('')
      setDirectReason('')
      toast.success(
        t('Margin credited. Current margin balance: {{balance}} USD', {
          balance: res.balance,
        })
      )
      void client.invalidateQueries({ queryKey: siteAdminKeys.all })
      void invalidateSidebarBadges(client)
    },
    onError: (error) => handleServerError(error),
  })
  const wallets = useQuery({
    queryKey: siteAdminKeys.list('wallets', { siteId: props.siteId }),
    queryFn: () => getAdminSiteWallets(props.siteId),
    enabled: isRoot,
  })
  const anomalies = useQuery({
    queryKey: siteAdminKeys.list('fund-anomalies', { siteId: props.siteId }),
    queryFn: () => getAdminFundAnomalies(props.siteId),
    enabled: isRoot,
  })
  if (!isRoot) return null
  // 缓存里可能留着旧形状的响应；列表只认数组，其它一律当空，否则 `.map` 会让整页白屏。
  const walletRows = Array.isArray(wallets.data) ? wallets.data : []
  const anomalyKinds = Array.isArray(anomalies.data) ? anomalies.data : []
  return (
    <section className='flex flex-col gap-5'>
      <div className='flex flex-col gap-2.5'>
        <h3 className='text-sm font-medium'>{t('Wallets')}</h3>
        {wallets.isPending && <LoadingState />}
        {wallets.isError && (
          <ErrorState onRetry={() => void wallets.refetch()} />
        )}
        {wallets.isSuccess && walletRows.length === 0 && (
          <p className='text-muted-foreground text-sm'>{t('No data')}</p>
        )}
        {wallets.isSuccess && walletRows.length > 0 && (
          <dl className='grid gap-3 sm:grid-cols-3'>
            {walletRows.map((wallet) => (
              <div
                key={wallet.id}
                className='bg-muted/40 rounded-lg border p-3 text-sm tabular-nums'
              >
                <dt className='text-muted-foreground text-xs'>
                  {siteWalletKindLabel(t, wallet.kind)}
                </dt>
                <dd className='mt-1 text-base font-semibold'>
                  {wallet.balance} USD
                </dd>
                <div className='text-muted-foreground mt-1 flex flex-col gap-0.5 text-xs'>
                  {Number(wallet.frozen_usd) > 0 && (
                    <span>
                      {t('Frozen')}: {wallet.frozen_usd} USD
                    </span>
                  )}
                  {Number(wallet.excess_frozen_usd) > 0 && (
                    <span>
                      {t('Excess frozen')}: {wallet.excess_frozen_usd} USD
                    </span>
                  )}
                  {wallet.kind === 'margin' && wallet.cycle_tier_usd && (
                    <span>
                      {wallet.cycle_used_usd}/{wallet.cycle_tier_usd} USD
                    </span>
                  )}
                </div>
              </div>
            ))}
          </dl>
        )}
      </div>

      <div className='flex flex-col gap-2.5 border-t pt-4'>
        <h3 className='text-sm font-medium'>{t('Direct margin credit')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Credits the site margin wallet directly and opens a new 90% margin cycle without touching any main-site customer wallet. Excess margin above the site level deposit floor can also be used by the owner to issue customer redemption codes.'
          )}
        </p>
        <div className='grid max-w-2xl gap-3 sm:grid-cols-2'>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='direct-margin-amount'>{t('Amount')}</Label>
            <Input
              id='direct-margin-amount'
              value={directAmount}
              inputMode='decimal'
              autoComplete='off'
              onChange={(event) => setDirectAmount(event.target.value)}
            />
          </div>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='direct-margin-reason'>{t('Reason')}</Label>
            <Input
              id='direct-margin-reason'
              value={directReason}
              onChange={(event) => setDirectReason(event.target.value)}
            />
          </div>
        </div>
        <div>
          <Button
            type='button'
            variant='outline'
            disabled={
              directCredit.isPending ||
              directAmount.trim() === '' ||
              directReason.trim() === ''
            }
            onClick={() => directCredit.mutate()}
          >
            {t('Credit margin directly')}
          </Button>
        </div>
      </div>

      <div className='border-t pt-4'>
        <SiteWalletLedger
          label={t('Wallet ledger')}
          queryKey={siteAdminKeys.list('wallet-ledger', {
            siteId: props.siteId,
          })}
          kinds={['operating', 'earnings', 'margin']}
          fetchPage={(kind, page, pageSize) =>
            getAdminSiteWalletLedger(props.siteId, kind, page, pageSize)
          }
          showOperator
        />
      </div>

      <div className='flex flex-col gap-2.5 border-t pt-4'>
        <h3 className='text-sm font-medium'>{t('Fund anomalies')}</h3>
        {anomalies.isPending && <LoadingState />}
        {anomalies.isError && (
          <ErrorState onRetry={() => void anomalies.refetch()} />
        )}
        {anomalies.isSuccess && anomalyKinds.length === 0 && (
          <p className='text-muted-foreground text-sm'>{t('No data')}</p>
        )}
        {anomalies.isSuccess && anomalyKinds.length > 0 && (
          <ul className='flex flex-col gap-1 text-sm'>
            {anomalyKinds.map((kind) => (
              <li key={kind} className='text-xs'>
                {fundAnomalyKindLabel(kind, t)}
              </li>
            ))}
          </ul>
        )}
        <FundReviewPanel siteId={props.siteId} />
      </div>
    </section>
  )
}

export function FundReviewPanel(props: { siteId: number }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const [note, setNote] = useState('')
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [kind, setKind] = useState('')
  const [order, setOrder] = useState('')
  const filters = { status, kind, order_ref: order, p: page, page_size: 20 }
  const dispositions = useQuery({
    queryKey: siteAdminKeys.list('dispositions', {
      siteId: props.siteId,
      ...filters,
    }),
    queryFn: () => getAdminAnomalyDispositions(props.siteId, filters),
    enabled: isRoot,
  })
  const review = useMutation({
    mutationFn: (input: { id: number; status: 'reviewing' | 'closed' }) =>
      reviewAdminAnomalyDisposition(input.id, input.status, note.trim()),
    onSuccess: () => {
      setNote('')
      void client.invalidateQueries({ queryKey: siteAdminKeys.all })
      void invalidateSidebarBadges(client)
    },
    onError: (error) => handleServerError(error),
  })
  // 异常处置列表同样是「成功但形状不可信」的高危点：崩了站长就看不出哪笔资金
  // 异常还没处置完，所以和 wallets/anomalies 一样只认数组。
  const dispositionRows = Array.isArray(dispositions.data?.items)
    ? dispositions.data.items
    : []
  if (!isRoot) return null
  return (
    <section className='flex flex-col gap-3 pt-2'>
      <h4 className='text-sm font-medium'>{t('Financial review history')}</h4>
      <div className='grid max-w-3xl gap-3 sm:grid-cols-3'>
        <div className='flex flex-col gap-1'>
          <Label htmlFor='disposition-status'>{t('Status')}</Label>
          <NativeSelect
            id='disposition-status'
            value={status}
            onChange={(e) => {
              setStatus(e.target.value)
              setPage(1)
            }}
          >
            <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
            <NativeSelectOption value='open'>{t('Open')}</NativeSelectOption>
            <NativeSelectOption value='reviewing'>
              {t('Reviewing')}
            </NativeSelectOption>
            <NativeSelectOption value='closed'>{t('Closed')}</NativeSelectOption>
          </NativeSelect>
        </div>
        <div className='flex flex-col gap-1'>
          <Label htmlFor='disposition-order'>{t('Order number')}</Label>
          <Input
            id='disposition-order'
            value={order}
            onChange={(e) => {
              setOrder(e.target.value)
              setPage(1)
            }}
          />
        </div>
        <div className='flex flex-col gap-1'>
          <Label htmlFor='disposition-kind'>{t('Anomaly type')}</Label>
          <Input
            id='disposition-kind'
            value={kind}
            onChange={(e) => {
              setKind(e.target.value)
              setPage(1)
            }}
          />
        </div>
      </div>
      {dispositions.isPending && <LoadingState />}
      {dispositions.isError && (
        <ErrorState onRetry={() => void dispositions.refetch()} />
      )}
      {dispositions.isSuccess && dispositionRows.length === 0 && (
        <p className='text-sm'>{t('No data')}</p>
      )}
      <PageSwitcher
        page={page}
        pageSize={20}
        total={dispositions.data?.total ?? 0}
        onPageChange={setPage}
      />
      {dispositions.isSuccess && dispositionRows.length > 0 && (
        <div className='flex flex-col gap-2'>
          <Label htmlFor='anomaly-note'>{t('Note')}</Label>
          <Input
            id='anomaly-note'
            value={note}
            onChange={(event) => setNote(event.target.value)}
          />
          {dispositionRows.map((row) => (
            <div
              key={row.id}
              className='flex flex-wrap items-center gap-2 text-sm'
            >
              <span className='text-xs'>
                {fundAnomalyKindLabel(row.kind, t)}
              </span>
              <span>{dispositionStatusLabel(row.status, t)}</span>
              {row.difference ? (
                <span className='tabular-nums'>{row.difference}</span>
              ) : null}
              {row.team_id ? (
                <span>
                  {t('Team')} #{row.team_id}
                </span>
              ) : null}
              {row.account_id ? <span>{row.account_id}</span> : null}
              {row.order_ref ? (
                <span className='font-mono text-xs'>{row.order_ref}</span>
              ) : null}
              {row.evidence ? (
                <span className='text-muted-foreground text-xs break-all'>
                  {row.evidence}
                </span>
              ) : null}
              {row.status !== 'closed' && (
                <>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={review.isPending || row.status === 'reviewing'}
                    onClick={() =>
                      review.mutate({ id: row.id, status: 'reviewing' })
                    }
                  >
                    {t('Mark reviewing')}
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={review.isPending || note.trim() === ''}
                    onClick={() =>
                      review.mutate({ id: row.id, status: 'closed' })
                    }
                  >
                    {t('Close')}
                  </Button>
                </>
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
