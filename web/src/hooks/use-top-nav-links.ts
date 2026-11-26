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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import type { TopNavLink } from '@/components/layout/types'
import { resolveDocsLink } from '@/hooks/use-docs-link'
import { useStatus } from '@/hooks/use-status'
import { parseHeaderNavModulesFromStatus } from '@/lib/nav-modules'
import { ROLE } from '@/lib/roles'
import { readReferralEnabledFromStatus } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

export type { TopNavLink } from '@/components/layout/types'

/**
 * Generate top navigation links based on HeaderNavModules configuration from backend /api/status
 * Backend format example (stringified JSON):
 * {
 *   home: true,
 *   console: true,
 *   pricing: { enabled: true, requireAuth: false },
 *   rankings: { enabled: true, requireAuth: false },
 *   docs: true,
 *   about: true
 * }
 */
export function useTopNavLinks(): TopNavLink[] {
  const { t } = useTranslation()
  const { status } = useStatus()
  const { auth } = useAuthStore()

  // Parse HeaderNavModules
  const modules = useMemo(() => {
    return parseHeaderNavModulesFromStatus(
      status as Record<string, unknown> | null
    )
  }, [status])

  // Documentation link (external URL, mounted static /docs/, or built-in /docs)
  const docsTarget = resolveDocsLink(
    status as Record<string, unknown> | null | undefined
  )

  const user = auth?.user
  const isAuthed = !!user
  const isAdmin = Boolean(user && user.role >= ROLE.ADMIN)
  const isSiteOwner = user?.adty_site_owner === true
  const isPureSiteOwner = isSiteOwner && !isAdmin
  const isStaff = (user?.staff_site_id ?? 0) > 0

  const links: TopNavLink[] = []

  // Home
  if (modules?.home !== false) {
    links.push({ id: 'home', title: t('Home'), href: '/' })
  }

  // Console -> /dashboard (or /site-owner/overview for pure site owners)
  if (modules?.console !== false) {
    links.push({
      id: 'console',
      title: t('Console'),
      href: isPureSiteOwner ? '/site-owner/overview' : '/dashboard',
    })
  }

  // Pricing
  const pricing = modules?.pricing
  if (pricing && typeof pricing === 'object' && pricing.enabled) {
    const requiresAuth = pricing.requireAuth && !isAuthed
    links.push({
      id: 'pricing',
      title: t('Model Square'),
      href: '/pricing',
      requiresAuth,
    })
  }

  // Channel Status (authenticated console entry for ordinary and admin users)
  if (
    isAuthed &&
    !isPureSiteOwner &&
    !isStaff &&
    modules?.model_status !== false
  ) {
    links.push({
      id: 'model-status',
      title: t('Channel Status'),
      href: '/model-status',
    })
  }

  // Docs (supports external links and mounted static /docs/)
  if (modules?.docs !== false) {
    links.push({
      id: 'docs',
      title: t('Docs'),
      href: docsTarget.href,
      ...(docsTarget.external ? { external: true } : {}),
      ...(docsTarget.reloadDocument ? { reloadDocument: true } : {}),
    })
  }

  // About
  if (modules?.about !== false) {
    links.push({ id: 'about', title: t('About'), href: '/about' })
  }

  // ADTY task 8.2: unknown nav keys default to visible, so no admin setting
  // is needed; HeaderNavModules {"affiliate": false} hides it.
  // R4-045: Hide the main-site affiliate program entry on subsites.
  // R-32 #21: Hide affiliate entry for signed-in admins and site owners.
  // R1-53: Gate /affiliate visibility on site-scoped adty_referral_enabled.
  const isSubsite = Boolean(status?.adty_site)
  const referralEnabled =
    readReferralEnabledFromStatus(status) ?? user?.adty_referral_enabled
  if (
    !isSubsite &&
    !isAdmin &&
    !isSiteOwner &&
    referralEnabled === true &&
    modules?.affiliate !== false
  ) {
    links.push({
      id: 'affiliate',
      title: t('Affiliate Program'),
      href: '/affiliate',
    })
  }

  return links
}
