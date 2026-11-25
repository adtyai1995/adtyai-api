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

import { recordReferralClick } from '../api'

const DEVICE_COOKIE = 'adty_did'
export const SITE_INVITE_COOKIE = 'adty_site_invite'
const SITE_INVITE_MAX_AGE_SECONDS = 30 * 24 * 60 * 60

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

/**
 * Stores a sub-site `?site_invite=` code in a same-host cookie with its capture
 * timestamp so `SignUpForm` can read it after cross-page navigation.
 */
export function storeSiteInviteCookie(
  code: string,
  doc: Document = document,
  nowSeconds: number = Math.floor(Date.now() / 1000)
): void {
  const trimmed = code.trim()
  if (!trimmed) return
  const secure = doc.location?.protocol === 'https:' ? '; Secure' : ''
  const encoded = `${encodeURIComponent(trimmed)}|${Math.max(0, Math.floor(nowSeconds))}`
  doc.cookie = `${SITE_INVITE_COOKIE}=${encoded}; Max-Age=${SITE_INVITE_MAX_AGE_SECONDS}; Path=/; SameSite=Lax${secure}`
}

/** Reads the captured sub-site invite code and timestamp from the same-host cookie. */
export function readSiteInviteCookie(
  doc: Document = document
): CapturedSiteInvite | null {
  const prefix = `${SITE_INVITE_COOKIE}=`
  const match = doc.cookie
    .split('; ')
    .find((part) => part.startsWith(prefix))
  if (!match) return null
  const raw = match.slice(prefix.length).trim()
  if (!raw) return null
  const pipeIndex = raw.lastIndexOf('|')
  if (pipeIndex > 0) {
    const codePart = raw.slice(0, pipeIndex)
    const tsPart = Number(raw.slice(pipeIndex + 1))
    try {
      const code = decodeURIComponent(codePart).trim()
      if (!code) return null
      return {
        code,
        capturedAt: Number.isFinite(tsPart) && tsPart > 0 ? tsPart : 0,
      }
    } catch {
      return null
    }
  }
  try {
    const code = decodeURIComponent(raw).trim()
    return code ? { code, capturedAt: 0 } : null
  } catch {
    return null
  }
}

/** Clears the captured sub-site invite cookie on the current host. */
export function clearSiteInviteCookie(doc: Document = document): void {
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
    storeSiteInviteCookie(siteInvite)
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
