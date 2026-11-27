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
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { toast } from 'sonner'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { OwnerVerificationProvider } from '../components/owner-verification'
import { SiteSettingsPanel } from '../components/site-settings-panel'
import type { OwnerSite, SiteDomain } from '../types'

vi.mock('@/components/confirm-dialog', () => ({
  ConfirmDialog: () => null,
}))

vi.mock('@/features/system-settings/hooks/use-draft-navigation', () => ({
  useDraftNavigation: vi.fn(),
}))

vi.mock('../components/site-legal-card', () => ({
  SiteLegalCard: () => null,
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
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
    <QueryClientProvider client={client}>
      <OwnerVerificationProvider>{ui}</OwnerVerificationProvider>
    </QueryClientProvider>
  )
}

function makeOwnerSite(domains: SiteDomain[], overrides: Partial<OwnerSite> = {}): OwnerSite {
  return {
    id: 1,
    status: 'active',
    subdomain: 'shop',
    root_domain: 'adtyai-fz.top',
    host: 'shop.adtyai-fz.top',
    base_url: 'https://shop.adtyai-fz.top',
    api_base_url: 'https://api-s1.adtyai-fz.top',
    cost_factor: '1.0',
    custom_domain_enabled: true,
    cname_target: 'edge.adtyaicf.top',
    domain_mainland_target: 'cn.adtyaicf.top',
    edge_mainland_enabled: true,
    edge_acme_alias_suffix: 'acme.adtyaicf.top',
    sender_domain_enabled: false,
    brand: { name: 'Shop', logo_url: '', notice: '', support_contact: '' },
    bonus_methods: [],
    seo_index: true,
    close_execute_at: 0,
    prices: [],
    pricing_version: 1,
    domains,
    visibility: {},
    ...overrides,
  }
}

describe('owner custom domain streamlined flow and optional mainland acceleration', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('omits redundant global CNAME box and platform domain cards, collapses mainland acceleration by default without scary waiting errors, and gates step 2', () => {
    const platformSite: SiteDomain = {
      id: 1,
      hostname: 'shop.adtyai-fz.top',
      role: 'site',
      managed_by: 'platform',
      status: 'active',
      ssl_status: 'active',
      verify_records: '[]',
      last_error: '',
      created_at: 1700000000,
    }
    const waitingSite: SiteDomain = {
      id: 10,
      hostname: 'www.shop.example',
      role: 'site',
      managed_by: 'owner',
      status: 'active',
      ssl_status: 'active',
      verify_records: '[]',
      last_error: '',
      edge_acme_alias: 'd10-aabbccdd',
      edge_cert_status: 'waiting_dns',
      edge_cert_error:
        'add CNAME _acme-challenge.www.shop.example -> d10-aabbccdd.acme.adtyaicf.top',
      created_at: 1700000000,
    }
    const apiDomain: SiteDomain = {
      id: 11,
      hostname: 'api.shop.example',
      role: 'base_url',
      managed_by: 'owner',
      status: 'active',
      ssl_status: 'active',
      verify_records: '[]',
      last_error: '',
      created_at: 1700000000,
    }

    renderWithQuery(
      <SiteSettingsPanel
        site={makeOwnerSite([platformSite, waitingSite, apiDomain])}
      />
    )

    // Redundant top CNAME banner and duplicate platform domain card are gone.
    expect(screen.queryByText('Default CNAME target')).toBeNull()
    expect(screen.queryByText('DNS CNAME target')).toBeNull()
    expect(screen.queryByText('Platform domains are managed by the platform')).toBeNull()

    // Mainland acceleration starts collapsed with calm "Not configured" status and no raw error text.
    expect(screen.getByText('Mainland China acceleration (optional)')).toBeTruthy()
    expect(screen.getByText('Not configured')).toBeTruthy()
    expect(screen.queryByText('_acme-challenge.www.shop.example')).toBeNull()
    expect(screen.queryByText(/add CNAME/)).toBeNull()
    expect(screen.queryByText('cn.adtyaicf.top')).toBeNull()

    // Expanding reveals Step 1 and Check certificate button; Step 2 stays hidden until ready.
    fireEvent.click(screen.getByRole('button', { name: 'Configure' }))
    expect(screen.getByText('_acme-challenge.www.shop.example')).toBeTruthy()
    expect(screen.getByText('d10-aabbccdd.acme.adtyaicf.top')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Check certificate' })).toBeTruthy()
    expect(
      screen.getByText(
        'Step 2 will appear automatically once the domain is active and the edge certificate is ready.'
      )
    ).toBeTruthy()
    expect(screen.queryByText('cn.adtyaicf.top')).toBeNull()
    expect(screen.queryByText('_acme-challenge.api.shop.example')).toBeNull()
  })

  it('reveals step 2 mainland CNAME target once active and edge_cert_status is ready, and lets pending site domains expand step 1 early while still gating step 2', () => {
    const readySite: SiteDomain = {
      id: 12,
      hostname: 'portal.shop.example',
      role: 'site',
      managed_by: 'owner',
      status: 'active',
      ssl_status: 'active',
      verify_records: '[]',
      last_error: '',
      edge_acme_alias: 'd12-11223344',
      edge_cert_status: 'ready',
      edge_cert_not_after: 1800000000,
      created_at: 1700000000,
    }

    renderWithQuery(<SiteSettingsPanel site={makeOwnerSite([readySite])} />)

    expect(screen.getByText('Certificate ready')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'View' }))
    expect(screen.getByText('cn.adtyaicf.top')).toBeTruthy()
    expect(
      screen.getByText(
        'Step 2: Point your mainland China DNS line to the acceleration target'
      )
    ).toBeTruthy()

    cleanup()

    const pendingReadyCertSite: SiteDomain = {
      ...readySite,
      id: 13,
      status: 'pending',
      verify_records: JSON.stringify([
        {
          type: 'CNAME',
          name: 'portal.shop.example',
          value: 'edge.adtyaicf.top',
        },
      ]),
    }
    renderWithQuery(
      <SiteSettingsPanel site={makeOwnerSite([pendingReadyCertSite])} />
    )
    fireEvent.click(screen.getByRole('button', { name: 'View' }))
    expect(screen.getByText('_acme-challenge.portal.shop.example')).toBeTruthy()
    // Even if edge cert is already pre-issued, Step 2 stays gated until domain is active.
    expect(screen.queryByText('cn.adtyaicf.top')).toBeNull()
  })

  it('supports Check DNS now on pending custom domains and Check certificate on active custom site domains', async () => {
    const pendingOwnerDomain: SiteDomain = {
      id: 21,
      hostname: 'api.n8-n.top',
      role: 'base_url',
      managed_by: 'owner',
      status: 'pending',
      automation_state: 'retry',
      ssl_status: '',
      verify_records: JSON.stringify([
        { type: 'CNAME', name: 'api.n8-n.top', value: 'edge.adtyaicf.top' },
      ]),
      last_error: 'Domain CNAME is not pointing to the target yet',
      created_at: 1700000000,
    }
    const platformPendingDomain: SiteDomain = {
      id: 22,
      hostname: 'shop.adtyai-fz.top',
      role: 'site',
      managed_by: 'platform',
      status: 'pending',
      ssl_status: '',
      verify_records: '[]',
      last_error: '',
      created_at: 1700000000,
    }
    const activeOwnerSiteDomain: SiteDomain = {
      id: 23,
      hostname: 'www.n8-n.top',
      role: 'site',
      managed_by: 'owner',
      status: 'active',
      ssl_status: 'active',
      verify_records: '[]',
      last_error: '',
      edge_acme_alias: 'd23-99887766',
      edge_cert_status: 'waiting_dns',
      created_at: 1700000000,
    }

    const postSpy = vi
      .spyOn(api, 'post')
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: {
            ...pendingOwnerDomain,
            last_error: 'Domain CNAME is not pointing to the target yet',
          },
        },
      } as never)
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: {
            ...pendingOwnerDomain,
            status: 'active',
            automation_state: 'ready',
            ssl_status: 'active',
            last_error: '',
          },
        },
      } as never)
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: {
            ...activeOwnerSiteDomain,
            edge_cert_status: 'ready',
            edge_cert_error: '',
          },
        },
      } as never)

    try {
      renderWithQuery(
        <SiteSettingsPanel
          site={makeOwnerSite([
            pendingOwnerDomain,
            platformPendingDomain,
            activeOwnerSiteDomain,
          ])}
        />
      )

      const buttons = screen.getAllByRole('button', { name: 'Check DNS now' })
      expect(buttons).toHaveLength(1)

      fireEvent.click(buttons[0])
      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/adty/site/domains/21/check',
          { mode: 'dns' }
        )
        expect(toast.error).toHaveBeenCalledWith(
          'Domain CNAME is not pointing to the target yet'
        )
      })

      fireEvent.click(buttons[0])
      await waitFor(() => {
        expect(toast.success).toHaveBeenCalledWith(
          'Domain DNS verified and active'
        )
      })

      // Expand Mainland China acceleration on the active custom site domain and trigger Check certificate.
      fireEvent.click(screen.getByRole('button', { name: 'Configure' }))
      fireEvent.click(screen.getByRole('button', { name: 'Check certificate' }))
      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/adty/site/domains/23/check',
          { mode: 'edge_cert' }
        )
        expect(toast.success).toHaveBeenCalledWith(
          'Edge certificate is ready. You can now configure Step 2.'
        )
      })
    } finally {
      postSpy.mockRestore()
    }
  })
})
