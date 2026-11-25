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
import { Loader2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PasswordInput } from '@/components/password-input'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { teamRegistrationUrl } from '@/features/adty-team-registration/api'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import { formatQuota, formatTimestampToDate } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { adtyTopupQueryKeys, getAdtyTopupInfo, quoteAdtyTopup } from './api'
import { EpayPayDialog } from './components/epay-pay-dialog'
import { PaymentMethodButtons } from './components/payment-method-buttons'
import { UsdtPayDialog } from './components/usdt-pay-dialog'
import { paymentMethodKey } from './lib/methods'
import { formatPaymentAmount, formatUsd } from './lib/money'
import type { AdtyOrderExt } from './types'

function formatFixedPayAmount(raw: string): string {
  const num = Number(raw)
  if (!Number.isFinite(num)) return raw
  const trimmed = raw.trim()
  const dotIndex = trimmed.indexOf('.')
  if (dotIndex >= 0 && trimmed.length - dotIndex - 1 > 2) {
    return trimmed
  }
  return num.toFixed(2)
}

type TeamMember = {
  user_id: number
  username: string
  role: string
  status: string
  independent_employee?: boolean
}

type TeamKey = {
  token_id: number
  name: string
  user_id: number
  status: string
  binding_status?: string
}

const teamKeyStatusLabels: Record<string, string> = {
  active: 'Active',
  disabled: 'Disabled',
  expired: 'Expired',
  exhausted: 'Exhausted',
  deleted: 'Deleted',
  left: 'Revoked',
}

const memberStatusLabels: Record<string, string> = {
  active: 'Active',
  disabled: 'Disabled',
  left: 'Left the team',
  deleted: 'Deleted',
}

type TeamMine = {
  team_id: number
  name: string
  site_id?: number
  source_site_id?: number
  role: string
  independent_employee?: boolean
  discount?: string
  shared_quota: number
  status: string
  closed_at?: number
  members?: TeamMember[]
  keys?: TeamKey[]
}

type TeamInvite = {
  id: number
  created_by: number
  created_at: number
  expires_at: number
  used_by: number
  used_at: number
  revoked_by: number
  revoked_at: number
  status: string
}

const inviteStatusLabel: Record<string, string> = {
  outstanding: 'Outstanding',
  expired: 'Expired',
  used: 'Used',
  revoked: 'Revoked',
}

