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
/* eslint-disable react-refresh/only-export-components */
import { skipToken, useQuery } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import {
  AlertTriangle,
  Calendar,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Download,
  FileSpreadsheet,
  Printer,
  RotateCcw,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestamp } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import {
  getOwnerSettlementDetail,
  getOwnerSettlementSheets,
  type SettlementPage,
  type SettlementSheet,
  siteOwnerKeys,
} from '../api'
import { isNegativeAmount } from '../lib/site-owner'

export type SettlementLine = {
  id: string
  label: string
  value: string
  raw?: string
  hideWhenZero: boolean
  tone?: 'default' | 'warning'
}

export type SettlementSectionId =
  | 'topup-consumption'
  | 'marketing-referral'
  | 'wallet-withdrawals'
  | 'owner-self-use'

export type SettlementSection = {
  id: SettlementSectionId
  title: string
  lines: SettlementLine[]
}

export type SettlementCsvOptions = {
  admin?: boolean
  siteName?: string
  generatedAt?: number
  revisionId?: number
  sources?: string[]
  legacyCoverage?: SettlementSheet['legacy_coverage']
}

export type SettlementPrintHtmlOptions = {
  admin?: boolean
  siteName?: string
  generatedAt?: number
  revisionId?: number
  sources?: string[]
  legacyCoverage?: SettlementSheet['legacy_coverage']
}

const PURE_DECIMAL_RE = /^-?\d+(?:\.\d+)?$/
const CSV_FORMULA_PREFIX_RE = /^[=+\-@\t\r]/

/** True when a numeric string or number represents zero (including empty / "-0.00"). */
export function isZeroAmount(value: string | number | undefined): boolean {
  if (value == null) return true
  if (typeof value === 'number') {
    return Number.isFinite(value) && value === 0
  }
  const trimmed = value.trim()
  if (trimmed === '') return true
  const parsed = Number(trimmed)
  return Number.isFinite(parsed) && parsed === 0
}

function isPositiveAmount(value: string | undefined): boolean {
  const parsed = Number(value ?? 0)
  return Number.isFinite(parsed) && parsed > 0
}

/** True when all six owner self-use amounts on a settlement sheet are zero. */
export function isOwnerSelfUseAllZero(
  sheet: Pick<
    SettlementSheet,
    | 'owner_self_use_topup_usd'
    | 'owner_self_use_voucher_usd'
    | 'owner_self_use_refund_usd'
    | 'owner_transfer_usd'
    | 'owner_self_use_usd'
    | 'owner_self_use_cost_usd'
  >
): boolean {
  return (
    isZeroAmount(sheet.owner_self_use_topup_usd) &&
    isZeroAmount(sheet.owner_self_use_voucher_usd) &&
    isZeroAmount(sheet.owner_self_use_refund_usd) &&
    isZeroAmount(sheet.owner_transfer_usd) &&
    isZeroAmount(sheet.owner_self_use_usd) &&
    isZeroAmount(sheet.owner_self_use_cost_usd)
  )
}

/** Filters section rows according to the "Hide zero rows" toggle. */
export function filterVisibleLines(
  lines: SettlementLine[],
  hideZeroRows: boolean
): SettlementLine[] {
  if (!hideZeroRows) return lines
  return lines.filter((line) => !line.hideWhenZero)
}

function parsePaidByCurrency(raw: string | undefined): [string, string][] {
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return []
    }
    return Object.entries(parsed as Record<string, unknown>).map(
      ([currency, amount]) => [currency, String(amount ?? '')]
    )
  } catch {
    return []
  }
}

function formatSettlementMoney(
  value: string | undefined,
  locale: string | undefined
): string {
  const parsed = Number(value || 0)
  return Number.isFinite(parsed)
    ? formatUsd(parsed, locale)
    : String(value ?? '')
}

