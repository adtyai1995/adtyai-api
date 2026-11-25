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
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect } from 'react'

import { subsiteDocument } from '@/lib/subsite-document'

import { recordReferralClick, recordSiteReferralClick } from '../api'

const DEVICE_COOKIE = 'adty_did'
export const SITE_INVITE_COOKIE = 'adty_site_invite'
const SITE_INVITE_PREVIEW_STORAGE_KEY = 'adty_site_invite_preview'

export type CapturedSiteInvite = {
  code: string
  capturedAt: number
}

/** Records a referral click; failures (bad code, rate limit) are ignored. */
export async function trackReferral(
  code: string,
  landingPath: string
): Promise<void> {
  if (subsiteDocument()) return
  const trimmed = code.trim()
  if (!trimmed) return
  try {
    await recordReferralClick(trimmed, landingPath)
  } catch {
    // An invalid or disabled code simply does not set the cookie.
  }
}

/**
 * Writes a random device ID once (one year, SameSite=Lax) for the self-invite
 * check on both the main site and reseller sub-sites.
 */
export function ensureDeviceCookie(doc: Document = document): void {
  if (
    doc.cookie.split('; ').some((part) => part.startsWith(`${DEVICE_COOKIE}=`))
  ) {
    return
  }
  const id = crypto.randomUUID().replaceAll('-', '')
  const secure = doc.location?.protocol === 'https:' ? '; Secure' : ''
  doc.cookie = `${DEVICE_COOKIE}=${id}; Max-Age=31536000; Path=/; SameSite=Lax${secure}`
}

/** Records a subsite invite click so the server issues a signed HttpOnly cookie. */
export async function trackSiteInvite(code: string): Promise<string | null> {
  if (!subsiteDocument()) return null
  const trimmed = code.trim()
  if (!trimmed) return null
  try {
    const res = await recordSiteReferralClick(trimmed)
    return res.code || trimmed
  } catch {
    return null
  }
}

/**
 * Stores a sub-site invite preview in same-origin sessionStorage so `SignUpForm`
 * can display the captured code across navigation without overwriting the
 * server-signed HttpOnly `adty_site_invite` cookie.
 */
export function storeSiteInviteCookie(
  code: string,
  _doc: Document = document,
  nowSeconds: number = Math.floor(Date.now() / 1000)
): void {
  const trimmed = code.trim()
  if (!trimmed || typeof sessionStorage === 'undefined') return
  const payload = JSON.stringify({
    code: trimmed,
    capturedAt: Math.max(0, Math.floor(nowSeconds)),
  })
  try {
    sessionStorage.setItem(SITE_INVITE_PREVIEW_STORAGE_KEY, payload)
  } catch {
    // Ignore storage quota / privacy mode errors; the signed HttpOnly cookie still attributes.
  }
}

/** Reads the captured sub-site invite preview from same-origin sessionStorage. */
export function readSiteInviteCookie(
  _doc: Document = document
): CapturedSiteInvite | null {
  if (typeof sessionStorage === 'undefined') return null
  try {
    const raw = sessionStorage.getItem(SITE_INVITE_PREVIEW_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as { code?: unknown; capturedAt?: unknown }
    const code = typeof parsed?.code === 'string' ? parsed.code.trim() : ''
    if (!code) return null
    const ts = Number(parsed?.capturedAt)
    return {
      code,
      capturedAt: Number.isFinite(ts) && ts > 0 ? ts : 0,
    }
  } catch {
    return null
  }
}

/** Clears the captured sub-site invite preview in sessionStorage (and any legacy non-HttpOnly cookie). */
export function clearSiteInviteCookie(doc: Document = document): void {
  if (typeof sessionStorage !== 'undefined') {
    try {
      sessionStorage.removeItem(SITE_INVITE_PREVIEW_STORAGE_KEY)
    } catch {
      // Ignore storage errors.
    }
  }
  const secure = doc.location?.protocol === 'https:' ? '; Secure' : ''
  doc.cookie = `${SITE_INVITE_COOKIE}=; Max-Age=0; Path=/; SameSite=Lax${secure}`
}

/**
 * Root-level referral handling (adty tasks 6.2, 6.5):
 * - Writes the `adty_did` device cookie once on both main-site and sub-site hosts.
 * - On the main site, captures `?ref=`, reports the click, and drops the parameter.
 * - On a sub-site, captures `?site_invite=` into a same-host cookie with timestamp
 *   and drops the parameter without reporting to the main-site click endpoint.
 */
export function useReferralCapture(): void {
  const navigate = useNavigate()
  const location = useRouterState({ select: (state) => state.location })
  const search = location.search as Record<string, unknown>
  const ref = typeof search.ref === 'string' ? search.ref : ''
  const siteInvite =
    typeof search.site_invite === 'string' ? search.site_invite.trim() : ''

  useEffect(() => {
    ensureDeviceCookie()
  }, [])

  useEffect(() => {
    if (!ref || subsiteDocument()) return
    void trackReferral(ref, location.pathname)
    void navigate({
      to: '.',
      search: (prev: Record<string, unknown>) => {
        const next = { ...prev }
        delete next.ref
        return next
      },
      replace: true,
    })
  }, [ref, location.pathname, navigate])

  useEffect(() => {
    if (!siteInvite || !subsiteDocument()) return
    void trackSiteInvite(siteInvite).then((accepted) => {
      if (accepted) {
        storeSiteInviteCookie(accepted)
      }
    })
    void navigate({
      to: '.',
      search: (prev: Record<string, unknown>) => {
        const next = { ...prev }
        delete next.site_invite
        return next
      },
      replace: true,
    })
  }, [siteInvite, navigate])
}
