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
import { createFileRoute } from '@tanstack/react-router'
import { ExternalLink, Headset } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { useAdtySite } from '@/features/adty-site/api'
import { TicketCenter } from '@/features/adty-ticket/ticket-center'
import { useStatus } from '@/hooks/use-status'
import { isHttpUrl } from '@/lib/content-format'
import { subsiteDocument } from '@/lib/subsite-document'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/support/')({
  component: SupportCenterPage,
})

function SupportCenterPage() {
  const { t } = useTranslation()
  const { status } = useStatus()
  const siteQuery = useAdtySite()
  const user = useAuthStore((state) => state.auth.user)
  const tenant = subsiteDocument()
  const isSubsite = Boolean(
    tenant || status?.adty_site || (user?.site_id ?? 0) > 0
  )

  const siteBrandName = (
    tenant?.name ||
    tenant?.title ||
    (typeof status?.system_name === 'string' ? status.system_name : '')
  ).trim()

  const supportContact = siteQuery.data?.support_contact?.trim() ?? ''
  const supportEmail = siteQuery.data?.support_email?.trim() ?? ''
  const supportHours = siteQuery.data?.support_hours?.trim() ?? ''
  const rawTelegramUrl = siteQuery.data?.telegram_url?.trim() ?? ''
  const telegramUrl =
    !isSubsite &&
    rawTelegramUrl &&
    !rawTelegramUrl.includes('\\') &&
    isHttpUrl(rawTelegramUrl)
      ? rawTelegramUrl
      : ''

  let description = t('Submit and track support tickets with our support team.')
  if (isSubsite) {
    description = siteBrandName
      ? t('Submit and track support tickets with {{site}} support.', {
          site: siteBrandName,
        })
      : t('Submit and track support tickets with site support.')
  }

  const subsiteContactText = supportContact || supportEmail
  const hasMainSupportInfo = Boolean(
    !isSubsite && (supportEmail || supportHours || telegramUrl)
  )

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Support Center')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {description}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-7xl flex-col gap-4'>
          {isSubsite && subsiteContactText && (
            <Alert>
              <Headset aria-hidden='true' />
              <AlertTitle>
                {siteBrandName
                  ? `${siteBrandName} · ${t('Support contact')}`
                  : t('Support contact')}
              </AlertTitle>
              <AlertDescription className='mt-1 break-all'>
                {subsiteContactText}
              </AlertDescription>
            </Alert>
          )}
          {hasMainSupportInfo && (
            <Alert>
              <Headset aria-hidden='true' />
              <AlertTitle>{t('Support contact')}</AlertTitle>
              <AlertDescription className='mt-1 flex flex-wrap items-center gap-x-4 gap-y-1'>
                {supportEmail && (
                  <span>
                    {t('Support email')}: {supportEmail}
                  </span>
                )}
                {supportHours && (
                  <span>
                    {t('Support hours')}: {supportHours}
                  </span>
                )}
                {telegramUrl && (
                  <a
                    href={telegramUrl}
                    target='_blank'
                    rel='noopener noreferrer'
                    className='inline-flex items-center gap-1 underline underline-offset-4'
                  >
                    {t('Telegram support')}
                    <ExternalLink className='size-3.5' aria-hidden='true' />
                  </a>
                )}
              </AlertDescription>
            </Alert>
          )}
          <TicketCenter scope='personal' />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
