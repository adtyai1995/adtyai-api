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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRightLeft, KeyRound } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { AdtyOnlineTopup } from '@/features/wallet/adty/components/adty-online-topup'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { toIntlLocale } from '@/i18n/languages'
import { handleServerError } from '@/lib/handle-server-error'

import {
  createSelfUseKey,
  getOwnerWallets,
  getSelfUsePrincipal,
  siteOwnerKeys,
  transferOwnerPrincipal,
} from '../api'
import { newIdempotencyKey, ownerTransferPreview } from '../lib/site-owner'
import { OwnerSelfUseHistory } from './owner-self-use-history'

export function OwnerSelfUsePanel(props: {
  returnTradeNo?: string
  onReturnClosed?: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const money = (value: string) => formatUsd(Number(value), locale, 6)
  const client = useQueryClient()
  const principal = useQuery({
    queryKey: siteOwnerKeys.list('self-use', {}),
    queryFn: getSelfUsePrincipal,
  })
  const wallets = useQuery({
    queryKey: siteOwnerKeys.list('wallets', {}),
    queryFn: getOwnerWallets,
  })
  const [amount, setAmount] = useState('')
  const [confirm, setConfirm] = useState(false)
  const [key, setKey] = useState('')
  const [receipt, setReceipt] = useState('')
  const intent = useRef({ amount: '', key: '' })
  const after = ownerTransferPreview(
    wallets.data?.find((w) => w.kind === 'earnings'),
    amount
  )
  const transfer = useMutation({
    mutationFn: () => {
      if (intent.current.amount !== amount) {
        intent.current = { amount, key: newIdempotencyKey() }
      }
      return transferOwnerPrincipal(amount, intent.current.key)
    },
    onSuccess: (data) => {
      intent.current = { amount: '', key: '' }
      setConfirm(false)
      setAmount('')
      setReceipt(data.transfer_id)
      void client.invalidateQueries({ queryKey: siteOwnerKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  const create = useMutation({
    mutationFn: () => createSelfUseKey(t('Site self-use key')),
    onSuccess: setKey,
    onError: (error) => handleServerError(error),
  })
  if (principal.isError || wallets.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void principal.refetch()
          void wallets.refetch()
        }}
      />
    )
  }
  if (principal.isPending || wallets.isPending) return <LoadingState />
  return (
    <section className='flex flex-col gap-3'>
      <h2 className='text-sm font-medium'>{t('Site self-use principal')}</h2>
      <output
        aria-label={t('Site self-use principal')}
        className='tabular-nums'
      >
        {money(principal.data.available_usd)}
      </output>
      <Label htmlFor='owner-principal-amount'>
        {t('Transfer to personal balance')} (USD)
      </Label>
      <Input
        id='owner-principal-amount'
        value={amount}
        inputMode='decimal'
        autoComplete='off'
        disabled={transfer.isPending}
        onChange={(e) => {
          setAmount(e.target.value)
          setReceipt('')
        }}
      />
      <div className='flex flex-wrap gap-2'>
        <Button
          variant='outline'
          disabled={after === null || transfer.isPending}
          onClick={() => setConfirm(true)}
        >
          <ArrowRightLeft aria-hidden='true' />
          {t('Transfer to personal balance')}
        </Button>
        <Button
          variant='outline'
          disabled={create.isPending || !!key}
          onClick={() => create.mutate()}
        >
          <KeyRound aria-hidden='true' />
          {t('Create site self-use key')}
        </Button>
      </div>
      {receipt && (
        <output>
          {t('Transfer ID')}: {receipt}
        </output>
      )}
      {key && (
        <div className='flex flex-wrap items-center gap-2'>
          <code className='break-all'>{key}</code>
          <CopyButton value={key} />
          <Button variant='ghost' onClick={() => setKey('')}>
            {t('Close')}
          </Button>
        </div>
      )}
      <ConfirmDialog
        open={confirm}
        onOpenChange={(open) => {
          if (!transfer.isPending) setConfirm(open)
        }}
        title={t('Transfer to personal balance')}
        desc={
          <div>
            {t('Transfer amount')}: {money(amount)}
            <br />
            {t('Site principal credit')}: {money(amount)}
            <br />
            {t('Earnings after transfer')}: {money(after ?? '0')}
          </div>
        }
        handleConfirm={() => transfer.mutate()}
        isLoading={transfer.isPending}
        disabled={after === null}
      />
      <OwnerSelfUseHistory />
      <AdtyOnlineTopup
        business='owner_self_use_topup'
        returnTradeNo={props.returnTradeNo}
        onReturnClosed={props.onReturnClosed}
        onSettled={() =>
          client.invalidateQueries({ queryKey: siteOwnerKeys.all })
        }
      />
    </section>
  )
}
