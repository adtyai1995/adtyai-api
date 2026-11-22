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
import { skipToken, useQuery } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import {
  getOwnerSettlementSheets,
  getOwnerSettlementDetail,
  type SettlementPage,
  siteOwnerKeys,
  type SettlementSheet,
} from '../api'
import { isNegativeAmount } from '../lib/site-owner'

type Line = {
  id: string
  label: string
  value: string
  raw?: string
}

function settlementSheetLines(
  t: TFunction,
  sheet: SettlementSheet,
  locale: string | undefined
): Line[] {
  const money = (value: string) => formatUsd(Number(value || 0), locale)
  const usd = (id: string, label: string, raw: string): Line => ({
    id,
    label,
    value: money(raw),
    raw,
  })
  const lines: Line[] = [
    { id: 'day', label: t('Date'), value: sheet.business_day },
    { id: 'zone', label: t('Timezone'), value: sheet.timezone },
    {
      id: 'start',
      label: t('Day start'),
      value: formatTimestamp(sheet.day_start),
    },
    { id: 'end', label: t('Day end'), value: formatTimestamp(sheet.day_end) },
    usd('topup', t('Top-up'), sheet.topup_face_usd),
    usd('paid', t('Paid in USD'), sheet.paid_usd),
  ]
  let paid: Record<string, string> = {}
  try {
    paid = JSON.parse(sheet.paid_by_currency || '{}') as Record<string, string>
  } catch {
    paid = {}
  }
  for (const [currency, amount] of Object.entries(paid)) {
    lines.push({
      id: `paid-${currency || 'other'}`,
      label: t('Other paid amounts'),
      value: `${amount} ${currency}`.trim(),
    })
  }
  lines.push(
    usd('consumption', t('Customer consumption'), sheet.consumption_usd),
    usd(
      'owner-topup',
      t('Owner self-use top-ups'),
      sheet.owner_self_use_topup_usd ?? '0'
    ),
    usd(
      'owner-voucher',
      t('Owner self-use vouchers'),
      sheet.owner_self_use_voucher_usd ?? '0'
    ),
    usd(
      'owner-refund',
      t('Owner self-use refunds'),
      sheet.owner_self_use_refund_usd ?? '0'
    ),
    usd(
      'owner-transfer',
      t('Transfer to personal balance'),
      sheet.owner_transfer_usd ?? '0'
    ),
    usd('owner-usage', t('Owner API usage'), sheet.owner_self_use_usd ?? '0'),
    usd(
      'owner-cost',
      t('Owner API supply cost'),
      sheet.owner_self_use_cost_usd ?? '0'
    ),
    usd('cost', t('Supply cost'), sheet.supply_cost_usd),
    usd('spread', t('Settlement spread'), sheet.spread_usd),
    usd('reserve', t('Gift reserve'), sheet.gift_reserve_usd),
    usd('funding', t('Gift funding'), sheet.gift_funding_usd),
    usd('release', t('Gift release'), sheet.gift_release_usd),
    usd('trial', t('Trial expense'), sheet.trial_expense_usd),
    usd('commission', t('Commission'), sheet.commission_usd),
    sheet.pending_coverage_unverified
      ? {
          id: 'pending',
          label: t('Pending coverage'),
          value: t('Historical amount requires verification'),
        }
      : usd('pending', t('Pending coverage'), sheet.pending_coverage_usd),
    usd('margin', t('Margin change'), sheet.margin_delta_usd),
    usd('earnings', t('Earnings change'), sheet.earnings_delta_usd),
    usd('available', t('Available change'), sheet.earnings_available_delta_usd),
    usd('frozen', t('Frozen change'), sheet.earnings_frozen_delta_usd),
    usd('withdrawal', t('Withdrawal amount'), sheet.withdrawal_usd),
    usd('fee', t('Withdrawal fee'), sheet.withdrawal_fee_usd),
    usd('adjustment', t('Financial adjustment'), sheet.adjustment_usd),
    {
      id: 'anomalies',
      label: t('Anomalies'),
      value: String(sheet.anomaly_count),
    },
    { id: 'batch', label: t('Batch'), value: sheet.batch_id },
    {
      id: 'watermark',
      label: t('Event watermark'),
      value: String(sheet.event_watermark),
    }
  )
  return lines
}

