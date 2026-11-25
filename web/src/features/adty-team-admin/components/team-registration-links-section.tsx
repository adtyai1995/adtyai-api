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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  issueTeamRegistration,
  listTeamRegistrations,
  revokeTeamRegistration,
  teamRegistrationUrl,
  TEAM_REGISTRATION_MAX_TTL_HOURS,
  type TeamRegistrationActor,
  type TeamRegistrationView,
} from '@/features/adty-team-registration/api'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { formatTimestamp } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

const statusLabel: Record<string, string> = {
  outstanding: 'Outstanding',
  expired: 'Expired',
  used: 'Used',
  revoked: 'Revoked',
}

function registrationStatusVariant(status: string) {
  if (status === 'outstanding' || status === 'active') return 'info' as const
  if (status === 'used') return 'success' as const
  if (status === 'expired') return 'warning' as const
  if (status === 'revoked') return 'danger' as const
  return 'neutral' as const
}

function ttlSecondsFromHours(value: string): number | null {
  const hours = Number(value)
  if (
    !Number.isFinite(hours) ||
    hours <= 0 ||
    hours > TEAM_REGISTRATION_MAX_TTL_HOURS
  ) {
    return null
  }
  return Math.round(hours * 3600)
}

/**
 * Console-styled team registration links section for both Root (/admin-teams)
 * and site owners (/site-owner/teams).
 */
export function TeamRegistrationLinksSection(props: {
  actor: TeamRegistrationActor
  closing?: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const closing = Boolean(props.closing)
  const queryKey = ['adty-team-registrations', props.actor] as const
  const links = useQuery({
    queryKey,
    queryFn: () => listTeamRegistrations(props.actor),
  })
  const [ttlHours, setTtlHours] = useState('24')
  const [issuedUrl, setIssuedUrl] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  const refresh = () => void queryClient.invalidateQueries({ queryKey })

  const issue = async () => {
    const ttlSeconds = ttlSecondsFromHours(ttlHours)
    if (ttlSeconds == null) {
      setError(t('Invalid parameters'))
      return
    }
    setPending(true)
    setError('')
    try {
      const issued = await issueTeamRegistration(props.actor, ttlSeconds)
      setIssuedUrl(teamRegistrationUrl(issued.code))
      refresh()
    } catch (cause) {
      setError(t(getServerErrorMessage(cause, t('Failed to save'))))
    } finally {
      setPending(false)
    }
  }

  const revoke = async (id: number) => {
    if (closing) return
    setPending(true)
    setError('')
    try {
      await revokeTeamRegistration(props.actor, id)
      refresh()
    } catch (cause) {
      setError(t(getServerErrorMessage(cause, t('Failed to save'))))
    } finally {
      setPending(false)
    }
  }

  const rows: TeamRegistrationView[] = Array.isArray(links.data)
    ? links.data
    : []

  const tableSection = (
    <ContentSection
      title={
        props.actor === 'root'
          ? t('Team registration links')
          : t('Team registration')
      }
    >
      {error && props.actor !== 'root' && (
        <p className='text-destructive text-sm'>{error}</p>
      )}
      {links.isPending && <LoadingState />}
      {links.isError && <ErrorState onRetry={() => void links.refetch()} />}
      {links.isSuccess && (
        <StaticDataTable
          tableProps={{ 'aria-label': t('Team registration') }}
          data={rows}
          getRowKey={(row) => String(row.id)}
          emptyContent={
            <EmptyState
              className='min-h-24'
              title={t('No registration links yet')}
            />
          }
          columns={[
            {
              id: 'id',
              header: t('ID'),
              cellClassName: 'font-mono text-xs tabular-nums',
              cell: (row) => `#${row.id}`,
            },
            {
              id: 'status',
              header: t('Status'),
              cell: (row) => (
                <StatusBadge
                  label={t(statusLabel[row.status] ?? row.status)}
                  variant={registrationStatusVariant(row.status)}
                  copyable={false}
                />
              ),
            },
            {
              id: 'expires_at',
              header: t('Validity'),
              cellClassName: 'tabular-nums whitespace-nowrap',
              cell: (row) =>
                row.expires_at ? formatTimestamp(row.expires_at) : '—',
            },
            {
              id: 'actions',
              header: t('Actions'),
              className: 'text-right',
              cellClassName: 'text-right',
              cell: (row) =>
                row.status === 'outstanding' ? (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    disabled={closing || pending}
                    onClick={() => void revoke(row.id)}
                  >
                    {t('Revoke registration link')}
                  </Button>
                ) : (
                  <span className='text-muted-foreground'>—</span>
                ),
            },
          ]}
        />
      )}
    </ContentSection>
  )

  if (props.actor !== 'root') {
    return tableSection
  }

  return (
    <div className='flex flex-col gap-8'>
      <SettingsSection
        title={t('Team registration')}
        description={t(
          'Issue a team registration link so the recipient can set a username, password, and team name.'
        )}
      >
        <div className='flex flex-col gap-4'>
          <div className='flex flex-wrap items-end gap-3'>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor={`team-registration-ttl-${props.actor}`}>
                {t('Validity (hours)')}
              </Label>
              <Input
                id={`team-registration-ttl-${props.actor}`}
                className='w-36'
                value={ttlHours}
                inputMode='decimal'
                autoComplete='off'
                onChange={(event) => setTtlHours(event.target.value)}
              />
            </div>
            <Button
              type='button'
              variant='brand'
              disabled={pending}
              onClick={() => void issue()}
            >
              {t('Issue registration link')}
            </Button>
          </div>
          {issuedUrl && (
            <div className='bg-muted/40 flex max-w-2xl flex-col gap-2 rounded-[10px] px-4 py-3 text-sm'>
              <p className='font-mono text-xs break-all'>
                {t('Registration link')}: {issuedUrl}
              </p>
              <div className='flex flex-wrap items-center gap-3'>
                <CopyButton
                  value={issuedUrl}
                  variant='outline'
                  size='sm'
                  tooltip={t('Copy link')}
                  aria-label={t('Copy link')}
                >
                  {t('Copy link')}
                </CopyButton>
                <span className='text-dim text-xs'>
                  {t('The registration link is shown once')}
                </span>
              </div>
            </div>
          )}
          {error && <p className='text-destructive text-sm'>{error}</p>}
        </div>
      </SettingsSection>
      {tableSection}
    </div>
  )
}
