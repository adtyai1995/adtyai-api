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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { UserLedgerDialog } from '../user-ledger-dialog'
import { UserLimitsFields } from '../user-limits-fields'

function wrapper(props: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

afterEach(() => vi.restoreAllMocks())

describe('UserLimitsFields', () => {
  it('shows -1 as empty and saves an emptied field as -1 and 0 as no limit', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { rpm: 60, tpm: -1, concurrency: 2 } },
    })
    const put = vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: true, data: { rpm: -1, tpm: 0, concurrency: 2 } },
    })
    const user = userEvent.setup()
    render(<UserLimitsFields userId={7} canEdit />, { wrapper })
    await waitFor(() => expect(screen.getByLabelText('RPM')).toHaveValue(60))
    expect(screen.getByLabelText('TPM')).toHaveValue(null)
    await user.clear(screen.getByLabelText('RPM'))
    await user.type(screen.getByLabelText('TPM'), '0')
    await user.click(screen.getByRole('button', { name: 'Save limits' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/api/adty/admin/users/7/limits', {
        rpm: -1,
        tpm: 0,
        concurrency: 2,
      })
    )
  })

  it('is read-only without the edit permission', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { rpm: -1, tpm: -1, concurrency: -1 } },
    })
    render(<UserLimitsFields userId={7} canEdit={false} />, { wrapper })
    await waitFor(() => expect(screen.getByLabelText('RPM')).toBeDisabled())
    expect(
      screen.queryByRole('button', { name: 'Save limits' })
    ).not.toBeInTheDocument()
  })

  it('disables inputs and Save limits on query failure and when switching users before the new user loads', async () => {
    let resolveUser8!: (value: unknown) => void
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (url === '/api/adty/admin/users/7/limits') {
        return {
          data: { success: true, data: { rpm: 60, tpm: 1000, concurrency: 2 } },
        }
      }
      if (url === '/api/adty/admin/users/8/limits') {
        return await new Promise((resolve) => {
          resolveUser8 = resolve
        })
      }
      throw new Error(`Unexpected GET ${url}`)
    })
    const put = vi.spyOn(api, 'put')
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    const view = render(
      <QueryClientProvider client={client}>
        <UserLimitsFields userId={7} canEdit />
      </QueryClientProvider>
    )
    await waitFor(() => expect(screen.getByLabelText('RPM')).toHaveValue(60))
    expect(screen.getByRole('button', { name: 'Save limits' })).toBeEnabled()

    view.rerender(
      <QueryClientProvider client={client}>
        <UserLimitsFields userId={8} canEdit />
      </QueryClientProvider>
    )

    expect(screen.getByLabelText('RPM')).toHaveValue(null)
    expect(screen.getByLabelText('RPM')).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save limits' })).toBeDisabled()

    await act(async () => {
      resolveUser8({ data: { success: false, message: 'DB unavailable' } })
    })

    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load')
    expect(screen.getByRole('button', { name: 'Save limits' })).toBeDisabled()
    expect(put).not.toHaveBeenCalled()
    client.clear()
  })
})

describe('UserLedgerDialog', () => {
  it('distinguishes loading and error states from an empty ledger and resets page when userId changes', async () => {
    let failUser7 = true
    const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
      if (url === '/api/adty/admin/users/7/ledger') {
        if (failUser7) {
          return { data: { success: false, message: 'DB unavailable' } }
        }
        return {
          data: {
            success: true,
            data: {
              total: 25,
              items: [
                {
                  id: 1,
                  user_id: 7,
                  delta_quota: 500_000,
                  balance_before: 0,
                  balance_after: 500_000,
                  type: 'manual_adjust',
                  ref_type: '',
                  ref_id: '',
                  reason: 'top-up',
                  operator_id: 1,
                  created_at: 1_700_000_000,
                },
              ],
            },
          },
        }
      }
      if (url === '/api/adty/admin/users/8/ledger') {
        expect(config?.params).toMatchObject({ p: 1 })
        return {
          data: {
            success: true,
            data: { total: 0, items: [] },
          },
        }
      }
      throw new Error(`Unexpected GET ${url}`)
    })

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const user = userEvent.setup()

    const view = render(
      <QueryClientProvider client={client}>
        <UserLedgerDialog open onOpenChange={() => undefined} userId={7} />
      </QueryClientProvider>
    )

    const retry = await screen.findByRole('button', { name: 'Retry' })
    expect(screen.queryByText('No records')).not.toBeInTheDocument()

    failUser7 = false
    await user.click(retry)
    expect(await screen.findByText('top-up')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Next' }))
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith('/api/adty/admin/users/7/ledger', {
        params: { p: 2, page_size: 20 },
      })
    )

    view.rerender(
      <QueryClientProvider client={client}>
        <UserLedgerDialog open onOpenChange={() => undefined} userId={8} />
      </QueryClientProvider>
    )

    expect(await screen.findByText('No records')).toBeInTheDocument()
    client.clear()
  })
})
