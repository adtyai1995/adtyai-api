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

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestamp } from '@/lib/format'

import {
  getOwnerPrincipalTransfers,
  getOwnerSelfUseUsage,
  OWNER_PAGE_SIZE,
  siteOwnerKeys,
} from '../api'

export function OwnerSelfUseHistory() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [transfersPage, setTransfersPage] = useState(1)
  const [usagePage, setUsagePage] = useState(1)
  const transfers = useQuery({
    queryKey: siteOwnerKeys.list('owner-transfers', { p: transfersPage }),
    queryFn: () => getOwnerPrincipalTransfers(transfersPage),
  })
  const usage = useQuery({
    queryKey: siteOwnerKeys.list('owner-usage', { p: usagePage }),
    queryFn: () => getOwnerSelfUseUsage(usagePage),
  })
  const money = (value: string) =>
    value === '' ? '-' : formatUsd(Number(value), locale, 6)
  const unitLabels: Record<string, string> = {
    per_million_input_tokens: t('Per 1M input tokens'),
    per_call: t('Per call'),
  }
  if (transfers.isError || usage.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void transfers.refetch()
          void usage.refetch()
        }}
      />
    )
  }
  if (transfers.isPending || usage.isPending) return <LoadingState />
  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <h3 className='text-sm font-medium'>{t('Owner principal transfers')}</h3>
      <StaticDataTable
        data={transfers.data.items ?? []}
        tableProps={{ 'aria-label': t('Owner principal transfers') }}
        getRowKey={(row) => row.transfer_id}
        columns={[
          {
            id: 'id',
            header: t('Transfer ID'),
            cell: (row) => row.transfer_id,
          },
          {
            id: 'time',
            header: t('Time'),
            cell: (row) => formatTimestamp(row.created_at),
          },
          {
            id: 'amount',
            header: t('Amount'),
            cell: (row) => money(row.amount_usd),
          },
          {
            id: 'earnings',
            header: t('Earnings balance change'),
            cell: (row) =>
              `${money(row.earnings_before)} → ${money(row.earnings_after)}`,
          },
          {
            id: 'personal',
            header: t('Personal balance change'),
            cell: (row) =>
              `${money(row.personal_before)} → ${money(row.personal_after)}`,
          },
        ]}
      />
      <PageSwitcher
        page={transfersPage}
        total={transfers.data.total}
        pageSize={OWNER_PAGE_SIZE}
        onPageChange={setTransfersPage}
      />
      <h3 className='text-sm font-medium'>{t('Owner API usage')}</h3>
      <StaticDataTable
        data={usage.data.items ?? []}
        tableProps={{ 'aria-label': t('Owner API usage') }}
        getRowKey={(row) => row.operation_key}
        columns={[
          {
            id: 'request',
            header: t('Request ID'),
            cell: (row) => row.request_id,
          },
          {
            id: 'time',
            header: t('Time'),
            cell: (row) => formatTimestamp(row.created_at),
          },
          {
            id: 'model',
            header: t('Model'),
            cell: (row) => row.model_name || '-',
          },
          {
            id: 'group',
            header: t('Billing group'),
            cell: (row) => row.billing_group || '-',
          },
          {
            id: 'usage',
            header: t('Usage'),
            cell: (row) =>
              row.usage_facts
                ? Object.entries(row.usage_facts)
                    .map(
                      ([name, value]) =>
                        `${name}: ${formatNumber(value, locale)}`
                    )
                    .join(', ')
                : `${formatNumber(row.prompt_tokens, locale)} / ${formatNumber(row.completion_tokens, locale)}`,
          },
          {
            id: 'price',
            header: t('Supply unit price'),
            cell: (row) =>
              row.billing_unit === 'tiered_expr' ? (
                t('Tiered pricing')
              ) : (
                <span>
                  {money(row.supply_unit_price)}
                  <br />
                  {unitLabels[row.billing_unit] ?? '-'}
                </span>
              ),
          },
          {
            id: 'charged',
            header: t('Actual debit'),
            cell: (row) => money(row.charged_usd),
          },
        ]}
      />
      <PageSwitcher
        page={usagePage}
        total={usage.data.total}
        pageSize={OWNER_PAGE_SIZE}
        onPageChange={setUsagePage}
      />
    </div>
  )
}
