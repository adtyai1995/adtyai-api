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
import { ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { HOME_PRIMARY_BUTTON, HOME_SECONDARY_BUTTON } from './home-styles'

/** City points on the 1440 × 536 dot map (public/adty-world-dots.svg). */
const POINTS = {
  sanFrancisco: [230, 161],
  newYork: [424, 149],
  london: [720, 106],
  frankfurt: [755, 112],
  dubai: [941, 211],
  singapore: [1135, 307],
  hongKong: [1177, 223],
  shanghai: [1206, 187],
  tokyo: [1279, 169],
  sydney: [1325, 448],
} as const

type PointName = keyof typeof POINTS

const ARCS: Array<[PointName, PointName]> = [
  ['sanFrancisco', 'newYork'],
  ['newYork', 'london'],
  ['london', 'frankfurt'],
  ['frankfurt', 'dubai'],
  ['frankfurt', 'shanghai'],
  ['dubai', 'singapore'],
  ['hongKong', 'tokyo'],
  ['singapore', 'sydney'],
]

/** A raised quadratic curve between two map points. */
function arcPath(from: PointName, to: PointName): string {
  const [x1, y1] = POINTS[from]
  const [x2, y2] = POINTS[to]
  const lift = Math.hypot(x2 - x1, y2 - y1) * 0.42
  const cx = (x1 + x2) / 2
  const cy = Math.min(y1, y2) - lift
  return `M${x1} ${y1}Q${cx} ${cy} ${x2} ${y2}`
}

const ENDPOINTS = [...new Set(ARCS.flat())]

/**
 * First screen: the dotted world map with drawn arcs fills the top, the
 * headline sits bottom-left, and the protocol pills with both actions sit
 * bottom-right. Built from props only (prerendered at build time).
 */
export function Hero(props: {
  signedIn: boolean
  docsHref?: string
  docsExternal?: boolean
}) {
  const { t } = useTranslation()
  const firstLine = t('One API,')
  const apiAt = firstLine.indexOf('API')
  const pills = [
    t('OpenAI compatible · Responses'),
    t('Anthropic Messages, native'),
    t('Gemini, native'),
  ]

  return (
    <section
      id='hero'
      aria-labelledby='hero-title'
      className='relative isolate flex min-h-svh flex-col justify-end overflow-hidden px-5 pt-28 pb-16 sm:px-8 md:px-10 md:pb-20'
    >
      <div
        aria-hidden
        className='pointer-events-none absolute inset-x-0 top-20 -z-10 mx-auto aspect-[1440/536] w-[170%] max-w-[1440px] -translate-x-[20%] sm:w-full sm:translate-x-0 md:top-16'
      >
        <img
          src='/adty-world-dots.svg'
          width={1440}
          height={536}
          alt=''
          className='size-full select-none dark:opacity-40'
          draggable={false}
        />
        <svg
          viewBox='0 0 1440 536'
          className='absolute inset-0 size-full overflow-visible'
          fill='none'
        >
          {ARCS.map(([from, to], index) => (
            <path
              key={`${from}-${to}`}
              d={arcPath(from, to)}
              pathLength={1}
              className='stroke-primary/70 motion-safe:animate-[adty-arc-draw_1.8s_cubic-bezier(0.16,1,0.3,1)_both]'
              style={{ animationDelay: `${index * 110}ms` }}
              strokeWidth={1.6}
              strokeDasharray='1'
              strokeLinecap='round'
            />
          ))}
          {ENDPOINTS.map((name) => (
            <g key={name}>
              <circle
                cx={POINTS[name][0]}
                cy={POINTS[name][1]}
                r={9}
                className='fill-primary/12'
              />
              <circle
                cx={POINTS[name][0]}
                cy={POINTS[name][1]}
                r={3.6}
                className='fill-primary'
              />
            </g>
          ))}
        </svg>
      </div>
      <div
        aria-hidden
        className='from-background via-background/85 pointer-events-none absolute inset-x-0 bottom-0 -z-10 h-[52%] bg-linear-to-t to-transparent'
      />

      <div className='mx-auto grid w-full max-w-[1320px] gap-10 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end'>
        <div className='min-w-0'>
          <h1
            id='hero-title'
            className='font-display text-[clamp(2.1rem,5.6vw,5.2rem)] leading-[1.08] font-extrabold tracking-[-0.035em] [word-break:keep-all] lg:*:whitespace-nowrap'
          >
            <span className='block'>
              {apiAt < 0 ? (
                firstLine
              ) : (
                <>
                  {firstLine.slice(0, apiAt)}
                  <span className='text-primary'>API</span>
                  {firstLine.slice(apiAt + 3)}
                </>
              )}
            </span>
            <span className='block'>{t('for Claude, GPT,')}</span>
            <span className='block'>{t('Gemini and the rest')}</span>
          </h1>
          <p className='text-muted-foreground mt-7 max-w-[38rem] text-[17px] leading-[1.75] text-pretty [word-break:keep-all]'>
            {t(
              'OpenAI, Anthropic and Gemini requests in their native shapes, on one host. Keep the SDK and the auth header you already send.'
            )}
          </p>
        </div>

        <div className='flex flex-col gap-7 lg:items-start lg:pb-2'>
          <ul className='flex flex-wrap gap-2.5 lg:flex-col lg:items-start'>
            {pills.map((pill) => (
              <li
                key={pill}
                className='border-border bg-background/85 text-foreground/80 flex items-center gap-2.5 rounded-full border py-1.5 pr-4 pl-2.5 text-sm backdrop-blur-sm'
              >
                <span
                  aria-hidden
                  className='border-primary size-3 shrink-0 rounded-full border-[2.5px]'
                />
                {pill}
              </li>
            ))}
          </ul>
          <div className='flex flex-wrap gap-3'>
            <a
              href={props.signedIn ? '/dashboard' : '/sign-up'}
              className={cn(
                buttonVariants({ size: 'lg' }),
                HOME_PRIMARY_BUTTON
              )}
            >
              {props.signedIn ? t('Go to console') : t('Get a key')}
            </a>
            <a
              href={props.docsHref || '/docs'}
              target={props.docsExternal ? '_blank' : undefined}
              rel={props.docsExternal ? 'noopener noreferrer' : undefined}
              className={cn(
                buttonVariants({ variant: 'outline', size: 'lg' }),
                HOME_SECONDARY_BUTTON
              )}
            >
              {t('Read the docs')}
            </a>
          </div>
        </div>
      </div>
      <a
        href='#models'
        aria-label={t('Scroll to the models')}
        className='text-muted-foreground hover:text-primary absolute inset-x-0 bottom-5 mx-auto hidden w-fit p-2 transition-colors md:block'
      >
        <ChevronDown aria-hidden className='size-5' />
      </a>
    </section>
  )
}
