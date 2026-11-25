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
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { SiteCustomerReferral } from '../components/site-customer-referral'

const { apiGetMock } = vi.hoisted(() => ({
  apiGetMock: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: {
    get: (...args: unknown[]) => apiGetMock(...args),
    post: vi.fn(),
  },
}))

vi.mock('@/features/wallet/adty/site-commission-panel', () => ({
  SiteCommissionPanel: () => <div data-testid='site-commission-panel' />,
}))

vi.mock('qrcode.react', () => ({
  QRCodeSVG: () => <svg data-testid='qr-code' />,
}))

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

describe('SiteCustomerReferral invitee identity display', () => {
  afterEach(() => {
    cleanup()
    apiGetMock.mockReset()
  })

  it('formats invitees as ID / username instead of username (#ID)', async () => {
    apiGetMock.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          site_id: 5,
          site_name: 'Subsite',
          site_status: 'active',
          enabled: true,
          rate_configured: true,
          rate: '0.1',
          rate_percent: '10',
          observation_days: 7,
          invite_code: 'INV123',
          invite_url: 'https://sub.example.com/register?ref=INV123',
          invited_count: 2,
          invites: [
            {
              invitee_id: 202,
              invitee_username: 'alice',
              rate: '0.1',
              rate_percent: '10',
              created_at: 1700000000,
            },
            {
              invitee_id: 203,
              invitee_username: '',
              rate: '0.1',
              rate_percent: '10',
              created_at: 1700000100,
            },
          ],
          has_inviter: false,
          can_bind: false,
        },
      },
    })

    const user = userEvent.setup()
    renderWithQuery(<SiteCustomerReferral />)

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Invited users' })).toBeTruthy()
    })

    await user.click(screen.getByRole('tab', { name: 'Invited users' }))

    await waitFor(() => {
      expect(screen.getByText('202 / alice')).toBeTruthy()
      expect(screen.getByText('203 / -')).toBeTruthy()
    })
    expect(screen.queryByText('alice (#202)')).toBeNull()
  })
})
