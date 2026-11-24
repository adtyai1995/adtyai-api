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
import { Gift } from 'lucide-react'
import * as React from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { TitledCard } from '@/components/ui/titled-card'
import { adtyTopupQueryKeys } from '@/features/wallet/adty/api'
import { PaymentMethodIcon } from '@/features/wallet/adty/components/payment-logos'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

type MainBonusMethodRule = {
  enabled: boolean
  rate: string
}

type MainBonusMethodsMap = Record<string, MainBonusMethodRule>

const MAIN_BONUS_METHODS = [
  { key: 'alipay', labelKey: 'Alipay' },
  { key: 'wxpay', labelKey: 'WeChat Pay' },
  { key: 'usdt', labelKey: 'USDT' },
] as const

function isValidBonusRateDecimal(raw: string): boolean {
  const trimmed = raw.trim()
  if (!/^(?:0(?:\.\d+)?|1(?:\.0+)?)$/.test(trimmed)) {
    return false
  }
  const num = Number(trimmed)
  return Number.isFinite(num) && num >= 0 && num <= 1
}

function formatBonusRatePercent(raw: string): string {
  const trimmed = raw.trim()
  if (!isValidBonusRateDecimal(trimmed)) return ''
  const pct = Number((Number(trimmed) * 100).toFixed(4))
  return `= ${pct}%`
}

export function RechargeBonusSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [drafts, setDrafts] = React.useState<MainBonusMethodsMap>({})
  const savedRef = React.useRef<MainBonusMethodsMap | null>(null)
  const [savingMethod, setSavingMethod] = React.useState<string | null>(null)

  const query = useQuery({
    queryKey: ['adty-admin-bonus-methods'],
    queryFn: async () => {
      const res = await api.get<{
        success: boolean
        message?: string
        data?: MainBonusMethodsMap
      }>('/api/adty/admin/bonus-methods')
      return requireServerSuccess(res.data).data ?? {}
    },
  })

  React.useEffect(() => {
    if (!query.data) return
    const incoming: MainBonusMethodsMap = {}
    for (const item of MAIN_BONUS_METHODS) {
      const remote = query.data?.[item.key]
      incoming[item.key] = {
        enabled: Boolean(remote?.enabled),
        rate: remote?.rate ?? '',
      }
    }
    const prevSaved = savedRef.current
    if (!prevSaved) {
      savedRef.current = incoming
      setDrafts(incoming)
      return
    }
    setDrafts((prev) => {
      const next: MainBonusMethodsMap = { ...prev }
      for (const item of MAIN_BONUS_METHODS) {
        const cur = prev[item.key]
        const base = prevSaved[item.key]
        const isClean =
          !cur ||
          !base ||
          (cur.enabled === base.enabled && cur.rate === base.rate)
        if (isClean) {
          next[item.key] = incoming[item.key]
        }
      }
      return next
    })
    savedRef.current = incoming
  }, [query.data])

  const mutation = useMutation({
    mutationFn: async (payload: {
      method: string
      enabled: boolean
      rate: string
    }) => {
      const res = await api.put<{
        success: boolean
        message?: string
        data?: MainBonusMethodsMap
      }>('/api/adty/admin/bonus-methods', payload)
      return requireServerSuccess(res.data).data ?? {}
    },
    onSuccess: (updated, variables) => {
      const remote = updated[variables.method]
      const savedEntry = {
        enabled: Boolean(remote?.enabled),
        rate: remote?.rate ?? '',
      }
      if (savedRef.current) {
        savedRef.current = {
          ...savedRef.current,
          [variables.method]: savedEntry,
        }
      }
      setDrafts((prev) => ({
        ...prev,
        [variables.method]: savedEntry,
      }))
      queryClient.setQueryData(['adty-admin-bonus-methods'], updated)
      void queryClient.invalidateQueries({ queryKey: adtyTopupQueryKeys.info })
      toast.success(t('Saved'))
    },
    onError: (error) => handleServerError(error),
    onSettled: () => setSavingMethod(null),
  })

  const handleSave = (methodKey: string) => {
    const draft = drafts[methodKey] ?? { enabled: false, rate: '' }
    const trimmed = draft.rate.trim()
    if (draft.enabled) {
      if (!isValidBonusRateDecimal(trimmed)) {
        toast.error(t('Enter a decimal rate between 0 and 1 (e.g. 0.025)'))
        return
      }
    } else if (trimmed !== '' && !isValidBonusRateDecimal(trimmed)) {
      toast.error(t('Enter a decimal rate between 0 and 1 (e.g. 0.025)'))
      return
    }
    setSavingMethod(methodKey)
    mutation.mutate({
      method: methodKey,
      enabled: draft.enabled,
      rate: trimmed || '0',
    })
  }

  let bodyContent: React.ReactNode
  if (query.isPending) {
    bodyContent = (
      <div className='space-y-3'>
        <Skeleton className='h-14 w-full' />
        <Skeleton className='h-14 w-full' />
        <Skeleton className='h-14 w-full' />
      </div>
    )
  } else if (query.isError) {
    bodyContent = (
      <ErrorState
        className='min-h-32'
        title={t('Failed to load')}
        onRetry={() => void query.refetch()}
      />
    )
  } else {
    bodyContent = MAIN_BONUS_METHODS.map((item) => {
      const draft = drafts[item.key] ?? { enabled: false, rate: '' }
      const percentHint = formatBonusRatePercent(draft.rate)
      const isSavingThis = savingMethod === item.key && mutation.isPending
      return (
        <div
          key={item.key}
          className='flex flex-col gap-3 rounded-lg border p-3.5 sm:flex-row sm:items-center sm:justify-between'
        >
          <div className='flex items-center gap-3'>
            <Switch
              id={`main-bonus-switch-${item.key}`}
              checked={draft.enabled}
              onCheckedChange={(checked) =>
                setDrafts((prev) => ({
                  ...prev,
                  [item.key]: {
                    enabled: checked,
                    rate: prev[item.key]?.rate ?? '',
                  },
                }))
              }
            />
            <PaymentMethodIcon method={item.key} className='size-6 shrink-0' />
            <Label
              htmlFor={`main-bonus-switch-${item.key}`}
              className='min-w-24 font-medium'
            >
              {t(item.labelKey)}
            </Label>
          </div>
          <div className='flex flex-wrap items-center gap-2.5'>
            <div className='flex items-center gap-2'>
              <Input
                type='text'
                inputMode='decimal'
                aria-label={`${t(item.labelKey)} ${t('Bonus rate')}`}
                placeholder='0.025'
                className='w-32 font-mono text-xs tabular-nums'
                value={draft.rate}
                onChange={(event) =>
                  setDrafts((prev) => ({
                    ...prev,
                    [item.key]: {
                      enabled: prev[item.key]?.enabled ?? false,
                      rate: event.target.value,
                    },
                  }))
                }
              />
              {percentHint && (
                <span className='text-muted-foreground min-w-16 font-mono text-xs tabular-nums'>
                  {percentHint}
                </span>
              )}
            </div>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={isSavingThis}
              onClick={() => handleSave(item.key)}
            >
              {t('Save')}
            </Button>
          </div>
        </div>
      )
    })
  }

  return (
    <TitledCard
      icon={<Gift className='size-4' />}
      title={t('Payment Method Recharge Bonus')}
      description={t(
        'Configure main-site recharge bonus rates (0 to 1, e.g. 0.025 for 2.5%) for Alipay, WeChat Pay, and USDT. Bonus credits do not earn referral commissions.'
      )}
      disableHoverEffect
      contentClassName='space-y-3'
    >
      {bodyContent}
    </TitledCard>
  )
}