export function SettlementSheetLines({ sheet }: { sheet: SettlementSheet }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [selected, setSelected] = useState('latest')
  const revisions = sheet.revisions ?? []
  const revision =
    selected === 'latest'
      ? revisions[revisions.length - 1]
      : revisions.find((r) => String(r.id) === selected)
  const generatedAt = revision?.created_at || sheet.generated_at
  const lines = settlementSheetLines(t, revision?.after ?? sheet, locale)
  return (
    <>
      {revisions.length > 0 && (
        <label>
          {t('Statement version')}{' '}
          <select
            value={selected}
            onChange={(e) => setSelected(e.target.value)}
          >
            <option value='latest'>{t('Latest')}</option>
            <option value='original'>
              {sheet.legacy_coverage?.length
                ? t('Recorded statement')
                : t('Original')}
            </option>
            {revisions.map((r) => (
              <option key={r.id} value={r.id}>
                #{r.id} · {formatTimestamp(r.created_at)}
              </option>
            ))}
          </select>
        </label>
      )}
      {generatedAt ? (
        <p>
          {t('Generated at')} ·{' '}
          {formatTimestamp(generatedAt)}
        </p>
      ) : null}
      {!!sheet.legacy_coverage?.length && (
        <details>
          <summary>{t('Historical coverage adjustments')}</summary>
          <ul>
            {sheet.legacy_coverage.map((a) => (
              <li key={a.id}>
                {formatTimestamp(a.created_at)} · {t('Pending coverage')} USD{' '}
                {a.before_usd} → {a.after_usd}
              </li>
            ))}
          </ul>
        </details>
      )}
      {revision && (
        <details>
          <summary>{t('Source records')}</summary>
          {revision.sources.join(', ')}
        </details>
      )}
      <dl className='grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-2 text-sm'>
        {lines.map((line) => (
          <div key={line.id} className='contents'>
            <dt className='text-muted-foreground'>{line.label}</dt>
            <dd
              className={cn(
                'text-right tabular-nums',
                isNegativeAmount(line.raw) && 'text-destructive'
              )}
              data-negative={isNegativeAmount(line.raw) ? 'true' : undefined}
            >
              {line.value}
            </dd>
          </div>
        ))}
      </dl>
    </>
  )
}

/** Previous-day check sheet. It lists recorded lines and does not credit earnings. */
export function SettlementSheetPanel({ siteId }: { siteId?: number }) {
  return (
    <SettlementBrowser
      scope={siteOwnerKeys.list('settlement-sheets', { siteId })}
      siteId={siteId}
      list={getOwnerSettlementSheets}
      detail={getOwnerSettlementDetail}
    />
  )
}

type BrowserProps = {
  scope: readonly unknown[]
  siteId?: number
  admin?: boolean
  list: (page: number, day: string, siteId?: number) => Promise<SettlementPage>
  detail: (id: number, siteId?: number) => Promise<SettlementSheet>
}

export function SettlementBrowser({
  scope,
  siteId,
  admin,
  list,
  detail,
}: BrowserProps) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [day, setDay] = useState('')
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState(0)
  const site = admin ? (filter ? Number(filter) : undefined) : siteId
  const query = useQuery({
    queryKey: [...scope, 'page', site, page, day],
    queryFn: () => list(page, day, site),
  })
  const rows = query.data?.items ?? []
  const current = rows.find((row) => row.id === selected) ?? rows[0]
  const item = useQuery({
    queryKey: [...scope, 'detail', site, current?.id],
    queryFn: current ? () => detail(current.id, site) : skipToken,
  })
  return (
    <section className='flex flex-col gap-3'>
      <h2 className='text-sm font-medium'>{t('Settlement sheet')}</h2>
      <div className='flex flex-wrap gap-2'>
        <label>
          {t('Date')}
          <Input
            type='date'
            value={day}
            onChange={(e) => {
              setDay(e.target.value)
              setPage(1)
            }}
          />
        </label>
        {admin && (
          <label>
            {t('Site ID')}
            <Input
              type='number'
              min={1}
              value={filter}
              onChange={(e) => {
                setFilter(e.target.value)
                setPage(1)
              }}
            />
          </label>
        )}
      </div>
      {query.isError && (
        <ErrorState
          description={getServerErrorMessage(query.error)}
          onRetry={() => void query.refetch()}
        />
      )}
      {query.isSuccess && rows.length === 0 && (
        <EmptyState className='min-h-24' title={t('No settlement sheet yet')} />
      )}
      {current && (
        <select
          aria-label={t('Settlement sheet')}
          value={current.id}
          onChange={(e) => setSelected(Number(e.target.value))}
        >
          {rows.map((row) => (
            <option key={row.id} value={row.id}>
              {admin ? t('Site ID') + ' ' + row.site_id + ' · ' : ''}
              {row.business_day}
            </option>
          ))}
        </select>
      )}
      {item.isError && (
        <ErrorState
          description={getServerErrorMessage(item.error)}
          onRetry={() => void item.refetch()}
        />
      )}
      {current && item.data && (
        <SettlementSheetLines key={current.id} sheet={item.data} />
      )}
      {((query.data?.total ?? 0) > 20 || page > 1) && (
        <div className='flex gap-2'>
          <Button
            variant='outline'
            disabled={page === 1}
            onClick={() => setPage(page - 1)}
          >
            {t('Previous page')}
          </Button>
          <Button
            variant='outline'
            disabled={page * 20 >= (query.data?.total ?? 0)}
            onClick={() => setPage(page + 1)}
          >
            {t('Next page')}
          </Button>
        </div>
      )}
    </section>
  )
}
