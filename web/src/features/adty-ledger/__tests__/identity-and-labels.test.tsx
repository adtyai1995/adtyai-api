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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import type { TFunction } from 'i18next'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { siteWalletLedgerTypeLabel } from '../site-wallet-labels'
import { SiteWalletLedger } from '../site-wallet-ledger'
import { UserLedgerDialog } from '../user-ledger-dialog'
import { VoucherTable } from '../voucher-table'

const { apiGetMock } = vi.hoisted(() => ({
  apiGetMock: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: {
    get: (...args: unknown[]) => apiGetMock(...args),
  },
}))

vi.mock('@/components/dialog', () => ({
  Dialog: (props: {
    open?: boolean
    title?: ReactNode
    children?: ReactNode
  }) =>
    props.open ? (
      <div role='dialog'>
        <h2>{props.title}</h2>
        {props.children}
      </div>
    ) : null,
}))

vi.mock('@/components/confirm-dialog', () => ({
  ConfirmDialog: () => null,
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

describe('adty-ledger user identity and redemption code labels', () => {
  afterEach(() => {
    cleanup()
    apiGetMock.mockReset()
  })

  it('uses redemption code wording for customer vouchers and keeps margin code distinct', () => {
    const t = ((key: string) => key) as unknown as TFunction
    expect(siteWalletLedgerTypeLabel(t, 'voucher_freeze')).toBe(
      'Redemption code issued'
    )
    expect(siteWalletLedgerTypeLabel(t, 'voucher_release')).toBe(
      'Redemption code cancelled'
    )
    expect(siteWalletLedgerTypeLabel(t, 'voucher_spend')).toBe(
      'Redemption code redeemed'
    )
    expect(siteWalletLedgerTypeLabel(t, 'voucher_margin')).toBe(
      'Margin code redeemed'
    )
  })

  it('formats SiteWalletLedger operator as ID / username with fallback', async () => {
    renderWithQuery(
      <SiteWalletLedger
        label='Wallet ledger'
        queryKey={['test-site-wallet-ledger']}
        kinds={['margin']}
        showOperator
        fetchPage={async () => ({
          total: 2,
          page: 1,
          page_size: 20,
          items: [
            {
              id: 1,
              site_id: 2,
              wallet_id: 3,
              wallet_kind: 'margin',
              delta: '10',
              hold_delta: '0',
              balance_before: '0',
              balance_after: '10',
              type: 'voucher_spend',
              ref_type: 'voucher',
              ref_id: 'V-1',
              reason: '',
              operator_id: 7,
              operator_username: 'root',
              allow_overdraft: false,
              created_at: 1700000000,
            },
            {
              id: 2,
              site_id: 2,
              wallet_id: 3,
              wallet_kind: 'margin',
              delta: '-5',
              hold_delta: '0',
              balance_before: '10',
              balance_after: '5',
              type: 'voucher_freeze',
              ref_type: 'voucher',
              ref_id: 'V-2',
              reason: '',
              operator_id: 8,
              allow_overdraft: false,
              created_at: 1700000100,
            },
          ],
        })}
      />
    )

    await waitFor(() => {
      expect(screen.getByText('7 / root')).toBeTruthy()
      expect(screen.getByText('8 / -')).toBeTruthy()
      expect(screen.getByText('Redemption code redeemed')).toBeTruthy()
      expect(screen.getByText('Redemption code issued')).toBeTruthy()
    })
    expect(screen.queryByText('#7')).toBeNull()
    expect(screen.queryByText('#8')).toBeNull()
  })

  it('formats UserLedgerDialog operator and voucher label consistently', async () => {
    apiGetMock.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          total: 2,
          items: [
            {
              id: 11,
              user_id: 99,
              delta_quota: 500000,
              balance_before: 0,
              balance_after: 500000,
              type: 'voucher',
              ref_type: 'voucher',
              ref_id: 'V-99',
              reason: '',
              operator_id: 9,
              operator_username: 'opsadmin',
              created_at: 1700000000,
            },
            {
              id: 12,
              user_id: 99,
              delta_quota: 500000,
              balance_before: 500000,
              balance_after: 1000000,
              type: 'manual_adjust',
              ref_type: 'admin',
              ref_id: 'A-1',
              reason: 'adjust',
              operator_id: 12,
              created_at: 1700000200,
            },
          ],
        },
      },
    })

    renderWithQuery(
      <UserLedgerDialog open onOpenChange={vi.fn()} userId={99} />
    )

    await waitFor(() => {
      expect(screen.getByText('Redemption code redeemed')).toBeTruthy()
      expect(screen.getByText('9 / opsadmin')).toBeTruthy()
      expect(screen.getByText('12 / -')).toBeTruthy()
    })
    expect(screen.queryByText('Recharge code redeemed')).toBeNull()
    expect(screen.queryByText('#9')).toBeNull()
    expect(screen.queryByText('#12')).toBeNull()
  })

  it('uses redemption code empty state by default in VoucherTable and supports emptyText override', () => {
    const { rerender } = render(
      <VoucherTable
        label='Codes'
        rows={[]}
        busy={false}
        onFreeze={vi.fn()}
        onCancel={vi.fn()}
      />
    )
    expect(screen.getByText('No redemption codes')).toBeTruthy()

    rerender(
      <VoucherTable
        label='Margin codes'
        rows={[]}
        busy={false}
        onFreeze={vi.fn()}
        onCancel={vi.fn()}
        emptyText='No margin recharge codes'
      />
    )
    expect(screen.getByText('No margin recharge codes')).toBeTruthy()
  })
})
