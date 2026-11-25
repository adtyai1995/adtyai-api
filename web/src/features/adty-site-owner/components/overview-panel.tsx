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
import { Link } from '@tanstack/react-router'
import { ArrowUpRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { useSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getOwnerEarnings, getOwnerStats, siteOwnerKeys } from '../api'
import { isNegativeAmount } from '../lib/site-owner'
import { OwnerStatGrid } from './stat-grid'

/** Overview: four earning numbers, a 30-day trend and the risk notice (task 11.6). */
export function OverviewPanel() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const stats = useQuery({
    queryKey: siteOwnerKeys.stats('', ''),
    queryFn: () => getOwnerStats('', ''),
  })
  const earnings = useQuery({
    queryKey: siteOwnerKeys.list('earnings', { p: 1, kind: '' }),
    queryFn: () => getOwnerEarnings(1, ''),
  })
  const { counts, failed } = useSidebarBadges()
  const tasks = [
    {
      key: 'tickets',
      count: counts.owner_tickets_pending ?? counts.tickets_pending ?? 0,
      section: 'tickets' as const,
      label: t('Ticket management'),
    },
    {
      key: 'commissions',
      count: counts.invite_withdrawals_pending ?? 0,
      section: 'commissions' as const,
      label: t('Invite commission withdrawal'),
    },
  ]
  const balances = earnings.data?.balances
  const total = stats.data?.total
  const money = (value: string | undefined) =>
    value == null || value === '' ? '—' : formatUsd(Number(value), locale)
  const chartConfig = {
    revenue: { label: t('Customer spend'), color: 'var(--chart-1)' },
    earning: { label: t('Your earnings'), color: 'var(--chart-2)' },
  } satisfies ChartConfig
  const series = (stats.data?.series ?? []).map((day) => ({
    day: day.day,
    revenue: Number(day.revenue_usd),
    earning: Number(day.earning_usd),
  }))
  return (
    <div className='flex flex-col gap-6'>
      <div className='flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between'>
        <section
          aria-labelledby='owner-pending-tasks'
          className='flex flex-wrap items-center gap-2'
        >
          <h2
            id='owner-pending-tasks'
            className='text-dim me-1 text-[13px] font-medium'
          >
            {t('Pending tasks')}
          </h2>
          {tasks.map((task) => {
            const count = failed ? '—' : task.count
            const due = !failed && task.count > 0
            return (
              <Button
                key={task.key}
                variant='outline'
                className='gap-2'
                render={
                  <Link
                    to='/site-owner/$section'
                    params={{ section: task.section }}
                    hash='pending'
                  />
                }
              >
                <span>{task.label}</span>
                <span
                  className={cn(
                    'min-w-5 rounded-full px-1.5 text-center text-[11.5px] leading-5 font-semibold tabular-nums',
                    due
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground'
                  )}
                >
                  {count}
                </span>
              </Button>
            )
          })}
        </section>
        <nav
          aria-label={t('Earnings')}
          className='-mx-2 flex flex-wrap items-center gap-0.5'
        >
          {(
            [
              ['earnings', t('Earnings Details')],
              ['withdrawals', t('Earnings Withdrawals')],
              ['wallets', t('Transfer earnings to margin')],
              ['settlements', t('Settlement Sheets')],
              ['commissions', t('Promotion Management')],
            ] as const
          ).map(([section, label]) => (
            <Button
              key={section}
              variant='ghost'
              className='text-muted-foreground hover:text-foreground gap-1'
              render={<Link to='/site-owner/$section' params={{ section }} />}
            >
              {label}
              <ArrowUpRight
                data-icon='inline-end'
                className='opacity-60'
                aria-hidden='true'
              />
            </Button>
          ))}
        </nav>
      </div>
      {(earnings.isError || stats.isError) && (
        <ErrorState
          onRetry={() => {
            if (earnings.isError) void earnings.refetch()
            if (stats.isError) void stats.refetch()
          }}
        />
      )}
      {earnings.isPending && <LoadingState />}
      {balances && (
        <section className='flex flex-col gap-2.5'>
          <h2 className='text-[15px] font-semibold tracking-tight'>
            {t('Earnings')}
          </h2>
          <OwnerStatGrid
            label={t('Earnings')}
            items={[
              {
                label: t('Pending settlement'),
                value: money(balances?.pending_usd),
                negative: isNegativeAmount(balances?.pending_usd),
              },
              {
                label: t('Withdrawable'),
                value: balances?.requires_review
                  ? t(
                      'Contact the platform to reconcile this historical site account'
                    )
                  : money(balances?.available_usd),
                negative: isNegativeAmount(balances?.available_usd),
              },
              { label: t('Withdrawn'), value: money(balances?.withdrawn_usd) },
              {
                label: t('Last 30 days'),
                value: money(total?.earning_usd),
                negative: isNegativeAmount(total?.earning_usd),
              },
            ]}
          />
          {Number(balances?.skipped_usd ?? 0) !== 0 && (
            <Alert>
              <AlertDescription>
                {t(
                  '{{amount}} of earnings is not counted because some customers are under review for self-use or moving from the main site.',
                  { amount: money(balances?.skipped_usd) }
                )}
              </AlertDescription>
            </Alert>
          )}
        </section>
      )}
      {stats.isPending && !earnings.isPending && <LoadingState />}
      {stats.data && (
        <section className='flex flex-col gap-2.5'>
          <h2 className='text-[15px] font-semibold tracking-tight'>
            {t('Last 30 days')}
          </h2>
          {total && (
            <OwnerStatGrid
              label={t('Last 30 days')}
              items={[
                {
                  label: t('New customers'),
                  value: formatNumber(total?.new_customers ?? 0, locale),
                },
                {
                  label: t('Paying customers'),
                  value: formatNumber(total?.paid_customers ?? 0, locale),
                },
                {
                  label: t('Customer spend'),
                  value: money(total?.revenue_usd),
                },
              ]}
            />
          )}
          {series.length === 0 ? (
            <EmptyState className='min-h-48' title={t('No data yet')} />
          ) : (
            <div className='bg-card rounded-[14px] border px-3 pt-5 pb-3 sm:px-5'>
              <ChartContainer config={chartConfig} className='h-64 w-full'>
                <AreaChart
                  data={series}
                  accessibilityLayer
                  margin={{ left: 0, right: 8 }}
                >
                  <CartesianGrid vertical={false} />
                  <XAxis
                    dataKey='day'
                    tickLine={false}
                    axisLine={false}
                    tickMargin={8}
                    minTickGap={24}
                  />
                  <YAxis tickLine={false} axisLine={false} width={48} />
                  <ChartTooltip content={<ChartTooltipContent />} />
                  <ChartLegend content={<ChartLegendContent />} />
                  <Area
                    dataKey='revenue'
                    type='monotone'
                    fill='var(--color-revenue)'
                    stroke='var(--color-revenue)'
                    strokeWidth={1.75}
                    fillOpacity={0.12}
                  />
                  <Area
                    dataKey='earning'
                    type='monotone'
                    fill='var(--color-earning)'
                    stroke='var(--color-earning)'
                    strokeWidth={1.75}
                    fillOpacity={0.22}
                  />
                </AreaChart>
              </ChartContainer>
            </div>
          )}
        </section>
      )}
    </div>
  )
}
