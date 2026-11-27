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
import { z } from 'zod'

import {
  getNotificationPlainLength,
  MAX_NOTIFICATION_PLAIN_LENGTH,
  MAX_NOTIFICATION_RAW_BYTES,
} from './rich-text'

export const ROOT_TARGET_AUDIENCES = [
  'main_users',
  'all_site_owners',
  'site_users',
  'main_teams',
] as const

export const OWNER_TARGET_AUDIENCES = ['site_users', 'site_teams'] as const

export const ALL_TARGET_AUDIENCES = [
  'main_users',
  'all_site_owners',
  'site_users',
  'main_teams',
  'site_teams',
] as const

export type NotificationTargetAudience = (typeof ALL_TARGET_AUDIENCES)[number]

export const LEGACY_NOTIFICATION_AUDIENCES = [
  'all_users',
  'all_sites',
  'site',
  'team',
  'user',
] as const

export type LegacyNotificationAudience =
  (typeof LEGACY_NOTIFICATION_AUDIENCES)[number]

export const PRESERVED_LEGACY_AUDIENCES = [
  'user',
  'team',
  'all_users',
] as const

export type PreservedLegacyAudience =
  (typeof PRESERVED_LEGACY_AUDIENCES)[number]

export const NOTIFICATION_AUDIENCES = [
  ...LEGACY_NOTIFICATION_AUDIENCES,
  ...ALL_TARGET_AUDIENCES,
  'multi',
  'all',
] as const

export const NOTIFICATION_CONTENT_FORMATS = [
  'html',
  'markdown',
  'text',
] as const
export type NotificationContentFormat =
  (typeof NOTIFICATION_CONTENT_FORMATS)[number]

const baseNotificationObject = z.object({
  title: z.string().trim().max(120, 'Invalid parameters'),
  content: z
    .string()
    .refine(
      (val) => new TextEncoder().encode(val).length <= MAX_NOTIFICATION_RAW_BYTES,
      'Notification content exceeds 32KB limit'
    ),
  content_format: z.enum(NOTIFICATION_CONTENT_FORMATS),
  kind: z.enum(['platform', 'system', 'maintenance', 'rules']),
  audience: z.enum(NOTIFICATION_AUDIENCES),
  target_audiences: z.array(z.enum(ALL_TARGET_AUDIENCES)),
  target_site_ids: z.array(z.number().int().positive()),
  target_site_id: z.number().int().nonnegative(),
  target_team_id: z.number().int().nonnegative(),
  target_user_id: z.number().int().nonnegative(),
  business: z.string().trim().max(200, 'Invalid parameters'),
  sync_telegram: z.boolean(),
  telegram_channel: z.string().trim().max(128, 'Invalid parameters'),
})

function refineNotificationInput(
  value: z.infer<typeof baseNotificationObject>,
  ctx: z.RefinementCtx,
  options: {
    mode?: 'root' | 'owner'
    draft: boolean
    preserveLegacyAudience?: boolean
  }
) {
  const plainLength = getNotificationPlainLength(value.content)
  if (!options.draft && plainLength === 0) {
    ctx.addIssue({
      code: 'custom',
      path: ['content'],
      message: 'Content is required',
    })
  }
  if (plainLength > MAX_NOTIFICATION_PLAIN_LENGTH) {
    ctx.addIssue({
      code: 'custom',
      path: ['content'],
      message: 'Content cannot exceed 2000 characters',
    })
  }

  const isPreservedLegacy =
    options.preserveLegacyAudience === true &&
    value.target_audiences.length === 0 &&
    (value.audience === 'user' ||
      value.audience === 'team' ||
      value.audience === 'all_users')

  if (!isPreservedLegacy && value.target_audiences.length === 0) {
    ctx.addIssue({
      code: 'custom',
      path: ['target_audiences'],
      message: 'Select at least one recipient group',
    })
  }

  if (
    !isPreservedLegacy &&
    options.mode === 'root' &&
    value.target_audiences.includes('site_users') &&
    value.target_site_ids.length === 0
  ) {
    ctx.addIssue({
      code: 'custom',
      path: ['target_site_ids'],
      message: 'Select at least one subsite',
    })
  }

  const supportsPrivateTelegram =
    options.mode === 'owner' ||
    value.audience === 'team' ||
    value.audience === 'user' ||
    value.target_audiences.length > 0
  if (
    value.sync_telegram &&
    !supportsPrivateTelegram &&
    value.telegram_channel === ''
  ) {
    ctx.addIssue({
      code: 'custom',
      path: ['telegram_channel'],
      message: 'Enter the Telegram channel',
    })
  }
}

export function createNotificationSchema(
  mode: 'root' | 'owner',
  draft = false,
  preserveLegacyAudience = false
) {
  const base = draft
    ? baseNotificationObject
    : baseNotificationObject.extend({
        title: z
          .string()
          .trim()
          .min(1, 'Title is required')
          .max(120, 'Invalid parameters'),
      })
  return base.superRefine((value, ctx) =>
    refineNotificationInput(value, ctx, { mode, draft, preserveLegacyAudience })
  )
}

