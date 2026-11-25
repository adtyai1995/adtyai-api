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
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { QuickMarginVoucherDialog } from '@/features/adty-voucher/quick-margin-voucher-dialog'
import { DraftNavigationProvider } from '@/features/system-settings/components/draft-navigation-provider'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { deleteSite, getSite, getSiteStats, siteAdminKeys } from '../api'
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
                <dl className='grid gap-3 sm:grid-cols-2 lg:grid-cols-5'>
                  <div className='bg-card rounded-xl border p-3.5'>
                    <dt className='text-muted-foreground text-xs'>
                      {t('Owner')}
                    </dt>
                    <dd className='mt-1 truncate text-sm font-medium'>
                      {formatUserIdentity(
                        site.owner_user_id,
                        site.owner_username
                      )}
                    </dd>
                  </div>
                  <div className='bg-card rounded-xl border p-3.5'>
                    <dt className='text-muted-foreground text-xs'>
                      {t('Customers')}
                    </dt>
                    <dd className='mt-1 text-sm font-semibold tabular-nums'>
                      {formatNumber(site.customers, locale)}
                    </dd>
                  </div>
                  <div className='bg-card rounded-xl border p-3.5'>
                    <dt className='text-muted-foreground text-xs'>
                      {t('Effective cost factor')}
                    </dt>
                    <dd className='mt-1 text-sm font-semibold tabular-nums'>
                      {site.effective_cost_factor ||
                        site.cost_factor ||
                        site.tier_cost_factor ||
                        t('Follow site level')}
                    </dd>
                  </div>
                  <div className='bg-card rounded-xl border p-3.5'>
                    <dt className='text-muted-foreground text-xs'>
                      {t('Site level')}
                    </dt>
                    <dd className='mt-1 text-sm font-medium'>
                      {site.tier_code
                        ? `${site.tier_code} (${site.tier_mode === 'manual' ? t('Pinned') : t('Automatic')})`
                        : t('Automatic')}
                    </dd>
                  </div>
                  <div className='bg-card rounded-xl border p-3.5'>
                    <dt className='text-muted-foreground text-xs'>
                      {site.close_execute_at > 0
                        ? t('Closes on')
                        : t('Opened')}
                    </dt>
                    <dd className='mt-1 text-sm font-medium tabular-nums'>
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

                  <TabsContent value='basic'>
                    <SiteBasicBrandForm key={site.id} site={site} />
                  </TabsContent>

                  <TabsContent value='domains'>
                    <SiteDomainsCard
                      site={site}
                      siteStatus={site.status}
                      domains={domains}
                      senders={senders}
                    />
                  </TabsContent>

                  <TabsContent value='business' className='flex flex-col gap-5'>
                    <SiteBusinessStatsCard
                      data={statsQuery.data}
                      isPending={statsQuery.isPending}
                      isError={statsQuery.isError}
                      onRetry={() => void statsQuery.refetch()}
                    />
                  </TabsContent>

                  {isRoot && (
                    <TabsContent value='tier' className='flex flex-col gap-5'>
                      <Card>
                        <CardHeader>
                          <CardTitle>{t('Level & Supply')}</CardTitle>
                        </CardHeader>
                        <CardContent>
                          <SiteTierControl
                            key={`${site.id}:${site.tier_mode}:${site.tier_code}`}
                            site={site}
                          />
                        </CardContent>
                      </Card>
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
                      className='flex flex-col gap-5'
                    >
                      <Card>
                        <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-2 space-y-0'>
                          <CardTitle>{t('Wallets & Finance')}</CardTitle>
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
                        </CardHeader>
                        <CardContent>
                          <SiteFundPanel
                            siteId={site.id}
                            siteStatus={site.status}
                          />
                        </CardContent>
                      </Card>
                    </TabsContent>
                  )}

                  <TabsContent
                    value='customers'
                    className='flex flex-col gap-5'
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
                    <TabsContent value='danger' className='flex flex-col gap-5'>
                      {site.status !== 'closed' &&
                        site.status !== 'deleting' && (
                        <Card className='border-warning/40'>
                          <CardHeader>
                            <CardTitle className='flex items-center gap-2'>
                              <AlertTriangle
                                className='text-warning size-4'
                                aria-hidden
                              />
                              {site.status === 'closing'
                                ? t('Reschedule closure')
                                : t('Close site')}
                            </CardTitle>
                            <CardDescription>
                              {t(
                                'Closing a site migrates its customers to the main site, stops API domains, and preserves settled earnings.'
                              )}
                            </CardDescription>
                          </CardHeader>
                          <CardContent>
                            <Button
                              type='button'
                              variant='destructive'
                              onClick={() => setClosing(true)}
                            >
                              {site.status === 'closing'
                                ? t('Reschedule closure')
                                : t('Close site')}
                            </Button>
                          </CardContent>
                        </Card>
                      )}

                      <Card className='border-destructive/40'>
                        <CardHeader>
                          <CardTitle className='text-destructive flex items-center gap-2'>
                            <Trash2 className='size-4' aria-hidden />
                            {t('Delete site')}
                          </CardTitle>
                          <CardDescription>
                            {t(
                              'Zero-data sites are permanently deleted. Already-closed sites are archived and hidden from the default list. Active sites with customers or financial records must be closed first.'
                            )}
                          </CardDescription>
                        </CardHeader>
                        <CardContent className='flex flex-col gap-3'>
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
                        </CardContent>
                      </Card>
                    </TabsContent>
                  )}
                </Tabs>

                {isRoot && (
                  <>
                    <QuickMarginVoucherDialog
                      siteId={site.id}
                      siteLabel={
                        brand.name ? `${brand.name} · ${displayHost}` : displayHost
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
