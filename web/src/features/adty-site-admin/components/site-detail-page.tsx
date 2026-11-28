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
import { useLocation, useNavigate } from '@tanstack/react-router'
import { AlertTriangle, ArrowLeft, Ticket, Trash2 } from 'lucide-react'
import { useEffect, useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { QuickMarginVoucherDialog } from '@/features/adty-voucher/quick-margin-voucher-dialog'
import { DraftNavigationProvider } from '@/features/system-settings/components/draft-navigation-provider'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatNumber,
  formatTimestamp,
  formatTimestampToDate,
} from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  deleteSite,
  getSite,
  getSiteStats,
  siteAdminKeys,
  updateSitePartnerReferral,
} from '../api'
import type { AdminSite } from '../types'
import { parseSiteBrand } from '../lib/sites'
import { CloseSiteDialog } from './close-site-dialog'
import { SiteBasicBrandForm } from './site-basic-brand-form'
import {
  CloseMailCard,
  SiteAuditCard,
  SiteBusinessStatsCard,
  SiteCustomersCard,
  SiteDomainsCard,
  SiteMarginPoliciesCard,
  SiteSupplyDiscountCard,
} from './site-detail-sections'
import { SiteFundPanel } from './site-fund-panel'
import { SiteStatusBadge } from './site-status-badge'
import { SiteTierControl } from './site-tier-control'

type SiteDetailTab =
  | 'basic'
  | 'domains'
  | 'business'
  | 'tier'
  | 'wallets'
  | 'customers'
  | 'danger'

function resolveSiteDetailTab(
  raw: string | null | undefined,
  isRoot: boolean
): SiteDetailTab {
  const normalized = raw?.trim().toLowerCase()
  if (normalized === 'finance') {
    return isRoot ? 'wallets' : 'business'
  }
  switch (normalized) {
    case 'basic':
    case 'domains':
    case 'business':
    case 'customers':
      return normalized
    case 'tier':
    case 'wallets':
    case 'danger':
      return isRoot ? normalized : 'basic'
    default:
      return 'basic'
  }
}

const LATE_BIND_WINDOW_SECONDS = 7 * 24 * 3600

function partnerStopReasonLabel(
  reason: string | undefined,
  t: (key: string) => string
): string {
  switch (reason) {
    case 'referred_site_closed':
      return t('Referred site closed')
    case 'referrer_site_closed':
      return t('Referrer site closed')
    default:
      return reason || '—'
  }
}

