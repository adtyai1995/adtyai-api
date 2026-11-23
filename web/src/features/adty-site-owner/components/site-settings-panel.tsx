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
import type { TFunction } from 'i18next'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { adtySiteQueryKey } from '@/features/adty-site/api'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { handleServerError } from '@/lib/handle-server-error'
import { toSquareIconFile } from '@/lib/square-icon'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import {
  addDomain,
  addSenderDomain,
  getSenderDomain,
  requestSiteClose,
  removeDomain,
  removeSenderDomain,
  saveOwnerSite,
  siteOwnerKeys,
  uploadLogo,
} from '../api'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import { parseSenderRecords } from '../lib/site-owner'
import type {
  OwnerSite,
  SenderDomain,
  SiteBrand,
  SiteDomain,
  SiteDomainRole,
} from '../types'

const BRAND_LIMITS: Record<
  Exclude<keyof SiteBrand, 'logo_url' | 'favicon_url'>,
  number
> = {
  name: 40,
  tagline: 80,
  notice: 500,
  support_contact: 200,
  title: 80,
  theme_color: 7,
  tawk_property_id: 64,
  tawk_widget_id: 64,
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

function domainAutomationLabel(t: TFunction, state: string): string {
  switch (state) {
    case 'queued':
      return t('Domain automation: Queued')
    case 'validating':
      return t('Domain automation: Validating')
    case 'ready':
      return t('Domain automation: Ready')
    case 'retry':
      return t('Domain automation: Retrying')
    case 'redirect_only':
      return t('Domain automation: Redirect only')
    case 'removed':
      return t('Domain automation: Removed')
    default:
      return state
  }
}

function BrandCard(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [brand, setBrand] = useState<SiteBrand>(props.site.brand)
  const [acknowledged,setAcknowledged] = useState<{previous: SiteBrand; current: SiteBrand} | null>(null)
  const observed = acknowledged?.previous === props.site.brand ? acknowledged.current : props.site.brand
  const edits = useRef<Partial<Record<keyof SiteBrand,number>>>({})
  const editBrand = (patch: Partial<SiteBrand>) => {
    for (const key of Object.keys(patch) as (keyof SiteBrand)[]) edits.current[key] = (edits.current[key] ?? 0) + 1
    setBrand(current => ({...current, ...patch}))
  }
  const [baseline,setBaseline]=useState(props.site.brand)
  const changed=(Object.keys(brand) as (keyof SiteBrand)[]).filter(key=>(brand[key]??'')!==(baseline[key]??''))
  const conflicts=changed.filter(key=>baseline[key]!==observed[key] && brand[key]!==observed[key])
  useDraftNavigation(changed.length>0)
  useEffect(()=>{
    if (!changed.length) {setBrand(observed);setBaseline(observed)}
  },[observed,changed.length])
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
      if (faviconUrl) edits.current.favicon_url = (edits.current.favicon_url ?? 0) + 1
      setBrand(prev => ({ ...prev, [key]: url, ...(key === 'logo_url' && faviconUrl && !prev.favicon_url ? { favicon_url: faviconUrl } : {}) }))
    },
    onError: (err) => handleServerError(err),
  })
  const save = useMutation({
    mutationFn: (_submitted: typeof edits.current) => saveOwnerSite({
      brand: Object.fromEntries(changed.map(key=>[key,brand[key]??''])),
      expected_brand:Object.fromEntries(changed.map(key=>[key,baseline[key]??''])),
    }),
    onSuccess: (updated, submitted) => {
      queryClient.setQueryData(siteOwnerKeys.site,updated)
      setAcknowledged({previous:props.site.brand,current:updated.brand})
      setBaseline(updated.brand)
      setBrand(current => ({
        ...updated.brand,
        ...Object.fromEntries((Object.keys(edits.current) as (keyof SiteBrand)[])
          .filter(key => edits.current[key] !== submitted[key])
          .map(key => [key,current[key]])),
      }))
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void queryClient.invalidateQueries({ queryKey: adtySiteQueryKey })
      void queryClient.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
    },
    onError: (err) => {handleServerError(err);void queryClient.invalidateQueries({queryKey:siteOwnerKeys.all})},
  })
  const field = (
    key: keyof typeof BRAND_LIMITS,
    label: string,
    multiline = false
  ) => {
    const id = `brand-${key}`
    const inputProps = {
      id,
      maxLength: BRAND_LIMITS[key],
      value: brand[key] ?? '',
      placeholder: key === 'theme_color' ? '#RRGGBB' : undefined,
      onChange: (event: { target: { value: string } }) =>
        editBrand({ [key]: event.target.value }),
    }
    return (
      <div className='space-y-2'>
        <Label htmlFor={id}>{label}</Label>
        {multiline ? <Textarea {...inputProps} /> : <Input {...inputProps} />}
        <p className='text-muted-foreground text-xs'>
          {t('{{count}} / {{max}} characters', {
            count: [...(brand[key] ?? '')].length,
            max: BRAND_LIMITS[key],
          })}
        </p>
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
        disabled={upload.isPending}
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
            size='sm'
            variant='outline'
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
    theme_color: t('Theme color'),
    tagline: t('Tagline'),
    notice: t('Notice'),
    support_contact: t('Support contact'),
    tawk_property_id: t('Tawk.to property ID'),
    tawk_widget_id: t('Tawk.to widget ID'),
    logo_url: t('Logo'),
    favicon_url: t('Favicon'),
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Brand')}</CardTitle>
      </CardHeader>
      <CardContent className='grid gap-3'>
        {conflicts.length>0 && <div role="alert">
          {conflicts.map(key=><p key={key}>{brandFieldLabels[key] ?? key} · {t('Current value')}: {observed[key]} · {t('Previous value')}: {baseline[key]}</p>)}
          <Button variant="outline" onClick={()=>{
            setBrand({...observed,...Object.fromEntries(changed.map(key=>[key,brand[key]]))})
            setBaseline(observed)
          }}>{t('Keep my changes')}</Button>
        </div>}
        {field('name', t('Site name'))}
        {field('title', t('Website title'))}
        {field('theme_color', t('Theme color'))}
        {field('tagline', t('Tagline'))}
        {field('notice', t('Notice'), true)}
        {field('support_contact', t('Support contact'))}
        <div className='grid gap-3 sm:grid-cols-2'>
          {field('tawk_property_id', t('Tawk.to property ID'))}
          {field('tawk_widget_id', t('Tawk.to widget ID'))}
        </div>
        <p className='text-muted-foreground text-xs'>
          {t('Live chat appears when both Tawk.to IDs are set.')}
        </p>
        {imageField('logo_url', t('Logo'), t('Remove logo'))}
        {imageField('favicon_url', t('Favicon'), t('Remove favicon'))}
        <p className='text-muted-foreground text-xs'>
          {t('Brand text is shown as plain text.')}
        </p>
        <div>
          <Button
            variant='brand'
            disabled={save.isPending || !changed.length || conflicts.length>0}
            onClick={() => save.mutate({...edits.current})}
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
  const [hostname, setHostname] = useState('')
  const [role, setRole] = useState<SiteDomainRole>('site')
  useDraftNavigation(hostname !== '')
  const [deleting, setDeleting] = useState<SiteDomain | null>(null)
  const add = useMutation({
    mutationFn: () => addDomain(hostname.trim(), role),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      setHostname('')
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
  const domains = props.site.domains ?? []
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Custom domains')}</CardTitle>
      </CardHeader>
      <CardContent className='grid gap-3'>
        <p className='text-sm'>
          {t('Platform address')}:{' '}
          <span className='font-mono'>{props.site.host}</span>{' '}
          <CopyButton value={props.site.base_url} />
        </p>
        {props.site.api_base_url && (
          <p className='text-sm'>
            {t('API address')}:{' '}
            <span className='font-mono'>{props.site.api_base_url}</span>{' '}
            <CopyButton value={props.site.api_base_url} />
          </p>
        )}
        {!props.site.custom_domain_enabled ? (
          <p className='text-muted-foreground text-sm'>
            {t('Custom domains are not available yet.')}
          </p>
        ) : (
          <>
            {domains.map((domain) => (
              <div key={domain.id} className='space-y-1 rounded-md border p-3'>
                <div className='flex items-center justify-between gap-2'>
                  <span className='font-mono text-sm'>{domain.hostname}</span>
                  <div className='flex items-center gap-2'>
                    <span className='text-muted-foreground text-xs'>
                      {domain.role === 'base_url'
                        ? t('API address')
                        : t('Site domain')}
                    </span>
                    <StatusBadge
                      variant={domainVariant(domain.status)}
                      label={domainLabel(t, domain.status)}
                    />
                    <Button
                      size='sm'
                      variant='outline'
                      onClick={() => setDeleting(domain)}
                      disabled={domain.managed_by === 'platform'}
                    >
                      {t('Delete')}
                    </Button>
                  </div>
                </div>
                {domain.managed_by === 'platform' && (
                  <p className='text-muted-foreground text-xs'>
                    {t('Platform domains are managed by the platform')}
                  </p>
                )}
                {domain.automation_state && (
                  <p className='text-muted-foreground text-xs'>
                    {domainAutomationLabel(t, domain.automation_state)}
                  </p>
                )}
                {domain.managed_by !== 'platform' &&
                  domain.status !== 'active' &&
                  domain.verify_records && (
                    <pre className='bg-muted overflow-x-auto rounded p-2 text-xs'>
                      {domain.verify_records}
                    </pre>
                  )}
                {domain.last_error && (
                  <p className='text-destructive text-xs'>
                    {domain.last_error}
                  </p>
                )}
              </div>
            ))}
            <div className='flex flex-wrap items-end gap-2'>
              <div className='space-y-2'>
                <Label htmlFor='new-domain'>{t('Domain')}</Label>
                <Input
                  id='new-domain'
                  className='w-64'
                  placeholder='www.example.com'
                  value={hostname}
                  onChange={(event) => setHostname(event.target.value)}
                />
              </div>
              <div className='space-y-2'>
                <Label>{t('What it is for')}</Label>
                <Select
                  value={role}
                  onValueChange={(value) => setRole(value as SiteDomainRole)}
                >
                  <SelectTrigger className='w-56'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='site'>{t('Site domain')}</SelectItem>
                      <SelectItem value='base_url'>
                        {t('API address')}
                      </SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
              <Button
                disabled={!hostname.trim() || add.isPending}
                onClick={() => add.mutate()}
              >
                {t('Add')}
              </Button>
            </div>
            <p className='text-muted-foreground text-xs'>
              {role === 'base_url'
                ? t(
                    'An API address answers model API calls and also opens your site homepage and console. Search engines do not index it.'
                  )
                : t(
                    'A site domain serves web pages, sign-in and console, and rejects model API calls.'
                  )}
            </p>
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
            disabled={seo.isPending}
            onCheckedChange={(checked) => seo.mutate(checked)}
          />
        </div>
      </CardContent>
    </Card>
  )
}

function senderLabel(t: TFunction, status: SenderDomain['status']): string {
  if (status === 'verified') return t('Verified')
  if (status === 'failed') return t('Verification failed')
  if (status === 'removing') return t('Removing')
  if (status === 'disabled') return t('Turned off')
  return t('Waiting for DNS')
}

function senderVariant(status: SenderDomain['status']) {
  if (status === 'verified') return 'success' as const
  if (status === 'failed') return 'danger' as const
  if (status === 'disabled') return 'neutral' as const
  return 'warning' as const
}

/** Sender domain (task 11.3): DNS records to add and the verification status. */
function SenderDomainCard(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [domain, setDomain] = useState('')
  useDraftNavigation(domain !== '')
  const [deleting, setDeleting] = useState(false)
  const query = useQuery({
    queryKey: siteOwnerKeys.list('sender-domain', {}),
    queryFn: getSenderDomain,
    enabled: props.site.sender_domain_enabled,
  })
  const add = useMutation({
    mutationFn: () => addSenderDomain(domain.trim()),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      setDomain('')
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  const remove = useVerifiedOwnerMutation<void>({
    action: 'sender.delete',
    target: () => props.site.id,
    run: (_vars, proof) => removeSenderDomain(proof),
    successMessage: t('Deleted successfully'),
    onSuccess: () => setDeleting(false),
  })
  const current =
    query.data && query.data.status !== 'disabled' ? query.data : null
  const records = parseSenderRecords(current?.records)

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Sender domain')}</CardTitle>
      </CardHeader>
      <CardContent className='grid gap-3'>
        {!props.site.sender_domain_enabled && (
          <p className='text-muted-foreground text-sm'>
            {t('Not available yet')}
          </p>
        )}
        {props.site.sender_domain_enabled && query.isError && (
          <ErrorState onRetry={() => void query.refetch()} />
        )}
        {props.site.sender_domain_enabled && query.isPending && (
          <LoadingState />
        )}
        {props.site.sender_domain_enabled && query.isSuccess && (
          <>
            <p className='text-muted-foreground text-sm'>
              {t(
                'System emails to your customers are sent from your own domain once it is verified. Until then they come from the platform address with your site name.'
              )}
            </p>
            {query.data?.status === 'disabled' &&
              query.data.disabled_by > 0 && (
                <p className='text-muted-foreground text-sm'>
                  {t('The platform turned off your sender domain.')}
                </p>
              )}
            {current ? (
              <div className='space-y-2 rounded-md border p-3'>
                <div className='flex items-center justify-between gap-2'>
                  <span className='font-mono text-sm'>{current.domain}</span>
                  <div className='flex items-center gap-2'>
                    <StatusBadge
                      variant={senderVariant(current.status)}
                      label={senderLabel(t, current.status)}
                    />
                    <Button
                      size='sm'
                      variant='outline'
                      disabled={current.status === 'removing'}
                      onClick={() => setDeleting(true)}
                    >
                      {t('Delete')}
                    </Button>
                  </div>
                </div>
                {current.status === 'removing' && current.cleanup_error && (
                  <p className='text-destructive text-xs'>
                    {current.cleanup_error}
                  </p>
                )}
                {current.status !== 'verified' &&
                  current.status !== 'removing' &&
                  records.length > 0 && (
                  <StaticDataTable
                    tableProps={{ 'aria-label': t('DNS records to add') }}
                    data={records}
                    getRowKey={(row) => `${row.type}-${row.name}-${row.value}`}
                    columns={[
                      {
                        id: 'type',
                        header: t('Type'),
                        cell: (row) => row.type,
                      },
                      {
                        id: 'name',
                        header: t('Name'),
                        cell: (row) => (
                          <span className='font-mono text-xs'>{row.name}</span>
                        ),
                      },
                      {
                        id: 'value',
                        header: t('Value'),
                        cell: (row) => (
                          <span className='flex items-center gap-1'>
                            <span className='max-w-64 truncate font-mono text-xs'>
                              {row.value}
                            </span>
                            <CopyButton value={row.value} />
                          </span>
                        ),
                      },
                      {
                        id: 'priority',
                        header: t('Priority'),
                        cell: (row) => row.priority ?? '—',
                      },
                    ]}
                  />
                )}
              </div>
            ) : (
              <div className='flex items-end gap-2'>
                <div className='space-y-2'>
                  <Label htmlFor='sender-domain'>{t('Domain')}</Label>
                  <Input
                    id='sender-domain'
                    className='w-72'
                    placeholder='mail.example.com'
                    value={domain}
                    onChange={(event) => setDomain(event.target.value)}
                  />
                </div>
                <Button
                  disabled={!domain.trim() || add.isPending}
                  onClick={() => add.mutate()}
                >
                  {t('Add')}
                </Button>
              </div>
            )}
          </>
        )}
        <ConfirmDialog
          open={deleting}
          onOpenChange={setDeleting}
          title={t('Delete sender domain')}
          desc={t(
            'Emails go back to the platform address with your site name.'
          )}
          destructive
          isLoading={remove.isPending}
          handleConfirm={() => remove.mutate()}
        />
      </CardContent>
    </Card>
  )
}

function CloseRequestCard(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [reason, setReason] = useState('')
  useDraftNavigation(reason !== '')
  const requested = (props.site.close_requested_at ?? 0) > 0
  const request = useMutation({
    mutationFn: () => requestSiteClose(reason.trim()),
    onSuccess: () => {
      toast.success(
        t(
          'Close request sent. When the site closes, customer balances and unredeemed recharge codes are automatically inherited by the main site.'
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
          <>
            <Label htmlFor='close-reason'>{t('Reason')}</Label>
            <Textarea
              id='close-reason'
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
            <Button
              variant='destructive'
              disabled={reason.trim() === '' || request.isPending}
              onClick={() => request.mutate()}
            >
              {t('Request to close this site')}
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  )
}

/** Site settings: brand, custom domains, sender domain and SEO (task 11.6). */
export function SiteSettingsPanel(props: { site: OwnerSite }) {
  return (
    <div className='grid gap-4'>
      <BrandCard site={props.site} />
      <DomainsCard site={props.site} />
      <SenderDomainCard site={props.site} />
      <SearchCard site={props.site} />
      <CloseRequestCard site={props.site} />
    </div>
  )
}
