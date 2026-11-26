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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { buttonVariants } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  buildClientSnippet,
  CLIENT_SNIPPET_LABELS,
  DEFAULT_SNIPPET_MODEL,
  type ClientSnippetId,
} from '@/features/adty-site/client-snippets'
import { cn } from '@/lib/utils'

import { HOME_CONTAINER, HOME_PRIMARY_BUTTON } from './home-styles'

const CLIENTS: ClientSnippetId[] = [
  'claude-code',
  'codex',
  'openai-sdk',
  'cherry-studio',
]

const ENDPOINTS_TAB = 'endpoints'

type EndpointRow = {
  label: string
  detail: string
  url: string
  note?: string
  auth: string[]
}

function EndpointTable(props: { rows: EndpointRow[] }) {
  const { t } = useTranslation()
  return (
    <ul className='divide-y divide-white/8'>
      {props.rows.map((row) => (
        <li
          key={row.label}
          className='grid gap-3 px-5 py-4 sm:grid-cols-[11rem_minmax(0,1fr)_auto] sm:items-center sm:gap-5'
        >
          <div>
            <p className='text-[15px] font-semibold text-white'>{row.label}</p>
            <p className='mt-0.5 text-[13px] text-white/50'>{row.detail}</p>
          </div>
          <dl className='grid min-w-0 grid-cols-[3rem_minmax(0,1fr)] gap-x-3 gap-y-1.5 font-mono text-[13px]'>
            <dt className='text-white/40'>URL</dt>
            <dd className='flex min-w-0 items-baseline gap-2 text-[oklch(0.86_0.1_217)]'>
              <span className='truncate'>{row.url}</span>
              {row.note && (
                <span className='shrink-0 text-[12px] text-white/45'>
                  {row.note}
                </span>
              )}
            </dd>
            <dt className='text-white/40'>{t('Auth')}</dt>
            <dd className='flex min-w-0 flex-wrap gap-x-2 text-white/80'>
              {row.auth.map((header, index) => (
                <span key={header} className='whitespace-nowrap'>
                  {index > 0 && (
                    <span className='mr-2 font-sans text-white/40'>
                      {t('or')}
                    </span>
                  )}
                  {header}
                </span>
              ))}
            </dd>
          </dl>
          <CopyButton
            value={row.url}
            size='sm'
            variant='outline'
            className='h-7 justify-self-start rounded-full border-white/20 bg-transparent px-3 text-xs text-white/80 hover:bg-white/10 hover:text-white sm:justify-self-end'
            iconClassName='size-3.5'
            aria-label={t('Copy {{label}} base URL', { label: row.label })}
          >
            {t('Copy')}
          </CopyButton>
        </li>
      ))}
    </ul>
  )
}

/**
 * Closing quick start: a dark card with the one change to make on the left
 * and a terminal window on the right holding the four base URLs with their
 * auth headers, plus the client snippets the console uses.
 */
