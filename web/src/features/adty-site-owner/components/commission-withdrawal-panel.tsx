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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { adminReferralKeys } from '@/features/admin-referral/api'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { usdtNetworkLabel } from '@/features/referral/lib/address'
import {
  WITHDRAWAL_STATUS_LABELS,
  WITHDRAWAL_TARGET_LABELS,
} from '@/features/referral/lib/labels'
import { WithdrawalCancellation } from '@/features/wallet/adty/withdrawal-cancellation'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getOwnerCommissionWithdrawals,
  OWNER_PAGE_SIZE,
  markOwnerCommissionWithdrawalPaid,
  reviewOwnerCommissionWithdrawal,
  siteOwnerKeys,
  type SiteInviteCommissionWithdrawal,
} from '../api'

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

function Payee(props: { row: SiteInviteCommissionWithdrawal }) {
  const { t } = useTranslation()
  const row = props.row
  if (row.method === 'usdt') {
    return (
      <div className='flex flex-col gap-1'>
        <span>{usdtNetworkLabel(row.network)}</span>
        <div className='flex items-center gap-1'>
          <span className='font-mono text-sm break-all'>{row.account_no}</span>
          <CopyButton
            value={row.account_no}
            size='icon'
            aria-label={t('Copy address')}
          />
        </div>
      </div>
    )
  }
  if (row.method === 'balance') return <span>{t('Account balance')}</span>
  return (
    <div className='flex items-center gap-2'>
      {row.qr_image_url !== '' && (
        <a href={row.qr_image_url} target='_blank' rel='noreferrer'>
          <img
            src={row.qr_image_url}
            alt={t('Payment QR code')}
            className='h-16 w-16 rounded-lg border object-contain'
          />
        </a>
      )}
      <div className='flex flex-col'>
        <span>{t(methodLabel(row.method))}</span>
        <span>{row.account_name}</span>
      </div>
    </div>
  )
}

function ReviewRow(props: {
  closed?: boolean
  canReview?: boolean
  canPay?: boolean
  row: SiteInviteCommissionWithdrawal
  onDone: () => void
}) {
  const { t } = useTranslation()
  const row = props.row
  const [reason, setReason] = useState('')
  const [txId, setTxId] = useState('')
  const [proof, setProof] = useState('')
  const review = useMutation({
    mutationFn: (approve: boolean) =>
      props.closed
        ? reviewOwnerCommissionWithdrawal(
            row.id,
            approve,
            reason,
            true,
            row.status === 'approved'
          )
        : reviewOwnerCommissionWithdrawal(row.id, approve, reason),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      setReason('')
      props.onDone()
    },
    onError: (err) => handleServerError(err),
  })
  const pay = useMutation({
    mutationFn: () =>
      props.closed
        ? markOwnerCommissionWithdrawalPaid(
            row.id,
            txId.trim(),
            proof.trim(),
            true
          )
        : markOwnerCommissionWithdrawalPaid(row.id, txId.trim(), proof.trim()),
    onSuccess: () => {
      toast.success(t('Marked as paid'))
      setTxId('')
      setProof('')
      props.onDone()
    },
    onError: (err) => handleServerError(err),
  })
  return (
    <li className='flex flex-col gap-2 text-sm'>
      <p className='tabular-nums'>
        {props.closed && (
          <>
            {t('Site')} #{row.site_id} ·{' '}
          </>
        )}
        #{row.id} · {row.username || row.user_id} · {row.amount_usd} USD ·{' '}
        {t(methodLabel(row.method))} · {t(statusLabel(row.status))}
        {row.payout_amount !== '' &&
          ` · ${row.payout_amount} ${row.payout_currency}`}
      </p>
      {row.method === 'usdt' && (
        <p>
          {t('USDT fee (USD)')} {row.fee_usd}
        </p>
      )}
      <WithdrawalCancellation state={row} />
      {[
        [
          t('Legacy decision record'),
          row.legacy_decision_by,
          row.legacy_decision_at,
        ],
        [t('Withdrawal reviewer'), row.reviewed_by, row.reviewed_at],
        [t('Payment recorded by'), row.paid_by, row.paid_at],
        [t('Failed payout recorded by'), row.failure_by, row.failure_at],
      ].map(([label, actor, at]) =>
        at ? (
          <p key={label}>
            {label} {actor ? `#${actor}` : t('Unknown')} ·{' '}
            {new Date(Number(at) * 1000).toLocaleString()}
          </p>
        ) : null
      )}
      {row.failure_at ? <p>{row.reject_reason}</p> : null}
      <Payee row={row} />
      {row.status === 'paid' && row.tx_id && (
        <p>
          {t('Transfer reference')} {row.tx_id}
        </p>
      )}
      {row.status === 'paid' && row.proof_note && (
        <p>
          {t('Proof note')}{' '}
          {row.proof_note.startsWith('/api/referral-assets/') ? (
            <a href={row.proof_note} target='_blank' rel='noreferrer'>
              {t('Payment proof')}
            </a>
          ) : (
            row.proof_note
          )}
        </p>
      )}
      {props.canReview !== false &&
        row.status === 'pending' &&
        row.method !== 'balance' && (
          <div className='flex flex-col gap-2'>
            <Label htmlFor={`commission-reject-${row.id}`}>{t('Reason')}</Label>
            <Input
              id={`commission-reject-${row.id}`}
              value={reason}
              autoComplete='off'
              onChange={(event) => setReason(event.target.value)}
            />
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                variant='outline'
                disabled={review.isPending}
                onClick={() => review.mutate(true)}
              >
                {t('Approve')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={review.isPending}
                onClick={() => review.mutate(false)}
              >
                {t('Reject')}
              </Button>
            </div>
          </div>
        )}
      {row.status === 'approved' && (
        <form
          className='flex flex-col gap-2'
          onSubmit={(event) => {
            event.preventDefault()
            if (props.canPay !== false) pay.mutate()
          }}
        >
          {(row.method === 'usdt' || row.method === 'alipay') && (
            <div className='flex flex-col gap-2'>
              <Label htmlFor={`commission-tx-${row.id}`}>
                {row.method === 'usdt'
                  ? t('Transaction hash')
                  : t('Transfer reference')}
              </Label>
              <Input
                id={`commission-tx-${row.id}`}
                value={txId}
                autoComplete='off'
                onChange={(event) => setTxId(event.target.value)}
                required={row.method === 'usdt'}
              />
            </div>
          )}
          <div className='flex flex-col gap-2'>
            <Label htmlFor={`commission-proof-${row.id}`}>
              {t('Proof note')}
            </Label>
            <Input
              id={`commission-proof-${row.id}`}
              value={proof}
              autoComplete='off'
              onChange={(event) => setProof(event.target.value)}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <Label htmlFor={`commission-fail-${row.id}`}>
              {t('Failed payout reference')}
            </Label>
            <Input
              id={`commission-fail-${row.id}`}
              value={reason}
              autoComplete='off'
              onChange={(event) => setReason(event.target.value)}
            />
          </div>
          <div className='flex flex-wrap gap-2'>
            <Button
              type='submit'
              variant='outline'
              disabled={
                props.canPay === false ||
                pay.isPending ||
                (!txId.trim() &&
                  (row.method !== 'alipay' ||
                    !proof.trim().startsWith('/api/referral-assets/')))
              }
            >
              {t('Mark as paid')}
            </Button>
            <Button
              type='button'
              variant='outline'
              disabled={
                props.canPay === false || review.isPending || !reason.trim()
              }
              onClick={() => review.mutate(false)}
            >
              {t('Confirm failed payout')}
            </Button>
          </div>
        </form>
      )}
    </li>
  )
}

