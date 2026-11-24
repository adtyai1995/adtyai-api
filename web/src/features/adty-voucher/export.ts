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
import dayjs from '@/lib/dayjs'

import type { VoucherBatchIssued, VoucherBatchItem } from './api'

const DECIMAL_PATTERN = /^\d+(?:\.\d{1,8})?$/
const SCALE_DIGITS = 8
const SCALE = 100_000_000n

export const VOUCHER_BATCH_MIN_COUNT = 1
export const VOUCHER_BATCH_MAX_COUNT = 100

export type NormalizedVoucherBatchItem = {
  id: number
  code: string
  code_tail: string
  purpose: string
  status: string
  amount_usd: string
  quota: number
  batch_no: string
  reason: string
  created_at: number
}

export type NormalizedVoucherBatch = {
  batch_no: string
  purpose: string
  amount_usd: string
  count: number
  total_amount_usd: string
  reason: string
  source_kind?: string
  frozen_earnings_usd?: string
  frozen_margin_usd?: string
  target_site_label?: string
  items: NormalizedVoucherBatchItem[]
}

function parseUnits(raw: string): bigint | null {
  const trimmed = raw.trim()
  if (!DECIMAL_PATTERN.test(trimmed)) return null
  const [wholePart, fracPart = ''] = trimmed.split('.')
  const paddedFrac = fracPart.slice(0, SCALE_DIGITS).padEnd(SCALE_DIGITS, '0')
  return BigInt(wholePart) * SCALE + BigInt(paddedFrac)
}

function formatUnits(units: bigint): string {
  if (units <= 0n) return '0.00'
  const whole = units / SCALE
  const frac = (units % SCALE).toString().padStart(SCALE_DIGITS, '0')
  const trimmedFrac = frac.replace(/0+$/, '')
  const displayFrac = trimmedFrac.length < 2 ? frac.slice(0, 2) : trimmedFrac
  return `${whole.toString()}.${displayFrac}`
}

/** Parse and validate an integer batch quantity in [1, 100]. */
export function parseVoucherBatchCount(raw: string): number | null {
  const trimmed = raw.trim()
  if (!/^\d+$/.test(trimmed)) return null
  const parsed = Number.parseInt(trimmed, 10)
  if (
    !Number.isSafeInteger(parsed) ||
    parsed < VOUCHER_BATCH_MIN_COUNT ||
    parsed > VOUCHER_BATCH_MAX_COUNT
  ) {
    return null
  }
  return parsed
}

/** Compute exact USD total for a unit amount and batch count without float drift. */
export function computeVoucherBatchTotal(
  amountRaw: string,
  countRaw: string
): {
  validAmount: boolean
  validCount: boolean
  valid: boolean
  count: number
  unitAmountUsd: string
  totalAmountUsd: string
} {
  const unitUnits = parseUnits(amountRaw)
  const validAmount = unitUnits !== null && unitUnits > 0n
  const parsedCount = parseVoucherBatchCount(countRaw)
  const validCount = parsedCount !== null
  const count = parsedCount ?? 0
  const totalUnits = validAmount && validCount ? unitUnits * BigInt(count) : 0n
  return {
    validAmount,
    validCount,
    valid: validAmount && validCount,
    count,
    unitAmountUsd: validAmount ? formatUnits(unitUnits) : '0.00',
    totalAmountUsd: validAmount && validCount ? formatUnits(totalUnits) : '0.00',
  }
}

/**
 * Normalize single-code and batch-code backend responses into a unified list
 * suitable for display, clipboard copy, and TXT/CSV export.
 */
