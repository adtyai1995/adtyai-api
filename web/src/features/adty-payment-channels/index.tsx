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
import { Plus } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  deleteEpayChannel,
  epayChannelsQueryKey,
  getEpayChannels,
  revokeEpayChannel,
  setEpayChannelStatus,
  testEpayChannel,
  type EpayChannel,
} from './api'
import { ChannelDrawer } from './components/channel-drawer'

const STATUS_VARIANT = {
  enabled: 'success',
  maintenance: 'warning',
  disabled: 'neutral',
  revoked: 'danger',
} as const

function paymentMethodLabel(
  method: string,
  t: (key: string) => string
): string {
  if (method === 'alipay') return t('Alipay')
  if (method === 'wxpay') return t('WeChat Pay')
  return method
}

function paymentReturnTypeLabel(
  returnType: string,
  t: (key: string) => string
): string {
  switch (returnType) {
    case 'qrcode':
      return t('QR code')
    case 'payurl':
      return t('Payment URL')
    case 'urlscheme':
      return t('App scheme')
    default:
      return returnType
  }
}

function paymentOrderModeLabel(
  orderMode: string,
  t: (key: string) => string
): string {
  if (orderMode === 'api') return t('API order')
  if (orderMode === 'submit') return t('Form submit')
  return orderMode
}

function paymentChannelStatusLabel(
  status: EpayChannel['status'],
  t: (key: string) => string
): string {
  switch (status) {
    case 'enabled':
      return t('Enabled')
    case 'maintenance':
      return t('Maintenance')
    case 'disabled':
      return t('Disabled')
    case 'revoked':
      return t('Revoked')
    default:
      return status
  }
}

function paymentStatusReasonLabel(
  reason: string,
  t: (key: string) => string
): string {
  switch (reason) {
    case 'manual':
      return t('Manual')
    case 'unpaid':
      return t('Consecutive unpaid orders')
    case 'probe':
      return t('Health probe failed')
    default:
      return reason
  }
}