export function buildSettlementSections(
  t: TFunction,
  sheet: SettlementSheet,
  locale: string | undefined
): SettlementSection[] {
  const money = (value: string | undefined) =>
    formatSettlementMoney(value, locale)
  const usd = (
    id: string,
    label: string,
    raw: string | undefined
  ): SettlementLine => {
    const normalized = raw ?? '0'
    return {
      id,
      label,
      value: money(normalized),
      raw: normalized,
      hideWhenZero: isZeroAmount(normalized),
    }
  }

  const topupAndConsumption: SettlementLine[] = [
    usd('topup', t('Top-up'), sheet.topup_face_usd),
    usd('paid', t('Paid in USD'), sheet.paid_usd),
  ]
  for (const [currency, amount] of parsePaidByCurrency(
    sheet.paid_by_currency
  )) {
    topupAndConsumption.push({
      id: `paid-${currency || 'other'}`,
      label: t('Other paid amounts'),
      value: `${amount} ${currency}`.trim(),
      hideWhenZero: isZeroAmount(amount),
    })
  }
  topupAndConsumption.push(
    usd('consumption', t('Customer consumption'), sheet.consumption_usd),
    usd('cost', t('Supply cost'), sheet.supply_cost_usd),
    usd('spread', t('Settlement spread'), sheet.spread_usd)
  )

  const pendingCoverageLine: SettlementLine = sheet.pending_coverage_unverified
    ? {
        id: 'pending',
        label: t('Pending coverage'),
        value: t('Historical amount requires verification'),
        hideWhenZero: false,
        tone: 'warning',
      }
    : usd('pending', t('Pending coverage'), sheet.pending_coverage_usd)

  const marketingAndReferral: SettlementLine[] = [
    usd('reserve', t('Gift reserve'), sheet.gift_reserve_usd),
    usd('funding', t('Gift funding'), sheet.gift_funding_usd),
    usd('release', t('Gift release'), sheet.gift_release_usd),
    usd('trial', t('Trial expense'), sheet.trial_expense_usd),
    usd('commission', t('Commission'), sheet.commission_usd),
    pendingCoverageLine,
  ]

  const anomalyCount = sheet.anomaly_count ?? 0
  const walletAndWithdrawals: SettlementLine[] = [
    usd('margin', t('Margin change'), sheet.margin_delta_usd),
    usd('earnings', t('Earnings change'), sheet.earnings_delta_usd),
    usd('available', t('Available change'), sheet.earnings_available_delta_usd),
    usd('frozen', t('Frozen change'), sheet.earnings_frozen_delta_usd),
    usd('withdrawal', t('Withdrawal amount'), sheet.withdrawal_usd),
    usd('fee', t('Withdrawal fee'), sheet.withdrawal_fee_usd),
    usd('adjustment', t('Financial adjustment'), sheet.adjustment_usd),
    {
      id: 'anomalies',
      label: t('Anomalies'),
      value: String(anomalyCount),
      hideWhenZero: anomalyCount === 0,
      tone: anomalyCount > 0 ? 'warning' : 'default',
    },
  ]

  const ownerSelfUse: SettlementLine[] = [
    usd(
      'owner-topup',
      t('Owner self-use top-ups'),
      sheet.owner_self_use_topup_usd ?? '0'
    ),
    usd(
      'owner-voucher',
      t('Owner self-use vouchers'),
      sheet.owner_self_use_voucher_usd ?? '0'
    ),
    usd(
      'owner-refund',
      t('Owner self-use refunds'),
      sheet.owner_self_use_refund_usd ?? '0'
    ),
    usd(
      'owner-transfer',
      t('Transfer to personal balance'),
      sheet.owner_transfer_usd ?? '0'
    ),
    usd('owner-usage', t('Owner API usage'), sheet.owner_self_use_usd ?? '0'),
    usd(
      'owner-cost',
      t('Owner API supply cost'),
      sheet.owner_self_use_cost_usd ?? '0'
    ),
  ]

  return [
    {
      id: 'topup-consumption',
      title: t('Customer top-up & consumption'),
      lines: topupAndConsumption,
    },
    {
      id: 'marketing-referral',
      title: t('Marketing & referral expenses'),
      lines: marketingAndReferral,
    },
    {
      id: 'wallet-withdrawals',
      title: t('Wallet movements & withdrawals'),
      lines: walletAndWithdrawals,
    },
    {
      id: 'owner-self-use',
      title: t('Owner self-use'),
      lines: ownerSelfUse,
    },
  ]
}

function escapeCsvCell(value: string, numeric = false): string {
  const str = String(value ?? '')
  if (numeric) {
    const trimmed = str.trim()
    if (
      trimmed !== '' &&
      PURE_DECIMAL_RE.test(trimmed) &&
      Number.isFinite(Number(trimmed))
    ) {
      return trimmed
    }
  }
  const safe = CSV_FORMULA_PREFIX_RE.test(str) ? `'${str}` : str
  if (
    safe.includes('"') ||
    safe.includes(',') ||
    safe.includes('\n') ||
    safe.includes('\r')
  ) {
    return `"${safe.replaceAll('"', '""')}"`
  }
  return safe
}

function formatCsvRow(
  cells: Array<{ value: string; numeric?: boolean }>
): string {
  return cells.map((cell) => escapeCsvCell(cell.value, cell.numeric)).join(',')
}

/** Builds a UTF-8 BOM CSV export of the settlement sheet with RFC 4180 + formula-injection safety. */
export function buildSettlementSheetCsv(
  sheet: SettlementSheet,
  t: TFunction,
  locale: string | undefined,
  options?: SettlementCsvOptions
): string {
  const isAdmin = Boolean(options?.admin)
  const sections = buildSettlementSections(t, sheet, locale)
  const generatedAt = options?.generatedAt ?? sheet.generated_at
  const legacyCoverage = options?.legacyCoverage ?? sheet.legacy_coverage ?? []

  const rows: string[] = [
    formatCsvRow([
      { value: t('Category') },
      { value: t('Description') },
      { value: t('Value') },
      { value: t('Amount (USD)') },
    ]),
  ]

  const pushMeta = (label: string, value: string) => {
    rows.push(
      formatCsvRow([
        { value: t('Metadata') },
        { value: label },
        { value },
        { value: '' },
      ])
    )
  }

  pushMeta(t('Date'), sheet.business_day)
  if (options?.siteName) {
    pushMeta(t('Site'), options.siteName)
  } else if (isAdmin) {
    pushMeta(t('Site ID'), String(sheet.site_id))
  }
  pushMeta(t('Timezone'), sheet.timezone)
  pushMeta(t('Day start'), formatTimestamp(sheet.day_start))
  pushMeta(t('Day end'), formatTimestamp(sheet.day_end))
  if (generatedAt) {
    pushMeta(t('Generated at'), formatTimestamp(generatedAt))
  }
  if (options?.revisionId != null) {
    pushMeta(t('Statement version'), `#${options.revisionId}`)
  }

  for (const section of sections) {
    for (const line of section.lines) {
      rows.push(
        formatCsvRow([
          { value: section.title },
          { value: line.label },
          { value: line.value },
          { value: line.raw ?? '', numeric: line.raw != null },
        ])
      )
    }
  }

  for (const entry of legacyCoverage) {
    rows.push(
      formatCsvRow([
        { value: t('Historical coverage adjustments') },
        { value: formatTimestamp(entry.created_at) },
        {
          value: `${t('Pending coverage')} USD ${entry.before_usd} → ${entry.after_usd}`,
        },
        { value: entry.after_usd, numeric: true },
      ])
    )
  }

  if (isAdmin) {
    rows.push(
      formatCsvRow([
        { value: t('Admin audit details') },
        { value: t('Batch') },
        { value: sheet.batch_id ?? '' },
        { value: '' },
      ])
    )
    rows.push(
      formatCsvRow([
        { value: t('Admin audit details') },
        { value: t('Event watermark') },
        { value: String(sheet.event_watermark ?? '') },
        { value: '' },
      ])
    )
    if (
      (options?.sources && options.sources.length > 0) ||
      options?.revisionId != null
    ) {
      const sourceText =
        options?.sources && options.sources.length > 0
          ? options.sources.join(', ')
          : t('None')
      rows.push(
        formatCsvRow([
          { value: t('Admin audit details') },
          { value: t('Source records') },
          { value: sourceText },
          { value: '' },
        ])
      )
    }
  }

  return `\uFEFF${rows.join('\r\n')}\r\n`
}

