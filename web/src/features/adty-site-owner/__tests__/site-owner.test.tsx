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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { BonusPanel } from '../components/bonus-panel'
import { CustomerSheet } from '../components/customer-sheet'
import { EarningToMarginPanel } from '../components/earning-to-margin-panel'
import { EarningsPanel } from '../components/earnings-panel'
import { OverviewPanel } from '../components/overview-panel'
import { OwnerSelfUseHistory } from '../components/owner-self-use-history'
import { OwnerSelfUsePanel } from '../components/owner-self-use-panel'
import { OwnerVerificationProvider } from '../components/owner-verification'
import { PricingPanel } from '../components/pricing-panel'
import { SiteSettingsPanel } from '../components/site-settings-panel'
import type { OwnerEarnings, OwnerSite, SitePrice } from '../types'

const api = vi.hoisted(() => ({
  getOwnerStats: vi.fn(),
  getOwnerEarnings: vi.fn(),
  savePricing: vi.fn(),
  downloadExport: vi.fn(),
  getSenderDomain: vi.fn(),
  removeSenderDomain: vi.fn(),
  uploadLogo: vi.fn(),
  saveOwnerSite: vi.fn(),
  saveOwnerSiteMethodBonus: vi.fn(),
  transferEarningToMargin: vi.fn(),
  getOwnerCustomer: vi.fn(),
  getOwnerCustomerLogs: vi.fn(),
  getOwnerCustomerTopUps: vi.fn(),
  grantTrial: vi.fn(),
  getOwnerWallets: vi.fn(),
  getSelfUsePrincipal: vi.fn(),
  transferOwnerPrincipal: vi.fn(),
  createSelfUseKey: vi.fn(),
  getOwnerPrincipalTransfers: vi.fn(),
  getOwnerSelfUseUsage: vi.fn(),
}))
vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  ...api,
}))

// The step-up dialog itself is covered by the secure-verification tests; here
// we only control what proof (if any) the owner's verification returns.
const verification = vi.hoisted(() => ({ requestVerification: vi.fn() }))
vi.mock('@/features/auth/secure-verification', async (importOriginal) => ({
  ...(await importOriginal<
    typeof import('@/features/auth/secure-verification')
  >()),
  useSecureVerification: () => ({
    requestVerification: verification.requestVerification,
    requestLoginVerification: vi.fn(),
    cancel: vi.fn(),
    isActive: false,
    dialogProps: {
      state: { phase: 'idle' },
      passkeyDomains: null,
      onCancel: vi.fn(),
      onRetry: vi.fn(),
      onInputChange: vi.fn(),
      onVerify: vi.fn(),
    },
  }),
  SecureVerificationDialog: () => null,
}))

function price(overrides: Partial<SitePrice>): SitePrice {
  return {
    id: 1,
    site_id: 7,
    user_id: 0,
    group: '',
    model: '',
    markup: '1.2',
    pending_markup: '0',
    pending_at: 0,
    version: 3,
    ...overrides,
  }
}

function ownerSite(overrides: Partial<OwnerSite> = {}): OwnerSite {
  return {
    id: 7,
    status: 'active',
    subdomain: 'shop',
    root_domain: 'sites.example',
    host: 'shop.sites.example',
    base_url: 'https://shop.sites.example',
    cost_factor: '0.8',
    markup_min: '0',
    markup_max: '3',
    markup_increase_delay_hours: 24,
    custom_domain_enabled: false,
    sender_domain_enabled: false,
    brand: {
      name: '',
      logo_url: '',
      tagline: '',
      notice: '',
      support_contact: '',
    },
    bonus_methods: [],
    seo_index: false,
    agreement_version: '3',
    agreement_at: 1_700_000_000,
    prices: [price({})],
    pricing_version: 3,
    domains: [],
    visibility: {},
    ...overrides,
  }
}

function wrap(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <QueryClientProvider client={client}>
      <OwnerVerificationProvider>{node}</OwnerVerificationProvider>
    </QueryClientProvider>
  )
}