export function normalizeVoucherBatchResult(
  issued: VoucherBatchIssued,
  options?: {
    fallbackReason?: string
    targetSiteLabel?: string
  }
): NormalizedVoucherBatch | null {
  const fallbackReason = options?.fallbackReason?.trim() ?? ''
  let rawItems: VoucherBatchItem[] = []
  if (Array.isArray(issued.items) && issued.items.length > 0) {
    rawItems = issued.items
  } else if (issued.code) {
    rawItems = [
      {
        id: issued.id,
        code: issued.code,
        code_tail: issued.code_tail,
        purpose: issued.purpose,
        status: issued.status,
        amount_usd: issued.amount_usd,
        quota: issued.quota,
        batch_no: issued.batch_no,
        reason: issued.reason ?? fallbackReason,
        created_at: issued.created_at,
      },
    ]
  }

  const items: NormalizedVoucherBatchItem[] = rawItems
    .filter((item): item is VoucherBatchItem & { code: string } =>
      Boolean(item.code && item.code.trim() !== '')
    )
    .map((item) => ({
      id: item.id,
      code: item.code.trim(),
      code_tail: item.code_tail || item.code.trim().slice(-6),
      purpose: item.purpose || issued.purpose,
      status: item.status || issued.status || 'pending',
      amount_usd: item.amount_usd || issued.amount_usd,
      quota: item.quota ?? issued.quota,
      batch_no: item.batch_no || issued.batch_no || '',
      reason: item.reason || issued.reason || fallbackReason,
      created_at: item.created_at ?? issued.created_at ?? 0,
    }))

  if (items.length === 0) return null

  const unitAmount = items[0].amount_usd || issued.amount_usd
  const unitUnits = parseUnits(unitAmount)
  const fallbackTotal =
    unitUnits !== null ? formatUnits(unitUnits * BigInt(items.length)) : unitAmount

  return {
    batch_no: issued.batch_no || items[0].batch_no || '',
    purpose: issued.purpose || items[0].purpose,
    amount_usd: unitAmount,
    count: issued.count && issued.count > 0 ? issued.count : items.length,
    total_amount_usd: issued.total_amount_usd || fallbackTotal,
    reason: issued.reason || items[0].reason || fallbackReason,
    source_kind: issued.source_kind,
    frozen_earnings_usd: issued.frozen_earnings_usd,
    frozen_margin_usd: issued.frozen_margin_usd,
    target_site_label: options?.targetSiteLabel,
    items,
  }
}

/** Join all plaintext codes with newlines for Copy All and TXT export. */
export function formatVoucherBatchTxt(batch: NormalizedVoucherBatch): string {
  return batch.items.map((item) => item.code).join('\n')
}

function escapeCsvField(value: string): string {
  if (/[",\r\n]/.test(value)) {
    return `"${value.replaceAll('"', '""')}"`
  }
  return value
}

function formatIsoOrEmpty(createdAt: number): string {
  if (!createdAt || createdAt <= 0) return ''
  return dayjs(createdAt * 1000).format('YYYY-MM-DD HH:mm:ss')
}

/**
 * Build a UTF-8 BOM CSV document with header:
 * code,amount_usd,purpose,batch_no,reason,created_at
 */
export function formatVoucherBatchCsv(batch: NormalizedVoucherBatch): string {
  const header = 'code,amount_usd,purpose,batch_no,reason,created_at'
  const lines = batch.items.map((item) =>
    [
      item.code,
      item.amount_usd,
      item.purpose,
      item.batch_no || batch.batch_no,
      item.reason || batch.reason,
      formatIsoOrEmpty(item.created_at),
    ]
      .map((field) => escapeCsvField(String(field ?? '')))
      .join(',')
  )
  return `\uFEFF${[header, ...lines].join('\r\n')}\r\n`
}

/** Build a safe filename `vouchers-<batch_or_date>.<ext>`. */
export function buildVoucherExportFilename(
  batchNo: string | undefined,
  ext: 'txt' | 'csv',
  now: Date = new Date()
): string {
  const sanitizedBatch = (batchNo ?? '')
    .trim()
    .replaceAll(/[^a-zA-Z0-9_-]+/g, '-')
    .replaceAll(/^-+|-+$/g, '')
  const suffix = sanitizedBatch || dayjs(now).format('YYYYMMDD-HHmmss')
  return `vouchers-${suffix}.${ext}`
}

/** Trigger a client-side file download from a UTF-8 text payload. */
export function downloadVoucherTextFile(
  content: string,
  filename: string,
  mimeType: string
): void {
  const blob = new Blob([content], { type: mimeType })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.append(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
