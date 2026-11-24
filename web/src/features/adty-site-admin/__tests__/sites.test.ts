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
import { describe, expect, it } from 'vitest'

import {
  generateOwnerPassword,
  isValidOptionalHttpUrl,
  parseSiteBrand,
  siteNotice,
} from '../lib/sites'

describe('adty-site-admin/lib/sites', () => {
  it('parses valid brand JSON including wallet_notice and voucher_purchase_url', () => {
    const raw = JSON.stringify({
      name: 'Acme AI',
      title: 'Acme Portal',
      notice: '  Maintenance tonight  ',
      wallet_notice: 'Top-up bonus active',
      voucher_purchase_url: 'https://shop.example.com/codes',
      unknown_field: 123,
    })
    expect(parseSiteBrand(raw)).toEqual({
      name: 'Acme AI',
      title: 'Acme Portal',
      notice: '  Maintenance tonight  ',
      wallet_notice: 'Top-up bonus active',
      voucher_purchase_url: 'https://shop.example.com/codes',
    })
    expect(siteNotice(raw)).toBe('Maintenance tonight')
  })

  it('handles empty or malformed brand JSON safely', () => {
    expect(parseSiteBrand(undefined)).toEqual({})
    expect(parseSiteBrand('{broken')).toEqual({})
    expect(parseSiteBrand('[]')).toEqual({})
    expect(siteNotice('{broken')).toBe('')
  })

  it('validates optional http(s) URLs', () => {
    expect(isValidOptionalHttpUrl('')).toBe(true)
    expect(isValidOptionalHttpUrl('   ')).toBe(true)
    expect(isValidOptionalHttpUrl('https://shop.example.com/buy?id=1')).toBe(
      true
    )
    expect(isValidOptionalHttpUrl('http://localhost:3000/vouchers')).toBe(true)
    expect(isValidOptionalHttpUrl('javascript:alert(1)')).toBe(false)
    expect(isValidOptionalHttpUrl('ftp://files.example.com')).toBe(false)
    expect(isValidOptionalHttpUrl('not-a-url')).toBe(false)
  })

  it('generates standalone owner passwords within 8-64 chars', () => {
    const pwd = generateOwnerPassword(16)
    expect(pwd).toHaveLength(16)
    expect(/[A-Z]/.test(pwd)).toBe(true)
    expect(/[a-z]/.test(pwd)).toBe(true)
    expect(/[0-9]/.test(pwd)).toBe(true)
  })
})
