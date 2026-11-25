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

  it('shows redemption code empty text for general/customer voucher tables', () => {
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
  })

  it('shows margin recharge code empty text when targeting subsites', () => {
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
