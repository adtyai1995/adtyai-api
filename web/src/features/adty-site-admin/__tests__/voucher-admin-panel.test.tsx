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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  cancelAdminVoucher,
  issueAdminMarginVoucher,
  issueMainUserVoucher,
  resetVoucherIssuanceIntent,
} from '../api'
import { SiteFundPanel } from '../components/site-fund-panel'
import { VoucherAdminPanel } from '../components/voucher-admin-panel'

function wrapper(props: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

beforeEach(() => {
  resetVoucherIssuanceIntent()
  useAuthStore.setState((state) => ({
    ...state,
    auth: {
      ...state.auth,
      user: {
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
        aff_code: '',
        inviter_id: 0,
      },
    },
  }))
})

afterEach(() => {
  resetVoucherIssuanceIntent()
  vi.restoreAllMocks()
})

describe('voucher issuance intent API helpers', () => {
  it('reuses the same intent_key on retry after a lost response and rotates after cancel', async () => {
    const postBodies: Array<Record<string, unknown>> = []
    let callCount = 0
    vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
      const path = String(url)
      if (path === '/api/adty/admin/vouchers') {
        callCount += 1
        const payload = (body ?? {}) as Record<string, unknown>
        postBodies.push(payload)
        if (callCount === 1) {
          throw new Error('network timeout after server commit')
        }
        if (callCount === 2) {
          return {
            data: {
              success: true,
              data: {
                id: 42,
                code: '',
                code_tail: 'WXYZ',
                purpose: 'main_user',
                status: 'pending',
                amount_usd: '15',
                quota: 7500000,
                intent_key: String(payload.intent_key),
                replayed: true,
              },
            },
          }
        }
        return {
          data: {
            success: true,
            data: {
              id: 43,
              code: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ',
              code_tail: 'WXYZ',
              purpose: 'main_user',
              status: 'pending',
              amount_usd: '15',
              quota: 7500000,
              intent_key: String(payload.intent_key),
              replayed: false,
            },
          },
        }
      }
      if (path === '/api/adty/admin/vouchers/42/cancel') {
        return { data: { success: true, data: { id: 42, status: 'cancelled' } } }
      }
      return { data: { success: true, data: {} } }
    })

    await expect(
      issueMainUserVoucher({ amount_usd: '15', reason: 'retry-test' })
    ).rejects.toThrow('network timeout')

    const reconciled = await issueMainUserVoucher({
      amount_usd: '15',
      reason: 'retry-test',
    })
    expect(reconciled.replayed).toBe(true)
    expect(reconciled.code).toBe('')
    expect(postBodies).toHaveLength(2)
    expect(postBodies[0].intent_key).toBeTruthy()
    expect(postBodies[1].intent_key).toBe(postBodies[0].intent_key)

    await cancelAdminVoucher(42)

    const replacement = await issueMainUserVoucher({
      amount_usd: '15',
      reason: 'retry-test',
    })
    expect(replacement.replayed).toBe(false)
    expect(replacement.code).toBe('ABCDEFGHIJKLMNOPQRSTUVWXYZ')
    expect(postBodies[2].intent_key).not.toBe(postBodies[0].intent_key)
  })

  it('forwards designated_owner_id and preserves intent_key for site margin vouchers', async () => {
    const postBodies: Array<Record<string, unknown>> = []
    let first = true
    vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
      const payload = (body ?? {}) as Record<string, unknown>
      postBodies.push(payload)
      if (first) {
        first = false
        throw new Error('connection reset')
      }
      return {
        data: {
          success: true,
          data: {
            id: 77,
            code: '',
            code_tail: 'M999',
            purpose: 'site_margin',
            status: 'pending',
            target_site_id: 9,
            designated_owner_id: 18,
            amount_usd: '25',
            quota: 12500000,
            intent_key: String(payload.intent_key),
            replayed: true,
          },
        },
      }
    })

    await expect(
      issueAdminMarginVoucher({
        site_id: 9,
        designated_owner_id: 18,
        amount_usd: '25',
        reason: 'margin-topup',
      })
    ).rejects.toThrow('connection reset')

    const replay = await issueAdminMarginVoucher({
      site_id: 9,
      designated_owner_id: 18,
      amount_usd: '25',
      reason: 'margin-topup',
    })
    expect(replay.replayed).toBe(true)
    expect(replay.designated_owner_id).toBe(18)
    expect(postBodies[0].designated_owner_id).toBe(18)
    expect(postBodies[0].intent_key).toBe(postBodies[1].intent_key)
  })
})

