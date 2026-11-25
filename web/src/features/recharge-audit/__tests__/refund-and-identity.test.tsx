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

import { GiftFulfillmentsPanel } from '../components/gift-fulfillments-panel'
import { RefundDialog } from '../components/order-action-dialogs'
import { OrderTraceTable } from '../components/order-trace-table'
import { PaymentOrphanReviewDialog } from '../components/payment-orphan-review-dialog'

const {
  listTopupManualRefundsMock,
  previewTopupRefundMock,
  getOrderTraceMock,
  refundTopupMock,
  getAdminGiftFulfillmentsMock,
  requestVerificationMock,
} = vi.hoisted(() => ({
  listTopupManualRefundsMock: vi.fn(),
  previewTopupRefundMock: vi.fn(),
  getOrderTraceMock: vi.fn(),
  refundTopupMock: vi.fn(),
  getAdminGiftFulfillmentsMock: vi.fn(),
  requestVerificationMock: vi.fn(),
}))

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    listTopupManualRefunds: (...args: unknown[]) =>
      listTopupManualRefundsMock(...args),
    previewTopupRefund: (...args: unknown[]) => previewTopupRefundMock(...args),
    getOrderTrace: (...args: unknown[]) => getOrderTraceMock(...args),
    refundTopup: (...args: unknown[]) => refundTopupMock(...args),
    getAdminGiftFulfillments: (...args: unknown[]) =>
      getAdminGiftFulfillmentsMock(...args),
  }
})

vi.mock(
  '@/features/auth/secure-verification/hooks/use-secure-verification',
  () => ({
    useSecureVerification: () => ({
      requestVerification: (...args: unknown[]) =>
        requestVerificationMock(...args),
      dialogProps: {
        open: false,
        onOpenChange: vi.fn(),
      },
    }),
  })
)

vi.mock(
  '@/features/auth/secure-verification/components/secure-verification-dialog',
  () => ({
    SecureVerificationDialog: () => null,
  })
)

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
        <p>{props.description}</p>
        {props.children}
        {props.footer}
      </div>
    ) : null,
}))

