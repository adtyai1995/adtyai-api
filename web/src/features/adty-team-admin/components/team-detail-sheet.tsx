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
import { useState, type ReactNode } from 'react'
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
import {
  SUBSITE_TEAM_MULTIPLIER_MIN,
  TEAM_DISCOUNT_MAX,
  validTeamDiscount,
} from '@/features/adty-team-registration/discount'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import {
  formatQuota,
  formatTimestamp,
  formatTimestampToDate,
} from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'

import {
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
            {t(
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
                    team={team}
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
      formatUserIdentity(team.leader_user_id, team.leader_username),
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
  const isSubsite = team.site_id > 0
  const [draft, setDraft] = useState('')
  // Both Root (for main-site and subsite teams) and site owners (for their own site teams) can edit active teams.
  const editable = team.status === 'active'
  const trimmed = draft.trim()
  const valid = validTeamDiscount(trimmed, isSubsite)
  const showError = trimmed !== '' && !valid
  const save = useMutation({
    mutationFn: () => saveTeamDiscount(props.scope, team.id, trimmed),
    onSuccess: () => {
      setDraft('')
      toast.success(t('Saved'))
      void client.invalidateQueries({ queryKey: teamAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <section className='flex flex-col gap-2'>
      <h3 className='text-sm font-medium'>
        {isSubsite ? t('Supply Price Multiplier') : t('Team Discount Ratio')}
      </h3>
      <p className='text-muted-foreground text-sm tabular-nums'>
        {isSubsite
          ? t('Current supply price multiplier: {{value}}x', {
              value: team.discount,
            })
          : t('Current team discount ratio: {{value}}', {
              value: team.discount,
            })}
      </p>
      {editable && (
        <>
          <p className='text-muted-foreground text-sm'>
            {isSubsite
              ? t(
                  'Applied directly to the main-site supply price (1.0 to 10.0). Cannot be below the 1.0 supply floor. Shared balance is not changed.'
                )
              : t(
                  'Applied to the main-site group price (greater than 0, up to 10.0; for example, 0.8 means 80% of the main-site group price). Shared balance is not changed.'
                )}
          </p>
          <div className='flex flex-wrap items-end gap-2'>
            <div className='flex flex-col gap-1'>
              <Label htmlFor={`team-admin-discount-${team.id}`}>
                {isSubsite
                  ? t('Supply Price Multiplier')
                  : t('Team Discount Ratio')}
              </Label>
              <Input
                id={`team-admin-discount-${team.id}`}
                className='w-44'
                type='number'
                min={isSubsite ? SUBSITE_TEAM_MULTIPLIER_MIN : undefined}
                max={TEAM_DISCOUNT_MAX}
                step='0.01'
                value={draft}
                inputMode='decimal'
                autoComplete='off'
                aria-invalid={showError}
                placeholder={team.discount}
                onChange={(event) => setDraft(event.target.value)}
              />
            </div>
            <Button
              type='button'
              variant='outline'
              disabled={save.isPending || !valid}
              onClick={() => save.mutate()}
            >
              {t('Save')}
            </Button>
          </div>
          {showError ? (
            <p role='alert' className='text-destructive text-xs'>
              {isSubsite
                ? t(
                    'Supply price multiplier must be between 1.0 and 10.0 (up to 4 decimal places) and cannot be below the supply floor.'
                  )
                : t(
                    'Team discount ratio must be greater than 0 and at most 10.0 (up to 4 decimal places).'
                  )}
            </p>
          ) : (
            <p className='text-muted-foreground text-xs'>
              {isSubsite
                ? t(
                    '1.0 means charge at the main-site supply price; 1.5 means 1.5× supply price.'
                  )
                : t(
                    '0.8 means 80% of the main-site group price; 1 means full price.'
                  )}
            </p>
          )}
        </>
      )}
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
          <TableHead>{t('ID / Username')}</TableHead>
          <TableHead>{t('Role')}</TableHead>
          <TableHead>{t('Account type')}</TableHead>
          <TableHead>{t('Status')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.team.members.map((member) => (
          <TableRow key={member.user_id}>
            <TableCell className='tabular-nums'>
              {formatUserIdentity(member.user_id, member.username)}
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
            <TableCell className='tabular-nums'>
              {formatUserIdentity(
                key.user_id,
                key.username || names.get(key.user_id)
              )}
            </TableCell>
            <TableCell>{labelOf(t, KEY_STATUS_LABELS, key.status)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function LedgerTable(props: { scope: TeamAdminScope; team: TeamAdminDetail }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const memberNames = new Map<number, string>([
    [props.team.leader_user_id, props.team.leader_username],
    ...props.team.members.map(
      (member) => [member.user_id, member.username] as const
    ),
  ])
  const ledger = useQuery({
    queryKey: teamAdminKeys.ledger(props.scope, props.team.id, page),
    queryFn: () => listAdminTeamLedger(props.scope, props.team.id, page),
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
                  {row.actor_user_id > 0
                    ? formatUserIdentity(
                        row.actor_user_id,
                        row.actor_username ||
                          memberNames.get(row.actor_user_id)
                      )
                    : '-'}
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
