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
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, ImagePlus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

import { ticketRelations } from './ticket-draft'
import type { TicketPolicy } from './ticket-image-policy'
import {
  formatTicketFileSize,
  formatTicketNumber,
  TICKET_IMAGE_ACCEPT,
  TICKET_PRIORITIES,
  TICKET_TYPES,
  validateTicketImageBatch,
  type Ticket,
} from './ticket-types'

export type PendingCreateFile = { id: string; file: File }

export function TicketCreateDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  userId?: number
  canViewTeam: boolean
  imagePolicy?: TicketPolicy
  pending: boolean
  error: string
  onClearError: () => void
  onError: (message: string) => void
  title: string
  onTitleChange: (value: string) => void
  body: string
  onBodyChange: (value: string) => void
  type: string
  onTypeChange: (value: string) => void
  priority: string
  onPriorityChange: (value: string) => void
  audience: string
  onAudienceChange: (value: string) => void
  relatedKind: string
  onRelatedKindChange: (value: string) => void
  related: string
  onRelatedChange: (value: string) => void
  createFiles: PendingCreateFile[]
  onCreateFilesChange: (
    updater: (prev: PendingCreateFile[]) => PendingCreateFile[]
  ) => void
  onDraftMutated: () => void
  onJumpToTicket: (ticket: Ticket) => void
  onSubmit: (resolvedAudience: string) => Promise<void>
}) {
  const { t } = useTranslation()
  const [showRelatedSection, setShowRelatedSection] = useState(
    Boolean(props.relatedKind || props.related)
  )

  const requestRef = props.relatedKind === 'request' ? props.related.trim() : ''
  const requestAudience = useQuery({
    queryKey: ['ticket-request-audience', props.userId, requestRef],
    enabled: props.open && Boolean(requestRef && props.userId),
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
        throw createServerError(data, t('Failed to load data'))
      }
      return data.data.audience as 'personal' | 'team'
    },
  })

  const resolvedAudience = requestRef ? requestAudience.data : props.audience

  const relatedTickets = useQuery({
    queryKey: [
      'adty-tickets',
      props.userId,
      resolvedAudience,
      'related',
      props.relatedKind,
      props.related.trim(),
    ],
    enabled:
      props.open &&
      Boolean(props.relatedKind && props.related.trim() && resolvedAudience),
    retry: false,
    queryFn: async ({ signal }) => {
      const { data } = await api.get('/api/adty/tickets', {
        params: {
          scope: resolvedAudience,
          related_kind: props.relatedKind,
          related_ref: props.related.trim(),
        },
        signal,
      })
      if (!data.success) {
        throw createServerError(data, t('Failed to load data'))
      }
      return (data.data?.tickets ?? []) as Ticket[]
    },
  })

  const relatedExpanded =
    showRelatedSection || Boolean(props.relatedKind || props.related)

  const canSubmit =
    !props.pending &&
    props.title.trim() !== '' &&
    props.body.trim() !== '' &&
    !(props.relatedKind && !props.related.trim()) &&
    Boolean(resolvedAudience)

  return (
    <Dialog
      open={props.open}
      onOpenChange={(nextOpen) => {
        if (!props.pending) props.onOpenChange(nextOpen)
      }}
      title={t('Create ticket')}
      description={t(
        'Describe your issue clearly. Images only — mask API keys, passwords, and payment details.'
      )}
      contentClassName='sm:max-w-xl'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            className='rounded-full'
            disabled={props.pending}
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            variant='brand'
            className='rounded-full px-5'
            disabled={!canSubmit}
            onClick={() => {
              if (resolvedAudience) {
                void props.onSubmit(resolvedAudience)
              }
            }}
          >
            {props.pending ? t('Submitting...') : t('Create ticket')}
          </Button>
        </>
      }
    >
      <div
        className='flex flex-col gap-3.5'
        onChangeCapture={() => {
          props.onDraftMutated()
        }}
      >
        <div className='flex flex-col gap-1.5'>
          <Label htmlFor='ticket-title'>{t('Title')}</Label>
          <Input
            id='ticket-title'
            maxLength={120}
            placeholder={t('Briefly summarize your issue')}
            value={props.title}
            onChange={(event) => props.onTitleChange(event.target.value)}
          />
        </div>

        <div className='grid gap-3 sm:grid-cols-2'>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='ticket-type'>{t('Issue type')}</Label>
            <NativeSelect
              id='ticket-type'
              className='w-full'
              value={props.type}
              onChange={(event) => {
                props.onTypeChange(event.target.value)
                if (event.target.value === 'account') {
                  props.onAudienceChange('personal')
                  props.onRelatedKindChange('')
                  props.onRelatedChange('')
                }
              }}
            >
              {Object.entries(TICKET_TYPES).map(([value, label]) => (
                <NativeSelectOption value={value} key={value}>
                  {t(label)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='ticket-priority'>{t('Priority')}</Label>
            <NativeSelect
              id='ticket-priority'
              className='w-full'
              value={props.priority}
              onChange={(event) => props.onPriorityChange(event.target.value)}
            >
              {Object.entries(TICKET_PRIORITIES).map(([value, label]) => (
                <NativeSelectOption value={value} key={value}>
                  {t(label)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
        </div>

        {(props.canViewTeam ||
          Boolean(requestRef) ||
          props.relatedKind.startsWith('team_')) && (
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='ticket-audience'>{t('Ticket audience')}</Label>
            <NativeSelect
              id='ticket-audience'
              className='w-full'
              value={resolvedAudience ?? ''}
              disabled={
                props.type === 'account' ||
                Boolean(requestRef) ||
                props.relatedKind.startsWith('team_')
              }
              onChange={(event) => props.onAudienceChange(event.target.value)}
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
              {props.canViewTeam && (
                <NativeSelectOption value='team'>
                  {t('My team and authorized support')}
                </NativeSelectOption>
              )}
            </NativeSelect>
          </div>
        )}

        <div className='flex flex-col gap-1.5'>
          <Label htmlFor='ticket-body'>{t('Description')}</Label>
          <Textarea
            id='ticket-body'
            rows={4}
            placeholder={t(
              'Provide details, steps to reproduce, error messages, or request timestamps.'
            )}
            value={props.body}
            onChange={(event) => props.onBodyChange(event.target.value)}
          />
        </div>

        <div className='border-border rounded-xl border'>
          <button
            type='button'
            className='hover:bg-muted/40 flex w-full items-center justify-between px-3.5 py-2.5 text-left text-xs font-medium transition-colors'
            aria-expanded={relatedExpanded}
            onClick={() => setShowRelatedSection((prev) => !prev)}
          >
            <span>
              {t('Related business reference (optional)')}
              {props.relatedKind && props.related.trim()
                ? ` · ${props.related.trim()}`
                : ''}
            </span>
            <ChevronDown
              className={
                relatedExpanded
                  ? 'size-4 rotate-180 transition-transform'
                  : 'size-4 transition-transform'
              }
              aria-hidden='true'
            />
          </button>
          {relatedExpanded && (
            <div className='border-border flex flex-col gap-2.5 border-t px-3.5 py-3'>
              <div className='grid gap-2.5 sm:grid-cols-2'>
                <div className='flex flex-col gap-1'>
                  <Label htmlFor='ticket-related-kind' className='text-xs'>
                    {t('Related business')}
                  </Label>
                  <NativeSelect
                    id='ticket-related-kind'
                    className='w-full'
                    value={props.relatedKind}
                    onChange={(event) => {
                      const kind = event.target.value
                      props.onRelatedKindChange(kind)
                      props.onRelatedChange('')
                      if (kind.startsWith('team_')) {
                        props.onAudienceChange('team')
                        if (props.type === 'account') {
                          props.onTypeChange('billing')
                        }
                      } else if (['topup', 'refund'].includes(kind)) {
                        props.onAudienceChange('personal')
                      }
                    }}
                  >
                    <NativeSelectOption value=''>
                      {t('None')}
                    </NativeSelectOption>
                    {Object.entries(ticketRelations)
                      .filter(
                        ([kind]) =>
                          !kind.startsWith('team_') || props.canViewTeam
                      )
                      .map(([kind, label]) => (
                        <NativeSelectOption key={kind} value={kind}>
                          {t(label)}
                        </NativeSelectOption>
                      ))}
                  </NativeSelect>
                </div>
                {props.relatedKind && (
                  <div className='flex flex-col gap-1'>
                    <Label htmlFor='ticket-related-ref' className='text-xs'>
                      {t('Business reference')}
                    </Label>
                    <Input
                      id='ticket-related-ref'
                      aria-label={t('Business reference')}
                      value={props.related}
                      onChange={(event) =>
                        props.onRelatedChange(event.target.value)
                      }
                    />
                  </div>
                )}
              </div>
              {Boolean(relatedTickets.data?.length) && (
                <div className='bg-muted/40 flex flex-col items-start gap-1 rounded-lg p-2.5 text-xs'>
                  <p className='text-muted-foreground font-medium'>
                    {t('Open tickets for this reference')}
                  </p>
                  {relatedTickets.data?.map((ticket) => (
                    <Button
                      type='button'
                      variant='link'
                      size='sm'
                      className='h-auto p-0 text-xs'
                      key={ticket.id}
                      onClick={() => props.onJumpToTicket(ticket)}
                    >
                      {formatTicketNumber(ticket)} · {ticket.title}
                    </Button>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>

        <div className='border-border flex flex-col gap-2 rounded-xl border p-3.5'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <Label
              htmlFor='ticket-create-images'
              className='flex items-center gap-1.5 text-xs font-medium'
            >
              <ImagePlus className='size-3.5' aria-hidden='true' />
              {t('Attachments (optional)')}
            </Label>
            {props.imagePolicy && (
              <span className='text-muted-foreground text-xs tabular-nums'>
                {t('Images used {{used}}/{{max}}', {
                  used: props.createFiles.length,
                  max: props.imagePolicy.max_count,
                })}
              </span>
            )}
          </div>
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
            disabled={props.pending}
            onChange={(event) => {
              const picked = [...(event.target.files ?? [])]
              event.target.value = ''
              if (picked.length === 0) return
              const currentBytes = props.createFiles.reduce(
                (sum, item) => sum + item.file.size,
                0
              )
              const validationError = validateTicketImageBatch(
                picked,
                props.createFiles.length,
                currentBytes,
                props.imagePolicy,
                t
              )
              if (validationError) {
                props.onError(validationError)
                return
              }
              props.onClearError()
              props.onCreateFilesChange((prev) => [
                ...prev,
                ...picked.map((file, index) => ({
                  id: `${Date.now()}-${index}-${Math.random().toString(36).slice(2)}`,
                  file,
                })),
              ])
            }}
          />
          {props.createFiles.length > 0 && (
            <div className='border-border divide-border divide-y rounded-lg border'>
              {props.createFiles.map((item) => (
                <div
                  key={item.id}
                  className='flex items-center justify-between gap-2 px-3 py-1.5 text-xs'
                >
                  <div className='min-w-0 flex-1'>
                    <p className='truncate font-medium'>{item.file.name}</p>
                    <p className='text-muted-foreground tabular-nums'>
                      {formatTicketFileSize(item.file.size)}
                    </p>
                  </div>
                  <Button
                    type='button'
                    size='icon-xs'
                    variant='ghost'
                    disabled={props.pending}
                    aria-label={t('Remove')}
                    onClick={() =>
                      props.onCreateFilesChange((prev) =>
                        prev.filter((entry) => entry.id !== item.id)
                      )
                    }
                  >
                    <Trash2 className='size-3.5' aria-hidden='true' />
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>

        {props.error && (
          <p role='alert' className='text-destructive text-sm'>
            {props.error}
          </p>
        )}
      </div>
    </Dialog>
  )
}
