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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import * as apiModule from '@/lib/api'
import { formatQuota } from '@/lib/format'

import { WalletLotProjectionLines } from '../components/wallet-lot-projection'
import { WalletStatsCard } from '../components/wallet-stats-card'
import { Wallet } from '../index'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    Link: ({
      children,
      to,
      hash,
      ...rest
    }: {
      children?: ReactNode
      to?: string
      hash?: string
      [key: string]: unknown
    }) => (
      <a href={`${to ?? ''}${hash ? `#${hash}` : ''}`} {...rest}>
        {children}
      </a>
    ),
  }
})

it.each([true, false])(
  'shows expiring gifts separately from permanent gifts, compact=%s',
  (compact) => {
    render(
      <WalletLotProjectionLines
        compact={compact}
        projection={{
          principal_quota: 1_000_000,
          bonus_quota: 500_000,
          total_quota: 2_000_000,
          principal_usd: '2',
          bonus_usd: '1',
          total_usd: '4',
          expiring_bonus_quota: 500_000,
          expiring_bonus_usd: '1',
        }}
      />
    )
    expect(screen.getByText(/Expiring bonus/i)).toBeVisible()
    expect(screen.getByText(/permanent bonus/i)).toBeVisible()
    expect(screen.getAllByText('$1')).toHaveLength(2)
    expect(screen.getByText('$2')).toBeVisible()
  }
)

beforeEach(() => {
  localStorage.setItem(
    'status',
    JSON.stringify({ quota_per_unit: 500000, display_in_currency: true })
  )
})

function wrapper(props: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

function mockWalletChildGets() {
  vi.spyOn(apiModule.api, 'get').mockImplementation(async (url) => {
    const path = String(url)
    if (path === '/api/adty/topup/info') {
      return {
        data: {
          success: true,
          data: {
            tiers: [{ amount: 50, rate: 0 }],
            bonus_rates: {},
            min_topup: 1,
            methods: [],
            price_cny_per_usd: 7.3,
            usdt_order_minutes: 15,
            epay_order_minutes: 10,
            pending: null,
          },
        },
      }
    }
    if (path === '/api/adty/teams/mine') {
      return { data: { success: true, data: { team: null } } }
    }
    if (path === '/api/adty/site-commissions') {
      return { data: { success: true, data: { items: [] } } }
    }
    if (path === '/api/adty/topup/self') {
      return {
        data: {
          success: true,
          data: { page: 1, page_size: 10, total: 0, items: [] },
        },
      }
    }
    return { data: { success: true, data: {} } }
  })
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('WalletStatsCard', () => {
  it('renders unavailable state instead of stale balance when error is true', () => {
    render(
      <WalletStatsCard
        user={{
          id: 1,
          username: 'alice',
          quota: 5_000_000,
          used_quota: 500_000,
          request_count: 12,
          group: 'default',
        }}
        loading={false}
        error={true}
      />
    )
    expect(screen.getByText('Balance unavailable')).toBeInTheDocument()
    expect(screen.queryByText(formatQuota(5_000_000))).not.toBeInTheDocument()
  })
})

describe('Wallet balance refresh failure handling', () => {
  it('clears stale balance, shows Balance unavailable and Retry when refresh after redemption fails, and recovers on retry', async () => {
    mockWalletChildGets()
    let selfCall = 0
    vi.spyOn(apiModule, 'getSelf').mockImplementation(async () => {
      selfCall += 1
      if (selfCall === 1) {
        return {
          success: true,
          message: '',
          data: {
            id: 1,
            username: 'alice',
            quota: 5_000_000,
            used_quota: 0,
            request_count: 3,
            group: 'default',
          },
        }
      }
      if (selfCall === 2) {
        return {
          success: false,
          message: 'Temporary database error',
        }
      }
      return {
        success: true,
        message: '',
        data: {
          id: 1,
          username: 'alice',
          quota: 10_000_000,
          used_quota: 0,
          request_count: 3,
          group: 'default',
        },
      }
    })

    vi.spyOn(apiModule.api, 'post').mockImplementation(async (url) => {
      if (String(url) === '/api/adty/vouchers/redeem') {
        return {
          data: {
            success: true,
            data: { trade_no: 'VCH-100' },
          },
        }
      }
      return { data: { success: true, data: {} } }
    })

    const user = userEvent.setup()
    render(<Wallet />, { wrapper })

    expect(await screen.findByText(formatQuota(5_000_000))).toBeInTheDocument()

    // Trigger balance refresh via voucher redemption; getSelf() fails on call #2
    await user.type(screen.getByLabelText('Recharge code'), 'ABCDEFGH23456789')
    await user.click(screen.getByRole('button', { name: 'Redeem' }))

    expect(await screen.findByText('Balance unavailable')).toBeInTheDocument()
    expect(screen.queryByText(formatQuota(5_000_000))).not.toBeInTheDocument()

    const retryBtn = await screen.findByRole('button', { name: 'Retry' })
    await user.click(retryBtn)

    await waitFor(() =>
      expect(screen.getByText(formatQuota(10_000_000))).toBeInTheDocument()
    )
    expect(screen.queryByText('Balance unavailable')).not.toBeInTheDocument()
  })

  it('shows unavailable status and retries when getSelf throws a network error', async () => {
    mockWalletChildGets()
    let failed = true
    vi.spyOn(apiModule, 'getSelf').mockImplementation(async () => {
      if (failed) {
        throw new Error('network offline')
      }
      return {
        success: true,
        message: '',
        data: {
          id: 1,
          username: 'alice',
          quota: 7_500_000,
          used_quota: 0,
          request_count: 1,
          group: 'default',
        },
      }
    })

    const user = userEvent.setup()
    render(<Wallet />, { wrapper })

    expect(await screen.findByText('Balance unavailable')).toBeInTheDocument()
    const retryBtn = await screen.findByRole('button', { name: 'Retry' })
    failed = false
    await user.click(retryBtn)

    expect(await screen.findByText(formatQuota(7_500_000))).toBeInTheDocument()
  })
})
