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
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

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
import {
  adtyVoucherKeys,
  cancelOwnerVoucherById,
  freezeOwnerVoucherById,
  getOwnerVouchersList,
  issueOwnerVoucherBatch,
} from '@/features/adty-voucher/api'
import {
  computeVoucherBatchTotal,
  normalizeVoucherBatchResult,
  VOUCHER_BATCH_MAX_COUNT,
  VOUCHER_BATCH_MIN_COUNT,
  type NormalizedVoucherBatch,
} from '@/features/adty-voucher/export'
import { VoucherBatchResultDialog } from '@/features/adty-voucher/voucher-batch-result-dialog'
import { VoucherListTable } from '@/features/adty-voucher/voucher-list-table'
import { PageSwitcher } from '@/features/referral/components/page-switcher'

import { getOwnerWallets, siteOwnerKeys } from '../api'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import { computeOwnerFundingPreview } from '../lib/funding-preview'
import { newIdempotencyKey } from '../lib/site-owner'
import { OwnerFundingPreviewBox } from './owner-funding-preview'

const PAGE_SIZE = 20

/**
 * Subsite owner customer redemption codes (`purpose = 'site_customer'`).
 * Funded automatically from available earnings first and excess margin above
 * the level deposit floor second. Every write asks the owner to verify first.
 */
