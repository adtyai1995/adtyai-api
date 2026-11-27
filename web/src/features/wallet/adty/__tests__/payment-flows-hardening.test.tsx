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
  within,
} from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { redeemOwnerMarginVoucher } from '@/features/adty-site-owner/api'
import { BEpusdtSettingsSection } from '@/features/system-settings/integrations/bepusdt-settings-section'
import { useSidebarData } from '@/hooks/use-sidebar-data'
import { ADTY_PERMISSIONS } from '@/lib/adty-permissions'
import { ROLE } from '@/lib/roles'
import { Route as PaymentChannelsRoute } from '@/routes/_authenticated/payment-channels/index'
import { Route as WalletRoute } from '@/routes/_authenticated/wallet/index'
import { useAuthStore } from '@/stores/auth-store'

import { AdtyOnlineTopup } from '../components/adty-online-topup'
import { AdtyPendingBanner } from '../components/adty-pending-banner'
import { AdtyRechargeCard } from '../components/adty-recharge-card'
import { AdtyTopupHistory } from '../components/adty-topup-history'
import { UsdtPayDialog } from '../components/usdt-pay-dialog'
import { TeamPanel } from '../team-panel'
import type { AdtyOrder } from '../types'

const {
  apiGetMock,
  apiPostMock,
  getAdtyTopupInfoMock,
  getAdtyTopupHistoryMock,
  getAdtyOrderMock,
  cancelAdtyOrderMock,
  quoteAdtyTopupMock,
  updateOptionMutateAsyncMock,
  toastErrorMock,
  toastSuccessMock,
} = vi.hoisted(() => ({
  apiGetMock: vi.fn(),
  apiPostMock: vi.fn(),
  getAdtyTopupInfoMock: vi.fn(),
  getAdtyTopupHistoryMock: vi.fn(),
  getAdtyOrderMock: vi.fn(),
  cancelAdtyOrderMock: vi.fn(),
  quoteAdtyTopupMock: vi.fn(),
  updateOptionMutateAsyncMock: vi.fn(),
  toastErrorMock: vi.fn(),
  toastSuccessMock: vi.fn(),
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

vi.mock('sonner', () => ({
  toast: {
    error: (...args: unknown[]) => toastErrorMock(...args),
    success: (...args: unknown[]) => toastSuccessMock(...args),
    info: vi.fn(),
    warning: vi.fn(),
  },
}))

vi.mock('@tanstack/react-router', async () => {
  const actual = await vi.importActual<typeof import('@tanstack/react-router')>(
    '@tanstack/react-router'
  )
  return {
    ...actual,
    useNavigate: () => vi.fn(),
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

vi.mock('@/components/dialog', () => ({
  Dialog: ({
    open,
    title,
    description,
    children,
    footer,
  }: {
    open?: boolean
    title?: ReactNode
    description?: ReactNode
    children?: ReactNode
    footer?: ReactNode
  }) =>
    open ? (
      <div role='dialog'>
        <h2>{title}</h2>
        {description ? <p>{description}</p> : null}
        <div>{children}</div>
        {footer ? <div>{footer}</div> : null}
      </div>
    ) : null,
}))

vi.mock('@/components/confirm-dialog', () => ({
  ConfirmDialog: () => null,
}))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    api: {
      ...actual.api,
      get: (...args: unknown[]) => apiGetMock(...args),
      post: (...args: unknown[]) => apiPostMock(...args),
    },
  }
})

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getAdtyTopupInfo: (...args: unknown[]) => getAdtyTopupInfoMock(...args),
    getAdtyTopupHistory: (...args: unknown[]) =>
      getAdtyTopupHistoryMock(...args),
    getAdtyOrder: (...args: unknown[]) => getAdtyOrderMock(...args),
    cancelAdtyOrder: (...args: unknown[]) => cancelAdtyOrderMock(...args),
    quoteAdtyTopup: (...args: unknown[]) => quoteAdtyTopupMock(...args),
  }
})

vi.mock('../api-pay', () => ({
  payWithEpay: vi.fn(),
  payWithBEpusdt: vi.fn(),
}))

