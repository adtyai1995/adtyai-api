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
import { useState } from 'react'
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
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { getSystemOptions } from '@/features/system-settings/api'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { api } from '@/lib/api'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  disableSiteDomain,
  disableSiteSenderDomain,
  getAdminBalanceRequests,
  getAdminSiteBonusMarginFill,
  getCloseMail,
  getSiteCustomers,
  resolveCloseMail,
  retryDomainAutomation,
  reviewAdminBalanceRequest,
  saveAdminSiteBonusMarginFill,
  SITE_ADMIN_PAGE_SIZE,
  siteAdminKeys,
  type AdminBalanceRequest,
  type AdminSenderDomain,
  type AdminSiteDomain,
  type CloseMailRecipient,
} from '../api'
import type { AdminSite, SiteAudit, SiteStatsResponse } from '../types'
import { SwitchRow } from './setting-fields'

function balanceRequestStatusLabel(
  status: AdminBalanceRequest['status'],
  t: (key: string) => string
) {
  if (status === 'pending') return t('Pending')
  if (status === 'approved') return t('Approved')
  return t('Rejected')
}

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

function balanceFundingSourceLabel(
  source: string,
  t: (key: string) => string
): string {
  switch (source) {
    case 'auto':
      return t('Automatic (earnings first, then margin)')
    case 'site_earnings':
    case 'earnings':
      return t('Earnings wallet')
    case 'site_margin':
    case 'margin':
      return t('Margin wallet')
    case 'platform':
      return t('Platform')
    case 'specified_batch':
      return t('Specified fund batch')
    default:
      return source
  }
}

function balanceBatchKindLabel(
  kind: string,
  t: (key: string) => string
): string {
  switch (kind) {
    case 'principal':
      return t('Principal')
    case 'bonus':
      return t('Bonus')
    case 'expiring_bonus':
      return t('Expiring gift')
    default:
      return kind
  }
}

function balanceBatchSourceLabel(
  source: string,
  t: (key: string) => string
): string {
  switch (source) {
    case 'legacy':
      return t('Legacy balance')
    case 'topup':
      return t('Top-up')
    case 'voucher':
      return t('Voucher')
    case 'manual':
      return t('Manual adjustment')
    case 'referral':
      return t('Referral')
    case 'gift':
      return t('Gift')
    case 'site_close':
      return t('Site closure transfer')
    default:
      return source
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

      <Card>
        <CardHeader>
          <CardTitle>{t('Sender domain')}</CardTitle>
        </CardHeader>
        <CardContent className='flex flex-col gap-2.5'>
          {props.senders.length === 0 ? (
            <EmptyState className='min-h-20' title={t('No data')} />
          ) : (
            props.senders.map((sender) => (
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
            ))
          )}
        </CardContent>
      </Card>
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
                <h4 className='text-xs font-medium tracking-wide uppercase'>
                  {label}
                </h4>
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

const SITE_BONUS_METHODS: ReadonlyArray<{
  method: 'alipay' | 'wxpay' | 'usdt'
  label: string
}> = [
  { method: 'alipay', label: 'Alipay' },
  { method: 'wxpay', label: 'WeChat Pay' },
  { method: 'usdt', label: 'USDT' },
]

function parseSiteMethodAuth(
  raw: string | undefined,
  siteId: number
): { valid: boolean; authorized: Set<string> } {
  if (!raw || raw.trim() === '') {
    return { valid: true, authorized: new Set() }
  }
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return { valid: false, authorized: new Set() }
    }
    for (const value of Object.values(parsed as Record<string, unknown>)) {
      if (
        value !== null &&
        (!Array.isArray(value) ||
          value.some((item) => typeof item !== 'string'))
      ) {
        return { valid: false, authorized: new Set() }
      }
    }
    const list = (parsed as Record<string, string[] | null>)[String(siteId)]
    return {
      valid: true,
      authorized: new Set(
        Array.isArray(list) ? list.map((item) => item.trim().toLowerCase()) : []
      ),
    }
  } catch {
    return { valid: false, authorized: new Set() }
  }
}

