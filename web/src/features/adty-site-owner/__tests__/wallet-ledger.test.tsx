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
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { WalletLedgerPanel } from '../components/wallet-ledger-panel'
import { marginCycleSummary } from '../lib/site-owner'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('computes marginCycleSummary using scaled BigInt arithmetic', () => {
  expect(
    marginCycleSummary({
      balance: '70',
      frozen_usd: '10',
      cycle_tier_usd: '100',
      cycle_used_usd: '30',
    })
  ).toEqual({
    tierUsd: '100',
    usedUsd: '30',
    limit90Usd: '90',
    availableUsd: '50',
  })

  // Cash room limited: limit90 - used - frozen = 90 - 10 - 10 = 70, balance - frozen - floor10 = 50 - 10 - 10 = 30 -> 30
  expect(
    marginCycleSummary({
      balance: '50',
      frozen_usd: '10',
      cycle_tier_usd: '100',
      cycle_used_usd: '10',
    })
  ).toEqual({
    tierUsd: '100',
    usedUsd: '10',
    limit90Usd: '90',
    availableUsd: '30',
  })

  // Clamped at 0 when overused or under floor
  expect(
    marginCycleSummary({
      balance: '15',
      frozen_usd: '10',
      cycle_tier_usd: '100',
      cycle_used_usd: '85',
    })
  ).toEqual({
    tierUsd: '100',
    usedUsd: '85',
    limit90Usd: '90',
    availableUsd: '0',
  })

  // High precision decimals without float drift
  expect(
    marginCycleSummary({
      balance: '0.3',
      frozen_usd: '0.1',
      cycle_tier_usd: '0.3',
      cycle_used_usd: '0.05',
    })
  ).toEqual({
    tierUsd: '0.3',
    usedUsd: '0.05',
    limit90Usd: '0.27',
    availableUsd: '0.12',
  })
})

it('shows earnings and margin wallets with margin cycle summary and without operating wallet or operator ids', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/adty/site/wallets') {
      return {
        data: {
          success: true,
          data: [
            { id: 1, kind: 'operating', balance: '999' },
            { id: 2, kind: 'earnings', balance: '20', frozen_usd: '5' },
            {
              id: 3,
              kind: 'margin',
              balance: '70',
              frozen_usd: '10',
              cycle_tier_usd: '100',
              cycle_used_usd: '30',
            },
          ],
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          total: 1,
          page: 1,
          page_size: 20,
          items: [
            {
              id: 4,
              site_id: 7,
              wallet_id: 2,
              wallet_kind: 'earnings',
              delta: '0',
              hold_delta: '5',
              balance_before: '20',
              balance_after: '20',
              type: 'voucher_freeze',
              ref_type: 'voucher',
              ref_id: 'abc123',
              reason: 'voucher issuance freeze',
              operator_id: 70,
              allow_overdraft: false,
              created_at: 1,
            },
          ],
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <WalletLedgerPanel />
    </QueryClientProvider>
  )
  const balances = await screen.findByRole('region', { name: 'Wallets' })
  expect(balances).toHaveTextContent('Earnings wallet')
  expect(balances).toHaveTextContent('20 USD')
  expect(balances).toHaveTextContent('Margin wallet')
  expect(balances).toHaveTextContent('70 USD')
  expect(balances).toHaveTextContent('Margin wallet · Frozen')
  expect(balances).toHaveTextContent('10 USD')
  expect(balances).toHaveTextContent('Margin wallet · Tier')
  expect(balances).toHaveTextContent('100 USD')
  expect(balances).toHaveTextContent('Margin wallet · Used')
  expect(balances).toHaveTextContent('30 USD')
  expect(balances).toHaveTextContent('Margin wallet · Limit')
  expect(balances).toHaveTextContent('90 USD')
  expect(balances).toHaveTextContent('Margin wallet · Available')
  expect(balances).toHaveTextContent('50 USD')
  expect(balances).not.toHaveTextContent('Operating wallet')
  expect(balances).not.toHaveTextContent('999')
  const table = await screen.findByRole('table', { name: 'Wallet ledger' })
  expect(table).toHaveTextContent('Recharge code issued')
  expect(table).toHaveTextContent('+5 USD')
  expect(within(table).queryByText('#70')).not.toBeInTheDocument()
  const filter = screen.getByRole('combobox', { name: 'Wallet' })
  expect(
    within(filter)
      .getAllByRole('option')
      .map((option) => option.getAttribute('value'))
  ).toEqual(['', 'earnings', 'margin'])
  expect(get).toHaveBeenCalledWith('/api/adty/site/wallet-ledgers', {
    params: { kind: undefined, p: 1, page_size: 20 },
  })
  client.clear()
})
