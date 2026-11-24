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
import { RotateCw } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { cn } from '@/lib/utils'

import {
  AUTO_REFRESH_INTERVAL_OPTIONS,
  type AggregateHealthState,
  type AutoRefreshInterval,
} from '../lib/channel-status'

function getAggregateHealthPresentation(state: AggregateHealthState): {
  labelKey: string
  dotClass: string
  textClass: string
} {
  switch (state) {
    case 'operational':
      return {
        labelKey: 'All operational',
        dotClass: 'bg-success',
        textClass: 'text-success',
      }
    case 'degraded':
      return {
        labelKey: 'Partial degradation',
        dotClass: 'bg-warning',
        textClass: 'text-warning',
      }
    case 'disrupted':
      return {
        labelKey: 'Service disruption',
        dotClass: 'bg-destructive',
        textClass: 'text-destructive',
      }
    default:
      return {
        labelKey: 'No data',
        dotClass: 'bg-muted-foreground/50',
        textClass: 'text-muted-foreground',
      }
  }
}

export interface AutoRefreshControlsProps {
  aggregateHealth: AggregateHealthState
  intervalSeconds: AutoRefreshInterval
  isFetching: boolean
  refreshCycleKey: number
  onIntervalChange: (next: AutoRefreshInterval) => void
  onRefresh: () => void
}

export function AutoRefreshControls(props: AutoRefreshControlsProps) {
  const { t } = useTranslation()
  const onRefreshRef = useRef(props.onRefresh)
  onRefreshRef.current = props.onRefresh

  const [remainingSeconds, setRemainingSeconds] = useState<number>(
    props.intervalSeconds > 0 ? props.intervalSeconds : 60
  )

  useEffect(() => {
    if (props.intervalSeconds <= 0) return
    let left = props.intervalSeconds
    setRemainingSeconds(left)
    const timer = window.setInterval(() => {
      left -= 1
      if (left <= 0) {
        left = props.intervalSeconds
        setRemainingSeconds(left)
        onRefreshRef.current()
      } else {
        setRemainingSeconds(left)
      }
    }, 1000)
    return () => window.clearInterval(timer)
  }, [props.intervalSeconds, props.refreshCycleKey])

  const healthMeta = getAggregateHealthPresentation(props.aggregateHealth)

  const handleIntervalSelect = (raw: string) => {
    const parsed = Number(raw)
    if (parsed === 30 || parsed === 60 || parsed === 120 || parsed === 0) {
      props.onIntervalChange(parsed)
    }
  }

  return (
    <div className='flex flex-wrap items-center gap-2.5'>
      <div
        className={cn(
          'inline-flex items-center gap-1.5 text-xs font-medium',
          healthMeta.textClass
        )}
      >
        <span
          className={cn('size-1.5 rounded-full', healthMeta.dotClass)}
          aria-hidden='true'
        />
        <span>{t(healthMeta.labelKey)}</span>
      </div>

      <Button
        type='button'
        variant='outline'
        size='icon'
        onClick={props.onRefresh}
        disabled={props.isFetching}
        aria-label={t('Refresh')}
        className='size-8 rounded-full shadow-none'
      >
        <RotateCw
          className={cn('size-3.5', props.isFetching && 'animate-spin')}
          aria-hidden='true'
        />
      </Button>

      <NativeSelect
        size='sm'
        aria-label={t('Auto-refresh')}
        value={String(props.intervalSeconds)}
        onChange={(e) => handleIntervalSelect(e.target.value)}
        className='w-20'
      >
        {AUTO_REFRESH_INTERVAL_OPTIONS.map((sec) => (
          <NativeSelectOption key={sec} value={String(sec)}>
            {sec === 0 ? t('Off') : `${sec}s`}
          </NativeSelectOption>
        ))}
      </NativeSelect>

      {props.intervalSeconds > 0 && (
        <span
          aria-live='off'
          className='text-muted-foreground min-w-8 text-right font-mono text-xs tabular-nums'
        >
          {remainingSeconds}s
        </span>
      )}
    </div>
  )
}
