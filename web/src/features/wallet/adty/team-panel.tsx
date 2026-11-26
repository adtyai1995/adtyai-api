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
import { useNavigate } from '@tanstack/react-router'
import { Loader2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PasswordInput } from '@/components/password-input'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { labelOf, LEDGER_TYPE_LABELS } from '@/features/adty-team-admin/labels'
import { teamRegistrationUrl } from '@/features/adty-team-registration/api'
import { toIntlLocale } from '@/i18n/languages'
import { api, getSelf } from '@/lib/api'
import { formatQuota, formatTimestamp, formatTimestampToDate } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import {
  createServerError,
  getServerErrorMessage,
  getServerMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import {
  adtyTopupQueryKeys,
  cancelAdtyOrder,
  getAdtyOrder,
  getAdtyTopupInfo,
  quoteAdtyTopup,
} from './api'
import { EpayPayDialog } from './components/epay-pay-dialog'
import { PaymentMethodButtons } from './components/payment-method-buttons'
import { UsdtPayDialog } from './components/usdt-pay-dialog'
import { useNowSeconds } from './lib/countdown'
import { detectPayDevice } from './lib/device'
import {
  methodLabel,
  ORDER_STATUS_LABELS,
  ORDER_STATUS_VARIANTS,
  paymentMethodKey,
} from './lib/methods'
import { formatPaymentAmount, formatUsd } from './lib/money'
import type { AdtyOrderExt, AdtyOrderStatus } from './types'

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

type TeamTopupRow = AdtyOrderExt & {
  status?: AdtyOrderStatus | string
  created_at?: number
  create_time?: number
}

type TeamLedgerRow = {
  id: number
  team_id: number
  delta_quota: number
  balance_before: number
  balance_after: number
  type: string
  ref_id: string
  actor_user_id: number
  actor_username?: string
  created_at: number
}

const TEAM_LEDGER_PAGE_SIZE = 10

export function TeamPanel(
  props: { returnTradeNo?: string; onReturnClosed?: () => void } = {}
) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [dismissedReturnTradeNo, setDismissedReturnTradeNo] = useState<string>()
  const [ledgerPage, setLedgerPage] = useState(1)
  const returnTradeNo = props.returnTradeNo?.trim() || undefined
  const returning = Boolean(
    returnTradeNo && returnTradeNo !== dismissedReturnTradeNo
  )
  const returnOrderQuery = useQuery({
    queryKey: ['adty-team', 'return', returnTradeNo],
    enabled: returning,
    queryFn: () => getAdtyOrder(returnTradeNo ?? ''),
  })
  const mine = useQuery({
    queryKey: ['adty-team', 'mine'],
    queryFn: async () => {
      const response = await api.get('/api/adty/teams/mine')
      const body = response.data as {
        success?: boolean
        data?: { team?: TeamMine | null }
      }
      if (!body.success) throw createServerError(body, t('Failed to load data'))
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
  let topupAmountError = ''
  if (topupAmount.trim() !== '' && !validTopupAmount) {
    if (
      !Number.isFinite(parsedTopupAmount) ||
      !Number.isInteger(parsedTopupAmount)
    ) {
      topupAmountError = t('Enter a whole number in USD')
    } else if (parsedTopupAmount < minTeamTopup) {
      topupAmountError = t('At least ${{amount}}', { amount: minTeamTopup })
    } else if (maxTeamTopup > 0 && parsedTopupAmount > maxTeamTopup) {
      topupAmountError = t('At most ${{amount}}', { amount: maxTeamTopup })
    } else {
      topupAmountError = t('Enter a valid amount')
    }
  }

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
  const teamQuoteAvailable = teamQuoteQuery.data?.available ?? false
  const teamQuoteError = teamQuoteQuery.isError
    ? getServerErrorMessage(teamQuoteQuery.error, t('Failed to load data'))
    : ''
  let isTeamUsdtChannel = false
  if (teamQuote && teamQuoteAvailable) {
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
  const hasEstimatedTeamPay =
    validTopupAmount &&
    topupMethod !== null &&
    Boolean(teamQuote) &&
    teamQuoteAvailable &&
    !teamQuoteQuery.isError
  let teamPayAmountText = isTeamUsdtChannel ? '0.00 USDT' : '¥0.00'
  let teamPayRateHint = ''
  if (hasEstimatedTeamPay && teamQuote) {
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
  }

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['adty-team'] })
    void queryClient.invalidateQueries({ queryKey: ['adty-topup'] })
  }
  const [checkout, setCheckout] = useState<AdtyOrderExt | null>(null)
  const attempt = useRef({ body: '', key: '' })
  const pendingOrders = useQuery({
    queryKey: ['adty-team', 'pending-orders', team?.team_id],
    enabled: team?.role === 'leader' && isActive,
    queryFn: async () => {
      const { data } = await api.get<{
        success: boolean
        data: { items: TeamTopupRow[] }
      }>(`/api/adty/teams/${team?.team_id}/topups`, {
        params: { status: 'pending' },
      })
      return requireServerSuccess(data).data.items ?? []
    },
  })
  const teamTopups = useQuery({
    queryKey: ['adty-team', 'topup-history', team?.team_id],
    enabled: team?.role === 'leader' && Boolean(team?.team_id),
    queryFn: async () => {
      const { data } = await api.get<{
        success: boolean
        data: { items: TeamTopupRow[] }
      }>(`/api/adty/teams/${team?.team_id}/topups`)
      return requireServerSuccess(data).data.items ?? []
    },
  })
  const teamLedgers = useQuery({
    queryKey: ['adty-team', 'ledgers', team?.team_id, ledgerPage],
    enabled: team?.role === 'leader' && Boolean(team?.team_id),
    queryFn: async () => {
      const { data } = await api.get<{
        success: boolean
        data: {
          items: TeamLedgerRow[]
          total: number
          page: number
          page_size: number
        }
      }>(`/api/adty/teams/${team?.team_id}/ledgers`, {
        params: { p: ledgerPage, page_size: TEAM_LEDGER_PAGE_SIZE },
      })
      return requireServerSuccess(data).data
    },
  })
  const rawPendingOrders = pendingOrders.data ?? []
  const hasAnyPendingTeamTopup =
    rawPendingOrders.length > 0 ||
    (teamTopups.data ?? []).some((item) => item.status === 'pending')
  const nowSec = useNowSeconds(hasAnyPendingTeamTopup)
  const isOrderExpired = (order: TeamTopupRow) =>
    (order.expire_at ?? 0) > 0 && order.expire_at <= nowSec
  const activePendingOrders = rawPendingOrders.filter(
    (order) => !isOrderExpired(order)
  )
  const activePendingTradeNos = new Set(
    activePendingOrders.map((item) => item.trade_no)
  )
  const resolveTeamTopupStatus = (order: TeamTopupRow): AdtyOrderStatus => {
    if (order.status && order.status in ORDER_STATUS_LABELS) {
      if (order.status === 'pending' && isOrderExpired(order)) {
        return 'expired'
      }
      return order.status as AdtyOrderStatus
    }
    if (activePendingTradeNos.has(order.trade_no)) return 'pending'
    if ((order.refunded_at ?? 0) > 0) return 'refunded'
    if (Number(order.paid_amount) > 0) return 'success'
    if (isOrderExpired(order)) return 'expired'
    return 'canceled'
  }
  const returnedCheckout: AdtyOrderExt | null =
    returning && returnOrderQuery.data
      ? {
          ...(returnOrderQuery.data.ext ?? ({} as AdtyOrderExt)),
          trade_no: returnOrderQuery.data.trade_no,
          provider: returnOrderQuery.data.payment_provider,
          method: returnOrderQuery.data.payment_method,
          face_usd:
            returnOrderQuery.data.ext?.face_usd ??
            String(returnOrderQuery.data.amount),
        }
      : null
  const activeCheckout = checkout ?? returnedCheckout
  const activeGatewayReturn = !checkout && Boolean(returnedCheckout)
  const closeCheckout = () => {
    if (activeGatewayReturn && returnTradeNo) {
      setDismissedReturnTradeNo(returnTradeNo)
      props.onReturnClosed?.()
    }
    setCheckout(null)
    refresh()
  }
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
      if (!body.success) throw createServerError(body, t('Failed to load data'))
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
        setError(getServerMessage(body) || t('Failed to save'))
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
      const message = getServerErrorMessage(cause, t('Failed to save'))
      setError(message)
      toast.error(message)
    } finally {
      setPending(false)
    }
  }

  const resumeTeamOrder = (order: TeamTopupRow) => {
    if (!team || isOrderExpired(order)) return
    const hasCheckout = Boolean(
      order.pay_address || order.pay_url || order.qr_code || order.url_scheme
    )
    if (hasCheckout) {
      setCheckout(order)
      return
    }
    void run(async () => {
      const response = await api.post(
        `/api/adty/teams/${team.team_id}/topups/${encodeURIComponent(order.trade_no)}/resume`
      )
      setCheckout(requireServerSuccess(response.data).data as AdtyOrderExt)
    })
  }

  const cancelTeamOrder = (tradeNo: string) => {
    void run(async () => {
      await cancelAdtyOrder(tradeNo)
      toast.success(t('Order canceled'))
    })
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
          {returning && returnOrderQuery.isError && (
            <ErrorState onRetry={() => void returnOrderQuery.refetch()} />
          )}
          {mine.isPending && <LoadingState />}
          {mine.isError && <ErrorState onRetry={() => void mine.refetch()} />}
          {mine.isSuccess && !team && (
            <EmptyState
              title={t('You are not in a team')}
              description={t(
                'Platform administrators and site owners can create teams or issue team registration links.'
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
                      throw createServerError(
                        response.data,
                        t('Failed to save')
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
                    inputMode='numeric'
                    autoComplete='off'
                    aria-invalid={Boolean(topupAmountError)}
                    placeholder={t('At least ${{amount}}', {
                      amount: minTeamTopup,
                    })}
                    onChange={(event) => setTopupAmount(event.target.value)}
                  />
                  {topupAmountError && (
                    <p className='text-destructive text-xs' role='alert'>
                      {topupAmountError}
                    </p>
                  )}
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
                        pending ||
                        !validTopupAmount ||
                        topupMethod === null ||
                        !teamQuoteAvailable ||
                        teamQuoteQuery.isFetching ||
                        teamQuoteQuery.isError
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
                              device: detectPayDevice(),
                            },
                            {
                              headers: {
                                'Idempotency-Key': attempt.current.key,
                              },
                            }
                          )
                          if (!response.data?.success) {
                            throw createServerError(
                              response.data,
                              t('Failed to save')
                            )
                          }
                          const order = response.data.data as AdtyOrderExt
                          if (!order?.trade_no) {
                            throw new Error(t('Order number not provided'))
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
                  {teamQuoteQuery.data && !teamQuoteAvailable && (
                    <p className='text-destructive text-sm' role='alert'>
                      {t(
                        'This method cannot take this amount. Choose another method or amount.'
                      )}
                    </p>
                  )}
                  {teamQuoteError && (
                    <p className='text-destructive text-sm' role='alert'>
                      {teamQuoteError}
                    </p>
                  )}
                </section>
              </div>
              {pendingOrders.isError && (
                <ErrorState onRetry={() => void pendingOrders.refetch()} />
              )}
              {activePendingOrders.map((order) => {
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
                  <div
                    key={order.trade_no}
                    className='flex flex-wrap items-center gap-2'
                  >
                    <Button
                      type='button'
                      variant='outline'
                      disabled={pending}
                      onClick={() => resumeTeamOrder(order)}
                    >
                      {t('Continue payment')} · {order.trade_no}
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      disabled={pending}
                      onClick={() => cancelTeamOrder(order.trade_no)}
                    >
                      {t('Cancel')}
                    </Button>
                  </div>
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
                              throw createServerError(
                                response.data,
                                t('Failed to save')
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
                        throw createServerError(
                          response.data,
                          t('Failed to save')
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
                                throw createServerError(
                                  response.data,
                                  t('Failed to save')
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
                                  throw createServerError(
                                    response.data,
                                    t('Failed to save')
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
                        (member.status === 'active' ||
                          member.status === 'disabled') && (
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
                        )}
                      {member.role !== 'leader' &&
                        member.independent_employee &&
                        member.status !== 'deleted' && (
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
                      throw createServerError(
                        response.data,
                        t('Failed to save')
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
                      throw createServerError(
                        response.data,
                        t('Failed to save')
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
                      throw createServerError(
                        response.data,
                        t('Failed to save')
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
                              throw createServerError(
                                response.data,
                                t('Failed to save')
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
          {team?.role === 'leader' && (
            <div className='flex flex-col gap-3'>
              <h3 className='text-sm font-medium'>{t('Team top-up history')}</h3>
              {teamTopups.isError && (
                <ErrorState onRetry={() => void teamTopups.refetch()} />
              )}
              {!teamTopups.isError && (
                <StaticDataTable
                  tableClassName='min-w-[680px]'
                  tableProps={{ 'aria-label': t('Team top-up history') }}
                  data={teamTopups.data ?? []}
                  getRowKey={(row) => row.trade_no}
                  emptyContent={
                    <EmptyState
                      className='min-h-24'
                      title={
                        teamTopups.isLoading
                          ? t('Loading...')
                          : t('No top-ups yet')
                      }
                    />
                  }
                  columns={[
                    {
                      id: 'time',
                      header: t('Time'),
                      cell: (row) => {
                        const ts = row.created_at ?? row.create_time ?? 0
                        return ts > 0 ? formatTimestamp(ts) : '—'
                      },
                    },
                    {
                      id: 'method',
                      header: t('Payment method'),
                      cell: (row) =>
                        row.method_label || t(methodLabel(row.method)),
                    },
                    {
                      id: 'amount',
                      header: t('Amount'),
                      className: 'text-right',
                      cellClassName: 'text-right tabular-nums',
                      cell: (row) => formatUsd(Number(row.face_usd), locale),
                    },
                    {
                      id: 'paid',
                      header: t('Paid'),
                      className: 'text-right',
                      cellClassName: 'text-right tabular-nums',
                      cell: (row) => {
                        const paid = Number(row.paid_amount)
                        const val = paid > 0 ? row.paid_amount : row.pay_amount
                        const curr = row.paid_currency || row.pay_currency
                        return val && curr ? `${val} ${curr}` : '—'
                      },
                    },
                    {
                      id: 'status',
                      header: t('Status'),
                      cell: (row) => {
                        const rowStatus = resolveTeamTopupStatus(row)
                        return (
                          <StatusBadge
                            label={t(
                              ORDER_STATUS_LABELS[rowStatus] ?? rowStatus
                            )}
                            variant={
                              ORDER_STATUS_VARIANTS[rowStatus] ?? 'neutral'
                            }
                            copyable={false}
                          />
                        )
                      },
                    },
                    {
                      id: 'trade_no',
                      header: t('Order number'),
                      cell: (row) => (
                        <code className='font-mono text-xs'>
                          {row.trade_no}
                        </code>
                      ),
                    },
                    {
                      id: 'actions',
                      header: t('Actions'),
                      className: 'text-right',
                      cellClassName: 'text-right',
                      cell: (row) => {
                        const rowStatus = resolveTeamTopupStatus(row)
                        const actionable =
                          isActive &&
                          rowStatus === 'pending' &&
                          (!row.expire_at || row.expire_at > nowSec)
                        if (!actionable) return null
                        const hasCheckout = Boolean(
                          row.pay_address ||
                            row.pay_url ||
                            row.qr_code ||
                            row.url_scheme
                        )
                        const canResume =
                          hasCheckout ||
                          (Boolean(row.method) &&
                            row.method !== 'team' &&
                            !(row.provider === 'adty_epay' && !row.channel_id))
                        return (
                          <div className='flex items-center justify-end gap-1.5 whitespace-nowrap'>
                            {canResume && (
                              <Button
                                type='button'
                                size='sm'
                                disabled={pending}
                                onClick={() => resumeTeamOrder(row)}
                              >
                                {t('Continue payment')}
                              </Button>
                            )}
                            <Button
                              type='button'
                              size='sm'
                              variant='outline'
                              disabled={pending}
                              onClick={() => cancelTeamOrder(row.trade_no)}
                            >
                              {t('Cancel')}
                            </Button>
                          </div>
                        )
                      },
                    },
                  ]}
                />
              )}
            </div>
          )}
          {team?.role === 'leader' && (
            <div className='flex flex-col gap-3'>
              <h3 className='text-sm font-medium'>
                {t('Shared balance ledger')}
              </h3>
              {teamLedgers.isError && (
                <ErrorState onRetry={() => void teamLedgers.refetch()} />
              )}
              {!teamLedgers.isError && (
                <>
                  <StaticDataTable
                    tableClassName='min-w-[600px]'
                    tableProps={{ 'aria-label': t('Shared balance ledger') }}
                    data={teamLedgers.data?.items ?? []}
                    getRowKey={(row) => String(row.id)}
                    emptyContent={
                      <EmptyState
                        className='min-h-24'
                        title={
                          teamLedgers.isLoading
                            ? t('Loading...')
                            : t('No ledger entries yet')
                        }
                      />
                    }
                    columns={[
                      {
                        id: 'time',
                        header: t('Time'),
                        cell: (row) =>
                          row.created_at > 0
                            ? formatTimestamp(row.created_at)
                            : '—',
                      },
                      {
                        id: 'type',
                        header: t('Type'),
                        cell: (row) =>
                          labelOf(t, LEDGER_TYPE_LABELS, row.type),
                      },
                      {
                        id: 'delta',
                        header: t('Change'),
                        className: 'text-right',
                        cellClassName: 'text-right tabular-nums',
                        cell: (row) =>
                          `${row.delta_quota > 0 ? '+' : ''}${formatQuota(row.delta_quota)}`,
                      },
                      {
                        id: 'balance_after',
                        header: t('Balance after'),
                        className: 'text-right',
                        cellClassName: 'text-right tabular-nums',
                        cell: (row) => formatQuota(row.balance_after),
                      },
                      {
                        id: 'ref',
                        header: t('Reference'),
                        cell: (row) => (
                          <code className='font-mono text-xs'>
                            {row.ref_id || '—'}
                          </code>
                        ),
                      },
                    ]}
                  />
                  {Math.ceil(
                    (teamLedgers.data?.total ?? 0) / TEAM_LEDGER_PAGE_SIZE
                  ) > 1 && (
                    <div className='flex items-center justify-end gap-2'>
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        disabled={ledgerPage <= 1}
                        onClick={() => setLedgerPage((p) => p - 1)}
                      >
                        {t('Previous')}
                      </Button>
                      <span className='text-muted-foreground text-sm'>
                        {ledgerPage} /{' '}
                        {Math.ceil(
                          (teamLedgers.data?.total ?? 0) / TEAM_LEDGER_PAGE_SIZE
                        )}
                      </span>
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        disabled={
                          ledgerPage >=
                          Math.ceil(
                            (teamLedgers.data?.total ?? 0) /
                              TEAM_LEDGER_PAGE_SIZE
                          )
                        }
                        onClick={() => setLedgerPage((p) => p + 1)}
                      >
                        {t('Next')}
                      </Button>
                    </div>
                  )}
                </>
              )}
            </div>
          )}
          {error && (
            <p role='alert' className='text-destructive text-sm'>
              {error}
            </p>
          )}
        </CardContent>
      </Card>
      {activeCheckout &&
        (activeCheckout.provider === 'bepusdt' ||
        activeCheckout.method.startsWith('usdt.') ? (
          <UsdtPayDialog
            key={activeCheckout.trade_no}
            tradeNo={activeCheckout.trade_no}
            chain={activeCheckout.method}
            amount={Number(activeCheckout.face_usd)}
            onClose={closeCheckout}
            onChangeChain={closeCheckout}
            onSettled={refresh}
          />
        ) : (
          <EpayPayDialog
            key={activeCheckout.trade_no}
            tradeNo={activeCheckout.trade_no}
            method={activeCheckout.method}
            amount={Number(activeCheckout.face_usd)}
            gatewayReturn={activeGatewayReturn}
            onClose={closeCheckout}
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
              throw createServerError(response.data, t('Failed to save'))
            }
            setConfirmLeave(false)
            try {
              const selfRes = await getSelf()
              if (selfRes?.success && selfRes.data) {
                useAuthStore.getState().auth.setUser(selfRes.data as AuthUser)
              }
            } catch {
              // Independent employee sessions may already be revoked on leave.
            }
            void queryClient.invalidateQueries({ queryKey: ['user', 'self'] })
            void navigate({
              to: '/dashboard/$section',
              params: { section: 'overview' },
              replace: true,
            })
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
              throw createServerError(response.data, t('Failed to save'))
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
