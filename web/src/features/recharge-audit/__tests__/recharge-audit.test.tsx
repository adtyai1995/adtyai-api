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

import { useAuthStore } from '@/stores/auth-store'
import { api as httpApi } from '@/lib/api'

import type { RechargeAuditOrder } from '../api'
import { RechargeAudit } from '../index'

const api = vi.hoisted(() => ({
  getRechargeAudit: vi.fn(),
  getRechargeAuditSummary: vi.fn(),
  getOrderTrace: vi.fn(),
  getPaymentOrphans: vi.fn(),
  getSiteEarningReviews: vi.fn().mockResolvedValue({ total: 0, items: [] }),
  previewTopupRefund: vi.fn(),
  refundTopup: vi.fn(),
  listTopupManualRefunds: vi.fn(),
  runTopupManualRefundStep: vi.fn(),
  completeTopupManually: vi.fn(),
  creditPaymentOrphan: vi.fn(),
  resolvePaymentOrphan: vi.fn(),
}))
vi.mock('../api', () => api)
const verification = vi.hoisted(() => ({ requestVerification: vi.fn() }))
vi.mock('@/features/auth/secure-verification/hooks/use-secure-verification', () => ({
  useSecureVerification: () => ({
    requestVerification: verification.requestVerification,
    dialogProps: { state: { phase: 'idle' } },
    isActive: false,
    cancel: vi.fn(),
  }),
}))
vi.mock(
  '@/features/auth/secure-verification/components/secure-verification-dialog',
  () => ({
    SecureVerificationDialog: () => null,
  })
)
vi.mock('@/components/layout', () => {
  const Layout = (props: { children: ReactNode }) => <div>{props.children}</div>
  Layout.Title = (props: { children: ReactNode }) => <h1>{props.children}</h1>
  Layout.Content = (props: { children: ReactNode }) => (
    <div>{props.children}</div>
  )
  return { SectionPageLayout: Layout }
})

const order: RechargeAuditOrder = {
  id: 1,
  user_id: 7,
  username: 'buyer',
  site_id: 7,
  site_name: 'North Studio',
  order_type: 'topup',
  trade_no: 'EPY7001',
  payment_method: 'alipay',
  payment_provider: 'adty_epay',
  status: 'success',
  create_time: 1,
  complete_time: 2,
  amount: 100,
  money: 730,
  has_ext: true,
  face_usd: '100',
  bonus_usd: '2.5',
  credit_quota: 51250000,
  pay_amount: '730',
  pay_currency: 'CNY',
  paid_amount: '730',
  paid_currency: 'CNY',
  paid_tx_hash: '2026103100001',
  paid_source: 'callback',
  channel_id: 3,
  channel_name: 'ZPay A',
  anomaly: '',
  actions: { complete: false, refund: true, query: false },
}

function renderPage(permissions: Record<string, boolean>, ownerMode?: boolean) {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'finance',
    role: 10,
    permissions: { admin_permissions: { adty_finance: permissions } },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RechargeAudit ownerMode={ownerMode} ownerSiteId={ownerMode ? 7 : undefined} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  verification.requestVerification.mockResolvedValue({ proof_token: 'proof' })
  api.previewTopupRefund.mockResolvedValue({
    deducted_quota: 50000000,
    bonus_quota: 1250000,
    refund_quota: 50000000,
    refund_amount: '100',
    currency: 'USDT',
    facts: {
      trade_no: 'EPY7001',
      site_id: 7,
      user_id: 7,
      face_usd: '100',
      pay_currency: 'CNY',
      paid_amount: '730',
      paid_currency: 'CNY',
      order_rate: '7.3',
      pending_quota: 0,
      refund_occupied_quota: 0,
      refund_completed_usd: '0',
      refund_in_progress_usd: '0',
      refund_unknown_usd: '0',
      refund_remaining_usd: '100',
      bonus_usd: '2.5',
      bonus_used_quota: 0,
      bonus_remaining_quota: 1250000,
      cumulative_refund_usd: '0',
    },
  })
  api.listTopupManualRefunds.mockResolvedValue({
    occupancy: {
      face_quota: 50000000,
      completed_quota: 0,
      in_progress_quota: 0,
      unknown_quota: 0,
      occupied_quota: 0,
      remaining_quota: 50000000,
      face_usd: '100',
      completed_usd: '0',
      in_progress_usd: '0',
      unknown_usd: '0',
      occupied_usd: '0',
      remaining_usd: '100',
    },
    facts: {
      trade_no: 'EPY7001',
      site_id: 7,
      user_id: 7,
      face_usd: '100',
      pay_currency: 'CNY',
      paid_amount: '730',
      paid_currency: 'CNY',
      order_rate: '7.3',
      pending_quota: 0,
      refund_occupied_quota: 0,
      refund_completed_usd: '0',
      refund_in_progress_usd: '0',
      refund_unknown_usd: '0',
      refund_remaining_usd: '100',
      bonus_usd: '2.5',
      bonus_used_quota: 0,
      bonus_remaining_quota: 1250000,
      cumulative_refund_usd: '0',
      manual_refunds: [],
    },
  })
  api.getRechargeAudit.mockResolvedValue({
    page: 1,
    page_size: 20,
    total: 1,
    items: [order],
  })
  api.getRechargeAuditSummary.mockResolvedValue({
    orders: 1,
    credited_usd: '100',
    bonus_usd: '2.5',
    currency: 'USD',
    status_options: [],
    provider_option: [],
  })
  api.getPaymentOrphans.mockResolvedValue({
    page: 1,
    page_size: 20,
    total: 0,
    items: [],
  })
})

