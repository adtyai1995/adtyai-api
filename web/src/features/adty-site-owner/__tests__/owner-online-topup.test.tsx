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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AxiosAdapter } from 'axios'
import { afterEach, beforeEach, expect, it } from 'vitest'

import type { AdtyTopupInfo } from '@/features/wallet/adty/types'
import { api } from '@/lib/api'

import { OwnerSelfUsePanel } from '../components/owner-self-use-panel'
import { SettlementSheetPanel } from '../components/settlement-sheet-panel'

const business = 'owner_self_use_topup'
const usdt = { id: 'usdt.trc20', kind: 'usdt' as const, available: true }
const alipay = {
  id: 'alipay',
  kind: 'epay' as const,
  available: true,
  channel_id: 7,
}
const adapter = api.defaults.adapter
let client: QueryClient
const returnedOrder = {
  trade_no: 'OWNER-RETURN',
  status: 'success',
  payment_method: 'alipay',
  payment_provider: 'adty_epay',
  amount: 100,
  ext: { business: 'owner_self_use_topup', face_usd: '100', bonus_usd: '0' },
}
let info: AdtyTopupInfo
let requests: { path: string; params: unknown; body: unknown }[]

beforeEach(() => {
  requests = []
  info = {
    methods: [usdt],
    tiers: [{ amount: 100, rate: 20 }],
    bonus_rates: { '100': 20 },
    min_topup: 1,
    price_cny_per_usd: 7,
    usdt_order_minutes: 15,
    epay_order_minutes: 10,
    pending: null,
    scenes: [
      {
        key: 'owner_first',
        name: 'Other checkout',
        min_usd: '1',
        max_usd: '0',
        methods: [],
      },
      {
        key: 'owner_checkout',
        name: 'Owner checkout',
        min_usd: '1',
        max_usd: '0',
        methods: [usdt, alipay],
      },
    ],
  }
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  api.defaults.adapter = (async (config) => {
    const path = config.url ?? ''
    const body = config.data && JSON.parse(config.data)
    requests.push({ path, params: config.params, body })
    let data: unknown
    switch (path) {
      case '/api/adty/site/wallets/self-use':
        data = { available_usd: '25' }
        break
      case '/api/adty/site/wallets':
        data = []
        break
      case '/api/adty/site/settlement-sheets':
        data = [
          {
            business_day: '2026-11-18',
            topup_face_usd: '45',
            owner_self_use_topup_usd: '100',
            owner_self_use_refund_usd: '9.5',
            owner_self_use_voucher_usd: '20',
          },
        ]
        break
      case '/api/adty/site/self-use/transfers':
      case '/api/adty/site/self-use/usage':
        data = { page: 1, page_size: 10, items: [], total: 0 }
        break
      case '/api/adty/topup/self': {
        const items =
          config.params?.trade_no && config.params.business === business
            ? [returnedOrder]
            : []
        data = { page: 1, page_size: 10, items, total: items.length }
        break
      }
      case '/api/adty/topup/orders/OWNER-RETURN':
        data = returnedOrder
        break
      case '/api/adty/topup/info':
        data = info
        break
      case '/api/adty/topup/quote':
        data = {
          available: true,
          quote: {
            face_usd: '100',
            bonus_rate: '0',
            bonus_usd: '0',
            credit_quota: 50000000,
            pay_currency: body.method === 'alipay' ? 'CNY' : 'USDT',
            pay_amount: body.method === 'alipay' ? '700' : '100',
            price_cny_per_usd: '7',
            topup_group_ratio: '1',
          },
        }
        break
      case '/api/adty/epay/pay':
      case '/api/user/bepusdt/pay':
      case '/api/adty/topup/orders/OWNER-100':
        data = { trade_no: 'OWNER-100', status: 'pending', ext: null }
        break
      default:
        throw new Error(`Unexpected request ${path}`)
    }
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data },
    }
  }) satisfies AxiosAdapter
})

