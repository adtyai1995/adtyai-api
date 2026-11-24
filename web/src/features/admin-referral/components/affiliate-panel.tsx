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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'

import {
  ADMIN_REFERRAL_PAGE_SIZE,
  adminReferralKeys,
  getAffiliates,
  updateAffiliate,
  type AdminReferralAffiliate,
  type AffiliateAction,
} from '../api'

function AffiliateState(props: { affiliate: AdminReferralAffiliate }) {
  const { t } = useTranslation()
  const affiliate = props.affiliate
  return (
    <div className='flex flex-wrap gap-1'>
      {affiliate.status === 'disabled' && (
        <StatusBadge label={t('Disabled')} variant='neutral' />
      )}
      {!affiliate.settlement_enabled && (
        <StatusBadge label={t('Settlement frozen')} variant='danger' />
      )}
      {!affiliate.withdrawal_enabled && (
        <StatusBadge label={t('Withdrawals frozen')} variant='danger' />
      )}
      {affiliate.site_owner_since > 0 && (
        <StatusBadge label={t('Runs a reseller site')} variant='info' />
      )}
    </div>
  )
}

export function AffiliatePanel(props: {
  canAdjust: boolean
  onViewCommissions?: (affiliate: AdminReferralAffiliate) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const [editing, setEditing] = useState<AdminReferralAffiliate | null>(null)
  const [rate, setRate] = useState('')
  const [amount, setAmount] = useState('')
  const [adjustKey, setAdjustKey] = useState(() => crypto.randomUUID())
  const [reason, setReason] = useState('')

  const openEditing = (row: AdminReferralAffiliate) => {
    setEditing(row)
    setRate(
      row.rate_override === null || row.rate_override === undefined
        ? ''
        : String(row.rate_override)
    )
    setAmount('')
    setReason('')
    setAdjustKey(crypto.randomUUID())
  }

  const closeEditing = () => {
    setEditing(null)
    setRate('')
    setAmount('')
    setReason('')
    setAdjustKey(crypto.randomUUID())
  }

  const params = {
    p: page,
    keyword: search,
  }
  const query = useQuery({
    queryKey: adminReferralKeys.list('affiliates', params),
    queryFn: () => getAffiliates(params),
  })
  const done = () => {
    toast.success(t('Saved successfully'))
    closeEditing()
    void queryClient.invalidateQueries({ queryKey: adminReferralKeys.all })
    void invalidateSidebarBadges(queryClient)
  }

  // An empty own rate goes back to the default rate.
  const trimmedRate = rate.trim()
  const ownRate = trimmedRate === '' ? null : Number(trimmedRate)
  const rateInvalid =
    ownRate !== null &&
    (!Number.isFinite(ownRate) || ownRate < 0 || ownRate > 100)

  const update = useMutation({
    mutationFn: (input: {
      affiliate: AdminReferralAffiliate
      action: AffiliateAction
    }) =>
      updateAffiliate(input.affiliate.user_id, {
        action: input.action,
        rate: input.action === 'rate' ? ownRate : undefined,
        amount: input.action === 'adjust' ? amount.trim() : undefined,
        reason: reason.trim(),
        idempotency_key: input.action === 'adjust' ? adjustKey : undefined,
      }),
    onSuccess: done,
    onError: (err) => handleServerError(err),
  })
  const act = (affiliate: AdminReferralAffiliate, action: AffiliateAction) => {
    if (update.isPending) return
    if (action === 'rate' && rateInvalid) return
    update.mutate({ affiliate, action })
  }

  if (query.isError && !query.data) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }

  return (
    <div className='flex flex-col gap-3'>
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      <form
        className='flex gap-2'
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
      </form>
      <StaticDataTable
        tableClassName='min-w-[900px]'
        tableProps={{ 'aria-label': t('Affiliates') }}
        data={query.data?.items ?? []}
        getRowKey={(row) => String(row.id)}
        emptyContent={<EmptyState className='min-h-32' title={t('No data')} />}
        columns={[
          {
            id: 'user',
            header: t('User'),
            cellClassName: 'tabular-nums',
            cell: (row) => formatUserIdentity(row.user_id, row.username),
          },
          {
            id: 'code',
            header: t('Invite code'),
            cell: (row) => <span className='font-mono'>{row.invite_code}</span>,
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
            id: 'state',
            header: t('Status'),
            cell: (row) => <AffiliateState affiliate={row} />,
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
                {props.onViewCommissions && (
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => props.onViewCommissions?.(row)}
                  >
                    {t('View commissions')}
                  </Button>
                )}
                {props.canAdjust && (
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => openEditing(row)}
                  >
                    {t('Adjust')}
                  </Button>
                )}
              </div>
            ),
          },
        ]}
      />
      <PageSwitcher
        page={page}
        total={query.data?.total ?? 0}
        pageSize={ADMIN_REFERRAL_PAGE_SIZE}
        onPageChange={setPage}
      />

      <Dialog
        open={editing !== null}
        onOpenChange={(open) => !open && closeEditing()}
        title={t('Adjust affiliate')}
        description={
          editing
            ? `${formatUserIdentity(editing.user_id, editing.username)} · ${editing.invite_code}`
            : ''
        }
      >
        {editing && (
          <div className='space-y-5'>
            <div className='space-y-2'>
              <Label htmlFor='adjust-reason'>{t('Reason')}</Label>
              <Input
                id='adjust-reason'
                value={reason}
                disabled={update.isPending}
                onChange={(event) => setReason(event.target.value)}
              />
            </div>
            <div className='space-y-1.5'>
              <div className='flex items-end gap-2'>
                <div className='flex-1 space-y-2'>
                  <Label htmlFor='adjust-rate'>
                    {t('Own rate (%), empty for the default')}
                  </Label>
                  <Input
                    id='adjust-rate'
                    type='number'
                    min={0}
                    max={100}
                    step='0.01'
                    disabled={update.isPending}
                    aria-invalid={rateInvalid}
                    value={rate}
                    onChange={(event) => setRate(event.target.value)}
                  />
                </div>
                <Button
                  type='button'
                  disabled={update.isPending || rateInvalid}
                  onClick={() => act(editing, 'rate')}
                >
                  {t('Set rate')}
                </Button>
              </div>
              {rateInvalid && (
                <p role='alert' className='text-destructive text-xs'>
                  {t('The rate must be between 0 and 100')}
                </p>
              )}
            </div>
            <div className='flex items-end gap-2'>
              <div className='flex-1 space-y-2'>
                <Label htmlFor='adjust-amount'>
                  {t('Adjust withdrawable (USD, negative to deduct)')}
                </Label>
                <Input
                  id='adjust-amount'
                  type='number'
                  step='0.01'
                  disabled={update.isPending}
                  value={amount}
                  onChange={(event) => setAmount(event.target.value)}
                />
              </div>
              <Button
                type='button'
                variant='brand'
                disabled={
                  update.isPending ||
                  amount.trim() === '' ||
                  reason.trim() === ''
                }
                onClick={() => act(editing, 'adjust')}
              >
                {t('Apply')}
              </Button>
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
                  disabled={update.isPending}
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
                  disabled={update.isPending}
                  onClick={() =>
                    act(
                      editing,
                      editing.settlement_enabled
                        ? 'freeze_settlement'
                        : 'restore_settlement'
                    )
                  }
                >
                  {editing.settlement_enabled
                    ? t('Freeze settlement')
                    : t('Restore settlement')}
                </Button>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={update.isPending}
                  onClick={() =>
                    act(
                      editing,
                      editing.withdrawal_enabled
                        ? 'freeze_withdrawal'
                        : 'restore_withdrawal'
                    )
                  }
                >
                  {editing.withdrawal_enabled
                    ? t('Freeze withdrawals')
                    : t('Restore withdrawals')}
                </Button>
                {editing.site_owner_since > 0 && (
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={update.isPending}
                    onClick={() => act(editing, 'restore_referral')}
                  >
                    {t('Restore referral commission')}
                  </Button>
                )}
              </div>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  )
}
