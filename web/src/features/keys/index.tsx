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
import { getRouteApi } from '@tanstack/react-router'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'

import { ApiKeysDialogs } from './components/api-keys-dialogs'
import { ApiKeysPrimaryButtons } from './components/api-keys-primary-buttons'
import { ApiKeysProvider, useApiKeys } from './components/api-keys-provider'
import { ApiKeysTable } from './components/api-keys-table'

const route = getRouteApi('/_authenticated/keys/')

function ApiKeysCreateFromSearch() {
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const { setOpen, setInitialCreateGroup } = useApiKeys()

  useEffect(() => {
    if (search.create !== '1') return
    setInitialCreateGroup(search.group ?? null)
    setOpen('create')
    void navigate({
      search: (prev) => ({
        ...prev,
        create: undefined,
        group: undefined,
      }),
      replace: true,
    })
  }, [navigate, search.create, search.group, setInitialCreateGroup, setOpen])

  return null
}

export function ApiKeys() {
  const { t } = useTranslation()
  return (
    <ApiKeysProvider>
      <ApiKeysCreateFromSearch />
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('API Keys')}</SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {t('Create a key for your app or service')}
        </SectionPageLayout.Description>
        <SectionPageLayout.Actions>
          <ApiKeysPrimaryButtons />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <ApiKeysTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ApiKeysDialogs />
    </ApiKeysProvider>
  )
}
