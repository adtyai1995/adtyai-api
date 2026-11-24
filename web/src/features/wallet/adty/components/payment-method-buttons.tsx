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

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { resolveMethodBonusPercent } from '../api'
import { methodLabel, paymentMethodKey, paymentMethodLabels } from '../lib/methods'
import type { AdtyMethod } from '../types'
import { PaymentMethodIcon } from './payment-logos'

type PaymentMethodButtonsProps = {
  methods: AdtyMethod[]
  /** paymentMethodKey of the chosen button, or null. */
  selected: string | null
  onSelect: (key: string) => void
  disabled?: boolean
  /** Optional active per-method recharge bonus rates (e.g. { alipay: "0.05", usdt: "0.025" }). */
  methodBonusRates?: Record<string, string>
}

/** One button per payment method: each USDT chain and each Epay channel. */
export function PaymentMethodButtons(props: PaymentMethodButtonsProps) {
  const { t } = useTranslation()
  const labels = paymentMethodLabels(
    props.methods.map((item) => item.label || t(methodLabel(item.id)))
  )
  return (
    <div className='grid grid-cols-2 gap-2.5 sm:grid-cols-3'>
      {props.methods.map((item, index) => {
        const key = paymentMethodKey(item)
        const bonusPercent = item.available
          ? resolveMethodBonusPercent(item.id, props.methodBonusRates)
          : null
        return (
          <Button
            key={key}
            type='button'
            variant='outline'
            disabled={props.disabled || !item.available}
            aria-pressed={props.selected === key}
            className={cn(
              'h-auto min-h-14 justify-start px-3 py-2.5 text-left whitespace-normal',
              props.selected === key
                ? 'border-foreground bg-foreground/5'
                : 'border-muted'
            )}
            onClick={() => props.onSelect(key)}
          >
            <div className='flex w-full min-w-0 items-center gap-2.5'>
              <PaymentMethodIcon
                method={item.id}
                className='size-6 shrink-0'
              />
              <div className='flex min-w-0 flex-1 flex-col items-start gap-1'>
                <span className='w-full truncate text-sm leading-tight font-medium'>
                  {labels[index]}
                </span>
                {!item.available && (
                  <span className='text-muted-foreground text-xs leading-none'>
                    {t('Unavailable')}
                  </span>
                )}
                {item.available && bonusPercent && (
                  <span className='bg-emerald-500/12 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300 inline-flex items-center rounded-full px-1.5 py-0.5 text-[11px] leading-none font-medium tabular-nums'>
                    {t('Bonus +{{percent}}%', { percent: bonusPercent })}
                  </span>
                )}
              </div>
            </div>
          </Button>
        )
      })}
    </div>
  )
}