afterEach(() => {
  useAuthStore.getState().auth.reset()
  vi.clearAllMocks()
})

describe('RechargeAudit', () => {
  it('filters consumption orders and opens their own business trace', async () => {
    const consumption = {
      ...order,
      order_type: 'consumption',
      source_type: 'billing',
      reference: 'request:visible',
      trade_no: 'request:visible',
      status: 'settled',
      has_ext: false,
      face_usd: '1',
      amount: 500000,
      actions: { complete: true, refund: true, query: true },
    }
    api.getRechargeAudit.mockResolvedValue({ total: 1, items: [consumption] })
    api.getOrderTrace.mockResolvedValue({
      order: consumption,
      refund: { refunded_at: 0 },
      section: 'business',
      sections: ['business', 'wallet', 'funds'],
      items: [],
      total: 0,
    })
    renderPage({ read: true, refund: true, manual_credit: true })
    const user = userEvent.setup()
    const type = await screen.findByRole('combobox', { name: 'Order type' })
    await user.selectOptions(type, 'consumption')
    await waitFor(() =>
      expect(api.getRechargeAudit).toHaveBeenLastCalledWith(
        expect.objectContaining({ order_type: 'consumption', page: 1 }),
        undefined
      )
    )
    const table = screen.getByRole('table', { name: 'Order management' })
    expect(within(table).getByText('API consumption')).toBeVisible()
    expect(within(table).getByText('$1.00')).toBeVisible()
    expect(within(table).queryByRole('button', { name: 'Refund' })).toBeNull()
    await user.click(within(table).getByRole('button', { name: 'request:visible' }))
    const dialog = await screen.findByRole('dialog', { name: 'Order details' })
    await within(dialog).findByText('API consumption')
    expect(api.getOrderTrace).toHaveBeenCalledWith(
      'request:visible', 'payments', 1, undefined, 'consumption', 'billing'
    )
    const trace = within(dialog).getByRole('combobox', { name: 'Trace records' })
    expect(within(trace).queryByRole('option', { name: 'Payment reconciliation events' })).toBeNull()
    await user.selectOptions(trace, 'funds')
    await waitFor(() =>
      expect(api.getOrderTrace).toHaveBeenLastCalledWith(
        'request:visible', 'funds', 1, undefined, 'consumption', 'billing'
      )
    )
  })

  it('shows face value, bonus, payment and channel of each order', async () => {
    renderPage({ read: true })
    expect(await screen.findByText('EPY7001')).toBeVisible()
    expect(screen.getAllByText('$100.00').length).toBeGreaterThan(0)
    expect(screen.getAllByText('$2.50').length).toBeGreaterThan(0)
    expect(screen.getAllByText('730 CNY')).toHaveLength(2)
    expect(screen.getByText('ZPay A')).toBeVisible()
  })

  it('hides refund and completion buttons without those permissions', async () => {
    renderPage({ read: true })
    await screen.findByText('EPY7001')
    expect(
      screen.queryByRole('button', { name: 'Refund' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Complete' })
    ).not.toBeInTheDocument()
  })

  it('explains the offline stepwise refund flow', async () => {
    const user = userEvent.setup()
    renderPage({ read: true, refund: true })
    await user.click(await screen.findByRole('button', { name: 'Refund' }))
    const dialog = await screen.findByRole('dialog', { name: 'Manual refund' })
    expect(
      within(dialog).getByText(/record the offline payout, and complete/)
    ).toBeVisible()
    expect(within(dialog).getByText('Completed refund (S)')).toBeVisible()
    expect(within(dialog).getByText('In-progress refund (H)')).toBeVisible()
    expect(within(dialog).getByText('Unknown payout (U)')).toBeVisible()
  })

  it('applies a USD refund request without completing payout', async () => {
    api.refundTopup.mockResolvedValue({
      refund: {
        refund_id: 'refund-1',
        trade_no: 'EPY7001',
        status: 'submitted',
        request_usd: '40',
        request_quota: 20000000,
        reason: 'chargeback',
      },
    })
    const user = userEvent.setup()
    renderPage({ read: true, refund: true })
    await user.click(await screen.findByRole('button', { name: 'Refund' }))
    const dialog = await screen.findByRole('dialog', { name: 'Manual refund' })
    await user.type(screen.getByLabelText('Refund amount in USD'), '40')
    await user.type(screen.getByLabelText('Reason (required)'), 'chargeback')
    await user.click(within(dialog).getByRole('button', { name: 'Apply refund' }))
    await waitFor(() => expect(api.refundTopup).toHaveBeenCalled())
    expect(api.refundTopup.mock.calls[0][0]).toBe('EPY7001')
    expect(api.refundTopup.mock.calls[0][1]).toBe('chargeback')
    expect(api.refundTopup.mock.calls[0][4]).toBe('40')
  })

  it('lets an operator record an offline payout on an approved refund', async () => {
    api.listTopupManualRefunds.mockResolvedValue({
      occupancy: {
        face_quota: 50000000,
        completed_quota: 0,
        in_progress_quota: 20000000,
        unknown_quota: 0,
        occupied_quota: 20000000,
        remaining_quota: 30000000,
        face_usd: '100',
        completed_usd: '0',
        in_progress_usd: '40',
        unknown_usd: '0',
        occupied_usd: '40',
        remaining_usd: '60',
      },
      facts: {
        trade_no: 'EPY7001',
        site_id: 7,
        user_id: 7,
        face_usd: '100',
        pay_currency: 'CNY',
        paid_amount: '730',
        paid_currency: 'CNY',
        order_rate: '7.3',
        pending_quota: 0,
        refund_occupied_quota: 20000000,
        refund_completed_usd: '0',
        refund_in_progress_usd: '40',
        refund_unknown_usd: '0',
        refund_remaining_usd: '60',
        bonus_usd: '2.5',
        bonus_used_quota: 0,
        bonus_remaining_quota: 1250000,
        cumulative_refund_usd: '0',
        manual_shortfall_usd: '20',
        manual_refunds: [
          {
            refund_id: 'refund-1',
            trade_no: 'EPY7001',
            status: 'approved',
            request_usd: '40',
            request_quota: 20000000,
            reason: 'chargeback',
            approved_at: 2,
            reclaimed_at: 3,
            shortfall_quota: 10000000,
            shortfall_usd: '20',
            bonus_at: 4,
            commission_at: 5,
          },
        ],
      },
    })
    api.runTopupManualRefundStep.mockResolvedValue({
      refund: { refund_id: 'refund-1', payout_status: 'success' },
    })
    const user = userEvent.setup()
    renderPage({ read: true, refund: true })
    await user.click(await screen.findByRole('button', { name: 'Refund' }))
    const dialog = await screen.findByRole('dialog', { name: 'Manual refund' })
    expect(within(dialog).getByText(/Manual-handling shortfall/)).toBeVisible()
    await user.type(screen.getByLabelText('Offline payout currency'), 'CNY')
    await user.type(screen.getByLabelText('Offline payout amount'), '292')
    await user.type(screen.getByLabelText('Offline payout voucher'), 'bank-slip-88')
    await user.click(
      within(dialog).getByRole('button', { name: 'Record offline payout' })
    )
    await waitFor(() => expect(api.runTopupManualRefundStep).toHaveBeenCalled())
    expect(api.runTopupManualRefundStep.mock.calls[0][0]).toBe('EPY7001')
    expect(api.runTopupManualRefundStep.mock.calls[0][1]).toBe('refund-1')
    expect(api.runTopupManualRefundStep.mock.calls[0][2]).toBe('payout')
  })

  it.each([
    [true,'Returned 40 USD to team 8, lot 3, ledger 42.'],
    [false,'This team recharge requires historical principal review'],
  ])('shows the original team refund ledger when evidence exists (%s)',async(released,expected)=>{
    api.listTopupManualRefunds.mockResolvedValue({facts:{
      trade_no:'EPY7001',team_id:8,team_lot_id:3,team_releases:released?{'refund-2':{ledger_id:42,amount_usd:'40'}}:{},
      manual_refunds:[{refund_id:'refund-2',trade_no:'EPY7001',status:'terminated',deducted_principal_quota:20000000,request_usd:'40',request_quota:20000000}]
    }})
    renderPage({read:true,refund:true})
    await userEvent.setup().click(await screen.findByRole('button',{name:'Refund'}))
    expect(await screen.findByText(String(expected))).toBeVisible()
  })
  it.each([
    ['failed', true],
    ['success', false],
  ])(
    'offers termination with a note when the payout is %s',
    async (payoutStatus, allowed) => {
      api.listTopupManualRefunds.mockResolvedValue({
        facts: {
          trade_no: 'EPY7001',
          site_id: 7,
          user_id: 7,
          face_usd: '100',
          manual_refunds: [
            {
              refund_id: 'refund-2',
              trade_no: 'EPY7001',
              status: 'approved',
              request_usd: '40',
              request_quota: 20000000,
              reason: 'chargeback',
              approved_at: 2,
              payout_status: payoutStatus,
            },
          ],
        },
      })
      api.runTopupManualRefundStep.mockResolvedValue({
        refund: { refund_id: 'refund-2', status: 'terminated' },
      })
      const user = userEvent.setup()
      renderPage({ read: true, refund: true })
      await user.click(await screen.findByRole('button', { name: 'Refund' }))
      const dialog = await screen.findByRole('dialog', {
        name: 'Manual refund',
      })
      const terminate = await within(dialog).findByRole('button', {
        name: 'Terminate refund',
      })
      if (!allowed) {
        expect(terminate).toBeDisabled()
        return
      }
      await user.click(terminate)
      const confirm = await screen.findByRole('alertdialog', {
        name: 'Terminate refund',
      })
      await user.type(
        within(confirm).getByLabelText('Note (optional)'),
        'customer withdrew'
      )
      await user.click(
        within(confirm).getByRole('button', { name: 'Terminate refund' })
      )
      await waitFor(() =>
        expect(api.runTopupManualRefundStep).toHaveBeenCalled()
      )
      expect(api.runTopupManualRefundStep.mock.calls[0]).toEqual([
        'EPY7001',
        'refund-2',
        'terminate',
        { idempotency_key: 'refund-2:terminate', note: 'customer withdrew' },
        'proof',
        false,
      ])
    }
  )

  it('requires a reason and USD amount before applying', async () => {
    api.refundTopup.mockResolvedValue({
      refund: {
        refund_id: 'refund-1',
        trade_no: 'EPY7001',
        status: 'submitted',
        request_usd: '40',
        request_quota: 20000000,
        reason: 'chargeback',
      },
    })
    const user = userEvent.setup()
    renderPage({ read: true, refund: true })
    await user.click(await screen.findByRole('button', { name: 'Refund' }))
    const dialog = await screen.findByRole('dialog', { name: 'Manual refund' })
    const confirm = within(dialog).getByRole('button', { name: 'Apply refund' })
    expect(confirm).toBeDisabled()
    await user.type(screen.getByLabelText('Reason (required)'), 'chargeback')
    expect(confirm).toBeDisabled()
    await user.type(screen.getByLabelText('Refund amount in USD'), '40')
    await waitFor(() => expect(confirm).toBeEnabled())
    await user.click(confirm)
    await waitFor(() => expect(api.refundTopup).toHaveBeenCalled())
    expect(api.refundTopup.mock.calls[0][0]).toBe('EPY7001')
    expect(api.refundTopup.mock.calls[0][1]).toBe('chargeback')
  })

  it('uses the owner refund scope and original order when the owner applies', async () => {
    api.refundTopup.mockResolvedValue({ refund: { refund_id: 'owner-refund' } })
    const user = userEvent.setup()
    renderPage({}, true)
    await user.click(await screen.findByRole('button', { name: 'Refund' }))
    const dialog = within(await screen.findByRole('dialog', { name: 'Manual refund' }))
    expect(screen.queryByRole('button', { name: 'Complete' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Query payment' })).not.toBeInTheDocument()
    await user.type(dialog.getByLabelText('Reason (required)'), 'return unused principal')
    await user.type(dialog.getByLabelText('Refund amount in USD'), '40')
    await waitFor(() => expect(dialog.getByRole('button', { name: 'Apply refund' })).toBeEnabled())
    await user.click(dialog.getByRole('button', { name: 'Apply refund' }))
    await waitFor(() => expect(api.refundTopup).toHaveBeenCalledWith(
      'EPY7001', 'return unused principal', expect.any(String), 0, '40', 'proof', true
    ))
    expect(verification.requestVerification).toHaveBeenCalledWith(expect.objectContaining({
      scope: 'site.order.refund',
      context: { trade_no: 'EPY7001', reason: 'return unused principal', refund_usd: '40' },
    }))
    expect(api.listTopupManualRefunds).toHaveBeenCalledWith('EPY7001', true)
    expect(api.previewTopupRefund).toHaveBeenCalledWith('EPY7001', '40', true)
  })

  it('uses the owner refund step scope to approve the selected application', async () => {
    api.listTopupManualRefunds.mockResolvedValue({ facts: { manual_refunds: [{
      refund_id: 'owner-refund', status: 'submitted', request_usd: '40', reason: 'return unused principal',
    }] } })
    api.runTopupManualRefundStep.mockResolvedValue({ refund: { refund_id: 'owner-refund', status: 'approved' } })
    const user = userEvent.setup()
    renderPage({}, true)
    await user.click(await screen.findByRole('button', { name: 'Refund' }))
    const dialog = within(await screen.findByRole('dialog', { name: 'Manual refund' }))
    await user.click(await dialog.findByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(api.runTopupManualRefundStep).toHaveBeenCalledWith(
      'EPY7001', 'owner-refund', 'approve', { idempotency_key: 'owner-refund:approve' }, 'proof', true
    ))
    expect(verification.requestVerification).toHaveBeenCalledWith(expect.objectContaining({
      scope: 'site.order.refund_step',
      context: expect.objectContaining({ trade_no: 'EPY7001', refund_id: 'owner-refund', step: 'approve' }),
    }))
  })

  it('uses owner refund endpoints for preview, application, listing and steps', async () => {
    const actual = await vi.importActual<typeof import('../api')>('../api')
    const get = vi.spyOn(httpApi, 'get').mockResolvedValue({ data: { success: true, data: {} } })
    const post = vi.spyOn(httpApi, 'post').mockResolvedValue({ data: { success: true, data: {} } })
    try {
      await actual.previewTopupRefund('original/order', '40', true)
      await actual.listTopupManualRefunds('original/order', true)
      await actual.refundTopup('original/order', 'return', 'apply-key', 0, '40', 'owner-proof', true)
      await actual.runTopupManualRefundStep('original/order', 'refund/id', 'approve', {}, 'owner-proof', true)
      expect(get).toHaveBeenCalledWith('/api/adty/site/orders/original%2Forder/refund-preview', expect.anything())
      expect(get).toHaveBeenCalledWith('/api/adty/site/orders/original%2Forder/refunds')
      expect(post).toHaveBeenCalledWith('/api/adty/site/orders/original%2Forder/refund', expect.objectContaining({ refund_usd: '40' }), { headers: { 'X-Security-Proof': 'owner-proof' } })
      expect(post).toHaveBeenCalledWith('/api/adty/site/orders/original%2Forder/refunds/refund%2Fid/approve', {}, { headers: { 'X-Security-Proof': 'owner-proof' } })
    } finally {
      get.mockRestore()
      post.mockRestore()
    }
  })
})
