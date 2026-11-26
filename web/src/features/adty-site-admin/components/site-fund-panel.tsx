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
import { useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { siteWalletKindLabel } from '@/features/adty-ledger/site-wallet-labels'
import { SiteWalletLedger } from '@/features/adty-ledger/site-wallet-ledger'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { formatUserIdentity } from '@/lib/format-user-identity'
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
import type { SiteStatus } from '../types'

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
export function SiteFundPanel(props: {
  siteId: number
  siteStatus?: SiteStatus
  actions?: ReactNode
}) {
  const { t } = useTranslation()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const directMarginLocked =
    props.siteStatus === 'closed' || props.siteStatus === 'deleting'
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
    <div className='flex flex-col gap-8'>
      <ContentSection title={t('Wallets')} actions={props.actions}>
        {wallets.isPending && <LoadingState />}
        {wallets.isError && (
          <ErrorState onRetry={() => void wallets.refetch()} />
        )}
        {wallets.isSuccess && walletRows.length === 0 && (
          <EmptyState className='min-h-24' title={t('No data')} bordered />
        )}
        {wallets.isSuccess && walletRows.length > 0 && (
          <dl
            className={
              walletRows.length === 2
                ? 'border-border bg-border grid grid-cols-1 gap-px overflow-hidden rounded-[14px] border sm:grid-cols-2'
                : 'border-border bg-border grid grid-cols-1 gap-px overflow-hidden rounded-[14px] border sm:grid-cols-3'
            }
          >
            {walletRows.map((wallet) => (
              <div
                key={wallet.id}
                className='bg-card flex flex-col gap-1.5 px-5 py-4 tabular-nums'
              >
                <dt className='text-dim text-[12px] leading-none'>
                  {siteWalletKindLabel(t, wallet.kind)}
                </dt>
                <dd className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
                  {wallet.balance} USD
                </dd>
                {(Number(wallet.frozen_usd) > 0 ||
                  Number(wallet.excess_frozen_usd) > 0 ||
                  (wallet.kind === 'margin' && wallet.cycle_tier_usd)) && (
                  <div className='text-dim flex flex-col gap-0.5 text-xs'>
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
                )}
              </div>
            ))}
          </dl>
        )}
      </ContentSection>

      <SettingsSection
        title={t('Direct margin credit')}
        description={t(
          'Credits the site margin wallet directly and opens a new 90% margin cycle without touching any main-site customer wallet. Excess margin above the site level deposit floor can also be used by the owner to issue customer redemption codes.'
        )}
      >
        <div className='flex max-w-2xl flex-col gap-4'>
          <div className='grid gap-4 sm:grid-cols-[11rem_minmax(0,1fr)]'>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor='direct-margin-amount'>{t('Amount')}</Label>
              <Input
                id='direct-margin-amount'
                value={directAmount}
                inputMode='decimal'
                autoComplete='off'
                disabled={directMarginLocked || directCredit.isPending}
                onChange={(event) => setDirectAmount(event.target.value)}
              />
            </div>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor='direct-margin-reason'>{t('Reason')}</Label>
              <Input
                id='direct-margin-reason'
                value={directReason}
                disabled={directMarginLocked || directCredit.isPending}
                onChange={(event) => setDirectReason(event.target.value)}
              />
            </div>
          </div>
          {directMarginLocked && (
            <p className='text-dim text-xs'>{t('Site is closed')}</p>
          )}
          <div>
            <Button
              type='button'
              variant='brand'
              disabled={
                directMarginLocked ||
                directCredit.isPending ||
                directAmount.trim() === '' ||
                directReason.trim() === ''
              }
              onClick={() => {
                if (!directMarginLocked) directCredit.mutate()
              }}
            >
              {t('Credit margin directly')}
            </Button>
          </div>
        </div>
      </SettingsSection>

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

      <ContentSection title={t('Fund anomalies')}>
        {anomalies.isPending && <LoadingState />}
        {anomalies.isError && (
          <ErrorState onRetry={() => void anomalies.refetch()} />
        )}
        {anomalies.isSuccess && anomalyKinds.length === 0 && (
          <EmptyState className='min-h-20' title={t('No data')} bordered />
        )}
        {anomalies.isSuccess && anomalyKinds.length > 0 && (
          <ul className='border-border divide-border bg-card divide-y overflow-hidden rounded-[14px] border text-sm'>
            {anomalyKinds.map((kind) => (
              <li key={kind} className='px-5 py-3 text-[13px]'>
                {fundAnomalyKindLabel(kind, t)}
              </li>
            ))}
          </ul>
        )}
      </ContentSection>

      <FundReviewPanel siteId={props.siteId} />
    </div>
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
    <ContentSection
      title={t('Financial review history')}
      actions={
        <>
          <NativeSelect
            id='disposition-status'
            aria-label={t('Status')}
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
            <NativeSelectOption value='closed'>
              {t('Closed')}
            </NativeSelectOption>
          </NativeSelect>
          <Input
            id='disposition-order'
            className='w-48'
            aria-label={t('Order number')}
            placeholder={t('Order number')}
            value={order}
            onChange={(e) => {
              setOrder(e.target.value)
              setPage(1)
            }}
          />
          <Input
            id='disposition-kind'
            className='w-48'
            aria-label={t('Anomaly type')}
            placeholder={t('Anomaly type')}
            value={kind}
            onChange={(e) => {
              setKind(e.target.value)
              setPage(1)
            }}
          />
        </>
      }
    >
      {dispositions.isPending && <LoadingState />}
      {dispositions.isError && (
        <ErrorState onRetry={() => void dispositions.refetch()} />
      )}
      {dispositions.isSuccess && dispositionRows.length === 0 && (
        <EmptyState className='min-h-24' title={t('No data')} bordered />
      )}
      {dispositions.isSuccess && dispositionRows.length > 0 && (
        <div className='flex flex-col gap-3'>
          <div className='flex max-w-md flex-col gap-1.5'>
            <Label htmlFor='anomaly-note'>{t('Note')}</Label>
            <Input
              id='anomaly-note'
              value={note}
              onChange={(event) => setNote(event.target.value)}
            />
          </div>
          <div className='border-border divide-border bg-card divide-y overflow-hidden rounded-[14px] border'>
            {dispositionRows.map((row) => (
              <div
                key={row.id}
                className='flex flex-wrap items-center justify-between gap-3 px-5 py-3 text-sm'
              >
                <div className='flex min-w-0 flex-wrap items-center gap-2'>
                  <span className='font-medium'>
                    {fundAnomalyKindLabel(row.kind, t)}
                  </span>
                  <span className='text-dim text-xs'>
                    {dispositionStatusLabel(row.status, t)}
                  </span>
                  {row.difference ? (
                    <span className='tabular-nums'>{row.difference}</span>
                  ) : null}
                  {row.team_id ? (
                    <span>
                      {t('Team')} #{row.team_id}
                    </span>
                  ) : null}
                  {row.account_id ? (
                    <span className='tabular-nums'>
                      {formatUserIdentity(row.account_id, row.account_username)}
                    </span>
                  ) : null}
                  {row.order_ref ? (
                    <span className='font-mono text-xs'>{row.order_ref}</span>
                  ) : null}
                  {row.evidence ? (
                    <span className='text-dim text-xs break-all'>
                      {row.evidence}
                    </span>
                  ) : null}
                </div>
                {row.status !== 'closed' && (
                  <div className='flex shrink-0 flex-wrap gap-1.5'>
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
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}
      <PageSwitcher
        page={page}
        pageSize={20}
        total={dispositions.data?.total ?? 0}
        onPageChange={setPage}
      />
    </ContentSection>
  )
}
