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
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { SectionPageLayout } from '@/components/layout'
import { TeamPanel } from '@/features/wallet/adty/team-panel'
import { useAuthStore } from '@/stores/auth-store'

const myTeamSearchSchema = z.object({
  trade_no: z.string().optional(),
})

export const Route = createFileRoute('/_authenticated/my-team')({
  validateSearch: myTeamSearchSchema,
  beforeLoad: () => {
    if (useAuthStore.getState().auth.user?.adty_has_team !== true) {
      throw redirect({
        to: '/dashboard/$section',
        params: { section: 'overview' },
        replace: true,
      })
    }
  },
  component: MyTeam,
})

function MyTeam() {
  const { t } = useTranslation()
  const { trade_no } = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('My team')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <TeamPanel
          returnTradeNo={trade_no}
          onReturnClosed={() => {
            void navigate({
              search: (prev) => ({ ...prev, trade_no: undefined }),
              replace: true,
            })
          }}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