export function QuickStart(props: {
  signedIn: boolean
  baseUrl: string
  docsHref?: string
  docsExternal?: boolean
}) {
  const { t } = useTranslation()
  const docsIsCustomOrStatic =
    Boolean(props.docsExternal) || props.docsHref === '/docs/'
  const docsQuickstartHref = docsIsCustomOrStatic
    ? props.docsHref || '/docs/'
    : '/docs/quickstart'
  const docsTarget = props.docsExternal ? '_blank' : undefined
  const docsRel = props.docsExternal ? 'noopener noreferrer' : undefined
  const [tab, setTab] = useState<string>(ENDPOINTS_TAB)
  const snippetLabels = {
    provider: t('Provider type'),
    address: t('API address'),
    key: t('API Key'),
    model: t('Model'),
  }
  const rows: EndpointRow[] = [
    {
      label: t('OpenAI compatible'),
      detail: 'Chat Completions',
      url: `${props.baseUrl}/v1`,
      auth: ['Authorization: Bearer sk-…'],
    },
    {
      label: t('OpenAI Responses'),
      detail: 'Responses API',
      url: `${props.baseUrl}/v1`,
      auth: ['Authorization: Bearer sk-…'],
    },
    {
      label: t('Anthropic Messages'),
      detail: 'Claude Code',
      url: props.baseUrl,
      note: t('no /v1'),
      auth: ['x-api-key', 'Authorization: Bearer'],
    },
    {
      label: t('Gemini native'),
      detail: '/v1beta',
      url: props.baseUrl,
      auth: ['x-goog-api-key', '?key='],
    },
  ]
  const chips = [
    t('Billed in USD, per request'),
    t('Alipay · WeChat Pay · USDT'),
  ]

  return (
    <section
      id='quick-start'
      aria-labelledby='quick-start-title'
      className='pb-10'
    >
      <div className={HOME_CONTAINER}>
        <div className='dark bg-background text-foreground relative isolate overflow-hidden rounded-[28px] px-6 py-12 md:px-12 md:py-16'>
          <div
            aria-hidden
            className='pointer-events-none absolute inset-0 -z-10 [background-image:radial-gradient(var(--foreground)_1px,transparent_1px)] [mask-image:radial-gradient(70%_80%_at_100%_0%,#000,transparent_70%)] [background-size:22px_22px] opacity-[0.07]'
          />
          <div className='grid gap-12 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)] lg:items-start lg:gap-14'>
            <div>
              <h2
                id='quick-start-title'
                className='font-display text-[clamp(2rem,3.4vw,3rem)] leading-[1.12] font-extrabold tracking-[-0.03em] [word-break:keep-all] text-white'
              >
                <span className='block'>{t('Change one base URL,')}</span>
                <span className='text-primary block brightness-125'>
                  {t('keep everything else.')}
                </span>
              </h2>
              <p className='mt-6 max-w-[32rem] text-[16px] leading-[1.8] text-white/65'>
                {t(
                  'Create a key on the API Keys page of the console, then point your client at this site’s address. Requests keep their native shapes, so nothing else changes.'
                )}
              </p>
              <ul className='mt-7 flex flex-wrap gap-2.5'>
                {chips.map((chip) => (
                  <li
                    key={chip}
                    className='rounded-full border border-white/15 px-3.5 py-1.5 text-sm text-white/80'
                  >
                    {chip}
                  </li>
                ))}
              </ul>
              <div className='mt-9 flex flex-wrap gap-3'>
                <a
                  href={props.signedIn ? '/keys' : '/sign-up'}
                  className={cn(
                    buttonVariants({ size: 'lg' }),
                    HOME_PRIMARY_BUTTON
                  )}
                >
                  {props.signedIn ? t('Create an API key') : t('Get a key')}
                </a>
                <a
                  href={docsQuickstartHref}
                  target={docsTarget}
                  rel={docsRel}
                  className={cn(
                    buttonVariants({ variant: 'outline', size: 'lg' }),
                    'h-12 rounded-full border-white/25 bg-transparent px-7 text-[15px] font-semibold text-white hover:bg-white/10 hover:text-white'
                  )}
                >
                  {t('Read the docs')}
                </a>
              </div>
            </div>

            <div className='overflow-hidden rounded-2xl border border-white/10 bg-[oklch(0.13_0.01_262)] shadow-[0_30px_60px_-30px] shadow-black/60'>
              <Tabs
                value={tab}
                onValueChange={(value) => setTab(String(value))}
              >
                <div className='flex min-w-0 items-center gap-3 border-b border-white/10 px-4 py-2.5'>
                  <span aria-hidden className='hidden shrink-0 gap-1.5 sm:flex'>
                    <span className='size-2.5 rounded-full bg-white/15' />
                    <span className='size-2.5 rounded-full bg-white/15' />
                    <span className='size-2.5 rounded-full bg-white/15' />
                  </span>
                  <TabsList
                    variant='line'
                    className='h-8 w-auto min-w-0 flex-1 scroll-px-2 justify-start overflow-x-auto px-1'
                  >
                    <TabsTrigger
                      value={ENDPOINTS_TAB}
                      className='flex-none px-2.5 text-xs'
                    >
                      {t('Base URL & auth')}
                    </TabsTrigger>
                    {CLIENTS.map((id) => (
                      <TabsTrigger
                        key={id}
                        value={id}
                        className='flex-none px-2.5 text-xs'
                      >
                        {CLIENT_SNIPPET_LABELS[id]}
                      </TabsTrigger>
                    ))}
                  </TabsList>
                </div>
                <TabsContent value={ENDPOINTS_TAB}>
                  <EndpointTable rows={rows} />
                </TabsContent>
                {CLIENTS.map((id) => {
                  const snippet = buildClientSnippet(
                    id,
                    props.baseUrl,
                    DEFAULT_SNIPPET_MODEL,
                    snippetLabels
                  )
                  return (
                    <TabsContent key={id} value={id} className='relative'>
                      <CopyButton
                        value={snippet}
                        size='sm'
                        variant='ghost'
                        className='absolute top-3 right-3 text-white/70 hover:bg-white/10 hover:text-white'
                        aria-label={t('Copy code')}
                      />
                      <pre className='overflow-x-auto p-5 pr-14 font-mono text-[13px] leading-[2] text-white/85'>
                        <code>{snippet}</code>
                      </pre>
                    </TabsContent>
                  )
                })}
              </Tabs>
            </div>
          </div>
        </div>

        <nav
          aria-label={t('Quick links')}
          className='flex flex-wrap items-center justify-between gap-x-8 gap-y-3 px-2 pt-6 text-[15px]'
        >
          <span className='text-foreground/60'>{t('Quick links')}</span>
          <ul className='text-foreground/60 flex flex-wrap gap-x-6 gap-y-2'>
            <li>
              <a
                className='hover:text-primary transition-colors'
                href='/pricing'
              >
                {t('Model pricing')}
              </a>
            </li>
            <li>
              <a
                className='hover:text-primary transition-colors'
                href={docsQuickstartHref}
                target={docsTarget}
                rel={docsRel}
              >
                {t('Quick start guide')}
              </a>
            </li>
            {!docsIsCustomOrStatic && (
              <>
                <li>
                  <a
                    className='hover:text-primary transition-colors'
                    href='/docs/claude-code'
                  >
                    {t('Claude Code setup')}
                  </a>
                </li>
                <li>
                  <a
                    className='hover:text-primary transition-colors'
                    href='/docs/codex-cli'
                  >
                    {t('Codex CLI setup')}
                  </a>
                </li>
              </>
            )}
          </ul>
        </nav>
      </div>
    </section>
  )
}
