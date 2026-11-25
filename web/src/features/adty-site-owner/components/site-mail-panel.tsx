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
import { type FormEvent, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { PasswordInput } from '@/components/password-input'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import type { SiteOwnerAction } from '@/features/auth/secure-verification'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getOwnerSiteMailConfig,
  saveOwnerSiteMailConfig,
  siteOwnerKeys,
  testOwnerSiteMailConfig,
} from '../api'
import type {
  OwnerSite,
  SiteMailConfig,
  SiteMailConfigUpdate,
  SiteMailProvider,
} from '../types'
import { useOwnerVerification } from './owner-verification'

type SmtpSecurityMode = 'none' | 'ssl_tls' | 'starttls'

const MAIL_CONFIG_ACTION: SiteOwnerAction = 'mail.config'

type MailDraft = {
  provider: SiteMailProvider
  sender_name: string
  smtp_server: string
  smtp_port: string
  smtp_account: string
  smtp_from: string
  smtp_sender_name: string
  smtp_token: string
  clear_smtp_token: boolean
  smtp_ssl_enabled: boolean
  smtp_starttls_enabled: boolean
  smtp_insecure_skip_verify: boolean
  smtp_force_auth_login: boolean
  resend_from: string
  resend_sender_name: string
  resend_api_key: string
  clear_resend_api_key: boolean
}

const mailConfigQueryKey = [...siteOwnerKeys.all, 'mail-config'] as const

function getSmtpSecurityMode(values: {
  smtp_ssl_enabled: boolean
  smtp_starttls_enabled: boolean
}): SmtpSecurityMode {
  if (values.smtp_ssl_enabled) return 'ssl_tls'
  if (values.smtp_starttls_enabled) return 'starttls'
  return 'none'
}

function toDraft(cfg: SiteMailConfig): MailDraft {
  return {
    provider: cfg.provider || 'platform',
    sender_name: cfg.sender_name ?? '',
    smtp_server: cfg.smtp_server ?? '',
    smtp_port: String(cfg.smtp_port > 0 ? cfg.smtp_port : 587),
    smtp_account: cfg.smtp_account ?? '',
    smtp_from: cfg.smtp_from ?? '',
    smtp_sender_name: cfg.smtp_sender_name ?? '',
    smtp_token: '',
    clear_smtp_token: false,
    smtp_ssl_enabled: Boolean(cfg.smtp_ssl_enabled),
    smtp_starttls_enabled: Boolean(cfg.smtp_starttls_enabled),
    smtp_insecure_skip_verify: Boolean(cfg.smtp_insecure_skip_verify),
    smtp_force_auth_login: Boolean(cfg.smtp_force_auth_login),
    resend_from: cfg.resend_from ?? '',
    resend_sender_name: cfg.resend_sender_name ?? '',
    resend_api_key: '',
    clear_resend_api_key: false,
  }
}

function serializeDraft(draft: MailDraft): string {
  return JSON.stringify(draft)
}