function SitePartnerReferralCard(props: { site: AdminSite; isRoot: boolean }) {
  const { site, isRoot } = props
  const { t } = useTranslation()
  const fieldId = useId()
  const queryClient = useQueryClient()

  const [bindCode, setBindCode] = useState('')
  const [bindSiteId, setBindSiteId] = useState('')
  const [bindRate, setBindRate] = useState('')
  const [bindReason, setBindReason] = useState('')

  const [rateValue, setRateValue] = useState(site.referrer_rate ?? '')
  const [rateReason, setRateReason] = useState('')

  const [stopDialogOpen, setStopDialogOpen] = useState(false)
  const [stopReason, setStopReason] = useState('')

  useEffect(() => {
    setRateValue(site.referrer_rate ?? '')
  }, [site.id, site.referrer_rate])

  const hasReferrer = Boolean(site.referrer_site_id && site.referrer_site_id > 0)
  const isStopped = Boolean(site.referrer_stopped_at && site.referrer_stopped_at > 0)
  const withinLateBindWindow =
    site.created_at > 0 &&
    Math.floor(Date.now() / 1000) - site.created_at <= LATE_BIND_WINDOW_SECONDS
  const canLateBind =
    isRoot &&
    !hasReferrer &&
    !site.referrer_late_bound &&
    site.status === 'active' &&
    withinLateBindWindow
  const canUpdateRate =
    isRoot &&
    hasReferrer &&
    !isStopped &&
    site.status !== 'closed' &&
    site.status !== 'deleting'
  const canStopReferral = isRoot && hasReferrer && !isStopped

  const parsedBindSiteId = bindSiteId.trim() === '' ? null : Number(bindSiteId)
  const bindSiteIdError =
    parsedBindSiteId !== null &&
    (!Number.isInteger(parsedBindSiteId) || parsedBindSiteId <= 0)
      ? t('Enter a valid referrer site ID')
      : ''

  const parsedBindRate = bindRate.trim() === '' ? null : Number(bindRate)
  const bindRateError =
    parsedBindRate !== null &&
    (!Number.isFinite(parsedBindRate) ||
      parsedBindRate < 0 ||
      parsedBindRate > 50)
      ? t('Partner referral rate must be between 0% and 50%')
      : ''

  const parsedUpdateRate = rateValue.trim() === '' ? null : Number(rateValue)
  const updateRateError =
    parsedUpdateRate !== null &&
    (!Number.isFinite(parsedUpdateRate) ||
      parsedUpdateRate < 0 ||
      parsedUpdateRate > 50)
      ? t('Partner referral rate must be between 0% and 50%')
      : ''

  const bindMutation = useMutation({
    mutationFn: () => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      const payload: Parameters<typeof updateSitePartnerReferral>[1] = {}
      if (bindCode.trim() !== '') {
        payload.bind_referrer_code = bindCode.trim().toUpperCase()
      }
      if (parsedBindSiteId !== null && parsedBindSiteId > 0) {
        payload.bind_referrer_site_id = parsedBindSiteId
      }
      if (bindRate.trim() !== '') {
        payload.rate_percent = bindRate.trim()
      }
      if (bindReason.trim() !== '') {
        payload.reason = bindReason.trim()
      }
      return updateSitePartnerReferral(site.id, payload)
    },
    onSuccess: () => {
      setBindCode('')
      setBindSiteId('')
      setBindRate('')
      setBindReason('')
      toast.success(t('Partner referrer bound'))
      void queryClient.invalidateQueries({
        queryKey: siteAdminKeys.site(site.id),
      })
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (err) => handleServerError(err),
  })

  const rateMutation = useMutation({
    mutationFn: () => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      return updateSitePartnerReferral(site.id, {
        rate_percent: rateValue.trim(),
        reason: rateReason.trim() || undefined,
      })
    },
    onSuccess: () => {
      setRateReason('')
      toast.success(t('Partner referral rate updated'))
      void queryClient.invalidateQueries({
        queryKey: siteAdminKeys.site(site.id),
      })
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (err) => handleServerError(err),
  })

  const stopMutation = useMutation({
    mutationFn: () => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      return updateSitePartnerReferral(site.id, {
        stop: true,
        reason: stopReason.trim(),
      })
    },
    onSuccess: () => {
      setStopDialogOpen(false)
      setStopReason('')
      toast.success(t('Partner referral payout stopped'))
      void queryClient.invalidateQueries({
        queryKey: siteAdminKeys.site(site.id),
      })
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (err) => handleServerError(err),
  })

  let referrerLabel = t('None')
  if (hasReferrer) {
    referrerLabel = site.referrer_site_subdomain
      ? `${site.referrer_site_subdomain} (#${site.referrer_site_id})`
      : `#${site.referrer_site_id}`
  }

  let commissionRateLabel = '—'
  if (hasReferrer) {
    commissionRateLabel =
      site.referrer_rate != null && site.referrer_rate !== ''
        ? `${site.referrer_rate}%`
        : t('Default')
  }

  return (
    <SettingsSection
      title={t('Partner Referral')}
      description={t(
        'Opaque partner code owned by this site and its upstream partner referrer attribution.'
      )}
      bodyClassName='flex flex-col gap-5'
    >
      <dl className='grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-4'>
        <div className='flex flex-col gap-1'>
          <dt className='text-muted-foreground text-xs'>
            {t('Site partner code')}
          </dt>
          <dd className='flex items-center gap-1.5 font-mono text-sm'>
            <span>{site.partner_code || '—'}</span>
            {site.partner_code && (
              <CopyButton
                value={site.partner_code}
                size='icon'
                variant='outline'
                className='size-6'
                aria-label={t('Copy partner code')}
              />
            )}
          </dd>
        </div>

        <div className='flex flex-col gap-1'>
          <dt className='text-muted-foreground text-xs'>
            {t('Bound referrer')}
          </dt>
          <dd className='flex flex-wrap items-center gap-1.5 font-medium'>
            <span>{referrerLabel}</span>
            {site.referrer_late_bound && (
              <Badge variant='secondary' className='text-[11px]'>
                {t('Late-bound')}
              </Badge>
            )}
          </dd>
        </div>

        <div className='flex flex-col gap-1'>
          <dt className='text-muted-foreground text-xs'>
            {t('Commission rate')}
          </dt>
          <dd className='font-medium tabular-nums'>{commissionRateLabel}</dd>
        </div>

        <div className='flex flex-col gap-1'>
          <dt className='text-muted-foreground text-xs'>
            {t('Bound at')}
          </dt>
          <dd className='tabular-nums'>
            {site.referrer_bound_at && site.referrer_bound_at > 0
              ? formatTimestamp(site.referrer_bound_at)
              : '—'}
          </dd>
        </div>
      </dl>

      {isStopped && (
        <div className='border-destructive/30 bg-destructive/5 flex flex-col gap-1 rounded-lg border p-3 text-xs'>
          <div className='flex flex-wrap items-center gap-2'>
            <Badge variant='destructive'>{t('Referral payout stopped')}</Badge>
            <span className='text-muted-foreground tabular-nums'>
              {formatTimestamp(site.referrer_stopped_at ?? 0)}
            </span>
          </div>
          <p className='text-muted-foreground break-words'>
            {t('Reason')}:{' '}
            {partnerStopReasonLabel(site.referrer_stop_reason, t)}
          </p>
        </div>
      )}

      {canLateBind && (
        <form
          className='border-border flex flex-col gap-3 border-t pt-4'
          onSubmit={(event) => {
            event.preventDefault()
            if (
              bindMutation.isPending ||
              (bindCode.trim() === '' && parsedBindSiteId === null) ||
              bindSiteIdError !== '' ||
              bindRateError !== ''
            ) {
              return
            }
            bindMutation.mutate()
          }}
        >
          <h4 className='text-sm font-medium'>
            {t('Late-Bind Referrer (7-day window)')}
          </h4>
          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor={`${fieldId}-bind-code`}>
                {t('Referrer partner code')}
              </Label>
              <Input
                id={`${fieldId}-bind-code`}
                value={bindCode}
                placeholder='PTN...'
                autoComplete='off'
                onChange={(event) => setBindCode(event.target.value)}
              />
            </div>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor={`${fieldId}-bind-site-id`}>
                {t('Referrer site ID')}
              </Label>
              <Input
                id={`${fieldId}-bind-site-id`}
                type='number'
                min={1}
                step={1}
                value={bindSiteId}
                placeholder='#'
                aria-invalid={bindSiteIdError !== ''}
                onChange={(event) => setBindSiteId(event.target.value)}
              />
              {bindSiteIdError && (
                <p className='text-destructive text-xs'>{bindSiteIdError}</p>
              )}
            </div>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor={`${fieldId}-bind-rate`}>
                {t('Custom referral rate (%)')}
              </Label>
              <Input
                id={`${fieldId}-bind-rate`}
                type='number'
                min={0}
                max={50}
                step='any'
                inputMode='decimal'
                value={bindRate}
                placeholder={t('Default')}
                aria-invalid={bindRateError !== ''}
                onChange={(event) => setBindRate(event.target.value)}
              />
              {bindRateError && (
                <p className='text-destructive text-xs'>{bindRateError}</p>
              )}
            </div>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor={`${fieldId}-bind-reason`}>
                {t('Reason (optional)')}
              </Label>
              <Input
                id={`${fieldId}-bind-reason`}
                value={bindReason}
                placeholder={t('Optional audit note')}
                onChange={(event) => setBindReason(event.target.value)}
              />
            </div>
          </div>
          <div>
            <Button
              type='submit'
              size='sm'
              variant='brand'
              disabled={
                bindMutation.isPending ||
                (bindCode.trim() === '' && parsedBindSiteId === null) ||
                bindSiteIdError !== '' ||
                bindRateError !== ''
              }
            >
              {t('Late-Bind Referrer')}
            </Button>
          </div>
        </form>
      )}

      {(canUpdateRate || canStopReferral) && (
        <div className='border-border flex flex-col gap-4 border-t pt-4'>
          {canUpdateRate && (
            <form
              className='flex flex-wrap items-end gap-3'
              onSubmit={(event) => {
                event.preventDefault()
                if (
                  rateMutation.isPending ||
                  rateValue.trim() === '' ||
                  updateRateError !== ''
                ) {
                  return
                }
                rateMutation.mutate()
              }}
            >
              <div className='flex w-44 flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-update-rate`}>
                  {t('Update Rate (%)')}
                </Label>
                <Input
                  id={`${fieldId}-update-rate`}
                  type='number'
                  min={0}
                  max={50}
                  step='any'
                  inputMode='decimal'
                  value={rateValue}
                  aria-invalid={updateRateError !== ''}
                  onChange={(event) => setRateValue(event.target.value)}
                />
                {updateRateError && (
                  <p className='text-destructive text-xs'>{updateRateError}</p>
                )}
              </div>
              <div className='flex min-w-52 flex-1 flex-col gap-1.5'>
                <Label htmlFor={`${fieldId}-rate-reason`}>
                  {t('Reason (optional)')}
                </Label>
                <Input
                  id={`${fieldId}-rate-reason`}
                  value={rateReason}
                  placeholder={t('Optional audit note')}
                  onChange={(event) => setRateReason(event.target.value)}
                />
              </div>
              <Button
                type='submit'
                size='sm'
                variant='brand'
                disabled={
                  rateMutation.isPending ||
                  rateValue.trim() === '' ||
                  updateRateError !== ''
                }
              >
                {t('Update Rate')}
              </Button>
            </form>
          )}

          {canStopReferral && (
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Stopping referral payout permanently halts future partner commissions for this site.'
                )}
              </p>
              <Button
                type='button'
                size='sm'
                variant='destructive'
                disabled={stopMutation.isPending}
                onClick={() => {
                  setStopReason('')
                  setStopDialogOpen(true)
                }}
              >
                {t('Stop Referral Payout')}
              </Button>
            </div>
          )}
        </div>
      )}

      {isRoot && (
        <Dialog
          open={stopDialogOpen}
          onOpenChange={(open) => {
            if (!open && !stopMutation.isPending) {
              setStopDialogOpen(false)
              setStopReason('')
            }
          }}
          title={t('Stop Referral Payout')}
          description={t(
            'This permanently stops partner referral commissions from this site. Already settled commissions are kept.'
          )}
          contentClassName='sm:max-w-lg'
          footer={
            <div className='flex flex-wrap justify-end gap-2'>
              <Button
                type='button'
                variant='outline'
                disabled={stopMutation.isPending}
                onClick={() => {
                  setStopDialogOpen(false)
                  setStopReason('')
                }}
              >
                {t('Cancel')}
              </Button>
              <Button
                type='button'
                variant='destructive'
                disabled={stopMutation.isPending || stopReason.trim() === ''}
                onClick={() => stopMutation.mutate()}
              >
                {t('Confirm stop payout')}
              </Button>
            </div>
          }
        >
          <div className='flex flex-col gap-2'>
            <Label htmlFor={`${fieldId}-stop-reason`}>
              {t('Reason')} *
            </Label>
            <Textarea
              id={`${fieldId}-stop-reason`}
              rows={3}
              value={stopReason}
              placeholder={t('Enter the reason for stopping referral payout')}
              onChange={(event) => setStopReason(event.target.value)}
            />
          </div>
        </Dialog>
      )}
    </SettingsSection>
  )
}