beforeEach(() => {
  api.savePricing.mockResolvedValue({
    version: 4,
    immediate: [''],
    pending: {},
  })
  api.removeSenderDomain.mockResolvedValue(undefined)
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('OwnerSelfUsePanel', () => {
  beforeEach(() => {
    api.getOwnerWallets.mockResolvedValue([
      { id: 1, kind: 'earnings', balance: '100', frozen_usd: '10' },
    ])
    api.getSelfUsePrincipal.mockResolvedValue({ available_usd: '0' })
    api.getOwnerPrincipalTransfers.mockResolvedValue({ items: [], total: 0 })
    api.getOwnerSelfUseUsage.mockResolvedValue({ items: [], total: 0 })
  })

  it('confirms the credit and available earnings, reuses a timed-out transfer, and refreshes balances', async () => {
    api.transferOwnerPrincipal
      .mockRejectedValueOnce(new Error('timeout'))
      .mockImplementationOnce(async () => {
        api.getSelfUsePrincipal.mockResolvedValue({ available_usd: '20' })
        return { transfer_id: 'owner-receipt' }
      })
    render(wrap(<OwnerSelfUsePanel />))
    const amount = await screen.findByLabelText(
      'Transfer to personal balance (USD)'
    )
    await userEvent.type(amount, '20')
    await userEvent.click(
      screen.getByRole('button', { name: 'Transfer to personal balance' })
    )
    const dialog = screen.getByRole('alertdialog')
    expect(dialog).toHaveTextContent('Site principal credit')
    expect(dialog).toHaveTextContent('70')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Continue' })
    )
    await waitFor(() =>
      expect(api.transferOwnerPrincipal).toHaveBeenCalledTimes(1)
    )
    const intent = api.transferOwnerPrincipal.mock.calls[0]?.[1]
    await waitFor(() =>
      expect(
        within(dialog).getByRole('button', { name: 'Continue' })
      ).toBeEnabled()
    )
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Continue' })
    )
    expect(await screen.findByText(/owner-receipt/)).toBeInTheDocument()
    expect(api.transferOwnerPrincipal).toHaveBeenLastCalledWith('20', intent)
    await waitFor(() =>
      expect(
        screen.getByLabelText('Site self-use principal')
      ).toHaveTextContent('20')
    )
  })

  it('blocks transfers while balances are unavailable and supports retry', async () => {
    api.getSelfUsePrincipal.mockRejectedValueOnce(new Error('unavailable'))
    render(wrap(<OwnerSelfUsePanel />))
    await userEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByLabelText('Transfer to personal balance (USD)')
    ).toBeInTheDocument()
  })

  it('rejects frozen funds and fractional quota amounts, and only displays a key after creation', async () => {
    api.createSelfUseKey.mockResolvedValue('sk-owner-once')
    render(wrap(<OwnerSelfUsePanel />))
    const amount = await screen.findByLabelText(
      'Transfer to personal balance (USD)'
    )
    await userEvent.type(amount, '91')
    expect(
      screen.getByRole('button', { name: 'Transfer to personal balance' })
    ).toBeDisabled()
    await userEvent.clear(amount)
    await userEvent.type(amount, '0.000001')
    expect(
      screen.getByRole('button', { name: 'Transfer to personal balance' })
    ).toBeDisabled()
    await userEvent.click(
      screen.getByRole('button', { name: 'Create site self-use key' })
    )
    expect(await screen.findByText('sk-owner-once')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(screen.queryByText('sk-owner-once')).not.toBeInTheDocument()
  })
})

