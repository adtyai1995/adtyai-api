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
import { Link } from '@tanstack/react-router'
import { type FormEvent, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import type { SiteOwnerAction } from '@/features/auth/secure-verification'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { handleServerError } from '@/lib/handle-server-error'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import {
  getOwnerSiteAuthConfig,
  saveOwnerSiteAuthConfig,
  siteOwnerKeys,
} from '../api'
import type { OwnerSite, SiteAuthConfig, SiteAuthConfigView } from '../types'
import { useOwnerVerification } from './owner-verification'

const AUTH_CONFIG_ACTION: SiteOwnerAction = 'auth.config'
const authConfigQueryKey = [...siteOwnerKeys.all, 'auth-config'] as const

type AuthDraft = {
  password_login_enabled: boolean
  register_enabled: boolean
  password_register_enabled: boolean
  email_verification_enabled: boolean
  email_domain_restriction_enabled: boolean
  email_alias_restriction_enabled: boolean
  email_domain_whitelist_text: string
}

function toDraft(cfg: SiteAuthConfig): AuthDraft {
  return {
    password_login_enabled: cfg.password_login_enabled,
    register_enabled: cfg.register_enabled,
    password_register_enabled: cfg.password_register_enabled,
    email_verification_enabled: cfg.email_verification_enabled,
    email_domain_restriction_enabled: cfg.email_domain_restriction_enabled,
    email_alias_restriction_enabled: cfg.email_alias_restriction_enabled,
    email_domain_whitelist_text: (cfg.email_domain_whitelist ?? []).join('\n'),
  }
}

function parseWhitelistText(text: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const part of text.split(/[\n,\r]+/)) {
    const domain = part.trim().toLowerCase()
    if (domain && !seen.has(domain)) {
      seen.add(domain)
      out.push(domain)
    }
  }
  return out
}

function serializeDraft(draft: AuthDraft): string {
  return JSON.stringify({
    ...draft,
    email_domain_whitelist_text: parseWhitelistText(
      draft.email_domain_whitelist_text
    ).join('\n'),
  })
}

