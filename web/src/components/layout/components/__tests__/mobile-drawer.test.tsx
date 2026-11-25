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
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ROLE } from '@/lib/roles'
import type { AuthUser } from '@/stores/auth-store'

import { MobileDrawer } from '../mobile-drawer'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: 'en', resolvedLanguage: 'en' },
  }),
}))

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    children,
    to,
    ...rest
  }: {
    children?: ReactNode
    to?: string
    className?: string
    onClick?: () => void
  }) => (
    <a href={to ?? '#'} {...rest}>
      {children}
    </a>
  ),
}))

vi.mock('motion/react', () => ({
  AnimatePresence: ({ children }: { children: ReactNode }) => children,
  motion: {
    div: ({
      children,
      className,
    }: {
      children?: ReactNode
      className?: string
    }) => <div className={className}>{children}</div>,
  },
}))

vi.mock('@/hooks/use-sidebar-config', () => ({
  useIsSidebarModuleVisible: () => true,
}))

vi.mock('@/components/sign-out-dialog', () => ({
  SignOutDialog: () => null,
}))

function renderDrawer(user: AuthUser) {
  return render(
    <MobileDrawer
      isOpen
      onClose={vi.fn()}
      homeUrl='/'
      displayLogo={<span>Logo</span>}
      displaySiteName='ADTY AI'
      loading={false}
      logoLoaded
      mobileLinksList={[]}
      showAuthButtons
      user={user}
    />
  )
}

describe('MobileDrawer user profile isolation', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('hides Profile, Wallet, and user.group for pure site owners while keeping Security & Access', () => {
    renderDrawer({
      id: 11,
      username: 'siteowner',
      display_name: 'Site Owner',
      role: ROLE.USER,
      adty_site_owner: true,
      group: 'default',
    })

    expect(screen.queryByRole('link', { name: 'Profile' })).toBeNull()
    expect(screen.queryByRole('link', { name: 'Wallet' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Security & Access' })).toBeTruthy()
    expect(screen.queryByText('default')).toBeNull()
    expect(screen.queryByText('·')).toBeNull()
  })

  it('hides Profile, Wallet, Security & Access, and user.group for site staff', () => {
    renderDrawer({
      id: 12,
      username: 'staff1',
      display_name: 'Staff One',
      role: ROLE.USER,
      staff_site_id: 7,
      group: 'default',
    })

    expect(screen.queryByRole('link', { name: 'Profile' })).toBeNull()
    expect(screen.queryByRole('link', { name: 'Wallet' })).toBeNull()
    expect(screen.queryByRole('link', { name: 'Security & Access' })).toBeNull()
    expect(screen.queryByText('default')).toBeNull()
  })

  it('shows Profile and Wallet for ordinary customers without rendering user.group', () => {
    renderDrawer({
      id: 13,
      username: 'customer1',
      display_name: 'Customer One',
      role: ROLE.USER,
      group: 'legacy-tier',
    })

    expect(screen.getByRole('link', { name: 'Profile' })).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Wallet' })).toBeTruthy()
    expect(screen.queryByText('legacy-tier')).toBeNull()
    expect(screen.queryByText('·')).toBeNull()
  })
})
