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
import { ExternalLink, RefreshCw } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  getPartnerLeads,
  getResellerSettings,
  rejectPartnerLead,
  SITE_ADMIN_PAGE_SIZE,
  siteAdminKeys,
} from '../api'
import type { AdminSite, PartnerLeadItem } from '../types'
import { OpenSiteDialog } from './open-site-dialog'

type LeadStatusFilter = 'all' | 'pending' | 'approved' | 'rejected'

function LeadStatusBadge(props: { status: PartnerLeadItem['status'] }) {
  const { t } = useTranslation()
  switch (props.status) {
    case 'pending':
      return (
        <Badge
          variant='outline'
          className='border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400'
        >
          {t('Pending review')}
        </Badge>
      )
    case 'approved':
      return (
        <Badge
          variant='outline'
          className='border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
        >
          {t('Approved')}
        </Badge>
      )
    case 'rejected':
      return <Badge variant='destructive'>{t('Rejected')}</Badge>
    default:
      return <Badge variant='secondary'>{props.status}</Badge>
  }
}

/** Root-only partner applications queue: approve & open site or reject with note. */
export function PartnerLeadsPanel(props: {
  onOpened: (site: AdminSite) => void
}) {
  const { t } = useTranslation()
  const fieldId = useId()
  const queryClient = useQueryClient()
  const [status, setStatus] = useState<LeadStatusFilter>('all')
  const [page, setPage] = useState(1)
  const [openingLead, setOpeningLead] = useState<PartnerLeadItem | null>(null)
  const [rejectingLead, setRejectingLead] = useState<PartnerLeadItem | null>(
    null
  )
  const [reviewNote, setReviewNote] = useState('')

  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN

  const settingsQuery = useQuery({
    queryKey: siteAdminKeys.settings,
    queryFn: getResellerSettings,
    enabled: isRoot,
  })

  const query = useQuery({
    queryKey: siteAdminKeys.list('partner-leads', { status, page }),
    queryFn: () =>
      getPartnerLeads({
        status: status === 'all' ? undefined : status,
        page,
        page_size: SITE_ADMIN_PAGE_SIZE,
      }),
    enabled: isRoot,
  })

  const rejectMutation = useMutation({
    mutationFn: () => {
      if (!rejectingLead) {
        throw new Error(t('Invalid parameters'))
      }
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      return rejectPartnerLead(rejectingLead.id, reviewNote.trim())
    },
    onSuccess: () => {
      setRejectingLead(null)
      setReviewNote('')
      toast.success(t('Partner application rejected'))
      void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (err) => handleServerError(err),
  })

  const rows = query.data?.items ?? []

  if (query.isError && !query.data) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  if (query.isPending) {
    return <LoadingState />
  }

  return (
    <div className='flex flex-col gap-5'>
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}

      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='flex flex-wrap items-center gap-2'>
          <NativeSelect
            aria-label={t('Status')}
            value={status}
            onChange={(event) => {
              setStatus(event.target.value as LeadStatusFilter)
              setPage(1)
            }}
          >
            <NativeSelectOption value='all'>
              {t('All statuses')}
            </NativeSelectOption>
            <NativeSelectOption value='pending'>
              {t('Pending review')}
            </NativeSelectOption>
            <NativeSelectOption value='approved'>
              {t('Approved')}
            </NativeSelectOption>
            <NativeSelectOption value='rejected'>
              {t('Rejected')}
            </NativeSelectOption>
          </NativeSelect>
        </div>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <RefreshCw className='size-3.5' aria-hidden />
          {t('Refresh')}
        </Button>
      </div>

      <div className='border-border bg-card w-full min-w-0 overflow-x-auto overflow-y-hidden rounded-[14px] border'>
        <Table
          withContainer={false}
          className='min-w-[960px] text-[13px] [&_td]:px-2.5 [&_th]:px-2.5'
          aria-label={t('Partner applications')}
        >
          <TableHeader>
            <TableRow>
              <TableHead className='w-16'>{t('ID')}</TableHead>
              <TableHead>{t('Contact')}</TableHead>
              <TableHead>{t('Estimated volume & note')}</TableHead>
              <TableHead>{t('Referrer source')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Submitted')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7}>
                  <EmptyState
                    className='min-h-32'
                    title={t('No partner applications yet')}
                  />
                </TableCell>
              </TableRow>
            ) : (
              rows.map((lead) => (
                <TableRow key={lead.id} className='hover:bg-muted/40'>
                  <TableCell className='font-mono text-xs tabular-nums'>
                    #{lead.id}
                  </TableCell>
                  <TableCell className='whitespace-normal'>
                    <div className='flex flex-col gap-1'>
                      <div className='flex flex-wrap items-center gap-1.5'>
                        <span className='font-medium'>{lead.contact_name}</span>
                        {lead.contact_channel && (
                          <Badge variant='secondary' className='text-[11px]'>
                            {lead.contact_channel}
                          </Badge>
                        )}
                      </div>
                      <div className='text-muted-foreground flex items-center gap-1 font-mono text-xs break-all'>
                        <span>{lead.contact_value}</span>
                        {lead.contact_value && (
                          <CopyButton
                            value={lead.contact_value}
                            size='icon'
                            variant='ghost'
                            className='size-6 shrink-0'
                            aria-label={t('Copy contact')}
                          />
                        )}
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className='max-w-xs whitespace-normal'>
                    <div className='flex flex-col gap-0.5 text-xs'>
                      {lead.estimated_volume && (
                        <span className='font-medium'>
                          {lead.estimated_volume}
                        </span>
                      )}
                      {lead.note ? (
                        <span className='text-muted-foreground line-clamp-3 break-words'>
                          {lead.note}
                        </span>
                      ) : (
                        !lead.estimated_volume && (
                          <span className='text-muted-foreground'>—</span>
                        )
                      )}
                    </div>
                  </TableCell>
                  <TableCell className='whitespace-normal'>
                    {lead.referrer_site_id > 0 || lead.referral_code ? (
                      <div className='flex flex-col gap-1'>
                        <span className='text-xs font-medium'>
                          {(() => {
                            if (lead.referrer_site_subdomain) {
                              return `${lead.referrer_site_subdomain} (#${lead.referrer_site_id})`
                            }
                            if (lead.referrer_site_id > 0) {
                              return `#${lead.referrer_site_id}`
                            }
                            return t('Partner code')
                          })()}
                        </span>
                        {lead.referrer_site_name && (
                          <span className='text-muted-foreground text-[11px]'>
                            {lead.referrer_site_name}
                          </span>
                        )}
                        {lead.referral_code && (
                          <span className='text-muted-foreground font-mono text-[11px]'>
                            {lead.referral_code}
                          </span>
                        )}
                      </div>
                    ) : (
                      <span className='text-muted-foreground text-xs'>
                        {t('Direct inquiry')}
                      </span>
                    )}
                  </TableCell>
                  <TableCell>
                    <LeadStatusBadge status={lead.status} />
                  </TableCell>
                  <TableCell className='text-xs tabular-nums'>
                    {lead.created_at > 0 ? formatTimestamp(lead.created_at) : '—'}
                  </TableCell>
                  <TableCell className='text-right whitespace-normal'>
                    {lead.status === 'pending' && (
                      <div className='flex flex-wrap items-center justify-end gap-1.5'>
                        <Button
                          type='button'
                          size='sm'
                          variant='brand'
                          className='h-7 px-2.5 text-xs'
                          onClick={() => setOpeningLead(lead)}
                        >
                          {t('Approve & Open Site')}
                        </Button>
                        <Button
                          type='button'
                          size='sm'
                          variant='outline'
                          className='text-destructive hover:text-destructive h-7 px-2.5 text-xs'
                          onClick={() => {
                            setReviewNote('')
                            setRejectingLead(lead)
                          }}
                        >
                          {t('Reject')}
                        </Button>
                      </div>
                    )}
                    {lead.status === 'approved' && lead.opened_site_id > 0 && (
                      <div className='flex justify-end'>
                        <Button
                          variant='outline'
                          size='sm'
                          className='h-7 gap-1 px-2 text-xs font-mono'
                          render={
                            <Link
                              to='/site-admin/sites/$siteId'
                              params={{ siteId: String(lead.opened_site_id) }}
                            />
                          }
                        >
                          <span>
                            {lead.opened_site_subdomain
                              ? `${lead.opened_site_subdomain} (#${lead.opened_site_id})`
                              : `#${lead.opened_site_id}`}
                          </span>
                          <ExternalLink className='size-3' aria-hidden />
                        </Button>
                      </div>
                    )}
                    {lead.status === 'rejected' && (
                      <div className='text-muted-foreground ml-auto max-w-xs text-right text-xs break-words'>
                        {lead.review_note || '—'}
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <PageSwitcher
        page={page}
        total={query.data?.total ?? 0}
        pageSize={SITE_ADMIN_PAGE_SIZE}
        onPageChange={setPage}
      />

      <OpenSiteDialog
        open={openingLead !== null}
        settings={settingsQuery.data}
        initialLead={openingLead}
        onOpenChange={(open) => {
          if (!open) setOpeningLead(null)
        }}
        onOpened={props.onOpened}
        onViewSite={props.onOpened}
      />

      <Dialog
        open={rejectingLead !== null}
        onOpenChange={(open) => {
          if (!open && !rejectMutation.isPending) {
            setRejectingLead(null)
            setReviewNote('')
          }
        }}
        title={t('Reject partner application #{{id}}', {
          id: rejectingLead?.id ?? 0,
        })}
        description={
          rejectingLead
            ? t('Applicant: {{name}} ({{channel}}: {{value}})', {
                name: rejectingLead.contact_name,
                channel: rejectingLead.contact_channel,
                value: rejectingLead.contact_value,
              })
            : undefined
        }
        contentClassName='sm:max-w-lg'
        footer={
          <div className='flex flex-wrap justify-end gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={rejectMutation.isPending}
              onClick={() => {
                setRejectingLead(null)
                setReviewNote('')
              }}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              variant='destructive'
              disabled={rejectMutation.isPending}
              onClick={() => rejectMutation.mutate()}
            >
              {t('Confirm reject')}
            </Button>
          </div>
        }
      >
        <div className='flex flex-col gap-2'>
          <Label htmlFor={`${fieldId}-reject-note`}>
            {t('Review note (optional)')}
          </Label>
          <Textarea
            id={`${fieldId}-reject-note`}
            rows={3}
            value={reviewNote}
            placeholder={t('Optional reason for rejecting this application')}
            onChange={(event) => setReviewNote(event.target.value)}
          />
        </div>
      </Dialog>
    </div>
  )
}
