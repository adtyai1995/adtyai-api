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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import {
  COMMISSION_STATUS_LABELS,
  COMMISSION_STATUS_VARIANTS,
} from '@/features/referral/lib/labels'
import type { ReferralCommissionStatus } from '@/features/referral/types'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'

import {
  ADMIN_REFERRAL_PAGE_SIZE,
  adminReferralKeys,
  fetchAdminReferralCommissions,
} from '../api'

const COMMISSION_STATUSES: ReferralCommissionStatus[] = [
  'pending',
  'available',
  'frozen',
  'paid',
  'void',
  'clawed_back',
]

export type CommissionAffiliateFilter = {
  affiliateId?: number
  userId?: number
  username?: string
}

export function CommissionsPanel(props: {
  affiliateFilter: CommissionAffiliateFilter | null
  onClearAffiliateFilter: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')

  const affiliateId = props.affiliateFilter?.affiliateId
  const affiliateUserId = props.affiliateFilter?.userId

  useEffect(() => {
    setPage(1)
  }, [affiliateId, affiliateUserId])

  const params = {
    p: page,
    page_size: ADMIN_REFERRAL_PAGE_SIZE,
    status: status || undefined,
    affiliate_id: affiliateId,
    affiliate_user_id: affiliateUserId,
    q: search || undefined,
  }

  const query = useQuery({
    queryKey: adminReferralKeys.list('commissions', params),
    queryFn: () => fetchAdminReferralCommissions(params),
    placeholderData: keepPreviousData,
  })

  const rows = Array.isArray(query.data?.items) ? query.data.items : []

  return (
    <Card>
      <CardContent className='flex flex-col gap-3 py-4'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <form
            className='flex flex-wrap items-center gap-2'
            onSubmit={(event) => {
              event.preventDefault()
              setSearch(keyword.trim())
              setPage(1)
            }}
          >
            <Input
              className='w-72'
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
              placeholder={t('Search by trade no, affiliate or invitee')}
              aria-label={t('Search')}
            />
            <Button variant='brand' type='submit'>
              {t('Search')}
            </Button>
            {search !== '' && (
              <Button
                type='button'
                variant='outline'
                onClick={() => {
                  setKeyword('')
                  setSearch('')
                  setPage(1)
                }}
              >
                {t('Reset')}
              </Button>
            )}
          </form>
          <NativeSelect
            className='w-44'
            aria-label={t('Status')}
            value={status}
            onChange={(event) => {
              setStatus(event.target.value)
              setPage(1)
            }}
          >
            <NativeSelectOption value=''>{t('All statuses')}</NativeSelectOption>
            {COMMISSION_STATUSES.map((value) => (
              <NativeSelectOption key={value} value={value}>
                {t(COMMISSION_STATUS_LABELS[value])}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>

        {props.affiliateFilter && (
          <div className='flex flex-wrap items-center gap-2'>
            <Badge variant='secondary' className='gap-1.5 py-1 pr-1.5 pl-2.5'>
              <span className='tabular-nums'>
                {t('Affiliate')}:{' '}
                {formatUserIdentity(
                  props.affiliateFilter.userId,
                  props.affiliateFilter.username
                )}
              </span>
              <button
                type='button'
                className='hover:bg-muted-foreground/20 inline-flex size-4 items-center justify-center rounded-sm transition-colors'
                aria-label={t('Clear filter')}
                onClick={() => {
                  props.onClearAffiliateFilter()
                  setPage(1)
                }}
              >
                <X className='size-3' aria-hidden='true' />
              </button>
            </Badge>
          </div>
        )}

        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {query.isPending && !query.data ? (
          <LoadingState />
        ) : (
          <>
            <StaticDataTable
              tableClassName='min-w-[1040px]'
              tableProps={{ 'aria-label': t('Commission details') }}
              data={rows}
              getRowKey={(row) => String(row.id)}
              emptyContent={
                <EmptyState
                  className='min-h-32'
                  title={t('No commission yet')}
                />
              }
              columns={[
                {
                  id: 'time',
                  header: t('Time'),
                  cellClassName: 'tabular-nums whitespace-nowrap',
                  cell: (row) =>
                    row.created_at > 0 ? formatTimestamp(row.created_at) : '-',
                },
                {
                  id: 'affiliate',
                  header: t('Affiliate'),
                  cellClassName: 'tabular-nums',
                  cell: (row) =>
                    formatUserIdentity(
                      row.affiliate_user_id,
                      row.affiliate_username
                    ),
                },
                {
                  id: 'invitee',
                  header: t('Invitee'),
                  cellClassName: 'tabular-nums',
                  cell: (row) =>
                    formatUserIdentity(
                      row.invitee_user_id,
                      row.invitee_username
                    ),
                },
                {
                  id: 'order',
                  header: t('Order number'),
                  cell: (row) => (
                    <code className='font-mono text-xs break-all'>
                      {row.source_trade_no || '-'}
                    </code>
                  ),
                },
                {
                  id: 'base_amount',
                  header: t('Base amount'),
                  className: 'text-right',
                  cellClassName: 'text-right tabular-nums',
                  cell: (row) => formatUsd(Number(row.base_amount ?? 0), locale),
                },
                {
                  id: 'rate',
                  header: t('Rate'),
                  className: 'text-right',
                  cellClassName: 'text-right tabular-nums',
                  cell: (row) => `${row.rate}%`,
                },
                {
                  id: 'commission_amount',
                  header: t('Commission'),
                  className: 'text-right',
                  cellClassName: 'text-right font-medium tabular-nums',
                  cell: (row) =>
                    formatUsd(Number(row.commission_amount ?? 0), locale),
                },
                {
                  id: 'status',
                  header: t('Status'),
                  cell: (row) => (
                    <StatusBadge
                      label={t(
                        COMMISSION_STATUS_LABELS[row.status] ?? row.status
                      )}
                      variant={
                        COMMISSION_STATUS_VARIANTS[row.status] ?? 'neutral'
                      }
                      copyable={false}
                    />
                  ),
                },
                {
                  id: 'settle_at',
                  header: t('Settles at'),
                  cellClassName: 'tabular-nums whitespace-nowrap',
                  cell: (row) =>
                    row.settle_at > 0 ? formatTimestamp(row.settle_at) : '-',
                },
              ]}
            />
            <PageSwitcher
              page={page}
              total={Number(query.data?.total) || 0}
              pageSize={ADMIN_REFERRAL_PAGE_SIZE}
              onPageChange={setPage}
            />
          </>
        )}
      </CardContent>
    </Card>
  )
}
