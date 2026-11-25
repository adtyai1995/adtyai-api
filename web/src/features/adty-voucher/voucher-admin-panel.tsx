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
import { useRef, useState } from 'react'
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
import { siteAdminKeys } from '@/features/adty-site-admin/api'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { handleServerError } from '@/lib/handle-server-error'

import {
  adtyVoucherKeys,
  cancelAdminVoucherById,
  createVoucherBatchIntentKey,
  freezeAdminVoucherById,
  getAdminVoucherIssuance,
  getAdminVouchersList,
  issueAdminVoucherBatch,
  saveAdminVoucherIssuanceLimit,
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

/**
 * Main-site `/vouchers`: strictly scoped to main-site user redemption codes
 * (`purpose = 'main_user'`). Subsite margin recharge codes live in Reseller
 * Site Admin.
 */
export function VoucherAdminPanel() {
  const { t } = useTranslation()
  const client = useQueryClient()

  const [limit, setLimit] = useState('')
  const [amount, setAmount] = useState('')
  const [countRaw, setCountRaw] = useState('1')
  const [reason, setReason] = useState('')
  const intentRef = useRef<{ signature: string; key: string } | null>(null)
  const [reconciled, setReconciled] = useState<VoucherBatchIssued | null>(null)
  const [batchResult, setBatchResult] = useState<NormalizedVoucherBatch | null>(
    null
  )
  const [resultOpen, setResultOpen] = useState(false)

  const [statusFilter, setStatusFilter] = useState('')
  const [batchNoFilter, setBatchNoFilter] = useState('')
  const [page, setPage] = useState(1)

  const batchSummary = computeVoucherBatchTotal(amount, countRaw)

  const issuance = useQuery({
    queryKey: adtyVoucherKeys.adminIssuance,
    queryFn: getAdminVoucherIssuance,
  })

  const list = useQuery({
    queryKey: adtyVoucherKeys.adminList({
      purpose: 'main_user',
      status: statusFilter,
      batch_no: batchNoFilter.trim(),
      page,
    }),
    queryFn: () =>
      getAdminVouchersList({
        purpose: 'main_user',
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
  }

  const saveLimit = useMutation({
    mutationFn: () => saveAdminVoucherIssuanceLimit(limit.trim()),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      setLimit('')
      refresh()
    },
    onError: (error) => handleServerError(error),
  })

  const issue = useMutation({
    mutationFn: (input: {
      amountUsd: string
      count: number
      reason: string
      intentKey: string
    }) =>
      issueAdminVoucherBatch({
        purpose: 'main_user',
        amount_usd: input.amountUsd,
        count: input.count,
        reason: input.reason,
        intent_key: input.intentKey,
      }),
    onSuccess: (issued, input) => {
      const normalized = normalizeVoucherBatchResult(issued, {
        fallbackReason: input.reason,
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
        t('Issued {{count}} redemption code(s)', { count: normalized.count })
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
    <div className='flex flex-col gap-5'>
      <div className='grid gap-5 lg:grid-cols-2'>
        <Card>
          <CardHeader>
            <CardTitle>{t('Main-site issuance limit')}</CardTitle>
            <CardDescription>
              {t(
                'Main-site user codes and site margin codes are both counted against this limit. Codes cannot be issued until it is set.'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent className='flex flex-col gap-4'>
            {issuance.isPending && <LoadingState />}
            {issuance.isError && (
              <ErrorState onRetry={() => void issuance.refetch()} />
            )}
            {issuance.data && (
              <dl className='grid grid-cols-3 gap-3 text-sm'>
                <div className='bg-muted/40 border-border rounded-lg border p-3'>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Limit')}
                  </dt>
                  <dd className='mt-1 font-semibold tabular-nums'>
                    ${issuance.data.limit_usd}
                  </dd>
                </div>
                <div className='bg-muted/40 border-border rounded-lg border p-3'>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Held by unredeemed codes')}
                  </dt>
                  <dd className='mt-1 font-semibold tabular-nums'>
                    ${issuance.data.frozen_usd}
                  </dd>
                </div>
                <div className='bg-muted/40 border-border rounded-lg border p-3'>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Redeemed')}
                  </dt>
                  <dd className='mt-1 font-semibold tabular-nums'>
                    ${issuance.data.spent_usd}
                  </dd>
                </div>
              </dl>
            )}
            <form
              className='flex flex-wrap items-end gap-2'
              onSubmit={(event) => {
                event.preventDefault()
                if (limit.trim() === '' || saveLimit.isPending) return
                saveLimit.mutate()
              }}
            >
              <div className='flex min-w-[180px] flex-1 flex-col gap-1.5'>
                <Label htmlFor='voucher-limit'>{t('New limit (USD)')}</Label>
                <Input
                  id='voucher-limit'
                  inputMode='decimal'
                  placeholder='1000.00'
                  value={limit}
                  onChange={(event) => setLimit(event.target.value)}
                />
              </div>
              <Button
                type='submit'
                variant='outline'
                disabled={saveLimit.isPending || limit.trim() === ''}
              >
                {t('Save')}
              </Button>
            </form>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t('Issue main-site user redemption codes')}</CardTitle>
            <CardDescription>
              {t(
                'Only main-site users can redeem these codes in their wallet. Generate 1 to 100 codes per batch; plaintext codes are shown once.'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className='flex flex-col gap-3.5'
              onSubmit={(event) => {
                event.preventDefault()
                const trimmedAmount = amount.trim()
                const trimmedReason = reason.trim()
                if (
                  issue.isPending ||
                  reconciled !== null ||
                  !batchSummary.valid ||
                  trimmedReason === ''
                ) {
                  return
                }
                const signature = `main_user:0:${trimmedAmount}:${batchSummary.count}:${trimmedReason}`
                if (intentRef.current?.signature !== signature) {
                  intentRef.current = {
                    signature,
                    key: createVoucherBatchIntentKey(),
                  }
                }
                issue.mutate({
                  amountUsd: trimmedAmount,
                  count: batchSummary.count,
                  reason: trimmedReason,
                  intentKey: intentRef.current.key,
                })
              }}
            >
              <div className='grid gap-3 sm:grid-cols-2'>
                <div className='flex flex-col gap-1.5'>
                  <Label htmlFor='main-voucher-amount'>
                    {t('Amount (USD)')}
                  </Label>
                  <Input
                    id='main-voucher-amount'
                    inputMode='decimal'
                    placeholder='10.00'
                    value={amount}
                    onChange={(event) => {
                      setAmount(event.target.value)
                      setReconciled(null)
                    }}
                  />
                </div>
                <div className='flex flex-col gap-1.5'>
                  <Label htmlFor='main-voucher-count'>
                    {t('Quantity (1-100)')}
                  </Label>
                  <Input
                    id='main-voucher-count'
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
                <Label htmlFor='main-voucher-reason'>{t('Reason')}</Label>
                <Input
                  id='main-voucher-reason'
                  placeholder={t('e.g. Activity / Compensation')}
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
                    !batchSummary.valid ||
                    reason.trim() === ''
                  }
                >
                  {batchSummary.count > 1
                    ? t('Issue {{count}} codes', { count: batchSummary.count })
                    : t('Issue code')}
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
                  data-testid='voucher-reconcile-banner'
                  className='border-border bg-muted/40 flex flex-col gap-2 rounded-md border p-3 text-sm'
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
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-3 space-y-0'>
          <div className='flex flex-col gap-1'>
            <CardTitle>{t('Main-site user redemption codes')}</CardTitle>
            <CardDescription>
              {t('Showing only main-site user redemption codes.')}
            </CardDescription>
          </div>
          <div className='flex flex-wrap items-center gap-2'>
            <Input
              aria-label={t('Batch number')}
              placeholder={t('Filter by batch number')}
              className='w-44'
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
                label={t('Main-site user redemption codes')}
                rows={rows}
                busy={change.isPending}
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
