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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { adtySiteQueryKey } from '@/features/adty-site/api'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { formatDomainLastError } from '@/lib/domain-error'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { toSquareIconFile } from '@/lib/square-icon'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import {
  addDomain,
  addDomainPair,
  checkDomainNow,
  requestSiteClose,
  removeDomain,
  saveOwnerSite,
  siteOwnerKeys,
  uploadLogo,
} from '../api'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import { isValidOptionalHttpUrl } from '../lib/site-owner'
import type { OwnerSite, SiteBrand, SiteDomain, SiteDomainRole } from '../types'
import { SiteLegalCard } from './site-legal-card'

const BRAND_LIMITS: Record<
  Exclude<keyof SiteBrand, 'logo_url' | 'favicon_url'>,
  number
> = {
  name: 40,
  notice: 500,
  support_contact: 200,
  title: 80,
  tawk_property_id: 64,
  tawk_widget_id: 64,
  wallet_notice: 2000,
  voucher_purchase_url: 500,
  docs_url: 500,
}

type DomainVerifyRecord = {
  type: string
  name: string
  value: string
}

function parseVerifyRecords(raw: string | undefined): DomainVerifyRecord[] {
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(
      (item): item is DomainVerifyRecord =>
        typeof item === 'object' &&
        item !== null &&
        typeof (item as DomainVerifyRecord).type === 'string' &&
        typeof (item as DomainVerifyRecord).name === 'string' &&
        typeof (item as DomainVerifyRecord).value === 'string'
    )
  } catch {
    return []
  }
}

