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
import { Check, CheckCircle2, Search } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  createSite,
  getSiteOwnerCandidates,
  getSiteTiers,
  siteAdminKeys,
  type SiteOwnerCandidate,
} from '../api'
import type { AdminSite, ResellerSettings } from '../types'

const SUBDOMAIN_PATTERN = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/

function newOpenIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `open-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export function OpenSiteDialog(props: {
  open: boolean
  settings: ResellerSettings | undefined
  onOpenChange: (open: boolean) => void
  onViewSite: (site: AdminSite) => void
}) {
  const { t } = useTranslation()
  const fieldId = useId()
  const queryClient = useQueryClient()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN

  const [keyword, setKeyword] = useState('')
  const [debouncedKeyword, setDebouncedKeyword] = useState('')
  const [selectedOwner, setSelectedOwner] = useState<SiteOwnerCandidate | null>(
    null
  )
  const [subdomainMode, setSubdomainMode] = useState<'auto' | 'custom'>('auto')
  const [subdomain, setSubdomain] = useState('')
  const [rootDomain, setRootDomain] = useState('')
  const [tierCode, setTierCode] = useState('')
  const [tierMode, setTierMode] = useState<'auto' | 'manual'>('auto')
  const [costFactor, setCostFactor] = useState('')
  const [contactInfo, setContactInfo] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const [createdSite, setCreatedSite] = useState<AdminSite | null>(null)
  const intentRef = useRef<{ payload: string; key: string } | null>(null)

  const roots = props.settings?.root_domains ?? []
  const automationOn = props.settings?.domain_automation_enabled === true
  const managedRoot = props.settings?.aliyun_domain?.trim() ?? ''
  const effectiveRoot = automationOn
    ? managedRoot || roots[0] || ''
    : rootDomain || roots[0] || ''

  useEffect(() => {
    const timer = window.setTimeout(
      () => setDebouncedKeyword(keyword.trim()),
      250
    )
    return () => window.clearTimeout(timer)
  }, [keyword])

  const resetForm = () => {
    setKeyword('')
    setDebouncedKeyword('')
    setSelectedOwner(null)
    setSubdomainMode('auto')
    setSubdomain('')
    setRootDomain(roots[0] ?? '')
    setTierCode('')
    setTierMode('auto')
    setCostFactor('')
    setContactInfo('')
    setConfirmed(false)
    setCreatedSite(null)
    intentRef.current = null
  }

  const candidates = useQuery({
    queryKey: siteAdminKeys.list('owner-candidates', {
      keyword: debouncedKeyword,
    }),
    queryFn: () =>
      getSiteOwnerCandidates({
        keyword: debouncedKeyword,
        p: 1,
        page_size: 20,
      }),
    enabled: props.open && isRoot && createdSite === null,
  })

  const tiers = useQuery({
    queryKey: siteAdminKeys.list('tiers', {}),
    queryFn: getSiteTiers,
    enabled: props.open && isRoot && createdSite === null,
  })
  const enabledTiers = (tiers.data ?? []).filter(
    (item) => item.enabled && item.cost_factor
  )

  const normalizedSub = subdomain.trim().toLowerCase()
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

  const openMutation = useMutation({
    mutationFn: () => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      if (!selectedOwner) {
        throw new Error(t('Select owner'))
      }
      const body: Parameters<typeof createSite>[0] = {
        user_id: selectedOwner.id,
        subdomain: subdomainMode === 'custom' ? normalizedSub : '',
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
      setCreatedSite(site)
      toast.success(t('Site opened'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })

  const canSubmit =
    Boolean(selectedOwner) &&
    confirmed &&
    !openMutation.isPending &&
    (subdomainMode === 'auto' || normalizedSub !== '') &&
    subdomainError === '' &&
    costFactorError === '' &&
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

  const candidateRows = candidates.data?.items ?? []

  return (
    <Dialog
      open={props.open}
      onOpenChange={(nextOpen) => {
        if (openMutation.isPending) return
        if (!nextOpen) resetForm()
        props.onOpenChange(nextOpen)
      }}
      title={
        createdSite ? t('Site opened') : t('Open a reseller site')
      }
      description={
        createdSite
          ? t(
              'Review the generated site addresses and continue to site details to monitor domain readiness or configure settings.'
            )
          : t(
              'Designate an existing main-site user as the permanent site owner and configure initial domain and supply settings.'
            )
      }
      contentClassName='sm:max-w-2xl'
      footer={
        createdSite ? (
          <div className='flex flex-wrap justify-end gap-2'>
            <Button type='button' variant='ghost' onClick={() => {
              resetForm()
              props.onOpenChange(false)
            }}>
              {t('Close')}
            </Button>
            <Button type='button' variant='outline' onClick={resetForm}>
              {t('Open another site')}
            </Button>
            <Button
              type='button'
              variant='brand'
              onClick={() => {
                const target = createdSite
                resetForm()
                props.onOpenChange(false)
                props.onViewSite(target)
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
      {createdSite ? (
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
                  'Fixed owner: {{name}} (#{{id}}). Main-site referral commissions for this account have stopped.',
                  {
                    name:
                      createdSite.owner_username ||
                      selectedOwner?.username ||
                      `#${createdSite.owner_user_id}`,
                    id: createdSite.owner_user_id,
                  }
                )}
              </p>
            </div>
          </div>

          <dl className='grid gap-3 rounded-lg border p-3 text-sm sm:grid-cols-2'>
            <div className='flex flex-col gap-1 sm:col-span-2'>
              <dt className='text-muted-foreground text-xs'>
                {t('Website address')}
              </dt>
              <dd className='flex items-center gap-2 font-mono text-sm break-all'>
                <span>{createdSite.host || `${createdSite.subdomain}.${createdSite.root_domain}`}</span>
                <CopyButton
                  value={`https://${createdSite.host || `${createdSite.subdomain}.${createdSite.root_domain}`}`}
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
              <dt className='text-muted-foreground text-xs'>{t('Site level')}</dt>
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
          {/* Step 1: Select fixed owner */}
          <section className='flex flex-col gap-2.5'>
            <div className='flex items-center justify-between gap-2'>
              <Label
                htmlFor={`${fieldId}-owner-search`}
                className='text-sm font-medium'
              >
                {t('1. Select fixed site owner')}
              </Label>
              {selectedOwner && (
                <Badge variant='secondary'>
                  {selectedOwner.username} (#{selectedOwner.id})
                </Badge>
              )}
            </div>
            <div className='relative'>
              <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2' />
              <Input
                id={`${fieldId}-owner-search`}
                className='pl-8'
                value={keyword}
                placeholder={t('Search eligible main-site users by username, email, or ID')}
                onChange={(event) => setKeyword(event.target.value)}
              />
            </div>
            {candidates.isError && (
              <ErrorState onRetry={() => void candidates.refetch()} />
            )}
            {candidates.isPending && <LoadingState />}
            {candidates.isSuccess && (
              <div
                role='listbox'
                aria-label={t('Eligible main-site users')}
                className='max-h-44 overflow-y-auto rounded-md border divide-y'
              >
                {candidateRows.length === 0 ? (
                  <p className='text-muted-foreground p-3 text-center text-xs'>
                    {t('No eligible main-site users found')}
                  </p>
                ) : (
                  candidateRows.map((item) => {
                    const isSelected = selectedOwner?.id === item.id
                    return (
                      <button
                        key={item.id}
                        type='button'
                        role='option'
                        aria-selected={isSelected}
                        className={cn(
                          'hover:bg-muted/60 flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-sm transition-colors',
                          isSelected && 'bg-muted font-medium'
                        )}
                        onClick={() => {
                          setSelectedOwner(item)
                          setConfirmed(false)
                        }}
                      >
                        <div className='min-w-0 flex-1'>
                          <div className='truncate'>
                            {item.username}{' '}
                            <span className='text-muted-foreground text-xs tabular-nums'>
                              #{item.id}
                            </span>
                            {item.display_name &&
                              item.display_name !== item.username && (
                                <span className='text-muted-foreground ml-1.5 text-xs'>
                                  ({item.display_name})
                                </span>
                              )}
                          </div>
                          {item.email && (
                            <p className='text-muted-foreground truncate text-xs'>
                              {item.email}
                            </p>
                          )}
                        </div>
                        {isSelected && (
                          <Check className='text-primary size-4 shrink-0' />
                        )}
                      </button>
                    )
                  })
                )}
              </div>
            )}
            <p className='text-muted-foreground text-xs'>
              {t(
                'Only active main-site accounts without an existing site (and not site staff or team employees) are listed.'
              )}
            </p>
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
                    {t('Managed automatically by AliDNS and Cloudflare for SaaS.')}
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
          </section>

          {/* Step 3: Verification & irreversible confirmation */}
          <section className='border-warning/40 bg-warning/5 flex flex-col gap-3 rounded-lg border p-3'>
            <div className='text-xs'>
              <p className='font-medium'>{t('3. Confirm permanent owner')}</p>
              <ul className='text-muted-foreground mt-1 list-disc space-y-1 pl-4'>
                <li>
                  {t(
                    'The site owner is fixed upon creation and cannot be replaced or transferred later.'
                  )}
                </li>
                <li>
                  {t(
                    'Main-site referral commission for this account will stop immediately once the site opens.'
                  )}
                </li>
                <li>
                  {t('Effective cost factor')}: {previewCostFactor}
                </li>
              </ul>
            </div>
            {selectedOwner ? (
              <div className='flex items-center gap-2'>
                <Checkbox
                  id={`${fieldId}-confirm`}
                  checked={confirmed}
                  onCheckedChange={(value) => setConfirmed(value === true)}
                />
                <Label htmlFor={`${fieldId}-confirm`} className='text-sm'>
                  {t('Confirm site owner {{name}} (#{{id}})', {
                    name: selectedOwner.username,
                    id: selectedOwner.id,
                  })}
                </Label>
              </div>
            ) : (
              <p className='text-muted-foreground text-xs'>
                {t('Select an eligible main-site user above to enable confirmation.')}
              </p>
            )}
          </section>
        </form>
      )}
    </Dialog>
  )
}
