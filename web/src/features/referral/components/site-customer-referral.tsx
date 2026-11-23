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
import { QRCodeSVG } from 'qrcode.react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SiteCommissionPanel } from '@/features/wallet/adty/site-commission-panel'
import { api } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

type ApiResponse<T> = { success: boolean; message?: string; data: T }

export type SiteCustomerReferralInvitee = {
  invitee_id: number
  invitee_username: string
  rate: string
  rate_percent?: string
  created_at: number
}

export type SiteCustomerReferralProfile = {
  site_id: number
  site_name: string
  site_status: string
  enabled: boolean
  rate_configured: boolean
  rate?: string
  rate_percent?: string
  observation_days?: number
  pending_coverage_cap?: string
  invite_code: string
  invite_url: string
  invited_count: number
  invites: SiteCustomerReferralInvitee[]
  has_inviter: boolean
  inviter_username?: string
  can_bind: boolean
}

const siteReferralProfileKey = ['adty-site-referral', 'profile'] as const

async function getSiteReferralProfile(): Promise<SiteCustomerReferralProfile> {
  const res = await api.get<ApiResponse<SiteCustomerReferralProfile>>(
    '/api/adty/site-referral/profile'
  )
  return requireServerSuccess(res.data).data
}

async function bindSiteInviteCode(inviteCode: string): Promise<void> {
  const res = await api.post<ApiResponse<{ bound: boolean }>>(
    '/api/adty/site-referral/bind',
    { invite_code: inviteCode }
  )
  requireServerSuccess(res.data)
}

