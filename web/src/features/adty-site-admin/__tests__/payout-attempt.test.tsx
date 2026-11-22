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