describe('OwnerSelfUseHistory', () => {
  it('labels dollar prices with their billing units and does not invent a flat tiered price', async () => {
    api.getOwnerPrincipalTransfers.mockResolvedValue({ items: [], total: 0 })
    const item = {
      request_id: 'owner-request',
      created_at: 1700000000,
      model_name: 'owner-model',
      billing_group: 'default',
      charged_usd: '0.8',
      prompt_tokens: 12,
      completion_tokens: 8,
    }
    api.getOwnerSelfUseUsage.mockResolvedValue({
      items: [
        {
          ...item,
          operation_key: 'token',
          billing_unit: 'per_million_input_tokens',
          supply_unit_price: '2.4',
        },
        {
          ...item,
          operation_key: 'call',
          billing_unit: 'per_call',
          supply_unit_price: '0.8',
        },
        {
          ...item,
          operation_key: 'expression',
          billing_unit: 'tiered_expr',
          supply_unit_price: '',
        },
      ],
      total: 3,
    })
    render(wrap(<OwnerSelfUseHistory />))
    const priceCell = await screen.findByRole('cell', {
      name: /Per 1M input tokens/,
    })
    expect(priceCell).toBeVisible()
    expect(screen.getByRole('table', { name: 'Owner API usage' })).toBeVisible()
    expect(
      screen.getByRole('table', { name: 'Owner principal transfers' })
    ).toBeVisible()
    expect(screen.getByRole('cell', { name: /Per call/ })).toBeVisible()
    expect(screen.getByText('Tiered pricing')).toBeVisible()
    expect(screen.queryByText('per_token')).not.toBeInTheDocument()
    expect(screen.queryByText('tiered_expr')).not.toBeInTheDocument()
    expect(priceCell).toHaveTextContent('2.40')
  })
})

describe('PricingPanel', () => {
  it('lists a saved increase that is not in effect yet with its new markup', () => {
    render(
      wrap(
        <PricingPanel
          site={ownerSite({
            prices: [
              price({}),
              price({
                id: 2,
                group: 'vip',
                markup: '1.5',
                pending_markup: '2',
                pending_at: 1_800_000_000,
              }),
            ],
          })}
        />
      )
    )
    const rows = screen.getAllByTestId('pending-increase')
    expect(rows).toHaveLength(1)
    expect(rows[0]).toHaveTextContent('vip: 1.5 → 2')
    expect(screen.getByText(/after 24 hours/)).toBeInTheDocument()
  })

  it('shows no pending section when nothing is waiting', () => {
    render(wrap(<PricingPanel site={ownerSite()} />))
    expect(screen.queryByTestId('pending-increase')).not.toBeInTheDocument()
  })

  it('lets the owner enter a markup below 1 when the API minimum is 0', async () => {
    verification.requestVerification.mockResolvedValue({
      proof_token: 'proof-k',
    })
    render(wrap(<PricingPanel site={ownerSite()} />))
    const field = screen.getByLabelText('Default markup')
    expect(field).toHaveAttribute('min', '0')
    await userEvent.clear(field)
    await userEvent.type(field, '0.8')
    await userEvent.click(screen.getByRole('button', { name: 'Save prices' }))
    await waitFor(() =>
      expect(api.savePricing).toHaveBeenCalledWith(
        expect.objectContaining({ default_markup: '0.8', expected_version: 3 }),
        'proof-k'
      )
    )
    expect(screen.getByText(/Allowed markup: 0 to 3/)).toBeInTheDocument()
  })

  it('sends the proof for site-wide pricing with the current version', async () => {
    verification.requestVerification.mockResolvedValue({
      proof_token: 'proof-1',
    })
    render(wrap(<PricingPanel site={ownerSite()} />))
    await userEvent.clear(screen.getByLabelText('Default markup'))
    await userEvent.type(screen.getByLabelText('Default markup'), '1.1')
    await userEvent.click(screen.getByRole('button', { name: 'Save prices' }))

    await waitFor(() => expect(api.savePricing).toHaveBeenCalled())
    expect(verification.requestVerification).toHaveBeenCalledWith(
      expect.objectContaining({
        scope: 'site.owner.manage',
        context: { action: 'pricing', target: 0 },
      })
    )
    expect(api.savePricing).toHaveBeenCalledWith(
      expect.objectContaining({ default_markup: '1.1', expected_version: 3 }),
      'proof-1'
    )
  })

  it('does not save when the owner cancels the verification', async () => {
    verification.requestVerification.mockResolvedValue(null)
    render(wrap(<PricingPanel site={ownerSite()} />))
    await userEvent.click(screen.getByRole('button', { name: 'Save prices' }))
    await waitFor(() =>
      expect(verification.requestVerification).toHaveBeenCalled()
    )
    expect(api.savePricing).not.toHaveBeenCalled()
  })

  it('does not lock the form for a leftover paused site', () => {
    render(wrap(<PricingPanel site={ownerSite({ status: 'paused' })} />))
    expect(screen.getByRole('button', { name: 'Save prices' })).toBeEnabled()
    expect(screen.getByLabelText('Default markup')).toBeEnabled()
  })

  it('locks the form while the site is closing', () => {
    render(wrap(<PricingPanel site={ownerSite({ status: 'closing' })} />))
    expect(screen.getByRole('button', { name: 'Save prices' })).toBeDisabled()
    expect(screen.getByLabelText('Default markup')).toBeDisabled()
  })

  it('lists a pending independent model markup and can remove it to restore global', async () => {
    verification.requestVerification.mockResolvedValue({
      proof_token: 'proof-model',
    })
    render(
      wrap(
        <PricingPanel
          site={ownerSite({
            prices: [
              price({}),
              price({
                id: 3,
                model: 'gpt-4',
                markup: '1.5',
                pending_markup: '1.8',
                pending_at: 1_800_000_000,
              }),
            ],
          })}
        />
      )
    )
    const rows = screen.getAllByTestId('pending-increase')
    expect(rows).toHaveLength(1)
    expect(rows[0]).toHaveTextContent('gpt-4: 1.5 → 1.8')
    expect(screen.getByDisplayValue('gpt-4')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Remove' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save prices' }))
    await waitFor(() => expect(api.savePricing).toHaveBeenCalled())
    expect(api.savePricing).toHaveBeenCalledWith(
      expect.objectContaining({
        model_markups: { 'gpt-4': '0' },
        expected_version: 3,
      }),
      'proof-model'
    )
  })
})

