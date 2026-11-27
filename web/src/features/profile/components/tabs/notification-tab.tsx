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
import { Bell, Loader2, Mail, Server, Webhook } from 'lucide-react'
import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { PasswordInput } from '@/components/password-input'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  SettingsControlChildren,
  SettingsControlGroup,
  SettingsSwitchField,
} from '@/features/system-settings/components/settings-form-layout'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import {
  formatQuota,
  parseQuotaFromDollars,
  quotaUnitsToDollars,
} from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { updateUserSettings } from '../../api'
import { NOTIFICATION_METHODS } from '../../constants'
import { normalizeUserSettings } from '../../lib/user-settings'
import type {
  NotifyType,
  UpdateUserSettingsRequest,
  UserProfile,
} from '../../types'

type UserSettingSecretMeta = {
  has_webhook_secret?: boolean
  has_bark_url?: boolean
  has_gotify_token?: boolean
  webhook_secret?: string
  bark_url?: string
  gotify_token?: string
}

function readSecretConfiguredFlags(profile: UserProfile | null): {
  hasWebhookSecret: boolean
  hasBarkUrl: boolean
  hasGotifyToken: boolean
} {
  const topLevel = profile as (UserProfile & UserSettingSecretMeta) | null
  let parsed: UserSettingSecretMeta = {}
  if (profile?.setting) {
    try {
      const raw: unknown = JSON.parse(profile.setting)
      if (raw && typeof raw === 'object') {
        parsed = raw as UserSettingSecretMeta
      }
    } catch {
      parsed = {}
    }
  }
  return {
    hasWebhookSecret: Boolean(
      topLevel?.has_webhook_secret ||
        parsed.has_webhook_secret ||
        (typeof parsed.webhook_secret === 'string' &&
          parsed.webhook_secret.trim() !== '')
    ),
    hasBarkUrl: Boolean(
      topLevel?.has_bark_url ||
        parsed.has_bark_url ||
        (typeof parsed.bark_url === 'string' && parsed.bark_url.trim() !== '')
    ),
    hasGotifyToken: Boolean(
      topLevel?.has_gotify_token ||
        parsed.has_gotify_token ||
        (typeof parsed.gotify_token === 'string' &&
          parsed.gotify_token.trim() !== '')
    ),
  }
}

const NOTIFICATION_ICONS: Record<NotifyType, typeof Mail> = {
  email: Mail,
  webhook: Webhook,
  bark: Bell,
  gotify: Server,
}

// ============================================================================
// Settings Tab Component
// ============================================================================

interface NotificationTabProps {
  profile: UserProfile | null
  onUpdate: () => void
  /** Whether the site sends low balance reminders at all */
  quotaRemindEnabled: boolean
  /** The site default reminder threshold, in quota units */
  quotaRemindThreshold: number
}

