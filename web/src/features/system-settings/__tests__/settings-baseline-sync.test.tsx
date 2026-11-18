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
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createElement, useState, type ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { BasicAuthSection } from '../auth/basic-auth-section'
import { SettingsPageProvider } from '../components/settings-page-context'
import { ApiInfoSection } from '../content/api-info-section'
import { useUpdateOption } from '../hooks/use-update-option'
import type { SystemOption, SystemOptionsResponse } from '../types'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('System settings baseline and cache sync after save (ISS-R1-0190)', () => {
  it('useUpdateOption updates the ["system-options"] React Query cache in-place on save', async () => {
    vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    const initialOptions: SystemOption[] = [
      { key: 'PasswordLoginEnabled', value: 'true' },
      { key: 'RegisterEnabled', value: 'true' },
    ]
    client.setQueryData<SystemOptionsResponse>(['system-options'], {
      success: true,
      message: '',
      data: initialOptions,
    })

    function wrapper({ children }: { children: ReactNode }) {
      return createElement(QueryClientProvider, { client }, children)
    }

    const { result } = renderHook(() => useUpdateOption(), {
      wrapper,
    })

    await result.current.mutateAsync({
      key: 'PasswordLoginEnabled',
      value: 'false',
    })

    const cached = client.getQueryData<SystemOptionsResponse>([
      'system-options',
    ])
    expect(cached?.data).toEqual([
      { key: 'PasswordLoginEnabled', value: 'false' },
      { key: 'RegisterEnabled', value: 'true' },
    ])
  })

  it('BasicAuthSection updates its baseline on save so toggling true -> false -> true saves both changes', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    const user = userEvent.setup()

    function Fixture() {
      const [actions, setActions] = useState<HTMLDivElement | null>(null)
      const [client] = useState(
        () =>
          new QueryClient({
            defaultOptions: {
              queries: { retry: false },
              mutations: { retry: false },
            },
          })
      )
      return (
        <QueryClientProvider client={client}>
          <SettingsPageProvider actionsContainer={actions}>
            <div ref={setActions} />
            <BasicAuthSection
              defaultValues={{
                PasswordLoginEnabled: true,
                PasswordRegisterEnabled: true,
                EmailVerificationEnabled: false,
                RegisterEnabled: true,
                EmailDomainRestrictionEnabled: false,
                EmailAliasRestrictionEnabled: false,
                EmailDomainWhitelist: '',
              }}
            />
          </SettingsPageProvider>
        </QueryClientProvider>
      )
    }

    render(<Fixture />)

    const switches = screen.getAllByRole('switch')
    const passwordLoginSwitch = switches[0]

    // First save: true -> false
    await user.click(passwordLoginSwitch)
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenNthCalledWith(1, '/api/option/', {
      key: 'PasswordLoginEnabled',
      value: false,
    })

    // Second save without prop change: false -> true must be recognized as a change!
    await user.click(passwordLoginSwitch)
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
    expect(put).toHaveBeenNthCalledWith(2, '/api/option/', {
      key: 'PasswordLoginEnabled',
      value: true,
    })
  })

  it('ApiInfoSection updates its saved baseline after saving so added items remain persisted for subsequent edits', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    const user = userEvent.setup()

    function Fixture() {
      const [client] = useState(
        () =>
          new QueryClient({
            defaultOptions: {
              queries: { retry: false },
              mutations: { retry: false },
            },
          })
      )
      return (
        <QueryClientProvider client={client}>
          <ApiInfoSection enabled data='[]' />
        </QueryClientProvider>
      )
    }

    render(<Fixture />)

    await user.click(screen.getByRole('button', { name: 'Add API' }))
    await user.type(
      screen.getByPlaceholderText('https://api.example.com'),
      'https://api.adty.ai'
    )
    await user.type(screen.getByPlaceholderText('e.g., CN2 GIA'), 'Primary Gateway')
    await user.type(
      screen.getByPlaceholderText('e.g., Recommended for China Mainland Users'),
      'Main production route'
    )
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(screen.getByText('Primary Gateway')).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Save Settings' }))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith(
      '/api/option/',
      expect.objectContaining({
        key: 'console_setting.api_info',
      })
    )

    // After save completes, the added item should still be rendered and Save Settings disabled until next edit
    expect(screen.getByText('Primary Gateway')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Save Settings' })).toBeDisabled()
  })
})
