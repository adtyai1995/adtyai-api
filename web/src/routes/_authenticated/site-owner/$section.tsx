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
import { createFileRoute, redirect } from '@tanstack/react-router'
import { z } from 'zod'

import { SiteOwnerCenter } from '@/features/adty-site-owner'
import {
  isSiteOwnerSection,
  SITE_OWNER_DEFAULT_SECTION,
} from '@/features/adty-site-owner/sections'
import { useAuthStore } from '@/stores/auth-store'

// The server checks ownership on every request; this only keeps non-owners
// from landing on an empty page.
export const Route = createFileRoute('/_authenticated/site-owner/$section')({
  validateSearch: z.object({
    trade_no: z.string().trim().min(1).max(64).optional().catch(undefined),
  }),
  beforeLoad: ({ params }) => {
    if (!isSiteOwnerSection(params.section)) {
      throw redirect({
        to: '/site-owner/$section',
        params: { section: SITE_OWNER_DEFAULT_SECTION },
      })
    }
    if (useAuthStore.getState().auth.user?.adty_site_owner !== true) {
      throw redirect({ to: '/403' })
    }
  },
  component: SiteOwnerPage,
})

function SiteOwnerPage() {
  const params = Route.useParams()
  const { trade_no } = Route.useSearch()
  return (
    <SiteOwnerCenter
      returnTradeNo={params.section === 'earnings' ? trade_no : undefined}
      section={
        isSiteOwnerSection(params.section)
          ? params.section
          : SITE_OWNER_DEFAULT_SECTION
      }
    />
  )
}
