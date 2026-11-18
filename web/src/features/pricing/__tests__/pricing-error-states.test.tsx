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
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ModelDetails } from '../components/model-details'
import { Pricing } from '../index'

const usePricingDataMock = vi.hoisted(() => vi.fn())
const navigateMock = vi.hoisted(() => vi.fn())

vi.mock('../hooks/use-pricing-data', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../hooks/use-pricing-data')>()
  return {
    ...actual,
    usePricingData: usePricingDataMock,
  }
})

vi.mock('../components/model-details-performance', () => ({
  ModelDetailsPerformance: () => null,
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    Link: ({
      children,
      to,
      ...rest
    }: {
      children?: React.ReactNode
      to?: string
      [key: string]: unknown
    }) => (
      <a href={typeof to === 'string' ? to : '#'} {...rest}>
        {children}
      </a>
    ),
    useNavigate: () => navigateMock,
    useSearch: () => ({}),
    useParams: () => ({ modelId: 'gpt-4o' }),
    useRouterState: () => ({ location: { pathname: '/pricing' } }),
  }
})

vi.mock('@/components/layout/header', () => ({
  Header: ({ children }: { children?: React.ReactNode }) => (
    <header>{children}</header>
  ),
}))

vi.mock('@/components/layout/main', () => ({
  Main: ({ children }: { children?: React.ReactNode }) => (
    <main>{children}</main>
  ),
}))

vi.mock('@/components/profile-dropdown', () => ({
  ProfileDropdown: () => null,
}))

vi.mock('@/components/search', () => ({
  Search: () => null,
}))

vi.mock('@/components/theme-switch', () => ({
  ThemeSwitch: () => null,
}))

vi.mock('@/features/adty-groups/hooks/use-group-display', () => ({
  useGroupDisplay: () => ({
    userLabel: (g: string) => g,
    userDescription: () => '',
  }),
}))

vi.mock('@/hooks/use-copy-to-clipboard', () => ({
  useCopyToClipboard: () => ({ copy: vi.fn(), isCopied: () => false }),
}))

describe('Pricing & ModelDetails error states (ISS-R1-0168)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    cleanup()
  })

  it('Pricing renders ErrorState with retry button instead of "No models found" when /api/pricing fails', async () => {
    const refetch = vi.fn()
    usePricingDataMock.mockReturnValue({
      models: [],
      vendors: [],
      groupRatio: {},
      usableGroup: {},
      supportedEndpoint: {},
      autoGroups: [],
      priceRate: 1,
      usdExchangeRate: 1,
      hasData: false,
      isLoading: false,
      isFetching: false,
      isError: true,
      error: new Error('Pricing service down'),
      refetch,
    })

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const wrap = (node: ReactNode) => (
      <QueryClientProvider client={client}>{node}</QueryClientProvider>
    )
    const user = userEvent.setup()
    render(wrap(<Pricing />))

    expect(screen.getByText('Failed to load models')).toBeVisible()
    expect(screen.getByText('Pricing service down')).toBeVisible()
    expect(screen.queryByText('No models found')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('ModelDetails renders ErrorState with retry button instead of "Model not found" when /api/pricing fails', async () => {
    const refetch = vi.fn()
    usePricingDataMock.mockReturnValue({
      models: [],
      vendors: [],
      groupRatio: {},
      usableGroup: {},
      supportedEndpoint: {},
      autoGroups: [],
      priceRate: 1,
      usdExchangeRate: 1,
      hasData: false,
      isLoading: false,
      isFetching: false,
      isError: true,
      error: new Error('Gateway 502'),
      refetch,
    })

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const wrap = (node: ReactNode) => (
      <QueryClientProvider client={client}>{node}</QueryClientProvider>
    )
    const user = userEvent.setup()
    render(wrap(<ModelDetails />))

    expect(screen.getByText('Failed to load model details')).toBeVisible()
    expect(screen.getByText('Gateway 502')).toBeVisible()
    expect(
      screen.queryByText('The model "{{name}}" does not exist')
    ).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalledTimes(1)
  })
})
