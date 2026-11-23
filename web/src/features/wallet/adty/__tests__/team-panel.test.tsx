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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { TeamPanel } from '../team-panel'

function mockMineTeamGet(handler: () => unknown | Promise<unknown>) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    const path = String(url)
    if (path === '/api/adty/teams/mine') {
      return handler()
    }
    return { data: { success: true, data: { items: [] } } }
  })
}

function wrapper(props: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('TeamPanel', () => {
  it('shows a loading state while the mine query is pending and does not offer create or join', async () => {
    mockMineTeamGet(() => new Promise(() => {}))
    render(<TeamPanel />, { wrapper })
    expect(await screen.findByText('Team')).toBeInTheDocument()
    expect(screen.getByText('Loading...')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Create team' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Join team' })
    ).not.toBeInTheDocument()
  })

  it('shows an error state when the mine query fails and does not offer create or join', async () => {
    mockMineTeamGet(() => {
      throw new Error('offline')
    })
    render(<TeamPanel />, { wrapper })
    expect(
      await screen.findByText('Oops! Something went wrong')
    ).toBeInTheDocument()
    expect(screen.getByText('Team')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Create team' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Join team' })
    ).not.toBeInTheDocument()
  })

  it('retries the mine query without exposing Root-only team creation', async () => {
    let failed = true
    mockMineTeamGet(() => {
      if (failed) throw new Error('offline')
      return { data: { success: true, data: { team: null } } }
    })
    render(<TeamPanel />, { wrapper })
    const retry = await screen.findByRole('button', { name: 'Retry' })
    failed = false
    await userEvent.setup().click(retry)
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: 'Retry' })
      ).not.toBeInTheDocument()
    )
    expect(
      screen.queryByRole('button', { name: 'Create team' })
    ).not.toBeInTheDocument()
    // Logged-in accounts cannot accept an invite. An empty mine result must
    // not bring the join entrance back.
    expect(
      screen.queryByRole('button', { name: 'Join team' })
    ).not.toBeInTheDocument()
  })

  it('shows team details instead of create or join when the user has a team', async () => {
    mockMineTeamGet(() => ({
      data: {
        success: true,
        data: {
          team: {
            team_id: 3,
            name: 'Alpha',
            role: 'member',
            discount: '1',
            shared_quota: 10,
            status: 'active',
          },
        },
      },
    }))
    render(<TeamPanel />, { wrapper })
    expect(await screen.findByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Post-discount payment ratio')).toBeInTheDocument()
    expect(screen.queryByText('Discount')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Create team' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Join team' })
    ).not.toBeInTheDocument()
  })

  it('lets a member revoke their own bound key', async () => {
    mockMineTeamGet(() => ({
      data: {
        success: true,
        data: {
          team: {
            team_id: 3,
            name: 'Alpha',
            role: 'member',
            discount: '1',
            shared_quota: 1,
            status: 'active',
            keys: [{ token_id: 7, name: 'My key', status: 'active' }],
          },
        },
      },
    }))
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true } })
    render(<TeamPanel />, { wrapper })
    await userEvent
      .setup()
      .click(await screen.findByRole('button', { name: 'Revoke key' }))
    expect(post).toHaveBeenCalledWith('/api/adty/teams/3/keys/7/revoke')
  })

  it('lets the leader top up through the chosen Epay channel button', async () => {
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      const path = String(url)
      if (path === '/api/adty/teams/mine') {
        return {
          data: {
            success: true,
            data: {
              team: {
                team_id: 3,
                name: 'Alpha',
                role: 'leader',
                discount: '1',
                shared_quota: 10,
                status: 'active',
              },
            },
          },
        }
      }
      if (path === '/api/adty/topup/info') {
        return {
          data: {
            success: true,
            data: {
              methods: [],
              scenes: [
                {
                  key: 'wallet_topup',
                  name: 'Wallet top-up',
                  min_usd: '0',
                  max_usd: '0',
                  methods: [
                    { id: 'usdt.trc20', kind: 'usdt', available: true },
                    {
                      id: 'alipay',
                      kind: 'epay',
                      available: true,
                      label: '支付宝1',
                      channel_id: 7,
                    },
                    {
                      id: 'alipay',
                      kind: 'epay',
                      available: true,
                      label: '支付宝2',
                      channel_id: 9,
                    },
                  ],
                },
              ],
            },
          },
        }
      }
      return { data: { success: true, data: { items: [] } } }
    })
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true, data: {} } })
    const user = userEvent.setup()
    render(<TeamPanel />, { wrapper })
    await user.type(await screen.findByLabelText('Team top-up'), '10')
    const topup = screen.getByRole('button', { name: 'Team top-up' })
    expect(topup).toBeDisabled()
    expect(screen.getByRole('button', { name: 'USDT · TRC20' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: '支付宝2' }))
    await user.click(topup)
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/adty/teams/3/topups',
        {
          amount_usd: '10',
          payment_scene: 'wallet_topup',
          method: 'alipay',
          channel_id: 9,
        },
        expect.anything()
      )
    )
  })

  it('does not let an ordinary account choose its team protocol price', async () => {
    mockMineTeamGet(() => ({
      data: { success: true, data: { team: null } },
    }))
    render(<TeamPanel />, { wrapper })
    await waitFor(() =>
      expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
    )
    expect(
      screen.queryByRole('button', { name: 'Join team' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Create team' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByLabelText('Post-discount payment ratio')
    ).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Discount')).not.toBeInTheDocument()
  })

  it('lets a leader create an employee and manage only dedicated employee accounts', async () => {
    mockMineTeamGet(() => ({
      data: {
        success: true,
        data: {
          team: {
            team_id: 3,
            name: 'Alpha',
            role: 'leader',
            discount: '1',
            shared_quota: 0,
            status: 'active',
            members: [
              {
                user_id: 11,
                username: 'employee',
                role: 'member',
                status: 'disabled',
                independent_employee: true,
              },
              {
                user_id: 12,
                username: 'legacy',
                role: 'member',
                status: 'left',
              },
              {
                user_id: 13,
                username: 'deleted',
                role: 'member',
                status: 'deleted',
                independent_employee: true,
              },
            ],
          },
        },
      },
    }))
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true } })
    const user = userEvent.setup()
    render(<TeamPanel />, { wrapper })
    await user.type(
      await screen.findByLabelText('Employee username'),
      'new-employee'
    )
    await user.type(
      screen.getByLabelText('Employee password'),
      'Employee-password-2026'
    )
    await user.click(screen.getByRole('button', { name: 'Create employee' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/adty/teams/3/employees', {
        username: 'new-employee',
        password: 'Employee-password-2026',
      })
    )
    expect(screen.getByLabelText('Employee password')).toHaveValue('')
    await user.click(screen.getByRole('button', { name: 'Enable employee' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/adty/teams/3/members/11/enable')
    )
    expect(
      screen.getAllByRole('button', { name: 'Delete employee' })
    ).toHaveLength(1)
    await user.click(screen.getByRole('button', { name: 'Delete employee' }))
    expect(post).not.toHaveBeenCalledWith('/api/adty/teams/3/members/11/delete')
    await user.click(screen.getByRole('button', { name: 'Confirm delete' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/adty/teams/3/members/11/delete')
    )
  })

  it('offers a copyable employee registration link after issuing an invite', async () => {
    mockMineTeamGet(() => ({
      data: {
        success: true,
        data: {
          team: {
            team_id: 3,
            name: 'Alpha',
            role: 'leader',
            discount: '1',
            shared_quota: 0,
            status: 'active',
          },
        },
      },
    }))
    vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: true, data: { code: 'a'.repeat(32) } },
    })
    const user = userEvent.setup()
    render(<TeamPanel />, { wrapper })
    await user.click(
      await screen.findByRole('button', { name: 'Create invite' })
    )
    const link = await screen.findByLabelText('Employee registration link')
    expect(link).toHaveValue(
      `${window.location.origin}/team-register/${'a'.repeat(32)}?kind=employee`
    )
    expect(
      screen.getByRole('button', { name: 'Copy employee registration link' })
    ).toBeEnabled()
  })
})
