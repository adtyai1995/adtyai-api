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
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { VoucherListTable } from '../voucher-list-table'

vi.mock('@/components/confirm-dialog', () => ({
  ConfirmDialog: () => null,
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

describe('VoucherListTable empty states', () => {
  afterEach(() => {
    cleanup()
  })

  it('shows redemption code empty text and header for general/customer voucher tables', () => {
    render(
      <VoucherListTable
        label='Main-site user redemption codes'
        rows={[]}
        busy={false}
        onFreeze={vi.fn()}
        onCancel={vi.fn()}
      />
    )
    expect(screen.getByText('No redemption codes')).toBeTruthy()
    expect(
      screen.getByRole('columnheader', { name: 'Redemption code' })
    ).toBeTruthy()
  })

  it('shows margin recharge code empty text, Recharge code header, and redeemer identity when targeting subsites', () => {
    render(
      <VoucherListTable
        label='Margin recharge codes'
        rows={[
          {
            id: 9,
            code_tail: 'MGN9',
            purpose: 'site_margin',
            status: 'redeemed',
            amount_usd: '100.00',
            quota: 50000000,
            source_kind: 'platform',
            source_site_id: 0,
            target_site_id: 4,
            issuer_id: 1,
            redeemer_id: 42,
            redeemer_username: 'owner42',
            inherited_by_main: false,
            trade_no: 'T-9',
            created_at: 1700000000,
            redeemed_at: 1700003600,
          },
        ]}
        busy={false}
        showTargetSite
        onFreeze={vi.fn()}
        onCancel={vi.fn()}
      />
    )
    expect(
      screen.getByRole('columnheader', { name: 'Recharge code' })
    ).toBeTruthy()
    expect(screen.getByText(/42 \/ owner42/)).toBeTruthy()
    expect(screen.queryByText(/#42/)).toBeNull()
  })

  it('shows margin recharge code empty text when targeting subsites with empty rows', () => {
    render(
      <VoucherListTable
        label='Margin recharge codes'
        rows={[]}
        busy={false}
        showTargetSite
        onFreeze={vi.fn()}
        onCancel={vi.fn()}
      />
    )
    expect(screen.getByText('No margin recharge codes')).toBeTruthy()
    expect(
      screen.getByRole('columnheader', { name: 'Recharge code' })
    ).toBeTruthy()
  })

  it('honors explicit emptyText override', () => {
    render(
      <VoucherListTable
        label='Custom'
        rows={[]}
        busy={false}
        emptyText='Custom empty label'
        onFreeze={vi.fn()}
        onCancel={vi.fn()}
      />
    )
    expect(screen.getByText('Custom empty label')).toBeTruthy()
  })
})
