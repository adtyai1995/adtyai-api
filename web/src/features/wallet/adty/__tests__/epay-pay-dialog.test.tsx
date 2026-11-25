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
import { cleanup, render, screen } from '@testing-library/react'
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

function renderDialog(method: 'alipay' | 'wxpay') {
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
})
