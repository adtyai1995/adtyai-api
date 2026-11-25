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
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Download,
  LifeBuoy,
  Plus,
  RefreshCw,
  SlidersHorizontal,
  X,
} from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { api } from '@/lib/api'
import { formatUserIdentity } from '@/lib/format-user-identity'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { TicketPriorityPill, TicketStatusPill } from './ticket-badges'
import {
  TicketCreateDialog,
  type PendingCreateFile,
} from './ticket-create-dialog'
import { TicketDetailView } from './ticket-detail-view'
import { ticketDraftLink, ticketOperationKey } from './ticket-draft'
import { hashTicketImageData } from './ticket-hash'
import { TicketImagePolicySettings } from './ticket-image-policy'
import {
  formatTicketNumber,
  formatTicketShortTime,
  parseInitialTicketHashFilters,
  readFileAsBase64,
  TICKET_PRIORITIES,
  TICKET_STATUS_FILTER_OPTIONS,
  TICKET_TYPES,
  validateTicketImageBatch,
  type Ticket,
  type TicketDetail,
  type TicketList,
  type TicketScope,
} from './ticket-types'

const PAGE_SIZE = 20

export function TicketCenter(props: { scope?: TicketScope }) {
  const workspace = props.scope ?? 'personal'
  const userId = useAuthStore((state) => state.auth.user?.id)
  return (
    <TicketWorkspace
      key={`${userId}-${workspace}`}
      workspace={workspace}
      userId={userId}
    />
  )
}

