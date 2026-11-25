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
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  Plus,
  Settings2,
  Trash2,
} from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  deleteSite,
  getResellerSettings,
  getSites,
  getSitesOverview,
  SITE_ADMIN_PAGE_SIZE,
  siteAdminKeys,
} from '../api'
import {
  DEFAULT_SITE_SORT,
  nextSiteSort,
  parseSiteBrand,
  type SiteSort,
  type SiteSortKey,
} from '../lib/sites'
import type { SiteAdminSectionId } from '../sections'
import type { AdminSite, SiteStatus } from '../types'
import { OpenSiteDialog } from './open-site-dialog'
import { SiteStatusBadge } from './site-status-badge'

const STATUSES: SiteStatus[] = [
  'active',
  'closing',
  'closed',
  'deleting',
  'pending',
]

/** Column header that sorts the loaded page; aria-sort sits on the header cell. */
function SortHeader(props: {
  label: string
  sortKey: SiteSortKey
  sort: SiteSort
  onSort: (key: SiteSortKey) => void
}) {
  const active = props.sort.key === props.sortKey
  let icon: ReactNode = <ArrowUpDown className='size-3.5' aria-hidden />
  if (active) {
    icon =
      props.sort.direction === 'asc' ? (
        <ArrowUp className='size-3.5' aria-hidden />
      ) : (
        <ArrowDown className='size-3.5' aria-hidden />
      )
  }
  return (
    <Button
      type='button'
      variant='ghost'
      size='sm'
      className='-ml-1 h-7 gap-1 px-1 text-[13px]'
      onClick={() => props.onSort(props.sortKey)}
    >
      {props.label}
      {icon}
    </Button>
  )
}

function ariaSort(sort: SiteSort, key: SiteSortKey) {
  if (sort.key !== key) return undefined
  return sort.direction === 'asc' ? 'ascending' : 'descending'
}

