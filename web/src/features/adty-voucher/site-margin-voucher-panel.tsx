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
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  VOUCHER_STATUSES,
  voucherStatusLabel,
} from '@/features/adty-ledger/voucher-labels'
import { getSites, siteAdminKeys } from '@/features/adty-site-admin/api'
import { parseSiteBrand } from '@/features/adty-site-admin/lib/sites'
import type { AdminSite } from '@/features/adty-site-admin/types'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'

import {
  adtyVoucherKeys,
  cancelAdminVoucherById,
  createVoucherBatchIntentKey,
  freezeAdminVoucherById,
  getAdminVouchersList,
  issueAdminVoucherBatch,
  type VoucherBatchIssued,
} from './api'
import {
  computeVoucherBatchTotal,
  normalizeVoucherBatchResult,
  VOUCHER_BATCH_MAX_COUNT,
  VOUCHER_BATCH_MIN_COUNT,
  type NormalizedVoucherBatch,
} from './export'
import { VoucherBatchResultDialog } from './voucher-batch-result-dialog'
import { VoucherListTable } from './voucher-list-table'

const PAGE_SIZE = 20
const SITES_PAGE_SIZE = 100
const MAX_SITES_PAGES = 50

async function fetchAllSitesForMarginVouchers(): Promise<AdminSite[]> {
  const collected: AdminSite[] = []
  const seen = new Set<number>()
  for (let currentPage = 1; currentPage <= MAX_SITES_PAGES; currentPage += 1) {
    const res = await getSites({ p: currentPage, page_size: SITES_PAGE_SIZE })
    const batch = Array.isArray(res?.items) ? res.items : []
    for (const site of batch) {
      if (!seen.has(site.id)) {
        seen.add(site.id)
        collected.push(site)
      }
    }
    const total = Number(res?.total) || 0
    if (
      batch.length < SITES_PAGE_SIZE ||
      (total > 0 && collected.length >= total)
    ) {
      break
    }
  }
  return collected
}

function formatSiteOptionLabel(site: AdminSite): string {
  const brand = parseSiteBrand(site.brand)
  const host = site.host || `${site.subdomain}.${site.root_domain}`
  const owner = formatUserIdentity(site.owner_user_id, site.owner_username)
  if (brand.name) {
    return `${brand.name} · ${host} (${owner})`
  }
  return `${host} (${owner})`
}

/**
 * Dedicated Root panel for subsite margin recharge codes (`purpose = 'site_margin'`).
 * Embedded inside `/site-admin/vouchers` so margin codes are completely separated
 * from main-site user redemption codes.
 */
