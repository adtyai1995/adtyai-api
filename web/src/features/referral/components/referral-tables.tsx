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
import { ExternalLink } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  cancelReferralWithdrawal,
  getReferralCommissions,
  getReferralWithdrawals,
  REFERRAL_PAGE_SIZE,
  referralQueryKeys,
} from '../api'
import { formatUsdtAmount, usdtNetworkLabel } from '../lib/address'
import {
  COMMISSION_STATUS_VARIANTS,
  commissionSourceLabel,
  commissionStatusLabel,
  WITHDRAWAL_STATUS_LABELS,
  WITHDRAWAL_STATUS_VARIANTS,
  WITHDRAWAL_TARGET_LABELS,
} from '../lib/labels'
import type { ReferralWithdrawal } from '../types'
import { PageSwitcher } from './page-switcher'

export function CommissionTable() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: referralQueryKeys.commissions(page),
    queryFn: () => getReferralCommissions(page),
  })
  if (query.isError && !query.data) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }

  return (
    <div className='flex flex-col gap-3'>
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      <StaticDataTable
        tableProps={{ 'aria-label': t('Commission details') }}
        data={query.data?.items ?? []}
        getRowKey={(row) => String(row.id)}
        emptyContent={
          <EmptyState className='min-h-32' title={t('No commission yet')} />
        }
        columns={[
          {
            id: 'time',
            header: t('Time'),
            cell: (row) => formatTimestamp(row.created_at),
          },
          {
            id: 'order',
            header: t('Paid consumption'),
            className: 'text-right',
            cellClassName: 'text-right tabular-nums',
            cell: (row) => (
              <div className='flex flex-col items-end'>
                <span>{formatUsd(row.base_amount, locale)}</span>
                {row.source_type && (
                  <span className='text-muted-foreground text-xs'>
                    {commissionSourceLabel(t, row.source_type)}
                  </span>
                )}
              </div>
            ),
          },
          {
            id: 'rate',
            header: t('Rate'),
            className: 'text-right',
            cellClassName: 'text-right tabular-nums',
            cell: (row) => `${row.rate}%`,
          },
          {
            id: 'amount',
            header: t('Commission'),
            className: 'text-right',
            cellClassName: 'text-right tabular-nums',
            cell: (row) => formatUsd(row.commission_amount, locale),
          },
          {
            id: 'status',
            header: t('Status'),
            cell: (row) => (
              <StatusBadge
                label={commissionStatusLabel(t, row.status)}
                variant={COMMISSION_STATUS_VARIANTS[row.status] ?? 'neutral'}
                copyable={false}
              />
            ),
          },
          {
            id: 'settle',
            header: t('Settles at'),
            cell: (row) =>
              row.status === 'pending' && row.settle_at > 0
                ? formatTimestamp(row.settle_at)
                : '-',
          },
        ]}
      />
      <PageSwitcher
        page={page}
        total={query.data?.total ?? 0}
        pageSize={REFERRAL_PAGE_SIZE}
        onPageChange={setPage}
      />
    </div>
  )
}

/** Where the money goes: chain + address, payee name + account, or balance. */
function withdrawalTarget(row: ReferralWithdrawal): string {
  if (row.account_type === 'usdt') {
    return `${usdtNetworkLabel(row.account_network)} ${row.account_no}`
  }
  if (row.account_type === 'balance') return ''
  return [row.account_name, row.account_no].filter(Boolean).join(' · ')
}

type ReferralWithdrawalRow = ReferralWithdrawal & {
  can_cancel?: boolean
  cancel_deadline?: number
  cancel_reason?: string
}

function resolveCancelDeadline(
  row: ReferralWithdrawalRow,
  cancelWindowMinutes: number
): number {
  if (typeof row.cancel_deadline === 'number' && row.cancel_deadline > 0) {
    return row.cancel_deadline
  }
  if (row.status === 'pending' && cancelWindowMinutes > 0) {
    return row.submitted_at + cancelWindowMinutes * 60
  }
  return 0
}

function canCancelWithdrawalRow(
  row: ReferralWithdrawalRow,
  cancelWindowMinutes: number,
  nowSeconds: number
): boolean {
  if (row.status !== 'pending') return false
  if (typeof row.can_cancel === 'boolean') {
    if (!row.can_cancel) return false
    const deadline = row.cancel_deadline ?? 0
    return deadline <= 0 || nowSeconds < deadline
  }
  const deadline = resolveCancelDeadline(row, cancelWindowMinutes)
  return deadline > 0 && nowSeconds < deadline
}

