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
import { useMemo } from 'react'

import { useStatus } from '@/hooks/use-status'

export type ResolvedDocsLink = {
  href: string
  external: boolean
  reloadDocument: boolean
}

function isHttpUrl(raw: string): boolean {
  try {
    const parsed = new URL(raw)
    return (
      (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      parsed.hostname.length > 0
    )
  } catch {
    return false
  }
}

/**
 * Resolves the documentation target for the current site from /api/status:
 * 1. Explicit external http(s) docs_link (main site or subsite brand override)
 * 2. Mounted static white-label /docs/ (full browser load so origin/edge serves it)
 * 3. Built-in React SPA /docs fallback
 */
export function resolveDocsLink(
  status: Record<string, unknown> | null | undefined
): ResolvedDocsLink {
  const rawDocsLink =
    typeof status?.docs_link === 'string' ? status.docs_link.trim() : ''
  if (rawDocsLink && isHttpUrl(rawDocsLink)) {
    return {
      href: rawDocsLink,
      external: true,
      reloadDocument: false,
    }
  }
  if (status?.docs_static_mounted === true) {
    return {
      href: '/docs/',
      external: false,
      reloadDocument: true,
    }
  }
  return {
    href: '/docs',
    external: false,
    reloadDocument: false,
  }
}

export function useDocsLink(): ResolvedDocsLink {
  const { status } = useStatus()
  return useMemo(
    () =>
      resolveDocsLink(status as Record<string, unknown> | null | undefined),
    [status]
  )
}