describe('OverviewPanel', () => {
  it('does not show a period or cumulative top-up total', async () => {
    api.getOwnerStats.mockResolvedValue({
      from: '2026-10-15',
      to: '2026-11-14',
      total: {
        day: '',
        new_customers: 2,
        paid_customers: 1,
        active_customers: 1,
        topup_usd: '88',
        revenue_usd: '12',
        platform_usd: '8',
        earning_usd: '4',
        bonus_usd: '0',
        refund_usd: '0',
        skipped_usd: '0',
      },
      series: [],
    })
    api.getOwnerEarnings.mockResolvedValue({
      balances: {
        pending_usd: '4',
        skipped_usd: '0',
        available_usd: '0',
        frozen_usd: '0',
        withdrawn_usd: '0',
      },
      page: { page: 1, page_size: 20, total: 0, items: [] },
    })
    render(wrap(<OverviewPanel />))
    expect(await screen.findByText('Customer spend')).toBeInTheDocument()
    expect(screen.getByText('New customers')).toBeInTheDocument()
    expect(screen.getByText('Paying customers')).toBeInTheDocument()
    expect(screen.getByText('$12.00')).toBeInTheDocument()
    expect(screen.getAllByText('$0.00').length).toBeGreaterThan(0)
    expect(screen.queryByText('Top-ups')).not.toBeInTheDocument()
    expect(screen.queryByText('$88.00')).not.toBeInTheDocument()
  })

  it('shows a loading state for amounts instead of $0.00 while queries are pending', async () => {
    api.getOwnerStats.mockReturnValue(new Promise(() => {}))
    api.getOwnerEarnings.mockReturnValue(new Promise(() => {}))
    render(wrap(<OverviewPanel />))
    expect(await screen.findByText('Loading...')).toBeInTheDocument()
    expect(screen.queryByText('$0.00')).not.toBeInTheDocument()
  })
})