/** Per-subsite Basic Authentication controls in Site Owner Center. */
export function SiteAuthPanel(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const ownerVerification = useOwnerVerification()
  const isClosing = props.site.status === 'closing'

  const configQuery = useQuery({
    queryKey: authConfigQueryKey,
    queryFn: getOwnerSiteAuthConfig,
  })

  const [draft, setDraft] = useState<AuthDraft | null>(null)
  const [baseline, setBaseline] = useState<AuthDraft | null>(null)
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

  const updateDraft = (patch: Partial<AuthDraft>) => {
    setValidationError(null)
    setDraft((current) => (current ? { ...current, ...patch } : current))
  }

  const save = useMutation({
    mutationFn: async (
      payload: SiteAuthConfig
    ): Promise<SiteAuthConfigView | null> => {
      const proof = await ownerVerification.verify(
        AUTH_CONFIG_ACTION,
        props.site.id
      )
      if (!proof) return null
      return saveOwnerSiteAuthConfig(payload, proof)
    },
    onSuccess: (updated) => {
      if (!updated) return
      queryClient.setQueryData(authConfigQueryKey, updated)
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.site })
      void queryClient.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
      const next = toDraft(updated)
      setDraft(next)
      setBaseline(next)
      setValidationError(null)
      toast.success(t('Saved successfully'))
    },
    onError: (error) => handleServerError(error),
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

  const mailConfigured = Boolean(configQuery.data.mail_configured)
  const disabled = isClosing || save.isPending || ownerVerification.verifying
  const canSave = !disabled && isDirty

  const handleSave = (event: FormEvent) => {
    event.preventDefault()
    if (!canSave) return

    const whitelist = parseWhitelistText(draft.email_domain_whitelist_text)
    if (draft.email_verification_enabled && !mailConfigured) {
      setValidationError(
        t(
          "Configure your site's email sending (SMTP or Resend) before enabling email verification"
        )
      )
      return
    }
    if (draft.email_domain_restriction_enabled && whitelist.length === 0) {
      setValidationError(
        t(
          'Cannot enable the email domain restriction. Enter the allowed email domains first.'
        )
      )
      return
    }

    save.mutate({
      password_login_enabled: draft.password_login_enabled,
      register_enabled: draft.register_enabled,
      password_register_enabled: draft.password_register_enabled,
      email_verification_enabled: draft.email_verification_enabled,
      email_domain_restriction_enabled: draft.email_domain_restriction_enabled,
      email_alias_restriction_enabled: draft.email_alias_restriction_enabled,
      email_domain_whitelist: whitelist,
    })
  }

  return (
    <form className='flex flex-col gap-6' onSubmit={handleSave}>
      <SettingsSection
        title={t('Basic Authentication')}
        description={t(
          'Configure login, registration, email verification, and email domain rules for customers of this site independently from the main site.'
        )}
      >
        <div className='space-y-4'>
          <div className='flex items-center justify-between rounded-lg border p-4'>
            <div className='space-y-0.5 pr-4'>
              <Label htmlFor='site-auth-password-login' className='text-sm font-medium'>
                {t('Password Login')}
              </Label>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Allow users to log in with password. Site owners can still sign in on the main site if this is disabled.'
                )}
              </p>
            </div>
            <Switch
              id='site-auth-password-login'
              checked={draft.password_login_enabled}
              disabled={disabled}
              onCheckedChange={(checked) =>
                updateDraft({ password_login_enabled: checked })
              }
            />
          </div>

          <div className='flex items-center justify-between rounded-lg border p-4'>
            <div className='space-y-0.5 pr-4'>
              <Label htmlFor='site-auth-register' className='text-sm font-medium'>
                {t('Registration Enabled')}
              </Label>
              <p className='text-muted-foreground text-xs'>
                {t('Allow new users to register')}
              </p>
            </div>
            <Switch
              id='site-auth-register'
              checked={draft.register_enabled}
              disabled={disabled}
              onCheckedChange={(checked) =>
                updateDraft({ register_enabled: checked })
              }
            />
          </div>

          <div className='flex items-center justify-between rounded-lg border p-4'>
            <div className='space-y-0.5 pr-4'>
              <Label
                htmlFor='site-auth-password-register'
                className='text-sm font-medium'
              >
                {t('Password Registration')}
              </Label>
              <p className='text-muted-foreground text-xs'>
                {t('Allow registration with password')}
              </p>
            </div>
            <Switch
              id='site-auth-password-register'
              checked={draft.password_register_enabled}
              disabled={disabled}
              onCheckedChange={(checked) =>
                updateDraft({ password_register_enabled: checked })
              }
            />
          </div>

          <div className='space-y-3 rounded-lg border p-4'>
            <div className='flex items-center justify-between'>
              <div className='space-y-0.5 pr-4'>
                <div className='flex items-center gap-2'>
                  <Label
                    htmlFor='site-auth-email-verification'
                    className='text-sm font-medium'
                  >
                    {t('Email Verification')}
                  </Label>
                  <Badge variant={mailConfigured ? 'secondary' : 'outline'}>
                    {mailConfigured
                      ? t('Email configured')
                      : t('Email not configured')}
                  </Badge>
                </div>
                <p className='text-muted-foreground text-xs'>
                  {t('Require email verification for new accounts')}
                </p>
              </div>
              <Switch
                id='site-auth-email-verification'
                checked={draft.email_verification_enabled}
                disabled={
                  disabled ||
                  (!mailConfigured && !draft.email_verification_enabled)
                }
                onCheckedChange={(checked) =>
                  updateDraft({ email_verification_enabled: checked })
                }
              />
            </div>
            {!mailConfigured && (
              <Alert>
                <AlertDescription className='flex flex-wrap items-center justify-between gap-2'>
                  <span>
                    {t(
                      "Configure your site's email sending (SMTP or Resend) before enabling email verification"
                    )}
                  </span>
                  <Link
                    to='/site-owner/$section'
                    params={{ section: 'mail' }}
                    className='text-primary font-medium underline underline-offset-4'
                  >
                    {t('Go to email configuration')}
                  </Link>
                </AlertDescription>
              </Alert>
            )}
          </div>

          <div className='flex items-center justify-between rounded-lg border p-4'>
            <div className='space-y-0.5 pr-4'>
              <Label
                htmlFor='site-auth-domain-restriction'
                className='text-sm font-medium'
              >
                {t('Email Domain Restriction')}
              </Label>
              <p className='text-muted-foreground text-xs'>
                {t('Only allow specific email domains')}
              </p>
            </div>
            <Switch
              id='site-auth-domain-restriction'
              checked={draft.email_domain_restriction_enabled}
              disabled={disabled}
              onCheckedChange={(checked) =>
                updateDraft({ email_domain_restriction_enabled: checked })
              }
            />
          </div>

          <div className='flex items-center justify-between rounded-lg border p-4'>
            <div className='space-y-0.5 pr-4'>
              <Label
                htmlFor='site-auth-alias-restriction'
                className='text-sm font-medium'
              >
                {t('Email Alias Restriction')}
              </Label>
              <p className='text-muted-foreground text-xs'>
                {t('Block email aliases (e.g., user+alias@domain.com)')}
              </p>
            </div>
            <Switch
              id='site-auth-alias-restriction'
              checked={draft.email_alias_restriction_enabled}
              disabled={disabled}
              onCheckedChange={(checked) =>
                updateDraft({ email_alias_restriction_enabled: checked })
              }
            />
          </div>

          <div className='space-y-2 rounded-lg border p-4'>
            <Label
              htmlFor='site-auth-domain-whitelist'
              className='text-sm font-medium'
            >
              {t('Email Domain Whitelist')}
            </Label>
            <Textarea
              id='site-auth-domain-whitelist'
              placeholder={'example.com\ncompany.com'}
              rows={4}
              disabled={disabled}
              value={draft.email_domain_whitelist_text}
              onChange={(e) =>
                updateDraft({ email_domain_whitelist_text: e.target.value })
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'One domain per line (only used when domain restriction is enabled)'
              )}
            </p>
          </div>

          {validationError && (
            <Alert variant='destructive'>
              <AlertDescription>{validationError}</AlertDescription>
            </Alert>
          )}

          <div className='flex justify-end gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={!isDirty || disabled}
              onClick={() => {
                if (baseline) setDraft(baseline)
                setValidationError(null)
              }}
            >
              {t('Reset')}
            </Button>
            <Button type='submit' variant='brand' disabled={!canSave}>
              {t('Save')}
            </Button>
          </div>
        </div>
      </SettingsSection>
    </form>
  )
}
