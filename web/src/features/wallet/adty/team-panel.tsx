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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { teamRegistrationUrl } from '@/features/adty-team-registration/api'
import { api } from '@/lib/api'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { adtyTopupQueryKeys, getAdtyTopupInfo } from './api'
import { EpayPayDialog } from './components/epay-pay-dialog'
import { PaymentMethodButtons } from './components/payment-method-buttons'
import { UsdtPayDialog } from './components/usdt-pay-dialog'
import { paymentMethodKey } from './lib/methods'
import type { AdtyOrderExt } from './types'

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
}

type TeamMine = {
  team_id: number
  name: string
  role: string
  discount: string
  shared_quota: number
  status: string
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
  const { t } = useTranslation()
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
  const topupInfo = useQuery({
    queryKey: adtyTopupQueryKeys.info,
    queryFn: () => getAdtyTopupInfo(),
  })
  const topupMethods = (
    topupInfo.data?.scenes?.find((scene) => scene.key === 'wallet_topup')
      ?.methods ??
    topupInfo.data?.methods ??
    []
  ).filter((method) => method.face_usd === undefined)
  const [rename, setRename] = useState('')
  const [topupAmount, setTopupAmount] = useState('')
  const [topupMethodKey, setTopupMethodKey] = useState<string | null>(null)

  const [issued, setIssued] = useState('')
  const [employeeUsername, setEmployeeUsername] = useState('')
  const [employeePassword, setEmployeePassword] = useState('')
  const [deleteEmployee, setDeleteEmployee] = useState<TeamMember | null>(null)
  const [ttlHours, setTtlHours] = useState('24')
  const [tokenId, setTokenId] = useState('')
  const [confirmClose, setConfirmClose] = useState(false)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const topupMethod =
    topupMethods.find(
      (method) => paymentMethodKey(method) === topupMethodKey
    ) ?? null

  const refresh = () =>
    void queryClient.invalidateQueries({ queryKey: ['adty-team'] })
  const team = mine.data
  const [checkout, setCheckout] = useState<AdtyOrderExt | null>(null)
  const attempt = useRef({ body: '', key: '' })
  const pendingOrders = useQuery({
    queryKey: ['adty-team', 'pending-orders', team?.team_id],
    enabled: team?.role === 'leader',
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
    enabled: team?.role === 'leader' && Boolean(team?.team_id),
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
          {team && (
            <dl className='grid grid-cols-2 gap-3 text-sm'>
              <div className='flex flex-col gap-1'>
                <dt className='text-muted-foreground'>{t('Name')}</dt>
                <dd>{team.name}</dd>
              </div>
              <div className='flex flex-col gap-1'>
                <dt className='text-muted-foreground'>{t('Shared quota')}</dt>
                <dd className='tabular-nums'>{team.shared_quota}</dd>
              </div>
              <div className='flex flex-col gap-1'>
                <dt className='text-muted-foreground'>
                  {t('Post-discount payment ratio')}
                </dt>
                <dd className='tabular-nums'>{team.discount}</dd>
              </div>
              <div className='flex flex-col gap-1'>
                <dt className='text-muted-foreground'>{t('Role')}</dt>
                <dd>{team.role === 'leader' ? t('Leader') : t('Member')}</dd>
              </div>
            </dl>
          )}
          {team?.role === 'leader' && (
            <div className='flex flex-col gap-2'>
              <div className='flex flex-col gap-1'>
                <Label htmlFor='team-rename'>{t('Team name')}</Label>
                <Input
                  id='team-rename'
                  value={rename}
                  onChange={(event) => setRename(event.target.value)}
                />
              </div>
              <Button
                type='button'
                variant='outline'
                disabled={
                  pending || rename.trim() === '' || rename.trim() === team.name
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
              <div className='flex flex-col gap-1'>
                <Label htmlFor='team-topup'>{t('Team top-up')}</Label>
                <Input
                  id='team-topup'
                  value={topupAmount}
                  inputMode='decimal'
                  autoComplete='off'
                  onChange={(event) => setTopupAmount(event.target.value)}
                />
              </div>
              <div className='flex flex-col gap-1'>
                <Label>{t('Payment method')}</Label>
                <PaymentMethodButtons
                  methods={topupMethods}
                  selected={topupMethodKey}
                  onSelect={setTopupMethodKey}
                  disabled={pending}
                />
              </div>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'A team top-up does not include a payment-method gift. The order stays pending until payment completion credits the shared balance.'
                )}
              </p>
              <Button
                type='button'
                variant='outline'
                disabled={
                  pending || topupAmount.trim() === '' || topupMethod === null
                }
                onClick={() =>
                  void run(async () => {
                    if (!topupMethod) return
                    const body = JSON.stringify([
                      team.team_id,
                      topupAmount.trim(),
                      topupMethodKey,
                    ])
                    if (attempt.current.body !== body) {
                      attempt.current = { body, key: crypto.randomUUID() }
                    }
                    const response = await api.post(
                      `/api/adty/teams/${team.team_id}/topups`,
                      {
                        amount_usd: topupAmount.trim(),
                        method: topupMethod.id,
                        channel_id: topupMethod.channel_id,
                      },
                      { headers: { 'Idempotency-Key': attempt.current.key } }
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
                {t('Team top-up')}
              </Button>
              {pendingOrders.isError && (
                <ErrorState onRetry={() => void pendingOrders.refetch()} />
              )}
              {pendingOrders.data?.map((order) => (
                <Button
                  key={order.trade_no}
                  type='button'
                  variant='outline'
                  onClick={() => setCheckout(order)}
                >
                  {t('Continue payment')} · {order.trade_no}
                </Button>
              ))}
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
                        ? new Date(row.expires_at * 1000).toLocaleString()
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
                      {member.username || member.user_id} ·{' '}
                      {member.role === 'leader' ? t('Leader') : t('Member')} ·{' '}
                      {member.status}
                    </span>
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
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          disabled={pending}
                          onClick={() => setDeleteEmployee(member)}
                        >
                          {t('Delete employee')}
                        </Button>
                      )}
                  </div>
                ))}
              </div>
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
                      {key.name || key.token_id} · {key.status}
                    </span>
                    {key.status === 'active' && (
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
          {team && team.role !== 'leader' && (
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
                onClick={() =>
                  void run(async () => {
                    const response = await api.post(
                      `/api/adty/teams/${team.team_id}/leave`
                    )
                    if (!response.data?.success) {
                      throw new Error(
                        response.data?.message || 'Failed to save'
                      )
                    }
                  })
                }
              >
                {t('Leave team')}
              </Button>
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
      <ConfirmDialog
        open={deleteEmployee !== null}
        onOpenChange={(open) => {
          if (!open && !pending) setDeleteEmployee(null)
        }}
        title={t('Delete employee')}
        desc={deleteEmployee?.username || String(deleteEmployee?.user_id ?? '')}
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
      />
    </>
  )
}
