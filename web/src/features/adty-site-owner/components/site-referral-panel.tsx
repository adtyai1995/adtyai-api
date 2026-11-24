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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  canonicalWithdrawMethods,
  canonicalWithdrawNetworks,
  CASH_WITHDRAW_METHODS,
  type CashWithdrawMethod,
} from '@/features/admin-referral/lib/settings'
import {
  USDT_NETWORKS,
  usdtNetworkLabel,
  type UsdtNetwork,
} from '@/features/referral/lib/address'
import { WITHDRAWAL_TARGET_LABELS } from '@/features/referral/lib/labels'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import {
  getOwnerSiteReferral,
  saveOwnerSiteReferral,
  saveOwnerSiteReferralEnabled,
  siteOwnerKeys,
  type SiteMarketingField,
  type SiteMarketingUpdate,
  type SiteReferralView,
} from '../api'
import {
  MAX_SITE_REFERRAL_RATE,
  validSiteReferralRate,
} from '../lib/site-owner'

type PolicyField = {
  key: SiteMarketingField
  label: string
  integer?: boolean
  max?: number
  hint?: string
}

function MarketingFields(props: {
  title: string
  description: string
  fields: PolicyField[]
  policy: SiteReferralView
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [draft, setDraft] = useState<
    Partial<Record<SiteMarketingField, string>>
  >({})
  const rateDraft = draft.rate
  const rateInvalid =
    rateDraft !== undefined &&
    rateDraft.trim() !== '' &&
    !validSiteReferralRate(rateDraft)
  const save = useMutation({
    mutationFn: () => saveOwnerSiteReferral(draft),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void invalidateSidebarBadges(client)
      setDraft({})
      toast.success(t('Saved successfully'))
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <form
      className='space-y-3'
      onSubmit={(event) => {
        event.preventDefault()
        if (rateInvalid) return
        save.mutate()
      }}
    >
      <div className='space-y-1'>
        <h3 className='text-sm font-medium'>{props.title}</h3>
        <p className='text-muted-foreground text-sm'>{props.description}</p>
      </div>
      <div className='grid gap-4 sm:grid-cols-3'>
        {props.fields.map(({ key, label, integer, max, hint }) => {
          const currentValue = String(draft[key] ?? props.policy[key] ?? '')
          const isRate = key === 'rate'
          const showRateError = isRate && rateInvalid
          const parsedRate = isRate ? Number(currentValue) : Number.NaN
          const ratePctHint =
            isRate &&
            currentValue.trim() !== '' &&
            !showRateError &&
            Number.isFinite(parsedRate)
              ? `${Number((parsedRate * 100).toFixed(4))}%`
              : ''
          return (
            <div key={key} className='space-y-1.5'>
              <Label htmlFor={`site-policy-${key}`}>{label}</Label>
              <Input
                id={`site-policy-${key}`}
                type='number'
                min='0'
                max={max}
                step={integer ? '1' : 'any'}
                aria-invalid={showRateError}
                value={currentValue}
                onChange={(event) =>
                  setDraft({ ...draft, [key]: event.target.value })
                }
              />
              {showRateError ? (
                <p role='alert' className='text-destructive text-xs'>
                  {t(
                    'Commission rate must be between 0 and 0.30 (0% to 30%, up to 6 decimal places).'
                  )}
                </p>
              ) : (
                hint && (
                  <p className='text-muted-foreground text-xs'>
                    {hint}
                    {ratePctHint ? ` (${ratePctHint})` : ''}
                  </p>
                )
              )}
            </div>
          )
        })}
      </div>
      <Button
        type='submit'
        variant='brand'
        disabled={
          save.isPending || Object.keys(draft).length === 0 || rateInvalid
        }
      >
        {t('Save')}
      </Button>
    </form>
  )
}

function SiteWithdrawPolicyForm(props: { policy: SiteReferralView }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const policy = props.policy

  const savedMethods = canonicalWithdrawMethods(policy.withdraw_methods)
  const savedNetworks = canonicalWithdrawNetworks(policy.withdraw_usdt_networks)
  const savedMin = String(policy.min_usd ?? '')
  const savedFee = String(policy.fee_usd ?? '')
  const savedRate = String(policy.cny_rate ?? '')
  const savedCancel = String(policy.cancel_window_minutes ?? '')

  const [methods, setMethods] = useState<CashWithdrawMethod[]>(savedMethods)
  const [networks, setNetworks] = useState<UsdtNetwork[]>(savedNetworks)
  const [minUsd, setMinUsd] = useState(savedMin)
  const [feeUsd, setFeeUsd] = useState(savedFee)
  const [cnyRate, setCnyRate] = useState(savedRate)
  const [cancelWindowMinutes, setCancelWindowMinutes] = useState(savedCancel)

  useEffect(() => {
    setMethods(canonicalWithdrawMethods(policy.withdraw_methods))
    setNetworks(canonicalWithdrawNetworks(policy.withdraw_usdt_networks))
    setMinUsd(String(policy.min_usd ?? ''))
    setFeeUsd(String(policy.fee_usd ?? ''))
    setCnyRate(String(policy.cny_rate ?? ''))
    setCancelWindowMinutes(String(policy.cancel_window_minutes ?? ''))
  }, [policy])

  const usdtEnabled = methods.includes('usdt')
  const alipayEnabled = methods.includes('alipay')
  const anyCashEnabled = usdtEnabled || alipayEnabled

  const usdtChainError =
    usdtEnabled && networks.length === 0 ? t('Choose at least one chain') : ''
  const minValue = Number(minUsd)
  const minError =
    anyCashEnabled &&
    (minUsd.trim() === '' || !Number.isFinite(minValue) || !(minValue > 0))
      ? t('Not set')
      : ''
  const feeValue = Number(feeUsd)
  const feeError =
    usdtEnabled &&
    (feeUsd.trim() === '' || !Number.isFinite(feeValue) || feeValue < 0)
      ? t('Not set')
      : ''
  const cnyValue = Number(cnyRate)
  const cnyError =
    alipayEnabled &&
    (cnyRate.trim() === '' || !Number.isFinite(cnyValue) || !(cnyValue > 0))
      ? t('Not set')
      : ''
  const hasError = Boolean(usdtChainError || minError || feeError || cnyError)

  const isDirty =
    JSON.stringify(methods) !== JSON.stringify(savedMethods) ||
    JSON.stringify(networks) !== JSON.stringify(savedNetworks) ||
    minUsd.trim() !== savedMin.trim() ||
    feeUsd.trim() !== savedFee.trim() ||
    cnyRate.trim() !== savedRate.trim() ||
    cancelWindowMinutes.trim() !== savedCancel.trim()

  const save = useMutation({
    mutationFn: () => {
      const payload: SiteMarketingUpdate = {
        withdraw_methods: methods,
        withdraw_usdt_networks: networks,
      }
      if (minUsd.trim() !== savedMin.trim()) payload.min_usd = minUsd.trim()
      if (feeUsd.trim() !== savedFee.trim()) payload.fee_usd = feeUsd.trim()
      if (cnyRate.trim() !== savedRate.trim()) payload.cny_rate = cnyRate.trim()
      if (cancelWindowMinutes.trim() !== savedCancel.trim()) {
        payload.cancel_window_minutes = cancelWindowMinutes.trim()
      }
      return saveOwnerSiteReferral(payload)
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      await client.invalidateQueries({
        queryKey: ['adty-site-invite-withdraw-rules'],
      })
      void invalidateSidebarBadges(client)
      toast.success(t('Saved successfully'))
    },
    onError: (error) => handleServerError(error),
  })

  const toggleMethod = (method: CashWithdrawMethod, checked: boolean) => {
    setMethods((prev) => {
      const next = checked
        ? [...prev, method]
        : prev.filter((item) => item !== method)
      return canonicalWithdrawMethods(next)
    })
  }

  const toggleNetwork = (network: UsdtNetwork, checked: boolean) => {
    setNetworks((prev) => {
      const next = checked
        ? [...prev, network]
        : prev.filter((item) => item !== network)
      return canonicalWithdrawNetworks(next)
    })
  }

  return (
    <form
      className='space-y-4 rounded-lg border p-4'
      onSubmit={(event) => {
        event.preventDefault()
        if (hasError || !isDirty || save.isPending) return
        save.mutate()
      }}
    >
      <div className='space-y-1'>
        <h3 className='text-sm font-medium'>
          {t('Invite commission withdrawal')}
        </h3>
        <p className='text-muted-foreground text-sm'>
          {t('Transfer to account balance is always enabled.')}
        </p>
      </div>

      <fieldset className='space-y-2'>
        <legend className='text-sm font-medium'>
          {t('Cash withdrawal methods')}
        </legend>
        <div className='flex flex-wrap gap-5'>
          {CASH_WITHDRAW_METHODS.map((method) => (
            <label
              key={method}
              className='flex items-center gap-2 text-sm font-medium'
            >
              <Checkbox
                disabled={save.isPending}
                checked={methods.includes(method)}
                onCheckedChange={(checked) =>
                  toggleMethod(method, checked === true)
                }
              />
              {t(WITHDRAWAL_TARGET_LABELS[method])}
            </label>
          ))}
        </div>
      </fieldset>

      <div className='grid gap-4 sm:grid-cols-2'>
        <div className='space-y-1.5'>
          <Label htmlFor='site-policy-min_usd'>
            {t('Minimum withdrawal (USD)')}
          </Label>
          <Input
            id='site-policy-min_usd'
            type='number'
            min='0'
            step='any'
            aria-invalid={Boolean(minError)}
            value={minUsd}
            placeholder={t('Not set')}
            onChange={(event) => setMinUsd(event.target.value)}
          />
          {minError && <p className='text-destructive text-xs'>{minError}</p>}
        </div>
        <div className='space-y-1.5'>
          <Label htmlFor='site-policy-cancel_window_minutes'>
            {t('Cancel window (minutes)')}
          </Label>
          <Input
            id='site-policy-cancel_window_minutes'
            type='number'
            min='0'
            step='1'
            value={cancelWindowMinutes}
            onChange={(event) => setCancelWindowMinutes(event.target.value)}
          />
        </div>
      </div>

      {usdtEnabled && (
        <div className='bg-muted/30 space-y-4 rounded-md border p-3'>
          <fieldset className='space-y-2'>
            <legend className='text-sm font-medium'>
              {t('Enabled chains')}
            </legend>
            <div className='flex flex-wrap gap-4'>
              {USDT_NETWORKS.map((network) => (
                <label
                  key={network}
                  className='flex items-center gap-2 text-sm'
                >
                  <Checkbox
                    disabled={save.isPending}
                    checked={networks.includes(network)}
                    onCheckedChange={(checked) =>
                      toggleNetwork(network, checked === true)
                    }
                  />
                  {usdtNetworkLabel(network)}
                </label>
              ))}
            </div>
            {usdtChainError && (
              <p className='text-destructive text-xs'>{usdtChainError}</p>
            )}
          </fieldset>
          <div className='max-w-xs space-y-1.5'>
            <Label htmlFor='site-policy-fee_usd'>{t('USDT fee (USD)')}</Label>
            <Input
              id='site-policy-fee_usd'
              type='number'
              min='0'
              step='any'
              aria-invalid={Boolean(feeError)}
              value={feeUsd}
              placeholder={t('Not set')}
              onChange={(event) => setFeeUsd(event.target.value)}
            />
            {feeError && <p className='text-destructive text-xs'>{feeError}</p>}
          </div>
        </div>
      )}

      {alipayEnabled && (
        <div className='bg-muted/30 space-y-1.5 rounded-md border p-3'>
          <Label htmlFor='site-policy-cny_rate'>
            {t('CNY withdrawal rate (1 USD = ? CNY)')}
          </Label>
          <Input
            id='site-policy-cny_rate'
            className='w-44'
            type='number'
            min='0'
            step='any'
            aria-invalid={Boolean(cnyError)}
            value={cnyRate}
            placeholder={t('Not set')}
            onChange={(event) => setCnyRate(event.target.value)}
          />
          {cnyError && <p className='text-destructive text-xs'>{cnyError}</p>}
        </div>
      )}

      <Button
        type='submit'
        variant='brand'
        disabled={save.isPending || !isDirty || hasError}
      >
        {t('Save')}
      </Button>
    </form>
  )
}

/** Subsite referral policy settings: enable switch, invite rate (<= 30%), coverage & withdrawal terms. */
export function SiteReferralPanel() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const rules = useQuery({
    queryKey: siteOwnerKeys.list('site-referral', {}),
    queryFn: getOwnerSiteReferral,
  })
  const enable = useMutation({
    mutationFn: (enabled: boolean) => saveOwnerSiteReferralEnabled(enabled),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void client.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
      void invalidateSidebarBadges(client)
    },
    onError: (error) => handleServerError(error),
  })

  if (rules.isPending) return <LoadingState />
  if (rules.isError) return <ErrorState onRetry={() => void rules.refetch()} />
  if (!rules.data) return null

  const rateMissing = !rules.data.rate_configured
  const isEnabled = rules.data.enabled === true

  return (
    <section className='flex max-w-3xl flex-col gap-6'>
      <div className='flex items-center justify-between gap-4 rounded-lg border p-3'>
        <div className='flex flex-col gap-0.5'>
          <Label htmlFor='site-referral-enabled'>
            {t('Invite commission')}
          </Label>
          <span className='text-muted-foreground text-xs'>
            {rateMissing
              ? t('Set the invite rate first before enabling.')
              : t('Saving a rate does not pay commission.')}
          </span>
        </div>
        <Switch
          id='site-referral-enabled'
          checked={isEnabled}
          disabled={enable.isPending || (rateMissing && !isEnabled)}
          onCheckedChange={(value) => enable.mutate(value)}
        />
      </div>

      <MarketingFields
        title={t('Commission coverage')}
        description={t(
          'New orders use these terms. Zero observation days makes fully covered commission available immediately.'
        )}
        policy={rules.data}
        fields={[
          {
            key: 'rate',
            label: t('Invite rate (0 to 0.30, max 30%)'),
            max: MAX_SITE_REFERRAL_RATE,
            hint: t(
              'Commission rate cannot exceed 30% (0 to 0.30). Example: 0.15 = 15%.'
            ),
          },
          {
            key: 'pending_coverage_cap',
            label: t('Pending coverage cap (USD)'),
          },
          {
            key: 'observation_days',
            label: t('Observation days'),
            integer: true,
          },
        ]}
      />

      <SiteWithdrawPolicyForm policy={rules.data} />
    </section>
  )
}
