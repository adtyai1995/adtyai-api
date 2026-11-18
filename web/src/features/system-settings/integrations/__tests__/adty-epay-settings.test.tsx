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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import {
  AdtyEpaySettingsSection,
  type AdtyEpaySettingsDefaults,
} from '../adty-epay-settings-section'

const baseDefaults: AdtyEpaySettingsDefaults = {
  orderMinutes: 15,
  channelTimeoutSeconds: 5,
  totalTimeoutSeconds: 10,
  queryAfterSeconds: 60,
  expiredQuerySeconds: 0,
  unpaidThreshold: 3,
  probeEnabled: false,
  probeIntervalMinutes: 5,
}

function Fixture(props: { defaultValues: AdtyEpaySettingsDefaults }) {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
      })
  )
  return (
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <div ref={setActions} />
        <AdtyEpaySettingsSection defaultValues={props.defaultValues} />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('AdtyEpaySettingsSection', () => {
  it('accepts unpaidThreshold = 0 (do not auto-switch to maintenance) and saves it via PUT /api/option/', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    const user = userEvent.setup()

    render(<Fixture defaultValues={baseDefaults} />)

    const unpaidInput = screen.getByLabelText(
      'Unpaid orders in a row before maintenance'
    )
    await user.clear(unpaidInput)
    await user.type(unpaidInput, '0')

    expect(unpaidInput).toHaveAttribute('aria-invalid', 'false')
    const saveButton = screen.getByRole('button', { name: 'Save Changes' })
    expect(saveButton).toBeEnabled()

    await user.click(saveButton)
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith('/api/option/', {
      key: 'adty_epay_setting.unpaid_threshold',
      value: 0,
    })
  })
})