export function SiteMarginVoucherPanel() {
  const { t } = useTranslation()
  const client = useQueryClient()

  const [selectedSiteId, setSelectedSiteId] = useState('')
  const [amount, setAmount] = useState('')
  const [countRaw, setCountRaw] = useState('1')
  const [reason, setReason] = useState('')
  const intentRef = useRef<{ signature: string; key: string } | null>(null)
  const [reconciled, setReconciled] = useState<VoucherBatchIssued | null>(null)
  const [batchResult, setBatchResult] = useState<NormalizedVoucherBatch | null>(
    null
  )
  const [resultOpen, setResultOpen] = useState(false)

  const [filterSiteId, setFilterSiteId] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [batchNoFilter, setBatchNoFilter] = useState('')
  const [page, setPage] = useState(1)

  const batchSummary = computeVoucherBatchTotal(amount, countRaw)

  const sitesQuery = useQuery({
    queryKey: siteAdminKeys.list('sites-for-margin-vouchers', {
      page_size: SITES_PAGE_SIZE,
    }),
    queryFn: fetchAllSitesForMarginVouchers,
  })

  const allSites = useMemo(
    () => (Array.isArray(sitesQuery.data) ? sitesQuery.data : []),
    [sitesQuery.data]
  )
  const activeSites = useMemo(
    () => allSites.filter((site) => site.status === 'active'),
    [allSites]
  )
  const siteLookup = useMemo(() => {
    const map = new Map<number, AdminSite>()
    for (const site of allSites) {
      map.set(site.id, site)
    }
    return map
  }, [allSites])

  const targetSiteNumericId = Number(selectedSiteId) || 0
  const selectedSite = siteLookup.get(targetSiteNumericId)

  const list = useQuery({
    queryKey: adtyVoucherKeys.adminList({
      purpose: 'site_margin',
      site_id: Number(filterSiteId) || 0,
      status: statusFilter,
      batch_no: batchNoFilter.trim(),
      page,
    }),
    queryFn: () =>
      getAdminVouchersList({
        purpose: 'site_margin',
        site_id: Number(filterSiteId) || undefined,
        status: statusFilter,
        batch_no: batchNoFilter.trim(),
        page,
        page_size: PAGE_SIZE,
      }),
    placeholderData: keepPreviousData,
  })

  const refresh = () => {
    void client.invalidateQueries({ queryKey: adtyVoucherKeys.all })
    void client.invalidateQueries({ queryKey: siteAdminKeys.all })
    void invalidateSidebarBadges(client)
  }

  const issue = useMutation({
    mutationFn: (input: {
      siteId: number
      amountUsd: string
      count: number
      reason: string
      intentKey: string
      targetSiteLabel: string
    }) =>
      issueAdminVoucherBatch({
        purpose: 'site_margin',
        site_id: input.siteId,
        amount_usd: input.amountUsd,
        count: input.count,
        reason: input.reason,
        intent_key: input.intentKey,
      }),
    onSuccess: (issued, input) => {
      const normalized = normalizeVoucherBatchResult(issued, {
        fallbackReason: input.reason,
        targetSiteLabel: input.targetSiteLabel,
      })
      if (issued.replayed || !normalized) {
        setReconciled(issued)
        refresh()
        return
      }
      intentRef.current = null
      setReconciled(null)
      setBatchResult(normalized)
      setResultOpen(true)
      setAmount('')
      setCountRaw('1')
      setReason('')
      toast.success(
        t('Issued {{count}} margin recharge code(s)', {
          count: normalized.count,
        })
      )
      refresh()
    },
    onError: (error) => handleServerError(error),
  })

  const change = useMutation({
    mutationFn: (input: { kind: 'freeze' | 'cancel'; id: number }) =>
      input.kind === 'freeze'
        ? freezeAdminVoucherById(input.id)
        : cancelAdminVoucherById(input.id),
    onSuccess: (_data, input) => {
      if (input.kind === 'cancel' && reconciled?.id === input.id) {
        intentRef.current = null
        setReconciled(null)
      }
      toast.success(
        input.kind === 'freeze' ? t('Code frozen') : t('Code cancelled')
      )
      refresh()
    },
    onError: (error) => handleServerError(error),
  })

  const rows = list.data?.items ?? []
  const replayedCount = reconciled
    ? (reconciled.count ?? reconciled.items?.length ?? 1)
    : 0
  const isBatchReplay = replayedCount > 1

  return (
    <div className='flex flex-col gap-8'>
      <SettingsSection
        title={t('Issue site margin recharge codes')}
        description={t(
          'Only the target active site owner can redeem these codes to credit their site margin wallet. Generate 1 to 100 codes per batch; plaintext codes are shown once.'
        )}
      >
        {sitesQuery.isError && (
          <ErrorState onRetry={() => void sitesQuery.refetch()} />
        )}
        <form
          className='flex max-w-xl flex-col gap-4'
          onSubmit={(event) => {
            event.preventDefault()
            const trimmedAmount = amount.trim()
            const trimmedReason = reason.trim()
            if (
              issue.isPending ||
              reconciled !== null ||
              targetSiteNumericId <= 0 ||
              !batchSummary.valid ||
              trimmedReason === ''
            ) {
              return
            }
            const signature = `site_margin:${targetSiteNumericId}:${trimmedAmount}:${batchSummary.count}:${trimmedReason}`
            if (intentRef.current?.signature !== signature) {
              intentRef.current = {
                signature,
                key: createVoucherBatchIntentKey(),
              }
            }
            issue.mutate({
              siteId: targetSiteNumericId,
              amountUsd: trimmedAmount,
              count: batchSummary.count,
              reason: trimmedReason,
              intentKey: intentRef.current.key,
              targetSiteLabel: selectedSite
                ? formatSiteOptionLabel(selectedSite)
                : `#${targetSiteNumericId}`,
            })
          }}
        >
          <div className='flex max-w-lg flex-col gap-1.5'>
            <Label htmlFor='margin-voucher-site'>{t('Target site')}</Label>
            <NativeSelect
              id='margin-voucher-site'
              className='w-full'
              value={selectedSiteId}
              disabled={sitesQuery.isPending}
              onChange={(event) => {
                setSelectedSiteId(event.target.value)
                setReconciled(null)
              }}
            >
              <NativeSelectOption value=''>
                {sitesQuery.isPending
                  ? t('Loading...')
                  : t('Select an active site')}
              </NativeSelectOption>
              {activeSites.map((site) => (
                <NativeSelectOption key={site.id} value={String(site.id)}>
                  {formatSiteOptionLabel(site)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>

          <div className='flex flex-wrap gap-4'>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor='margin-voucher-amount'>{t('Amount (USD)')}</Label>
              <Input
                id='margin-voucher-amount'
                className='w-40'
                inputMode='decimal'
                placeholder='100.00'
                value={amount}
                onChange={(event) => {
                  setAmount(event.target.value)
                  setReconciled(null)
                }}
              />
            </div>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor='margin-voucher-count'>
                {t('Quantity (1-100)')}
              </Label>
              <Input
                id='margin-voucher-count'
                className='w-32'
                type='number'
                inputMode='numeric'
                min={VOUCHER_BATCH_MIN_COUNT}
                max={VOUCHER_BATCH_MAX_COUNT}
                step={1}
                value={countRaw}
                onChange={(event) => {
                  setCountRaw(event.target.value)
                  setReconciled(null)
                }}
              />
            </div>
          </div>

          <div className='bg-muted/50 flex max-w-lg flex-wrap items-center justify-between gap-2 rounded-lg px-3 py-2 text-xs'>
            <span className='text-dim'>{t('Batch total preview')}</span>
            {batchSummary.valid ? (
              <span className='font-semibold tabular-nums'>
                {t('{{count}} × ${{unit}} = ${{total}} USD', {
                  count: batchSummary.count,
                  unit: batchSummary.unitAmountUsd,
                  total: batchSummary.totalAmountUsd,
                })}
              </span>
            ) : (
              <span className='text-dim'>
                {!batchSummary.validCount
                  ? t('Quantity must be an integer between 1 and 100')
                  : t('Enter a positive USD amount')}
              </span>
            )}
          </div>

          <div className='flex max-w-lg flex-col gap-1.5'>
            <Label htmlFor='margin-voucher-reason'>{t('Reason')}</Label>
            <Input
              id='margin-voucher-reason'
              maxLength={200}
              placeholder={t('e.g. Offline bank transfer margin deposit')}
              value={reason}
              onChange={(event) => {
                setReason(event.target.value)
                setReconciled(null)
              }}
            />
          </div>

          <div className='flex flex-wrap items-center gap-2'>
            <Button
              type='submit'
              variant='brand'
              disabled={
                issue.isPending ||
                reconciled !== null ||
                targetSiteNumericId <= 0 ||
                !batchSummary.valid ||
                reason.trim() === ''
              }
            >
              {batchSummary.count > 1
                ? t('Issue {{count}} margin codes', {
                    count: batchSummary.count,
                  })
                : t('Issue margin recharge code')}
            </Button>
            {batchResult && (
              <Button
                type='button'
                variant='outline'
                onClick={() => setResultOpen(true)}
              >
                {t('View last generated codes')}
              </Button>
            )}
          </div>

          {reconciled && (
            <div
              data-testid='margin-voucher-reconcile-banner'
              className='bg-muted/50 flex flex-col gap-2 rounded-lg p-3 text-sm'
            >
              {isBatchReplay ? (
                <>
                  <p>
                    {reconciled.batch_no
                      ? t(
                          'Previous issuance request already created batch {{batchNo}} ({{count}} codes). Full codes are shown once. Filter by this batch number in the list below to manage or cancel them.',
                          {
                            batchNo: reconciled.batch_no,
                            count: replayedCount,
                          }
                        )
                      : t(
                          'Previous issuance request already created {{count}} codes. Full codes are shown once. Manage or cancel them in the list below.',
                          { count: replayedCount }
                        )}
                  </p>
                  <div className='flex flex-wrap gap-2'>
                    {reconciled.batch_no && (
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        onClick={() => {
                          setBatchNoFilter(reconciled.batch_no ?? '')
                          setPage(1)
                        }}
                      >
                        {t('Filter this batch')}
                      </Button>
                    )}
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      onClick={() => {
                        intentRef.current = null
                        setReconciled(null)
                      }}
                    >
                      {t('Prepare replacement batch')}
                    </Button>
                  </div>
                </>
              ) : (
                <>
                  <p>
                    {t(
                      'Previous issuance request already created code #{{id}} (tail {{tail}}). Full codes are shown once.',
                      {
                        id: reconciled.id,
                        tail: reconciled.code_tail,
                      }
                    )}
                  </p>
                  <div className='flex flex-wrap gap-2'>
                    {reconciled.id > 0 &&
                      (!reconciled.status ||
                        reconciled.status === 'pending' ||
                        reconciled.status === 'frozen') && (
                        <Button
                          type='button'
                          size='sm'
                          variant='outline'
                          disabled={change.isPending}
                          onClick={() =>
                            change.mutate({
                              kind: 'cancel',
                              id: reconciled.id,
                            })
                          }
                        >
                          {t('Cancel code and replace')}
                        </Button>
                      )}
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      onClick={() => {
                        intentRef.current = null
                        setReconciled(null)
                      }}
                    >
                      {t('Prepare replacement code')}
                    </Button>
                  </div>
                </>
              )}
            </div>
          )}
        </form>
      </SettingsSection>

      <ContentSection
        title={t('Margin recharge codes')}
        description={t('Showing only subsite margin recharge codes.')}
        actions={
          <>
            <NativeSelect
              className='max-w-52'
              aria-label={t('Filter by site')}
              value={filterSiteId}
              onChange={(event) => {
                setFilterSiteId(event.target.value)
                setPage(1)
              }}
            >
              <NativeSelectOption value=''>{t('All sites')}</NativeSelectOption>
              {allSites.map((site) => (
                <NativeSelectOption key={site.id} value={String(site.id)}>
                  {formatSiteOptionLabel(site)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            <Input
              aria-label={t('Batch number')}
              placeholder={t('Filter by batch number')}
              className='w-40'
              value={batchNoFilter}
              onChange={(event) => {
                setBatchNoFilter(event.target.value)
                setPage(1)
              }}
            />
            <NativeSelect
              aria-label={t('Status')}
              value={statusFilter}
              onChange={(event) => {
                setStatusFilter(event.target.value)
                setPage(1)
              }}
            >
              <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
              {VOUCHER_STATUSES.map((value) => (
                <NativeSelectOption key={value} value={value}>
                  {voucherStatusLabel(t, value)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={list.isFetching}
              onClick={() => void list.refetch()}
            >
              <RefreshCw
                className={
                  list.isFetching ? 'size-3.5 animate-spin' : 'size-3.5'
                }
                aria-hidden='true'
              />
              {t('Refresh')}
            </Button>
          </>
        }
      >
        {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
        {list.isPending ? (
          <LoadingState />
        ) : (
          <>
            <VoucherListTable
              label={t('Margin recharge codes')}
              rows={rows}
              busy={change.isPending}
              showTargetSite
              formatTargetSite={(siteId) => {
                const found = siteLookup.get(siteId)
                return found ? formatSiteOptionLabel(found) : `#${siteId}`
              }}
              onFreeze={(voucher) =>
                change.mutate({ kind: 'freeze', id: voucher.id })
              }
              onCancel={(voucher) =>
                change.mutate({ kind: 'cancel', id: voucher.id })
              }
            />
            <PageSwitcher
              page={page}
              total={Number(list.data?.total) || 0}
              pageSize={PAGE_SIZE}
              onPageChange={setPage}
            />
          </>
        )}
      </ContentSection>

      <VoucherBatchResultDialog
        batch={batchResult}
        open={resultOpen}
        onOpenChange={setResultOpen}
      />
    </div>
  )
}
