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
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SiteWalletLedger } from '@/features/adty-ledger/site-wallet-ledger'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  getOwnerWalletLedger,
  redeemOwnerMarginVoucher,
  siteOwnerKeys,
  type RedeemedOwnerVoucher,
} from '../api'
import { EarningToMarginPanel } from './earning-to-margin-panel'
import { OwnerWalletBalances } from './wallet-ledger-panel'

function MarginVoucherRedeemCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [redeemed, setRedeemed] = useState<RedeemedOwnerVoucher | null>(null)

  const redeem = useMutation({
    mutationFn: (rawCode: string) => redeemOwnerMarginVoucher(rawCode),
    onMutate: () => {
      setError('')
    },
    onSuccess: (result) => {
      setRedeemed(result)
      setCode('')
      toast.success(
        t('Margin recharge code redeemed ({{amount}} USD)', {
          amount: result.amount_usd,
        })
      )
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void queryClient.invalidateQueries({ queryKey: ['adty-topup'] })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (cause) => {
      setError(getServerErrorMessage(cause, t('Voucher code is invalid')))
      handleServerError(cause)
    },
  })

  return (
    <SettingsSection
      title={t('Margin recharge code')}
      description={t(
        'Redeem a platform-issued margin recharge code to credit your site margin wallet directly.'
      )}
      bodyClassName='flex flex-col gap-3'
    >
      <form
        className='flex max-w-xl flex-col gap-2 sm:flex-row sm:items-end'
        onSubmit={(event) => {
          event.preventDefault()
          if (redeem.isPending || code.trim() === '') return
          redeem.mutate(code)
        }}
      >
        <div className='flex flex-1 flex-col gap-1'>
          <Label htmlFor='owner-margin-voucher-code'>
            {t('Recharge code')}
          </Label>
          <Input
            id='owner-margin-voucher-code'
            value={code}
            autoComplete='off'
            placeholder={t('Enter margin recharge code')}
            onChange={(event) => setCode(event.target.value)}
          />
        </div>
        <Button
          type='submit'
          variant='brand'
          disabled={redeem.isPending || code.trim() === ''}
        >
          {t('Redeem margin recharge code')}
        </Button>
      </form>
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {error}
        </p>
      )}
      {redeemed && (
        <p className='text-sm tabular-nums'>
          {t('Redeemed as order {{trade}}', { trade: redeemed.trade_no })} ·{' '}
          {redeemed.amount_usd} USD
        </p>
      )}
    </SettingsSection>
  )
}

/**
 * Owner Wallets page (R-33 #1): earnings & margin wallet cards, margin
 * recharge code redemption, earning-to-margin transfer, online margin top-up,
 * and wallet ledger.
 */
export function WalletsPanel(props: {
  returnTradeNo?: string
  onReturnClosed?: () => void
}) {
  const { t } = useTranslation()
  return (
    <div className='flex flex-col gap-8'>
      <OwnerWalletBalances />
      <MarginVoucherRedeemCard />
      <section id='margin'>
        <EarningToMarginPanel
          returnTradeNo={props.returnTradeNo}
          onReturnClosed={props.onReturnClosed}
        />
      </section>
      <SiteWalletLedger
        label={t('Wallet ledger')}
        queryKey={siteOwnerKeys.list('wallet-ledger', {})}
        kinds={['earnings', 'margin']}
        fetchPage={getOwnerWalletLedger}
      />
    </div>
  )
}
