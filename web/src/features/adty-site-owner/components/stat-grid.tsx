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
import { cn } from '@/lib/utils'

export type OwnerStat = {
  label: string
  value: string
  /** Shown in the destructive colour, e.g. a negative balance. */
  negative?: boolean
}

/** Row of headline numbers drawn as connected cells (1px hairlines, 14px
 *  radius); negative amounts are marked with data-negative for styling and tests. */
export function OwnerStatGrid(props: { items: OwnerStat[]; label: string }) {
  const threeUp = props.items.length === 3
  const columns = threeUp ? 3 : 4
  const desktopFill = (columns - (props.items.length % columns)) % columns
  const mobileFill = props.items.length % 2
  return (
    <section
      aria-label={props.label}
      className={cn(
        'border-border bg-border grid grid-cols-2 gap-px overflow-hidden rounded-[14px] border',
        threeUp ? 'lg:grid-cols-3' : 'lg:grid-cols-4'
      )}
    >
      {props.items.map((item) => (
        <div key={item.label} className='bg-card flex flex-col gap-2 px-5 py-4'>
          <span className='text-dim text-[12px] leading-none'>
            {item.label}
          </span>
          <span
            data-negative={item.negative ? 'true' : undefined}
            className={cn(
              'font-display text-[19px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums sm:text-[22px]',
              item.negative && 'text-destructive'
            )}
          >
            {item.value}
          </span>
        </div>
      ))}
      {/* Keep the trailing cells white when a row is not full (2 columns on
          mobile, 3 or 4 on desktop); no copy. */}
      {['stat-filler-a', 'stat-filler-b', 'stat-filler-c']
        .slice(0, Math.max(desktopFill, mobileFill))
        .map((key, index) => (
          <div
            key={key}
            aria-hidden
            className={cn(
              'bg-card px-5 py-4',
              index < mobileFill ? 'block' : 'hidden',
              index < desktopFill ? 'lg:block' : 'lg:hidden'
            )}
          />
        ))}
    </section>
  )
}
