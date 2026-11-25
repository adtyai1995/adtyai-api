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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { CommissionWithdrawalPanel } from '@/features/adty-site-owner/components/commission-withdrawal-panel'
import { DraftNavigationProvider } from '@/features/system-settings/components/draft-navigation-provider'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { toIntlLocale } from '@/i18n/languages'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  adminReferralKeys,
  getReferralBadges,
  getReferralOverview,
  runSettlement,
} from './api'
import { AffiliatePanel } from './components/affiliate-panel'
import {
  CommissionsPanel,
  type CommissionAffiliateFilter,
} from './components/commissions-panel'
import { AuditLogPanel } from './components/records-panels'
import { SettingsPanel } from './components/settings-panel'
import { WithdrawalPanel } from './components/withdrawal-panel'

type ReferralEntry = { initialStatus?: 'pending' | 'approved' | 'actionable' }

function WithdrawalWork(
  props: ReferralEntry & { canReview: boolean; canPayout: boolean }
) {
  const { canReview, canPayout } = props
  const [status, setStatus] = useState(() => {
    if (props.initialStatus === 'pending' && canReview) return 'pending'
    if (props.initialStatus === 'approved' && canPayout) return 'approved'
    if (canReview && canPayout) return 'actionable'
    if (canReview) return 'pending'
    if (canPayout) return 'approved'
    return ''
  })
  return (
    <>
      <WithdrawalPanel
        key={`platform-${status}`}
        {...props}
        status={status}
        onStatusChange={setStatus}
      />
      <CommissionWithdrawalPanel
        key={`closed-${status}`}
        closed
        status={status}
        canReview={canReview}
        canPay={canPayout}
      />
    </>
  )
}

