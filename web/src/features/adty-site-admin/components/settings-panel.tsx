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
import { Plus, Trash2 } from 'lucide-react'
import { useEffect, useEffectEvent, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  getResellerSettings,
  checkDomainProvider,
  saveResellerSettings,
  siteAdminKeys,
} from '../api'
import {
  buildResellerUpdate,
  cloudflareReady,
  missingMoneyKeys,
  mergeResellerDrafts,
  newRowKey,
  MONEY_KEYS,
  RULE_KEYS,
  SWITCH_KEYS,
  toResellerDrafts,
  VISIBILITY_KEYS,
  type MoneyKey,
  type ResellerDrafts,
  type RuleKey,
  type SwitchKey,
  type VisibilityKey,
} from '../lib/settings'
import type { ResellerSettingsUpdate } from '../types'
import { SecretField, SwitchRow, TextField } from './setting-fields'

/**
 * Every adty_reseller_setting key (task 11.9, convention I). Money keys show
 * "Not configured" until set and keep the master switch off; custom and
 * sender domains stay off until Cloudflare and Resend are filled in. The
 * server checks the same rules; this only explains them up front.
 */
export function SettingsPanel(props: {
  section: 'supply' | 'domains' | 'policies'
  canWrite: boolean
  canWriteSecrets: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: siteAdminKeys.settings,
    queryFn: getResellerSettings,
  })
  const saved = query.data
  const [drafts, setDrafts] = useState<ResellerDrafts | null>(null)

  const [baseline, setBaseline] = useState(saved)
  const edited = useRef(false)
  const mergeSavedFields = useEffectEvent(() => {
    if (!saved) return
    if (edited.current && baseline && drafts && baseline !== saved) {
      const changes = buildResellerUpdate(baseline, drafts)
      const dirtyKeys = [
        ...Object.keys(changes),
        ...(changes.clear_secrets ?? []),
      ]
      const mergedBase = {
        ...saved,
        field_versions: { ...saved.field_versions },
      }
      for (const key of dirtyKeys) {
        if (key in baseline) {
          Object.assign(mergedBase, {
            [key]: baseline[key as keyof typeof baseline],
          })
        }
        mergedBase.field_versions[key] = baseline.field_versions?.[key] ?? ''
      }
      setBaseline(mergedBase)
      setDrafts(mergeResellerDrafts(baseline, drafts, saved))
    } else if (!edited.current) {
      setBaseline(saved)
      setDrafts(toResellerDrafts(saved))
    }
  })
  useEffect(() => { mergeSavedFields() }, [saved])

  const save = useMutation({
    mutationFn: (body: ResellerSettingsUpdate) => {
      const user = useAuthStore.getState().auth.user
      if (!props.canWrite || !canAdmin(user, ADTY_PERMISSIONS.SITE_SETTINGS)) {
        throw new Error(t('No permission to perform this action'))
      }
      const permitted = { ...body }
      if (!props.canWriteSecrets || user?.role !== 100) {
        delete permitted.cloudflare_apiToken
        delete permitted.aliyun_access_key_id
        delete permitted.aliyun_access_key_Secret
        delete permitted.domain_automation_enabled
        delete permitted.aliyun_domain
        delete permitted.domain_mainland_target
        delete permitted.cloudflare_zone_id
        delete permitted.cloudflare_account_id
        delete permitted.cname_target
        delete permitted.fallback_origin
        delete permitted.turnstile_widget_secret
        delete permitted.clear_secrets
      }
      return saveResellerSettings({
        ...permitted,
        expected_versions: baseline?.field_versions,
      })
    },
    onSuccess: (next) => {
      edited.current = false
      setBaseline(next)
      setDrafts(toResellerDrafts(next))
      queryClient.setQueryData(siteAdminKeys.settings, next)
      toast.success(t('Saved successfully'))
    },
    onError: (err) => {
      handleServerError(err)
      void query.refetch()
    },
  })
  const checkProvider = useMutation({
    mutationFn: checkDomainProvider,
    onSuccess: () =>
      toast.success(
        t(
          'Provider configuration is readable; write access and public HTTPS still require verification'
        )
      ),
    onError: (error) => handleServerError(error),
  })

  useDraftNavigation(
    !!baseline &&
      !!drafts &&
      Object.keys(buildResellerUpdate(baseline, drafts)).length > 0
  )
  if (query.isError && !saved) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  if (!baseline || !saved || !drafts) return <LoadingState />

  const disabled = !props.canWrite || save.isPending
  const secretsDisabled = disabled || !props.canWriteSecrets
  const set = <K extends keyof ResellerDrafts>(
    key: K,
    value: ResellerDrafts[K]
  ) => {
    edited.current = true
    setDrafts((prev) => (prev ? { ...prev, [key]: value } : prev))
  }
  const patch = buildResellerUpdate(baseline, drafts)
  const changedKeys = [...Object.keys(patch), ...(patch.clear_secrets ?? [])]
  const conflicts = changedKeys.filter(
    (key) =>
      (saved.field_versions?.[key] ?? '') !==
      (baseline.field_versions?.[key] ?? '')
  )

  const missingMoney = missingMoneyKeys(drafts)
  const rootsSet = drafts.root_domains.trim() !== ''
  const canEnable = missingMoney.length === 0 && rootsSet
  const cfReady = cloudflareReady(saved, drafts)

  const moneyLabels: Record<MoneyKey, string> = {
    cost_factor: t('Cost factor d (greater than 0)'),
    min_cost_factor: t('Minimum cost factor'),
  }
  const ruleLabels: Record<RuleKey, string> = {
    markup_increase_delay_hours: t('Supply cost increase delay (hours)'),
    close_notice_days: t('Close notice days'),
    fingerprint_window_days: t('Fingerprint window (days)'),
    customer_lookup_per_hour: t('Customer lookups per hour'),
    max_custom_domains_per_site: t('Custom domains per site'),
    custom_domain_creates_per_hour: t('Custom domains added per hour'),
  }
  const switchLabels: Record<SwitchKey, string> = {
    site_login_only: t('Site customers sign in only on their site'),
    self_use_check_enabled: t('Check for self-use'),
    migration_check_enabled: t('Check for main site migration'),
  }
  const visibilityLabels: Record<VisibilityKey, string> = {
    customer_email: t('Customer email'),
    customer_balance: t('Customer balance'),
    customer_topups: t('Customer top-ups'),
    customer_usage: t('Customer usage'),
    request_logs: t('Request logs'),
    customer_tokens: t('Customer API keys'),
  }

  let enableHint: string | undefined
  if (missingMoney.length > 0) {
    enableHint = t('Set every money setting marked Not configured first.')
  } else if (!rootsSet) {
    enableHint = t('Add at least one root domain first.')
  }

  return (
    <form
      className='flex max-w-3xl flex-col gap-6'
      onSubmit={(event) => {
        event.preventDefault()
        if (disabled || conflicts.length) return
        save.mutate(buildResellerUpdate(baseline, drafts))
      }}
    >
      {conflicts.length > 0 && (
        <div role='alert'>
          <p>
            {t('These settings have changed')} · {conflicts.join(', ')}
          </p>
          {conflicts
            .filter(
              (key) =>
                key in saved &&
                !key.toLowerCase().includes('secret') &&
                !key.toLowerCase().includes('token')
            )
            .map((key) => (
              <p key={key}>
                {key} · {t('Current value')}{' '}
                {JSON.stringify(saved[key as keyof typeof saved])}
              </p>
            ))}
          <Button
            type='button'
            variant='outline'
            onClick={() => {
              setDrafts(mergeResellerDrafts(baseline, drafts, saved))
              setBaseline(saved)
            }}
          >
            {t('Keep my changes')}
          </Button>
          <Button
            type='button'
            variant='ghost'
            onClick={() => {
              edited.current = false
              setBaseline(saved)
              setDrafts(toResellerDrafts(saved))
            }}
          >
            {t('Discard changes')}
          </Button>
        </div>
      )}
      {(saved.settings_warnings ?? []).map((warning) => (
        <p key={warning} role='status' className='text-warning text-sm'>
          {t(warning)}
        </p>
      ))}
      {props.section === 'supply' && (
        <SwitchRow
          label={t('Reseller sites')}
          checked={drafts.enabled}
          disabled={disabled || (!canEnable && !drafts.enabled)}
          hint={enableHint}
          onCheckedChange={(value) => set('enabled', value)}
        />
      )}

      {props.section === 'supply' && (
        <fieldset className='grid gap-4 sm:grid-cols-2'>
          <legend className='mb-2 text-sm font-medium'>{t('Money')}</legend>
          {MONEY_KEYS.map((key) => (
            <TextField
              key={key}
              type='number'
              required
              label={moneyLabels[key]}
              value={drafts[key]}
              disabled={disabled}
              onChange={(value) => set(key, value)}
            />
          ))}
          <div className='space-y-2 sm:col-span-2'>
            <Label>{t('Cost factor tiers by last month customer spend')}</Label>
            {drafts.tiers.map((tier, index) => (
              <div key={tier.key} className='flex items-end gap-2'>
                <TextField
                  type='number'
                  label={t('From (USD per month)')}
                  value={tier.min_monthly_usd}
                  disabled={disabled}
                  onChange={(value) =>
                    set(
                      'tiers',
                      drafts.tiers.map((item, i) =>
                        i === index ? { ...item, min_monthly_usd: value } : item
                      )
                    )
                  }
                />
                <TextField
                  type='number'
                  label={t('Cost factor')}
                  value={tier.cost_factor}
                  disabled={disabled}
                  onChange={(value) =>
                    set(
                      'tiers',
                      drafts.tiers.map((item, i) =>
                        i === index ? { ...item, cost_factor: value } : item
                      )
                    )
                  }
                />
                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  disabled={disabled}
                  aria-label={t('Remove tier {{n}}', { n: index + 1 })}
                  onClick={() =>
                    set(
                      'tiers',
                      drafts.tiers.filter((_, i) => i !== index)
                    )
                  }
                >
                  <Trash2 aria-hidden />
                </Button>
              </div>
            ))}
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={disabled}
              onClick={() =>
                set('tiers', [
                  ...drafts.tiers,
                  { key: newRowKey(), min_monthly_usd: '', cost_factor: '' },
                ])
              }
            >
              <Plus aria-hidden />
              {t('Add tier')}
            </Button>
            <p className='text-muted-foreground text-xs'>
              {t('Leave empty to use the global cost factor for every site.')}
            </p>
          </div>
        </fieldset>
      )}

      {props.section === 'domains' && (
        <fieldset className='grid gap-4 sm:grid-cols-2'>
          <legend className='mb-2 text-sm font-medium'>{t('Domains')}</legend>
          <div className='space-y-2'>
            <Label htmlFor='reseller-roots'>
              {t('Root domains (one per line)')}
            </Label>
            <Textarea
              id='reseller-roots'
              rows={3}
              value={drafts.root_domains}
              disabled={disabled}
              onChange={(event) => set('root_domains', event.target.value)}
            />
            {!rootsSet && (
              <p className='text-warning text-xs'>{t('Not configured')}</p>
            )}
          </div>
          <div className='space-y-2'>
            <Label htmlFor='reseller-reserved'>
              {t('Reserved subdomains (one per line)')}
            </Label>
            <Textarea
              id='reseller-reserved'
              rows={3}
              value={drafts.reserved_subdomains}
              disabled={disabled}
              onChange={(event) =>
                set('reserved_subdomains', event.target.value)
              }
            />
          </div>
          <div className='space-y-2'>
            <p>{t('Unknown host')} · {t('Return 404')}</p>
          </div>
        </fieldset>
      )}

      {props.section === 'domains' && (
        <fieldset className='grid gap-4 sm:grid-cols-2'>
          <legend className='mb-2 text-sm font-medium'>
            {t('Custom domains (Cloudflare for SaaS)')}
          </legend>
          <div className='flex gap-2 sm:col-span-2'>
            <Button
              type='button'
              variant='outline'
              disabled={secretsDisabled || checkProvider.isPending}
              onClick={() => checkProvider.mutate('cloudflare')}
            >
              {t('Check Cloudflare')}
            </Button>
            <Button
              type='button'
              variant='outline'
              disabled={secretsDisabled || checkProvider.isPending}
              onClick={() => checkProvider.mutate('aliyun')}
            >
              {t('Check AliDNS')}
            </Button>
          </div>
          <SwitchRow
            label={t('Automate platform domains')}
            checked={drafts.domain_automation_enabled}
            disabled={secretsDisabled}
            onCheckedChange={(value) => set('domain_automation_enabled', value)}
          />
          <TextField
            label={t('AliDNS managed root domain')}
            value={drafts.aliyun_domain}
            disabled={secretsDisabled}
            onChange={(value) => set('aliyun_domain', value)}
          />
          <TextField
            label={t('Mainland CNAME target')}
            value={drafts.domain_mainland_target}
            disabled={secretsDisabled}
            onChange={(value) => set('domain_mainland_target', value)}
          />
          <SecretField
            label={t('AliDNS AccessKey ID')}
            value={drafts.aliyun_id}
            configured={!!saved.aliyun_id_configured && !drafts.clear_aliyun}
            disabled={secretsDisabled}
            onChange={(value) => set('aliyun_id', value)}
          />
          <SecretField
            label={t('AliDNS AccessKey Secret')}
            value={drafts.aliyun_secret}
            configured={
              !!saved.aliyun_secret_configured && !drafts.clear_aliyun
            }
            disabled={secretsDisabled}
            onChange={(value) => set('aliyun_secret', value)}
          />
          <SwitchRow
            label={t('Remove saved AliDNS credentials')}
            checked={drafts.clear_aliyun}
            disabled={secretsDisabled}
            onCheckedChange={(value) => set('clear_aliyun', value)}
          />
          <SecretField
            label={t('Cloudflare API token')}
            value={drafts.cloudflare_token}
            configured={
              saved.cloudflare_token_configured &&
              !drafts.clear_cloudflare_token
            }
            disabled={secretsDisabled}
            onChange={(value) => set('cloudflare_token', value)}
          />
          <TextField
            label={t('Cloudflare zone ID')}
            required
            value={drafts.cloudflare_zone_id}
            disabled={secretsDisabled}
            onChange={(value) => set('cloudflare_zone_id', value)}
          />
          <TextField
            label={t('Cloudflare account ID')}
            required
            value={drafts.cloudflare_account_id}
            disabled={secretsDisabled}
            onChange={(value) => set('cloudflare_account_id', value)}
          />
          <TextField
            label={t('CNAME target')}
            required
            placeholder='customers.example.com'
            value={drafts.cname_target}
            disabled={secretsDisabled}
            onChange={(value) => set('cname_target', value)}
          />
          <TextField
            label={t('Fallback origin')}
            required
            value={drafts.fallback_origin}
            disabled={secretsDisabled}
            onChange={(value) => set('fallback_origin', value)}
          />
          {saved.cloudflare_token_configured && (
            <div className='flex items-center gap-2 self-end'>
              <Checkbox
                id='reseller-clear-token'
                checked={drafts.clear_cloudflare_token}
                disabled={secretsDisabled}
                onCheckedChange={(value) =>
                  set('clear_cloudflare_token', value === true)
                }
              />
              <Label htmlFor='reseller-clear-token'>
                {t('Remove the saved token')}
              </Label>
            </div>
          )}
          <div className='space-y-2 sm:col-span-2'>
            <Label>{t('Turnstile widgets')}</Label>
            {drafts.widgets.map((widget, index) => (
              <div
                key={widget.key}
                className='grid items-end gap-2 sm:grid-cols-[1fr_1fr_1fr_auto]'
              >
                <TextField
                  label={t('Widget ID of widget {{n}}', { n: index + 1 })}
                  value={widget.id}
                  disabled={disabled}
                  onChange={(value) =>
                    set(
                      'widgets',
                      drafts.widgets.map((item, i) =>
                        i === index ? { ...item, id: value } : item
                      )
                    )
                  }
                />
                <TextField
                  label={t('Site key of widget {{n}}', { n: index + 1 })}
                  value={widget.site_key}
                  disabled={disabled}
                  onChange={(value) =>
                    set(
                      'widgets',
                      drafts.widgets.map((item, i) =>
                        i === index ? { ...item, site_key: value } : item
                      )
                    )
                  }
                />
                <SecretField
                  label={t('Secret of widget {{n}}', { n: index + 1 })}
                  value={widget.secret}
                  configured={
                    saved.turnstile_secret_configured?.[widget.id] === true
                  }
                  disabled={secretsDisabled}
                  onChange={(value) =>
                    set(
                      'widgets',
                      drafts.widgets.map((item, i) =>
                        i === index ? { ...item, secret: value } : item
                      )
                    )
                  }
                />
                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  disabled={disabled}
                  aria-label={t('Remove widget {{n}}', { n: index + 1 })}
                  onClick={() =>
                    set(
                      'widgets',
                      drafts.widgets.filter((_, i) => i !== index)
                    )
                  }
                >
                  <Trash2 aria-hidden />
                </Button>
              </div>
            ))}
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={disabled}
              onClick={() =>
                set('widgets', [
                  ...drafts.widgets,
                  { key: newRowKey(), id: '', site_key: '', secret: '' },
                ])
              }
            >
              <Plus aria-hidden />
              {t('Add widget')}
            </Button>
            {drafts.widgets.length === 0 && (
              <p className='text-warning text-xs'>{t('Not configured')}</p>
            )}
          </div>
        </fieldset>
      )}

      {props.section === 'domains' && (
        <fieldset className='flex flex-col gap-3'>
          <legend className='mb-2 text-sm font-medium'>{t('Features')}</legend>
          <SwitchRow
            label={t('Custom domains')}
            checked={drafts.custom_domain_enabled}
            disabled={disabled || (!cfReady && !drafts.custom_domain_enabled)}
            hint={
              cfReady
                ? undefined
                : t(
                    'Fill in the Cloudflare API token, zone ID and CNAME target first.'
                  )
            }
            onCheckedChange={(value) => set('custom_domain_enabled', value)}
          />
          <SwitchRow
            label={t('Site owner sender domains')}
            checked={drafts.sender_domain_enabled}
            disabled={
              disabled ||
              (!saved.resend_configured && !drafts.sender_domain_enabled)
            }
            hint={
              saved.resend_configured
                ? undefined
                : t(
                    'Fill in the Resend API key under System Settings > Email first.'
                  )
            }
            onCheckedChange={(value) => set('sender_domain_enabled', value)}
          />
        </fieldset>
      )}

      {props.section === 'policies' && (
        <fieldset className='grid gap-4 sm:grid-cols-2'>
          <legend className='mb-2 text-sm font-medium'>{t('Rules')}</legend>
          {SWITCH_KEYS.map((key) => (
            <SwitchRow
              key={key}
              label={switchLabels[key]}
              checked={drafts[key]}
              disabled={disabled}
              onCheckedChange={(value) => set(key, value)}
            />
          ))}
        </fieldset>
      )}

      {props.section === 'policies' && (
        <fieldset className='grid gap-4 sm:grid-cols-2'>
          <legend className='mb-2 text-sm font-medium'>
            {t('What site owners can see by default')}
          </legend>
          {VISIBILITY_KEYS.map((key) => (
            <div key={key} className='space-y-2'>
              <Label htmlFor={`reseller-visibility-${key}`}>
                {visibilityLabels[key]}
              </Label>
              <NativeSelect
                id={`reseller-visibility-${key}`}
                value={drafts.visibility[key]}
                disabled={disabled}
                onChange={(event) =>
                  set('visibility', {
                    ...drafts.visibility,
                    [key]: event.target.value,
                  })
                }
              >
                {key === 'customer_email' ? (
                  <>
                    <NativeSelectOption value='masked'>
                      {t('Masked')}
                    </NativeSelectOption>
                    <NativeSelectOption value='full'>
                      {t('Full')}
                    </NativeSelectOption>
                  </>
                ) : (
                  <>
                    <NativeSelectOption value='on'>
                      {t('Shown')}
                    </NativeSelectOption>
                    <NativeSelectOption value='off'>
                      {t('Hidden')}
                    </NativeSelectOption>
                  </>
                )}
              </NativeSelect>
            </div>
          ))}
        </fieldset>
      )}

      <details>
        <summary className='cursor-pointer text-sm font-medium'>
          {t('Advanced settings')}
        </summary>
        <div className='mt-3 grid gap-3 sm:grid-cols-2'>
          {RULE_KEYS.filter((key) => {
            if (key === 'markup_increase_delay_hours') {
              return props.section === 'supply'
            }
            if (
              key === 'max_custom_domains_per_site' ||
              key === 'custom_domain_creates_per_hour'
            ) {
              return props.section === 'domains'
            }
            return props.section === 'policies'
          }).map((key) => (
            <TextField
              key={key}
              type='number'
              label={ruleLabels[key]}
              value={drafts[key]}
              disabled={disabled}
              onChange={(value) => set(key, value)}
            />
          ))}
        </div>
      </details>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Site agreement version {{agreement}}, site customer terms version {{terms}}. Edit them under System Settings > Content.',
          {
            agreement: saved.site_agreement_version,
            terms: saved.site_customer_terms_version,
          }
        )}
      </p>

      {props.canWrite && (
        <div>
          <Button
            variant='brand'
            type='submit'
            disabled={
              save.isPending ||
              conflicts.length > 0 ||
              Object.keys(buildResellerUpdate(baseline, drafts)).length === 0
            }
          >
            {t('Save')}
          </Button>
        </div>
      )}
    </form>
  )
}