/** Sub-site customer level-1 invitation link, invitee list, binding, and site commissions. */
export function SiteCustomerReferral() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [bindCode, setBindCode] = useState('')
  const profileQuery = useQuery({
    queryKey: siteReferralProfileKey,
    queryFn: getSiteReferralProfile,
  })
  const bindMutation = useMutation({
    mutationFn: () => bindSiteInviteCode(bindCode.trim()),
    onSuccess: () => {
      setBindCode('')
      toast.success(t('Inviter bound successfully'))
      void queryClient.invalidateQueries({ queryKey: siteReferralProfileKey })
    },
    onError: (error) => handleServerError(error),
  })

  if (profileQuery.isLoading) return <LoadingState />
  if (profileQuery.isError) {
    return <ErrorState onRetry={() => void profileQuery.refetch()} />
  }
  const profile = profileQuery.data
  if (!profile) return null

  const inviteLink =
    profile.invite_url.startsWith('http')
      ? profile.invite_url
      : `${window.location.origin}${profile.invite_url}`
  const invites = Array.isArray(profile.invites) ? profile.invites : []
  const rateText = profile.rate_percent
    ? `${profile.rate_percent}%`
    : t('Not set')
  const observationText =
    typeof profile.observation_days === 'number'
      ? t('{{days}} days', { days: profile.observation_days })
      : t('Not set')

  return (
    <div className='flex flex-col gap-4'>
      <Card className='bg-foreground text-background'>
        <CardContent className='flex flex-col gap-6 py-6 md:flex-row md:items-center md:justify-between'>
          <div className='flex min-w-0 flex-col gap-4'>
            <div className='flex flex-col gap-1'>
              <span className='text-background/70 text-xs tracking-wider uppercase'>
                {t('Your site invite link')}
              </span>
              <div className='flex items-center gap-2'>
                <span
                  className='truncate font-mono text-base sm:text-lg'
                  data-testid='site-referral-link'
                >
                  {inviteLink}
                </span>
                <CopyButton
                  value={inviteLink}
                  variant='secondary'
                  size='icon'
                  aria-label={t('Copy link')}
                />
              </div>
              <div className='text-background/80 flex items-center gap-2 text-xs'>
                <span>
                  {t('Invite code')}:{' '}
                  <code className='font-mono'>{profile.invite_code}</code>
                </span>
                <CopyButton
                  value={profile.invite_code}
                  variant='secondary'
                  size='sm'
                  aria-label={t('Copy invite code')}
                />
              </div>
            </div>
            {!profile.enabled && (
              <p className='text-warning text-sm'>
                {t('Site referral is currently disabled by the site owner.')}
              </p>
            )}
            <dl className='grid grid-cols-2 gap-4 text-sm sm:grid-cols-3'>
              <div>
                <dt className='text-background/70 text-xs'>
                  {t('Commission rate')}
                </dt>
                <dd className='text-xl font-semibold'>{rateText}</dd>
              </div>
              <div>
                <dt className='text-background/70 text-xs'>
                  {t('Observation period')}
                </dt>
                <dd className='text-xl font-semibold'>{observationText}</dd>
              </div>
              <div>
                <dt className='text-background/70 text-xs'>
                  {t('Direct invitees')}
                </dt>
                <dd className='text-xl font-semibold tabular-nums'>
                  {profile.invited_count}
                </dd>
              </div>
            </dl>
          </div>
          <div className='bg-background shrink-0 self-start rounded-lg p-3'>
            <QRCodeSVG value={inviteLink} size={112} />
          </div>
        </CardContent>
      </Card>

      {(profile.has_inviter || profile.can_bind) && (
        <Card>
          <CardHeader>
            <CardTitle className='text-base'>{t('My inviter')}</CardTitle>
          </CardHeader>
          <CardContent>
            {profile.has_inviter ? (
              <p className='text-muted-foreground text-sm'>
                {t('Invited by {{username}}', {
                  username: profile.inviter_username || '-',
                })}
              </p>
            ) : (
              <form
                className='flex flex-col gap-3 sm:flex-row sm:items-end'
                onSubmit={(event) => {
                  event.preventDefault()
                  if (bindCode.trim()) bindMutation.mutate()
                }}
              >
                <div className='flex flex-1 flex-col gap-1'>
                  <Label htmlFor='site-bind-invite-code'>
                    {t('Bind inviter code (before first paid order)')}
                  </Label>
                  <Input
                    id='site-bind-invite-code'
                    value={bindCode}
                    placeholder={t('Enter site invite code')}
                    onChange={(event) => setBindCode(event.target.value)}
                  />
                </div>
                <Button
                  type='submit'
                  variant='outline'
                  disabled={bindMutation.isPending || bindCode.trim() === ''}
                >
                  {t('Bind inviter')}
                </Button>
              </form>
            )}
          </CardContent>
        </Card>
      )}

      <Tabs defaultValue='commissions'>
        <TabsList>
          <TabsTrigger value='commissions'>{t('Commission details')}</TabsTrigger>
          <TabsTrigger value='invitees'>{t('Invited users')}</TabsTrigger>
          <TabsTrigger value='rules'>{t('Program rules')}</TabsTrigger>
        </TabsList>
        <TabsContent value='commissions'>
          <Card>
            <CardContent className='pt-6'>
              <SiteCommissionPanel
                onConverted={() =>
                  void queryClient.invalidateQueries({
                    queryKey: siteReferralProfileKey,
                  })
                }
              />
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value='invitees'>
          <Card>
            <CardContent className='pt-6'>
              {invites.length === 0 ? (
                <EmptyState title={t('No invited users yet')} />
              ) : (
                <StaticDataTable
                  rows={invites}
                  getRowId={(row) => String(row.invitee_id)}
                  columns={[
                    {
                      id: 'invitee',
                      header: t('User'),
                      cell: (row) =>
                        `${row.invitee_username || '-'} (#${row.invitee_id})`,
                    },
                    {
                      id: 'rate',
                      header: t('Commission rate'),
                      cell: (row) =>
                        `${row.rate_percent ?? Number((Number(row.rate) * 100).toFixed(4))}%`,
                    },
                    {
                      id: 'created_at',
                      header: t('Joined at'),
                      cell: (row) =>
                        row.created_at > 0
                          ? formatTimestamp(row.created_at)
                          : '-',
                    },
                  ]}
                />
              )}
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value='rules'>
          <Card>
            <CardContent className='py-4'>
              <ul className='text-muted-foreground list-disc space-y-2 pl-5 text-sm'>
                <li>
                  {t(
                    'Only direct one-level invites within this sub-site qualify for commission.'
                  )}
                </li>
                <li>
                  {t(
                    'Commission is calculated only on direct invitees paid personal top-up principal. Bonus credit and team top-ups earn nothing.'
                  )}
                </li>
                <li>
                  {t(
                    'Commission settles after site coverage and the {{days}}-day observation period. Refunded orders void or claw back their commission.',
                    { days: profile.observation_days ?? 0 }
                  )}
                </li>
                <li>
                  {t(
                    'Invite relationships can only be bound at sign-up or before your first paid order.'
                  )}
                </li>
              </ul>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