export const notificationDraftSchema = baseNotificationObject.superRefine(
  (value, ctx) => refineNotificationInput(value, ctx, { draft: true })
)

export const notificationSchema = baseNotificationObject
  .extend({
    title: z
      .string()
      .trim()
      .min(1, 'Title is required')
      .max(120, 'Invalid parameters'),
  })
  .superRefine((value, ctx) =>
    refineNotificationInput(value, ctx, { draft: false })
  )

export type NotificationInput = z.infer<typeof notificationSchema>

export const notificationKindLabels: Record<NotificationInput['kind'], string> =
  {
    platform: 'Platform announcement',
    system: 'System notification',
    maintenance: 'Maintenance notification',
    rules: 'Operating rules update',
  }

export const notificationAudienceLabels: Record<string, string> = {
  all_users: 'All users',
  all_sites: 'All sites',
  site: 'Selected site',
  team: 'Specific team',
  user: 'Specific user',
  main_users: 'Main-site users',
  all_site_owners: 'All subsite owners',
  site_users: 'Subsite users',
  main_teams: 'Main-site teams',
  site_teams: 'Subsite team users',
}

export function deriveLegacyAudience(
  mode: 'root' | 'owner',
  siteId: number,
  targetAudiences: NotificationTargetAudience[],
  targetSiteIds: number[]
): {
  audience: LegacyNotificationAudience
  target_site_id: number
} {
  if (mode === 'owner') {
    return { audience: 'site', target_site_id: siteId }
  }
  if (
    targetAudiences.length === 1 &&
    targetAudiences[0] === 'site_users' &&
    targetSiteIds.length === 1
  ) {
    return { audience: 'site', target_site_id: targetSiteIds[0] }
  }
  if (
    targetAudiences.length === 1 &&
    targetAudiences[0] === 'all_site_owners'
  ) {
    return { audience: 'all_sites', target_site_id: 0 }
  }
  return { audience: 'all_users', target_site_id: 0 }
}

function parseTargetStringList(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value
      .filter((item): item is string => typeof item === 'string')
      .map((item) => item.trim())
      .filter(Boolean)
  }
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) return []
    try {
      const parsed: unknown = JSON.parse(trimmed)
      if (Array.isArray(parsed)) {
        return parsed
          .filter((item): item is string => typeof item === 'string')
          .map((item) => item.trim())
          .filter(Boolean)
      }
    } catch {
      return []
    }
  }
  return []
}

export function isPreservedLegacyNotification(
  row: {
    audience?: string
    target_audiences?: string[] | string
  } | null
): boolean {
  if (!row) return false
  if (parseTargetStringList(row.target_audiences).length > 0) return false
  return (
    row.audience === 'user' ||
    row.audience === 'team' ||
    row.audience === 'all_users'
  )
}

export function resolveLegacyTargetIds(
  row: {
    audience?: string
    target_site_id?: number
    target_site_name?: string
    target_team_id?: number
    target_user_id?: number
  } | null
): {
  target_site_id: number
  target_team_id: number
  target_user_id: number
} {
  if (!row) {
    return { target_site_id: 0, target_team_id: 0, target_user_id: 0 }
  }
  const targetSiteId = Math.max(0, row.target_site_id ?? 0)
  let targetTeamId = Math.max(0, row.target_team_id ?? 0)
  let targetUserId = Math.max(0, row.target_user_id ?? 0)
  const siteName = (row.target_site_name ?? '').trim()
  if (row.audience === 'team' && targetTeamId === 0) {
    const match = /^team:(\d+)$/.exec(siteName)
    if (match) targetTeamId = Number(match[1])
  }
  if (row.audience === 'user' && targetUserId === 0) {
    const match = /^user:(\d+)$/.exec(siteName)
    if (match) targetUserId = Number(match[1])
  }
  return {
    target_site_id: targetSiteId,
    target_team_id: targetTeamId,
    target_user_id: targetUserId,
  }
}

function parseTargetSiteIdList(value: unknown): number[] {
  let raw: unknown[] = []
  if (Array.isArray(value)) {
    raw = value
  } else if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) return []
    try {
      const parsed: unknown = JSON.parse(trimmed)
      if (Array.isArray(parsed)) {
        raw = parsed
      }
    } catch {
      return []
    }
  }
  const out: number[] = []
  for (const item of raw) {
    const id = typeof item === 'number' ? item : Number(item)
    if (Number.isInteger(id) && id > 0 && !out.includes(id)) {
      out.push(id)
    }
  }
  return out
}

