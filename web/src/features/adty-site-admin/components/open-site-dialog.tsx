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
import { CheckCircle2, ChevronRight, RefreshCw } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { PasswordInput } from '@/components/password-input'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  createSite,
  getResellerSettings,
  getSiteTiers,
  siteAdminKeys,
} from '../api'
import { generateOwnerPassword } from '../lib/sites'
import type {
  AdminSite,
  OpenSitePayload,
  PartnerLeadItem,
  ResellerSettings,
} from '../types'

const SUBDOMAIN_PATTERN = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/
const USERNAME_PATTERN = /^[a-zA-Z0-9_.-]{3,20}$/
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

function newOpenIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `open-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export function OpenSiteDialog(props: {
  open: boolean
  settings?: ResellerSettings
  initialLead?: PartnerLeadItem | null
  onOpenChange: (open: boolean) => void
  onViewSite?: (site: AdminSite) => void
  onOpened?: (site: AdminSite) => void
}) {
  const { t } = useTranslation()
  const fieldId = useId()
  const queryClient = useQueryClient()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN

  const [ownerUsername, setOwnerUsername] = useState('')
  const [ownerPassword, setOwnerPassword] = useState('')
  const [ownerEmail, setOwnerEmail] = useState('')
  const [ownerDisplayName, setOwnerDisplayName] = useState('')
  const [subdomainMode, setSubdomainMode] = useState<'auto' | 'custom'>('auto')
  const [subdomain, setSubdomain] = useState('')
  const [rootDomain, setRootDomain] = useState('')
  const [tierCode, setTierCode] = useState('')
  const [tierMode, setTierMode] = useState<'auto' | 'manual'>('auto')
  const [costFactor, setCostFactor] = useState('')
  const [contactInfo, setContactInfo] = useState('')
  const [referrerCode, setReferrerCode] = useState('')
  const [referrerSiteId, setReferrerSiteId] = useState('')
  const [referrerRate, setReferrerRate] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const [createdResult, setCreatedResult] = useState<{
    site: AdminSite
    ownerUsername: string
    ownerPassword: string
  } | null>(null)
  const intentRef = useRef<{ payload: string; key: string } | null>(null)

  const fallbackSettingsQuery = useQuery({
    queryKey: siteAdminKeys.settings,
    queryFn: getResellerSettings,
    enabled:
      props.open &&
      isRoot &&
      props.settings === undefined &&
      createdResult === null,
  })
  const resolvedSettings = props.settings ?? fallbackSettingsQuery.data
  const roots = resolvedSettings?.root_domains ?? []
  const automationOn = resolvedSettings?.domain_automation_enabled === true
  const managedRoot = resolvedSettings?.aliyun_domain?.trim() ?? ''
  const effectiveRoot = automationOn
    ? managedRoot || roots[0] || ''
    : rootDomain || roots[0] || ''
  const defaultPartnerRate =
    resolvedSettings?.partner_referral_default_rate ?? 5

  const lead = props.initialLead ?? null

  useEffect(() => {
    if (!props.open || !lead) return
    setContactInfo(
      `${lead.contact_name} (${lead.contact_channel}: ${lead.contact_value})`
    )
    setReferrerCode(lead.referral_code || '')
    setReferrerSiteId(
      lead.referrer_site_id > 0 ? String(lead.referrer_site_id) : ''
    )
    setReferrerRate('')
    setConfirmed(false)
    setCreatedResult(null)
    intentRef.current = null
  }, [props.open, lead])

  const resetForm = () => {
    setOwnerUsername('')
    setOwnerPassword('')
    setOwnerEmail('')
    setOwnerDisplayName('')
    setSubdomainMode('auto')
    setSubdomain('')
    setRootDomain(roots[0] ?? '')
    setTierCode('')
    setTierMode('auto')
    setCostFactor('')
    setContactInfo('')
    setReferrerCode('')
    setReferrerSiteId('')
    setReferrerRate('')
    setConfirmed(false)
    setCreatedResult(null)
    intentRef.current = null
  }

  const tiers = useQuery({
    queryKey: siteAdminKeys.list('tiers', {}),
    queryFn: getSiteTiers,
    enabled: props.open && isRoot && createdResult === null,
  })
  const enabledTiers = (tiers.data ?? []).filter(
    (item) => item.enabled && item.cost_factor
  )

  const trimmedUsername = ownerUsername.trim()
  const trimmedEmail = ownerEmail.trim()
  const trimmedDisplayName = ownerDisplayName.trim()
  const normalizedSub = subdomain.trim().toLowerCase()

  let usernameError = ''
  if (trimmedUsername !== '') {
    if (trimmedUsername.length < 3 || trimmedUsername.length > 20) {
      usernameError = t('Owner username must be 3 to 20 characters')
    } else if (trimmedUsername.includes('@')) {
      usernameError = t('Username cannot contain @')
    } else if (!USERNAME_PATTERN.test(trimmedUsername)) {
      usernameError = t(
        'Owner username may only contain letters, digits, underscores, dots, and hyphens'
      )
    }
  }

  let passwordError = ''
  if (
    ownerPassword !== '' &&
    (ownerPassword.length < 8 || ownerPassword.length > 128)
  ) {
    passwordError = t('Owner password must be 8 to 128 characters')
  }

  let displayNameError = ''
  if (trimmedDisplayName.length > 20) {
    displayNameError = t('Display name must be 20 characters or fewer')
  }

  let emailError = ''
  if (trimmedEmail !== '' && !EMAIL_PATTERN.test(trimmedEmail)) {
    emailError = t('Enter a valid email address')
  }

  const subdomainError =
    subdomainMode === 'custom' &&
    normalizedSub !== '' &&
    (!SUBDOMAIN_PATTERN.test(normalizedSub) || normalizedSub.startsWith('xn--'))
      ? t(
          'The subdomain may only contain lowercase letters, digits and hyphens, and cannot start or end with a hyphen'
        )
      : ''

  const parsedCostFactor = costFactor.trim() === '' ? null : Number(costFactor)
  let costFactorError = ''
  if (parsedCostFactor !== null) {
    if (!Number.isFinite(parsedCostFactor) || parsedCostFactor <= 0) {
      costFactorError = t('The cost factor must be greater than 0')
    }
  }

  const trimmedReferrerSiteId = referrerSiteId.trim()
  let referrerSiteIdError = ''
  if (trimmedReferrerSiteId !== '') {
    const parsedId = Number(trimmedReferrerSiteId)
    if (!Number.isInteger(parsedId) || parsedId <= 0) {
      referrerSiteIdError = t('Enter a valid referrer site ID')
    }
  }

  const trimmedReferrerRate = referrerRate.trim()
  let referrerRateError = ''
  if (trimmedReferrerRate !== '') {
    const parsedRate = Number(trimmedReferrerRate)
    if (!Number.isFinite(parsedRate) || parsedRate < 0 || parsedRate > 50) {
      referrerRateError = t(
        'Partner referral rate must be between 0% and 50%'
      )
    }
  }

  const openMutation = useMutation({
    mutationFn: () => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      const body: OpenSitePayload = {
        owner_username: trimmedUsername,
        owner_password: ownerPassword,
        subdomain: subdomainMode === 'custom' ? normalizedSub : '',
      }
      if (trimmedEmail !== '') {
        body.owner_email = trimmedEmail
      }
      if (trimmedDisplayName !== '') {
        body.owner_display_name = trimmedDisplayName
      }
      if (!automationOn && effectiveRoot) {
        body.root_domain = effectiveRoot
      }
      if (costFactor.trim() !== '') {
        body.cost_factor = costFactor.trim()
      }
      if (tierCode !== '') {
        body.tier_code = tierCode
        body.tier_mode = tierMode
      }
      if (contactInfo.trim() !== '') {
        body.contact_info = contactInfo.trim()
      }
      if (lead?.id) {
        body.partner_lead_id = lead.id
      }
      if (referrerCode.trim() !== '') {
        body.referrer_code = referrerCode.trim().toUpperCase()
      }
      if (trimmedReferrerSiteId !== '') {
        body.referrer_site_id = Number(trimmedReferrerSiteId)
      }
      if (trimmedReferrerRate !== '') {
        body.referrer_rate = trimmedReferrerRate
      }
      const serialized = JSON.stringify(body)
      if (intentRef.current?.payload !== serialized) {
        intentRef.current = {
          payload: serialized,
          key: newOpenIdempotencyKey(),
        }
      }
      return createSite(body, intentRef.current.key)
    },
    onSuccess: (site) => {
      intentRef.current = null
      setCreatedResult({
        site,
        ownerUsername: site.owner_username || trimmedUsername,
        ownerPassword,
      })
      toast.success(t('Site opened'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (error) => handleServerError(error),
  })

  const canSubmit =
    trimmedUsername !== '' &&
    usernameError === '' &&
    ownerPassword.length >= 8 &&
    ownerPassword.length <= 128 &&
    passwordError === '' &&
    displayNameError === '' &&
    emailError === '' &&
    confirmed &&
    !openMutation.isPending &&
    (subdomainMode === 'auto' || normalizedSub !== '') &&
    subdomainError === '' &&
    costFactorError === '' &&
    referrerSiteIdError === '' &&
    referrerRateError === '' &&
    effectiveRoot !== ''

  const previewWebsiteHost = effectiveRoot
    ? `${subdomainMode === 'custom' && normalizedSub ? normalizedSub : 's********'}.${effectiveRoot}`
    : '-'
  const previewApiHost = effectiveRoot
    ? `api-s<ID>.${effectiveRoot}`
    : t('Configure a custom API domain after opening')

  const selectedTierObj =
    enabledTiers.find((item) => item.code === tierCode) ?? enabledTiers[0]
  let previewCostFactor = '-'
  if (costFactor.trim() !== '') {
    previewCostFactor = `${costFactor.trim()} (${t('Per-site')})`
  } else if (selectedTierObj?.cost_factor) {
    previewCostFactor = `${selectedTierObj.cost_factor} (${selectedTierObj.name})`
  }

  const createdSite = createdResult?.site ?? null
  const createdHost = createdSite
    ? createdSite.host || `${createdSite.subdomain}.${createdSite.root_domain}`
    : ''

  return (
    <Dialog
      open={props.open}
      onOpenChange={(nextOpen) => {
        if (openMutation.isPending) return
        if (!nextOpen) resetForm()
        props.onOpenChange(nextOpen)
      }}
      title={createdSite ? t('Site opened') : t('Open a reseller site')}
      description={
        createdSite
          ? t(
              'Save the newly created site owner credentials and review the generated site addresses below.'
            )
          : t(
              'Create a dedicated standalone site owner account and configure initial domain and supply settings.'
            )
      }
      contentClassName='sm:max-w-2xl'
      footer={
        createdSite ? (
          <div className='flex flex-wrap justify-end gap-2'>
            <Button
              type='button'
              variant='ghost'
              onClick={() => {
                resetForm()
                props.onOpenChange(false)
              }}
            >
              {t('Close')}
            </Button>
            {!lead && (
              <Button type='button' variant='outline' onClick={resetForm}>
                {t('Open another site')}
              </Button>
            )}
            <Button
              type='button'
              variant='brand'
              onClick={() => {
                const target = createdSite
                resetForm()
                props.onOpenChange(false)
                ;(props.onViewSite ?? props.onOpened)?.(target)
              }}
            >
              {t('View site details')}
            </Button>
          </div>
        ) : (
          <div className='flex flex-wrap justify-end gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={openMutation.isPending}
              onClick={() => {
                resetForm()
                props.onOpenChange(false)
              }}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='submit'
              variant='brand'
              form={`${fieldId}-form`}
              disabled={!canSubmit}
            >
              {t('Confirm and open site')}
            </Button>
          </div>
        )
      }
    >
      {createdResult && createdSite ? (
        <div className='flex flex-col gap-4'>
          <div className='border-success/40 bg-success/5 flex items-start gap-3 rounded-lg border p-3'>
            <CheckCircle2 className='text-success mt-0.5 size-5 shrink-0' />
            <div className='flex flex-col gap-1 text-sm'>
              <p className='font-medium'>
                {t('Reseller site #{{id}} is now active', {
                  id: createdSite.id,
                })}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Standalone site owner account {{name}} (#{{id}}) has been created. Save the initial password now — it will not be shown again.',
                  {
                    name: createdResult.ownerUsername,
                    id: createdSite.owner_user_id,
                  }
                )}
              </p>
            </div>
          </div>

          <dl className='grid gap-3 rounded-lg border p-3 text-sm sm:grid-cols-2'>
            <div className='flex flex-col gap-1'>
              <dt className='text-muted-foreground text-xs'>
                {t('Owner username')}
              </dt>
              <dd className='flex items-center gap-2 font-mono text-sm break-all'>
                <span>{createdResult.ownerUsername}</span>
                <CopyButton
                  value={createdResult.ownerUsername}
                  size='sm'
                  variant='outline'
                  aria-label={t('Copy owner username')}
                />
              </dd>
            </div>
            <div className='flex flex-col gap-1'>
              <dt className='text-muted-foreground text-xs'>
                {t('Initial password')}
              </dt>
              <dd className='flex items-center gap-2 font-mono text-sm break-all'>
                <span>{createdResult.ownerPassword}</span>
                <CopyButton
                  value={createdResult.ownerPassword}
                  size='sm'
                  variant='outline'
                  aria-label={t('Copy password')}
                />
              </dd>
            </div>
            <div className='flex flex-col gap-1 sm:col-span-2'>
              <dt className='text-muted-foreground text-xs'>
                {t('Website address')}
              </dt>
              <dd className='flex items-center gap-2 font-mono text-sm break-all'>
                <span>{createdHost}</span>
                <CopyButton
                  value={`https://${createdHost}`}
                  size='sm'
                  variant='outline'
                  aria-label={t('Copy link')}
                />
              </dd>
            </div>
            <div className='flex flex-col gap-1 sm:col-span-2'>
              <dt className='text-muted-foreground text-xs'>
                {t('API address')}
              </dt>
              <dd className='flex items-center gap-2 text-sm break-all'>
                {createdSite.api_host ? (
                  <>
                    <span className='font-mono'>{createdSite.api_host}</span>
                    <CopyButton
                      value={`https://${createdSite.api_host}`}
                      size='sm'
                      variant='outline'
                      aria-label={t('Copy link')}
                    />
                  </>
                ) : (
                  <span className='text-muted-foreground'>
                    {t('Configure a custom API domain after opening')}
                  </span>
                )}
              </dd>
            </div>
            <div className='flex flex-col gap-0.5'>
              <dt className='text-muted-foreground text-xs'>
                {t('Effective cost factor')}
              </dt>
              <dd className='tabular-nums'>
                {createdSite.effective_cost_factor ||
                  createdSite.cost_factor ||
                  t('Follow site level')}
              </dd>
            </div>
            <div className='flex flex-col gap-0.5'>
              <dt className='text-muted-foreground text-xs'>
                {t('Site level')}
              </dt>
              <dd>
                {createdSite.tier_code
                  ? `${createdSite.tier_code} (${createdSite.tier_mode === 'manual' ? t('Pinned') : t('Automatic')})`
                  : t('Automatic')}
              </dd>
            </div>
          </dl>

          {automationOn && (
            <Alert>
              <AlertDescription className='text-xs'>
                {t(
                  'Platform DNS records and TLS certificates are provisioning in the background. Open site details to monitor readiness or retry failed steps.'
                )}
              </AlertDescription>
            </Alert>
          )}
        </div>
      ) : (
        <form
          id={`${fieldId}-form`}
          className='flex flex-col gap-5'
          onSubmit={(event) => {
            event.preventDefault()
            if (!canSubmit) return
            openMutation.mutate()
          }}
        >
          {lead && (
            <Alert>
              <AlertDescription className='text-xs'>
                {t('Approving partner application #{{id}} for {{name}}.', {
                  id: lead.id,
                  name: lead.contact_name,
                })}
              </AlertDescription>
            </Alert>
          )}
          {/* Step 1: New standalone site owner account */}
          <section className='flex flex-col gap-3'>
            <div className='flex flex-col gap-1'>
              <h3 className='text-sm font-medium'>
                {t('1. New standalone site owner account')}
              </h3>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'A dedicated site owner account is created exclusively for managing this subsite (no main-site customer wallet or self-use API access).'
                )}
              </p>
            </div>
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-owner-username`}>
                  {t('Owner username')} *
                </Label>
                <Input
                  id={`${fieldId}-owner-username`}
                  value={ownerUsername}
                  maxLength={20}
                  autoComplete='off'
                  placeholder='partner_admin'
                  aria-invalid={usernameError !== ''}
                  onChange={(event) => {
                    setOwnerUsername(event.target.value)
                    setConfirmed(false)
                  }}
                />
                {usernameError ? (
                  <p className='text-destructive text-xs'>{usernameError}</p>
                ) : (
                  <p className='text-muted-foreground text-xs'>
                    {t('3 to 20 characters (letters, digits, _, ., -, no @)')}
                  </p>
                )}
              </div>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-owner-password`}>
                  {t('Owner password')} *
                </Label>
                <div className='flex items-center gap-1.5'>
                  <PasswordInput
                    id={`${fieldId}-owner-password`}
                    className='min-w-0 flex-1'
                    value={ownerPassword}
                    maxLength={128}
                    autoComplete='new-password'
                    placeholder={t('8 to 128 characters')}
                    aria-invalid={passwordError !== ''}
                    onChange={(event) => setOwnerPassword(event.target.value)}
                  />
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    onClick={() => setOwnerPassword(generateOwnerPassword(16))}
                  >
                    <RefreshCw className='size-3.5' aria-hidden />
                    {t('Generate')}
                  </Button>
                  {ownerPassword && (
                    <CopyButton
                      value={ownerPassword}
                      size='icon'
                      variant='outline'
                      aria-label={t('Copy password')}
                    />
                  )}
                </div>
                {passwordError ? (
                  <p className='text-destructive text-xs'>{passwordError}</p>
                ) : (
                  <p className='text-muted-foreground text-xs'>
                    {t('8 to 128 characters. Copy and deliver it securely to the owner.')}
                  </p>
                )}
              </div>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-owner-email`}>
                  {t('Owner email (optional)')}
                </Label>
                <Input
                  id={`${fieldId}-owner-email`}
                  type='email'
                  value={ownerEmail}
                  autoComplete='off'
                  placeholder='owner@example.com'
                  aria-invalid={emailError !== ''}
                  onChange={(event) => setOwnerEmail(event.target.value)}
                />
                {emailError && (
                  <p className='text-destructive text-xs'>{emailError}</p>
                )}
              </div>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-owner-display-name`}>
                  {t('Display name (optional)')}
                </Label>
                <Input
                  id={`${fieldId}-owner-display-name`}
                  value={ownerDisplayName}
                  maxLength={20}
                  autoComplete='off'
                  placeholder={t('Defaults to owner username')}
                  aria-invalid={displayNameError !== ''}
                  onChange={(event) => setOwnerDisplayName(event.target.value)}
                />
                {displayNameError && (
                  <p className='text-destructive text-xs'>{displayNameError}</p>
                )}
              </div>
            </div>
          </section>

          {/* Step 2: Domain & initial settings */}
          <section className='flex flex-col gap-3 border-t pt-4'>
            <h3 className='text-sm font-medium'>
              {t('2. Domain and initial configuration')}
            </h3>
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-root-domain`}>
                  {t('Platform root domain')}
                </Label>
                {automationOn ? (
                  <Input
                    id={`${fieldId}-root-domain`}
                    value={effectiveRoot}
                    disabled
                  />
                ) : (
                  <NativeSelect
                    id={`${fieldId}-root-domain`}
                    value={effectiveRoot}
                    onChange={(event) => setRootDomain(event.target.value)}
                  >
                    {roots.map((domain) => (
                      <NativeSelectOption key={domain} value={domain}>
                        {domain}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                )}
                {automationOn && (
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Managed automatically by AliDNS and Cloudflare for SaaS.'
                    )}
                  </p>
                )}
              </div>

              <div className='flex flex-col gap-1.5'>
                <Label>{t('Subdomain allocation')}</Label>
                <div className='flex gap-2'>
                  <Button
                    type='button'
                    size='sm'
                    variant={subdomainMode === 'auto' ? 'default' : 'outline'}
                    onClick={() => {
                      setSubdomainMode('auto')
                      setSubdomain('')
                    }}
                  >
                    {t('Auto allocate')}
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    variant={subdomainMode === 'custom' ? 'default' : 'outline'}
                    onClick={() => setSubdomainMode('custom')}
                  >
                    {t('Custom subdomain')}
                  </Button>
                </div>
              </div>
            </div>

            {subdomainMode === 'custom' && (
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-subdomain`}>
                  {t('Subdomain label')}
                </Label>
                <Input
                  id={`${fieldId}-subdomain`}
                  value={subdomain}
                  placeholder='partner-a'
                  autoComplete='off'
                  onChange={(event) => setSubdomain(event.target.value)}
                />
                {subdomainError && (
                  <p className='text-destructive text-xs'>{subdomainError}</p>
                )}
              </div>
            )}

            <div className='bg-muted/40 grid gap-2 rounded-md border p-3 text-xs sm:grid-cols-2'>
              <div>
                <span className='text-muted-foreground block'>
                  {t('Website address preview')}
                </span>
                <span className='font-mono break-all'>
                  {previewWebsiteHost}
                </span>
              </div>
              <div>
                <span className='text-muted-foreground block'>
                  {t('API address preview')}
                </span>
                <span className='font-mono break-all'>{previewApiHost}</span>
              </div>
            </div>

            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-cost-factor`}>
                  {t('Initial cost factor (optional)')}
                </Label>
                <Input
                  id={`${fieldId}-cost-factor`}
                  type='number'
                  step='any'
                  inputMode='decimal'
                  value={costFactor}
                  placeholder={t('Follow site level')}
                  onChange={(event) => setCostFactor(event.target.value)}
                />
                {costFactorError && (
                  <p className='text-destructive text-xs'>{costFactorError}</p>
                )}
              </div>

              <div className='flex flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-tier`}>
                  {t('Initial site level (optional)')}
                </Label>
                <NativeSelect
                  id={`${fieldId}-tier`}
                  value={tierCode}
                  onChange={(event) => setTierCode(event.target.value)}
                >
                  <NativeSelectOption value=''>
                    {enabledTiers[0]
                      ? t('Auto-assign lowest level ({{name}})', {
                          name: enabledTiers[0].name,
                        })
                      : t('Use automatic level evaluation')}
                  </NativeSelectOption>
                  {enabledTiers.map((tier) => (
                    <NativeSelectOption key={tier.code} value={tier.code}>
                      {tier.name} ({tier.code}
                      {tier.cost_factor ? ` · ${tier.cost_factor}` : ''})
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                {tierCode !== '' && (
                  <NativeSelect
                    aria-label={t('Level mode')}
                    value={tierMode}
                    onChange={(event) =>
                      setTierMode(event.target.value as 'auto' | 'manual')
                    }
                  >
                    <NativeSelectOption value='auto'>
                      {t('Automatic (can upgrade/downgrade)')}
                    </NativeSelectOption>
                    <NativeSelectOption value='manual'>
                      {t('Manual pin (do not auto-change)')}
                    </NativeSelectOption>
                  </NativeSelect>
                )}
              </div>
            </div>

            <div className='flex flex-col gap-1.5'>
              <Label htmlFor={`${fieldId}-contact`}>
                {t('Owner contact (optional)')}
              </Label>
              <Input
                id={`${fieldId}-contact`}
                value={contactInfo}
                placeholder={t('Telegram, WeChat, or email for operations')}
                onChange={(event) => setContactInfo(event.target.value)}
              />
            </div>

            <details
              open={Boolean(lead)}
              className='group border-border rounded-lg border'
            >
              <summary className='flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-sm font-medium [&::-webkit-details-marker]:hidden'>
                <ChevronRight
                  aria-hidden
                  className='text-muted-foreground size-4 transition-transform group-open:rotate-90'
                />
                {t('Partner referrer (optional)')}
              </summary>
              <div className='border-border grid gap-3 border-t p-3 sm:grid-cols-3'>
                <div className='flex flex-col gap-1.5'>
                  <Label htmlFor={`${fieldId}-referrer-code`}>
                    {t('Referrer partner code')}
                  </Label>
                  <Input
                    id={`${fieldId}-referrer-code`}
                    value={referrerCode}
                    placeholder='PTN...'
                    autoComplete='off'
                    onChange={(event) => setReferrerCode(event.target.value)}
                  />
                </div>
                <div className='flex flex-col gap-1.5'>
                  <Label htmlFor={`${fieldId}-referrer-site-id`}>
                    {t('Referrer site ID')}
                  </Label>
                  <Input
                    id={`${fieldId}-referrer-site-id`}
                    type='number'
                    min={1}
                    step={1}
                    value={referrerSiteId}
                    placeholder='#'
                    aria-invalid={referrerSiteIdError !== ''}
                    onChange={(event) => setReferrerSiteId(event.target.value)}
                  />
                  {referrerSiteIdError && (
                    <p className='text-destructive text-xs'>
                      {referrerSiteIdError}
                    </p>
                  )}
                </div>
                <div className='flex flex-col gap-1.5'>
                  <Label htmlFor={`${fieldId}-referrer-rate`}>
                    {t('Custom referral rate (%)')}
                  </Label>
                  <Input
                    id={`${fieldId}-referrer-rate`}
                    type='number'
                    min={0}
                    max={50}
                    step='any'
                    inputMode='decimal'
                    value={referrerRate}
                    placeholder={t('Default {{rate}}%', {
                      rate: defaultPartnerRate,
                    })}
                    aria-invalid={referrerRateError !== ''}
                    onChange={(event) => setReferrerRate(event.target.value)}
                  />
                  {referrerRateError && (
                    <p className='text-destructive text-xs'>
                      {referrerRateError}
                    </p>
                  )}
                </div>
              </div>
            </details>
          </section>

          {/* Step 3: Verification & irreversible confirmation */}
          <section className='border-warning/40 bg-warning/5 flex flex-col gap-3 rounded-lg border p-3'>
            <div className='text-xs'>
              <p className='font-medium'>{t('3. Confirm site creation')}</p>
              <ul className='text-muted-foreground mt-1 list-disc space-y-1 pl-4'>
                <li>
                  {t(
                    'A standalone owner account will be bound permanently to this site and cannot be replaced later.'
                  )}
                </li>
                <li>
                  {t('Effective cost factor')}: {previewCostFactor}
                </li>
              </ul>
            </div>
            {trimmedUsername !== '' && usernameError === '' ? (
              <div className='flex items-center gap-2'>
                <Checkbox
                  id={`${fieldId}-confirm`}
                  checked={confirmed}
                  onCheckedChange={(value) => setConfirmed(value === true)}
                />
                <Label htmlFor={`${fieldId}-confirm`} className='text-sm'>
                  {t('Confirm creating standalone site owner {{name}}', {
                    name: trimmedUsername,
                  })}
                </Label>
              </div>
            ) : (
              <p className='text-muted-foreground text-xs'>
                {t('Enter a valid owner username above to enable confirmation.')}
              </p>
            )}
          </section>
        </form>
      )}
    </Dialog>
  )
}
