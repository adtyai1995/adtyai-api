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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import {
  sideDrawerContentClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { getSystemOptions } from '@/features/system-settings/api'
import { DraftDiscardDialog } from '@/features/system-settings/components/draft-discard-dialog'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { api } from '@/lib/api'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  adjustSite,
  clearSiteNotice,
  getCloseMail,
  resolveCloseMail,
  type CloseMailRecipient,
  disableSiteDomain,
  retryDomainAutomation,
  disableSiteSenderDomain,
  getAdminBalanceRequests,
  getAdminSiteBonusMarginFill,
  getSite,
  getSiteCustomers,
  getSiteStats,
  reviewAdminBalanceRequest,
  resumeSiteSettlement,
  reviewSite,
  saveAdminSiteBonusMarginFill,
  SITE_ADMIN_PAGE_SIZE,
  siteAdminKeys,
  type AdminBalanceRequest,
  type AdminSenderDomain,
  type AdminSiteDomain,
} from '../api'
import { VISIBILITY_KEYS } from '../lib/settings'
import {
  readSiteVisibility,
  serializeSiteVisibility,
  validSiteVisibility,
} from '../lib/site-visibility'
import { siteNotice } from '../lib/sites'
import type { AdminSite, SiteStatsResponse } from '../types'
import { CloseSiteDialog } from './close-site-dialog'
import { SwitchRow } from './setting-fields'
import { SiteFundPanel } from './site-fund-panel'
import { SiteStatusBadge } from './site-status-badge'
import { SiteTierControl } from './site-tier-control'
import { SiteVisibilityEditor } from './site-visibility-editor'

function readAllowedGroups(raw?: string) {
  if (!raw) return ''
  try {
    const parsed = JSON.parse(raw) as unknown
    if (Array.isArray(parsed)) {
      return parsed.filter((id) => typeof id === 'string').join(', ')
    }
  } catch {
    return raw
  }
  return raw
}

function balanceRequestStatusLabel(
  status: AdminBalanceRequest['status'],
  t: (key: string) => string
) {
  if (status === 'pending') return t('Pending')
  if (status === 'approved') return t('Approved')
  return t('Rejected')
}

function RequestUsage({ stats, breakdown }: SiteStatsResponse) {
  const { t } = useTranslation()
  const payerLabels: Record<string, string> = { team: t('Team'), owner_self_use: t('Owner self-use'), personal: t('Personal') }
  const dimensions = [
    { label: t('Model'), rows: breakdown.by_model ?? [] },
    { label: t('Group'), rows: breakdown.by_group ?? [] },
    { label: t('Payer'), rows: (breakdown.by_payer ?? []).map((row) => ({
      ...row, key: payerLabels[row.key] ?? row.key,
    })) },
  ]
  const missing = [
    { label: t('Unverified payer for this site'), ...breakdown.unverified },
    { label: t('Unknown site across all consumption logs'), ...breakdown.unknown_site },
  ]
  return (
    <section className='flex flex-col gap-2'>
      <h3 className='text-sm font-medium'>{t('Request usage')} · {stats.from} – {stats.to}</h3>
      {dimensions.map(({ label, rows }) => (
        <StaticDataTable
          key={label}
          tableProps={{ 'aria-label': label }}
          data={rows}
          getRowKey={(row) => row.key}
          emptyContent={<EmptyState title={t('No data')} />}
          columns={[
            { id: 'key', header: label, cell: (row) => row.key || '—' },
            { id: 'requests', header: t('Requests'), cell: (row) => row.requests },
            { id: 'quota', header: t('Quota'), cell: (row) => row.quota },
          ]}
        />
      ))}
      {missing.filter((row) => row.requests > 0).map((row) => (
        <div key={row.label} className='text-muted-foreground text-sm'>
          <span>{row.label}</span> · {t('Requests')} {row.requests} · {t('Quota')} {row.quota}
        </div>
      ))}
    </section>
  )
}

