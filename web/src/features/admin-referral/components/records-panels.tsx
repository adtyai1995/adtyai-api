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
import type { TFunction } from 'i18next'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'

import {
  ADMIN_REFERRAL_PAGE_SIZE,
  adminReferralKeys,
  getReferralAuditLogs,
} from '../api'

function auditActionLabel(t: TFunction, action: string) {
  switch (action) {
    case 'settings_update':
      return t('Updated referral settings')
    case 'partner_approve':
      return t('Approved partner application')
    case 'partner_reject':
      return t('Rejected partner application')
    case 'withdrawal_approve':
      return t('Approved withdrawal')
    case 'withdrawal_reject':
      return t('Rejected withdrawal')
    case 'withdrawal_pay':
      return t('Marked withdrawal paid')
    case 'site_invite_withdrawal_approve':
      return t('Approved closed-site withdrawal')
    case 'site_invite_withdrawal_reject':
      return t('Rejected closed-site withdrawal')
    case 'site_invite_withdrawal_pay':
      return t('Marked closed-site withdrawal paid')
    case 'site_invite_withdrawal_fail':
      return t('Recorded failed closed-site withdrawal')
    case 'withdraw_cny_rate':
      return t('Changed withdrawal CNY rate')
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
    case 'restore_referral':
      return t('Restored referral commission')
    case 'adjust':
      return t('Adjusted commission')
    default:
      return action
  }
}

/** Shortens a stored JSON snapshot for the table; the full value stays in the title. */
function snippet(value: string) {
  const text = (value || '').trim()
  if (!text || text === 'null' || text === '{}') return ''
  return text.length > 120 ? `${text.slice(0, 120)}…` : text
}

/** Referral admin actions: withdrawal reviews, affiliate changes, rate changes. */
export function AuditLogPanel() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: adminReferralKeys.list('audit-logs', { page }),
    queryFn: () => getReferralAuditLogs({ p: page }),
    placeholderData: keepPreviousData,
  })
  const rows = Array.isArray(query.data?.items) ? query.data.items : []
  return (
    <ContentSection
      title={t('Admin activity')}
      description={t(
        'Who reviewed withdrawals, changed affiliates, adjusted commission or changed the withdrawal rate.'
      )}
    >
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.isPending ? (
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
                cell: (row) => auditActionLabel(t, row.action),
              },
              {
                id: 'admin',
                header: t('Operator'),
                cellClassName: 'tabular-nums',
                cell: (row) =>
                  row.admin_user_id
                    ? formatUserIdentity(row.admin_user_id, row.admin_username)
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
                  const before = snippet(row.old_value)
                  const after = snippet(row.new_value)
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
            pageSize={ADMIN_REFERRAL_PAGE_SIZE}
            onPageChange={setPage}
          />
        </>
      )}
    </ContentSection>
  )
}
