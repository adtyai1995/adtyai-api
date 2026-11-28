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
import type { TFunction } from 'i18next'
import { QRCodeSVG } from 'qrcode.react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import {
  SiteCommissionPanel,
  type SiteCommission,
} from '@/features/wallet/adty/site-commission-panel'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  SiteCustomerReferralInvitee,
  SiteCustomerReferralProfile,
} from '../types'

export type { SiteCustomerReferralInvitee, SiteCustomerReferralProfile }

type ApiResponse<T> = { success: boolean; message?: string; data: T }

type CustomerSiteCommission = SiteCommission & {
  face_usd?: string
  created_at?: number
}

const siteReferralProfileKey = ['adty-site-referral', 'profile'] as const
const siteCommissionsKey = ['adty-site-commissions'] as const

async function getSiteReferralProfile(): Promise<SiteCustomerReferralProfile> {
  const res = await api.get<ApiResponse<SiteCustomerReferralProfile>>(
    '/api/adty/site-referral/profile'
  )
  return requireServerSuccess(res.data).data
}

async function listCustomerCommissions(): Promise<CustomerSiteCommission[]> {
  const res = await api.get<ApiResponse<{ items?: CustomerSiteCommission[] }>>(
    '/api/adty/site-commissions'
  )
  if (!res?.data) return []
  return requireServerSuccess(res.data).data.items ?? []
}

async function bindSiteInviteCode(inviteCode: string): Promise<void> {
  const res = await api.post<ApiResponse<{ bound: boolean }>>(
    '/api/adty/site-referral/bind',
    { invite_code: inviteCode }
  )
  requireServerSuccess(res.data)
}

function parseAmount(raw?: string): number {
  const value = Number(raw ?? 0)
  return Number.isFinite(value) ? value : 0
}

function formatCustomerRate(raw?: string): string {
  const trimmed = (raw ?? '').trim()
  if (trimmed === '') return '-'
  const value = Number(trimmed)
  if (!Number.isFinite(value)) return trimmed
  const pct = value <= 1 ? value * 100 : value
  return `${Number(pct.toFixed(4))}%`
}

/** White-label customer commission status labels (literal t() calls for i18n scanner). */
function customerCommissionStatusLabel(t: TFunction, status: string): string {
  switch (status) {
    case 'risk_held':
      return t('Risk held')
    case 'pending_coverage':
      return t('Pending')
    case 'covered':
    case 'pending':
      return t('Settling')
    case 'available':
      return t('Withdrawable')
    case 'frozen':
      return t('Withdrawing')
    case 'paid':
      return t('Withdrawn')
    case 'void':
      return t('Voided')
    case 'clawed_back':
      return t('Clawed back')
    default:
      return status || '-'
  }
}

function customerCommissionStatusVariant(status: string): StatusVariant {
  if (status === 'available') return 'success'
  if (status === 'risk_held' || status === 'clawed_back') return 'danger'
  if (
    status === 'pending_coverage' ||
    status === 'covered' ||
    status === 'pending'
  ) {
    return 'warning'
  }
  if (status === 'frozen') return 'info'
  return 'neutral'
}

