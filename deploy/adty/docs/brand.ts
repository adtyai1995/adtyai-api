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

import { ref, type Ref } from 'vue'

/**
 * VitePress white-label & live site-status runtime helper for ADTY AI.
 *
 * Behavior:
 * - On the standalone main docs domain (e.g. docs.adtyai.com at "/"), keeps the
 *   configured default brand (ADTY AI / https://api.adtyai.com / https://www.adtyai.com).
 * - When mounted under "/docs/" on the main site or any subsite, fetches same-origin
 *   GET /api/status (zero CORS, fresh per page session) and drives:
 *   1. Brand replacement (site name, logo, favicon, console URL, API Base URL).
 *   2. Live referral summary (<ReferralSummary />) and automatic nav hiding when disabled.
 *   3. Live payment methods (<PaymentMethodsList />).
 *   4. Main-site-only gating (<MainSiteOnly />) so subsites hide reseller partner docs.
 */
export interface PublicReferralInfo {
  enabled: boolean
  defaultRatePercent: string
  observationDays: number
  minWithdrawUSD: string
  usdtWithdrawFeeUSD: string
  withdrawMethods: string[]
  withdrawUSDTNetworks: string[]
}

export interface SiteBrand {
  loaded: boolean
  name: string
  apiBase: string
  siteUrl: string
  logo: string
  favicon: string
  isWhiteLabel: boolean
  isSubsite: boolean
  paymentMethods: string[]
  referral: PublicReferralInfo
}

export const DEFAULT_REFERRAL: PublicReferralInfo = {
  enabled: true,
  defaultRatePercent: '',
  observationDays: 7,
  minWithdrawUSD: '',
  usdtWithdrawFeeUSD: '',
  withdrawMethods: [],
  withdrawUSDTNetworks: [],
}

export const DEFAULT_BRAND: SiteBrand = {
  loaded: false,
  name: 'ADTY AI',
  apiBase: 'https://api.adtyai.com',
  siteUrl: 'https://www.adtyai.com',
  logo: '',
  favicon: '',
  isWhiteLabel: false,
  isSubsite: false,
  paymentMethods: [],
  referral: { ...DEFAULT_REFERRAL },
}

const CONTROL_CHAR_RE = /[\u0000-\u001F\u007F]/

interface BrandReplacementState {
  template: string
  rendered: string
}

const textNodeState = new WeakMap<Text, BrandReplacementState>()
const anchorHrefState = new WeakMap<HTMLAnchorElement, BrandReplacementState>()
let lastTitleState: BrandReplacementState | null = null

const brandState = ref<SiteBrand>({
  ...DEFAULT_BRAND,
  referral: { ...DEFAULT_REFERRAL },
})

let inflightPromise: Promise<SiteBrand> | null = null

export function useSiteBrand(): Ref<SiteBrand> {
  return brandState
}