describe('EarningsPanel', () => {
  const earnings: OwnerEarnings = {
    balances: {
      pending_usd: '12.5',
      skipped_usd: '0',
      available_usd: '-8',
      frozen_usd: '0',
      withdrawn_usd: '30',
    },
    page: {
      page: 1,
      page_size: 20,
      total: 2,
      items: [
        {
          day: '2026-10-01',
          user_id: 5,
          kind: 'usage',
          revenue_usd: '125',
          earning_usd: '45',
          status: 'available',
          settled_at: 1_700_000_000,
        },
        {
          day: '2026-10-01+1',
          user_id: 5,
          kind: 'bonus',
          revenue_usd: '0',
          earning_usd: '-5',
          status: 'pending',
          settled_at: 0,
        },
      ],
    },
  }

  it('marks negative balances and negative rows in red, and leaves positive ones alone', async () => {
    api.getOwnerEarnings.mockResolvedValue(earnings)
    render(wrap(<EarningsPanel />))

    const negativeRow = await screen.findByText('$-5.00')
    expect(negativeRow).toHaveAttribute('data-negative', 'true')
    expect(screen.getByText('$45.00')).not.toHaveAttribute('data-negative')
    expect(screen.getByText('$-8.00')).toHaveAttribute('data-negative', 'true')
    expect(screen.getByText('$12.50')).not.toHaveAttribute('data-negative')
    expect(screen.getAllByText('2026-10-01')).toHaveLength(2)
  })

  it('shows a loading state for amounts instead of $0.00 while the query is pending', async () => {
    api.getOwnerEarnings.mockReturnValue(new Promise(() => {}))
    render(wrap(<EarningsPanel />))
    expect(await screen.findByText('Loading...')).toBeInTheDocument()
    expect(screen.queryByText('$0.00')).not.toBeInTheDocument()
    expect(screen.queryByText('Pending settlement')).not.toBeInTheDocument()
  })

  it('points withdrawals to the earnings wallet withdrawal on this tab, not the Referral page', async () => {
    api.getOwnerEarnings.mockResolvedValue(earnings)
    render(wrap(<EarningsPanel />))
    const guidance = await screen.findByText(
      'Withdraw site earnings using Earnings wallet withdrawal on this tab. A negative balance cannot be withdrawn.'
    )
    expect(guidance).toBeInTheDocument()
    expect(guidance).toHaveTextContent('A negative balance cannot be withdrawn.')
    expect(guidance).not.toHaveTextContent('Referral page')
    expect(guidance).not.toHaveTextContent('referral commissions')
    expect(
      screen.queryByText(
        /settle into the same account as referral commissions|withdraw them on the Referral page/i
      )
    ).not.toBeInTheDocument()
  })
})

describe('Sender domain', () => {
  const pending = {
    site_id: 7,
    domain: 'mail.shop.example',
    status: 'pending',
    records: JSON.stringify([
      {
        record: 'SPF',
        name: 'send.mail.shop.example',
        type: 'TXT',
        value: 'v=spf1 include:amazonses.com ~all',
        ttl: 'Auto',
        status: 'not_started',
      },
    ]),
    disabled_by: 0,
    last_checked_at: 0,
    created_at: 0,
  }

  it('shows Not available yet when the platform has sender domains switched off', () => {
    render(
      wrap(
        <SiteSettingsPanel site={ownerSite({ sender_domain_enabled: false })} />
      )
    )
    expect(screen.getByText('Not available yet')).toBeInTheDocument()
    expect(api.getSenderDomain).not.toHaveBeenCalled()
  })

  it('lists the DNS records to add while the domain waits for verification', async () => {
    api.getSenderDomain.mockResolvedValue(pending)
    render(
      wrap(
        <SiteSettingsPanel site={ownerSite({ sender_domain_enabled: true })} />
      )
    )
    const table = await screen.findByRole('table', {
      name: 'DNS records to add',
    })
    expect(table).toHaveTextContent('send.mail.shop.example')
    expect(table).toHaveTextContent('v=spf1 include:amazonses.com ~all')
    expect(screen.getByText('Waiting for DNS')).toBeInTheDocument()
  })

  it('hides the records once the domain is verified', async () => {
    api.getSenderDomain.mockResolvedValue({ ...pending, status: 'verified' })
    render(
      wrap(
        <SiteSettingsPanel site={ownerSite({ sender_domain_enabled: true })} />
      )
    )
    expect(await screen.findByText('Verified')).toBeInTheDocument()
    expect(
      screen.queryByRole('table', { name: 'DNS records to add' })
    ).not.toBeInTheDocument()
  })

  it('asks for a sender.delete proof bound to the site before deleting', async () => {
    api.getSenderDomain.mockResolvedValue(pending)
    verification.requestVerification.mockResolvedValue({
      proof_token: 'proof-s',
    })
    render(
      wrap(
        <SiteSettingsPanel site={ownerSite({ sender_domain_enabled: true })} />
      )
    )
    const name = await screen.findByText('mail.shop.example')
    const card = name.closest('div.rounded-md') as HTMLElement
    await userEvent.click(within(card).getByRole('button', { name: 'Delete' }))
    await userEvent.click(
      await screen.findByRole('button', { name: 'Continue' })
    )

    await waitFor(() =>
      expect(api.removeSenderDomain).toHaveBeenCalledWith('proof-s')
    )
    expect(verification.requestVerification).toHaveBeenCalledWith(
      expect.objectContaining({
        context: { action: 'sender.delete', target: 7 },
      })
    )
  })
})