function Info(props: { label: string; value: string }) {
  return (
    <div className='flex flex-col gap-0.5'>
      <dt className='text-muted-foreground text-xs'>{props.label}</dt>
      <dd className='text-sm break-all'>{props.value || '-'}</dd>
    </div>
  )
}

/** Review, adjust and status actions of one site, by permission. */
function SiteActions(props: {
  domains: AdminSiteDomain[]
  site: AdminSite
  onDirtyChange: (dirty: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const canClose = user?.role === ROLE.SUPER_ADMIN
  const canReview = user?.role === ROLE.SUPER_ADMIN
  const canAdjust = canAdmin(user, ADTY_PERMISSIONS.SITE_ADJUST)
  const canDelete = canAdmin(user, ADTY_PERMISSIONS.SITE_DELETE)
  const site = props.site
  const [costFactor, setCostFactor] = useState(site.cost_factor ?? '')
  const [visibility, setVisibility] = useState(() =>
    readSiteVisibility(site.visibility)
  )
  const [reason, setReason] = useState('')
  const [allowedGroups, setAllowedGroups] = useState(() =>
    readAllowedGroups(site.allowed_groups)
  )

  const previousSite = useRef(site)
  useEffect(() => {
    const previous = previousSite.current
    setCostFactor((current) =>
      current === (previous.cost_factor ?? '')
        ? (site.cost_factor ?? '')
        : current
    )
    const previousVisibility = readSiteVisibility(previous.visibility)
    const nextVisibility = readSiteVisibility(site.visibility)
    setVisibility((current) => {
      const merged = { ...current }
      for (const key of VISIBILITY_KEYS) {
        if (current[key] === previousVisibility[key]) {
          merged[key] = nextVisibility[key]
        }
      }
      return merged
    })
    setAllowedGroups((current) =>
      current === readAllowedGroups(previous.allowed_groups)
        ? readAllowedGroups(site.allowed_groups)
        : current
    )
    previousSite.current = site
  }, [site])
  const savedVisibility = readSiteVisibility(site.visibility)
  const visibilityValid = validSiteVisibility(site.visibility)
  const visibilityChanged = VISIBILITY_KEYS.some(
    (key) => visibility[key] !== savedVisibility[key]
  )
  const adjustmentDirty =
    costFactor.trim() !== (site.cost_factor ?? '') ||
    allowedGroups.trim() !== readAllowedGroups(site.allowed_groups) ||
    visibilityChanged
  const dirty =
    costFactor !== (site.cost_factor ?? '') ||
    allowedGroups !== readAllowedGroups(site.allowed_groups) ||
    reason !== '' ||
    visibilityChanged
  const { onDirtyChange } = props
  useDraftNavigation(dirty)
  useEffect(() => {
    onDirtyChange(dirty)
    return () => onDirtyChange(false)
  }, [dirty, onDirtyChange])
  const [closing, setClosing] = useState(false)
  const [takingDown, setTakingDown] = useState(false)
  const done = () => {
    toast.success(t('Saved successfully'))
    setReason('')
    void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
  }
  const review = useMutation({
    mutationFn: (approve: boolean) => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      return reviewSite(site.id, {
        approve,
        cost_factor: costFactor.trim() || undefined,
        note: reason.trim(),
      })
    },
    onSuccess: done,
    onError: (err) => handleServerError(err),
  })
  const adjust = useMutation({
    mutationFn: () => {
      if (
        !canAdmin(
          useAuthStore.getState().auth.user,
          ADTY_PERMISSIONS.SITE_ADJUST
        )
      ) {
        throw new Error(t('No permission to perform this action'))
      }
      const body: Parameters<typeof adjustSite>[1] = { reason: reason.trim() }
      const clear: Array<'cost_factor' | 'allowed_groups'> = []
      if (costFactor.trim() !== (site.cost_factor ?? '')) {
        if (costFactor.trim() === '') clear.push('cost_factor')
        else body.cost_factor = costFactor.trim()
      }
      if (visibilityChanged) {
        body.visibility = serializeSiteVisibility(visibility)
      }
      if (allowedGroups.trim() !== readAllowedGroups(site.allowed_groups)) {
        if (allowedGroups.trim() === '') {
          clear.push('allowed_groups')
        } else {
          body.allowed_groups = JSON.stringify(
            allowedGroups
              .split(',')
              .map((id) => id.trim())
              .filter(Boolean)
          )
        }
      }
      if (clear.length > 0) body.clear = clear
      return adjustSite(site.id, body)
    },
    onSuccess: (updated) => {
      setCostFactor(updated.cost_factor ?? '')
      setAllowedGroups(readAllowedGroups(updated.allowed_groups))
      setVisibility(readSiteVisibility(updated.visibility))
      queryClient.setQueryData<Awaited<ReturnType<typeof getSite>>>(
        siteAdminKeys.site(site.id),
        (cached) => (cached ? { ...cached, site: updated } : cached)
      )
      done()
    },
    onError: (err) => handleServerError(err),
  })
  const settlement = useMutation({
    mutationFn: () => resumeSiteSettlement(site.id),
    onSuccess: done,
    onError: (err) => handleServerError(err),
  })
  const takedown = useMutation({
    mutationFn: () => {
      if (
        !canAdmin(
          useAuthStore.getState().auth.user,
          ADTY_PERMISSIONS.SITE_DELETE
        )
      ) {
        throw new Error(t('No permission to perform this action'))
      }
      return clearSiteNotice(site.id, reason.trim())
    },
    onSuccess: () => {
      setTakingDown(false)
      done()
    },
    onError: (err) => handleServerError(err),
  })
  const notice = siteNotice(site.brand)
  const busy =
    review.isPending ||
    adjust.isPending ||
    settlement.isPending ||
    takedown.isPending
  const editable = site.status !== 'closed' && site.status !== 'closing'

  const visibilityEditor = (
    <SiteVisibilityEditor
      value={visibility}
      onChange={setVisibility}
      disabled={
        !canAdjust ||
        !editable ||
        site.status === 'pending' ||
        busy ||
        !visibilityValid
      }
    />
  )
  if (!canReview && !canAdjust && !canDelete) return visibilityEditor
  return (
    <section className='flex flex-col gap-3 rounded-lg border p-3'>
      <h3 className='text-sm font-medium'>{t('Manage')}</h3>
      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='space-y-1'>
          <Label htmlFor='site-cost-factor'>{t('Cost factor')}</Label>
          <Input
            id='site-cost-factor'
            value={costFactor}
            placeholder={t('Follow site level')}
            disabled={(!canReview && !canAdjust) || !editable || busy}
            onChange={(event) => setCostFactor(event.target.value)}
          />
        </div>
      </div>
      <div className='space-y-1'>
        <Label htmlFor='site-allowed-groups'>{t('Allowed groups')}</Label>
        <Input
          id='site-allowed-groups'
          value={allowedGroups}
          placeholder={t('Every live billing group')}
          disabled={(!canReview && !canAdjust) || !editable || busy}
          onChange={(event) => setAllowedGroups(event.target.value)}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Leave empty to allow every live billing group. Separate group IDs with commas.'
          )}
        </p>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Leave empty to follow the site level supply discount. The owner is notified when it changes.'
        )}
      </p>
      {visibilityEditor}
      {!visibilityValid && (
        <p role='alert' className='text-destructive text-sm'>
          {t(
            'Saved site visibility settings are invalid. Reload or repair the saved settings before editing this site.'
          )}
        </p>
      )}
      <div className='space-y-1'>
        <Label htmlFor='site-reason'>{t('Reason')}</Label>
        <Textarea
          id='site-reason'
          rows={2}
          disabled={busy}
          value={reason}
          onChange={(event) => setReason(event.target.value)}
        />
      </div>
      <div className='flex flex-wrap gap-2'>
        {canReview && site.status === 'pending' && (
          <>
            <Button disabled={busy} onClick={() => review.mutate(true)}>
              {t('Approve')}
            </Button>
            <Button
              variant='outline'
              disabled={busy}
              onClick={() => review.mutate(false)}
            >
              {t('Reject')}
            </Button>
          </>
        )}
        {canAdjust && editable && site.status !== 'pending' && (
          <Button
            variant='outline'
            disabled={
              busy ||
              !adjustmentDirty ||
              reason.trim() === '' ||
              !visibilityValid
            }
            onClick={() => adjust.mutate()}
          >
            {t('Save site settings')}
          </Button>
        )}
        {canAdjust && (site.settle_hold_reason ?? '') !== '' && (
          <Button
            variant='outline'
            disabled={busy}
            onClick={() => settlement.mutate()}
          >
            {t('Resume settlement')}
          </Button>
        )}
        {(site.settle_hold_reason ?? '') !== '' && (
          <p className='text-warning w-full text-sm'>
            {t('Settlement is on hold: {{reason}}', {
              reason: site.settle_hold_reason,
            })}
          </p>
        )}
        {canDelete && notice && (
          <Button
            variant='outline'
            disabled={busy}
            onClick={() => setTakingDown(true)}
          >
            {t('Take down notice')}
          </Button>
        )}
        {canClose && site.status !== 'closed' && (
          <Button
            variant='destructive'
            disabled={busy}
            onClick={() => setClosing(true)}
          >
            {site.status==='closing'?t('Reschedule closure'):t('Close site')}
          </Button>
        )}
        {canReview && ['active', 'paused'].includes(site.status) && (
          <p className='text-muted-foreground text-sm'>
            {t('Site owners cannot be replaced')}
          </p>
        )}
      </div>
      {canClose && (site.status==='closing'||site.status==='closed') && <CloseMailPanel key={site.id} siteId={site.id}/>}
      {notice && (
        <div className='space-y-1'>
          <span className='text-muted-foreground text-xs'>
            {t('Current notice')}
          </span>
          <p className='bg-muted rounded p-2 text-sm whitespace-pre-wrap'>
            {notice}
          </p>
        </div>
      )}
      <ConfirmDialog
        open={takingDown}
        onOpenChange={setTakingDown}
        title={t('Take down notice')}
        desc={t(
          "The notice disappears from the site at once. The owner's other brand settings stay."
        )}
        destructive
        isLoading={takedown.isPending}
        disabled={!canDelete}
        handleConfirm={() => canDelete && takedown.mutate()}
      />
      {canClose && (
        <CloseSiteDialog
          domains={props.domains}
          site={site}
          open={closing}
          onOpenChange={setClosing}
        />
      )}
    </section>
  )
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

