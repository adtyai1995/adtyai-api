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
import { adtySiteQueryKey } from '@/features/adty-site/api'
import { referralQueryKeys } from '@/features/referral/api'
import {
  USDT_NETWORKS,
  usdtNetworkLabel,
  type UsdtNetwork,
} from '@/features/referral/lib/address'
import { WITHDRAWAL_TARGET_LABELS } from '@/features/referral/lib/labels'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { getSelf } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import {
  getOwnerSiteReferral,
  saveOwnerSiteReferral,
  siteOwnerKeys,
  type SiteMarketingUpdate,
  type SiteReferralView,
} from '../api'
import {
  decimalRateToPercentString,
  MAX_SITE_REFERRAL_RATE_PERCENT,
  percentStringToDecimalRate,
  validSiteReferralRatePercent,
} from '../lib/site-owner'

type SiteRuleKey =
  | 'cookie_ttl_days'
  | 'observation_days'
  | 'cancel_window_minutes'
  | 'self_invite_window_days'
  | 'batch_register_threshold'
  | 'batch_register_window_hours'

const SITE_RULE_KEYS: SiteRuleKey[] = [
  'cookie_ttl_days',
  'observation_days',
  'cancel_window_minutes',
  'self_invite_window_days',
  'batch_register_threshold',
  'batch_register_window_hours',
]

type SiteSettingsDraft = {
  rate_percent: string
  pending_coverage_cap: string
  min_usd: string
  fee_usd: string
  cny_rate: string
  cookie_ttl_days: string
  observation_days: string
  cancel_window_minutes: string
  self_invite_window_days: string
  batch_register_threshold: string
  batch_register_window_hours: string
}

function toDraft(policy: SiteReferralView): SiteSettingsDraft {
  return {
    rate_percent: policy.rate_configured
      ? decimalRateToPercentString(policy.rate)
      : '',
    pending_coverage_cap: String(policy.pending_coverage_cap ?? ''),
    min_usd: String(policy.min_usd ?? ''),
    fee_usd: String(policy.fee_usd ?? ''),
    cny_rate: String(policy.cny_rate ?? ''),
    cookie_ttl_days:
      policy.cookie_ttl_days === null || policy.cookie_ttl_days === undefined
        ? ''
        : String(policy.cookie_ttl_days),
    observation_days:
      policy.observation_days === null || policy.observation_days === undefined
        ? ''
        : String(policy.observation_days),
    cancel_window_minutes:
      policy.cancel_window_minutes === null ||
      policy.cancel_window_minutes === undefined
        ? ''
        : String(policy.cancel_window_minutes),
    self_invite_window_days:
      policy.self_invite_window_days === null ||
      policy.self_invite_window_days === undefined
        ? ''
        : String(policy.self_invite_window_days),
    batch_register_threshold:
      policy.batch_register_threshold === null ||
      policy.batch_register_threshold === undefined
        ? ''
        : String(policy.batch_register_threshold),
    batch_register_window_hours:
      policy.batch_register_window_hours === null ||
      policy.batch_register_window_hours === undefined
        ? ''
        : String(policy.batch_register_window_hours),
  }
}

function buildSiteReferralUpdate(
  saved: SiteReferralView,
  draft: SiteSettingsDraft,
  enabled: boolean,
  methods: CashWithdrawMethod[],
  networks: UsdtNetwork[]
): SiteMarketingUpdate {
  const baseline = toDraft(saved)
  const payload: SiteMarketingUpdate = {}
  const savedMethods = canonicalWithdrawMethods(saved.withdraw_methods)
  const savedNetworks = canonicalWithdrawNetworks(saved.withdraw_usdt_networks)

  if (enabled !== Boolean(saved.enabled)) {
    payload.enabled = enabled
  }
  if (draft.rate_percent.trim() !== baseline.rate_percent.trim()) {
    if (draft.rate_percent.trim() !== '') {
      payload.rate = percentStringToDecimalRate(draft.rate_percent)
    }
  }
  if (
    draft.pending_coverage_cap.trim() !== baseline.pending_coverage_cap.trim()
  ) {
    payload.pending_coverage_cap = draft.pending_coverage_cap.trim()
  }

  let withdrawalTouched = false
  if (draft.min_usd.trim() !== baseline.min_usd.trim()) {
    payload.min_usd = draft.min_usd.trim()
    withdrawalTouched = true
  }
  if (draft.fee_usd.trim() !== baseline.fee_usd.trim()) {
    payload.fee_usd = draft.fee_usd.trim()
    withdrawalTouched = true
  }
  if (draft.cny_rate.trim() !== baseline.cny_rate.trim()) {
    payload.cny_rate = draft.cny_rate.trim()
    withdrawalTouched = true
  }
  const methodsChanged =
    JSON.stringify(methods) !== JSON.stringify(savedMethods)
  const networksChanged =
    JSON.stringify(networks) !== JSON.stringify(savedNetworks)
  if (methodsChanged || networksChanged || withdrawalTouched) {
    payload.withdraw_methods = methods
    payload.withdraw_usdt_networks = networks
  }

  for (const key of SITE_RULE_KEYS) {
    if (draft[key].trim() !== baseline[key].trim()) {
      payload[key] = draft[key].trim()
    }
  }

  return payload
}

