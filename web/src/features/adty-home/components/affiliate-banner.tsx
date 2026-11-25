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

import { buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { HOME_CONTAINER, HOME_PRIMARY_BUTTON } from './home-styles'

/**
 * Referral card before the quick start. Hidden while the program is off or
 * its settings are unknown; without a default rate it promises no number.
 */
export function AffiliateBanner(props: { open: boolean; rate: number | null }) {
  const { t } = useTranslation()
  if (!props.open) return null
  return (
    <section aria-labelledby='affiliate-title' className='pb-24 md:pb-28'>
      <div className={HOME_CONTAINER}>
        <div className='bg-muted grid gap-8 overflow-hidden rounded-[28px] px-7 py-12 md:grid-cols-[minmax(0,1fr)_auto] md:items-center md:px-14 md:py-16'>
          <div>
            <h2
              id='affiliate-title'
              className='text-[clamp(1.9rem,3.4vw,2.8rem)] leading-[1.12] font-extrabold tracking-[-0.03em] text-balance'
            >
              {t('Invite friends, earn on every top-up')}
            </h2>
            <p className='text-muted-foreground mt-4 max-w-2xl text-[16px] leading-[1.8]'>
              {props.rate !== null
                ? t(
                    'Earn {{rate}}% of every top-up the people you invite make, for as long as they stay.',
                    { rate: props.rate }
                  )
                : t(
                    'Earn a commission on every top-up the people you invite make, for as long as they stay.'
                  )}
            </p>
          </div>
          {props.rate !== null && (
            <p
              aria-hidden
              className='text-primary text-[clamp(4rem,9vw,7rem)] leading-none font-extrabold tracking-[-0.05em] md:order-none'
            >
              {props.rate}
              <span className='text-foreground/30 align-super text-[0.4em]'>
                %
              </span>
            </p>
          )}
          <a
            href='/affiliate'
            className={cn(
              buttonVariants({ size: 'lg' }),
              HOME_PRIMARY_BUTTON,
              'justify-self-start md:col-span-2'
            )}
          >
            {t('Affiliate Program')}
          </a>
        </div>
      </div>
    </section>
  )
}