function normalizeDisplayUrl(raw: string | undefined): string {
  const trimmed = (raw ?? '').trim()
  if (!trimmed) return ''
  if (/^https?:\/\//i.test(trimmed)) return trimmed
  return `https://${trimmed}`
}

function domainVariant(status: SiteDomain['status']) {
  if (status === 'active') return 'success' as const
  if (status === 'failed') return 'danger' as const
  return 'warning' as const
}

function domainLabel(t: TFunction, status: SiteDomain['status']): string {
  if (status === 'active') return t('Active')
  if (status === 'failed') return t('Failed')
  if (status === 'removed') return t('Removed')
  return t('Waiting for DNS')
}

function edgeCertVariant(status: string | undefined) {
  switch (status) {
    case 'ready':
      return 'success' as const
    case 'failed':
      return 'danger' as const
    case 'issuing':
      return 'info' as const
    default:
      return 'warning' as const
  }
}

function edgeCertLabel(t: TFunction, status: string | undefined): string {
  switch (status) {
    case 'ready':
      return t('Certificate ready')
    case 'issuing':
      return t('Issuing certificate')
    case 'failed':
      return t('Issuance failed')
    default:
      return t('Waiting for DNS')
  }
}

function BrandCard(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const closing = props.site.status === 'closing'
  const [brand, setBrand] = useState<SiteBrand>(props.site.brand)
  const [acknowledged, setAcknowledged] = useState<{
    previous: SiteBrand
    current: SiteBrand
  } | null>(null)
  const observed =
    acknowledged?.previous === props.site.brand
      ? acknowledged.current
      : props.site.brand
  const edits = useRef<Partial<Record<keyof SiteBrand, number>>>({})
  const editBrand = (patch: Partial<SiteBrand>) => {
    for (const key of Object.keys(patch) as (keyof SiteBrand)[]) {
      edits.current[key] = (edits.current[key] ?? 0) + 1
    }
    setBrand((current) => ({ ...current, ...patch }))
  }
  const [baseline, setBaseline] = useState(props.site.brand)
  const changed = (Object.keys(brand) as (keyof SiteBrand)[]).filter(
    (key) => (brand[key] ?? '') !== (baseline[key] ?? '')
  )
  const conflicts = changed.filter(
    (key) => baseline[key] !== observed[key] && brand[key] !== observed[key]
  )
  useDraftNavigation(changed.length > 0)
  useEffect(() => {
    if (!changed.length) {
      setBrand(observed)
      setBaseline(observed)
    }
  }, [observed, changed.length])
  const upload = useMutation({
    mutationFn: async (input: {
      key: 'logo_url' | 'favicon_url'
      file: File
    }) => {
      const url = await uploadLogo(input.file)
      // A logo doubles as the fallback tab icon: square it off when the site
      // owner has not uploaded a dedicated favicon yet. Failures here are
      // silent so the logo upload itself still succeeds.
      let faviconUrl: string | null = null
      if (input.key === 'logo_url' && !brand.favicon_url) {
        const squareFile = await toSquareIconFile(input.file)
        if (squareFile) {
          try {
            faviconUrl = await uploadLogo(squareFile)
          } catch {
            faviconUrl = null
          }
        }
      }
      return { key: input.key, url, faviconUrl }
    },
    onSuccess: ({ key, url, faviconUrl }) => {
      edits.current[key] = (edits.current[key] ?? 0) + 1
      if (faviconUrl) {
        edits.current.favicon_url = (edits.current.favicon_url ?? 0) + 1
      }
      setBrand((prev) => ({
        ...prev,
        [key]: url,
        ...(key === 'logo_url' && faviconUrl && !prev.favicon_url
          ? { favicon_url: faviconUrl }
          : {}),
      }))
    },
    onError: (err) => handleServerError(err),
  })
  const save = useMutation({
    mutationFn: (_submitted: typeof edits.current) =>
      saveOwnerSite({
        brand: Object.fromEntries(
          changed.map((key) => [key, brand[key] ?? ''])
        ),
        expected_brand: Object.fromEntries(
          changed.map((key) => [key, baseline[key] ?? ''])
        ),
      }),
    onSuccess: (updated, submitted) => {
      queryClient.setQueryData(siteOwnerKeys.site, updated)
      setAcknowledged({ previous: props.site.brand, current: updated.brand })
      setBaseline(updated.brand)
      setBrand((current) => ({
        ...updated.brand,
        ...Object.fromEntries(
          (Object.keys(edits.current) as (keyof SiteBrand)[])
            .filter((key) => edits.current[key] !== submitted[key])
            .map((key) => [key, current[key]])
        ),
      }))
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void queryClient.invalidateQueries({ queryKey: adtySiteQueryKey })
      void queryClient.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
    },
    onError: (err) => {
      handleServerError(err)
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
    },
  })
  const field = (
    key: keyof typeof BRAND_LIMITS,
    label: string,
    multiline = false,
    extraHint?: string
  ) => {
    const id = `brand-${key}`
    const isUrlField = key === 'voucher_purchase_url' || key === 'docs_url'
    let placeholder: string | undefined
    if (isUrlField) placeholder = 'https://...'
    const isUrlInvalid =
      isUrlField && !isValidOptionalHttpUrl(brand[key] ?? '')
    const inputProps = {
      id,
      maxLength: BRAND_LIMITS[key],
      value: brand[key] ?? '',
      placeholder,
      disabled: closing || save.isPending,
      'aria-invalid': isUrlInvalid || undefined,
      onChange: (event: { target: { value: string } }) =>
        editBrand({ [key]: event.target.value }),
    }
    return (
      <div className='space-y-2'>
        <Label htmlFor={id}>{label}</Label>
        {multiline ? <Textarea {...inputProps} /> : <Input {...inputProps} />}
        {isUrlInvalid ? (
          <p role='alert' className='text-destructive text-xs'>
            {t('Enter a valid http:// or https:// URL')}
          </p>
        ) : (
          <p className='text-muted-foreground text-xs'>
            {extraHint ? `${extraHint} · ` : ''}
            {t('{{count}} / {{max}} characters', {
              count: [...(brand[key] ?? '')].length,
              max: BRAND_LIMITS[key],
            })}
          </p>
        )}
      </div>
    )
  }
  const imageField = (
    key: 'logo_url' | 'favicon_url',
    label: string,
    removeLabel: string
  ) => (
    <div className='space-y-2'>
      <Label htmlFor={`brand-${key}`}>{label}</Label>
      <Input
        id={`brand-${key}`}
        type='file'
        accept='image/png,image/jpeg,image/webp'
        disabled={closing || upload.isPending}
        onChange={(event) => {
          const file = event.target.files?.[0]
          if (file) upload.mutate({ key, file })
        }}
      />
      <p className='text-muted-foreground text-xs'>
        {t('PNG, JPEG or WebP, up to 200 KB. Save the brand to use it.')}
      </p>
      {brand[key] && (
        <div className='flex items-center gap-3'>
          <img
            src={brand[key]}
            alt={label}
            className='h-12 w-12 rounded border object-contain'
          />
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={closing || upload.isPending || save.isPending}
            onClick={() => editBrand({ [key]: '' })}
          >
            {removeLabel}
          </Button>
        </div>
      )}
    </div>
  )
  const brandFieldLabels: Record<keyof SiteBrand, string> = {
    name: t('Site name'),
    title: t('Website title'),
    notice: t('Announcement'),
    support_contact: t('Support contact'),
    tawk_property_id: t('Tawk.to property ID'),
    tawk_widget_id: t('Tawk.to widget ID'),
    wallet_notice: t('Wallet notice'),
    voucher_purchase_url: t('Voucher purchase link'),
    docs_url: t('Documentation link'),
    logo_url: t('Logo'),
    favicon_url: t('Favicon'),
  }
  const voucherUrlInvalid = !isValidOptionalHttpUrl(
    brand.voucher_purchase_url ?? ''
  )
  const docsUrlInvalid = !isValidOptionalHttpUrl(brand.docs_url ?? '')
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Brand')}</CardTitle>
      </CardHeader>
      <CardContent className='grid gap-4'>
        {conflicts.length > 0 && (
          <div role='alert'>
            {conflicts.map((key) => (
              <p key={key}>
                {brandFieldLabels[key] ?? key} · {t('Current value')}:{' '}
                {observed[key]} · {t('Previous value')}: {baseline[key]}
              </p>
            ))}
            <Button
              type='button'
              variant='outline'
              disabled={closing || save.isPending}
              onClick={() => {
                setBrand({
                  ...observed,
                  ...Object.fromEntries(
                    changed.map((key) => [key, brand[key]])
                  ),
                })
                setBaseline(observed)
              }}
            >
              {t('Keep my changes')}
            </Button>
          </div>
        )}
        <div className='grid gap-x-6 gap-y-4 lg:grid-cols-2'>
          {field('name', t('Site name'))}
          {field('title', t('Website title'))}
          {field('notice', t('Announcement'), true)}
          {field(
            'wallet_notice',
            t('Wallet notice'),
            true,
            t('Displayed on the customer wallet page')
          )}
          {field(
            'voucher_purchase_url',
            t('Voucher purchase link'),
            false,
            t('External http(s) link where customers can buy redemption codes')
          )}
          {field(
            'docs_url',
            t('Documentation link'),
            false,
            t(
              'Optional external http(s) documentation link; leave empty to use the built-in same-domain /docs/ documentation'
            )
          )}
          {field('support_contact', t('Support contact'))}
          {field('tawk_property_id', t('Tawk.to property ID'))}
          {field('tawk_widget_id', t('Tawk.to widget ID'))}
        </div>
        <p className='text-muted-foreground -mt-1 text-xs'>
          {t('Live chat appears when both Tawk.to IDs are set.')}
        </p>
        <div className='grid gap-x-6 gap-y-4 border-t pt-4 lg:grid-cols-2'>
          {imageField('logo_url', t('Logo'), t('Remove logo'))}
          {imageField('favicon_url', t('Favicon'), t('Remove favicon'))}
        </div>
        <p className='text-muted-foreground text-xs'>
          {t('Brand text is shown as plain text.')}
        </p>
        <div>
          <Button
            type='button'
            variant='brand'
            disabled={
              closing ||
              save.isPending ||
              !changed.length ||
              conflicts.length > 0 ||
              voucherUrlInvalid ||
              docsUrlInvalid
            }
            onClick={() => save.mutate({ ...edits.current })}
          >
            {t('Save brand')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function DomainsCard(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [siteHostname, setSiteHostname] = useState('')
  const [apiHostname, setApiHostname] = useState('')
  useDraftNavigation(siteHostname !== '' || apiHostname !== '')
  const [deleting, setDeleting] = useState<SiteDomain | null>(null)
  const [checkingTarget, setCheckingTarget] = useState<{
    id: number
    mode: 'dns' | 'edge_cert'
  } | null>(null)
  const [expandedEdgeIds, setExpandedEdgeIds] = useState<
    Record<number, boolean>
  >({})
  const domains = props.site.domains ?? []
  const platformSiteDomain = domains.find(
    (d) =>
      d.managed_by === 'platform' &&
      d.role === 'site' &&
      d.status !== 'removed'
  )
  const platformApiDomain = domains.find(
    (d) =>
      d.managed_by === 'platform' &&
      d.role === 'base_url' &&
      d.status !== 'removed'
  )
  const customDomains = domains.filter(
    (d) => d.managed_by !== 'platform' && d.status !== 'removed'
  )
  const customSiteDomain = customDomains.find((d) => d.role === 'site')
  const customApiDomain = customDomains.find((d) => d.role === 'base_url')
  const bothEmpty = !customSiteDomain && !customApiDomain
  const bothConfigured = Boolean(customSiteDomain && customApiDomain)
  let missingRole: SiteDomainRole | null = null
  if (customSiteDomain && !customApiDomain) {
    missingRole = 'base_url'
  } else if (!customSiteDomain && customApiDomain) {
    missingRole = 'site'
  }

  const normalizedSite = siteHostname.trim().toLowerCase()
  const normalizedApi = apiHostname.trim().toLowerCase()
  const pairDuplicate =
    bothEmpty &&
    normalizedSite !== '' &&
    normalizedApi !== '' &&
    normalizedSite === normalizedApi

  const addPair = useMutation({
    mutationFn: () => addDomainPair(siteHostname.trim(), apiHostname.trim()),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      setSiteHostname('')
      setApiHostname('')
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  const addSingleMissing = useMutation({
    mutationFn: (input: { hostname: string; role: SiteDomainRole }) =>
      addDomain(input.hostname, input.role),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      setSiteHostname('')
      setApiHostname('')
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  const remove = useVerifiedOwnerMutation<number>({
    action: 'domain.delete',
    target: (id) => id,
    run: (id, proof) => removeDomain(id, proof),
    successMessage: t('Deleted successfully'),
    onSuccess: () => setDeleting(null),
  })
  const checkNow = useMutation({
    mutationFn: (input: { id: number; mode: 'dns' | 'edge_cert' }) =>
      checkDomainNow(input.id),
    onMutate: (input) => setCheckingTarget(input),
    onSuccess: (updated, input) => {
      queryClient.setQueryData<OwnerSite | undefined>(
        siteOwnerKeys.site,
        (prev) =>
          prev
            ? {
                ...prev,
                domains: (prev.domains ?? []).map((item) =>
                  item.id === updated.id ? { ...item, ...updated } : item
                ),
              }
            : prev
      )
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      if (input.mode === 'edge_cert') {
        if (updated.edge_cert_status === 'ready') {
          toast.success(
            t('Edge certificate is ready. You can now configure Step 2.')
          )
        } else if (updated.edge_cert_error) {
          toast.error(formatDomainLastError(updated.edge_cert_error, t))
        } else {
          toast.error(t('ACME verification CNAME is not detected yet'))
        }
        return
      }
      if (updated.status === 'active') {
        toast.success(t('Domain DNS verified and active'))
      } else if (updated.last_error) {
        toast.error(formatDomainLastError(updated.last_error, t))
      } else if (updated.automation_state === 'validating') {
        toast.success(
          t('DNS ownership verified; waiting for SSL certificate issuance')
        )
      } else {
        toast.success(t('DNS check completed'))
      }
    },
    onError: (err) => handleServerError(err),
    onSettled: () => setCheckingTarget(null),
  })

  const platformSiteUrl = normalizeDisplayUrl(
    props.site.base_url || props.site.host
  )
  const platformApiUrl = normalizeDisplayUrl(props.site.api_base_url)
  const mainlandTarget = (props.site.domain_mainland_target ?? '').trim()
  const acmeSuffix = (props.site.edge_acme_alias_suffix ?? '').trim()
  const edgeMainlandEnabled = Boolean(props.site.edge_mainland_enabled)
  const closing = props.site.status === 'closing'
  const toggleEdgePanel = (id: number) =>
    setExpandedEdgeIds((prev) => ({ ...prev, [id]: !prev[id] }))

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Custom domains')}</CardTitle>
      </CardHeader>
      <CardContent className='grid gap-4'>
        <div className='bg-muted/30 grid gap-3 rounded-lg border p-3 sm:grid-cols-2'>
          <div className='space-y-1'>
            <div className='flex items-center gap-2'>
              <p className='text-muted-foreground text-xs font-medium'>
                {t('Platform site address')}
              </p>
              {platformSiteDomain && platformSiteDomain.status !== 'active' && (
                <StatusBadge
                  variant={domainVariant(platformSiteDomain.status)}
                  label={domainLabel(t, platformSiteDomain.status)}
                />
              )}
            </div>
            <div className='flex items-center gap-1.5'>
              <span className='truncate font-mono text-sm'>
                {platformSiteUrl}
              </span>
              {platformSiteUrl && <CopyButton value={platformSiteUrl} />}
            </div>
          </div>
          {platformApiUrl && (
            <div className='space-y-1'>
              <div className='flex items-center gap-2'>
                <p className='text-muted-foreground text-xs font-medium'>
                  {t('Platform API address')}
                </p>
                {platformApiDomain && platformApiDomain.status !== 'active' && (
                  <StatusBadge
                    variant={domainVariant(platformApiDomain.status)}
                    label={domainLabel(t, platformApiDomain.status)}
                  />
                )}
              </div>
              <div className='flex items-center gap-1.5'>
                <span className='truncate font-mono text-sm'>
                  {platformApiUrl}
                </span>
                <CopyButton value={platformApiUrl} />
              </div>
            </div>
          )}
        </div>

        {!props.site.custom_domain_enabled ? (
          <p className='text-muted-foreground text-sm'>
            {t('Custom domains are not available yet.')}
          </p>
        ) : (
          <>
            {customDomains.length > 0 && (
              <div className='grid gap-2.5'>
                {customDomains.map((domain) => {
                  const verifyRecords = parseVerifyRecords(
                    domain.verify_records
                  )
                  const showEdgePanel =
                    edgeMainlandEnabled &&
                    domain.role === 'site' &&
                    Boolean(domain.edge_acme_alias && acmeSuffix) &&
                    (domain.status === 'active' || domain.status === 'pending')
                  const edgeExpanded = Boolean(expandedEdgeIds[domain.id])
                  const edgeReady = domain.edge_cert_status === 'ready'
                  const edgeFailed = domain.edge_cert_status === 'failed'
                  const edgeIssuing = domain.edge_cert_status === 'issuing'
                  const edgeMismatch = Boolean(
                    domain.edge_cert_error &&
                      domain.edge_cert_error.includes('currently points to')
                  )
                  const canShowStep2 =
                    domain.status === 'active' &&
                    edgeReady &&
                    Boolean(mainlandTarget)
                  let edgeToggleLabel = t('Configure')
                  if (edgeExpanded) {
                    edgeToggleLabel = t('Hide')
                  } else if (edgeReady) {
                    edgeToggleLabel = t('View')
                  }
                  const showEdgeError =
                    (edgeFailed || edgeMismatch) &&
                    Boolean(domain.edge_cert_error)
                  return (
                    <div
                      key={domain.id}
                      className='space-y-2.5 rounded-lg border p-3'
                    >
                      <div className='flex flex-wrap items-center justify-between gap-2'>
                        <div className='flex flex-wrap items-center gap-2'>
                          <span className='font-mono text-sm font-medium'>
                            {domain.hostname}
                          </span>
                          <Badge variant='secondary'>
                            {domain.role === 'base_url'
                              ? t('API domain')
                              : t('Site domain')}
                          </Badge>
                        </div>
                        <div className='flex items-center gap-2'>
                          <StatusBadge
                            variant={domainVariant(domain.status)}
                            label={domainLabel(t, domain.status)}
                          />
                          {domain.status === 'pending' && (
                            <Button
                              size='sm'
                              variant='outline'
                              disabled={closing || checkNow.isPending}
                              onClick={() =>
                                checkNow.mutate({ id: domain.id, mode: 'dns' })
                              }
                            >
                              {checkingTarget?.id === domain.id &&
                              checkingTarget.mode === 'dns' &&
                              checkNow.isPending
                                ? t('Checking DNS...')
                                : t('Check DNS now')}
                            </Button>
                          )}
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() => setDeleting(domain)}
                            disabled={remove.isPending}
                          >
                            {t('Delete')}
                          </Button>
                        </div>
                      </div>
                      {domain.status !== 'active' &&
                        domain.verify_records &&
                        (verifyRecords.length > 0 ? (
                          <div className='space-y-2 pt-0.5'>
                            <p className='text-muted-foreground text-xs font-medium'>
                              {t('DNS records to add')}
                            </p>
                            <div className='grid gap-2'>
                              {verifyRecords.map((rec) => (
                                <div
                                  key={`${rec.type}-${rec.name}-${rec.value}`}
                                  className='bg-muted/40 grid gap-2 rounded-md border p-2.5 text-xs sm:grid-cols-[auto_minmax(0,1fr)_minmax(0,1fr)] sm:items-center'
                                >
                                  <Badge
                                    variant='outline'
                                    className='w-fit font-mono'
                                  >
                                    {rec.type}
                                  </Badge>
                                  <div className='flex min-w-0 items-center justify-between gap-1.5'>
                                    <div className='min-w-0'>
                                      <span className='text-muted-foreground block text-[11px]'>
                                        {t('Host record')}
                                      </span>
                                      <span className='block truncate font-mono'>
                                        {rec.name}
                                      </span>
                                    </div>
                                    <CopyButton value={rec.name} />
                                  </div>
                                  <div className='flex min-w-0 items-center justify-between gap-1.5'>
                                    <div className='min-w-0'>
                                      <span className='text-muted-foreground block text-[11px]'>
                                        {t('Record value')}
                                      </span>
                                      <span className='block truncate font-mono'>
                                        {rec.value}
                                      </span>
                                    </div>
                                    <CopyButton value={rec.value} />
                                  </div>
                                </div>
                              ))}
                            </div>
                          </div>
                        ) : (
                          <pre className='bg-muted overflow-x-auto rounded p-2 text-xs'>
                            {domain.verify_records}
                          </pre>
                        ))}
                      {domain.last_error && (
                        <p className='text-destructive text-xs'>
                          {formatDomainLastError(domain.last_error, t)}
                        </p>
                      )}
                      {showEdgePanel && (
                        <div className='bg-muted/30 space-y-2.5 rounded-md border px-3 py-2.5'>
                          <div className='flex flex-wrap items-center justify-between gap-2'>
                            <div className='flex flex-wrap items-center gap-2'>
                              <span className='text-xs font-medium'>
                                {t('Mainland China acceleration (optional)')}
                              </span>
                              {edgeReady || edgeIssuing || edgeFailed ? (
                                <StatusBadge
                                  variant={edgeCertVariant(
                                    domain.edge_cert_status
                                  )}
                                  label={edgeCertLabel(
                                    t,
                                    domain.edge_cert_status
                                  )}
                                />
                              ) : (
                                <span className='text-muted-foreground text-xs'>
                                  {t('Not configured')}
                                </span>
                              )}
                              {edgeReady &&
                                (domain.edge_cert_not_after ?? 0) > 0 && (
                                  <span className='text-muted-foreground text-[11px]'>
                                    {t('Expires {{date}}', {
                                      date: formatTimestampToDate(
                                        domain.edge_cert_not_after
                                      ),
                                    })}
                                  </span>
                                )}
                            </div>
                            <Button
                              type='button'
                              size='sm'
                              variant='ghost'
                              className='h-7 px-2 text-xs'
                              onClick={() => toggleEdgePanel(domain.id)}
                            >
                              {edgeToggleLabel}
                            </Button>
                          </div>
                          {edgeExpanded && (
                            <div className='space-y-2.5 border-t pt-2.5'>
                              <p className='text-muted-foreground text-xs'>
                                {t(
                                  'Optional: speed up mainland China homepage visits through the platform edge node. Your API domain stays on Cloudflare.'
                                )}
                              </p>
                              <div className='space-y-1.5'>
                                <div className='flex flex-wrap items-center justify-between gap-2'>
                                  <p className='text-xs font-medium'>
                                    {t(
                                      'Step 1: Add the ACME certificate verification CNAME (default line)'
                                    )}
                                  </p>
                                  {!edgeReady && (
                                    <Button
                                      type='button'
                                      size='sm'
                                      variant='outline'
                                      className='h-7 text-xs'
                                      disabled={closing || checkNow.isPending}
                                      onClick={() =>
                                        checkNow.mutate({
                                          id: domain.id,
                                          mode: 'edge_cert',
                                        })
                                      }
                                    >
                                      {checkingTarget?.id === domain.id &&
                                      checkingTarget.mode === 'edge_cert' &&
                                      checkNow.isPending
                                        ? t('Checking certificate...')
                                        : t('Check certificate')}
                                    </Button>
                                  )}
                                </div>
                                <div className='bg-background grid gap-2 rounded-md border p-2.5 text-xs sm:grid-cols-[auto_minmax(0,1fr)_minmax(0,1fr)] sm:items-center'>
                                  <Badge
                                    variant='outline'
                                    className='w-fit font-mono'
                                  >
                                    CNAME
                                  </Badge>
                                  <div className='flex min-w-0 items-center justify-between gap-1.5'>
                                    <div className='min-w-0'>
                                      <span className='text-muted-foreground block text-[11px]'>
                                        {t('Host record')}
                                      </span>
                                      <span className='block truncate font-mono'>
                                        {`_acme-challenge.${domain.hostname}`}
                                      </span>
                                    </div>
                                    <CopyButton
                                      value={`_acme-challenge.${domain.hostname}`}
                                    />
                                  </div>
                                  <div className='flex min-w-0 items-center justify-between gap-1.5'>
                                    <div className='min-w-0'>
                                      <span className='text-muted-foreground block text-[11px]'>
                                        {t('Record value')}
                                      </span>
                                      <span className='block truncate font-mono'>
                                        {`${domain.edge_acme_alias}.${acmeSuffix}`}
                                      </span>
                                    </div>
                                    <CopyButton
                                      value={`${domain.edge_acme_alias}.${acmeSuffix}`}
                                    />
                                  </div>
                                </div>
                              </div>
                              {showEdgeError && (
                                <p className='text-destructive text-xs'>
                                  {formatDomainLastError(
                                    domain.edge_cert_error,
                                    t
                                  )}
                                </p>
                              )}
                              {!showEdgeError && !edgeReady && (
                                <p className='text-muted-foreground text-xs'>
                                  {t(
                                    'Add the CNAME record above and click "Check certificate" to issue the edge certificate.'
                                  )}
                                </p>
                              )}
                              {canShowStep2 ? (
                                <div className='space-y-1.5'>
                                  <p className='text-xs font-medium'>
                                    {t(
                                      'Step 2: Point your mainland China DNS line to the acceleration target'
                                    )}
                                  </p>
                                  <div className='bg-background grid gap-2 rounded-md border p-2.5 text-xs sm:grid-cols-[auto_minmax(0,1fr)_minmax(0,1fr)] sm:items-center'>
                                    <Badge
                                      variant='outline'
                                      className='w-fit font-mono'
                                    >
                                      CNAME
                                    </Badge>
                                    <div className='flex min-w-0 items-center justify-between gap-1.5'>
                                      <div className='min-w-0'>
                                        <span className='text-muted-foreground block text-[11px]'>
                                          {t('Host record')} ·{' '}
                                          {t('Mainland China line')}
                                        </span>
                                        <span className='block truncate font-mono'>
                                          {domain.hostname}
                                        </span>
                                      </div>
                                      <CopyButton value={domain.hostname} />
                                    </div>
                                    <div className='flex min-w-0 items-center justify-between gap-1.5'>
                                      <div className='min-w-0'>
                                        <span className='text-muted-foreground block text-[11px]'>
                                          {t('Record value')}
                                        </span>
                                        <span className='block truncate font-mono'>
                                          {mainlandTarget}
                                        </span>
                                      </div>
                                      <CopyButton value={mainlandTarget} />
                                    </div>
                                  </div>
                                  <p className='text-muted-foreground text-[11px]'>
                                    {t(
                                      'Keep the default/overseas CNAME pointing to Cloudflare. Requires a DNS provider that supports split-line routing (such as AliDNS or DNSPod).'
                                    )}
                                  </p>
                                </div>
                              ) : (
                                <p className='text-muted-foreground text-xs'>
                                  {t(
                                    'Step 2 will appear automatically once the domain is active and the edge certificate is ready.'
                                  )}
                                </p>
                              )}
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            )}

            {bothEmpty && (
              <div className='border-border space-y-3 border-t pt-4'>
                <div className='space-y-0.5'>
                  <p className='text-sm font-semibold'>
                    {t('Bind custom domains')}
                  </p>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Bind both a site domain and an API domain together. After submitting, add the 2 default-line CNAME records shown below to issue HTTPS certificates automatically.'
                    )}
                  </p>
                </div>
                <div className='grid gap-3 sm:grid-cols-2'>
                  <div className='space-y-1.5'>
                    <Label htmlFor='new-site-domain'>{t('Site domain')}</Label>
                    <Input
                      id='new-site-domain'
                      placeholder='www.example.com'
                      disabled={closing || addPair.isPending}
                      value={siteHostname}
                      onChange={(event) => setSiteHostname(event.target.value)}
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'A site domain serves web pages, sign-in and console, and rejects model API calls.'
                      )}
                    </p>
                  </div>
                  <div className='space-y-1.5'>
                    <Label htmlFor='new-api-domain'>{t('API domain')}</Label>
                    <Input
                      id='new-api-domain'
                      placeholder='api.example.com'
                      disabled={closing || addPair.isPending}
                      value={apiHostname}
                      onChange={(event) => setApiHostname(event.target.value)}
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'An API address answers model API calls and also opens your site homepage and console. Search engines do not index it.'
                      )}
                    </p>
                  </div>
                </div>
                {pairDuplicate && (
                  <p role='alert' className='text-destructive text-xs'>
                    {t('Site domain and API domain must be different')}
                  </p>
                )}
                <div>
                  <Button
                    type='button'
                    variant='brand'
                    disabled={
                      closing ||
                      !normalizedSite ||
                      !normalizedApi ||
                      pairDuplicate ||
                      addPair.isPending
                    }
                    onClick={() => addPair.mutate()}
                  >
                    {t('Submit custom domains')}
                  </Button>
                </div>
              </div>
            )}

            {missingRole && (
              <div className='border-border space-y-3 border-t pt-4'>
                <p className='text-muted-foreground text-xs'>
                  {missingRole === 'base_url'
                    ? t(
                        'Your site domain is configured. Add the matching API domain below, or delete the existing domain to reconfigure both together.'
                      )
                    : t(
                        'Your API domain is configured. Add the matching site domain below, or delete the existing domain to reconfigure both together.'
                      )}
                </p>
                <div className='flex flex-wrap items-end gap-2'>
                  <div className='space-y-1.5'>
                    <Label htmlFor='missing-domain'>
                      {missingRole === 'base_url'
                        ? t('API domain')
                        : t('Site domain')}
                    </Label>
                    <Input
                      id='missing-domain'
                      className='w-64'
                      disabled={closing || addSingleMissing.isPending}
                      placeholder={
                        missingRole === 'base_url'
                          ? 'api.example.com'
                          : 'www.example.com'
                      }
                      value={
                        missingRole === 'base_url' ? apiHostname : siteHostname
                      }
                      onChange={(event) =>
                        missingRole === 'base_url'
                          ? setApiHostname(event.target.value)
                          : setSiteHostname(event.target.value)
                      }
                    />
                  </div>
                  <Button
                    type='button'
                    disabled={
                      closing ||
                      addSingleMissing.isPending ||
                      !(missingRole === 'base_url'
                        ? apiHostname.trim()
                        : siteHostname.trim())
                    }
                    onClick={() =>
                      addSingleMissing.mutate({
                        hostname: (missingRole === 'base_url'
                          ? apiHostname
                          : siteHostname
                        ).trim(),
                        role: missingRole,
                      })
                    }
                  >
                    {t('Add')}
                  </Button>
                </div>
              </div>
            )}

            {bothConfigured && (
              <p className='text-muted-foreground text-xs'>
                {t('To replace a domain, delete it above and bind a new one.')}
              </p>
            )}
          </>
        )}
        <ConfirmDialog
          open={deleting !== null}
          onOpenChange={(open) => !open && setDeleting(null)}
          title={t('Delete domain')}
          desc={t('Customers can no longer reach your site on {{host}}.', {
            host: deleting?.hostname ?? '',
          })}
          destructive
          isLoading={remove.isPending}
          handleConfirm={() => deleting && remove.mutate(deleting.id)}
        />
      </CardContent>
    </Card>
  )
}

function SearchCard(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const closing = props.site.status === 'closing'
  const seo = useMutation({
    mutationFn: (value: boolean) => saveOwnerSite({ seo_index: value }),
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all }),
    onError: (err) => handleServerError(err),
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Search engines')}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className='flex items-center justify-between gap-4'>
          <Label htmlFor='seo-index'>
            {t('Let search engines index my site')}
          </Label>
          <Switch
            id='seo-index'
            checked={props.site.seo_index}
            disabled={closing || seo.isPending}
            onCheckedChange={(checked) => seo.mutate(checked)}
          />
        </div>
      </CardContent>
    </Card>
  )
}

function CloseRequestCard(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [reason, setReason] = useState('')
  useDraftNavigation(reason !== '')
  const requested =
    (props.site.close_requested_at ?? 0) > 0 || props.site.status === 'closing'
  const request = useMutation({
    mutationFn: () => requestSiteClose(reason.trim()),
    onSuccess: () => {
      toast.success(
        t(
          'Close request sent. When the site closes, customer balances and unredeemed redemption codes are automatically inherited by the main site.'
        )
      )
      setReason('')
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.site })
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Request to close this site')}</CardTitle>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        {requested ? (
          <p className='text-muted-foreground text-sm'>
            {t('Close requested')}
          </p>
        ) : (
          <div className='flex max-w-2xl flex-col gap-3'>
            <Label htmlFor='close-reason'>{t('Reason')}</Label>
            <Textarea
              id='close-reason'
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
            <div>
              <Button
                variant='destructive'
                disabled={reason.trim() === '' || request.isPending}
                onClick={() => request.mutate()}
              >
                {t('Request to close this site')}
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function HomePageCard(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const saved = props.site.home_page_content ?? ''
  const [content, setContent] = useState(saved)
  const previousSaved = useRef(saved)
  useEffect(() => {
    if (previousSaved.current !== saved) {
      setContent((current) =>
        current === previousSaved.current ? saved : current
      )
      previousSaved.current = saved
    }
  }, [saved])
  const dirty = content !== saved
  useDraftNavigation(dirty)
  const closing = props.site.status === 'closing'
  const save = useMutation({
    mutationFn: () => saveOwnerSite({ home_page_content: content }),
    onSuccess: (updated) => {
      queryClient.setQueryData(siteOwnerKeys.site, updated)
      const nextSaved = updated.home_page_content ?? ''
      previousSaved.current = nextSaved
      setContent(nextSaved)
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void queryClient.invalidateQueries({ queryKey: adtySiteQueryKey })
      void queryClient.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
    },
    onError: (err) => handleServerError(err),
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Custom Home Page')}</CardTitle>
      </CardHeader>
      <CardContent className='grid gap-3'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Enter Markdown, HTML, or an http:// or https:// URL (embedded as an iframe). Setting custom home page content replaces the default homepage and hides default status info.'
          )}
        </p>
        <div className='space-y-2'>
          <Label htmlFor='owner-home-page-content'>
            {t('Home page content')}
          </Label>
          <Textarea
            id='owner-home-page-content'
            rows={8}
            className='font-mono text-xs'
            disabled={closing || save.isPending}
            value={content}
            placeholder={t(
              'Leave empty to use the default homepage, or paste Markdown, HTML, or https://...'
            )}
            onChange={(event) => setContent(event.target.value)}
          />
        </div>
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            variant='brand'
            disabled={closing || !dirty || save.isPending}
            onClick={() => save.mutate()}
          >
            {t('Save home page')}
          </Button>
          {dirty && (
            <Button
              type='button'
              variant='outline'
              disabled={save.isPending}
              onClick={() => setContent(saved)}
            >
              {t('Reset')}
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

/** Site settings: brand, custom home page, legal docs, custom domains and SEO (task 11.6). */
export function SiteSettingsPanel(props: { site: OwnerSite }) {
  return (
    <div className='flex flex-col gap-6'>
      <BrandCard site={props.site} />
      <HomePageCard site={props.site} />
      <SiteLegalCard site={props.site} />
      <DomainsCard site={props.site} />
      <SearchCard site={props.site} />
      <CloseRequestCard site={props.site} />
    </div>
  )
}
