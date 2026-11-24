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

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { getUserSites } from '@/features/users/api'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota, formatTimestampToDate } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  listAdminTeams,
  TEAM_ADMIN_PAGE_SIZE,
  teamAdminKeys,
  type TeamAdminRow,
  type TeamAdminScope,
  type TeamStatusFilter,
} from '../api'
import { labelOf, TEAM_STATUS_LABELS, teamStatusVariant } from '../labels'

/** Paged team list. Root can narrow by site; the owner list is its own site. */
export function TeamListPanel(props: {
  scope: TeamAdminScope
  onOpenTeam: (team: TeamAdminRow) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState<TeamStatusFilter>('active')
  const [siteId, setSiteId] = useState('')
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const isRoot = props.scope === 'root'
  const sites = useQuery({
    queryKey: ['team-admin-sites'],
    queryFn: async () => requireServerSuccess(await getUserSites()).data ?? [],
    enabled: isRoot,
  })
  const params = { p: page, status, q: search, siteId }
  const teams = useQuery({
    queryKey: teamAdminKeys.list(props.scope, params),
    queryFn: () => listAdminTeams(props.scope, params),
  })
  const rows = teams.data?.items ?? []
  const columns = isRoot ? 10 : 9

  return (
    <div className='flex flex-col gap-3'>
      <form
        className='flex flex-wrap gap-2'
        onSubmit={(event) => {
          event.preventDefault()
          setSearch(keyword.trim())
          setPage(1)
        }}
      >
        {isRoot && (
          <NativeSelect
            aria-label={t('Site')}
            value={siteId}
            onChange={(event) => {
              setSiteId(event.target.value)
              setPage(1)
            }}
          >
            <NativeSelectOption value=''>{t('All sites')}</NativeSelectOption>
            <NativeSelectOption value='0'>{t('Main site')}</NativeSelectOption>
            {sites.data
              ?.filter((item) => item.site_id > 0)
              .map((item) => (
                <NativeSelectOption
                  key={item.site_id}
                  value={String(item.site_id)}
                >
                  {item.site_name} (#{item.site_id})
                </NativeSelectOption>
              ))}
          </NativeSelect>
        )}
        <NativeSelect
          aria-label={t('Status')}
          value={status}
          onChange={(event) => {
            setStatus(event.target.value as TeamStatusFilter)
            setPage(1)
          }}
        >
          <NativeSelectOption value=''>{t('All statuses')}</NativeSelectOption>
          <NativeSelectOption value='active'>{t('Active')}</NativeSelectOption>
          <NativeSelectOption value='closed'>{t('Closed')}</NativeSelectOption>
        </NativeSelect>
        <Input
          className='w-64'
          value={keyword}
          onChange={(event) => setKeyword(event.target.value)}
          placeholder={t('Team name, team ID or leader')}
          aria-label={t('Search teams')}
        />
        <Button variant='brand' type='submit'>
          {t('Search')}
        </Button>
      </form>
      {sites.isError && <ErrorState onRetry={() => void sites.refetch()} />}
      {teams.isError && <ErrorState onRetry={() => void teams.refetch()} />}
      {teams.isPending ? (
        <LoadingState />
      ) : (
        <div className='border-border bg-card w-full min-w-0 overflow-x-auto overflow-y-hidden rounded-[14px] border'>
          <Table
            withContainer={false}
            className='w-max min-w-full'
            aria-label={t('Teams')}
          >
            <TableHeader>
              <TableRow>
                <TableHead>{t('Team')}</TableHead>
                {isRoot && <TableHead>{t('Site')}</TableHead>}
                <TableHead>{t('Leader')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead className='text-right'>
                  {t('Active members')}
                </TableHead>
                <TableHead className='text-right'>{t('Active keys')}</TableHead>
                <TableHead className='text-right'>
                  {t('Shared balance')}
                </TableHead>
                <TableHead className='text-right'>
                  {isRoot
                    ? t('Discount / Multiplier')
                    : t('Supply Price Multiplier')}
                </TableHead>
                <TableHead>{t('Created')}</TableHead>
                <TableHead className='text-right'>{t('Actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={columns}>
                    <EmptyState className='min-h-32' title={t('No data')} />
                  </TableCell>
                </TableRow>
              ) : (
                rows.map((row) => (
                  <TableRow key={row.id}>
                    <TableCell>
                      {row.name}{' '}
                      <span className='text-muted-foreground tabular-nums'>
                        #{row.id}
                      </span>
                    </TableCell>
                    {isRoot && (
                      <TableCell>
                        {row.site_id === 0 ? t('Main site') : row.site_name}
                        {row.source_site_id > 0 && (
                          <span className='text-muted-foreground block text-xs'>
                            {t('Moved from {{site}}', {
                              site:
                                row.source_site_name ||
                                `#${row.source_site_id}`,
                            })}
                          </span>
                        )}
                      </TableCell>
                    )}
                    <TableCell>
                      {row.leader_username || '-'}{' '}
                      <span className='text-muted-foreground tabular-nums'>
                        #{row.leader_user_id}
                      </span>
                    </TableCell>
                    <TableCell>
                      <StatusBadge
                        label={labelOf(t, TEAM_STATUS_LABELS, row.status)}
                        variant={teamStatusVariant(row.status)}
                      />
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatNumber(row.active_members, locale)}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatNumber(row.active_keys, locale)}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatQuota(row.shared_quota)}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {row.site_id > 0 ? `${row.discount}x` : row.discount}
                    </TableCell>
                    <TableCell>{formatTimestampToDate(row.created_at)}</TableCell>
                    <TableCell className='text-right'>
                      <Button
                        size='sm'
                        variant='outline'
                        onClick={() => props.onOpenTeam(row)}
                      >
                        {t('Details')}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      )}
      <PageSwitcher
        page={page}
        total={teams.data?.total ?? 0}
        pageSize={TEAM_ADMIN_PAGE_SIZE}
        onPageChange={setPage}
      />
    </div>
  )
}
