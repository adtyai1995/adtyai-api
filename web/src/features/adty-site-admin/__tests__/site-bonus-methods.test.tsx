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

import {
  SiteBonusMethodAuth,
  SiteDetailSheet,
} from '../components/site-detail-sheet'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

describe('SiteBonusMethodAuth', () => {
  it('hides bonus method authorization from non-Root admins', () => {
    useAuthStore.getState().auth.setUser({ id: 2, username: 'admin', role: 10 })
    const get = vi.spyOn(api, 'get')
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const { container } = render(
      <QueryClientProvider client={client}>
        <SiteBonusMethodAuth siteId={5} />
      </QueryClientProvider>
    )
    expect(container).toBeEmptyDOMElement()
    expect(get).not.toHaveBeenCalled()
    client.clear()
  })

  it('loads adty_topup_setting.site_method_auth for Root and toggles alipay/wxpay/usdt via PUT', async () => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
    let authJson = JSON.stringify({ 5: ['alipay', 'usdt'] })
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      const path = String(url)
      if (path === '/api/option/') {
        return {
          data: {
            success: true,
            message: '',
            data: [
              {
                key: 'adty_topup_setting.site_method_auth',
                value: authJson,
              },
            ],
          },
        }
      }
      if (path.endsWith('/stats')) {
        return { data: { success: true, data: { stats: { total: {} } } } }
      }
      if (path.endsWith('/customers')) {
        return { data: { success: true, data: { items: [], total: 0 } } }
      }
      return {
        data: {
          success: true,
          data: {
            site: {
              id: 5,
              status: 'active',
              host: 'shop5.example',
              owner_user_id: 9,
              created_at: 1,
              close_execute_at: 0,
              contact_info: '',
              cost_factor: null,
              max_markup: null,
              brand: '',
              customers: 0,
            },
            domains: [],
            sender_domains: [],
            audits: [],
          },
        },
      }
    })
    const put = vi
      .spyOn(api, 'put')
      .mockImplementation(async (_url, payload) => {
        const body = payload as { method: string; allowed: boolean }
        const parsed = JSON.parse(authJson) as Record<string, string[]>
        const current = new Set(parsed['5'] ?? [])
        if (body.allowed) current.add(body.method)
        else current.delete(body.method)
        parsed['5'] = [...current]
        authJson = JSON.stringify(parsed)
        return {
          data: {
            success: true,
            data: { site_id: 5, allowed: body.allowed },
          },
        }
      })

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <SiteDetailSheet siteId={5} onOpenChange={() => {}} />
      </QueryClientProvider>
    )

    const alipay = await screen.findByRole('switch', { name: 'Alipay' })
    const wxpay = screen.getByRole('switch', { name: 'WeChat Pay' })
    const usdt = screen.getByRole('switch', { name: 'USDT' })

    expect(alipay).toBeChecked()
    expect(wxpay).not.toBeChecked()
    expect(usdt).toBeChecked()

    const user = userEvent.setup()
    await user.click(wxpay)
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/api/adty/admin/sites/5/bonus-methods', {
        method: 'wxpay',
        allowed: true,
      })
    )
    await waitFor(() => expect(wxpay).toBeChecked())

    await user.click(alipay)
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/api/adty/admin/sites/5/bonus-methods', {
        method: 'alipay',
        allowed: false,
      })
    )
    await waitFor(() => expect(alipay).not.toBeChecked())
    client.clear()
  })

  it.each(['{broken', '["not-a-map"]', '{"5":"not-an-array"}'])(
    'fails closed on malformed site_method_auth JSON (%s) and shows ErrorState with retry',
    async (malformed) => {
      useAuthStore
        .getState()
        .auth.setUser({ id: 1, username: 'root', role: 100 })
      let currentVal = malformed
      vi.spyOn(api, 'get').mockImplementation(async () => ({
        data: {
          success: true,
          message: '',
          data: [
            {
              key: 'adty_topup_setting.site_method_auth',
              value: currentVal,
            },
          ],
        },
      }))
      const put = vi.spyOn(api, 'put')
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      })
      render(
        <QueryClientProvider client={client}>
          <SiteBonusMethodAuth siteId={5} />
        </QueryClientProvider>
      )

      const retry = await screen.findByRole('button', { name: 'Retry' })
      for (const name of ['Alipay', 'WeChat Pay', 'USDT']) {
        const sw = screen.getByRole('switch', { name })
        expect(sw).not.toBeChecked()
        expect(sw).toHaveAttribute('aria-disabled', 'true')
      }
      expect(put).not.toHaveBeenCalled()

      currentVal = JSON.stringify({ 5: ['usdt'] })
      await userEvent.setup().click(retry)
      await waitFor(() =>
        expect(
          screen.queryByRole('button', { name: 'Retry' })
        ).not.toBeInTheDocument()
      )
      expect(screen.getByRole('switch', { name: 'USDT' })).toBeChecked()
      expect(
        screen.getByRole('switch', { name: 'USDT' })
      ).not.toHaveAttribute('aria-disabled', 'true')
      client.clear()
    }
  )

  it('shows ErrorState when getSystemOptions fails and recovers on Retry', async () => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
    let fail = true
    vi.spyOn(api, 'get').mockImplementation(async () => {
      if (fail) throw new Error('network error')
      return {
        data: {
          success: true,
          message: '',
          data: [
            {
              key: 'adty_topup_setting.site_method_auth',
              value: JSON.stringify({ 5: ['alipay'] }),
            },
          ],
        },
      }
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <SiteBonusMethodAuth siteId={5} />
      </QueryClientProvider>
    )

    const retry = await screen.findByRole('button', { name: 'Retry' })
    fail = false
    await userEvent.setup().click(retry)
    await waitFor(() =>
      expect(screen.getByRole('switch', { name: 'Alipay' })).toBeChecked()
    )
    client.clear()
  })
})