export function resolveInitialTargets(
  mode: 'root' | 'owner',
  row: {
    audience?: string
    target_audiences?: string[] | string
    target_site_id?: number
    target_site_ids?: number[] | string
  } | null
): {
  target_audiences: NotificationTargetAudience[]
  target_site_ids: number[]
} {
  const allowed = new Set<string>(
    mode === 'root' ? ROOT_TARGET_AUDIENCES : OWNER_TARGET_AUDIENCES
  )
  const rawAudiences = parseTargetStringList(row?.target_audiences).filter(
    (item): item is NotificationTargetAudience => allowed.has(item)
  )

  const rawSiteIds = parseTargetSiteIdList(row?.target_site_ids)

  if (rawAudiences.length > 0) {
    let resolvedSiteIds = rawSiteIds
    if (
      resolvedSiteIds.length === 0 &&
      row?.target_site_id &&
      row.target_site_id > 0
    ) {
      resolvedSiteIds = [row.target_site_id]
    }
    return {
      target_audiences: rawAudiences,
      target_site_ids: resolvedSiteIds,
    }
  }

  if (!row) {
    return {
      target_audiences: [mode === 'root' ? 'main_users' : 'site_users'],
      target_site_ids: [],
    }
  }

  if (isPreservedLegacyNotification(row)) {
    return {
      target_audiences: [],
      target_site_ids: [],
    }
  }

  if (mode === 'owner') {
    return {
      target_audiences: [row.audience === 'team' ? 'site_teams' : 'site_users'],
      target_site_ids: [],
    }
  }

  if (row.audience === 'all_sites') {
    return { target_audiences: ['all_site_owners'], target_site_ids: [] }
  }
  if (row.audience === 'site') {
    return {
      target_audiences: ['site_users'],
      target_site_ids:
        row.target_site_id && row.target_site_id > 0 ? [row.target_site_id] : [],
    }
  }
  if (row.audience === 'team') {
    return { target_audiences: ['main_teams'], target_site_ids: [] }
  }
  return { target_audiences: ['main_users'], target_site_ids: [] }
}

const customerAudienceLabels: Record<string, string> = {
  all_users: 'All users',
  all_sites: 'All users',
  site: 'All users',
  main_users: 'All users',
  site_users: 'All users',
  team: 'Team',
  main_teams: 'Team',
  site_teams: 'Team',
  user: 'Specific user',
  all_site_owners: 'Announcement',
  all: 'All users',
  multi: 'All users',
}

export interface FormatNotificationRecipientsOptions {
  /**
   * White-label reader mode for end-user notification popovers/inboxes:
   * hides platform-subsite hierarchy and cross-site names/IDs.
   */
  readerMode?: 'admin' | 'customer'
}

export function formatNotificationRecipients(
  row: {
    audience?: string
    target_audiences?: string[] | string
    target_site_name?: string
    target_site_names?: string[]
    target_site_ids?: number[] | string
    target_team_id?: number
    target_user_id?: number
  },
  t: TFunction,
  options?: FormatNotificationRecipientsOptions
): string {
  const parsedAudiences = parseTargetStringList(row.target_audiences)
  if (options?.readerMode === 'customer') {
    if (parsedAudiences.length > 0) {
      const labels: string[] = []
      for (const aud of parsedAudiences) {
        const key = Object.hasOwn(customerAudienceLabels, aud)
          ? customerAudienceLabels[aud]
          : 'Announcement'
        const label = t(key)
        if (!labels.includes(label)) {
          labels.push(label)
        }
      }
      return labels.join(' · ')
    }
    if (!row.audience) return ''
    if (Object.hasOwn(customerAudienceLabels, row.audience)) {
      return t(customerAudienceLabels[row.audience])
    }
    return t('Announcement')
  }

  const parsedSiteIds = parseTargetSiteIdList(row.target_site_ids)
  if (parsedAudiences.length > 0) {
    const labels = parsedAudiences.map((aud) => {
      if (aud === 'site_users') {
        if (
          Array.isArray(row.target_site_names) &&
          row.target_site_names.length > 0
        ) {
          return `${t(notificationAudienceLabels.site_users)} (${row.target_site_names.join(', ')})`
        }
        if (row.target_site_name) {
          return `${t(notificationAudienceLabels.site_users)} (${row.target_site_name})`
        }
        if (parsedSiteIds.length > 0) {
          return `${t(notificationAudienceLabels.site_users)} (${parsedSiteIds.map((id) => `#${id}`).join(', ')})`
        }
      }
      const key = notificationAudienceLabels[aud]
      return key ? t(key) : aud
    })
    return labels.join(' · ')
  }

  if (row.audience === 'site') {
    return row.target_site_name || t(notificationAudienceLabels.site)
  }
  if (row.audience === 'team') {
    const teamId =
      row.target_team_id || (row.target_site_name ?? '').replace(/^team:/, '')
    return `${t(notificationAudienceLabels.team)} #${teamId}`
  }
  if (row.audience === 'user') {
    const userId =
      row.target_user_id || (row.target_site_name ?? '').replace(/^user:/, '')
    return `${t(notificationAudienceLabels.user)} #${userId}`
  }
  if (row.audience && Object.hasOwn(notificationAudienceLabels, row.audience)) {
    return t(notificationAudienceLabels[row.audience])
  }
  return row.audience ?? ''
}
