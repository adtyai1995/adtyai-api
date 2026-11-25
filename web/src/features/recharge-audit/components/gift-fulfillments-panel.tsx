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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getAdminGiftFulfillments,
  reviewAdminGiftFulfillment,
  type GiftFulfillment,
} from '../api'

const PAGE_SIZE = 20

function giftFulfillmentStatusLabel(
  status: string,
  t: (key: string) => string
): string {
  switch (status) {
    case 'pending_review':
      return t('Pending review')
    case 'pending':
      return t('Pending fulfillment')
    case 'fulfilled':
      return t('Fulfilled')
    case 'canceled':
      return t('Canceled')
    default:
      return status
  }
}

function giftFulfillmentStatusVariant(
  status: string
): 'warning' | 'info' | 'success' | 'neutral' {
  switch (status) {
    case 'pending_review':
      return 'warning'
    case 'pending':
      return 'info'
    case 'fulfilled':
      return 'success'
    default:
      return 'neutral'
  }
}

/**
 * Root review panel for site-funded top-up gift fulfillments (R2-039 / A.8).
 * Delayed payments or manual completions after expiry/cancel enter
 * `pending_review` and must be approved or rejected by Root.
 */
export function GiftFulfillmentsPanel() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const [statusFilter, setStatusFilter] = useState('pending_review')
  const [page, setPage] = useState(1)
  const [reviewing, setReviewing] = useState<GiftFulfillment | null>(null)
  const query = useQuery({
    queryKey: ['recharge-audit', 'gift-fulfillments', statusFilter, page],
    queryFn: () => getAdminGiftFulfillments(statusFilter, page, PAGE_SIZE),
  })
  const rows = Array.isArray(query.data?.items) ? query.data.items : []

  return (
    <>
      <ContentSection
        title={t('Site gift fulfillment review')}
        description={t(
          'Site-funded top-up bonuses held for Root review after late payment or manual completion.'
        )}
        actions={
          <NativeSelect
            aria-label={t('Status')}
            value={statusFilter}
            onChange={(event) => {
              setStatusFilter(event.target.value)
              setPage(1)
            }}
          >
            <NativeSelectOption value='pending_review'>
              {t('Pending review')}
            </NativeSelectOption>
            <NativeSelectOption value='pending'>
              {t('Pending fulfillment')}
            </NativeSelectOption>
            <NativeSelectOption value='fulfilled'>
              {t('Fulfilled')}
            </NativeSelectOption>
            <NativeSelectOption value='canceled'>
              {t('Canceled')}
            </NativeSelectOption>
            <NativeSelectOption value='all'>
              {t('All statuses')}
            </NativeSelectOption>
          </NativeSelect>
        }
      >
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {query.isPending ? (
          <LoadingState />
        ) : (
          query.data && (
            <>
              <StaticDataTable
                  tableClassName='min-w-[820px]'
                  tableProps={{
                    'aria-label': t('Site gift fulfillment review'),
                  }}
                  data={rows}
                  getRowKey={(row) => row.id}
                  emptyContent={
                    <EmptyState
                      className='min-h-24'
                      title={t('Nothing to review')}
                    />
                  }
                  columns={[
                    {
                      id: 'time',
                      header: t('Time'),
                      cell: (row) => formatTimestamp(row.created_at),
                    },
                    {
                      id: 'trade_no',
                      header: t('Order number'),
                      cell: (row) => (
                        <code className='font-mono text-xs'>
                          {row.trade_no}
                        </code>
                      ),
                    },
                    {
                      id: 'site',
                      header: t('Site ID'),
                      cell: (row) => row.site_id,
                    },
                    {
                      id: 'customer',
                      header: t('User'),
                      cellClassName: 'tabular-nums',
                      cell: (row) =>
                        formatUserIdentity(row.user_id, row.username),
                    },
                    {
                      id: 'bonus',
                      header: t('Bonus'),
                      cellClassName: 'tabular-nums',
                      cell: (row) => formatUsd(Number(row.bonus_usd), locale),
                    },
                    {
                      id: 'status',
                      header: t('Status'),
                      cell: (row) => (
                        <StatusBadge
                          label={giftFulfillmentStatusLabel(row.status, t)}
                          variant={giftFulfillmentStatusVariant(row.status)}
                          copyable={false}
                        />
                      ),
                    },
                    {
                      id: 'reason',
                      header: t('Reason'),
                      cell: (row) => (
                        <span className='text-muted-foreground text-xs break-words'>
                          {row.reason || '—'}
                        </span>
                      ),
                    },
                    {
                      id: 'actions',
                      header: t('Actions'),
                      className: 'text-right',
                      cellClassName: 'text-right',
                      cell: (row) =>
                        row.status === 'pending_review' ? (
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() => setReviewing(row)}
                          >
                            {t('Review')}
                          </Button>
                        ) : null,
                    },
                  ]}
                />
                <PageSwitcher
                  page={page}
                  total={Number(query.data.total) || 0}
                  pageSize={PAGE_SIZE}
                  onPageChange={setPage}
                />
              </>
            )
          )}
      </ContentSection>
      {reviewing && (
        <GiftFulfillmentReviewDialog
          key={reviewing.id}
          record={reviewing}
          onClose={() => setReviewing(null)}
          onDone={() =>
            void queryClient.invalidateQueries({ queryKey: ['recharge-audit'] })
          }
        />
      )}
    </>
  )
}