/** Alipay / WeChat channels: status, limits, today's amount, tests and switches. */
export function PaymentChannels(props: { embedded?: boolean } = {}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const isRoot = user?.role === ROLE.SUPER_ADMIN
  const canOperate = canAdmin(user, ADTY_PERMISSIONS.PAYMENT_OPERATE)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [editing, setEditing] = useState<EpayChannel | null>(null)
  const [deleting, setDeleting] = useState<EpayChannel | null>(null)
  const [revoking, setRevoking] = useState<EpayChannel | null>(null)
  const query = useQuery({
    queryKey: epayChannelsQueryKey,
    queryFn: getEpayChannels,
  })
  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: epayChannelsQueryKey })

  const status = useMutation({
    mutationFn: (args: { id: number; status: EpayChannel['status'] }) =>
      setEpayChannelStatus(args.id, args.status),
    onSuccess: () => void refresh(),
    onError: (error) => handleServerError(error),
  })
  const test = useMutation({
    mutationFn: (id: number) => testEpayChannel(id),
    onSuccess: (results) => {
      if (results.length === 0) {
        toast.info(t('No enabled payment methods to test on this channel'))
      }
      for (const result of results) {
        const label = paymentMethodLabel(result.method, t)
        if (result.ok) {
          toast.success(
            t('{{method}}: returns {{type}}', {
              method: label,
              type: paymentReturnTypeLabel(result.return_type, t),
            })
          )
          continue
        }
        if (result.disabled) {
          toast.warning(
            t('{{method}} is not open on this channel and was turned off', {
              method: label,
            })
          )
          continue
        }
        toast.error(`${label}: ${result.message}`)
      }
      void refresh()
    },
    onError: (error) => handleServerError(error),
  })
  const remove = useMutation({
    mutationFn: (id: number) => deleteEpayChannel(id),
    onSuccess: () => {
      setDeleting(null)
      void refresh()
    },
    onError: (error) => handleServerError(error),
  })
  const revoke = useMutation({
    mutationFn: (id: number) => revokeEpayChannel(id),
    onSuccess: () => {
      setRevoking(null)
      void refresh()
    },
    onError: (error) => handleServerError(error),
  })

  const addChannelButton = isRoot ? (
    <Button
      type='button'
      size='sm'
      variant='brand'
      onClick={() => {
        setEditing(null)
        setDrawerOpen(true)
      }}
    >
      <Plus className='size-4' aria-hidden='true' />
      {t('Add payment channel')}
    </Button>
  ) : null

  let tableContent: ReactNode
  if (query.isLoading) {
    tableContent = (
      <div className='text-muted-foreground flex min-h-32 items-center justify-center text-sm'>
        {t('Loading payment channels...')}
      </div>
    )
  } else if (query.isError) {
    tableContent = (
      <ErrorState
        className='min-h-32'
        title={t('Failed to load payment channels')}
        onRetry={() => void query.refetch()}
      />
    )
  } else {
    tableContent = (
      <StaticDataTable
        tableClassName='min-w-[820px] text-[13px] [&_td]:px-2 [&_th]:px-2'
        tableProps={{ 'aria-label': t('Payment channels') }}
        data={query.data ?? []}
        getRowKey={(row) => String(row.id)}
        emptyContent={
          <EmptyState
            className='min-h-32'
            title={t('No payment channels yet')}
          />
        }
        columns={[
          {
            id: 'name',
            header: t('Name'),
            cellClassName: 'font-medium',
            cell: (row) => row.name,
          },
          {
            id: 'sign_type',
            header: t('Signature type'),
            cell: (row) => {
              const signType = row.sign_type === 'RSA' ? 'RSA' : 'MD5'
              return (
                <StatusBadge
                  label={signType}
                  variant={signType === 'RSA' ? 'info' : 'neutral'}
                  copyable={false}
                />
              )
            },
          },
          {
            id: 'methods',
            header: t('Methods'),
            cellClassName: 'whitespace-normal',
            cell: (row) => {
              const enabledMethods = row.methods.filter((m) => m.enabled)
              if (enabledMethods.length === 0) return '-'
              return (
                <div className='flex flex-col gap-0.5 text-xs leading-snug'>
                  {enabledMethods.map((m) => (
                    <span key={m.method} className='whitespace-nowrap'>
                      {paymentMethodLabel(m.method, t)}
                      {m.return_type
                        ? ` (${paymentReturnTypeLabel(m.return_type, t)})`
                        : ''}
                    </span>
                  ))}
                </div>
              )
            },
          },
          {
            id: 'priority',
            header: t('Priority / weight'),
            cellClassName: 'tabular-nums',
            cell: (row) => `${row.priority} / ${row.weight}`,
          },
          {
            id: 'mode',
            header: t('Order mode'),
            cell: (row) => paymentOrderModeLabel(row.order_mode, t),
          },
          {
            id: 'status',
            header: t('Status'),
            cellClassName: 'whitespace-normal',
            cell: (row) => (
              <div className='flex flex-col gap-0.5'>
                <StatusBadge
                  label={paymentChannelStatusLabel(row.status, t)}
                  variant={STATUS_VARIANT[row.status]}
                  copyable={false}
                />
                {row.credential_decrypt_error && (
                  <StatusBadge
                    label={t('Credential decryption failed')}
                    variant='danger'
                    copyable={false}
                  />
                )}
                {row.status_reason && (
                  <span className='text-muted-foreground max-w-[120px] text-[11px] leading-tight'>
                    {paymentStatusReasonLabel(row.status_reason, t)}
                  </span>
                )}
              </div>
            ),
          },
          {
            id: 'unpaid',
            header: t('Unpaid in a row'),
            cellClassName: 'tabular-nums',
            cell: (row) => row.unpaid_streak,
          },
          {
            id: 'today',
            header: t('Today'),
            cellClassName: 'whitespace-normal tabular-nums',
            cell: (row) => {
              const entries = Object.entries(row.today_cny)
              if (entries.length === 0) return '-'
              return (
                <div className='flex flex-col gap-0.5 text-xs leading-snug'>
                  {entries.map(([method, sum]) => (
                    <span key={method} className='whitespace-nowrap'>
                      {paymentMethodLabel(method, t)} ¥{sum}
                    </span>
                  ))}
                </div>
              )
            },
          },
          {
            id: 'actions',
            header: t('Actions'),
            className: 'text-right',
            cellClassName: 'text-right',
            cell: (row) => (
              <div className='flex items-center justify-end gap-1 whitespace-nowrap'>
                {canOperate &&
                  row.status !== 'revoked' &&
                  row.order_mode === 'api' && (
                    <Button
                      size='sm'
                      variant='outline'
                      className='h-7 px-2 text-xs'
                      disabled={test.isPending}
                      onClick={() => test.mutate(row.id)}
                    >
                      {t('Test order')}
                    </Button>
                  )}
                {canOperate && row.status !== 'revoked' && (
                  <Button
                    size='sm'
                    variant='outline'
                    className='h-7 px-2 text-xs'
                    disabled={status.isPending}
                    onClick={() =>
                      status.mutate({
                        id: row.id,
                        status:
                          row.status === 'enabled' ? 'disabled' : 'enabled',
                      })
                    }
                  >
                    {row.status === 'enabled' ? t('Disable') : t('Enable')}
                  </Button>
                )}
                {isRoot && (
                  <>
                    {row.status !== 'revoked' && (
                      <>
                        <Button
                          size='sm'
                          variant='ghost'
                          className='h-7 px-2 text-xs'
                          onClick={() => {
                            setEditing(row)
                            setDrawerOpen(true)
                          }}
                        >
                          {t('Edit')}
                        </Button>
                        <Button
                          size='sm'
                          variant='ghost'
                          className='h-7 px-2 text-xs'
                          disabled={revoke.isPending}
                          onClick={() => setRevoking(row)}
                        >
                          {t('Revoke credentials')}
                        </Button>
                      </>
                    )}
                    <Button
                      size='sm'
                      variant='ghost'
                      className='h-7 px-2 text-xs'
                      onClick={() => setDeleting(row)}
                    >
                      {t('Delete')}
                    </Button>
                  </>
                )}
              </div>
            ),
          },
        ]}
      />
    )
  }

  const dialogs = (
    <>
      {isRoot && (
        <ChannelDrawer
          open={drawerOpen}
          channel={editing}
          onOpenChange={setDrawerOpen}
          onSaved={() => void refresh()}
        />
      )}
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(open) => !open && setRevoking(null)}
        title={t('Revoke credentials')}
        desc={t(
          'Permanently revoke {{name}}? This erases its key and blocks late callbacks and automatic queries. Reconcile old payments manually with evidence. This cannot be undone.',
          { name: revoking?.name ?? '' }
        )}
        destructive
        isLoading={revoke.isPending}
        handleConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('Delete payment channel')}
        desc={t('Delete {{name}}? Channels with orders can only be disabled.', {
          name: deleting?.name ?? '',
        })}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => deleting && remove.mutate(deleting.id)}
      />
    </>
  )

  if (props.embedded) {
    return (
      <>
        <ContentSection
          title={t('Payment channels')}
          description={t(
            'Configure one or more Epay gateways (gateway URL, merchant ID, merchant key, Alipay/WeChat method settings, limits, priority and order tests).'
          )}
          actions={addChannelButton}
        >
          {tableContent}
        </ContentSection>
        {dialogs}
      </>
    )
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Payment channels')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {t(
            'Configure one or more Epay gateways (gateway URL, merchant ID, merchant key, Alipay/WeChat method settings, limits, priority and order tests).'
          )}
        </SectionPageLayout.Description>
        {addChannelButton && (
          <SectionPageLayout.Actions>
            {addChannelButton}
          </SectionPageLayout.Actions>
        )}
        <SectionPageLayout.Content>{tableContent}</SectionPageLayout.Content>
      </SectionPageLayout>
      {dialogs}
    </>
  )
}
