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
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
  getOwnerSiteReferralAuditLogs,
  getOwnerSiteReferralCommissions,
  getOwnerSiteReferralOverview,
  OWNER_PAGE_SIZE,
  resumeOwnerSiteCoverage,
  siteOwnerKeys,
  updateOwnerSiteReferralAffiliate,
  type SiteReferralAffiliateAction,
  type SiteReferralAffiliateItem,
  type SiteReferralOverview,
} from '../api'
import { MAX_SITE_REFERRAL_RATE_PERCENT } from '../lib/site-owner'
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

function formatSiteRate(raw: string | undefined): string {
  const trimmed = (raw ?? '').trim()
  if (trimmed === '') return '-'
  const value = Number(trimmed)
  if (!Number.isFinite(value)) return trimmed
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

function siteAuditActionLabel(t: TFunction, action: string): string {
  switch (action) {
    case 'settings_update':
      return t('Updated referral settings')
    case 'coverage_resume':
      return t('Resumed pending commission coverage')
    case 'invite_bind':
      return t('Bound invite manually')
    case 'withdrawal_approve':
      return t('Approved withdrawal')
    case 'withdrawal_reject':
      return t('Rejected withdrawal')
    case 'withdrawal_pay':
      return t('Marked withdrawal paid')
    case 'rate':
      return t('Changed affiliate rate')
    case 'disable':
      return t('Disabled affiliate')
    case 'restore':
      return t('Restored affiliate')
    case 'freeze_settlement':
      return t('Froze settlement')
    case 'restore_settlement':
      return t('Restored settlement')
    case 'freeze_withdrawal':
      return t('Froze withdrawals')
    case 'restore_withdrawal':
      return t('Restored withdrawals')
    default:
      return action
  }
}

function auditSnippet(value: string) {
  const text = (value || '').trim()
  if (!text || text === 'null' || text === '{}') return ''
  return text.length > 120 ? `${text.slice(0, 120)}…` : text
}

function SiteAffiliateState(props: { affiliate: SiteReferralAffiliateItem }) {
  const { t } = useTranslation()
  const aff = props.affiliate
  const disabled = aff.status === 'disabled'
  const settlementFrozen = aff.settlement_enabled === false
  const withdrawalFrozen = aff.withdrawal_enabled === false
  if (!disabled && !settlementFrozen && !withdrawalFrozen) {
    return (
      <StatusBadge label={t('Normal')} variant='success' copyable={false} />
    )
  }
  return (
    <div className='flex flex-wrap gap-1'>
      {disabled && (
        <StatusBadge label={t('Disabled')} variant='neutral' copyable={false} />
      )}
      {settlementFrozen && (
        <StatusBadge
          label={t('Settlement frozen')}
          variant='danger'
          copyable={false}
        />
      )}
      {withdrawalFrozen && (
        <StatusBadge
          label={t('Withdrawals frozen')}
          variant='danger'
          copyable={false}
        />
      )}
    </div>
  )
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
          <span className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
            {item.value}
          </span>
        </div>
      ))}
    </div>
  )
}

