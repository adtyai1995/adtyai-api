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
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { CustomerTermsGate } from '../customer-terms-gate'

const api = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api }))

function wrap(children: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

function user(overrides: Partial<AuthUser> = {}): AuthUser {
  return {
    id: 7,
    username: 'shop-customer',
    display_name: 'Shop Customer',
    role: 1,
    status: 1,
    group: 'default',
    ...overrides,
  }
}

describe('CustomerTermsGate', () => {
  beforeEach(() => {
    api.get.mockReset()
    api.post.mockReset()
    api.get.mockResolvedValue({
      data: { success: true, data: 'Customer terms body' },
    })
    useAuthStore.getState().auth.setUser(null)
  })

  afterEach(() => {
    cleanup()
  })

  it('hides the console until a site customer accepts the current terms', async () => {
    useAuthStore.getState().auth.setUser(
      user({
        site_terms_required: true,
        site_terms_version: '3',
        adty_site_owner: false,
      })
    )
    render(
      wrap(
        <CustomerTermsGate>
          <p>wallet console</p>
        </CustomerTermsGate>
      )
    )
    expect(screen.queryByText('wallet console')).toBeNull()
    expect(
      screen.getByText('Accept the current site customer terms first')
    ).toBeTruthy()
    expect(api.get).toHaveBeenCalledWith(
      '/api/adty/legal/site-customer-terms',
      expect.anything()
    )
    await screen.findByText('Customer terms body')
    api.post.mockResolvedValue({ data: { success: true } })
    await userEvent.click(
      screen.getByRole('button', { name: 'I agree to the site customer terms' })
    )
    expect(api.post).toHaveBeenCalledWith('/api/adty/site/terms/accept', {
      version: '3',
    })
  })

  it('disables Agree while loading, on error, on success:false, or on empty text, and retries to recover', async () => {
    useAuthStore.getState().auth.setUser(
      user({
        site_terms_required: true,
        site_terms_version: '3',
        adty_site_owner: false,
      })
    )
    let mode: 'error' | 'false' | 'empty' | 'ok' = 'error'
    api.get.mockImplementation(async () => {
      if (mode === 'error') throw new Error('network error')
      if (mode === 'false') {
        return { data: { success: false, message: 'unavailable' } }
      }
      if (mode === 'empty') {
        return { data: { success: true, data: '   ' } }
      }
      return { data: { success: true, data: 'Loaded terms content' } }
    })

    render(
      wrap(
        <CustomerTermsGate>
          <p>wallet console</p>
        </CustomerTermsGate>
      )
    )

    const agree = screen.getByRole('button', {
      name: 'I agree to the site customer terms',
    })
    expect(agree).toBeDisabled()

    // Error state
    const retry1 = await screen.findByRole('button', { name: 'Retry' })
    expect(agree).toBeDisabled()

    // Retry -> success: false -> still ErrorState and disabled
    mode = 'false'
    await userEvent.click(retry1)
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
    expect(agree).toBeDisabled()

    // Retry -> empty text -> EmptyState with Retry and disabled
    mode = 'empty'
    await userEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    await screen.findByText('No data')
    expect(agree).toBeDisabled()

    // Retry -> valid text -> Agree enabled
    mode = 'ok'
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await screen.findByText('Loaded terms content')
    expect(agree).toBeEnabled()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('leaves the owner center to its own agreement panel', () => {
    useAuthStore.getState().auth.setUser(
      user({
        site_terms_required: true,
        adty_site_owner: true,
      })
    )
    render(
      wrap(
        <CustomerTermsGate>
          <p>owner console</p>
        </CustomerTermsGate>
      )
    )
    expect(screen.getByText('owner console')).toBeTruthy()
  })
})
