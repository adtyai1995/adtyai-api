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
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { getOwnerSiteAuthConfig, saveOwnerSiteAuthConfig } from '../api'
import { SiteAuthPanel } from '../components/site-auth-panel'
import type { OwnerSite } from '../types'

const verifyMock = vi.fn()

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getOwnerSiteAuthConfig: vi.fn(),
    saveOwnerSiteAuthConfig: vi.fn(),
  }
})

vi.mock('../components/owner-verification', () => ({
  useOwnerVerification: () => ({
    verify: verifyMock,
    verifying: false,
  }),
}))

vi.mock('@/features/system-settings/hooks/use-draft-navigation', () => ({
  useDraftNavigation: vi.fn(),
}))

vi.mock('@tanstack/react-router', () => ({
  Link: (props: { children?: ReactNode; to?: string }) => (
    <a href={props.to ?? '#'}>{props.children}</a>
  ),
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
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

function makeOwnerSite(): OwnerSite {
  return {
    id: 1,
    status: 'active',
    subdomain: 'shop',
    root_domain: 'example.test',
    host: 'shop.example.test',
    base_url: 'https://shop.example.test',
    cost_factor: '1.0',
    custom_domain_enabled: true,
    sender_domain_enabled: false,
    brand: { name: 'Shop', logo_url: '', notice: '', support_contact: '' },
    bonus_methods: [],
    seo_index: true,
    close_execute_at: 0,
    prices: [],
    pricing_version: 1,
    domains: [],
    visibility: {},
  }
}

describe('SiteAuthPanel', () => {
  beforeEach(() => {
    verifyMock.mockResolvedValue('proof-token-123')
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('disables enabling email verification and links to mail config when site email is unconfigured', async () => {
    vi.mocked(getOwnerSiteAuthConfig).mockResolvedValue({
      password_login_enabled: true,
      register_enabled: true,
      password_register_enabled: true,
      email_verification_enabled: false,
      email_domain_restriction_enabled: false,
      email_alias_restriction_enabled: false,
      email_domain_whitelist: [],
      mail_configured: false,
    })

    renderWithQuery(<SiteAuthPanel site={makeOwnerSite()} />)

    await waitFor(() => {
      expect(screen.getByText('Basic Authentication')).toBeTruthy()
    })

    expect(screen.getByText('Email not configured')).toBeTruthy()
    expect(screen.getByText('Go to email configuration')).toBeTruthy()

    const emailSwitch = screen.getByRole('switch', {
      name: 'Email Verification',
    })
    expect(emailSwitch.getAttribute('aria-checked')).toBe('false')
    expect(emailSwitch.hasAttribute('data-disabled')).toBe(true)
    fireEvent.click(emailSwitch)
    expect(emailSwitch.getAttribute('aria-checked')).toBe('false')
  })

  it('validates domain whitelist and saves with step-up proof when mail is configured', async () => {
    vi.mocked(getOwnerSiteAuthConfig).mockResolvedValue({
      password_login_enabled: true,
      register_enabled: true,
      password_register_enabled: true,
      email_verification_enabled: false,
      email_domain_restriction_enabled: false,
      email_alias_restriction_enabled: false,
      email_domain_whitelist: [],
      mail_configured: true,
    })
    vi.mocked(saveOwnerSiteAuthConfig).mockResolvedValue({
      password_login_enabled: true,
      register_enabled: true,
      password_register_enabled: true,
      email_verification_enabled: true,
      email_domain_restriction_enabled: true,
      email_alias_restriction_enabled: true,
      email_domain_whitelist: ['example.com', 'company.com'],
      mail_configured: true,
    })

    renderWithQuery(<SiteAuthPanel site={makeOwnerSite()} />)

    await waitFor(() => {
      expect(screen.getByText('Email configured')).toBeTruthy()
    })

    fireEvent.click(screen.getByRole('switch', { name: 'Email Verification' }))
    fireEvent.click(
      screen.getByRole('switch', { name: 'Email Domain Restriction' })
    )
    fireEvent.click(
      screen.getByRole('switch', { name: 'Email Alias Restriction' })
    )

    // Empty whitelist blocks submit with inline error.
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      screen.getByText(
        'Cannot enable the email domain restriction. Enter the allowed email domains first.'
      )
    ).toBeTruthy()
    expect(saveOwnerSiteAuthConfig).not.toHaveBeenCalled()

    // Enter domains and submit.
    fireEvent.change(screen.getByLabelText('Email Domain Whitelist'), {
      target: { value: 'Example.com\ncompany.com\nexample.com' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => {
      expect(verifyMock).toHaveBeenCalledWith('auth.config', 1)
      expect(saveOwnerSiteAuthConfig).toHaveBeenCalledWith(
        {
          password_login_enabled: true,
          register_enabled: true,
          password_register_enabled: true,
          email_verification_enabled: true,
          email_domain_restriction_enabled: true,
          email_alias_restriction_enabled: true,
          email_domain_whitelist: ['example.com', 'company.com'],
        },
        'proof-token-123'
      )
    })
  })
})
