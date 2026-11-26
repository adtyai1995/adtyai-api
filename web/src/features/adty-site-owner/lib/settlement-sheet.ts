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

import { formatUsd } from '@/features/wallet/adty/lib/money'
import { formatTimestamp } from '@/lib/format'

import type { SettlementSheet } from '../api'
import { isNegativeAmount } from './site-owner'

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

function isLeadingCsvWhitespaceOrControl(char: string): boolean {
  const code = char.charCodeAt(0)
  return code <= 0x20 || (code >= 0x7f && code <= 0x9f) || /\s/.test(char)
}

function needsCsvFormulaPrefix(value: string): boolean {
  if (value.length === 0) return false
  const firstCode = value.charCodeAt(0)
  if (firstCode < 0x20 || (firstCode >= 0x7f && firstCode <= 0x9f)) {
    return true
  }
  let index = 0
  while (
    index < value.length &&
    isLeadingCsvWhitespaceOrControl(value[index])
  ) {
    index += 1
  }
  if (index >= value.length) return false
  const lead = value[index]
  return lead === '=' || lead === '+' || lead === '-' || lead === '@'
}

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

export function formatSettlementMoney(
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
    usd('voucher-funding', t('Voucher funding'), sheet.voucher_funding_usd),
    usd('trial', t('Trial expense'), sheet.trial_expense_usd),
    usd('commission', t('Commission'), sheet.commission_usd),
    usd('commission-paid', t('Commission paid'), sheet.commission_paid_usd),
    usd(
      'commission-clawback-return',
      t('Commission clawback return'),
      sheet.commission_clawback_return_usd
    ),
    pendingCoverageLine,
  ]

  const anomalyCount = sheet.anomaly_count ?? 0
  const walletAndWithdrawals: SettlementLine[] = [
    usd('margin', t('Margin change'), sheet.margin_delta_usd),
    usd('margin-topup', t('Margin top-up'), sheet.margin_topup_usd),
    usd(
      'earning-to-margin',
      t('Earnings moved to margin'),
      sheet.earning_to_margin_usd
    ),
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
  const safe = needsCsvFormulaPrefix(str) ? `'${str}` : str
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

function getEarningsPrintTone(raw: string | undefined): string {
  if (isNegativeAmount(raw)) return 'neg'
  if (isPositiveAmount(raw)) return 'pos'
  return ''
}

export function getEarningsTextTone(raw: string | undefined): string | undefined {
  if (isNegativeAmount(raw)) return 'text-destructive'
  if (isPositiveAmount(raw)) return 'text-emerald-600 dark:text-emerald-400'
  return undefined
}

export function resolveSettlementVersion(
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

export function triggerCsvDownload(csv: string, businessDay: string) {
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

export function triggerPrintHtml(html: string, title: string) {
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
