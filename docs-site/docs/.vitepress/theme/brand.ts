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
 * When mounted under "/docs/" on the main site or any subsite, fetches same-origin
 * GET /api/status (zero CORS, fresh per page session) and drives:
 * 1. Brand replacement (site name, logo, favicon, console URL, API Base URL).
 * 2. Live referral summary (<ReferralSummary />) and automatic nav hiding when disabled.
 * 3. Live payment methods (<PaymentMethodsList />).
 * 4. Main-site-only gating (<MainSiteOnly />) so subsites hide reseller partner docs.
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
      const apiBase =
        normalizeOrigin(String(data.server_address || '')) || DEFAULT_BRAND.apiBase
      const siteUrl = normalizeOrigin(window.location.origin) || DEFAULT_BRAND.siteUrl
      const logo = String(data.logo || '').trim()
      const favicon = String(data.favicon || '').trim()
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

function replaceBrandText(input: string, brand: SiteBrand): string {
  let out = input
  if (brand.apiBase && brand.apiBase !== DEFAULT_BRAND.apiBase) {
    out = out.split(DEFAULT_BRAND.apiBase).join(brand.apiBase)
  }
  if (brand.siteUrl && brand.siteUrl !== DEFAULT_BRAND.siteUrl) {
    out = out.split(DEFAULT_BRAND.siteUrl).join(brand.siteUrl)
  }
  if (brand.name && brand.name !== DEFAULT_BRAND.name) {
    out = out.split(DEFAULT_BRAND.name).join(brand.name)
  }
  return out
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
    document.title = replaceBrandText(document.title, brand)
  }

  if (brand.favicon) {
    let iconLink = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (!iconLink) {
      iconLink = document.createElement('link')
      iconLink.rel = 'icon'
      document.head.appendChild(iconLink)
    }
    if (iconLink.href !== brand.favicon) {
      iconLink.href = brand.favicon
    }
  }

  if (brand.logo) {
    const titleEl = document.querySelector<HTMLElement>('.VPNavBarTitle .title')
    if (titleEl && !titleEl.querySelector('img.adty-wl-logo')) {
      const img = document.createElement('img')
      img.className = 'adty-wl-logo'
      img.src = brand.logo
      img.alt = brand.name
      img.style.height = '24px'
      img.style.width = 'auto'
      img.style.marginRight = '8px'
      img.style.borderRadius = '4px'
      titleEl.prepend(img)
    }
  }

  document.querySelectorAll<HTMLAnchorElement>('a[href]').forEach((anchor) => {
    const href = anchor.getAttribute('href') || ''
    if (href.startsWith(DEFAULT_BRAND.siteUrl)) {
      anchor.setAttribute('href', href.replace(DEFAULT_BRAND.siteUrl, brand.siteUrl))
    } else if (href.startsWith(DEFAULT_BRAND.apiBase)) {
      anchor.setAttribute('href', href.replace(DEFAULT_BRAND.apiBase, brand.apiBase))
    }
  })

  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
  let node: Node | null = walker.nextNode()
  while (node) {
    const parentEl = node.parentElement
    const parentTag = parentEl?.tagName
    if (
      parentTag !== 'SCRIPT' &&
      parentTag !== 'STYLE' &&
      !parentEl?.closest('[data-adty-keep-raw="true"]')
    ) {
      const val = node.nodeValue
      if (
        val &&
        (val.includes(DEFAULT_BRAND.apiBase) ||
          val.includes(DEFAULT_BRAND.siteUrl) ||
          val.includes(DEFAULT_BRAND.name))
      ) {
        const next = replaceBrandText(val, brand)
        if (next !== val) {
          node.nodeValue = next
        }
      }
    }
    node = walker.nextNode()
  }
}
