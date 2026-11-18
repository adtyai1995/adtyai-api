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
import { createElement, type ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useSystemConfig } from '../use-system-config'

const ensureStatus = vi.hoisted(() => vi.fn())
const handleServerError = vi.hoisted(() => vi.fn())
const getServerErrorMessage = vi.hoisted(() =>
  vi.fn((_err: unknown, fallback?: string) => fallback ?? 'Failed to load')
)

vi.mock('@/lib/status-query', () => ({
  ensureStatus,
}))

vi.mock('@/lib/handle-server-error', () => ({
  handleServerError,
}))

vi.mock('@/lib/server-error-message', () => ({
  getServerErrorMessage,
}))

describe('useSystemConfig', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('surfaces errors via handleServerError and error state without logging raw Axios error objects to console.error', async () => {
    const consoleErrorSpy = vi
      .spyOn(console, 'error')
      .mockImplementation(() => {})
    const rawAxiosError = {
      isAxiosError: true,
      message: 'Request failed with status code 500',
      config: {
        headers: {
          Authorization: 'Bearer secret-token-do-not-leak',
        },
      },
      response: {
        status: 500,
        data: { success: false, message: 'Database unavailable' },
      },
    }
    getServerErrorMessage.mockReturnValue('Database unavailable')
    ensureStatus.mockRejectedValueOnce(rawAxiosError)

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    function wrapper({ children }: { children: ReactNode }) {
      return createElement(QueryClientProvider, { client }, children)
    }

    const { result } = renderHook(
      () => useSystemConfig({ autoLoad: true }),
      { wrapper }
    )

    await waitFor(() => expect(result.current.loading).toBe(false))
    await waitFor(() => expect(handleServerError).toHaveBeenCalledTimes(1))

    expect(consoleErrorSpy).not.toHaveBeenCalled()
    expect(handleServerError).toHaveBeenCalledWith(
      rawAxiosError,
      'Failed to load system config'
    )
    expect(result.current.error).toBe('Database unavailable')
    consoleErrorSpy.mockRestore()
  })
})
