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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
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
  const [intentKey, setIntentKey] = useState(() =>
    createVoucherBatchIntentKey()
  )
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
      page_size: 200,
    }),
    queryFn: () => getSites({ p: 1, page_size: 200 }),
  })

  const allSites = useMemo(
    () => (Array.isArray(sitesQuery.data?.items) ? sitesQuery.data.items : []),
    [sitesQuery.data?.items]
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
    mutationFn: () =>
      issueAdminVoucherBatch({
        purpose: 'site_margin',
        site_id: targetSiteNumericId,
        amount_usd: amount.trim(),
        count: batchSummary.count,
        reason: reason.trim(),
        intent_key: intentKey,
      }),
    onSuccess: (issued) => {
      const normalized = normalizeVoucherBatchResult(issued, {
        fallbackReason: reason.trim(),
        targetSiteLabel: selectedSite
          ? formatSiteOptionLabel(selectedSite)
          : `#${targetSiteNumericId}`,
      })
      if (issued.replayed || !normalized) {
        setReconciled(issued)
        refresh()
        return
      }
      setReconciled(null)
      setBatchResult(normalized)
      setResultOpen(true)
      setAmount('')
      setCountRaw('1')
      setReason('')
      setIntentKey(createVoucherBatchIntentKey())
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
        setReconciled(null)
        setIntentKey(createVoucherBatchIntentKey())
      }
      toast.success(
        input.kind === 'freeze' ? t('Code frozen') : t('Code cancelled')
      )
      refresh()
    },
    onError: (error) => handleServerError(error),
  })

  const rows = list.data?.items ?? []

  return (
    <div className='flex flex-col gap-5'>
      <Card>
        <CardHeader>
          <CardTitle>{t('Issue site margin recharge codes')}</CardTitle>
          <CardDescription>
            {t(
              'Only the target active site owner can redeem these codes to credit their site margin wallet. Generate 1 to 100 codes per batch; plaintext codes are shown once.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {sitesQuery.isError && (
            <ErrorState onRetry={() => void sitesQuery.refetch()} />
          )}
          <form
            className='flex max-w-2xl flex-col gap-3.5'
            onSubmit={(event) => {
              event.preventDefault()
              if (
                issue.isPending ||
                reconciled !== null ||
                targetSiteNumericId <= 0 ||
                !batchSummary.valid ||
                reason.trim() === ''
              ) {
                return
              }
              issue.mutate()
            }}
          >
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor='margin-voucher-site'>{t('Target site')}</Label>
              <NativeSelect
                id='margin-voucher-site'
                className='w-full'
                value={selectedSiteId}
                disabled={sitesQuery.isPending}
                onChange={(event) => setSelectedSiteId(event.target.value)}
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

            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='margin-voucher-amount'>
                  {t('Amount (USD)')}
                </Label>
                <Input
                  id='margin-voucher-amount'
                  inputMode='decimal'
                  placeholder='100.00'
                  value={amount}
                  onChange={(event) => setAmount(event.target.value)}
                />
              </div>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='margin-voucher-count'>
                  {t('Quantity (1-100)')}
                </Label>
                <Input
                  id='margin-voucher-count'
                  type='number'
                  inputMode='numeric'
                  min={VOUCHER_BATCH_MIN_COUNT}
                  max={VOUCHER_BATCH_MAX_COUNT}
                  step={1}
                  value={countRaw}
                  onChange={(event) => setCountRaw(event.target.value)}
                />
              </div>
            </div>

            <div className='bg-muted/40 border-border flex flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-2 text-xs'>
              <span className='text-muted-foreground'>
                {t('Batch total preview')}
              </span>
              {batchSummary.valid ? (
                <span className='font-semibold tabular-nums'>
                  {t('{{count}} × ${{unit}} = ${{total}} USD', {
                    count: batchSummary.count,
                    unit: batchSummary.unitAmountUsd,
                    total: batchSummary.totalAmountUsd,
                  })}
                </span>
              ) : (
                <span className='text-muted-foreground'>
                  {!batchSummary.validCount
                    ? t('Quantity must be an integer between 1 and 100')
                    : t('Enter a positive USD amount')}
                </span>
              )}
            </div>

            <div className='flex flex-col gap-1.5'>
              <Label htmlFor='margin-voucher-reason'>{t('Reason')}</Label>
              <Input
                id='margin-voucher-reason'
                placeholder={t('e.g. Offline bank transfer margin deposit')}
                value={reason}
                onChange={(event) => setReason(event.target.value)}
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
                className='border-border bg-muted/40 flex flex-col gap-2 rounded-md border p-3 text-sm'
              >
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
                          change.mutate({ kind: 'cancel', id: reconciled.id })
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
                      setReconciled(null)
                      setIntentKey(createVoucherBatchIntentKey())
                    }}
                  >
                    {t('Prepare replacement code')}
                  </Button>
                </div>
              </div>
            )}
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-3 space-y-0'>
          <div className='flex flex-col gap-1'>
            <CardTitle>{t('Margin recharge codes')}</CardTitle>
            <CardDescription>
              {t('Showing only subsite margin recharge codes.')}
            </CardDescription>
          </div>
          <div className='flex flex-wrap items-center gap-2'>
            <NativeSelect
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
          </div>
        </CardHeader>
        <CardContent className='flex flex-col gap-4'>
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
        </CardContent>
      </Card>

      <VoucherBatchResultDialog
        batch={batchResult}
        open={resultOpen}
        onOpenChange={setResultOpen}
      />
    </div>
  )
}
