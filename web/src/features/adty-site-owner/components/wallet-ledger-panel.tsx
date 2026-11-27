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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { siteWalletKindLabel } from '@/features/adty-ledger/site-wallet-labels'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { getOwnerWallets, siteOwnerKeys } from '../api'
import { marginCycleSummary } from '../lib/site-owner'
import { OwnerStatGrid } from './stat-grid'

/**
 * Earnings and margin wallet summary cards. The operating wallet is platform
 * bookkeeping and is never shown to the owner.
 */
export function OwnerWalletBalances() {
  const { t } = useTranslation()
  const wallets = useQuery({
    queryKey: siteOwnerKeys.list('wallets', {}),
    queryFn: getOwnerWallets,
  })
  const rows = (Array.isArray(wallets.data) ? wallets.data : []).filter(
    (wallet) => wallet.kind !== 'operating'
  )
  return (
    <ContentSection
      title={t('Wallets')}
      description={t(
        'Frozen amounts are held for redemption codes, commission or withdrawals and cannot be used until released.'
      )}
    >
      {wallets.isPending && <LoadingState />}
      {wallets.isError && (
        <ErrorState
          description={getServerErrorMessage(wallets.error)}
          onRetry={() => void wallets.refetch()}
        />
      )}
      {rows.length > 0 && (
        <OwnerStatGrid
          label={t('Wallets')}
          items={rows.flatMap((wallet) => {
            const label = siteWalletKindLabel(t, wallet.kind)
            const base = [
              {
                label,
                value: `${wallet.balance} USD`,
                negative: Number(wallet.balance) < 0,
              },
              {
                label: `${label} · ${t('Frozen')}`,
                value: `${wallet.frozen_usd || '0'} USD`,
              },
              ...(Number(wallet.excess_frozen_usd) > 0
                ? [
                    {
                      label: `${label} · ${t('Excess frozen')}`,
                      value: `${wallet.excess_frozen_usd} USD`,
                    },
                  ]
                : []),
            ]
            if (wallet.kind !== 'margin') return base
            const cycle = marginCycleSummary(wallet)
            return [
              ...base,
              {
                label: `${label} · ${t('Tier')}`,
                value: `${cycle.tierUsd} USD`,
              },
              {
                label: `${label} · ${t('Used')}`,
                value: `${cycle.usedUsd} USD`,
              },
              {
                label: `${label} · ${t('Limit')}`,
                value: `${cycle.limit90Usd} USD`,
              },
              {
                label: `${label} · ${t('Available')}`,
                value: `${cycle.availableUsd} USD`,
              },
            ]
          })}
        />
      )}
    </ContentSection>
  )
}
