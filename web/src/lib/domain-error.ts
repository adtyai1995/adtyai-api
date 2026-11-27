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
const EDGE_CNAME_MISMATCH_RE =
  /^(\S+) currently points to (\S+) instead of (\S+)$/
const DNS_CNAME_CHECK_FAILED_RE = /^DNS CNAME check failed for ([^:]+)$/
const EDGE_CERT_MISMATCH_RE = /^Issued edge certificate does not match (.+)$/
const ACME_ERROR_RE = /^ACME error \((\d+)\)(?::\s*.+)?$/

const EXACT_ERROR_KEYS: Record<string, string> = {
  'ACME authorization does not offer dns-01 challenge':
    'Certificate authority did not offer DNS-01 verification. Please retry later.',
  'ACME server returned empty certificate chain':
    'Certificate authority returned an empty certificate chain. Please retry later.',
  'Failed to parse issued edge certificate':
    'Failed to parse the issued edge certificate. Please retry later.',
  'Certificate check timed out. Please try again later.':
    'Certificate check timed out. Please try again later.',
}

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

  const dnsCnameMatch = DNS_CNAME_CHECK_FAILED_RE.exec(trimmed)
  if (dnsCnameMatch) {
    return t(
      'DNS CNAME check failed for {{host}}. Please verify the CNAME record and try again.',
      { host: dnsCnameMatch[1].trim() }
    )
  }

  const edgeCertMismatchMatch = EDGE_CERT_MISMATCH_RE.exec(trimmed)
  if (edgeCertMismatchMatch) {
    return t(
      'Issued edge certificate does not match {{host}}. Please retry later.',
      { host: edgeCertMismatchMatch[1].trim() }
    )
  }

  const acmeMatch = ACME_ERROR_RE.exec(trimmed)
  if (acmeMatch) {
    return t(
      'Certificate issuance failed (ACME {{status}}). Please verify your CNAME record and retry later.',
      { status: acmeMatch[1] }
    )
  }

  const exactKey = Object.hasOwn(EXACT_ERROR_KEYS, trimmed)
    ? EXACT_ERROR_KEYS[trimmed]
    : undefined
  if (exactKey) {
    return t(exactKey)
  }

  return t(trimmed, { defaultValue: trimmed })
}
