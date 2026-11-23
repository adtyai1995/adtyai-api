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
import { useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { sideDrawerContentClassName } from '@/components/drawer-layout'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getOwnerWallets, siteOwnerKeys } from '@/features/adty-site-owner/api'
import { OwnerFundingPreviewBox } from '@/features/adty-site-owner/components/owner-funding-preview'
import { useVerifiedOwnerMutation } from '@/features/adty-site-owner/hooks/use-verified-owner-mutation'
import { computeOwnerFundingPreview } from '@/features/adty-site-owner/lib/funding-preview'
import { newIdempotencyKey } from '@/features/adty-site-owner/lib/site-owner'
import { validTeamDiscount } from '@/features/adty-team-registration/discount'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatQuota, formatTimestamp, formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  creditOwnerTeamBalance,
  getAdminTeam,
  listAdminTeamLedger,
  saveTeamDiscount,
  TEAM_ADMIN_PAGE_SIZE,
  teamAdminKeys,
  type TeamAdminDetail,
  type TeamAdminScope,
} from '../api'
import {
  KEY_STATUS_LABELS,
  labelOf,
  LEDGER_TYPE_LABELS,
  MEMBER_STATUS_LABELS,
  TEAM_STATUS_LABELS,
  teamStatusVariant,
} from '../labels'

/**
 * Read-only team detail for Root or the site owner. Members and keys stay
 * managed by the team leader; only the agreement discount is editable here.
 */
