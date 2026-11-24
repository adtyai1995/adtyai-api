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
import type { TFunction } from 'i18next'
import { ChevronDown, ChevronUp, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { adtySiteQueryKey } from '@/features/adty-site/api'
import { referralQueryKeys } from '@/features/referral/api'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import { getSelf } from '@/lib/api'
import { formatNumber, formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import {
  bindOwnerSiteInvite,
  getOwnerSiteInvites,
  getOwnerSiteReferral,
  getOwnerSiteReferralAffiliates,
  getOwnerSiteReferralCommissions,
  getOwnerSiteReferralOverview,
  OWNER_PAGE_SIZE,
  resumeOwnerSiteCoverage,
  siteOwnerKeys,
  type SiteReferralAffiliateItem,
  type SiteReferralOverview,
} from '../api'
import { CommissionWithdrawalPanel } from './commission-withdrawal-panel'
import { SiteReferralPanel } from './site-referral-panel'

type SubsiteAffiliateFilter = {
  userId: number
  username?: string
}

const COMMISSION_STATUS_OPTIONS = [
  'pending_coverage',
  'covered',
  'available',
  'clawed_back',
] as const

function formatSiteRate(raw: string): string {
  const value = Number(raw)
  if (!Number.isFinite(value)) return raw || '-'
  const pct = value <= 1 ? value * 100 : value
  return `${Number(pct.toFixed(4))}%`
}

/** Literal t() calls so the i18n key scanner sees every status label. */
function siteCommissionStatusLabel(t: TFunction, status: string): string {
  switch (status) {
    case 'pending_coverage':
      return t('Pending coverage')
    case 'covered':
    case 'pending':
      return t('Settling')
    case 'available':
      return t('Withdrawable')
    case 'frozen':
      return t('Withdrawing')
    case 'paid':
      return t('Withdrawn')
    case 'void':
      return t('Voided')
    case 'clawed_back':
      return t('Clawed back')
    default:
      return status || '-'
  }
}

function siteCommissionStatusVariant(status: string): StatusVariant {
  if (status === 'available') return 'success'
  if (
    status === 'pending_coverage' ||
    status === 'covered' ||
    status === 'pending'
  ) {
    return 'warning'
  }
  if (status === 'frozen') return 'info'
  if (status === 'clawed_back') return 'danger'
  return 'neutral'
}

function coverageStatusLabel(t: TFunction, status: string): string {
  switch (status) {
    case 'covered':
    case 'full':
      return t('Fully covered')
    case 'partial':
      return t('Partially covered')
    case 'pending':
    case 'pending_coverage':
    case 'uncovered':
      return t('Uncovered')
    default:
      return status
  }
}

function SiteReferralKpiStrip(props: { data: SiteReferralOverview }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const data = props.data

  const items = [
    {
      label: t('Affiliates'),
      value: formatNumber(data.affiliates ?? 0, locale),
    },
    {
      label: t('Referred users'),
      value: formatNumber(data.bindings ?? 0, locale),
    },
    {
      label: t('Settling'),
      value: formatUsd(Number(data.pending ?? 0), locale),
    },
    {
      label: t('Withdrawable'),
      value: formatUsd(Number(data.available ?? 0), locale),
    },
    {
      label: t('Withdrawing'),
      value: formatUsd(Number(data.frozen ?? 0), locale),
    },
    {
      label: t('Withdrawn'),
      value: formatUsd(Number(data.withdrawn ?? 0), locale),
    },
  ]

  return (
    <div className='border-border bg-border grid grid-cols-2 gap-px overflow-hidden rounded-[14px] border lg:grid-cols-3 xl:grid-cols-6'>
      {items.map((item) => (
        <div key={item.label} className='bg-card flex flex-col gap-1 px-5 py-4'>
          <span className='text-dim text-[12px] leading-none'>
            {item.label}
          </span>
          <span className='text-[19px] leading-tight font-semibold tabular-nums'>
            {item.value}
          </span>
        </div>
      ))}
    </div>
  )
}

function SiteManualBindingsCard() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [open, setOpen] = useState(false)
  const [inviterId, setInviterId] = useState('')
  const [inviteeId, setInviteeId] = useState('')

  const rules = useQuery({
    queryKey: siteOwnerKeys.list('site-referral', {}),
    queryFn: getOwnerSiteReferral,
    enabled: open,
  })
  const invites = useQuery({
    queryKey: siteOwnerKeys.list('site-invites', {}),
    queryFn: getOwnerSiteInvites,
    enabled: open,
  })

  const bind = useMutation({
    mutationFn: () => bindOwnerSiteInvite(Number(inviterId), Number(inviteeId)),
    onSuccess: () => {
      setInviterId('')
      setInviteeId('')
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void invalidateSidebarBadges(client)
    },
    onError: (error) => handleServerError(error),
  })

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <Card size='sm'>
        <CardHeader>
          <CardTitle>{t('Manual invite binding')}</CardTitle>
          <CardDescription>
            {t(
              'Manually bind an inviter and invitee on this site, or review existing one-level invite bindings.'
            )}
          </CardDescription>
          <CardAction>
            <CollapsibleTrigger
              render={<Button type='button' size='sm' variant='outline' />}
            >
              {open ? t('Collapse') : t('Expand')}
              {open ? (
                <ChevronUp className='size-4' aria-hidden='true' />
              ) : (
                <ChevronDown className='size-4' aria-hidden='true' />
              )}
            </CollapsibleTrigger>
          </CardAction>
        </CardHeader>
        <CollapsibleContent>
          <CardContent className='flex flex-col gap-4 border-t pt-4'>
            <form
              className='flex flex-wrap items-end gap-3'
              onSubmit={(event) => {
                event.preventDefault()
                if (
                  bind.isPending ||
                  !rules.data?.rate_configured ||
                  inviterId.trim() === '' ||
                  inviteeId.trim() === ''
                ) {
                  return
                }
                bind.mutate()
              }}
            >
              <div className='flex min-w-40 flex-1 flex-col gap-1'>
                <Label htmlFor='site-inviter'>{t('Inviter')}</Label>
                <Input
                  id='site-inviter'
                  value={inviterId}
                  inputMode='numeric'
                  placeholder={t('User ID')}
                  onChange={(event) => setInviterId(event.target.value)}
                />
              </div>
              <div className='flex min-w-40 flex-1 flex-col gap-1'>
                <Label htmlFor='site-invitee'>{t('Invitee')}</Label>
                <Input
                  id='site-invitee'
                  value={inviteeId}
                  inputMode='numeric'
                  placeholder={t('User ID')}
                  onChange={(event) => setInviteeId(event.target.value)}
                />
              </div>
              <Button
                type='submit'
                variant='outline'
                disabled={
                  bind.isPending ||
                  !rules.data?.rate_configured ||
                  inviterId.trim() === '' ||
                  inviteeId.trim() === ''
                }
              >
                {t('Bind invite')}
              </Button>
            </form>
            {rules.isSuccess && !rules.data.rate_configured && (
              <p className='text-muted-foreground text-xs'>
                {t('Set the invite rate first before enabling.')}
              </p>
            )}

            {invites.isPending && <LoadingState />}
            {invites.isError && (
              <ErrorState onRetry={() => void invites.refetch()} />
            )}
            {invites.isSuccess && (
              <StaticDataTable
                data={invites.data}
                getRowKey={(row) => String(row.id)}
                tableProps={{ 'aria-label': t('One-level invites') }}
                emptyContent={
                  <EmptyState
                    className='min-h-24'
                    title={t('No invites yet')}
                  />
                }
                columns={[
                  {
                    id: 'inviter',
                    header: t('Inviter'),
                    cellClassName: 'tabular-nums',
                    cell: (row) =>
                      formatUserIdentity(row.inviter_id, row.inviter_username),
                  },
                  {
                    id: 'invitee',
                    header: t('Invitee'),
                    cellClassName: 'tabular-nums',
                    cell: (row) =>
                      formatUserIdentity(row.invitee_id, row.invitee_username),
                  },
                  {
                    id: 'rate',
                    header: t('Rate at binding'),
                    className: 'text-right',
                    cellClassName: 'text-right tabular-nums',
                    cell: (row) => formatSiteRate(row.rate),
                  },
                  {
                    id: 'created_at',
                    header: t('Created at'),
                    cell: (row) =>
                      row.created_at > 0
                        ? formatTimestamp(row.created_at)
                        : '-',
                  },
                ]}
              />
            )}
          </CardContent>
        </CollapsibleContent>
      </Card>
    </Collapsible>
  )
}

