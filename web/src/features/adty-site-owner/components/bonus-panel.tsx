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
import { PaymentMethodIcon } from '@/features/wallet/adty/components/payment-logos'

import { saveOwnerSiteMethodBonus } from '../api'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import type { OwnerSite } from '../types'

type MethodRow = { method: string; enabled: boolean; rate: string }

/** Canonical payment-method keys are stored, the labels are translated. */
const METHOD_LABELS: Record<string, string> = {
  usdt: 'USDT',
}

function isSupportedBonusMethod(method: string): boolean {
  return method.toLowerCase().trim() === 'usdt'
}

function formatBonusRatePercent(raw: string): string {
  const trimmed = raw.trim()
  if (!/^(?:0(?:\.\d+)?|1(?:\.0+)?)$/.test(trimmed)) return ''
  const num = Number(trimmed)
  if (!Number.isFinite(num) || num < 0 || num > 1) return ''
  const pct = Number((num * 100).toFixed(4))
  return `= ${pct}%`
}

const rowValue = (row: MethodRow) =>
  JSON.stringify({ enabled: row.enabled, rate: row.rate })

/** USDT recharge bonus rule for this site; the bonus is paid from the owner's earnings. */
export function BonusPanel(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const saved = (props.site.bonus_methods ?? []).filter((row) =>
    isSupportedBonusMethod(row.method)
  )
  const savedRef = useRef<Map<string, string>>(
    new Map(saved.map((row) => [row.method, rowValue(row)]))
  )
  const [rows, setRows] = useState<MethodRow[]>(() =>
    saved.map(({ method, enabled, rate }) => ({ method, enabled, rate }))
  )
  useEffect(() => {
    const incoming = (props.site.bonus_methods ?? [])
      .filter((row) => isSupportedBonusMethod(row.method))
      .map(({ method, enabled, rate }) => ({
        method,
        enabled,
        rate,
      }))
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
      <p className='text-dim max-w-[80ch] text-[13px] leading-relaxed'>
        {t(
          'Configure the USDT recharge bonus rate for your site (applies to all USDT networks). Eligible customer USDT recharges receive the configured bonus paid from your earnings.'
        )}{' '}
        {t('A rate of 0.025 grants 2.5% of the recharge amount.')}
      </p>
      {rows.length === 0 && (
        <EmptyState
          className='min-h-24'
          title={t('USDT payment is not currently enabled on the main site.')}
        />
      )}
      {rows.length > 0 && (
        <ul className='border-border divide-border divide-y overflow-hidden rounded-[14px] border'>
          {rows.map((row) => {
            const unchanged = savedByMethod.get(row.method) === rowValue(row)
            const percentHint = formatBonusRatePercent(row.rate)
            return (
              <li
                key={row.method}
                className='grid items-center gap-x-6 gap-y-3 px-5 py-4 sm:grid-cols-[minmax(9rem,13rem)_minmax(0,1fr)_auto]'
              >
                <div className='flex items-center gap-3'>
                  <Switch
                    id={`bonus-method-${row.method}`}
                    checked={row.enabled}
                    disabled={closing}
                    onCheckedChange={(enabled) =>
                      update(row.method, { enabled })
                    }
                  />
                  <PaymentMethodIcon
                    method={row.method}
                    className='size-6 shrink-0'
                  />
                  <Label
                    htmlFor={`bonus-method-${row.method}`}
                    className='text-[14px] font-semibold'
                  >
                    {t(METHOD_LABELS[row.method] ?? row.method)}
                  </Label>
                </div>
                <div className='flex items-center gap-3'>
                  <Label
                    htmlFor={`bonus-rate-${row.method}`}
                    className='text-dim shrink-0 text-[13px] font-normal'
                  >
                    {t('Recharge bonus rate')}
                  </Label>
                  <Input
                    id={`bonus-rate-${row.method}`}
                    className='w-32 font-mono tabular-nums'
                    inputMode='decimal'
                    autoComplete='off'
                    placeholder='0.025'
                    value={row.rate}
                    disabled={closing}
                    onChange={(event) =>
                      update(row.method, { rate: event.target.value })
                    }
                  />
                  <span className='text-dim w-16 font-mono text-xs tabular-nums'>
                    {percentHint}
                  </span>
                </div>
                <Button
                  type='button'
                  variant='brand'
                  size='sm'
                  className='justify-self-start sm:justify-self-end'
                  disabled={closing || save.isPending || unchanged}
                  onClick={() => save.mutate(row)}
                >
                  {t('Save')}
                </Button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
