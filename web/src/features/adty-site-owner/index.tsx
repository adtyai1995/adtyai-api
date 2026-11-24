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
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { NotificationManager } from '@/features/adty-notifications/components/notification-manager'
import { OwnerTeamAdmin } from '@/features/adty-team-admin'
import { TelegramBindCard } from '@/features/adty-telegram/bind-card'
import { TicketCenter } from '@/features/adty-ticket/ticket-center'
import { RechargeAudit } from '@/features/recharge-audit'
import { DraftNavigationProvider } from '@/features/system-settings/components/draft-navigation-provider'
import { toIntlLocale } from '@/i18n/languages'

import { getOwnerSite, siteOwnerKeys } from './api'
import { ArchivedAccountsPanel } from './components/archived-accounts-panel'
import { BonusPanel } from './components/bonus-panel'
import { CommissionWithdrawalPanel } from './components/commission-withdrawal-panel'
import { CustomersPanel } from './components/customers-panel'
import { EarningsPanel } from './components/earnings-panel'
import { OverviewPanel } from './components/overview-panel'
import { OwnerVerificationProvider } from './components/owner-verification'
import { PricingPanel } from './components/pricing-panel'
import { SettlementSheetPanel } from './components/settlement-sheet-panel'
import { SiteReferralPanel } from './components/site-referral-panel'
import { SiteSettingsPanel } from './components/site-settings-panel'
import { StaffPanel } from './components/staff-panel'
import { VoucherPanel } from './components/voucher-panel'
import { WalletsPanel } from './components/wallets-panel'
import { WithdrawalPanel } from './components/withdrawal-panel'
import type { SiteOwnerSectionId } from './sections'

/** Site owner center (task 11.6, R-33 #1): renders only the active sidebar section with no duplicate horizontal tab bar. */
export function SiteOwnerCenter(props: {
  section: SiteOwnerSectionId
  returnTradeNo?: string
  returnBusiness?: 'owner_margin_topup'
}) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const site = useQuery({
    queryKey: siteOwnerKeys.site,
    queryFn: getOwnerSite,
    enabled: props.section !== 'history',
  })
  const labels: Record<SiteOwnerSectionId, string> = {
    overview: t('Site Overview'),
    customers: t('Customers'),
    teams: t('Team management'),
    orders: t('Order management'),
    pricing: t('Pricing & Multiplier'),
    bonus: t('Top-up Bonus'),
    commissions: t('Promotion Management'),
    earnings: t('Earnings Details'),
    withdrawals: t('Withdrawal Management'),
    settlements: t('Settlement Sheets'),
    wallets: t('Owner Wallets'),
    vouchers: t('Redemption Codes'),
    notices: t('Notifications'),
    staff: t('Staff accounts'),
    tickets: t('Ticket management'),
    settings: t('Site settings'),
    history: t('Historical site accounts'),
  }

  if (props.section === 'history') {
    return (
      <OwnerVerificationProvider>
        <ArchivedAccountsPanel />
      </OwnerVerificationProvider>
    )
  }

  return (
    <OwnerVerificationProvider>
      <DraftNavigationProvider>
        <SectionPageLayout>
          <SectionPageLayout.Title>
            {labels[props.section]}
          </SectionPageLayout.Title>
          <SectionPageLayout.Content>
            {site.isLoading && <Skeleton className='h-40 w-full' />}
            {site.isError && (
              <ErrorState
                className='min-h-48'
                onRetry={() => void site.refetch()}
              />
            )}
            {site.data && (
              <div className='flex flex-col gap-4'>
                {site.data.status === 'closing' && (
                  <Alert>
                    <AlertDescription>
                      {t(
                        'Your site is closing. Customers will be emailed before it closes.'
                      )}
                      {site.data.close_execute_at > 0 && (
                        <p>
                          {t('Closes on')} ·{' '}
                          {new Intl.DateTimeFormat(
                            toIntlLocale(
                              i18n.resolvedLanguage || i18n.language
                            ),
                            {
                              dateStyle: 'medium',
                              timeStyle: 'long',
                              timeZone: 'Asia/Shanghai',
                            }
                          ).format(new Date(site.data.close_execute_at * 1000))}
                        </p>
                      )}
                    </AlertDescription>
                  </Alert>
                )}
                {props.section === 'overview' && <OverviewPanel />}
                {props.section === 'customers' && (
                  <div className='flex flex-col gap-5'>
                    <CustomersPanel />
                  </div>
                )}
                {props.section === 'teams' && <OwnerTeamAdmin />}
                {props.section === 'orders' && (
                  <RechargeAudit ownerMode ownerSiteId={site.data.id} />
                )}
                {props.section === 'pricing' && (
                  <PricingPanel site={site.data} />
                )}
                {props.section === 'bonus' && <BonusPanel site={site.data} />}
                {props.section === 'commissions' && (
                  <div className='flex flex-col gap-6'>
                    <SiteReferralPanel />
                    <CommissionWithdrawalPanel />
                  </div>
                )}
                {props.section === 'earnings' && <EarningsPanel />}
                {props.section === 'withdrawals' && (
                  <WithdrawalPanel
                    key={site.data.id}
                    activeSiteId={site.data.id}
                  />
                )}
                {props.section === 'settlements' && <SettlementSheetPanel />}
                {props.section === 'wallets' && (
                  <WalletsPanel
                    returnTradeNo={
                      props.returnBusiness === 'owner_margin_topup'
                        ? props.returnTradeNo
                        : undefined
                    }
                    onReturnClosed={() =>
                      void navigate({
                        to: '/site-owner/$section',
                        params: { section: 'wallets' },
                        search: {},
                        replace: true,
                      })
                    }
                  />
                )}
                {props.section === 'vouchers' && (
                  <VoucherPanel siteId={site.data.id} />
                )}
                {props.section === 'notices' && (
                  <div className='flex flex-col gap-6'>
                    <NotificationManager mode='owner' siteId={site.data.id} />
                    <TelegramBindCard purpose='admin' />
                  </div>
                )}
                {props.section === 'staff' && (
                  <StaffPanel
                    siteId={site.data.id}
                    closing={site.data.status === 'closing'}
                  />
                )}
                {props.section === 'tickets' && <TicketCenter scope='owner' />}
                {props.section === 'settings' && (
                  <SiteSettingsPanel site={site.data} />
                )}
              </div>
            )}
          </SectionPageLayout.Content>
        </SectionPageLayout>
      </DraftNavigationProvider>
    </OwnerVerificationProvider>
  )
}
