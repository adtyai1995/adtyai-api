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
import { ExternalLink, X } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { uploadReferralAsset } from '@/features/referral/api'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { usdtNetworkLabel } from '@/features/referral/lib/address'
import {
  WITHDRAWAL_STATUS_LABELS,
  WITHDRAWAL_STATUS_VARIANTS,
  WITHDRAWAL_TARGET_LABELS,
} from '@/features/referral/lib/labels'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'

import {
  ADMIN_REFERRAL_PAGE_SIZE,
  adminReferralKeys,
  getAdminWithdrawals,
  payWithdrawal,
  reviewWithdrawal,
  type AdminReferralWithdrawal,
} from '../api'
import { payoutText } from '../lib/payout'

type ImagePreviewInfo = {
  url: string
  title: string
  subtitle?: string
}

function PayeeCell(props: {
  row: AdminReferralWithdrawal
  onPreviewImage: (info: ImagePreviewInfo) => void
}) {
  const { t } = useTranslation()
  const row = props.row
  if (row.account_type === 'usdt') {
    return (
      <div className='flex flex-col gap-1'>
        <span className='font-medium'>
          {usdtNetworkLabel(row.account_network)}
        </span>
        <div className='flex items-center gap-1'>
          <span className='font-mono text-xs break-all'>{row.account_no}</span>
          <CopyButton
            value={row.account_no}
            size='icon'
            aria-label={t('Copy address')}
          />
        </div>
      </div>
    )
  }
  if (row.account_type === 'balance') {
    return <span>{t('Account balance')}</span>
  }
  return (
    <div className='flex items-center gap-2'>
      {row.qr_image_url && (
        <button
          type='button'
          className='shrink-0 cursor-zoom-in rounded-md border bg-white p-0.5 transition hover:opacity-80'
          aria-label={t('Preview payment QR code')}
          onClick={() =>
            props.onPreviewImage({
              url: row.qr_image_url,
              title: t('Payment QR code'),
              subtitle: [row.account_name, row.account_no]
                .filter(Boolean)
                .join(' · '),
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
        <span>{t(WITHDRAWAL_TARGET_LABELS[row.account_type])}</span>
        {row.account_name && (
          <span className='font-medium'>{row.account_name}</span>
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

export function WithdrawalPanel(props: {
  canReview: boolean
  canPayout: boolean
  status?: string
  onStatusChange?: (status: string) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const [ownStatus, setOwnStatus] = useState('pending')
  const status = props.status ?? ownStatus
  const [page, setPage] = useState(1)
  const [rejecting, setRejecting] = useState<AdminReferralWithdrawal | null>(
    null
  )
  const [paying, setPaying] = useState<AdminReferralWithdrawal | null>(null)
  const [previewImage, setPreviewImage] = useState<ImagePreviewInfo | null>(
    null
  )
  const [reason, setReason] = useState('')
  const [txnNo, setTxnNo] = useState('')
  const [proofUrl, setProofUrl] = useState('')
  const proofFileRef = useRef<HTMLInputElement | null>(null)
  const activePayingIdRef = useRef<number | null>(null)
  const resetPayForm = () => {
    setTxnNo('')
    setProofUrl('')
    if (proofFileRef.current) proofFileRef.current.value = ''
  }
  const openPaying = (row: AdminReferralWithdrawal) => {
    activePayingIdRef.current = row.id
    resetPayForm()
    setPaying(row)
  }
  const closePaying = () => {
    activePayingIdRef.current = null
    setPaying(null)
    resetPayForm()
  }
  const openRejecting = (row: AdminReferralWithdrawal) => {
    setReason('')
    setRejecting(row)
  }
  const closeRejecting = () => {
    setRejecting(null)
    setReason('')
  }
  const params = { p: page, status }
  const query = useQuery({
    queryKey: adminReferralKeys.list('withdrawals', params),
    queryFn: () => getAdminWithdrawals(params),
  })
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: adminReferralKeys.all })
    void invalidateSidebarBadges(queryClient)
  }
  const review = useMutation({
    mutationFn: (input: { row: AdminReferralWithdrawal; approve: boolean }) =>
      reviewWithdrawal(input.row.id, { approve: input.approve, reason }),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      closeRejecting()
      void refresh()
    },
    onError: (err) => handleServerError(err),
  })
  const pay = useMutation({
    mutationFn: (row: AdminReferralWithdrawal) =>
      payWithdrawal(row.id, {
        txn_no: txnNo.trim(),
        proof_url: proofUrl,
        note: '',
      }),
    onSuccess: () => {
      toast.success(t('Marked as paid'))
      closePaying()
      void refresh()
    },
    onError: (err) => handleServerError(err),
  })
  const upload = useMutation({
    mutationFn: (input: { file: File; withdrawalId: number }) =>
      uploadReferralAsset(input.file, '/api/user/admin/referral/upload'),
    onSuccess: (url, input) => {
      if (activePayingIdRef.current === input.withdrawalId) {
        setProofUrl(url)
        toast.success(t('Payment proof uploaded'))
      }
    },
    onError: (err) => handleServerError(err),
  })

  let payMissingEvidence = false
  if (paying?.account_type === 'usdt') {
    payMissingEvidence = txnNo.trim() === ''
  } else if (paying?.account_type === 'alipay') {
    payMissingEvidence = txnNo.trim() === '' && proofUrl.trim() === ''
  }

  if (query.isError && !query.data) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }

  return (
    <ContentSection
      title={t('Withdrawals')}
      actions={
        <NativeSelect
          className='w-48'
          aria-label={t('Status')}
          value={status}
          onChange={(event) => {
            const changeStatus = props.onStatusChange ?? setOwnStatus
            changeStatus(event.target.value)
            setPage(1)
          }}
        >
          {props.canReview && props.canPayout && (
            <option value='actionable'>
              {t('Awaiting review or payment')}
            </option>
          )}
          <option value=''>{t('All statuses')}</option>
          {Object.entries(WITHDRAWAL_STATUS_LABELS).map(([value, label]) => (
            <option key={value} value={value}>
              {t(label)}
            </option>
          ))}
        </NativeSelect>
      }
    >
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      <StaticDataTable
        tableClassName='min-w-[1000px]'
        tableProps={{ 'aria-label': t('Withdrawals') }}
        data={query.data?.items ?? []}
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
            id: 'user',
            header: t('User'),
            cellClassName: 'tabular-nums',
            cell: (row) => formatUserIdentity(row.user_id, row.username),
          },
          {
            id: 'payee',
            header: t('Pay to'),
            cell: (row) => (
              <PayeeCell row={row} onPreviewImage={setPreviewImage} />
            ),
          },
          {
            id: 'payout',
            header: t('Amount to pay'),
            cell: (row) => {
              const usdText = formatUsd(
                row.amount,
                locale,
                row.account_type === 'usdt' ? 6 : 2
              )
              return (
                <div className='flex flex-col'>
                  <span className='font-semibold' data-testid='payout'>
                    {payoutText(row, usdText)}
                  </span>
                  <span className='text-muted-foreground text-xs'>
                    {usdText}
                  </span>
                </div>
              )
            },
          },
          {
            id: 'status',
            header: t('Status'),
            cell: (row) => (
              <div className='flex flex-col items-start gap-1'>
                <StatusBadge
                  label={t(WITHDRAWAL_STATUS_LABELS[row.status])}
                  variant={WITHDRAWAL_STATUS_VARIANTS[row.status]}
                />
                {row.payment_txn_no && (
                  <span className='font-mono text-xs break-all'>
                    {row.payment_txn_no}
                  </span>
                )}
                {row.payment_proof_url && (
                  <button
                    type='button'
                    className='inline-flex cursor-zoom-in items-center gap-1.5 rounded-md border bg-white p-1 text-xs transition hover:opacity-80'
                    aria-label={t('Preview payment proof')}
                    onClick={() =>
                      setPreviewImage({
                        url: row.payment_proof_url,
                        title: t('Payment proof'),
                        subtitle: formatUserIdentity(row.user_id, row.username),
                      })
                    }
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
                {row.status === 'paid' &&
                  (Boolean(row.paid_by) || Boolean(row.paid_by_username)) && (
                    <span className='text-muted-foreground text-[11px] tabular-nums'>
                      {t('Payment recorded by')}{' '}
                      {formatUserIdentity(
                        row.paid_by ?? 0,
                        row.paid_by_username
                      )}
                      {row.paid_at > 0
                        ? ` · ${formatTimestamp(row.paid_at)}`
                        : ''}
                    </span>
                  )}
              </div>
            ),
          },
          {
            id: 'actions',
            header: t('Actions'),
            className: 'text-right',
            cellClassName: 'text-right',
            cell: (row) => (
              <div className='flex justify-end gap-1'>
                {props.canReview && row.status === 'pending' && (
                  <>
                    <Button
                      size='sm'
                      disabled={review.isPending}
                      onClick={() => review.mutate({ row, approve: true })}
                    >
                      {t('Approve')}
                    </Button>
                    <Button
                      size='sm'
                      variant='outline'
                      onClick={() => openRejecting(row)}
                    >
                      {t('Reject')}
                    </Button>
                  </>
                )}
                {row.status === 'approved' && (
                  <>
                    {props.canPayout && (
                      <Button size='sm' onClick={() => openPaying(row)}>
                        {t('Mark as paid')}
                      </Button>
                    )}
                    {props.canReview && (
                      <Button
                        size='sm'
                        variant='outline'
                        onClick={() => openRejecting(row)}
                      >
                        {t('Reject')}
                      </Button>
                    )}
                  </>
                )}
              </div>
            ),
          },
        ]}
      />
      <PageSwitcher
        page={page}
        total={query.data?.total ?? 0}
        pageSize={ADMIN_REFERRAL_PAGE_SIZE}
        onPageChange={setPage}
      />

      <Dialog
        open={rejecting !== null}
        onOpenChange={(open) => {
          if (!open) closeRejecting()
        }}
        title={t('Reject the withdrawal')}
        footer={
          <Button
            variant='destructive'
            disabled={reason.trim() === '' || review.isPending}
            onClick={() =>
              rejecting && review.mutate({ row: rejecting, approve: false })
            }
          >
            {t('Reject')}
          </Button>
        }
      >
        <div className='space-y-2'>
          <Label htmlFor='reject-reason'>{t('Reason')}</Label>
          <Textarea
            id='reject-reason'
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </div>
      </Dialog>

      <Dialog
        open={paying !== null}
        onOpenChange={(open) => {
          if (!open) closePaying()
        }}
        title={t('Mark as paid')}
        description={
          paying
            ? payoutText(
                paying,
                formatUsd(
                  paying.amount,
                  locale,
                  paying.account_type === 'usdt' ? 6 : 2
                )
              )
            : ''
        }
        footer={
          <Button
            variant='brand'
            disabled={
              !paying ||
              pay.isPending ||
              upload.isPending ||
              payMissingEvidence
            }
            onClick={() => paying && pay.mutate(paying)}
          >
            {t('Confirm')}
          </Button>
        }
      >
        <div className='space-y-4'>
          {paying?.account_type === 'alipay' && (
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
              {paying.qr_image_url && (
                <button
                  type='button'
                  className='shrink-0 cursor-zoom-in rounded-md border bg-white p-1 transition hover:opacity-80'
                  aria-label={t('Preview payment QR code')}
                  onClick={() =>
                    setPreviewImage({
                      url: paying.qr_image_url,
                      title: t('Payment QR code'),
                      subtitle: [paying.account_name, paying.account_no]
                        .filter(Boolean)
                        .join(' · '),
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
          {(paying?.account_type === 'usdt' ||
            paying?.account_type === 'alipay') && (
            <div className='space-y-2'>
              <Label htmlFor='pay-txn'>
                {paying.account_type === 'usdt'
                  ? t('Transaction hash')
                  : t('Transfer reference')}
              </Label>
              <Input
                id='pay-txn'
                autoComplete='off'
                value={txnNo}
                onChange={(event) => setTxnNo(event.target.value)}
              />
            </div>
          )}
          <div className='space-y-2'>
            <Label htmlFor='pay-proof'>{t('Payment proof')}</Label>
            <Input
              ref={proofFileRef}
              id='pay-proof'
              type='file'
              accept='image/png,image/jpeg,image/webp'
              disabled={upload.isPending}
              onChange={(event) => {
                const file = event.target.files?.[0]
                if (file && paying) {
                  upload.mutate({ file, withdrawalId: paying.id })
                }
              }}
            />
            {proofUrl && (
              <div className='flex items-start gap-2 pt-1'>
                <button
                  type='button'
                  className='cursor-zoom-in rounded-md border bg-white p-1 transition hover:opacity-80'
                  aria-label={t('Preview payment proof')}
                  onClick={() =>
                    setPreviewImage({
                      url: proofUrl,
                      title: t('Payment proof'),
                    })
                  }
                >
                  <img
                    src={proofUrl}
                    alt={t('Payment proof')}
                    className='h-20 w-20 rounded object-contain'
                  />
                </button>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  onClick={() => {
                    setProofUrl('')
                    if (proofFileRef.current) proofFileRef.current.value = ''
                  }}
                >
                  <X className='size-3.5' aria-hidden='true' />
                  {t('Clear')}
                </Button>
              </div>
            )}
            {paying?.account_type === 'alipay' && (
              <p className='text-muted-foreground text-xs'>
                {t('Enter a transfer reference or upload a payment proof image.')}
              </p>
            )}
          </div>
        </div>
      </Dialog>

      <Dialog
        open={previewImage !== null}
        onOpenChange={(open) => {
          if (!open) setPreviewImage(null)
        }}
        title={previewImage?.title ?? t('Payment proof')}
        description={previewImage?.subtitle ?? ''}
        contentClassName='sm:max-w-md'
        footer={
          previewImage ? (
            <div className='flex w-full items-center justify-between gap-2'>
              <a
                href={previewImage.url}
                target='_blank'
                rel='noopener noreferrer'
                className='text-primary inline-flex items-center gap-1 text-sm underline'
              >
                <ExternalLink className='size-3.5' aria-hidden='true' />
                {t('Open in new tab')}
              </a>
              <Button variant='outline' onClick={() => setPreviewImage(null)}>
                {t('Close')}
              </Button>
            </div>
          ) : undefined
        }
      >
        {previewImage && (
          <div className='flex flex-col items-center gap-3'>
            <img
              src={previewImage.url}
              alt={previewImage.title}
              className='max-h-[70vh] w-full max-w-sm rounded-lg border object-contain'
            />
          </div>
        )}
      </Dialog>
    </ContentSection>
  )
}
