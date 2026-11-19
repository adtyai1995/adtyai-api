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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery } from '@tanstack/react-query'
import timezone from 'dayjs/plugin/timezone'
import utc from 'dayjs/plugin/utc'
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { getAdminBusinessTimezone } from '@/features/adty-site-admin/api'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import dayjs from '@/lib/dayjs'
import { formatQuota, parseQuotaFromDollars } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { adjustUserQuota, grantExpiringUserGift } from '../api'
import { USER_ROLE } from '../constants'
import { rootGiftFormSchema, type RootGiftFormValues } from '../lib/user-form'
import type { QuotaAdjustMode } from '../types'
import { useUsers } from './users-provider'

dayjs.extend(utc)
dayjs.extend(timezone)

interface UserQuotaDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  userId: number
  currentQuota: number
  currentLiveQuota?: number
  onSuccess: () => void
}

export function UserQuotaDialog(props: UserQuotaDialogProps) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<QuotaAdjustMode>('add')
  const [amount, setAmount] = useState('')
  const [loading, setLoading] = useState(false)
  // adty task 7.2: a reason is required; one idempotency key per dialog so a
  // retry after a network failure cannot apply the change twice.
  const [reason, setReason] = useState('')
  const [orderRef, setOrderRef] = useState('')
  const [fundingSource, setFundingSource] = useState('')
  const [batchKind, setBatchKind] = useState('')
  const [batchSource, setBatchSource] = useState('')
  const [idempotencyKey, setIdempotencyKey] = useState(() =>
    crypto.randomUUID()
  )
  const reasonLength = [...reason.trim()].length
  const reasonValid = reasonLength > 0 && reasonLength <= 200

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'

  const amountValue = Number.parseFloat(amount) || 0
  const quotaValue = parseQuotaFromDollars(Math.abs(amountValue))
  const overrideQuota = parseQuotaFromDollars(amountValue)
  const currentLiveQuota = props.currentLiveQuota ?? props.currentQuota
  const overrideLiveQuota =
    overrideQuota + (currentLiveQuota - props.currentQuota)
  const floorBlocked =
    (mode === 'override' && (overrideQuota < 0 || overrideLiveQuota < 0)) ||
    (mode === 'subtract' && currentLiveQuota - quotaValue < 0)
  const decreases =
    mode === 'subtract' ||
    (mode === 'override' && overrideQuota < props.currentQuota)
  const provenanceValid =
    orderRef.trim() !== '' &&
    orderRef.trim().length <= 64 &&
    (decreases
      ? batchKind !== '' && batchSource.trim() !== ''
      : fundingSource !== '')
  const amountValid =
    amount.trim() !== '' &&
    Number.isFinite(Number(amount)) &&
    (mode === 'override' ? amountValue >= 0 : amountValue > 0 && quotaValue > 0)

  const getPreviewText = () => {
    if (floorBlocked) return t('Adjustment cannot make the balance negative')
    const current = currentLiveQuota
    const val = quotaValue
    switch (mode) {
      case 'add':
        return `${t('Current quota')}: ${formatQuota(current)}  +${formatQuota(val)} = ${formatQuota(current + val)}`
      case 'subtract':
        return `${t('Current quota')}: ${formatQuota(current)}  -${formatQuota(val)} = ${formatQuota(current - val)}`
      case 'override': {
        return `${t('Current quota')}: ${formatQuota(current)} → ${formatQuota(overrideLiveQuota)}`
      }
      default:
        return ''
    }
  }

  const resetForm = () => {
    setAmount('')
    setMode('add')
    setReason('')
    setOrderRef('')
    setFundingSource('')
    setBatchKind('')
    setBatchSource('')
    setIdempotencyKey(crypto.randomUUID())
  }

  const handleConfirm = async () => {
    if (
      loading ||
      !reasonValid ||
      floorBlocked ||
      !provenanceValid ||
      !amountValid
    ) {
      return
    }
    if (!amount && mode !== 'override') return
    if (quotaValue <= 0 && mode !== 'override') return

    setLoading(true)
    try {
      const value =
        mode === 'override' ? parseQuotaFromDollars(amountValue) : quotaValue
      const result = await adjustUserQuota({
        id: props.userId,
        action: 'add_quota',
        mode,
        value: mode === 'override' ? value : Math.abs(value),
        reason: reason.trim(),
        idempotency_key: idempotencyKey,
        order_ref: orderRef.trim(),
        funding_source: decreases ? 'specified_batch' : fundingSource,
        batch_kind: decreases ? batchKind : '',
        batch_source: decreases ? batchSource.trim() : '',
      })
      if (result.success) {
        toast.success(t('Quota adjusted successfully'))
        resetForm()
        props.onOpenChange(false)
        props.onSuccess()
      } else {
        handleServerError(result, t('Failed to adjust quota'))
      }
    } catch (e: unknown) {
      handleServerError(e, t('Failed to adjust quota'))
    } finally {
      setLoading(false)
    }
  }

  const handleCancel = () => {
    resetForm()
    props.onOpenChange(false)
  }

  const placeholder = tokensOnly
    ? t('Enter amount in tokens')
    : t('Enter amount in {{currency}}', { currency: currencyLabel })

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Adjust Quota')}
      description={t('Select an operation mode and enter the amount')}
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={handleCancel}>
            {t('Cancel')}
          </Button>
          <Button
            variant='brand'
            onClick={handleConfirm}
            disabled={
              loading ||
              !reasonValid ||
              floorBlocked ||
              !provenanceValid ||
              !amountValid
            }
          >
            {loading ? t('Processing...') : t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='text-muted-foreground text-sm'>{getPreviewText()}</div>

        <div className='space-y-2'>
          <Label>{t('Mode')}</Label>
          <div className='flex gap-1'>
            {(['add', 'subtract', 'override'] as const).map((m) => (
              <Button
                key={m}
                type='button'
                variant='outline'
                size='sm'
                className={cn(
                  mode === m &&
                    'bg-primary text-primary-foreground hover:bg-primary/90 hover:text-primary-foreground'
                )}
                onClick={() => {
                  setMode(m)
                  setAmount('')
                  setFundingSource('')
                  setBatchKind('')
                  setBatchSource('')
                }}
              >
                {m === 'add' && t('Add')}
                {!(m === 'add') && m === 'subtract' && t('Subtract')}
                {!(m === 'add') && !(m === 'subtract') && t('Override')}
              </Button>
            ))}
          </div>
        </div>

        <div className='space-y-2'>
          <Label>
            {t('Amount')} ({currencyLabel})
          </Label>
          <Input
            type='number'
            step={tokensOnly ? 1 : 0.000001}
            min={0}
            placeholder={placeholder}
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleConfirm()
            }}
          />
        </div>

        <div className='space-y-2'>
          <Label htmlFor='quota-order-ref'>{t('Original order')}</Label>
          <Input
            id='quota-order-ref'
            value={orderRef}
            maxLength={64}
            disabled={loading}
            onChange={(event) => setOrderRef(event.target.value)}
          />
        </div>
        {decreases ? (
          <>
            <div className='space-y-2'>
              <Label htmlFor='quota-batch-kind'>{t('Batch kind')}</Label>
              <NativeSelect
                id='quota-batch-kind'
                className='w-full'
                value={batchKind}
                disabled={loading}
                onChange={(event) => setBatchKind(event.target.value)}
              >
                <NativeSelectOption value=''>
                  {t('Batch kind')}
                </NativeSelectOption>
                <NativeSelectOption value='principal'>
                  {t('Principal')}
                </NativeSelectOption>
                <NativeSelectOption value='bonus'>
                  {t('Permanent gift')}
                </NativeSelectOption>
                <NativeSelectOption value='expiring_bonus'>
                  {t('Expiring gift')}
                </NativeSelectOption>
              </NativeSelect>
            </div>
            <div className='space-y-2'>
              <Label htmlFor='quota-batch-source'>{t('Batch source')}</Label>
              <Input
                id='quota-batch-source'
                value={batchSource}
                maxLength={64}
                disabled={loading}
                onChange={(event) => setBatchSource(event.target.value)}
              />
            </div>
          </>
        ) : (
          <div className='space-y-2'>
            <Label htmlFor='quota-funding-source'>{t('Funding source')}</Label>
            <NativeSelect
              id='quota-funding-source'
              className='w-full'
              value={fundingSource}
              disabled={loading}
              onChange={(event) => setFundingSource(event.target.value)}
            >
              <NativeSelectOption value=''>
                {t('Funding source')}
              </NativeSelectOption>
              <NativeSelectOption value='platform_correction'>
                {t('Platform correction')}
              </NativeSelectOption>
              <NativeSelectOption value='site_earnings'>
                {t('Earnings wallet')}
              </NativeSelectOption>
              <NativeSelectOption value='site_margin'>
                {t('Margin wallet')}
              </NativeSelectOption>
            </NativeSelect>
          </div>
        )}
        <div className='space-y-2'>
          <Label htmlFor='quota-reason'>{t('Reason (admins only)')}</Label>
          <Textarea
            id='quota-reason'
            value={reason}
            maxLength={200}
            aria-invalid={reason !== '' && !reasonValid}
            onChange={(e) => setReason(e.target.value)}
          />
          <p className='text-muted-foreground text-xs'>
            {t('{{count}}/200, not shown to the user', { count: reasonLength })}
          </p>
        </div>
      </div>
    </Dialog>
  )
}

