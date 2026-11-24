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
import { Globe, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { useApiBaseUrl } from '@/features/adty-site/api'

import { useApiKeys } from './api-keys-provider'

export function ApiKeysPrimaryButtons() {
  const { t } = useTranslation()
  const { setOpen } = useApiKeys()
  const apiBaseUrl = useApiBaseUrl()

  return (
    <div className='flex flex-wrap items-center gap-2'>
      {apiBaseUrl ? (
        <div
          className='bg-muted/50 border-border/80 flex h-8 max-w-full min-w-0 items-center gap-1.5 rounded-md border pr-1 pl-2.5 text-xs'
          aria-label={t('API Address')}
        >
          <Globe
            aria-hidden='true'
            className='text-muted-foreground size-3.5 shrink-0'
          />
          <span className='text-muted-foreground hidden shrink-0 sm:inline'>
            {t('API Address')}:
          </span>
          <code
            className='text-foreground max-w-[180px] truncate font-mono text-xs select-all sm:max-w-[260px]'
            title={apiBaseUrl}
          >
            {apiBaseUrl}
          </code>
          <CopyButton
            value={apiBaseUrl}
            size='icon'
            className='size-6'
            iconClassName='size-3.5'
            tooltip={t('Copy API URL')}
            aria-label={`${t('Copy API URL')}: ${apiBaseUrl}`}
          />
        </div>
      ) : (
        <div className='bg-muted/40 border-border/60 text-muted-foreground flex h-8 items-center gap-1.5 rounded-md border px-2.5 text-xs'>
          <Globe aria-hidden='true' className='size-3.5 shrink-0' />
          <span>{t('API address is not configured yet')}</span>
        </div>
      )}
      <Button variant='brand' size='sm' onClick={() => setOpen('create')}>
        <Plus className='h-4 w-4' />
        {t('Create API Key')}
      </Button>
    </div>
  )
}
