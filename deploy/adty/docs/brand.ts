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

export const SUBSITE_API_BASE_PLACEHOLDER = 'https://YOUR_API_DOMAIN'
const SUBSITE_API_HOST_PLACEHOLDER = 'YOUR_API_DOMAIN'
const DEFAULT_API_HOST = 'api.adtyai.com'
const DEFAULT_SITE_HOST = 'www.adtyai.com'
const BLOCK_BOUNDARY_SELECTOR =
  'p, div, li, ul, ol, tr, td, th, h1, h2, h3, h4, h5, h6, pre, blockquote, dt, dd, section, article, header, footer, aside, nav, .title, .line'

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
const RETRY_COOLDOWN_MS = 2000

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
let lastFailedAt = 0

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

function createFallbackBrand(loaded: boolean): SiteBrand {
  return {
    ...DEFAULT_BRAND,
    loaded,
    referral: { ...DEFAULT_REFERRAL },
  }
}

export async function resolveSiteBrand(): Promise<SiteBrand> {
  if (typeof window === 'undefined' || !shouldFetchSameOriginBrand()) {
    const fallback = createFallbackBrand(true)
    brandState.value = fallback
    return fallback
  }

  if (inflightPromise) {
    return inflightPromise
  }

  let currentPromise: Promise<SiteBrand> | null = null
  const pending = (async (): Promise<SiteBrand> => {
    const fail = (): SiteBrand => {
      if (currentPromise && inflightPromise === currentPromise) {
        inflightPromise = null
      }
      lastFailedAt = Date.now()
      const fallback = createFallbackBrand(false)
      brandState.value = fallback
      return fallback
    }

    try {
      const resp = await fetch('/api/status', {
        method: 'GET',
        credentials: 'omit',
        cache: 'no-store',
        headers: { Accept: 'application/json' },
      })
      if (!resp.ok) {
        return fail()
      }
      const json = (await resp.json()) as { data?: Record<string, unknown> }
      const data = json?.data
      if (!data || typeof data !== 'object') {
        return fail()
      }

      const isSubsite = Boolean(
        data.adty_site && typeof data.adty_site === 'object'
      )
      const name = String(data.system_name || '').trim() || DEFAULT_BRAND.name
      const rawApiBase = String(data.server_address || '')
      const apiBase = isSafeHttpOrigin(rawApiBase)
        ? normalizeOrigin(rawApiBase)
        : isSubsite
          ? SUBSITE_API_BASE_PLACEHOLDER
          : DEFAULT_BRAND.apiBase
      const rawSiteUrl = String(window.location.origin || '')
      const siteUrl = isSafeHttpOrigin(rawSiteUrl)
        ? normalizeOrigin(rawSiteUrl)
        : DEFAULT_BRAND.siteUrl
      const rawLogo = String(data.logo || '')
      const logo = isSafeAssetUrl(rawLogo) ? rawLogo.trim() : ''
      const rawFavicon = String(data.favicon || '')
      const favicon = isSafeAssetUrl(rawFavicon) ? rawFavicon.trim() : ''
      const paymentMethods = toStringArray(data.payment_methods)
      const referral = parseReferralInfo(data)

      lastFailedAt = 0
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
      return fail()
    }
  })()

  currentPromise = pending
  inflightPromise = pending
  void pending.then((result) => {
    if (!result.loaded && inflightPromise === pending) {
      inflightPromise = null
    }
  })

  return pending
}

function extractOriginHost(origin: string): string {
  if (!origin) return ''
  if (origin === SUBSITE_API_BASE_PLACEHOLDER) {
    return SUBSITE_API_HOST_PLACEHOLDER
  }
  try {
    return new URL(origin).host
  } catch {
    return ''
  }
}

function containsDefaultBrandToken(value: string): boolean {
  return (
    value.includes(DEFAULT_BRAND.apiBase) ||
    value.includes(DEFAULT_BRAND.siteUrl) ||
    value.includes(DEFAULT_API_HOST) ||
    value.includes(DEFAULT_SITE_HOST) ||
    value.includes(DEFAULT_BRAND.name)
  )
}

function resolveReplacementApiBase(brand: SiteBrand): string {
  if (brand.apiBase && isSafeHttpOrigin(brand.apiBase)) {
    return normalizeOrigin(brand.apiBase)
  }
  if (brand.isSubsite) {
    return SUBSITE_API_BASE_PLACEHOLDER
  }
  return ''
}