/** The site owner reviews customer invite-commission withdrawals. The platform handles closed sites. */
export function CommissionWithdrawalPanel(
  props: {
    closed?: boolean
    canReview?: boolean
    canPay?: boolean
    status?: string
  } = {}
) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [page, setPage] = useState(1)
  useEffect(() => {
    if (window.location.hash === '#pending') {
      document.querySelector('#pending')?.scrollIntoView()
    }
  }, [])
  const [ownStatus, setStatus] = useState(() =>
    window.location.hash === '#pending' ? 'pending' : 'actionable'
  )
  const status = props.status ?? ownStatus
  const list = useQuery({
    queryKey: siteOwnerKeys.list('commission-withdrawals', {
      page,
      status,
      closed: props.closed,
    }),
    queryFn: () =>
      props.closed
        ? getOwnerCommissionWithdrawals(page, status, true)
        : getOwnerCommissionWithdrawals(page, status),
  })
  // A decision can remove the last request on the current filtered page.
  const total = list.data?.total
  useEffect(() => {
    if (total === undefined) return
    const lastPage = Math.max(1, Math.ceil(total / OWNER_PAGE_SIZE))
    if (page > lastPage) setPage(lastPage)
  }, [page, total])
  const refresh = () => {
    void client.invalidateQueries({ queryKey: siteOwnerKeys.all })
    if (props.closed) {
      void client.invalidateQueries({ queryKey: adminReferralKeys.badges })
    }
    void invalidateSidebarBadges(client)
  }

  return (
    <section
      id='pending'
      className='flex flex-col gap-4'
      aria-label={t('Invite commission withdrawal')}
    >
      <div className='flex flex-col gap-1'>
        <h2 className='text-sm font-medium'>
          {props.closed
            ? t('Closed-site commission withdrawals')
            : t('Invite commission withdrawal')}
        </h2>
        <p className='text-muted-foreground text-sm'>
          {props.closed
            ? t('Process withdrawals for closed sites.')
            : t(
                'The site owner reviews cash withdrawals of invite commission.'
              )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'The platform or its authorized payer pays the customer offline. Recording payment releases the site’s hold.'
          )}
        </p>
      </div>
      {props.status === undefined && (
        <div className='flex items-center gap-2'>
          <Label htmlFor='commission-status'>{t('Status')}</Label>
          <select
            id='commission-status'
            className='bg-background h-9 rounded-md border px-3'
            value={status}
            onChange={(event) => {
              setStatus(event.target.value)
              setPage(1)
            }}
          >
            <option value='actionable'>
              {t('Awaiting review or payment')}
            </option>
            <option value=''>{t('All')}</option>
            {['pending', 'approved', 'paid', 'rejected', 'canceled'].map(
              (value) => (
                <option key={value} value={value}>
                  {t(statusLabel(value))}
                </option>
              )
            )}
          </select>
        </div>
      )}
      {list.isPending && <LoadingState />}
      {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
      {list.isSuccess && list.data.items.length === 0 && (
        <p className='text-muted-foreground text-sm'>
          {t('No withdrawals yet')}
        </p>
      )}
      <ul className='flex flex-col gap-4'>
        {(list.data?.items ?? []).map((row) => (
          <ReviewRow {...props} key={row.id} row={row} onDone={refresh} />
        ))}
      </ul>
      {list.data && (
        <PageSwitcher
          page={page}
          total={list.data.total}
          pageSize={OWNER_PAGE_SIZE}
          onPageChange={setPage}
        />
      )}
    </section>
  )
}
