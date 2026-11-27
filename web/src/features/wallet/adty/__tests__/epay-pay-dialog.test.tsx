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
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { EpayPayDialog } from '../components/epay-pay-dialog'

const { getAdtyOrderMock } = vi.hoisted(() => ({
  getAdtyOrderMock: vi.fn(),
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      if (!params) return key
      return Object.entries(params).reduce(
        (acc, [paramKey, val]) =>
          acc.replaceAll(`{{${paramKey}}}`, String(val)),
        key
      )
    },
    i18n: { language: 'en', resolvedLanguage: 'en' },
  }),
}))

vi.mock('@/components/dialog', () => ({
  Dialog: ({
    title,
    children,
    footer,
  }: {
    title: ReactNode
    children: ReactNode
    footer?: ReactNode
  }) => (
    <div role='dialog'>
      <h2>{title}</h2>
      <div>{children}</div>
      {footer ? <div>{footer}</div> : null}
    </div>
  ),
}))

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getAdtyOrder: (...args: unknown[]) => getAdtyOrderMock(...args),
    cancelAdtyOrder: vi.fn(),
  }
})

vi.mock('../api-pay', () => ({
  payWithEpay: vi.fn(),
}))

function renderDialog(
  method: 'alipay' | 'wxpay',
  extraProps: Partial<Parameters<typeof EpayPayDialog>[0]> = {}
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <EpayPayDialog
        method={method}
        amount={10}
        tradeNo='ORD-TEST-001'
        device='pc'
        onClose={vi.fn()}
        onSettled={vi.fn()}
        {...extraProps}
      />
    </QueryClientProvider>
  )
}

