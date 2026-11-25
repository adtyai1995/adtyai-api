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
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { VoucherBatchResultDialog } from '../voucher-batch-result-dialog'

const copyMock = vi.fn().mockResolvedValue(true)

vi.mock('@/hooks/use-copy-to-clipboard', () => ({
  useCopyToClipboard: () => ({
    copiedText: null,
    copyToClipboard: copyMock,
  }),
}))

vi.mock('@/components/dialog', () => ({
  Dialog: (props: {
    open?: boolean
    title?: React.ReactNode
    description?: React.ReactNode
    children?: React.ReactNode
    footer?: React.ReactNode
  }) =>
    props.open ? (
      <div role='dialog'>
        <h2>{props.title}</h2>
        <p>{props.description}</p>
        {props.children}
        {props.footer}
      </div>
    ) : null,
}))

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

describe('VoucherBatchResultDialog', () => {
  afterEach(() => {
    cleanup()
    copyMock.mockClear()
    vi.restoreAllMocks()
  })

  it('displays one-time warning, summary, codes, and copies all codes newline-separated', () => {
    render(
      <VoucherBatchResultDialog
        open
        onOpenChange={vi.fn()}
        batch={{
          batch_no: 'B-2026-001',
          purpose: 'main_user',
          amount_usd: '25.00',
          count: 2,
          total_amount_usd: '50.00',
          reason: 'Compensation',
          items: [
            {
              id: 1,
              code: 'ADTY-CODE-1',
              code_tail: 'CODE-1',
              purpose: 'main_user',
              status: 'pending',
              amount_usd: '25.00',
              quota: 12500000,
              batch_no: 'B-2026-001',
              reason: 'Compensation',
              created_at: 1700000000,
            },
            {
              id: 2,
              code: 'ADTY-CODE-2',
              code_tail: 'CODE-2',
              purpose: 'main_user',
              status: 'pending',
              amount_usd: '25.00',
              quota: 12500000,
              batch_no: 'B-2026-001',
              reason: 'Compensation',
              created_at: 1700000000,
            },
          ],
        }}
      />
    )

    expect(screen.getByText('One-time plaintext display')).toBeTruthy()
    expect(screen.getByText('B-2026-001')).toBeTruthy()
    expect(screen.getByText('ADTY-CODE-1')).toBeTruthy()
    expect(screen.getByText('ADTY-CODE-2')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: 'Copy All' }))
    expect(copyMock).toHaveBeenCalledWith('ADTY-CODE-1\nADTY-CODE-2')
  })

  it('uses margin recharge code title and list label for site_margin batches', () => {
    render(
      <VoucherBatchResultDialog
        open
        onOpenChange={vi.fn()}
        batch={{
          batch_no: 'B-MARGIN-01',
          purpose: 'site_margin',
          amount_usd: '100.00',
          count: 1,
          total_amount_usd: '100.00',
          reason: 'Offline deposit',
          items: [
            {
              id: 5,
              code: 'ADTY-MGN-1',
              code_tail: 'MGN-1',
              purpose: 'site_margin',
              status: 'pending',
              amount_usd: '100.00',
              quota: 50000000,
              batch_no: 'B-MARGIN-01',
              reason: 'Offline deposit',
              created_at: 1700000000,
            },
          ],
        }}
      />
    )

    expect(
      screen.getByRole('heading', { name: 'Generated margin recharge codes' })
    ).toBeTruthy()
    expect(
      screen.getByLabelText('Generated margin recharge codes')
    ).toBeTruthy()
    expect(screen.queryByText('Generated redemption codes')).toBeNull()
  })
})
