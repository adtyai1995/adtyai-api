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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { CommissionWithdrawalPanel } from '../components/commission-withdrawal-panel'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('requires an Alipay transfer reference and sends it with the original withdrawal', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        total: 1,
        items: [
          {
            id: 17,
            user_id: 5,
            method: 'alipay',
            status: 'approved',
            amount_usd: '10',
            qr_image_url: '',
            payout_amount: '70',
            payout_currency: 'CNY',
            account_name: 'test',
          },
        ],
      },
    },
  })
  const paid = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <CommissionWithdrawalPanel />
    </QueryClientProvider>
  )
  const pay = await screen.findByRole('button', { name: 'Mark as paid' })
  expect((pay as HTMLButtonElement).disabled).toBe(true)
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Proof note'), 'offline')
  expect((pay as HTMLButtonElement).disabled).toBe(true)
  await user.type(screen.getByLabelText('Transfer reference'), 'A123')
  await user.click(pay)
  await waitFor(() =>
    expect(paid).toHaveBeenCalledWith(
      '/api/adty/site/commission-withdrawals/17/paid',
      { tx_id: 'A123', proof: 'offline' }
    )
  )
})
