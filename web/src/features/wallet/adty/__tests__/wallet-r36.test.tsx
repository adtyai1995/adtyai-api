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

import { SYSTEM_SETTINGS_VIEW } from '@/components/layout/config/system-settings.config'
import { BILLING_SECTION_IDS } from '@/features/system-settings/billing/section-registry'
import { useSidebarData } from '@/hooks/use-sidebar-data'
import { ROLE } from '@/lib/roles'
import { Route as BillingSectionRoute } from '@/routes/_authenticated/system-settings/billing/$section'
import { useAuthStore } from '@/stores/auth-store'

import {
  formatBonusRateFraction,
  formatBonusRatePercent,
  resolveMethodBonusPercent,
  type AdtyTopupInfo,
} from '../api'
import { AdtyOnlineTopup } from '../components/adty-online-topup'
import { AdtyRechargeCard } from '../components/adty-recharge-card'
import { VoucherRedeemCard } from '../voucher-redeem-card'

const { quoteAdtyTopupMock, getAdtyTopupInfoMock, getAdtyTopupHistoryMock } =
  vi.hoisted(() => ({
    quoteAdtyTopupMock: vi.fn(),
    getAdtyTopupInfoMock: vi.fn(),
    getAdtyTopupHistoryMock: vi.fn(),
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
    i18n: { language: 'en', resolvedLanguage: 'en' },
  }),
}))

vi.mock('@tanstack/react-router', async () => {
  const actual = await vi.importActual<typeof import('@tanstack/react-router')>(
    '@tanstack/react-router'
  )
  return {
    ...actual,
    Link: ({
      children,
      to,
      ...rest
    }: {
      children?: ReactNode
      to?: string
      className?: string
    }) => (
      <a href={to ?? '#'} {...rest}>
        {children}
      </a>
    ),
  }
})

vi.mock('@/features/adty-site/api', () => ({
  useAdtySite: () => ({
    data: {
      voucher_purchase_url: '',
      wallet_notice: '',
    },
  }),
}))

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    quoteAdtyTopup: (...args: unknown[]) => quoteAdtyTopupMock(...args),
    getAdtyTopupInfo: (...args: unknown[]) => getAdtyTopupInfoMock(...args),
    getAdtyTopupHistory: (...args: unknown[]) =>
      getAdtyTopupHistoryMock(...args),
  }
})

vi.mock('../components/adty-topup-history', () => ({
  AdtyTopupHistory: () => <div data-testid='mock-topup-history'>History</div>,
}))

vi.mock('../components/adty-payment-dialogs', () => ({
  AdtyPaymentDialogs: () => null,
}))

vi.mock('../components/adty-pending-banner', () => ({
  AdtyPendingBanner: () => null,
}))

vi.mock('@/hooks/use-sidebar-badges', async () => {
  const actual = await vi.importActual<
    typeof import('@/hooks/use-sidebar-badges')
  >('@/hooks/use-sidebar-badges')
  return {
    ...actual,
    useSidebarBadges: () => ({
      counts: {},
      badge: () => undefined,
    }),
  }
})

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: undefined }),
}))

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

const SINGLE_SCENE_INFO: AdtyTopupInfo = {
  min_topup: 1,
  price_cny_per_usd: 7,
  usdt_order_minutes: 15,
  epay_order_minutes: 10,
  pending: null,
  tiers: [
    { amount: 10, rate: 1 },
    { amount: 50, rate: 1 },
  ],
  methods: [],
  scenes: [
    {
      key: 'wallet_topup',
      name: 'Wallet top-up',
      min_usd: '1',
      max_usd: '1000',
      methods: [
        {
          id: 'alipay',
          label: 'Alipay',
          kind: 'epay',
          currency: 'CNY',
          available: true,
        },
      ],
    },
  ],
}

const MULTI_SCENE_INFO: AdtyTopupInfo = {
  ...SINGLE_SCENE_INFO,
  scenes: [
    ...(SINGLE_SCENE_INFO.scenes ?? []),
    {
      key: 'vip_topup',
      name: 'VIP top-up',
      min_usd: '10',
      max_usd: '5000',
      methods: [
        {
          id: 'usdt.trc20',
          label: 'USDT TRC20',
          kind: 'usdt',
          currency: 'USDT',
          available: true,
        },
      ],
    },
  ],
}

