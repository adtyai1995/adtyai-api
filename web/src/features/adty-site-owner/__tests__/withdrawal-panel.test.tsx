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
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { WithdrawalPanel } from '../components/withdrawal-panel'

afterEach(() => {
  cleanup()
  localStorage.clear()
  useAuthStore.getState().auth.reset()
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

    vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
      if (
        config?.params &&
        typeof config.params === 'object' &&
        'amount_usd' in config.params
      )
        {return { data: { success: true, data: { payout_amount: '134.90' } } }}
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

  it('recovers the accepted withdrawal after a lost response and a site switch without another freeze', async () => {
    useAuthStore.getState().auth.setUser({ id: 9, username: 'owner', role: 1 })
    let acceptedKey = ''
    vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
      const params = config?.params as
        | { archived_site_id?: number; amount_usd?: string }
        | undefined
      if (url.endsWith('/by-key')) {
        expect(config?.headers?.['Idempotency-Key']).toBe(acceptedKey)
        expect(params?.archived_site_id).toBe(7)
        return { data: { success: true, data: { id: 83 } } }
      }
      if (params?.amount_usd)
        {return { data: { success: true, data: { payout_amount: '134.9' } } }}
      if (url.endsWith('/rules'))
        {return { data: { success: true, data: configuredRules } }}
      return { data: { success: true, data: { items: [] } } }
    })
    const post = vi
      .spyOn(api, 'post')
      .mockImplementation(async (_url, _body, config) => {
        acceptedKey = String(config?.headers?.['Idempotency-Key'])
        throw new Error('response lost')
      })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const panel = (siteId: number) => (
      <QueryClientProvider client={client}>
        <WithdrawalPanel key={siteId} siteId={siteId} />
      </QueryClientProvider>
    )
    const view = render(panel(7))
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('Amount USD'), '20')
    await user.type(screen.getByLabelText('Account'), 'private-account')
    await user.type(screen.getByLabelText('Payee name'), 'Private Payee')
    await user.click(screen.getByRole('checkbox'))
    await user.click(screen.getByRole('button', { name: 'Submit withdrawal' }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(JSON.stringify(localStorage)).not.toMatch(
      /private-account|Private Payee/
    )
    view.rerender(panel(8))
    expect(screen.queryByText('Check previous request')).not.toBeInTheDocument()
    view.rerender(panel(7))
    await user.click(
      await screen.findByRole('button', { name: 'Check previous request' })
    )
    expect(await screen.findByText(/#83/)).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
    await user.click(
      screen.getByRole('button', { name: 'Start a new request' })
    )
    expect(screen.getByLabelText('Account')).toHaveValue('')
    expect(screen.getByRole('checkbox')).not.toBeChecked()
    client.clear()
  })

  it('requires a fresh confirmation when a background quote refresh changes the payout', async () => {
    useAuthStore.getState().auth.setUser({ id: 9, username: 'owner', role: 1 })
    let fee = '1'
    vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
      const params = config?.params as { amount_usd?: string } | undefined
      if (params?.amount_usd)
        {return {
          data: {
            success: true,
            data: { payout_amount: fee === '1' ? '134.9' : '127.8' },
          },
        }}
      if (url.endsWith('/rules'))
        {return {
          data: { success: true, data: { ...configuredRules, fee_usd: fee } },
        }}
      return { data: { success: true, data: { items: [] } } }
    })
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true, data: { id: 81 } } })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <WithdrawalPanel activeSiteId={7} />
      </QueryClientProvider>
    )
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('Amount USD'), '20')
    await user.type(screen.getByLabelText('Account'), 'acc')
    await user.type(screen.getByLabelText('Payee name'), 'Alice')
    await screen.findByText(/134.9 CNY/)
    const checkbox = screen.getByRole('checkbox')
    await user.click(checkbox)
    const submit = screen.getByRole('button', { name: 'Submit withdrawal' })
    expect(submit).toBeEnabled()
    fee = '2'
    await act(async () => {
      await client.invalidateQueries()
    })
    await screen.findByText(/127.8 CNY/)
    expect(checkbox).not.toBeChecked()
    expect(submit).toBeDisabled()
    await user.click(checkbox)
    await user.click(submit)
    expect(await screen.findByText(/#81/)).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][1]).toMatchObject({ expected_payout: '127.8' })
    client.clear()
  })
})
