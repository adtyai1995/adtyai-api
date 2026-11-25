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

import { cn } from '@/lib/utils'

type ContentSectionProps = {
  title: ReactNode
  /** One short explanation under the title; keep it to a sentence or two. */
  description?: ReactNode
  /** Filters or buttons aligned to the right of the heading. */
  actions?: ReactNode
  children?: ReactNode
  className?: string
  'aria-label'?: string
}

/**
 * An unboxed group inside a console page (design system: Console section).
 * A 15px/600 heading with an optional 12.5px dim line, actions on the right,
 * and the content (usually a table) directly below. Pages stack these with
 * 32px between groups; forms that need a frame use SettingsSection instead.
 */
export function ContentSection(props: ContentSectionProps) {
  return (
    <section
      aria-label={props['aria-label']}
      className={cn('flex min-w-0 flex-col gap-3', props.className)}
    >
      <div className='flex flex-wrap items-end justify-between gap-x-4 gap-y-2.5'>
        <div className='min-w-[12rem] flex-1'>
          <h3 className='text-[15px] leading-snug font-semibold tracking-tight [word-break:keep-all]'>
            {props.title}
          </h3>
          {props.description != null && (
            <p className='text-dim mt-1 max-w-[80ch] text-[12.5px] leading-relaxed'>
              {props.description}
            </p>
          )}
        </div>
        {props.actions != null && (
          <div className='flex flex-wrap items-center gap-2'>
            {props.actions}
          </div>
        )}
      </div>
      {props.children}
    </section>
  )
}
