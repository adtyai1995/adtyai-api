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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import type { OwnerCustomer } from '@/features/adty-site-owner/types'
import { handleServerError } from '@/lib/handle-server-error'

import {
  createStaffBalanceRequest,
  type StaffBalanceDirection,
} from './api'

const notice =
  'This request does not change the customer balance until the site owner or Root approves it.'

export function StaffBalanceRequestDialog(props: {
  customer: OwnerCustomer
  userId: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [direction, setDirection] = useState<StaffBalanceDirection | ''>('')
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [orderRef, setOrderRef] = useState('')
  const [fundingSource, setFundingSource] = useState('')
  const [batchKind, setBatchKind] = useState('')
  const [batchSource, setBatchSource] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const [idempotencyKey] = useState(() => crypto.randomUUID())
  const submit = useMutation({
    retry: false,
    mutationFn: () => {
      if (direction !== 'increase' && direction !== 'decrease') {
        throw new Error(t('Invalid parameters'))
      }
      return createStaffBalanceRequest({
        customer_id: props.customer.id,
        direction,
        amount_usd: amount.trim(),
        idempotency_key: idempotencyKey,
        reason: reason.trim(),
        order_ref: orderRef.trim(),
        funding_source: direction === 'decrease' ? 'specified_batch' : fundingSource,
        batch_kind: direction === 'decrease' ? batchKind : '',
        batch_source: direction === 'decrease' ? batchSource.trim() : '',
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({
        queryKey: ['adty-staff', props.userId, 'balance-requests'],
      })
      void invalidateSidebarBadges(client)
      props.onClose()
    },
    onError: (error) => handleServerError(error),
  })
  const ready =
    (direction === 'increase' || direction === 'decrease') &&
    amount.trim() !== '' &&
    Number.isFinite(Number(amount)) && Number(amount) > 0 &&
    reason.trim() !== '' && [...reason.trim()].length <= 200 &&
    orderRef.trim() !== '' && orderRef.trim().length <= 64 &&
    (direction === 'increase' ? fundingSource !== '' : batchKind !== '' && batchSource.trim() !== '') &&
    confirmed &&
    !submit.isPending
  return (
    <Dialog
      open
      title={t('Request balance change')}
      description={t(notice)}
      footer={
        <Button type='button' disabled={!ready} onClick={() => submit.mutate()}>
          {t('Request balance change')}
        </Button>
      }
      onOpenChange={(open) => {
        if (!open && !submit.isPending) props.onClose()
      }}
    >
        <div className='flex flex-col gap-3'>
          <p className='text-sm break-all'>{props.customer.username}</p>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='staff-balance-direction'>{t('Direction')}</Label>
            <NativeSelect
              id='staff-balance-direction'
              className='w-full'
              value={direction}
              onChange={(event) => {
                setDirection(event.target.value as StaffBalanceDirection | '')
                setFundingSource('')
                setBatchKind('')
                setBatchSource('')
              }}
            >
              <NativeSelectOption value=''>{t('Direction')}</NativeSelectOption>
              <NativeSelectOption value='increase'>{t('Increase')}</NativeSelectOption>
              <NativeSelectOption value='decrease'>{t('Decrease')}</NativeSelectOption>
            </NativeSelect>
          </div>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='staff-balance-amount'>{t('Amount')}</Label>
            <Input
              id='staff-balance-amount'
              value={amount}
              inputMode='decimal'
              autoComplete='off'
              onChange={(event) => setAmount(event.target.value)}
            />
          </div>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='staff-balance-order'>{t('Original order')}</Label>
            <Input id='staff-balance-order' value={orderRef} maxLength={64}
              onChange={(event) => setOrderRef(event.target.value)} />
          </div>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='staff-balance-reason'>{t('Reason')}</Label>
            <Textarea id='staff-balance-reason' value={reason} maxLength={200}
              onChange={(event) => setReason(event.target.value)} />
          </div>
          {direction === 'increase' && (
            <div className='flex flex-col gap-1'>
              <Label htmlFor='staff-balance-source'>{t('Funding source')}</Label>
              <NativeSelect id='staff-balance-source' className='w-full' value={fundingSource}
                onChange={(event) => setFundingSource(event.target.value)}>
                <NativeSelectOption value=''>{t('Funding source')}</NativeSelectOption>
                <NativeSelectOption value='site_earnings'>{t('Earnings wallet')}</NativeSelectOption>
                <NativeSelectOption value='site_margin'>{t('Margin wallet')}</NativeSelectOption>
              </NativeSelect>
            </div>
          )}
          {direction === 'decrease' && (
            <>
              <div className='flex flex-col gap-1'>
                <Label htmlFor='staff-balance-batch-kind'>{t('Batch kind')}</Label>
                <NativeSelect id='staff-balance-batch-kind' className='w-full' value={batchKind}
                  onChange={(event) => setBatchKind(event.target.value)}>
                  <NativeSelectOption value=''>{t('Batch kind')}</NativeSelectOption>
                  <NativeSelectOption value='principal'>{t('Principal')}</NativeSelectOption>
                  <NativeSelectOption value='bonus'>{t('Permanent gift')}</NativeSelectOption>
                  <NativeSelectOption value='expiring_bonus'>{t('Expiring gift')}</NativeSelectOption>
                </NativeSelect>
              </div>
              <div className='flex flex-col gap-1'>
                <Label htmlFor='staff-balance-batch-source'>{t('Batch source')}</Label>
                <Input id='staff-balance-batch-source' value={batchSource} maxLength={64}
                  onChange={(event) => setBatchSource(event.target.value)} />
              </div>
            </>
          )}
          <Label className='flex items-start gap-2 text-sm'>
            <Checkbox
              className='mt-1'
              checked={confirmed}
              onCheckedChange={(checked) => setConfirmed(checked === true)}
            />
            <span>{t(notice)}</span>
          </Label>
        </div>
    </Dialog>
  )
}
