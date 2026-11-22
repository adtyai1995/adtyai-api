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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { api } from '@/lib/api'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { TicketImage } from './ticket-image'
import {
  TICKET_QUEUES,
  TICKET_STATUS_LABELS,
  type TicketBadges,
  type TicketQueueId,
  normalizeTicketStatus,
  ticketStatusLabel,
} from './ticket-status'

type TicketScope = 'personal' | 'platform' | 'owner' | 'staff' | 'team'
const ticketTypes: Record<string, string> = {
  billing: 'Billing',
  api: 'API',
  account: 'Account',
  other: 'Other',
}
const ticketPriorities: Record<string, string> = {
  normal: 'Normal',
  important: 'Important',
  urgent: 'Urgent',
}
const eventLabels: Record<string, string> = {
  create: 'New ticket',
  reply: 'Reply',
  note: 'Internal note',
  assign: 'Assign ticket',
  transfer: 'Transfer ticket',
  status: 'Status',
  priority: 'Priority',
  reopen: 'Reopen ticket',
  image_upload: 'Upload image',
  image_view: 'View image',
}

type Ticket = {
  type: string
  user_id: number
  site_id: number
  team_id: number
  related_kind?: string
  related_ref?: string
  related_model?: string
  related_group?: string
  related_error?: string
  id: number
  title: string
  status: string
  priority?: string
  assignee_id?: number
}

type TicketMessage = {
  id: number
  visibility: string
  body: string
}

type TicketImage = {
  id: number
  object_id?: string
  ticket_id?: number
  message_id?: number
  visibility: string
  mime: string
  size?: number
  width?: number
  height?: number
  sha256?: string
  created_at?: number
}

type TicketEvent = {
  id: number
  ticket_id?: number
  actor_id?: number
  action: string
  detail?: string
  created_at?: number
}

type TicketDetail = {
  ticket: Ticket
  assignees?: { id: number; username: string; display_name: string }[]
  messages: TicketMessage[]
  images?: TicketImage[]
  events?: TicketEvent[]
  can_note: boolean
  can_manage: boolean
}

type TicketList = {
  tickets?: Ticket[]
  total: number
  can_view_team: boolean
  can_note?: boolean
  can_manage?: boolean
  badges?: TicketBadges
}

export function TicketCenter({
  scope: workspace = 'personal',
}: {
  scope?: TicketScope
}) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  return (
    <TicketWorkspace
      key={`${userId}-${workspace}`}
      workspace={workspace}
      userId={userId}
    />
  )
}

