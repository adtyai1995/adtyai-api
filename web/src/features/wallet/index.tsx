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
import { Megaphone } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { RichContent } from '@/components/rich-content'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAdtySite } from '@/features/adty-site/api'
import { getSelf } from '@/lib/api'

import { AdtyOnlineTopup } from './adty/components/adty-online-topup'
import { SiteCommissionPanel } from './adty/site-commission-panel'
import { VoucherRedeemCard } from './adty/voucher-redeem-card'
import { WalletStatsCard } from './components/wallet-stats-card'
import type { UserWalletData } from './types'

interface WalletProps {
  /** Scrolls to the top-up history. */
  initialShowHistory?: boolean
}

export function Wallet(props: WalletProps) {
  const { t } = useTranslation()
  const siteQuery = useAdtySite()
  const walletNotice = siteQuery.data?.wallet_notice?.trim() ?? ''
  const [user, setUser] = useState<UserWalletData | null>(null)
  const [userLoading, setUserLoading] = useState(true)
  const [userError, setUserError] = useState(false)

  const fetchUser = useCallback(async () => {
    try {
      setUserLoading(true)
      setUserError(false)
      const response = await getSelf()
      if (response.success && response.data) {
        setUser(response.data as UserWalletData)
        return
      }
      setUser(null)
      setUserError(true)
    } catch {
      setUser(null)
      setUserError(true)
    } finally {
      setUserLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchUser()
  }, [fetchUser])

  useEffect(() => {
    if (!props.initialShowHistory) return
    document.querySelector('#wallet-history')?.scrollIntoView()
  }, [props.initialShowHistory])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Wallet')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Balance and top-up management')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          size='sm'
          render={<a href='#wallet-history' />}
        >
          {t('Top-up history')}
        </Button>
        <Button
          variant='brand'
          size='sm'
          render={<a href='#wallet-add-funds' />}
        >
          {t('Add credits')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-7xl flex-col gap-4'>
          {walletNotice && (
            <Alert>
              <Megaphone aria-hidden='true' />
              <AlertTitle>{t('Wallet Announcement')}</AlertTitle>
              <AlertDescription className='mt-1'>
                <RichContent breaks content={walletNotice} className='text-sm' />
              </AlertDescription>
            </Alert>
          )}
          <WalletStatsCard
            user={userError ? null : user}
            loading={userLoading}
            error={userError}
          />
          {userError && <ErrorState onRetry={() => void fetchUser()} />}
          <VoucherRedeemCard onRedeemed={() => void fetchUser()} />
          <SiteCommissionPanel onConverted={() => void fetchUser()} />
          <AdtyOnlineTopup onSettled={fetchUser} />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
