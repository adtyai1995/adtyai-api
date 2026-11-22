import { describe, expect, it } from 'vitest'

import { mobileTarget, qrContent } from '../payment-links'

describe('resume a gateway order', () => {
  const empty = { qr_code: '', pay_url: '', url_scheme: '' }
  it('shows the original QR payload when only a QR code is returned', () => {
    expect(qrContent({ ...empty, qr_code: 'merchant-order:123' })).toBe(
      'merchant-order:123'
    )
  })
  it.each([
    ['url_scheme', 'alipays://platformapi/startapp?order=123'],
    ['pay_url', 'https://pay.example/order/123'],
  ])('continues the same order when only %s is returned', (field, value) => {
    const order = { ...empty, [field]: value }
    expect(mobileTarget(order)).toBe(value)
    expect(qrContent(order)).toBe(value)
  })
  it('rejects executable gateway links on either device', () => {
    const order = { ...empty, url_scheme: 'javascript:alert(123)' }
    expect(mobileTarget(order)).toBe('')
    expect(qrContent(order)).toBe('')
  })
})
