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
  Activity,
  Bell,
  Box,
  ClipboardList,
  CreditCard,
  FileSpreadsheet,
  FileText,
  Gift,
  HandCoins,
  Key,
  LayoutDashboard,
  LifeBuoy,
  ListTodo,
  Mail,
  Megaphone,
  PlugZap,
  Radio,
  ReceiptText,
  ServerCog,
  Settings,
  ShieldCheck,
  Store,
  Tags,
  Ticket,
  TrendingUp,
  User,
  UserCog,
  Users,
  UsersRound,
  Wallet,
} from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import type {
  NavLink,
  SidebarBadges,
  SidebarData,
} from '@/components/layout/types'
import {
  ORDER_MANAGEMENT_BADGE_KINDS,
  OWNER_TICKET_BADGE_KINDS,
  PERSONAL_TICKET_BADGE_KINDS,
  PLATFORM_TICKET_BADGE_KINDS,
  REFERRAL_ADMIN_BADGE_KINDS,
  RESELLER_SITES_BADGE_KINDS,
  SITE_OWNER_COMMISSIONS_BADGE_KINDS,
  STAFF_WORKSPACE_BADGE_KINDS,
  useSidebarBadges,
} from '@/hooks/use-sidebar-badges'
import { useStatus } from '@/hooks/use-status'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { ROLE } from '@/lib/roles'
import { readReferralEnabledFromStatus } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

/**
 * Root navigation groups for the application sidebar.
 *
 * These are shown when the URL does not match any nested sidebar view
 * registered in `layout/lib/sidebar-view-registry.ts`.
 */
