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
import {
  cleanup,
  act,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AxiosAdapter } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { OwnerVerificationProvider } from '@/features/adty-site-owner/components/owner-verification'
import { StaffPanel } from '@/features/adty-site-owner/components/staff-panel'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { StaffWorkspace } from '../index'

const verification = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/features/auth/secure-verification', async (original) => ({
  ...(await original<typeof import('@/features/auth/secure-verification')>()),
  useSecureVerification: () => ({
    requestVerification: verification.request,
    isActive: false,
    dialogProps: { state: { phase: 'idle' } },
  }),
}))

const originalAdapter = api.defaults.adapter
let members: Record<string, unknown>[]
let writes: { path: string; body: Record<string, unknown>; proof: unknown }[]
let permissions: string[]
let reads: string[]
let staffVersion: number
let orderReads: { path: string; kind: unknown; page?: number }[]
let orderTotal: number
let orderFailure: boolean
let requestStatus: 'approved' | 'rejected' | 'pending' | undefined
let clients: QueryClient[] = []
beforeEach(() => {
  members = []
  writes = []
  permissions = ['customers.read']
  reads = []
  staffVersion = 1
  orderReads = []
  orderTotal = 1
  orderFailure = false
  requestStatus = undefined
  verification.request.mockResolvedValue({ proof_token: 'single-use-proof' })
  useAuthStore.getState().auth.setBundle({
    user: { id: 11, username: 'staff', role: 1, staff_site_id: 7 },
    access_token: 'test-session',
    token_type: 'Bearer',
    access_expires_at: Math.floor(Date.now() / 1000) + 3600,
    session: {
      sid: 'staff-session',
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: 1,
      last_active_at: 1,
      expires_at: Math.floor(Date.now() / 1000) + 7200,
    },
  })
  api.defaults.adapter = (async (config) => {
    const path = config.url ?? ''
    if (config.method === 'get') reads.push(path)
    let data: unknown
    if (config.method !== 'get') {
      const body =
        typeof config.data === 'string' ? JSON.parse(config.data) : config.data
      writes.push({ path, body, proof: config.headers.get('X-Security-Proof') })
      if (path === '/api/adty/site/balance-requests/42/review') {
        requestStatus = body.approve ? 'approved' : 'rejected'
      } else if (config.method === 'post') {
        members = [
          {
            ...body,
            id: 1,
            user_id: 11,
            site_id: 7,
            status: 'enabled',
            version: 1,
          },
        ]
      } else if (config.method === 'delete') {
        members = []
      } else {
        members = members.map((member) => ({
          ...member,
          ...body,
          version: Number(member.version) + 1,
        }))
      }
      data = members[0]
    } else if (path.endsWith('/staff/self')) {
      data = {
        staff: {
          id: 1,
          user_id: 11,
          site_id: 7,
          username: 'staff',
          display_name: 'Support',
          role: 'support',
          status: 'enabled',
          version: staffVersion,
          permissions,
        },
        site_name: 'shop.example',
      }
    } else if (path.endsWith('/staff/customers')) {
      data = {
        total: 1,
        items: [
          {
            id: 20,
            username: 'local-customer',
            email: 'p***@example.com',
            status: 1,
            group: 'default',
            created_at: 1,
            reviewed: false,
          },
        ],
      }
    } else if (path.includes('/customers/20/orders')) {
      const kind = config.params?.order_type
      orderReads.push({ path, kind, page: config.params?.p })
      if (orderFailure) throw new Error('Order service unavailable')
      const order = {
        trade_no: `staff-${kind}-order`, order_type: kind, status: 'success',
        face_usd: '1.25', created_at: 1001, completed_at: 1002,
      }
      data = path.endsWith('/orders') ? { total: orderTotal, items: orderTotal ? [order] : [] } : { order }
    } else if (path.endsWith('/topups')) {
      data = {
        total: 1,
        items: [
          {
            trade_no: 'staff-visible-order',
            credit_quota: 500000,
            status: 'success',
            created_at: 1001,
          },
        ],
      }
    } else if (path.endsWith('/staff/audits')) {
      data = { total: 0, items: [] }
    } else if (path.endsWith('/balance-requests')) {
      const items = requestStatus && config.params?.status === requestStatus
        ? [{
            id: 42,
            customer_user_id: 20,
            direction: 'increase',
            amount_usd: '2',
            status: requestStatus,
            order_ref: 'paid-order-20',
            reason: 'Correct credit',
            funding_source: 'site_earnings',
            reviewed_at: requestStatus === 'pending' ? 0 : 1000,
          }]
        : []
      data = { total: items.length, items }
    } else {
      data = { total: members.length, items: members }
    }
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data },
    }
  }) satisfies AxiosAdapter
})
afterEach(() => {
  cleanup()
  for (const client of clients) client.clear()
  clients = []
  api.defaults.adapter = originalAdapter
  vi.clearAllMocks()
  useAuthStore.getState().auth.reset()
})

