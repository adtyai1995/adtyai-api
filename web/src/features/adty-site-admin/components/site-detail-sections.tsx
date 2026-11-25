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
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  adjustSite,
  disableSiteDomain,
  disableSiteSenderDomain,
  getAdminSiteBonusMarginFill,
  getAdminSiteCommissionMargin,
  getCloseMail,
  type getSite,
  getSiteCustomers,
  resolveCloseMail,
  retryDomainAutomation,
  saveAdminSiteBonusMarginFill,
  saveAdminSiteCommissionMargin,
  SITE_ADMIN_PAGE_SIZE,
  siteAdminKeys,
  type AdminSenderDomain,
  type AdminSiteDomain,
  type CloseMailRecipient,
} from '../api'
import type { AdminSite, SiteAudit, SiteStatsResponse } from '../types'
import { SwitchRow } from './setting-fields'

function closeMailBatchStatusLabel(
  status: string,
  t: (key: string) => string
): string {
  switch (status) {
    case 'active':
      return t('Active')
    case 'queued':
      return t('Queued')
    case 'sending':
    case 'running':
      return t('Sending')
    case 'complete':
    case 'completed':
    case 'done':
      return t('Completed')
    case 'retryable':
    case 'retry':
    case 'retrying':
      return t('Retrying')
    case 'failed':
      return t('Failed')
    case 'superseded':
      return t('Superseded')
    case 'none':
    case '':
      return '-'
    default:
      return status
  }
}

function closeMailRecipientStatusLabel(
  status: string,
  t: (key: string) => string
): string {
  switch (status) {
    case 'pending':
    case 'queued':
      return t('Pending')
    case 'sending':
      return t('Sending')
    case 'success':
    case 'sent':
    case 'delivered':
    case 'confirmed':
      return t('Delivered')
    case 'failed':
      return t('Failed')
    case 'review':
      return t('Needs review')
    case 'skipped':
      return t('Skipped')
    default:
      return status
  }
}

function adminDomainStatusLabel(
  status: string,
  t: (key: string) => string
): string {
  switch (status) {
    case 'active':
      return t('Active')
    case 'pending':
      return t('Waiting for DNS')
    case 'failed':
      return t('Failed')
    case 'removed':
      return t('Removed')
    case 'disabled':
      return t('Disabled')
    default:
      return status
  }
}

function adminDomainSslLabel(
  status: string,
  t: (key: string) => string
): string {
  switch (status) {
    case 'active':
    case 'ready':
      return t('Active')
    case 'pending':
    case 'pending_validation':
    case 'validating':
      return t('Pending')
    case 'failed':
      return t('Failed')
    case 'none':
    case '':
      return '-'
    default:
      return status
  }
}

