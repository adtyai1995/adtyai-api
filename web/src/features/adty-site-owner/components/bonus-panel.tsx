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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'

import { saveOwnerSiteMethodBonus } from '../api'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import type { OwnerSite } from '../types'

type MethodRow = { method: string; enabled: boolean; rate: string }

/** Canonical payment-method keys are stored, the labels are translated. */
const METHOD_LABELS: Record<string, string> = {
  alipay: 'Alipay',
  wxpay: 'WeChat Pay',
  usdt: 'USDT',
}

const rowValue = (row: MethodRow) =>
  JSON.stringify({ enabled: row.enabled, rate: row.rate })

/** Recharge bonus rules by payment method for this site; the bonus is paid from the owner's earnings. */
export function BonusPanel(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const saved = props.site.bonus_methods ?? []
  const savedRef = useRef<Map<string, string>>(
    new Map(saved.map((row) => [row.method, rowValue(row)]))
  )
  const [rows, setRows] = useState<MethodRow[]>(() =>
    saved.map(({ method, enabled, rate }) => ({ method, enabled, rate }))
  )
  useEffect(() => {
    const incoming = (props.site.bonus_methods ?? []).map(
      ({ method, enabled, rate }) => ({
        method,
        enabled,
        rate,
      })
    )
    const prevSaved = savedRef.current
    setRows((current) => {
      const currentByMethod = new Map(current.map((row) => [row.method, row]))
      return incoming.map((serverRow) => {
        const existing = currentByMethod.get(serverRow.method)
        const prevBaseline = prevSaved.get(serverRow.method)
        const serverSerialized = rowValue(serverRow)
        if (
          existing &&
          prevBaseline !== undefined &&
          rowValue(existing) !== prevBaseline &&
          serverSerialized === prevBaseline
        ) {
          return existing
        }
        return serverRow
      })
    })
    savedRef.current = new Map(
      incoming.map((row) => [row.method, rowValue(row)])
    )
  }, [props.site.bonus_methods])
  const savedByMethod = new Map(saved.map((row) => [row.method, rowValue(row)]))
  useDraftNavigation(
    rows.some((row) => savedByMethod.get(row.method) !== rowValue(row))
  )
  const save = useVerifiedOwnerMutation<MethodRow>({
    action: 'bonus',
    target: () => props.site.id,
    run: (row, proof) =>
      saveOwnerSiteMethodBonus(row.method, row.enabled, row.rate.trim(), proof),
  })
  const closing = props.site.status !== 'active'
  const update = (method: string, patch: Partial<MethodRow>) =>
    setRows((prev) =>
      prev.map((row) => (row.method === method ? { ...row, ...patch } : row))
    )

  return (
    <div className='flex flex-col gap-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Configure recharge bonus rates by payment method. Eligible customer recharges receive the configured bonus paid from your earnings.'
        )}
      </p>
      {rows.length === 0 && (
        <EmptyState
          className='min-h-24'
          title={t('No payment methods are currently enabled on the main site.')}
        />
      )}
      {rows.map((row) => (
        <div
          key={row.method}
          className='flex flex-col gap-3 border-b pb-4 last:border-b-0 last:pb-0'
        >
          <div className='flex items-center justify-between gap-4'>
            <Label htmlFor={`bonus-method-${row.method}`}>
              {t(METHOD_LABELS[row.method] ?? row.method)}
            </Label>
            <Switch
              id={`bonus-method-${row.method}`}
              checked={row.enabled}
              disabled={closing}
              onCheckedChange={(enabled) => update(row.method, { enabled })}
            />
          </div>
          <div className='flex flex-col gap-1'>
            <Label htmlFor={`bonus-rate-${row.method}`}>
              {t('Recharge bonus rate')}
            </Label>
            <Input
              id={`bonus-rate-${row.method}`}
              inputMode='decimal'
              autoComplete='off'
              value={row.rate}
              disabled={closing}
              onChange={(event) =>
                update(row.method, { rate: event.target.value })
              }
            />
            <span className='text-muted-foreground text-sm'>
              {t('A rate of 0.025 grants 2.5% of the recharge amount.')}
            </span>
          </div>
          <Button
            type='button'
            variant='brand'
            size='sm'
            className='self-start'
            disabled={
              closing ||
              save.isPending ||
              savedByMethod.get(row.method) === rowValue(row)
            }
            onClick={() => save.mutate(row)}
          >
            {t('Save')}
          </Button>
        </div>
      ))}
    </div>
  )
}
