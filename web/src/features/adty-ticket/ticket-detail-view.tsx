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
import {
  ArrowLeft,
  ChevronDown,
  ImagePlus,
  Lock,
  RefreshCw,
  Send,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { formatDateTimeObject } from '@/lib/time'
import { cn } from '@/lib/utils'

import { TicketPriorityPill, TicketStatusPill } from './ticket-badges'
import { TicketImage } from './ticket-image'
import type { TicketPolicy } from './ticket-image-policy'
import {
  normalizeTicketStatus,
  TICKET_STATUS_LABELS,
  ticketStatusLabel,
} from './ticket-status'
import {
  formatTicketFileSize,
  formatTicketNumber,
  formatTicketShortTime,
  TICKET_AUTHOR_ROLE_LABELS,
  TICKET_COMMON_REPLIES,
  TICKET_EVENT_LABELS,
  TICKET_IMAGE_ACCEPT,
  TICKET_PRIORITIES,
  TICKET_TYPES,
  type TicketDetail,
  type TicketScope,
} from './ticket-types'

export function TicketDetailView(props: {
  scope: TicketScope
  userId?: number
  detail: TicketDetail | undefined
  isPending: boolean
  isError: boolean
  isFetching: boolean
  imagePolicy?: TicketPolicy
  busy: boolean
  error: string
  reply: string
  note: string
  assignee: string
  effectiveImageMessage: number
  onBack: () => void
  onRefresh: () => void
  onReplyChange: (value: string) => void
  onNoteChange: (value: string) => void
  onAssigneeChange: (value: string) => void
  onImageMessageChange: (messageId: number) => void
  onSubmitReply: () => Promise<void>
  onSubmitNote: () => Promise<void>
  onUpdateStatus: (
    status: string,
    assigneeId?: number,
    nextPriority?: string
  ) => Promise<void>
  onUploadImages: (files: File[]) => Promise<void>
  onDeleteImage: (imageId: number) => Promise<void>
}) {
  const { t } = useTranslation()
  const [confirmDeleteImageId, setConfirmDeleteImageId] = useState<
    number | null
  >(null)
  const [eventsExpanded, setEventsExpanded] = useState(false)

  const detail = props.detail
  const allowed = (action: string) =>
    detail?.actions?.includes(action) === true
  const terminal =
    detail?.ticket.status === 'closed' || detail?.ticket.status === 'resolved'

  const authorMessages = (detail?.messages ?? []).filter(
    (message) => message.author_id === props.userId
  )

  const renderedMessageIds = new Set(
    (detail?.messages ?? []).map((message) => message.id)
  )
  const unattachedImages = (detail?.images ?? []).filter(
    (image) =>
      (!image.message_id || !renderedMessageIds.has(image.message_id)) &&
      (detail?.can_note || image.visibility === 'public')
  )

  const visibleEvents = (detail?.events ?? []).filter(
    (event) => event.action !== 'note' || detail?.can_note
  )

  const relatedEntries = detail
    ? Object.entries({
        [t('Business reference')]: detail.ticket.related_ref,
        [t('Model')]: detail.ticket.related_model,
        [t('Group')]: detail.ticket.related_group,
        [t('Error')]: detail.ticket.related_error,
        [t('Request time')]:
          detail.ticket.related_at &&
          formatDateTimeObject(new Date(detail.ticket.related_at * 1000)),
      }).filter(([, value]) => Boolean(value))
    : []

  const statusActionEntries = detail
    ? Object.entries({
        pending_assignment: 'Ticket status pending assignment',
        in_progress: 'Ticket status in progress',
        waiting_user: 'Ticket status waiting user',
        waiting_internal: 'Ticket status waiting internal',
        resolve: detail.can_manage
          ? 'Ticket status resolved'
          : 'Confirm ticket resolved',
        close: 'Ticket status closed',
        reopen: detail.can_manage ? 'Reopen ticket' : 'Request ticket continue',
      }).filter(([action]) => allowed(action))
    : []

  return (
    <div className='flex flex-col'>
      <div className='border-border flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3.5'>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='-ml-2 rounded-full'
          onClick={props.onBack}
        >
          <ArrowLeft className='size-4' aria-hidden='true' />
          {t('Back to ticket list')}
        </Button>
        <div className='flex flex-wrap items-center gap-2'>
          {detail && (
            <>
              <TicketPriorityPill priority={detail.ticket.priority} />
              <TicketStatusPill
                status={detail.ticket.status}
                assigneeId={detail.ticket.assignee_id}
              />
            </>
          )}
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='rounded-full px-3.5'
            disabled={props.isFetching}
            onClick={props.onRefresh}
          >
            <RefreshCw
              className={
                props.isFetching ? 'size-3.5 animate-spin' : 'size-3.5'
              }
              aria-hidden='true'
            />
            {t('Refresh')}
          </Button>
        </div>
      </div>

      {props.isPending && (
        <div className='p-6'>
          <LoadingState />
        </div>
      )}

      {props.isError && (
        <div className='p-6'>
          <ErrorState onRetry={props.onRefresh} />
        </div>
      )}

      {detail && (
        <div className='flex flex-col gap-5 p-5'>
          <div className='flex flex-col gap-2'>
            <h2 className='text-foreground text-lg leading-snug font-semibold break-words'>
              {detail.ticket.title}
            </h2>
            <div className='text-muted-foreground flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs'>
              <span className='font-mono'>
                {formatTicketNumber(detail.ticket)}
              </span>
              <span>·</span>
              <span>{t(TICKET_TYPES[detail.ticket.type] ?? 'Other')}</span>
              {detail.ticket.created_at ? (
                <>
                  <span>·</span>
                  <span className='tabular-nums'>
                    {formatTicketShortTime(detail.ticket.created_at)}
                  </span>
                </>
              ) : null}
              {detail.can_manage && (
                <>
                  <span>·</span>
                  <span>
                    {t('Submitter')}:{' '}
                    {detail.ticket.username || `#${detail.ticket.user_id}`}
                  </span>
                  {detail.ticket.assignee_id ? (
                    <>
                      <span>·</span>
                      <span>
                        {t('Ticket assignee')}:{' '}
                        {detail.ticket.assignee_username ||
                          `#${detail.ticket.assignee_id}`}
                      </span>
                    </>
                  ) : null}
                  <span>·</span>
                  <span>
                    {t('Site ID')} {detail.ticket.site_id}
                  </span>
                  {detail.ticket.team_id > 0 && (
                    <>
                      <span>·</span>
                      <span>
                        {t('Team ID')} {detail.ticket.team_id}
                      </span>
                    </>
                  )}
                </>
              )}
              {detail.ticket.team_shared && (
                <Badge variant='secondary' className='rounded-full text-[11px]'>
                  {t('Team tickets')}
                </Badge>
              )}
            </div>
          </div>

          {relatedEntries.length > 0 && (
            <dl className='bg-muted/30 border-border grid gap-2 rounded-xl border p-3.5 text-xs sm:grid-cols-2 lg:grid-cols-3'>
              {relatedEntries.map(([label, value]) => (
                <div key={label} className='flex flex-col gap-0.5'>
                  <dt className='text-muted-foreground'>{label}</dt>
                  <dd className='font-mono break-all'>{String(value)}</dd>
                </div>
              ))}
            </dl>
          )}

          {(detail.can_manage || statusActionEntries.length > 0) && (
            <div className='bg-muted/25 border-border flex flex-col gap-3 rounded-xl border p-4'>
              {detail.can_manage && (
                <div className='flex flex-wrap items-end gap-3'>
                  {allowed('assign') && (
                    <div className='flex min-w-[220px] flex-1 flex-wrap items-end gap-2'>
                      <div className='flex min-w-[180px] flex-1 flex-col gap-1'>
                        <Label htmlFor='ticket-assignee' className='text-xs'>
                          {t('Ticket assignee')}
                        </Label>
                        <NativeSelect
                          id='ticket-assignee'
                          className='w-full'
                          value={
                            props.assignee ||
                            String(detail.ticket.assignee_id || '')
                          }
                          onChange={(event) =>
                            props.onAssigneeChange(event.target.value)
                          }
                        >
                          <NativeSelectOption value=''>
                            {t('Unassigned')}
                          </NativeSelectOption>
                          {(detail.ticket.assignee_id ?? 0) > 0 &&
                            !detail.assignees?.some(
                              (handler) =>
                                handler.id === detail.ticket.assignee_id
                            ) && (
                              <NativeSelectOption
                                value={detail.ticket.assignee_id}
                                disabled
                              >
                                #{detail.ticket.assignee_id}
                              </NativeSelectOption>
                            )}
                          {detail.assignees?.map((handler) => (
                            <NativeSelectOption
                              key={handler.id}
                              value={handler.id}
                            >
                              {handler.display_name || handler.username} ·{' '}
                              {handler.username} (#{handler.id})
                            </NativeSelectOption>
                          ))}
                        </NativeSelect>
                      </div>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        className='rounded-full'
                        disabled={
                          props.busy || !/^\d+$/.test(props.assignee.trim())
                        }
                        onClick={() =>
                          void props.onUpdateStatus(
                            '',
                            Number(props.assignee.trim())
                          )
                        }
                      >
                        {detail.ticket.assignee_id
                          ? t('Transfer ticket')
                          : t('Assign ticket')}
                      </Button>
                    </div>
                  )}

                  <div className='flex flex-col gap-1'>
                    <Label
                      htmlFor='ticket-current-priority'
                      className='text-xs'
                    >
                      {t('Priority')}
                    </Label>
                    <NativeSelect
                      id='ticket-current-priority'
                      disabled={props.busy}
                      value={detail.ticket.priority || 'normal'}
                      onChange={(event) =>
                        void props.onUpdateStatus(
                          '',
                          undefined,
                          event.target.value
                        )
                      }
                    >
                      {Object.entries(TICKET_PRIORITIES).map(
                        ([value, label]) => (
                          <NativeSelectOption value={value} key={value}>
                            {t(label)}
                          </NativeSelectOption>
                        )
                      )}
                    </NativeSelect>
                  </div>
                </div>
              )}

              {statusActionEntries.length > 0 && (
                <div className='flex flex-wrap items-center gap-2'>
                  {statusActionEntries.map(([action, label]) => (
                    <Button
                      key={action}
                      type='button'
                      size='sm'
                      variant='outline'
                      className='rounded-full'
                      disabled={props.busy}
                      onClick={() => void props.onUpdateStatus(action)}
                    >
                      {t(label)}
                    </Button>
                  ))}
                </div>
              )}
            </div>
          )}

          <div className='flex flex-col gap-3'>
            {detail.messages.map((message) => {
              const isInternal = message.visibility === 'internal'
              const isSupport = Boolean(
                message.author_role && message.author_role !== 'customer'
              )
              const roleLabel = t(
                TICKET_AUTHOR_ROLE_LABELS[message.author_role ?? ''] ??
                  'Historical author role unconfirmed'
              )
              const messageImages = (detail.images ?? []).filter(
                (image) =>
                  image.message_id === message.id &&
                  (detail.can_note || image.visibility === 'public')
              )

              let cardTone = 'border-border bg-muted/20'
              if (isInternal) {
                cardTone = 'border-warning/35 bg-warning-soft/50'
              } else if (isSupport) {
                cardTone = 'border-brand/20 bg-brand-soft/40'
              }

              const authorLabel =
                message.author_username ||
                (message.author_id > 0 ? `#${message.author_id}` : '')

              return (
                <div
                  key={message.id}
                  className={cn(
                    'flex flex-col gap-2.5 rounded-xl border p-4',
                    cardTone
                  )}
                >
                  <div className='flex flex-wrap items-center justify-between gap-2 text-xs'>
                    <div className='flex flex-wrap items-center gap-2'>
                      <span className='text-foreground font-medium'>
                        {roleLabel}
                      </span>
                      {authorLabel && (
                        <span className='text-muted-foreground'>
                          {authorLabel}
                        </span>
                      )}
                      {isInternal && (
                        <Badge
                          variant='warning'
                          className='rounded-full text-[11px]'
                        >
                          <Lock className='size-3' aria-hidden='true' />
                          {t('Internal note')}
                        </Badge>
                      )}
                    </div>
                    <span className='text-muted-foreground tabular-nums'>
                      {formatDateTimeObject(new Date(message.created_at * 1000))}
                    </span>
                  </div>

                  <p className='text-foreground text-sm leading-relaxed break-words whitespace-pre-wrap'>
                    {message.body}
                  </p>

                  {message.english && (
                    <div className='border-border/70 border-t pt-2'>
                      <p
                        lang='en'
                        className='text-muted-foreground text-sm leading-relaxed break-words whitespace-pre-wrap'
                      >
                        {message.english}
                      </p>
                    </div>
                  )}

                  {message.translation_status && !message.english && (
                    <p className='text-muted-foreground text-xs'>
                      {t('English is not ready')}
                    </p>
                  )}

                  {messageImages.length > 0 && (
                    <div className='flex flex-wrap gap-4 pt-1'>
                      {messageImages.map((image) => {
                        const canDelete =
                          !terminal &&
                          (detail.can_manage ||
                            (image.visibility === 'public' &&
                              !isSupport &&
                              (detail.can_control ||
                                detail.ticket.user_id === props.userId ||
                                message.author_id === props.userId)))
                        return (
                          <TicketImage
                            key={image.id}
                            ticketID={detail.ticket.id}
                            imageID={image.id}
                            grant={detail.image_grants?.[image.id]}
                            expiresAt={detail.image_grants_exp}
                            scope={props.scope}
                            userId={props.userId}
                            label={`${t('Image')} ${image.object_id || `#${image.id}`}`}
                            deleting={props.busy}
                            onDelete={
                              canDelete
                                ? () => setConfirmDeleteImageId(image.id)
                                : undefined
                            }
                          />
                        )
                      })}
                    </div>
                  )}
                </div>
              )
            })}
          </div>

          {unattachedImages.length > 0 && (
            <div className='border-border flex flex-col gap-2.5 rounded-xl border p-4'>
              <p className='text-muted-foreground text-xs font-medium'>
                {t('Attachments')}
              </p>
              <div className='flex flex-wrap gap-4'>
                {unattachedImages.map((image) => {
                  const canDelete =
                    !terminal &&
                    (detail.can_manage ||
                      (image.visibility === 'public' &&
                        (detail.can_control ||
                          detail.ticket.user_id === props.userId)))
                  return (
                    <TicketImage
                      key={image.id}
                      ticketID={detail.ticket.id}
                      imageID={image.id}
                      grant={detail.image_grants?.[image.id]}
                      expiresAt={detail.image_grants_exp}
                      scope={props.scope}
                      userId={props.userId}
                      label={`${t('Image')} ${image.object_id || `#${image.id}`}`}
                      deleting={props.busy}
                      onDelete={
                        canDelete
                          ? () => setConfirmDeleteImageId(image.id)
                          : undefined
                      }
                    />
                  )
                })}
              </div>
            </div>
          )}

          {allowed('reply') && (
            <div className='border-border flex flex-col gap-3 rounded-xl border p-4'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <Label htmlFor='ticket-reply-input' className='text-sm font-medium'>
                  {t('Reply')}
                </Label>
                {detail.can_manage && (
                  <NativeSelect
                    aria-label={t('Common replies')}
                    size='sm'
                    value=''
                    onChange={(event) => {
                      if (event.target.value) {
                        props.onReplyChange(t(event.target.value))
                      }
                    }}
                  >
                    <NativeSelectOption value=''>
                      {t('Common replies')}
                    </NativeSelectOption>
                    {TICKET_COMMON_REPLIES.map((key) => (
                      <NativeSelectOption key={key} value={key}>
                        {t(key)}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                )}
              </div>
              <Textarea
                id='ticket-reply-input'
                rows={3}
                disabled={terminal}
                aria-label={t('Reply')}
                placeholder={t('Write a reply...')}
                value={props.reply}
                onChange={(event) => props.onReplyChange(event.target.value)}
              />
              <div className='flex justify-end'>
                <Button
                  type='button'
                  variant='brand'
                  className='rounded-full px-5'
                  disabled={terminal || props.busy || props.reply.trim() === ''}
                  onClick={() => void props.onSubmitReply()}
                >
                  <Send className='size-3.5' aria-hidden='true' />
                  {t('Reply')}
                </Button>
              </div>
            </div>
          )}

          {allowed('note') && (
            <div className='border-warning/35 bg-warning-soft/30 flex flex-col gap-3 rounded-xl border p-4'>
              <Label
                htmlFor='ticket-note-input'
                className='flex items-center gap-1.5 text-sm font-medium'
              >
                <Lock className='text-warning size-3.5' aria-hidden='true' />
                {t('Internal note')}
              </Label>
              <Textarea
                id='ticket-note-input'
                rows={2}
                aria-label={t('Internal note')}
                placeholder={t('Internal note visible only to support staff...')}
                value={props.note}
                onChange={(event) => props.onNoteChange(event.target.value)}
              />
              <div className='flex justify-end'>
                <Button
                  type='button'
                  variant='outline'
                  className='rounded-full px-4'
                  disabled={terminal || props.busy || props.note.trim() === ''}
                  onClick={() => void props.onSubmitNote()}
                >
                  {t('Internal note')}
                </Button>
              </div>
            </div>
          )}

          {!terminal && (
            <div className='border-border flex flex-col gap-2.5 rounded-xl border p-4'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <Label
                  htmlFor='ticket-image'
                  className='flex items-center gap-1.5 text-xs font-medium'
                >
                  <ImagePlus className='size-3.5' aria-hidden='true' />
                  {t('Upload image')}
                </Label>
                {props.imagePolicy && (
                  <span className='text-muted-foreground text-xs tabular-nums'>
                    {t('Images used {{used}}/{{max}}', {
                      used: detail.images?.length ?? 0,
                      max: props.imagePolicy.max_count,
                    })}{' '}
                    · {formatTicketFileSize(props.imagePolicy.max_bytes)}
                  </span>
                )}
              </div>
              <div className='grid gap-2.5 sm:grid-cols-2'>
                <div className='flex flex-col gap-1'>
                  <Label htmlFor='ticket-image-message' className='text-xs'>
                    {t('Attach image to message')}
                  </Label>
                  <NativeSelect
                    id='ticket-image-message'
                    className='w-full'
                    disabled={terminal}
                    value={props.effectiveImageMessage}
                    onChange={(event) =>
                      props.onImageMessageChange(Number(event.target.value))
                    }
                  >
                    <NativeSelectOption value={0}>
                      {t('Attach directly to ticket')}
                    </NativeSelectOption>
                    {authorMessages.map((message) => (
                      <NativeSelectOption value={message.id} key={message.id}>
                        #{message.id} ·{' '}
                        {message.visibility === 'internal' &&
                          `${t('Internal note')} · `}
                        {message.body.slice(0, 60)}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </div>
                <div className='flex flex-col gap-1'>
                  <Label htmlFor='ticket-image' className='text-xs'>
                    {t('Image')}
                  </Label>
                  <Input
                    id='ticket-image'
                    type='file'
                    multiple
                    accept={TICKET_IMAGE_ACCEPT}
                    disabled={terminal || props.busy}
                    onChange={(event) => {
                      const files = [...(event.target.files ?? [])]
                      event.target.value = ''
                      if (files.length === 0) return
                      void props.onUploadImages(files)
                    }}
                  />
                </div>
              </div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Mask API keys, emails, verification codes and payment secrets before uploading. PNG, JPEG and WebP only.'
                )}
              </p>
            </div>
          )}

          {visibleEvents.length > 0 && (
            <div className='border-border rounded-xl border'>
              <button
                type='button'
                className='hover:bg-muted/40 flex w-full items-center justify-between px-4 py-2.5 text-left text-xs font-medium transition-colors'
                aria-expanded={eventsExpanded}
                onClick={() => setEventsExpanded((prev) => !prev)}
              >
                <span>
                  {t('Activity history')} ({visibleEvents.length})
                </span>
                <ChevronDown
                  className={
                    eventsExpanded
                      ? 'size-4 rotate-180 transition-transform'
                      : 'size-4 transition-transform'
                  }
                  aria-hidden='true'
                />
              </button>
              {eventsExpanded && (
                <div className='border-border divide-border divide-y border-t px-4 py-2'>
                  {visibleEvents.map((event) => {
                    let detailLabel = ''
                    if (event.detail) {
                      detailLabel = event.detail
                      if (
                        TICKET_STATUS_LABELS[
                          normalizeTicketStatus(event.detail)
                        ]
                      ) {
                        detailLabel = t(ticketStatusLabel(event.detail))
                      } else if (event.action === 'priority') {
                        detailLabel = t(
                          TICKET_PRIORITIES[event.detail] ?? event.detail
                        )
                      }
                    }
                    return (
                      <div
                        key={event.id}
                        className='text-muted-foreground flex flex-wrap items-center justify-between gap-2 py-1.5 text-xs'
                      >
                        <span>
                          {t(TICKET_EVENT_LABELS[event.action] ?? event.action)}
                          {detailLabel ? ` · ${detailLabel}` : ''}
                        </span>
                        {event.created_at ? (
                          <span className='tabular-nums'>
                            {formatTicketShortTime(event.created_at)}
                          </span>
                        ) : null}
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {props.error && (
            <p role='alert' className='text-destructive text-sm'>
              {props.error}
            </p>
          )}
        </div>
      )}

      <ConfirmDialog
        open={confirmDeleteImageId !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmDeleteImageId(null)
        }}
        title={t('Delete image')}
        desc={t(
          'Are you sure you want to delete this attachment? This action cannot be undone.'
        )}
        confirmText={t('Delete image')}
        destructive
        isLoading={props.busy}
        handleConfirm={() => {
          if (confirmDeleteImageId === null) return
          const targetId = confirmDeleteImageId
          setConfirmDeleteImageId(null)
          void props.onDeleteImage(targetId)
        }}
      />
    </div>
  )
}