function adminDomainAutomationLabel(
  state: string,
  t: (key: string) => string
): string {
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

function adminSenderStatusLabel(
  status: string,
  t: (key: string) => string
): string {
  switch (status) {
    case 'verified':
      return t('Verified')
    case 'pending':
      return t('Waiting for DNS')
    case 'failed':
      return t('Verification failed')
    case 'disabled':
      return t('Turned off')
    case 'removing':
      return t('Removing')
    default:
      return status
  }
}

function siteAuditActionLabel(
  action: string,
  t: (key: string) => string
): string {
  switch (action) {
    case 'open':
      return t('Open site')
    case 'approve':
      return t('Approve')
    case 'reject':
      return t('Reject')
    case 'pause':
      return t('Pause')
    case 'resume':
      return t('Resume')
    case 'adjust':
      return t('Adjust')
    case 'paused_repair':
      return t('Paused repair')
    case 'close_request':
      return t('Request to close this site')
    case 'close_schedule':
      return t('Schedule close')
    case 'close_reschedule':
      return t('Reschedule close')
    case 'close':
      return t('Close')
    case 'delete':
      return t('Delete site')
    case 'notice_takedown':
      return t('Take down notice')
    case 'customer_review':
      return t('Review this customer')
    case 'customer_terms_recover':
      return t('Recover customer terms')
    case 'earning_review':
      return t('Review earnings')
    case 'margin.root_credit':
      return t('Root direct margin credit')
    case 'customer.balance_credit':
      return t('Manual customer balance credit')
    case 'team.balance_credit':
      return t('Manual team balance credit')
    case 'tier.principal_adjust':
      return t('Tier principal adjustment')
    case 'settlement_resume':
      return t('Resume settlement')
    case 'close_mail_retry':
      return t('Retry')
    case 'close_mail_delivered':
      return t('Confirm delivered')
    case 'report_resolved':
      return t('Resolve report')
    case 'report_dismissed':
      return t('Dismiss report')
    case 'staff.create':
      return t('Create staff account')
    case 'staff.update':
      return t('Edit staff account')
    case 'staff.password':
      return t('Reset staff password')
    case 'staff.delete':
      return t('Delete staff account')
    case 'staff.customer.disable':
      return t('Disable customer')
    case 'staff.balance_request':
      return t('Request balance change')
    case 'staff.balance_review':
      return t('Review balance request')
    default:
      return action
  }
}

export function SiteDomainsCard(props: {
  site: AdminSite
  domains: AdminSiteDomain[]
  senders: AdminSenderDomain[]
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const canDelete = canAdmin(user, ADTY_PERMISSIONS.SITE_DELETE)
  const disableDomain = useMutation({
    mutationFn: (id: number) => {
      if (
        !canAdmin(
          useAuthStore.getState().auth.user,
          ADTY_PERMISSIONS.SITE_DELETE
        )
      ) {
        throw new Error(t('No permission to perform this action'))
      }
      return disableSiteDomain(id)
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  const disableSender = useMutation({
    mutationFn: () => {
      if (
        !canAdmin(
          useAuthStore.getState().auth.user,
          ADTY_PERMISSIONS.SITE_DELETE
        )
      ) {
        throw new Error(t('No permission to perform this action'))
      }
      return disableSiteSenderDomain(props.site.id)
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  const retryDomain = useMutation({
    mutationFn: retryDomainAutomation,
    onSuccess: () => {
      toast.success(t('Domain check queued'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })

  const defaultHost =
    props.site.host || `${props.site.subdomain}.${props.site.root_domain}`
  const apiHost =
    props.site.api_host ||
    props.domains.find((d) => d.role === 'base_url' && d.status !== 'removed')
      ?.hostname ||
    ''

  return (
    <div className='flex flex-col gap-4'>
      <Card>
        <CardHeader>
          <CardTitle>{t('Default access URLs')}</CardTitle>
          <CardDescription>
            {t(
              'Primary platform-allocated website and API addresses for this subsite.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <dl className='grid gap-4 text-sm sm:grid-cols-2'>
            <div className='flex flex-col gap-1.5 rounded-lg border p-3'>
              <dt className='text-muted-foreground text-xs'>
                {t('Website address')}
              </dt>
              <dd className='flex items-center justify-between gap-2 font-mono text-xs break-all'>
                <span>https://{defaultHost}</span>
                <CopyButton
                  value={`https://${defaultHost}`}
                  size='sm'
                  variant='outline'
                  aria-label={t('Copy link')}
                />
              </dd>
            </div>
            <div className='flex flex-col gap-1.5 rounded-lg border p-3'>
              <dt className='text-muted-foreground text-xs'>
                {t('API address')}
              </dt>
              <dd className='flex items-center justify-between gap-2 text-xs break-all'>
                {apiHost ? (
                  <>
                    <span className='font-mono'>https://{apiHost}</span>
                    <CopyButton
                      value={`https://${apiHost}`}
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
          </dl>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('Custom domains')}</CardTitle>
        </CardHeader>
        <CardContent className='flex flex-col gap-2.5'>
          {props.domains.length === 0 ? (
            <EmptyState className='min-h-20' title={t('No data')} />
          ) : (
            props.domains.map((domain) => (
              <div
                key={domain.id}
                className='flex flex-wrap items-center justify-between gap-2 rounded-lg border p-3'
              >
                <div className='min-w-0 flex-1 text-sm break-all'>
                  <div className='flex flex-wrap items-center gap-2'>
                    <span className='font-mono text-xs font-medium'>
                      {domain.hostname}
                    </span>
                    <span className='text-muted-foreground text-xs'>
                      {domain.role === 'base_url'
                        ? t('API address')
                        : t('Website')}{' '}
                      · {adminDomainStatusLabel(domain.status, t)} ·{' '}
                      {t('SSL')}: {adminDomainSslLabel(domain.ssl_status ?? '', t)}
                    </span>
                  </div>
                  {domain.automation_state && (
                    <p className='text-muted-foreground mt-0.5 text-xs'>
                      {adminDomainAutomationLabel(domain.automation_state, t)}
                    </p>
                  )}
                  {domain.last_error && (
                    <p className='text-destructive mt-0.5 text-xs'>
                      {domain.last_error}
                    </p>
                  )}
                </div>
                <div className='flex shrink-0 gap-1.5'>
                  {user?.role === ROLE.SUPER_ADMIN &&
                    domain.status !== 'removed' &&
                    domain.automation_state && (
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        disabled={retryDomain.isPending}
                        onClick={() => retryDomain.mutate(domain.id)}
                      >
                        {t('Retry')}
                      </Button>
                    )}
                  {canDelete &&
                    domain.status !== 'removed' &&
                    domain.managed_by !== 'platform' && (
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        disabled={disableDomain.isPending}
                        onClick={() => disableDomain.mutate(domain.id)}
                      >
                        {t('Disable')}
                      </Button>
                    )}
                </div>
              </div>
            ))
          )}
        </CardContent>
      </Card>

      {props.senders.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>{t('Sender domain')}</CardTitle>
            <CardDescription>
              {t('Legacy Resend sender domains retained for cleanup.')}
            </CardDescription>
          </CardHeader>
          <CardContent className='flex flex-col gap-2.5'>
            {props.senders.map((sender) => (
              <div
                key={sender.domain}
                className='flex flex-wrap items-center justify-between gap-2 rounded-lg border p-3'
              >
                <div className='text-sm break-all'>
                  <span className='font-mono text-xs font-medium'>
                    {sender.domain}
                  </span>{' '}
                  · {adminSenderStatusLabel(sender.status, t)}
                  {sender.cleanup_error && (
                    <span className='text-destructive block text-xs'>
                      {t('Cleanup failed')} · {sender.cleanup_error}
                    </span>
                  )}
                  {Boolean(sender.retry_at) && (
                    <span className='text-muted-foreground block text-xs'>
                      {t('Next retry')} ·{' '}
                      {formatTimestampToDate(sender.retry_at ?? 0)}
                    </span>
                  )}
                </div>
                {canDelete && (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    disabled={disableSender.isPending}
                    onClick={() => disableSender.mutate()}
                  >
                    {sender.status === 'removing' ? t('Retry') : t('Disable')}{' '}
                    {t('Sender domain')}
                  </Button>
                )}
              </div>
            ))}
          </CardContent>
        </Card>
      )}
    </div>
  )
}

export function SiteBusinessStatsCard(props: {
  data: SiteStatsResponse | undefined
  isPending: boolean
  isError: boolean
  onRetry: () => void
}) {
  const { t } = useTranslation()
  const totals = props.data?.stats.total
  const breakdown = props.data?.breakdown
  const payerLabels: Record<string, string> = {
    team: t('Team'),
    personal: t('Personal'),
  }
  const dimensions = breakdown
    ? [
        { label: t('Model'), rows: breakdown.by_model ?? [] },
        { label: t('Group'), rows: breakdown.by_group ?? [] },
        {
          label: t('Payer'),
          rows: (breakdown.by_payer ?? []).map((row) => ({
            ...row,
            key: payerLabels[row.key] ?? row.key,
          })),
        },
      ]
    : []
  const missing = breakdown
    ? [
        {
          label: t('Unverified payer for this site'),
          ...breakdown.unverified,
        },
        {
          label: t('Unknown site across all consumption logs'),
          ...breakdown.unknown_site,
        },
      ]
    : []

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Business data')}</CardTitle>
        {props.data && (
          <CardDescription>
            {props.data.stats.from} – {props.data.stats.to}
          </CardDescription>
        )}
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        {props.isError && <ErrorState onRetry={props.onRetry} />}
        {props.isPending && <LoadingState />}
        {props.data && (
          <>
            <dl className='grid gap-3 sm:grid-cols-4'>
              {[
                [t('Revenue'), `${totals?.revenue_usd ?? '0'} USD`],
                [t('Earnings'), `${totals?.earning_usd ?? '0'} USD`],
                [t('Top-up'), `${totals?.topup_usd ?? '0'} USD`],
                [
                  t('Withdrawn'),
                  props.data.withdrawn_usd
                    ? `${props.data.withdrawn_usd} USD`
                    : '-',
                ],
              ].map(([label, value]) => (
                <div key={label} className='bg-muted/40 rounded-lg border p-3'>
                  <dt className='text-muted-foreground text-xs'>{label}</dt>
                  <dd className='mt-1 text-base font-semibold tabular-nums'>
                    {value}
                  </dd>
                </div>
              ))}
            </dl>
            {dimensions.map(({ label, rows }) => (
              <div key={label} className='flex flex-col gap-2'>
                <h4 className='text-xs font-medium tracking-wide'>{label}</h4>
                <StaticDataTable
                  tableProps={{ 'aria-label': label }}
                  data={rows}
                  getRowKey={(row) => row.key}
                  emptyContent={
                    <EmptyState className='min-h-16' title={t('No data')} />
                  }
                  columns={[
                    { id: 'key', header: label, cell: (row) => row.key || '—' },
                    {
                      id: 'requests',
                      header: t('Requests'),
                      cell: (row) => (
                        <span className='tabular-nums'>{row.requests}</span>
                      ),
                    },
                    {
                      id: 'quota',
                      header: t('Quota'),
                      cell: (row) => (
                        <span className='tabular-nums'>{row.quota}</span>
                      ),
                    },
                  ]}
                />
              </div>
            ))}
            {missing
              .filter((row) => row.requests > 0)
              .map((row) => (
                <div key={row.label} className='text-muted-foreground text-sm'>
                  <span>{row.label}</span> · {t('Requests')} {row.requests} ·{' '}
                  {t('Quota')} {row.quota}
                </div>
              ))}
          </>
        )}
      </CardContent>
    </Card>
  )
}

/** Root-only per-site supply discount (cost_factor) editor, located in the Level & Supply tab. */
export function SiteSupplyDiscountCard(props: { site: AdminSite }) {
  const { t } = useTranslation()
  const fieldId = useId()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const isRoot = user?.role === ROLE.SUPER_ADMIN
  const canAdjust = canAdmin(user, ADTY_PERMISSIONS.SITE_ADJUST)
  const site = props.site

  const savedCostFactor = site.cost_factor ?? ''
  const [costFactor, setCostFactor] = useState(savedCostFactor)
  const [reason, setReason] = useState('')
  const previousSite = useRef(site)

  useEffect(() => {
    const prev = previousSite.current
    if (prev === site) return
    setCostFactor((current) =>
      current === (prev.cost_factor ?? '') ? (site.cost_factor ?? '') : current
    )
    previousSite.current = site
  }, [site])

  const trimmed = costFactor.trim()
  const costFactorChanged = trimmed !== savedCostFactor
  const parsedCostFactor = trimmed === '' ? null : Number(trimmed)
  const costFactorInvalid =
    parsedCostFactor !== null &&
    (!Number.isFinite(parsedCostFactor) || parsedCostFactor <= 0)
  const editable =
    canAdjust &&
    site.status !== 'closed' &&
    site.status !== 'closing' &&
    site.status !== 'deleting'

  const save = useMutation({
    mutationFn: () => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      const body: Parameters<typeof adjustSite>[1] = {
        reason: reason.trim(),
      }
      if (trimmed === '') {
        body.clear = ['cost_factor']
      } else {
        body.cost_factor = trimmed
      }
      return adjustSite(site.id, body)
    },
    onSuccess: (updated) => {
      setCostFactor(updated.cost_factor ?? '')
      setReason('')
      queryClient.setQueryData<Awaited<ReturnType<typeof getSite>>>(
        siteAdminKeys.site(site.id),
        (cached) => (cached ? { ...cached, site: updated } : cached)
      )
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (err) => handleServerError(err),
  })

  if (!isRoot) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Per-site supply discount')}</CardTitle>
        <CardDescription>
          {t(
            'Override the site level supply discount for this specific site. Leave empty to follow the site level supply discount.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className='flex max-w-xl flex-col gap-4'
          onSubmit={(event) => {
            event.preventDefault()
            if (!editable || !costFactorChanged || costFactorInvalid) return
            save.mutate()
          }}
        >
          <dl className='grid gap-3 text-sm sm:grid-cols-2'>
            <div className='bg-muted/40 rounded-lg border p-3'>
              <dt className='text-muted-foreground text-xs'>
                {t('Effective cost factor')}
              </dt>
              <dd className='mt-1 font-semibold tabular-nums'>
                {site.effective_cost_factor ||
                  site.cost_factor ||
                  site.tier_cost_factor ||
                  t('Follow site level')}
              </dd>
            </div>
            <div className='bg-muted/40 rounded-lg border p-3'>
              <dt className='text-muted-foreground text-xs'>
                {t('Site level')}
              </dt>
              <dd className='mt-1 font-medium tabular-nums'>
                {site.tier_cost_factor || t('Follow site level')}
              </dd>
            </div>
          </dl>

          <div className='space-y-1.5'>
            <Label htmlFor={`${fieldId}-cost-factor`}>{t('Cost factor')}</Label>
            <Input
              id={`${fieldId}-cost-factor`}
              inputMode='decimal'
              value={costFactor}
              placeholder={t('Follow site level')}
              disabled={!editable || save.isPending}
              aria-invalid={costFactorInvalid}
              onChange={(event) => setCostFactor(event.target.value)}
            />
            {costFactorInvalid ? (
              <p className='text-destructive text-xs'>
                {t('The cost factor must be greater than 0')}
              </p>
            ) : (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Leave empty to follow the site level supply discount. The owner is notified when it changes.'
                )}
              </p>
            )}
          </div>

          <div className='space-y-1.5'>
            <Label htmlFor={`${fieldId}-reason`}>
              {t('Change note (optional)')}
            </Label>
            <Textarea
              id={`${fieldId}-reason`}
              rows={2}
              disabled={!editable || save.isPending}
              value={reason}
              placeholder={t('Optional audit note for this update')}
              onChange={(event) => setReason(event.target.value)}
            />
          </div>

          {editable && (
            <div className='flex flex-wrap items-center gap-2'>
              <Button
                type='submit'
                variant='brand'
                disabled={
                  save.isPending || !costFactorChanged || costFactorInvalid
                }
              >
                {t('Save')}
              </Button>
              {(costFactorChanged || reason !== '') && (
                <Button
                  type='button'
                  variant='outline'
                  disabled={save.isPending}
                  onClick={() => {
                    setCostFactor(savedCostFactor)
                    setReason('')
                  }}
                >
                  {t('Reset')}
                </Button>
              )}
            </div>
          )}
        </form>
      </CardContent>
    </Card>
  )
}

/** Root-only margin coverage policy switches (top-up bonus margin-fill & commission margin-cover). */
export function SiteMarginPoliciesCard(props: { siteId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN

  const bonusMarginFill = useQuery({
    queryKey: siteAdminKeys.list('bonus-margin-fill', { siteId: props.siteId }),
    queryFn: () => getAdminSiteBonusMarginFill(props.siteId),
    enabled: isRoot,
  })
  const commissionMargin = useQuery({
    queryKey: siteAdminKeys.list('commission-margin', { siteId: props.siteId }),
    queryFn: () => getAdminSiteCommissionMargin(props.siteId),
    enabled: isRoot,
  })

  const saveBonusMarginFill = useMutation({
    mutationFn: async (enabled: boolean) => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      await saveAdminSiteBonusMarginFill(props.siteId, enabled)
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (err) => handleServerError(err),
  })

  const saveCommissionMargin = useMutation({
    mutationFn: async (allowed: boolean) => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      await saveAdminSiteCommissionMargin(props.siteId, allowed)
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (err) => handleServerError(err),
  })

  if (!isRoot) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Margin coverage policies')}</CardTitle>
        <CardDescription>
          {t(
            'Control whether this site may draw from its open margin cycle when available earnings are insufficient for top-up bonuses or referral commissions.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        {bonusMarginFill.isPending && <LoadingState />}
        {bonusMarginFill.isError && (
          <ErrorState onRetry={() => void bonusMarginFill.refetch()} />
        )}
        {bonusMarginFill.isSuccess && (
          <SwitchRow
            label={t('Allow this site to use margin to cover top-up bonuses')}
            hint={t(
              'When enabled, top-up bonuses can draw from the open margin cycle if available earnings are insufficient.'
            )}
            checked={bonusMarginFill.data.enabled === true}
            disabled={saveBonusMarginFill.isPending}
            onCheckedChange={(enabled) => saveBonusMarginFill.mutate(enabled)}
          />
        )}

        {commissionMargin.isPending && <LoadingState />}
        {commissionMargin.isError && (
          <ErrorState onRetry={() => void commissionMargin.refetch()} />
        )}
        {commissionMargin.isSuccess && (
          <SwitchRow
            label={t('Cover commission from margin')}
            hint={t(
              'After earnings, the open margin cycle can cover the rest. No amount is set here.'
            )}
            checked={commissionMargin.data?.allowed === true}
            disabled={saveCommissionMargin.isPending}
            onCheckedChange={(allowed) => saveCommissionMargin.mutate(allowed)}
          />
        )}
      </CardContent>
    </Card>
  )
}

export function SiteCustomersCard(props: { siteId: number }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: siteAdminKeys.list('customers', { id: props.siteId, page }),
    queryFn: () => getSiteCustomers(props.siteId, page),
  })
  const riskLabel = (flag: string) => {
    if (flag === 'self_use') return t('Possible self-use')
    if (flag === 'migration') return t('Possible main site migration')
    return '-'
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Customers')}</CardTitle>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {query.isPending ? (
          <LoadingState />
        ) : (
          <>
            <StaticDataTable
              tableClassName='min-w-[640px]'
              tableProps={{ 'aria-label': t('Customers') }}
              data={query.data?.items ?? []}
              getRowKey={(row) => String(row.id)}
              emptyContent={
                <EmptyState className='min-h-24' title={t('No data')} />
              }
              columns={[
                {
                  id: 'user',
                  header: t('User'),
                  cell: (row) => formatUserIdentity(row.id, row.username),
                },
                {
                  id: 'email',
                  header: t('Email'),
                  cell: (row) => row.email || '-',
                },
                {
                  id: 'quota',
                  header: t('Balance'),
                  cell: (row) => formatQuotaWithCurrency(row.quota),
                },
                {
                  id: 'used',
                  header: t('Used'),
                  cell: (row) => formatQuotaWithCurrency(row.used_quota),
                },
                {
                  id: 'risk',
                  header: t('Risk'),
                  cell: (row) => riskLabel(row.risk_flag),
                },
              ]}
            />
            <PageSwitcher
              page={page}
              total={query.data?.total ?? 0}
              pageSize={SITE_ADMIN_PAGE_SIZE}
              onPageChange={setPage}
            />
          </>
        )}
      </CardContent>
    </Card>
  )
}

export function CloseMailCard(props: { siteId: number }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [batch, setBatch] = useState(0)
  const [reason, setReason] = useState('')
  const query = useQuery({
    queryKey: siteAdminKeys.list('close-mail', {
      siteId: props.siteId,
      batch,
      page,
    }),
    queryFn: () => getCloseMail(props.siteId, batch, page),
    refetchInterval: 30000,
  })
  const resolve = useMutation({
    mutationFn: ({
      row,
      decision,
    }: {
      row: CloseMailRecipient
      decision: string
    }) => resolveCloseMail(props.siteId, row, decision, reason),
    onSuccess: () => {
      setReason('')
      void query.refetch()
    },
    onError: (error) => handleServerError(error),
  })

  if (query.isError) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  const data = query.data
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Closure notification delivery')}</CardTitle>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        {query.isPending && <LoadingState />}
        {data && (
          <>
            <div className='flex flex-wrap items-end gap-3'>
              <div className='flex flex-col gap-1'>
                <Label htmlFor={`close-mail-batch-${props.siteId}`}>
                  {t('Notification batch')}
                </Label>
                <NativeSelect
                  id={`close-mail-batch-${props.siteId}`}
                  value={String(data.batch_id)}
                  onChange={(event) => {
                    setBatch(Number(event.target.value))
                    setPage(1)
                  }}
                >
                  {data.batches.map((b) => (
                    <NativeSelectOption key={b.id} value={String(b.id)}>
                      #{b.id} ·{' '}
                      {b.execute_at > 0
                        ? formatTimestampToDate(b.execute_at)
                        : t('Unknown')}{' '}
                      · {closeMailBatchStatusLabel(b.retry_status, t)}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className='min-w-56 flex-1 space-y-1'>
                <Label htmlFor={`close-mail-reason-${props.siteId}`}>
                  {t('Reconciliation note')}
                </Label>
                <Input
                  id={`close-mail-reason-${props.siteId}`}
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                />
              </div>
            </div>
            {data.recipients.length === 0 ? (
              <EmptyState className='min-h-16' title={t('No data')} />
            ) : (
              <div className='flex flex-col gap-2'>
                {data.recipients.map((row) => (
                  <div
                    key={row.id}
                    className='flex flex-wrap items-center justify-between gap-2 rounded-md border p-2.5 text-sm'
                  >
                    <div>
                      <span>
                        {row.email} ·{' '}
                        {closeMailRecipientStatusLabel(row.status, t)} ·{' '}
                        {t('Attempts')} {row.attempt_count} {row.last_error}
                      </span>
                      {row.attempts && row.attempts.length > 0 && (
                        <details className='mt-1 text-xs'>
                          <summary>{t('Delivery attempts')}</summary>
                          <ul className='text-muted-foreground mt-1 space-y-0.5 pl-4'>
                            {row.attempts.map((attempt) => (
                              <li key={attempt.number}>
                                #{attempt.number} · {attempt.provider} ·{' '}
                                {closeMailRecipientStatusLabel(
                                  attempt.outcome,
                                  t
                                )}{' '}
                                · {formatTimestampToDate(attempt.started_at)}{' '}
                                {attempt.provider_message_id}
                              </li>
                            ))}
                          </ul>
                        </details>
                      )}
                    </div>
                    {(row.status === 'failed' || row.status === 'review') &&
                      data.batches.find((b) => b.id === data.batch_id)
                        ?.retry_status !== 'superseded' && (
                        <div className='flex gap-1.5'>
                          <Button
                            type='button'
                            size='sm'
                            variant='outline'
                            disabled={resolve.isPending || !reason.trim()}
                            onClick={() =>
                              resolve.mutate({ row, decision: 'retry' })
                            }
                          >
                            {t('Retry')}
                          </Button>
                          <Button
                            type='button'
                            size='sm'
                            variant='outline'
                            disabled={resolve.isPending || !reason.trim()}
                            onClick={() =>
                              resolve.mutate({ row, decision: 'delivered' })
                            }
                          >
                            {t('Confirm delivered')}
                          </Button>
                        </div>
                      )}
                  </div>
                ))}
              </div>
            )}
            <PageSwitcher
              page={page}
              pageSize={20}
              total={data.total}
              onPageChange={setPage}
            />
          </>
        )}
      </CardContent>
    </Card>
  )
}

export function SiteAuditCard(props: { audits: SiteAudit[] }) {
  const { t } = useTranslation()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Audit trail')}</CardTitle>
      </CardHeader>
      <CardContent>
        <StaticDataTable
          tableClassName='min-w-[600px]'
          tableProps={{ 'aria-label': t('Audit trail') }}
          data={props.audits}
          getRowKey={(row) => String(row.id)}
          emptyContent={<EmptyState className='min-h-24' title={t('No data')} />}
          columns={[
            {
              id: 'time',
              header: t('Time'),
              cell: (row) => formatTimestampToDate(row.created_at),
            },
            {
              id: 'action',
              header: t('Action'),
              cell: (row) => (
                <span className='text-xs'>
                  {siteAuditActionLabel(row.action, t)}
                </span>
              ),
            },
            {
              id: 'operator',
              header: t('Operator'),
              cell: (row) =>
                formatUserIdentity(row.operator_id, row.operator_username),
            },
            {
              id: 'change',
              header: t('Change'),
              cell: (row) => (
                <span className='font-mono text-xs break-all'>
                  {row.old_value || '-'} → {row.new_value || '-'}
                </span>
              ),
            },
            {
              id: 'reason',
              header: t('Reason'),
              cell: (row) => row.reason || '-',
            },
          ]}
        />
      </CardContent>
    </Card>
  )
}
