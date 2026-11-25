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
import { useTranslation } from 'react-i18next'

import { TruncatedCell } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { useGroupDisplay } from '@/features/adty-groups/hooks/use-group-display'
import { useMediaQuery } from '@/hooks'
import { cn } from '@/lib/utils'

import type { GroupRatio } from './auto-group-visuals'

type ApiKeyGroupCellProps = {
  crossGroupRetry?: boolean
  group: string
  ratio?: GroupRatio
  shouldReduceMotion?: boolean
}

export function ApiKeyGroupCell(props: ApiKeyGroupCellProps) {
  const { t } = useTranslation()
  const groupDisplay = useGroupDisplay()
  const isMobile = useMediaQuery('(max-width: 640px)')

  const group = props.group?.trim() || ''
  if (!group) {
    return (
      <span className='text-muted-foreground text-xs'>{t('Not set')}</span>
    )
  }

  const displayName = groupDisplay.name(group)
  const ratio = typeof props.ratio === 'number' ? props.ratio : undefined
  return (
    <TruncatedCell
      className={isMobile ? 'w-full' : 'max-w-50'}
      tabIndex={0}
      tooltipContent={displayName}
      tooltipClassName='break-all'
    >
      <GroupBadge
        group={group}
        label={displayName}
        ratio={ratio}
        type='text'
        className='px-0'
        containerClassName={cn('gap-3', isMobile && 'w-full justify-between')}
      />
    </TruncatedCell>
  )
}
