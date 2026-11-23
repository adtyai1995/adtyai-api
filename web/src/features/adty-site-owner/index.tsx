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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { NotificationManager } from '@/features/adty-notifications/components/notification-manager'
import { OwnerTeamAdmin } from '@/features/adty-team-admin'
import { TelegramBindCard } from '@/features/adty-telegram/bind-card'
import { TicketCenter } from '@/features/adty-ticket/ticket-center'
import { RechargeAudit } from '@/features/recharge-audit'
import { DraftNavigationProvider } from '@/features/system-settings/components/draft-navigation-provider'
import { useAuthStore } from '@/stores/auth-store'

import { getOwnerSite, siteOwnerKeys } from './api'
import { AgreementPanel } from './components/agreement-panel'
import { ArchivedAccountsPanel } from './components/archived-accounts-panel'
import { BonusPanel } from './components/bonus-panel'
import { CommissionWithdrawalPanel } from './components/commission-withdrawal-panel'
import { CustomersPanel } from './components/customers-panel'
import { EarningToMarginPanel } from './components/earning-to-margin-panel'
import { EarningsPanel } from './components/earnings-panel'
import { OverviewPanel } from './components/overview-panel'
import { OwnerSelfUsePanel } from './components/owner-self-use-panel'
import { toIntlLocale } from '@/i18n/languages'
import { OwnerVerificationProvider } from './components/owner-verification'
import { PricingPanel } from './components/pricing-panel'
import { SettlementSheetPanel } from './components/settlement-sheet-panel'
import { SiteReferralPanel } from './components/site-referral-panel'
import { SiteSettingsPanel } from './components/site-settings-panel'
import { StaffPanel } from './components/staff-panel'
import { TermsAcceptPanel } from './components/terms-accept-panel'
import { VoucherPanel } from './components/voucher-panel'
import { WalletLedgerPanel } from './components/wallet-ledger-panel'
import { WithdrawalPanel } from './components/withdrawal-panel'
import {
  SITE_OWNER_SECTIONS,
  SITE_OWNER_STANDALONE_SECTIONS,
  type SiteOwnerSectionId,
} from './sections'

