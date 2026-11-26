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

/**
 * VitePress white-label runtime helper for ADTY AI same-domain /docs/ mounting.
 *
 * Behavior:
 * - On the standalone main docs domain (e.g. docs.adtyai.com at "/"), keeps the
 *   configured default brand (ADTY AI / https://api.adtyai.com / https://www.adtyai.com).
 * - When mounted under "/docs/" on any main site or subsite, fetches same-origin
 *   GET /api/status (zero CORS) and replaces brand name, logo, favicon, console
 *   URL, and API Base URL across text, links, and code blocks (including Copy).
 */
export interface SiteBrand {
  name: string
  apiBase: string
  siteUrl: string
  logo: string
  favicon: string
  isWhiteLabel: boolean
}

export const DEFAULT_BRAND: SiteBrand = {
  name: 'ADTY AI',
  apiBase: 'https://api.adtyai.com',
  siteUrl: 'https://www.adtyai.com',
  logo: '',
  favicon: '',
  isWhiteLabel: false,
}

const CACHE_KEY_PREFIX = 'adty_docs_brand_v1:'

function normalizeOrigin(raw: string): string {
  return raw.trim().replace(/\/+$/, '')
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
    return DEFAULT_BRAND
  }

  const cacheKey = CACHE_KEY_PREFIX + window.location.host
  try {
    const cached = sessionStorage.getItem(cacheKey)
    if (cached) {
      const parsed = JSON.parse(cached) as SiteBrand
      if (parsed && parsed.name && parsed.apiBase) {
        return parsed
      }
    }
  } catch {
    // Ignore restricted storage environments.
  }

  try {
    const resp = await fetch('/api/status', {
      method: 'GET',
      credentials: 'omit',
      headers: { Accept: 'application/json' },
    })
    if (!resp.ok) return DEFAULT_BRAND
    const json = (await resp.json()) as { data?: Record<string, unknown> }
    const data = json?.data
    if (!data || typeof data !== 'object') return DEFAULT_BRAND

    const name = String(data.system_name || '').trim() || DEFAULT_BRAND.name
    const apiBase =
      normalizeOrigin(String(data.server_address || '')) || DEFAULT_BRAND.apiBase
    const siteUrl = normalizeOrigin(window.location.origin) || DEFAULT_BRAND.siteUrl
    const logo = String(data.logo || '').trim()
    const favicon = String(data.favicon || '').trim()

    const brand: SiteBrand = {
      name,
      apiBase,
      siteUrl,
      logo,
      favicon,
      isWhiteLabel: true,
    }
    try {
      sessionStorage.setItem(cacheKey, JSON.stringify(brand))
    } catch {
      // Ignore storage quota errors.
    }
    return brand
  } catch {
    return DEFAULT_BRAND
  }
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

export function applyBrandToDOM(brand: SiteBrand): void {
  if (typeof document === 'undefined' || !brand.isWhiteLabel) return

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
    const parentTag = node.parentElement?.tagName
    if (parentTag !== 'SCRIPT' && parentTag !== 'STYLE') {
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