describe('VoucherAdminPanel and SiteFundPanel reconciliation', () => {
  it('reconciles a lost issuance response in VoucherAdminPanel without minting a duplicate until cancelled', async () => {
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      const path = String(url)
      if (path === '/api/adty/admin/vouchers/limit') {
        return {
          data: {
            success: true,
            data: { limit_usd: '100', frozen_usd: '20', spent_usd: '0' },
          },
        }
      }
      return {
        data: {
          success: true,
          data: { total: 0, items: [], page: 1, page_size: 20 },
        },
      }
    })

    const postBodies: Array<Record<string, unknown>> = []
    let issueAttempt = 0
    const postSpy = vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
      const path = String(url)
      if (path === '/api/adty/admin/vouchers') {
        issueAttempt += 1
        const payload = (body ?? {}) as Record<string, unknown>
        postBodies.push(payload)
        if (issueAttempt === 1) {
          throw new Error('lost response')
        }
        if (issueAttempt === 2) {
          return {
            data: {
              success: true,
              data: {
                id: 88,
                code: '',
                code_tail: 'TAIL',
                purpose: 'main_user',
                status: 'pending',
                amount_usd: '20',
                quota: 10000000,
                intent_key: String(payload.intent_key),
                replayed: true,
              },
            },
          }
        }
        return {
          data: {
            success: true,
            data: {
              id: 89,
              code: 'NEWCODE23456789ABCDEFGHJKLM',
              code_tail: 'JKLM',
              purpose: 'main_user',
              status: 'pending',
              amount_usd: '20',
              quota: 10000000,
              intent_key: String(payload.intent_key),
              replayed: false,
            },
          },
        }
      }
      if (path === '/api/adty/admin/vouchers/88/cancel') {
        return { data: { success: true, data: { id: 88, status: 'cancelled' } } }
      }
      return { data: { success: true, data: {} } }
    })

    const user = userEvent.setup()
    render(<VoucherAdminPanel />, { wrapper })

    await user.type(await screen.findByLabelText('Amount (USD)'), '20')
    await user.type(screen.getByLabelText('Reason'), 'ambiguous-retry')

    const issueBtn = screen.getByRole('button', { name: 'Issue code' })
    await user.click(issueBtn)
    await waitFor(() => expect(postBodies).toHaveLength(1))

    // Retry after lost response sends the exact same intent_key
    await user.click(issueBtn)
    await waitFor(() => expect(postBodies).toHaveLength(2))
    expect(postBodies[1].intent_key).toBe(postBodies[0].intent_key)

    // Reconciled banner is shown; full code is NOT shown; Issue button is disabled until reconciled
    const banner = await screen.findByTestId('voucher-reconcile-banner')
    expect(banner).toHaveTextContent('#88')
    expect(banner).toHaveTextContent('TAIL')
    expect(screen.queryByText(/Voucher code:/)).not.toBeInTheDocument()
    expect(issueBtn).toBeDisabled()

    // Cancel existing voucher and replace with a new code
    await user.click(
      screen.getByRole('button', { name: 'Cancel code and replace' })
    )
    await waitFor(() =>
      expect(postSpy).toHaveBeenCalledWith('/api/adty/admin/vouchers/88/cancel')
    )
    await waitFor(() => expect(issueBtn).toBeEnabled())

    await user.click(issueBtn)
    await waitFor(() => expect(postBodies).toHaveLength(3))
    expect(postBodies[2].intent_key).not.toBe(postBodies[0].intent_key)
    expect(
      await screen.findByText('NEWCODE23456789ABCDEFGHJKLM')
    ).toBeInTheDocument()
  })

  it('reconciles a lost margin code response in SiteFundPanel and rotates intent_key after cancel', async () => {
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      const path = String(url)
      if (path.endsWith('/wallets')) {
        return { data: { success: true, data: [] } }
      }
      if (path.endsWith('/wallet-ledgers')) {
        return {
          data: {
            success: true,
            data: { total: 0, items: [], page: 1, page_size: 20 },
          },
        }
      }
      if (path.endsWith('/commission-margin') || path.endsWith('/margin')) {
        return { data: { success: true, data: { site_id: 5, allowed: true } } }
      }
      return { data: { success: true, data: { items: [], kinds: [] } } }
    })

    const postBodies: Array<Record<string, unknown>> = []
    let attempt = 0
    vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
      const path = String(url)
      if (path === '/api/adty/admin/vouchers') {
        attempt += 1
        const payload = (body ?? {}) as Record<string, unknown>
        postBodies.push(payload)
        if (attempt === 1) {
          throw new Error('gateway timeout')
        }
        if (attempt === 2) {
          return {
            data: {
              success: true,
              data: {
                id: 91,
                code: '',
                code_tail: 'MG91',
                purpose: 'site_margin',
                status: 'pending',
                target_site_id: 5,
                designated_owner_id: 12,
                amount_usd: '30',
                quota: 15000000,
                intent_key: String(payload.intent_key),
                replayed: true,
              },
            },
          }
        }
        return {
          data: {
            success: true,
            data: {
              id: 92,
              code: 'MARGINREPLACEMENTCODE234567',
              code_tail: '4567',
              purpose: 'site_margin',
              status: 'pending',
              target_site_id: 5,
              designated_owner_id: 12,
              amount_usd: '30',
              quota: 15000000,
              intent_key: String(payload.intent_key),
              replayed: false,
            },
          },
        }
      }
      if (path === '/api/adty/admin/vouchers/91/cancel') {
        return { data: { success: true, data: { id: 91, status: 'cancelled' } } }
      }
      return { data: { success: true, data: {} } }
    })

    const user = userEvent.setup()
    render(<SiteFundPanel siteId={5} />, { wrapper })

    const amountField = await waitFor(() => {
      const element = document.querySelector('#margin-code-amount')
      if (!element) throw new Error('margin-code-amount not mounted')
      return element
    })
    const reasonField = document.querySelector('#margin-code-reason')
    if (!reasonField) throw new Error('margin-code-reason not mounted')
    await user.type(amountField, '30')
    await user.type(reasonField, 'margin-hold')

    const issueBtn = screen.getByRole('button', { name: 'Issue margin code' })
    await user.click(issueBtn)
    await waitFor(() => expect(postBodies).toHaveLength(1))

    await user.click(issueBtn)
    await waitFor(() => expect(postBodies).toHaveLength(2))
    expect(postBodies[1].intent_key).toBe(postBodies[0].intent_key)

    const banner = await screen.findByTestId('margin-voucher-reconcile-banner')
    expect(banner).toHaveTextContent('#91')
    expect(banner).toHaveTextContent('MG91')
    expect(issueBtn).toBeDisabled()

    await user.click(
      screen.getByRole('button', { name: 'Cancel code and replace' })
    )
    await waitFor(() => expect(issueBtn).toBeEnabled())

    await user.click(issueBtn)
    await waitFor(() => expect(postBodies).toHaveLength(3))
    expect(postBodies[2].intent_key).not.toBe(postBodies[0].intent_key)
    expect(
      await screen.findByText('MARGINREPLACEMENTCODE234567')
    ).toBeInTheDocument()
  })
})