/** Dedicated detail and edit page for a single reseller subsite. */
export function SiteDetailPage(props: { siteId: number }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const location = useLocation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const fieldId = useId()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN

  const requestedTab = useMemo(() => {
    const tabParam = new URLSearchParams(location.searchStr).get('tab')
    return resolveSiteDetailTab(tabParam, isRoot)
  }, [location.searchStr, isRoot])
  const [activeTab, setActiveTab] = useState<SiteDetailTab>(requestedTab)

  useEffect(() => {
    setActiveTab(requestedTab)
  }, [requestedTab])

  const [closing, setClosing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [deleteReason, setDeleteReason] = useState('')
  const [quickMarginOpen, setQuickMarginOpen] = useState(false)

  const query = useQuery({
    queryKey: siteAdminKeys.site(props.siteId),
    queryFn: () => getSite(props.siteId),
  })
  const statsQuery = useQuery({
    queryKey: siteAdminKeys.list('stats', { id: props.siteId }),
    queryFn: () => getSiteStats(props.siteId),
  })

  const removeMutation = useMutation({
    mutationFn: () => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      return deleteSite(props.siteId, deleteReason.trim() || undefined)
    },
    onSuccess: (result) => {
      setConfirmDelete(false)
      setDeleteReason('')
      toast.success(
        result.mode === 'hard_deleted'
          ? t('Site permanently deleted')
          : t('Closed site archived and hidden')
      )
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
      void invalidateSidebarBadges(queryClient)
      void navigate({
        to: '/site-admin/$section',
        params: { section: 'sites' },
      })
    },
    onError: (err) => handleServerError(err),
  })

  const site = query.data?.site
  const domains = query.data?.domains ?? []
  const senders = query.data?.sender_domains ?? []
  const audits = query.data?.audits ?? []
  const brand = parseSiteBrand(site?.brand)
  const displayHost = site
    ? site.host || `${site.subdomain}.${site.root_domain}`
    : `#${props.siteId}`

  return (
    <DraftNavigationProvider>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          <span className='inline-flex flex-wrap items-center gap-2.5'>
            <span className='font-mono'>{displayHost}</span>
            {site && <SiteStatusBadge status={site.status} />}
          </span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {brand.name
            ? `${brand.name} · ${t('Site')} #${props.siteId}`
            : `${t('Site')} #${props.siteId}`}
        </SectionPageLayout.Description>
        <SectionPageLayout.Content>
          <div className='flex flex-col gap-5'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() =>
                  void navigate({
                    to: '/site-admin/$section',
                    params: { section: 'sites' },
                  })
                }
              >
                <ArrowLeft className='size-4' aria-hidden />
                {t('Back to sites')}
              </Button>
            </div>

            {query.isError && (
              <ErrorState onRetry={() => void query.refetch()} />
            )}
            {query.isPending && <LoadingState />}

            {site && (
              <>
                <dl className='border-border bg-border grid grid-cols-2 gap-px overflow-hidden rounded-[14px] border lg:grid-cols-5'>
                  <div className='bg-card flex flex-col gap-1.5 px-4 py-3.5'>
                    <dt className='text-dim text-[12px] leading-none'>
                      {t('Owner')}
                    </dt>
                    <dd className='truncate text-sm font-semibold'>
                      {formatUserIdentity(
                        site.owner_user_id,
                        site.owner_username
                      )}
                    </dd>
                  </div>
                  <div className='bg-card flex flex-col gap-1.5 px-4 py-3.5'>
                    <dt className='text-dim text-[12px] leading-none'>
                      {t('Customers')}
                    </dt>
                    <dd className='font-display text-[20px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
                      {formatNumber(site.customers, locale)}
                    </dd>
                  </div>
                  <div className='bg-card flex flex-col gap-1.5 px-4 py-3.5'>
                    <dt className='text-dim text-[12px] leading-none'>
                      {t('Effective cost factor')}
                    </dt>
                    <dd className='font-display text-[20px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
                      {site.effective_cost_factor ||
                        site.cost_factor ||
                        site.tier_cost_factor ||
                        t('Follow site level')}
                    </dd>
                  </div>
                  <div className='bg-card flex flex-col gap-1.5 px-4 py-3.5'>
                    <dt className='text-dim text-[12px] leading-none'>
                      {t('Site level')}
                    </dt>
                    <dd className='truncate text-sm font-semibold'>
                      {site.tier_code
                        ? `${site.tier_code} (${site.tier_mode === 'manual' ? t('Pinned') : t('Automatic')})`
                        : t('Automatic')}
                    </dd>
                  </div>
                  <div className='bg-card col-span-2 flex flex-col gap-1.5 px-4 py-3.5 lg:col-span-1'>
                    <dt className='text-dim text-[12px] leading-none'>
                      {site.close_execute_at > 0 ? t('Closes on') : t('Opened')}
                    </dt>
                    <dd className='text-sm font-semibold tabular-nums'>
                      {site.close_execute_at > 0
                        ? formatTimestampToDate(site.close_execute_at)
                        : formatTimestampToDate(site.created_at)}
                    </dd>
                  </div>
                </dl>

                <Tabs
                  value={activeTab}
                  onValueChange={(value) =>
                    setActiveTab(resolveSiteDetailTab(value, isRoot))
                  }
                  className='flex flex-col gap-4'
                >
                  <TabsList
                    variant='line'
                    className='border-border h-auto flex-wrap justify-start gap-x-6 gap-y-1 rounded-none border-b p-0 [--tab-underline:var(--brand)] group-data-horizontal/tabs:h-auto'
                  >
                    <TabsTrigger
                      value='basic'
                      className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                    >
                      {t('Basic & Brand')}
                    </TabsTrigger>
                    <TabsTrigger
                      value='domains'
                      className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                    >
                      {t('Domains & Access')}
                    </TabsTrigger>
                    <TabsTrigger
                      value='business'
                      className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                    >
                      {t('Business data')}
                    </TabsTrigger>
                    {isRoot && (
                      <TabsTrigger
                        value='tier'
                        className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                      >
                        {t('Level & Supply')}
                      </TabsTrigger>
                    )}
                    {isRoot && (
                      <TabsTrigger
                        value='wallets'
                        className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                      >
                        {t('Wallets & Finance')}
                      </TabsTrigger>
                    )}
                    <TabsTrigger
                      value='customers'
                      className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                    >
                      {t('Customers & Risk')}
                    </TabsTrigger>
                    {isRoot && (
                      <TabsTrigger
                        value='danger'
                        className='text-dim hover:text-foreground data-active:text-destructive h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                      >
                        {t('Danger Zone')}
                      </TabsTrigger>
                    )}
                  </TabsList>

                  <TabsContent
                    value='basic'
                    className='flex flex-col gap-6 pt-2'
                  >
                    <SiteBasicBrandForm key={site.id} site={site} />
                    <SitePartnerReferralCard
                      key={`partner-${site.id}`}
                      site={site}
                      isRoot={isRoot}
                    />
                  </TabsContent>

                  <TabsContent value='domains' className='pt-2'>
                    <SiteDomainsCard
                      site={site}
                      siteStatus={site.status}
                      domains={domains}
                      senders={senders}
                    />
                  </TabsContent>

                  <TabsContent
                    value='business'
                    className='flex flex-col gap-8 pt-2'
                  >
                    <SiteBusinessStatsCard
                      data={statsQuery.data}
                      isPending={statsQuery.isPending}
                      isError={statsQuery.isError}
                      onRetry={() => void statsQuery.refetch()}
                    />
                  </TabsContent>

                  {isRoot && (
                    <TabsContent
                      value='tier'
                      className='flex flex-col gap-8 pt-2'
                    >
                      <SiteTierControl
                        key={`${site.id}:${site.tier_mode}:${site.tier_code}`}
                        site={site}
                      />
                      <SiteSupplyDiscountCard
                        key={`${site.id}:${site.cost_factor ?? ''}`}
                        site={site}
                      />
                      <SiteMarginPoliciesCard
                        siteId={site.id}
                        siteStatus={site.status}
                      />
                    </TabsContent>
                  )}

                  {isRoot && (
                    <TabsContent
                      value='wallets'
                      className='flex flex-col gap-8 pt-2'
                    >
                      <SiteFundPanel
                        siteId={site.id}
                        siteStatus={site.status}
                        actions={
                          <Button
                            type='button'
                            size='sm'
                            variant='brand'
                            disabled={site.status !== 'active'}
                            onClick={() => setQuickMarginOpen(true)}
                          >
                            <Ticket className='size-3.5' aria-hidden='true' />
                            {t('Issue margin recharge code')}
                          </Button>
                        }
                      />
                    </TabsContent>
                  )}

                  <TabsContent
                    value='customers'
                    className='flex flex-col gap-8 pt-2'
                  >
                    <SiteCustomersCard siteId={site.id} />
                    {isRoot &&
                      (site.status === 'closing' ||
                        site.status === 'closed') && (
                        <CloseMailCard key={site.id} siteId={site.id} />
                      )}
                    <SiteAuditCard audits={audits} />
                  </TabsContent>

                  {isRoot && (
                    <TabsContent
                      value='danger'
                      className='flex flex-col gap-6 pt-2'
                    >
                      {site.status !== 'closed' &&
                        site.status !== 'deleting' && (
                          <SettingsSection
                            className='border-warning/40'
                            title={
                              site.status === 'closing'
                                ? t('Reschedule closure')
                                : t('Close site')
                            }
                            description={t(
                              'Closing a site migrates its customers to the main site, stops API domains, and preserves settled earnings.'
                            )}
                          >
                            <Button
                              type='button'
                              variant='destructive'
                              onClick={() => setClosing(true)}
                            >
                              <AlertTriangle className='size-4' aria-hidden />
                              {site.status === 'closing'
                                ? t('Reschedule closure')
                                : t('Close site')}
                            </Button>
                          </SettingsSection>
                        )}

                      <SettingsSection
                        className='border-destructive/40'
                        title={t('Delete site')}
                        titleProps={{ className: 'text-destructive' }}
                        description={t(
                          'Zero-data sites are permanently deleted. Already-closed sites are archived and hidden from the default list. Active sites with customers or financial records must be closed first.'
                        )}
                      >
                        <div className='flex flex-col gap-4'>
                          <div className='max-w-lg space-y-1.5'>
                            <Label htmlFor={`${fieldId}-delete-reason`}>
                              {t('Reason (optional)')}
                            </Label>
                            <Textarea
                              id={`${fieldId}-delete-reason`}
                              rows={2}
                              value={deleteReason}
                              placeholder={t(
                                'Optional audit note for deleting this site'
                              )}
                              onChange={(event) =>
                                setDeleteReason(event.target.value)
                              }
                            />
                          </div>
                          <div>
                            <Button
                              type='button'
                              variant='destructive'
                              disabled={removeMutation.isPending}
                              onClick={() => setConfirmDelete(true)}
                            >
                              <Trash2 className='size-4' aria-hidden />
                              {t('Delete site')}
                            </Button>
                          </div>
                        </div>
                      </SettingsSection>
                    </TabsContent>
                  )}
                </Tabs>

                {isRoot && (
                  <>
                    <QuickMarginVoucherDialog
                      siteId={site.id}
                      siteLabel={
                        brand.name
                          ? `${brand.name} · ${displayHost}`
                          : displayHost
                      }
                      ownerLabel={formatUserIdentity(
                        site.owner_user_id,
                        site.owner_username
                      )}
                      open={quickMarginOpen}
                      onOpenChange={setQuickMarginOpen}
                    />
                    <CloseSiteDialog
                      domains={domains}
                      site={site}
                      open={closing}
                      onOpenChange={setClosing}
                    />
                    <ConfirmDialog
                      open={confirmDelete}
                      onOpenChange={(open) => {
                        if (!removeMutation.isPending) setConfirmDelete(open)
                      }}
                      title={t('Delete {{site}}', { site: displayHost })}
                      desc={t(
                        'Zero-data sites will be permanently deleted, and closed sites will be archived and hidden. Active sites with customers or financial records cannot be deleted until closed.'
                      )}
                      confirmText={t('Delete site')}
                      destructive
                      isLoading={removeMutation.isPending}
                      handleConfirm={() => removeMutation.mutate()}
                    />
                  </>
                )}
              </>
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    </DraftNavigationProvider>
  )
}
