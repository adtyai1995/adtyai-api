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
