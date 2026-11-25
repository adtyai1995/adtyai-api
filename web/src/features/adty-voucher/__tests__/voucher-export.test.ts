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
  buildVoucherExportFilename,
  computeVoucherBatchTotal,
  formatVoucherBatchCsv,
  formatVoucherBatchTxt,
  normalizeVoucherBatchResult,
  parseVoucherBatchCount,
} from '../export'

describe('adty-voucher export and batch calculation', () => {
  it('validates batch quantities strictly within 1..100 integers', () => {
    expect(parseVoucherBatchCount('1')).toBe(1)
    expect(parseVoucherBatchCount('100')).toBe(100)
    expect(parseVoucherBatchCount(' 25 ')).toBe(25)
    expect(parseVoucherBatchCount('0')).toBeNull()
    expect(parseVoucherBatchCount('101')).toBeNull()
    expect(parseVoucherBatchCount('-3')).toBeNull()
    expect(parseVoucherBatchCount('1.5')).toBeNull()
    expect(parseVoucherBatchCount('')).toBeNull()
  })

  it('computes exact USD totals using fixed-point integer arithmetic and enforces 2 decimals and $100,000 max', () => {
    const summary = computeVoucherBatchTotal('0.1', '3')
    expect(summary.valid).toBe(true)
    expect(summary.count).toBe(3)
    expect(summary.unitAmountUsd).toBe('0.10')
    expect(summary.totalAmountUsd).toBe('0.30')

    const large = computeVoucherBatchTotal('12.34', '100')
    expect(large.valid).toBe(true)
    expect(large.totalAmountUsd).toBe('1234.00')

    const maxValid = computeVoucherBatchTotal('100000', '1')
    expect(maxValid.valid).toBe(true)
    expect(maxValid.totalAmountUsd).toBe('100000.00')

    expect(computeVoucherBatchTotal('12.345', '100').valid).toBe(false)
    expect(computeVoucherBatchTotal('100000.01', '1').valid).toBe(false)
    expect(computeVoucherBatchTotal('0', '5').valid).toBe(false)
    expect(computeVoucherBatchTotal('10', '101').valid).toBe(false)
  })

  it('normalizes multi-item batch responses and generates TXT and BOM CSV', () => {
    const normalized = normalizeVoucherBatchResult({
      id: 10,
      code: 'ADTY-AAA1',
      code_tail: 'AAA1',
      purpose: 'main_user',
      status: 'pending',
      amount_usd: '10.00',
      quota: 5000000,
      batch_no: 'BATCH-20260617-01',
      count: 2,
      total_amount_usd: '20.00',
      items: [
        {
          id: 10,
          code: 'ADTY-AAA1',
          code_tail: 'AAA1',
          purpose: 'main_user',
          status: 'pending',
          amount_usd: '10.00',
          quota: 5000000,
          batch_no: 'BATCH-20260617-01',
          reason: 'Campaign, "VIP"',
          created_at: 1700000000,
        },
        {
          id: 11,
          code: 'ADTY-BBB2',
          code_tail: 'BBB2',
          purpose: 'main_user',
          status: 'pending',
          amount_usd: '10.00',
          quota: 5000000,
          batch_no: 'BATCH-20260617-01',
          reason: 'Campaign, "VIP"',
          created_at: 1700000000,
        },
      ],
    })

    expect(normalized).not.toBeNull()
    if (!normalized) return
    expect(normalized.count).toBe(2)
    expect(normalized.total_amount_usd).toBe('20.00')
    expect(formatVoucherBatchTxt(normalized)).toBe('ADTY-AAA1\nADTY-BBB2')

    const csv = formatVoucherBatchCsv(normalized)
    expect(csv.startsWith('\uFEFFcode,amount_usd,purpose,batch_no,reason,created_at\r\n')).toBe(true)
    expect(csv).toContain('ADTY-AAA1,10.00,main_user,BATCH-20260617-01,"Campaign, ""VIP""",')
    expect(csv).toContain('ADTY-BBB2,10.00,main_user,BATCH-20260617-01,"Campaign, ""VIP""",')
  })

  it('neutralizes spreadsheet formula prefixes before RFC 4180 escaping in CSV export', () => {
    const normalized = normalizeVoucherBatchResult({
      id: 20,
      code: 'ADTY-SAFE1',
      code_tail: 'SAFE1',
      purpose: 'main_user',
      status: 'pending',
      amount_usd: '10.00',
      quota: 5000000,
      batch_no: 'B-1',
      count: 4,
      items: [
        {
          id: 20,
          code: 'ADTY-SAFE1',
          code_tail: 'SAFE1',
          purpose: 'main_user',
          status: 'pending',
          amount_usd: '10.00',
          quota: 5000000,
          batch_no: 'B-1',
          reason: '=HYPERLINK("http://evil","x")',
          created_at: 1700000000,
        },
        {
          id: 21,
          code: 'ADTY-SAFE2',
          code_tail: 'SAFE2',
          purpose: 'main_user',
          status: 'pending',
          amount_usd: '10.00',
          quota: 5000000,
          batch_no: 'B-1',
          reason: '  +SUM(A1:A2)',
          created_at: 1700000000,
        },
        {
          id: 22,
          code: 'ADTY-SAFE3',
          code_tail: 'SAFE3',
          purpose: 'main_user',
          status: 'pending',
          amount_usd: '10.00',
          quota: 5000000,
          batch_no: 'B-1',
          reason: '@cmd',
          created_at: 1700000000,
        },
        {
          id: 23,
          code: 'ADTY-SAFE4',
          code_tail: 'SAFE4',
          purpose: 'main_user',
          status: 'pending',
          amount_usd: '10.00',
          quota: 5000000,
          batch_no: 'B-1',
          reason: '\t-2+3',
          created_at: 1700000000,
        },
      ],
    })
    expect(normalized).not.toBeNull()
    if (!normalized) return
    const csv = formatVoucherBatchCsv(normalized)
    expect(csv).toContain(`"'=HYPERLINK(""http://evil"",""x"")"`)
    expect(csv).toContain(`'  +SUM(A1:A2)`)
    expect(csv).toContain(`'@cmd`)
    expect(csv).toContain(`'\t-2+3`)
  })

  it('normalizes legacy single-item response without items array', () => {
    const normalized = normalizeVoucherBatchResult(
      {
        id: 1,
        code: 'ADTY-SINGLE',
        code_tail: 'SINGLE',
        purpose: 'site_margin',
        amount_usd: '50.00',
        quota: 25000000,
      },
      { fallbackReason: 'Offline transfer', targetSiteLabel: 'alpha.example.com' }
    )
    expect(normalized?.items).toHaveLength(1)
    expect(normalized?.items[0].code).toBe('ADTY-SINGLE')
    expect(normalized?.items[0].reason).toBe('Offline transfer')
    expect(normalized?.target_site_label).toBe('alpha.example.com')
  })

  it('builds safe export filenames using batch_no or timestamp fallback', () => {
    expect(buildVoucherExportFilename('BATCH_2026/01', 'csv')).toBe(
      'vouchers-BATCH_2026-01.csv'
    )
    expect(
      buildVoucherExportFilename('', 'txt', new Date('2026-06-17T06:36:00Z'))
    ).toMatch(/^vouchers-\d{8}-\d{6}\.txt$/)
  })
})
