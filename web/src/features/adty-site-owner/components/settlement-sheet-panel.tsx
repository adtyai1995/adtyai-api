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
import { skipToken, useQuery } from '@tanstack/react-query'
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
import {
  buildSettlementSections,
  buildSettlementSheetCsv,
  buildSettlementSheetPrintHtml,
  filterVisibleLines,
  formatSettlementMoney,
  getEarningsTextTone,
  isOwnerSelfUseAllZero,
  isZeroAmount,
  resolveSettlementVersion,
  triggerCsvDownload,
  triggerPrintHtml,
  type SettlementLine,
} from '../lib/settlement-sheet'
import { isNegativeAmount } from '../lib/site-owner'

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

  const money = (value: string | undefined) =>
    formatSettlementMoney(value, locale)
  const partnerReferralRaw = activeSheet.partner_referral_earning_usd ?? '0'
  const partnerReferralLine: SettlementLine = {
    id: 'partner-referral-earning',
    label: t('Partner referral commission'),
    value: money(partnerReferralRaw),
    raw: partnerReferralRaw,
    hideWhenZero: isZeroAmount(partnerReferralRaw),
  }
  const sections = buildSettlementSections(t, activeSheet, locale).map(
    (section) =>
      section.id === 'topup-consumption'
        ? { ...section, lines: [...section.lines, partnerReferralLine] }
        : section
  )
  const regularSections = sections.filter(
    (section) => section.id !== 'owner-self-use'
  )
  const selfUseSection = sections.find(
    (section) => section.id === 'owner-self-use'
  )
  const allSelfUseZero = isOwnerSelfUseAllZero(activeSheet)

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
  const partnerReferralNegative = isNegativeAmount(partnerReferralRaw)
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

      <dl
        aria-label={t('Settlement sheet')}
        className='border-border bg-border grid grid-cols-2 gap-px overflow-hidden rounded-[14px] border lg:grid-cols-5'
      >
        <div className='bg-card flex flex-col gap-2 px-5 py-4'>
          <dt className='text-dim text-[12px] leading-none'>
            {t('Customer consumption')}
          </dt>
          <dd
            className={cn(
              'font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums',
              consumptionNegative && 'text-destructive'
            )}
            data-negative={consumptionNegative ? 'true' : undefined}
          >
            {money(activeSheet.consumption_usd)}
          </dd>
        </div>

        <div className='bg-card flex flex-col gap-2 px-5 py-4'>
          <dt className='text-dim text-[12px] leading-none'>
            {t('Supply cost')}
          </dt>
          <dd
            className={cn(
              'font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums',
              supplyCostNegative && 'text-destructive'
            )}
            data-negative={supplyCostNegative ? 'true' : undefined}
          >
            {money(activeSheet.supply_cost_usd)}
          </dd>
        </div>

        <div className='bg-card flex flex-col gap-2 px-5 py-4'>
          <dt className='text-dim text-[12px] leading-none'>
            {t('Settlement spread')}
          </dt>
          <dd
            className={cn(
              'font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums',
              spreadNegative && 'text-destructive'
            )}
            data-negative={spreadNegative ? 'true' : undefined}
          >
            {money(activeSheet.spread_usd)}
          </dd>
        </div>

        <div className='bg-card flex flex-col gap-2 px-5 py-4'>
          <dt className='text-dim text-[12px] leading-none'>
            {t('Partner referral commission')}
          </dt>
          <dd
            className={cn(
              'font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums',
              partnerReferralNegative && 'text-destructive'
            )}
            data-negative={partnerReferralNegative ? 'true' : undefined}
          >
            {money(partnerReferralRaw)}
          </dd>
        </div>

        <div className='bg-card col-span-2 flex flex-col gap-2 px-5 py-4 lg:col-span-1'>
          <dt className='text-dim text-[12px] leading-none'>
            {t('Earnings change')}
          </dt>
          <dd
            className={cn(
              'font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums',
              getEarningsTextTone(activeSheet.earnings_delta_usd)
            )}
            data-negative={earningsNegative ? 'true' : undefined}
          >
            {money(activeSheet.earnings_delta_usd)}
          </dd>
        </div>
      </dl>

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
