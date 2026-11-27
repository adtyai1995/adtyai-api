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
import { useEffect } from 'react'
import { z } from 'zod'

import { Wallet } from '@/features/wallet'
import { useAuthStore } from '@/stores/auth-store'

const walletSearchSchema = z.object({
  show_history: z.boolean().optional(),
  trade_no: z.string().optional(),
})

export const Route = createFileRoute('/_authenticated/wallet/')({
  beforeLoad: () => {
    const user = useAuthStore.getState().auth.user
    if (user?.is_team_employee === true) {
      throw redirect({ to: '/my-team', replace: true })
    }
  },
  component: RouteComponent,
  validateSearch: walletSearchSchema,
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const isTeamEmployee = useAuthStore(
    (state) => state.auth.user?.is_team_employee === true
  )

  useEffect(() => {
    if (!isTeamEmployee) return
    void navigate({ to: '/my-team', replace: true })
  }, [isTeamEmployee, navigate])

  if (isTeamEmployee) return null

  return (
    <Wallet
      initialShowHistory={search.show_history}
      returnTradeNo={search.trade_no}
      onReturnClosed={() => {
        void navigate({
          search: (prev) => ({ ...prev, trade_no: undefined }),
          replace: true,
        })
      }}
    />
  )
}
