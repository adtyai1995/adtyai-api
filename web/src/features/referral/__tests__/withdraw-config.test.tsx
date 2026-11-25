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
import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  canonicalWithdrawMethods,
  canonicalWithdrawNetworks,
} from '@/features/admin-referral/lib/settings'
import { WithdrawalPanel } from '@/features/admin-referral/components/withdrawal-panel'
import { payoutText } from '@/features/admin-referral/lib/payout'
import { SiteInviteWithdrawPanel } from '@/features/wallet/adty/site-invite-withdraw-panel'
import { api } from '@/lib/api'

import { WithdrawDrawer } from '../components/withdraw-drawer'
import { formatUsdtAmount, usdtNetAmount } from '../lib/address'
import {
  clearSiteInviteCookie,
  ensureDeviceCookie,
  readSiteInviteCookie,
  storeSiteInviteCookie,
} from '../lib/click'
import {
  availableUsdtNetworks,
  availableWithdrawalTargets,
  withdrawTargetAvailable,
} from '../lib/labels'
import type { ReferralProfile } from '../types'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

vi.mock('@/hooks/use-sidebar-badges', () => ({
  invalidateSidebarBadges: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/components/dialog', () => ({
  Dialog: (props: {
    open?: boolean
    title?: ReactNode
    description?: ReactNode
    children?: ReactNode
    footer?: ReactNode
  }) =>
    props.open ? (
      <div role='dialog'>
        <h2>{props.title}</h2>
        {props.description && <p>{props.description}</p>}
        {props.children}
        {props.footer}
      </div>
    ) : null,
}))

vi.mock('@/components/ui/sheet', () => ({
  Sheet: (props: { open?: boolean; children?: ReactNode }) =>
    props.open ? <div>{props.children}</div> : null,
  SheetContent: (props: { children?: ReactNode }) => <div>{props.children}</div>,
  SheetHeader: (props: { children?: ReactNode }) => <div>{props.children}</div>,
  SheetTitle: (props: { children?: ReactNode }) => <h2>{props.children}</h2>,
  SheetDescription: (props: { children?: ReactNode }) => <p>{props.children}</p>,
}))

vi.mock('@/components/confirm-dialog', () => ({
  ConfirmDialog: (props: {
    open?: boolean
    title?: ReactNode
    desc?: ReactNode
    handleConfirm?: () => void
  }) =>
    props.open ? (
      <div role='dialog'>
        <h3>{props.title}</h3>
        <div>{props.desc}</div>
        <button type='button' onClick={props.handleConfirm}>
          Confirm
        </button>
      </div>
    ) : null,
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    i18n: { language: 'en', resolvedLanguage: 'en' },
    t: (key: string, params?: Record<string, string | number>) => {
      if (!params) return key
      return Object.entries(params).reduce(
        (acc, [k, v]) => acc.replaceAll(`{{${k}}}`, String(v)),
        key
      )
    },
  }),
}))

function makeProfile(overrides: Partial<ReferralProfile> = {}): ReferralProfile {
  return {
    affiliate: {
      id: 1,
      user_id: 10,
      invite_code: 'ABC12345',
      status: 'approved',
      rate_override: null,
      acquisition_enabled: true,
      settlement_enabled: true,
      withdrawal_enabled: true,
      risk_reason: '',
      risk_note: '',
      site_owner_since: 0,
      created_at: 1700000000,
    },
    account: {
      pending_amount: 0,
      available_amount: 100,
      frozen_amount: 0,
      withdrawn_amount: 0,
    },
    rate: '10',
    freeze_days: 7,
    min_withdraw_usd: 10,
    usdt_withdraw_fee: 1,
    withdraw_methods: ['usdt', 'alipay'],
    withdraw_usdt_networks: ['TRC20', 'POLYGON'],
    cancel_window_minutes: 30,
    cny_rate: '7.2',
    cny_rate_updated_at: 1700000000,
    enabled: true,
    site_owner: false,
    funnel: { clicks: 0, signups: 0, paying: 0, earned: '0.00' },
    ...overrides,
  }
}

function createClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

describe('referral withdraw config helpers (R-36 #1)', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('normalizes canonical methods and networks and rejects wechat', () => {
    expect(canonicalWithdrawMethods(['alipay', 'USDT', 'wechat'])).toEqual([
      'usdt',
      'alipay',
    ])
    expect(canonicalWithdrawNetworks(['XLAYER', 'trc20', 'ERC20'])).toEqual([
      'TRC20',
      'XLAYER',
    ])
    expect(availableUsdtNetworks(['POLYGON', 'TRC20'])).toEqual([
      'TRC20',
      'POLYGON',
    ])
  })

  it('computes dynamic main-site targets based on enabled methods and chains', () => {
    const both = makeProfile()
    expect(withdrawTargetAvailable(both, 'wechat')).toBe(false)
    expect(availableWithdrawalTargets(both)).toEqual([
      'balance',
      'usdt',
      'alipay',
    ])

    const usdtOnly = makeProfile({
      withdraw_methods: ['usdt'],
      cny_rate: '',
      withdraw_usdt_networks: ['TRC20'],
    })
    expect(availableWithdrawalTargets(usdtOnly)).toEqual(['balance', 'usdt'])

    const alipayOnly = makeProfile({
      withdraw_methods: ['alipay'],
      usdt_withdraw_fee: null,
      withdraw_usdt_networks: [],
    })
    expect(availableWithdrawalTargets(alipayOnly)).toEqual([
      'balance',
      'alipay',
    ])

    const noneEnabled = makeProfile({
      withdraw_methods: [],
      withdraw_usdt_networks: [],
    })
    expect(availableWithdrawalTargets(noneEnabled)).toEqual(['balance'])

    const usdtNoChains = makeProfile({
      withdraw_methods: ['usdt'],
      withdraw_usdt_networks: [],
    })
    expect(availableWithdrawalTargets(usdtNoChains)).toEqual(['balance'])
  })

  it('WithdrawDrawer renders only enabled methods, auto-selects single USDT chain, and supports optional Alipay QR', async () => {
    const postSpy = vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: true, data: { id: 42 } },
    })

    const profile = makeProfile({
      withdraw_methods: ['usdt', 'alipay'],
      withdraw_usdt_networks: ['TRC20'],
    })

    render(
      <QueryClientProvider client={createClient()}>
        <WithdrawDrawer
          open
          onOpenChange={vi.fn()}
          profile={profile}
          onDone={vi.fn()}
        />
      </QueryClientProvider>
    )

    expect(screen.queryByText('WeChat Pay')).toBeNull()
    expect(screen.getByRole('button', { name: 'Account balance' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'USDT' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Alipay' })).toBeTruthy()

    // Single USDT chain auto-selects TRC20.
    fireEvent.click(screen.getByRole('button', { name: 'USDT' }))
    const netSelect = screen.getByLabelText('Network') as HTMLSelectElement
    expect(netSelect.value).toBe('TRC20')
    expect([...netSelect.options].map((o) => o.value)).toEqual(['TRC20'])

    // Alipay requires real name + account and submits without QR code.
    fireEvent.click(screen.getByRole('button', { name: 'Alipay' }))
    fireEvent.change(screen.getByLabelText('Amount (USD)'), {
      target: { value: '20' },
    })
    fireEvent.change(screen.getByLabelText('Payee real name'), {
      target: { value: '张三' },
    })
    fireEvent.change(screen.getByLabelText('Alipay account'), {
      target: { value: '13800138000' },
    })

    const submitBtn = screen.getByRole('button', {
      name: 'Submit',
    }) as HTMLButtonElement
    expect(submitBtn.disabled).toBe(false)
    fireEvent.click(submitBtn)
    fireEvent.click(await screen.findByRole('button', { name: 'Confirm' }))

    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(1))
    expect(postSpy.mock.calls[0][0]).toBe('/api/user/referral/withdrawals')
    expect(postSpy.mock.calls[0][1]).toMatchObject({
      amount: '20.00',
      account_type: 'alipay',
      account_name: '张三',
      account_no: '13800138000',
      qr_image_url: undefined,
    })
  })

  it('SiteInviteWithdrawPanel hides disabled methods, auto-selects single chain, and formats rules conditionally', async () => {
    vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
      if (url === '/api/adty/site-commissions/withdrawals/rules') {
        return {
          data: {
            success: true,
            data: {
              configured: true,
              min_usd: '10',
              fee_usd: '1',
              methods: ['balance', 'usdt'],
              usdt_networks: ['POLYGON'],
            },
          },
        }
      }
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                id: 9,
                site_id: 3,
                amount_usd: '25',
                fee_usd: '0',
                net_usd: '25',
                method: 'alipay',
                network: '',
                account_no: 'alice@example.com',
                account_name: 'Alice',
                status: 'paid',
                can_cancel: false,
                cancel_deadline: 0,
                cancel_reason: '',
                payout_amount: '180.00',
                payout_currency: 'CNY',
                reject_reason: '',
              },
            ],
          },
        },
      }
    })

    render(
      <QueryClientProvider client={createClient()}>
        <SiteInviteWithdrawPanel
          rows={[
            {
              site_id: 3,
              id: 1,
              trade_no: 'T-1',
              status: 'available',
              amount_usd: '50',
              rate: '0.1',
              clawed_usd: '0',
              converted_usd: '0',
              withdrawn_usd: '0',
              frozen_usd: '0',
              earnings_hold_usd: '50',
              margin_hold_usd: '0',
              available_at: 1000,
            },
          ]}
          onChanged={vi.fn()}
        />
      </QueryClientProvider>
    )

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'USDT' })).toBeTruthy()
    )
    // Alipay is disabled -> button must not be rendered at all.
    expect(screen.queryByRole('button', { name: 'Alipay' })).toBeNull()
    // Rules summary shows min & USDT fee, NOT CNY rate.
    expect(
      screen.getByText('Minimum 10 USD · USDT fee 1 USD')
    ).toBeTruthy()
    // History shows Alipay account_name + account_no cleanly.
    expect(screen.getByText(/Alice · alice@example\.com/)).toBeTruthy()

    // Single USDT chain auto-selects POLYGON.
    fireEvent.click(screen.getByRole('button', { name: 'USDT' }))
    const netSelect = screen.getByLabelText('Network') as HTMLSelectElement
    expect(netSelect.value).toBe('POLYGON')
    expect([...netSelect.options].map((o) => o.value)).toEqual(['POLYGON'])
  })

  it('Admin WithdrawalPanel displays Alipay name, account, QR lightbox, and Mark-as-Paid payee summary', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          page: 1,
          page_size: 20,
          total: 1,
          items: [
            {
              id: 77,
              affiliate_id: 2,
              user_id: 11,
              username: 'alice',
              amount: 20,
              fee_amount: 0,
              net_amount: 20,
              account_type: 'alipay',
              account_name: '张三',
              account_no: '13800138000',
              account_network: '',
              qr_image_url: '/api/referral-assets/qr-77.png?expires=9&sig=x',
              cny_rate: '7.2',
              cny_amount: '144.00',
              admin_note: '',
              payment_txn_no: '',
              payment_proof_url: '',
              status: 'approved',
              reject_reason: '',
              submitted_at: 1700000000,
              paid_at: 0,
            },
          ],
        },
      },
    })

    render(
      <QueryClientProvider client={createClient()}>
        <WithdrawalPanel canReview canPayout status='approved' />
      </QueryClientProvider>
    )

    await waitFor(() => expect(screen.getByText('张三')).toBeTruthy())
    expect(screen.getByText('13800138000')).toBeTruthy()

    // Clicking QR thumbnail opens lightbox dialog with "Open in new tab" link.
    fireEvent.click(screen.getByLabelText('Preview payment QR code'))
    expect(screen.getByRole('dialog')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Open in new tab' })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))

    // Opening Mark as paid shows Alipay payee summary inside the dialog.
    fireEvent.click(screen.getByRole('button', { name: 'Mark as paid' }))
    expect(screen.getByText('Alipay payee details')).toBeTruthy()
  })

  it('resets Mark-as-Paid and Reject modal fields across different rows (R1-49)', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          page: 1,
          page_size: 20,
          total: 2,
          items: [
            {
              id: 81,
              affiliate_id: 2,
              user_id: 11,
              username: 'alice',
              amount: 20.123456,
              fee_amount: 1,
              net_amount: 19.123456,
              account_type: 'usdt',
              account_name: '',
              account_no: 'T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb',
              account_network: 'TRC20',
              qr_image_url: '',
              cny_rate: '',
              cny_amount: '',
              admin_note: '',
              payment_txn_no: '',
              payment_proof_url: '',
              status: 'approved',
              reject_reason: '',
              submitted_at: 1700000000,
              paid_at: 0,
            },
            {
              id: 82,
              affiliate_id: 3,
              user_id: 12,
              username: 'bob',
              amount: 30,
              fee_amount: 1,
              net_amount: 29,
              account_type: 'usdt',
              account_name: '',
              account_no: 'T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb',
              account_network: 'TRC20',
              qr_image_url: '',
              cny_rate: '',
              cny_amount: '',
              admin_note: '',
              payment_txn_no: '',
              payment_proof_url: '',
              status: 'approved',
              reject_reason: '',
              submitted_at: 1700000100,
              paid_at: 0,
            },
          ],
        },
      },
    })

    render(
      <QueryClientProvider client={createClient()}>
        <WithdrawalPanel canReview canPayout status='approved' />
      </QueryClientProvider>
    )

    await waitFor(() => expect(screen.getByText('19.123456 USDT')).toBeTruthy())

    const payButtons = screen.getAllByRole('button', { name: 'Mark as paid' })
    fireEvent.click(payButtons[0])
    const txnInput = screen.getByLabelText('Transaction hash') as HTMLInputElement
    fireEvent.change(txnInput, { target: { value: '0xstale-hash' } })
    expect(txnInput.value).toBe('0xstale-hash')

    // Opening row #2 must clear the stale transaction hash from row #1.
    fireEvent.click(payButtons[1])
    expect(
      (screen.getByLabelText('Transaction hash') as HTMLInputElement).value
    ).toBe('')
  })

  it('preserves 6-decimal USDT precision in helpers and payoutText (R1-50)', () => {
    expect(usdtNetAmount(20.123456, 1)).toBe(19.123456)
    expect(formatUsdtAmount(19.123456)).toBe('19.123456')
    expect(formatUsdtAmount(19.5)).toBe('19.50')
    expect(formatUsdtAmount(19)).toBe('19.00')
    expect(
      payoutText(
        {
          id: 1,
          affiliate_id: 1,
          user_id: 1,
          amount: 20.123456,
          fee_amount: 1,
          net_amount: 19.123456,
          account_type: 'usdt',
          account_name: '',
          account_no: 'T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb',
          account_network: 'TRC20',
          qr_image_url: '',
          cny_rate: '',
          cny_amount: '',
          admin_note: '',
          payment_txn_no: '',
          payment_proof_url: '',
          status: 'approved',
          reject_reason: '',
          submitted_at: 1700000000,
          paid_at: 0,
        },
        '$20.123456'
      )
    ).toBe('19.123456 USDT')
  })

  it('writes adty_did cookie even on subsite documents and round-trips adty_site_invite cookie (R1-13, R1-46)', () => {
    document.documentElement.dataset.adtySite = '1'
    document.cookie = 'adty_did=; Max-Age=0; Path=/'
    clearSiteInviteCookie(document)

    ensureDeviceCookie(document)
    expect(document.cookie).toContain('adty_did=')

    storeSiteInviteCookie('SITE-CODE-99', document, 1700000123)
    expect(readSiteInviteCookie(document)).toEqual({
      code: 'SITE-CODE-99',
      capturedAt: 1700000123,
    })

    clearSiteInviteCookie(document)
    expect(readSiteInviteCookie(document)).toBeNull()
    delete document.documentElement.dataset.adtySite
  })
})
