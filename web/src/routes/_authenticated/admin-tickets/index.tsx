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
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { canOpenPlatformTickets } from '@/features/adty-site-admin/sections'
import { TicketCenter } from '@/features/adty-ticket/ticket-center'
import { useAuthStore } from '@/stores/auth-store'

// The platform ticket workspace: Root and main-site admins with ticket permission.
export const Route = createFileRoute('/_authenticated/admin-tickets/')({
  beforeLoad: () => {
    if (!canOpenPlatformTickets(useAuthStore.getState().auth.user)) {
      throw redirect({ to: '/403' })
    }
  },
  component: AdminTicketsPage,
})

function AdminTicketsPage() {
  const { t } = useTranslation()
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Ticket management')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t(
          'Claim, assign, reply to and close support tickets within your authorized scope.'
        )}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-5'>
          <TicketCenter scope='platform' />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
