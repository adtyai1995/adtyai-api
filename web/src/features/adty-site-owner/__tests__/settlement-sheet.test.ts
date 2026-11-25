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
import type { TFunction } from 'i18next'
import { describe, expect, it, vi } from 'vitest'

import type { SettlementSheet } from '../api'
import {
  buildSettlementSections,
  buildSettlementSheetCsv,
  buildSettlementSheetPrintHtml,
  filterVisibleLines,
  isOwnerSelfUseAllZero,
  isZeroAmount,
} from '../lib/settlement-sheet'

vi.mock('lucide-react', () => ({}))
vi.mock('@/components/ui/native-select', () => ({}))
vi.mock('@/components/empty-state', () => ({}))
vi.mock('@/components/error-state', () => ({}))

const mockT = ((key: string) => key) as unknown as TFunction

function makeSheet(overrides: Partial<SettlementSheet> = {}): SettlementSheet {
  return {
    id: 42,
    site_id: 7,
    business_day: '2026-10-15',
    timezone: 'Asia/Shanghai',
    day_start: 1760457600,
    day_end: 1760543999,
    generated_at: 1760544600,
    event_watermark: 987654,
    batch_id: 'batch-internal-20261015-abc',
    topup_face_usd: '250.00',
    paid_usd: '240.00',
    paid_by_currency: '{"CNY":"100.00"}',
    consumption_usd: '180.50',
    supply_cost_usd: '120.25',
    spread_usd: '60.25',
    gift_reserve_usd: '10.00',
    gift_funding_usd: '0.00',
    gift_release_usd: '5.00',
    trial_expense_usd: '2.50',
    commission_usd: '4.25',
    pending_coverage_usd: '0.00',
    pending_coverage_unverified: false,
    margin_delta_usd: '-120.25',
    earnings_delta_usd: '53.50',
    earnings_available_delta_usd: '40.00',
    earnings_frozen_delta_usd: '13.50',
    withdrawal_usd: '0.00',
    withdrawal_fee_usd: '0.00',
    adjustment_usd: '-1.50',
    anomaly_count: 0,
    owner_self_use_topup_usd: '0.00',
    owner_self_use_voucher_usd: '0.00',
    owner_self_use_refund_usd: '0.00',
    owner_transfer_usd: '0.00',
    owner_self_use_usd: '0.00',
    owner_self_use_cost_usd: '0.00',
    ...overrides,
  }
}

