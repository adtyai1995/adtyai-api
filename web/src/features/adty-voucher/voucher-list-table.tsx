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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  voucherCanCancel,
  voucherCanFreeze,
  voucherSourceLabel,
  voucherStatusLabel,
} from '@/features/adty-ledger/voucher-labels'
import { formatTimestamp } from '@/lib/format'

import type { VoucherListItem } from './api'

type PendingAction = { kind: 'freeze' | 'cancel'; voucher: VoucherListItem }

function statusVariant(status: string) {
  if (status === 'redeemed') return 'success' as const
  if (status === 'frozen') return 'warning' as const
  if (status === 'cancelled') return 'danger' as const
  return 'info' as const
}

export function VoucherListTable(props: {
  label: string
  rows: VoucherListItem[]
  busy: boolean
  onFreeze: (voucher: VoucherListItem) => void
  onCancel: (voucher: VoucherListItem) => void
  showSource?: boolean
  showTargetSite?: boolean
  formatTargetSite?: (siteId: number) => string
}) {
  const { t } = useTranslation()
  const [pending, setPending] = useState<PendingAction | null>(null)

  const columns: StaticDataTableColumn<VoucherListItem>[] = [
    {
      id: 'code',
      header: t('Voucher code'),
      cell: (row) => (
        <code className='font-mono text-xs'>…{row.code_tail}</code>
      ),
    },
    {
      id: 'batch_no',
      header: t('Batch number'),
      cell: (row) =>
        row.batch_no ? (
          <code className='text-muted-foreground font-mono text-xs'>
            {row.batch_no}
          </code>
        ) : (
          <span className='text-muted-foreground'>—</span>
        ),
    },
  ]

  if (props.showTargetSite) {
    columns.push({
      id: 'target_site',
      header: t('Target site'),
      cell: (row) => {
        if (!row.target_site_id || row.target_site_id <= 0) return '—'
        return props.formatTargetSite
          ? props.formatTargetSite(row.target_site_id)
          : `#${row.target_site_id}`
      },
    })
  }

  columns.push({
    id: 'amount',
    header: t('Amount (USD)'),
    cellClassName: 'tabular-nums font-medium',
    cell: (row) => `$${row.amount_usd}`,
  })

  if (props.showSource) {
    columns.push({
      id: 'source',
      header: t('Funded from'),
      cell: (row) => (
        <div className='flex flex-col gap-0.5'>
          <span>{voucherSourceLabel(t, row.source_kind)}</span>
          {row.source_kind === 'mixed' &&
            (row.frozen_earnings_usd || row.frozen_margin_usd) && (
              <span className='text-muted-foreground text-xs tabular-nums'>
                {t('Earnings ${{earnings}} + Margin ${{margin}}', {
                  earnings: row.frozen_earnings_usd ?? '0',
                  margin: row.frozen_margin_usd ?? '0',
                })}
              </span>
            )}
        </div>
      ),
    })
  }

  columns.push(
    {
      id: 'status',
      header: t('Status'),
      cell: (row) => (
        <StatusBadge
          label={voucherStatusLabel(t, row.status)}
          variant={statusVariant(row.status)}
          copyable={false}
        />
      ),
    },
    {
      id: 'reason',
      header: t('Reason'),
      cell: (row) =>
        row.reason ? (
          <span className='block max-w-[220px] truncate' title={row.reason}>
            {row.reason}
          </span>
        ) : (
          <span className='text-muted-foreground'>—</span>
        ),
    },
    {
      id: 'created_at',
      header: t('Created at'),
      cellClassName: 'tabular-nums text-xs text-muted-foreground',
      cell: (row) => formatTimestamp(row.created_at),
    },
    {
      id: 'redeemed_at',
      header: t('Redeemed at'),
      cellClassName: 'tabular-nums text-xs text-muted-foreground',
      cell: (row) =>
        row.redeemed_at > 0
          ? `${row.redeemer_id > 0 ? `#${row.redeemer_id} · ` : ''}${formatTimestamp(row.redeemed_at)}`
          : '—',
    },
    {
      id: 'actions',
      header: t('Actions'),
      className: 'text-right',
      cellClassName: 'text-right',
      cell: (row) =>
        voucherCanCancel(row) ? (
          <div className='flex justify-end gap-1.5'>
            {voucherCanFreeze(row) && (
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={props.busy}
                onClick={() => setPending({ kind: 'freeze', voucher: row })}
              >
                {t('Freeze')}
              </Button>
            )}
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={props.busy}
              onClick={() => setPending({ kind: 'cancel', voucher: row })}
            >
              {t('Cancel code')}
            </Button>
          </div>
        ) : null,
    }
  )

  return (
    <>
      <StaticDataTable
        tableClassName='min-w-[880px]'
        tableProps={{ 'aria-label': props.label }}
        data={props.rows}
        getRowKey={(row) => String(row.id)}
        emptyContent={
          <EmptyState className='min-h-24' title={t('No recharge codes')} />
        }
        columns={columns}
      />
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open) setPending(null)
        }}
        title={
          pending?.kind === 'freeze'
            ? t('Freeze this code?')
            : t('Cancel this code?')
        }
        desc={
          pending?.kind === 'freeze'
            ? t(
                'A frozen code cannot be redeemed and cannot be unfrozen. The amount stays held until the code is cancelled.'
              )
            : t(
                'The code can no longer be redeemed and its amount returns to where it was funded from.'
              )
        }
        confirmText={
          pending?.kind === 'freeze' ? t('Freeze') : t('Cancel code')
        }
        destructive
        handleConfirm={() => {
          if (!pending) return
          if (pending.kind === 'freeze') props.onFreeze(pending.voucher)
          else props.onCancel(pending.voucher)
          setPending(null)
        }}
      />
    </>
  )
}
