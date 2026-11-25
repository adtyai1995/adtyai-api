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
import type { TFunction } from 'i18next'

import dayjs from '@/lib/dayjs'
import { createServerError } from '@/lib/server-error-message'

import type { TicketPolicy } from './ticket-image-policy'
import { normalizeTicketStatus, type TicketBadges } from './ticket-status'

export type TicketScope = 'personal' | 'platform' | 'owner' | 'staff' | 'team'

export type Ticket = {
  id: number
  ticket_no?: string
  type: string
  user_id: number
  username?: string
  site_id: number
  team_id: number
  related_kind?: string
  related_ref?: string
  related_model?: string
  related_group?: string
  related_at?: number
  team_shared?: boolean
  related_error?: string
  initial_message_id?: number
  title: string
  status: string
  priority?: string
  assignee_id?: number
  assignee_username?: string
  created_at?: number
  updated_at?: number
}

export type TicketMessage = {
  author_id: number
  author_role?: string
  author_username?: string
  created_at: number
  english?: string
  translation_status?: string
  id: number
  visibility: string
  body: string
}

export type TicketImageItem = {
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
  can_delete?: boolean
}

export type TicketEvent = {
  id: number
  ticket_id?: number
  actor_id?: number
  actor_username?: string
  action: string
  detail?: string
  from_assignee_id?: number
  from_assignee_username?: string
  to_assignee_id?: number
  to_assignee_username?: string
  created_at?: number
}

export type TicketDetail = {
  actions: string[]
  image_grants?: Record<number, string>
  image_grants_exp?: number
  ticket: Ticket
  assignees?: { id: number; username: string; display_name: string }[]
  messages: TicketMessage[]
  images?: TicketImageItem[]
  events?: TicketEvent[]
  can_note: boolean
  can_manage: boolean
  can_control?: boolean
}

export type TicketList = {
  image_policy: TicketPolicy
  tickets?: Ticket[]
  total: number
  can_view_team: boolean
  can_note?: boolean
  can_manage?: boolean
  badges?: TicketBadges
}

export const TICKET_TYPES: Record<string, string> = {
  billing: 'Billing',
  api: 'API',
  account: 'Account',
  other: 'Other',
}

export const TICKET_PRIORITIES: Record<string, string> = {
  normal: 'Normal',
  important: 'Important',
  urgent: 'Urgent',
}

export const TICKET_EVENT_LABELS: Record<string, string> = {
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

export const TICKET_AUTHOR_ROLE_LABELS: Record<string, string> = {
  customer: 'Customer',
  staff: 'Support staff',
  support: 'Support staff',
  owner: 'Site owner',
  platform: 'Platform support',
  platform_main: 'Platform support',
  unconfirmed: 'Historical author role unconfirmed',
}

export const TICKET_COMMON_REPLIES = [
  'Please share the request ID and time.',
  'We are checking your order.',
  'Please confirm whether the issue is resolved.',
] as const

export const TICKET_STATUS_FILTER_OPTIONS = [
  { value: 'pending', labelKey: 'Ticket queue pending' },
  { value: 'in_progress', labelKey: 'Ticket status in progress' },
  { value: 'waiting_user', labelKey: 'Ticket status waiting user' },
  { value: 'waiting_internal', labelKey: 'Ticket status waiting internal' },
  { value: 'resolved', labelKey: 'Ticket status resolved' },
  { value: 'closed', labelKey: 'Ticket status closed' },
] as const

export const TICKET_IMAGE_ACCEPT = 'image/png,image/jpeg,image/webp'
export const ACCEPTED_TICKET_IMAGE_MIMES = new Set([
  'image/png',
  'image/jpeg',
  'image/webp',
])
export const ACCEPTED_TICKET_IMAGE_EXT = /\.(png|jpe?g|webp)$/i

export function formatTicketNumber(
  ticket: Pick<Ticket, 'id' | 'ticket_no'>
): string {
  const no = ticket.ticket_no?.trim()
  return no ? no : `#${ticket.id}`
}

export function formatTicketShortTime(timestamp?: number): string {
  if (!timestamp || timestamp <= 0) return ''
  return dayjs(timestamp * 1000).format('YYYY-MM-DD HH:mm')
}

export function ticketDisplayStatusKey(
  status: string,
  assigneeId = 0
): string {
  const normalized = normalizeTicketStatus(status, assigneeId)
  switch (normalized) {
    case 'new':
    case 'pending_assignment':
      return 'Ticket queue pending'
    case 'in_progress':
      return 'Ticket status in progress'
    case 'waiting_user':
      return 'Admin replied'
    case 'waiting_internal':
      return 'Ticket status waiting internal'
    case 'resolved':
      return 'Ticket status resolved'
    case 'closed':
      return 'Ticket status closed'
    default:
      return normalized
  }
}

export function ticketStatusPillClass(
  status: string,
  assigneeId = 0
): string {
  const normalized = normalizeTicketStatus(status, assigneeId)
  switch (normalized) {
    case 'new':
    case 'pending_assignment':
      return 'bg-brand text-brand-foreground border border-transparent'
    case 'in_progress':
      return 'bg-info-soft text-info border border-transparent'
    case 'waiting_user':
      return 'bg-brand-soft text-brand-ink border border-transparent'
    case 'waiting_internal':
      return 'bg-warning-soft text-warning border border-transparent'
    case 'resolved':
      return 'bg-success-soft text-success border border-transparent'
    case 'closed':
      return 'bg-background text-muted-foreground border border-border'
    default:
      return 'bg-muted text-muted-foreground border border-transparent'
  }
}

export function ticketPriorityPillClass(priority?: string): string {
  switch (priority) {
    case 'urgent':
      return 'bg-brand text-brand-foreground'
    case 'important':
      return 'bg-warning-soft text-warning'
    default:
      return 'bg-brand-soft text-brand-ink'
  }
}

export function parseInitialTicketHashFilters(
  hash: string,
  workspace: TicketScope
): { status: string; priority: string } {
  if (workspace === 'personal') {
    return { status: '', priority: '' }
  }
  const clean = hash.replace(/^#/, '')
  if (clean === 'urgent') {
    return { status: '', priority: 'urgent' }
  }
  if (
    [
      'pending',
      'in_progress',
      'waiting_user',
      'waiting_internal',
      'resolved',
      'closed',
    ].includes(clean)
  ) {
    return { status: clean, priority: '' }
  }
  return { status: '', priority: '' }
}

export function formatTicketFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`
}

export function readFileAsBase64(file: File): Promise<string> {
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
      () =>
        reject(
          createServerError({
            message: 'Only images are accepted',
            message_key: 'Only images are accepted',
          })
        ),
      { once: true }
    )
    reader.readAsDataURL(file)
  })
}

export function validateTicketImageBatch(
  incoming: File[],
  currentCount: number,
  currentTotalBytes: number,
  policy: TicketPolicy | undefined,
  t: TFunction
): string | null {
  const maxBytes = policy?.max_bytes ?? 0
  const maxCount = policy?.max_count ?? 0
  const maxTotal = policy?.max_total ?? 0
  if (maxCount > 0 && currentCount + incoming.length > maxCount) {
    return t('Image count exceeds the limit ({{limit}})', { limit: maxCount })
  }
  let nextTotal = currentTotalBytes
  for (const file of incoming) {
    const mime = file.type.toLowerCase()
    const mimeAllowed =
      mime !== ''
        ? ACCEPTED_TICKET_IMAGE_MIMES.has(mime)
        : ACCEPTED_TICKET_IMAGE_EXT.test(file.name)
    if (!mimeAllowed || /\.gif$/i.test(file.name)) {
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
