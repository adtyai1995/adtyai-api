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
import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useEffect } from 'react'

import { Referral } from '@/features/referral'
import { useStatus } from '@/hooks/use-status'
import { ROLE } from '@/lib/roles'
import {
  readReferralEnabledFromStatus,
  statusQueryOptions,
} from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

function ReferralRoutePage() {
  const navigate = useNavigate()
  const { status } = useStatus()
  const userReferralEnabled = useAuthStore(
    (state) => state.auth.user?.adty_referral_enabled
  )
  const referralEnabled =
    readReferralEnabledFromStatus(status) ?? userReferralEnabled
  const isDisabled = referralEnabled === false

  useEffect(() => {
    if (!isDisabled) return
    void navigate({
      to: '/dashboard/$section',
      params: { section: 'overview' },
      replace: true,
    })
  }, [isDisabled, navigate])

  if (isDisabled) return null
  return <Referral />
}

// R-32 #21 & R-35 #15: Admins, site owners, and sites with referral disabled
// do not use the personal referral page.
export const Route = createFileRoute('/_authenticated/referral/')({
  beforeLoad: async ({ context }) => {
    const user = useAuthStore.getState().auth.user
    if (!user) return
    if (user.adty_site_owner === true && user.role < ROLE.ADMIN) {
      throw redirect({
        to: '/site-owner/$section',
        params: { section: 'overview' },
      })
    }
    if (user.role >= ROLE.ADMIN || user.adty_site_owner === true) {
      throw redirect({
        to: '/dashboard/$section',
        params: { section: 'overview' },
      })
    }

    let statusReferralEnabled: boolean | undefined
    try {
      const status = await context.queryClient.fetchQuery(statusQueryOptions)
      statusReferralEnabled = readReferralEnabledFromStatus(status)
    } catch {
      // Ignore status fetch failure; fall back to the user payload flag below.
    }

    const referralEnabled = statusReferralEnabled ?? user.adty_referral_enabled
    if (referralEnabled === false) {
      throw redirect({
        to: '/dashboard/$section',
        params: { section: 'overview' },
      })
    }
  },
  component: ReferralRoutePage,
})