function escapeHtml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
}

/** Builds a self-contained, HTML-escaped A4 print document for Save as PDF. */
export function buildSettlementSheetPrintHtml(
  sheet: SettlementSheet,
  t: TFunction,
  locale: string | undefined,
  options?: SettlementPrintHtmlOptions
): string {
  const isAdmin = Boolean(options?.admin)
  const sections = buildSettlementSections(t, sheet, locale)
  const generatedAt = options?.generatedAt ?? sheet.generated_at
  const legacyCoverage = options?.legacyCoverage ?? sheet.legacy_coverage ?? []
  const money = (value: string | undefined) =>
    formatSettlementMoney(value, locale)

  let siteLabel = ''
  if (options?.siteName) {
    siteLabel = options.siteName
  } else if (isAdmin) {
    siteLabel = `${t('Site ID')} #${sheet.site_id}`
  }

  const anomalyCount = sheet.anomaly_count ?? 0
  const anomalyBadge =
    anomalyCount > 0
      ? `<span class="badge badge-danger">${escapeHtml(`${t('Anomalies')}: ${anomalyCount}`)}</span>`
      : `<span class="badge badge-ok">${escapeHtml(t('No anomalies'))}</span>`

  const revisionBadge =
    options?.revisionId != null
      ? `<span class="badge badge-muted">${escapeHtml(`${t('Statement version')} #${options.revisionId}`)}</span>`
      : ''

  const kpis: Array<{
    label: string
    value: string
    toneClass: string
  }> = [
    {
      label: t('Customer consumption'),
      value: money(sheet.consumption_usd),
      toneClass: isNegativeAmount(sheet.consumption_usd) ? 'neg' : '',
    },
    {
      label: t('Supply cost'),
      value: money(sheet.supply_cost_usd),
      toneClass: isNegativeAmount(sheet.supply_cost_usd) ? 'neg' : '',
    },
    {
      label: t('Settlement spread'),
      value: money(sheet.spread_usd),
      toneClass: isNegativeAmount(sheet.spread_usd) ? 'neg' : '',
    },
    {
      label: t('Earnings change'),
      value: money(sheet.earnings_delta_usd),
      toneClass: getEarningsPrintTone(sheet.earnings_delta_usd),
    },
  ]

  const kpiHtml = kpis
    .map(
      (kpi) =>
        `<div class="kpi"><div class="kpi-label">${escapeHtml(kpi.label)}</div><div class="kpi-value ${kpi.toneClass}">${escapeHtml(kpi.value)}</div></div>`
    )
    .join('')

  const sectionsHtml = sections
    .map((section) => {
      const rowsHtml = section.lines
        .map((line) => {
          const isNeg = isNegativeAmount(line.raw)
          let cls = 'val'
          if (isNeg) cls = 'val neg'
          else if (line.tone === 'warning') cls = 'val warn'
          return `<tr><td class="lbl">${escapeHtml(line.label)}</td><td class="${cls}">${escapeHtml(line.value)}</td></tr>`
        })
        .join('')
      return `<section class="card"><h2>${escapeHtml(section.title)}</h2><table><tbody>${rowsHtml}</tbody></table></section>`
    })
    .join('')

  const legacyHtml =
    legacyCoverage.length > 0
      ? `<section class="card"><h2>${escapeHtml(t('Historical coverage adjustments'))}</h2><table><tbody>${legacyCoverage
          .map(
            (entry) =>
              `<tr><td class="lbl">${escapeHtml(formatTimestamp(entry.created_at))}</td><td class="val">${escapeHtml(`${t('Pending coverage')} USD ${entry.before_usd} → ${entry.after_usd}`)}</td></tr>`
          )
          .join('')}</tbody></table></section>`
      : ''

  let adminHtml = ''
  if (isAdmin) {
    const auditRows: string[] = [
      `<tr><td class="lbl">${escapeHtml(t('Batch'))}</td><td class="val mono">${escapeHtml(sheet.batch_id ?? '')}</td></tr>`,
      `<tr><td class="lbl">${escapeHtml(t('Event watermark'))}</td><td class="val mono">${escapeHtml(String(sheet.event_watermark ?? ''))}</td></tr>`,
    ]
    if (
      (options?.sources && options.sources.length > 0) ||
      options?.revisionId != null
    ) {
      const sourceText =
        options?.sources && options.sources.length > 0
          ? options.sources.join(', ')
          : t('None')
      auditRows.push(
        `<tr><td class="lbl">${escapeHtml(t('Source records'))}</td><td class="val mono wrap">${escapeHtml(sourceText)}</td></tr>`
      )
    }
    adminHtml = `<section class="card"><h2>${escapeHtml(t('Admin audit details'))}</h2><table><tbody>${auditRows.join('')}</tbody></table></section>`
  }

  const metaItems: string[] = []
  if (siteLabel) {
    metaItems.push(`<span>${escapeHtml(siteLabel)}</span>`)
  }
  metaItems.push(`<span>${escapeHtml(sheet.timezone)}</span>`)
  metaItems.push(
    `<span class="num">${escapeHtml(`${formatTimestamp(sheet.day_start)} – ${formatTimestamp(sheet.day_end)}`)}</span>`
  )
  if (generatedAt) {
    metaItems.push(
      `<span class="num">${escapeHtml(`${t('Generated at')}: ${formatTimestamp(generatedAt)}`)}</span>`
    )
  }

  const docTitle = `${t('Settlement sheet')}-${sheet.business_day}`
  const htmlLang = escapeHtml(locale || 'en')

  return `<!DOCTYPE html>
<html lang="${htmlLang}">
<head>
<meta charset="utf-8" />
<title>${escapeHtml(docTitle)}</title>
<style>
@page { size: A4 portrait; margin: 14mm; }
* { box-sizing: border-box; }
body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; color: #111827; background: #ffffff; font-size: 12px; line-height: 1.45; }
.header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; border-bottom: 1.5px solid #111827; padding-bottom: 10px; margin-bottom: 14px; }
h1 { margin: 0 0 4px 0; font-size: 17px; font-weight: 700; letter-spacing: -0.01em; }
.meta { display: flex; flex-wrap: wrap; gap: 4px 12px; color: #4b5563; font-size: 11px; }
.badges { display: flex; flex-wrap: wrap; gap: 6px; justify-content: flex-end; }
.badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 10.5px; font-weight: 600; border: 1px solid #d1d5db; }
.badge-ok { color: #047857; border-color: #a7f3d0; background: #ecfdf5; }
.badge-danger { color: #b91c1c; border-color: #fecaca; background: #fef2f2; }
.badge-muted { color: #374151; background: #f3f4f6; }
.kpis { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; margin-bottom: 14px; }
.kpi { border: 1px solid #d1d5db; border-radius: 6px; padding: 8px 10px; break-inside: avoid; }
.kpi-label { font-size: 10.5px; color: #4b5563; margin-bottom: 4px; }
.kpi-value { font-size: 15px; font-weight: 700; font-variant-numeric: tabular-nums; }
.card { border: 1px solid #d1d5db; border-radius: 6px; margin-bottom: 10px; overflow: hidden; break-inside: avoid; }
.card h2 { margin: 0; padding: 7px 10px; font-size: 12px; font-weight: 600; background: #f9fafb; border-bottom: 1px solid #e5e7eb; }
table { width: 100%; border-collapse: collapse; }
tr + tr td { border-top: 1px solid #f3f4f6; }
td { padding: 5px 10px; vertical-align: top; }
.lbl { color: #4b5563; }
.val { text-align: right; font-weight: 500; font-variant-numeric: tabular-nums; white-space: nowrap; }
.num { font-variant-numeric: tabular-nums; }
.neg { color: #b91c1c; }
.pos { color: #047857; }
.warn { color: #b45309; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 11px; }
.wrap { white-space: normal; word-break: break-all; }
</style>
</head>
<body>
<header class="header">
  <div>
    <h1>${escapeHtml(`${t('Daily settlement statement')} · ${sheet.business_day}`)}</h1>
    <div class="meta">${metaItems.join('')}</div>
  </div>
  <div class="badges">${anomalyBadge}${revisionBadge}</div>
</header>
<div class="kpis">${kpiHtml}</div>
${sectionsHtml}
${legacyHtml}
${adminHtml}
</body>
</html>`
}

