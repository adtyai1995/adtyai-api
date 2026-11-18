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
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ReviewsPanel } from '../components/reviews-panel'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

describe('ReviewsPanel pagination', () => {
  it('paginates flagged customers and site reports independently and clamps when total shrinks', async () => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
    let reportsTotal = 21
    const get = vi
      .spyOn(api, 'get')
      .mockImplementation(async (url, config) => {
        const path = String(url)
        const page = Number(
          (config as { params?: { p?: number } } | undefined)?.params?.p ?? 1
        )
        if (path.endsWith('/flagged-customers')) {
          return {
            data: {
              success: true,
              data: {
                total: 25,
                items: [
                  {
                    site_id: 1,
                    user_id: page === 1 ? 101 : 202,
                    risk_flag: 'self_use',
                    risk_reason: `Flagged page ${page}`,
                    reviewed: false,
                  },
                ],
              },
            },
          }
        }
        if (path.endsWith('/reports')) {
          return {
            data: {
              success: true,
              data: {
                total: reportsTotal,
                items:
                  page === 2 && reportsTotal <= 20
                    ? []
                    : [
                        {
                          id: page === 1 ? 301 : 302,
                          site_id: 1,
                          reason: `Report page ${page}`,
                          status: 'pending',
                        },
                      ],
              },
            },
          }
        }
        return { data: { success: true, data: { items: [], total: 0 } } }
      })
    vi.spyOn(api, 'post').mockImplementation(async () => {
      reportsTotal = 20
      return {
        data: {
          success: true,
          data: { id: 302, status: 'resolved' },
        },
      }
    })

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <ReviewsPanel />
      </QueryClientProvider>
    )

    expect(await screen.findByText('Flagged page 1')).toBeInTheDocument()
    expect(await screen.findByText('Report page 1')).toBeInTheDocument()

    const sections = document.querySelectorAll('section')
    const riskSection = sections[0]
    const reportsSection = sections[1]
    const user = userEvent.setup()

    // Switch flagged customers to page 2 without changing reports page
    await user.click(within(riskSection).getByRole('button', { name: 'Next' }))
    expect(await screen.findByText('Flagged page 2')).toBeInTheDocument()
    expect(screen.getByText('Report page 1')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith(
      '/api/adty/admin/sites/flagged-customers',
      { params: { p: 2, page_size: 20 } }
    )

    // Switch reports to page 2
    await user.click(
      within(reportsSection).getByRole('button', { name: 'Next' })
    )
    expect(await screen.findByText('Report page 2')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/adty/admin/sites/reports', {
      params: { p: 2, page_size: 20, status: 'pending' },
    })

    // Resolve the only report on page 2 -> total shrinks to 20 -> clamps back to page 1
    await user.click(
      within(reportsSection).getByRole('button', { name: 'Resolve report' })
    )
    await waitFor(() =>
      expect(screen.getByText('Report page 1')).toBeInTheDocument()
    )
    client.clear()
  })
})