function SiteManualBindingsCard(props: { closing?: boolean }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const closing = Boolean(props.closing)
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
      <ContentSection
        className='border-border border-t pt-6'
        title={t('Manual invite binding')}
        description={t(
          'Manually bind an inviter and invitee on this site, or review existing one-level invite bindings.'
        )}
        actions={
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
        }
      >
        <CollapsibleContent className='flex flex-col gap-4 pt-2'>
          <form
            className='flex flex-wrap items-end gap-3'
            onSubmit={(event) => {
              event.preventDefault()
              if (
                closing ||
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
                disabled={closing}
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
                disabled={closing}
                onChange={(event) => setInviteeId(event.target.value)}
              />
            </div>
            <Button
              type='submit'
              variant='outline'
              disabled={
                closing ||
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
        </CollapsibleContent>
      </ContentSection>
    </Collapsible>
  )
}

function SiteAffiliatesTab(props: {
  closing?: boolean
  onViewCommissions: (affiliate: SiteReferralAffiliateItem) => void
}) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const closing = Boolean(props.closing)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const [editing, setEditing] = useState<SiteReferralAffiliateItem | null>(null)
  const [rate, setRate] = useState('')
  const [reason, setReason] = useState('')

  const openEditing = (row: SiteReferralAffiliateItem) => {
    setEditing(row)
    setRate(
      row.rate_override === null || row.rate_override === undefined
        ? ''
        : String(row.rate_override)
    )
    setReason('')
  }

  const closeEditing = () => {
    setEditing(null)
    setRate('')
    setReason('')
  }

  const query = useQuery({
    queryKey: siteOwnerKeys.list('site-referral-affiliates', {
      page,
      search,
    }),
    queryFn: () =>
      getOwnerSiteReferralAffiliates(page, OWNER_PAGE_SIZE, search),
    placeholderData: keepPreviousData,
  })

  const trimmedRate = rate.trim()
  const ownRate = trimmedRate === '' ? null : Number(trimmedRate)
  const rateInvalid =
    ownRate !== null &&
    (!Number.isFinite(ownRate) ||
      ownRate < 0 ||
      ownRate > MAX_SITE_REFERRAL_RATE_PERCENT)

  const update = useMutation({
    mutationFn: (input: {
      affiliate: SiteReferralAffiliateItem
      action: SiteReferralAffiliateAction
    }) =>
      updateOwnerSiteReferralAffiliate(input.affiliate.user_id, {
        action: input.action,
        rate: input.action === 'rate' ? ownRate : undefined,
        reason: reason.trim(),
      }),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      closeEditing()
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (err) => handleServerError(err),
  })

  const act = (
    affiliate: SiteReferralAffiliateItem,
    action: SiteReferralAffiliateAction
  ) => {
    if (closing || update.isPending) return
    if (action === 'rate' && rateInvalid) return
    update.mutate({ affiliate, action })
  }

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
          placeholder={t('Invite code or username')}
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
            tableClassName='min-w-[1100px]'
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
                id: 'invite_code',
                header: t('Invite code'),
                cell: (row) => (
                  <span className='font-mono text-xs'>
                    {row.invite_code || '-'}
                  </span>
                ),
              },
              {
                id: 'rate',
                header: t('Rate'),
                cell: (row) =>
                  row.rate_override === null || row.rate_override === undefined
                    ? t('Default')
                    : `${row.rate_override}%`,
              },
              {
                id: 'status',
                header: t('Status'),
                cell: (row) => <SiteAffiliateState affiliate={row} />,
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
                id: 'risk',
                header: t('Risk'),
                cell: (row) => (
                  <span className='text-muted-foreground text-xs'>
                    {row.risk_note || row.risk_reason || '-'}
                  </span>
                ),
              },
              {
                id: 'actions',
                header: t('Actions'),
                className: 'text-right',
                cellClassName: 'text-right',
                cell: (row) => (
                  <div className='flex justify-end gap-1'>
                    <Button
                      size='sm'
                      variant='outline'
                      onClick={() => props.onViewCommissions(row)}
                    >
                      {t('View commissions')}
                    </Button>
                    <Button
                      size='sm'
                      variant='outline'
                      disabled={closing}
                      onClick={() => openEditing(row)}
                    >
                      {t('Adjust')}
                    </Button>
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

      <Dialog
        open={editing !== null}
        onOpenChange={(open) => !open && closeEditing()}
        title={t('Adjust affiliate')}
        description={
          editing
            ? `${formatUserIdentity(editing.user_id, editing.username)}${
                editing.invite_code ? ` · ${editing.invite_code}` : ''
              }`
            : ''
        }
      >
        {editing && (
          <div className='space-y-5'>
            <div className='space-y-2'>
              <Label htmlFor='site-adjust-reason'>{t('Reason')}</Label>
              <Input
                id='site-adjust-reason'
                value={reason}
                disabled={closing || update.isPending}
                onChange={(event) => setReason(event.target.value)}
              />
            </div>
            <div className='space-y-1.5'>
              <div className='flex items-end gap-2'>
                <div className='flex-1 space-y-2'>
                  <Label htmlFor='site-adjust-rate'>
                    {t('Own rate (%, max 30%), empty for site default')}
                  </Label>
                  <Input
                    id='site-adjust-rate'
                    type='number'
                    min={0}
                    max={MAX_SITE_REFERRAL_RATE_PERCENT}
                    step='0.01'
                    disabled={closing || update.isPending}
                    aria-invalid={rateInvalid}
                    value={rate}
                    onChange={(event) => setRate(event.target.value)}
                  />
                </div>
                <Button
                  type='button'
                  disabled={closing || update.isPending || rateInvalid}
                  onClick={() => act(editing, 'rate')}
                >
                  {t('Set rate')}
                </Button>
              </div>
              {rateInvalid && (
                <p role='alert' className='text-destructive text-xs'>
                  {t('Commission rate must be between 0% and 30%.')}
                </p>
              )}
            </div>
            <div className='space-y-2 border-t pt-4'>
              <p className='text-muted-foreground text-xs font-medium'>
                {t('Account controls')}
              </p>
              <div className='flex flex-wrap gap-2'>
                <Button
                  type='button'
                  size='sm'
                  variant={
                    editing.status === 'disabled' ? 'outline' : 'destructive'
                  }
                  disabled={closing || update.isPending}
                  onClick={() =>
                    act(
                      editing,
                      editing.status === 'disabled' ? 'restore' : 'disable'
                    )
                  }
                >
                  {editing.status === 'disabled'
                    ? t('Restore affiliate')
                    : t('Disable affiliate')}
                </Button>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={closing || update.isPending}
                  onClick={() =>
                    act(
                      editing,
                      editing.settlement_enabled === false
                        ? 'restore_settlement'
                        : 'freeze_settlement'
                    )
                  }
                >
                  {editing.settlement_enabled === false
                    ? t('Restore settlement')
                    : t('Freeze settlement')}
                </Button>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={closing || update.isPending}
                  onClick={() =>
                    act(
                      editing,
                      editing.withdrawal_enabled === false
                        ? 'restore_withdrawal'
                        : 'freeze_withdrawal'
                    )
                  }
                >
                  {editing.withdrawal_enabled === false
                    ? t('Restore withdrawals')
                    : t('Freeze withdrawals')}
                </Button>
              </div>
            </div>
          </div>
        )}
      </Dialog>

      <SiteManualBindingsCard closing={closing} />
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

function SiteAuditLogTab() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: siteOwnerKeys.list('site-referral-audit-logs', { page }),
    queryFn: () => getOwnerSiteReferralAuditLogs(page, OWNER_PAGE_SIZE),
    placeholderData: keepPreviousData,
  })
  const rows = Array.isArray(query.data?.items) ? query.data.items : []

  return (
    <section className='flex flex-col gap-3 pt-4'>
      <p className='text-muted-foreground max-w-2xl text-sm'>
        {t(
          'Records of withdrawal reviews, affiliate adjustments, invite bindings, and referral settings changes on this site.'
        )}
      </p>
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.isPending && !query.data ? (
        <LoadingState />
      ) : (
        <>
          <StaticDataTable
            tableClassName='min-w-[900px]'
            tableProps={{ 'aria-label': t('Admin activity') }}
            data={rows}
            getRowKey={(row) => String(row.id)}
            emptyContent={
              <EmptyState className='min-h-24' title={t('No records')} />
            }
            columns={[
              {
                id: 'time',
                header: t('Time'),
                cell: (row) => formatTimestamp(row.created_at),
              },
              {
                id: 'action',
                header: t('Action'),
                cell: (row) => siteAuditActionLabel(t, row.action),
              },
              {
                id: 'operator',
                header: t('Operator'),
                cellClassName: 'tabular-nums',
                cell: (row) =>
                  row.operator_id
                    ? formatUserIdentity(row.operator_id, row.operator_username)
                    : '—',
              },
              {
                id: 'target',
                header: t('Target'),
                cellClassName: 'tabular-nums',
                cell: (row) =>
                  row.target_user_id
                    ? formatUserIdentity(
                        row.target_user_id,
                        row.target_username
                      )
                    : '—',
              },
              {
                id: 'reason',
                header: t('Reason'),
                cell: (row) => (
                  <span className='text-xs break-words'>
                    {row.reason || '—'}
                  </span>
                ),
              },
              {
                id: 'change',
                header: t('Change'),
                cell: (row) => {
                  const before = auditSnippet(row.old_value)
                  const after = auditSnippet(row.new_value)
                  if (!before && !after) return '—'
                  return (
                    <code
                      className='text-muted-foreground font-mono text-xs break-all'
                      title={`${row.old_value} → ${row.new_value}`}
                    >
                      {before ? `${before} → ` : ''}
                      {after}
                    </code>
                  )
                },
              },
              {
                id: 'ip',
                header: t('IP'),
                cell: (row) => (
                  <span className='font-mono text-xs'>{row.ip || '—'}</span>
                ),
              },
            ]}
          />
          <PageSwitcher
            page={page}
            total={Number(query.data?.total) || 0}
            pageSize={OWNER_PAGE_SIZE}
            onPageChange={setPage}
          />
        </>
      )}
    </section>
  )
}

/** Unified subsite Promotion Management workspace matching main-site /admin-referral (R-34 #1, R-37 #1). */
export function SiteReferralAdmin(props: { closing?: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const closing = Boolean(props.closing)
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
              value='audit-logs'
              className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
            >
              {t('Admin activity')}
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
            closing={closing}
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
        <TabsContent value='audit-logs'>
          <SiteAuditLogTab />
        </TabsContent>
        <TabsContent value='settings' className='pt-4'>
          <SiteReferralPanel closing={closing} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
