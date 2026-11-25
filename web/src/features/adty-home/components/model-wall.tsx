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
import { ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import { MODEL_VENDORS, type ModelVendor } from '../model-vendors'
import { HOME_CONTAINER, HOME_SECTION_TITLE } from './home-styles'

const ROW_COUNT = 3

/** Every vendor once, split into rows that drift in alternating directions. */
const ROWS: ModelVendor[][] = Array.from({ length: ROW_COUNT }, (_, row) =>
  MODEL_VENDORS.filter((_, index) => index % ROW_COUNT === row)
)

function VendorChip(props: { vendor: ModelVendor; chinese: boolean }) {
  const Icon = props.vendor.Icon
  return (
    <span className='flex h-14 shrink-0 items-center gap-3 px-6'>
      <Icon size={28} />
      <span className='text-foreground/85 text-[15px] font-medium whitespace-nowrap'>
        {props.chinese ? props.vendor.zh : props.vendor.en}
      </span>
    </span>
  )
}

/**
 * The vendor wall: copy on the left, three drifting rows of vendor marks on
 * the right, and a link to the pricing page, which is the authority on what
 * this site actually offers.
 */
export function ModelWall() {
  const { t, i18n } = useTranslation()
  const chinese = (i18n.resolvedLanguage || i18n.language).startsWith('zh')

  return (
    <section
      id='models'
      aria-labelledby='models-title'
      className='py-24 md:py-32'
    >
      <div
        className={cn(
          HOME_CONTAINER,
          'grid gap-12 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)] lg:items-center lg:gap-16'
        )}
      >
        <div>
          <h2 id='models-title' className={HOME_SECTION_TITLE}>
            {t('The models you need')}
          </h2>
          <p className='text-foreground/60 mt-4 text-[clamp(1.25rem,2vw,1.6rem)] font-bold tracking-[-0.02em]'>
            {t('From every major vendor')}
          </p>
          <p className='text-muted-foreground mt-8 max-w-[34rem] text-[17px] leading-[1.85] text-pretty'>
            {t(
              'OpenAI, Anthropic, Google, DeepSeek, Qwen, Doubao, Kimi and more, through the same endpoints. The pricing page lists every model this site offers and what it costs.'
            )}
          </p>
        </div>

        <div className='group/wall relative -mx-5 overflow-hidden [mask-image:linear-gradient(to_right,transparent,#000_12%,#000_88%,transparent)] py-2 sm:mx-0'>
          <ul className='sr-only'>
            {MODEL_VENDORS.map((vendor) => (
              <li key={vendor.id}>{chinese ? vendor.zh : vendor.en}</li>
            ))}
          </ul>
          <div aria-hidden className='flex flex-col gap-3'>
            {ROWS.map((row, rowIndex) => (
              <div
                key={row[0].id}
                className={cn(
                  'flex w-max group-hover/wall:[animation-play-state:paused]',
                  rowIndex % 2 === 0
                    ? 'motion-safe:animate-[adty-marquee_46s_linear_infinite]'
                    : 'motion-safe:animate-[adty-marquee_52s_linear_infinite_reverse]'
                )}
              >
                {row.map((vendor) => (
                  <VendorChip
                    key={vendor.id}
                    vendor={vendor}
                    chinese={chinese}
                  />
                ))}
                {row.map((vendor) => (
                  <VendorChip
                    key={`${vendor.id}-repeat`}
                    vendor={vendor}
                    chinese={chinese}
                  />
                ))}
              </div>
            ))}
          </div>
        </div>
      </div>

      <div className='mt-14 flex justify-center px-5'>
        <a
          href='/pricing'
          className='border-foreground/70 hover:bg-foreground hover:text-background inline-flex h-10 items-center gap-1.5 rounded-full border px-5 text-sm font-medium transition-colors'
        >
          {t('Browse all models')}
          <ChevronRight aria-hidden className='text-primary size-4' />
        </a>
      </div>
    </section>
  )
}
