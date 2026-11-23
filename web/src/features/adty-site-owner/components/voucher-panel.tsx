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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { VoucherTable } from '@/features/adty-ledger/voucher-table'
import { PageSwitcher } from '@/features/referral/components/page-switcher'

import {
  cancelOwnerVoucher,
  freezeOwnerVoucher,
  getOwnerVouchers,
  getOwnerWallets,
  issueOwnerVoucher,
  siteOwnerKeys,
} from '../api'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import { computeOwnerFundingPreview } from '../lib/funding-preview'
import { newIdempotencyKey } from '../lib/site-owner'
import { OwnerFundingPreviewBox } from './owner-funding-preview'

const PAGE_SIZE = 20

/**
 * Recharge codes funded automatically from site earnings first and excess
 * margin above the level deposit floor second. Every write asks the owner to
 * verify first.
 */
export function VoucherPanel(props: { siteId: number }) {
  const { t } = useTranslation()
  const [purpose, setPurpose] = useState('site_customer')
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [code, setCode] = useState('')
  const [issuedSplit, setIssuedSplit] = useState<{
    earnings: string
    margin: string
  } | null>(null)
  const [reconciled, setReconciled] = useState<{
    id: number
    tail: string
    amountUsd: string
    status?: string
  } | null>(null)
  const [page, setPage] = useState(1)
  const intentRef = useRef<{ payload: string; key: string } | null>(null)
  const list = useQuery({
    queryKey: siteOwnerKeys.list('vouchers', { page }),
    queryFn: () => getOwnerVouchers(page, PAGE_SIZE),
    placeholderData: keepPreviousData,
  })
  const wallets = useQuery({
    queryKey: siteOwnerKeys.list('wallets', {}),
    queryFn: getOwnerWallets,
  })
  const walletRows = Array.isArray(wallets.data) ? wallets.data : []
  const preview = computeOwnerFundingPreview(walletRows, amount)
  const issue = useVerifiedOwnerMutation<{
    purpose: string
    amount: string
    reason: string
    intentKey: string
  }>({
    action: 'voucher.issue',
    target: () => props.siteId,
    run: async (vars, proof) => {
      const issued = await issueOwnerVoucher(
        {
          purpose: vars.purpose,
          amount_usd: vars.amount,
          reason: vars.reason,
          intent_key: vars.intentKey,
        },
        proof
      )
      if (issued.replayed || !issued.code) {
        setCode('')
        setIssuedSplit(null)
        setReconciled({
          id: issued.id,
          tail: issued.code_tail,
          amountUsd: issued.amount_usd,
          status: issued.status,
        })
        return
      }
      setReconciled(null)
      setCode(issued.code)
      setIssuedSplit({
        earnings: issued.frozen_earnings_usd ?? '0',
        margin: issued.frozen_margin_usd ?? '0',
      })
      intentRef.current = null
      setAmount('')
      setReason('')
    },
    successMessage: t('Code issued'),
  })
  const freeze = useVerifiedOwnerMutation<number>({
    action: 'voucher.freeze',
    target: (id) => id,
    run: (id, proof) => freezeOwnerVoucher(id, proof),
    successMessage: t('Code frozen'),
  })
  const cancel = useVerifiedOwnerMutation<number>({
    action: 'voucher.cancel',
    target: (id) => id,
    run: (id, proof) => cancelOwnerVoucher(id, proof),
    successMessage: t('Code cancelled'),
  })
  const cancelAndReplace = useVerifiedOwnerMutation<number>({
    action: 'voucher.cancel',
    target: (id) => id,
    run: (id, proof) => cancelOwnerVoucher(id, proof),
    successMessage: t('Code cancelled'),
    onSuccess: () => {
      intentRef.current = null
      setReconciled(null)
    },
  })
  const rows = Array.isArray(list.data?.items) ? list.data.items : []

  return (
    <div className='flex flex-col gap-6'>
      <form
        className='flex max-w-xl flex-col gap-3'
        onSubmit={(event) => {
          event.preventDefault()
          setCode('')
          setIssuedSplit(null)
          setReconciled(null)
          const trimmedAmount = amount.trim()
          const trimmedReason = reason.trim()
          const payload = `${purpose}:${trimmedAmount}:${trimmedReason}`
          if (intentRef.current?.payload !== payload) {
            intentRef.current = { payload, key: newIdempotencyKey() }
          }
          issue.mutate({
            purpose,
            amount: trimmedAmount,
            reason: trimmedReason,
            intentKey: intentRef.current.key,
          })
        }}
      >
        <div className='flex flex-col gap-1'>
          <h2 className='text-sm font-medium'>{t('Recharge codes')}</h2>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Issuing a code automatically freezes available earnings first and excess margin above the level floor second. The full code is shown once.'
            )}
          </p>
        </div>
        <div className='flex flex-col gap-1'>
          <Label htmlFor='voucher-purpose'>{t('Purpose')}</Label>
          <NativeSelect
            id='voucher-purpose'
            value={purpose}
            onChange={(event) => setPurpose(event.target.value)}
          >
            <option value='site_customer'>{t('Customer principal')}</option>
            <option value='site_owner'>{t('My spending principal')}</option>
          </NativeSelect>
        </div>
        <div className='flex flex-col gap-1'>
          <Label htmlFor='voucher-amount'>{t('Amount (USD)')}</Label>
          <Input
            id='voucher-amount'
            inputMode='decimal'
            value={amount}
            onChange={(event) => setAmount(event.target.value)}
          />
        </div>
        <OwnerFundingPreviewBox wallets={walletRows} amount={amount} />
        <div className='flex flex-col gap-1'>
          <Label htmlFor='voucher-reason'>{t('Reason')}</Label>
          <Input
            id='voucher-reason'
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </div>
        <Button
          variant='brand'
          type='submit'
          disabled={
            issue.isPending ||
            !preview.valid ||
            (wallets.isSuccess && !preview.sufficient) ||
            reason.trim() === ''
          }
        >
          {t('Issue code')}
        </Button>
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
        {code && (
          <div className='flex flex-col gap-1 text-sm'>
            <div className='flex items-center gap-2'>
              <span>{t('Voucher code')}:</span>
              <code className='font-mono break-all'>{code}</code>
              <CopyButton
                value={code}
                size='icon'
                aria-label={t('Copy voucher code')}
              />
            </div>
            {issuedSplit && (
              <p className='text-muted-foreground text-xs tabular-nums'>
                {t(
                  'Frozen split: Earnings ${{earnings}} + Excess margin ${{margin}}',
                  {
                    earnings: issuedSplit.earnings,
                    margin: issuedSplit.margin,
                  }
                )}
              </p>
            )}
          </div>
        )}
      </form>

      <section className='flex flex-col gap-3'>
        <h2 className='text-sm font-medium'>{t('Issued codes')}</h2>
        {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
        {list.isPending ? (
          <LoadingState />
        ) : (
          <>
            <VoucherTable
              label={t('Issued codes')}
              rows={rows}
              busy={freeze.isPending || cancel.isPending}
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
      </section>
    </div>
  )
}
