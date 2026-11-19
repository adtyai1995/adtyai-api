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
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { useApiBaseUrl } from '../api'

const http = vi.hoisted(() => ({ get: vi.fn(), getStatus: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  api: { get: http.get },
  getStatus: http.getStatus,
}))

function wrapper(props: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

function siteResponse(apiBaseUrl: string) {
  return {
    data: {
      success: true,
      data: { api_base_url: apiBaseUrl, refund_policy_enabled: false },
    },
  }
}

afterEach(() => {
  window.localStorage.clear()
  http.get.mockReset()
  http.getStatus.mockReset()
})

describe('useApiBaseUrl', () => {
  it('prefers the subsite status server_address over global api_base_url on a subsite', async () => {
    http.get.mockResolvedValue(siteResponse('https://main-api.example.com/'))
    http.getStatus.mockResolvedValue({
      server_address: 'https://sub-api.example.com/',
      adty_site: { id: 3, status: 'active', subdomain: 'sub' },
    })
    const { result } = renderHook(() => useApiBaseUrl(), { wrapper })
    await waitFor(() =>
      expect(result.current).toBe('https://sub-api.example.com')
    )
  })

  it('never advertises the website or global address when a subsite has no API address', async () => {
    http.get.mockResolvedValue(siteResponse('https://main-api.example.com'))
    http.getStatus.mockResolvedValue({
      server_address: '   ',
      adty_site: { id: 3, status: 'active', subdomain: 'sub' },
    })
    const { result } = renderHook(() => useApiBaseUrl(), { wrapper })
    await waitFor(() => expect(result.current).toBe(''))
  })

  it('continues to use the global api_base_url on the main site', async () => {
    http.get.mockResolvedValue(siteResponse('https://main-api.example.com/'))
    http.getStatus.mockResolvedValue({
      server_address: 'https://srv.example.com',
    })
    const { result } = renderHook(() => useApiBaseUrl(), { wrapper })
    await waitFor(() =>
      expect(result.current).toBe('https://main-api.example.com')
    )
  })
})
