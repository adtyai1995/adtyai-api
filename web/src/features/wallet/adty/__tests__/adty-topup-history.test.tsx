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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { AdtyTopupHistory } from '../components/adty-topup-history'
import type { AdtyOrder } from '../types'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: { children: ReactNode; to?: string; hash?: string }) => (
    <a href={`${props.to ?? ''}#${props.hash ?? ''}`}>{props.children}</a>
  ),
}))

const sampleOrder: AdtyOrder = {
  id: 1,
  user_id: 7,
  trade_no: 'EPU700001',
  payment_method: 'usdt.trc20',
  payment_provider: 'bepusdt',
  status: 'success',
  create_time: 1700001000,
  complete_time: 1700001060,
  amount: 100,
  money: 100,
  ext: {
    trade_no: 'EPU700001',
    provider: 'bepusdt',
    method: 'usdt.trc20',
    payment_scene: 'wallet_topup',
    payment_scene_name: 'Wallet top-up',
    payment_scene_version: 1,
    face_usd: '100',
    bonus_rate: '0.025',
    bonus_usd: '2.5',
    credit_quota: 51250000,
    pay_amount: '100',
    pay_currency: 'USDT',
    price_snapshot: '0',
    expire_at: 1700001900,
    gateway_trade_id: 'GW-1',
    pay_address: 'Txxxx',
    pay_url: '',
    qr_code: '',
    url_scheme: '',
    channel_id: 0,
    paid_amount: '100',
    paid_currency: 'USDT',
    paid_tx_hash: '0xabc',
    refunded_at: 0,
  },
}

function wrapper(props: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('AdtyTopupHistory', () => {
  it('filters personal top-up history by status, payment method, order number, and date range and resets filters', async () => {
    const getSpy = vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          page: 1,
          page_size: 10,
          total: 1,
          items: [sampleOrder],
        },
      },
    })

    const user = userEvent.setup()
    render(<AdtyTopupHistory />, { wrapper })

    expect(await screen.findByText('EPU700001')).toBeInTheDocument()
    expect(getSpy).toHaveBeenLastCalledWith('/api/adty/topup/self', {
      params: { p: 1, page_size: 10 },
    })

    await user.selectOptions(screen.getByLabelText('Status'), 'success')
    await waitFor(() =>
      expect(getSpy).toHaveBeenLastCalledWith('/api/adty/topup/self', {
        params: { p: 1, page_size: 10, status: 'success' },
      })
    )

    await user.selectOptions(
      screen.getByLabelText('Payment method'),
      'usdt.trc20'
    )
    await waitFor(() =>
      expect(getSpy).toHaveBeenLastCalledWith('/api/adty/topup/self', {
        params: {
          p: 1,
          page_size: 10,
          status: 'success',
          payment_method: 'usdt.trc20',
        },
      })
    )

    await user.type(screen.getByLabelText('Order number'), 'EPU700001')
    await waitFor(() =>
      expect(getSpy).toHaveBeenLastCalledWith('/api/adty/topup/self', {
        params: {
          p: 1,
          page_size: 10,
          status: 'success',
          payment_method: 'usdt.trc20',
          trade_no: 'EPU700001',
        },
      })
    )

    await user.click(screen.getByRole('button', { name: 'Reset' }))
    await waitFor(() =>
      expect(getSpy).toHaveBeenLastCalledWith('/api/adty/topup/self', {
        params: { p: 1, page_size: 10 },
      })
    )
  })
})
