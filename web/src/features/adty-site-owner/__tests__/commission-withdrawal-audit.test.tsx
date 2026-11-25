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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { CommissionWithdrawalPanel } from '../components/commission-withdrawal-panel'

const { getOwnerCommissionWithdrawalsMock } = vi.hoisted(() => ({
  getOwnerCommissionWithdrawalsMock: vi.fn(),
}))

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getOwnerCommissionWithdrawals: (...args: unknown[]) =>
      getOwnerCommissionWithdrawalsMock(...args),
  }
})

vi.mock('@/components/dialog', () => ({
  Dialog: () => null,
}))

vi.mock('@/hooks/use-sidebar-badges', () => ({
  invalidateSidebarBadges: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: 'en', resolvedLanguage: 'en' },
  }),
}))

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

describe('CommissionWithdrawalPanel closed-site audit actor display', () => {
  afterEach(() => {
    cleanup()
    getOwnerCommissionWithdrawalsMock.mockReset()
  })

  it('formats closed-site audit actors with ID / username and preserves Unknown fallback', async () => {
    getOwnerCommissionWithdrawalsMock.mockResolvedValueOnce({
      total: 1,
      items: [
        {
          id: 501,
          site_id: 12,
          user_id: 88,
          username: 'cust88',
          created_at: 1700000000,
          reviewed_by: 3,
          reviewed_by_username: 'reviewerA',
          reviewed_at: 1700000100,
          paid_by: 4,
          paid_at: 1700000200,
          legacy_decision_by: 0,
          legacy_decision_at: 1700000050,
          can_cancel: false,
          cancel_deadline: 0,
          cancel_reason: '',
          amount_usd: '20.00',
          fee_usd: '0',
          net_usd: '20.00',
          method: 'usdt',
          network: 'TRC20',
          account_no: 'T123',
          account_name: '',
          qr_image_url: '',
          status: 'paid',
          payout_amount: '20.00',
          payout_currency: 'USDT',
          tx_id: 'tx-1',
          proof_note: '',
          reject_reason: '',
        },
      ],
    })

    renderWithQuery(<CommissionWithdrawalPanel closed />)

    await waitFor(() => {
      expect(screen.getByText(/Withdrawal reviewer 3 \/ reviewerA/)).toBeTruthy()
      expect(screen.getByText(/Payment recorded by 4 \/ -/)).toBeTruthy()
      expect(screen.getByText(/Legacy decision record Unknown/)).toBeTruthy()
    })
    expect(screen.queryByText(/#3/)).toBeNull()
    expect(screen.queryByText(/#4/)).toBeNull()
  })
})
