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
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPanel } from '../components/settings-panel'
import { SiteBasicBrandForm } from '../components/site-basic-brand-form'
import { SiteFundPanel } from '../components/site-fund-panel'
import type { AdminSite, ResellerSettings } from '../types'

const {
  getResellerSettingsMock,
  getAdminSiteWalletsMock,
  getAdminFundAnomaliesMock,
  getAdminSiteWalletLedgerMock,
  getAdminAnomalyDispositionsMock,
} = vi.hoisted(() => ({
  getResellerSettingsMock: vi.fn(),
  getAdminSiteWalletsMock: vi.fn(),
  getAdminFundAnomaliesMock: vi.fn(),
  getAdminSiteWalletLedgerMock: vi.fn(),
  getAdminAnomalyDispositionsMock: vi.fn(),
}))

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getResellerSettings: (...args: unknown[]) =>
      getResellerSettingsMock(...args),
    getAdminSiteWallets: (...args: unknown[]) =>
      getAdminSiteWalletsMock(...args),
    getAdminFundAnomalies: (...args: unknown[]) =>
      getAdminFundAnomaliesMock(...args),
    getAdminSiteWalletLedger: (...args: unknown[]) =>
      getAdminSiteWalletLedgerMock(...args),
    getAdminAnomalyDispositions: (...args: unknown[]) =>
      getAdminAnomalyDispositionsMock(...args),
  }
})

vi.mock('@/components/confirm-dialog', () => ({
  ConfirmDialog: () => null,
}))

vi.mock('@/hooks/use-sidebar-badges', () => ({
  invalidateSidebarBadges: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

vi.mock('@/features/system-settings/hooks/use-draft-navigation', () => ({
  useDraftNavigation: vi.fn(),
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      if (!params) return key
      return Object.entries(params).reduce(
        (acc, [paramKey, val]) =>
          acc.replaceAll(`{{${paramKey}}}`, String(val)),
        key
      )
    },
  }),
}))

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

const baseResellerSettings: ResellerSettings = {
  enabled: true,
  root_domains: ['example.com'],
  reserved_subdomains: ['www'],
  unknown_host_mode: 'reject',
  markup_increase_delay_hours: 0,
  settle_hour: 0,
  freeze_days: 7,
  close_notice_days: 7,
  site_login_only: true,
  custom_domain_enabled: false,
  sender_domain_enabled: false,
  domain_automation_enabled: false,
  aliyun_domain: '',
  domain_mainland_target: '',
  aliyun_access_key_id: '',
  aliyun_access_key_Secret: '',
  self_use_check_enabled: false,
  migration_check_enabled: false,
  fingerprint_window_days: 30,
  customer_lookup_per_hour: 60,
  max_custom_domains_per_site: 2,
  custom_domain_creates_per_hour: 5,
  visibility: null,
  cloudflare_apiToken: '',
  cloudflare_zone_id: '',
  cloudflare_account_id: '',
  cname_target: '',
  fallback_origin: '',
  turnstile_widgets: null,
  turnstile_widget_secret: null,
  cloudflare_token_configured: false,
  turnstile_secret_configured: null,
  cloudflare_configured: false,
  resend_configured: false,
  money_configured: true,
  tier_configured: true,
  site_customer_terms_version: 3,
}

describe('adty-site-admin copy updates and obsolete terms removal', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
    useAuthStore.getState().auth.reset()
  })

  it('omits the obsolete site_customer_terms version notice in SettingsPanel', async () => {
    getResellerSettingsMock.mockResolvedValueOnce(baseResellerSettings)

    renderWithQuery(
      <SettingsPanel section='policies' canWrite canWriteSecrets />
    )

    await waitFor(() => {
      expect(screen.getByText('Advanced settings')).toBeTruthy()
    })
    expect(screen.queryByText(/Site customer terms version/i)).toBeNull()
  })

  it('shows updated redemption code description without manual balance credit copy in SiteFundPanel', async () => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'root',
      display_name: 'Root',
      role: ROLE.SUPER_ADMIN,
      status: 1,
      email: 'root@example.com',
      quota: 0,
      used_quota: 0,
      request_count: 0,
      group: 'default',
    })
    getAdminSiteWalletsMock.mockResolvedValueOnce([])
    getAdminFundAnomaliesMock.mockResolvedValueOnce([])
    getAdminSiteWalletLedgerMock.mockResolvedValueOnce({
      total: 0,
      items: [],
      page: 1,
      page_size: 20,
    })
    getAdminAnomalyDispositionsMock.mockResolvedValueOnce({
      total: 0,
      items: [],
    })

    renderWithQuery(<SiteFundPanel siteId={5} />)

    await waitFor(() => {
      expect(
        screen.getByText(
          'Credits the site margin wallet directly and opens a new 90% margin cycle without touching any main-site customer wallet. Excess margin above the site level deposit floor can also be used by the owner to issue customer redemption codes.'
        )
      ).toBeTruthy()
    })
    expect(screen.queryByText(/manual customer\/team balance credits/i)).toBeNull()
    expect(screen.queryByText(/recharge codes/i)).toBeNull()
  })

  it('uses redemption codes wording for the voucher purchase link hint in SiteBasicBrandForm', () => {
    const site: AdminSite = {
      id: 5,
      owner_user_id: 10,
      owner_username: 'owner',
      subdomain: 'demo',
      root_domain: 'example.com',
      host: 'demo.example.com',
      status: 'active',
      cost_factor: null,
      tier_cost_factor: null,
      tier_month: '',
      visibility: '{}',
      brand: JSON.stringify({ name: 'Demo' }),
      contact_info: '',
      opened_by: 'apply',
      review_note: '',
      close_requested_at: 0,
      close_execute_at: 0,
      close_transfer_to: 0,
      close_claw_back: false,
      close_reason: '',
      closed_at: 0,
      customers: 0,
      created_at: 1700000000,
      updated_at: 1700000000,
    }

    renderWithQuery(<SiteBasicBrandForm site={site} />)

    expect(
      screen.getByText(
        'Optional external link shown to customers on the wallet page for purchasing redemption codes.'
      )
    ).toBeTruthy()
    expect(screen.queryByText(/purchasing recharge codes/i)).toBeNull()
  })
})
