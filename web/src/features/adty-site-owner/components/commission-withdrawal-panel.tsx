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
import { ExternalLink, Upload, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { adminReferralKeys } from '@/features/admin-referral/api'
import { uploadReferralAsset } from '@/features/referral/api'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { usdtNetworkLabel } from '@/features/referral/lib/address'
import {
  WITHDRAWAL_STATUS_LABELS,
  WITHDRAWAL_STATUS_VARIANTS,
  WITHDRAWAL_TARGET_LABELS,
} from '@/features/referral/lib/labels'
import type { ReferralWithdrawalStatus } from '@/features/referral/types'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { WithdrawalCancellation } from '@/features/wallet/adty/withdrawal-cancellation'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import {
  getOwnerCommissionWithdrawals,
  OWNER_PAGE_SIZE,
  markOwnerCommissionWithdrawalPaid,
  reviewOwnerCommissionWithdrawal,
  siteOwnerKeys,
  uploadOwnerWithdrawalQR,
  type SiteInviteCommissionWithdrawal,
} from '../api'

type CommissionWithdrawalRow = SiteInviteCommissionWithdrawal & {
  legacy_decision_by_username?: string
  reviewed_by_username?: string
  paid_by_username?: string
  failure_by_username?: string
}

const WITHDRAWAL_STATUS_OPTIONS: ReferralWithdrawalStatus[] = [
  'pending',
  'approved',
  'paid',
  'rejected',
  'canceled',
]

function methodLabel(method: string): string {
  if (method === 'wxpay') return WITHDRAWAL_TARGET_LABELS.wechat
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

function statusVariant(status: string): StatusVariant {
  if (status in WITHDRAWAL_STATUS_VARIANTS) {
    return WITHDRAWAL_STATUS_VARIANTS[
      status as keyof typeof WITHDRAWAL_STATUS_VARIANTS
    ]
  }
  return 'neutral'
}

function formatPayoutDisplay(
  row: CommissionWithdrawalRow,
  locale?: string
): string {
  if (row.payout_amount !== '') {
    return `${row.payout_amount} ${row.payout_currency}`.trim()
  }
  if (row.method === 'usdt' && row.net_usd) {
    return `${row.net_usd} USDT`
  }
  return formatUsd(
    Number(row.amount_usd || 0),
    locale,
    row.method === 'usdt' ? 6 : 2
  )
}

type QrPreviewInfo = {
  url: string
  name?: string
  accountNo?: string
}

function PayeeCell(props: {
  row: CommissionWithdrawalRow
  onPreviewQr: (info: QrPreviewInfo) => void
}) {
  const { t } = useTranslation()
  const row = props.row
  if (row.method === 'usdt') {
    return (
      <div className='flex flex-col gap-1'>
        <div className='flex flex-wrap items-center gap-1.5'>
          <span className='font-medium'>{t(methodLabel(row.method))}</span>
          {row.network && (
            <Badge variant='secondary'>{usdtNetworkLabel(row.network)}</Badge>
          )}
        </div>
        {row.account_no && (
          <div className='flex items-center gap-1'>
            <span className='font-mono text-xs break-all'>
              {row.account_no}
            </span>
            <CopyButton
              value={row.account_no}
              size='icon'
              aria-label={t('Copy address')}
            />
          </div>
        )}
      </div>
    )
  }
  if (row.method === 'balance') {
    return <span>{t('Account balance')}</span>
  }
  return (
    <div className='flex items-center gap-2'>
      {row.qr_image_url !== '' && (
        <button
          type='button'
          className='shrink-0 cursor-zoom-in rounded-md border bg-white p-0.5 transition hover:opacity-80'
          aria-label={t('Preview payment QR code')}
          onClick={() =>
            props.onPreviewQr({
              url: row.qr_image_url,
              name: row.account_name,
              accountNo: row.account_no,
            })
          }
        >
          <img
            src={row.qr_image_url}
            alt={t('Payment QR code')}
            className='h-14 w-14 rounded object-contain'
          />
        </button>
      )}
      <div className='flex min-w-0 flex-col gap-0.5'>
        <span className='font-medium'>{t(methodLabel(row.method))}</span>
        {row.account_name && (
          <span className='text-sm font-medium'>{row.account_name}</span>
        )}
        {row.account_no && (
          <div className='flex items-center gap-1'>
            <span className='text-muted-foreground font-mono text-xs break-all'>
              {row.account_no}
            </span>
            <CopyButton
              value={row.account_no}
              size='icon'
              aria-label={t('Copy account')}
            />
          </div>
        )}
      </div>
    </div>
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
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
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

  const [approving, setApproving] = useState<CommissionWithdrawalRow | null>(
    null
  )
  const [rejecting, setRejecting] = useState<CommissionWithdrawalRow | null>(
    null
  )
  const [paying, setPaying] = useState<CommissionWithdrawalRow | null>(null)
  const [failing, setFailing] = useState<CommissionWithdrawalRow | null>(null)
  const [previewQr, setPreviewQr] = useState<QrPreviewInfo | null>(null)

  const [approveNote, setApproveNote] = useState('')
  const [rejectReason, setRejectReason] = useState('')
  const [txId, setTxId] = useState('')
  const [proof, setProof] = useState('')
  const [failReason, setFailReason] = useState('')
  const proofFileRef = useRef<HTMLInputElement | null>(null)
  const activePayingIdRef = useRef<number | null>(null)

  const resetPayForm = () => {
    setTxId('')
    setProof('')
    if (proofFileRef.current) proofFileRef.current.value = ''
  }

  const openPaying = (row: CommissionWithdrawalRow) => {
    activePayingIdRef.current = row.id
    resetPayForm()
    setPaying(row)
  }

  const closePaying = () => {
    activePayingIdRef.current = null
    setPaying(null)
    resetPayForm()
  }

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

  const uploadProof = useMutation({
    mutationFn: (input: { file: File; withdrawalId: number }) =>
      props.closed
        ? uploadReferralAsset(input.file, '/api/user/admin/referral/upload')
        : uploadOwnerWithdrawalQR(input.file, 'payment_proof'),
    onSuccess: (url, input) => {
      if (activePayingIdRef.current !== input.withdrawalId) {
        return
      }
      setProof(url)
      toast.success(t('Payment proof uploaded'))
    },
    onError: (err) => handleServerError(err),
  })

  const review = useMutation({
    mutationFn: (input: {
      row: CommissionWithdrawalRow
      approve: boolean
      reason: string
    }) =>
      props.closed
        ? reviewOwnerCommissionWithdrawal(
            input.row.id,
            input.approve,
            input.reason,
            true,
            input.row.status === 'approved'
          )
        : reviewOwnerCommissionWithdrawal(
            input.row.id,
            input.approve,
            input.reason
          ),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      setApproving(null)
      setRejecting(null)
      setFailing(null)
      setApproveNote('')
      setRejectReason('')
      setFailReason('')
      refresh()
    },
    onError: (err) => handleServerError(err),
  })

  const pay = useMutation({
    mutationFn: (row: CommissionWithdrawalRow) =>
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
      closePaying()
      refresh()
    },
    onError: (err) => handleServerError(err),
  })

  const rows: CommissionWithdrawalRow[] = list.data?.items ?? []

  const payDisabled =
    !paying ||
    props.canPay === false ||
    pay.isPending ||
    uploadProof.isPending ||
    (!txId.trim() &&
      (paying.method !== 'alipay' ||
        !proof.trim().startsWith('/api/referral-assets/')))

  return (
    <section
      id='pending'
      className={cn(
        'flex flex-col gap-4',
        props.closed && 'border-border mt-6 border-t pt-6'
      )}
      aria-label={t('Invite commission withdrawal')}
    >
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='flex flex-col gap-1'>
          <h3 className='text-[15px] leading-snug font-semibold tracking-tight [word-break:keep-all]'>
            {props.closed
              ? t('Closed-site commission withdrawals')
              : t('Invite commission withdrawal')}
          </h3>
          <p className='text-dim max-w-[80ch] text-[12.5px] leading-relaxed'>
            {props.closed
              ? t('Process withdrawals for closed sites.')
              : t(
                  'The site owner reviews cash withdrawals of invite commission.'
                )}{' '}
            {props.closed
              ? t(
                  'The platform pays the customer offline. Recording payment deducts the closed site’s held funds.'
                )
              : t(
                  'The platform or its authorized payer pays the customer offline. Recording payment releases the site’s hold.'
                )}
          </p>
        </div>
        {props.status === undefined && (
          <NativeSelect
            id='commission-status'
            className='w-52'
            aria-label={t('Status')}
            value={status}
            onChange={(event) => {
              setStatus(event.target.value)
              setPage(1)
            }}
          >
            <NativeSelectOption value='actionable'>
              {t('Awaiting review or payment')}
            </NativeSelectOption>
            <NativeSelectOption value=''>
              {t('All statuses')}
            </NativeSelectOption>
            {WITHDRAWAL_STATUS_OPTIONS.map((value) => (
              <NativeSelectOption key={value} value={value}>
                {t(statusLabel(value))}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        )}
      </div>

      {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
      {list.isPending && !list.data ? (
        <LoadingState />
      ) : (
        <>
          <StaticDataTable
            tableClassName='min-w-[1040px]'
            tableProps={{ 'aria-label': t('Invite commission withdrawal') }}
            data={rows}
            getRowKey={(row) => String(row.id)}
            emptyContent={
              <EmptyState
                className='min-h-32'
                title={t('No withdrawals yet')}
              />
            }
            columns={[
              {
                id: 'time',
                header: t('Time'),
                cellClassName: 'tabular-nums whitespace-nowrap',
                cell: (row) => {
                  const createdAt = row.created_at ?? 0
                  const hasTime = createdAt > 0
                  let subtitle: string | null = null
                  if (props.closed && row.site_id) {
                    subtitle = `${t('Site')} #${row.site_id} · #${row.id}`
                  } else if (hasTime) {
                    subtitle = `#${row.id}`
                  }
                  return (
                    <div className='flex flex-col gap-0.5'>
                      <span>
                        {hasTime ? formatTimestamp(createdAt) : `#${row.id}`}
                      </span>
                      {subtitle && (
                        <span className='text-muted-foreground text-xs'>
                          {subtitle}
                        </span>
                      )}
                    </div>
                  )
                },
              },
              {
                id: 'user',
                header: t('User'),
                cellClassName: 'tabular-nums',
                cell: (row) => (
                  <span className='font-medium'>
                    {formatUserIdentity(row.user_id, row.username)}
                  </span>
                ),
              },
              {
                id: 'payee',
                header: t('Pay to'),
                cell: (row) => (
                  <PayeeCell row={row} onPreviewQr={setPreviewQr} />
                ),
              },
              {
                id: 'amount',
                header: t('Amount'),
                className: 'text-right',
                cellClassName: 'text-right tabular-nums',
                cell: (row) => {
                  const payoutDisplay = formatPayoutDisplay(row, locale)
                  const requestedDisplay = formatUsd(
                    Number(row.amount_usd || 0),
                    locale,
                    row.method === 'usdt' ? 6 : 2
                  )
                  const hasUsdtFee =
                    row.method === 'usdt' && Number(row.fee_usd || 0) > 0
                  return (
                    <div className='flex flex-col items-end gap-0.5'>
                      <span className='font-semibold'>{payoutDisplay}</span>
                      {payoutDisplay !== requestedDisplay && (
                        <span className='text-muted-foreground text-xs'>
                          {requestedDisplay}
                        </span>
                      )}
                      {hasUsdtFee && (
                        <span className='text-muted-foreground text-xs'>
                          {t('USDT fee (USD)')}: {row.fee_usd}
                        </span>
                      )}
                    </div>
                  )
                },
              },
              {
                id: 'status',
                header: t('Status'),
                cell: (row) => {
                  const auditEntries = props.closed
                    ? [
                        {
                          label: t('Legacy decision record'),
                          actor: row.legacy_decision_by,
                          actorUsername: row.legacy_decision_by_username,
                          at: row.legacy_decision_at,
                        },
                        {
                          label: t('Withdrawal reviewer'),
                          actor: row.reviewed_by,
                          actorUsername: row.reviewed_by_username,
                          at: row.reviewed_at,
                        },
                        {
                          label: t('Payment recorded by'),
                          actor: row.paid_by,
                          actorUsername: row.paid_by_username,
                          at: row.paid_at,
                        },
                        {
                          label: t('Failed payout recorded by'),
                          actor: row.failure_by,
                          actorUsername: row.failure_by_username,
                          at: row.failure_at,
                        },
                      ].filter((entry) => Boolean(entry.at))
                    : []
                  return (
                    <div className='flex max-w-xs flex-col items-start gap-1'>
                      <StatusBadge
                        label={t(statusLabel(row.status))}
                        variant={statusVariant(row.status)}
                        copyable={false}
                      />
                      {row.status === 'pending' &&
                        (row.can_cancel || row.cancel_deadline > 0) && (
                          <div className='text-muted-foreground text-xs [&_span]:text-xs'>
                            <WithdrawalCancellation state={row} />
                          </div>
                        )}
                      {row.status === 'paid' && row.tx_id && (
                        <span className='text-muted-foreground font-mono text-xs break-all'>
                          {row.tx_id}
                        </span>
                      )}
                      {row.status === 'paid' && row.proof_note && (
                        <div className='text-muted-foreground flex items-center gap-1.5 text-xs'>
                          {row.proof_note.startsWith(
                            '/api/referral-assets/'
                          ) ? (
                            <a
                              href={row.proof_note}
                              target='_blank'
                              rel='noopener noreferrer'
                              className='underline'
                            >
                              {t('Payment proof')}
                            </a>
                          ) : (
                            <span className='break-words'>
                              {row.proof_note}
                            </span>
                          )}
                        </div>
                      )}
                      {row.reject_reason && (
                        <span className='text-destructive/90 text-xs break-words'>
                          {row.reject_reason}
                        </span>
                      )}
                      {auditEntries.map(
                        ({ label, actor, actorUsername, at }) => (
                          <span
                            key={label}
                            className='text-muted-foreground text-[11px] tabular-nums'
                          >
                            {label}{' '}
                            {actor || actorUsername?.trim()
                              ? formatUserIdentity(actor, actorUsername)
                              : t('Unknown')}{' '}
                            · {formatTimestamp(Number(at))}
                          </span>
                        )
                      )}
                    </div>
                  )
                },
              },
              {
                id: 'actions',
                header: t('Actions'),
                className: 'text-right',
                cellClassName: 'text-right',
                cell: (row) => (
                  <div className='flex flex-wrap justify-end gap-1.5'>
                    {props.canReview !== false &&
                      row.status === 'pending' &&
                      row.method !== 'balance' && (
                        <>
                          <Button
                            size='sm'
                            disabled={review.isPending}
                            onClick={() => {
                              setApproveNote('')
                              setApproving(row)
                            }}
                          >
                            {t('Approve')}
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            disabled={review.isPending}
                            onClick={() => {
                              setRejectReason('')
                              setRejecting(row)
                            }}
                          >
                            {t('Reject')}
                          </Button>
                        </>
                      )}
                    {row.status === 'approved' && props.canPay !== false && (
                      <>
                        <Button
                          size='sm'
                          disabled={pay.isPending}
                          onClick={() => openPaying(row)}
                        >
                          {t('Mark as paid')}
                        </Button>
                        <Button
                          size='sm'
                          variant='outline'
                          disabled={review.isPending}
                          onClick={() => {
                            setFailReason('')
                            setFailing(row)
                          }}
                        >
                          {t('Confirm failed payout')}
                        </Button>
                      </>
                    )}
                  </div>
                ),
              },
            ]}
          />
          {list.data && (
            <PageSwitcher
              page={page}
              total={list.data.total}
              pageSize={OWNER_PAGE_SIZE}
              onPageChange={setPage}
            />
          )}
        </>
      )}

      {/* Approve Dialog */}
      <Dialog
        open={approving !== null}
        onOpenChange={(open) => {
          if (!open) setApproving(null)
        }}
        title={t('Approve withdrawal')}
        description={
          approving
            ? `${formatUserIdentity(approving.user_id, approving.username)} · ${formatPayoutDisplay(approving, locale)}`
            : ''
        }
        footer={
          <>
            <Button
              variant='outline'
              disabled={review.isPending}
              onClick={() => setApproving(null)}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='brand'
              disabled={review.isPending}
              onClick={() =>
                approving &&
                review.mutate({
                  row: approving,
                  approve: true,
                  reason: approveNote.trim(),
                })
              }
            >
              {t('Approve')}
            </Button>
          </>
        }
      >
        <div className='space-y-4'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Approve this withdrawal request? After approval, pay the user offline and mark it as paid.'
            )}
          </p>
          <div className='space-y-2'>
            <Label htmlFor='commission-approve-note'>
              {t('Note (optional)')}
            </Label>
            <Textarea
              id='commission-approve-note'
              rows={2}
              value={approveNote}
              onChange={(event) => setApproveNote(event.target.value)}
            />
          </div>
        </div>
      </Dialog>

      {/* Reject Dialog */}
      <Dialog
        open={rejecting !== null}
        onOpenChange={(open) => {
          if (!open) setRejecting(null)
        }}
        title={t('Reject the withdrawal')}
        description={
          rejecting
            ? `${formatUserIdentity(rejecting.user_id, rejecting.username)} · ${formatPayoutDisplay(rejecting, locale)}`
            : ''
        }
        footer={
          <>
            <Button
              variant='outline'
              disabled={review.isPending}
              onClick={() => setRejecting(null)}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='destructive'
              disabled={rejectReason.trim() === '' || review.isPending}
              onClick={() =>
                rejecting &&
                review.mutate({
                  row: rejecting,
                  approve: false,
                  reason: rejectReason.trim(),
                })
              }
            >
              {t('Reject')}
            </Button>
          </>
        }
      >
        <div className='space-y-2'>
          <Label htmlFor='commission-reject-reason'>
            {t('Reason (required)')}
          </Label>
          <Textarea
            id='commission-reject-reason'
            rows={3}
            value={rejectReason}
            onChange={(event) => setRejectReason(event.target.value)}
          />
        </div>
      </Dialog>

      {/* Mark Paid Dialog */}
      <Dialog
        open={paying !== null}
        onOpenChange={(open) => {
          if (!open) closePaying()
        }}
        title={t('Mark as paid')}
        description={
          paying
            ? `${formatUserIdentity(paying.user_id, paying.username)} · ${formatPayoutDisplay(paying, locale)}`
            : ''
        }
        footer={
          <>
            <Button
              variant='outline'
              disabled={pay.isPending}
              onClick={closePaying}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='brand'
              disabled={payDisabled}
              onClick={() => paying && pay.mutate(paying)}
            >
              {t('Confirm')}
            </Button>
          </>
        }
      >
        {paying && (
          <div className='space-y-4'>
            {paying.method === 'alipay' && (
              <div className='bg-muted/40 flex flex-wrap items-start justify-between gap-3 rounded-lg border p-3'>
                <div className='flex min-w-0 flex-1 flex-col gap-1 text-sm'>
                  <span className='text-muted-foreground text-xs'>
                    {t('Alipay payee details')}
                  </span>
                  {paying.account_name && (
                    <div className='font-medium'>{paying.account_name}</div>
                  )}
                  {paying.account_no && (
                    <div className='flex items-center gap-1'>
                      <span className='font-mono text-xs break-all'>
                        {paying.account_no}
                      </span>
                      <CopyButton
                        value={paying.account_no}
                        size='icon'
                        aria-label={t('Copy account')}
                      />
                    </div>
                  )}
                </div>
                {paying.qr_image_url !== '' && (
                  <button
                    type='button'
                    className='shrink-0 cursor-zoom-in rounded-md border bg-white p-1 transition hover:opacity-80'
                    aria-label={t('Preview payment QR code')}
                    onClick={() =>
                      setPreviewQr({
                        url: paying.qr_image_url,
                        name: paying.account_name,
                        accountNo: paying.account_no,
                      })
                    }
                  >
                    <img
                      src={paying.qr_image_url}
                      alt={t('Payment QR code')}
                      className='h-28 w-28 object-contain'
                    />
                  </button>
                )}
              </div>
            )}
            {(paying.method === 'usdt' || paying.method === 'alipay') && (
              <div className='space-y-2'>
                <Label htmlFor='commission-pay-tx'>
                  {paying.method === 'usdt'
                    ? t('Transaction hash')
                    : t('Transfer reference')}
                </Label>
                <Input
                  id='commission-pay-tx'
                  value={txId}
                  autoComplete='off'
                  onChange={(event) => setTxId(event.target.value)}
                  required={paying.method === 'usdt'}
                />
              </div>
            )}
            <div className='space-y-2'>
              <Label htmlFor='commission-pay-proof'>{t('Proof note')}</Label>
              <div className='flex flex-wrap items-center gap-2'>
                <Input
                  id='commission-pay-proof'
                  className='min-w-0 flex-1'
                  value={proof}
                  autoComplete='off'
                  placeholder={t('Enter transfer note or upload proof image')}
                  onChange={(event) => setProof(event.target.value)}
                />
                <input
                  ref={proofFileRef}
                  type='file'
                  accept='image/png,image/jpeg,image/webp'
                  className='hidden'
                  onChange={(event) => {
                    const file = event.target.files?.[0]
                    if (file && paying) {
                      uploadProof.mutate({ file, withdrawalId: paying.id })
                    }
                  }}
                />
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={uploadProof.isPending}
                  onClick={() => proofFileRef.current?.click()}
                >
                  <Upload className='size-3.5' aria-hidden='true' />
                  {t('Upload proof image')}
                </Button>
                {proof.trim().startsWith('/api/referral-assets/') && (
                  <Button
                    type='button'
                    size='sm'
                    variant='ghost'
                    onClick={() => {
                      setProof('')
                      if (proofFileRef.current) proofFileRef.current.value = ''
                    }}
                  >
                    <X className='size-3.5' aria-hidden='true' />
                    {t('Clear')}
                  </Button>
                )}
              </div>
              {proof.trim().startsWith('/api/referral-assets/') && (
                <a
                  href={proof.trim()}
                  target='_blank'
                  rel='noopener noreferrer'
                  className='block w-fit pt-1'
                >
                  <img
                    src={proof.trim()}
                    alt={t('Payment proof')}
                    className='h-20 w-20 rounded border object-contain'
                  />
                </a>
              )}
            </div>
          </div>
        )}
      </Dialog>

      {/* Confirm Failed Payout Dialog */}
      <Dialog
        open={failing !== null}
        onOpenChange={(open) => {
          if (!open) setFailing(null)
        }}
        title={t('Confirm failed payout')}
        description={
          failing
            ? `${formatUserIdentity(failing.user_id, failing.username)} · ${formatPayoutDisplay(failing, locale)}`
            : ''
        }
        footer={
          <>
            <Button
              variant='outline'
              disabled={review.isPending}
              onClick={() => setFailing(null)}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='destructive'
              disabled={failReason.trim() === '' || review.isPending}
              onClick={() =>
                failing &&
                review.mutate({
                  row: failing,
                  approve: false,
                  reason: failReason.trim(),
                })
              }
            >
              {t('Confirm failed payout')}
            </Button>
          </>
        }
      >
        <div className='space-y-4'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Record the failed payout reference so the withdrawal can be returned to the user.'
            )}
          </p>
          <div className='space-y-2'>
            <Label htmlFor='commission-fail-reason'>
              {t('Failed payout reference')}
            </Label>
            <Textarea
              id='commission-fail-reason'
              rows={3}
              value={failReason}
              onChange={(event) => setFailReason(event.target.value)}
            />
          </div>
        </div>
      </Dialog>

      {/* QR Lightbox Preview Dialog */}
      <Dialog
        open={previewQr !== null}
        onOpenChange={(open) => {
          if (!open) setPreviewQr(null)
        }}
        title={t('Payment QR code')}
        description={
          previewQr
            ? [previewQr.name, previewQr.accountNo].filter(Boolean).join(' · ')
            : ''
        }
        contentClassName='sm:max-w-md'
        footer={
          previewQr ? (
            <div className='flex w-full items-center justify-between gap-2'>
              <a
                href={previewQr.url}
                target='_blank'
                rel='noopener noreferrer'
                className='text-primary inline-flex items-center gap-1 text-sm underline'
              >
                <ExternalLink className='size-3.5' aria-hidden='true' />
                {t('Open in new tab')}
              </a>
              <Button variant='outline' onClick={() => setPreviewQr(null)}>
                {t('Close')}
              </Button>
            </div>
          ) : undefined
        }
      >
        {previewQr && (
          <div className='flex flex-col items-center gap-3'>
            <img
              src={previewQr.url}
              alt={t('Payment QR code')}
              className='max-h-[70vh] w-full max-w-sm rounded-lg border object-contain'
            />
          </div>
        )}
      </Dialog>
    </section>
  )
}
