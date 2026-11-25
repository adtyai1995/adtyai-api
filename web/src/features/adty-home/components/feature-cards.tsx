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

import { DEFAULT_SNIPPET_MODEL } from '@/features/adty-site/client-snippets'
import { cn } from '@/lib/utils'

import { HOME_CONTAINER, HOME_SECTION_TITLE } from './home-styles'

const PROTOCOL_ROUTES = [
  { path: 'POST /v1/chat/completions', auth: 'Bearer' },
  { path: 'POST /v1/responses', auth: 'Bearer' },
  { path: 'POST /v1/messages', auth: 'x-api-key' },
  { path: 'POST /v1beta/models/…', auth: 'x-goog-api-key' },
]

/** 24 hourly probe slots; the last four illustrate hours with no sample. */
const PROBE_SLOTS = Array.from({ length: 24 }, (_, hour) => ({
  hour,
  sampled: hour < 20,
}))

function ProtocolVisual() {
  return (
    <ul className='flex w-full flex-col gap-2'>
      {PROTOCOL_ROUTES.map((route) => (
        <li
          key={route.path}
          className='bg-background shadow-foreground/30 flex items-center justify-between gap-3 rounded-xl px-3.5 py-2.5 font-mono text-[12.5px] shadow-[0_6px_16px_-12px]'
        >
          <span className='truncate'>{route.path}</span>
          <span className='text-primary shrink-0'>{route.auth}</span>
        </li>
      ))}
    </ul>
  )
}

function MigrationVisual(props: { baseUrl: string }) {
  return (
    <div className='bg-background shadow-foreground/30 w-full overflow-hidden rounded-xl font-mono text-[12.5px] leading-[2.1] shadow-[0_6px_16px_-12px]'>
      <p className='bg-destructive/8 text-destructive truncate px-3.5'>
        - base_url = &quot;https://api.openai.com/v1&quot;
      </p>
      <p className='bg-primary/8 text-primary truncate px-3.5'>
        + base_url = &quot;{props.baseUrl}/v1&quot;
      </p>
      <p className='text-muted-foreground truncate px-3.5'>
        {'  '}model = &quot;{DEFAULT_SNIPPET_MODEL}&quot;
      </p>
    </div>
  )
}

function BillingVisual() {
  const { t } = useTranslation()
  const rows = [
    { label: t('Held when accepted'), value: '$0.0120' },
    { label: t('Used by the request'), value: '$0.0083' },
    { label: t('Returned to you'), value: '$0.0037', accent: true },
  ]
  return (
    <div className='bg-background shadow-foreground/30 w-full rounded-xl px-4 py-3 shadow-[0_6px_16px_-12px]'>
      <p className='text-muted-foreground text-xs'>{t('Example amounts')}</p>
      <dl className='mt-2 divide-y text-sm'>
        {rows.map((row) => (
          <div key={row.label} className='flex justify-between gap-3 py-2'>
            <dt className='text-foreground/75'>{row.label}</dt>
            <dd
              className={cn(
                'font-mono tabular-nums',
                row.accent && 'text-primary font-semibold'
              )}
            >
              {row.value}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

function StatusVisual() {
  const { t } = useTranslation()
  return (
    <div className='bg-background shadow-foreground/30 w-full rounded-xl px-4 py-3.5 shadow-[0_6px_16px_-12px]'>
      <div className='flex items-center justify-between text-xs'>
        <span className='text-muted-foreground'>{t('Illustration')}</span>
        <span className='text-muted-foreground'>{t('Last 24 hours')}</span>
      </div>
      <div className='mt-3 flex gap-[3px]'>
        {PROBE_SLOTS.map((slot) => (
          <span
            key={slot.hour}
            className={cn(
              'h-9 flex-1 rounded-[3px]',
              slot.sampled
                ? 'bg-primary/75'
                : 'border-border border border-dashed'
            )}
          />
        ))}
      </div>
      <div className='text-muted-foreground mt-3 flex justify-between text-xs'>
        <span>{t('Latency · success rate')}</span>
        <span>{t('No sample: —')}</span>
      </div>
    </div>
  )
}

function FeatureCard(props: {
  index: string
  title: string
  body: string
  children: ReactNode
}) {
  return (
    <article className='flex flex-col'>
      <p className='text-primary text-[15px] font-bold tabular-nums'>
        [{props.index}]
      </p>
      <h3 className='mt-1 text-[17px] font-bold tracking-[0.01em]'>
        {props.title}
      </h3>
      <div className='bg-muted mt-4 flex min-h-[220px] items-center justify-center rounded-[22px] p-6'>
        {props.children}
      </div>
      <p className='text-foreground/75 mt-4 px-1 text-[14.5px] leading-[1.75] text-pretty'>
        {props.body}
      </p>
    </article>
  )
}

/**
 * "All the models, one API": a three-line statement on the left and four
 * numbered cards, each showing the mechanism instead of an illustration.
 */
export function FeatureCards(props: { baseUrl: string }) {
  const { t } = useTranslation()

  return (
    <section
      aria-labelledby='features-title'
      className='relative isolate overflow-hidden py-24 md:py-32'
    >
      <img
        src='/adty-world-dots.svg'
        width={1440}
        height={536}
        alt=''
        aria-hidden
        draggable={false}
        className='pointer-events-none absolute top-10 left-1/2 -z-10 w-[1440px] max-w-none -translate-x-1/2 opacity-45 select-none dark:opacity-15'
      />
      <div
        className={cn(
          HOME_CONTAINER,
          'grid gap-12 lg:grid-cols-[minmax(0,4fr)_minmax(0,8fr)] lg:gap-14'
        )}
      >
        <h2
          id='features-title'
          className={cn(
            HOME_SECTION_TITLE,
            'lg:sticky lg:top-32 lg:self-start'
          )}
        >
          <span className='block'>{t('All the models.')}</span>
          <span className='block'>{t('One API.')}</span>
          <span className='text-primary block'>{t('Pay for what runs.')}</span>
        </h2>

        <div className='grid gap-x-6 gap-y-12 sm:grid-cols-2'>
          <FeatureCard
            index='01'
            title={t('Native protocols')}
            body={t(
              'OpenAI Chat Completions, Responses, Anthropic Messages and Gemini, each accepted in its own request shape with its own auth header.'
            )}
          >
            <ProtocolVisual />
          </FeatureCard>
          <FeatureCard
            index='02'
            title={t('Drop-in migration')}
            body={t(
              'Change the base URL. Your SDK, prompts and tools stay exactly as they are.'
            )}
          >
            <MigrationVisual baseUrl={props.baseUrl} />
          </FeatureCard>
          <FeatureCard
            index='03'
            title={t('Billed in USD, per request')}
            body={t(
              'Each request is priced when it is accepted and settled on what it actually used. No monthly fee.'
            )}
          >
            <BillingVisual />
          </FeatureCard>
          <FeatureCard
            index='04'
            title={t('Status you can check')}
            body={t(
              'Every group is probed around the clock. Signed in, you see 24-hour latency and success rate; an hour with no sample shows a dash, never a green bar.'
            )}
          >
            <StatusVisual />
          </FeatureCard>
        </div>
      </div>
    </section>
  )
}
