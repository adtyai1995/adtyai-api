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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { LogStatCards } from '../models/log-stat-cards'
import { UptimePanel } from '../overview/uptime-panel'

const dashboardApi = vi.hoisted(() => ({
  getUserQuotaDates: vi.fn(),
  getUserQuotaDataByUsers: vi.fn(),
  getUptimeStatus: vi.fn(),
}))

const handleServerError = vi.hoisted(() => vi.fn())

vi.mock('../../api', () => dashboardApi)

vi.mock('@/lib/handle-server-error', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/handle-server-error')>()
  return {
    ...actual,
    handleServerError,
  }
})

vi.mock('../../hooks/use-quota-display', () => ({
  useQuotaDisplay: () => ({
    formatQuota: (val: number) => `$${val}`,
  }),
}))

describe('Dashboard error state handling (ISS-R1-0175, ISS-R1-0176)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Element.prototype.getAnimations = () => []
  })

  afterEach(() => {
    cleanup()
  })

  it('LogStatCards treats HTTP 200 { success: false } as an error instead of rendering 0 stats', async () => {
    dashboardApi.getUserQuotaDates.mockResolvedValueOnce({
      success: false,
      message: 'ClickHouse query timeout',
    })

    render(<LogStatCards />)

    await waitFor(() => expect(handleServerError).toHaveBeenCalledTimes(1))
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(4)
  })

  it('UptimePanel renders error alert, Retry button, and header Refresh action when getUptimeStatus fails', async () => {
    dashboardApi.getUptimeStatus
      .mockResolvedValueOnce({
        success: false,
        message: 'Uptime Kuma upstream unreachable',
      })
      .mockResolvedValueOnce({
        success: true,
        data: [
          {
            category_name: 'Core API',
            monitors: [
              {
                name: 'Gateway',
                status: 1,
                uptime: 0.999,
                ping: 42,
              },
            ],
          },
        ],
      })

    const user = userEvent.setup()
    render(<UptimePanel />)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Uptime Kuma upstream unreachable')
    expect(handleServerError).toHaveBeenCalledTimes(1)
    expect(
      screen.queryByText('No uptime monitoring configured')
    ).not.toBeInTheDocument()

    // Header refresh action and retry button are both rendered in the error state
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeInTheDocument()
    const retryBtn = screen.getByRole('button', { name: 'Retry' })
    await user.click(retryBtn)

    await waitFor(() =>
      expect(dashboardApi.getUptimeStatus).toHaveBeenCalledTimes(2)
    )
    expect(await screen.findByText('Gateway')).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