export function VoucherPanel(props: { siteId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [amount, setAmount] = useState('')
  const [countRaw, setCountRaw] = useState('1')
  const [reason, setReason] = useState('')
  const [batchResult, setBatchResult] = useState<NormalizedVoucherBatch | null>(
    null
  )
  const [resultOpen, setResultOpen] = useState(false)
  const [reconciled, setReconciled] = useState<{
    id: number
    tail: string
    amountUsd: string
    status?: string
  } | null>(null)

  const [statusFilter, setStatusFilter] = useState('')
  const [batchNoFilter, setBatchNoFilter] = useState('')
  const [page, setPage] = useState(1)
  const intentRef = useRef<{ payload: string; key: string } | null>(null)

  const batchSummary = computeVoucherBatchTotal(amount, countRaw)

  const list = useQuery({
    queryKey: adtyVoucherKeys.ownerList({
      siteId: props.siteId,
      status: statusFilter,
      batch_no: batchNoFilter.trim(),
      page,
    }),
    queryFn: () =>
      getOwnerVouchersList({
        status: statusFilter,
        batch_no: batchNoFilter.trim(),
        page,
        page_size: PAGE_SIZE,
      }),
    placeholderData: keepPreviousData,
  })

  const wallets = useQuery({
    queryKey: siteOwnerKeys.list('wallets', {}),
    queryFn: getOwnerWallets,
  })

  const walletRows = Array.isArray(wallets.data) ? wallets.data : []
  const previewAmount = batchSummary.valid ? batchSummary.totalAmountUsd : amount
  const preview = computeOwnerFundingPreview(walletRows, previewAmount)

  const invalidateVoucherQueries = () => {
    void queryClient.invalidateQueries({ queryKey: adtyVoucherKeys.all })
    void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
  }

  const issue = useVerifiedOwnerMutation<{
    amount: string
    count: number
    reason: string
    intentKey: string
  }>({
    action: 'voucher.issue',
    target: () => props.siteId,
    run: async (vars, proof) => {
      const issued = await issueOwnerVoucherBatch(
        {
          purpose: 'site_customer',
          amount_usd: vars.amount,
          count: vars.count,
          reason: vars.reason,
          intent_key: vars.intentKey,
        },
        proof
      )
      const normalized = normalizeVoucherBatchResult(issued, {
        fallbackReason: vars.reason,
      })
      if (issued.replayed || !normalized) {
        setReconciled({
          id: issued.id,
          tail: issued.code_tail,
          amountUsd: issued.amount_usd,
          status: issued.status,
        })
        invalidateVoucherQueries()
        return
      }
      setReconciled(null)
      setBatchResult(normalized)
      setResultOpen(true)
      intentRef.current = null
      setAmount('')
      setCountRaw('1')
      setReason('')
      invalidateVoucherQueries()
    },
    successMessage: t('Code issued'),
  })

  const freeze = useVerifiedOwnerMutation<number>({
    action: 'voucher.freeze',
    target: (id) => id,
    run: async (id, proof) => {
      await freezeOwnerVoucherById(id, proof)
      invalidateVoucherQueries()
    },
    successMessage: t('Code frozen'),
  })

  const cancel = useVerifiedOwnerMutation<number>({
    action: 'voucher.cancel',
    target: (id) => id,
    run: async (id, proof) => {
      await cancelOwnerVoucherById(id, proof)
      invalidateVoucherQueries()
    },
    successMessage: t('Code cancelled'),
  })

  const cancelAndReplace = useVerifiedOwnerMutation<number>({
    action: 'voucher.cancel',
    target: (id) => id,
    run: async (id, proof) => {
      await cancelOwnerVoucherById(id, proof)
      invalidateVoucherQueries()
    },
    successMessage: t('Code cancelled'),
    onSuccess: () => {
      intentRef.current = null
      setReconciled(null)
    },
  })

  const rows = list.data?.items ?? []

  return (
    <div className='flex flex-col gap-5'>
      <Card>
        <CardHeader>
          <CardTitle>{t('Customer redemption codes')}</CardTitle>
          <CardDescription>
            {t(
              'Codes issued here are only for your site customers to redeem in Wallet. Funds are automatically frozen from available earnings first and excess margin above the level floor second. Generate 1 to 100 codes per batch; plaintext codes are shown once.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className='flex max-w-2xl flex-col gap-3.5'
            onSubmit={(event) => {
              event.preventDefault()
              if (
                issue.isPending ||
                !batchSummary.valid ||
                !preview.valid ||
                (wallets.isSuccess && !preview.sufficient) ||
                reason.trim() === ''
              ) {
                return
              }
              setReconciled(null)
              const trimmedAmount = amount.trim()
              const trimmedReason = reason.trim()
              const payload = `site_customer:${trimmedAmount}:${batchSummary.count}:${trimmedReason}`
              if (intentRef.current?.payload !== payload) {
                intentRef.current = { payload, key: newIdempotencyKey() }
              }
              issue.mutate({
                amount: trimmedAmount,
                count: batchSummary.count,
                reason: trimmedReason,
                intentKey: intentRef.current.key,
              })
            }}
          >
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='voucher-amount'>{t('Amount (USD)')}</Label>
                <Input
                  id='voucher-amount'
                  inputMode='decimal'
                  placeholder='10.00'
                  value={amount}
                  onChange={(event) => setAmount(event.target.value)}
                />
              </div>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='voucher-count'>{t('Quantity (1-100)')}</Label>
                <Input
                  id='voucher-count'
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

            <OwnerFundingPreviewBox
              wallets={walletRows}
              amount={previewAmount}
            />

            <div className='flex flex-col gap-1.5'>
              <Label htmlFor='voucher-reason'>{t('Reason')}</Label>
              <Input
                id='voucher-reason'
                placeholder={t('e.g. Customer campaign / offline top-up')}
                value={reason}
                onChange={(event) => setReason(event.target.value)}
              />
            </div>

            <div className='flex flex-wrap items-center gap-2'>
              <Button
                variant='brand'
                type='submit'
                disabled={
                  issue.isPending ||
                  !batchSummary.valid ||
                  !preview.valid ||
                  (wallets.isSuccess && !preview.sufficient) ||
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
                role='status'
                className='border-border bg-muted/40 flex flex-col gap-2 rounded-md border p-3 text-sm'
              >
                <p>
                  {t(
                    'Previous issuance request already created code #{{id}} (tail {{tail}}, {{amount}} USD). Full codes are shown once.',
                    {
                      id: reconciled.id,
                      tail: reconciled.tail,
                      amount: reconciled.amountUsd,
                    }
                  )}
                </p>
                <div className='flex flex-wrap gap-2'>
                  {(!reconciled.status ||
                    reconciled.status === 'pending' ||
                    reconciled.status === 'frozen') && (
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      disabled={cancelAndReplace.isPending}
                      onClick={() => cancelAndReplace.mutate(reconciled.id)}
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
              </div>
            )}
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-3 space-y-0'>
          <div className='flex flex-col gap-1'>
            <CardTitle>{t('Issued codes')}</CardTitle>
            <CardDescription>
              {t('Manage issued customer redemption codes and funding status.')}
            </CardDescription>
          </div>
          <div className='flex flex-wrap items-center gap-2'>
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
                label={t('Issued codes')}
                rows={rows}
                busy={freeze.isPending || cancel.isPending}
                showSource
                onFreeze={(voucher) => freeze.mutate(voucher.id)}
                onCancel={(voucher) => cancel.mutate(voucher.id)}
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
