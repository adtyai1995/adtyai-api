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
*/
import { type QueryClient, useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

type ApiResponse<T> = { success: boolean; message?: string; data: T }

export const sidebarBadgeKeys = {
  all: ['adty-sidebar-badges'] as const,
}

export type SidebarBadgeCounts = Record<string, number>

export const CONFIRMED_SIDEBAR_BADGE_KINDS = [
  'notification_unread',
  'referral_partners',
  'referral_withdrawals_pending',
  'referral_withdrawals_approved',
  'site_withdrawals_pending',
  'invite_withdrawals_pending',
  'site_invite_commissions_pending_coverage',
  'referral_commissions_frozen',
  'fund_anomalies_open',
  'staff_balance_pending',
  'tickets_pending',
  'platform_tickets_pending',
  'owner_tickets_pending',
  'tickets_waiting_user',
  'payment_orphans_pending_review',
] as const

export const UNCONFIRMED_SIDEBAR_BADGE_KINDS = [
  'topup_orders_pending',
  'payment_exceptions_other',
  'team_notification_unread',
  'site_applications_pending',
  'site_invite_commissions_frozen',
  'site_withdrawals_paying',
  'manual_refunds_submitted',
] as const

export const REFERRAL_ADMIN_BADGE_KINDS = [
  'referral_partners',
  'referral_withdrawals_pending',
  'referral_withdrawals_approved',
  'referral_commissions_frozen',
] as const

export const ORDER_MANAGEMENT_BADGE_KINDS = [
  'payment_orphans_pending_review',
] as const

export const RESELLER_SITES_BADGE_KINDS = [
  'site_withdrawals_pending',
  'fund_anomalies_open',
  'staff_balance_pending',
] as const

export const SITE_OWNER_EARNINGS_BADGE_KINDS = [
  'invite_withdrawals_pending',
  'site_invite_commissions_pending_coverage',
] as const

export const SITE_OWNER_STAFF_BADGE_KINDS = ['staff_balance_pending'] as const

export const SITE_OWNER_BADGE_KINDS = [
  ...SITE_OWNER_EARNINGS_BADGE_KINDS,
  ...SITE_OWNER_STAFF_BADGE_KINDS,
] as const

export const TICKET_BADGE_KINDS = [
  'tickets_pending',
  'tickets_waiting_user',
] as const

export const PERSONAL_TICKET_BADGE_KINDS = ['tickets_waiting_user'] as const
export const OWNER_TICKET_BADGE_KINDS = ['owner_tickets_pending'] as const
export const PLATFORM_TICKET_BADGE_KINDS = ['platform_tickets_pending'] as const

export const STAFF_WORKSPACE_BADGE_KINDS = ['tickets_pending'] as const

function normalizeBadgeUrl(url: string): string {
  const withoutQuery = url.split('?')[0] ?? ''
  if (withoutQuery.length > 1) {
    return withoutQuery.replace(/\/+$/, '')
  }
  return withoutQuery
}

/**
 * Resolve confirmed sidebar badge keys owned by a sidebar link URL or route
 * pathname so clicking the link or landing on the route immediately clears its
 * badge (R-32 #6).
 */
export function getSidebarBadgeKeysForPath(
  pathname: string
): readonly string[] {
  const normalized = normalizeBadgeUrl(pathname)
  if (!normalized) return []
  if (normalized === '/site-staff' || normalized.startsWith('/site-staff/')) {
    return STAFF_WORKSPACE_BADGE_KINDS
  }
  if (
    normalized === '/support' ||
    normalized.startsWith('/support/') ||
    normalized === '/profile#tickets'
  ) {
    return PERSONAL_TICKET_BADGE_KINDS
  }
  if (
    normalized === '/site-owner/tickets' ||
    normalized.startsWith('/site-owner/tickets/')
  ) {
    return OWNER_TICKET_BADGE_KINDS
  }
  if (
    normalized === '/site-owner/earnings' ||
    normalized.startsWith('/site-owner/earnings/') ||
    normalized === '/site-owner/commissions' ||
    normalized.startsWith('/site-owner/commissions/')
  ) {
    return SITE_OWNER_EARNINGS_BADGE_KINDS
  }
  if (
    normalized === '/site-owner/staff' ||
    normalized.startsWith('/site-owner/staff/')
  ) {
    return SITE_OWNER_STAFF_BADGE_KINDS
  }
  if (
    normalized === '/admin-tickets' ||
    normalized.startsWith('/admin-tickets/')
  ) {
    return PLATFORM_TICKET_BADGE_KINDS
  }
  if (
    normalized === '/admin-referral' ||
    normalized.startsWith('/admin-referral/')
  ) {
    return REFERRAL_ADMIN_BADGE_KINDS
  }
  if (normalized === '/site-admin' || normalized.startsWith('/site-admin/')) {
    return RESELLER_SITES_BADGE_KINDS
  }
  if (
    normalized === '/recharge-audit' ||
    normalized.startsWith('/recharge-audit/')
  ) {
    return ORDER_MANAGEMENT_BADGE_KINDS
  }
  return []
}

/** Existing referral sidebar refetch. Not a newly signed official interval. */
export const SIDEBAR_BADGE_EXISTING_REFETCH_MS = 5 * 60_000

const BADGE_SEEN_DEDUP_MS = 1500
const lastSeenPostByUserKey = new Map<string, number>()

export async function getSidebarBadges(): Promise<SidebarBadgeCounts> {
  const res =
    await api.get<ApiResponse<{ counts: SidebarBadgeCounts }>>(
      '/api/adty/badges'
    )
  return requireServerSuccess(res.data).data.counts ?? {}
}

export function invalidateSidebarBadges(
  queryClient: QueryClient
): Promise<void> {
  return queryClient.invalidateQueries({ queryKey: sidebarBadgeKeys.all })
}

function zeroCachedSidebarBadges(
  queryClient: QueryClient,
  keys: readonly string[]
): void {
  queryClient.setQueriesData<SidebarBadgeCounts>(
    { queryKey: sidebarBadgeKeys.all },
    (previous) => {
      if (!previous) return previous
      let changed = false
      const next: SidebarBadgeCounts = { ...previous }
      for (const key of keys) {
        if (typeof next[key] === 'number' && next[key] > 0) {
          next[key] = 0
          changed = true
        }
      }
      return changed ? next : previous
    }
  )
}

/**
 * Optimistically clear sidebar badge keys in React Query cache and notify
 * POST /api/adty/badges/seen best-effort so opening a menu clears its badge
 * immediately (R-32 #6).
 */
export async function markSidebarBadgesSeen(
  queryClient: QueryClient,
  keys: readonly string[]
): Promise<void> {
  const uniqueKeys = [
    ...new Set(
      keys.filter((key) => typeof key === 'string' && key.trim() !== '')
    ),
  ]
  if (uniqueKeys.length === 0) return

  const userID = useAuthStore.getState().auth.user?.id ?? 0
  if (userID <= 0) return

  zeroCachedSidebarBadges(queryClient, uniqueKeys)

  const now = Date.now()
  const keysToPost = uniqueKeys.filter((key) => {
    const dedupKey = `${userID}:${key}`
    const last = lastSeenPostByUserKey.get(dedupKey)
    if (last !== undefined && now - last < BADGE_SEEN_DEDUP_MS) {
      return false
    }
    lastSeenPostByUserKey.set(dedupKey, now)
    return true
  })
  if (keysToPost.length === 0) return

  try {
    await api.post(
      '/api/adty/badges/seen',
      { keys: keysToPost },
      { skipErrorHandler: true }
    )
    zeroCachedSidebarBadges(queryClient, keysToPost)
    await invalidateSidebarBadges(queryClient)
  } catch {
    // Best-effort badge acknowledgement; keep UI silent on network failure.
  }
}

/**
 * Clear sidebar badges associated with a sidebar link URL or route pathname
 * when any mapped key is positive (or cache is still cold, or `force` is set).
 */
export function clearSidebarBadgesForPath(
  queryClient: QueryClient,
  pathname: string,
  force = false
): void {
  const userID = useAuthStore.getState().auth.user?.id ?? 0
  if (userID <= 0) return

  const keys = getSidebarBadgeKeysForPath(pathname)
  if (keys.length === 0) return

  if (!force) {
    const cached = queryClient.getQueryData<SidebarBadgeCounts>([
      ...sidebarBadgeKeys.all,
      userID,
    ])
    if (cached && !keys.some((key) => (cached[key] ?? 0) > 0)) {
      return
    }
  }

  void markSidebarBadgesSeen(queryClient, keys)
}

/**
 * Build the sidebar cue for one menu item. Different duties are never summed.
 * A failed or stale query must not be shown as a successful number.
 */
export function sidebarBadgeText(
  counts: SidebarBadgeCounts | undefined,
  kinds: readonly string[],
  options: { failed?: boolean; mixedLabel: string; failedLabel: string }
): string | undefined {
  if (options.failed) {
    return options.failedLabel
  }
  if (!counts) {
    return undefined
  }
  const values = kinds
    .map((kind) => counts[kind])
    .filter((value): value is number => typeof value === 'number' && value > 0)
  if (values.length === 0) {
    return undefined
  }
  if (values.length === 1) {
    return String(values[0])
  }
  return options.mixedLabel
}

export function useSidebarBadges(): {
  counts: SidebarBadgeCounts | undefined
  failed: boolean
  badge: (
    kinds: readonly string[],
    mixedLabel: string,
    failedLabel: string
  ) => string | undefined
} {
  const userID = useAuthStore((state) => state.auth.user?.id ?? 0)
  const query = useQuery({
    queryKey: [...sidebarBadgeKeys.all, userID],
    queryFn: getSidebarBadges,
    enabled: userID > 0,
    // Keep the existing referral sidebar cadence so that badge does not go
    // staler. The interval itself is not a newly signed official rule.
    refetchInterval: SIDEBAR_BADGE_EXISTING_REFETCH_MS,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
    retry: false,
    meta: { errorToast: false },
  })
  // LIST-012: after a failed refetch, cached rows must not be shown as success.
  const failed = query.isError
  const counts = failed || !query.isSuccess ? undefined : query.data
  return {
    counts,
    failed,
    badge: (kinds, mixedLabel, failedLabel) =>
      sidebarBadgeText(counts, kinds, { failed, mixedLabel, failedLabel }),
  }
}