afterEach(() => {
  api.defaults.adapter = adapter
  client.clear()
})

function openPanel(returnTradeNo?: string) {
  render(
    <QueryClientProvider client={client}>
      <OwnerSelfUsePanel returnTradeNo={returnTradeNo} />
    </QueryClientProvider>
  )
}

it.each([
  { name: 'missing', scenes: undefined },
  { name: 'empty', scenes: [] },
])(
  'shows online top-up as unavailable for $name scenes even if wallet methods are present',
  async ({ scenes }) => {
    info.scenes = scenes
    openPanel()
    expect(
      await screen.findByText('Online top-up is unavailable for this site.')
    ).toBeVisible()
    expect(
      screen.queryByRole('button', { name: /TRC20|Pay now/ })
    ).not.toBeInTheDocument()
    expect(
      requests.find((req) => req.path.endsWith('/topup/info'))?.params
    ).toEqual({ business })
    await waitFor(() =>
      expect(
        requests.find((req) => req.path.endsWith('/topup/self'))?.params
      ).toEqual({ business, p: 1, page_size: 10 })
    )
  }
)

it.each([
  {
    label: /TRC20/,
    method: 'usdt.trc20',
    path: '/api/user/bepusdt/pay',
    fields: { chain: 'usdt.trc20' },
    quoteFields: {},
  },
  {
    label: /Alipay/,
    method: 'alipay',
    path: '/api/adty/epay/pay',
    fields: { method: 'alipay', device: 'pc', channel_id: 7 },
    quoteFields: { channel_id: 7 },
  },
])(
  'quotes and opens $method checkout for the selected owner scene at face value',
  async (provider) => {
    const user = userEvent.setup()
    openPanel()
    await user.selectOptions(
      await screen.findByLabelText('Payment scene'),
      'owner_checkout'
    )
    await user.click(screen.getByRole('button', { name: '$100' }))
    expect(screen.getByTestId('adty-credit')).toHaveTextContent('$100.00')
    expect(screen.queryByText(/Bonus 20%/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: provider.label }))
    const pay = screen.getByRole('button', { name: 'Pay now' })
    await waitFor(() => expect(pay).toBeEnabled())
    const common = { business, amount: 100, payment_scene: 'owner_checkout' }
    expect(requests.find((req) => req.path.endsWith('/quote'))?.body).toEqual({
      ...common,
      method: provider.method,
      ...provider.quoteFields,
    })
    await user.click(pay)
    expect(await screen.findByRole('dialog')).toBeVisible()
    await waitFor(() =>
      expect(requests.find((req) => req.path === provider.path)?.body).toEqual({
        ...common,
        ...provider.fields,
      })
    )
  }
)

it('opens the returned owner order even with no enabled scenes and never creates a second checkout', async () => {
  info.scenes = []
  openPanel('OWNER-RETURN')
  const dialog = await screen.findByRole('dialog', { name: 'Pay with Alipay' })
  expect(await within(dialog).findByText('Credited $100.00')).toBeVisible()
  await userEvent.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(requests.some((request) => request.path.endsWith('/pay'))).toBe(false)
})

it('rejects a partial trade number instead of opening a different order', async () => {
  openPanel('OWNER-RET')
  expect(
    await screen.findByText('This order was not found for this site.')
  ).toBeVisible()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('reports owner online principal, vouchers and refunds separately from customer top-ups', async () => {
  render(
    <QueryClientProvider client={client}>
      <SettlementSheetPanel />
    </QueryClientProvider>
  )
  for (const [label, amount] of [
    ['Top-up', '$45.00'],
    ['Owner self-use top-ups', '$100.00'],
    ['Owner self-use refunds', '$9.50'],
    ['Owner self-use vouchers', '$20.00'],
  ]) {
    const line = await screen.findByText(label)
    expect(line.nextElementSibling).toHaveTextContent(amount)
  }
})