export function SiteMailPanel(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const ownerVerification = useOwnerVerification()
  const isClosing = props.site.status === 'closing'
  const fallbackSenderName = props.site.brand.name || props.site.host

  const configQuery = useQuery({
    queryKey: mailConfigQueryKey,
    queryFn: getOwnerSiteMailConfig,
  })

  const [draft, setDraft] = useState<MailDraft | null>(null)
  const [baseline, setBaseline] = useState<MailDraft | null>(null)
  const [testEmail, setTestEmail] = useState('')
  const [validationError, setValidationError] = useState<string | null>(null)
  const initializedRef = useRef(false)

  const isDirty = useMemo(() => {
    if (!draft || !baseline) return false
    return serializeDraft(draft) !== serializeDraft(baseline)
  }, [draft, baseline])

  useDraftNavigation(isDirty)

  useEffect(() => {
    if (!configQuery.data) return
    const next = toDraft(configQuery.data)
    if (!initializedRef.current || !isDirty) {
      initializedRef.current = true
      setDraft(next)
      setBaseline(next)
    }
  }, [configQuery.data, isDirty])

  const updateDraft = (patch: Partial<MailDraft>) => {
    setValidationError(null)
    setDraft((current) => (current ? { ...current, ...patch } : current))
  }

  const save = useMutation({
    mutationFn: async (payload: SiteMailConfigUpdate) => {
      const proof = await ownerVerification.verify(
        MAIL_CONFIG_ACTION,
        props.site.id
      )
      if (!proof) return null
      return saveOwnerSiteMailConfig(payload, proof)
    },
    onSuccess: (updated) => {
      if (!updated) return
      queryClient.setQueryData(mailConfigQueryKey, updated)
      const next = toDraft(updated)
      setDraft(next)
      setBaseline(next)
      setValidationError(null)
      toast.success(t('Saved successfully'))
    },
    onError: (err) => handleServerError(err),
  })

  const sendTest = useMutation({
    mutationFn: (to: string) => testOwnerSiteMailConfig(to),
    onSuccess: () => {
      toast.success(t('Test email sent'))
    },
    onError: (err) => handleServerError(err),
  })

  if (configQuery.isLoading || (!draft && !configQuery.isError)) {
    return <Skeleton className='h-64 w-full' />
  }

  if (configQuery.isError || !configQuery.data || !draft) {
    return (
      <ErrorState
        className='min-h-48'
        onRetry={() => void configQuery.refetch()}
      />
    )
  }

  const savedConfig = configQuery.data
  const disabled = isClosing || save.isPending || ownerVerification.verifying

  const handleSave = (event: FormEvent) => {
    event.preventDefault()
    if (disabled || !isDirty) return

    if (draft.provider === 'smtp') {
      if (!draft.smtp_server.trim()) {
        setValidationError(t('Enter an SMTP host'))
        return
      }
      const port = draft.smtp_port.trim()
      if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) {
        setValidationError(t('Port must be a positive integer'))
        return
      }
      const effectiveFrom = draft.smtp_from.trim() || draft.smtp_account.trim()
      if (!effectiveFrom || !effectiveFrom.includes('@')) {
        setValidationError(t('Enter a valid sender email address'))
        return
      }
    }

    if (draft.provider === 'resend') {
      const from = draft.resend_from.trim()
      if (!/^[^\s<>@]+@[^\s<>@]+\.[^\s<>@]+$/.test(from)) {
        setValidationError(t('Enter a valid sender email address'))
        return
      }
      const hasResendKey =
        Boolean(draft.resend_api_key.trim()) ||
        (savedConfig.resend_api_key_configured && !draft.clear_resend_api_key)
      if (!hasResendKey) {
        setValidationError(t('Configure a Resend API key before enabling Resend'))
        return
      }
    }

    const securityMode = getSmtpSecurityMode(draft)
    const payload: SiteMailConfigUpdate = {
      provider: draft.provider,
      sender_name: draft.sender_name.trim(),
      smtp_server: draft.smtp_server.trim(),
      smtp_port: Number(draft.smtp_port.trim()) || 587,
      smtp_account: draft.smtp_account.trim(),
      smtp_from: draft.smtp_from.trim(),
      smtp_sender_name: draft.smtp_sender_name.trim(),
      ...(draft.smtp_token.trim()
        ? { smtp_token: draft.smtp_token.trim() }
        : {}),
      clear_smtp_token: draft.clear_smtp_token,
      smtp_ssl_enabled: securityMode === 'ssl_tls',
      smtp_starttls_enabled: securityMode === 'starttls',
      smtp_insecure_skip_verify: draft.smtp_insecure_skip_verify,
      smtp_force_auth_login: draft.smtp_force_auth_login,
      resend_from: draft.resend_from.trim(),
      resend_sender_name: draft.resend_sender_name.trim(),
      ...(draft.resend_api_key.trim()
        ? { resend_api_key: draft.resend_api_key.trim() }
        : {}),
      clear_resend_api_key: draft.clear_resend_api_key,
    }
    save.mutate(payload)
  }

  const handleSendTest = (event: FormEvent) => {
    event.preventDefault()
    const target = testEmail.trim()
    if (!target || isClosing || isDirty || sendTest.isPending) return
    sendTest.mutate(target)
  }

  return (
    <div className='flex flex-col gap-6'>
      <Card>
        <CardHeader>
          <CardTitle>{t('Email configuration')}</CardTitle>
          <CardDescription>
            {t(
              'Only the selected method sends email. The other method keeps its settings.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            onSubmit={handleSave}
            autoComplete='off'
            className='flex flex-col gap-6'
          >
            <div className='space-y-3'>
              <Label>{t('Email sending method')}</Label>
              <RadioGroup
                value={draft.provider}
                onValueChange={(value) =>
                  updateDraft({ provider: value as SiteMailProvider })
                }
                disabled={disabled}
                className='grid-cols-1 gap-3 sm:grid-cols-3'
              >
                <Label
                  className='flex cursor-pointer items-center gap-3 rounded-lg border p-4'
                  htmlFor='site-mail-provider-platform'
                >
                  <RadioGroupItem
                    value='platform'
                    id='site-mail-provider-platform'
                  />
                  {t('Platform default')}
                </Label>
                <Label
                  className='flex cursor-pointer items-center gap-3 rounded-lg border p-4'
                  htmlFor='site-mail-provider-smtp'
                >
                  <RadioGroupItem value='smtp' id='site-mail-provider-smtp' />
                  {t('SMTP')}
                </Label>
                <Label
                  className='flex cursor-pointer items-center gap-3 rounded-lg border p-4'
                  htmlFor='site-mail-provider-resend'
                >
                  <RadioGroupItem
                    value='resend'
                    id='site-mail-provider-resend'
                  />
                  {t('Resend API')}
                </Label>
              </RadioGroup>
            </div>

            {draft.provider === 'platform' && (
              <div className='space-y-2'>
                <Label htmlFor='site-mail-platform-sender-name'>
                  {t('Sender display name')}
                </Label>
                <Input
                  id='site-mail-platform-sender-name'
                  autoComplete='off'
                  maxLength={128}
                  disabled={disabled}
                  placeholder={fallbackSenderName}
                  value={draft.sender_name}
                  onChange={(event) =>
                    updateDraft({ sender_name: event.target.value })
                  }
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Uses the platform mail transport with your site display name. Leave blank to use your site name.'
                  )}
                </p>
              </div>
            )}

            {draft.provider === 'smtp' && (
              <div className='flex flex-col gap-6'>
                <div className='space-y-2'>
                  <Label htmlFor='site-mail-smtp-server'>
                    {t('SMTP Host')}
                  </Label>
                  <Input
                    id='site-mail-smtp-server'
                    autoComplete='off'
                    disabled={disabled}
                    placeholder='smtp.example.com'
                    value={draft.smtp_server}
                    onChange={(event) =>
                      updateDraft({ smtp_server: event.target.value })
                    }
                  />
                  <p className='text-muted-foreground text-xs'>
                    {t('Hostname or IP of your SMTP provider')}
                  </p>
                </div>

                <div className='grid gap-6 md:grid-cols-2'>
                  <div className='space-y-2'>
                    <Label htmlFor='site-mail-smtp-port'>{t('Port')}</Label>
                    <Input
                      id='site-mail-smtp-port'
                      autoComplete='off'
                      type='number'
                      min={1}
                      max={65535}
                      disabled={disabled}
                      placeholder='587'
                      value={draft.smtp_port}
                      onChange={(event) =>
                        updateDraft({ smtp_port: event.target.value })
                      }
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t('Common ports include 25, 465, and 587')}
                    </p>
                  </div>

                  <div className='space-y-2'>
                    <Label>{t('SMTP encryption')}</Label>
                    <RadioGroup
                      value={getSmtpSecurityMode(draft)}
                      onValueChange={(value) => {
                        const mode = value as SmtpSecurityMode
                        updateDraft({
                          smtp_ssl_enabled: mode === 'ssl_tls',
                          smtp_starttls_enabled: mode === 'starttls',
                        })
                      }}
                      disabled={disabled}
                      className='gap-3'
                    >
                      <div className='flex items-center gap-2'>
                        <RadioGroupItem
                          value='none'
                          id='site-smtp-security-none'
                        />
                        <Label
                          htmlFor='site-smtp-security-none'
                          className='cursor-pointer font-normal'
                        >
                          {t('No encryption')}
                        </Label>
                      </div>
                      <div className='flex items-center gap-2'>
                        <RadioGroupItem
                          value='ssl_tls'
                          id='site-smtp-security-ssl-tls'
                        />
                        <Label
                          htmlFor='site-smtp-security-ssl-tls'
                          className='cursor-pointer font-normal'
                        >
                          {t('SSL/TLS')}
                        </Label>
                      </div>
                      <div className='flex items-center gap-2'>
                        <RadioGroupItem
                          value='starttls'
                          id='site-smtp-security-starttls'
                        />
                        <Label
                          htmlFor='site-smtp-security-starttls'
                          className='cursor-pointer font-normal'
                        >
                          {t('STARTTLS')}
                        </Label>
                      </div>
                    </RadioGroup>
                    <p className='text-muted-foreground text-xs'>
                      {t('Choose one SMTP transport security mode')}
                    </p>
                  </div>

                  <div className='flex items-center justify-between gap-4 rounded-lg border p-4'>
                    <div className='space-y-1'>
                      <Label htmlFor='site-mail-smtp-skip-verify'>
                        {t('Skip SMTP TLS certificate verification')}
                      </Label>
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'Allow self-signed or hostname-mismatched SMTP certificates'
                        )}
                      </p>
                    </div>
                    <Switch
                      id='site-mail-smtp-skip-verify'
                      disabled={disabled}
                      checked={draft.smtp_insecure_skip_verify}
                      onCheckedChange={(checked) =>
                        updateDraft({ smtp_insecure_skip_verify: checked })
                      }
                    />
                  </div>

                  <div className='flex items-center justify-between gap-4 rounded-lg border p-4'>
                    <div className='space-y-1'>
                      <Label htmlFor='site-mail-smtp-force-login'>
                        {t('Force AUTH LOGIN')}
                      </Label>
                      <p className='text-muted-foreground text-xs'>
                        {t('Force SMTP authentication using AUTH LOGIN method')}
                      </p>
                    </div>
                    <Switch
                      id='site-mail-smtp-force-login'
                      disabled={disabled}
                      checked={draft.smtp_force_auth_login}
                      onCheckedChange={(checked) =>
                        updateDraft({ smtp_force_auth_login: checked })
                      }
                    />
                  </div>
                </div>

                <div className='space-y-2'>
                  <Label htmlFor='site-mail-smtp-account'>
                    {t('Username')}
                  </Label>
                  <Input
                    id='site-mail-smtp-account'
                    autoComplete='off'
                    disabled={disabled}
                    placeholder='noreply@example.com'
                    value={draft.smtp_account}
                    onChange={(event) =>
                      updateDraft({ smtp_account: event.target.value })
                    }
                  />
                  <p className='text-muted-foreground text-xs'>
                    {t('Account used when authenticating with the SMTP server')}
                  </p>
                </div>

                <div className='space-y-2'>
                  <div className='flex items-center gap-2'>
                    <Label htmlFor='site-mail-smtp-token'>
                      {t('Password / Access Token')}
                    </Label>
                    {savedConfig.smtp_token_configured &&
                      !draft.clear_smtp_token && (
                        <Badge variant='secondary'>{t('Configured')}</Badge>
                      )}
                  </div>
                  <PasswordInput
                    id='site-mail-smtp-token'
                    autoComplete='new-password'
                    disabled={disabled}
                    placeholder={t('Enter new token to update')}
                    value={draft.smtp_token}
                    onChange={(event) =>
                      updateDraft({
                        smtp_token: event.target.value,
                        clear_smtp_token: false,
                      })
                    }
                  />
                  <p className='text-muted-foreground text-xs'>
                    {t('Leave blank to keep the existing credential')}
                  </p>
                  {savedConfig.smtp_token_configured && (
                    <div className='flex items-center gap-2 pt-1'>
                      <Checkbox
                        id='site-mail-clear-smtp-token'
                        disabled={disabled}
                        checked={draft.clear_smtp_token}
                        onCheckedChange={(checked) => {
                          const shouldClear = Boolean(checked)
                          updateDraft({
                            clear_smtp_token: shouldClear,
                            ...(shouldClear ? { smtp_token: '' } : {}),
                          })
                        }}
                      />
                      <Label
                        htmlFor='site-mail-clear-smtp-token'
                        className='cursor-pointer text-xs font-normal'
                      >
                        {t('Clear saved credential')}
                      </Label>
                    </div>
                  )}
                </div>

                <div className='grid gap-6 md:grid-cols-2'>
                  <div className='space-y-2'>
                    <Label htmlFor='site-mail-smtp-from'>
                      {t('From Address')}
                    </Label>
                    <Input
                      id='site-mail-smtp-from'
                      autoComplete='off'
                      disabled={disabled}
                      placeholder='noreply@example.com'
                      value={draft.smtp_from}
                      onChange={(event) =>
                        updateDraft({ smtp_from: event.target.value })
                      }
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t('Sender email address used in outgoing messages')}
                    </p>
                  </div>

                  <div className='space-y-2'>
                    <Label htmlFor='site-mail-smtp-sender-name'>
                      {t('Sender display name')}
                    </Label>
                    <Input
                      id='site-mail-smtp-sender-name'
                      autoComplete='off'
                      maxLength={128}
                      disabled={disabled}
                      placeholder={fallbackSenderName}
                      value={draft.smtp_sender_name}
                      onChange={(event) =>
                        updateDraft({ smtp_sender_name: event.target.value })
                      }
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'The name recipients see. Leave blank to use the site name.'
                      )}
                    </p>
                  </div>
                </div>
              </div>
            )}

            {draft.provider === 'resend' && (
              <div className='flex flex-col gap-6'>
                <div className='grid gap-6 md:grid-cols-2'>
                  <div className='space-y-2'>
                    <Label htmlFor='site-mail-resend-from'>
                      {t('Sender email address')}
                    </Label>
                    <Input
                      id='site-mail-resend-from'
                      autoComplete='off'
                      type='email'
                      disabled={disabled}
                      placeholder='noreply@example.com'
                      value={draft.resend_from}
                      onChange={(event) =>
                        updateDraft({ resend_from: event.target.value })
                      }
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t('The domain must be verified in Resend')}
                    </p>
                  </div>

                  <div className='space-y-2'>
                    <Label htmlFor='site-mail-resend-sender-name'>
                      {t('Sender display name')}
                    </Label>
                    <Input
                      id='site-mail-resend-sender-name'
                      autoComplete='off'
                      maxLength={128}
                      disabled={disabled}
                      placeholder={fallbackSenderName}
                      value={draft.resend_sender_name}
                      onChange={(event) =>
                        updateDraft({ resend_sender_name: event.target.value })
                      }
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'The name recipients see. Leave blank to use the site name.'
                      )}
                    </p>
                  </div>
                </div>

                <div className='space-y-2'>
                  <div className='flex items-center gap-2'>
                    <Label htmlFor='site-mail-resend-key'>
                      {t('Resend API key')}
                    </Label>
                    {savedConfig.resend_api_key_configured &&
                      !draft.clear_resend_api_key && (
                        <Badge variant='secondary'>{t('Configured')}</Badge>
                      )}
                  </div>
                  <PasswordInput
                    id='site-mail-resend-key'
                    autoComplete='new-password'
                    disabled={disabled}
                    placeholder={t('Enter new token to update')}
                    value={draft.resend_api_key}
                    onChange={(event) =>
                      updateDraft({
                        resend_api_key: event.target.value,
                        clear_resend_api_key: false,
                      })
                    }
                  />
                  <p className='text-muted-foreground text-xs'>
                    {t('Leave blank to keep the existing key')}
                  </p>
                  {savedConfig.resend_api_key_configured && (
                    <div className='flex items-center gap-2 pt-1'>
                      <Checkbox
                        id='site-mail-clear-resend-key'
                        disabled={disabled}
                        checked={draft.clear_resend_api_key}
                        onCheckedChange={(checked) => {
                          const shouldClear = Boolean(checked)
                          updateDraft({
                            clear_resend_api_key: shouldClear,
                            ...(shouldClear ? { resend_api_key: '' } : {}),
                          })
                        }}
                      />
                      <Label
                        htmlFor='site-mail-clear-resend-key'
                        className='cursor-pointer text-xs font-normal'
                      >
                        {t('Clear saved API key')}
                      </Label>
                    </div>
                  )}
                </div>
              </div>
            )}

            {validationError && (
              <p role='alert' className='text-destructive text-sm'>
                {validationError}
              </p>
            )}

            <div>
              <Button
                type='submit'
                variant='brand'
                disabled={disabled || !isDirty}
              >
                {save.isPending ? t('Saving...') : t('Save')}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('Send test email')}</CardTitle>
          <CardDescription>
            {t(
              'Send a test message using the currently saved email configuration.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSendTest} className='flex flex-col gap-3'>
            <div className='flex flex-col gap-3 sm:flex-row sm:items-end'>
              <div className='flex-1 space-y-2'>
                <Label htmlFor='site-mail-test-to'>
                  {t('Recipient email address')}
                </Label>
                <Input
                  id='site-mail-test-to'
                  type='email'
                  autoComplete='off'
                  disabled={isClosing || sendTest.isPending}
                  placeholder='you@example.com'
                  value={testEmail}
                  onChange={(event) => setTestEmail(event.target.value)}
                />
              </div>
              <Button
                type='submit'
                variant='outline'
                disabled={
                  isClosing ||
                  isDirty ||
                  sendTest.isPending ||
                  !testEmail.trim()
                }
              >
                {sendTest.isPending ? t('Sending...') : t('Send test email')}
              </Button>
            </div>
            {isDirty && (
              <p className='text-muted-foreground text-xs'>
                {t('Save your changes before sending a test email.')}
              </p>
            )}
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