export function TeamDetailSheet(props: {
  scope: TeamAdminScope
  teamId: number | null
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const open = props.teamId != null
  const detail = useQuery({
    queryKey: teamAdminKeys.detail(props.scope, props.teamId ?? 0),
    queryFn: () => getAdminTeam(props.scope, props.teamId ?? 0),
    enabled: open,
  })
  const team = detail.data

  return (
    <Sheet open={open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-3xl')}>
        <SheetHeader>
          <SheetTitle>
            {team ? `${team.name} #${team.id}` : t('Team details')}
          </SheetTitle>
          <SheetDescription>
            {props.scope === 'owner'
              ? t(
                  'Members and team keys are managed by the team leader. You can adjust the agreement discount or add shared balance here.'
                )
              : t(
                  'Members and team keys are managed by the team leader. This page is read-only except for the discount.'
                )}
          </SheetDescription>
        </SheetHeader>
        <div className='flex flex-col gap-5 px-4 pb-6'>
          {detail.isPending && open && <LoadingState />}
          {detail.isError && (
            <ErrorState onRetry={() => void detail.refetch()} />
          )}
          {team && (
            <>
              <TeamSummary team={team} />
              <TeamDiscountEditor
                key={team.id}
                scope={props.scope}
                team={team}
              />
              {props.scope === 'owner' && team.status === 'active' && (
                <OwnerTeamBalanceCredit key={`credit-${team.id}`} team={team} />
              )}
              <Tabs defaultValue='members'>
                <TabsList>
                  <TabsTrigger value='members'>
                    {t('Members')} ({team.members.length})
                  </TabsTrigger>
                  <TabsTrigger value='keys'>
                    {t('Team keys')} ({team.keys.length})
                  </TabsTrigger>
                  <TabsTrigger value='ledger'>
                    {t('Shared balance ledger')}
                  </TabsTrigger>
                </TabsList>
                <TabsContent value='members'>
                  <MembersTable team={team} />
                </TabsContent>
                <TabsContent value='keys'>
                  <KeysTable team={team} />
                </TabsContent>
                <TabsContent value='ledger'>
                  <LedgerTable
                    key={team.id}
                    scope={props.scope}
                    teamId={team.id}
                  />
                </TabsContent>
              </Tabs>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function TeamSummary(props: { team: TeamAdminDetail }) {
  const { t } = useTranslation()
  const { team } = props
  const items: Array<[string, ReactNode]> = [
    [t('Site'), team.site_id === 0 ? t('Main site') : team.site_name],
    [
      t('Leader'),
      `${team.leader_username || '-'} #${team.leader_user_id}`,
    ],
    [
      t('Status'),
      <StatusBadge
        key='status'
        label={labelOf(t, TEAM_STATUS_LABELS, team.status)}
        variant={teamStatusVariant(team.status)}
      />,
    ],
    [t('Shared balance'), formatQuota(team.shared_quota)],
    [t('Active members'), String(team.active_members)],
    [t('Active keys'), String(team.active_keys)],
    [t('Created'), formatTimestampToDate(team.created_at)],
  ]
  if (team.closed_at > 0) {
    items.push([t('Closed at'), formatTimestampToDate(team.closed_at)])
  }
  if (team.source_site_id > 0) {
    items.push([
      t('Original site'),
      team.source_site_name || `#${team.source_site_id}`,
    ])
  }
  return (
    <dl className='grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2'>
      {items.map(([label, value]) => (
        <div key={label} className='flex justify-between gap-3'>
          <dt className='text-muted-foreground'>{label}</dt>
          <dd className='text-right tabular-nums'>{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function TeamDiscountEditor(props: {
  scope: TeamAdminScope
  team: TeamAdminDetail
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const { team } = props
  const [draft, setDraft] = useState('')
  // Root prices main-site teams; a subsite team's discount belongs to its owner.
  const editable =
    team.status === 'active' &&
    (props.scope === 'owner' || team.site_id === 0)
  const save = useMutation({
    mutationFn: () => saveTeamDiscount(props.scope, team.id, draft),
    onSuccess: () => {
      setDraft('')
      toast.success(t('Saved'))
      void client.invalidateQueries({ queryKey: teamAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <section className='flex flex-col gap-2'>
      <h3 className='text-sm font-medium'>{t('Agreement price')}</h3>
      <p className='text-muted-foreground text-sm'>
        {t('Current post-discount payment ratio: {{value}}', {
          value: team.discount,
        })}
      </p>
      {editable ? (
        <>
          <p className='text-muted-foreground text-sm'>
            {props.scope === 'root'
              ? t(
                  'A main-site team discount has no supply floor. A subsite team is refused.'
                )
              : t(
                  'A site team discount cannot go below the site supply price. Shared balance is not changed.'
                )}
          </p>
          <div className='flex flex-wrap items-end gap-2'>
            <div className='flex flex-col gap-1'>
              <Label htmlFor={`team-admin-discount-${team.id}`}>
                {t('Post-discount payment ratio')}
              </Label>
              <Input
                id={`team-admin-discount-${team.id}`}
                className='w-40'
                value={draft}
                inputMode='decimal'
                autoComplete='off'
                placeholder={team.discount}
                onChange={(event) => setDraft(event.target.value)}
              />
            </div>
            <Button
              type='button'
              variant='outline'
              disabled={save.isPending || !validTeamDiscount(draft)}
              onClick={() => save.mutate()}
            >
              {t('Save')}
            </Button>
          </div>
          <p className='text-muted-foreground text-xs'>
            {t(
              '0.8 means pay 80% of the ordinary price; 1 means pay the ordinary (full) price.'
            )}
          </p>
        </>
      ) : (
        team.status === 'active' &&
        props.scope === 'root' && (
          <p className='text-muted-foreground text-sm'>
            {t('A subsite team discount is set by that site owner.')}
          </p>
        )
      )}
    </section>
  )
}

/**
 * Isolated child component so useVerifiedOwnerMutation is only invoked under
 * OwnerVerificationProvider (never in the Root /admin-teams view).
 */
function OwnerTeamBalanceCredit(props: { team: TeamAdminDetail }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const intentRef = useRef<{ payload: string; key: string } | null>(null)
  const wallets = useQuery({
    queryKey: siteOwnerKeys.list('wallets', {}),
    queryFn: getOwnerWallets,
  })
  const walletRows = Array.isArray(wallets.data) ? wallets.data : []
  const preview = computeOwnerFundingPreview(walletRows, amount)
  const credit = useVerifiedOwnerMutation<{
    amount: string
    reason: string
    key: string
  }>({
    action: 'team.balance',
    target: () => props.team.id,
    run: (vars, proof) =>
      creditOwnerTeamBalance(
        props.team.id,
        { amount_usd: vars.amount, reason: vars.reason },
        vars.key,
        proof
      ),
    successMessage: t('Shared balance added'),
    onSuccess: () => {
      intentRef.current = null
      setAmount('')
      setReason('')
      void client.invalidateQueries({ queryKey: teamAdminKeys.all })
    },
  })
  return (
    <section className='flex flex-col gap-2'>
      <h3 className='text-sm font-medium'>{t('Add shared balance')}</h3>
      <div className='grid gap-2 sm:grid-cols-2'>
        <div className='flex flex-col gap-1'>
          <Label htmlFor={`team-credit-amount-${props.team.id}`}>
            {t('Amount (USD)')}
          </Label>
          <Input
            id={`team-credit-amount-${props.team.id}`}
            inputMode='decimal'
            autoComplete='off'
            value={amount}
            onChange={(event) => setAmount(event.target.value)}
          />
        </div>
        <div className='flex flex-col gap-1'>
          <Label htmlFor={`team-credit-reason-${props.team.id}`}>
            {t('Reason')}
          </Label>
          <Input
            id={`team-credit-reason-${props.team.id}`}
            maxLength={200}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </div>
      </div>
      <OwnerFundingPreviewBox wallets={walletRows} amount={amount} />
      <div>
        <Button
          type='button'
          variant='brand'
          disabled={
            credit.isPending ||
            !preview.valid ||
            (wallets.isSuccess && !preview.sufficient) ||
            reason.trim() === ''
          }
          onClick={() => {
            const trimmedAmount = amount.trim()
            const trimmedReason = reason.trim()
            const payload = `${props.team.id}:${trimmedAmount}:${trimmedReason}`
            if (intentRef.current?.payload !== payload) {
              intentRef.current = { payload, key: newIdempotencyKey() }
            }
            credit.mutate({
              amount: trimmedAmount,
              reason: trimmedReason,
              key: intentRef.current.key,
            })
          }}
        >
          {t('Add shared balance')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Funded automatically from available earnings first, then excess margin above the level deposit floor. Does not count toward site-level cumulative customer recharge.'
        )}
      </p>
    </section>
  )
}

function MembersTable(props: { team: TeamAdminDetail }) {
  const { t } = useTranslation()
  if (props.team.members.length === 0) {
    return <EmptyState className='min-h-24' title={t('No data')} />
  }
  return (
    <Table aria-label={t('Members')}>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Username')}</TableHead>
          <TableHead>{t('Role')}</TableHead>
          <TableHead>{t('Account type')}</TableHead>
          <TableHead>{t('Status')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.team.members.map((member) => (
          <TableRow key={member.user_id}>
            <TableCell>
              {member.username || '-'}{' '}
              <span className='text-muted-foreground tabular-nums'>
                #{member.user_id}
              </span>
            </TableCell>
            <TableCell>
              {member.role === 'leader' ? t('Leader') : t('Member')}
            </TableCell>
            <TableCell>
              {member.independent_employee
                ? t('Team employee account')
                : t('Personal account')}
            </TableCell>
            <TableCell>
              {labelOf(t, MEMBER_STATUS_LABELS, member.status)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function KeysTable(props: { team: TeamAdminDetail }) {
  const { t } = useTranslation()
  const names = new Map(
    props.team.members.map((member) => [member.user_id, member.username])
  )
  if (props.team.keys.length === 0) {
    return <EmptyState className='min-h-24' title={t('No team keys')} />
  }
  return (
    <Table aria-label={t('Team keys')}>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Name')}</TableHead>
          <TableHead>{t('Token id')}</TableHead>
          <TableHead>{t('Member')}</TableHead>
          <TableHead>{t('Status')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.team.keys.map((key) => (
          <TableRow key={key.token_id}>
            <TableCell>{key.name || '-'}</TableCell>
            <TableCell className='tabular-nums'>#{key.token_id}</TableCell>
            <TableCell>
              {names.get(key.user_id) || '-'}{' '}
              <span className='text-muted-foreground tabular-nums'>
                #{key.user_id}
              </span>
            </TableCell>
            <TableCell>{labelOf(t, KEY_STATUS_LABELS, key.status)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function LedgerTable(props: { scope: TeamAdminScope; teamId: number }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const ledger = useQuery({
    queryKey: teamAdminKeys.ledger(props.scope, props.teamId, page),
    queryFn: () => listAdminTeamLedger(props.scope, props.teamId, page),
  })
  if (ledger.isPending) return <LoadingState />
  if (ledger.isError) {
    return <ErrorState onRetry={() => void ledger.refetch()} />
  }
  return (
    <div className='flex flex-col gap-2'>
      {ledger.data.items.length === 0 ? (
        <EmptyState className='min-h-24' title={t('No data')} />
      ) : (
        <Table aria-label={t('Shared balance ledger')}>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Time')}</TableHead>
              <TableHead>{t('Type')}</TableHead>
              <TableHead className='text-right'>{t('Change')}</TableHead>
              <TableHead className='text-right'>{t('Balance after')}</TableHead>
              <TableHead>{t('Reference')}</TableHead>
              <TableHead>{t('Operator')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {ledger.data.items.map((row) => (
              <TableRow key={row.id}>
                <TableCell className='whitespace-nowrap'>
                  {formatTimestamp(row.created_at)}
                </TableCell>
                <TableCell>{labelOf(t, LEDGER_TYPE_LABELS, row.type)}</TableCell>
                <TableCell className='text-right tabular-nums'>
                  {row.delta_quota > 0 ? '+' : ''}
                  {formatQuota(row.delta_quota)}
                </TableCell>
                <TableCell className='text-right tabular-nums'>
                  {formatQuota(row.balance_after)}
                </TableCell>
                <TableCell className='max-w-56 truncate font-mono text-xs'>
                  {row.ref_id}
                </TableCell>
                <TableCell className='tabular-nums'>
                  {row.actor_user_id > 0 ? `#${row.actor_user_id}` : '-'}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <PageSwitcher
        page={page}
        total={ledger.data.total}
        pageSize={TEAM_ADMIN_PAGE_SIZE}
        onPageChange={setPage}
      />
    </div>
  )
}
