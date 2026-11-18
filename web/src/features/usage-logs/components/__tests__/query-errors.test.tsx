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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { CommonLogsStats } from '../common-logs-stats'
import { UsageLogsProvider, useLogsViewScope } from '../usage-logs-provider'
import { UsageLogsTable } from '../usage-logs-table'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('shows a retryable error instead of empty usage logs after GET fails', async () => {
  let failed = true
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.includes('/log/')) {
      if (failed) throw new Error('offline')
      return { data: { success: true, data: { items: [], total: 0 } } }
    }
    return { data: { success: true, data: {} } }
  })
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: () => (
      <UsageLogsProvider>
        <UsageLogsTable logCategory='common' />
      </UsageLogsProvider>
    ),
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: ['/usage-logs/common'] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  const retry = await screen.findByRole('button', { name: 'Retry' })
  expect(screen.queryByText('No Logs Found')).not.toBeInTheDocument()
  failed = false
  await userEvent.setup().click(retry)

  expect(await screen.findByText('No Logs Found')).toBeInTheDocument()
  client.clear()
})

function ScopeToggleStatsHarness() {
  const { setViewScope } = useLogsViewScope()
  return (
    <div>
      <button type='button' onClick={() => setViewScope('self')}>
        Switch to self
      </button>
      <CommonLogsStats />
    </div>
  )
}

it('does not reuse all-scope placeholder stats when switching to self scope and shows error+Retry on failure', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: 100 })

  let resolveSelfStats!: (val: unknown) => void
  let failSelfStats = true

  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.startsWith('/api/log/stat')) {
      return {
        data: {
          success: true,
          data: { quota: 500_000, rpm: 42, tpm: 999 },
        },
      }
    }
    if (url.startsWith('/api/log/self/stat')) {
      if (failSelfStats) {
        return await new Promise((resolve) => {
          resolveSelfStats = resolve
        })
      }
      return {
        data: {
          success: true,
          data: { quota: 100_000, rpm: 5, tpm: 50 },
        },
      }
    }
    return { data: { success: true, data: {} } }
  })

  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: () => (
      <UsageLogsProvider>
        <ScopeToggleStatsHarness />
      </UsageLogsProvider>
    ),
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: ['/usage-logs/common'] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )

  expect(await screen.findByText('999')).toBeInTheDocument()
  expect(screen.getByText('42')).toBeInTheDocument()

  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Switch to self' }))

  // While self stats request is pending, previous all-scope stats (42 / 999) must not be shown
  expect(screen.queryByText('999')).not.toBeInTheDocument()
  expect(screen.queryByText('42')).not.toBeInTheDocument()

  // When self stats query fails, it shows Failed to load + Retry instead of 0
  await act(async () => {
    resolveSelfStats({ data: { success: false, message: 'stat error' } })
  })
  const retry = await screen.findByRole('button', { name: 'Retry' })
  expect(screen.getByRole('alert')).toHaveTextContent('Failed to load')
  expect(screen.queryByText('RPM')).not.toBeInTheDocument()

  failSelfStats = false
  await user.click(retry)
  expect(await screen.findByText('50')).toBeInTheDocument()
  expect(screen.getByText('5')).toBeInTheDocument()
  client.clear()
})
