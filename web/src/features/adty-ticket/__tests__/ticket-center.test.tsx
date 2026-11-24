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
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { TicketCenter } from '../ticket-center'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (selector: (s: { auth: { user: { id: number; role: number } } }) => unknown) =>
    selector({ auth: { user: { id: 7, role: 100 } } }),
}))

vi.mock('@/hooks/use-sidebar-badges', () => ({
  invalidateSidebarBadges: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/components/dialog', () => ({
  Dialog: (props: {
    open?: boolean
    title?: React.ReactNode
    children?: React.ReactNode
    footer?: React.ReactNode
  }) =>
    props.open ? (
      <div role='dialog'>
        <h2>{props.title}</h2>
        {props.children}
        {props.footer}
      </div>
    ) : null,
}))

vi.mock('@/components/confirm-dialog', () => ({
  ConfirmDialog: () => null,
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, string | number>) => {
      if (!params) return key
      return Object.entries(params).reduce(
        (acc, [k, v]) => acc.replaceAll(`{{${k}}}`, String(v)),
        key
      )
    },
  }),
}))

function createClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

describe('TicketCenter redesign', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('renders user ticket list with ticket_no, pills, pagination, and opens create modal', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          total: 1,
          can_view_team: false,
          can_manage: false,
          image_policy: {
            max_bytes: 5_000_000,
            max_count: 5,
            max_total: 20_000_000,
            max_side: 4096,
            max_pixels: 16_000_000,
          },
          tickets: [
            {
              id: 97,
              ticket_no: 'ADTY2026-000097',
              title: 'API billing question',
              type: 'billing',
              priority: 'normal',
              status: 'closed',
              user_id: 7,
              site_id: 0,
              team_id: 0,
              created_at: 1781678160,
            },
          ],
        },
      },
    })

    render(
      <QueryClientProvider client={createClient()}>
        <TicketCenter scope='personal' />
      </QueryClientProvider>
    )

    expect(await screen.findByText('API billing question')).toBeTruthy()
    expect(screen.getByText('ADTY2026-000097')).toBeTruthy()
    expect(screen.getByText('Normal')).toBeTruthy()
    expect(screen.getByText('Ticket status closed')).toBeTruthy()
    expect(screen.getByText('Page 1 of 1, 1 tickets total')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Next page' })).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: 'Create ticket' }))
    expect(await screen.findByRole('dialog')).toBeTruthy()
  })

  it('renders admin filter bar, passes filter query params, and shows submitter username', async () => {
    const getSpy = vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          total: 2,
          can_view_team: false,
          can_manage: true,
          image_policy: {
            max_bytes: 5_000_000,
            max_count: 5,
            max_total: 20_000_000,
            max_side: 4096,
            max_pixels: 16_000_000,
          },
          tickets: [
            {
              id: 117,
              ticket_no: 'ADTY2026-000117',
              title: 'Image generation balance error',
              type: 'api',
              priority: 'urgent',
              status: 'waiting_user',
              user_id: 42,
              username: 'Mysterious',
              site_id: 0,
              team_id: 0,
              created_at: 1789267140,
            },
          ],
        },
      },
    })

    render(
      <QueryClientProvider client={createClient()}>
        <TicketCenter scope='platform' />
      </QueryClientProvider>
    )

    const rowButton = (
      await screen.findByText('Image generation balance error')
    ).closest('button')
    expect(rowButton).not.toBeNull()
    if (!rowButton) return
    expect(within(rowButton).getByText('42 / Mysterious')).toBeTruthy()
    expect(within(rowButton).getByText('Urgent')).toBeTruthy()
    expect(within(rowButton).getByText('Admin replied')).toBeTruthy()

    // Verify all 7 filter inputs exist
    expect(
      screen.getByPlaceholderText('Search ticket no., title, username')
    ).toBeTruthy()
    const statusSelect = screen.getByLabelText('Status')
    const prioritySelect = screen.getByLabelText('Priority')
    const typeSelect = screen.getByLabelText('Type')
    expect(screen.getByPlaceholderText('Assignee ID')).toBeTruthy()
    expect(screen.getByLabelText('Start date')).toBeTruthy()
    expect(screen.getByLabelText('End date')).toBeTruthy()

    fireEvent.change(statusSelect, { target: { value: 'waiting_user' } })
    fireEvent.change(prioritySelect, { target: { value: 'urgent' } })
    fireEvent.change(typeSelect, { target: { value: 'api' } })

    await waitFor(() => {
      const lastCall = getSpy.mock.calls.at(-1)
      expect(lastCall?.[1]?.params).toMatchObject({
        scope: 'platform',
        status: 'waiting_user',
        priority: 'urgent',
        type: 'api',
      })
    })
  })
})