/** Referral admin (adty task 6.7): overview, pending reviews, affiliates, withdrawals, settings. */
export function AdminReferral({ initialStatus }: ReferralEntry = {}) {
  const { t, i18n } = useTranslation()
  const [activeTab, setActiveTab] = useState('withdrawals')
  const [commissionFilterAffiliate, setCommissionFilterAffiliate] =
    useState<CommissionAffiliateFilter | null>(null)
  const [settingsVisited, setSettingsVisited] = useState(false)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const canReview = canAdmin(user, ADTY_PERMISSIONS.REFERRAL_REVIEW)
  const canPayout = canAdmin(user, ADTY_PERMISSIONS.REFERRAL_PAYOUT)
  const canAdjust = canAdmin(user, ADTY_PERMISSIONS.REFERRAL_ADJUST)
  const canSettings = canAdmin(user, ADTY_PERMISSIONS.REFERRAL_SETTINGS)
  const canReadSettings = canAdmin(
    user,
    ADTY_PERMISSIONS.REFERRAL_SETTINGS_READ
  )
  const overview = useQuery({
    queryKey: adminReferralKeys.overview,
    queryFn: getReferralOverview,
  })
  const badges = useQuery({
    queryKey: adminReferralKeys.badges,
    queryFn: getReferralBadges,
  })
  const settle = useMutation({
    mutationFn: runSettlement,
    onSuccess: () => {
      toast.success(t('Settlement finished'))
      void queryClient.invalidateQueries({ queryKey: adminReferralKeys.all })
      void invalidateSidebarBadges(queryClient)
    },
    onError: (err) => handleServerError(err),
  })
  const data = overview.data
  const pendingWithdrawals =
    (badges.data?.pending_withdrawals ?? 0) +
    (badges.data?.approved_withdrawals ?? 0)

  return (
    <DraftNavigationProvider>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Referral admin')}</SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {t('Affiliates, commissions and withdrawal review.')}
        </SectionPageLayout.Description>
        <SectionPageLayout.Content>
          <div className='flex flex-col gap-4'>
            {overview.isError && (
              <ErrorState onRetry={() => void overview.refetch()} />
            )}
            {badges.isError && (
              <ErrorState onRetry={() => void badges.refetch()} />
            )}
            {overview.isPending && <LoadingState />}
            {data && (
              <div className='border-border bg-border grid grid-cols-2 gap-px overflow-hidden rounded-[14px] border lg:grid-cols-3 xl:grid-cols-6'>
                {[
                  {
                    label: t('Affiliates'),
                    value: formatNumber(data?.affiliates ?? 0, locale),
                  },
                  {
                    label: t('Referred users'),
                    value: formatNumber(data?.bindings ?? 0, locale),
                  },
                  {
                    label: t('Settling'),
                    value: formatUsd(Number(data?.pending ?? 0), locale),
                  },
                  {
                    label: t('Withdrawable'),
                    value: formatUsd(Number(data?.available ?? 0), locale),
                  },
                  {
                    label: t('Withdrawing'),
                    value: formatUsd(Number(data?.frozen ?? 0), locale),
                  },
                  {
                    label: t('Withdrawn'),
                    value: formatUsd(Number(data?.withdrawn ?? 0), locale),
                  },
                ].map((item) => (
                  <div
                    key={item.label}
                    className='bg-card flex flex-col gap-1 px-5 py-4'
                  >
                    <span className='text-dim text-[12px] leading-none'>
                      {item.label}
                    </span>
                    <span className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
                      {item.value}
                    </span>
                  </div>
                ))}
              </div>
            )}
            <Tabs
              value={activeTab}
              onValueChange={(value) => {
                setActiveTab(value)
                if (value === 'settings') setSettingsVisited(true)
              }}
            >
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <TabsList
                  variant='line'
                  className='border-border h-auto flex-wrap justify-start gap-x-6 gap-y-1 rounded-none border-b p-0 [--tab-underline:var(--brand)] group-data-horizontal/tabs:h-auto'
                >
                  <TabsTrigger
                    value='withdrawals'
                    className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                  >
                    {t('Withdrawals')}
                    {pendingWithdrawals > 0 && ` (${pendingWithdrawals})`}
                  </TabsTrigger>
                  <TabsTrigger
                    value='affiliates'
                    className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                  >
                    {t('Affiliates')}
                  </TabsTrigger>
                  <TabsTrigger
                    value='commissions'
                    className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                  >
                    {t('Commission details')}
                  </TabsTrigger>
                  <TabsTrigger
                    value='audit-logs'
                    className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                  >
                    {t('Admin activity')}
                  </TabsTrigger>
                  {canReadSettings && (
                    <TabsTrigger
                      value='settings'
                      className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                    >
                      {t('Settings')}
                    </TabsTrigger>
                  )}
                </TabsList>
                {canAdjust && (
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={settle.isPending}
                    onClick={() => settle.mutate()}
                  >
                    {t('Run settlement now')}
                  </Button>
                )}
              </div>
              <TabsContent value='withdrawals'>
                <WithdrawalWork
                  key={`${canReview}-${canPayout}-${initialStatus ?? ''}`}
                  initialStatus={initialStatus}
                  canReview={canReview}
                  canPayout={canPayout}
                />
              </TabsContent>
              <TabsContent value='affiliates'>
                <AffiliatePanel
                  canAdjust={canAdjust}
                  onViewCommissions={(row) => {
                    setCommissionFilterAffiliate({
                      affiliateId: row.id,
                      userId: row.user_id,
                      username: row.username,
                    })
                    setActiveTab('commissions')
                  }}
                />
              </TabsContent>
              <TabsContent value='commissions' className='pt-3'>
                <CommissionsPanel
                  affiliateFilter={commissionFilterAffiliate}
                  onClearAffiliateFilter={() =>
                    setCommissionFilterAffiliate(null)
                  }
                />
              </TabsContent>
              <TabsContent value='audit-logs'>
                <AuditLogPanel />
              </TabsContent>
              {canReadSettings && (
                <TabsContent value='settings' keepMounted>
                  {settingsVisited && <SettingsPanel canWrite={canSettings} />}
                </TabsContent>
              )}
            </Tabs>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    </DraftNavigationProvider>
  )
}