function GiftFulfillmentReviewDialog(props: {
  record: GiftFulfillment
  onClose: () => void
  onDone: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const id = useId()
  const [action, setAction] = useState<'approve' | 'reject' | ''>('')
  const [note, setNote] = useState('')
  const mutation = useMutation({
    mutationFn: async () => {
      if (!action) return null
      return reviewAdminGiftFulfillment(props.record.id, {
        action,
        note: note.trim(),
      })
    },
    onSuccess: (res) => {
      if (!res) return
      if (action === 'approve' && res.fulfilled) {
        toast.success(t('Gift bonus approved and fulfilled'))
      } else if (action === 'approve') {
        toast.success(t('Gift bonus approved and queued for fulfillment'))
      } else {
        toast.success(t('Gift bonus rejected'))
      }
      props.onDone()
      props.onClose()
    },
    onError: (err) => handleServerError(err),
  })

  const needNote = action === 'reject' && note.trim() === ''

  return (
    <Dialog
      open
      onOpenChange={(open) => !open && !mutation.isPending && props.onClose()}
      title={t('Review gift fulfillment')}
      description={t('Order {{tradeNo}} · Site #{{siteId}} · Bonus {{bonus}}', {
        tradeNo: props.record.trade_no,
        siteId: props.record.site_id,
        bonus: formatUsd(Number(props.record.bonus_usd), locale),
      })}
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            disabled={mutation.isPending}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            variant={action === 'reject' ? 'destructive' : 'brand'}
            disabled={mutation.isPending || !action || needNote}
            onClick={() => mutation.mutate()}
          >
            {t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='flex flex-col gap-3'>
        <div className='flex flex-col gap-1'>
          <Label htmlFor={`${id}-action`}>{t('Decision')}</Label>
          <NativeSelect
            id={`${id}-action`}
            value={action}
            onChange={(event) =>
              setAction(event.target.value as 'approve' | 'reject' | '')
            }
          >
            <NativeSelectOption value=''>
              {t('Select decision')}
            </NativeSelectOption>
            <NativeSelectOption value='approve'>
              {t('Approve and fulfill')}
            </NativeSelectOption>
            <NativeSelectOption value='reject'>
              {t('Reject and cancel bonus')}
            </NativeSelectOption>
          </NativeSelect>
        </div>
        <div className='flex flex-col gap-1'>
          <Label htmlFor={`${id}-note`}>{t('Review note')}</Label>
          <Textarea
            id={`${id}-note`}
            value={note}
            onChange={(event) => setNote(event.target.value)}
            placeholder={
              action === 'reject'
                ? t('Required when rejecting a gift fulfillment')
                : t('Optional review note')
            }
          />
        </div>
      </div>
    </Dialog>
  )
}