export function WithdrawalTable(props: { cancelWindowMinutes: number }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [previewProofUrl, setPreviewProofUrl] = useState<string | null>(null)
  const [nowSeconds, setNowSeconds] = useState(() =>
    Math.floor(Date.now() / 1000)
  )
  const query = useQuery({
    queryKey: referralQueryKeys.withdrawals(page),
    queryFn: () => getReferralWithdrawals(page),
  })
  const rows: ReferralWithdrawalRow[] = query.data?.items ?? []
  const hasActiveCancelDeadline = rows.some((row) => {
    if (row.status !== 'pending' || row.can_cancel === false) return false
    const deadline = resolveCancelDeadline(row, props.cancelWindowMinutes)
    return deadline > nowSeconds
  })

  useEffect(() => {
    if (query.dataUpdatedAt > 0) {
      setNowSeconds(Math.floor(Date.now() / 1000))
    }
  }, [query.dataUpdatedAt])

  useEffect(() => {
    if (!hasActiveCancelDeadline) return
    const timer = window.setInterval(() => {
      setNowSeconds(Math.floor(Date.now() / 1000))
    }, 1000)
    return () => window.clearInterval(timer)
  }, [hasActiveCancelDeadline])

  const cancel = useMutation({
    mutationFn: cancelReferralWithdrawal,
    onSuccess: () => {
      toast.success(t('Withdrawal canceled'))
      void queryClient.invalidateQueries({ queryKey: referralQueryKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  if (query.isError && !query.data) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  return (
    <div className='flex flex-col gap-3'>
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      <StaticDataTable
        tableClassName='min-w-[860px]'
        tableProps={{ 'aria-label': t('Withdrawal records') }}
        data={rows}
        getRowKey={(row) => String(row.id)}
        emptyContent={
          <EmptyState className='min-h-32' title={t('No withdrawals yet')} />
        }
        columns={[
          {
            id: 'time',
            header: t('Time'),
            cell: (row) => formatTimestamp(row.submitted_at),
          },
          {
            id: 'target',
            header: t('Withdraw to'),
            cell: (row) => (
              <div className='flex flex-col'>
                <span>{t(WITHDRAWAL_TARGET_LABELS[row.account_type])}</span>
                <span className='text-muted-foreground font-mono text-xs break-all'>
                  {withdrawalTarget(row)}
                </span>
              </div>
            ),
          },
          {
            id: 'amount',
            header: t('Amount'),
            className: 'text-right',
            cellClassName: 'text-right tabular-nums',
            cell: (row) => {
              if (row.account_type === 'usdt') {
                return t('{{amount}} → {{net}} USDT', {
                  amount: formatUsd(row.amount, locale, 6),
                  net: formatUsdtAmount(row.net_amount),
                })
              }
              if (row.cny_amount) {
                return `${formatUsd(row.amount, locale)} → ¥${row.cny_amount}`
              }
              return formatUsd(row.amount, locale)
            },
          },
          {
            id: 'status',
            header: t('Status'),
            cell: (row) => (
              <div className='flex flex-col gap-1'>
                <StatusBadge
                  label={t(WITHDRAWAL_STATUS_LABELS[row.status])}
                  variant={WITHDRAWAL_STATUS_VARIANTS[row.status]}
                />
                {row.reject_reason && (
                  <span className='text-muted-foreground text-xs'>
                    {row.reject_reason}
                  </span>
                )}
              </div>
            ),
          },
          {
            id: 'txn',
            header: t('Transaction hash'),
            cell: (row) => {
              const hasTxn = Boolean(row.payment_txn_no)
              const hasProof = Boolean(row.payment_proof_url)
              if (!hasTxn && !hasProof) return '-'
              return (
                <div className='flex flex-col items-start gap-1.5'>
                  {hasTxn && (
                    <span className='font-mono text-xs break-all'>
                      {row.payment_txn_no}
                    </span>
                  )}
                  {hasProof && (
                    <button
                      type='button'
                      className='inline-flex cursor-zoom-in items-center gap-1.5 rounded-md border bg-white p-1 text-xs transition hover:opacity-80'
                      aria-label={t('Preview payment proof')}
                      onClick={() => setPreviewProofUrl(row.payment_proof_url)}
                    >
                      <img
                        src={row.payment_proof_url}
                        alt={t('Payment proof')}
                        className='h-10 w-10 rounded object-contain'
                      />
                      <span className='text-foreground pr-1'>
                        {t('Payment proof')}
                      </span>
                    </button>
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
            cell: (row) =>
              canCancelWithdrawalRow(
                row,
                props.cancelWindowMinutes,
                nowSeconds
              ) ? (
                <Button
                  size='sm'
                  variant='outline'
                  disabled={cancel.isPending}
                  onClick={() => cancel.mutate(row.id)}
                >
                  {t('Cancel')}
                </Button>
              ) : null,
          },
        ]}
      />
      <PageSwitcher
        page={page}
        total={query.data?.total ?? 0}
        pageSize={REFERRAL_PAGE_SIZE}
        onPageChange={setPage}
      />
      <Dialog
        open={previewProofUrl !== null}
        onOpenChange={(open) => {
          if (!open) setPreviewProofUrl(null)
        }}
        title={t('Payment proof')}
        contentClassName='sm:max-w-md'
        footer={
          previewProofUrl ? (
            <div className='flex w-full items-center justify-between gap-2'>
              <a
                href={previewProofUrl}
                target='_blank'
                rel='noopener noreferrer'
                className='text-primary inline-flex items-center gap-1 text-sm underline'
              >
                <ExternalLink className='size-3.5' aria-hidden='true' />
                {t('Open in new tab')}
              </a>
              <Button
                variant='outline'
                onClick={() => setPreviewProofUrl(null)}
              >
                {t('Close')}
              </Button>
            </div>
          ) : undefined
        }
      >
        {previewProofUrl && (
          <div className='flex flex-col items-center gap-3'>
            <img
              src={previewProofUrl}
              alt={t('Payment proof')}
              className='max-h-[70vh] w-full max-w-sm rounded-lg border object-contain'
            />
          </div>
        )}
      </Dialog>
    </div>
  )
}
