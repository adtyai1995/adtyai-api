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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { CreateTeamForm } from '@/features/adty-site-admin/components/create-team-form'
import { FundReviewPanel } from '@/features/adty-site-admin/components/site-fund-panel'

import { OwnerCreateTeamForm } from './components/owner-create-team-form'
import { TeamDetailSheet } from './components/team-detail-sheet'
import { TeamListPanel } from './components/team-list-panel'
import { TeamRegistrationLinksSection } from './components/team-registration-links-section'

const tabListClassName =
  'border-border h-auto flex-wrap justify-start gap-x-6 gap-y-1 rounded-none border-b p-0 [--tab-underline:var(--brand)] group-data-horizontal/tabs:h-auto'
const tabTriggerClassName =
  'text-dim hover:text-foreground data-active:text-foreground h-auto flex-none px-0 pt-0 pb-2.5 text-[13.5px] font-medium'

/** Root's standalone team management: every site's teams, creation and registration links. */
export function RootTeamAdmin() {
  const { t } = useTranslation()
  const [openTeam, setOpenTeam] = useState<number | null>(null)
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Team management')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t(
          'Find teams on every site, check members, keys and shared balance, create teams and issue team registration links.'
        )}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <Tabs defaultValue='teams'>
          <TabsList variant='line' className={tabListClassName}>
            <TabsTrigger value='teams' className={tabTriggerClassName}>
              {t('Teams')}
            </TabsTrigger>
            <TabsTrigger value='create' className={tabTriggerClassName}>
              {t('Create team')}
            </TabsTrigger>
            <TabsTrigger value='registrations' className={tabTriggerClassName}>
              {t('Team registration links')}
            </TabsTrigger>
            <TabsTrigger value='anomalies' className={tabTriggerClassName}>
              {t('Team fund anomalies')}
            </TabsTrigger>
          </TabsList>
          <TabsContent value='teams' className='min-w-0 pt-2'>
            <TeamListPanel
              scope='root'
              onOpenTeam={(team) => setOpenTeam(team.id)}
            />
          </TabsContent>
          <TabsContent value='create' className='pt-2'>
            <CreateTeamForm />
          </TabsContent>
          <TabsContent value='registrations' className='pt-2'>
            <TeamRegistrationLinksSection actor='root' />
          </TabsContent>
          <TabsContent value='anomalies' className='pt-2'>
            <FundReviewPanel siteId={0} />
          </TabsContent>
        </Tabs>
        <TeamDetailSheet
          scope='root'
          teamId={openTeam}
          onOpenChange={(open) => !open && setOpenTeam(null)}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

/** The site owner's full team management: teams on its site, team creation and registration links. */
export function OwnerTeamAdmin(props: {
  closing?: boolean
  siteBaseUrl?: string
}) {
  const { t } = useTranslation()
  const [openTeam, setOpenTeam] = useState<number | null>(null)
  return (
    <div className='flex flex-col gap-6'>
      <Tabs defaultValue='teams'>
        <TabsList variant='line' className={tabListClassName}>
          <TabsTrigger value='teams' className={tabTriggerClassName}>
            {t('Teams')}
          </TabsTrigger>
          <TabsTrigger value='create' className={tabTriggerClassName}>
            {t('Create team')}
          </TabsTrigger>
          <TabsTrigger value='registrations' className={tabTriggerClassName}>
            {t('Team registration links')}
          </TabsTrigger>
        </TabsList>
        <TabsContent value='teams' className='min-w-0 pt-2'>
          <TeamListPanel
            scope='owner'
            onOpenTeam={(team) => setOpenTeam(team.id)}
          />
        </TabsContent>
        <TabsContent value='create' className='pt-2'>
          <OwnerCreateTeamForm closing={props.closing} />
        </TabsContent>
        <TabsContent value='registrations' className='pt-2'>
          <TeamRegistrationLinksSection
            actor='owner'
            closing={props.closing}
            publicOrigin={props.siteBaseUrl}
          />
        </TabsContent>
      </Tabs>
      <TeamDetailSheet
        scope='owner'
        teamId={openTeam}
        closing={props.closing}
        onOpenChange={(open) => !open && setOpenTeam(null)}
      />
    </div>
  )
}