describe('Brand logo', () => {
  it('saves the uploaded logo address with the brand', async () => {
    api.uploadLogo.mockResolvedValue('/uploads/site-assets/site-7-abc.png')
    api.saveOwnerSite.mockResolvedValue(ownerSite())
    render(wrap(<SiteSettingsPanel site={ownerSite()} />))
    const file = new File(['png'], 'logo.png', { type: 'image/png' })
    await userEvent.upload(screen.getByLabelText('Logo'), file)
    expect(await screen.findByRole('img', { name: 'Logo' })).toHaveAttribute(
      'src',
      '/uploads/site-assets/site-7-abc.png'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save brand' }))
    await waitFor(() =>
      expect(api.saveOwnerSite).toHaveBeenCalledWith({
        brand: expect.objectContaining({
          logo_url: '/uploads/site-assets/site-7-abc.png',
        }),
      })
    )
  })
})

describe('BonusPanel', () => {
  it('edits a payment-method gift with step-up proof and shows no amount tiers', async () => {
    verification.requestVerification.mockResolvedValue({
      proof_token: 'proof-bonus',
    })
    api.saveOwnerSiteMethodBonus.mockResolvedValue(undefined)
    render(
      wrap(
        <BonusPanel
          site={ownerSite({
            bonus_methods: [{ method: 'alipay', enabled: false, rate: '0' }],
          })}
        />
      )
    )
    expect(screen.getByText('Alipay')).toBeInTheDocument()
    expect(screen.queryByText('Top-up from (USD)')).not.toBeInTheDocument()
    const rate = screen.getByLabelText('Bonus rate')
    await userEvent.clear(rate)
    await userEvent.type(rate, '0.025')
    await userEvent.click(screen.getByRole('switch'))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(api.saveOwnerSiteMethodBonus).toHaveBeenCalledWith(
        'alipay',
        true,
        '0.025',
        'proof-bonus'
      )
    )
    expect(verification.requestVerification).toHaveBeenCalledWith(
      expect.objectContaining({
        scope: 'site.owner.manage',
        context: { action: 'bonus', target: 7 },
      })
    )
  })

  it('does not save bonus when the owner cancels verification', async () => {
    verification.requestVerification.mockResolvedValue(null)
    render(
      wrap(
        <BonusPanel
          site={ownerSite({
            bonus_methods: [{ method: 'alipay', enabled: false, rate: '0' }],
          })}
        />
      )
    )
    await userEvent.click(screen.getByRole('switch'))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(verification.requestVerification).toHaveBeenCalled()
    )
    expect(api.saveOwnerSiteMethodBonus).not.toHaveBeenCalled()
  })

  it('says no method is authorized when the platform authorized none', () => {
    render(wrap(<BonusPanel site={ownerSite()} />))
    expect(
      screen.getByText(/No payment method is authorized for a gift yet/)
    ).toBeInTheDocument()
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  })
})