/** Site list: KPI summary strip, status filter, search, sortable columns, manage navigation, and delete action. */
export function SitesPanel(props: {
  onOpenSite: (site: AdminSite) => void
  onNavigateSection?: (section: SiteAdminSectionId) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const fieldId = useId()
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<SiteSort>(DEFAULT_SITE_SORT)
  const [openDialog, setOpenDialog] = useState(false)
  const [deletingSite, setDeletingSite] = useState<AdminSite | null>(null)
  const [deleteReason, setDeleteReason] = useState('')

  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const settingsQuery = useQuery({
    queryKey: siteAdminKeys.settings,
    queryFn: getResellerSettings,
    enabled: isRoot,
  })
  const overviewQuery = useQuery({
    queryKey: siteAdminKeys.list('sites-overview-strip', {}),
    queryFn: getSitesOverview,
  })

  const settings = settingsQuery.data
  const missingPrereqs: {
    key: string
    label: string
    section: SiteAdminSectionId
    actionLabel: string
  }[] = []
  if (settings) {
    if (!(settings.tier_configured ?? settings.money_configured)) {
      missingPrereqs.push({
        key: 'tiers',
        label: t('No enabled site level with a supply discount is configured'),
        section: 'settings',
        actionLabel: t('Supply settings'),
      })
    }
    if ((settings.root_domains ?? []).length === 0) {
      missingPrereqs.push({
        key: 'roots',
        label: t('No platform root domain is configured'),
        section: 'domains',
        actionLabel: t('Domains'),
      })
    }
    if (!settings.enabled) {
      missingPrereqs.push({
        key: 'enabled',
        label: t('Reseller sites master switch is turned off'),
        section: 'settings',
        actionLabel: t('Supply settings'),
      })
    }
  }
  const openingReady = Boolean(settings && missingPrereqs.length === 0)
  const params = {
    p: page,
    status,
    keyword: search,
    sort: sort.key,
    order: sort.direction,
  }
  const query = useQuery({
    queryKey: siteAdminKeys.list('sites', params),
    queryFn: () => getSites(params),
  })

  const removeMutation = useMutation({
    mutationFn: (site: AdminSite) => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      return deleteSite(site.id, deleteReason.trim() || undefined)
    },
    onSuccess: (result) => {
      setDeletingSite(null)
      setDeleteReason('')
      toast.success(
        result.mode === 'hard_deleted'
          ? t('Site permanently deleted')
          : t('Closed site archived and hidden')
      )
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (err) => handleServerError(err),
  })

  const rows = query.data?.items ?? []
  const onSort = (key: SiteSortKey) => {
    setSort((prev) => nextSiteSort(prev, key))
    setPage(1)
  }
  const statusLabels: Record<SiteStatus, string> = {
    pending: t('Pending review'),
    active: t('Active'),
    paused: t('Paused'),
    closing: t('Closing'),
    closed: t('Closed'),
    deleting: t('Deleting'),
  }

  const siteCounts = overviewQuery.data?.sites ?? {}
  const activeCount = siteCounts.active ?? 0
  const closingCount = siteCounts.closing ?? 0
  const closedCount = siteCounts.closed ?? 0
  const totalCount = Object.values(siteCounts).reduce(
    (sum, count) => sum + count,
    0
  )

  if (query.isError && !query.data) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }

  return (
    <div className='flex flex-col gap-6'>
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}

      {/* KPI Summary Strip */}
      <dl
        aria-label={t('Subsite summary')}
        className='border-border bg-border grid grid-cols-2 gap-px overflow-hidden rounded-[14px] border lg:grid-cols-4'
      >
        <div className='bg-card flex flex-col gap-2 px-5 py-4'>
          <dt className='text-dim text-[12px] leading-none'>
            {t('Total sites')}
          </dt>
          <dd className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
            {overviewQuery.isSuccess ? formatNumber(totalCount, locale) : '—'}
          </dd>
        </div>
        <div className='bg-card flex flex-col gap-2 px-5 py-4'>
          <dt className='text-dim text-[12px] leading-none'>
            {t('Active sites')}
          </dt>
          <dd className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
            {overviewQuery.isSuccess ? formatNumber(activeCount, locale) : '—'}
          </dd>
        </div>
        <div className='bg-card flex flex-col gap-2 px-5 py-4'>
          <dt className='text-dim text-[12px] leading-none'>
            {t('Closing sites')}
          </dt>
          <dd className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
            {overviewQuery.isSuccess ? formatNumber(closingCount, locale) : '—'}
          </dd>
        </div>
        <div className='bg-card flex flex-col gap-2 px-5 py-4'>
          <dt className='text-dim text-[12px] leading-none'>
            {t('Closed sites')}
          </dt>
          <dd className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
            {overviewQuery.isSuccess ? formatNumber(closedCount, locale) : '—'}
          </dd>
        </div>
      </dl>

      {isRoot && settings && !openingReady && (
        <Alert className='border-warning/40 bg-warning/5'>
          <AlertTriangle className='text-warning size-4' />
          <AlertTitle>
            {t('Complete prerequisites before opening a site')}
          </AlertTitle>
          <AlertDescription className='mt-2 flex flex-col gap-2'>
            <ul className='list-disc space-y-1 pl-4 text-xs'>
              {missingPrereqs.map((item) => (
                <li key={item.key}>{item.label}</li>
              ))}
            </ul>
            {props.onNavigateSection && (
              <div className='flex flex-wrap gap-2 pt-1'>
                {missingPrereqs.some((item) => item.section === 'settings') && (
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    onClick={() => props.onNavigateSection?.('settings')}
                  >
                    {t('Go to Supply settings')}
                  </Button>
                )}
                {missingPrereqs.some((item) => item.section === 'domains') && (
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    onClick={() => props.onNavigateSection?.('domains')}
                  >
                    {t('Go to Domains')}
                  </Button>
                )}
              </div>
            )}
          </AlertDescription>
        </Alert>
      )}

      {/* Search & Filter Toolbar */}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <form
          className='flex flex-wrap items-center gap-2'
          onSubmit={(event) => {
            event.preventDefault()
            setSearch(keyword.trim())
            setPage(1)
          }}
        >
          <NativeSelect
            aria-label={t('Status')}
            value={status}
            onChange={(event) => {
              setStatus(event.target.value)
              setPage(1)
            }}
          >
            <NativeSelectOption value=''>
              {t('All statuses')}
            </NativeSelectOption>
            {STATUSES.map((item) => (
              <NativeSelectOption key={item} value={item}>
                {statusLabels[item]}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <Input
            className='w-64'
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            placeholder={t('Subdomain or owner username')}
            aria-label={t('Search')}
          />
          <Button variant='outline' type='submit'>
            {t('Search')}
          </Button>
        </form>
        {isRoot && (
          <Button
            type='button'
            variant='brand'
            disabled={!openingReady}
            onClick={() => setOpenDialog(true)}
          >
            <Plus className='size-4' aria-hidden />
            {t('Open a reseller site')}
          </Button>
        )}
      </div>

      {isRoot && (
        <OpenSiteDialog
          open={openDialog}
          settings={settings}
          onOpenChange={setOpenDialog}
          onViewSite={props.onOpenSite}
        />
      )}

      {/* Clean Scannable Table */}
      <div className='border-border bg-card w-full min-w-0 overflow-x-auto overflow-y-hidden rounded-[14px] border'>
        <Table
          withContainer={false}
          className='min-w-[960px] text-[13px] [&_td]:px-2 [&_th]:px-2'
          aria-label={t('Reseller sites')}
        >
          <TableHeader>
            <TableRow>
              <TableHead>{t('Site')}</TableHead>
              <TableHead>{t('Owner')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead className='text-right'>{t('Cost factor')}</TableHead>
              <TableHead
                className='text-right'
                aria-sort={ariaSort(sort, 'customers')}
              >
                <SortHeader
                  label={t('Customers')}
                  sortKey='customers'
                  sort={sort}
                  onSort={onSort}
                />
              </TableHead>
              <TableHead
                className='text-right'
                aria-sort={ariaSort(sort, 'topup')}
              >
                <SortHeader
                  label={t('Top-up')}
                  sortKey='topup'
                  sort={sort}
                  onSort={onSort}
                />
              </TableHead>
              <TableHead
                className='text-right'
                aria-sort={ariaSort(sort, 'revenue')}
              >
                <SortHeader
                  label={t('Revenue')}
                  sortKey='revenue'
                  sort={sort}
                  onSort={onSort}
                />
              </TableHead>
              <TableHead
                className='text-right'
                aria-sort={ariaSort(sort, 'earning')}
              >
                <SortHeader
                  label={t('Earnings')}
                  sortKey='earning'
                  sort={sort}
                  onSort={onSort}
                />
              </TableHead>
              <TableHead
                className='text-right'
                aria-sort={ariaSort(sort, 'growth')}
              >
                <SortHeader
                  label={t('Growth')}
                  sortKey='growth'
                  sort={sort}
                  onSort={onSort}
                />
              </TableHead>
              <TableHead>{t('Opened')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={11}>
                  <EmptyState className='min-h-32' title={t('No data')} />
                </TableCell>
              </TableRow>
            ) : (
              rows.map((row) => {
                const brandName = parseSiteBrand(row.brand).name?.trim()
                return (
                  <TableRow key={row.id} className='hover:bg-muted/40'>
                    <TableCell>
                      <button
                        type='button'
                        className='group flex flex-col text-left'
                        onClick={() => props.onOpenSite(row)}
                      >
                        <span className='font-mono text-sm font-medium group-hover:underline'>
                          {row.host}
                        </span>
                        {brandName && (
                          <span className='text-muted-foreground text-xs'>
                            {brandName}
                          </span>
                        )}
                      </button>
                    </TableCell>
                    <TableCell>
                      {formatUserIdentity(
                        row.owner_user_id,
                        row.owner_username
                      )}
                    </TableCell>
                    <TableCell className='whitespace-normal'>
                      <div className='flex flex-col gap-0.5'>
                        <SiteStatusBadge status={row.status} />
                        {row.close_requested_at > 0 && (
                          <span className='text-muted-foreground max-w-[108px] text-[11px] leading-tight'>
                            {t('Close requested')}:{' '}
                            {formatTimestampToDate(row.close_requested_at)}
                          </span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {row.effective_cost_factor ??
                        row.cost_factor ??
                        row.tier_cost_factor ??
                        t('Follow site level')}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatNumber(row.customers, locale)}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {row.month_topup_usd ?? '0'}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {row.month_revenue_usd ?? '0'}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {row.month_earning_usd ?? '0'}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {row.growth_pct == null ? '-' : `${row.growth_pct}%`}
                    </TableCell>
                    <TableCell className='max-w-[92px] text-xs leading-tight whitespace-normal tabular-nums'>
                      {formatTimestampToDate(row.created_at)}
                    </TableCell>
                    <TableCell className='text-right'>
                      <div className='flex items-center justify-end gap-1'>
                        <Button
                          type='button'
                          size='sm'
                          variant='outline'
                          className='h-7 gap-1 px-2 text-xs'
                          onClick={() => props.onOpenSite(row)}
                        >
                          <Settings2 className='size-3.5' aria-hidden />
                          {t('Manage')}
                        </Button>
                        {isRoot && (
                          <Button
                            type='button'
                            size='sm'
                            variant='destructive'
                            className='h-7 gap-1 px-2 text-xs'
                            onClick={() => {
                              setDeleteReason('')
                              setDeletingSite(row)
                            }}
                          >
                            <Trash2 className='size-3.5' aria-hidden />
                            {t('Delete')}
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      <PageSwitcher
        page={page}
        total={query.data?.total ?? 0}
        pageSize={SITE_ADMIN_PAGE_SIZE}
        onPageChange={setPage}
      />

      {isRoot && (
        <ConfirmDialog
          open={deletingSite !== null}
          onOpenChange={(open) => {
            if (!open && !removeMutation.isPending) {
              setDeletingSite(null)
              setDeleteReason('')
            }
          }}
          title={t('Delete {{site}}', {
            site: deletingSite?.host ?? '',
          })}
          desc={t(
            'Zero-data sites will be permanently deleted, and closed sites will be archived and hidden from the list. Active sites with customers or financial records must be closed first.'
          )}
          confirmText={t('Delete site')}
          destructive
          isLoading={removeMutation.isPending}
          handleConfirm={() =>
            deletingSite && removeMutation.mutate(deletingSite)
          }
        >
          <div className='space-y-1.5 pt-2'>
            <Label htmlFor={`${fieldId}-delete-reason`}>
              {t('Reason (optional)')}
            </Label>
            <Textarea
              id={`${fieldId}-delete-reason`}
              rows={2}
              value={deleteReason}
              placeholder={t('Optional audit note for deleting this site')}
              onChange={(event) => setDeleteReason(event.target.value)}
            />
          </div>
        </ConfirmDialog>
      )}
    </div>
  )
}