/** Root-only per-site payment-method gift authorization (ADTY-09-03). */
export function SiteBonusMethodAuth(props: { siteId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const options = useQuery({
    queryKey: ['system-options'],
    queryFn: async () => requireServerSuccess(await getSystemOptions()),
    enabled: isRoot,
  })
  const marginFill = useQuery({
    queryKey: siteAdminKeys.list('bonus-margin-fill', { siteId: props.siteId }),
    queryFn: () => getAdminSiteBonusMarginFill(props.siteId),
    enabled: isRoot,
  })
  const save = useMutation({
    mutationFn: async (input: {
      method: 'alipay' | 'wxpay' | 'usdt'
      allowed: boolean
    }) => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      const res = await api.put<{
        success: boolean
        message?: string
        data?: { site_id: number; allowed: boolean }
      }>(`/api/adty/admin/sites/${props.siteId}/bonus-methods`, input)
      return requireServerSuccess(res.data)
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: ['system-options'] })
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  const saveMarginFill = useMutation({
    mutationFn: async (enabled: boolean) => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      await saveAdminSiteBonusMarginFill(props.siteId, enabled)
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  if (!isRoot) return null
  const optionRows = Array.isArray(options.data?.data) ? options.data.data : []
  const rawAuth = optionRows.find(
    (item) => item.key === 'adty_topup_setting.site_method_auth'
  )?.value
  const auth = parseSiteMethodAuth(rawAuth, props.siteId)
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Bonuses')}</CardTitle>
        <CardDescription>
          {t(
            'Authorize which top-up payment methods this site may grant bonuses on.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        {options.isPending && <LoadingState />}
        {(options.isError || (options.isSuccess && !auth.valid)) && (
          <ErrorState onRetry={() => void options.refetch()} />
        )}
        {options.isSuccess && (
          <div className='flex flex-col gap-2'>
            {SITE_BONUS_METHODS.map((item) => (
              <SwitchRow
                key={item.method}
                label={t(item.label)}
                checked={auth.valid && auth.authorized.has(item.method)}
                disabled={!auth.valid || save.isPending}
                onCheckedChange={(allowed) =>
                  save.mutate({ method: item.method, allowed })
                }
              />
            ))}
          </div>
        )}
        {marginFill.isPending && <LoadingState />}
        {marginFill.isError && (
          <ErrorState onRetry={() => void marginFill.refetch()} />
        )}
        {marginFill.isSuccess && (
          <SwitchRow
            label={t('Allow this site to use margin to cover top-up bonuses')}
            hint={t(
              'When enabled, top-up bonuses can draw from the open margin cycle if available earnings are insufficient.'
            )}
            checked={marginFill.data.enabled === true}
            disabled={saveMarginFill.isPending}
            onCheckedChange={(enabled) => saveMarginFill.mutate(enabled)}
          />
        )}
      </CardContent>
    </Card>
  )
}

export function RootBalanceRequestsCard(props: { siteId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('pending')
  const list = useQuery({
    queryKey: siteAdminKeys.list('balance-requests', {
      siteId: props.siteId,
      page,
      status,
    }),
    queryFn: () => getAdminBalanceRequests(props.siteId, page, status),
    enabled: isRoot,
  })
  const [reviewNote, setReviewNote] = useState('')
  const review = useMutation({
    mutationFn: (input: { id: number; approve: boolean }) =>
      reviewAdminBalanceRequest(input.id, input.approve, reviewNote.trim()),
    onSuccess: () => {
      setReviewNote('')
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (error) => handleServerError(error),
  })
  if (!isRoot) return null
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Balance requests')}</CardTitle>
        <CardDescription>
          {t(
            'This request does not change the customer balance until the site owner or Root approves it.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        <div className='flex flex-wrap items-center gap-2'>
          <NativeSelect
            value={status}
            aria-label={t('Status')}
            onChange={(event) => {
              setStatus(event.target.value)
              setPage(1)
            }}
          >
            <NativeSelectOption value=''>{t('All statuses')}</NativeSelectOption>
            <NativeSelectOption value='pending'>
              {t('Pending')}
            </NativeSelectOption>
            <NativeSelectOption value='approved'>
              {t('Approved')}
            </NativeSelectOption>
            <NativeSelectOption value='rejected'>
              {t('Rejected')}
            </NativeSelectOption>
          </NativeSelect>
          <Input
            className='max-w-xs'
            value={reviewNote}
            placeholder={t('Optional review note')}
            aria-label={t('Optional review note')}
            onChange={(event) => setReviewNote(event.target.value)}
          />
        </div>
        {list.isPending && <LoadingState />}
        {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
        {list.isSuccess && (
          <StaticDataTable
            tableClassName='min-w-[780px]'
            tableProps={{ 'aria-label': t('Balance requests') }}
            data={list.data.items}
            getRowKey={(row) => String(row.id)}
            emptyContent={
              <EmptyState className='min-h-24' title={t('No data')} />
            }
            columns={[
              {
                id: 'customer',
                header: t('Customer ID'),
                cell: (row: AdminBalanceRequest) => (
                  <span className='tabular-nums'>{row.customer_user_id}</span>
                ),
              },
              {
                id: 'staff',
                header: t('User ID'),
                cell: (row: AdminBalanceRequest) => (
                  <span className='tabular-nums'>{row.staff_user_id}</span>
                ),
              },
              {
                id: 'direction',
                header: t('Direction'),
                cell: (row: AdminBalanceRequest) =>
                  row.direction === 'increase' ? t('Increase') : t('Decrease'),
              },
              {
                id: 'amount',
                header: t('Amount'),
                cell: (row: AdminBalanceRequest) => (
                  <span className='tabular-nums'>{row.amount_usd}</span>
                ),
              },
              {
                id: 'provenance',
                header: t('Provenance'),
                cell: (row: AdminBalanceRequest) => (
                  <div className='flex max-w-xs flex-col gap-0.5 text-xs'>
                    {row.reason && (
                      <span className='break-all'>{row.reason}</span>
                    )}
                    {row.funding_source && (
                      <span className='text-muted-foreground'>
                        {t('Funding source')}:{' '}
                        {balanceFundingSourceLabel(row.funding_source, t)}
                      </span>
                    )}
                    {row.order_ref && (
                      <span className='text-muted-foreground font-mono'>
                        {t('Order ref')}: {row.order_ref}
                      </span>
                    )}
                    {(row.batch_kind || row.batch_source) && (
                      <span className='text-muted-foreground'>
                        {[
                          row.batch_kind
                            ? balanceBatchKindLabel(row.batch_kind, t)
                            : '',
                          row.batch_source
                            ? balanceBatchSourceLabel(row.batch_source, t)
                            : '',
                        ]
                          .filter(Boolean)
                          .join(' / ')}
                      </span>
                    )}
                    {row.ledger_id ? (
                      <span className='text-muted-foreground font-mono'>
                        #{row.ledger_id}
                      </span>
                    ) : null}
                    {!row.reason &&
                      !row.funding_source &&
                      !row.order_ref &&
                      !row.batch_kind &&
                      !row.batch_source &&
                      !row.ledger_id && <span>-</span>}
                  </div>
                ),
              },
              {
                id: 'status',
                header: t('Status'),
                cell: (row: AdminBalanceRequest) =>
                  balanceRequestStatusLabel(row.status, t),
              },
              {
                id: 'actions',
                header: t('Actions'),
                cell: (row: AdminBalanceRequest) =>
                  row.status === 'pending' ? (
                    <div className='flex flex-wrap gap-1'>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={review.isPending}
                        onClick={() =>
                          review.mutate({ id: row.id, approve: true })
                        }
                      >
                        {t('Approve')}
                      </Button>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={review.isPending}
                        onClick={() =>
                          review.mutate({ id: row.id, approve: false })
                        }
                      >
                        {t('Reject')}
                      </Button>
                    </div>
                  ) : (
                    <span className='text-muted-foreground text-xs tabular-nums'>
                      {row.reviewed_at > 0
                        ? formatTimestampToDate(row.reviewed_at)
                        : '-'}
                    </span>
                  ),
              },
            ]}
          />
        )}
        {list.data && (
          <PageSwitcher
            page={page}
            pageSize={SITE_ADMIN_PAGE_SIZE}
            total={list.data.total}
            onPageChange={setPage}
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
                { id: 'user', header: t('User'), cell: (row) => row.username },
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
              cell: (row) => `#${row.operator_id}`,
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