function TicketWorkspace({
  workspace,
  userId,
}: {
  workspace: TicketScope
  userId?: number
}) {
  const [scope, setScope] = useState(workspace)
  const [page, setPage] = useState(1)
  const [type, setType] = useState('other')
  const [priority, setPriority] = useState('normal')
  const [internalImage, setInternalImage] = useState(false)
  const pageSize = 20
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [queue, setQueue] = useState<TicketQueueId | ''>(() =>
    workspace === 'personal'
      ? ''
      : (TICKET_QUEUES.find((item) => window.location.hash === `#${item.id}`)
          ?.id ?? '')
  )
  const list = useQuery({
    queryKey: ['adty-tickets', userId, scope, queue, page],
    retry: false,
    refetchInterval: 30_000,
    queryFn: async () => {
      const response = await api.get('/api/adty/tickets', {
        params: { scope, queue, p: page, page_size: pageSize },
      })
      const body = response.data as {
        success?: boolean
        data?: TicketList
        message?: string
      }
      if (!body.success) {
        throw new Error(body.message || t('Failed to load data'))
      }
      return {
        tickets: body.data?.tickets ?? [],
        total: body.data?.total ?? 0,
        canViewTeam: Boolean(body.data?.can_view_team),
        can_note: Boolean(body.data?.can_note),
        can_manage: Boolean(body.data?.can_manage),
        badges: body.data?.badges,
      }
    },
  })
  const [selected, setSelected] = useState<number | null>(null)
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [reply, setReply] = useState('')
  const [note, setNote] = useState('')
  const [assignee, setAssignee] = useState('')
  const [related, setRelated] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  useEffect(() => {
    const hash = window.location.hash.replace('#', '')
    if (hash.startsWith('ticket-')) setRelated(hash.slice('ticket-'.length))
  }, [])

  const detail = useQuery({
    queryKey: ['adty-tickets', userId, scope, 'detail', selected],
    enabled: selected !== null && list.isSuccess,
    retry: false,
    refetchInterval: 30_000,
    queryFn: async () => {
      const response = await api.get(`/api/adty/tickets/${selected}`, {
        params: { scope },
      })
      const payload = response.data as {
        success?: boolean
        data?: TicketDetail
        message?: string
      }
      if (!payload.success || !payload.data) {
        throw new Error(payload.message || t('Failed to load data'))
      }
      return payload.data
    },
  })

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['adty-tickets'] })
    void invalidateSidebarBadges(queryClient)
  }

  const create = async () => {
    setPending(true)
    setError('')
    try {
      const response = await api.post('/api/adty/tickets', {
        type,
        title: title.trim(),
        body: body.trim(),
        priority,
        related_kind: related ? 'topup' : '',
        related_ref: related,
      })
      const payload = response.data as {
        success?: boolean
        message?: string
        data?: Ticket
      }
      if (!payload.success || !payload.data) {
        throw new Error(payload.message || t('Failed to save'))
      }
      setTitle('')
      setBody('')
      setPage(1)
      setQueue('')
      setScope('personal')
      selectTicket(payload.data.id)
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const updateStatus = async (
    status: string,
    assigneeId?: number,
    nextPriority?: string
  ) => {
    if (selected === null) return
    setPending(true)
    setError('')
    try {
      const response = await api.post(
        `/api/adty/tickets/${selected}/status`,
        {
          status,
          assignee_id: assigneeId ?? 0,
          priority: nextPriority,
        },
        { params: { scope } }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload.success) {
        throw new Error(payload.message || t('Failed to save'))
      }
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const submitReply = async () => {
    if (selected === null || reply.trim() === '') return
    setPending(true)
    setError('')
    try {
      const response = await api.post(
        `/api/adty/tickets/${selected}/replies`,
        {
          body: reply.trim(),
        },
        { params: { scope } }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload?.success) {
        throw new Error(payload?.message || t('Failed to save'))
      }
      setReply('')
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const submitNote = async () => {
    if (selected === null || note.trim() === '') return
    setPending(true)
    setError('')
    try {
      const response = await api.post(
        `/api/adty/tickets/${selected}/notes`,
        {
          body: note.trim(),
        },
        { params: { scope } }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload?.success) {
        throw new Error(payload?.message || t('Failed to save'))
      }
      setNote('')
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const uploadImage = async (encodedData: string) => {
    if (selected === null) return
    setPending(true)
    setError('')
    try {
      const response = await api.post(
        `/api/adty/tickets/${selected}/images`,
        {
          data: encodedData,
          visibility: internalImage ? 'internal' : 'public',
        },
        { params: { scope } }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload?.success) {
        throw new Error(payload?.message || t('Only images are accepted'))
      }
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Only images are accepted')))
    } finally {
      setPending(false)
    }
  }

  function selectTicket(id: number | null) {
    setSelected(id)
    setReply('')
    setNote('')
    setAssignee('')
    setInternalImage(false)
    setError('')
  }
  function changeQueue(next: TicketQueueId | '') {
    setQueue(next)
    setPage(1)
    selectTicket(null)
  }
  const badges = list.isSuccess ? list.data.badges : undefined
  const canManage = list.isSuccess && list.data.can_manage

  return (
    <Card id='tickets'>
      <CardContent className='flex flex-col gap-4 py-4'>
        <div className='flex flex-col gap-1'>
          <h2 className='text-sm font-medium'>
            {workspace === 'personal' ? t('My tickets') : t('Ticket workspace')}
          </h2>
          <p className='text-muted-foreground text-sm'>
            {t('Images only. Hide API keys, passwords, and payment details.')}
          </p>
        </div>
        {workspace === 'personal' &&
          list.isSuccess &&
          list.data.canViewTeam && (
            <div className='flex flex-wrap gap-2'>
              {(['personal', 'team'] as const).map((value) => (
                <Button
                  key={value}
                  variant={value === scope ? 'default' : 'outline'}
                  onClick={() => {
                    setScope(value)
                    changeQueue('')
                  }}
                >
                  {value === 'personal' ? t('My tickets') : t('Team tickets')}
                </Button>
              ))}
            </div>
          )}
        {canManage && badges && (
          <div className='flex flex-col gap-2'>
            <div className='flex flex-wrap gap-2'>
              <Button
                size='sm'
                variant={queue === '' ? 'default' : 'outline'}
                onClick={() => changeQueue('')}
              >
                {t('All tickets')}
              </Button>
              {TICKET_QUEUES.map((item) => (
                <Button
                  key={item.id}
                  type='button'
                  size='sm'
                  variant={queue === item.id ? 'default' : 'outline'}
                  onClick={() => changeQueue(item.id)}
                >
                  {t(item.label)}
                  <span className='tabular-nums'>
                    {badges[item.badge] ?? 0}
                  </span>
                </Button>
              ))}
            </div>
            <p className='text-muted-foreground text-xs'>
              {t('Ticket badge new')} {badges.new} ·{' '}
              {t('Ticket badge pending assignment')} {badges.pending_assignment}{' '}
              · {t('Ticket status in progress')} {badges.in_progress}
            </p>
          </div>
        )}
        {list.isLoading && (
          <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
        )}
        {list.isError && (
          <Button
            type='button'
            variant='outline'
            onClick={() => void list.refetch()}
          >
            {t('Retry')}
          </Button>
        )}
        {list.isSuccess && list.data.tickets.length === 0 && (
          <p className='text-muted-foreground text-sm'>{t('No tickets')}</p>
        )}
        <div className='flex flex-col gap-2'>
          {(list.isSuccess ? list.data.tickets : []).map((ticket) => (
            <button
              key={ticket.id}
              type='button'
              className='hover:bg-muted flex items-center justify-between gap-3 rounded-lg px-3 py-2 text-left text-sm transition-colors'
              disabled={pending}
              onClick={() => selectTicket(ticket.id)}
            >
              <span className='min-w-0 truncate'>
                {ticket.title} · {t(ticketTypes[ticket.type] ?? 'Other')}
              </span>
              <span className='text-muted-foreground shrink-0'>
                {t(ticketStatusLabel(ticket.status, ticket.assignee_id))}
              </span>
            </button>
          ))}
        </div>
        {list.isSuccess && (
          <PageSwitcher
            page={page}
            total={list.data.total}
            pageSize={pageSize}
            onPageChange={(next) => {
              setPage(next)
              selectTicket(null)
            }}
          />
        )}
        {selected !== null && list.isSuccess && detail.isError && (
          <Button variant='outline' onClick={() => void detail.refetch()}>
            {t('Retry')}
          </Button>
        )}
        {list.isSuccess && detail.isSuccess && (
          <div className='flex flex-col gap-3'>
            <h3 className='text-sm font-medium'>{detail.data.ticket.title}</h3>
            <p className='text-muted-foreground text-sm'>
              {t(
                ticketStatusLabel(
                  detail.data.ticket.status,
                  detail.data.ticket.assignee_id
                )
              )}{' '}
              · {t(ticketTypes[detail.data.ticket.type] ?? 'Other')} ·{' '}
              {t(
                ticketPriorities[detail.data.ticket.priority ?? 'normal'] ??
                  'Normal'
              )}
            </p>
            {[
              detail.data.ticket.related_ref,
              detail.data.ticket.related_model,
              detail.data.ticket.related_group,
              detail.data.ticket.related_error,
            ]
              .filter(Boolean)
              .map((value, index) => (
                <p className='text-sm break-all' key={index}>
                  {value}
                </p>
              ))}
            {detail.data.can_manage && (
              <p className='text-muted-foreground text-sm'>
                {t('Site ID')} {detail.data.ticket.site_id} · {t('User ID')}{' '}
                {detail.data.ticket.user_id}
                {detail.data.ticket.team_id > 0 && (
                  <>
                    {' '}
                    · {t('Team ID')} {detail.data.ticket.team_id}
                  </>
                )}
              </p>
            )}
            {detail.data.messages.map((message) => (
              <p key={message.id} className='text-sm whitespace-pre-wrap'>
                {message.visibility === 'internal'
                  ? `${t('Internal note')}: `
                  : ''}
                {message.body}
              </p>
            ))}
            {(detail.data.images ?? [])
              .filter(
                (image) => image.visibility === 'public' || detail.data.can_note
              )
              .map((image) => {
                const label = image.object_id
                  ? `${t('Image')} ${image.object_id}`
                  : `${t('Image')} #${image.id}`
                return (
                  <TicketImage
                    key={image.id}
                    ticketID={detail.data.ticket.id}
                    imageID={image.id}
                    scope={scope}
                    userId={userId}
                    label={
                      image.visibility === 'internal'
                        ? `${t('Internal note')}: ${label}`
                        : label
                    }
                  />
                )
              })}
            {(detail.data.events ?? [])
              .filter(
                (event) => event.action !== 'note' || detail.data.can_note
              )
              .map((event) => {
                let detailLabel = ''
                if (event.detail) {
                  detailLabel = TICKET_STATUS_LABELS[
                    normalizeTicketStatus(event.detail)
                  ]
                    ? `${t(ticketStatusLabel(event.detail))}`
                    : event.action === 'priority'
                      ? t(ticketPriorities[event.detail] ?? event.detail)
                      : event.detail
                }
                return (
                  <p key={event.id} className='text-muted-foreground text-xs'>
                    {t(eventLabels[event.action] ?? event.action)}
                    {detailLabel ? ` · ${detailLabel}` : ''}
                  </p>
                )
              })}
            <Textarea
              aria-label={t('Reply')}
              value={reply}
              onChange={(event) => setReply(event.target.value)}
            />
            <Button
              type='button'
              disabled={pending || reply.trim() === ''}
              onClick={() => void submitReply()}
            >
              {t('Reply')}
            </Button>
            {detail.data.can_note && (
              <>
                <Textarea
                  aria-label={t('Internal note')}
                  value={note}
                  onChange={(event) => setNote(event.target.value)}
                />
                <Button
                  type='button'
                  variant='outline'
                  disabled={pending || note.trim() === ''}
                  onClick={() => void submitNote()}
                >
                  {t('Internal note')}
                </Button>
              </>
            )}
            {detail.data.can_manage && (
              <>
                <Label htmlFor='ticket-assignee'>{t('Ticket assignee')}</Label>
                <NativeSelect
                  id='ticket-assignee'
                  value={
                    assignee || String(detail.data.ticket.assignee_id || '')
                  }
                  onChange={(event) => setAssignee(event.target.value)}
                >
                  <NativeSelectOption value=''>
                    {t('Unassigned')}
                  </NativeSelectOption>
                  {(detail.data.ticket.assignee_id ?? 0) > 0 &&
                    !detail.data.assignees?.some(
                      (handler) => handler.id === detail.data.ticket.assignee_id
                    ) && (
                      <NativeSelectOption
                        value={detail.data.ticket.assignee_id}
                        disabled
                      >
                        #{detail.data.ticket.assignee_id}
                      </NativeSelectOption>
                    )}
                  {detail.data.assignees?.map((handler) => (
                    <NativeSelectOption key={handler.id} value={handler.id}>
                      {handler.display_name || handler.username} ·{' '}
                      {handler.username} (#{handler.id})
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <Label htmlFor='ticket-current-priority'>{t('Priority')}</Label>
                <NativeSelect
                  id='ticket-current-priority'
                  disabled={pending}
                  value={detail.data.ticket.priority || 'normal'}
                  onChange={(event) =>
                    void updateStatus('', undefined, event.target.value)
                  }
                >
                  {Object.entries(ticketPriorities).map(([value, label]) => (
                    <NativeSelectOption value={value} key={value}>
                      {t(label)}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <div className='flex flex-wrap gap-2'>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending || !/^\d+$/.test(assignee.trim())}
                    onClick={() =>
                      void updateStatus('', Number(assignee.trim()))
                    }
                  >
                    {detail.data.ticket.assignee_id
                      ? t('Transfer ticket')
                      : t('Assign ticket')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus('pending_assignment')}
                  >
                    {t('Ticket status pending assignment')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus('in_progress')}
                  >
                    {t('Ticket status in progress')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus('waiting_user')}
                  >
                    {t('Ticket status waiting user')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus('waiting_internal')}
                  >
                    {t('Ticket status waiting internal')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus('resolve')}
                  >
                    {t('Ticket status resolved')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus('close')}
                  >
                    {t('Ticket status closed')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus('reopen')}
                  >
                    {t('Reopen ticket')}
                  </Button>
                </div>
              </>
            )}
            {!detail.data.can_manage &&
              detail.data.ticket.user_id === userId && (
                <div className='flex flex-wrap gap-2'>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus('resolve')}
                  >
                    {t('Confirm ticket resolved')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus('reopen')}
                  >
                    {t('Request ticket continue')}
                  </Button>
                </div>
              )}
            {detail.data.can_note && (
              <>
                <Label htmlFor='ticket-image-visibility'>
                  {t('Image visibility')}
                </Label>
                <NativeSelect
                  id='ticket-image-visibility'
                  value={internalImage ? 'internal' : 'public'}
                  onChange={(event) =>
                    setInternalImage(event.target.value === 'internal')
                  }
                >
                  <NativeSelectOption value='public'>
                    {t('Public reply')}
                  </NativeSelectOption>
                  <NativeSelectOption value='internal'>
                    {t('Internal note')}
                  </NativeSelectOption>
                </NativeSelect>
              </>
            )}
            <Label htmlFor='ticket-image'>{t('Image')}</Label>
            <Input
              id='ticket-image'
              type='file'
              accept='image/png,image/jpeg,image/gif,image/webp'
              disabled={pending}
              onChange={(event) => {
                const file = event.target.files?.[0]
                if (!file || selected === null) return
                const reader = new FileReader()
                reader.onload = () => {
                  const value = String(reader.result || '')
                  void uploadImage(value.slice(value.indexOf(',') + 1))
                }
                reader.readAsDataURL(file)
              }}
            />
          </div>
        )}
        {workspace === 'personal' && (
          <>
            <Label htmlFor='ticket-type'>{t('Issue type')}</Label>
            <NativeSelect
              id='ticket-type'
              value={type}
              onChange={(event) => setType(event.target.value)}
            >
              {Object.entries(ticketTypes).map(([value, label]) => (
                <NativeSelectOption value={value} key={value}>
                  {t(label)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            <Label htmlFor='ticket-priority'>{t('Priority')}</Label>
            <NativeSelect
              id='ticket-priority'
              value={priority}
              onChange={(event) => setPriority(event.target.value)}
            >
              {Object.entries(ticketPriorities).map(([value, label]) => (
                <NativeSelectOption value={value} key={value}>
                  {t(label)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            <Label htmlFor='ticket-title'>{t('Title')}</Label>
            <Input
              id='ticket-title'
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
            <Label htmlFor='ticket-body'>{t('Description')}</Label>
            <Textarea
              id='ticket-body'
              value={body}
              onChange={(event) => setBody(event.target.value)}
            />
            {related && (
              <p className='text-muted-foreground text-sm tabular-nums'>
                {related}
              </p>
            )}
            <Button
              type='button'
              disabled={pending || title.trim() === '' || body.trim() === ''}
              onClick={() => void create()}
            >
              {t('New ticket')}
            </Button>
          </>
        )}
        {error && <p className='text-destructive text-sm'>{error}</p>}
      </CardContent>
    </Card>
  )
}