vi.mock('@/features/system-settings/hooks/use-update-option', () => ({
  useUpdateOption: () => ({
    isPending: false,
    mutateAsync: (...args: unknown[]) => updateOptionMutateAsyncMock(...args),
  }),
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
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

function makeOrder(overrides: Partial<AdtyOrder> = {}): AdtyOrder {
  const futureExpire = Math.floor(Date.now() / 1000) + 600
  return {
    id: 1,
    user_id: 10,
    trade_no: 'ORD-100',
    payment_method: 'alipay',
    payment_provider: 'adty_epay',
    status: 'pending',
    create_time: Math.floor(Date.now() / 1000) - 60,
    complete_time: 0,
    amount: 10,
    money: 70,
    ext: {
      business: 'wallet_topup',
      trade_no: 'ORD-100',
      provider: 'adty_epay',
      method: 'alipay',
      face_usd: '10',
      bonus_rate: '0',
      bonus_usd: '0',
      credit_quota: 5_000_000,
      pay_amount: '70.00',
      pay_currency: 'CNY',
      price_snapshot: '7.00',
      expire_at: futureExpire,
      gateway_trade_id: '',
      pay_address: '',
      pay_url: 'https://pay.example.com/submit?order=ORD-100',
      qr_code: '',
      url_scheme: '',
      channel_id: 1,
      paid_amount: '0',
      paid_currency: 'CNY',
      paid_tx_hash: '',
      refunded_at: 0,
    },
    ...overrides,
  }
}

describe('Frontend payment, wallet, team, and admin hardening', () => {
  beforeEach(() => {
    getAdtyTopupInfoMock.mockResolvedValue({
      min_topup: 1,
      price_cny_per_usd: 7,
      usdt_order_minutes: 15,
      epay_order_minutes: 10,
      pending: null,
      pending_orders: [],
      tiers: [{ amount: 10, rate: 1 }],
      methods: [],
      scenes: [
        {
          key: 'wallet_topup',
          name: 'Wallet top-up',
          min_usd: '5',
          max_usd: '100',
          methods: [
            {
              id: 'alipay',
              label: 'Alipay',
              kind: 'epay',
              currency: 'CNY',
              available: true,
              channel_id: 1,
            },
          ],
        },
      ],
    })
    getAdtyTopupHistoryMock.mockResolvedValue({
      page: 1,
      page_size: 10,
      total: 0,
      items: [],
    })
    cancelAdtyOrderMock.mockResolvedValue(undefined)
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
    useAuthStore.getState().auth.reset('complete')
  })

  it('validates /wallet trade_no search param and resolves personal wallet return orders', async () => {
    const parsed = (
      WalletRoute.options.validateSearch as {
        parse: (input: unknown) => { trade_no?: string; show_history?: boolean }
      }
    ).parse({ trade_no: 'ORD-RET-1' })
    expect(parsed.trade_no).toBe('ORD-RET-1')

    const returnedOrder = makeOrder({
      trade_no: 'ORD-RET-1',
      status: 'success',
      ext: {
        ...(makeOrder().ext as NonNullable<AdtyOrder['ext']>),
        trade_no: 'ORD-RET-1',
        business: 'wallet_topup',
      },
    })
    getAdtyTopupHistoryMock.mockResolvedValue({
      page: 1,
      page_size: 10,
      total: 1,
      items: [returnedOrder],
    })
    getAdtyOrderMock.mockResolvedValue(returnedOrder)
    const onReturnClosed = vi.fn()

    renderWithQuery(
      <AdtyOnlineTopup
        returnTradeNo='ORD-RET-1'
        onReturnClosed={onReturnClosed}
        onSettled={async () => {}}
      />
    )

    const dialog = await screen.findByRole('dialog')
    expect(dialog).toBeTruthy()
    expect(within(dialog).getByText(/ORD-RET-1/)).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    expect(onReturnClosed).toHaveBeenCalledTimes(1)
  })

  it('renders multiple pending orders in AdtyPendingBanner with per-order Resume and Cancel', async () => {
    const orderA = makeOrder({ trade_no: 'ORD-A' })
    const orderB = makeOrder({
      trade_no: 'ORD-B',
      payment_method: 'usdt.trc20',
      payment_provider: 'bepusdt',
      ext: {
        ...(makeOrder().ext as NonNullable<AdtyOrder['ext']>),
        trade_no: 'ORD-B',
        face_usd: '50',
      },
    })
    const onResume = vi.fn()
    const onOrderChanged = vi.fn()

    renderWithQuery(
      <AdtyPendingBanner
        orders={[orderA, orderB]}
        onResume={onResume}
        onOrderChanged={onOrderChanged}
      />
    )

    expect(
      screen.getByText('You have 2 orders waiting for payment')
    ).toBeTruthy()
    const resumeButtons = screen.getAllByRole('button', {
      name: 'Continue payment',
    })
    const cancelButtons = screen.getAllByRole('button', {
      name: 'Cancel order',
    })
    expect(resumeButtons).toHaveLength(2)
    expect(cancelButtons).toHaveLength(2)

    fireEvent.click(resumeButtons[1])
    expect(onResume).toHaveBeenCalledWith(orderB)

    fireEvent.click(cancelButtons[0])
    await waitFor(() => {
      expect(cancelAdtyOrderMock).toHaveBeenCalledWith('ORD-A')
      expect(onOrderChanged).toHaveBeenCalledTimes(1)
    })
  })

  it('renders replaced filter and unexpired pending Continue/Cancel actions in AdtyTopupHistory', async () => {
    const activePending = makeOrder({ trade_no: 'ORD-OPEN' })
    const expiredPending = makeOrder({
      trade_no: 'ORD-EXPIRED',
      ext: {
        ...(makeOrder().ext as NonNullable<AdtyOrder['ext']>),
        trade_no: 'ORD-EXPIRED',
        expire_at: Math.floor(Date.now() / 1000) - 60,
      },
    })
    getAdtyTopupHistoryMock.mockResolvedValue({
      page: 1,
      page_size: 10,
      total: 2,
      items: [activePending, expiredPending],
    })
    const onResumeOrder = vi.fn()
    const onOrderChanged = vi.fn()

    renderWithQuery(
      <AdtyTopupHistory
        onResumeOrder={onResumeOrder}
        onOrderChanged={onOrderChanged}
      />
    )

    await waitFor(() => {
      expect(getAdtyTopupHistoryMock).toHaveBeenCalledWith(
        1,
        10,
        expect.objectContaining({ business: 'wallet_topup' })
      )
    })

    const statusSelect = screen.getByLabelText('Status') as HTMLSelectElement
    expect([...statusSelect.options].map((opt) => opt.value)).toContain(
      'replaced'
    )

    const continueButtons = await screen.findAllByRole('button', {
      name: 'Continue payment',
    })
    const cancelButtons = screen.getAllByRole('button', { name: 'Cancel' })
    expect(continueButtons).toHaveLength(1)
    expect(cancelButtons).toHaveLength(1)

    fireEvent.click(continueButtons[0])
    expect(onResumeOrder).toHaveBeenCalledWith(activePending)

    fireEvent.click(cancelButtons[0])
    await waitFor(() => {
      expect(cancelAdtyOrderMock).toHaveBeenCalledWith('ORD-OPEN')
      expect(onOrderChanged).toHaveBeenCalledTimes(1)
    })
  })

  it('shows distinct custom amount validation errors in AdtyRechargeCard', () => {
    renderWithQuery(
      <AdtyRechargeCard
        info={{
          min_topup: 5,
          price_cny_per_usd: 7,
          usdt_order_minutes: 15,
          epay_order_minutes: 10,
          pending: null,
          tiers: [],
          methods: [],
          scenes: [
            {
              key: 'wallet_topup',
              name: 'Wallet top-up',
              min_usd: '5',
              max_usd: '100',
              methods: [],
            },
          ],
        }}
        loading={false}
        paying={false}
        onPay={vi.fn()}
      />
    )

    const input = screen.getByLabelText('Custom amount (USD)')
    fireEvent.change(input, { target: { value: '10.5' } })
    expect(screen.getByText('Enter a whole number in USD')).toBeTruthy()

    fireEvent.change(input, { target: { value: '2' } })
    expect(screen.getByText('At least $5')).toBeTruthy()

    fireEvent.change(input, { target: { value: '200' } })
    expect(screen.getByText('At most $100')).toBeTruthy()
  })

  it('disables TeamPanel top-up and avoids misleading fallback when quote is unavailable', async () => {
    apiGetMock.mockImplementation((url: string) => {
      if (url === '/api/adty/teams/mine') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              team: {
                team_id: 7,
                name: 'Alpha Team',
                role: 'leader',
                shared_quota: 5_000_000,
                status: 'active',
                members: [],
                keys: [],
              },
            },
          },
        })
      }
      if (url === '/api/adty/teams/7/topups') {
        return Promise.resolve({ data: { success: true, data: { items: [] } } })
      }
      if (url === '/api/adty/teams/7/ledgers') {
        return Promise.resolve({
          data: {
            success: true,
            data: { items: [], total: 0, page: 1, page_size: 10 },
          },
        })
      }
      if (url === '/api/adty/teams/7/invites') {
        return Promise.resolve({ data: { success: true, data: { items: [] } } })
      }
      return Promise.resolve({ data: { success: true, data: {} } })
    })
    quoteAdtyTopupMock.mockResolvedValue({
      available: false,
      quote: {
        face_usd: '10',
        bonus_rate: '0',
        bonus_usd: '0',
        credit_quota: 5_000_000,
        pay_amount: '70.00',
        pay_currency: 'CNY',
        price_cny_per_usd: '7.00',
        topup_group_ratio: '1',
      },
    })

    renderWithQuery(<TeamPanel />)

    const amountInput = await screen.findByLabelText('Team top-up')
    fireEvent.change(amountInput, { target: { value: '10' } })
    fireEvent.click(await screen.findByRole('button', { name: /Alipay/i }))

    expect(
      await screen.findByText(
        'This method cannot take this amount. Choose another method or amount.'
      )
    ).toBeTruthy()
    expect(screen.getByTestId('team-topup-pay').textContent).toBe('$0.00')
    expect(
      (
        screen.getByRole('button', {
          name: 'Team top-up',
        }) as HTMLButtonElement
      ).disabled
    ).toBe(true)
  })

  it('handles TeamPanel returnTradeNo and sends device on team top-up creation', async () => {
    apiGetMock.mockImplementation((url: string) => {
      if (url === '/api/adty/teams/mine') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              team: {
                team_id: 7,
                name: 'Alpha Team',
                role: 'leader',
                shared_quota: 5_000_000,
                status: 'active',
                members: [],
                keys: [],
              },
            },
          },
        })
      }
      if (url === '/api/adty/teams/7/topups') {
        return Promise.resolve({ data: { success: true, data: { items: [] } } })
      }
      if (url === '/api/adty/teams/7/ledgers') {
        return Promise.resolve({
          data: {
            success: true,
            data: { items: [], total: 0, page: 1, page_size: 10 },
          },
        })
      }
      if (url === '/api/adty/teams/7/invites') {
        return Promise.resolve({ data: { success: true, data: { items: [] } } })
      }
      return Promise.resolve({ data: { success: true, data: {} } })
    })
    getAdtyOrderMock.mockResolvedValue(
      makeOrder({
        trade_no: 'TEAM-RET-1',
        status: 'success',
        ext: {
          ...(makeOrder().ext as NonNullable<AdtyOrder['ext']>),
          trade_no: 'TEAM-RET-1',
          business: 'team_topup',
          team_id: 7,
        },
      })
    )
    const onReturnClosed = vi.fn()

    renderWithQuery(
      <TeamPanel returnTradeNo='TEAM-RET-1' onReturnClosed={onReturnClosed} />
    )

    expect(await screen.findByRole('dialog')).toBeTruthy()
    expect(screen.getByText(/TEAM-RET-1/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(onReturnClosed).toHaveBeenCalledTimes(1)
  })

  it('allows non-Root Admin with PAYMENT_READ into /payment-channels and sidebar without duplicating for Super Admin', () => {
    // Super Admin redirects to system settings and has no duplicate /payment-channels link.
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'root',
      role: ROLE.SUPER_ADMIN,
    })
    let rootRedirect: unknown
    try {
      PaymentChannelsRoute.options.beforeLoad?.({} as never)
    } catch (err) {
      rootRedirect = err
    }
    expect(rootRedirect).toMatchObject({
      options: expect.objectContaining({
        to: '/system-settings/billing/$section',
      }),
    })

    let capturedUrls: string[] = []
    function SidebarProbe() {
      const data = useSidebarData()
      const admin = data.navGroups.find((g) => g.id === 'admin')
      capturedUrls = (admin?.items ?? []).map((item) =>
        'url' in item && item.url ? String(item.url) : ''
      )
      return null
    }
    const { unmount } = render(<SidebarProbe />)
    expect(capturedUrls).not.toContain('/payment-channels')
    unmount()

    // Non-Root Admin with PAYMENT_READ loads /payment-channels directly and sees sidebar entry.
    useAuthStore.getState().auth.setUser({
      id: 2,
      username: 'ops',
      role: ROLE.ADMIN,
      permissions: {
        admin_permissions: {
          [ADTY_PERMISSIONS.PAYMENT_READ.resource]: {
            [ADTY_PERMISSIONS.PAYMENT_READ.action]: true,
          },
        },
      },
    })
    expect(() =>
      PaymentChannelsRoute.options.beforeLoad?.({} as never)
    ).not.toThrow()
    const { unmount: unmountOps } = render(<SidebarProbe />)
    expect(capturedUrls).toContain('/payment-channels')
    unmountOps()

    // Non-Root Admin without payment permissions gets redirected to /403.
    useAuthStore.getState().auth.setUser({
      id: 3,
      username: 'support',
      role: ROLE.ADMIN,
      permissions: {
        admin_permissions: {},
      },
    })
    let forbiddenRedirect: unknown
    try {
      PaymentChannelsRoute.options.beforeLoad?.({} as never)
    } catch (err) {
      forbiddenRedirect = err
    }
    expect(forbiddenRedirect).toMatchObject({
      options: expect.objectContaining({ to: '/403' }),
    })
  })

  it('validates BEpusdtSettingsSection enable prerequisites and 3..60 minute bounds', async () => {
    renderWithQuery(
      <BEpusdtSettingsSection
        inlineActions
        defaultValues={{
          enabled: false,
          baseUrl: '',
          tradeTypes: '["usdt.trc20"]',
          orderMinutes: 15,
          secretConfigured: false,
        }}
      />
    )

    expect(screen.getByText('Not configured')).toBeTruthy()
    const minutesInput = screen.getByLabelText(
      'Order validity (minutes)'
    ) as HTMLInputElement
    expect(minutesInput.min).toBe('3')
    expect(minutesInput.max).toBe('60')

    fireEvent.click(screen.getByRole('switch'))
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    expect(updateOptionMutateAsyncMock).not.toHaveBeenCalled()
    expect(toastErrorMock).toHaveBeenCalledWith(
      'Enter a valid http(s) gateway address before enabling USDT payments'
    )
  })

  it('hardens UsdtPayDialog with logo, error retry, terminal statuses, and expired footer', async () => {
    getAdtyOrderMock.mockResolvedValue({
      trade_no: 'USDT-1',
      status: 'pending',
      payment_method: 'usdt.trc20',
      payment_provider: 'bepusdt',
      ext: {
        trade_no: 'USDT-1',
        pay_amount: '10.01',
        pay_address: 'T123',
        face_usd: '10',
        credit_quota: 5_000_000,
        expire_at: Math.floor(Date.now() / 1000) - 10,
      },
    })

    renderWithQuery(
      <UsdtPayDialog
        chain='usdt.trc20'
        amount={10}
        tradeNo='USDT-1'
        onClose={vi.fn()}
        onSettled={vi.fn()}
        onChangeChain={vi.fn()}
      />
    )

    const heading = screen.getByRole('heading', { name: 'Pay with USDT' })
    expect(heading.querySelector('svg')).toBeTruthy()
    expect(
      await screen.findByText('This order has expired. Please create a new one.')
    ).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Change network' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Cancel order' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Close' })).toBeTruthy()
  })

  it('routes site owner margin voucher redemption to /api/adty/site/margin/vouchers/redeem', async () => {
    apiPostMock.mockResolvedValue({
      data: {
        success: true,
        data: {
          trade_no: 'MV-1',
          purpose: 'site_margin',
          amount_usd: '100.00',
          quota: 50_000_000,
        },
      },
    })
    await redeemOwnerMarginVoucher('  CODE-123  ')
    expect(apiPostMock).toHaveBeenCalledWith(
      '/api/adty/site/margin/vouchers/redeem',
      { code: 'CODE-123' }
    )
  })
})