export function NotificationTab({
  profile,
  onUpdate,
  quotaRemindEnabled,
  quotaRemindThreshold,
}: NotificationTabProps) {
  const { t } = useTranslation()
  const isTeamEmployee = useAuthStore(
    (state) => state.auth.user?.is_team_employee === true
  )
  const role = profile?.role ?? 0
  const isAdmin = role >= ROLE.ADMIN
  const showQuotaReminder = quotaRemindEnabled && !isTeamEmployee
  const [loading, setLoading] = useState(false)
  const [settings, setSettings] = useState(() => normalizeUserSettings())
  const [thresholdInput, setThresholdInput] = useState('')
  const [hasWebhookSecret, setHasWebhookSecret] = useState(false)
  const [hasBarkUrl, setHasBarkUrl] = useState(false)
  const [hasGotifyToken, setHasGotifyToken] = useState(false)
  const [clearWebhookSecret, setClearWebhookSecret] = useState(false)

  // Update form field helper
  const updateField = useCallback(
    <K extends keyof typeof settings>(
      field: K,
      value: (typeof settings)[K]
    ) => {
      setSettings((prev) => ({ ...prev, [field]: value }))
    },
    []
  )

  useEffect(() => {
    const flags = readSecretConfiguredFlags(profile)
    setHasWebhookSecret(flags.hasWebhookSecret)
    setHasBarkUrl(flags.hasBarkUrl)
    setHasGotifyToken(flags.hasGotifyToken)
    setClearWebhookSecret(false)
    if (profile?.setting) {
      const next = normalizeUserSettings(profile.setting)
      setSettings({
        ...next,
        webhook_secret: '',
        bark_url: '',
        gotify_token: '',
      })
      // 0 means the site default and shows as an empty input.
      setThresholdInput(
        next.quota_warning_threshold
          ? String(quotaUnitsToDollars(next.quota_warning_threshold))
          : ''
      )
    }
  }, [profile])

  const handleSave = async () => {
    try {
      setLoading(true)
      const { record_ip_log: _recordIpLog, ...notificationSettings } = settings
      const payload: UpdateUserSettingsRequest & {
        clear_webhook_secret?: boolean
      } = {
        ...notificationSettings,
        webhook_secret:
          notificationSettings.notify_type === 'webhook' && clearWebhookSecret
            ? ''
            : notificationSettings.webhook_secret,
        ...(notificationSettings.notify_type === 'webhook' && clearWebhookSecret
          ? { clear_webhook_secret: true }
          : {}),
      }
      const response = await updateUserSettings(payload)

      if (response.success) {
        toast.success(t('Settings updated successfully'))
        onUpdate()
      } else {
        handleServerError(response, t('Failed to update settings'))
      }
    } catch (error) {
      handleServerError(error, t('Failed to update settings'))
    } finally {
      setLoading(false)
    }
  }

  const notifyType = settings.notify_type
  // Switches the user never chose: reminders stay on, and root alone gets
  // channel notifications.
  const quotaWarningEnabled = settings.quota_warning_enabled ?? true
  const channelNotifyEnabled =
    settings.channel_notify_enabled ?? role >= ROLE.SUPER_ADMIN
  const tokensOnly = getCurrencyDisplay().meta.kind === 'tokens'

  return (
    <div className='space-y-4 sm:space-y-6'>
      {!showQuotaReminder && !isAdmin && (
        <p className='text-muted-foreground text-sm'>
          {t('No notifications are available to you right now')}
        </p>
      )}

      {(showQuotaReminder || isAdmin) && (
        <>
          {/* Notification Type */}
          <div className='space-y-2.5'>
            <Label>{t('Notification Method')}</Label>
            <ToggleGroup
              value={[notifyType]}
              onValueChange={(value) => {
                const nextValue = value.find((item) => item !== notifyType)
                if (nextValue) {
                  updateField('notify_type', nextValue as NotifyType)
                }
              }}
              aria-label={t('Notification Method')}
              variant='outline'
              size='lg'
              spacing={2}
              className='grid w-full grid-cols-2 gap-2 sm:grid-cols-4 sm:gap-3'
            >
              {NOTIFICATION_METHODS.map((method) => {
                const Icon = NOTIFICATION_ICONS[method.value]
                return (
                  <ToggleGroupItem
                    key={method.value}
                    value={method.value}
                    className='h-auto min-h-14 w-full flex-col gap-1.5 px-3 py-3 sm:min-h-16 [--toggle-pressed-bg:var(--brand-soft)] [--toggle-pressed-border:color-mix(in_oklch,var(--brand)_35%,var(--border))] [--toggle-pressed-fg:var(--brand-ink)]'
                  >
                    <Icon className='h-4 w-4 sm:h-5 sm:w-5' />
                    <span className='max-w-full truncate text-xs font-medium sm:text-sm'>
                      {t(method.label)}
                    </span>
                  </ToggleGroupItem>
                )
              })}
            </ToggleGroup>
          </div>

          {/* Email Settings */}
          {notifyType === 'email' && (
            <div className='space-y-1.5'>
              <Label htmlFor='notifyEmail'>{t('Notification Email')}</Label>
              <Input
                id='notifyEmail'
                type='email'
                className='h-9'
                value={settings.notification_email}
                onChange={(e) =>
                  updateField('notification_email', e.target.value)
                }
                placeholder={t('Leave empty to use account email')}
              />
            </div>
          )}

          {/* Webhook Settings */}
          {notifyType === 'webhook' && (
            <>
              <div className='space-y-1.5'>
                <Label htmlFor='webhookUrl'>{t('Webhook URL')}</Label>
                <Input
                  id='webhookUrl'
                  type='url'
                  className='h-9'
                  value={settings.webhook_url}
                  onChange={(e) => updateField('webhook_url', e.target.value)}
                  placeholder={t('https://example.com/webhook')}
                />
              </div>
              <div className='space-y-1.5'>
                <div className='flex items-center gap-2'>
                  <Label htmlFor='webhookSecret'>{t('Webhook Secret')}</Label>
                  {hasWebhookSecret && !clearWebhookSecret && (
                    <Badge variant='secondary'>{t('Configured')}</Badge>
                  )}
                </div>
                <PasswordInput
                  id='webhookSecret'
                  autoComplete='new-password'
                  value={settings.webhook_secret}
                  onChange={(e) => {
                    setClearWebhookSecret(false)
                    updateField('webhook_secret', e.target.value)
                  }}
                  placeholder={
                    hasWebhookSecret && !clearWebhookSecret
                      ? t('Leave empty to keep the current key')
                      : t('Enter secret key')
                  }
                />
                {hasWebhookSecret && (
                  <>
                    <p className='text-muted-foreground text-xs'>
                      {t('Leave blank to keep the existing credential')}
                    </p>
                    <div className='flex items-center gap-2 pt-1'>
                      <Checkbox
                        id='clearWebhookSecret'
                        disabled={loading}
                        checked={clearWebhookSecret}
                        onCheckedChange={(checked) => {
                          const shouldClear = checked === true
                          setClearWebhookSecret(shouldClear)
                          if (shouldClear) {
                            updateField('webhook_secret', '')
                          }
                        }}
                      />
                      <Label
                        htmlFor='clearWebhookSecret'
                        className='cursor-pointer text-xs font-normal'
                      >
                        {t('Clear saved credential')}
                      </Label>
                    </div>
                  </>
                )}
              </div>
            </>
          )}

          {/* Bark Settings */}
          {notifyType === 'bark' && (
            <div className='space-y-1.5'>
              <div className='flex items-center gap-2'>
                <Label htmlFor='barkUrl'>{t('Bark Push URL')}</Label>
                {hasBarkUrl && (
                  <Badge variant='secondary'>{t('Configured')}</Badge>
                )}
              </div>
              <Input
                id='barkUrl'
                type='url'
                autoComplete='off'
                className='h-9'
                value={settings.bark_url}
                onChange={(e) => updateField('bark_url', e.target.value)}
                placeholder={
                  hasBarkUrl
                    ? t('Leave empty to keep unchanged')
                    : t('https://api.day.app/yourkey/{{title}}/{{content}}', {
                        skipInterpolation: true,
                      })
                }
              />
              {hasBarkUrl && (
                <p className='text-muted-foreground text-xs'>
                  {t('Leave blank to keep the existing credential')}
                </p>
              )}
              <p className='text-muted-foreground text-xs'>
                {t('Template variables:')} {'{{title}}'}, {'{{content}}'}
              </p>
            </div>
          )}

          {/* Gotify Settings */}
          {notifyType === 'gotify' && (
            <>
              <div className='space-y-1.5'>
                <Label htmlFor='gotifyUrl'>{t('Gotify Server URL')}</Label>
                <Input
                  id='gotifyUrl'
                  type='url'
                  className='h-9'
                  value={settings.gotify_url}
                  onChange={(e) => updateField('gotify_url', e.target.value)}
                  placeholder={t('https://gotify.example.com')}
                />
                <p className='text-muted-foreground text-xs'>
                  {t('Enter the full URL of your Gotify server')}
                </p>
              </div>
              <div className='space-y-1.5'>
                <div className='flex items-center gap-2'>
                  <Label htmlFor='gotifyToken'>
                    {t('Gotify Application Token')}
                  </Label>
                  {hasGotifyToken && (
                    <Badge variant='secondary'>{t('Configured')}</Badge>
                  )}
                </div>
                <PasswordInput
                  id='gotifyToken'
                  autoComplete='new-password'
                  value={settings.gotify_token}
                  onChange={(e) => updateField('gotify_token', e.target.value)}
                  placeholder={
                    hasGotifyToken
                      ? t('Leave empty to keep the current token')
                      : t('Enter application token')
                  }
                />
                <p className='text-muted-foreground text-xs'>
                  {hasGotifyToken
                    ? t('Leave blank to keep the existing credential')
                    : t('Token obtained from your Gotify application')}
                </p>
              </div>
              <div className='space-y-1.5'>
                <Label htmlFor='gotifyPriority'>{t('Message Priority')}</Label>
                <Input
                  id='gotifyPriority'
                  type='number'
                  className='h-9'
                  min='0'
                  max='10'
                  value={settings.gotify_priority}
                  onChange={(e) =>
                    updateField('gotify_priority', Number(e.target.value))
                  }
                  placeholder='5'
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Priority level from 0 (lowest) to 10 (highest), default is 5'
                  )}
                </p>
              </div>
              <div className='bg-muted/50 rounded-lg border p-3 sm:p-4'>
                <h5 className='mb-1.5 text-sm font-medium sm:mb-2'>
                  {t('Setup Instructions')}
                </h5>
                <ol className='text-muted-foreground space-y-1 text-xs'>
                  <li>{t('1. Create an application in your Gotify server')}</li>
                  <li>{t('2. Copy the application token')}</li>
                  <li>
                    {t('3. Enter your Gotify server URL and token above')}
                  </li>
                </ol>
                <p className='text-muted-foreground mt-3 text-xs'>
                  {t('Learn more:')}{' '}
                  <a
                    href='https://gotify.net/'
                    target='_blank'
                    rel='noopener noreferrer'
                    className='text-primary underline underline-offset-4'
                  >
                    {t('Gotify Documentation')}
                  </a>
                </p>
              </div>
            </>
          )}

          {/* Notifications to receive */}
          <div className='space-y-2.5'>
            <h4 className='text-sm font-medium'>
              {t('Notifications to receive')}
            </h4>
            <SettingsControlGroup>
              {showQuotaReminder && (
                <>
                  <SettingsSwitchField
                    controlId='quotaWarningEnabled'
                    label={t('Low balance reminder')}
                    description={t(
                      'Remind me once when my balance drops below the threshold'
                    )}
                    checked={quotaWarningEnabled}
                    onCheckedChange={(checked) =>
                      updateField('quota_warning_enabled', checked)
                    }
                  />
                  {quotaWarningEnabled && (
                    <SettingsControlChildren className='space-y-1.5 pb-1'>
                      <Label htmlFor='threshold'>
                        {t('Reminder threshold')} ({getCurrencyLabel()})
                      </Label>
                      <Input
                        id='threshold'
                        type='number'
                        className='h-9'
                        min={0}
                        step={tokensOnly ? 1 : 0.000001}
                        value={thresholdInput}
                        onChange={(e) => {
                          const value = e.target.value
                          setThresholdInput(value)
                          updateField(
                            'quota_warning_threshold',
                            value.trim() === ''
                              ? 0
                              : parseQuotaFromDollars(Number(value))
                          )
                        }}
                        placeholder={String(
                          quotaUnitsToDollars(quotaRemindThreshold)
                        )}
                      />
                      <p className='text-muted-foreground text-xs'>
                        {t('Leave empty to use the site default ({{amount}})', {
                          amount: formatQuota(quotaRemindThreshold),
                        })}
                      </p>
                    </SettingsControlChildren>
                  )}
                </>
              )}
              {isAdmin && (
                <>
                  <SettingsSwitchField
                    controlId='channelNotify'
                    label={t('Channel notifications')}
                    description={t(
                      'When a channel is disabled or re-enabled automatically, or a test of all channels finishes'
                    )}
                    checked={channelNotifyEnabled}
                    onCheckedChange={(checked) =>
                      updateField('channel_notify_enabled', checked)
                    }
                  />
                  <SettingsSwitchField
                    controlId='upstreamModelUpdateNotify'
                    label={t('Upstream Model Updates')}
                    description={t(
                      'Only available for admins. When enabled, you will receive a summary notification via your selected method when the scheduled model check detects upstream model changes or check failures.'
                    )}
                    checked={settings.upstream_model_update_notify_enabled}
                    onCheckedChange={(checked) =>
                      updateField(
                        'upstream_model_update_notify_enabled',
                        checked
                      )
                    }
                  />
                </>
              )}
            </SettingsControlGroup>
          </div>
        </>
      )}

      {/* Divider */}
      <div className='border-t' />

      {/* Preferences Section */}
      <div className='space-y-3'>
        <div>
          <h4 className='text-sm font-medium'>{t('Preferences')}</h4>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t('Configure your account behavior preferences')}
          </p>
        </div>

        {/* Accept Unset Model Price */}
        <div className='flex items-start justify-between gap-3 rounded-lg border p-3 sm:items-center sm:p-4'>
          <div className='space-y-0.5'>
            <Label htmlFor='acceptUnsetPrice'>
              {t('Accept Unpriced Models')}
            </Label>
            <p className='text-muted-foreground text-xs sm:text-sm'>
              {t('Allow using models without price configuration')}
            </p>
          </div>
          <Switch
            id='acceptUnsetPrice'
            className='shrink-0'
            checked={settings.accept_unset_model_ratio_model}
            onCheckedChange={(checked) =>
              updateField('accept_unset_model_ratio_model', checked)
            }
          />
        </div>
      </div>

      {/* Save Button */}
      <div className='flex justify-end'>
        <Button variant='brand' onClick={handleSave} disabled={loading}>
          {loading && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
          {loading ? t('Saving...') : t('Save Settings')}
        </Button>
      </div>
    </div>
  )
}
