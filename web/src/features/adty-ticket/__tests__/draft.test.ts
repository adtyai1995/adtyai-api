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
import { expect, it } from 'vitest'

import { ticketDraftLink, ticketOperationKey } from '../ticket-draft'

it('retains an operation when a lost response is retried and renews it after edits', () => {
  const attempts = new Map<string, { value: string; key: string }>()
  const first = ticketOperationKey(attempts, 'reply', 'hello')
  expect(ticketOperationKey(attempts, 'reply', 'hello')).toBe(first)
  expect(ticketOperationKey(attempts, 'reply', 'updated')).not.toBe(first)
  expect(ticketOperationKey(attempts, 'note', 'hello')).not.toBe(first)
})

it('recognizes top-up links from older clients and refund links from the order list', () => {
  expect(ticketDraftLink('#ticket-order-45')).toEqual({
    kind: 'topup',
    ref: 'order-45',
  })
  expect(ticketDraftLink('#ticket:refund:order%2F45')).toEqual({
    kind: 'refund',
    ref: 'order/45',
  })
  expect(ticketDraftLink('#case-45')).toBeNull()
})
