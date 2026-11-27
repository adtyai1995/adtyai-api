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
const SEARCH_HIDDEN_ATTR = 'data-adty-wl-hidden'
const SEARCH_EMPTY_ATTR = 'data-adty-wl-empty'

interface BrandReplacementState {
  template: string
  rendered: string
}

const textNodeState = new WeakMap<Text, BrandReplacementState>()
const anchorHrefState = new WeakMap<HTMLAnchorElement, BrandReplacementState>()
const anchorAriaLabelState = new WeakMap<
  HTMLAnchorElement,
  BrandReplacementState
>()
let lastTitleState: BrandReplacementState | null = null

const brandState = ref<SiteBrand>({
  ...DEFAULT_BRAND,
  referral: { ...DEFAULT_REFERRAL },
})
let lastObservedBrandRef: SiteBrand = brandState.value
let lastAppliedBrand: SiteBrand | null = null

function getActiveSearchBrand(): SiteBrand {
  if (brandState.value !== lastObservedBrandRef) {
    lastObservedBrandRef = brandState.value
    lastAppliedBrand = null
  }
  return lastAppliedBrand ?? brandState.value
}

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

interface SplitTextSegment {
  node: Text
  start: number
  end: number
  original: string
  nextTemplate: string
  modified: boolean
}

