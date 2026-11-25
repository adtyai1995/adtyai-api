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

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  getStaffAccounts,
  getStaffAudits,
  STAFF_PAGE_SIZE,
  type StaffAccount,
} from '@/features/adty-site-staff/api'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'

import { siteOwnerKeys } from '../api'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import { StaffDialog } from './staff-dialog'

export function StaffPanel(props: { siteId: number; closing?: boolean }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [editing, setEditing] = useState<{
    mode: 'create' | 'edit' | 'password'
    staff: StaffAccount | null
  } | null>(null)
  const [confirming, setConfirming] = useState<{
    staff: StaffAccount
    action: 'status' | 'delete'
  } | null>(null)
  const list = useQuery({
    queryKey: siteOwnerKeys.list('staff', { p: page }),
    queryFn: () => getStaffAccounts(page),
  })
  const changeStatus = useVerifiedOwnerMutation<StaffAccount>({
    action: 'staff.update',
    target: (staff) => staff.id,
    staffRequest: (staff) => ({
      display_name: staff.display_name,
      role: staff.role,
      permissions: staff.permissions,
      status: staff.status === 'enabled' ? 'disabled' : 'enabled',
      expected_version: staff.version,
    }),
    onSuccess: () => setConfirming(null),
  })
  const remove = useVerifiedOwnerMutation<StaffAccount>({
    action: 'staff.delete',
    target: (staff) => staff.id,
    staffRequest: (staff) => ({ expected_version: staff.version }),
    onSuccess: () => setConfirming(null),
  })
  const roleLabels = {
    support: t('Customer support'),
    finance: t('Finance'),
    operations: t('Operations'),
    manager: t('Management'),
  }
  let confirmLabel = t('Delete')
  if (confirming?.action === 'status') {
    confirmLabel =
      confirming.staff.status === 'enabled' ? t('Disable') : t('Enable')
  }
  return (
    <div className='flex flex-col gap-8'>
      <section className='flex flex-col gap-3' aria-label={t('Staff accounts')}>
        <div className='flex flex-wrap items-center justify-between gap-x-4 gap-y-2'>
          <p className='text-dim max-w-[80ch] text-[13px] leading-relaxed'>
            {t(
              'Staff receive only the permissions selected here. Wallets, withdrawals, API keys and cross-site access are always blocked.'
            )}
          </p>
          <Button
            type='button'
            variant='brand'
            disabled={props.closing}
            onClick={() => setEditing({ mode: 'create', staff: null })}
          >
            {t('Create staff account')}
          </Button>
        </div>
        {list.isPending && <Skeleton className='h-24' />}
        {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
        {!list.isPending && !list.isError && (
          <>
            <StaticDataTable
              data={list.data.items ?? []}
              getRowKey={(staff) => staff.id}
              tableProps={{ 'aria-label': t('Staff accounts') }}
              emptyContent={<EmptyState title={t('No staff accounts')} />}
              columns={[
                {
                  id: 'name',
                  header: t('ID / Username'),
                  cell: (staff) => (
                    <div>
                      <div className='tabular-nums'>
                        {formatUserIdentity(staff.user_id, staff.username)}
                      </div>
                      {staff.display_name &&
                        staff.display_name !== staff.username && (
                          <div className='text-muted-foreground text-xs'>
                            {staff.display_name}
                          </div>
                        )}
                    </div>
                  ),
                },
                {
                  id: 'role',
                  header: t('Staff role'),
                  cell: (staff) => roleLabels[staff.role],
                },
                {
                  id: 'status',
                  header: t('Status'),
                  cell: (staff) =>
                    staff.status === 'enabled' ? t('Enabled') : t('Disabled'),
                },
                {
                  id: 'actions',
                  header: t('Actions'),
                  cell: (staff) => (
                    <div className='flex flex-wrap gap-1'>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        onClick={() => setEditing({ mode: 'edit', staff })}
                      >
                        {t('Edit')}
                      </Button>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={props.closing}
                        onClick={() => setEditing({ mode: 'password', staff })}
                      >
                        {t('Reset staff password')}
                      </Button>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={props.closing && staff.status !== 'enabled'}
                        onClick={() =>
                          setConfirming({ staff, action: 'status' })
                        }
                      >
                        {staff.status === 'enabled'
                          ? t('Disable')
                          : t('Enable')}
                      </Button>
                      <Button
                        type='button'
                        size='sm'
                        variant='destructive'
                        onClick={() =>
                          setConfirming({ staff, action: 'delete' })
                        }
                      >
                        {t('Delete')}
                      </Button>
                    </div>
                  ),
                },
              ]}
            />
            <PageSwitcher
              page={page}
              total={list.data.total}
              pageSize={STAFF_PAGE_SIZE}
              onPageChange={setPage}
            />
          </>
        )}
      </section>
      {editing && (
        <StaffDialog
          siteId={props.siteId}
          staff={editing.staff}
          closing={props.closing}
          mode={editing.mode}
          onClose={() => setEditing(null)}
        />
      )}
      <ConfirmDialog
        open={confirming !== null}
        onOpenChange={(open) => {
          if (!open) setConfirming(null)
        }}
        title={confirmLabel}
        confirmText={confirmLabel}
        destructive={
          confirming?.action === 'delete' ||
          confirming?.staff.status === 'enabled'
        }
        isLoading={changeStatus.isPending || remove.isPending}
        desc={t(
          'Existing staff sessions will stop working immediately. The staff member must sign in again after access is restored.'
        )}
        handleConfirm={() => {
          if (!confirming) return
          if (confirming.action === 'delete') remove.mutate(confirming.staff)
          else changeStatus.mutate(confirming.staff)
        }}
      />
      <StaffActivity />
    </div>
  )
}

function StaffActivity() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const audits = useQuery({
    queryKey: siteOwnerKeys.list('staff-audits', { p: page }),
    queryFn: () => getStaffAudits(page),
  })
  const actionLabels: Record<string, string> = {
    'staff.create': t('Create staff account'),
    'staff.update': t('Edit staff account'),
    'staff.password': t('Reset staff password'),
    'staff.delete': t('Delete staff account'),
    'staff.customers.read': t('View customer list'),
    'staff.customer.read': t('View customer list'),
    'staff.topups.read': t('View customer recharges'),
    'staff.usage.read': t('View customer usage'),
    'staff.orders.read': t('View customer orders'),
    'staff.customer.password.request': t('Send password reset instructions'),
    'staff.customer.disable': t('Disable customer'),
    'staff.balance_request': t('Submit balance request'),
    'staff.balance_review': t('Review balance request'),
  }
  return (
    <ContentSection title={t('Staff activity')}>
      {audits.isPending && <Skeleton className='h-24' />}
      {audits.isError && <ErrorState onRetry={() => void audits.refetch()} />}
      {audits.data && !audits.isError && (
        <>
          <StaticDataTable
            data={audits.data.items ?? []}
            getRowKey={(audit) => audit.id}
            tableProps={{ 'aria-label': t('Staff activity') }}
            emptyContent={<EmptyState title={t('No activity yet')} />}
            columns={[
              {
                id: 'when',
                header: t('Time'),
                cell: (audit) => formatTimestamp(audit.created_at),
              },
              {
                id: 'operator',
                header: t('Operator'),
                cell: (audit) =>
                  audit.operator_id > 0 ? (
                    <span className='tabular-nums'>
                      {formatUserIdentity(
                        audit.operator_id,
                        audit.operator_username
                      )}
                    </span>
                  ) : (
                    '—'
                  ),
              },
              {
                id: 'action',
                header: t('Action'),
                cell: (audit) => actionLabels[audit.action] ?? audit.action,
              },
              {
                id: 'staff',
                header: t('Staff'),
                cell: (audit) => {
                  if (audit.staff_user_id && audit.staff_user_id > 0) {
                    return (
                      <span className='tabular-nums'>
                        {formatUserIdentity(
                          audit.staff_user_id,
                          audit.staff_username
                        )}
                      </span>
                    )
                  }
                  const staffUsername = audit.staff_username?.trim()
                  return staffUsername || '—'
                },
              },
              {
                id: 'customer',
                header: t('Customer'),
                cell: (audit) =>
                  audit.customer_id > 0 ? (
                    <span className='tabular-nums'>
                      {formatUserIdentity(
                        audit.customer_id,
                        audit.customer_username
                      )}
                    </span>
                  ) : (
                    '—'
                  ),
              },
            ]}
          />
          <PageSwitcher
            page={page}
            total={audits.data.total}
            pageSize={STAFF_PAGE_SIZE}
            onPageChange={setPage}
          />
        </>
      )}
    </ContentSection>
  )
}