vi.mock('@/components/confirm-dialog', () => ({
  ConfirmDialog: () => null,
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

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

const SAMPLE_ORDER = {
  id: 1,
  user_id: 20,
  username: 'alice',
  site_id: 0,
  site_name: '',
  order_type: 'topup' as const,
  trade_no: 'ORD-100',
  payment_method: 'alipay',
  payment_provider: 'epay',
  status: 'success',
  create_time: 1700000000,
  complete_time: 1700000100,
  amount: 5000000,
  money: 10,
  has_ext: true,
  face_usd: '10.00',
  bonus_usd: '0.00',
  credit_quota: 5000000,
  pay_amount: '70.00',
  pay_currency: 'CNY',
  paid_amount: '70.00',
  paid_currency: 'CNY',
  paid_tx_hash: '',
  paid_source: 'callback',
  channel_id: 1,
  channel_name: 'Alipay',
  anomaly: '' as const,
  actions: { complete: false, refund: true, query: false },
}

describe('Recharge audit manual refund reconciliation & operator/customer identity (R1-10, R1-60)', () => {
  beforeEach(() => {
    requestVerificationMock.mockResolvedValue({ proof_token: 'proof-123' })
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('allows selecting pending external reversal orphan, enables Complete for reconciled refund, and disables Terminate on unknown payout', async () => {
    getOrderTraceMock.mockResolvedValue({
      total: 2,
      page: 1,
      page_size: 20,
      items: [
        {
          id: 88,
          kind: 'Reconciliation event',
          source: 'charge.refunded',
          status: 'pending_review',
          reference: 'evt_refund_88',
          created_at: 1700000500,
        },
        {
          id: 89,
          kind: 'Reconciliation event',
          source: 'payment_callback',
          status: 'pending_review',
          reference: 'evt_pay_89',
          created_at: 1700000400,
        },
      ],
    })
    previewTopupRefundMock.mockResolvedValue({
      deducted_quota: 5000000,
      bonus_quota: 0,
      refund_quota: 5000000,
      refund_amount: '10.00',
      currency: 'USD',
    })
    refundTopupMock.mockResolvedValue({
      refund: { refund_id: 'RF-NEW' },
    })
    listTopupManualRefundsMock.mockResolvedValue({
      facts: {
        trade_no: 'ORD-100',
        site_id: 0,
        user_id: 20,
        face_usd: '10.00',
        pay_currency: 'CNY',
        paid_amount: '70.00',
        paid_currency: 'CNY',
        order_rate: '7.00',
        pending_quota: 0,
        refund_occupied_quota: 0,
        bonus_usd: '0.00',
        bonus_used_quota: 0,
        bonus_remaining_quota: 0,
        cumulative_refund_usd: '0.00',
        manual_refunds: [
          {
            refund_id: 'RF-RECON',
            trade_no: 'ORD-100',
            status: 'approved',
            request_usd: '10.00',
            request_quota: 5000000,
            reason: 'Reconcile',
            reconcile_orphan_id: 88,
            approved_at: 1700001000,
            reclaimed_at: 1700001001,
            bonus_at: 1700001002,
            commission_at: 1700001003,
            payout_status: '',
          },
          {
            refund_id: 'RF-UNK',
            trade_no: 'ORD-100',
            status: 'approved',
            request_usd: '5.00',
            request_quota: 2500000,
            reason: 'Unknown payout test',
            reconcile_orphan_id: 0,
            approved_at: 1700001000,
            reclaimed_at: 1700001001,
            bonus_at: 1700001002,
            commission_at: 1700001003,
            payout_status: 'unknown',
          },
        ],
      },
    })

    renderWithQuery(
      <RefundDialog order={SAMPLE_ORDER} onOpenChange={vi.fn()} onDone={vi.fn()} />
    )

    // Reconciled refund (RF-RECON) enables Complete refund even without offline payout.
    await waitFor(() => {
      const completeBtn = screen.getByRole('button', {
        name: 'Complete refund',
      }) as HTMLButtonElement
      expect(completeBtn.disabled).toBe(false)
    })

    // Switching to RF-UNK (payout_status === 'unknown') disables both Complete and Terminate and explains why.
    fireEvent.change(screen.getByLabelText('Refund applications'), {
      target: { value: 'RF-UNK' },
    })
    expect(
      (screen.getByRole('button', { name: 'Complete refund' }) as HTMLButtonElement)
        .disabled
    ).toBe(true)
    expect(
      (screen.getByRole('button', { name: 'Terminate refund' }) as HTMLButtonElement)
        .disabled
    ).toBe(true)
    expect(
      screen.getByText(
        'An unknown offline payout cannot be terminated before failure is confirmed'
      )
    ).toBeTruthy()

    // Select orphan #88 (only external reversal event appears, not payment_callback #89) and apply refund.
    const orphanSelect = screen.getByLabelText(
      'Reconcile external reversal event (optional)'
    ) as HTMLSelectElement
    expect([...orphanSelect.options].map((o) => o.value)).toEqual(['', '88'])
    fireEvent.change(orphanSelect, { target: { value: '88' } })
    expect(
      screen.getByText(
        'This refund will reconcile the selected external reversal event. No second offline payout is required.'
      )
    ).toBeTruthy()

    fireEvent.change(screen.getByLabelText('Refund amount in USD'), {
      target: { value: '10' },
    })
    fireEvent.change(screen.getByLabelText('Reason (required)'), {
      target: { value: 'External chargeback' },
    })

    await waitFor(() => {
      expect(
        (screen.getByRole('button', { name: 'Apply refund' }) as HTMLButtonElement)
          .disabled
      ).toBe(false)
    })
    fireEvent.click(screen.getByRole('button', { name: 'Apply refund' }))

    await waitFor(() => expect(refundTopupMock).toHaveBeenCalledTimes(1))
    expect(requestVerificationMock).toHaveBeenCalledWith(
      expect.objectContaining({
        context: expect.objectContaining({ reconcile_orphan_id: 88 }),
      })
    )
    expect(refundTopupMock).toHaveBeenCalledWith(
      'ORD-100',
      'External chargeback',
      expect.any(String),
      0,
      '10',
      'proof-123',
      false,
      88
    )
  })

  it('formats identities via formatUserIdentity across GiftFulfillmentsPanel, OrderTraceTable, and PaymentOrphanReviewDialog', async () => {
    getAdminGiftFulfillmentsMock.mockResolvedValue({
      total: 1,
      page: 1,
      page_size: 20,
      items: [
        {
          id: 1,
          trade_no: 'ORD-GIFT-1',
          user_id: 55,
          username: 'carol',
          site_id: 2,
          bonus_usd: '5.00',
          bonus_quota: 2500000,
          original_source: 'earnings',
          original_earning: '5.00',
          original_margin: '0',
          status: 'pending_review',
          reason: 'manual check',
          created_at: 1700000000,
          updated_at: 1700000000,
        },
      ],
    })

    renderWithQuery(
      <>
        <GiftFulfillmentsPanel />
        <OrderTraceTable
          entries={[
            {
              id: 7,
              kind: 'Manual refund',
              source: 'admin',
              status: 'completed',
              created_at: 1700000000,
              reference: 'RF-7',
              operator_id: 1,
              operator_username: 'root',
            },
          ]}
        />
        <PaymentOrphanReviewDialog
          event={{
            id: 9,
            provider: 'epay',
            event_type: 'charge.refunded',
            reference_id: 'ORD-100',
            status: 'pending_review',
            reason: 'reversal',
            error: '',
            create_time: 1700000000,
            resolution_note: '',
            can_credit: false,
            review: {
              id: 3,
              status: 'pending_review',
              note: 'checking',
              proof: 'TX-3',
              trade_no: 'ORD-100',
              external_amount: '10.00',
              external_currency: 'USD',
              refund_ledger_id: 0,
              deducted_quota: 0,
              refund_quota: 0,
              refund_amount: '0',
              paid_amount: '10',
              paid_currency: 'USD',
              exposure_amount: '0',
              created_at: 1700000100,
              operator_id: 4,
              operator_username: 'finance_ops',
            },
          }}
          canReview={false}
          onClose={vi.fn()}
          onDone={vi.fn()}
        />
      </>
    )

    await waitFor(() => {
      expect(screen.getByText('55 / carol')).toBeTruthy()
    })
    expect(screen.getByText(/1 \/ root/)).toBeTruthy()
    expect(screen.getByText('4 / finance_ops')).toBeTruthy()
  })
})
