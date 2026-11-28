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
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { LandingSections } from '..'
import type { LandingData } from '../hooks/use-landing-data'
import { MODEL_VENDORS } from '../model-vendors'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      values
        ? key.replaceAll(/\{\{(\w+)\}\}/g, (_, name) => String(values[name]))
        : key,
    i18n: { language: 'en', resolvedLanguage: 'en' },
  }),
}))

const CLOSED: LandingData = { affiliate: { open: false, rate: null } }

function renderLanding(baseUrl: string, data = CLOSED) {
  return render(
    <LandingSections signedIn={false} baseUrl={baseUrl} data={data} />
  )
}

function endpointTable() {
  const row = screen
    .getByText('Anthropic Messages', { selector: 'p' })
    .closest('ul')
  if (!row) throw new Error('endpoint table not rendered')
  return within(row)
}

describe('adty home landing sections', () => {
  afterEach(() => {
    cleanup()
  })

  it('shows the base URL placeholder in the endpoint table when the site address is empty', () => {
    renderLanding('')

    const table = endpointTable()
    expect(table.getAllByText('<BASE_URL>/v1')).toHaveLength(2)
    expect(table.getAllByText('<BASE_URL>')).toHaveLength(2)
    expect(table.getByText('no /v1')).toBeTruthy()
  })

  it('uses the site API address for every endpoint once it is known', () => {
    renderLanding('https://api.example.com')

    const table = endpointTable()
    expect(table.getAllByText('https://api.example.com/v1')).toHaveLength(2)
    expect(table.getAllByText('https://api.example.com')).toHaveLength(2)
    expect(table.queryByText(/<BASE_URL>/)).toBeNull()
  })

  it('names every vendor of the logo wall once for assistive technology', () => {
    renderLanding('https://api.example.com')

    const names = MODEL_VENDORS.map((vendor) => vendor.en)
    for (const name of names) {
      expect(screen.getAllByText(name, { selector: 'li' })).toHaveLength(1)
    }
  })

  it('hides the referral card while the program is off', () => {
    renderLanding('https://api.example.com')

    expect(
      screen.queryByText('Invite friends, earn on their consumption')
    ).toBeNull()
  })

  it('shows the default referral rate when the program is open', () => {
    renderLanding('https://api.example.com', {
      affiliate: { open: true, rate: 12 },
    })

    expect(
      screen.getByText('Invite friends, earn on their consumption')
    ).toBeTruthy()
    expect(
      screen.getByText(
        'Earn 12% of the paid consumption from people you invite, settled hourly with no freeze.'
      )
    ).toBeTruthy()
  })
})
