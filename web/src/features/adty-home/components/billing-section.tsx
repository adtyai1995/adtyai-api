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

import { cn } from '@/lib/utils'

import { HOME_CONTAINER } from './home-styles'

/**
 * The billing mechanics, set under one oversized word: two statements and
 * the four rules behind them. Every line is a rule the gateway enforces.
 */
export function BillingSection() {
  const { t } = useTranslation()
  const statements = [t('Priced when accepted.'), t('Settled on actual usage.')]
  const rules = [
    t(
      'A price snapshot is taken when your request is accepted; a later price change does not touch it.'
    ),
    t(
      'An estimate is held first, then the request settles on real usage and the difference comes back.'
    ),
    t('A failed, cancelled or interrupted request returns what was held.'),
    t('Your usage log shows the model, tokens and cost of every request.'),
  ]

  return (
    <section
      aria-labelledby='billing-title'
      className='overflow-hidden py-24 md:py-32'
    >
      <div className={HOME_CONTAINER}>
        <p
          aria-hidden
          className='text-foreground/[0.06] -ml-1 text-[clamp(4.5rem,13vw,10rem)] leading-[0.9] font-black tracking-[-0.045em] select-none'
        >
          {t('Billing')}
        </p>
        <h2 id='billing-title' className='sr-only'>
          {t('Billing')}
        </h2>
        <div className='mt-6 grid gap-10 lg:grid-cols-2 lg:gap-16'>
          <div>
            {statements.map((statement, index) => (
              <p
                key={statement}
                className={cn(
                  'font-display border-border border-t py-7 text-[clamp(1.6rem,2.8vw,2.4rem)] font-extrabold tracking-[-0.025em]',
                  index === 1 && 'text-primary border-b'
                )}
              >
                {statement}
              </p>
            ))}
          </div>
          <ul className='text-foreground/70 space-y-5 text-[17px] leading-[1.75] lg:pt-7'>
            {rules.map((rule) => (
              <li key={rule} className='flex gap-3'>
                <span
                  aria-hidden
                  className='bg-primary mt-[0.7em] size-1.5 shrink-0 rounded-full'
                />
                {rule}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  )
}
