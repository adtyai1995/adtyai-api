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
import { useCallback, useEffect, useRef, useState } from 'react'
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
import { formatDateTimeObject } from '@/lib/time'
import { useAuthStore } from '@/stores/auth-store'

import {
  ticketDraftLink,
  ticketOperationKey,
  ticketRelations,
} from './ticket-draft'
import { TicketImage } from './ticket-image'
import {
  TicketImagePolicySettings,
  type TicketPolicy,
} from './ticket-image-policy'
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
  site_handoff: 'Transferred to main-site support',
  status: 'Status',
  priority: 'Priority',
  reopen: 'Reopen ticket',
  image_upload: 'Upload image',
  image_view: 'View image',
  image_delete: 'Delete image',
}

const TICKET_IMAGE_ACCEPT = 'image/png,image/jpeg,image/webp'
const ACCEPTED_TICKET_IMAGE_MIMES = new Set([
  'image/png',
  'image/jpeg',
  'image/webp',
])

function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.addEventListener(
      'load',
      () => {
        const value = String(reader.result || '')
        resolve(value.slice(value.indexOf(',') + 1))
      },
      { once: true }
    )
    reader.addEventListener(
      'error',
      () => reject(new Error('Only images are accepted')),
      { once: true }
    )
    reader.readAsDataURL(file)
  })
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
  related_at?: number
  team_shared?: boolean
  related_error?: string
  id: number
  initial_message_id?: number
  title: string
  status: string
  priority?: string
  assignee_id?: number
}

type TicketMessage = {
  author_id: number
  author_role?: string
  created_at: number
  english?: string
  translation_status?: string
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
  actions: string[]
  image_grants?: Record<number, string>
  image_grants_exp?: number
  ticket: Ticket
  assignees?: { id: number; username: string; display_name: string }[]
  messages: TicketMessage[]
  images?: TicketImage[]
  events?: TicketEvent[]
  can_note: boolean
  can_manage: boolean
}

