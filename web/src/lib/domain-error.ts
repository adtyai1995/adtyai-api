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
import type { TFunction } from 'i18next'

const DOMAIN_HTTP_ERROR_RE = /^Domain returned HTTP (\d+)$/
const CLOUDFLARE_HTTP_ERROR_RE = /^Cloudflare returned HTTP (\d+)$/
const EDGE_ADD_CNAME_RE = /^add CNAME (\S+) -> (\S+)$/
const EDGE_CNAME_MISMATCH_RE = /^(\S+) currently points to (\S+) instead of (\S+)$/

export function formatDomainLastError(
  raw: string | null | undefined,
  t: TFunction
): string {
  const trimmed = raw?.trim() ?? ''
  if (!trimmed) return ''

  const domainHttpMatch = DOMAIN_HTTP_ERROR_RE.exec(trimmed)
  if (domainHttpMatch) {
    return t('Domain returned HTTP {{status}}', {
      status: domainHttpMatch[1],
    })
  }

  const cloudflareHttpMatch = CLOUDFLARE_HTTP_ERROR_RE.exec(trimmed)
  if (cloudflareHttpMatch) {
    return t('Cloudflare returned HTTP {{status}}', {
      status: cloudflareHttpMatch[1],
    })
  }

  const edgeAddMatch = EDGE_ADD_CNAME_RE.exec(trimmed)
  if (edgeAddMatch) {
    return t('Please add CNAME record {{host}} -> {{target}}', {
      host: edgeAddMatch[1],
      target: edgeAddMatch[2],
    })
  }

  const edgeMismatchMatch = EDGE_CNAME_MISMATCH_RE.exec(trimmed)
  if (edgeMismatchMatch) {
    return t('{{host}} currently points to {{actual}}; expected {{expected}}', {
      host: edgeMismatchMatch[1],
      actual: edgeMismatchMatch[2],
      expected: edgeMismatchMatch[3],
    })
  }

  return t(trimmed, { defaultValue: trimmed })
}
