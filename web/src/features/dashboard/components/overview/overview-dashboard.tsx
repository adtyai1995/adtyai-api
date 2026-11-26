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
import { Link } from '@tanstack/react-router'
import { KeyRound } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import {
  CardStaggerContainer,
  CardStaggerItem,
} from '@/components/page-transition'
import { Button } from '@/components/ui/button'
import { useDocsLink } from '@/hooks/use-docs-link'

import { useDashboardContentVisibility } from '../../hooks/use-status-data'
import { AnnouncementsPanel } from './announcements-panel'
import { ApiInfoPanel } from './api-info-panel'
import { FAQPanel } from './faq-panel'
import { SummaryCards } from './summary-cards'
import { UptimePanel } from './uptime-panel'

export function OverviewDashboard() {
  const { t } = useTranslation()
  const docsLink = useDocsLink()
  const {
    announcements: showAnnouncementsPanel,
    faq: showFAQPanel,
    uptimeKuma: showUptimePanel,
  } = useDashboardContentVisibility()

  const docsRender =
    docsLink.external || docsLink.reloadDocument ? (
      <a
        href={docsLink.href}
        target={docsLink.external ? '_blank' : undefined}
        rel={docsLink.external ? 'noopener noreferrer' : undefined}
      />
    ) : (
      <Link to='/docs' />
    )

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Overview')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Monitor balance, usage, and request volume')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Actions>
        <Button variant='outline' size='sm' render={docsRender}>
          {t('Docs')}
        </Button>
        <Button variant='brand' size='sm' render={<Link to='/keys' />}>
          <KeyRound data-icon='inline-start' />
          {t('Create API Key')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          <SummaryCards />

          <CardStaggerContainer className='grid grid-cols-1 gap-4 lg:grid-cols-2'>
            <CardStaggerItem>
              <ApiInfoPanel />
            </CardStaggerItem>
            {showAnnouncementsPanel && (
              <CardStaggerItem>
                <AnnouncementsPanel />
              </CardStaggerItem>
            )}
            {showUptimePanel && (
              <CardStaggerItem>
                <UptimePanel />
              </CardStaggerItem>
            )}
            {showFAQPanel && (
              <CardStaggerItem>
                <FAQPanel />
              </CardStaggerItem>
            )}
          </CardStaggerContainer>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