function TicketWorkspace(props: { workspace: TicketScope; userId?: number }) {
  const { workspace, userId } = props
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isRoot = useAuthStore((state) => state.auth.user?.role === 100)
  const isAdminView = workspace !== 'personal'

  const initialHash = parseInitialTicketHashFilters(
    typeof window !== 'undefined' ? window.location.hash : '',
    workspace
  )

  const [scope, setScope] = useState<TicketScope>(workspace)
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<number | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [policyOpen, setPolicyOpen] = useState(false)

  // Admin filter controls
  const [searchInput, setSearchInput] = useState('')
  const [appliedSearch, setAppliedSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState(initialHash.status)
  const [priorityFilter, setPriorityFilter] = useState(initialHash.priority)
  const [typeFilter, setTypeFilter] = useState('')
  const [assigneeInput, setAssigneeInput] = useState('')
  const [appliedAssignee, setAppliedAssignee] = useState('')
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')

  // Create form state
  const createVersion = useRef(0)
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [type, setType] = useState('other')
  const [priority, setPriority] = useState('normal')
  const [audience, setAudience] = useState('personal')
  const [relatedKind, setRelatedKind] = useState('')
  const [related, setRelated] = useState('')
  const [createFiles, setCreateFiles] = useState<PendingCreateFile[]>([])

  // Detail state
  const [imageMessage, setImageMessage] = useState<number | null>(null)
  const [assignee, setAssignee] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  const attempts = useRef(new Map<string, { key: string; value: string }>())
  const idempotencyHeaders = (action: string, value: string) => ({
    'Idempotency-Key': ticketOperationKey(attempts.current, action, value),
  })

  // Debounce text filter inputs for admin view
  useEffect(() => {
    const timer = window.setTimeout(() => {
      const nextSearch = searchInput.trim()
      const nextAssignee = assigneeInput.trim()
      setAppliedSearch((prev) => {
        if (prev !== nextSearch) setPage(1)
        return nextSearch
      })
      setAppliedAssignee((prev) => {
        if (prev !== nextAssignee) setPage(1)
        return nextAssignee
      })
    }, 300)
    return () => window.clearTimeout(timer)
  }, [searchInput, assigneeInput])

  const selectTicket = useCallback((id: number | null) => {
    setSelected(id)
    setAssignee('')
    setImageMessage(null)
    setError('')
  }, [])

  // Handle deep-link hashes (#case-123, #case-123-team, #ticket-..., #ticket:kind:ref)
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
        if (workspace === 'personal') {
          setCreateOpen(true)
        }
      }
      if (match || source) {
        document.querySelector('#tickets')?.scrollIntoView()
      }
    }
    readLink()
    window.addEventListener('hashchange', readLink)
    return () => window.removeEventListener('hashchange', readLink)
  }, [selectTicket, workspace])

  const listParams = {
    scope,
    q: isAdminView && appliedSearch ? appliedSearch : undefined,
    status: isAdminView && statusFilter ? statusFilter : undefined,
    priority: isAdminView && priorityFilter ? priorityFilter : undefined,
    type: isAdminView && typeFilter ? typeFilter : undefined,
    assignee_id:
      isAdminView && /^\d+$/.test(appliedAssignee)
        ? appliedAssignee
        : undefined,
    date_from: isAdminView && dateFrom ? dateFrom : undefined,
    date_to: isAdminView && dateTo ? dateTo : undefined,
    p: page,
    page,
    page_size: PAGE_SIZE,
  }

  const list = useQuery({
    queryKey: ['adty-tickets', userId, listParams],
    retry: false,
    refetchInterval: 30_000,
    placeholderData: keepPreviousData,
    queryFn: async () => {
      const response = await api.get('/api/adty/tickets', {
        params: listParams,
      })
      const payload = response.data as {
        success?: boolean
        data?: TicketList
        message?: string
      }
      if (!payload.success) {
        throw createServerError(payload, t('Failed to load data'))
      }
      return {
        tickets: payload.data?.tickets ?? [],
        total: payload.data?.total ?? 0,
        imagePolicy: payload.data?.image_policy,
        canViewTeam: Boolean(payload.data?.can_view_team),
        can_note: Boolean(payload.data?.can_note),
        can_manage: Boolean(payload.data?.can_manage),
        badges: payload.data?.badges,
      }
    },
  })

  const detail = useQuery({
    queryKey: ['adty-tickets', userId, scope, 'detail', selected],
    enabled: selected !== null,
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
        throw createServerError(payload, t('Failed to load data'))
      }
      return payload.data
    },
  })

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

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['adty-tickets'] }),
      invalidateSidebarBadges(queryClient),
    ])
  }

  const postTicketImage = async (
    ticketId: number,
    messageId: number,
    visibility: string | undefined,
    targetScope: string,
    encodedData: string
  ) => {
    const hash = await hashTicketImageData(encodedData)
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
      throw createServerError(payload, t('Only images are accepted'))
    }
  }

  const createTicket = async (resolvedAudience: string) => {
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
        headers: idempotencyHeaders('create', JSON.stringify(draft)),
      })
      const payload = response.data as {
        success?: boolean
        message?: string
        data?: Ticket
      }
      if (!payload.success || !payload.data) {
        throw createServerError(payload, t('Failed to save'))
      }
      const createdTicket = payload.data
      const targetScope: TicketScope =
        resolvedAudience === 'team' ? 'team' : 'personal'
      const pendingItems = [...createFiles]
      attempts.current.delete('create')
      if (submittedVersion === createVersion.current) {
        setTitle('')
        setBody('')
        setRelated('')
        setRelatedKind('')
        setAudience('personal')
        setType('other')
        setPriority('normal')
        if (ticketDraftLink(window.location.hash)) {
          window.history.replaceState(window.history.state, '', '#tickets')
        }
      }
      setPage(1)
      setScope(targetScope)
      setCreateOpen(false)
      selectTicket(createdTicket.id)

      const firstMessageId = createdTicket.initial_message_id ?? 0
      const failedItems: PendingCreateFile[] = []
      let firstErr = ''
      for (const item of pendingItems) {
        try {
          const encoded = await readFileAsBase64(item.file)
          await postTicketImage(
            createdTicket.id,
            firstMessageId,
            'public',
            targetScope,
            encoded
          )
        } catch (cause) {
          failedItems.push(item)
          if (!firstErr) {
            firstErr = getServerErrorMessage(cause, t('Only images are accepted'))
          }
        }
      }
      if (submittedVersion === createVersion.current) {
        setCreateFiles(failedItems)
      }
      if (failedItems.length > 0) {
        setError(
          failedItems.length === 1 && pendingItems.length === 1
            ? firstErr
            : t('{{failed}} of {{total}} images failed to upload: {{reason}}', {
                failed: failedItems.length,
                total: pendingItems.length,
                reason: firstErr,
              })
        )
      }
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      await refresh()
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
              ? idempotencyHeaders(`reopen:${selected}`, String(closure))
              : undefined,
        }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload.success) {
        throw createServerError(payload, t('Failed to save'))
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
        { body: reply.trim() },
        {
          params: { scope },
          headers: idempotencyHeaders(
            `${draftKey}:reply`,
            JSON.stringify([selected, scope, reply.trim()])
          ),
        }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload?.success) {
        throw createServerError(payload, t('Failed to save'))
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
        { body: note.trim() },
        {
          params: { scope },
          headers: idempotencyHeaders(
            `${draftKey}:note`,
            JSON.stringify([selected, scope, note.trim()])
          ),
        }
      )
      const payload = response.data as { success?: boolean; message?: string }
      if (!payload?.success) {
        throw createServerError(payload, t('Failed to save'))
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

  const authorMessages = (detail.data?.messages ?? []).filter(
    (message) => message.author_id === userId
  )
  const latestAuthorMessageId = authorMessages.at(-1)?.id ?? 0
  let effectiveImageMessage = latestAuthorMessageId
  if (imageMessage !== null) {
    if (
      imageMessage === 0 ||
      authorMessages.some((message) => message.id === imageMessage)
    ) {
      effectiveImageMessage = imageMessage
    }
  }

  const uploadImages = async (files: File[]) => {
    if (selected === null || files.length === 0) return
    const existingImages = detail.data?.images ?? []
    const existingTotal = existingImages.reduce(
      (sum, image) => sum + (image.size ?? 0),
      0
    )
    const validationError = validateTicketImageBatch(
      files,
      existingImages.length,
      existingTotal,
      list.data?.imagePolicy,
      t
    )
    if (validationError) {
      setError(validationError)
      return
    }
    setPending(true)
    setError('')
    try {
      const targetMessageId = effectiveImageMessage
      const visibility =
        detail.data?.messages.find((message) => message.id === targetMessageId)
          ?.visibility ?? 'public'
      let failedCount = 0
      let firstErr = ''
      for (const file of files) {
        try {
          const encoded = await readFileAsBase64(file)
          await postTicketImage(
            selected,
            targetMessageId,
            visibility,
            scope,
            encoded
          )
        } catch (cause) {
          failedCount++
          if (!firstErr) {
            firstErr = getServerErrorMessage(cause, t('Only images are accepted'))
          }
        }
      }
      if (failedCount > 0) {
        setError(
          failedCount === 1 && files.length === 1
            ? firstErr
            : t('{{failed}} of {{total}} images failed to upload: {{reason}}', {
                failed: failedCount,
                total: files.length,
                reason: firstErr,
              })
        )
      }
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Only images are accepted')))
    } finally {
      await refresh()
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
        throw createServerError(payload, t('Failed to save'))
      }
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const exportTickets = async () => {
    setPending(true)
    setError('')
    try {
      const rows: Ticket[] = []
      let before = 0
      for (;;) {
        const response = await api.get('/api/adty/tickets', {
          params: {
            ...listParams,
            p: 1,
            page: 1,
            page_size: 100,
            before_id: before,
          },
        })
        if (!response.data.success) {
          throw createServerError(response.data, t('Failed to load data'))
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

  const hasActiveFilters = Boolean(
    searchInput.trim() ||
      statusFilter ||
      priorityFilter ||
      typeFilter ||
      assigneeInput.trim() ||
      dateFrom ||
      dateTo
  )

  const clearFilters = () => {
    setSearchInput('')
    setAppliedSearch('')
    setStatusFilter('')
    setPriorityFilter('')
    setTypeFilter('')
    setAssigneeInput('')
    setAppliedAssignee('')
    setDateFrom('')
    setDateTo('')
    setPage(1)
  }

  const tickets = list.data?.tickets ?? []
  const total = list.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const canManage = Boolean(list.data?.can_manage)
  const canViewTeam = Boolean(list.data?.canViewTeam)

  const selectPillClass =
    'w-auto min-w-[104px] [&_select]:h-9 [&_select]:rounded-full [&_select]:px-3.5 [&_select]:pr-8 [&_select]:text-sm'

  return (
    <div
      id='tickets'
      className='border-border bg-card overflow-hidden rounded-2xl border'
    >
      {selected !== null ? (
        <TicketDetailView
          key={selected}
          scope={scope}
          userId={userId}
          detail={detail.data}
          isPending={detail.isPending}
          isError={detail.isError}
          isFetching={detail.isFetching}
          imagePolicy={list.data?.imagePolicy}
          busy={pending}
          error={error}
          reply={reply}
          note={note}
          assignee={assignee}
          effectiveImageMessage={effectiveImageMessage}
          onBack={() => selectTicket(null)}
          onRefresh={() => void refresh()}
          onReplyChange={(value) => updateDraft('reply', value)}
          onNoteChange={(value) => updateDraft('note', value)}
          onAssigneeChange={setAssignee}
          onImageMessageChange={setImageMessage}
          onSubmitReply={submitReply}
          onSubmitNote={submitNote}
          onUpdateStatus={updateStatus}
          onUploadImages={uploadImages}
          onDeleteImage={deleteImage}
        />
      ) : (
        <div className='flex flex-col'>
          <div className='border-border flex flex-col gap-3.5 border-b px-5 py-4'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
              <div className='flex flex-wrap items-center gap-3'>
                <div className='flex items-center gap-2'>
                  <LifeBuoy
                    className='text-foreground size-4.5 shrink-0'
                    aria-hidden='true'
                  />
                  <h2 className='text-foreground text-[15px] font-semibold'>
                    {t('Ticket list')}
                  </h2>
                </div>
                {!isAdminView && canViewTeam && (
                  <div className='bg-muted inline-flex items-center rounded-full p-0.5'>
                    {(['personal', 'team'] as const).map((value) => (
                      <button
                        key={value}
                        type='button'
                        className={cn(
                          'rounded-full px-3 py-1 text-xs font-medium transition-colors',
                          scope === value
                            ? 'bg-background text-foreground shadow-xs'
                            : 'text-muted-foreground hover:text-foreground'
                        )}
                        onClick={() => {
                          setScope(value)
                          setPage(1)
                        }}
                      >
                        {value === 'personal'
                          ? t('My tickets')
                          : t('Team tickets')}
                      </button>
                    ))}
                  </div>
                )}
              </div>

              <div className='flex flex-wrap items-center gap-2'>
                {!isAdminView && (
                  <Button
                    type='button'
                    variant='brand'
                    size='sm'
                    className='h-8 rounded-full px-4 shadow-none'
                    onClick={() => {
                      setError('')
                      setCreateOpen(true)
                    }}
                  >
                    <Plus className='size-3.5' aria-hidden='true' />
                    {t('Create ticket')}
                  </Button>
                )}
                {workspace === 'platform' &&
                  isRoot &&
                  list.data?.imagePolicy && (
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      className='h-8 rounded-full px-3.5'
                      onClick={() => setPolicyOpen(true)}
                    >
                      <SlidersHorizontal
                        className='size-3.5'
                        aria-hidden='true'
                      />
                      {t('Ticket image limits')}
                    </Button>
                  )}
                {isAdminView && canManage && (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    className='h-8 rounded-full px-3.5'
                    disabled={pending}
                    onClick={() => void exportTickets()}
                  >
                    <Download className='size-3.5' aria-hidden='true' />
                    {t('Export')}
                  </Button>
                )}
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  className='h-8 rounded-full px-3.5'
                  disabled={list.isFetching}
                  onClick={() => void refresh()}
                >
                  <RefreshCw
                    className={
                      list.isFetching ? 'size-3.5 animate-spin' : 'size-3.5'
                    }
                    aria-hidden='true'
                  />
                  {t('Refresh')}
                </Button>
              </div>
            </div>

            {isAdminView && (
              <div className='flex flex-wrap items-center gap-2'>
                <Input
                  aria-label={t('Search ticket no., title, username')}
                  placeholder={t('Search ticket no., title, username')}
                  className='h-9 min-w-[200px] flex-1 rounded-full px-4'
                  value={searchInput}
                  onChange={(event) => setSearchInput(event.target.value)}
                />
                <NativeSelect
                  aria-label={t('Status')}
                  className={selectPillClass}
                  value={statusFilter}
                  onChange={(event) => {
                    setStatusFilter(event.target.value)
                    setPage(1)
                  }}
                >
                  <NativeSelectOption value=''>
                    {t('All statuses')}
                  </NativeSelectOption>
                  {TICKET_STATUS_FILTER_OPTIONS.map((option) => (
                    <NativeSelectOption key={option.value} value={option.value}>
                      {t(option.labelKey)}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <NativeSelect
                  aria-label={t('Priority')}
                  className={selectPillClass}
                  value={priorityFilter}
                  onChange={(event) => {
                    setPriorityFilter(event.target.value)
                    setPage(1)
                  }}
                >
                  <NativeSelectOption value=''>
                    {t('All priorities')}
                  </NativeSelectOption>
                  {Object.entries(TICKET_PRIORITIES).map(([value, label]) => (
                    <NativeSelectOption key={value} value={value}>
                      {t(label)}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <NativeSelect
                  aria-label={t('Type')}
                  className={selectPillClass}
                  value={typeFilter}
                  onChange={(event) => {
                    setTypeFilter(event.target.value)
                    setPage(1)
                  }}
                >
                  <NativeSelectOption value=''>
                    {t('All types')}
                  </NativeSelectOption>
                  {Object.entries(TICKET_TYPES).map(([value, label]) => (
                    <NativeSelectOption key={value} value={value}>
                      {t(label)}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <Input
                  aria-label={t('Assignee ID')}
                  placeholder={t('Assignee ID')}
                  inputMode='numeric'
                  className='h-9 w-28 rounded-full px-3.5 sm:w-32'
                  value={assigneeInput}
                  onChange={(event) =>
                    setAssigneeInput(event.target.value.replaceAll(/\D+/g, ''))
                  }
                />
                <Input
                  type='date'
                  aria-label={t('Start date')}
                  className='h-9 w-36 rounded-full px-3.5'
                  value={dateFrom}
                  onChange={(event) => {
                    setDateFrom(event.target.value)
                    setPage(1)
                  }}
                />
                <Input
                  type='date'
                  aria-label={t('End date')}
                  className='h-9 w-36 rounded-full px-3.5'
                  value={dateTo}
                  onChange={(event) => {
                    setDateTo(event.target.value)
                    setPage(1)
                  }}
                />
                {hasActiveFilters && (
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    className='text-muted-foreground h-9 rounded-full px-3'
                    onClick={clearFilters}
                  >
                    <X className='size-3.5' aria-hidden='true' />
                    {t('Reset')}
                  </Button>
                )}
              </div>
            )}
          </div>

          {error && (
            <div className='border-border border-b px-5 py-2.5'>
              <p role='alert' className='text-destructive text-sm'>
                {error}
              </p>
            </div>
          )}

          {list.isPending && (
            <div className='p-8'>
              <LoadingState />
            </div>
          )}

          {list.isError && (
            <div className='p-8'>
              <ErrorState onRetry={() => void list.refetch()} />
            </div>
          )}

          {!list.isPending && !list.isError && tickets.length === 0 && (
            <div className='py-12'>
              <EmptyState title={t('No tickets')} />
            </div>
          )}

          {!list.isPending && !list.isError && tickets.length > 0 && (
            <div className='divide-border divide-y'>
              {tickets.map((ticket) => {
                const timestampText = formatTicketShortTime(ticket.created_at)
                const submitterText =
                  ticket.user_id > 0 || ticket.username
                    ? formatUserIdentity(ticket.user_id, ticket.username)
                    : ''
                const assigneeText = ticket.assignee_id
                  ? formatUserIdentity(
                      ticket.assignee_id,
                      ticket.assignee_username
                    )
                  : ''

                return (
                  <button
                    key={ticket.id}
                    type='button'
                    className='hover:bg-muted/35 focus-visible:ring-ring flex w-full items-center justify-between gap-4 px-5 py-4 text-left transition-colors focus-visible:ring-2 focus-visible:outline-none focus-visible:ring-inset'
                    disabled={pending}
                    onClick={() => selectTicket(ticket.id)}
                  >
                    <div className='flex min-w-0 flex-1 flex-col gap-1.5'>
                      <p className='text-foreground truncate text-[15px] font-medium'>
                        {ticket.title}
                      </p>
                      <p className='text-muted-foreground truncate text-xs'>
                        <span className='font-mono'>
                          {formatTicketNumber(ticket)}
                        </span>
                        {' · '}
                        {t(TICKET_TYPES[ticket.type] ?? 'Other')}
                      </p>
                      <div className='text-muted-foreground flex flex-wrap items-center gap-2 text-xs'>
                        <TicketPriorityPill priority={ticket.priority} />
                        {timestampText && (
                          <span className='tabular-nums'>{timestampText}</span>
                        )}
                        {isAdminView && submitterText && (
                          <span>{submitterText}</span>
                        )}
                        {isAdminView && assigneeText && (
                          <span className='bg-muted rounded-full px-2 py-0.5 text-[11px]'>
                            {t('Ticket assignee')}: {assigneeText}
                          </span>
                        )}
                        {!isAdminView && ticket.team_shared && (
                          <span className='bg-muted rounded-full px-2 py-0.5 text-[11px]'>
                            {t('Team tickets')}
                          </span>
                        )}
                      </div>
                    </div>

                    <div className='shrink-0'>
                      <TicketStatusPill
                        status={ticket.status}
                        assigneeId={ticket.assignee_id}
                      />
                    </div>
                  </button>
                )
              })}
            </div>
          )}

          {!list.isError && (
            <div className='border-border flex flex-col gap-3 border-t px-5 py-4'>
              <p className='text-muted-foreground text-center text-xs tabular-nums'>
                {t('Page {{page}} of {{totalPages}}, {{total}} tickets total', {
                  page,
                  totalPages,
                  total,
                })}
              </p>
              <div className='grid grid-cols-2 gap-3'>
                <Button
                  type='button'
                  variant='outline'
                  className='text-muted-foreground h-9 w-full rounded-full'
                  disabled={page <= 1 || list.isFetching}
                  onClick={() => setPage((prev) => Math.max(1, prev - 1))}
                >
                  {t('Previous page')}
                </Button>
                <Button
                  type='button'
                  variant='outline'
                  className='text-muted-foreground h-9 w-full rounded-full'
                  disabled={page >= totalPages || list.isFetching}
                  onClick={() =>
                    setPage((prev) => Math.min(totalPages, prev + 1))
                  }
                >
                  {t('Next page')}
                </Button>
              </div>
            </div>
          )}
        </div>
      )}

      {!isAdminView && (
        <TicketCreateDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          userId={userId}
          canViewTeam={canViewTeam}
          imagePolicy={list.data?.imagePolicy}
          pending={pending}
          error={error}
          onClearError={() => setError('')}
          onError={setError}
          title={title}
          onTitleChange={setTitle}
          body={body}
          onBodyChange={setBody}
          type={type}
          onTypeChange={setType}
          priority={priority}
          onPriorityChange={setPriority}
          audience={audience}
          onAudienceChange={setAudience}
          relatedKind={relatedKind}
          onRelatedKindChange={setRelatedKind}
          related={related}
          onRelatedChange={setRelated}
          createFiles={createFiles}
          onCreateFilesChange={setCreateFiles}
          onDraftMutated={() => {
            createVersion.current++
          }}
          onJumpToTicket={(ticket) => {
            setCreateOpen(false)
            setScope(ticket.team_shared ? 'team' : 'personal')
            selectTicket(ticket.id)
          }}
          onSubmit={createTicket}
        />
      )}

      {workspace === 'platform' && isRoot && list.data?.imagePolicy && (
        <Dialog
          open={policyOpen}
          onOpenChange={setPolicyOpen}
          title={t('Ticket image limits')}
          contentClassName='sm:max-w-md'
        >
          <TicketImagePolicySettings
            embedded
            policy={list.data.imagePolicy}
            onSaved={async () => {
              await refresh()
              setPolicyOpen(false)
            }}
          />
        </Dialog>
      )}
    </div>
  )
}