interface BrandReplacementMatcher {
  regex: RegExp
  lookup: Map<string, string>
}

function buildBrandReplacementMatcher(
  brand: SiteBrand
): BrandReplacementMatcher | null {
  const replacements: Array<[string, string]> = []
  const effectiveApiBase = resolveReplacementApiBase(brand)
  if (effectiveApiBase && effectiveApiBase !== DEFAULT_BRAND.apiBase) {
    replacements.push([DEFAULT_BRAND.apiBase, effectiveApiBase])
  }
  const effectiveSiteUrl =
    brand.siteUrl && isSafeHttpOrigin(brand.siteUrl)
      ? normalizeOrigin(brand.siteUrl)
      : ''
  if (effectiveSiteUrl && effectiveSiteUrl !== DEFAULT_BRAND.siteUrl) {
    replacements.push([DEFAULT_BRAND.siteUrl, effectiveSiteUrl])
  }
  const effectiveApiHost = extractOriginHost(effectiveApiBase)
  if (effectiveApiHost && effectiveApiHost !== DEFAULT_API_HOST) {
    replacements.push([DEFAULT_API_HOST, effectiveApiHost])
  }
  const effectiveSiteHost = extractOriginHost(effectiveSiteUrl)
  if (effectiveSiteHost && effectiveSiteHost !== DEFAULT_SITE_HOST) {
    replacements.push([DEFAULT_SITE_HOST, effectiveSiteHost])
  }
  if (brand.name && brand.name !== DEFAULT_BRAND.name) {
    replacements.push([DEFAULT_BRAND.name, brand.name])
  }
  if (replacements.length === 0) return null

  replacements.sort((a, b) => b[0].length - a[0].length)

  const regex = new RegExp(
    replacements.map(([from]) => escapeRegExp(from)).join('|'),
    'g'
  )
  const lookup = new Map(replacements)
  return { regex, lookup }
}

