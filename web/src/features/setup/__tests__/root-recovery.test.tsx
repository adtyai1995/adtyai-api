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
import { cleanup, render, screen } from '@testing-library/react'
import { useForm } from 'react-hook-form'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Form } from '@/components/ui/form'

import { buildSetupPayload } from '../api'
import { AdminStep } from '../components/admin-step'
import type { SetupFormValues } from '../types'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

function AdminStepHarness(props: { rootInitialized: boolean }) {
  const form = useForm<SetupFormValues>({
    defaultValues: {
      username: '',
      password: '',
      confirmPassword: '',
      usageMode: 'external',
    },
  })
  return (
    <Form {...form}>
      <AdminStep form={form} rootInitialized={props.rootInitialized} />
    </Form>
  )
}

describe('Setup wizard root_init recovery (R1-04)', () => {
  afterEach(() => {
    cleanup()
  })

  it('shows existing Root username and password inputs when rootInitialized is true', () => {
    render(<AdminStepHarness rootInitialized />)

    expect(
      screen.getByText(
        'The administrator account is already initialized. Enter the existing Root username and password to verify your identity and finish setup.'
      )
    ).toBeTruthy()
    expect(screen.getByLabelText('Administrator username')).toBeTruthy()
    expect(screen.getByLabelText('Password')).toBeTruthy()
    expect(screen.queryByLabelText('Confirm password')).toBeNull()
  })

  it('sends trimmed username, password, and matching confirmPassword when rootInitialized is true', () => {
    const payload = buildSetupPayload(
      {
        username: '  rootadmin  ',
        password: 'existing-secret-password',
        confirmPassword: '',
        usageMode: 'self',
      },
      true
    )

    expect(payload).toEqual({
      username: 'rootadmin',
      password: 'existing-secret-password',
      confirmPassword: 'existing-secret-password',
      SelfUseModeEnabled: true,
      DemoSiteEnabled: false,
    })
  })
})
