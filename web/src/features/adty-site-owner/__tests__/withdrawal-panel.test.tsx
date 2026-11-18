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
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { WithdrawalPanel } from '../components/withdrawal-panel'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const configuredRules = {
  configured: true,
  min_usd: '10',
  fee_usd: '1',
  alipay_cny_rate: '7.1',
  usdt_usd_rate: '1',
  methods: ['alipay', 'usdt'],
}

describe('WithdrawalPanel', () => {
  it('distinguishes loading, error with retry, and empty states for withdrawal history while preserving form draft', async () => {
    let historyState: 'pending' | 'error' | 'empty' = 'pending'
    const pending: { resolve?: (value: unknown) => void } = {}

    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (url === '/api/adty/site/withdrawals/rules') {
        return { data: { success: true, data: configuredRules } }
      }
      if (url === '/api/adty/site/withdrawals') {
        if (historyState === 'pending') {
          return new Promise((resolve) => {
            pending.resolve = resolve
          })
        }
        if (historyState === 'error') {
          throw new Error('history read failed')
        }
        return { data: { success: true, data: { items: [] } } }
      }
      throw new Error(`unexpected url: ${String(url)}`)
    })

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <WithdrawalPanel />
      </QueryClientProvider>
    )

    const historySection = await screen.findByRole('region', {
      name: 'Withdrawal history',
    })
    expect(within(historySection).getByText('Loading...')).toBeInTheDocument()

    // Enter draft while history is loading/failing
    const amountInput = await screen.findByLabelText('Amount USD')
    const accountInput = screen.getByLabelText('Account')
    const user = userEvent.setup()
    await user.type(amountInput, '25')
    await user.type(accountInput, 'alice@example.com')

    // Reject the pending history request -> error state
    historyState = 'error'
    pending.resolve?.({ data: { success: false, message: 'failed' } })

    const retryBtn = await within(historySection).findByRole('button', {
      name: 'Retry',
    })
    expect(
      within(historySection).queryByText('No withdrawals yet')
    ).not.toBeInTheDocument()
    expect(amountInput).toHaveValue('25')
    expect(accountInput).toHaveValue('alice@example.com')

    // Retry -> empty state, preserving draft
    historyState = 'empty'
    await user.click(retryBtn)
    expect(
      await within(historySection).findByText('No withdrawals yet')
    ).toBeInTheDocument()
    expect(amountInput).toHaveValue('25')
    expect(accountInput).toHaveValue('alice@example.com')
    client.clear()
  })

  it('reuses the same Idempotency-Key on retry with the same payload and rotates on payload change or success', async () => {
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (url === '/api/adty/site/withdrawals/rules') {
        return { data: { success: true, data: configuredRules } }
      }
      return { data: { success: true, data: { items: [] } } }
    })

    let postCount = 0
    const postSpy = vi.spyOn(api, 'post').mockImplementation(async () => {
      postCount += 1
      if (postCount === 1 || postCount === 4) {
        throw new Error('ambiguous network timeout')
      }
      return {
        data: {
          success: true,
          data: {
            id: postCount,
            amount_usd: '20',
            fee_usd: '1',
            method: 'alipay',
            network: '',
            status: 'pending',
            rate: '7.1',
            payout_amount: '134.9',
            payout_currency: 'CNY',
          },
        },
      }
    })

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <WithdrawalPanel />
      </QueryClientProvider>
    )

    const user = userEvent.setup()
    const amountInput = await screen.findByLabelText('Amount USD')
    const accountInput = screen.getByLabelText('Account')
    const nameInput = screen.getByLabelText('Payee name')
    const checkbox = screen.getByRole('checkbox')
    const submitBtn = screen.getByRole('button', { name: 'Submit withdrawal' })

    await user.type(amountInput, '20')
    await user.type(accountInput, 'acc-1')
    await user.type(nameInput, 'Alice')
    await user.click(checkbox)
    await user.click(submitBtn)

    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(1))
    const key1 = (
      postSpy.mock.calls[0]?.[2] as { headers?: Record<string, string> }
    )?.headers?.['Idempotency-Key']
    expect(key1).toBeTruthy()

    // Retry same payload after failure -> reuses key1
    await user.click(submitBtn)
    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(2))
    const key2 = (
      postSpy.mock.calls[1]?.[2] as { headers?: Record<string, string> }
    )?.headers?.['Idempotency-Key']
    expect(key2).toBe(key1)

    // After success, submitting same payload again -> rotates key
    await user.type(amountInput, '20')
    await user.type(accountInput, 'acc-1')
    await user.type(nameInput, 'Alice')
    await user.click(checkbox)
    await user.click(submitBtn)
    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(3))
    const key3 = (
      postSpy.mock.calls[2]?.[2] as { headers?: Record<string, string> }
    )?.headers?.['Idempotency-Key']
    expect(key3).not.toBe(key1)

    // Submit and fail, then change payload -> rotates key
    await user.type(amountInput, '30')
    await user.type(accountInput, 'acc-1')
    await user.type(nameInput, 'Alice')
    await user.click(checkbox)
    await user.click(submitBtn)
    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(4))
    const key4 = (
      postSpy.mock.calls[3]?.[2] as { headers?: Record<string, string> }
    )?.headers?.['Idempotency-Key']

    await user.clear(amountInput)
    await user.type(amountInput, '35')
    await user.click(submitBtn)
    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(5))
    const key5 = (
      postSpy.mock.calls[4]?.[2] as { headers?: Record<string, string> }
    )?.headers?.['Idempotency-Key']
    expect(key5).not.toBe(key4)
    client.clear()
  })
})
