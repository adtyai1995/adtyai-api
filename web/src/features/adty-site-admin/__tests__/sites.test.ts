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
  generateOwnerPassword,
  isValidOptionalHttpUrl,
  parseSiteBrand,
  siteNotice,
} from '../lib/sites'
import { buildResellerUpdate, toResellerDrafts } from '../lib/settings'
import type { ResellerSettings } from '../types'

describe('adty-site-admin/lib/sites', () => {
  it('parses valid brand JSON including wallet_notice and voucher_purchase_url', () => {
    const raw = JSON.stringify({
      name: 'Acme AI',
      title: 'Acme Portal',
      notice: '  Maintenance tonight  ',
      wallet_notice: 'Top-up bonus active',
      voucher_purchase_url: 'https://shop.example.com/codes',
      unknown_field: 123,
    })
    expect(parseSiteBrand(raw)).toEqual({
      name: 'Acme AI',
      title: 'Acme Portal',
      notice: '  Maintenance tonight  ',
      wallet_notice: 'Top-up bonus active',
      voucher_purchase_url: 'https://shop.example.com/codes',
    })
    expect(siteNotice(raw)).toBe('Maintenance tonight')
  })

  it('handles empty or malformed brand JSON safely', () => {
    expect(parseSiteBrand(undefined)).toEqual({})
    expect(parseSiteBrand('{broken')).toEqual({})
    expect(parseSiteBrand('[]')).toEqual({})
    expect(siteNotice('{broken')).toBe('')
  })

  it('validates optional http(s) URLs', () => {
    expect(isValidOptionalHttpUrl('')).toBe(true)
    expect(isValidOptionalHttpUrl('   ')).toBe(true)
    expect(isValidOptionalHttpUrl('https://shop.example.com/buy?id=1')).toBe(
      true
    )
    expect(isValidOptionalHttpUrl('http://localhost:3000/vouchers')).toBe(true)
    expect(isValidOptionalHttpUrl('javascript:alert(1)')).toBe(false)
    expect(isValidOptionalHttpUrl('ftp://files.example.com')).toBe(false)
    expect(isValidOptionalHttpUrl('not-a-url')).toBe(false)
  })

  it('generates standalone owner passwords within 8-64 chars', () => {
    const pwd = generateOwnerPassword(16)
    expect(pwd).toHaveLength(16)
    expect(/[A-Z]/.test(pwd)).toBe(true)
    expect(/[a-z]/.test(pwd)).toBe(true)
    expect(/[0-9]/.test(pwd)).toBe(true)
  })

  it('normalizes multi-node edge_node_ips and falls back to legacy edge_node_ip', () => {
    const base: ResellerSettings = {
      enabled: true,
      root_domains: ['adtyai-fz.top'],
      reserved_subdomains: ['www'],
      unknown_host_mode: 'reject',
      markup_increase_delay_hours: 0,
      settle_hour: 4,
      freeze_days: 7,
      close_notice_days: 7,
      site_login_only: true,
      custom_domain_enabled: true,
      sender_domain_enabled: false,
      self_use_check_enabled: true,
      migration_check_enabled: true,
      fingerprint_window_days: 30,
      customer_lookup_per_hour: 20,
      max_custom_domains_per_site: 2,
      custom_domain_creates_per_hour: 3,
      visibility: null,
      cloudflare_apiToken: '',
      cloudflare_zone_id: 'zone1',
      cloudflare_account_id: '',
      cname_target: 'edge.adtyaicf.top',
      fallback_origin: 'origin.adtyaicf.top',
      edge_mainland_enabled: true,
      edge_acme_alias_suffix: 'acme.adtyaicf.top',
      edge_node_ip: '47.242.10.20',
      edge_probe_enabled: true,
      edge_auto_failover: false,
      turnstile_widgets: [],
      turnstile_widget_secret: null,
      cloudflare_token_configured: true,
      turnstile_secret_configured: null,
      cloudflare_configured: true,
      resend_configured: false,
      money_configured: true,
      site_customer_terms_version: 0,
    }
    const drafts = toResellerDrafts(base)
    expect(drafts.edge_node_ips).toBe('47.242.10.20')
    expect(buildResellerUpdate(base, drafts)).toEqual({})

    drafts.edge_node_ips = '47.242.10.20, 47.242.10.21\n47.242.10.20'
    drafts.edge_auto_failover = true
    expect(buildResellerUpdate(base, drafts)).toEqual({
      edge_node_ips: ['47.242.10.20', '47.242.10.21'],
      edge_auto_failover: true,
    })
  })
})
