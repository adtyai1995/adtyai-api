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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SiteFundPanel } from '../components/site-fund-panel'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

describe('SiteFundPanel anomaly dispositions', () => {
  it('renders ErrorState for a failed dispositions query and retries it independently', async () => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
    let failDispositions = true
    const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
      const path = String(url)
      if (path.endsWith('/wallets')) {
        return { data: { success: true, data: [] } }
      }
      if (path.endsWith('/commission-margin') || path.endsWith('/margin')) {
        return { data: { success: true, data: { site_id: 1, allowed: false } } }
      }
      if (path.endsWith('/fund-anomalies')) {
        return { data: { success: true, data: { kinds: [] } } }
      }
      if (path.endsWith('/dispositions')) {
        if (failDispositions) {
          throw new Error('dispositions unavailable')
        }
        return {
          data: {
            success: true,
            data: {
              items: [
                {
                  id: 42,
                  kind: 'ledger_mismatch',
                  ref: 'ref-42',
                  status: 'open',
                  note: '',
                  operator_id: 1,
                  order_ref: 'ORD-42',
                },
              ],
            },
          },
        }
      }
      return { data: { success: true, data: { items: [], total: 0 } } }
    })

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <SiteFundPanel siteId={1} />
      </QueryClientProvider>
    )

    expect(
      await screen.findByText('Financial review history')
    ).toBeInTheDocument()
    const retry = await screen.findByRole('button', { name: 'Retry' })
    expect(screen.queryByText('ORD-42')).not.toBeInTheDocument()

    failDispositions = false
    await userEvent.setup().click(retry)

    await waitFor(() =>
      expect(screen.getByText('ORD-42')).toBeInTheDocument()
    )
    expect(
      screen.queryByRole('button', { name: 'Retry' })
    ).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledWith(
      '/api/adty/admin/fund-anomalies/dispositions',
      { params: { site_id: 1 } }
    )
    client.clear()
  })
})