/** Customer level-1 invitation link, invitee list, binding, and commissions (strict white-label). */
export function SiteCustomerReferral() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n?.resolvedLanguage || i18n?.language)
  const queryClient = useQueryClient()
  const [bindCode, setBindCode] = useState('')
  const profileQuery = useQuery({
    queryKey: siteReferralProfileKey,
    queryFn: getSiteReferralProfile,
  })
  const commissionsQuery = useQuery({
    queryKey: siteCommissionsKey,
    queryFn: listCustomerCommissions,
    enabled: profileQuery.isSuccess,
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
  const commissions = Array.isArray(commissionsQuery.data)
    ? commissionsQuery.data
    : []
  const rateText = profile.rate_percent
    ? `${profile.rate_percent}%`
    : t('Not set')

  const pendingHourUsd = Math.max(parseAmount(profile.pending_hour_usd), 0)
  let availableUsd = 0
  let pendingHeldUsd = 0
  for (const row of commissions) {
    const withdrawable = Math.max(parseAmount(row.withdrawable_usd), 0)
    const netAmount = Math.max(
      parseAmount(row.amount_usd) - parseAmount(row.clawed_usd),
      0
    )
    if (row.status === 'available') {
      availableUsd += withdrawable
    } else if (
      row.status === 'risk_held' ||
      row.status === 'pending_coverage'
    ) {
      pendingHeldUsd += netAmount
    } else if (row.status === 'covered' || row.status === 'pending') {
      const remaining = Math.max(
        netAmount -
          parseAmount(row.converted_usd) -
          parseAmount(row.withdrawn_usd) -
          parseAmount(row.frozen_usd),
        0
      )
      pendingHeldUsd += remaining
    }
  }

  return (
    <div className='flex flex-col gap-4'>
      <Card className='bg-foreground text-background'>
        <CardContent className='flex flex-col gap-6 py-6 md:flex-row md:items-center md:justify-between'>
          <div className='flex min-w-0 flex-col gap-4'>
            <div className='flex flex-col gap-1'>
              <span className='text-background/70 text-xs'>
                {t('Your invite link')}
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
                {profile.affiliate_disabled === true ||
                profile.affiliate_status === 'disabled'
                  ? t(
                      'Your affiliate account has been disabled by the site administrator.'
                    )
                  : t('The referral program is currently disabled on this site.')}
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
                  {t('Settlement')}
                </dt>
                <dd className='text-xl font-semibold'>
                  {t('Hourly (no freeze)')}
                </dd>
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

      <div
        className='border-border bg-border grid grid-cols-1 gap-px overflow-hidden rounded-[14px] border sm:grid-cols-3'
        data-testid='site-referral-balances'
      >
        <div className='bg-card flex flex-col gap-1 px-5 py-4'>
          <span className='text-dim text-[12px] leading-none'>
            {t('Current hour estimate')}
          </span>
          <span className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
            {formatUsd(pendingHourUsd, locale)}
          </span>
          <span className='text-muted-foreground text-[11px] leading-snug'>
            {t('Settled hourly on the hour')}
          </span>
        </div>
        <div className='bg-card flex flex-col gap-1 px-5 py-4'>
          <span className='text-dim text-[12px] leading-none'>
            {t('Withdrawable')}
          </span>
          <span className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
            {formatUsd(availableUsd, locale)}
          </span>
        </div>
        <div className='bg-card flex flex-col gap-1 px-5 py-4'>
          <span className='text-dim text-[12px] leading-none'>
            {t('Pending / Held')}
          </span>
          <span className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
            {formatUsd(pendingHeldUsd, locale)}
          </span>
        </div>
      </div>

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
                    placeholder={t('Enter invite code')}
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
          <div className='flex flex-col gap-4'>
            {commissions.length > 0 && (
              <StaticDataTable
                tableProps={{ 'aria-label': t('Commission details') }}
                data={commissions}
                getRowKey={(row) => String(row.id)}
                columns={[
                  {
                    id: 'time',
                    header: t('Time'),
                    cell: (row) =>
                      row.created_at && row.created_at > 0
                        ? formatTimestamp(row.created_at)
                        : '-',
                  },
                  {
                    id: 'consumption',
                    header: t('Paid consumption'),
                    className: 'text-right',
                    cellClassName: 'text-right tabular-nums',
                    cell: (row) =>
                      row.face_usd !== undefined
                        ? formatUsd(parseAmount(row.face_usd), locale)
                        : '-',
                  },
                  {
                    id: 'rate',
                    header: t('Rate'),
                    className: 'text-right',
                    cellClassName: 'text-right tabular-nums',
                    cell: (row) => formatCustomerRate(row.rate),
                  },
                  {
                    id: 'amount',
                    header: t('Commission'),
                    className: 'text-right',
                    cellClassName: 'text-right tabular-nums',
                    cell: (row) =>
                      formatUsd(parseAmount(row.amount_usd), locale),
                  },
                  {
                    id: 'status',
                    header: t('Status'),
                    cell: (row) => (
                      <StatusBadge
                        label={customerCommissionStatusLabel(t, row.status)}
                        variant={customerCommissionStatusVariant(row.status)}
                        copyable={false}
                      />
                    ),
                  },
                ]}
              />
            )}
            <Card>
              <CardContent className='pt-6'>
                <SiteCommissionPanel
                  legacyMode={false}
                  onConverted={() => {
                    void queryClient.invalidateQueries({
                      queryKey: siteReferralProfileKey,
                    })
                    void queryClient.invalidateQueries({
                      queryKey: siteCommissionsKey,
                    })
                    void queryClient.invalidateQueries({
                      queryKey: ['adty-topup', 'balance'],
                    })
                  }}
                />
              </CardContent>
            </Card>
          </div>
        </TabsContent>
        <TabsContent value='invitees' className='pt-3'>
          <ContentSection title={t('Invited users')}>
            {invites.length === 0 ? (
              <EmptyState title={t('No invited users yet')} />
            ) : (
              <StaticDataTable
                data={invites}
                getRowKey={(row) => String(row.invitee_id)}
                columns={[
                  {
                    id: 'invitee',
                    header: t('User'),
                    cell: (row) =>
                      formatUserIdentity(
                        row.invitee_id,
                        row.invitee_username
                      ),
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
          </ContentSection>
        </TabsContent>
        <TabsContent value='rules'>
          <Card>
            <CardContent className='py-4'>
              <ul className='text-muted-foreground list-disc space-y-2 pl-5 text-sm'>
                <li>
                  {t(
                    'Only direct one-level invites within this site qualify for commission.'
                  )}
                </li>
                <li>
                  {t(
                    'Commission is calculated only on direct invitees actual paid consumption (excludes bonuses, redemption codes, and manual adjustments).'
                  )}
                </li>
                <li>
                  {t(
                    'Commissions settle hourly on the hour with no freeze. Task refunds offset future commission earnings automatically; risk-flagged settlements are held for review.'
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