export function useSidebarData(badges: SidebarBadges = {}): SidebarData {
  // ADTY task 8.2: titles and order follow the console mockup. Group ids are
  // unchanged because module switches and the admin-only filter key on them.
  const { t } = useTranslation()
  const sidebarBadges = useSidebarBadges()
  const mixedLabel = t('Sidebar badge pending work')
  const staffWorkspaceBadge = sidebarBadges.badge(
    STAFF_WORKSPACE_BADGE_KINDS,
    mixedLabel
  )
  const personalTicketsBadge = sidebarBadges.badge(
    PERSONAL_TICKET_BADGE_KINDS,
    mixedLabel
  )
  const ownerTicketsBadge = sidebarBadges.badge(
    OWNER_TICKET_BADGE_KINDS,
    mixedLabel
  )
  const platformTicketsBadge = sidebarBadges.badge(
    PLATFORM_TICKET_BADGE_KINDS,
    mixedLabel
  )
  const siteOwnerCommissionsBadge = sidebarBadges.badge(
    SITE_OWNER_COMMISSIONS_BADGE_KINDS,
    mixedLabel
  )
  const referralAdminBadge = sidebarBadges.badge(
    REFERRAL_ADMIN_BADGE_KINDS,
    mixedLabel
  )
  const reviewWork =
    (sidebarBadges.counts.referral_withdrawals_pending ?? 0) > 0
  const payoutWork =
    (sidebarBadges.counts.referral_withdrawals_approved ?? 0) > 0
  let withdrawalFilter: 'actionable' | 'pending' | 'approved' | undefined
  if (reviewWork && payoutWork) withdrawalFilter = 'actionable'
  else if (reviewWork) withdrawalFilter = 'pending'
  else if (payoutWork) withdrawalFilter = 'approved'
  const resellerSitesBadge = sidebarBadges.badge(
    RESELLER_SITES_BADGE_KINDS,
    mixedLabel
  )
  const orderManagementBadge = sidebarBadges.badge(
    ORDER_MANAGEMENT_BADGE_KINDS,
    mixedLabel
  )
  const platformTickets = useAuthStore(
    (state) =>
      canAdmin(state.auth.user, ADTY_PERMISSIONS.TICKET_MANAGE) ||
      canAdmin(state.auth.user, ADTY_PERMISSIONS.TICKET_MANAGE_ALL)
  )
  const showStandalonePaymentChannels = useAuthStore((state) => {
    const user = state.auth.user
    if (!user || user.role >= ROLE.SUPER_ADMIN) return false
    return (
      canAdmin(user, ADTY_PERMISSIONS.PAYMENT_READ) ||
      canAdmin(user, ADTY_PERMISSIONS.PAYMENT_OPERATE)
    )
  })
  const userRole = useAuthStore((state) => state.auth.user?.role ?? ROLE.GUEST)
  const isAdmin = userRole >= ROLE.ADMIN
  const hasArchivedSite = useAuthStore(
    (state) => state.auth.user?.adty_archived_site_owner === true
  )
  const { status } = useStatus()
  const isSiteOwner = useAuthStore(
    (state) => state.auth.user?.adty_site_owner === true
  )
  const userReferralEnabled = useAuthStore(
    (state) => state.auth.user?.adty_referral_enabled
  )
  const hasLegacySiteCommissions = useAuthStore(
    (state) => state.auth.user?.adty_has_legacy_site_commissions === true
  )
  const referralEnabled =
    (readReferralEnabledFromStatus(status) ?? userReferralEnabled) === true
  const isPureSiteOwner = isSiteOwner && !isAdmin
  const isStaff = useAuthStore(
    (state) => (state.auth.user?.staff_site_id ?? 0) > 0
  )
  const showReferral =
    !isAdmin &&
    !isSiteOwner &&
    !isStaff &&
    (referralEnabled || hasLegacySiteCommissions)
  const hasTeam = useAuthStore(
    (state) => state.auth.user?.adty_has_team === true
  )

  return useMemo(() => {
    if (isStaff) {
      return {
        navGroups: [
          {
            id: 'personal',
            title: t('Account'),
            items: [
              {
                title: t('Staff workspace'),
                url: '/site-staff',
                icon: Users,
                badge: staffWorkspaceBadge,
              },
            ],
          },
        ],
      }
    }

    const siteOwnerManagementItems: NavLink[] = [
      {
        title: t('Site Overview'),
        url: '/site-owner/overview',
        icon: LayoutDashboard,
      },
      {
        title: t('Customers'),
        url: '/site-owner/customers',
        icon: Users,
      },
      {
        title: t('Team management'),
        url: '/site-owner/teams',
        icon: UsersRound,
      },
      {
        title: t('Order management'),
        url: '/site-owner/orders',
        icon: ReceiptText,
      },
      {
        title: t('Pricing & Multiplier'),
        url: '/site-owner/pricing',
        icon: Tags,
      },
      {
        title: t('Recharge Bonus'),
        url: '/site-owner/bonus',
        icon: Gift,
      },
      {
        title: t('Promotion Management'),
        url: '/site-owner/commissions',
        icon: Megaphone,
        badge: siteOwnerCommissionsBadge,
      },
      {
        title: t('Earnings Details'),
        url: '/site-owner/earnings',
        icon: TrendingUp,
      },
      {
        title: t('Earnings Withdrawals'),
        url: '/site-owner/withdrawals',
        icon: HandCoins,
      },
      {
        title: t('Settlement Sheets'),
        url: '/site-owner/settlements',
        icon: FileSpreadsheet,
      },
      {
        title: t('Owner Wallets'),
        url: '/site-owner/wallets',
        icon: Wallet,
      },
      {
        title: t('Redemption Codes'),
        url: '/site-owner/vouchers',
        icon: Ticket,
      },
      {
        title: t('Notifications'),
        url: '/site-owner/notices',
        icon: Bell,
      },
      {
        title: t('Staff accounts'),
        url: '/site-owner/staff',
        icon: UserCog,
      },
      {
        title: t('Ticket management'),
        url: '/site-owner/tickets',
        icon: LifeBuoy,
        badge: ownerTicketsBadge,
      },
      {
        title: t('Email configuration'),
        url: '/site-owner/mail',
        icon: Mail,
      },
      {
        title: t('Site settings'),
        url: '/site-owner/settings',
        icon: Settings,
      },
    ]

    // R-32 #25 & R-33 #1: Site owners (non-admin) get a pure management sidebar only.
    if (isPureSiteOwner) {
      return {
        navGroups: [
          {
            id: 'site-owner-management',
            title: t('Site Management'),
            items: siteOwnerManagementItems,
          },
          {
            id: 'site-owner-account',
            title: t('Account'),
            items: [
              ...(hasArchivedSite
                ? [
                    {
                      title: t('Historical site accounts'),
                      url: '/site-owner/history',
                      icon: Store,
                    },
                  ]
                : []),
              {
                title: t('Security & Access'),
                url: '/security',
                icon: ShieldCheck,
              },
            ],
          },
        ],
      }
    }

    return {
      navGroups: [
        {
          id: 'general',
          title: t('Console'),
          items: [
            {
              title: t('Overview'),
              url: '/dashboard/overview',
              icon: Activity,
            },
            {
              title: t('Dashboard'),
              url: '/dashboard/models',
              icon: LayoutDashboard,
              requiredPermission: ADTY_PERMISSIONS.LOG_READ,
            },
            {
              title: t('Channel Status'),
              url: '/model-status',
              icon: Activity,
            },
            {
              title: t('API Keys'),
              url: '/keys',
              icon: Key,
            },
            {
              title: t('Usage Logs'),
              url: '/usage-logs/common',
              icon: FileText,
            },
            {
              title: t('Audit Logs'),
              url: '/usage-logs/audit',
              icon: ClipboardList,
            },
            {
              title: t('Task Logs'),
              url: '/usage-logs/task',
              icon: ListTodo,
            },
          ],
        },
        {
          id: 'personal',
          title: t('Account'),
          items: [
            ...(hasTeam
              ? [
                  {
                    title: t('My team'),
                    url: '/my-team',
                    icon: Users,
                  },
                ]
              : []),
            {
              title: t('Wallet'),
              url: '/wallet',
              icon: Wallet,
            },
            {
              title: t('Support Center'),
              url: '/support',
              icon: LifeBuoy,
              badge: personalTicketsBadge,
            },
            ...(showReferral
              ? [
                  {
                    title: t('Referral'),
                    url: '/referral',
                    icon: Gift,
                  },
                ]
              : []),
            ...(hasArchivedSite
              ? [
                  {
                    title: t('Historical site accounts'),
                    url: '/site-owner/history',
                    icon: Store,
                  },
                ]
              : []),
            {
              title: t('Profile'),
              url: '/profile',
              icon: User,
            },
            {
              title: t('Security & Access'),
              url: '/security',
              icon: ShieldCheck,
            },
          ],
        },
        ...(isSiteOwner
          ? [
              {
                id: 'site-owner-management',
                title: t('Site Management'),
                items: siteOwnerManagementItems,
              },
            ]
          : []),
        {
          id: 'admin',
          title: t('Administration'),
          items: [
            ...(platformTickets
              ? [
                  {
                    title: t('Ticket management'),
                    url: '/admin-tickets',
                    icon: LifeBuoy,
                    badge: platformTicketsBadge,
                  },
                ]
              : []),
            {
              title: t('Channels'),
              url: '/channels',
              icon: Radio,
              requiredPermission: ADTY_PERMISSIONS.CHANNEL_READ,
            },
            {
              title: t('Models'),
              url: '/models/metadata',
              icon: Box,
              requiredPermission: ADTY_PERMISSIONS.MODEL_READ,
            },
            {
              title: t('Users'),
              url: '/users',
              icon: Users,
              requiredPermission: ADTY_PERMISSIONS.USER_READ,
            },
            {
              title: t('Referral admin'),
              url: '/admin-referral',
              icon: HandCoins,
              badge: referralAdminBadge,
              linkOptions: withdrawalFilter
                ? {
                    to: '/admin-referral',
                    search: { withdrawals: withdrawalFilter },
                  }
                : undefined,
              requiredPermission: ADTY_PERMISSIONS.REFERRAL_READ,
            },
            {
              title: t('Team management'),
              url: '/admin-teams',
              icon: Users,
              requiredRole: ROLE.SUPER_ADMIN,
            },
            {
              title: t('Reseller sites'),
              url: '/site-admin/sites',
              activeUrls: ['/site-admin'],
              icon: Store,
              badge: resellerSitesBadge,
              requiredPermission: ADTY_PERMISSIONS.SITE_READ,
            },
            {
              title: t('Order management'),
              url: '/recharge-audit',
              icon: ReceiptText,
              badge: orderManagementBadge,
              requiredPermission: ADTY_PERMISSIONS.FINANCE_READ,
            },
            ...(showStandalonePaymentChannels
              ? [
                  {
                    title: t('Payment channels'),
                    url: '/payment-channels',
                    icon: CreditCard,
                  },
                ]
              : []),
            {
              title: t('Recharge Bonus'),
              url: '/recharge-bonus',
              icon: Gift,
              requiredRole: ROLE.SUPER_ADMIN,
            },
            {
              title: t('Redemption Codes'),
              url: '/vouchers',
              icon: Ticket,
              requiredRole: ROLE.SUPER_ADMIN,
            },
            {
              title: t('System Info'),
              url: '/system-info',
              icon: ServerCog,
              requiredRole: ROLE.SUPER_ADMIN,
            },
            {
              title: t('Task Plugins'),
              url: '/task-plugins',
              icon: PlugZap,
              requiredRole: ROLE.SUPER_ADMIN,
            },
            {
              title: t('System Settings'),
              url: '/system-settings/site',
              activeUrls: ['/system-settings'],
              icon: Settings,
              requiredRole: ROLE.SUPER_ADMIN,
              // Model pricing lives in System Settings.
              badge: badges.modelPricing,
            },
          ],
        },
      ],
    }
  }, [
    t,
    platformTickets,
    showStandalonePaymentChannels,
    staffWorkspaceBadge,
    personalTicketsBadge,
    ownerTicketsBadge,
    platformTicketsBadge,
    siteOwnerCommissionsBadge,
    referralAdminBadge,
    withdrawalFilter,
    resellerSitesBadge,
    orderManagementBadge,
    isSiteOwner,
    isPureSiteOwner,
    showReferral,
    hasTeam,
    hasArchivedSite,
    isStaff,
    badges.modelPricing,
  ])
}
