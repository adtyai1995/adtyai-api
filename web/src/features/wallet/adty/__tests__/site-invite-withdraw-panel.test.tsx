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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SiteInviteWithdrawPanel } from '../site-invite-withdraw-panel'

afterEach(() => {
  localStorage.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})
it('hides cancellation on a pending withdrawal after its frozen deadline', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: String(url).endsWith('rules')
        ? { configured: false, methods: ['balance'] }
        : {
            items: [
              {
                id: 1,
                amount_usd: '1.2',
                method: 'usdt',
                status: 'pending',
                payout_amount: '1.2',
                payout_currency: 'USDT',
                reject_reason: '',
                can_cancel: false,
                cancel_deadline: 2,
                cancel_reason: 'The cancel window has passed',
              },
            ],
          },
    },
  }))
  render(
    <QueryClientProvider client={new QueryClient()}>
      <SiteInviteWithdrawPanel rows={[]} onChanged={() => {}} />
    </QueryClientProvider>
  )
  expect(await screen.findByText(/1.2 USDT/)).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
})

it('shows the exact server payout and requires confirmation again when the fee changes', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 18, username: 'customer', role: 1 })
  const quote = {
    quote_id: 'first',
    rate: '1',
    fee_usd: '0.1',
    payout_amount: '0.900001',
    payout_currency: 'USDT',
  }
  const changed = {
    ...quote,
    quote_id: 'second',
    fee_usd: '0.2',
    payout_amount: '0.800001',
  }
  const responses: Record<string, unknown> = {
    quote,
    rules: {
      configured: true,
      methods: ['usdt'],
      min_usd: '0.1',
      fee_usd: '0.1',
      cny_rate: '7',
    },
    'by-key': { id: 0 },
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: responses[String(url).split('/').at(-1) ?? ''] ?? { items: [] },
    },
  }))
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({
      data: { success: false, code: 'quote_changed', data: changed },
    })
    .mockResolvedValueOnce({ data: { success: true, data: { id: 8 } } })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <SiteInviteWithdrawPanel
        rows={[
          {
            id: 4,
            site_id: 3,
            trade_no: 'C',
            status: 'available',
            amount_usd: '2',
            rate: '1',
            clawed_usd: '0',
            converted_usd: '0',
            withdrawn_usd: '0',
            frozen_usd: '0',
            earnings_hold_usd: '2',
            margin_hold_usd: '0',
            available_at: 1,
          },
        ]}
        onChanged={() => {}}
      />
    </QueryClientProvider>
  )
  await user.click(await screen.findByRole('button', { name: 'USDT' }))
  await user.type(screen.getByLabelText('Amount USD'), '1.0000005')
  await user.selectOptions(screen.getByLabelText('Network'), 'TRC20')
  await user.type(
    screen.getByLabelText('USDT address'),
    'T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb'
  )
  await user.click(screen.getByRole('button', { name: 'Submit withdrawal' }))
  const dialog = await screen.findByRole('alertdialog')
  expect(within(dialog).getByText(/0.900001 USDT/)).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  expect(await within(dialog).findByText(/0.800001 USDT/)).toBeVisible()
  expect(post).toHaveBeenCalledTimes(1)
  await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  await screen.findByRole('button', { name: 'Start a new request' })
  expect(
    post.mock.calls.map(
      ([, body]) => (body as { expected_quote_id: string }).expected_quote_id
    )
  ).toEqual(['first', 'second'])
})
