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
import { ADTY_BRAND_NAME } from '@/lib/adty-brand'

/**
 * Static header with the same logo position and height as the public header, with the
 * logo and the site name; the real header replaces it on mount.
 */
export function PrerenderHeader({
  siteTemplate = false,
}: {
  siteTemplate?: boolean
}) {
  return (
    <header className='border-border bg-background/85 fixed inset-x-0 top-0 z-50 border-b backdrop-blur-[10px]'>
      <div className='mx-auto flex h-[66px] w-full max-w-[1180px] items-center px-5 sm:px-10'>
        {siteTemplate ? (
          <span className='flex h-7 max-w-full items-center truncate text-sm font-semibold tracking-tight'>
            __ADTY_SITE_BRAND__
          </span>
        ) : (
          <>
            {/* The logo artwork already contains the ADTY wordmark; the brand name
            stays in the markup for assistive tech only. */}
            <img
              src='/logo.png'
              width={522}
              height={128}
              alt=''
              aria-hidden='true'
              className='h-7 w-auto max-w-none object-contain dark:hidden'
            />
            <img
              src='/logo-dark.png'
              width={522}
              height={128}
              alt=''
              aria-hidden='true'
              className='hidden h-7 w-auto max-w-none object-contain dark:block'
            />
            <span className='sr-only'>{ADTY_BRAND_NAME}</span>
          </>
        )}
      </div>
    </header>
  )
}
