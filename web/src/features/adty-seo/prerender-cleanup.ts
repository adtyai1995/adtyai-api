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
import { useRouterState } from '@tanstack/react-router'
import { useLayoutEffect } from 'react'

import { isHomePath } from './home-langs'

/** Id of the server-inserted, build-time rendered home page body (task 9.3). */
export const PRERENDER_ELEMENT_ID = 'adty-prerender'

export function removePrerenderedHome(): void {
  document.getElementById(PRERENDER_ELEMENT_ID)?.remove()
  document.getElementById('adty-home-critical')?.remove()
}

/**
 * The landing page removes the prerendered copy before the browser paints,
 * so the real page replaces it in the same frame without a blank flash.
 */
export function useRemovePrerenderOnMount(): void {
  useLayoutEffect(() => {
    if (!document.getElementById(PRERENDER_ELEMENT_ID)) return
    if (!document.querySelector('template[data-adty-home-style]')) {
      removePrerenderedHome()
      return
    }
    // Failed stylesheets keep their template, leaving the static links usable.
    document.addEventListener('adty-home-styles-ready', removePrerenderedHome)
    return () => {
      document.removeEventListener(
        'adty-home-styles-ready',
        removePrerenderedHome
      )
    }
  }, [])
}

/**
 * Root-level fallback: when the app ends up on a page other than the home
 * page (a redirect to /setup, an error page), the prerendered copy goes too.
 */
export function useAdtyPrerenderCleanup(): void {
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })
  useLayoutEffect(() => {
    if (!isHomePath(pathname)) removePrerenderedHome()
  }, [pathname])
}