describe('EpayPayDialog official payment logos', () => {
  beforeEach(() => {
    getAdtyOrderMock.mockResolvedValue({
      trade_no: 'ORD-TEST-001',
      status: 'pending',
      payment_method: 'alipay',
      ext: {
        trade_no: 'ORD-TEST-001',
        pay_amount: '70.00',
        amount: 10,
        credit_quota: 5_000_000,
        qr_code: 'https://qr.alipay.com/test',
        expire_at: Math.floor(Date.now() / 1000) + 600,
      },
    })
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('renders the official Alipay SVG logo in the header title', () => {
    renderDialog('alipay')

    const heading = screen.getByRole('heading', { name: 'Pay with Alipay' })
    const svg = heading.querySelector('svg')
    expect(svg).toBeTruthy()
    expect(svg?.querySelector('rect[fill="#1677FF"]')).toBeTruthy()
  })

  it('renders the official WeChat Pay SVG logo in the header title', () => {
    renderDialog('wxpay')

    const heading = screen.getByRole('heading', { name: 'Pay with WeChat Pay' })
    const svg = heading.querySelector('svg')
    expect(svg).toBeTruthy()
    expect(svg?.querySelector('rect[fill="#07C160"]')).toBeTruthy()
  })

  it('renders a primary Open payment page link button on desktop when pay_url exists without qr_code', async () => {
    getAdtyOrderMock.mockResolvedValue({
      trade_no: 'ORD-TEST-001',
      status: 'pending',
      payment_method: 'alipay',
      ext: {
        trade_no: 'ORD-TEST-001',
        pay_amount: '70.00',
        face_usd: '10',
        credit_quota: 5_000_000,
        qr_code: '',
        pay_url: 'https://pay.example.com/submit.php?pid=1&money=70.00',
        expire_at: Math.floor(Date.now() / 1000) + 600,
      },
    })

    renderDialog('alipay', { device: 'pc' })

    const openLink = (await screen.findByRole('link', {
      name: 'Open payment page',
    })) as HTMLAnchorElement
    expect(openLink.href).toBe(
      'https://pay.example.com/submit.php?pid=1&money=70.00'
    )
    expect(openLink.target).toBe('_blank')
    expect(openLink.rel).toContain('noopener')
    expect(openLink.rel).toContain('noreferrer')
    expect(screen.queryByTestId('epay-qr')).toBeNull()
    expect(screen.getAllByRole('button', { name: 'Cancel order' })).toHaveLength(1)
  })

  it('renders a manual Open payment app button on mobile return so users never hit a dead end', async () => {
    const navigateMock = vi.fn()
    getAdtyOrderMock.mockResolvedValue({
      trade_no: 'ORD-TEST-001',
      status: 'pending',
      payment_method: 'alipay',
      ext: {
        trade_no: 'ORD-TEST-001',
        pay_amount: '70.00',
        face_usd: '10',
        credit_quota: 5_000_000,
        qr_code: '',
        pay_url: 'https://pay.example.com/mobile-pay',
        expire_at: Math.floor(Date.now() / 1000) + 600,
      },
    })

    renderDialog('alipay', {
      device: 'mobile',
      gatewayReturn: true,
      navigate: navigateMock,
    })

    const btn = await screen.findByRole('button', { name: 'Open payment app' })
    expect(navigateMock).not.toHaveBeenCalled()
    fireEvent.click(btn)
    expect(navigateMock).toHaveBeenCalledWith('https://pay.example.com/mobile-pay')
  })

  it.each([
    ['canceled', 'This order was canceled.'],
    ['failed', 'This payment failed. Please create a new order.'],
    ['refunded', 'This order has been refunded.'],
    ['replaced', 'This order was replaced by a newer order.'],
  ] as const)(
    'renders clear status view for terminal status %s',
    async (status, expectedText) => {
      getAdtyOrderMock.mockResolvedValue({
        trade_no: 'ORD-TEST-001',
        status,
        payment_method: 'alipay',
        ext: {
          trade_no: 'ORD-TEST-001',
          pay_amount: '70.00',
          face_usd: '10',
          credit_quota: 5_000_000,
          expire_at: Math.floor(Date.now() / 1000) + 600,
        },
      })

      renderDialog('alipay')

      expect(await screen.findByText(expectedText)).toBeTruthy()
      expect(screen.getByRole('button', { name: 'Close' })).toBeTruthy()
      expect(screen.queryByRole('button', { name: 'Cancel order' })).toBeNull()
    }
  )

  it('shows error notice with Retry button when order query fails', async () => {
    getAdtyOrderMock.mockRejectedValueOnce(new Error('Network down'))

    renderDialog('alipay')

    expect(await screen.findByText('Network down')).toBeTruthy()
    expect(screen.queryByText('Creating the order...')).toBeNull()

    getAdtyOrderMock.mockResolvedValueOnce({
      trade_no: 'ORD-TEST-001',
      status: 'pending',
      payment_method: 'alipay',
      ext: {
        trade_no: 'ORD-TEST-001',
        pay_amount: '70.00',
        face_usd: '10',
        credit_quota: 5_000_000,
        qr_code: 'https://qr.alipay.com/retry',
        expire_at: Math.floor(Date.now() / 1000) + 600,
      },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() => {
      expect(screen.getByTestId('epay-qr')).toBeTruthy()
    })
    expect(screen.getByTestId('epay-amount').textContent).toBe('$10.00')
    expect(screen.queryByText(/¥/)).toBeNull()
  })

  it('transitions immediately to expired view when local expire_at has passed', async () => {
    getAdtyOrderMock.mockResolvedValue({
      trade_no: 'ORD-TEST-001',
      status: 'pending',
      payment_method: 'alipay',
      ext: {
        trade_no: 'ORD-TEST-001',
        pay_amount: '70.00',
        face_usd: '10',
        credit_quota: 5_000_000,
        qr_code: 'https://qr.alipay.com/stale',
        expire_at: Math.floor(Date.now() / 1000) - 5,
      },
    })

    renderDialog('alipay')

    expect(
      await screen.findByText('This order has expired. Please create a new one.')
    ).toBeTruthy()
    expect(screen.queryByTestId('epay-qr')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Cancel order' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Close' })).toBeTruthy()
  })
})