type TicketList = {
  image_policy: TicketPolicy
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
  const createVersion = useRef(0)
  const [type, setType] = useState('other')
  const [priority, setPriority] = useState('normal')
  const [createFiles, setCreateFiles] = useState<{ id: string; file: File }[]>([])
  const [imageMessage, setImageMessage] = useState(0)
  const [audience, setAudience] = useState('personal')
  const [relatedKind, setRelatedKind] = useState('')
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState('')
  const attempts = useRef(new Map<string, { key: string; value: string }>())
  const headers = (action: string, value: string) => ({
    'Idempotency-Key': ticketOperationKey(attempts.current, action, value),
  })
  const isRoot = useAuthStore((state) => state.auth.user?.role === 100)
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
    queryKey: ['adty-tickets', userId, scope, queue, page, filter],
    retry: false,
    refetchInterval: 30_000,
    queryFn: async () => {
      const response = await api.get('/api/adty/tickets', {
        params: { scope, queue, q: filter, p: page, page_size: pageSize },
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
        imagePolicy: body.data?.image_policy,
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
  const draftKey = JSON.stringify([userId, scope, selected])
  const [drafts, setDrafts] = useState<
    Record<string, { reply?: string; note?: string }>
  >({})
  const reply = drafts[draftKey]?.reply ?? ''
  const note = drafts[draftKey]?.note ?? ''
  function updateDraft(field: 'reply' | 'note', value: string) {
    setDrafts((previous) => ({
      ...previous,
      [draftKey]: { ...previous[draftKey], [field]: value },
    }))
  }
  function clearSubmitted(field: 'reply' | 'note', submitted: string) {
    setDrafts((previous) =>
      previous[draftKey]?.[field] === submitted
        ? { ...previous, [draftKey]: { ...previous[draftKey], [field]: '' } }
        : previous
    )
  }
  const [assignee, setAssignee] = useState('')
  const [related, setRelated] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  const selectTicket = useCallback((id: number | null) => {
    setSelected(id)
    setAssignee('')
    setImageMessage(0)
    setError('')
  }, [])

  const requestRef = relatedKind === 'request' ? related.trim() : ''
  const requestAudience = useQuery({
    queryKey: ['ticket-request-audience', userId, requestRef],
    enabled: Boolean(requestRef && userId),
    retry: false,
    queryFn: async ({ signal }) => {
      const { data } = await api.get('/api/adty/tickets/request-audience', {
        params: { ref: requestRef },
        signal,
      })
      if (
        !data.success ||
        !['personal', 'team'].includes(data.data?.audience)
      ) {
        throw new Error(data.message || t('Failed to load data'))
      }
      return data.data.audience as 'personal' | 'team'
    },
  })
  const resolvedAudience = requestRef ? requestAudience.data : audience
  const relatedTickets = useQuery({
    queryKey: [
      'adty-tickets',
      userId,
      resolvedAudience,
      'related',
      relatedKind,
      related.trim(),
    ],
    enabled:
      workspace === 'personal' &&
      Boolean(relatedKind && related.trim() && resolvedAudience),
    retry: false,
    queryFn: async ({ signal }) => {
      const { data } = await api.get('/api/adty/tickets', {
        params: {
          scope: resolvedAudience,
          related_kind: relatedKind,
          related_ref: related.trim(),
        },
        signal,
      })
      if (!data.success) {
        throw new Error(data.message || t('Failed to load data'))
      }
      return (data.data?.tickets ?? []) as Ticket[]
    },
  })

  useEffect(() => {
    function readLink() {
      const hash = window.location.hash
      const match = /^#case-(\d+)(-team)?$/.exec(hash)
      const source = ticketDraftLink(hash)
      if (match) {
        if (workspace === 'personal') setScope(match[2] ? 'team' : 'personal')
        setPage(1)
        selectTicket(Number(match[1]))
      }
      if (source) {
        createVersion.current++
        setRelatedKind(source.kind)
        setRelated(source.ref)
        setType(
          source.kind === 'request' || source.kind === 'team_key'
            ? 'api'
            : 'billing'
        )
        setAudience(source.kind.startsWith('team_') ? 'team' : 'personal')
      }
      if (match || source) {
        document.querySelector('#tickets')?.scrollIntoView()
      }
    }
    readLink()
    window.addEventListener('hashchange', readLink)
    return () => window.removeEventListener('hashchange', readLink)
  }, [selectTicket, workspace])

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
    const submittedVersion = createVersion.current
    setPending(true)
    setError('')
    try {
      const draft = {
        type,
        title: title.trim(),
        body: body.trim(),
        priority,
        audience: resolvedAudience,
        related_kind: related.trim() ? relatedKind : '',
        related_ref: relatedKind ? related.trim() : '',
      }
      const response = await api.post('/api/adty/tickets', draft, {
        headers: headers('create', JSON.stringify(draft)),
      })
      const payload = response.data as {
        success?: boolean
        message?: string
        data?: Ticket
      }
      if (!payload.success || !payload.data) {
        throw new Error(payload.message || t('Failed to save'))
      }
      const createdTicket = payload.data
      const targetScope = resolvedAudience === 'team' ? 'team' : 'personal'
      const filesToUpload = createFiles.map((item) => item.file)
      attempts.current.delete('create')
      if (submittedVersion === createVersion.current) {
        setTitle('')
        setBody('')
        setRelated('')
        setRelatedKind('')
        setAudience('personal')
        setType('other')
        setPriority('normal')
        setCreateFiles([])
        if (ticketDraftLink(window.location.hash)) {
          window.history.replaceState(window.history.state, '', '#tickets')
        }
      }
      setPage(1)
      setQueue('')
      setScope(targetScope)
      selectTicket(createdTicket.id)
      const firstMessageId = createdTicket.initial_message_id ?? 0
      for (const file of filesToUpload) {
        const encoded = await readFileAsBase64(file)
        await postTicketImage(
          createdTicket.id,
          firstMessageId,
          'public',
          targetScope,
          encoded
        )
      }
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
      const closure =
        [...(detail.data?.events ?? [])]
          .reverse()
          .find(
            (event) =>
              event.action === 'status' &&
              ['closed', 'resolved'].includes(event.detail ?? '')
          )?.id ?? 0
      const response = await api.post(
        `/api/adty/tickets/${selected}/status`,
        {
          status,
          closure_event_id: status === 'reopen' ? closure : undefined,
          assignee_id: assigneeId ?? 0,
          priority: nextPriority,
        },
        {
          params: { scope },
          headers:
            status === 'reopen'
              ? headers(`reopen:${selected}`, String(closure))
              : undefined,
        }
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
        {
          params: { scope },
          headers: headers(
            `${draftKey}:reply`,
            JSON.stringify([selected, scope, reply.trim()])
          ),
        }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload?.success) {
        throw new Error(payload?.message || t('Failed to save'))
      }
      attempts.current.delete(`${draftKey}:reply`)
      clearSubmitted('reply', reply)
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
        {
          params: { scope },
          headers: headers(
            `${draftKey}:note`,
            JSON.stringify([selected, scope, note.trim()])
          ),
        }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload?.success) {
        throw new Error(payload?.message || t('Failed to save'))
      }
      attempts.current.delete(`${draftKey}:note`)
      clearSubmitted('note', note)
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const postTicketImage = async (
    ticketId: number,
    messageId: number,
    visibility: string | undefined,
    targetScope: string,
    encodedData: string
  ) => {
    const digest = await crypto.subtle.digest(
      'SHA-256',
      new TextEncoder().encode(encodedData)
    )
    const hash = Array.from(new Uint8Array(digest), (b) =>
      b.toString(16).padStart(2, '0')
    ).join('')
    const response = await api.post(
      `/api/adty/tickets/${ticketId}/images`,
      {
        data: encodedData,
        message_id: messageId,
        visibility: visibility || 'public',
      },
      {
        params: { scope: targetScope },
        headers: { 'Idempotency-Key': `image:${messageId}:${hash}` },
      }
    )
    const payload = response.data as { success?: boolean; message?: string }
    if (!payload?.success) {
      throw new Error(payload?.message || t('Only images are accepted'))
    }
  }

  const validateImageBatch = (
    incoming: File[],
    currentCount: number,
    currentTotalBytes: number
  ): string | null => {
    const policy = list.data?.imagePolicy
    const maxBytes = policy?.max_bytes ?? 0
    const maxCount = policy?.max_count ?? 0
    const maxTotal = policy?.max_total ?? 0
    if (maxCount > 0 && currentCount + incoming.length > maxCount) {
      return t('Image count exceeds the limit ({{limit}})', { limit: maxCount })
    }
    let nextTotal = currentTotalBytes
    for (const file of incoming) {
      if (file.type && !ACCEPTED_TICKET_IMAGE_MIMES.has(file.type)) {
        return t('Only PNG, JPEG and WebP images are accepted')
      }
      if (maxBytes > 0 && file.size > maxBytes) {
        return t('Image exceeds the size limit')
      }
      nextTotal += file.size
    }
    if (maxTotal > 0 && nextTotal > maxTotal) {
      return t('Total image size exceeds the limit')
    }
    return null
  }

  const uploadImages = async (files: File[]) => {
    if (selected === null || files.length === 0) return
    const existingImages = detail.data?.images ?? []
    const existingTotal = existingImages.reduce(
      (sum, img) => sum + (img.size ?? 0),
      0
    )
    const validationError = validateImageBatch(
      files,
      existingImages.length,
      existingTotal
    )
    if (validationError) {
      setError(validationError)
      return
    }
    setPending(true)
    setError('')
    try {
      const visibility = detail.data?.messages.find(
        (m) => m.id === imageMessage
      )?.visibility
      for (const file of files) {
        const encoded = await readFileAsBase64(file)
        await postTicketImage(selected, imageMessage, visibility, scope, encoded)
      }
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Only images are accepted')))
    } finally {
      setPending(false)
    }
  }

  const deleteImage = async (imageId: number) => {
    if (selected === null) return
    setPending(true)
    setError('')
    try {
      const response = await api.delete(
        `/api/adty/tickets/${selected}/images/${imageId}`,
        { params: { scope } }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload?.success) {
        throw new Error(payload?.message || t('Failed to save'))
      }
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  function changeQueue(next: TicketQueueId | '') {
    setQueue(next)
    setPage(1)
    selectTicket(null)
  }
  const badges = list.isSuccess ? list.data.badges : undefined
  const canManage = list.isSuccess && list.data.can_manage

  async function exportTickets() {
    setPending(true)
    setError('')
    try {
      const rows: Ticket[] = []
      let before = 0
      for (;;) {
        const response = await api.get('/api/adty/tickets', {
          params: {
            scope,
            queue,
            q: filter,
            p: 1,
            page_size: 100,
            before_id: before,
          },
        })
        if (!response.data.success) {
          throw new Error(response.data.message || t('Failed to load data'))
        }
        const batch = (response.data.data as TicketList).tickets ?? []
        rows.push(...batch)
        const last = batch.at(-1)
        if (batch.length < 100 || !last) break
        before = last.id
      }
      const url = URL.createObjectURL(
        new Blob([JSON.stringify(rows, null, 2)], { type: 'application/json' })
      )
      const link = document.createElement('a')
      link.href = url
      link.download = 'tickets.json'
      link.click()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to load data')))
    } finally {
      setPending(false)
    }
  }
  const allowed = (action: string) =>
    detail.data?.actions?.includes(action) === true
  const terminal =
    detail.data?.ticket.status === 'closed' ||
    detail.data?.ticket.status === 'resolved'
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
        {workspace === 'platform' && isRoot && list.data?.imagePolicy && (
          <TicketImagePolicySettings
            policy={list.data.imagePolicy}
            onSaved={refresh}
          />
        )}
        <form
          className='flex flex-wrap gap-2'
          onSubmit={(event) => {
            event.preventDefault()
            setFilter(search.trim())
            setPage(1)
            selectTicket(null)
          }}
        >
          <Input
            aria-label={t('Ticket or business number')}
            placeholder={t('Ticket or business number')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <Button type='submit' variant='outline'>
            {t('Search')}
          </Button>
          {canManage && (
            <Button
              type='button'
              variant='outline'
              disabled={pending}
              onClick={() => void exportTickets()}
            >
              {t('Export tickets')}
            </Button>
          )}
        </form>
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
                #{ticket.id} · {ticket.title} ·{' '}
                {t(ticketTypes[ticket.type] ?? 'Other')}
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
            {Object.entries({
              [t('Business reference')]: detail.data.ticket.related_ref,
              [t('Model')]: detail.data.ticket.related_model,
              [t('Group')]: detail.data.ticket.related_group,
              [t('Error')]: detail.data.ticket.related_error,
              [t('Request time')]:
                detail.data.ticket.related_at &&
                formatDateTimeObject(
                  new Date(detail.data.ticket.related_at * 1000)
                ),
            })
              .filter(([, value]) => value)
              .map(([label, value]) => (
                <p className='text-sm break-all' key={label}>
                  {label} · {value}
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
              <div key={message.id} className='grid gap-1 border-b py-2'>
                <p className='text-muted-foreground text-xs'>
                  #{message.id} ·{' '}
                  {t(
                    (
                      {
                        customer: 'Customer',
                        staff: 'Support staff',
                        support: 'Support staff',
                        owner: 'Site owner',
                        platform: 'Platform support',
                        platform_main: 'Platform support',
                      } as Record<string, string>
                    )[message.author_role ?? ''] ??
                      'Historical author role unconfirmed'
                  )}{' '}
                  #{message.author_id} ·{' '}
                  {formatDateTimeObject(new Date(message.created_at * 1000))}
                </p>
                <p className='text-sm whitespace-pre-wrap'>
                  {message.visibility === 'internal' &&
                    `${t('Internal note')} · `}
                  {message.body}
                </p>
                {message.english && (
                  <p lang='en' className='text-sm whitespace-pre-wrap'>
                    {message.english}
                  </p>
                )}
                {message.translation_status && !message.english && (
                  <p className='text-muted-foreground text-xs'>
                    {t('English is not ready')}
                  </p>
                )}
                {detail.data.images
                  ?.filter(
                    (image) =>
                      image.message_id === message.id &&
                      (detail.data.can_note || image.visibility === 'public')
                  )
                  .map((image) => {
                    const canDelete =
                      !terminal &&
                      (detail.data.can_manage ||
                        (image.visibility === 'public' &&
                          (detail.data.ticket.user_id === userId ||
                            message.author_id === userId)))
                    return (
                      <TicketImage
                        key={image.id}
                        ticketID={detail.data.ticket.id}
                        imageID={image.id}
                        grant={detail.data.image_grants?.[image.id]}
                        expiresAt={detail.data.image_grants_exp}
                        scope={scope}
                        userId={userId}
                        label={`${t('Image')} ${image.object_id || `#${image.id}`}`}
                        deleting={pending}
                        onDelete={
                          canDelete ? () => void deleteImage(image.id) : undefined
                        }
                      />
                    )
                  })}
              </div>
            ))}
            {detail.data.images
              ?.filter(
                (image) =>
                  !image.message_id &&
                  (detail.data.can_note || image.visibility === 'public')
              )
              .map((image) => {
                const canDelete =
                  !terminal &&
                  (detail.data.can_manage ||
                    (image.visibility === 'public' &&
                      detail.data.ticket.user_id === userId))
                return (
                  <TicketImage
                    key={image.id}
                    ticketID={detail.data.ticket.id}
                    imageID={image.id}
                    grant={detail.data.image_grants?.[image.id]}
                    expiresAt={detail.data.image_grants_exp}
                    scope={scope}
                    userId={userId}
                    label={`${t('Image')} ${image.object_id || `#${image.id}`}`}
                    deleting={pending}
                    onDelete={
                      canDelete ? () => void deleteImage(image.id) : undefined
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
                  detailLabel = event.detail
                  if (
                    TICKET_STATUS_LABELS[normalizeTicketStatus(event.detail)]
                  ) {
                    detailLabel = t(ticketStatusLabel(event.detail))
                  } else if (event.action === 'priority') {
                    detailLabel = t(
                      ticketPriorities[event.detail] ?? event.detail
                    )
                  }
                }
                return (
                  <p key={event.id} className='text-muted-foreground text-xs'>
                    {t(eventLabels[event.action] ?? event.action)}
                    {detailLabel ? ` · ${detailLabel}` : ''}
                  </p>
                )
              })}
            {detail.data.can_manage && allowed('reply') && (
              <NativeSelect
                aria-label={t('Common replies')}
                value=''
                onChange={(event) =>
                  updateDraft('reply', t(event.target.value))
                }
              >
                <NativeSelectOption value=''>
                  {t('Common replies')}
                </NativeSelectOption>
                {[
                  'Please share the request ID and time.',
                  'We are checking your order.',
                  'Please confirm whether the issue is resolved.',
                ].map((key) => (
                  <NativeSelectOption key={key} value={key}>
                    {t(key)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            )}
            {allowed('reply') && (
              <>
                <Textarea
                  disabled={terminal}
                  aria-label={t('Reply')}
                  value={reply}
                  onChange={(event) => updateDraft('reply', event.target.value)}
                />
                <Button
                  type='button'
                  disabled={terminal || pending || reply.trim() === ''}
                  onClick={() => void submitReply()}
                >
                  {t('Reply')}
                </Button>
              </>
            )}
            {allowed('note') && (
              <>
                <Textarea
                  aria-label={t('Internal note')}
                  value={note}
                  onChange={(event) => updateDraft('note', event.target.value)}
                />
                <Button
                  type='button'
                  variant='outline'
                  disabled={terminal || pending || note.trim() === ''}
                  onClick={() => void submitNote()}
                >
                  {t('Internal note')}
                </Button>
              </>
            )}
            {detail.data.can_manage && (
              <>
                {allowed('assign') && (
                  <>
                    <Label htmlFor='ticket-assignee'>
                      {t('Ticket assignee')}
                    </Label>
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
                          (handler) =>
                            handler.id === detail.data.ticket.assignee_id
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
                  </>
                )}
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
                  {allowed('assign') && (
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
                  )}
                </div>
              </>
            )}
            <div className='flex flex-wrap gap-2'>
              {Object.entries({
                pending_assignment: 'Ticket status pending assignment',
                in_progress: 'Ticket status in progress',
                waiting_user: 'Ticket status waiting user',
                waiting_internal: 'Ticket status waiting internal',
                resolve: detail.data.can_manage
                  ? 'Ticket status resolved'
                  : 'Confirm ticket resolved',
                close: 'Ticket status closed',
                reopen: detail.data.can_manage
                  ? 'Reopen ticket'
                  : 'Request ticket continue',
              })
                .filter(([action]) => allowed(action))
                .map(([action, label]) => (
                  <Button
                    key={action}
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() => void updateStatus(action)}
                  >
                    {t(label)}
                  </Button>
                ))}
            </div>
            <Label htmlFor='ticket-image-message'>
              {t('Attach image to message')}
            </Label>
            <NativeSelect
              id='ticket-image-message'
              disabled={terminal}
              value={imageMessage}
              onChange={(event) => setImageMessage(Number(event.target.value))}
            >
              <NativeSelectOption value={0}>
                {t('Select a message')}
              </NativeSelectOption>
              {detail.data.messages
                .filter((m) => m.author_id === userId)
                .map((m) => (
                  <NativeSelectOption value={m.id} key={m.id}>
                    #{m.id} ·{' '}
                    {m.visibility === 'internal' && `${t('Internal note')} · `}
                    {m.body.slice(0, 60)}
                  </NativeSelectOption>
                ))}
            </NativeSelect>
            <Label htmlFor='ticket-image'>
              {t('Image')} · {t('Upload limit (bytes)')}{' '}
              {list.data.imagePolicy?.max_bytes} ·{' '}
              {t('Images used {{used}}/{{max}}', {
                used: detail.data.images?.length ?? 0,
                max: list.data.imagePolicy?.max_count ?? 0,
              })}
            </Label>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Mask API keys, emails, verification codes and payment secrets before uploading. PNG, JPEG and WebP only.'
              )}
            </p>
            <Input
              id='ticket-image'
              type='file'
              multiple
              accept={TICKET_IMAGE_ACCEPT}
              disabled={terminal || pending || !imageMessage}
              onChange={(event) => {
                const files = [...(event.target.files ?? [])]
                event.target.value = ''
                if (files.length === 0 || selected === null) return
                void uploadImages(files)
              }}
            />
          </div>
        )}
        {workspace === 'personal' && (
          <div
            className='flex flex-col gap-2'
            onChangeCapture={() => {
              createVersion.current++
            }}
          >
            <Label htmlFor='ticket-audience'>{t('Ticket audience')}</Label>
            <NativeSelect
              id='ticket-audience'
              value={resolvedAudience ?? ''}
              disabled={
                type === 'account' ||
                Boolean(requestRef) ||
                relatedKind.startsWith('team_')
              }
              onChange={(e) => setAudience(e.target.value)}
            >
              {requestRef && !resolvedAudience && (
                <NativeSelectOption value=''>
                  {t(
                    requestAudience.isError
                      ? 'Failed to load data'
                      : 'Loading...'
                  )}
                </NativeSelectOption>
              )}
              <NativeSelectOption value='personal'>
                {t('Only me and authorized support')}
              </NativeSelectOption>
              {list.data?.canViewTeam && (
                <NativeSelectOption value='team'>
                  {t('My team and authorized support')}
                </NativeSelectOption>
              )}
            </NativeSelect>
            <Label htmlFor='ticket-type'>{t('Issue type')}</Label>
            <NativeSelect
              id='ticket-type'
              value={type}
              onChange={(event) => {
                setType(event.target.value)
                if (event.target.value === 'account') {
                  setAudience('personal')
                  setRelatedKind('')
                  setRelated('')
                }
              }}
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
            <Label htmlFor='ticket-related-kind'>{t('Related business')}</Label>
            <NativeSelect
              id='ticket-related-kind'
              value={relatedKind}
              onChange={(e) => {
                const kind = e.target.value
                setRelatedKind(kind)
                setRelated('')
                if (kind.startsWith('team_')) {
                  setAudience('team')
                  if (type === 'account') setType('billing')
                } else if (['topup', 'refund'].includes(kind)) {
                  setAudience('personal')
                }
              }}
            >
              <NativeSelectOption value=''>{t('None')}</NativeSelectOption>
              {Object.entries(ticketRelations)
                .filter(
                  ([kind]) =>
                    !kind.startsWith('team_') || list.data?.canViewTeam
                )
                .map(([kind, label]) => (
                  <NativeSelectOption key={kind} value={kind}>
                    {t(label)}
                  </NativeSelectOption>
                ))}
            </NativeSelect>
            {relatedKind && (
              <Input
                aria-label={t('Business reference')}
                value={related}
                onChange={(e) => setRelated(e.target.value)}
              />
            )}
            {Boolean(relatedTickets.data?.length) && (
              <div className='flex flex-col items-start gap-1'>
                <p>{t('Open tickets for this reference')}</p>
                {relatedTickets.data?.map((ticket) => (
                  <Button
                    type='button'
                    variant='link'
                    key={ticket.id}
                    onClick={() => {
                      setScope(ticket.team_shared ? 'team' : 'personal')
                      selectTicket(ticket.id)
                    }}
                  >
                    #{ticket.id} · {ticket.title}
                  </Button>
                ))}
              </div>
            )}
            <div className='flex flex-col gap-1'>
              <Label htmlFor='ticket-create-images'>
                {t('Attachments (optional)')}
                {list.data?.imagePolicy &&
                  ` · ${t('Images used {{used}}/{{max}}', {
                    used: createFiles.length,
                    max: list.data.imagePolicy.max_count,
                  })}`}
              </Label>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Mask API keys, emails, verification codes and payment secrets before uploading. PNG, JPEG and WebP only.'
                )}
              </p>
              <Input
                id='ticket-create-images'
                type='file'
                multiple
                accept={TICKET_IMAGE_ACCEPT}
                disabled={pending}
                onChange={(event) => {
                  const picked = [...(event.target.files ?? [])]
                  event.target.value = ''
                  if (picked.length === 0) return
                  const currentBytes = createFiles.reduce(
                    (sum, item) => sum + item.file.size,
                    0
                  )
                  const validationError = validateImageBatch(
                    picked,
                    createFiles.length,
                    currentBytes
                  )
                  if (validationError) {
                    setError(validationError)
                    return
                  }
                  setError('')
                  setCreateFiles((prev) => [
                    ...prev,
                    ...picked.map((file, i) => ({
                      id: `${Date.now()}-${i}-${Math.random().toString(36).slice(2)}`,
                      file,
                    })),
                  ])
                }}
              />
              {createFiles.length > 0 && (
                <div className='flex flex-col gap-1'>
                  {createFiles.map((item) => (
                    <div
                      key={item.id}
                      className='flex items-center justify-between gap-2 text-xs'
                    >
                      <span className='min-w-0 truncate'>{item.file.name}</span>
                      <Button
                        type='button'
                        size='sm'
                        variant='ghost'
                        disabled={pending}
                        onClick={() =>
                          setCreateFiles((prev) =>
                            prev.filter((entry) => entry.id !== item.id)
                          )
                        }
                      >
                        {t('Remove')}
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </div>
            <Button
              type='button'
              disabled={
                pending ||
                title.trim() === '' ||
                body.trim() === '' ||
                Boolean(relatedKind && !related.trim()) ||
                !resolvedAudience
              }
              onClick={() => void create()}
            >
              {t('New ticket')}
            </Button>
          </div>
        )}
        {error && <p className='text-destructive text-sm'>{error}</p>}
      </CardContent>
    </Card>
  )
}