describe('buildSettlementSheetCsv', () => {
  it('outputs UTF-8 BOM, grouped sections, neutralizes formula injection, and hides admin audit fields in owner mode', () => {
    const sheet = makeSheet({
      paid_by_currency: JSON.stringify({
        CNY: '=1+1',
        EUR: '+cmd|calc',
      }),
    })
    const csv = buildSettlementSheetCsv(sheet, mockT, 'en-US', {
      admin: false,
      siteName: '=SUM(A1:A5), "Acme"',
      revisionId: 3,
      sources: ['internal_source_row_1', 'internal_source_row_2'],
    })

    expect(csv.startsWith('\uFEFF')).toBe(true)

    // Grouped sections included
    expect(csv).toContain('Customer top-up & consumption')
    expect(csv).toContain('Marketing & referral expenses')
    expect(csv).toContain('Wallet movements & withdrawals')
    expect(csv).toContain('Owner self-use')

    // Formula injection neutralized on text cells & RFC 4180 escaped
    expect(csv).toContain(`"'=SUM(A1:A5), ""Acme"""`)
    expect(csv).toContain("'=1+1 CNY")
    expect(csv).toContain("'+cmd|calc EUR")

    // Pure numeric negative USD amounts remain numeric (no leading apostrophe) in Amount (USD) column
    expect(csv).toContain(',-120.25\r\n')
    expect(csv).toContain(',-1.50\r\n')

    // Internal audit fields MUST NOT appear on owner export even if sources were passed
    expect(csv).not.toContain('Admin audit details')
    expect(csv).not.toContain('Batch')
    expect(csv).not.toContain('batch-internal-20261015-abc')
    expect(csv).not.toContain('Event watermark')
    expect(csv).not.toContain('987654')
    expect(csv).not.toContain('Source records')
    expect(csv).not.toContain('internal_source_row_1')
  })

  it('includes Batch, Event watermark, and Source records in admin mode', () => {
    const sheet = makeSheet()
    const csv = buildSettlementSheetCsv(sheet, mockT, 'en-US', {
      admin: true,
      revisionId: 5,
      sources: ['ledger:101', 'order:202'],
    })

    expect(csv.startsWith('\uFEFF')).toBe(true)
    expect(csv).toContain('Site ID,7,')
    expect(csv).toContain(
      'Admin audit details,Batch,batch-internal-20261015-abc,'
    )
    expect(csv).toContain('Admin audit details,Event watermark,987654,')
    expect(csv).toContain(
      'Admin audit details,Source records,"ledger:101, order:202",'
    )
  })

  it('neutralizes malicious non-numeric payloads across =, +, -, @, tab, and CR triggers', () => {
    const sheet = makeSheet({
      adjustment_usd: '=HYPERLINK("https://evil.example","click")',
      paid_by_currency: JSON.stringify({
        A: '@SUM(1+1)',
        D: '-2+3',
      }),
      pending_coverage_unverified: true,
      legacy_coverage: [
        {
          id: 9,
          created_at: 1760544000,
          before_usd: '12.00',
          after_usd: '8.50',
        },
      ],
    })
    const csvTab = buildSettlementSheetCsv(sheet, mockT, 'en-US', {
      admin: false,
      siteName: '\t=1+1',
    })
    const csvCr = buildSettlementSheetCsv(sheet, mockT, 'en-US', {
      admin: false,
      siteName: '\r+1+1',
    })

    expect(csvTab).toContain(
      `"'=HYPERLINK(""https://evil.example"",""click"")"`
    )
    expect(csvTab).toContain("'@SUM(1+1) A")
    expect(csvTab).toContain("'-2+3 D")
    expect(csvTab).toContain("Metadata,Site,'\t=1+1,")
    expect(csvCr).toContain(`Metadata,Site,"'\r+1+1",`)
    expect(csvTab).toContain('Historical amount requires verification')
    expect(csvTab).toContain('Historical coverage adjustments')
    expect(csvTab).toContain('Pending coverage USD 12.00 → 8.50,8.50')
  })
})

describe('buildSettlementSheetPrintHtml', () => {
  it('escapes HTML to prevent XSS and excludes internal audit fields in owner mode', () => {
    const sheet = makeSheet({
      paid_by_currency: JSON.stringify({
        '<b>USD</b>': '<img src=x onerror=alert(1)>',
      }),
    })
    const html = buildSettlementSheetPrintHtml(sheet, mockT, 'en-US', {
      admin: false,
      siteName: '<script>alert("xss")</script>',
      revisionId: 2,
      sources: ['<svg onload=alert(1)>'],
    })

    expect(html).toContain('<!DOCTYPE html>')
    expect(html).toContain('@page { size: A4 portrait;')
    expect(html).toContain('Daily settlement statement')
    expect(html).toContain('Customer top-up &amp; consumption')
    expect(html).toContain('Owner self-use')

    // XSS escaped
    expect(html).not.toContain('<script>alert')
    expect(html).not.toContain('<img src=x')
    expect(html).not.toContain('<svg onload')
    expect(html).toContain(
      '&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;'
    )
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;')

    // Internal audit fields hidden in owner mode
    expect(html).not.toContain('Admin audit details')
    expect(html).not.toContain('batch-internal-20261015-abc')
    expect(html).not.toContain('Event watermark')
    expect(html).not.toContain('987654')
    expect(html).not.toContain('Source records')
  })

  it('includes escaped Admin audit details in admin mode', () => {
    const sheet = makeSheet({
      batch_id: 'batch-<unsafe>&1',
    })
    const html = buildSettlementSheetPrintHtml(sheet, mockT, 'en-US', {
      admin: true,
      revisionId: 4,
      sources: ['src_a', '<script>bad()</script>'],
    })

    expect(html).toContain('Admin audit details')
    expect(html).toContain('Batch')
    expect(html).toContain('batch-&lt;unsafe&gt;&amp;1')
    expect(html).toContain('Event watermark')
    expect(html).toContain('987654')
    expect(html).toContain('Source records')
    expect(html).toContain('src_a, &lt;script&gt;bad()&lt;/script&gt;')
    expect(html).not.toContain('<script>bad()</script>')
  })
})