describe('EarningToMarginPanel idempotency', () => {
  it('reuses the same idempotency key on retry and rotates on payload change or success', async () => {
    let callCount = 0
    api.transferEarningToMargin.mockImplementation(async () => {
      callCount += 1
      if (callCount === 1) throw new Error('network error')
    })
    render(wrap(<EarningToMarginPanel />))
    const input = screen.getByLabelText('Amount')
    const button = screen.getByRole('button', {
      name: 'Transfer earnings to margin',
    })

    await userEvent.type(input, '10')
    await userEvent.click(button)
    await waitFor(() =>
      expect(api.transferEarningToMargin).toHaveBeenCalledTimes(1)
    )
    const firstKey = api.transferEarningToMargin.mock.calls[0]?.[1]
    expect(firstKey).toBeTruthy()

    // Retry same payload -> same key
    await userEvent.click(button)
    await waitFor(() =>
      expect(api.transferEarningToMargin).toHaveBeenCalledTimes(2)
    )
    const secondKey = api.transferEarningToMargin.mock.calls[1]?.[1]
    expect(secondKey).toBe(firstKey)

    // After success, submitting same amount again rotates key
    await userEvent.type(input, '10')
    await userEvent.click(button)
    await waitFor(() =>
      expect(api.transferEarningToMargin).toHaveBeenCalledTimes(3)
    )
    const thirdKey = api.transferEarningToMargin.mock.calls[2]?.[1]
    expect(thirdKey).not.toBe(firstKey)

    // If failed then payload changed -> rotates key
    api.transferEarningToMargin.mockRejectedValueOnce(new Error('network error'))
    await userEvent.type(input, '20')
    await userEvent.click(button)
    await waitFor(() =>
      expect(api.transferEarningToMargin).toHaveBeenCalledTimes(4)
    )
    const fourthKey = api.transferEarningToMargin.mock.calls[3]?.[1]

    api.transferEarningToMargin.mockResolvedValueOnce(undefined)
    await userEvent.clear(input)
    await userEvent.type(input, '25')
    await userEvent.click(button)
    await waitFor(() =>
      expect(api.transferEarningToMargin).toHaveBeenCalledTimes(5)
    )
    const fifthKey = api.transferEarningToMargin.mock.calls[4]?.[1]
    expect(fifthKey).not.toBe(fourthKey)
  })
})

describe('CustomerSheet trial idempotency', () => {
  it('reuses the same idempotency key on retry and rotates on amount change or success', async () => {
    verification.requestVerification.mockResolvedValue({
      proof_token: 'proof-trial',
    })
    api.getOwnerCustomer.mockResolvedValue({
      customer: { id: 9, username: 'bob', created_at: 1_700_000_000 },
      prices: [],
      tokens: [],
    })
    api.getOwnerCustomerLogs.mockResolvedValue({ items: [], total: 0 })
    api.getOwnerCustomerTopUps.mockResolvedValue({ items: [], total: 0 })
    let attempts = 0
    api.grantTrial.mockImplementation(async () => {
      attempts += 1
      if (attempts === 1) throw new Error('timeout')
    })

    render(wrap(<CustomerSheet customerId={9} onOpenChange={() => {}} />))
    const input = await screen.findByLabelText('Amount (USD)')
    const sendBtn = screen.getByRole('button', { name: 'Send' })

    await userEvent.type(input, '5')
    await userEvent.click(sendBtn)
    await waitFor(() => expect(api.grantTrial).toHaveBeenCalledTimes(1))
    const key1 = api.grantTrial.mock.calls[0]?.[2]
    expect(key1).toBeTruthy()

    // Retry same amount -> same key
    await userEvent.click(sendBtn)
    await waitFor(() => expect(api.grantTrial).toHaveBeenCalledTimes(2))
    const key2 = api.grantTrial.mock.calls[1]?.[2]
    expect(key2).toBe(key1)

    // New intent after success -> rotates key
    await userEvent.type(input, '5')
    await userEvent.click(sendBtn)
    await waitFor(() => expect(api.grantTrial).toHaveBeenCalledTimes(3))
    const key3 = api.grantTrial.mock.calls[2]?.[2]
    expect(key3).not.toBe(key1)
  })
})
