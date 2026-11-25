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
import { adtySiteQueryKey } from '@/features/adty-site/api'
import { referralQueryKeys } from '@/features/referral/api'
import {
  USDT_NETWORKS,
  usdtNetworkLabel,
  type UsdtNetwork,
} from '@/features/referral/lib/address'
import { WITHDRAWAL_TARGET_LABELS } from '@/features/referral/lib/labels'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { getSelf } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import {
  adminReferralKeys,
  getReferralSettings,
  saveReferralSettings,
  type ReferralSettings,
  type ReferralSettingsUpdate,
} from '../api'
import {
  canonicalWithdrawMethods,
  canonicalWithdrawNetworks,
  CASH_WITHDRAW_METHODS,
  MONEY_KEYS,
  RULE_KEYS,
  toDrafts,
  type CashWithdrawMethod,
  type Drafts,
  type MoneyKey,
  type RuleKey,
} from '../lib/settings'

function buildReferralSettingsUpdate(
  saved: ReferralSettings,
  drafts: Drafts,
  enabled: boolean,
  withdrawMethods: CashWithdrawMethod[],
  withdrawUsdtNetworks: UsdtNetwork[]
): ReferralSettingsUpdate {
  const body: ReferralSettingsUpdate = {}
  const clear: NonNullable<ReferralSettingsUpdate['clear']> = []
  let withdrawalTouched = false
  for (const key of MONEY_KEYS) {
    const draft = drafts[key].trim()
    if (draft === '') {
      if (saved[key] !== null && saved[key] !== undefined) {
        clear.push(key)
        if (key !== 'default_rate') withdrawalTouched = true
      }
    } else if (Number(draft) !== saved[key]) {
      body[key] = Number(draft)
      if (key !== 'default_rate') withdrawalTouched = true
    }
  }
  for (const key of RULE_KEYS) {
    if (Number(drafts[key]) !== saved[key]) body[key] = Number(drafts[key])
  }
  const cnyDraft = drafts.cny_rate.trim()
  if (cnyDraft !== saved.withdraw_cny_rate) {
    withdrawalTouched = true
    if (cnyDraft === '') {
      clear.push('withdraw_cny_rate')
    } else {
      body.withdraw_cny_rate = cnyDraft
    }
  }
  if (clear.length > 0) body.clear = clear
  if (enabled !== saved.enabled) body.enabled = enabled
  const savedMethods = canonicalWithdrawMethods(saved.withdraw_methods)
  const savedNetworks = canonicalWithdrawNetworks(saved.withdraw_usdt_networks)
  const methodsChanged =
    JSON.stringify(withdrawMethods) !== JSON.stringify(savedMethods)
  const networksChanged =
    JSON.stringify(withdrawUsdtNetworks) !== JSON.stringify(savedNetworks)
  if (methodsChanged || networksChanged || withdrawalTouched) {
    body.withdraw_methods = withdrawMethods
    body.withdraw_usdt_networks = withdrawUsdtNetworks
  }
  return body
}