export function RootGiftDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  userId: number
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const role = useAuthStore((state) => state.auth.user?.role)
  const { requestVerification, verificationActive } = useUsers()
  const form = useForm<RootGiftFormValues>({
    resolver: zodResolver(rootGiftFormSchema),
    mode: 'onChange',
    defaultValues: { amount: '', date: '', reason: '' },
  })
  const date = form.watch('date')
  const [key, setKey] = useState(() => crypto.randomUUID())
  const active = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const zone = useQuery({
    queryKey: ['root-gift-business-timezone'],
    queryFn: getAdminBusinessTimezone,
    enabled: props.open && role === USER_ROLE.ROOT,
  })
  const businessZone = zone.data?.timezone
  let expires = 0
  if (date && businessZone) {
    try {
      const midnight = dayjs.tz(date, businessZone).startOf('day')
      if (midnight.isValid() && midnight.format('YYYY-MM-DD') === date) {
        expires = midnight.unix()
      }
    } catch {
      expires = 0
    }
  }
  const valid =
    zone.isSuccess &&
    form.formState.isValid &&
    Number.isFinite(expires) &&
    expires > Date.now() / 1000
  const grant = useMutation({
    retry: false,
    mutationFn: async (values: RootGiftFormValues) => {
      if (!valid || active.current || role !== USER_ROLE.ROOT) return false
      active.current = true
      const input = {
        user_id: props.userId,
        amount_usd: values.amount,
        expires_at: expires,
        reason: values.reason,
        idempotency_key: key,
      }
      try {
        const proof = await requestVerification({
          scope: 'adty.root_gift.grant',
          context: input,
          title: t('Grant expiring gift'),
        })
        if (
          !proof ||
          !mounted.current ||
          useAuthStore.getState().auth.user?.role !== USER_ROLE.ROOT
        ) {
          return false
        }
        await grantExpiringUserGift(input, proof.proof_token)
        return true
      } finally {
        active.current = false
      }
    },
    onSuccess: (granted) => {
      if (!granted || !mounted.current) return
      toast.success(t('Expiring gift granted'))
      form.reset()
      setKey(crypto.randomUUID())
      props.onOpenChange(false)
      props.onSuccess()
    },
    onError: (error) =>
      handleServerError(error, t('Failed to grant expiring gift')),
  })
  const loading = grant.isPending
  const confirm = form.handleSubmit((values) => grant.mutate(values))
  if (role !== USER_ROLE.ROOT) return null
  return (
    <Dialog
      open={props.open && !verificationActive}
      onOpenChange={(value) => {
        if (!loading) props.onOpenChange(value)
      }}
      title={t('Grant expiring gift')}
      description={t('Trial gifts cannot be withdrawn or refunded as cash.')}
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            variant='outline'
            disabled={loading}
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button disabled={!valid || loading} onClick={() => void confirm()}>
            {loading ? t('Processing...') : t('Confirm')}
          </Button>
        </>
      }
    >
      {zone.isError && <ErrorState onRetry={() => void zone.refetch()} />}
      <div className='space-y-2'>
        <Label htmlFor={`gift-amount-${props.userId}`}>
          {t('Gift amount (USD)')}
        </Label>
        <Input
          id={`gift-amount-${props.userId}`}
          type='number'
          min='0'
          step='0.000002'
          {...form.register('amount')}
          disabled={loading}
          aria-invalid={!!form.formState.errors.amount}
        />
        {form.formState.errors.amount && (
          <p className='text-destructive text-xs' role='alert'>
            {t('Invalid parameters')}
          </p>
        )}
      </div>
      <div className='space-y-2'>
        <Label htmlFor={`gift-date-${props.userId}`}>{t('Expiry date')}</Label>
        <Input
          id={`gift-date-${props.userId}`}
          type='date'
          {...form.register('date')}
          disabled={loading}
          aria-invalid={
            !!form.formState.errors.date ||
            (!!date && expires <= Date.now() / 1000)
          }
        />
        {!!date && expires <= Date.now() / 1000 && (
          <p className='text-destructive text-xs' role='alert'>
            {t('Invalid parameters')}
          </p>
        )}
        {businessZone && (
          <p className='text-muted-foreground text-xs'>
            {t('Gift expires at 00:00 in {{timezone}} on the selected date.', {
              timezone: businessZone,
            })}
          </p>
        )}
      </div>
      <div className='space-y-2'>
        <Label htmlFor={`gift-reason-${props.userId}`}>
          {t('Reason (admins only)')}
        </Label>
        <Textarea
          id={`gift-reason-${props.userId}`}
          {...form.register('reason')}
          maxLength={200}
          disabled={loading}
          aria-invalid={!!form.formState.errors.reason}
        />
        {form.formState.errors.reason && (
          <p className='text-destructive text-xs' role='alert'>
            {t('Invalid parameters')}
          </p>
        )}
      </div>
    </Dialog>
  )
}