function replaceBrandText(input: string, brand: SiteBrand): string {
  const matcher = buildBrandReplacementMatcher(brand)
  if (!matcher) return input
  return input.replace(
    matcher.regex,
    (match) => matcher.lookup.get(match) ?? match
  )
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

function matchesGuideRoute(href: string, guidePath: string): boolean {
  const idx = href.indexOf(guidePath)
  if (idx === -1) return false
  const after = href.charAt(idx + guidePath.length)
  return (
    after === '' ||
    after === '#' ||
    after === '?' ||
    after === '/' ||
    after === '.'
  )
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

function rewriteAnchorsInRoot(root: ParentNode, brand: SiteBrand): void {
  const safeSiteUrl = isSafeHttpOrigin(brand.siteUrl)
    ? normalizeOrigin(brand.siteUrl)
    : ''
  const safeApiBase = resolveReplacementApiBase(brand)

  root.querySelectorAll<HTMLAnchorElement>('a[href]').forEach((anchor) => {
    if (anchor.closest('[data-adty-keep-raw="true"]')) return
    const currentHref = anchor.getAttribute('href') || ''
    const prev = anchorHrefState.get(anchor)
    const template =
      prev && currentHref === prev.rendered ? prev.template : currentHref

    let nextHref: string | null = null
    if (safeSiteUrl) {
      nextHref = replaceOriginPrefix(
        template,
        DEFAULT_BRAND.siteUrl,
        safeSiteUrl
      )
    }
    if (nextHref === null && safeApiBase) {
      nextHref = replaceOriginPrefix(
        template,
        DEFAULT_BRAND.apiBase,
        safeApiBase
      )
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
}

function rewriteTextNodesInRoot(root: Node, brand: SiteBrand): void {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  let node = walker.nextNode() as Text | null
  while (node) {
    const parentEl = node.parentElement
    const parentTag = parentEl?.tagName
    if (
      parentTag !== 'SCRIPT' &&
      parentTag !== 'STYLE' &&
      !parentEl?.closest('[data-adty-keep-raw="true"]') &&
      !parentEl?.closest('.VPLocalSearchBox .no-results')
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

function replaceBrandAcrossSplitNodes(
  root: HTMLElement,
  brand: SiteBrand
): void {
  const raw = root.textContent
  if (!raw || !containsDefaultBrandToken(raw)) return

  const matcher = buildBrandReplacementMatcher(brand)
  if (!matcher) return

  const segments: Array<{ node: Text; start: number; end: number }> = []
  let fullText = ''
  let prevBoundary: Element | null = null

  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  let current = walker.nextNode() as Text | null
  while (current) {
    const parentEl = current.parentElement
    const parentTag = parentEl?.tagName
    if (
      parentTag !== 'SCRIPT' &&
      parentTag !== 'STYLE' &&
      !parentEl?.closest('[data-adty-keep-raw="true"]') &&
      !parentEl?.closest('.VPLocalSearchBox .no-results')
    ) {
      const val = current.nodeValue || ''
      if (val.length > 0) {
        const boundary = parentEl?.closest(BLOCK_BOUNDARY_SELECTOR) ?? root
        if (prevBoundary !== null && boundary !== prevBoundary) {
          fullText += '\u0000'
        }
        prevBoundary = boundary
        const start = fullText.length
        fullText += val
        segments.push({ node: current, start, end: fullText.length })
      }
    }
    current = walker.nextNode() as Text | null
  }

  if (segments.length === 0 || !containsDefaultBrandToken(fullText)) return

  const matches: Array<{ start: number; end: number; replacement: string }> = []
  matcher.regex.lastIndex = 0
  let match = matcher.regex.exec(fullText)
  while (match !== null) {
    const matched = match[0]
    const replacement = matcher.lookup.get(matched) ?? matched
    if (replacement !== matched) {
      matches.push({
        start: match.index,
        end: match.index + matched.length,
        replacement,
      })
    }
    match = matcher.regex.exec(fullText)
  }

  if (matches.length === 0) return

  const emptyMarks = new Set<HTMLElement>()
  for (let i = matches.length - 1; i >= 0; i--) {
    const m = matches[i]
    let placed = false
    for (const seg of segments) {
      if (seg.end <= m.start || seg.start >= m.end) continue
      const val = seg.node.nodeValue || ''
      const localStart = Math.max(0, m.start - seg.start)
      const localEnd = Math.min(val.length, m.end - seg.start)
      const before = val.slice(0, localStart)
      const after = val.slice(localEnd)
      if (!placed) {
        seg.node.nodeValue = before + m.replacement + after
        placed = true
      } else {
        seg.node.nodeValue = before + after
      }
      textNodeState.delete(seg.node)
      const markEl = seg.node.parentElement
      if (
        seg.node.nodeValue === '' &&
        markEl?.tagName === 'MARK' &&
        markEl.textContent === ''
      ) {
        emptyMarks.add(markEl)
      }
    }
  }
  emptyMarks.forEach((markEl) => {
    if (markEl.textContent === '') {
      markEl.remove()
    }
  })
}

function shouldPatchSearchBox(brand: SiteBrand): boolean {
  return (
    brand.isWhiteLabel ||
    brand.isSubsite ||
    (brand.loaded && !brand.referral.enabled)
  )
}

function patchSearchBoxElement(searchBox: HTMLElement, brand: SiteBrand): void {
  const hidePartner = brand.isSubsite
  const hideReferral = brand.loaded && !brand.referral.enabled

  const resultAnchors = searchBox.querySelectorAll<HTMLAnchorElement>(
    'a.result[href], li[role="option"] > a[href], li.result > a[href]'
  )
  const seenAnchors = new Set<HTMLAnchorElement>()
  resultAnchors.forEach((anchor) => {
    if (seenAnchors.has(anchor) || anchor.closest('.excerpt')) return
    seenAnchors.add(anchor)

    const href = anchor.getAttribute('href') || ''
    const hidden =
      (hidePartner && matchesGuideRoute(href, '/guides/partner')) ||
      (hideReferral && matchesGuideRoute(href, '/guides/referral'))
    const container =
      anchor.closest<HTMLElement>('li[role="option"]') ||
      anchor.closest<HTMLElement>('li') ||
      anchor
    const nextDisplay = hidden ? 'none' : ''
    if (container.style.display !== nextDisplay) {
      container.style.display = nextDisplay
    }
    if (hidden) {
      if (container.getAttribute('aria-hidden') !== 'true') {
        container.setAttribute('aria-hidden', 'true')
      }
    } else if (container.hasAttribute('aria-hidden')) {
      container.removeAttribute('aria-hidden')
    }

    if (brand.isWhiteLabel) {
      const ariaLabel = anchor.getAttribute('aria-label')
      if (ariaLabel && containsDefaultBrandToken(ariaLabel)) {
        const nextLabel = replaceBrandText(ariaLabel, brand)
        if (nextLabel !== ariaLabel) {
          anchor.setAttribute('aria-label', nextLabel)
        }
      }
    }
  })

  if (!brand.isWhiteLabel) return

  rewriteAnchorsInRoot(searchBox, brand)
  rewriteTextNodesInRoot(searchBox, brand)

  searchBox
    .querySelectorAll<HTMLElement>('li[role="option"], li.result')
    .forEach((item) => {
      replaceBrandAcrossSplitNodes(item, brand)
    })
}

function patchOpenSearchBoxes(brand: SiteBrand): void {
  if (typeof document === 'undefined' || !shouldPatchSearchBox(brand)) return
  document
    .querySelectorAll<HTMLElement>('.VPLocalSearchBox')
    .forEach((searchBox) => {
      patchSearchBoxElement(searchBox, brand)
    })
}

export function installSearchBoxWhiteLabelObserver(): () => void {
  if (
    typeof document === 'undefined' ||
    typeof MutationObserver === 'undefined'
  ) {
    return () => {}
  }

  let active = true
  let scheduled = false
  let isPatchingSearch = false

  const flush = () => {
    scheduled = false
    if (!active) return
    const brand = brandState.value
    if (!shouldPatchSearchBox(brand)) return
    if (!document.querySelector('.VPLocalSearchBox')) return
    isPatchingSearch = true
    try {
      patchOpenSearchBoxes(brand)
    } finally {
      observer.takeRecords()
      isPatchingSearch = false
    }
  }

  const schedule = () => {
    if (scheduled || isPatchingSearch || !active) return
    const brand = brandState.value
    if (!shouldPatchSearchBox(brand)) return
    if (!document.querySelector('.VPLocalSearchBox')) return
    scheduled = true
    queueMicrotask(flush)
  }

  const observer = new MutationObserver(() => {
    schedule()
  })

  if (document.body) {
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
    })
  }

  schedule()

  return () => {
    active = false
    observer.disconnect()
  }
}

export function applyBrandToDOM(brand: SiteBrand): void {
  if (typeof document === 'undefined') return

  const effectiveBrand =
    !brand.loaded && brandState.value.loaded ? brandState.value : brand

  if (
    !effectiveBrand.loaded &&
    lastFailedAt > 0 &&
    shouldFetchSameOriginBrand() &&
    !inflightPromise &&
    Date.now() - lastFailedAt >= RETRY_COOLDOWN_MS
  ) {
    void resolveSiteBrand().then((next) => {
      if (next.loaded) {
        applyBrandToDOM(next)
      }
    })
  }

  const root = document.documentElement
  root.classList.toggle('adty-is-subsite', effectiveBrand.isSubsite)
  root.classList.toggle(
    'adty-referral-disabled',
    effectiveBrand.loaded && !effectiveBrand.referral.enabled
  )

  toggleNavItemVisibility('/guides/partner', effectiveBrand.isSubsite)
  toggleNavItemVisibility(
    '/guides/referral',
    effectiveBrand.loaded && !effectiveBrand.referral.enabled
  )

  if (!effectiveBrand.isWhiteLabel) {
    patchOpenSearchBoxes(effectiveBrand)
    return
  }

  if (document.title) {
    const currentTitle = document.title
    const titleTemplate =
      lastTitleState && currentTitle === lastTitleState.rendered
        ? lastTitleState.template
        : currentTitle
    const nextTitle = replaceBrandText(titleTemplate, effectiveBrand)
    if (nextTitle !== currentTitle) {
      document.title = nextTitle
    }
    lastTitleState = { template: titleTemplate, rendered: nextTitle }
  }

  if (effectiveBrand.favicon && isSafeAssetUrl(effectiveBrand.favicon)) {
    const safeFavicon = effectiveBrand.favicon.trim()
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

  if (effectiveBrand.logo && isSafeAssetUrl(effectiveBrand.logo)) {
    const safeLogo = effectiveBrand.logo.trim()
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
      if (img.alt !== effectiveBrand.name) {
        img.alt = effectiveBrand.name
      }
    }
  }

  rewriteAnchorsInRoot(document, effectiveBrand)
  rewriteTextNodesInRoot(document.body, effectiveBrand)
  patchOpenSearchBoxes(effectiveBrand)
}