describe('R-36 Items 2, 3, and 4', () => {
  beforeEach(() => {
    quoteAdtyTopupMock.mockResolvedValue({
      available: true,
      quote: {
        amount_usd: '10',
        face_usd: '10',
        bonus_usd: '0',
        credit_quota: 5_000_000,
        pay_amount: '70.00',
        pay_currency: 'CNY',
        price_cny_per_usd: '7.00',
      },
    })
    getAdtyTopupInfoMock.mockResolvedValue(SINGLE_SCENE_INFO)
    getAdtyTopupHistoryMock.mockResolvedValue({ items: [], total: 0 })
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'root',
      role: ROLE.SUPER_ADMIN,
    })
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
    useAuthStore.getState().auth.reset('complete')
  })

  it('hides Payment scene dropdown when only 1 scene exists while still quoting and paying with that scene', async () => {
    const onPay = vi.fn()
    renderWithQuery(
      <AdtyRechargeCard
        info={SINGLE_SCENE_INFO}
        loading={false}
        paying={false}
        onPay={onPay}
      />
    )

    expect(screen.queryByLabelText('Payment scene')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: '$10' }))
    fireEvent.click(screen.getByRole('button', { name: /Alipay/i }))

    await waitFor(() => {
      expect(quoteAdtyTopupMock).toHaveBeenCalledWith(
        10,
        'alipay',
        'wallet_topup',
        undefined,
        undefined
      )
    })

    const payButton = screen.getByRole('button', { name: 'Pay now' })
    await waitFor(() => {
      expect((payButton as HTMLButtonElement).disabled).toBe(false)
    })
    fireEvent.click(payButton)
    expect(onPay).toHaveBeenCalledWith(
      10,
      expect.objectContaining({ id: 'alipay' }),
      'wallet_topup'
    )
  })

  it('shows Payment scene dropdown when multiple scenes exist', () => {
    renderWithQuery(
      <AdtyRechargeCard
        info={MULTI_SCENE_INFO}
        loading={false}
        paying={false}
        onPay={vi.fn()}
      />
    )

    const select = screen.getByLabelText('Payment scene') as HTMLSelectElement
    expect(select).toBeTruthy()
    expect(select.value).toBe('wallet_topup')
  })

  it('uses unified redemption code terminology in VoucherRedeemCard', () => {
    renderWithQuery(<VoucherRedeemCard onRedeemed={vi.fn()} />)

    expect(screen.getByText('Credit Redemption Code')).toBeTruthy()
    expect(
      screen.getByText(
        'A redemption code adds principal once. It does not add a bonus.'
      )
    ).toBeTruthy()
    expect(screen.getByLabelText('Redemption code')).toBeTruthy()
    expect(screen.queryByText('Redeem a code')).toBeNull()
    expect(screen.queryByLabelText('Recharge code')).toBeNull()
  })

  it('renders afterRechargeSlot between Online Top-up card and Top-up History', async () => {
    const { container } = renderWithQuery(
      <AdtyOnlineTopup
        onSettled={async () => {}}
        afterRechargeSlot={<VoucherRedeemCard onRedeemed={vi.fn()} />}
      />
    )

    await waitFor(() => {
      expect(screen.getByText('Add funds')).toBeTruthy()
    })

    const rechargeSection = container.querySelector('#wallet-add-funds')
    const voucherHeading = screen.getByText('Credit Redemption Code')
    const historyNode = screen.getByTestId('mock-topup-history')

    expect(rechargeSection).toBeTruthy()
    if (!rechargeSection) {
      throw new Error('Expected #wallet-add-funds container to be rendered')
    }
    expect(
      rechargeSection.compareDocumentPosition(voucherHeading) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    expect(
      voucherHeading.compareDocumentPosition(historyNode) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
  })

  it('exposes standalone Recharge Bonus in main-site Administration sidebar and removes it from System Settings', () => {
    expect(BILLING_SECTION_IDS as readonly string[]).not.toContain(
      'recharge-bonus'
    )

    const systemGroups = SYSTEM_SETTINGS_VIEW.getNavGroups(
      ((key: string) => key) as never
    )
    const billingGroup = systemGroups[0]?.items.find(
      (item) => item.title === 'Billing & Payment'
    )
    expect(billingGroup && 'items' in billingGroup).toBe(true)
    if (billingGroup && 'items' in billingGroup && billingGroup.items) {
      expect(
        billingGroup.items.some(
          (item) => item.url === '/system-settings/billing/recharge-bonus'
        )
      ).toBe(false)
    }

    let capturedUrls: string[] = []
    let capturedRole: number | undefined
    function Probe() {
      const data = useSidebarData()
      const admin = data.navGroups.find((group) => group.id === 'admin')
      capturedUrls = (admin?.items ?? []).map((item) =>
        'url' in item && item.url ? String(item.url) : ''
      )
      const bonusItem = (admin?.items ?? []).find(
        (item) => 'url' in item && item.url === '/recharge-bonus'
      )
      capturedRole = bonusItem?.requiredRole
      return null
    }
    render(<Probe />)

    const auditIdx = capturedUrls.indexOf('/recharge-audit')
    const bonusIdx = capturedUrls.indexOf('/recharge-bonus')
    const vouchersIdx = capturedUrls.indexOf('/vouchers')
    expect(auditIdx).toBeGreaterThanOrEqual(0)
    expect(bonusIdx).toBe(auditIdx + 1)
    expect(vouchersIdx).toBe(bonusIdx + 1)
    expect(capturedRole).toBe(ROLE.SUPER_ADMIN)
  })

  it('redirects legacy /system-settings/billing/recharge-bonus to /recharge-bonus', () => {
    const beforeLoad = BillingSectionRoute.options.beforeLoad
    expect(beforeLoad).toBeTypeOf('function')
    let caught: unknown
    try {
      beforeLoad?.({
        params: { section: 'recharge-bonus' },
      } as never)
    } catch (err) {
      caught = err
    }
    expect(caught).toMatchObject({
      options: expect.objectContaining({
        to: '/recharge-bonus',
        replace: true,
      }),
    })
  })

  it('formats bonus rate percentages <= 1% without multiplying by 100 and scales fraction rates by 100 (R1-09, R2-01)', async () => {
    expect(formatBonusRatePercent('0.5')).toBe('0.5')
    expect(formatBonusRatePercent('1')).toBe('1')
    expect(formatBonusRatePercent('2.5')).toBe('2.5')
    expect(formatBonusRateFraction('0.005')).toBe('0.5')
    expect(formatBonusRateFraction('0.01')).toBe('1')
    expect(formatBonusRateFraction('0.05')).toBe('5')
    expect(resolveMethodBonusPercent('usdt.trc20', { usdt: '0.005' })).toBe(
      '0.5'
    )

    quoteAdtyTopupMock.mockResolvedValueOnce({
      available: true,
      quote: {
        amount_usd: '100',
        face_usd: '100',
        bonus_rate: '0.005',
        bonus_usd: '0.50',
        credit_quota: 50_250_000,
        pay_amount: '700.00',
        pay_currency: 'CNY',
        price_cny_per_usd: '7.00',
      },
    })

    renderWithQuery(
      <AdtyRechargeCard
        info={SINGLE_SCENE_INFO}
        loading={false}
        paying={false}
        onPay={vi.fn()}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: '$10' }))
    fireEvent.click(screen.getByRole('button', { name: /Alipay/i }))

    await waitFor(() => {
      expect(screen.getByText(/\(\+0\.5%\)/)).toBeTruthy()
    })
    expect(screen.queryByText(/\(\+50%\)/)).toBeNull()
  })

  it('scales decimal quote.bonus_rate 0.05 to +5% in AdtyRechargeCard (R2-01)', async () => {
    quoteAdtyTopupMock.mockResolvedValueOnce({
      available: true,
      quote: {
        amount_usd: '100',
        face_usd: '100',
        bonus_rate: '0.05',
        bonus_usd: '5.00',
        credit_quota: 52_500_000,
        pay_amount: '700.00',
        pay_currency: 'CNY',
        price_cny_per_usd: '7.00',
      },
    })

    renderWithQuery(
      <AdtyRechargeCard
        info={SINGLE_SCENE_INFO}
        loading={false}
        paying={false}
        onPay={vi.fn()}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: '$10' }))
    fireEvent.click(screen.getByRole('button', { name: /Alipay/i }))

    await waitFor(() => {
      expect(screen.getByText(/\(\+5%\)/)).toBeTruthy()
    })
    expect(screen.queryByText(/\(\+0\.05%\)/)).toBeNull()
  })

  it('shows Referral sidebar entry for migrated main-site user with adty_has_legacy_site_commissions even when referral is off (R1-12)', () => {
    useAuthStore.getState().auth.setUser({
      id: 99,
      username: 'migrated',
      role: ROLE.USER,
      site_id: 0,
      adty_referral_enabled: false,
      adty_has_legacy_site_commissions: true,
    })

    let personalUrls: string[] = []
    function Probe() {
      const data = useSidebarData()
      const personal = data.navGroups.find((group) => group.id === 'personal')
      personalUrls = (personal?.items ?? []).map((item) =>
        'url' in item && item.url ? String(item.url) : ''
      )
      return null
    }
    render(<Probe />)
    expect(personalUrls).toContain('/referral')
  })
})