/** Unified subsite referral settings matching main-site SettingsPanel (R-37 #1). */
export function SiteReferralPanel() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const query = useQuery({
    queryKey: siteOwnerKeys.list('site-referral', {}),
    queryFn: getOwnerSiteReferral,
  })
  const saved = query.data

  const [draft, setDraft] = useState<SiteSettingsDraft | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [methods, setMethods] = useState<CashWithdrawMethod[]>([])
  const [networks, setNetworks] = useState<UsdtNetwork[]>([])

  useEffect(() => {
    if (!saved) return
    setDraft(toDraft(saved))
    setEnabled(saved.enabled === true)
    setMethods(canonicalWithdrawMethods(saved.withdraw_methods))
    setNetworks(canonicalWithdrawNetworks(saved.withdraw_usdt_networks))
  }, [saved])

  const savedMethods = saved
    ? canonicalWithdrawMethods(saved.withdraw_methods)
    : []
  const savedNetworks = saved
    ? canonicalWithdrawNetworks(saved.withdraw_usdt_networks)
    : []
  const isDirty =
    !!saved &&
    !!draft &&
    (enabled !== Boolean(saved.enabled) ||
      JSON.stringify(draft) !== JSON.stringify(toDraft(saved)) ||
      JSON.stringify(methods) !== JSON.stringify(savedMethods) ||
      JSON.stringify(networks) !== JSON.stringify(savedNetworks))

  useDraftNavigation(isDirty)

  const save = useMutation({
    mutationFn: (payload: SiteMarketingUpdate) =>
      saveOwnerSiteReferral(payload),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      await client.invalidateQueries({
        queryKey: ['adty-site-invite-withdraw-rules'],
      })
      void client.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
      void client.invalidateQueries({ queryKey: adtySiteQueryKey })
      void client.invalidateQueries({ queryKey: referralQueryKeys.all })
      void client.invalidateQueries({ queryKey: ['user', 'self'] })
      void invalidateSidebarBadges(client)
      void getSelf()
        .then((res) => {
          if (res?.success && res.data) {
            useAuthStore.getState().auth.setUser(res.data as AuthUser)
          }
        })
        .catch(() => {})
      toast.success(t('Saved successfully'))
    },
    onError: (error) => handleServerError(error),
  })

  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  if (!saved || !draft) return <LoadingState />

  const disabled = save.isPending
  const set = (key: keyof SiteSettingsDraft, value: string) =>
    setDraft((prev) => (prev ? { ...prev, [key]: value } : prev))

  const rateTrimmed = draft.rate_percent.trim()
  const rateMissing = rateTrimmed === ''
  const rateCleared = Boolean(saved.rate_configured) && rateMissing
  const rateInvalid = !rateMissing && !validSiteReferralRatePercent(rateTrimmed)
  const capMissing = draft.pending_coverage_cap.trim() === ''
  const obsMissing = draft.observation_days.trim() === ''
  const missingEnablePrerequisites = rateMissing || capMissing || obsMissing

  const usdtEnabled = methods.includes('usdt')
  const alipayEnabled = methods.includes('alipay')
  const anyCashEnabled = usdtEnabled || alipayEnabled
  const usdtChainError =
    usdtEnabled && networks.length === 0 ? t('Choose at least one chain') : ''
  const minVal = Number(draft.min_usd)
  const minMissingError =
    anyCashEnabled &&
    (draft.min_usd.trim() === '' || !Number.isFinite(minVal) || !(minVal > 0))
      ? t('Not set')
      : ''
  const feeVal = Number(draft.fee_usd)
  const feeMissingError =
    usdtEnabled &&
    (draft.fee_usd.trim() === '' || !Number.isFinite(feeVal) || feeVal < 0)
      ? t('Not set')
      : ''
  const cnyVal = Number(draft.cny_rate)
  const cnyMissingError =
    alipayEnabled &&
    (draft.cny_rate.trim() === '' || !Number.isFinite(cnyVal) || !(cnyVal > 0))
      ? t('Not set')
      : ''

  const hasValidationError = Boolean(
    rateCleared ||
    rateInvalid ||
    usdtChainError ||
    minMissingError ||
    feeMissingError ||
    cnyMissingError
  )

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

  const ruleLabels: Record<SiteRuleKey, string> = {
    cookie_ttl_days: t('Referral cookie (days)'),
    observation_days: t('Freeze period (days)'),
    cancel_window_minutes: t('Cancel window (minutes)'),
    self_invite_window_days: t('Self-invite check window (days)'),
    batch_register_threshold: t(
      'Sign-ups from one IP before freezing (0 = off)'
    ),
    batch_register_window_hours: t('Batch sign-up window (hours)'),
  }

  const ruleMin: Record<SiteRuleKey, number> = {
    cookie_ttl_days: 1,
    observation_days: 0,
    cancel_window_minutes: 0,
    self_invite_window_days: 1,
    batch_register_threshold: 0,
    batch_register_window_hours: 1,
  }

  return (
    <SettingsSection title={t('Settings')}>
      <form
        className='flex max-w-3xl flex-col gap-6'
        onSubmit={(event) => {
          event.preventDefault()
          if (disabled || hasValidationError || !isDirty) return
          const payload = buildSiteReferralUpdate(
            saved,
            draft,
            enabled,
            methods,
            networks
          )
          if (Object.keys(payload).length === 0) return
          save.mutate(payload)
        }}
      >
        <div className='border-border flex items-center justify-between gap-4 border-b pb-4'>
          <div className='flex flex-col gap-0.5'>
            <Label htmlFor='site-referral-enabled'>
              {t('Referral program')}
            </Label>
            <span className='text-muted-foreground text-xs'>
              {missingEnablePrerequisites
                ? t(
                    'Set the default commission rate, pending coverage cap, and freeze period before enabling.'
                  )
                : t(
                    'New orders use these terms. Zero freeze period makes fully covered commission available immediately.'
                  )}
            </span>
          </div>
          <Switch
            id='site-referral-enabled'
            checked={enabled}
            disabled={disabled || (missingEnablePrerequisites && !enabled)}
            onCheckedChange={setEnabled}
          />
        </div>

        <fieldset className='space-y-3'>
          <legend className='text-[15px] leading-snug font-semibold tracking-tight'>
            {t('Money (no built-in values)')}
          </legend>
          <div className='grid gap-4 sm:grid-cols-3'>
            <div className='space-y-2'>
              <Label htmlFor='site-policy-rate'>
                {t('Default commission rate (%)')}
              </Label>
              <Input
                id='site-policy-rate'
                disabled={disabled}
                type='number'
                min={0}
                max={MAX_SITE_REFERRAL_RATE_PERCENT}
                step='0.01'
                value={draft.rate_percent}
                placeholder={t('Not set')}
                aria-invalid={rateInvalid || rateCleared}
                onChange={(event) => set('rate_percent', event.target.value)}
              />
              {rateInvalid && (
                <p role='alert' className='text-destructive text-xs'>
                  {t('Commission rate must be between 0% and 30%.')}
                </p>
              )}
              {!rateInvalid && rateCleared && (
                <p role='alert' className='text-destructive text-xs'>
                  {t(
                    'Commission rate cannot be cleared once configured. Set it to 0% to disable commission.'
                  )}
                </p>
              )}
              {!rateInvalid && !rateCleared && rateMissing && (
                <p className='text-warning text-xs'>{t('Not set')}</p>
              )}
              {!rateInvalid && !rateMissing && (
                <p className='text-muted-foreground text-xs'>
                  {t('Up to 30% for subsite invitations.')}
                </p>
              )}
            </div>

            <div className='space-y-2'>
              <Label htmlFor='site-policy-pending_coverage_cap'>
                {t('Pending coverage cap (USD)')}
              </Label>
              <Input
                id='site-policy-pending_coverage_cap'
                disabled={disabled}
                type='number'
                min={0}
                step='0.01'
                value={draft.pending_coverage_cap}
                placeholder={t('Not set')}
                onChange={(event) =>
                  set('pending_coverage_cap', event.target.value)
                }
              />
              {capMissing && (
                <p className='text-warning text-xs'>{t('Not set')}</p>
              )}
            </div>

            <div className='space-y-2'>
              <Label htmlFor='site-policy-min_usd'>
                {t('Minimum withdrawal (USD)')}
              </Label>
              <Input
                id='site-policy-min_usd'
                disabled={disabled}
                type='number'
                min={0}
                step='0.01'
                value={draft.min_usd}
                placeholder={t('Not set')}
                aria-invalid={Boolean(minMissingError)}
                onChange={(event) => set('min_usd', event.target.value)}
              />
              {minMissingError ? (
                <p className='text-destructive text-xs'>{minMissingError}</p>
              ) : (
                draft.min_usd.trim() === '' && (
                  <p className='text-warning text-xs'>{t('Not set')}</p>
                )
              )}
            </div>
          </div>
        </fieldset>

        <div className='border-border space-y-4 border-t pt-5'>
          <div className='space-y-1'>
            <h3 className='text-[15px] leading-snug font-semibold tracking-tight'>
              {t('Cash withdrawal methods')}
            </h3>
            <p className='text-dim text-[12.5px] leading-relaxed'>
              {t('Transfer to account balance is always enabled.')}
            </p>
          </div>

          <fieldset className='space-y-2'>
            <legend className='sr-only'>{t('Cash withdrawal methods')}</legend>
            <div className='flex flex-wrap gap-5'>
              {CASH_WITHDRAW_METHODS.map((method) => (
                <label
                  key={method}
                  className='flex items-center gap-2 text-sm font-medium'
                >
                  <Checkbox
                    disabled={disabled}
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

          {usdtEnabled && (
            <div className='bg-muted space-y-4 rounded-[12px] p-4'>
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
                        disabled={disabled}
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
              <div className='space-y-2'>
                <Label htmlFor='site-policy-fee_usd'>
                  {t('USDT withdrawal fee (USDT)')}
                </Label>
                <Input
                  id='site-policy-fee_usd'
                  disabled={disabled}
                  className='w-40 tabular-nums'
                  type='number'
                  min={0}
                  step='0.000001'
                  value={draft.fee_usd}
                  placeholder={t('Not set')}
                  aria-invalid={Boolean(feeMissingError)}
                  onChange={(event) => set('fee_usd', event.target.value)}
                />
                {feeMissingError && (
                  <p className='text-destructive text-xs'>{feeMissingError}</p>
                )}
              </div>
            </div>
          )}

          {alipayEnabled && (
            <div className='bg-muted space-y-2 rounded-[12px] p-4'>
              <Label htmlFor='site-policy-cny_rate'>
                {t('CNY withdrawal rate (1 USD = ? CNY)')}
              </Label>
              <Input
                id='site-policy-cny_rate'
                disabled={disabled}
                className='w-40 tabular-nums'
                type='number'
                min={0}
                step='0.0001'
                value={draft.cny_rate}
                placeholder={t('Not set')}
                aria-invalid={Boolean(cnyMissingError)}
                onChange={(event) => set('cny_rate', event.target.value)}
              />
              {cnyMissingError && (
                <p className='text-destructive text-xs'>{cnyMissingError}</p>
              )}
            </div>
          )}
        </div>

        <div className='border-border border-t pt-5'>
          <fieldset className='grid gap-4 sm:grid-cols-3'>
            <legend className='mb-3 text-[15px] leading-snug font-semibold tracking-tight'>
              {t('Rules')}
            </legend>
            {SITE_RULE_KEYS.map((key) => (
              <div key={key} className='space-y-2'>
                <Label htmlFor={`site-policy-${key}`}>{ruleLabels[key]}</Label>
                <Input
                  id={`site-policy-${key}`}
                  disabled={disabled}
                  type='number'
                  min={ruleMin[key]}
                  step={1}
                  value={draft[key]}
                  placeholder={t('Not set')}
                  onChange={(event) => set(key, event.target.value)}
                />
              </div>
            ))}
          </fieldset>
        </div>

        <div>
          <Button
            variant='brand'
            type='submit'
            disabled={disabled || hasValidationError || !isDirty}
          >
            {t('Save Changes')}
          </Button>
        </div>
      </form>
    </SettingsSection>
  )
}
