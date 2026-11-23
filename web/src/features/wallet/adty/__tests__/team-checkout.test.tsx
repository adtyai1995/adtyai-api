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
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { TeamPanel } from '../team-panel'

const api = vi.hoisted(() => ({
  get: vi.fn<(url: string) => Promise<unknown>>(),
  post: vi.fn(),
}))
vi.mock('@/lib/api', () => ({ api }))
afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

it.each([
  ['qr_code', 'order-only-qr'],
  ['url_scheme', 'alipays://order/team-123'],
  ['pay_url', 'https://pay.example/team-123'],
])('opens a pending team order with its original %s', async (field, value) => {
  const order = {
    trade_no: 'TEAM123',
    method: 'alipay',
    face_usd: '20',
    pay_amount: '140',
    expire_at: Math.floor(Date.now() / 1000) + 300,
    [field]: value,
  }
  const answers: Record<string, unknown> = {
    '/api/adty/teams/mine': {
      team: {
        team_id: 10,
        name: 'Finance',
        role: 'leader',
        discount: '1',
        shared_quota: 0,
        status: 'active',
      },
    },
    '/api/adty/topup/info': { methods: [], scenes: [] },
    '/api/adty/teams/10/invites': { items: [] },
    '/api/adty/teams/10/topups': { items: [order] },
    '/api/adty/topup/orders/TEAM123': {
      trade_no: order.trade_no,
      payment_method: 'alipay',
      status: 'pending',
      ext: order,
    },
  }
  api.get.mockImplementation(async (url) => ({
    data: { success: true, data: answers[url] },
  }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <TeamPanel />
    </QueryClientProvider>
  )
  fireEvent.click(
    await screen.findByRole('button', { name: /Continue payment.*TEAM123/ })
  )
  expect(
    (await screen.findByTestId('epay-qr')).getAttribute('data-value')
  ).toBe(value)
  expect(screen.getByText(/Order number.*TEAM123/)).toBeTruthy()
  expect(api.post).not.toHaveBeenCalled()
  client.clear()
})

it('resumes a recoverable order by its original number and shows legacy orders as unavailable', async () => {
  const order = { trade_no: 'TEAM-GAP', provider: 'adty_epay', method: 'alipay', channel_id: 9, face_usd: '20', pay_amount: '140' }
  const recovered = { ...order, qr_code: 'recovered-qr' }
  const answers: Record<string, unknown> = {
    '/api/adty/teams/mine': { team: { team_id: 10, role: 'leader', name: 'Finance', discount: '1', shared_quota: 0, status: 'active' } },
    '/api/adty/topup/info': { methods: [], scenes: [] },
    '/api/adty/teams/10/invites': { items: [] },
    '/api/adty/teams/10/topups': { items: [order, { trade_no: 'LEGACY', method: 'team', channel_id: 0 }] },
    '/api/adty/topup/orders/TEAM-GAP': { trade_no: order.trade_no, status: 'pending', payment_method: 'alipay', ext: recovered },
  }
  api.get.mockImplementation(async (url) => ({ data: { success: true, data: answers[url] } }))
  api.post.mockResolvedValue({ data: { success: true, data: recovered } })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><TeamPanel /></QueryClientProvider>)
  expect(await screen.findByText(/LEGACY.*This order has no payment channel/)).toBeTruthy()
  expect(screen.queryByRole('button', { name: /LEGACY/ })).toBeNull()
  fireEvent.click(await screen.findByRole('button', { name: /Continue payment.*TEAM-GAP/ }))
  expect((await screen.findByTestId('epay-qr')).getAttribute('data-value')).toBe('recovered-qr')
  expect(api.post).toHaveBeenCalledWith('/api/adty/teams/10/topups/TEAM-GAP/resume')
  client.clear()
})