function renderStaff(owner = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>
      {owner ? (
        <OwnerVerificationProvider>
          <StaffPanel siteId={7} />
        </OwnerVerificationProvider>
      ) : (
        <StaffWorkspace />
      )}
    </QueryClientProvider>
  )
}

describe('staff management and workspace', () => {
  it('queries unified orders and their safe detail using only granted categories', async () => {
    permissions = ['customers.read', 'customers.topups']
    const user = userEvent.setup()
    renderStaff(false)
    await user.click(await screen.findByRole('button', { name: 'Orders' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByRole('button', { name: 'staff-topup-order' })
    const filter = within(dialog).getByLabelText('Order type')
    expect(within(filter).getAllByRole('option').map((option) => (option as HTMLOptionElement).value)).toEqual(['topup', 'voucher'])
    await user.selectOptions(filter, 'voucher')
    await user.click(await within(dialog).findByRole('button', { name: 'staff-voucher-order' }))
    await within(dialog).findByText('staff-voucher-order', { selector: 'dd' })
    expect(orderReads).toContainEqual({ path: '/api/adty/staff/customers/20/orders', kind: 'voucher', page: 1 })
    expect(orderReads).toContainEqual({ path: '/api/adty/staff/customers/20/orders/staff-voucher-order', kind: 'voucher', page: undefined })
    expect(within(dialog).queryByText('Principal and gift batches')).not.toBeInTheDocument()
    expect(within(dialog).queryByText('Financial review history')).not.toBeInTheDocument()
  })

  it('limits usage-only staff to consumption orders and removes an open view after revocation', async () => {
    permissions = ['customers.read', 'customers.usage']
    const user = userEvent.setup()
    renderStaff(false)
    await user.click(await screen.findByRole('button', { name: 'Orders' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByRole('button', { name: 'staff-consumption-order' })
    expect(within(within(dialog).getByLabelText('Order type')).getAllByRole('option')).toHaveLength(1)
    permissions = ['customers.read']
    staffVersion = 2
    await act(async () => { await clients[0].invalidateQueries({ queryKey: ['adty-staff', 11, 'self'] }) })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Orders' })).not.toBeInTheDocument()
    expect(orderReads.every((read) => read.kind === 'consumption')).toBe(true)
  })

  it('resets unified order pagination when the selected category changes', async () => {
    permissions = ['customers.read', 'customers.topups', 'customers.usage']
    orderTotal = 21
    const user = userEvent.setup()
    renderStaff(false)
    await user.click(await screen.findByRole('button', { name: 'Orders' }))
    const dialog = screen.getByRole('dialog')
    await user.click(await within(dialog).findByRole('button', { name: 'Next' }))
    await within(dialog).findByText('2 / 2')
    expect(orderReads).toContainEqual({ path: '/api/adty/staff/customers/20/orders', kind: 'topup', page: 2 })
    await user.selectOptions(within(dialog).getByLabelText('Order type'), 'consumption')
    await within(dialog).findByRole('button', { name: 'staff-consumption-order' })
    expect(within(dialog).getByText('1 / 2')).toBeInTheDocument()
    expect(orderReads).toContainEqual({ path: '/api/adty/staff/customers/20/orders', kind: 'consumption', page: 1 })
  })

  it('shows an order query failure and retries to the explicit empty state', async () => {
    permissions = ['customers.read', 'customers.topups']
    orderFailure = true
    const user = userEvent.setup()
    renderStaff(false)
    await user.click(await screen.findByRole('button', { name: 'Orders' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('Oops! Something went wrong')
    expect(within(dialog).queryByText('No records')).not.toBeInTheDocument()
    orderFailure = false
    orderTotal = 0
    await user.click(within(dialog).getByRole('button', { name: 'Retry' }))
    await within(dialog).findByText('No records')
    expect(within(dialog).queryByText('Oops! Something went wrong')).not.toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: 'Next' })).not.toBeInTheDocument()
  })

  it('requires concrete provenance before submitting a balance increase', async () => {
    permissions = ['customers.read', 'customers.balance', 'customers.balance_request']
    const user = userEvent.setup()
    renderStaff(false)
    await user.click(await screen.findByRole('button', { name: 'Request balance change' }))
    const dialog = screen.getByRole('dialog')
    await user.selectOptions(within(dialog).getByLabelText('Direction'), 'increase')
    await user.type(within(dialog).getByLabelText('Amount'), '2')
    await user.click(within(dialog).getByRole('checkbox'))
    const submit = within(dialog).getByRole('button', { name: 'Request balance change' })
    expect(submit).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Original order'), 'paid-order-20')
    await user.type(within(dialog).getByLabelText('Reason'), 'Correct original credit')
    await user.selectOptions(within(dialog).getByLabelText('Funding source'), 'site_earnings')
    await user.click(submit)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(writes[0]).toMatchObject({
      path: '/api/adty/staff/balance-requests',
      body: { customer_id: 20, direction: 'increase', amount_usd: '2',
        reason: 'Correct original credit', order_ref: 'paid-order-20',
        funding_source: 'site_earnings', batch_kind: '', batch_source: '' },
    })
  })

  it('requires an explicit batch for a balance reduction', async () => {
    permissions = ['customers.read', 'customers.balance', 'customers.balance_request']
    const user = userEvent.setup()
    renderStaff(false)
    await user.click(await screen.findByRole('button', { name: 'Request balance change' }))
    const dialog = screen.getByRole('dialog')
    await user.selectOptions(within(dialog).getByLabelText('Direction'), 'decrease')
    await user.type(within(dialog).getByLabelText('Amount'), '1')
    await user.type(within(dialog).getByLabelText('Original order'), 'paid-order-20')
    await user.type(within(dialog).getByLabelText('Reason'), 'Recover unused credit')
    await user.click(within(dialog).getByRole('checkbox'))
    const submit = within(dialog).getByRole('button', { name: 'Request balance change' })
    expect(submit).toBeDisabled()
    await user.selectOptions(within(dialog).getByLabelText('Batch kind'), 'principal')
    await user.type(within(dialog).getByLabelText('Batch source'), 'paid-order-20')
    await user.click(submit)
    await waitFor(() => expect(writes).toHaveLength(1))
    expect(writes[0].body).toMatchObject({ funding_source: 'specified_batch',
      batch_kind: 'principal', batch_source: 'paid-order-20' })
  })

  it.each([
    { button: 'Approve', status: 'approved', approve: true },
    { button: 'Reject', status: 'rejected', approve: false },
  ])('lets the owner select $button and refreshes the pending list', async (action) => {
    requestStatus = 'pending'
    const user = userEvent.setup()
    renderStaff()
    const table = await screen.findByRole('table', { name: 'Balance requests' })
    expect(await within(table).findByText(/paid-order-20/)).toBeVisible()
    await user.click(within(table).getByRole('button', { name: action.button }))
    expect(await within(table).findByText('No data')).toBeVisible()
    expect(writes).toHaveLength(1)
    expect(writes[0]).toMatchObject({
      path: '/api/adty/site/balance-requests/42/review',
      body: { approve: action.approve },
    })
    await user.selectOptions(screen.getByLabelText('Status'), action.status)
    const reviewed = await screen.findByRole('table', { name: 'Balance requests' })
    expect(await within(reviewed).findByText(/paid-order-20/)).toBeVisible()
    expect(within(reviewed).queryByRole('button')).not.toBeInTheDocument()
  })

  it('creates a staff account with explicit read permissions after verification', async () => {
    const user = userEvent.setup()
    renderStaff()
    await user.click(
      await screen.findByRole('button', { name: 'Create staff account' })
    )
    const dialog = screen.getByRole('dialog')
    await user.type(within(dialog).getByLabelText('Username'), 'employee-1')
    await user.type(
      within(dialog).getByLabelText('Display name'),
      'Finance One'
    )
    await user.type(
      within(dialog).getByLabelText('Password'),
      'employee-password-42'
    )
    await user.selectOptions(
      within(dialog).getByLabelText('Staff role'),
      'finance'
    )
    await user.click(
      within(dialog).getByRole('checkbox', { name: 'View customer usage' })
    )
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await screen.findByText('Finance One')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(writes).toHaveLength(1)
    expect(writes[0].proof).toBe('single-use-proof')
    expect(writes[0].body.permissions).toEqual([
      'customers.read',
      'customers.balance',
      'customers.topups',
    ])
    expect(screen.queryByText('employee-password-42')).not.toBeInTheDocument()
  })

  it('keeps the form open when verification is cancelled', async () => {
    verification.request.mockResolvedValue(null)
    const user = userEvent.setup()
    renderStaff()
    await user.click(
      await screen.findByRole('button', { name: 'Create staff account' })
    )
    const dialog = screen.getByRole('dialog')
    await user.type(within(dialog).getByLabelText('Username'), 'employee-2')
    await user.type(
      within(dialog).getByLabelText('Display name'),
      'Support Two'
    )
    await user.type(
      within(dialog).getByLabelText('Password'),
      'employee-password-42'
    )
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: 'Save' })).toBeEnabled()
    )
    expect(writes).toHaveLength(0)
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('disables with confirmation and sends the current revision', async () => {
    members = [
      {
        id: 1,
        site_id: 7,
        user_id: 11,
        username: 'staff',
        display_name: 'Support One',
        role: 'support',
        permissions: ['customers.read'],
        status: 'enabled',
        version: 4,
      },
    ]
    const user = userEvent.setup()
    renderStaff()
    await user.click(await screen.findByRole('button', { name: 'Disable' }))
    const confirmation = screen.getByRole('alertdialog')
    await user.click(
      within(confirmation).getByRole('button', { name: 'Disable' })
    )
    await screen.findByRole('button', { name: 'Enable' })
    expect(writes[0].body).toMatchObject({
      expected_version: 4,
      status: 'disabled',
    })
    expect(writes[0].body).not.toHaveProperty('password')
  })

  it('shows only authorized workspace data and never owner or key actions', async () => {
    renderStaff(false)
    await screen.findByText('local-customer')
    expect(
      screen.getByRole('heading', { name: 'Staff workspace' })
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('columnheader', { name: 'Balance' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Recharge records' })
    ).not.toBeInTheDocument()
    expect(screen.queryByText('API Keys')).not.toBeInTheDocument()
    expect(screen.queryByText('Site owner center')).not.toBeInTheDocument()
  })

  it('does not query or display customers without a list grant', async () => {
    permissions = []
    renderStaff(false)
    await screen.findByText('No customer permissions have been assigned.')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() =>
      expect(reads.filter((path) => path.endsWith('/staff/self'))).toHaveLength(
        2
      )
    )
    expect(reads).not.toContain('/api/adty/staff/customers')
    expect(screen.queryByText('local-customer')).not.toBeInTheDocument()
  })

  it('hides an open record dialog when a refreshed grant is removed', async () => {
    permissions = ['customers.read', 'customers.topups']
    const user = userEvent.setup()
    renderStaff(false)
    await user.click(
      await screen.findByRole('button', { name: 'Recharge records' })
    )
    await screen.findByText('staff-visible-order')
    permissions = ['customers.read']
    staffVersion = 2
    await act(async () => {
      await clients[0].invalidateQueries({
        queryKey: ['adty-staff', 11, 'self'],
      })
    })
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(screen.queryByText('staff-visible-order')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Recharge records' })
    ).not.toBeInTheDocument()
  })
})