function CloseMailPanel({siteId}:{siteId:number}){
 const {t}=useTranslation()
 const [page,setPage]=useState(1)
 const [batch,setBatch]=useState(0)
 const [reason,setReason]=useState('')
 const query=useQuery({queryKey:siteAdminKeys.list('close-mail',{siteId,batch,page}),queryFn:()=>getCloseMail(siteId,batch,page),refetchInterval:30000})
 const resolve=useMutation({mutationFn:({row,decision}:{row:CloseMailRecipient;decision:string})=>resolveCloseMail(siteId,row,decision,reason),onSuccess:()=>{setReason('');void query.refetch()},onError:(error)=>handleServerError(error)})
 if(query.isError)return <ErrorState onRetry={()=>void query.refetch()}/>
 const data=query.data
 return <section className="space-y-2">
 <h3>{t('Closure notification delivery')}</h3>
 {data && <>
 <select aria-label={t('Notification batch')} value={data.batch_id} onChange={e=>{setBatch(Number(e.target.value));setPage(1)}}>
 {data.batches.map(b=><option key={b.id} value={b.id}>#{b.id} · {b.execute_at > 0 ? formatTimestampToDate(b.execute_at) : t('Unknown')} · {closeMailBatchStatusLabel(b.retry_status, t)}</option>)}
 </select>
 <Label>{t('Reconciliation note')}<Input value={reason} onChange={e=>setReason(e.target.value)}/></Label>
 {data.recipients.map(row=><div key={row.id} className="space-x-2 text-sm">
 <span>{row.email} · {closeMailRecipientStatusLabel(row.status, t)} · {t('Attempts')} {row.attempt_count} {row.last_error}</span>
 {row.attempts?.length ? <details><summary>{t('Delivery attempts')}</summary><ul>{row.attempts.map(attempt =>
   <li key={attempt.number}>#{attempt.number} · {attempt.provider} · {closeMailRecipientStatusLabel(attempt.outcome, t)} · {formatTimestampToDate(attempt.started_at)} {attempt.provider_message_id}</li>
 )}</ul></details> : null}
 {(row.status==='failed'||row.status==='review')&&data.batches.find(b=>b.id===data.batch_id)?.retry_status!=='superseded' && <>
 <Button variant="outline" disabled={resolve.isPending||!reason.trim()} onClick={()=>resolve.mutate({row,decision:'retry'})}>{t('Retry')}</Button>
 <Button variant="outline" disabled={resolve.isPending||!reason.trim()} onClick={()=>resolve.mutate({row,decision:'delivered'})}>{t('Confirm delivered')}</Button>
 </>}
 </div>)}
 {data.total>20 && <div><Button disabled={page===1} onClick={()=>setPage(page-1)}>{t('Previous')}</Button><Button disabled={20*page>=data.total} onClick={()=>setPage(page+1)}>{t('Next')}</Button></div>}
 </>}
 </section>
}

function SiteCustomers(props: { siteId: number }) {
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
  if (query.isError && !query.data) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  return (
    <section className='flex flex-col gap-2'>
      <h3 className='text-sm font-medium'>{t('Customers')}</h3>
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      <StaticDataTable
        tableClassName='min-w-[640px]'
        tableProps={{ 'aria-label': t('Customers') }}
        data={query.data?.items ?? []}
        getRowKey={(row) => String(row.id)}
        emptyContent={<EmptyState className='min-h-24' title={t('No data')} />}
        columns={[
          { id: 'user', header: t('User'), cell: (row) => row.username },
          { id: 'email', header: t('Email'), cell: (row) => row.email || '-' },
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
    </section>
  )
}

function SiteDomains(props: {
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
    <section className='flex flex-col gap-3 rounded-lg border p-3'>
      <h3 className='text-sm font-medium'>{t('Site access and domains')}</h3>
      <dl className='grid gap-3 text-sm sm:grid-cols-2'>
        <div className='flex flex-col gap-1'>
          <dt className='text-muted-foreground text-xs'>
            {t('Website address')}
          </dt>
          <dd className='flex items-center gap-2 font-mono text-xs break-all'>
            <span>{defaultHost}</span>
            <CopyButton
              value={`https://${defaultHost}`}
              size='sm'
              variant='outline'
              aria-label={t('Copy link')}
            />
          </dd>
        </div>
        <div className='flex flex-col gap-1'>
          <dt className='text-muted-foreground text-xs'>{t('API address')}</dt>
          <dd className='flex items-center gap-2 text-xs break-all'>
            {apiHost ? (
              <>
                <span className='font-mono'>{apiHost}</span>
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
      {props.domains.length > 0 && (
        <div className='flex flex-col gap-2 border-t pt-2'>
          {props.domains.map((domain) => (
            <div
              key={domain.id}
              className='flex items-center justify-between gap-2'
            >
              <span className='text-sm break-all'>
                <span className='font-mono text-xs'>{domain.hostname}</span> ·{' '}
                {domain.role === 'base_url' ? t('API address') : t('Website')} ·{' '}
                {adminDomainStatusLabel(domain.status, t)} ·{' '}
                {adminDomainSslLabel(domain.ssl_status ?? '', t)}
                {domain.automation_state && (
                  <span className='text-muted-foreground block text-xs'>
                    {adminDomainAutomationLabel(domain.automation_state, t)}
                  </span>
                )}
                {domain.last_error && (
                  <span className='text-destructive block text-xs'>
                    {domain.last_error}
                  </span>
                )}
              </span>
              <div className='flex shrink-0 gap-1.5'>
                {user?.role === ROLE.SUPER_ADMIN &&
                  domain.status !== 'removed' &&
                  domain.automation_state && (
                    <Button
                      variant='outline'
                      size='sm'
                      disabled={retryDomain.isPending}
                      onClick={() => retryDomain.mutate(domain.id)}
                    >
                      {t('Retry')}
                    </Button>
                  )}
                {canDelete && domain.status !== 'removed' && (
                  <Button
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
          ))}
        </div>
      )}
      {props.senders.length > 0 && (
        <div className='flex flex-col gap-2 border-t pt-2'>
          {props.senders.map((sender) => (
            <div
              key={sender.domain}
              className='flex items-center justify-between gap-2'
            >
              <span className='text-sm break-all'>
                {sender.domain} · {adminSenderStatusLabel(sender.status, t)}
                {sender.cleanup_error && (
                  <span className='block text-xs'>
                    {t('Cleanup failed')} · {sender.cleanup_error}
                  </span>
                )}
                {!!sender.retry_at && (
                  <span className='block text-xs'>
                    {t('Next retry')} · {formatTimestampToDate(sender.retry_at)}
                  </span>
                )}
              </span>
              {canDelete && (
                <Button
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
        </div>
      )}
    </section>
  )
}

function RootBalanceRequests(props: { siteId: number }) {
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
  const review = useMutation({
    mutationFn: (input: { id: number; approve: boolean }) =>
      reviewAdminBalanceRequest(input.id, input.approve),
    onSuccess: () =>
      void queryClient.invalidateQueries({
        queryKey: siteAdminKeys.list('balance-requests', {
          siteId: props.siteId,
        }),
      }),
    onError: (error) => handleServerError(error),
  })
  if (!isRoot) return null
  return (
    <section className='flex flex-col gap-2'>
      <h3 className='text-sm font-medium'>{t('Balance requests')}</h3>
      <p className='text-muted-foreground text-sm'>
        {t(
          'This request does not change the customer balance until the site owner or Root approves it.'
        )}
      </p>
      <select
        className='w-fit rounded-md border p-2'
        value={status}
        aria-label={t('Status')}
        onChange={(event) => {
          setStatus(event.target.value)
          setPage(1)
        }}
      >
        <option value=''>{t('All statuses')}</option>
        <option value='pending'>{t('Pending')}</option>
        <option value='approved'>{t('Approved')}</option>
        <option value='rejected'>{t('Rejected')}</option>
      </select>
      {list.isPending && <LoadingState />}
      {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
      {list.isSuccess && (
        <StaticDataTable
          tableClassName='min-w-[640px]'
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
    </section>
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
    <section className='flex flex-col gap-2'>
      <h3 className='text-sm font-medium'>{t('Bonuses')}</h3>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Your customers get this bonus on top-ups with the chosen payment method. It is paid from your earnings, and only methods the platform authorized for this site are shown.'
        )}
      </p>
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
    </section>
  )
}

/** Details of one site (task 11.9): info, actions, customers and the audit trail. */
export function SiteDetailSheet(props: {
  siteId: number | null
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const [dirty, setDirty] = useState(false)
  const [discarding, setDiscarding] = useState(false)
  const query = useQuery({
    queryKey: siteAdminKeys.site(props.siteId ?? 0),
    queryFn: () => getSite(props.siteId ?? 0),
    enabled: props.siteId !== null,
  })
  const site = query.data?.site
  const statsQuery = useQuery({
    queryKey: siteAdminKeys.list('stats', { id: props.siteId ?? 0 }),
    queryFn: () => getSiteStats(props.siteId ?? 0),
    enabled: props.siteId !== null,
  })
  const totals = statsQuery.data?.stats.total

  return (
    <Sheet
      open={props.siteId !== null}
      onOpenChange={(open) => {
        if (!open && dirty) setDiscarding(true)
        else props.onOpenChange(open)
      }}
    >
      <SheetContent className={sideDrawerContentClassName('sm:max-w-3xl')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{site?.host ?? t('Site')}</SheetTitle>
          <SheetDescription>
            {site ? <SiteStatusBadge status={site.status} /> : null}
          </SheetDescription>
        </SheetHeader>
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {!query.isError &&
          (!site ? (
            <LoadingState />
          ) : (
            <div className='flex flex-col gap-5 overflow-y-auto p-4'>
              <dl className='grid gap-3 sm:grid-cols-3'>
                <Info
                  label={t('Owner')}
                  value={site.owner_username || `#${site.owner_user_id}`}
                />
                <Info
                  label={t('Opened')}
                  value={formatTimestampToDate(site.created_at)}
                />
                <Info label={t('Customers')} value={String(site.customers)} />
                <Info
                  label={t('Cost factor')}
                  value={site.cost_factor ?? t('Follow site level')}
                />
                <Info
                  label={t('Tier cost factor')}
                  value={
                    site.tier_cost_factor
                      ? [
                          site.tier_cost_factor,
                          site.tier_code && `(${site.tier_code})`,
                        ]
                          .filter(Boolean)
                          .join(' ')
                      : '-'
                  }
                />
                <Info label={t('Owner contact')} value={site.contact_info} />
                {site.close_execute_at > 0 && (
                  <Info
                    label={t('Closes on')}
                    value={formatTimestampToDate(site.close_execute_at)}
                  />
                )}
              </dl>
              <SiteDomains
                site={site}
                domains={query.data?.domains ?? []}
                senders={query.data?.sender_domains ?? []}
              />
              <section className='flex flex-col gap-2'>
                <h3 className='text-sm font-medium'>{t('Business data')}</h3>
                {statsQuery.isError && (
                  <ErrorState onRetry={() => void statsQuery.refetch()} />
                )}
                {statsQuery.isPending && <LoadingState />}
                {statsQuery.data && (
                  <dl className='grid gap-3 sm:grid-cols-4'>
                    <Info
                      label={t('Revenue')}
                      value={totals?.revenue_usd ?? '0'}
                    />
                    <Info
                      label={t('Earnings')}
                      value={totals?.earning_usd ?? '0'}
                    />
                    <Info
                      label={t('Top-up')}
                      value={totals?.topup_usd ?? '0'}
                    />
                    <Info
                      label={t('Withdrawn')}
                      value={statsQuery.data?.withdrawn_usd ?? '-'}
                    />
                  </dl>
                )}
              </section>
              {statsQuery.data?.breakdown && <RequestUsage {...statsQuery.data} />}
              <SiteActions
                domains={query.data?.domains ?? []}
                key={site.id}
                site={site}
                onDirtyChange={setDirty}
              />
              <SiteBonusMethodAuth siteId={site.id} />
              <RootBalanceRequests siteId={site.id} />
              <SiteTierControl
                key={`${site.id}:${site.tier_mode}:${site.tier_code}`}
                site={site}
              />
              <SiteFundPanel siteId={site.id} />
              <SiteCustomers siteId={site.id} />
              <section className='flex flex-col gap-2'>
                <h3 className='text-sm font-medium'>{t('Audit trail')}</h3>
                <StaticDataTable
                  tableClassName='min-w-[600px]'
                  tableProps={{ 'aria-label': t('Audit trail') }}
                  data={query.data?.audits ?? []}
                  getRowKey={(row) => String(row.id)}
                  emptyContent={
                    <EmptyState className='min-h-24' title={t('No data')} />
                  }
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
              </section>
            </div>
          ))}
      </SheetContent>
      <DraftDiscardDialog
        open={discarding}
        onOpenChange={setDiscarding}
        onDiscard={() => props.onOpenChange(false)}
      />
    </Sheet>
  )
}