function getEarningsPrintTone(raw: string | undefined): string {
  if (isNegativeAmount(raw)) return 'neg'
  if (isPositiveAmount(raw)) return 'pos'
  return ''
}

function getEarningsTextTone(raw: string | undefined): string | undefined {
  if (isNegativeAmount(raw)) return 'text-destructive'
  if (isPositiveAmount(raw)) return 'text-emerald-600 dark:text-emerald-400'
  return undefined
}

function resolveSettlementVersion(
  sheet: SettlementSheet,
  selectedVersion: string
) {
  const revisions = sheet.revisions ?? []
  const revision =
    selectedVersion === 'latest'
      ? revisions.at(-1)
      : revisions.find((r) => String(r.id) === selectedVersion)
  const activeSheet = revision?.after ?? sheet
  const generatedAt = revision?.created_at || sheet.generated_at
  return { revisions, revision, activeSheet, generatedAt }
}

function triggerCsvDownload(csv: string, businessDay: string) {
  if (typeof document === 'undefined' || typeof URL === 'undefined') return
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `settlement-sheet-${businessDay}.csv`
  anchor.style.display = 'none'
  document.body.append(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function triggerPrintHtml(html: string, title: string) {
  if (typeof window === 'undefined' || typeof document === 'undefined') return
  const previousTitle = document.title
  document.title = title

  const iframe = document.createElement('iframe')
  iframe.setAttribute('aria-hidden', 'true')
  iframe.style.position = 'fixed'
  iframe.style.right = '0'
  iframe.style.bottom = '0'
  iframe.style.width = '0'
  iframe.style.height = '0'
  iframe.style.border = '0'
  iframe.style.visibility = 'hidden'

  let cleaned = false
  let fallbackTimer = 0
  const cleanup = () => {
    if (cleaned) return
    cleaned = true
    window.clearTimeout(fallbackTimer)
    document.title = previousTitle
    iframe.remove()
  }

  fallbackTimer = window.setTimeout(cleanup, 60_000)

  const handleLoad = () => {
    const win = iframe.contentWindow
    if (!win) {
      cleanup()
      window.print()
      return
    }
    win.addEventListener('afterprint', cleanup, { once: true })
    window.addEventListener('afterprint', cleanup, { once: true })
    try {
      win.focus()
      win.print()
    } catch {
      cleanup()
      window.print()
    }
  }

  iframe.addEventListener('load', handleLoad, { once: true })
  iframe.srcdoc = html
  document.body.append(iframe)
}

function SectionRows({
  lines,
  hideZeroRows,
  emptyLabel,
}: {
  lines: SettlementLine[]
  hideZeroRows: boolean
  emptyLabel: string
}) {
  const visible = filterVisibleLines(lines, hideZeroRows)
  if (visible.length === 0) {
    return (
      <p className='text-muted-foreground px-4 py-4 text-sm'>{emptyLabel}</p>
    )
  }
  return (
    <dl className='divide-y'>
      {visible.map((line) => {
        const negative = isNegativeAmount(line.raw)
        return (
          <div
            key={line.id}
            className='flex items-baseline justify-between gap-4 px-4 py-2.5 text-sm'
          >
            <dt className='text-muted-foreground'>{line.label}</dt>
            <dd
              className={cn(
                'text-right font-medium tabular-nums',
                negative && 'text-destructive',
                !negative &&
                  line.tone === 'warning' &&
                  'text-amber-600 dark:text-amber-400'
              )}
              data-negative={negative ? 'true' : undefined}
            >
              {line.value}
            </dd>
          </div>
        )
      })}
    </dl>
  )
}

function CollapsibleSectionCard({
  title,
  defaultOpen,
  badge,
  children,
}: {
  title: string
  defaultOpen: boolean
  badge?: React.ReactNode
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <Card className='gap-0 py-0'>
        <CollapsibleTrigger
          render={
            <button
              type='button'
              className='hover:bg-muted/40 focus-visible:ring-ring/50 flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors outline-none focus-visible:ring-3'
            />
          }
        >
          <span className='flex items-center gap-2 text-sm font-semibold'>
            {open ? (
              <ChevronDown className='text-muted-foreground size-4 shrink-0' />
            ) : (
              <ChevronRight className='text-muted-foreground size-4 shrink-0' />
            )}
            <span>{title}</span>
          </span>
          {badge}
        </CollapsibleTrigger>
        <CollapsibleContent className='border-t'>{children}</CollapsibleContent>
      </Card>
    </Collapsible>
  )
}

export type SettlementSheetLinesProps = {
  sheet: SettlementSheet
  admin?: boolean
  siteName?: string
  selectedVersion?: string
  onSelectedVersionChange?: (version: string) => void
  hideZeroRows?: boolean
  onHideZeroRowsChange?: (hide: boolean) => void
  showInlineControls?: boolean
}

export function SettlementSheetLines({
  sheet,
  admin,
  siteName,
  selectedVersion: controlledVersion,
  onSelectedVersionChange,
  hideZeroRows: controlledHideZero,
  onHideZeroRowsChange,
  showInlineControls = true,
}: SettlementSheetLinesProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [internalVersion, setInternalVersion] = useState('latest')
  const [internalHideZero, setInternalHideZero] = useState(false)

  const selectedVersion = controlledVersion ?? internalVersion
  const setSelectedVersion = onSelectedVersionChange ?? setInternalVersion
  const hideZeroRows = controlledHideZero ?? internalHideZero
  const setHideZeroRows = onHideZeroRowsChange ?? setInternalHideZero

  const isAdmin = Boolean(admin)
  const { revisions, revision, activeSheet, generatedAt } =
    resolveSettlementVersion(sheet, selectedVersion)

  const sections = buildSettlementSections(t, activeSheet, locale)
  const regularSections = sections.filter(
    (section) => section.id !== 'owner-self-use'
  )
  const selfUseSection = sections.find(
    (section) => section.id === 'owner-self-use'
  )
  const allSelfUseZero = isOwnerSelfUseAllZero(activeSheet)
  const money = (value: string | undefined) =>
    formatSettlementMoney(value, locale)

  let siteLabel = ''
  if (siteName) {
    siteLabel = siteName
  } else if (isAdmin) {
    siteLabel = `${t('Site ID')} #${activeSheet.site_id}`
  }

  let revisionBadgeLabel = ''
  if (revision) {
    revisionBadgeLabel =
      selectedVersion === 'latest'
        ? `${t('Latest')} · #${revision.id}`
        : `#${revision.id}`
  } else if (sheet.legacy_coverage?.length) {
    revisionBadgeLabel = t('Recorded statement')
  } else if (revisions.length > 0) {
    revisionBadgeLabel = t('Original')
  }

  const handleExportCsv = () => {
    const csv = buildSettlementSheetCsv(activeSheet, t, locale, {
      admin: isAdmin,
      siteName,
      generatedAt,
      revisionId: revision?.id,
      sources: isAdmin ? revision?.sources : undefined,
      legacyCoverage: sheet.legacy_coverage,
    })
    triggerCsvDownload(csv, activeSheet.business_day)
  }

  const handleSavePdf = () => {
    const html = buildSettlementSheetPrintHtml(activeSheet, t, locale, {
      admin: isAdmin,
      siteName,
      generatedAt,
      revisionId: revision?.id,
      sources: isAdmin ? revision?.sources : undefined,
      legacyCoverage: sheet.legacy_coverage,
    })
    triggerPrintHtml(
      html,
      `${t('Settlement sheet')}-${activeSheet.business_day}`
    )
  }

  const anomalyCount = activeSheet.anomaly_count ?? 0
  const spreadNegative = isNegativeAmount(activeSheet.spread_usd)
  const earningsNegative = isNegativeAmount(activeSheet.earnings_delta_usd)
  const consumptionNegative = isNegativeAmount(activeSheet.consumption_usd)
  const supplyCostNegative = isNegativeAmount(activeSheet.supply_cost_usd)

  return (
    <div className='flex flex-col gap-4'>
      {showInlineControls && (
        <Card size='sm' className='print:hidden'>
          <CardContent className='flex flex-wrap items-center justify-between gap-3'>
            <div className='flex flex-wrap items-center gap-3'>
              {revisions.length > 0 && (
                <div className='flex items-center gap-2'>
                  <Label
                    htmlFor='inline-settlement-version'
                    className='text-muted-foreground text-xs font-medium'
                  >
                    {t('Statement version')}
                  </Label>
                  <NativeSelect
                    id='inline-settlement-version'
                    aria-label={t('Statement version')}
                    value={selectedVersion}
                    onChange={(e) => setSelectedVersion(e.target.value)}
                  >
                    <NativeSelectOption value='latest'>
                      {t('Latest')}
                    </NativeSelectOption>
                    <NativeSelectOption value='original'>
                      {sheet.legacy_coverage?.length
                        ? t('Recorded statement')
                        : t('Original')}
                    </NativeSelectOption>
                    {revisions.map((r) => (
                      <NativeSelectOption key={r.id} value={String(r.id)}>
                        {`#${r.id} · ${formatTimestamp(r.created_at)}`}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </div>
              )}
              <div className='flex items-center gap-2'>
                <Switch
                  id='inline-settlement-hide-zero'
                  checked={hideZeroRows}
                  onCheckedChange={setHideZeroRows}
                />
                <Label
                  htmlFor='inline-settlement-hide-zero'
                  className='cursor-pointer text-xs font-medium'
                >
                  {t('Hide zero rows')}
                </Label>
              </div>
            </div>
            <div className='flex items-center gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={handleExportCsv}
              >
                <Download className='size-3.5' />
                {t('Export CSV')}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={handleSavePdf}
              >
                <Printer className='size-3.5' />
                {t('Save as PDF')}
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader className='flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between'>
          <div className='flex items-start gap-3'>
            <div className='bg-muted/60 text-muted-foreground mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-lg border'>
              <FileSpreadsheet className='size-4' />
            </div>
            <div className='flex flex-col gap-1'>
              <CardTitle className='text-base font-semibold tracking-tight tabular-nums sm:text-lg'>
                {`${t('Daily settlement statement')} · ${activeSheet.business_day}`}
              </CardTitle>
              <CardDescription className='flex flex-wrap items-center gap-x-3 gap-y-1 text-xs'>
                {siteLabel && (
                  <span className='text-foreground/80 font-medium'>
                    {siteLabel}
                  </span>
                )}
                <span>{activeSheet.timezone}</span>
                <span className='inline-flex items-center gap-1 tabular-nums'>
                  <Calendar className='size-3 shrink-0' />
                  {`${formatTimestamp(activeSheet.day_start)} – ${formatTimestamp(activeSheet.day_end)}`}
                </span>
                {generatedAt ? (
                  <span className='tabular-nums'>
                    {`${t('Generated at')}: ${formatTimestamp(generatedAt)}`}
                  </span>
                ) : null}
              </CardDescription>
            </div>
          </div>
          <div className='flex flex-wrap items-center gap-2'>
            {anomalyCount > 0 ? (
              <Badge variant='destructive' className='tabular-nums'>
                <AlertTriangle />
                {`${t('Anomalies')}: ${anomalyCount}`}
              </Badge>
            ) : (
              <Badge
                variant='outline'
                className='border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
              >
                <CheckCircle2 />
                {t('No anomalies')}
              </Badge>
            )}
            {revisionBadgeLabel && (
              <Badge variant='secondary' className='tabular-nums'>
                {revisionBadgeLabel}
              </Badge>
            )}
          </div>
        </CardHeader>
      </Card>

      <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
        <Card size='sm'>
          <CardHeader className='gap-1'>
            <CardDescription className='text-xs font-medium'>
              {t('Customer consumption')}
            </CardDescription>
            <CardTitle
              className={cn(
                'text-xl font-semibold tracking-tight tabular-nums',
                consumptionNegative && 'text-destructive'
              )}
              data-negative={consumptionNegative ? 'true' : undefined}
            >
              {money(activeSheet.consumption_usd)}
            </CardTitle>
          </CardHeader>
        </Card>

        <Card size='sm'>
          <CardHeader className='gap-1'>
            <CardDescription className='text-xs font-medium'>
              {t('Supply cost')}
            </CardDescription>
            <CardTitle
              className={cn(
                'text-xl font-semibold tracking-tight tabular-nums',
                supplyCostNegative && 'text-destructive'
              )}
              data-negative={supplyCostNegative ? 'true' : undefined}
            >
              {money(activeSheet.supply_cost_usd)}
            </CardTitle>
          </CardHeader>
        </Card>

        <Card size='sm'>
          <CardHeader className='gap-1'>
            <CardDescription className='text-xs font-medium'>
              {t('Settlement spread')}
            </CardDescription>
            <CardTitle
              className={cn(
                'text-xl font-semibold tracking-tight tabular-nums',
                spreadNegative && 'text-destructive'
              )}
              data-negative={spreadNegative ? 'true' : undefined}
            >
              {money(activeSheet.spread_usd)}
            </CardTitle>
          </CardHeader>
        </Card>

        <Card size='sm'>
          <CardHeader className='gap-1'>
            <CardDescription className='text-xs font-medium'>
              {t('Earnings change')}
            </CardDescription>
            <CardTitle
              className={cn(
                'text-xl font-semibold tracking-tight tabular-nums',
                getEarningsTextTone(activeSheet.earnings_delta_usd)
              )}
              data-negative={earningsNegative ? 'true' : undefined}
            >
              {money(activeSheet.earnings_delta_usd)}
            </CardTitle>
          </CardHeader>
        </Card>
      </div>

      <div className='grid gap-4 lg:grid-cols-2'>
        {regularSections.map((section) => (
          <Card key={section.id} className='gap-0 py-0'>
            <CardHeader className='border-b py-3'>
              <CardTitle className='text-sm font-semibold'>
                {section.title}
              </CardTitle>
            </CardHeader>
            <CardContent className='p-0'>
              <SectionRows
                lines={section.lines}
                hideZeroRows={hideZeroRows}
                emptyLabel={t('All items in this section are zero')}
              />
            </CardContent>
          </Card>
        ))}

        {selfUseSection && (
          <CollapsibleSectionCard
            key={`self-use-${activeSheet.id}-${selectedVersion}-${allSelfUseZero ? 'zero' : 'active'}`}
            title={selfUseSection.title}
            defaultOpen={!allSelfUseZero}
            badge={
              allSelfUseZero ? (
                <Badge variant='secondary' className='text-muted-foreground'>
                  {t('All zero')}
                </Badge>
              ) : undefined
            }
          >
            <SectionRows
              lines={selfUseSection.lines}
              hideZeroRows={hideZeroRows}
              emptyLabel={t('All items in this section are zero')}
            />
          </CollapsibleSectionCard>
        )}
      </div>

      {!!sheet.legacy_coverage?.length && (
        <CollapsibleSectionCard
          key={`legacy-${sheet.id}`}
          title={t('Historical coverage adjustments')}
          defaultOpen={false}
          badge={
            <Badge variant='outline' className='tabular-nums'>
              {sheet.legacy_coverage.length}
            </Badge>
          }
        >
          <ul className='divide-y text-sm'>
            {sheet.legacy_coverage.map((entry) => (
              <li
                key={entry.id}
                className='flex flex-wrap items-baseline justify-between gap-2 px-4 py-2.5'
              >
                <span className='text-muted-foreground tabular-nums'>
                  {formatTimestamp(entry.created_at)}
                </span>
                <span className='font-medium tabular-nums'>
                  {`${t('Pending coverage')} USD ${entry.before_usd} → ${entry.after_usd}`}
                </span>
              </li>
            ))}
          </ul>
        </CollapsibleSectionCard>
      )}

      {isAdmin && (
        <CollapsibleSectionCard
          key={`admin-audit-${activeSheet.id}-${selectedVersion}`}
          title={t('Admin audit details')}
          defaultOpen
        >
          <dl className='divide-y text-sm'>
            <div className='flex items-baseline justify-between gap-4 px-4 py-2.5'>
              <dt className='text-muted-foreground'>{t('Batch')}</dt>
              <dd className='text-right font-mono text-xs break-all'>
                {activeSheet.batch_id ?? ''}
              </dd>
            </div>
            <div className='flex items-baseline justify-between gap-4 px-4 py-2.5'>
              <dt className='text-muted-foreground'>{t('Event watermark')}</dt>
              <dd className='text-right font-mono text-xs tabular-nums'>
                {String(activeSheet.event_watermark ?? '')}
              </dd>
            </div>
            {revision && (
              <div className='flex flex-col gap-1 px-4 py-2.5 sm:flex-row sm:items-baseline sm:justify-between sm:gap-4'>
                <dt className='text-muted-foreground shrink-0'>
                  {t('Source records')}
                </dt>
                <dd className='text-right font-mono text-xs break-all'>
                  {(revision.sources?.length ?? 0) > 0
                    ? revision.sources?.join(', ')
                    : t('None')}
                </dd>
              </div>
            )}
          </dl>
        </CollapsibleSectionCard>
      )}
    </div>
  )
}

/** Previous-day check sheet. It lists recorded lines and does not credit earnings. */
export function SettlementSheetPanel({
  siteId,
  siteName,
}: {
  siteId?: number
  siteName?: string
}) {
  return (
    <SettlementBrowser
      scope={siteOwnerKeys.list('settlement-sheets', { siteId })}
      siteId={siteId}
      siteName={siteName}
      list={getOwnerSettlementSheets}
      detail={getOwnerSettlementDetail}
    />
  )
}

type BrowserProps = {
  scope: readonly unknown[]
  siteId?: number
  siteName?: string
  admin?: boolean
  list: (page: number, day: string, siteId?: number) => Promise<SettlementPage>
  detail: (id: number, siteId?: number) => Promise<SettlementSheet>
}

export function SettlementBrowser({
  scope,
  siteId,
  siteName,
  admin,
  list,
  detail,
}: BrowserProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const [day, setDay] = useState('')
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState(0)
  const [hideZeroRows, setHideZeroRows] = useState(false)
  const [versionChoice, setVersionChoice] = useState<{
    sheetId: number
    value: string
  }>({ sheetId: 0, value: 'latest' })

  const isAdmin = Boolean(admin)
  let site = siteId
  if (isAdmin) {
    site = filter ? Number(filter) : undefined
  }

  const query = useQuery({
    queryKey: [...scope, 'page', site, page, day],
    queryFn: () => list(page, day, site),
  })
  const rows = query.data?.items ?? []
  const current = rows.find((row) => row.id === selected) ?? rows[0]

  const item = useQuery({
    queryKey: [...scope, 'detail', site, current?.id],
    queryFn: current ? () => detail(current.id, site) : skipToken,
  })

  const currentId = current?.id ?? 0
  const selectedVersion =
    versionChoice.sheetId === currentId ? versionChoice.value : 'latest'
  const setSelectedVersion = (value: string) =>
    setVersionChoice({ sheetId: currentId, value })

  const detailSheet = current ? item.data : undefined
  const resolved = detailSheet
    ? resolveSettlementVersion(detailSheet, selectedVersion)
    : undefined
  const revisions = resolved?.revisions ?? []

  const handleExportCsv = () => {
    if (!detailSheet || !resolved) return
    const csv = buildSettlementSheetCsv(resolved.activeSheet, t, locale, {
      admin: isAdmin,
      siteName,
      generatedAt: resolved.generatedAt,
      revisionId: resolved.revision?.id,
      sources: isAdmin ? resolved.revision?.sources : undefined,
      legacyCoverage: detailSheet.legacy_coverage,
    })
    triggerCsvDownload(csv, resolved.activeSheet.business_day)
  }

  const handleSavePdf = () => {
    if (!detailSheet || !resolved) return
    const html = buildSettlementSheetPrintHtml(
      resolved.activeSheet,
      t,
      locale,
      {
        admin: isAdmin,
        siteName,
        generatedAt: resolved.generatedAt,
        revisionId: resolved.revision?.id,
        sources: isAdmin ? resolved.revision?.sources : undefined,
        legacyCoverage: detailSheet.legacy_coverage,
      }
    )
    triggerPrintHtml(
      html,
      `${t('Settlement sheet')}-${resolved.activeSheet.business_day}`
    )
  }

  return (
    <section className='flex flex-col gap-4'>
      <Card size='sm' className='print:hidden'>
        <CardContent className='flex flex-col gap-3'>
          <div className='flex flex-wrap items-end gap-3'>
            <div className='flex flex-col gap-1.5'>
              <Label
                htmlFor='settlement-date-filter'
                className='text-muted-foreground text-xs font-medium'
              >
                <Calendar className='size-3.5' />
                {t('Date')}
              </Label>
              <div className='flex items-center gap-1.5'>
                <Input
                  id='settlement-date-filter'
                  type='date'
                  className='h-8 w-40'
                  value={day}
                  onChange={(e) => {
                    setDay(e.target.value)
                    setPage(1)
                  }}
                />
                {day && (
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-sm'
                    aria-label={t('Clear')}
                    title={t('Clear')}
                    onClick={() => {
                      setDay('')
                      setPage(1)
                    }}
                  >
                    <RotateCcw className='size-3.5' />
                  </Button>
                )}
              </div>
            </div>

            {isAdmin && (
              <div className='flex flex-col gap-1.5'>
                <Label
                  htmlFor='settlement-site-filter'
                  className='text-muted-foreground text-xs font-medium'
                >
                  {t('Site ID')}
                </Label>
                <Input
                  id='settlement-site-filter'
                  type='number'
                  min={1}
                  className='h-8 w-28 tabular-nums'
                  value={filter}
                  onChange={(e) => {
                    setFilter(e.target.value)
                    setPage(1)
                  }}
                />
              </div>
            )}

            {rows.length > 0 && current && (
              <div className='flex flex-col gap-1.5'>
                <Label
                  htmlFor='settlement-sheet-select'
                  className='text-muted-foreground text-xs font-medium'
                >
                  {t('Settlement sheet')}
                </Label>
                <NativeSelect
                  id='settlement-sheet-select'
                  aria-label={t('Settlement sheet')}
                  value={String(current.id)}
                  onChange={(e) => setSelected(Number(e.target.value))}
                >
                  {rows.map((row) => (
                    <NativeSelectOption key={row.id} value={row.id}>
                      {isAdmin
                        ? `${t('Site ID')} ${row.site_id} · ${row.business_day}`
                        : row.business_day}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
            )}

            {revisions.length > 0 && detailSheet && (
              <div className='flex flex-col gap-1.5'>
                <Label
                  htmlFor='settlement-version-select'
                  className='text-muted-foreground text-xs font-medium'
                >
                  {t('Statement version')}
                </Label>
                <NativeSelect
                  id='settlement-version-select'
                  aria-label={t('Statement version')}
                  value={selectedVersion}
                  onChange={(e) => setSelectedVersion(e.target.value)}
                >
                  <NativeSelectOption value='latest'>
                    {t('Latest')}
                  </NativeSelectOption>
                  <NativeSelectOption value='original'>
                    {detailSheet.legacy_coverage?.length
                      ? t('Recorded statement')
                      : t('Original')}
                  </NativeSelectOption>
                  {revisions.map((r) => (
                    <NativeSelectOption key={r.id} value={String(r.id)}>
                      {`#${r.id} · ${formatTimestamp(r.created_at)}`}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
            )}
          </div>

          <Separator />

          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div className='flex items-center gap-2'>
              <Switch
                id='settlement-hide-zero-rows'
                checked={hideZeroRows}
                onCheckedChange={setHideZeroRows}
              />
              <Label
                htmlFor='settlement-hide-zero-rows'
                className='cursor-pointer text-xs font-medium'
              >
                {t('Hide zero rows')}
              </Label>
            </div>

            <div className='flex flex-wrap items-center gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={!detailSheet}
                onClick={handleExportCsv}
              >
                <Download className='size-3.5' />
                {t('Export CSV')}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={!detailSheet}
                onClick={handleSavePdf}
              >
                <Printer className='size-3.5' />
                {t('Save as PDF')}
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      {query.isPending && <Skeleton className='h-48 w-full rounded-xl' />}

      {query.isError && (
        <ErrorState
          description={getServerErrorMessage(query.error)}
          onRetry={() => void query.refetch()}
        />
      )}

      {query.isSuccess && rows.length === 0 && (
        <EmptyState className='min-h-24' title={t('No settlement sheet yet')} />
      )}

      {item.isError && (
        <ErrorState
          description={getServerErrorMessage(item.error)}
          onRetry={() => void item.refetch()}
        />
      )}

      {current && item.isPending && (
        <Skeleton className='h-64 w-full rounded-xl' />
      )}

      {current && detailSheet && (
        <SettlementSheetLines
          key={current.id}
          sheet={detailSheet}
          admin={isAdmin}
          siteName={siteName}
          selectedVersion={selectedVersion}
          onSelectedVersionChange={setSelectedVersion}
          hideZeroRows={hideZeroRows}
          onHideZeroRowsChange={setHideZeroRows}
          showInlineControls={false}
        />
      )}

      {((query.data?.total ?? 0) > 20 || page > 1) && (
        <div className='flex gap-2 print:hidden'>
          <Button
            type='button'
            variant='outline'
            disabled={page === 1}
            onClick={() => setPage(page - 1)}
          >
            {t('Previous page')}
          </Button>
          <Button
            type='button'
            variant='outline'
            disabled={page * 20 >= (query.data?.total ?? 0)}
            onClick={() => setPage(page + 1)}
          >
            {t('Next page')}
          </Button>
        </div>
      )}
    </section>
  )
}
