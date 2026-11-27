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
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import { HOME_CONTAINER } from './home-styles'

const LOG_ROWS = [
  { model: 'gpt-4o-mini', cost: '$0.0004' },
  { model: 'claude-…', cost: '$0.0429' },
  { model: 'gemini-…', cost: '$0.0021' },
]

function Tile(props: {
  className?: string
  tone?: 'gray' | 'blue'
  title: string
  children: ReactNode
}) {
  const tone = props.tone ?? 'gray'
  return (
    <article
      className={cn(
        'flex flex-col rounded-[24px] p-7 md:p-8',
        tone === 'gray' && 'bg-muted',
        tone === 'blue' && 'bg-primary text-primary-foreground',
        props.className
      )}
    >
      <h3 className='font-display text-[1.6rem] leading-tight font-extrabold tracking-[-0.02em]'>
        {props.title}
      </h3>
      <div
        aria-hidden
        className={cn(
          'mt-4 h-px w-10',
          tone === 'gray' ? 'bg-foreground/15' : 'bg-white/25'
        )}
      />
      {props.children}
    </article>
  )
}

/**
 * What a signed-in account controls, as a bento of tiles: key limits, the
 * usage log, team accounts, payment methods, the probe window, languages
 * and support tickets.
 */
export function CapabilityBento() {
  const { t } = useTranslation()
  const keyLimits = [
    t('Quota cap'),
    t('Expiry'),
    t('Allowed models'),
    t('Group'),
    t('IP allowlist'),
    'RPM',
    'TPM',
    t('Concurrency'),
  ]
  const payments = [
    t('Alipay'),
    t('WeChat Pay'),
    'USDT · TRC20',
    'USDT · ERC20',
    'USDT · BEP20',
    'USDT · Polygon',
    'USDT · X Layer',
  ]

  return (
    <section
      aria-labelledby='control-title'
      className='overflow-hidden pb-24 md:pb-32'
    >
      <div className={HOME_CONTAINER}>
        <p
          aria-hidden
          className='text-foreground/[0.06] -ml-1 text-[clamp(4.5rem,13vw,10rem)] leading-[0.9] font-black tracking-[-0.045em] select-none'
        >
          {t('Control')}
        </p>
        <h2 id='control-title' className='sr-only'>
          {t('Control')}
        </h2>

        <div className='mt-4 grid gap-5 md:grid-cols-6 lg:grid-cols-12'>
          <Tile
            title={t('Key controls')}
            className='md:col-span-6 lg:col-span-6'
          >
            <p className='text-muted-foreground mt-5 max-w-md text-[15px] leading-[1.7]'>
              {t('Each key carries its own limits, set when you create it.')}
            </p>
            <ul className='mt-auto flex flex-wrap gap-2 pt-8'>
              {keyLimits.map((limit) => (
                <li
                  key={limit}
                  className='bg-background text-foreground/80 rounded-full border px-3.5 py-1.5 text-sm'
                >
                  {limit}
                </li>
              ))}
            </ul>
          </Tile>

          <Tile title={t('Usage log')} className='md:col-span-3 lg:col-span-3'>
            <p className='text-muted-foreground mt-5 text-[15px] leading-[1.7]'>
              {t('Every request, with its model, tokens and cost.')}
            </p>
            <div className='bg-background mt-auto rounded-xl px-4 py-3 font-mono text-[12px] leading-[2]'>
              <p className='text-muted-foreground font-sans text-xs'>
                {t('Example')}
              </p>
              {LOG_ROWS.map((row) => (
                <p key={row.model} className='flex justify-between gap-2'>
                  <span className='truncate'>{row.model}</span>
                  <span className='text-primary tabular-nums'>{row.cost}</span>
                </p>
              ))}
            </div>
          </Tile>

          <Tile
            title={t('Team accounts')}
            className='md:col-span-3 lg:col-span-3'
          >
            <p className='text-muted-foreground mt-5 text-[15px] leading-[1.7]'>
              {t(
                'A shared balance and team keys. A bound key spends the team balance, never a member’s own wallet.'
              )}
            </p>
            <p className='text-foreground/50 mt-auto pt-6 text-sm'>
              {t('Teams are opened by the platform.')}
            </p>
          </Tile>

          <Tile
            title={t('Payment')}
            className='md:col-span-3 lg:col-span-3 lg:row-span-2'
          >
            <ul className='divide-border mt-4 divide-y'>
              {payments.map((method) => (
                <li key={method} className='py-2.5 text-[15px] font-medium'>
                  {method}
                </li>
              ))}
            </ul>
            <p className='text-muted-foreground mt-auto pt-6 text-sm leading-[1.7]'>
              {t(
                'Your wallet shows the methods this site offers. Payment-method top-up bonuses never expire.'
              )}
            </p>
          </Tile>

          <Tile
            title={t('Probe window')}
            className='md:col-span-3 lg:col-span-6'
          >
            <p className='mt-5 flex items-start gap-1 font-extrabold tracking-[-0.04em]'>
              <span className='text-[clamp(4rem,8vw,6rem)] leading-none'>
                24
              </span>
              <span className='text-foreground/35 mt-2 text-3xl'>h</span>
            </p>
            <p className='text-muted-foreground mt-4 max-w-md text-[15px] leading-[1.7]'>
              {t(
                'Average latency and success rate for each group, on the Channel status page once you sign in.'
              )}
            </p>
          </Tile>

          <Tile
            tone='blue'
            title={t('Support tickets')}
            className='md:col-span-6 lg:col-span-3 lg:row-span-2'
          >
            <p className='mt-5 text-[15px] leading-[1.7] text-white/85'>
              {t(
                'Open a ticket from the console, attach screenshots, and follow the replies in the Support Center.'
              )}
            </p>
            <p className='mt-auto pt-10 text-[clamp(3rem,5vw,4.25rem)] leading-none font-extrabold tracking-[-0.04em]'>
              {t('Tickets')}
            </p>
          </Tile>

          <Tile
            title={t('Chinese and English')}
            className='md:col-span-3 lg:col-span-6'
          >
            <p className='text-muted-foreground mt-5 max-w-md text-[15px] leading-[1.7]'>
              {t(
                'The console, notifications and docs, in Simplified Chinese and English.'
              )}
            </p>
          </Tile>
        </div>
      </div>
    </section>
  )
}
