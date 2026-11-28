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

import { resolveDocsLink } from '../use-docs-link'
import { useTopNavLinks, type TopNavLink } from '../use-top-nav-links'

let mockStatus: Record<string, unknown> = {}

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: mockStatus }),
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
    mockStatus = {}
    delete document.documentElement.dataset.adtySiteId
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

  it('resolves docs link across external override, mounted static /docs/, and SPA fallback', () => {
    expect(resolveDocsLink({})).toEqual({
      href: '/docs',
      external: false,
      reloadDocument: false,
    })

    expect(resolveDocsLink({ docs_static_mounted: true })).toEqual({
      href: '/docs/',
      external: false,
      reloadDocument: true,
    })

    expect(
      resolveDocsLink({
        docs_link: 'https://help.sub.example/guide',
        docs_static_mounted: true,
      })
    ).toEqual({
      href: 'https://help.sub.example/guide',
      external: true,
      reloadDocument: false,
    })

    expect(
      resolveDocsLink({
        docs_link: 'javascript:alert(1)',
        docs_static_mounted: true,
      })
    ).toEqual({
      href: '/docs/',
      external: false,
      reloadDocument: true,
    })

    mockStatus = { docs_static_mounted: true }
    const mountedLinks = collectLinks()
    expect(mountedLinks.find((link) => link.id === 'docs')).toEqual({
      id: 'docs',
      title: 'Docs',
      href: '/docs/',
      reloadDocument: true,
    })

    cleanup()
    mockStatus = {
      docs_link: 'https://docs.sub.example',
      docs_static_mounted: true,
    }
    const externalLinks = collectLinks()
    expect(externalLinks.find((link) => link.id === 'docs')).toEqual({
      id: 'docs',
      title: 'Docs',
      href: 'https://docs.sub.example',
      external: true,
    })
  })

  it('shows Become a Partner only on the main site for non-staff users unless disabled', () => {
    const mainLinks = collectLinks()
    expect(mainLinks.find((link) => link.id === 'partner')).toEqual({
      id: 'partner',
      title: 'Become a Partner',
      href: '/partner',
    })

    cleanup()
    mockStatus = { adty_site: { id: 1, name: 'Subsite' } }
    expect(collectLinks().find((link) => link.id === 'partner')).toBeUndefined()

    cleanup()
    mockStatus = {}
    document.documentElement.dataset.adtySiteId = '42'
    expect(collectLinks().find((link) => link.id === 'partner')).toBeUndefined()
    delete document.documentElement.dataset.adtySiteId

    cleanup()
    useAuthStore.getState().auth.setUser({
      id: 9,
      username: 'staff',
      role: ROLE.USER,
      staff_site_id: 5,
    })
    expect(collectLinks().find((link) => link.id === 'partner')).toBeUndefined()

    cleanup()
    useAuthStore.getState().auth.reset('complete')
    mockStatus = { HeaderNavModules: JSON.stringify({ partner: false }) }
    expect(collectLinks().find((link) => link.id === 'partner')).toBeUndefined()
  })
})
