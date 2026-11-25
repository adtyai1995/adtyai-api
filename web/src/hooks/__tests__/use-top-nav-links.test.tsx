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
import { cleanup, render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useTopNavLinks, type TopNavLink } from '../use-top-nav-links'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: {} }),
}))

function collectLinks(): TopNavLink[] {
  let links: TopNavLink[] = []
  function Probe() {
    links = useTopNavLinks()
    return null
  }
  render(<Probe />)
  return links
}

describe('useTopNavLinks', () => {
  afterEach(() => {
    cleanup()
    useAuthStore.getState().auth.reset('complete')
  })

  it('labels the /model-status top nav link as Channel Status', () => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'alice',
      role: ROLE.USER,
    })

    const links = collectLinks()
    const statusLink = links.find((link) => link.id === 'model-status')
    expect(statusLink).toEqual({
      id: 'model-status',
      title: 'Channel Status',
      href: '/model-status',
    })
  })
})