export function SettingsPanel(props: { canWrite: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: adminReferralKeys.settings,
    queryFn: getReferralSettings,
  })
  const [drafts, setDrafts] = useState<Drafts | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [withdrawMethods, setWithdrawMethods] = useState<CashWithdrawMethod[]>(
    []
  )
  const [withdrawUsdtNetworks, setWithdrawUsdtNetworks] = useState<
    UsdtNetwork[]
  >([])
  const saved = query.data

  useEffect(() => {
    if (!saved) return
    setDrafts(toDrafts(saved))
    setEnabled(saved.enabled)
    setWithdrawMethods(canonicalWithdrawMethods(saved.withdraw_methods))
    setWithdrawUsdtNetworks(
      canonicalWithdrawNetworks(saved.withdraw_usdt_networks)
    )
  }, [saved])

  const save = useMutation({
    mutationFn: (body: ReferralSettingsUpdate) => {
      if (
        !props.canWrite ||
        !canAdmin(
          useAuthStore.getState().auth.user,
          ADTY_PERMISSIONS.REFERRAL_SETTINGS
        )
      ) {
        throw new Error(t('No permission to perform this action'))
      }
      return saveReferralSettings(body)
    },
    onSuccess: (next) => {
      queryClient.setQueryData(adminReferralKeys.settings, next)
      void queryClient.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
      void queryClient.invalidateQueries({ queryKey: adtySiteQueryKey })
      void queryClient.invalidateQueries({ queryKey: referralQueryKeys.all })
      void queryClient.invalidateQueries({ queryKey: ['user', 'self'] })
      void invalidateSidebarBadges(queryClient)
      void getSelf()
        .then((res) => {
          if (res?.success && res.data) {
            useAuthStore.getState().auth.setUser(res.data as AuthUser)
          }
        })
        .catch(() => {})
      toast.success(t('Saved successfully'))
    },
    onError: (err) => handleServerError(err),
  })

  const savedMethods = saved
    ? canonicalWithdrawMethods(saved.withdraw_methods)
    : []
  const savedNetworks = saved
    ? canonicalWithdrawNetworks(saved.withdraw_usdt_networks)
    : []
  useDraftNavigation(
    !!saved &&
      !!drafts &&
      (enabled !== saved.enabled ||
        JSON.stringify(drafts) !== JSON.stringify(toDrafts(saved)) ||
        JSON.stringify(withdrawMethods) !== JSON.stringify(savedMethods) ||
        JSON.stringify(withdrawUsdtNetworks) !== JSON.stringify(savedNetworks))
  )
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  if (!saved || !drafts) return <LoadingState />
  const disabled = !props.canWrite || save.isPending

  const labels: Record<MoneyKey | RuleKey, string> = {
    default_rate: t('Default commission rate (%)'),
    min_withdraw_usd: t('Minimum withdrawal (USD)'),
    usdt_withdraw_fee: t('USDT withdrawal fee (USDT)'),
    cookie_ttl_days: t('Referral cookie (days)'),
    freeze_days: t('Freeze period (days)'),
    cancel_window_minutes: t('Cancel window (minutes)'),
    self_invite_window_days: t('Self-invite check window (days)'),
    batch_register_threshold: t(
      'Sign-ups from one IP before freezing (0 = off)'
    ),
    batch_register_window_hours: t('Batch sign-up window (hours)'),
  }
  const set = (key: keyof Drafts, value: string) =>
    setDrafts((prev) => (prev ? { ...prev, [key]: value } : prev))
  const rateMissing = drafts.default_rate.trim() === ''
  const usdtEnabled = withdrawMethods.includes('usdt')
  const alipayEnabled = withdrawMethods.includes('alipay')
  const anyCashEnabled = usdtEnabled || alipayEnabled
  const usdtChainError =
    usdtEnabled && withdrawUsdtNetworks.length === 0
      ? t('Choose at least one chain')
      : ''
  const minMissingError =
    anyCashEnabled && drafts.min_withdraw_usd.trim() === '' ? t('Not set') : ''
  const feeMissingError =
    usdtEnabled && drafts.usdt_withdraw_fee.trim() === '' ? t('Not set') : ''
  const cnyRateNum = Number(drafts.cny_rate)
  const cnyRateMissingError =
    alipayEnabled &&
    (drafts.cny_rate.trim() === '' ||
      !Number.isFinite(cnyRateNum) ||
      !(cnyRateNum > 0))
      ? t('Not set')
      : ''
  const hasWithdrawValidationError = Boolean(
    usdtChainError || minMissingError || feeMissingError || cnyRateMissingError
  )

  const toggleMethod = (method: CashWithdrawMethod, checked: boolean) => {
    setWithdrawMethods((prev) => {
      const next = checked
        ? [...prev, method]
        : prev.filter((item) => item !== method)
      return canonicalWithdrawMethods(next)
    })
  }

  const toggleNetwork = (network: UsdtNetwork, checked: boolean) => {
    setWithdrawUsdtNetworks((prev) => {
      const next = checked
        ? [...prev, network]
        : prev.filter((item) => item !== network)
      return canonicalWithdrawNetworks(next)
    })
  }

  return (
    <form
      className='flex max-w-3xl flex-col gap-6'
      onSubmit={(event) => {
        event.preventDefault()
        if (disabled || hasWithdrawValidationError) return
        save.mutate(
          buildReferralSettingsUpdate(
            saved,
            drafts,
            enabled,
            withdrawMethods,
            withdrawUsdtNetworks
          )
        )
      }}
    >
      <div className='flex items-center justify-between gap-4 rounded-lg border p-3'>
        <div className='flex flex-col'>
          <Label htmlFor='referral-enabled'>{t('Referral program')}</Label>
          {rateMissing && (
            <span className='text-muted-foreground text-xs'>
              {t('Set the default rate first')}
            </span>
          )}
        </div>
        <Switch
          id='referral-enabled'
          checked={enabled}
          disabled={disabled || (rateMissing && !enabled)}
          onCheckedChange={setEnabled}
        />
      </div>

      <fieldset className='space-y-2'>
        <legend className='text-sm font-medium'>
          {t('Money (no built-in values)')}
        </legend>
        <div className='grid gap-4 sm:grid-cols-3'>
          <div className='space-y-2'>
            <Label htmlFor='referral-default_rate'>{labels.default_rate}</Label>
            <Input
              id='referral-default_rate'
              disabled={disabled}
              type='number'
              min={0}
              step='0.01'
              value={drafts.default_rate}
              placeholder={t('Not set')}
              onChange={(event) => set('default_rate', event.target.value)}
            />
            {rateMissing && (
              <p className='text-warning text-xs'>{t('Not set')}</p>
            )}
          </div>
          <div className='space-y-2'>
            <Label htmlFor='referral-min_withdraw_usd'>
              {labels.min_withdraw_usd}
            </Label>
            <Input
              id='referral-min_withdraw_usd'
              disabled={disabled}
              type='number'
              min={0}
              step='0.01'
              value={drafts.min_withdraw_usd}
              placeholder={t('Not set')}
              aria-invalid={Boolean(minMissingError)}
              onChange={(event) => set('min_withdraw_usd', event.target.value)}
            />
            {minMissingError ? (
              <p className='text-destructive text-xs'>{minMissingError}</p>
            ) : (
              drafts.min_withdraw_usd.trim() === '' && (
                <p className='text-warning text-xs'>{t('Not set')}</p>
              )
            )}
          </div>
        </div>
      </fieldset>

      <div className='space-y-4 rounded-lg border p-4'>
        <div className='space-y-1'>
          <h3 className='text-sm font-medium'>
            {t('Cash withdrawal methods')}
          </h3>
          <p className='text-muted-foreground text-xs'>
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
                  checked={withdrawMethods.includes(method)}
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
                      disabled={disabled}
                      checked={withdrawUsdtNetworks.includes(network)}
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
            <div className='max-w-xs space-y-2'>
              <Label htmlFor='referral-usdt_withdraw_fee'>
                {labels.usdt_withdraw_fee}
              </Label>
              <Input
                id='referral-usdt_withdraw_fee'
                disabled={disabled}
                type='number'
                min={0}
                step='0.000001'
                value={drafts.usdt_withdraw_fee}
                placeholder={t('Not set')}
                aria-invalid={Boolean(feeMissingError)}
                onChange={(event) =>
                  set('usdt_withdraw_fee', event.target.value)
                }
              />
              {feeMissingError && (
                <p className='text-destructive text-xs'>{feeMissingError}</p>
              )}
            </div>
          </div>
        )}

        {alipayEnabled && (
          <div className='bg-muted/30 space-y-2 rounded-md border p-3'>
            <Label htmlFor='referral-cny-rate'>
              {t('CNY withdrawal rate (1 USD = ? CNY)')}
            </Label>
            <Input
              id='referral-cny-rate'
              disabled={disabled}
              className='w-40'
              type='number'
              min={0}
              step='0.0001'
              value={drafts.cny_rate}
              placeholder={t('Not set')}
              aria-invalid={Boolean(cnyRateMissingError)}
              onChange={(event) => set('cny_rate', event.target.value)}
            />
            {cnyRateMissingError ? (
              <p className='text-destructive text-xs'>{cnyRateMissingError}</p>
            ) : (
              <p
                className='text-muted-foreground text-xs'
                data-testid='cny-updated'
              >
                {saved.withdraw_cny_rate_updated_at > 0
                  ? t('Updated {{time}}', {
                      time: formatTimestamp(saved.withdraw_cny_rate_updated_at),
                    })
                  : t('Not set')}
              </p>
            )}
          </div>
        )}
      </div>

      <fieldset className='grid gap-4 sm:grid-cols-3'>
        <legend className='mb-2 text-sm font-medium'>{t('Rules')}</legend>
        {RULE_KEYS.map((key) => (
          <div key={key} className='space-y-2'>
            <Label htmlFor={`referral-${key}`}>{labels[key]}</Label>
            <Input
              id={`referral-${key}`}
              disabled={disabled}
              type='number'
              min={0}
              step={1}
              value={drafts[key]}
              onChange={(event) => set(key, event.target.value)}
            />
          </div>
        ))}
      </fieldset>
      <div>
        <Button
          variant='brand'
          type='submit'
          disabled={disabled || hasWithdrawValidationError}
        >
          {t('Save Changes')}
        </Button>
      </div>
    </form>
  )
}