describe('zero-amount filtering and owner self-use collapse helpers', () => {
  it('detects zero amounts including empty, undefined, and negative zero', () => {
    expect(isZeroAmount(undefined)).toBe(true)
    expect(isZeroAmount('')).toBe(true)
    expect(isZeroAmount('0')).toBe(true)
    expect(isZeroAmount('0.00')).toBe(true)
    expect(isZeroAmount('-0.00')).toBe(true)
    expect(isZeroAmount(0)).toBe(true)

    expect(isZeroAmount('0.01')).toBe(false)
    expect(isZeroAmount('-1.50')).toBe(false)
    expect(isZeroAmount('Historical amount requires verification')).toBe(false)
  })

  it('collapses Owner self-use when all 6 fields are zero and expands when any is non-zero', () => {
    expect(isOwnerSelfUseAllZero(makeSheet())).toBe(true)
    expect(
      isOwnerSelfUseAllZero(makeSheet({ owner_self_use_topup_usd: '10.00' }))
    ).toBe(false)
    expect(
      isOwnerSelfUseAllZero(makeSheet({ owner_self_use_voucher_usd: '5.00' }))
    ).toBe(false)
    expect(
      isOwnerSelfUseAllZero(makeSheet({ owner_self_use_refund_usd: '-1.00' }))
    ).toBe(false)
    expect(
      isOwnerSelfUseAllZero(makeSheet({ owner_transfer_usd: '2.00' }))
    ).toBe(false)
    expect(
      isOwnerSelfUseAllZero(makeSheet({ owner_self_use_usd: '0.50' }))
    ).toBe(false)
    expect(
      isOwnerSelfUseAllZero(makeSheet({ owner_self_use_cost_usd: '0.25' }))
    ).toBe(false)
  })

  it('hides zero rows while preserving positive anomalies and unverified pending coverage', () => {
    const sheet = makeSheet({
      gift_reserve_usd: '0.00',
      gift_funding_usd: '0.00',
      gift_release_usd: '0.00',
      trial_expense_usd: '0.00',
      commission_usd: '0.00',
      pending_coverage_usd: '0.00',
      pending_coverage_unverified: true,
      margin_delta_usd: '0.00',
      earnings_delta_usd: '0.00',
      earnings_available_delta_usd: '0.00',
      earnings_frozen_delta_usd: '0.00',
      withdrawal_usd: '0.00',
      withdrawal_fee_usd: '0.00',
      adjustment_usd: '0.00',
      anomaly_count: 3,
    })

    const sections = buildSettlementSections(mockT, sheet, 'en-US')
    const marketing = sections.find((s) => s.id === 'marketing-referral')
    const wallet = sections.find((s) => s.id === 'wallet-withdrawals')
    const selfUse = sections.find((s) => s.id === 'owner-self-use')

    expect(marketing).toBeDefined()
    expect(wallet).toBeDefined()
    expect(selfUse).toBeDefined()

    const visibleMarketing = filterVisibleLines(marketing?.lines ?? [], true)
    expect(visibleMarketing.map((l) => l.id)).toEqual(['pending'])

    const visibleWallet = filterVisibleLines(wallet?.lines ?? [], true)
    expect(visibleWallet.map((l) => l.id)).toEqual(['anomalies'])

    const visibleSelfUse = filterVisibleLines(selfUse?.lines ?? [], true)
    expect(visibleSelfUse).toEqual([])
  })
})