export function TeamPanel() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const mine = useQuery({
    queryKey: ['adty-team', 'mine'],
    queryFn: async () => {
      const response = await api.get('/api/adty/teams/mine')
      const body = response.data as {
        success?: boolean
        data?: { team?: TeamMine | null }
      }
      if (!body.success) throw new Error('Failed to load data')
      return body.data?.team ?? null
    },
  })
  const team = mine.data
  const isActive = team?.status === 'active'
  const [sceneKey, setSceneKey] = useState('')
  const topupInfo = useQuery({
    queryKey: [...adtyTopupQueryKeys.info, 'team_topup', team?.team_id],
    queryFn: () => getAdtyTopupInfo('team_topup', team?.team_id),
    enabled: team?.role === 'leader' && isActive,
  })
  const scenes = topupInfo.data?.scenes ?? []
  const scene = scenes.find((item) => item.key === sceneKey) ?? scenes[0]
  const topupMethods = (scene?.methods ?? []).filter(
    (method) => method.kind === 'epay' || method.kind === 'usdt'
  )
  const [rename, setRename] = useState('')
  const nameTooLong = [...rename.trim()].length > 64
  const [topupAmount, setTopupAmount] = useState('')
  const [topupMethodKey, setTopupMethodKey] = useState<string | null>(null)

  const [issued, setIssued] = useState('')
  const [employeeUsername, setEmployeeUsername] = useState('')
  const [employeePassword, setEmployeePassword] = useState('')
  const [deleteEmployee, setDeleteEmployee] = useState<TeamMember | null>(null)
  const [resetMember, setResetMember] = useState<TeamMember | null>(null)
  const [resetPassword, setResetPassword] = useState('')
  const [ttlHours, setTtlHours] = useState('24')
  const [tokenId, setTokenId] = useState('')
  const [confirmClose, setConfirmClose] = useState(false)
  const [confirmLeave, setConfirmLeave] = useState(false)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const currentUserId = useAuthStore((state) => state.auth.user?.id)
  const selfMember =
    team?.members?.find((member) => member.user_id === currentUserId) ??
    (team && team.role !== 'leader' && team.members?.length === 1
      ? team.members[0]
      : undefined)
  const independentEmployeeFlag =
    team?.independent_employee ?? selfMember?.independent_employee
  let leaveConfirmDescription = t(
    'Leaving the team revokes your bound team keys immediately. If this is an independent team employee account, it will be permanently disabled and cannot be recovered.'
  )
  if (independentEmployeeFlag === true) {
    leaveConfirmDescription = t(
      'After leaving the team, this independent employee account will be permanently disabled and cannot be recovered, and all bound team keys will be revoked immediately.'
    )
  } else if (independentEmployeeFlag === false) {
    leaveConfirmDescription = t(
      'After leaving the team, you will lose access to the team shared quota and your bound team keys will be revoked.'
    )
  }
  const topupMethod =
    topupMethods.find(
      (method) => paymentMethodKey(method) === topupMethodKey
    ) ?? null
  const parsedTopupAmount =
    topupAmount.trim() === '' ? Number.NaN : Number(topupAmount.trim())
  const minTeamTopup = Math.max(
    topupInfo.data?.min_topup ?? 1,
    Number(scene?.min_usd ?? 0)
  )
  const maxTeamTopup = Number(scene?.max_usd ?? 0)
  const validTopupAmount =
    Number.isSafeInteger(parsedTopupAmount) &&
    parsedTopupAmount > 0 &&
    parsedTopupAmount >= minTeamTopup &&
    (maxTeamTopup <= 0 || parsedTopupAmount <= maxTeamTopup)

  const teamQuoteQuery = useQuery({
    queryKey: [
      'adty-topup',
      'quote',
      'team_topup',
      team?.team_id,
      scene?.key ?? '',
      parsedTopupAmount,
      topupMethodKey,
    ],
    queryFn: () =>
      quoteAdtyTopup(
        parsedTopupAmount,
        topupMethod?.id ?? '',
        scene?.key ?? 'wallet_topup',
        topupMethod?.channel_id,
        'team_topup',
        team?.team_id
      ),
    enabled:
      team?.role === 'leader' &&
      isActive &&
      validTopupAmount &&
      topupMethod !== null &&
      topupMethod.available,
    meta: { errorToast: false },
  })
  const teamQuote = teamQuoteQuery.data?.quote
  let isTeamUsdtChannel = false
  if (teamQuote) {
    isTeamUsdtChannel = teamQuote.pay_currency === 'USDT'
  } else if (topupMethod) {
    isTeamUsdtChannel =
      topupMethod.kind === 'usdt' ||
      topupMethod.id.toLowerCase().startsWith('usdt') ||
      topupMethod.currency === 'USDT'
  } else {
    isTeamUsdtChannel =
      topupMethods.length > 0 &&
      topupMethods.every((item) => item.kind === 'usdt')
  }
  const fallbackCnyRate = Number(topupInfo.data?.price_cny_per_usd ?? 0)
  const hasEstimatedTeamPay =
    validTopupAmount &&
    topupMethod !== null &&
    (Boolean(teamQuote) ||
      isTeamUsdtChannel ||
      (Number.isFinite(fallbackCnyRate) && fallbackCnyRate > 0))
  let teamPayAmountText = isTeamUsdtChannel ? '0.00 USDT' : '¥0.00'
  let teamPayRateHint = ''
  if (validTopupAmount && topupMethod !== null) {
    if (teamQuote) {
      if (teamQuote.pay_currency === 'USDT') {
        teamPayAmountText = `${formatFixedPayAmount(teamQuote.pay_amount)} USDT`
        teamPayRateHint = t('1 USDT = 1 USD')
      } else if (teamQuote.pay_currency === 'CNY') {
        teamPayAmountText = `¥${Number(teamQuote.pay_amount).toFixed(2)}`
        teamPayRateHint = t('1 USD = {{rate}} CNY', {
          rate: Number(teamQuote.price_cny_per_usd).toFixed(2),
        })
      } else {
        teamPayAmountText = formatPaymentAmount(
          teamQuote.pay_amount,
          teamQuote.pay_currency,
          locale
        )
      }
    } else if (isTeamUsdtChannel) {
      teamPayAmountText = `${parsedTopupAmount.toFixed(2)} USDT`
      teamPayRateHint = t('1 USDT = 1 USD')
    } else if (Number.isFinite(fallbackCnyRate) && fallbackCnyRate > 0) {
      teamPayAmountText = `¥${(parsedTopupAmount * fallbackCnyRate).toFixed(2)}`
      teamPayRateHint = t('1 USD = {{rate}} CNY', {
        rate: fallbackCnyRate.toFixed(2),
      })
    }
  }

  const refresh = () =>
    void queryClient.invalidateQueries({ queryKey: ['adty-team'] })
  const [checkout, setCheckout] = useState<AdtyOrderExt | null>(null)
  const attempt = useRef({ body: '', key: '' })
  const pendingOrders = useQuery({
    queryKey: ['adty-team', 'pending-orders', team?.team_id],
    enabled: team?.role === 'leader' && isActive,
    queryFn: async () => {
      const { data } = await api.get<{
        success: boolean
        data: { items: AdtyOrderExt[] }
      }>(`/api/adty/teams/${team?.team_id}/topups`, {
        params: { status: 'pending' },
      })
      return requireServerSuccess(data).data.items ?? []
    },
  })
  const employeeLink = issued
    ? `${teamRegistrationUrl(issued)}?kind=employee`
    : ''
  const invites = useQuery({
    queryKey: ['adty-team', 'invites', team?.team_id],
    enabled: team?.role === 'leader' && isActive && Boolean(team?.team_id),
    queryFn: async () => {
      const teamId = team?.team_id
      if (!teamId) return []
      const response = await api.get(`/api/adty/teams/${teamId}/invites`)
      const body = response.data as {
        success?: boolean
        data?: { items?: TeamInvite[] }
      }
      if (!body.success) throw new Error('Failed to load data')
      return body.data?.items ?? []
    },
  })
  useEffect(() => {
    setRename(team?.name ?? '')
  }, [team?.name])

  const issueInvite = async () => {
    if (!team) return
    const hours = Number(ttlHours)
    if (!Number.isFinite(hours) || hours <= 0) {
      setError(t('Invalid parameters'))
      return
    }
    setPending(true)
    setError('')
    try {
      const response = await api.post(
        `/api/adty/teams/${team.team_id}/invites`,
        {
          ttl_seconds: Math.round(hours * 3600),
        }
      )
      const body = response.data as {
        success?: boolean
        message?: string
        data?: { code?: string }
      }
      if (!body.success || !body.data?.code) {
        setError(t(body.message || 'Failed to save'))
        return
      }
      setIssued(body.data.code)
      void queryClient.invalidateQueries({ queryKey: ['adty-team', 'invites'] })
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const run = async (action: () => Promise<void>) => {
    setPending(true)
    setError('')
    try {
      await action()
      refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  return (
    <>
      <Card>
        <CardContent className='flex flex-col gap-4 py-4'>
          <div className='flex flex-col gap-1'>
            <h2 className='text-sm font-medium'>{t('Team')}</h2>
            <p className='text-muted-foreground text-sm'>
              {t(
                'A team key spends the shared balance. The discount applies only to that key.'
              )}
            </p>
          </div>
          {mine.isPending && <LoadingState />}
          {mine.isError && <ErrorState onRetry={() => void mine.refetch()} />}
          {mine.isSuccess && !team && (
            <EmptyState
              title={t('You are not in a team')}
              description={t(
                'Only the platform Root administrator can create teams or issue team registration links.'
              )}
            />
          )}
          {team && !isActive && (
            <Alert>
              <AlertDescription>
                {t(
                  'This team is closed. Members, keys and remaining balance are kept read-only for history.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {team && (
            <dl className='grid grid-cols-2 gap-3 text-sm'>
              <div className='flex flex-col gap-1'>
                <dt className='text-muted-foreground'>{t('Name')}</dt>
                <dd>{team.name}</dd>
              </div>
              <div className='flex flex-col gap-1'>
                <dt className='text-muted-foreground'>{t('Status')}</dt>
                <dd>{isActive ? t('Active') : t('Closed')}</dd>
              </div>
              <div className='flex flex-col gap-1'>
                <dt className='text-muted-foreground'>{t('Shared quota')}</dt>
                <dd className='tabular-nums'>
                  {formatQuota(team.shared_quota)}
                </dd>
              </div>
              {(team.site_id ?? 0) === 0 && Boolean(team.discount?.trim()) && (
                <div className='flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>
                    {t('Post-discount payment ratio')}
                  </dt>
                  <dd className='tabular-nums'>{team.discount}</dd>
                </div>
              )}
              <div className='flex flex-col gap-1'>
                <dt className='text-muted-foreground'>{t('Role')}</dt>
                <dd>{team.role === 'leader' ? t('Leader') : t('Member')}</dd>
              </div>
              {!isActive && Boolean(team.closed_at) && (
                <div className='flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>{t('Closed at')}</dt>
                  <dd className='tabular-nums'>
                    {formatTimestampToDate(team.closed_at ?? 0)}
                  </dd>
                </div>
              )}
            </dl>
          )}
          {team?.role === 'leader' && !isActive && (
            <div className='flex flex-col gap-2'>
              <h3 className='text-sm font-medium'>{t('Members')}</h3>
              {(team.members ?? []).map((member) => (
                <div
                  key={member.user_id}
                  className='flex flex-wrap items-center justify-between gap-3 text-sm'
                >
                  <span className='min-w-0 truncate'>
                    {formatUserIdentity(member.user_id, member.username)} ·{' '}
                    {member.role === 'leader' ? t('Leader') : t('Member')} ·{' '}
                    {t(memberStatusLabels[member.status] ?? member.status)}
                  </span>
                </div>
              ))}
            </div>
          )}
          {team?.role === 'leader' && isActive && (
            <div className='flex flex-col gap-2'>
              <div className='flex flex-col gap-1'>
                <Label htmlFor='team-rename'>{t('Team name')}</Label>
                <Input
                  id='team-rename'
                  aria-invalid={nameTooLong}
                  aria-describedby={
                    nameTooLong ? 'team-rename-error' : undefined
                  }
                  value={rename}
                  onChange={(event) => setRename(event.target.value)}
                />
                {nameTooLong && (
                  <p id='team-rename-error' role='alert'>
                    {t('Team name must contain 1 to 64 characters')}
                  </p>
                )}
              </div>
              <Button
                type='button'
                variant='outline'
                disabled={
                  pending ||
                  rename.trim() === '' ||
                  rename.trim() === team.name ||
                  nameTooLong
                }
                onClick={() =>
                  void run(async () => {
                    const response = await api.post(
                      `/api/adty/teams/${team.team_id}/profile`,
                      { name: rename.trim() }
                    )
                    if (!response.data?.success) {
                      throw new Error(
                        response.data?.message || 'Failed to save'
                      )
                    }
                  })
                }
              >
                {t('Rename')}
              </Button>
              <div className='flex flex-col gap-3'>
                <div className='flex flex-col gap-1'>
                  <Label htmlFor='team-topup'>{t('Team top-up')}</Label>
                  <Input
                    id='team-topup'
                    value={topupAmount}
                    inputMode='decimal'
                    autoComplete='off'
                    placeholder={t('At least ${{amount}}', {
                      amount: minTeamTopup,
                    })}
                    onChange={(event) => setTopupAmount(event.target.value)}
                  />
                </div>
                {scenes.length > 1 && (
                  <div className='flex flex-col gap-1'>
                    <Label htmlFor='team-payment-scene'>
                      {t('Payment scene')}
                    </Label>
                    <NativeSelect
                      id='team-payment-scene'
                      value={scene?.key ?? ''}
                      onChange={(e) => {
                        setSceneKey(e.target.value)
                        setTopupMethodKey(null)
                      }}
                    >
                      {scenes.map((item) => (
                        <NativeSelectOption key={item.key} value={item.key}>
                          {t(item.name)}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </div>
                )}
                <div className='flex flex-col gap-2'>
                  <Label>{t('Payment method')}</Label>
                  <PaymentMethodButtons
                    methods={topupMethods}
                    selected={topupMethodKey}
                    onSelect={setTopupMethodKey}
                    disabled={pending}
                  />
                </div>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'A team top-up does not include a payment-method gift. The order stays pending until payment completion credits the shared balance.'
                  )}
                </p>
                <section
                  className='bg-muted/40 flex flex-col gap-3 rounded-xl border p-4 sm:p-5'
                  aria-live='polite'
                >
                  <div className='flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between'>
                    <div className='grid flex-1 gap-3 sm:grid-cols-2 sm:gap-6'>
                      <div className='flex flex-col gap-1'>
                        <span className='text-muted-foreground text-xs font-medium'>
                          {t('Estimated Credit')}
                        </span>
                        <span
                          className={cn(
                            'text-xl font-semibold tracking-tight tabular-nums',
                            validTopupAmount
                              ? 'text-foreground'
                              : 'text-muted-foreground'
                          )}
                          data-testid='team-topup-credit'
                        >
                          {formatUsd(
                            validTopupAmount ? parsedTopupAmount : 0,
                            locale
                          )}
                        </span>
                      </div>
                      <div className='flex flex-col gap-1 sm:border-l sm:pl-6'>
                        <div className='flex items-center gap-2'>
                          <span className='text-muted-foreground text-xs font-medium'>
                            {t('Amount Due')}
                          </span>
                          {teamQuoteQuery.isFetching && (
                            <span className='text-muted-foreground inline-flex items-center gap-1 text-xs'>
                              <Loader2
                                className='size-3 animate-spin'
                                aria-hidden='true'
                              />
                              {t('Calculating...')}
                            </span>
                          )}
                        </div>
                        <div className='flex flex-wrap items-baseline gap-2'>
                          <span
                            className={cn(
                              'text-xl font-semibold tracking-tight tabular-nums',
                              hasEstimatedTeamPay
                                ? 'text-foreground'
                                : 'text-muted-foreground'
                            )}
                            data-testid='team-topup-pay'
                          >
                            {teamPayAmountText}
                          </span>
                          {teamPayRateHint && (
                            <span className='text-muted-foreground text-xs tabular-nums'>
                              {teamPayRateHint}
                            </span>
                          )}
                        </div>
                      </div>
                    </div>
                    <Button
                      type='button'
                      variant='brand'
                      className='w-full shrink-0 sm:w-auto sm:min-w-28'
                      disabled={
                        pending || !validTopupAmount || topupMethod === null
                      }
                      onClick={() =>
                        void run(async () => {
                          if (!topupMethod) return
                          const body = JSON.stringify([
                            team.team_id,
                            topupAmount.trim(),
                            scene?.key,
                            topupMethodKey,
                          ])
                          if (attempt.current.body !== body) {
                            attempt.current = { body, key: crypto.randomUUID() }
                          }
                          const response = await api.post(
                            `/api/adty/teams/${team.team_id}/topups`,
                            {
                              amount_usd: topupAmount.trim(),
                              payment_scene: scene?.key,
                              method: topupMethod.id,
                              channel_id: topupMethod.channel_id,
                            },
                            {
                              headers: {
                                'Idempotency-Key': attempt.current.key,
                              },
                            }
                          )
                          if (!response.data?.success) {
                            throw new Error(
                              response.data?.message || 'Failed to save'
                            )
                          }
                          const order = response.data.data as AdtyOrderExt
                          if (!order?.trade_no) {
                            throw new Error('Order number not provided')
                          }
                          setCheckout(order)
                          attempt.current = { body: '', key: '' }
                          setTopupAmount('')
                        })
                      }
                    >
                      {pending ? t('Processing...') : t('Team top-up')}
                    </Button>
                  </div>
                </section>
              </div>
              {pendingOrders.isError && (
                <ErrorState onRetry={() => void pendingOrders.refetch()} />
              )}
              {pendingOrders.data?.map((order) => {
                const hasCheckout = Boolean(
                  order.pay_address ||
                  order.pay_url ||
                  order.qr_code ||
                  order.url_scheme
                )
                if (
                  !hasCheckout &&
                  (!order.method ||
                    order.method === 'team' ||
                    (order.provider === 'adty_epay' && !order.channel_id))
                ) {
                  return (
                    <p
                      key={order.trade_no}
                      className='text-muted-foreground text-sm'
                    >
                      {order.trade_no} ·{' '}
                      {t('This order has no payment channel')}
                    </p>
                  )
                }
                return (
                  <Button
                    key={order.trade_no}
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => {
                      if (hasCheckout) {
                        setCheckout(order)
                        return
                      }
                      void run(async () => {
                        const response = await api.post(
                          `/api/adty/teams/${team.team_id}/topups/${encodeURIComponent(order.trade_no)}/resume`
                        )
                        setCheckout(
                          requireServerSuccess(response.data)
                            .data as AdtyOrderExt
                        )
                      })
                    }}
                  >
                    {t('Continue payment')} · {order.trade_no}
                  </Button>
                )
              })}
              <div className='flex flex-col gap-1'>
                <Label htmlFor='team-invite-ttl'>{t('Validity (hours)')}</Label>
                <Input
                  id='team-invite-ttl'
                  value={ttlHours}
                  inputMode='decimal'
                  autoComplete='off'
                  onChange={(event) => setTtlHours(event.target.value)}
                />
              </div>
              <Button
                type='button'
                variant='outline'
                disabled={pending}
                onClick={() => void issueInvite()}
              >
                {t('Create invite')}
              </Button>
              {issued && (
                <div className='flex flex-col gap-1'>
                  <Label htmlFor='employee-registration-link'>
                    {t('Employee registration link')}
                  </Label>
                  <div className='flex min-w-0 items-center gap-2'>
                    <Input
                      id='employee-registration-link'
                      readOnly
                      value={employeeLink}
                    />
                    <CopyButton
                      value={employeeLink}
                      tooltip={t('Copy employee registration link')}
                    />
                  </div>
                </div>
              )}
              <p className='text-muted-foreground text-sm'>
                {t('The invite code is shown once')}
              </p>
              <div className='flex flex-col gap-2'>
                <h3 className='text-sm font-medium'>{t('Invites')}</h3>
                {(invites.data ?? []).length === 0 && (
                  <p className='text-muted-foreground text-sm'>
                    {t('No invites yet')}
                  </p>
                )}
                {(invites.data ?? []).map((row) => (
                  <div
                    key={row.id}
                    className='flex items-center justify-between gap-3 text-sm'
                  >
                    <span className='min-w-0 truncate'>
                      #{row.id} ·{' '}
                      {t(inviteStatusLabel[row.status] ?? row.status)} ·{' '}
                      {t('Validity')}:{' '}
                      {row.expires_at
                        ? new Date(row.expires_at * 1000).toLocaleString(locale)
                        : '—'}
                    </span>
                    {row.status === 'outstanding' && (
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        disabled={pending}
                        onClick={() =>
                          void run(async () => {
                            const response = await api.post(
                              `/api/adty/teams/${team.team_id}/invites/${row.id}/revoke`
                            )
                            if (!response.data?.success) {
                              throw new Error(
                                response.data?.message || 'Failed to save'
                              )
                            }
                          })
                        }
                      >
                        {t('Revoke invite')}
                      </Button>
                    )}
                  </div>
                ))}
              </div>
              <div className='flex flex-col gap-2'>
                <h3 className='text-sm font-medium'>{t('Members')}</h3>
                <form
                  className='grid gap-3 sm:grid-cols-2'
                  onSubmit={(event) => {
                    event.preventDefault()
                    if (
                      pending ||
                      !employeeUsername.trim() ||
                      !employeePassword
                    ) {
                      return
                    }
                    void run(async () => {
                      const response = await api.post(
                        `/api/adty/teams/${team.team_id}/employees`,
                        {
                          username: employeeUsername.trim(),
                          password: employeePassword,
                        }
                      )
                      if (!response.data?.success) {
                        throw new Error(
                          response.data?.message || 'Failed to save'
                        )
                      }
                      setEmployeeUsername('')
                      setEmployeePassword('')
                    })
                  }}
                >
                  <div className='flex flex-col gap-1'>
                    <Label htmlFor='team-employee-username'>
                      {t('Employee username')}
                    </Label>
                    <Input
                      id='team-employee-username'
                      autoComplete='off'
                      maxLength={20}
                      value={employeeUsername}
                      onChange={(event) =>
                        setEmployeeUsername(event.target.value)
                      }
                    />
                  </div>
                  <div className='flex flex-col gap-1'>
                    <Label htmlFor='team-employee-password'>
                      {t('Employee password')}
                    </Label>
                    <PasswordInput
                      id='team-employee-password'
                      autoComplete='new-password'
                      value={employeePassword}
                      onChange={(event) =>
                        setEmployeePassword(event.target.value)
                      }
                    />
                  </div>
                  <Button
                    type='submit'
                    variant='outline'
                    disabled={
                      pending || !employeeUsername.trim() || !employeePassword
                    }
                  >
                    {t('Create employee')}
                  </Button>
                </form>
                {(team.members ?? []).map((member) => (
                  <div
                    key={member.user_id}
                    className='flex flex-wrap items-center justify-between gap-3 text-sm'
                  >
                    <span className='min-w-0 truncate'>
                      {formatUserIdentity(member.user_id, member.username)} ·{' '}
                      {member.role === 'leader' ? t('Leader') : t('Member')} ·{' '}
                      {t(memberStatusLabels[member.status] ?? member.status)}
                    </span>
                    <div className='flex flex-wrap gap-1.5'>
                      {member.role !== 'leader' && member.status === 'active' && (
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          disabled={pending}
                          onClick={() =>
                            void run(async () => {
                              const response = await api.post(
                                `/api/adty/teams/${team.team_id}/members/${member.user_id}/disable`
                              )
                              if (!response.data?.success) {
                                throw new Error(
                                  response.data?.message || 'Failed to save'
                                )
                              }
                            })
                          }
                        >
                          {t('Disable member')}
                        </Button>
                      )}
                      {member.role !== 'leader' &&
                        member.independent_employee &&
                        member.status === 'disabled' && (
                          <Button
                            type='button'
                            variant='outline'
                            size='sm'
                            disabled={pending}
                            onClick={() =>
                              void run(async () => {
                                const response = await api.post(
                                  `/api/adty/teams/${team.team_id}/members/${member.user_id}/enable`
                                )
                                if (!response.data?.success) {
                                  throw new Error(
                                    response.data?.message || 'Failed to save'
                                  )
                                }
                              })
                            }
                          >
                            {t('Enable employee')}
                          </Button>
                        )}
                      {member.role !== 'leader' &&
                        member.independent_employee &&
                        member.status !== 'deleted' && (
                          <>
                            <Button
                              type='button'
                              variant='outline'
                              size='sm'
                              disabled={pending}
                              onClick={() => {
                                setError('')
                                setResetPassword('')
                                setResetMember(member)
                              }}
                            >
                              {t('Reset password')}
                            </Button>
                            <Button
                              type='button'
                              variant='outline'
                              size='sm'
                              disabled={pending}
                              onClick={() => {
                                setError('')
                                setDeleteEmployee(member)
                              }}
                            >
                              {t('Delete employee')}
                            </Button>
                          </>
                        )}
                    </div>
                  </div>
                ))}
              </div>
              <div className='flex flex-col gap-1'>
                <Label htmlFor='leader-token'>{t('Token id')}</Label>
                <Input
                  id='leader-token'
                  value={tokenId}
                  inputMode='numeric'
                  onChange={(event) => setTokenId(event.target.value)}
                />
              </div>
              <Button
                type='button'
                variant='outline'
                disabled={pending || tokenId.trim() === ''}
                onClick={() =>
                  void run(async () => {
                    const response = await api.post(
                      `/api/adty/teams/${team.team_id}/keys`,
                      { token_id: Number(tokenId) }
                    )
                    if (!response.data?.success) {
                      throw new Error(
                        response.data?.message || 'Failed to save'
                      )
                    }
                    setTokenId('')
                  })
                }
              >
                {t('Bind key')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={pending}
                onClick={() => {
                  if (!confirmClose) {
                    setConfirmClose(true)
                    return
                  }
                  void run(async () => {
                    const response = await api.post(
                      `/api/adty/teams/${team.team_id}/close`
                    )
                    if (!response.data?.success) {
                      throw new Error(
                        response.data?.message || 'Failed to save'
                      )
                    }
                    setConfirmClose(false)
                  })
                }}
              >
                {confirmClose ? t('Confirm close') : t('Close team')}
              </Button>
            </div>
          )}
          {team && team.role !== 'leader' && isActive && (
            <div className='flex flex-col gap-2'>
              <div className='flex flex-col gap-1'>
                <Label htmlFor='team-token'>{t('Token id')}</Label>
                <Input
                  id='team-token'
                  value={tokenId}
                  inputMode='numeric'
                  onChange={(event) => setTokenId(event.target.value)}
                />
              </div>
              <Button
                type='button'
                variant='outline'
                disabled={pending || tokenId.trim() === ''}
                onClick={() =>
                  void run(async () => {
                    const response = await api.post(
                      `/api/adty/teams/${team.team_id}/keys`,
                      { token_id: Number(tokenId) }
                    )
                    if (!response.data?.success) {
                      throw new Error(
                        response.data?.message || 'Failed to save'
                      )
                    }
                    setTokenId('')
                  })
                }
              >
                {t('Bind key')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={pending}
                onClick={() => {
                  setError('')
                  setConfirmLeave(true)
                }}
              >
                {t('Leave team')}
              </Button>
            </div>
          )}
          {team && (
            <div className='flex flex-col gap-2'>
              <h3 className='text-sm font-medium'>{t('Team keys')}</h3>
              {(team.keys ?? []).length === 0 && (
                <p className='text-muted-foreground text-sm'>
                  {t('No team keys')}
                </p>
              )}
              {(team.keys ?? []).map((key) => (
                <div
                  key={key.token_id}
                  className='flex items-center justify-between gap-3 text-sm'
                >
                  <span className='min-w-0 truncate tabular-nums'>
                    {key.name || key.token_id} ·{' '}
                    {t(teamKeyStatusLabels[key.status] ?? key.status)}
                  </span>
                  {isActive &&
                    (key.binding_status ?? key.status) === 'active' &&
                    key.status !== 'deleted' && (
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        disabled={pending}
                        onClick={() =>
                          void run(async () => {
                            const response = await api.post(
                              `/api/adty/teams/${team.team_id}/keys/${key.token_id}/revoke`
                            )
                            if (!response.data?.success) {
                              throw new Error(
                                response.data?.message || 'Failed to save'
                              )
                            }
                          })
                        }
                      >
                        {t('Revoke key')}
                      </Button>
                    )}
                </div>
              ))}
            </div>
          )}
          {error && (
            <p role='alert' className='text-destructive text-sm'>
              {error}
            </p>
          )}
        </CardContent>
      </Card>
      {checkout &&
        (checkout.method.startsWith('usdt.') ? (
          <UsdtPayDialog
            key={checkout.trade_no}
            tradeNo={checkout.trade_no}
            chain={checkout.method}
            amount={Number(checkout.face_usd)}
            onClose={() => {
              setCheckout(null)
              refresh()
            }}
            onChangeChain={() => setCheckout(null)}
            onSettled={refresh}
          />
        ) : (
          <EpayPayDialog
            key={checkout.trade_no}
            tradeNo={checkout.trade_no}
            method={checkout.method}
            amount={Number(checkout.face_usd)}
            onClose={() => {
              setCheckout(null)
              refresh()
            }}
            onSettled={refresh}
          />
        ))}
      <Dialog
        open={resetMember !== null}
        onOpenChange={(open) => {
          if (!open && !pending) {
            setResetMember(null)
            setResetPassword('')
            setError('')
          }
        }}
        title={t('Reset employee password')}
        description={t(
          'Set a new password for {{name}}. Their existing sign-in sessions will be revoked immediately.',
          {
            name: resetMember
              ? formatUserIdentity(resetMember.user_id, resetMember.username)
              : '',
          }
        )}
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              disabled={pending}
              onClick={() => {
                setResetMember(null)
                setResetPassword('')
              }}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              variant='brand'
              disabled={pending || !resetPassword}
              onClick={() => {
                if (!team || !resetMember) return
                const userId = resetMember.user_id
                void run(async () => {
                  const response = await api.post(
                    `/api/adty/teams/${team.team_id}/members/${userId}/password`,
                    { password: resetPassword }
                  )
                  requireServerSuccess(response.data)
                  setResetMember(null)
                  setResetPassword('')
                  toast.success(t('Password updated'))
                })
              }}
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        <div className='flex flex-col gap-2'>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='team-employee-reset-password'>
              {t('New password')}
            </Label>
            <PasswordInput
              id='team-employee-reset-password'
              autoComplete='new-password'
              value={resetPassword}
              onChange={(event) => setResetPassword(event.target.value)}
            />
          </div>
          {error && (
            <p role='alert' className='text-destructive text-sm'>
              {error}
            </p>
          )}
        </div>
      </Dialog>
      <ConfirmDialog
        open={confirmLeave}
        onOpenChange={(open) => {
          if (!open && !pending) {
            setConfirmLeave(false)
            setError('')
          }
        }}
        title={t('Leave team')}
        desc={leaveConfirmDescription}
        confirmText={t('Confirm leave')}
        destructive
        isLoading={pending}
        handleConfirm={() => {
          if (!team) return
          void run(async () => {
            const response = await api.post(
              `/api/adty/teams/${team.team_id}/leave`
            )
            if (!response.data?.success) {
              throw new Error(response.data?.message || 'Failed to save')
            }
            setConfirmLeave(false)
          })
        }}
      >
        {error && (
          <p role='alert' className='text-destructive text-sm'>
            {error}
          </p>
        )}
      </ConfirmDialog>
      <ConfirmDialog
        open={deleteEmployee !== null}
        onOpenChange={(open) => {
          if (!open && !pending) {
            setDeleteEmployee(null)
            setError('')
          }
        }}
        title={t('Delete employee')}
        desc={
          deleteEmployee
            ? formatUserIdentity(deleteEmployee.user_id, deleteEmployee.username)
            : ''
        }
        confirmText={t('Confirm delete')}
        destructive
        isLoading={pending}
        handleConfirm={() => {
          if (!team || !deleteEmployee) return
          const userId = deleteEmployee.user_id
          void run(async () => {
            const response = await api.post(
              `/api/adty/teams/${team.team_id}/members/${userId}/delete`
            )
            if (!response.data?.success) {
              throw new Error(response.data?.message || 'Failed to save')
            }
            setDeleteEmployee(null)
          })
        }}
      >
        {error && (
          <p role='alert' className='text-destructive text-sm'>
            {error}
          </p>
        )}
      </ConfirmDialog>
    </>
  )
}
