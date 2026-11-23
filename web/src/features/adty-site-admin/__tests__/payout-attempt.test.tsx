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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SiteWithdrawalsPanel } from '../components/site-withdrawals-panel'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('resolves an outstanding recalled attempt without releasing or reissuing payment', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        total: 1,
        items: [
          {
            id: 1,
            site_id: 7,
            owner_user_id: 8,
            method: 'alipay',
            status: 'pending',
            attempt_open: true,
            attempt_number: 1,
            amount_usd: '10',
            fee_usd: '1',
            net_usd: '9',
            created_at: 100,
          },
        ],
      },
    },
  })
  render(
    <QueryClientProvider client={new QueryClient()}>
      <SiteWithdrawalsPanel />
    </QueryClientProvider>
  )
  await screen.findByRole('button', { name: 'Record payout' })
  expect(
    (screen.getByRole('button', { name: 'Confirm failed payout' }) as HTMLButtonElement).disabled
  ).toBe(false)
  expect(screen.queryByRole('button', { name: 'Reject' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Mark paying' })).toBeNull()
})