function SiteAffiliatesTab(props: {
  onViewCommissions: (affiliate: SiteReferralAffiliateItem) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')

  const query = useQuery({
    queryKey: siteOwnerKeys.list('site-referral-affiliates', {
      page,
      search,
    }),
    queryFn: () =>
      getOwnerSiteReferralAffiliates(page, OWNER_PAGE_SIZE, search),
    placeholderData: keepPreviousData,
  })

  return (
    <div className='flex flex-col gap-4 pt-3'>
      <form
        className='flex flex-wrap items-center gap-2'
        onSubmit={(event) => {
          event.preventDefault()
          setSearch(keyword.trim())
          setPage(1)
        }}
      >
        <Input
          className='w-64'
          value={keyword}
          onChange={(event) => setKeyword(event.target.value)}
          placeholder={t('Search by username or ID')}
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

      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.isPending && !query.data ? (
        <LoadingState />
      ) : (
        <>
          <StaticDataTable
            tableClassName='min-w-[960px]'
            tableProps={{ 'aria-label': t('Affiliates') }}
            data={query.data?.items ?? []}
            getRowKey={(row) => String(row.user_id)}
            emptyContent={
              <EmptyState className='min-h-32' title={t('No affiliates yet')} />
            }
            columns={[
              {
                id: 'user',
                header: t('Affiliate'),
                cellClassName: 'font-medium tabular-nums',
                cell: (row) => formatUserIdentity(row.user_id, row.username),
              },
              {
                id: 'invite_count',
                header: t('Referred users'),
                className: 'text-right',
                cellClassName: 'text-right tabular-nums',
                cell: (row) => formatNumber(row.invite_count ?? 0, locale),
              },
              {
                id: 'pending',
                header: t('Settling'),
                className: 'text-right',
                cellClassName: 'text-right tabular-nums',
                cell: (row) => formatUsd(Number(row.pending ?? 0), locale),
              },
              {
                id: 'available',
                header: t('Withdrawable'),
                className: 'text-right',
                cellClassName: 'text-right tabular-nums',
                cell: (row) => formatUsd(Number(row.available ?? 0), locale),
              },
              {
                id: 'frozen',
                header: t('Withdrawing'),
                className: 'text-right',
                cellClassName: 'text-right tabular-nums',
                cell: (row) => formatUsd(Number(row.frozen ?? 0), locale),
              },
              {
                id: 'withdrawn',
                header: t('Withdrawn'),
                className: 'text-right',
                cellClassName: 'text-right tabular-nums',
                cell: (row) => formatUsd(Number(row.withdrawn ?? 0), locale),
              },
              {
                id: 'total_earned',
                header: t('Total earned'),
                className: 'text-right',
                cellClassName: 'text-right font-medium tabular-nums',
                cell: (row) => formatUsd(Number(row.total_earned ?? 0), locale),
              },
              {
                id: 'actions',
                header: t('Actions'),
                className: 'text-right',
                cellClassName: 'text-right',
                cell: (row) => (
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => props.onViewCommissions(row)}
                  >
                    {t('View commissions')}
                  </Button>
                ),
              },
            ]}
          />
          <PageSwitcher
            page={page}
            total={query.data?.total ?? 0}
            pageSize={OWNER_PAGE_SIZE}
            onPageChange={setPage}
          />
        </>
      )}

      <SiteManualBindingsCard />
    </div>
  )
}

