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
import { describe, expect, it } from 'vitest'

import {
  getSidebarBadgeKeysForPath,
  SITE_OWNER_COMMISSIONS_BADGE_KINDS,
  sidebarBadgeText,
} from '@/hooks/use-sidebar-badges'

import {
  canonicalReferralAssetUrl,
  isValidOptionalHttpUrl,
  validRetailMarkup,
  validSiteReferralRate,
} from '../lib/site-owner'
import { computeOwnerFundingPreview } from '../lib/funding-preview'
import { isSiteOwnerSection, SITE_OWNER_SECTIONS } from '../sections'

describe('adty-site-owner/lib/site-owner', () => {
  it('validates single global retail multiplier within 1x to 10x', () => {
    expect(validRetailMarkup('1')).toBe(true)
    expect(validRetailMarkup('1.0')).toBe(true)
    expect(validRetailMarkup('2.5')).toBe(true)
    expect(validRetailMarkup('1.2345')).toBe(true)
    expect(validRetailMarkup('10')).toBe(true)
    expect(validRetailMarkup('10.0000')).toBe(true)

    expect(validRetailMarkup('')).toBe(false)
    expect(validRetailMarkup('0')).toBe(false)
    expect(validRetailMarkup('0.9999')).toBe(false)
    expect(validRetailMarkup('10.0001')).toBe(false)
    expect(validRetailMarkup('11')).toBe(false)
    expect(validRetailMarkup('-2')).toBe(false)
    expect(validRetailMarkup('1.12345')).toBe(false)
  })

  it('caps subsite referral commission rate at 30% (0 to 0.30)', () => {
    expect(validSiteReferralRate('0')).toBe(true)
    expect(validSiteReferralRate('0.05')).toBe(true)
    expect(validSiteReferralRate('0.15')).toBe(true)
    expect(validSiteReferralRate('0.3')).toBe(true)
    expect(validSiteReferralRate('0.300000')).toBe(true)

    expect(validSiteReferralRate('0.300001')).toBe(false)
    expect(validSiteReferralRate('0.31')).toBe(false)
    expect(validSiteReferralRate('1')).toBe(false)
    expect(validSiteReferralRate('-0.1')).toBe(false)
    expect(validSiteReferralRate('0.1234567')).toBe(false)
  })

  it('strips signed query string from referral asset URLs for digest parity', () => {
    expect(
      canonicalReferralAssetUrl(
        '/api/referral-assets/withdrawal-qr-1-abc.png?expires=12345&sig=xyz'
      )
    ).toBe('/api/referral-assets/withdrawal-qr-1-abc.png')
    expect(
      canonicalReferralAssetUrl('/api/referral-assets/withdrawal-qr-1-abc.png')
    ).toBe('/api/referral-assets/withdrawal-qr-1-abc.png')
    expect(canonicalReferralAssetUrl('')).toBe('')
  })

  it('validates optional voucher purchase URLs', () => {
    expect(isValidOptionalHttpUrl('')).toBe(true)
    expect(isValidOptionalHttpUrl('https://pay.example.com')).toBe(true)
    expect(isValidOptionalHttpUrl('javascript:void(0)')).toBe(false)
  })

  it('caps owner funding preview at 2 decimal places and 100,000 USD', () => {
    const wallets = [
      { kind: 'earnings', balance: '200000.00', frozen_balance: '0' },
      { kind: 'margin', balance: '50000.00', frozen_balance: '0', required_balance: '1000.00' },
    ] as never
    expect(computeOwnerFundingPreview(wallets, '10.25').valid).toBe(true)
    expect(computeOwnerFundingPreview(wallets, '100000').valid).toBe(true)
    expect(computeOwnerFundingPreview(wallets, '10.255').valid).toBe(false)
    const overMax = computeOwnerFundingPreview(wallets, '100000.01')
    expect(overMax.valid).toBe(false)
    expect(overMax.exceedsMax).toBe(true)
  })
})

describe('adty-site-owner/sections (R-33 #1)', () => {
  it('registers all split site owner sections and rejects unknown ones', () => {
    const expected = [
      'overview',
      'customers',
      'teams',
      'orders',
      'pricing',
      'bonus',
      'commissions',
      'earnings',
      'withdrawals',
      'settlements',
      'wallets',
      'vouchers',
      'notices',
      'staff',
      'tickets',
      'auth',
      'mail',
      'settings',
      'manual',
      'history',
    ]
    expect([...SITE_OWNER_SECTIONS]).toEqual(expected)
    for (const section of expected) {
      expect(isSiteOwnerSection(section)).toBe(true)
    }
    expect(isSiteOwnerSection('unknown')).toBe(false)
  })
})

describe('use-sidebar-badges fallback & split routes (R-33 #1, #3)', () => {
  it('never renders a failed/unavailable text pill when badge query fails', () => {
    expect(
      sidebarBadgeText(
        { invite_withdrawals_pending: 3 },
        SITE_OWNER_COMMISSIONS_BADGE_KINDS,
        {
          failed: true,
          mixedLabel: 'To-do',
          failedLabel: 'Unavailable',
        }
      )
    ).toBeUndefined()
    expect(
      sidebarBadgeText(undefined, SITE_OWNER_COMMISSIONS_BADGE_KINDS, {
        failed: false,
        mixedLabel: 'To-do',
      })
    ).toBeUndefined()
  })

  it('formats single and mixed positive counts cleanly', () => {
    expect(
      sidebarBadgeText(
        { invite_withdrawals_pending: 4 },
        SITE_OWNER_COMMISSIONS_BADGE_KINDS,
        { failed: false, mixedLabel: 'To-do' }
      )
    ).toBe('4')
    expect(
      sidebarBadgeText(
        {
          invite_withdrawals_pending: 2,
          site_invite_commissions_pending_coverage: 1,
        },
        SITE_OWNER_COMMISSIONS_BADGE_KINDS,
        { failed: false, mixedLabel: 'To-do' }
      )
    ).toBe('To-do')
  })

  it('maps commission badge clearing to /site-owner/commissions only, not /site-owner/earnings', () => {
    expect(getSidebarBadgeKeysForPath('/site-owner/commissions')).toEqual(
      SITE_OWNER_COMMISSIONS_BADGE_KINDS
    )
    expect(getSidebarBadgeKeysForPath('/site-owner/earnings')).toEqual([])
  })
})
