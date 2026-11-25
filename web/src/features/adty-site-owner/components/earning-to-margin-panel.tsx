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
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { AdtyOnlineTopup } from '@/features/wallet/adty/components/adty-online-topup'
import { handleServerError } from '@/lib/handle-server-error'

import { siteOwnerKeys, transferEarningToMargin } from '../api'
import { newIdempotencyKey } from '../lib/site-owner'

/** Moves typed earnings into margin or tops up margin online. The tier deposit comes from the enabled site tier. */
export function EarningToMarginPanel(props: {
  returnTradeNo?: string
  onReturnClosed?: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [amount, setAmount] = useState('')
  const intentRef = useRef<{ payload: string; key: string } | null>(null)
  const transfer = useMutation({
    mutationFn: () => {
      const trimmed = amount.trim()
      if (intentRef.current?.payload !== trimmed) {
        intentRef.current = { payload: trimmed, key: newIdempotencyKey() }
      }
      return transferEarningToMargin(trimmed, intentRef.current.key)
    },
    onSuccess: () => {
      intentRef.current = null
      setAmount('')
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: siteOwnerKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <section className='flex flex-col gap-8'>
      <SettingsSection
        title={t('Transfer earnings to margin')}
        description={t(
          'The margin tier comes from the enabled site tier. This page does not choose a tier amount.'
        )}
      >
        <div className='flex flex-col gap-2 sm:flex-row sm:items-end'>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='earn-margin-amount'>{t('Amount')}</Label>
            <Input
              id='earn-margin-amount'
              className='w-48 tabular-nums'
              value={amount}
              inputMode='decimal'
              autoComplete='off'
              onChange={(event) => setAmount(event.target.value)}
            />
          </div>
          <Button
            type='button'
            variant='outline'
            disabled={transfer.isPending || amount.trim() === ''}
            onClick={() => transfer.mutate()}
          >
            {t('Transfer earnings to margin')}
          </Button>
        </div>
      </SettingsSection>
      <AdtyOnlineTopup
        business='owner_margin_topup'
        returnTradeNo={props.returnTradeNo}
        onReturnClosed={props.onReturnClosed}
        onSettled={() =>
          client.invalidateQueries({ queryKey: siteOwnerKeys.all })
        }
      />
    </section>
  )
}
