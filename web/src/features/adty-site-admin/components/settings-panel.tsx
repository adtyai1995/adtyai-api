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
import { ChevronRight, Plus, Trash2 } from 'lucide-react'
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
import { SettingsSection } from '@/features/system-settings/components/settings-section'
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
  edgeMainlandReady,
  mergeResellerDrafts,
  newRowKey,
  RULE_KEYS,
  SWITCH_KEYS,
  toResellerDrafts,
  VISIBILITY_KEYS,
  type ResellerDrafts,
  type RuleKey,
  type SwitchKey,
  type VisibilityKey,
} from '../lib/settings'
import type { ResellerSettingsUpdate } from '../types'
import { EdgeStatusCard } from './edge-status-card'
import { SecretField, SwitchRow, TextField } from './setting-fields'

function resellerFieldLabel(key: string, t: (k: string) => string): string {
  switch (key) {
    case 'enabled':
      return t('Reseller sites')
    case 'root_domains':
      return t('Root domains')
    case 'reserved_subdomains':
      return t('Reserved subdomains')
    case 'unknown_host_mode':
      return t('Unknown host handling')
    case 'markup_increase_delay_hours':
      return t('Supply price increase delay (hours, 0 = immediate)')
    case 'close_notice_days':
      return t('Close notice days')
    case 'fingerprint_window_days':
      return t('Fingerprint window (days)')
    case 'customer_lookup_per_hour':
      return t('Customer lookups per hour')
    case 'max_custom_domains_per_site':
      return t('Custom domains per site')
    case 'custom_domain_creates_per_hour':
      return t('Custom domains added per hour')
    case 'site_login_only':
      return t('Site customers sign in only on their site')
    case 'custom_domain_enabled':
      return t('Custom domains')
    case 'self_use_check_enabled':
      return t('Check for self-use')
    case 'migration_check_enabled':
      return t('Check for main site migration')
    case 'visibility':
      return t('What site owners can see by default')
    case 'domain_automation_enabled':
      return t('Domain automation')
    case 'aliyun_domain':
      return t('Aliyun root domain')
    case 'domain_mainland_target':
      return t('Mainland target')
    case 'aliyun_access_key_id':
      return t('Aliyun AccessKey ID')
    case 'aliyun_access_key_Secret':
      return t('Aliyun AccessKey Secret')
    case 'cloudflare_apiToken':
      return t('Cloudflare API token')
    case 'cloudflare_zone_id':
      return t('Cloudflare zone ID')
    case 'cloudflare_account_id':
      return t('Cloudflare account ID')
    case 'cname_target':
      return t('CNAME target')
    case 'fallback_origin':
      return t('Fallback origin')
    case 'edge_mainland_enabled':
      return t('Enable mainland China acceleration')
    case 'edge_acme_alias_suffix':
      return t('ACME verification alias suffix')
    case 'edge_acme_staging':
      return t('ACME staging environment')
    case 'edge_node_ip':
      return t('Edge node IPv4 address')
    case 'edge_node_ips':
      return t('Edge node IPv4 addresses')
    case 'edge_failover_to_cf':
      return t('Edge failover to Cloudflare')
    case 'edge_probe_enabled':
      return t('Enable origin HTTPS health probe')
    case 'edge_auto_failover':
      return t('Automatic node failover')
    case 'edge_main_site_enabled':
      return t('Accelerate main site website domain')
    case 'edge_main_site_host':
      return t('Main site edge hostname')
    case 'edge_main_site_origin_sni':
      return t('Main site origin SNI (optional)')
    case 'turnstile_widgets':
      return t('Turnstile widgets')
    case 'turnstile_widget_secret':
      return t('Turnstile widget secrets')
    default:
      return key
  }
}

function resellerWarningText(
  warning: string,
  t: (k: string) => string
): string {
  switch (warning) {
    case 'cleared_secret:cloudflare_api_token':
      return t(
        'Cloudflare API token could not be decrypted and was cleared; please re-enter it.'
      )
    case 'cleared_secret:turnstile_widget_secrets':
      return t(
        'Turnstile widget secrets could not be decrypted and were cleared; please re-enter them.'
      )
    case 'cleared_secret:aliyun_access_key_id':
      return t(
        'Aliyun AccessKey ID could not be decrypted and was cleared; please re-enter it.'
      )
    case 'cleared_secret:aliyun_access_key_secret':
      return t(
        'Aliyun AccessKey Secret could not be decrypted and was cleared; please re-enter it.'
      )
    case 'cleared_secret:credential':
      return t(
        'A saved credential could not be decrypted and was cleared; please re-enter it.'
      )
    case 'Custom domains are on but Turnstile is not configured (account id or widgets missing): new domains will not be registered for human verification.':
      return t(
        'Custom domains are on but Turnstile is not configured (account id or widgets missing): new domains will not be registered for human verification.'
      )
    default:
      if (warning.startsWith('cleared_secret:')) {
        return t(
          'A saved credential could not be decrypted and was cleared; please re-enter it.'
        )
      }
      return t(warning)
  }
}

