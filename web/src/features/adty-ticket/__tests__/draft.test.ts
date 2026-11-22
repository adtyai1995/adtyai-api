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