function replaceBrandAcrossSplitNodes(
  root: HTMLElement,
  brand: SiteBrand
): void {
  const matcher = buildBrandReplacementMatcher(brand)
  if (!matcher) return

  const segments: SplitTextSegment[] = []
  let fullOriginal = ''
  let prevBoundary: Element | null = null

  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  let current = walker.nextNode() as Text | null
  while (current) {
    const parentEl = current.parentElement
    const parentTag = parentEl?.tagName
    const skipped =
      parentTag === 'SCRIPT' ||
      parentTag === 'STYLE' ||
      Boolean(parentEl?.closest('[data-adty-keep-raw="true"]')) ||
      Boolean(parentEl?.closest('.VPLocalSearchBox .no-results'))

    const currentVal = current.nodeValue || ''
    if (skipped) {
      if (currentVal.length > 0 && fullOriginal.length > 0) {
        fullOriginal += '\u0000'
        prevBoundary = null
      }
    } else if (currentVal.length > 0) {
      const prev = textNodeState.get(current)
      const original =
        prev && currentVal === prev.rendered ? prev.template : currentVal
      if (original.length > 0) {
        const boundary = parentEl?.closest(BLOCK_BOUNDARY_SELECTOR) ?? root
        if (prevBoundary !== null && boundary !== prevBoundary) {
          fullOriginal += '\u0000'
        }
        prevBoundary = boundary
        const start = fullOriginal.length
        fullOriginal += original
        segments.push({
          node: current,
          start,
          end: fullOriginal.length,
          original,
          nextTemplate: original,
          modified: false,
        })
      }
    }
    current = walker.nextNode() as Text | null
  }

  if (segments.length < 2 || !containsDefaultBrandToken(fullOriginal)) return

  const crossMatches: Array<{
    start: number
    end: number
    matched: string
    firstSegIdx: number
    lastSegIdx: number
  }> = []

  matcher.regex.lastIndex = 0
  let match = matcher.regex.exec(fullOriginal)
  while (match !== null) {
    const matched = match[0]
    const replacement = matcher.lookup.get(matched) ?? matched
    if (replacement !== matched) {
      const start = match.index
      const end = start + matched.length
      let firstSegIdx = -1
      let lastSegIdx = -1
      for (let idx = 0; idx < segments.length; idx++) {
        const seg = segments[idx]
        if (seg.end <= start) continue
        if (seg.start >= end) break
        if (firstSegIdx === -1) {
          firstSegIdx = idx
        }
        lastSegIdx = idx
      }
      if (firstSegIdx !== -1 && lastSegIdx > firstSegIdx) {
        crossMatches.push({
          start,
          end,
          matched,
          firstSegIdx,
          lastSegIdx,
        })
      }
    }
    match = matcher.regex.exec(fullOriginal)
  }

  if (crossMatches.length === 0) return

  for (let i = crossMatches.length - 1; i >= 0; i--) {
    const m = crossMatches[i]
    for (let idx = m.firstSegIdx; idx <= m.lastSegIdx; idx++) {
      const seg = segments[idx]
      const localStart = Math.max(0, m.start - seg.start)
      const localEnd = Math.min(seg.original.length, m.end - seg.start)
      const before = seg.nextTemplate.slice(0, localStart)
      const after = seg.nextTemplate.slice(localEnd)
      if (idx === m.firstSegIdx) {
        seg.nextTemplate = before + m.matched + after
      } else {
        seg.nextTemplate = before + after
      }
      seg.modified = true
    }
  }

  const emptyMarks = new Set<HTMLElement>()
  for (const seg of segments) {
    if (!seg.modified) continue
    if (seg.nextTemplate === '') {
      if (seg.node.nodeValue !== '') {
        seg.node.nodeValue = ''
      }
      textNodeState.delete(seg.node)
      const markEl = seg.node.parentElement
      if (markEl?.tagName === 'MARK' && markEl.textContent === '') {
        emptyMarks.add(markEl)
      }
    } else if (containsDefaultBrandToken(seg.nextTemplate)) {
      const nextRendered = replaceBrandText(seg.nextTemplate, brand)
      if (seg.node.nodeValue !== nextRendered) {
        seg.node.nodeValue = nextRendered
      }
      textNodeState.set(seg.node, {
        template: seg.nextTemplate,
        rendered: nextRendered,
      })
    } else {
      if (seg.node.nodeValue !== seg.nextTemplate) {
        seg.node.nodeValue = seg.nextTemplate
      }
      textNodeState.delete(seg.node)
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

function shouldHideDocsHref(href: string, brand: SiteBrand): boolean {
  if (!href) return false
  const hidePartner = brand.isSubsite
  const hideReferral = brand.loaded && !brand.referral.enabled
  return (
    (hidePartner && matchesGuideRoute(href, '/guides/partner')) ||
    (hideReferral && matchesGuideRoute(href, '/guides/referral'))
  )
}

interface SearchResultItem {
  container: HTMLElement
  anchor: HTMLAnchorElement
}

function collectSearchResultItems(searchBox: HTMLElement): SearchResultItem[] {
  const resultAnchors = searchBox.querySelectorAll<HTMLAnchorElement>(
    'a.result[href], li[role="option"] > a[href], li.result > a[href]'
  )
  const seenAnchors = new Set<HTMLAnchorElement>()
  const items: SearchResultItem[] = []
  resultAnchors.forEach((anchor) => {
    if (seenAnchors.has(anchor) || anchor.closest('.excerpt')) return
    seenAnchors.add(anchor)
    const container =
      anchor.closest<HTMLElement>('li[role="option"]') ||
      anchor.closest<HTMLElement>('li') ||
      anchor
    items.push({ container, anchor })
  })
  return items
}

function isHiddenSearchItem(item: SearchResultItem, brand: SiteBrand): boolean {
  const href = item.anchor.getAttribute('href') || ''
  return (
    shouldHideDocsHref(href, brand) ||
    item.container.getAttribute(SEARCH_HIDDEN_ATTR) === 'true' ||
    item.container.getAttribute('aria-hidden') === 'true' ||
    item.container.style.display === 'none'
  )
}

function getSearchInput(searchBox: HTMLElement): HTMLInputElement | null {
  return searchBox.querySelector<HTMLInputElement>(
    '#localsearch-input, input.search-input, input[type="search"]'
  )
}

function isZhSearchLocale(): boolean {
  if (typeof document !== 'undefined') {
    const lang = (document.documentElement.lang || '').trim().toLowerCase()
    if (lang.startsWith('zh')) return true
  }
  if (typeof window !== 'undefined') {
    const path = window.location.pathname.toLowerCase()
    if (
      path === '/zh-cn' ||
      path.startsWith('/zh-cn/') ||
      path.includes('/zh-cn/')
    ) {
      return true
    }
  }
  return false
}

function findSelectedSearchItem(
  searchBox: HTMLElement,
  items: SearchResultItem[]
): SearchResultItem | null {
  const byClassOrAria = items.find(
    (item) =>
      item.anchor.classList.contains('selected') ||
      item.container.getAttribute('aria-selected') === 'true'
  )
  if (byClassOrAria) return byClassOrAria

  const activeDescendantId =
    getSearchInput(searchBox)?.getAttribute('aria-activedescendant') || ''
  if (activeDescendantId) {
    const byActiveId = items.find(
      (item) => item.container.id === activeDescendantId
    )
    if (byActiveId) return byActiveId
  }
  return null
}

function dispatchSearchItemSelectionEvents(
  searchBox: HTMLElement,
  anchor: HTMLAnchorElement
): void {
  const prevActive =
    typeof document !== 'undefined' ? document.activeElement : null
  const focusInEvent =
    typeof FocusEvent === 'function'
      ? new FocusEvent('focusin', { bubbles: false })
      : new Event('focusin', { bubbles: false })
  anchor.dispatchEvent(focusInEvent)
  const mouseEnterEvent =
    typeof MouseEvent === 'function'
      ? new MouseEvent('mouseenter', { bubbles: false })
      : new Event('mouseenter', { bubbles: false })
  anchor.dispatchEvent(mouseEnterEvent)
  if (
    prevActive instanceof HTMLElement &&
    prevActive !== anchor &&
    searchBox.contains(prevActive) &&
    !prevActive.closest('a.result')
  ) {
    const restoreFocusIn =
      typeof FocusEvent === 'function'
        ? new FocusEvent('focusin', { bubbles: false })
        : new Event('focusin', { bubbles: false })
    prevActive.dispatchEvent(restoreFocusIn)
  }
}

function selectSearchResultItem(
  searchBox: HTMLElement,
  allItems: SearchResultItem[],
  target: SearchResultItem,
  scroll: boolean
): void {
  for (const item of allItems) {
    const isTarget = item === target
    if (item.anchor.classList.contains('selected') !== isTarget) {
      item.anchor.classList.toggle('selected', isTarget)
    }
    const nextAriaSelected = isTarget ? 'true' : 'false'
    if (item.container.getAttribute('aria-selected') !== nextAriaSelected) {
      item.container.setAttribute('aria-selected', nextAriaSelected)
    }
  }
  const searchInput = getSearchInput(searchBox)
  if (searchInput && target.container.id) {
    if (
      searchInput.getAttribute('aria-activedescendant') !== target.container.id
    ) {
      searchInput.setAttribute('aria-activedescendant', target.container.id)
    }
  }
  dispatchSearchItemSelectionEvents(searchBox, target.anchor)
  if (scroll && typeof target.anchor.scrollIntoView === 'function') {
    target.anchor.scrollIntoView({ block: 'nearest' })
  }
}

function clearSearchSelection(
  searchBox: HTMLElement,
  allItems: SearchResultItem[]
): void {
  for (const item of allItems) {
    if (item.anchor.classList.contains('selected')) {
      item.anchor.classList.remove('selected')
    }
    if (item.container.getAttribute('aria-selected') !== 'false') {
      item.container.setAttribute('aria-selected', 'false')
    }
  }
  const searchInput = getSearchInput(searchBox)
  if (searchInput?.hasAttribute('aria-activedescendant')) {
    searchInput.removeAttribute('aria-activedescendant')
  }
}

function syncSyntheticNoResults(
  searchBox: HTMLElement,
  hiddenCount: number,
  visibleCount: number
): void {
  const resultsList = searchBox.querySelector<HTMLElement>('ul.results')
  if (!resultsList) return

  const existing = resultsList.querySelector<HTMLElement>(
    `[${SEARCH_EMPTY_ATTR}="1"]`
  )
  const hasNativeNoResults = Boolean(
    resultsList.querySelector(`.no-results:not([${SEARCH_EMPTY_ATTR}="1"])`)
  )
  const query = getSearchInput(searchBox)?.value?.trim() || ''
  const shouldShow =
    hiddenCount > 0 &&
    visibleCount === 0 &&
    query.length > 0 &&
    !hasNativeNoResults

  if (!shouldShow) {
    if (existing) {
      existing.remove()
    }
    return
  }

  const zh = isZhSearchLocale()
  const localeKey = zh ? 'zh' : 'en'
  let emptyItem = existing
  if (!emptyItem) {
    emptyItem = document.createElement('li')
    emptyItem.className = 'no-results'
    emptyItem.setAttribute(SEARCH_EMPTY_ATTR, '1')
    for (const attr of Array.from(resultsList.attributes)) {
      if (attr.name.startsWith('data-v-')) {
        emptyItem.setAttribute(attr.name, attr.value)
      }
    }
    emptyItem.style.fontSize = '0.9rem'
    emptyItem.style.textAlign = 'center'
    emptyItem.style.padding = '12px'
    resultsList.appendChild(emptyItem)
  }

  if (
    emptyItem.dataset.adtyWlQuery !== query ||
    emptyItem.dataset.adtyWlLocale !== localeKey
  ) {
    emptyItem.dataset.adtyWlQuery = query
    emptyItem.dataset.adtyWlLocale = localeKey
    emptyItem.textContent = ''
    const prefix = zh ? '无匹配结果 "' : 'No results for "'
    emptyItem.appendChild(document.createTextNode(prefix))
    const strong = document.createElement('strong')
    strong.textContent = query
    emptyItem.appendChild(strong)
    emptyItem.appendChild(document.createTextNode('"'))
  }
}

function cleanupSearchBoxPatch(searchBox: HTMLElement): void {
  searchBox
    .querySelectorAll<HTMLElement>(`[${SEARCH_EMPTY_ATTR}="1"]`)
    .forEach((node) => {
      node.remove()
    })
  searchBox
    .querySelectorAll<HTMLElement>(`[${SEARCH_HIDDEN_ATTR}="true"]`)
    .forEach((container) => {
      if (container.style.display !== '') {
        container.style.display = ''
      }
      if (container.hasAttribute('aria-hidden')) {
        container.removeAttribute('aria-hidden')
      }
      container.removeAttribute(SEARCH_HIDDEN_ATTR)
    })
}

function patchSearchBoxElement(searchBox: HTMLElement, brand: SiteBrand): void {
  const allItems = collectSearchResultItems(searchBox)
  const visibleItems: SearchResultItem[] = []
  let hiddenCount = 0

  for (const item of allItems) {
    const { anchor, container } = item
    const href = anchor.getAttribute('href') || ''
    const hidden = shouldHideDocsHref(href, brand)
    const nextDisplay = hidden ? 'none' : ''
    if (container.style.display !== nextDisplay) {
      container.style.display = nextDisplay
    }
    if (hidden) {
      hiddenCount++
      if (container.getAttribute('aria-hidden') !== 'true') {
        container.setAttribute('aria-hidden', 'true')
      }
      if (container.getAttribute(SEARCH_HIDDEN_ATTR) !== 'true') {
        container.setAttribute(SEARCH_HIDDEN_ATTR, 'true')
      }
    } else {
      visibleItems.push(item)
      if (container.hasAttribute('aria-hidden')) {
        container.removeAttribute('aria-hidden')
      }
      if (container.hasAttribute(SEARCH_HIDDEN_ATTR)) {
        container.removeAttribute(SEARCH_HIDDEN_ATTR)
      }
    }

    if (brand.isWhiteLabel) {
      const ariaLabel = anchor.getAttribute('aria-label')
      if (ariaLabel !== null) {
        const prev = anchorAriaLabelState.get(anchor)
        const template =
          prev && ariaLabel === prev.rendered ? prev.template : ariaLabel
        if (containsDefaultBrandToken(template)) {
          const nextLabel = replaceBrandText(template, brand)
          if (nextLabel !== ariaLabel) {
            anchor.setAttribute('aria-label', nextLabel)
          }
          anchorAriaLabelState.set(anchor, {
            template,
            rendered: nextLabel,
          })
        } else if (prev && ariaLabel !== prev.rendered) {
          anchorAriaLabelState.delete(anchor)
        }
      } else if (anchorAriaLabelState.has(anchor)) {
        anchorAriaLabelState.delete(anchor)
      }
    }
  }

  if (hiddenCount > 0) {
    if (visibleItems.length === 0) {
      clearSearchSelection(searchBox, allItems)
    } else {
      const currentSelected = findSelectedSearchItem(searchBox, allItems)
      const hasHiddenSelected = allItems.some(
        (item) =>
          isHiddenSearchItem(item, brand) &&
          (item.anchor.classList.contains('selected') ||
            item.container.getAttribute('aria-selected') === 'true')
      )
      const searchInput = getSearchInput(searchBox)
      const activeDescendantId =
        searchInput?.getAttribute('aria-activedescendant') || ''
      const activeDescendantIsHidden =
        activeDescendantId !== '' &&
        allItems.some(
          (item) =>
            item.container.id === activeDescendantId &&
            isHiddenSearchItem(item, brand)
        )
      if (
        hasHiddenSelected ||
        !currentSelected ||
        isHiddenSearchItem(currentSelected, brand) ||
        activeDescendantIsHidden
      ) {
        const nextSelected =
          currentSelected && !isHiddenSearchItem(currentSelected, brand)
            ? currentSelected
            : visibleItems[0]
        selectSearchResultItem(searchBox, allItems, nextSelected, false)
      }
    }
  }

  syncSyntheticNoResults(searchBox, hiddenCount, visibleItems.length)

  if (!brand.isWhiteLabel) return

  rewriteAnchorsInRoot(searchBox, brand)

  searchBox
    .querySelectorAll<HTMLElement>('li[role="option"], li.result')
    .forEach((item) => {
      replaceBrandAcrossSplitNodes(item, brand)
    })

  rewriteTextNodesInRoot(searchBox, brand)
}

function patchOpenSearchBoxes(brand: SiteBrand): void {
  if (typeof document === 'undefined') return
  const shouldPatch = shouldPatchSearchBox(brand)
  document
    .querySelectorAll<HTMLElement>('.VPLocalSearchBox')
    .forEach((searchBox) => {
      if (shouldPatch) {
        patchSearchBoxElement(searchBox, brand)
      } else {
        cleanupSearchBoxPatch(searchBox)
      }
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

  const runWithSearchPatchGuard = (fn: () => void) => {
    isPatchingSearch = true
    try {
      fn()
    } finally {
      observer.takeRecords()
      isPatchingSearch = false
    }
  }

  const flush = () => {
    scheduled = false
    if (!active) return
    const brand = getActiveSearchBrand()
    if (!shouldPatchSearchBox(brand)) return
    if (!document.querySelector('.VPLocalSearchBox')) return
    runWithSearchPatchGuard(() => {
      patchOpenSearchBoxes(brand)
    })
  }

  const schedule = () => {
    if (scheduled || isPatchingSearch || !active) return
    const brand = getActiveSearchBrand()
    if (!shouldPatchSearchBox(brand)) return
    if (!document.querySelector('.VPLocalSearchBox')) return
    scheduled = true
    queueMicrotask(flush)
  }

  const observer = new MutationObserver(() => {
    schedule()
  })

  const onSearchKeyDown = (event: KeyboardEvent) => {
    if (!active || event.isComposing) return
    const key = event.key
    if (key !== 'ArrowDown' && key !== 'ArrowUp' && key !== 'Enter') return

    const brand = getActiveSearchBrand()
    if (!shouldPatchSearchBox(brand)) return

    const targetEl = event.target instanceof Element ? event.target : null
    const searchBox =
      targetEl?.closest<HTMLElement>('.VPLocalSearchBox') ??
      document.querySelector<HTMLElement>('.VPLocalSearchBox')
    if (!searchBox) return

    if (
      key === 'Enter' &&
      event.target instanceof HTMLButtonElement &&
      event.target.type !== 'submit'
    ) {
      return
    }

    const allItems = collectSearchResultItems(searchBox)
    if (allItems.length === 0) return

    const visibleItems = allItems.filter(
      (item) => !isHiddenSearchItem(item, brand)
    )
    const hiddenCount = allItems.length - visibleItems.length
    if (hiddenCount === 0) return

    if (visibleItems.length === 0) {
      event.preventDefault()
      event.stopImmediatePropagation()
      runWithSearchPatchGuard(() => {
        patchSearchBoxElement(searchBox, brand)
      })
      return
    }

    if (key === 'ArrowDown' || key === 'ArrowUp') {
      event.preventDefault()
      event.stopImmediatePropagation()
      const currentSelected = findSelectedSearchItem(searchBox, allItems)
      const currentVisibleIdx = currentSelected
        ? visibleItems.indexOf(currentSelected)
        : -1
      const nextVisibleIdx =
        key === 'ArrowDown'
          ? currentVisibleIdx === -1
            ? 0
            : (currentVisibleIdx + 1) % visibleItems.length
          : currentVisibleIdx <= 0
            ? visibleItems.length - 1
            : currentVisibleIdx - 1
      runWithSearchPatchGuard(() => {
        selectSearchResultItem(
          searchBox,
          allItems,
          visibleItems[nextVisibleIdx],
          true
        )
      })
      return
    }

    const targetAnchor = targetEl?.closest<HTMLAnchorElement>('a[href]')
    if (
      targetAnchor &&
      !targetAnchor.closest('.excerpt') &&
      shouldHideDocsHref(targetAnchor.getAttribute('href') || '', brand)
    ) {
      event.preventDefault()
      event.stopImmediatePropagation()
      runWithSearchPatchGuard(() => {
        patchSearchBoxElement(searchBox, brand)
      })
      return
    }

    const selectedItem = findSelectedSearchItem(searchBox, allItems)
    if (
      !selectedItem ||
      isHiddenSearchItem(selectedItem, brand) ||
      shouldHideDocsHref(selectedItem.anchor.getAttribute('href') || '', brand)
    ) {
      event.preventDefault()
      event.stopImmediatePropagation()
      runWithSearchPatchGuard(() => {
        patchSearchBoxElement(searchBox, brand)
      })
      return
    }

    dispatchSearchItemSelectionEvents(searchBox, selectedItem.anchor)
  }

  const onSearchClick = (event: MouseEvent) => {
    if (!active) return
    const brand = getActiveSearchBrand()
    if (!shouldPatchSearchBox(brand)) return
    const targetEl = event.target instanceof Element ? event.target : null
    const anchor = targetEl?.closest<HTMLAnchorElement>('a[href]')
    if (!anchor) return
    const searchBox = anchor.closest<HTMLElement>('.VPLocalSearchBox')
    if (!searchBox || anchor.closest('.excerpt')) return
    const href = anchor.getAttribute('href') || ''
    if (shouldHideDocsHref(href, brand)) {
      event.preventDefault()
      event.stopImmediatePropagation()
    }
  }

  const onSearchInput = (event: Event) => {
    if (!active) return
    const target = event.target
    if (
      target instanceof HTMLInputElement &&
      target.value.trim() === '' &&
      target.closest('.VPLocalSearchBox')
    ) {
      schedule()
    }
  }

  if (document.body) {
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ['aria-selected'],
    })
  }

  if (typeof window !== 'undefined') {
    window.addEventListener('keydown', onSearchKeyDown, true)
    window.addEventListener('click', onSearchClick, true)
    window.addEventListener('input', onSearchInput, true)
  }

  schedule()

  return () => {
    active = false
    observer.disconnect()
    if (typeof window !== 'undefined') {
      window.removeEventListener('keydown', onSearchKeyDown, true)
      window.removeEventListener('click', onSearchClick, true)
      window.removeEventListener('input', onSearchInput, true)
    }
  }
}

export function applyBrandToDOM(brand: SiteBrand): void {
  if (typeof document === 'undefined') return

  const effectiveBrand =
    !brand.loaded && brandState.value.loaded ? brandState.value : brand
  lastObservedBrandRef = brandState.value
  lastAppliedBrand = effectiveBrand

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
  patchOpenSearchBoxes(effectiveBrand)
  rewriteTextNodesInRoot(document.body, effectiveBrand)
}
