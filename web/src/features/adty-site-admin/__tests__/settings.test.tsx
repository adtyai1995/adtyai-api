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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { siteAdminKeys } from '../api'
import { SettingsPanel } from '../components/settings-panel'
import {
  buildResellerUpdate,
  missingMoneyKeys,
  toResellerDrafts,
} from '../lib/settings'
import type { ResellerSettings } from '../types'

const settings: ResellerSettings = {
  enabled: false,
  root_domains: ['sites.example.com'],
  reserved_subdomains: [],
  unknown_host_mode: 'main',
  cost_factor: 0.8,
  min_cost_factor: 0.5,
  cost_factor_tiers: [],

  max_site_bonus_extra: null,
  trial_grant_max_usd: 5,
  trial_grant_daily_usd: 20,
  markup_increase_delay_hours: 24,
  settle_hour: 4,
  freeze_days: 7,
  close_notice_days: 7,
  site_login_only: true,
  custom_domain_enabled: false,
  sender_domain_enabled: false,
  invite_code_enabled: false,
  self_use_check_enabled: true,
  migration_check_enabled: true,
  fingerprint_window_days: 30,
  customer_lookup_per_hour: 20,
  max_custom_domains_per_site: 2,
  custom_domain_creates_per_hour: 3,
  visibility: null,
  cloudflare_apiToken: '',
  cloudflare_zone_id: '',
  cloudflare_account_id: '',
  cname_target: '',
  fallback_origin: '',
  turnstile_widgets: [],
  turnstile_widget_secret: null,
  cloudflare_token_configured: false,
  turnstile_secret_configured: null,
  cloudflare_configured: false,
  resend_configured: false,
  money_configured: true,
  site_agreement_version: 3,
  site_customer_terms_version: 3,
}

describe('current reseller settings', () => {
  it('ignores generated row keys and displays server warnings', async () => {
    const saved = {...settings, cost_factor_tiers:[{min_monthly_usd:0,cost_factor:0.8}], turnstile_widgets:[{id:'w1',site_key:'key'}],settings_warnings:['Warning from server']}
    expect(buildResellerUpdate(saved,toResellerDrafts(saved))).toEqual({})
    const client=new QueryClient({defaultOptions:{queries:{staleTime:Infinity,retry:false}}})
    client.setQueryData(siteAdminKeys.settings,saved)
    const view=render(<QueryClientProvider client={client}><SettingsPanel section='supply' canWrite canWriteSecrets /></QueryClientProvider>)
    try {
      expect(await screen.findByText('Warning from server')).toBeInTheDocument()
      expect(screen.getByRole('button',{name:'Save'})).toBeDisabled()
    } finally {view.unmount();client.clear()}
  })

  it('saves domain automation credentials only when explicitly changed', () => {
    const saved = {
      ...settings,
      aliyun_id_configured: true,
      aliyun_secret_configured: true,
    }
    const drafts = toResellerDrafts(saved)
    expect(buildResellerUpdate(saved, drafts)).not.toHaveProperty(
      'aliyun_access_key_Secret'
    )
    drafts.aliyun_secret = 'new-secret'
    drafts.aliyun_id = 'new-id'
    drafts.domain_automation_enabled = true
    expect(buildResellerUpdate(saved, drafts)).toMatchObject({
      aliyun_access_key_Secret: 'new-secret',
      aliyun_access_key_id: 'new-id',
      domain_automation_enabled: true,
    })
  })
  it('allows enabling sites without a retired amount-tier extra', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity, retry: false } },
    })
    client.setQueryData(siteAdminKeys.settings, settings)
    const view = render(
      <QueryClientProvider client={client}>
        <SettingsPanel section='supply' canWrite canWriteSecrets />
      </QueryClientProvider>
    )
    try {
      expect(
        await screen.findByRole('switch', { name: 'Reseller sites' })
      ).not.toHaveAttribute('aria-disabled', 'true')
      expect(
        screen.queryByLabelText(
          'Extra top-up bonus over the main site (points)'
        )
      ).not.toBeInTheDocument()
    } finally {
      view.unmount()
      client.clear()
    }
  })

  it('builds an enable update from current money settings while preserving a historical extra', () => {
    const saved = { ...settings, max_site_bonus_extra: 2 }
    const drafts = toResellerDrafts(saved)
    drafts.enabled = true
    expect(missingMoneyKeys(drafts)).toEqual([])
    expect(buildResellerUpdate(saved, drafts)).toEqual({ enabled: true })
    expect(saved.max_site_bonus_extra).toBe(2)
  })
})
