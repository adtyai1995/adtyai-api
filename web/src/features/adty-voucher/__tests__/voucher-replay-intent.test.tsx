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

import { VoucherAdminPanel } from '../voucher-admin-panel'

const {
  getAdminVoucherIssuanceMock,
  getAdminVouchersListMock,
  issueAdminVoucherBatchMock,
  cancelAdminVoucherByIdMock,
} = vi.hoisted(() => ({
  getAdminVoucherIssuanceMock: vi.fn(),
  getAdminVouchersListMock: vi.fn(),
  issueAdminVoucherBatchMock: vi.fn(),
  cancelAdminVoucherByIdMock: vi.fn(),
}))

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getAdminVoucherIssuance: (...args: unknown[]) =>
      getAdminVoucherIssuanceMock(...args),
    getAdminVouchersList: (...args: unknown[]) =>
      getAdminVouchersListMock(...args),
    issueAdminVoucherBatch: (...args: unknown[]) =>
      issueAdminVoucherBatchMock(...args),
    cancelAdminVoucherById: (...args: unknown[]) =>
      cancelAdminVoucherByIdMock(...args),
  }
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, string | number>) => {
      if (!params) return key
      return Object.entries(params).reduce(
        (acc, [k, v]) => acc.replaceAll(`{{${k}}}`, String(v)),
        key
      )
    },
  }),
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

describe('VoucherAdminPanel intentKey rotation and batch replay (R1-11)', () => {
  beforeEach(() => {
    getAdminVoucherIssuanceMock.mockResolvedValue({
      limit_usd: '1000.00',
      frozen_usd: '0.00',
      spent_usd: '0.00',
    })
    getAdminVouchersListMock.mockResolvedValue({
      total: 0,
      page: 1,
      page_size: 20,
      items: [],
    })
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('reuses intent_key on same-parameter retry and rotates intent_key when parameters change', async () => {
    issueAdminVoucherBatchMock
      .mockRejectedValueOnce(new Error('network error'))
      .mockRejectedValueOnce(new Error('network error'))
      .mockResolvedValueOnce({
        id: 101,
        code: 'ADTY-NEW-1',
        code_tail: 'NEW-1',
        purpose: 'main_user',
        status: 'pending',
        amount_usd: '20.00',
        quota: 10000000,
        count: 1,
      })

    renderWithQuery(<VoucherAdminPanel />)

    fireEvent.change(screen.getByLabelText('Amount (USD)'), {
      target: { value: '10' },
    })
    fireEvent.change(screen.getByLabelText('Reason'), {
      target: { value: 'Promo' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Issue code' }))

    await waitFor(() =>
      expect(issueAdminVoucherBatchMock).toHaveBeenCalledTimes(1)
    )
    const firstKey = issueAdminVoucherBatchMock.mock.calls[0][0].intent_key

    // Retry with identical parameters reuses the same intent_key.
    fireEvent.click(screen.getByRole('button', { name: 'Issue code' }))
    await waitFor(() =>
      expect(issueAdminVoucherBatchMock).toHaveBeenCalledTimes(2)
    )
    expect(issueAdminVoucherBatchMock.mock.calls[1][0].intent_key).toBe(
      firstKey
    )

    // Changing amount rotates to a new intent_key.
    fireEvent.change(screen.getByLabelText('Amount (USD)'), {
      target: { value: '20' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Issue code' }))
    await waitFor(() =>
      expect(issueAdminVoucherBatchMock).toHaveBeenCalledTimes(3)
    )
    expect(issueAdminVoucherBatchMock.mock.calls[2][0].intent_key).not.toBe(
      firstKey
    )
  })

  it('hides misleading single-code cancel button on multi-code batch replay and filters batch instead', async () => {
    issueAdminVoucherBatchMock.mockResolvedValueOnce({
      id: 201,
      code: '',
      code_tail: '001',
      purpose: 'main_user',
      status: 'pending',
      amount_usd: '10.00',
      quota: 5000000,
      batch_no: 'BATCH-2026-99',
      count: 5,
      replayed: true,
    })

    renderWithQuery(<VoucherAdminPanel />)

    fireEvent.change(screen.getByLabelText('Amount (USD)'), {
      target: { value: '10' },
    })
    fireEvent.change(screen.getByLabelText('Quantity (1-100)'), {
      target: { value: '5' },
    })
    fireEvent.change(screen.getByLabelText('Reason'), {
      target: { value: 'Batch promo' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Issue 5 codes' }))

    await waitFor(() =>
      expect(screen.getByTestId('voucher-reconcile-banner')).toBeTruthy()
    )

    expect(screen.getByText(/BATCH-2026-99 \(5 codes\)/)).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: 'Cancel code and replace' })
    ).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Filter this batch' }))
    expect(
      (screen.getByLabelText('Batch number') as HTMLInputElement).value
    ).toBe('BATCH-2026-99')
  })
})
