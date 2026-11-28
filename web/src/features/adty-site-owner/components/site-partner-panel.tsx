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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatNumber,
  formatTimestamp,
  formatTimestampToDate,
} from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  getOwnerPartnerEarnings,
  getOwnerPartnerOverview,
  getOwnerPartnerSites,
  OWNER_PAGE_SIZE,
  siteOwnerKeys,
} from '../api'
import { isNegativeAmount } from '../lib/site-owner'
import { OwnerStatGrid } from './stat-grid'

function PartnerSiteStatusBadge(props: { status: string }) {
  const { t } = useTranslation()
  switch (props.status) {
    case 'active':
      return (
        <Badge
          variant='outline'
          className='border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
        >
          {t('Active')}
        </Badge>
      )
    case 'closing':
      return (
        <Badge
          variant='outline'
          className='border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400'
        >
          {t('Closing')}
        </Badge>
      )
    case 'closed':
    case 'deleting':
      return <Badge variant='secondary'>{t('Closed')}</Badge>
    case 'paused':
      return <Badge variant='outline'>{t('Paused')}</Badge>
    default:
      return <Badge variant='secondary'>{props.status}</Badge>
  }
}

/** Site owner partner referral dashboard: share link, KPI summary, anonymized referred subsites and hourly earnings. */
export function SitePartnerPanel(props: { closing: boolean }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [sitesPage, setSitesPage] = useState(1)
  const [earningsPage, setEarningsPage] = useState(1)

  const overviewQuery = useQuery({
    queryKey: siteOwnerKeys.list('partner-overview', {}),
    queryFn: getOwnerPartnerOverview,
  })
  const sitesQuery = useQuery({
    queryKey: siteOwnerKeys.list('partner-sites', { page: sitesPage }),
    queryFn: () => getOwnerPartnerSites(sitesPage, OWNER_PAGE_SIZE),
  })
  const earningsQuery = useQuery({
    queryKey: siteOwnerKeys.list('partner-earnings', { page: earningsPage }),
    queryFn: () => getOwnerPartnerEarnings(earningsPage, OWNER_PAGE_SIZE),
  })

  const money = (value: string | undefined) =>
    value == null || value === '' ? '—' : formatUsd(Number(value), locale)
  const amountCell = (value: string | undefined) => (
    <span
      data-negative={isNegativeAmount(value) ? 'true' : undefined}
      className={cn(
        'tabular-nums',
        isNegativeAmount(value) && 'text-destructive'
      )}
    >
      {money(value)}
    </span>
  )

  if (overviewQuery.isError && !overviewQuery.data) {
    return <ErrorState onRetry={() => void overviewQuery.refetch()} />
  }
  if (overviewQuery.isPending) {
    return <LoadingState />
  }

  const overview = overviewQuery.data
  const linkPaused = props.closing || !overview.link_active
  let shareUrl = ''
  if (overview.share_url) {
    shareUrl =
      overview.share_url.startsWith('/') && typeof window !== 'undefined'
        ? `${window.location.origin}${overview.share_url}`
        : overview.share_url
  }
  const referredSites = sitesQuery.data?.items ?? []
  const earningRows = earningsQuery.data?.items ?? []

  return (
    <div className='flex flex-col gap-6'>
      {linkPaused && (
        <Alert>
          <AlertDescription>
            {t(
              'Your site is closing. Your partner referral link is paused for new applications, while existing referred sites continue accruing until formal closure.'
            )}
          </AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader>
          <CardTitle className='text-base font-semibold'>
            {t('Partner referral link')}
          </CardTitle>
          <CardDescription>
            {t(
              "Earn {{rate}}% of referred subsites' net platform supply cost (personal + team consumption), settled hourly on the hour directly into your Earnings Wallet with no freeze, permanent while both sites remain open.",
              { rate: overview.default_rate_percent }
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-4 sm:grid-cols-2'>
          <div className='flex flex-col gap-1.5'>
            <span className='text-muted-foreground text-xs'>
              {t('Partner code')}
            </span>
            <div className='bg-muted/40 flex items-center justify-between gap-2 rounded-md border px-3 py-2'>
              <span className='font-mono text-sm font-medium break-all'>
                {overview.partner_code || '—'}
              </span>
              {overview.partner_code && (
                <CopyButton
                  value={overview.partner_code}
                  size='sm'
                  variant='outline'
                  aria-label={t('Copy partner code')}
                />
              )}
            </div>
          </div>

          <div className='flex flex-col gap-1.5'>
            <span className='text-muted-foreground text-xs'>
              {t('Share link')}
            </span>
            <div className='bg-muted/40 flex items-center justify-between gap-2 rounded-md border px-3 py-2'>
              <span className='truncate font-mono text-xs'>
                {shareUrl || '—'}
              </span>
              {shareUrl && (
                <CopyButton
                  value={shareUrl}
                  size='sm'
                  variant='outline'
                  aria-label={t('Copy link')}
                />
              )}
            </div>
          </div>
        </CardContent>
      </Card>

      <OwnerStatGrid
        label={t('Partner Referral')}
        items={[
          {
            label: t('Referred subsites'),
            value: formatNumber(overview.referred_count, locale),
          },
          {
            label: t('Current hour estimate'),
            value: money(overview.pending_hour_usd),
            negative: isNegativeAmount(overview.pending_hour_usd),
          },
          {
            label: t('Total partner earnings'),
            value: money(overview.total_earned_usd),
            negative: isNegativeAmount(overview.total_earned_usd),
          },
        ]}
      />

      <section className='flex flex-col gap-3'>
        <div className='flex flex-col gap-0.5'>
          <h3 className='text-[15px] leading-snug font-semibold tracking-tight'>
            {t('Referred subsites')}
          </h3>
          <p className='text-dim text-[12.5px]'>
            {t(
              "Referred subsites are anonymized to protect each operator's private business details."
            )}
          </p>
        </div>
        {sitesQuery.isError && (
          <ErrorState onRetry={() => void sitesQuery.refetch()} />
        )}
        {!sitesQuery.isError && sitesQuery.isPending && <LoadingState />}
        {!sitesQuery.isError && !sitesQuery.isPending && (
          <>
            <StaticDataTable
              tableClassName='min-w-[720px]'
              tableProps={{ 'aria-label': t('Referred subsites') }}
              data={referredSites}
              getRowKey={(row) => row.id}
              emptyContent={
                <EmptyState
                  className='min-h-28'
                  title={t('No referred subsites yet')}
                />
              }
              columns={[
                {
                  id: 'site',
                  header: t('Referred site'),
                  cell: (row) => (
                    <span className='font-medium'>{row.masked_name}</span>
                  ),
                },
                {
                  id: 'status',
                  header: t('Status'),
                  cell: (row) => <PartnerSiteStatusBadge status={row.status} />,
                },
                {
                  id: 'rate',
                  header: t('Commission rate'),
                  cell: (row) => (
                    <span className='tabular-nums'>{row.rate_percent}%</span>
                  ),
                },
                {
                  id: 'opened',
                  header: t('Opened'),
                  cell: (row) => (
                    <span className='tabular-nums'>
                      {row.opened_at > 0
                        ? formatTimestampToDate(row.opened_at)
                        : '—'}
                    </span>
                  ),
                },
                {
                  id: 'payout-status',
                  header: t('Payout status'),
                  cell: (row) =>
                    row.stopped_at > 0 ? (
                      <span className='text-muted-foreground text-xs tabular-nums'>
                        {t('Stopped')} · {formatTimestampToDate(row.stopped_at)}
                      </span>
                    ) : (
                      <Badge
                        variant='outline'
                        className='border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
                      >
                        {t('Accruing')}
                      </Badge>
                    ),
                },
                {
                  id: 'pending-hour',
                  header: t('Current hour estimate'),
                  cell: (row) => amountCell(row.pending_hour_usd),
                },
                {
                  id: 'total-earned',
                  header: t('Total earned'),
                  cell: (row) => amountCell(row.total_earned_usd),
                },
              ]}
            />
            <PageSwitcher
              page={sitesPage}
              total={sitesQuery.data?.total ?? 0}
              pageSize={OWNER_PAGE_SIZE}
              onPageChange={setSitesPage}
            />
          </>
        )}
      </section>

      <section className='flex flex-col gap-3'>
        <div className='flex flex-col gap-0.5'>
          <h3 className='text-[15px] leading-snug font-semibold tracking-tight'>
            {t('Hourly partner commission records')}
          </h3>
          <p className='text-dim text-[12.5px]'>
            {t(
              'Settled hourly on the hour directly into your Earnings Wallet with no freeze.'
            )}
          </p>
        </div>
        {earningsQuery.isError && (
          <ErrorState onRetry={() => void earningsQuery.refetch()} />
        )}
        {!earningsQuery.isError && earningsQuery.isPending && <LoadingState />}
        {!earningsQuery.isError && !earningsQuery.isPending && (
          <>
            <StaticDataTable
              tableClassName='min-w-[840px]'
              tableProps={{
                'aria-label': t('Hourly partner commission records'),
              }}
              data={earningRows}
              getRowKey={(row) => row.id}
              emptyContent={
                <EmptyState
                  className='min-h-28'
                  title={t('No partner commission records yet')}
                />
              }
              columns={[
                {
                  id: 'hour-slot',
                  header: t('Hour slot'),
                  cell: (row) => (
                    <span className='font-mono text-xs'>{row.hour_slot}</span>
                  ),
                },
                {
                  id: 'site',
                  header: t('Referred site'),
                  cell: (row) => (
                    <span className='font-medium'>{row.masked_name}</span>
                  ),
                },
                {
                  id: 'supply-net',
                  header: t('Net supply cost'),
                  cell: (row) => amountCell(row.supply_net_usd),
                },
                {
                  id: 'rate',
                  header: t('Rate'),
                  cell: (row) => (
                    <span className='tabular-nums'>{row.rate_percent}%</span>
                  ),
                },
                {
                  id: 'gross',
                  header: t('Gross commission'),
                  cell: (row) => amountCell(row.gross_commission_usd),
                },
                {
                  id: 'offset',
                  header: t('Offset applied'),
                  cell: (row) => amountCell(row.offset_applied_usd),
                },
                {
                  id: 'credited',
                  header: t('Credited'),
                  cell: (row) => amountCell(row.credited_usd),
                },
                {
                  id: 'settled-at',
                  header: t('Settled at'),
                  cell: (row) => (
                    <span className='text-xs tabular-nums'>
                      {row.settled_at > 0
                        ? formatTimestamp(row.settled_at)
                        : '—'}
                    </span>
                  ),
                },
              ]}
            />
            <PageSwitcher
              page={earningsPage}
              total={earningsQuery.data?.total ?? 0}
              pageSize={OWNER_PAGE_SIZE}
              onPageChange={setEarningsPage}
            />
          </>
        )}
      </section>
    </div>
  )
}