/**
 * Every adty_reseller_setting key (task 11.9, convention I). Money keys show
 * "Not configured" until set and keep the master switch off; custom domains
 * stay off until Cloudflare is filled in. The server checks the same rules;
 * this only explains them up front.
 */
export function SettingsPanel(props: {
  section: 'supply' | 'domains' | 'policies'
  canWrite: boolean
  canWriteSecrets: boolean
  onNavigateSection?: (section: 'settings' | 'domains') => void
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
  useEffect(() => {
    mergeSavedFields()
  }, [saved])

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
        delete permitted.edge_mainland_enabled
        delete permitted.edge_acme_alias_suffix
        delete permitted.edge_acme_staging
        delete permitted.edge_node_ip
        delete permitted.edge_node_ips
        delete permitted.edge_failover_to_cf
        delete permitted.edge_probe_enabled
        delete permitted.edge_auto_failover
        delete permitted.edge_main_site_enabled
        delete permitted.edge_main_site_host
        delete permitted.edge_main_site_origin_sni
        delete permitted.turnstile_widgets
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
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.edgeStatus })
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

  const tierReady = Boolean(saved.tier_configured ?? saved.money_configured)
  const rootsSet = drafts.root_domains.trim() !== ''
  const canEnable = tierReady && rootsSet
  const cfReady = cloudflareReady(saved, drafts)
  const edgeReady = edgeMainlandReady(saved, drafts)

  const ruleLabels: Record<RuleKey, string> = {
    markup_increase_delay_hours: t(
      'Supply price increase delay (hours, 0 = immediate)'
    ),
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
  if (!tierReady) {
    enableHint = t(
      'Add at least one enabled site level with a supply discount below first.'
    )
  } else if (!rootsSet) {
    enableHint = t('Add at least one root domain first.')
  }

  return (
    <form
      className='flex flex-col gap-4'
      onSubmit={(event) => {
        event.preventDefault()
        if (disabled || conflicts.length) return
        save.mutate(buildResellerUpdate(baseline, drafts))
      }}
    >
      {conflicts.length > 0 && (
        <div
          role='alert'
          className='border-warning/50 bg-warning/10 flex flex-col gap-2 rounded-[14px] border px-5 py-4 text-sm'
        >
          <p className='font-medium'>
            {t('These settings have changed')} ·{' '}
            {conflicts.map((key) => resellerFieldLabel(key, t)).join(', ')}
          </p>
          {conflicts
            .filter(
              (key) =>
                key in saved &&
                !key.toLowerCase().includes('secret') &&
                !key.toLowerCase().includes('token')
            )
            .map((key) => (
              <p key={key} className='text-dim text-[13px] break-all'>
                {resellerFieldLabel(key, t)} · {t('Current value')}{' '}
                {JSON.stringify(saved[key as keyof typeof saved])}
              </p>
            ))}
          <div className='flex flex-wrap gap-2'>
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
        </div>
      )}
      {(saved.settings_warnings ?? []).map((warning) => (
        <p key={warning} role='status' className='text-warning text-sm'>
          {resellerWarningText(warning, t)}
        </p>
      ))}
      {props.section === 'supply' && (
        <SettingsSection title={t('Supply settings')}>
          <div className='flex max-w-xl flex-col gap-3'>
            <SwitchRow
              label={t('Reseller sites')}
              checked={drafts.enabled}
              disabled={disabled || (!canEnable && !drafts.enabled)}
              hint={enableHint}
              onCheckedChange={(value) => set('enabled', value)}
            />
            {!rootsSet && props.onNavigateSection && (
              <div>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  onClick={() => props.onNavigateSection?.('domains')}
                >
                  {t('Go to Domains')}
                </Button>
              </div>
            )}
          </div>
        </SettingsSection>
      )}

      {props.section === 'domains' && (
        <SettingsSection title={t('Domains')}>
          <fieldset className='grid max-w-3xl gap-4 sm:grid-cols-2'>
            <legend className='sr-only'>{t('Domains')}</legend>
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
            <div className='sm:col-span-2'>
              <p className='text-dim text-[13px]'>
                {t('Unknown host')} · {t('Return 404')}
              </p>
            </div>
          </fieldset>
        </SettingsSection>
      )}

      {props.section === 'domains' && (
        <SettingsSection title={t('Custom domains (Cloudflare for SaaS)')}>
          <fieldset className='grid max-w-3xl gap-4 sm:grid-cols-2'>
            <legend className='sr-only'>
              {t('Custom domains (Cloudflare for SaaS)')}
            </legend>
            <div className='flex flex-wrap gap-2 sm:col-span-2'>
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
              onCheckedChange={(value) =>
                set('domain_automation_enabled', value)
              }
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
              <div className='flex h-8 items-center gap-2 self-end'>
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
            <div className='border-border space-y-2 border-t pt-4 sm:col-span-2'>
              <Label>{t('Turnstile widgets')}</Label>
              <p className='text-dim text-[12.5px] leading-relaxed'>
                {t(
                  'Optional Cloudflare Turnstile captcha pool for custom or automated site domains (up to 10 domains per widget). Leave empty if not using custom domain captcha yet.'
                )}
              </p>
              {drafts.widgets.map((widget, index) => (
                <div
                  key={widget.key}
                  className='grid items-end gap-2 sm:grid-cols-[1fr_1fr_1fr_auto]'
                >
                  <TextField
                    label={t('Widget ID of widget {{n}}', { n: index + 1 })}
                    value={widget.id}
                    disabled={secretsDisabled}
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
                    disabled={secretsDisabled}
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
                    disabled={secretsDisabled}
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
                disabled={secretsDisabled}
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
              {drafts.widgets.length === 0 &&
                (drafts.custom_domain_enabled ||
                  drafts.domain_automation_enabled) && (
                  <p className='text-warning text-xs'>{t('Not configured')}</p>
                )}
            </div>
          </fieldset>
        </SettingsSection>
      )}

      {props.section === 'domains' && (
        <SettingsSection title={t('Mainland China acceleration (Edge CDN)')}>
          <fieldset className='grid max-w-3xl gap-4 sm:grid-cols-2'>
            <legend className='sr-only'>
              {t('Mainland China acceleration (Edge CDN)')}
            </legend>
            <div className='sm:col-span-2'>
              <SwitchRow
                label={t('Enable mainland China acceleration')}
                checked={drafts.edge_mainland_enabled}
                disabled={
                  secretsDisabled ||
                  (!edgeReady && !drafts.edge_mainland_enabled)
                }
                hint={
                  edgeReady
                    ? undefined
                    : t(
                        'Configure Cloudflare, the mainland CNAME target, and the ACME verification alias suffix first.'
                      )
                }
                onCheckedChange={(value) => set('edge_mainland_enabled', value)}
              />
            </div>
            <TextField
              label={t('ACME verification alias suffix')}
              placeholder='acme.adtyaicf.top'
              value={drafts.edge_acme_alias_suffix}
              disabled={secretsDisabled}
              onChange={(value) => set('edge_acme_alias_suffix', value)}
            />
            <div className='space-y-2 sm:col-span-2'>
              <Label htmlFor='reseller-edge-node-ips'>
                {t('Edge node IPv4 addresses (one per line)')}
              </Label>
              <Textarea
                id='reseller-edge-node-ips'
                rows={3}
                className='font-mono text-sm'
                placeholder={'203.0.113.10\n203.0.113.11'}
                value={drafts.edge_node_ips}
                disabled={secretsDisabled}
                onChange={(event) => set('edge_node_ips', event.target.value)}
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Enter one or more Hong Kong edge VPS public IPv4 addresses (one per line or comma-separated). One IP runs as a single node; multiple IPs enable DNS round-robin.'
                )}
              </p>
            </div>
            <SwitchRow
              label={t("Use Let's Encrypt staging environment")}
              checked={drafts.edge_acme_staging}
              disabled={secretsDisabled}
              hint={t(
                'Staging certificates are not trusted by browsers. Use staging only during initial DNS and sync verification.'
              )}
              onCheckedChange={(value) => set('edge_acme_staging', value)}
            />
            <SwitchRow
              label={t('Enable origin HTTPS health probe')}
              checked={drafts.edge_probe_enabled}
              disabled={secretsDisabled}
              hint={t(
                'Periodically probe every configured edge node over HTTPS from the origin server every minute.'
              )}
              onCheckedChange={(value) => {
                set('edge_probe_enabled', value)
                if (!value && drafts.edge_auto_failover) {
                  set('edge_auto_failover', false)
                }
              }}
            />
            <div className='sm:col-span-2'>
              <SwitchRow
                label={t('Automatic node failover')}
                checked={drafts.edge_auto_failover}
                disabled={
                  secretsDisabled ||
                  (!drafts.edge_probe_enabled && !drafts.edge_auto_failover)
                }
                hint={
                  drafts.edge_probe_enabled
                    ? t(
                        'Automatically remove a node from DNS after 3 consecutive probe failures, restore it after 2 consecutive successes, and fail over to Cloudflare if all nodes fail.'
                      )
                    : t('Enable origin HTTPS health probe first.')
                }
                onCheckedChange={(value) => set('edge_auto_failover', value)}
              />
            </div>
            <div className='border-border grid gap-4 border-t pt-4 sm:col-span-2 sm:grid-cols-2'>
              <div className='sm:col-span-2'>
                <SwitchRow
                  label={t('Accelerate main site website domain')}
                  checked={drafts.edge_main_site_enabled}
                  disabled={
                    secretsDisabled ||
                    (!drafts.edge_main_site_host.trim() &&
                      !drafts.edge_main_site_enabled)
                  }
                  hint={
                    drafts.edge_main_site_host.trim()
                      ? t(
                          'Issue a dedicated edge certificate for the main site website domain and include it in the edge node manifest (leave origin SNI blank to reuse fallback origin).'
                        )
                      : t(
                          'Enter the main site website hostname first (for example www.adtyai.com). Keep the apex domain on Cloudflare 301 redirect and never put an API domain here.'
                        )
                  }
                  onCheckedChange={(value) =>
                    set('edge_main_site_enabled', value)
                  }
                />
              </div>
              <TextField
                label={t('Main site edge hostname')}
                placeholder='www.adtyai.com'
                value={drafts.edge_main_site_host}
                disabled={secretsDisabled}
                onChange={(value) => set('edge_main_site_host', value)}
              />
              <TextField
                label={t('Main site origin SNI (optional)')}
                placeholder='www.adtyai.com'
                value={drafts.edge_main_site_origin_sni}
                disabled={secretsDisabled}
                onChange={(value) => set('edge_main_site_origin_sni', value)}
              />
            </div>
            <EdgeStatusCard
              canManage={props.canWrite && props.canWriteSecrets}
            />
          </fieldset>
        </SettingsSection>
      )}

      {props.section === 'domains' && (
        <SettingsSection title={t('Features')}>
          <fieldset className='flex max-w-xl flex-col gap-3'>
            <legend className='sr-only'>{t('Features')}</legend>
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
          </fieldset>
        </SettingsSection>
      )}

      {props.section === 'policies' && (
        <SettingsSection title={t('Rules')}>
          <fieldset className='grid max-w-3xl gap-3 sm:grid-cols-2'>
            <legend className='sr-only'>{t('Rules')}</legend>
            {SWITCH_KEYS.map((key) => (
              <SwitchRow
                key={key}
                label={switchLabels[key]}
                checked={key === 'site_login_only' ? true : drafts[key]}
                disabled={disabled || key === 'site_login_only'}
                onCheckedChange={(value) => {
                  if (key !== 'site_login_only') set(key, value)
                }}
              />
            ))}
          </fieldset>
        </SettingsSection>
      )}

      {props.section === 'policies' && (
        <SettingsSection title={t('What site owners can see by default')}>
          <fieldset className='grid max-w-3xl grid-cols-2 gap-4 sm:grid-cols-3'>
            <legend className='sr-only'>
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
        </SettingsSection>
      )}

      <details className='group border-border bg-card overflow-hidden rounded-[14px] border'>
        <summary className='text-foreground flex cursor-pointer list-none items-center gap-2 px-5 py-4 text-[15px] leading-snug font-semibold tracking-tight sm:px-6 [&::-webkit-details-marker]:hidden'>
          <ChevronRight
            aria-hidden
            className='text-dim size-4 transition-transform group-open:rotate-90'
          />
          {t('Advanced settings')}
        </summary>
        <div className='border-border grid max-w-full gap-4 border-t px-5 py-5 sm:grid-cols-2 sm:px-6 sm:py-6 lg:max-w-3xl lg:border-r-0'>
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
              inputClassName='sm:max-w-44'
              label={ruleLabels[key]}
              value={drafts[key]}
              disabled={disabled}
              onChange={(value) => set(key, value)}
            />
          ))}
        </div>
      </details>

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
