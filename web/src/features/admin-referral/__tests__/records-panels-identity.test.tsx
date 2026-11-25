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

import { LedgerPanel } from '../components/records-panels'

const { getReferralLedgersMock } = vi.hoisted(() => ({
  getReferralLedgersMock: vi.fn(),
}))

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getReferralLedgers: (...args: unknown[]) => getReferralLedgersMock(...args),
  }
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
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

describe('admin-referral LedgerPanel operator identity', () => {
  afterEach(() => {
    cleanup()
    getReferralLedgersMock.mockReset()
  })

  it('formats non-system operators with ID / username and preserves System', async () => {
    getReferralLedgersMock.mockResolvedValueOnce({
      total: 3,
      items: [
        {
          id: 1,
          affiliate_id: 10,
          user_id: 100,
          username: 'aff100',
          commission_id: 0,
          withdrawal_id: 0,
          type: 'admin_adjust',
          ref_type: 'admin',
          ref_id: '1',
          settlement_currency: 'USD',
          delta_pending: 0,
          delta_available: 10,
          delta_frozen: 0,
          delta_withdrawn: 0,
          remark: 'manual',
          operator: '1',
          operator_username: 'root',
          created_at: 1700000000,
        },
        {
          id: 2,
          affiliate_id: 10,
          user_id: 100,
          username: 'aff100',
          commission_id: 0,
          withdrawal_id: 0,
          type: 'admin_adjust',
          ref_type: 'admin',
          ref_id: '2',
          settlement_currency: 'USD',
          delta_pending: 0,
          delta_available: 5,
          delta_frozen: 0,
          delta_withdrawn: 0,
          remark: 'manual2',
          operator: '2',
          created_at: 1700000100,
        },
        {
          id: 3,
          affiliate_id: 10,
          user_id: 100,
          username: 'aff100',
          commission_id: 1,
          withdrawal_id: 0,
          type: 'commission_accrue',
          ref_type: 'order',
          ref_id: 'ORD-1',
          settlement_currency: 'USD',
          delta_pending: 2,
          delta_available: 0,
          delta_frozen: 0,
          delta_withdrawn: 0,
          remark: '',
          operator: 'system',
          created_at: 1700000200,
        },
      ],
    })

    renderWithQuery(<LedgerPanel />)

    await waitFor(() => {
      expect(screen.getByText('1 / root')).toBeTruthy()
      expect(screen.getByText('2 / -')).toBeTruthy()
      expect(screen.getByText('System')).toBeTruthy()
    })
    expect(screen.queryByText('#1')).toBeNull()
    expect(screen.queryByText('#2')).toBeNull()
  })
})
