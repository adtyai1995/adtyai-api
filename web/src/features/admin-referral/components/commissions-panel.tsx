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
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import {
  COMMISSION_STATUS_VARIANTS,
  commissionSourceLabel,
  commissionStatusLabel,
} from '@/features/referral/lib/labels'
import type { ReferralCommissionStatus } from '@/features/referral/types'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'

import {
  ADMIN_REFERRAL_PAGE_SIZE,
  adminReferralKeys,
  fetchAdminReferralCommissions,
  releaseAdminReferralCommission,
  voidAdminReferralCommission,
  type AdminReferralCommission,
} from '../api'

const COMMISSION_STATUSES: ReferralCommissionStatus[] = [
  'pending',
  'risk_held',
  'available',
  'frozen',
  'paid',
  'void',
  'clawed_back',
]

type RiskActionKind = 'release' | 'void'

export type CommissionAffiliateFilter = {
  affiliateId?: number
  userId?: number
  username?: string
}

export function CommissionsPanel(props: {
  canAdjust?: boolean
  affiliateFilter: CommissionAffiliateFilter | null
  onClearAffiliateFilter: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n?.resolvedLanguage || i18n?.language)
  const queryClient = useQueryClient()
  const canAdjust = Boolean(props.canAdjust)
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const [pendingAction, setPendingAction] = useState<{
    row: AdminReferralCommission
    action: RiskActionKind
  } | null>(null)
  const [actionReason, setActionReason] = useState('')

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

  const riskActionMutation = useMutation({
    mutationFn: (input: {
      id: number
      action: RiskActionKind
      reason: string
    }) =>
      input.action === 'release'
        ? releaseAdminReferralCommission(input.id, input.reason)
        : voidAdminReferralCommission(input.id, input.reason),
    onSuccess: (_data, variables) => {
      toast.success(
        variables.action === 'release'
          ? t('Risk-held commission released')
          : t('Risk-held commission voided')
      )
      setPendingAction(null)
      setActionReason('')
      void queryClient.invalidateQueries({ queryKey: adminReferralKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (err) => handleServerError(err),
  })

  const openRiskAction = (
    row: AdminReferralCommission,
    action: RiskActionKind
  ) => {
    setPendingAction({ row, action })
    setActionReason('')
  }

  const closeRiskAction = () => {
    if (riskActionMutation.isPending) return
    setPendingAction(null)
    setActionReason('')
  }

  const rows = Array.isArray(query.data?.items) ? query.data.items : []

  const columns: StaticDataTableColumn<AdminReferralCommission>[] = [
    {
      id: 'time',
      header: t('Time'),
      cellClassName: 'tabular-nums whitespace-nowrap',
      cell: (row: AdminReferralCommission) =>
        row.created_at > 0 ? formatTimestamp(row.created_at) : '-',
    },
    {
      id: 'affiliate',
      header: t('Affiliate'),
      cellClassName: 'tabular-nums',
      cell: (row: AdminReferralCommission) =>
        formatUserIdentity(row.affiliate_user_id, row.affiliate_username),
    },
    {
      id: 'invitee',
      header: t('Invitee'),
      cellClassName: 'tabular-nums',
      cell: (row: AdminReferralCommission) =>
        formatUserIdentity(row.invitee_user_id, row.invitee_username),
    },
    {
      id: 'order',
      header: t('Order number'),
      cell: (row: AdminReferralCommission) => (
        <div className='flex flex-col gap-0.5'>
          <code className='font-mono text-xs break-all'>
            {row.source_trade_no || '-'}
          </code>
          {row.source_type && (
            <span className='text-muted-foreground text-xs'>
              {commissionSourceLabel(t, row.source_type)}
            </span>
          )}
        </div>
      ),
    },
    {
      id: 'base_amount',
      header: t('Base amount'),
      className: 'text-right',
      cellClassName: 'text-right tabular-nums',
      cell: (row: AdminReferralCommission) =>
        formatUsd(Number(row.base_amount ?? 0), locale),
    },
    {
      id: 'rate',
      header: t('Rate'),
      className: 'text-right',
      cellClassName: 'text-right tabular-nums',
      cell: (row: AdminReferralCommission) => `${row.rate}%`,
    },
    {
      id: 'commission_amount',
      header: t('Commission'),
      className: 'text-right',
      cellClassName: 'text-right font-medium tabular-nums',
      cell: (row: AdminReferralCommission) =>
        formatUsd(Number(row.commission_amount ?? 0), locale),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: (row: AdminReferralCommission) => (
        <StatusBadge
          label={commissionStatusLabel(t, row.status)}
          variant={COMMISSION_STATUS_VARIANTS[row.status] ?? 'neutral'}
          copyable={false}
        />
      ),
    },
    {
      id: 'settle_at',
      header: t('Settles at'),
      cellClassName: 'tabular-nums whitespace-nowrap',
      cell: (row: AdminReferralCommission) =>
        row.settle_at > 0 ? formatTimestamp(row.settle_at) : '-',
    },
    ...(canAdjust
      ? [
          {
            id: 'actions',
            header: t('Actions'),
            className: 'text-right',
            cellClassName: 'text-right whitespace-nowrap',
            cell: (row: AdminReferralCommission) =>
              row.status === 'risk_held' ? (
                <div className='flex justify-end gap-1'>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={riskActionMutation.isPending}
                    onClick={() => openRiskAction(row, 'release')}
                  >
                    {t('Release')}
                  </Button>
                  <Button
                    size='sm'
                    variant='destructive'
                    disabled={riskActionMutation.isPending}
                    onClick={() => openRiskAction(row, 'void')}
                  >
                    {t('Void')}
                  </Button>
                </div>
              ) : null,
          },
        ]
      : []),
  ]

  return (
    <div className='flex flex-col gap-3'>
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
              {commissionStatusLabel(t, value)}
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
              <EmptyState className='min-h-32' title={t('No commission yet')} />
            }
            columns={columns}
          />
          <PageSwitcher
            page={page}
            total={Number(query.data?.total) || 0}
            pageSize={ADMIN_REFERRAL_PAGE_SIZE}
            onPageChange={setPage}
          />
        </>
      )}

      <ConfirmDialog
        open={pendingAction !== null}
        onOpenChange={(open) => {
          if (!open) closeRiskAction()
        }}
        title={
          pendingAction?.action === 'release'
            ? t('Release commission')
            : t('Void commission')
        }
        desc={(() => {
          if (!pendingAction) return ''
          const amount = formatUsd(
            Number(pendingAction.row.commission_amount ?? 0),
            locale
          )
          if (pendingAction.action === 'release') {
            return t(
              'Release this risk-held commission ({{amount}}) into withdrawable balance?',
              { amount }
            )
          }
          return t(
            'Permanently void this risk-held commission ({{amount}})? This cannot be undone.',
            { amount }
          )
        })()}
        destructive={pendingAction?.action === 'void'}
        confirmText={
          pendingAction?.action === 'release' ? t('Release') : t('Void')
        }
        isLoading={riskActionMutation.isPending}
        handleConfirm={() => {
          if (!pendingAction) return
          riskActionMutation.mutate({
            id: pendingAction.row.id,
            action: pendingAction.action,
            reason: actionReason,
          })
        }}
      >
        <div className='space-y-2 py-2'>
          <Label htmlFor='admin-commission-risk-reason'>
            {t('Reason (optional)')}
          </Label>
          <Input
            id='admin-commission-risk-reason'
            value={actionReason}
            disabled={riskActionMutation.isPending}
            onChange={(event) => setActionReason(event.target.value)}
          />
        </div>
      </ConfirmDialog>
    </div>
  )
}