function SiteCommissionsTab(props: {
  affiliateFilter: SubsiteAffiliateFilter | null
  onClearAffiliateFilter: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')

  const inviterUserId = props.affiliateFilter?.userId

  useEffect(() => {
    setPage(1)
  }, [inviterUserId])

  const query = useQuery({
    queryKey: siteOwnerKeys.list('site-referral-commissions', {
      page,
      status,
      search,
      inviterUserId,
    }),
    queryFn: () =>
      getOwnerSiteReferralCommissions(
        page,
        OWNER_PAGE_SIZE,
        status,
        search,
        inviterUserId
      ),
    placeholderData: keepPreviousData,
  })

  return (
    <div className='flex flex-col gap-3 pt-3'>
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
            placeholder={t('Search by trade no, username or ID')}
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
          aria-label={t('Status')}
          value={status}
          onChange={(event) => {
            setStatus(event.target.value)
            setPage(1)
          }}
        >
          <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
          {COMMISSION_STATUS_OPTIONS.map((value) => (
            <NativeSelectOption key={value} value={value}>
              {siteCommissionStatusLabel(t, value)}
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
            tableClassName='min-w-[1060px]'
            tableProps={{ 'aria-label': t('Commission details') }}
            data={query.data?.items ?? []}
            getRowKey={(row) => String(row.id)}
            emptyContent={
              <EmptyState className='min-h-32' title={t('No commission yet')} />
            }
            columns={[
              {
                id: 'trade_no',
                header: t('Order number'),
                cell: (row) => (
                  <code className='font-mono text-xs break-all'>
                    {row.trade_no || `#${row.order_id}`}
                  </code>
                ),
              },
              {
                id: 'inviter',
                header: t('Inviter'),
                cellClassName: 'tabular-nums',
                cell: (row) =>
                  formatUserIdentity(row.inviter_id, row.inviter_username),
              },
              {
                id: 'invitee',
                header: t('Invitee'),
                cellClassName: 'tabular-nums',
                cell: (row) =>
                  formatUserIdentity(row.invitee_id, row.invitee_username),
              },
              {
                id: 'order_amount',
                header: t('Order amount'),
                className: 'text-right',
                cellClassName: 'text-right tabular-nums',
                cell: (row) =>
                  formatUsd(Number(row.order_amount_usd ?? 0), locale),
              },
              {
                id: 'rate',
                header: t('Rate'),
                className: 'text-right',
                cellClassName: 'text-right tabular-nums',
                cell: (row) => formatSiteRate(row.rate),
              },
              {
                id: 'amount',
                header: t('Commission'),
                className: 'text-right',
                cellClassName: 'text-right font-medium tabular-nums',
                cell: (row) => formatUsd(Number(row.amount_usd ?? 0), locale),
              },
              {
                id: 'covered',
                header: t('Covered amount'),
                className: 'text-right',
                cellClassName: 'text-right tabular-nums',
                cell: (row) => {
                  const remaining = Number(row.remaining_usd ?? 0)
                  return (
                    <div className='flex flex-col items-end'>
                      <span>
                        {formatUsd(Number(row.covered_usd ?? 0), locale)}
                      </span>
                      {remaining > 0 && (
                        <span className='text-muted-foreground text-xs'>
                          {t('Remaining')}: {formatUsd(remaining, locale)}
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
                  const showCoverageHint =
                    row.coverage_status !== '' &&
                    row.coverage_status !== row.status &&
                    row.coverage_status !== 'covered' &&
                    row.coverage_status !== 'full'
                  return (
                    <div className='flex flex-col items-start gap-1'>
                      <StatusBadge
                        label={siteCommissionStatusLabel(t, row.status)}
                        variant={siteCommissionStatusVariant(row.status)}
                        copyable={false}
                      />
                      {showCoverageHint && (
                        <span className='text-muted-foreground text-xs'>
                          {coverageStatusLabel(t, row.coverage_status)}
                        </span>
                      )}
                    </div>
                  )
                },
              },
              {
                id: 'time',
                header: t('Created / Settle time'),
                cell: (row) => (
                  <div className='flex flex-col text-xs tabular-nums'>
                    <span>
                      {row.created_at > 0
                        ? formatTimestamp(row.created_at)
                        : '-'}
                    </span>
                    {row.settle_at > 0 && (
                      <span className='text-muted-foreground'>
                        {t('Settles at')}: {formatTimestamp(row.settle_at)}
                      </span>
                    )}
                  </div>
                ),
              },
            ]}
          />
          <PageSwitcher
            page={page}
            total={query.data?.total ?? 0}
            pageSize={OWNER_PAGE_SIZE}
            onPageChange={setPage}
          />
        </>
      )}
    </div>
  )
}

/** Unified subsite Promotion Management workspace matching main-site /admin-referral (R-34 #1). */
export function SiteReferralAdmin() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [activeTab, setActiveTab] = useState('withdrawals')
  const [commissionAffiliateFilter, setCommissionAffiliateFilter] =
    useState<SubsiteAffiliateFilter | null>(null)

  const overview = useQuery({
    queryKey: siteOwnerKeys.list('site-referral-overview', {}),
    queryFn: getOwnerSiteReferralOverview,
  })

  // Refresh /api/status and user self cache immediately when the subsite referral
  // enabled switch changes in SiteReferralPanel (R-35 #15).
  const referralRules = useQuery({
    queryKey: siteOwnerKeys.list('site-referral', {}),
    queryFn: getOwnerSiteReferral,
  })
  const previousEnabledRef = useRef<boolean | undefined>(undefined)
  useEffect(() => {
    const currentEnabled = referralRules.data?.enabled
    if (currentEnabled === undefined) return
    if (
      previousEnabledRef.current !== undefined &&
      previousEnabledRef.current !== currentEnabled
    ) {
      void queryClient.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
      void queryClient.invalidateQueries({ queryKey: adtySiteQueryKey })
      void queryClient.invalidateQueries({ queryKey: referralQueryKeys.all })
      void queryClient.invalidateQueries({ queryKey: ['user', 'self'] })
      void getSelf()
        .then((res) => {
          if (res?.success && res.data) {
            useAuthStore.getState().auth.setUser(res.data as AuthUser)
          }
        })
        .catch(() => {})
    }
    previousEnabledRef.current = currentEnabled
  }, [referralRules.data?.enabled, queryClient])

  const resumeCoverage = useMutation({
    mutationFn: resumeOwnerSiteCoverage,
    onSuccess: () => {
      toast.success(t('Pending commission coverage resumed'))
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (error) => handleServerError(error),
  })

  const data = overview.data
  const pendingWithdrawals = data?.pending_withdrawals ?? 0
  const pendingCoverageCount = data?.pending_coverage_count ?? 0

  return (
    <div className='flex flex-col gap-4'>
      {overview.isError && (
        <ErrorState onRetry={() => void overview.refetch()} />
      )}
      {overview.isPending && <LoadingState />}
      {data && <SiteReferralKpiStrip data={data} />}

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <TabsList
            variant='line'
            className='border-border h-auto flex-wrap justify-start gap-x-6 gap-y-1 rounded-none border-b p-0 [--tab-underline:var(--brand)] group-data-horizontal/tabs:h-auto'
          >
            <TabsTrigger
              value='withdrawals'
              className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
            >
              {t('Withdrawals')}
              {pendingWithdrawals > 0 && ` (${pendingWithdrawals})`}
            </TabsTrigger>
            <TabsTrigger
              value='affiliates'
              className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
            >
              {t('Affiliates')}
            </TabsTrigger>
            <TabsTrigger
              value='commissions'
              className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
            >
              {t('Commission details')}
            </TabsTrigger>
            <TabsTrigger
              value='settings'
              className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
            >
              {t('Settings')}
            </TabsTrigger>
          </TabsList>
          <Button
            size='sm'
            variant='outline'
            disabled={resumeCoverage.isPending}
            onClick={() => resumeCoverage.mutate()}
          >
            {t('Cover pending commissions')}
            {pendingCoverageCount > 0 && ` (${pendingCoverageCount})`}
          </Button>
        </div>

        <TabsContent value='withdrawals' className='pt-3'>
          <CommissionWithdrawalPanel />
        </TabsContent>
        <TabsContent value='affiliates'>
          <SiteAffiliatesTab
            onViewCommissions={(row) => {
              setCommissionAffiliateFilter({
                userId: row.user_id,
                username: row.username,
              })
              setActiveTab('commissions')
            }}
          />
        </TabsContent>
        <TabsContent value='commissions'>
          <SiteCommissionsTab
            affiliateFilter={commissionAffiliateFilter}
            onClearAffiliateFilter={() => setCommissionAffiliateFilter(null)}
          />
        </TabsContent>
        <TabsContent value='settings' className='pt-4'>
          <SiteReferralPanel />
        </TabsContent>
      </Tabs>
    </div>
  )
}
