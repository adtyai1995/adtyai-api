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
import { useTranslation } from 'react-i18next'

import type { SiteWallet } from '@/features/adty-ledger/site-wallet-labels'

import {
  computeOwnerFundingPreview,
  formatFundingUsd,
} from '../lib/funding-preview'

/**
 * Live funding breakdown for owner-funded credits and vouchers:
 * available earnings first, then margin strictly above the level deposit floor.
 */
export function OwnerFundingPreviewBox(props: {
  wallets: SiteWallet[]
  amount: string
}) {
  const { t } = useTranslation()
  const preview = computeOwnerFundingPreview(props.wallets, props.amount)

  return (
    <div className='bg-muted/40 border-border space-y-1 rounded-md border p-3 text-xs'>
      <p className='text-muted-foreground'>
        {t(
          'Available earnings: ${{earnings}} · Usable excess margin (above ${{floor}} level floor): ${{margin}}',
          {
            earnings: formatFundingUsd(preview.earningsAvailable),
            floor: formatFundingUsd(preview.marginFloor),
            margin: formatFundingUsd(preview.marginExcessAvailable),
          }
        )}
      </p>
      {preview.exceedsMax && (
        <p className='text-destructive font-medium'>
          {t('Amount exceeds the maximum limit of 100,000 USD.')}
        </p>
      )}
      {preview.valid && (
        <p className='font-medium tabular-nums'>
          {t(
            'Funding split: Earnings ${{earnings}} + Excess margin ${{margin}}',
            {
              earnings: formatFundingUsd(preview.fromEarnings),
              margin: formatFundingUsd(preview.fromMargin),
            }
          )}
        </p>
      )}
      {preview.valid && !preview.sufficient && (
        <p className='text-destructive font-medium tabular-nums'>
          {t('Shortfall: ${{shortfall}} USD (deposit floor is reserved)', {
            shortfall: formatFundingUsd(preview.shortfall),
          })}
        </p>
      )}
    </div>
  )
}
