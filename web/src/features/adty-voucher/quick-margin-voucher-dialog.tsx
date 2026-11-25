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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { siteAdminKeys } from '@/features/adty-site-admin/api'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'

import {
  adtyVoucherKeys,
  cancelAdminVoucherById,
  createVoucherBatchIntentKey,
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

/**
 * Quick-issue modal pre-bound to a single active subsite inside Site Detail ->
 * Level & Wallets (`site-detail-page.tsx`). Supports batch quantity 1–100 and
 * opens `VoucherBatchResultDialog` on success.
 */
export function QuickMarginVoucherDialog(props: {
  siteId: number
  siteLabel: string
  ownerLabel: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()

  const [amount, setAmount] = useState('')
  const [countRaw, setCountRaw] = useState('1')
  const [reason, setReason] = useState('')
  const intentRef = useRef<{ signature: string; key: string } | null>(null)
  const [reconciled, setReconciled] = useState<VoucherBatchIssued | null>(null)
  const [batchResult, setBatchResult] = useState<NormalizedVoucherBatch | null>(
    null
  )
  const [resultOpen, setResultOpen] = useState(false)

  useEffect(() => {
    setReconciled(null)
  }, [props.siteId])

  const batchSummary = computeVoucherBatchTotal(amount, countRaw)

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
      setAmount('')
      setCountRaw('1')
      setReason('')
      props.onOpenChange(false)
      setBatchResult(normalized)
      setResultOpen(true)
      toast.success(
        t('Issued {{count}} margin recharge code(s)', {
          count: normalized.count,
        })
      )
      refresh()
    },
    onError: (error) => handleServerError(error),
  })

  const cancelReconciled = useMutation({
    mutationFn: (id: number) => cancelAdminVoucherById(id),
    onSuccess: () => {
      intentRef.current = null
      setReconciled(null)
      toast.success(t('Code cancelled'))
      refresh()
    },
    onError: (error) => handleServerError(error),
  })

  const handleSubmit = () => {
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
    const signature = `site_margin:${props.siteId}:${trimmedAmount}:${batchSummary.count}:${trimmedReason}`
    if (intentRef.current?.signature !== signature) {
      intentRef.current = {
        signature,
        key: createVoucherBatchIntentKey(),
      }
    }
    issue.mutate({
      siteId: props.siteId,
      amountUsd: trimmedAmount,
      count: batchSummary.count,
      reason: trimmedReason,
      intentKey: intentRef.current.key,
      targetSiteLabel: `${props.siteLabel} (${props.ownerLabel})`,
    })
  }

  const replayedCount = reconciled
    ? (reconciled.count ?? reconciled.items?.length ?? 1)
    : 0
  const isBatchReplay = replayedCount > 1

  return (
    <>
      <Dialog
        open={props.open}
        onOpenChange={(nextOpen) => {
          if (!issue.isPending) props.onOpenChange(nextOpen)
        }}
        title={t('Issue margin recharge code')}
        description={t(
          'Pre-bound to {{site}} ({{owner}}). Only this site owner can redeem these codes into the margin wallet.',
          { site: props.siteLabel, owner: props.ownerLabel }
        )}
        contentClassName='sm:max-w-lg'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              disabled={issue.isPending}
              onClick={() => props.onOpenChange(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              variant='brand'
              disabled={
                issue.isPending ||
                reconciled !== null ||
                !batchSummary.valid ||
                reason.trim() === ''
              }
              onClick={handleSubmit}
            >
              {batchSummary.count > 1
                ? t('Issue {{count}} margin codes', {
                    count: batchSummary.count,
                  })
                : t('Issue margin recharge code')}
            </Button>
          </>
        }
      >
        <div className='flex flex-col gap-3.5'>
          <div className='bg-muted/40 border-border rounded-lg border px-3 py-2 text-xs'>
            <span className='text-muted-foreground'>{t('Target site')}: </span>
            <span className='font-medium'>
              {props.siteLabel} ({props.ownerLabel})
            </span>
          </div>

          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor='quick-margin-amount'>{t('Amount (USD)')}</Label>
              <Input
                id='quick-margin-amount'
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
              <Label htmlFor='quick-margin-count'>
                {t('Quantity (1-100)')}
              </Label>
              <Input
                id='quick-margin-count'
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
            <Label htmlFor='quick-margin-reason'>{t('Reason')}</Label>
            <Input
              id='quick-margin-reason'
              placeholder={t('e.g. Offline bank transfer margin deposit')}
              value={reason}
              onChange={(event) => {
                setReason(event.target.value)
                setReconciled(null)
              }}
            />
          </div>

          {reconciled && (
            <div
              data-testid='quick-margin-reconcile-banner'
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
                          disabled={cancelReconciled.isPending}
                          onClick={() => cancelReconciled.mutate(reconciled.id)}
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
        </div>
      </Dialog>

      <VoucherBatchResultDialog
        batch={batchResult}
        open={resultOpen}
        onOpenChange={setResultOpen}
      />
    </>
  )
}
