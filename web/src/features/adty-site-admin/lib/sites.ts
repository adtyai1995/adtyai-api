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
import type { AdminSiteBrand } from '../types'

/** Columns the site list asks the server to sort (GET /api/adty/admin/sites). */
export type SiteSortKey =
  | 'customers'
  | 'topup'
  | 'revenue'
  | 'earning'
  | 'growth'
export type SortDirection = 'asc' | 'desc'
export type SiteSort = { key: SiteSortKey; direction: SortDirection }

export const DEFAULT_SITE_SORT: SiteSort = {
  key: 'revenue',
  direction: 'desc',
}

/** Clicking the sorted column flips it; another column starts descending. */
export function nextSiteSort(current: SiteSort, key: SiteSortKey): SiteSort {
  if (current.key !== key) return { key, direction: 'desc' }
  return { key, direction: current.direction === 'desc' ? 'asc' : 'desc' }
}

const BRAND_KEYS: ReadonlyArray<keyof AdminSiteBrand> = [
  'name',
  'title',
  'logo_url',
  'favicon_url',
  'theme_color',
  'tagline',
  'notice',
  'support_contact',
  'tawk_property_id',
  'tawk_widget_id',
  'wallet_notice',
  'voucher_purchase_url',
]

/** Safely parses a site's brand JSON string into an AdminSiteBrand object. */
export function parseSiteBrand(brand: string | undefined): AdminSiteBrand {
  if (!brand) return {}
  try {
    const parsed: unknown = JSON.parse(brand)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    const record = parsed as Record<string, unknown>
    const out: AdminSiteBrand = {}
    for (const key of BRAND_KEYS) {
      const value = record[key]
      if (typeof value === 'string') {
        out[key] = value
      }
    }
    return out
  } catch {
    return {}
  }
}

/** The notice in a site's brand JSON; empty when there is none or the JSON is bad. */
export function siteNotice(brand: string | undefined): string {
  return (parseSiteBrand(brand).notice ?? '').trim()
}

/** Validates an optional http:// or https:// URL (empty string is allowed). */
export function isValidOptionalHttpUrl(raw: string): boolean {
  const trimmed = raw.trim()
  if (trimmed === '') return true
  try {
    const parsed = new URL(trimmed)
    return (
      (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      parsed.hostname.length > 0
    )
  } catch {
    return false
  }
}

const UPPER = 'ABCDEFGHJKLMNPQRSTUVWXYZ'
const LOWER = 'abcdefghijkmnopqrstuvwxyz'
const DIGITS = '23456789'
const SYMBOLS = '!@#$%^&*-_'
const ALL_CHARS = UPPER + LOWER + DIGITS + SYMBOLS

function randomIndex(max: number): number {
  const buf = new Uint32Array(1)
  crypto.getRandomValues(buf)
  return buf[0] % max
}

/** Generates a random strong password (default 16 chars) for a new standalone site owner. */
export function generateOwnerPassword(length = 16): string {
  const targetLen = Math.max(8, Math.min(64, length))
  const chars = [
    UPPER[randomIndex(UPPER.length)],
    LOWER[randomIndex(LOWER.length)],
    DIGITS[randomIndex(DIGITS.length)],
    SYMBOLS[randomIndex(SYMBOLS.length)],
  ]
  while (chars.length < targetLen) {
    chars.push(ALL_CHARS[randomIndex(ALL_CHARS.length)])
  }
  for (let i = chars.length - 1; i > 0; i -= 1) {
    const j = randomIndex(i + 1)
    const tmp = chars[i]
    chars[i] = chars[j]
    chars[j] = tmp
  }
  return chars.join('')
}