/** Site owner center (task 11.6): one page with a tab per section. */
export function SiteOwnerCenter(props: {
  section: SiteOwnerSectionId
  returnTradeNo?: string
  returnBusiness?: 'owner_self_use_topup' | 'owner_margin_topup'
}) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const user = useAuthStore((state) => state.auth.user)
  const termsRequired = user?.site_terms_required === true
  // Tickets and teams have their own sidebar entries and render without the owner-center tabs.
  const standalone = SITE_OWNER_STANDALONE_SECTIONS.includes(props.section)
  const site = useQuery({
    queryKey: siteOwnerKeys.site,
    queryFn: getOwnerSite,
    enabled: !termsRequired && props.section !== 'history',
  })
  const labels: Record<SiteOwnerSectionId, string> = {
    overview: t('Overview'),
    history: t('Historical site accounts'),
    tickets: t('Ticket workspace'),
    customers: t('Customers'),
    teams: t('Team management'),
    commissions: t('Invite commission withdrawal'),
    staff: t('Staff accounts'),
    orders: t('Order management'),
    pricing: t('Pricing'),
    bonus: t('Bonuses'),
    settings: t('Site settings'),
    earnings: t('Earnings'),
    vouchers: t('Recharge codes'),
    notices: t('Notices'),
    agreement: t('Site agreement'),
  }

  if (props.section === 'history') return <OwnerVerificationProvider><ArchivedAccountsPanel /></OwnerVerificationProvider>
  return (
    <OwnerVerificationProvider>
      <DraftNavigationProvider>
        <SectionPageLayout>
          <SectionPageLayout.Title>
            {standalone ? labels[props.section] : t('Site owner center')}
          </SectionPageLayout.Title>
          <SectionPageLayout.Content>
            {termsRequired && (
              <TermsAcceptPanel version={user?.site_terms_version ?? ''} />
            )}
            {!termsRequired && site.isLoading && (
              <Skeleton className='h-40 w-full' />
            )}
            {!termsRequired && site.isError && (
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
                          {new Intl.DateTimeFormat(toIntlLocale(i18n.resolvedLanguage || i18n.language), {
                            dateStyle: 'medium',
                            timeStyle: 'long',
                            timeZone: 'Asia/Shanghai',
                          }).format(
                            new Date(site.data.close_execute_at * 1000)
                          )}
                        </p>
                      )}
                    </AlertDescription>
                  </Alert>
                )}
                {props.section === 'tickets' && <TicketCenter scope='owner' />}
                {props.section === 'teams' && <OwnerTeamAdmin />}
                {!standalone && (
                <Tabs
                  value={props.section}
                  onValueChange={(value) =>
                    void navigate({
                      to: '/site-owner/$section',
                      params: { section: value as SiteOwnerSectionId },
                    })
                  }
                >
                  {/* Section nav: text tabs over a hairline, with a 2px brand
                      underline on the active one (design system §4). */}
                  <TabsList
                    variant='line'
                    className='border-border h-auto flex-wrap justify-start gap-x-6 gap-y-1 rounded-none border-b p-0 [--tab-underline:var(--brand)] group-data-horizontal/tabs:h-auto'
                  >
                    {SITE_OWNER_SECTIONS.filter(
                      (section) =>
                        (section !== 'history' || user?.adty_archived_site_owner) &&
                        !SITE_OWNER_STANDALONE_SECTIONS.includes(section)
                    ).map((section) => (
                      <TabsTrigger
                        key={section}
                        value={section}
                        className='text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'
                      >
                        {labels[section]}
                      </TabsTrigger>
                    ))}
                  </TabsList>
                  <TabsContent value='overview'>
                    <OverviewPanel />
                  </TabsContent>
                  <TabsContent
                    value='customers'
                    className='flex flex-col gap-5'
                  >
                    <CustomersPanel />
                  </TabsContent>
                  <TabsContent value='commissions'>
                    <SiteReferralPanel />
                    <CommissionWithdrawalPanel />
                  </TabsContent>
                  <TabsContent value='orders'>
                    <RechargeAudit ownerMode ownerSiteId={site.data.id} />
                  </TabsContent>
                  <TabsContent value='pricing'>
                    <PricingPanel site={site.data} />
                  </TabsContent>
                  <TabsContent value='bonus'>
                    <BonusPanel site={site.data} />
                  </TabsContent>
                  <TabsContent value='staff'>
                    <StaffPanel
                      siteId={site.data.id}
                      closing={site.data.status === 'closing'}
                    />
                  </TabsContent>
                  <TabsContent value='settings'>
                    <SiteSettingsPanel site={site.data} />
                  </TabsContent>
                  <TabsContent value='earnings'>
                    <div className='flex flex-col gap-5'>
                      <EarningsPanel />
                      <section id='self-use'>
                        <OwnerSelfUsePanel
                          returnTradeNo={
                            props.returnBusiness === 'owner_margin_topup'
                              ? undefined
                              : props.returnTradeNo
                          }
                          onReturnClosed={() =>
                            void navigate({
                              to: '/site-owner/$section',
                              params: { section: 'earnings' },
                              search: {},
                              replace: true,
                            })
                          }
                        />
                      </section>
                      <section id='settlement'>
                        <SettlementSheetPanel />
                      </section>
                      <section id='margin'>
                        <EarningToMarginPanel
                          returnTradeNo={
                            props.returnBusiness === 'owner_margin_topup'
                              ? props.returnTradeNo
                              : undefined
                          }
                          onReturnClosed={() =>
                            void navigate({
                              to: '/site-owner/$section',
                              params: { section: 'earnings' },
                              search: {},
                              replace: true,
                            })
                          }
                        />
                      </section>
                      <section id='withdrawal'>
                        <WithdrawalPanel key={site.data.id} activeSiteId={site.data.id} />
                      </section>
                      <WalletLedgerPanel />
                    </div>
                  </TabsContent>
                  <TabsContent value='vouchers'>
                    <VoucherPanel siteId={site.data.id} />
                  </TabsContent>
                  <TabsContent value='notices'>
                    <NotificationManager mode='owner' siteId={site.data.id} />
                    <TelegramBindCard purpose='admin' />
                  </TabsContent>
                  <TabsContent value='agreement'>
                    <AgreementPanel site={site.data} />
                  </TabsContent>
                </Tabs>
                )}
              </div>
            )}
          </SectionPageLayout.Content>
        </SectionPageLayout>
      </DraftNavigationProvider>
    </OwnerVerificationProvider>
  )
}
