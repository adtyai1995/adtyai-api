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

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  deleteEpayChannel,
  getEpayChannels,
  replaceEpayChannel,
  saveEpayChannel,
  type EpayChannel,
} from '../api'
import { PaymentChannels } from '../index'

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getEpayChannels: vi.fn(),
    saveEpayChannel: vi.fn(),
    deleteEpayChannel: vi.fn(),
    replaceEpayChannel: vi.fn(),
    revokeEpayChannel: vi.fn(),
    setEpayChannelStatus: vi.fn(),
    testEpayChannel: vi.fn(),
  }
})

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    warning: vi.fn(),
    info: vi.fn(),
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
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

const sampleChannels: EpayChannel[] = [
  {
    id: 1,
    name: 'Futong',
    gateway_url: 'https://futong.example.com',
    merchant_id: '1001',
    sign_type: 'MD5',
    priority: 10,
    weight: 1,
    order_mode: 'api',
    probe_mode: 'off',
    status: 'disabled',
    status_reason: 'manual',
    unpaid_streak: 2,
    unpaid_threshold: 0,
    has_key: true,
    today_cny: { alipay: '100.00' },
    order_count: 5,
    scene_names: ['Wallet Top-up'],
    methods: [
      {
        method: 'alipay',
        display_name: 'Alipay 1',
        type_code: 'alipay',
        min_cny: '1',
        max_cny: '500',
        daily_limit_cny: '5000',
        enabled: true,
      },
    ],
  },
  {
    id: 2,
    name: 'Standby',
    gateway_url: 'https://standby.example.com',
    merchant_id: '2002',
    sign_type: 'MD5',
    priority: 5,
    weight: 1,
    order_mode: 'api',
    probe_mode: 'off',
    status: 'enabled',
    status_reason: '',
    unpaid_streak: 0,
    unpaid_threshold: 0,
    has_key: true,
    today_cny: {},
    order_count: 0,
    methods: [
      {
        method: 'alipay',
        type_code: 'alipay',
        min_cny: '0',
        max_cny: '0',
        daily_limit_cny: '0',
        enabled: true,
      },
    ],
  },
  {
    id: 3,
    name: 'OldRevoked',
    gateway_url: 'https://revoked.example.com',
    merchant_id: '3003',
    sign_type: 'MD5',
    priority: 1,
    weight: 1,
    order_mode: 'api',
    probe_mode: 'off',
    status: 'revoked',
    status_reason: 'manual',
    unpaid_streak: 0,
    unpaid_threshold: 0,
    has_key: false,
    today_cny: {},
    order_count: 2,
    methods: [
      {
        method: 'alipay',
        type_code: 'alipay',
        min_cny: '0',
        max_cny: '0',
        daily_limit_cny: '0',
        enabled: true,
      },
    ],
  },
]

describe('Epay PaymentChannels full edit, replace, and delete flows', () => {
  beforeEach(() => {
    useAuthStore.setState((state) => ({
      auth: {
        ...state.auth,
        user: {
          id: 1,
          username: 'root',
          display_name: 'Root',
          role: ROLE.SUPER_ADMIN,
        } as never,
      },
    }))
    vi.mocked(getEpayChannels).mockResolvedValue(sampleChannels)
    vi.mocked(saveEpayChannel).mockResolvedValue(undefined)
    vi.mocked(deleteEpayChannel).mockResolvedValue({
      id: 1,
      name: 'Futong',
      updated_scenes: 1,
      disabled_scenes: 0,
    })
    vi.mocked(replaceEpayChannel).mockResolvedValue({
      old_id: 1,
      old_name: 'Futong',
      new_id: 2,
      new_name: 'Standby',
      deleted_old: false,
      updated_scenes: 1,
    })
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('allows Root to edit all fields on ordered and revoked channels', async () => {
    renderWithQuery(<PaymentChannels embedded />)

    await waitFor(() => {
      expect(screen.getByText('Futong')).toBeTruthy()
    })

    // All 3 rows (including revoked) expose Edit, Replace, Delete.
    expect(screen.getAllByRole('button', { name: 'Edit' })).toHaveLength(3)
    expect(screen.getAllByRole('button', { name: 'Replace' })).toHaveLength(3)
    expect(screen.getAllByRole('button', { name: 'Delete' })).toHaveLength(3)

    // Edit ordered channel Futong: change gateway URL, merchant ID, and merchant key.
    fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0])

    await waitFor(() => {
      expect(screen.getByLabelText('Gateway address')).toBeTruthy()
    })
    fireEvent.change(screen.getByLabelText('Gateway address'), {
      target: { value: 'https://futong-new.example.com' },
    })
    fireEvent.change(screen.getByLabelText('Merchant ID'), {
      target: { value: '9999' },
    })
    fireEvent.change(screen.getByLabelText('Merchant key'), {
      target: { value: 'new-secret-key' },
    })

    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => {
      expect(saveEpayChannel).toHaveBeenCalledWith(
        1,
        expect.objectContaining({
          gateway_url: 'https://futong-new.example.com',
          merchant_id: '9999',
          merchant_key: 'new-secret-key',
        })
      )
    })
  })

  it('replaces a channel with an existing channel and migrates scenes', async () => {
    renderWithQuery(<PaymentChannels embedded />)

    await waitFor(() => {
      expect(screen.getByText('Futong')).toBeTruthy()
    })

    fireEvent.click(screen.getAllByRole('button', { name: 'Replace' })[0])

    await waitFor(() => {
      expect(screen.getByText('Replace payment channel: Futong')).toBeTruthy()
    })
    expect(
      screen.getByText(
        'Referenced payment scenes will be migrated automatically: Wallet Top-up'
      )
    ).toBeTruthy()

    fireEvent.click(screen.getByRole('radio', { name: 'Use existing channel' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm replace' }))

    await waitFor(() => {
      expect(replaceEpayChannel).toHaveBeenCalledWith(
        1,
        expect.objectContaining({
          target_channel_id: 2,
          delete_old: false,
        })
      )
    })
  })

  it('deletes an ordered channel with clear snapshot/scene warning', async () => {
    renderWithQuery(<PaymentChannels embedded />)

    await waitFor(() => {
      expect(screen.getByText('Futong')).toBeTruthy()
    })

    fireEvent.click(screen.getAllByRole('button', { name: 'Delete' })[0])

    await waitFor(() => {
      expect(
        screen.getByText(/It has 5 historical orders and is referenced by payment scenes/)
      ).toBeTruthy()
    })

    fireEvent.click(screen.getByRole('button', { name: 'Continue' }))

    await waitFor(() => {
      expect(deleteEpayChannel).toHaveBeenCalledWith(1)
    })
  })
})