function normalizeOrigin(raw: string): string {
  return raw.trim().replace(/\/+$/, '')
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

export function isSafeHttpOrigin(raw: string): boolean {
  if (typeof raw !== 'string' || CONTROL_CHAR_RE.test(raw)) return false
  const value = raw.trim()
  if (!value || !/^https?:\/\//i.test(value)) return false
  try {
    const parsed = new URL(value)
    return (
      (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      parsed.hostname.length > 0
    )
  } catch {
    return false
  }
}

export function isSafeAssetUrl(raw: string): boolean {
  if (typeof raw !== 'string' || CONTROL_CHAR_RE.test(raw)) return false
  const value = raw.trim()
  if (!value) return false
  if (value.startsWith('/')) {
    if (value.startsWith('//') || value.startsWith('/\\')) return false
    try {
      const parsed = new URL(value, 'https://example.invalid')
      return parsed.origin === 'https://example.invalid'
    } catch {
      return false
    }
  }
  return isSafeHttpOrigin(value)
}

function toStringArray(raw: unknown): string[] {
  if (!Array.isArray(raw)) return []
  return raw
    .map((item) => String(item || '').trim())
    .filter((item) => item.length > 0)
}

function parseReferralInfo(data: Record<string, unknown>): PublicReferralInfo {
  const raw = data.adty_referral_public
  if (raw && typeof raw === 'object') {
    const obj = raw as Record<string, unknown>
    return {
      enabled: Boolean(obj.enabled),
      defaultRatePercent: String(obj.default_rate_percent ?? '').trim(),
      observationDays: Math.max(0, Number(obj.observation_days ?? 0) || 0),
      minWithdrawUSD: String(obj.min_withdraw_usd ?? '').trim(),
      usdtWithdrawFeeUSD: String(obj.usdt_withdraw_fee_usd ?? '').trim(),
      withdrawMethods: toStringArray(obj.withdraw_methods),
      withdrawUSDTNetworks: toStringArray(obj.withdraw_usdt_networks),
    }
  }
  return {
    ...DEFAULT_REFERRAL,
    enabled: Boolean(data.adty_referral_enabled),
  }
}

function shouldFetchSameOriginBrand(): boolean {
  if (typeof window === 'undefined') return false
  const host = window.location.hostname.toLowerCase()
  if (host === 'docs.adtyai.com' && !window.location.pathname.startsWith('/docs')) {
    return false
  }
  return window.location.pathname.startsWith('/docs') || host !== 'docs.adtyai.com'
}

export async function resolveSiteBrand(): Promise<SiteBrand> {
  if (typeof window === 'undefined' || !shouldFetchSameOriginBrand()) {
    const fallback: SiteBrand = {
      ...DEFAULT_BRAND,
      loaded: true,
      referral: { ...DEFAULT_REFERRAL },
    }
    brandState.value = fallback
    return fallback
  }

  if (inflightPromise) {
    return inflightPromise
  }

  inflightPromise = (async () => {
    try {
      const resp = await fetch('/api/status', {
        method: 'GET',
        credentials: 'omit',
        cache: 'no-store',
        headers: { Accept: 'application/json' },
      })
      if (!resp.ok) {
        const fallback: SiteBrand = {
          ...DEFAULT_BRAND,
          loaded: true,
          referral: { ...DEFAULT_REFERRAL },
        }
        brandState.value = fallback
        return fallback
      }
      const json = (await resp.json()) as { data?: Record<string, unknown> }
      const data = json?.data
      if (!data || typeof data !== 'object') {
        const fallback: SiteBrand = {
          ...DEFAULT_BRAND,
          loaded: true,
          referral: { ...DEFAULT_REFERRAL },
        }
        brandState.value = fallback
        return fallback
      }

      const name = String(data.system_name || '').trim() || DEFAULT_BRAND.name
      const rawApiBase = String(data.server_address || '')
      const apiBase = isSafeHttpOrigin(rawApiBase)
        ? normalizeOrigin(rawApiBase)
        : DEFAULT_BRAND.apiBase
      const rawSiteUrl = String(window.location.origin || '')
      const siteUrl = isSafeHttpOrigin(rawSiteUrl)
        ? normalizeOrigin(rawSiteUrl)
        : DEFAULT_BRAND.siteUrl
      const rawLogo = String(data.logo || '')
      const logo = isSafeAssetUrl(rawLogo) ? rawLogo.trim() : ''
      const rawFavicon = String(data.favicon || '')
      const favicon = isSafeAssetUrl(rawFavicon) ? rawFavicon.trim() : ''
      const isSubsite = Boolean(data.adty_site && typeof data.adty_site === 'object')
      const paymentMethods = toStringArray(data.payment_methods)
      const referral = parseReferralInfo(data)

      const brand: SiteBrand = {
        loaded: true,
        name,
        apiBase,
        siteUrl,
        logo,
        favicon,
        isWhiteLabel: true,
        isSubsite,
        paymentMethods,
        referral,
      }
      brandState.value = brand
      return brand
    } catch {
      const fallback: SiteBrand = {
        ...DEFAULT_BRAND,
        loaded: true,
        referral: { ...DEFAULT_REFERRAL },
      }
      brandState.value = fallback
      return fallback
    }
  })()

  return inflightPromise
}

function containsDefaultBrandToken(value: string): boolean {
  return (
    value.includes(DEFAULT_BRAND.apiBase) ||
    value.includes(DEFAULT_BRAND.siteUrl) ||
    value.includes(DEFAULT_BRAND.name)
  )
}

function replaceBrandText(input: string, brand: SiteBrand): string {
  const replacements: Array<[string, string]> = []
  if (
    brand.apiBase &&
    brand.apiBase !== DEFAULT_BRAND.apiBase &&
    isSafeHttpOrigin(brand.apiBase)
  ) {
    replacements.push([DEFAULT_BRAND.apiBase, normalizeOrigin(brand.apiBase)])
  }
  if (
    brand.siteUrl &&
    brand.siteUrl !== DEFAULT_BRAND.siteUrl &&
    isSafeHttpOrigin(brand.siteUrl)
  ) {
    replacements.push([DEFAULT_BRAND.siteUrl, normalizeOrigin(brand.siteUrl)])
  }
  if (brand.name && brand.name !== DEFAULT_BRAND.name) {
    replacements.push([DEFAULT_BRAND.name, brand.name])
  }
  if (replacements.length === 0) return input

  const pattern = new RegExp(
    replacements.map(([from]) => escapeRegExp(from)).join('|'),
    'g'
  )
  const lookup = new Map(replacements)
  return input.replace(pattern, (match) => lookup.get(match) ?? match)
}

function replaceOriginPrefix(
  href: string,
  fromOrigin: string,
  toOrigin: string
): string | null {
  if (href === fromOrigin) {
    return toOrigin
  }
  if (
    href.startsWith(`${fromOrigin}/`) ||
    href.startsWith(`${fromOrigin}?`) ||
    href.startsWith(`${fromOrigin}#`)
  ) {
    return toOrigin + href.slice(fromOrigin.length)
  }
  return null
}

function toggleNavItemVisibility(pathNeedle: string, hidden: boolean): void {
  const anchors = document.querySelectorAll<HTMLAnchorElement>(
    `nav a[href*="${pathNeedle}"], aside a[href*="${pathNeedle}"], .VPNav a[href*="${pathNeedle}"], .VPSidebar a[href*="${pathNeedle}"], .VPDocFooter a[href*="${pathNeedle}"]`
  )
  anchors.forEach((anchor) => {
    const container =
      anchor.closest<HTMLElement>('.VPSidebarItem') ||
      anchor.closest<HTMLElement>('.VPNavScreenMenuLink') ||
      anchor.closest<HTMLElement>('.VPNavBarMenuLink') ||
      anchor.closest<HTMLElement>('.pager') ||
      anchor
    container.style.display = hidden ? 'none' : ''
  })
}

export function applyBrandToDOM(brand: SiteBrand): void {
  if (typeof document === 'undefined') return

  const root = document.documentElement
  root.classList.toggle('adty-is-subsite', brand.isSubsite)
  root.classList.toggle(
    'adty-referral-disabled',
    brand.loaded && !brand.referral.enabled
  )

  toggleNavItemVisibility('/guides/partner', brand.isSubsite)
  toggleNavItemVisibility(
    '/guides/referral',
    brand.loaded && !brand.referral.enabled
  )

  if (!brand.isWhiteLabel) return

  if (document.title) {
    const currentTitle = document.title
    const titleTemplate =
      lastTitleState && currentTitle === lastTitleState.rendered
        ? lastTitleState.template
        : currentTitle
    const nextTitle = replaceBrandText(titleTemplate, brand)
    if (nextTitle !== currentTitle) {
      document.title = nextTitle
    }
    lastTitleState = { template: titleTemplate, rendered: nextTitle }
  }

  if (brand.favicon && isSafeAssetUrl(brand.favicon)) {
    const safeFavicon = brand.favicon.trim()
    let iconLink = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (!iconLink) {
      iconLink = document.createElement('link')
      iconLink.rel = 'icon'
      document.head.appendChild(iconLink)
    }
    if (iconLink.getAttribute('href') !== safeFavicon) {
      iconLink.href = safeFavicon
    }
  }

  if (brand.logo && isSafeAssetUrl(brand.logo)) {
    const safeLogo = brand.logo.trim()
    const titleEl = document.querySelector<HTMLElement>('.VPNavBarTitle .title')
    if (titleEl) {
      let img = titleEl.querySelector<HTMLImageElement>('img.adty-wl-logo')
      if (!img) {
        img = document.createElement('img')
        img.className = 'adty-wl-logo'
        img.style.height = '24px'
        img.style.width = 'auto'
        img.style.marginRight = '8px'
        img.style.borderRadius = '4px'
        titleEl.prepend(img)
      }
      if (img.getAttribute('src') !== safeLogo) {
        img.src = safeLogo
      }
      if (img.alt !== brand.name) {
        img.alt = brand.name
      }
    }
  }

  const safeSiteUrl = isSafeHttpOrigin(brand.siteUrl)
    ? normalizeOrigin(brand.siteUrl)
    : ''
  const safeApiBase = isSafeHttpOrigin(brand.apiBase)
    ? normalizeOrigin(brand.apiBase)
    : ''

  document.querySelectorAll<HTMLAnchorElement>('a[href]').forEach((anchor) => {
    if (anchor.closest('[data-adty-keep-raw="true"]')) return
    const currentHref = anchor.getAttribute('href') || ''
    const prev = anchorHrefState.get(anchor)
    const template =
      prev && currentHref === prev.rendered ? prev.template : currentHref

    let nextHref: string | null = null
    if (safeSiteUrl) {
      nextHref = replaceOriginPrefix(template, DEFAULT_BRAND.siteUrl, safeSiteUrl)
    }
    if (nextHref === null && safeApiBase) {
      nextHref = replaceOriginPrefix(template, DEFAULT_BRAND.apiBase, safeApiBase)
    }

    if (nextHref !== null && isSafeHttpOrigin(nextHref)) {
      if (currentHref !== nextHref) {
        anchor.setAttribute('href', nextHref)
      }
      anchorHrefState.set(anchor, { template, rendered: nextHref })
    } else if (prev && currentHref !== prev.rendered) {
      anchorHrefState.delete(anchor)
    }
  })

  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
  let node = walker.nextNode() as Text | null
  while (node) {
    const parentEl = node.parentElement
    const parentTag = parentEl?.tagName
    if (
      parentTag !== 'SCRIPT' &&
      parentTag !== 'STYLE' &&
      !parentEl?.closest('[data-adty-keep-raw="true"]')
    ) {
      const currentVal = node.nodeValue
      if (currentVal) {
        const prev = textNodeState.get(node)
        const template =
          prev && currentVal === prev.rendered ? prev.template : currentVal
        if (containsDefaultBrandToken(template)) {
          const next = replaceBrandText(template, brand)
          if (next !== currentVal) {
            node.nodeValue = next
          }
          textNodeState.set(node, { template, rendered: next })
        } else if (prev && currentVal !== prev.rendered) {
          textNodeState.delete(node)
        }
      }
    }
    node = walker.nextNode() as Text | null
  }
}
