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
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { ProfileDropdown } from '../profile-dropdown'

const navigateMock = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: 'en', resolvedLanguage: 'en' },
  }),
}))

vi.mock('@/hooks/use-sidebar-config', () => ({
  useIsSidebarModuleVisible: () => true,
}))

vi.mock('@/components/sign-out-dialog', () => ({
  SignOutDialog: () => null,
}))

vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  DropdownMenuTrigger: ({ children }: { children: ReactNode }) => (
    <button type='button'>{children}</button>
  ),
  DropdownMenuContent: ({ children }: { children: ReactNode }) => (
    <div role='menu'>{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
  }: {
    children: ReactNode
    onClick?: () => void
  }) => (
    <button type='button' role='menuitem' onClick={onClick}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
}))

function setAuthUser(overrides: Partial<AuthUser>) {
  useAuthStore.getState().auth.setUser({
    id: 10,
    username: 'owner1',
    display_name: 'Owner One',
    role: ROLE.USER,
    group: 'legacy-group',
    ...overrides,
  })
}

describe('ProfileDropdown', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
    useAuthStore.getState().auth.reset('complete')
  })

  it('hides Profile, Wallet, and user.group for pure site owners while keeping Security & Access', () => {
    setAuthUser({
      role: ROLE.USER,
      adty_site_owner: true,
      group: 'default',
    })

    render(<ProfileDropdown />)

    expect(screen.queryByRole('menuitem', { name: 'Profile' })).toBeNull()
    expect(screen.queryByRole('menuitem', { name: 'Wallet' })).toBeNull()
    expect(
      screen.getByRole('menuitem', { name: 'Security & Access' })
    ).toBeTruthy()
    expect(screen.queryByText('default')).toBeNull()
    expect(screen.queryByText('·')).toBeNull()
  })

  it('shows Profile and Wallet for ordinary users and never renders user.group', () => {
    setAuthUser({
      role: ROLE.USER,
      adty_site_owner: false,
      group: 'vip-group',
    })

    render(<ProfileDropdown />)

    expect(screen.getByRole('menuitem', { name: 'Profile' })).toBeTruthy()
    expect(screen.getByRole('menuitem', { name: 'Wallet' })).toBeTruthy()
    expect(screen.queryByText('vip-group')).toBeNull()
    expect(screen.queryByText('·')).toBeNull()
  })

  it('keeps Profile and Wallet visible for admins who also own a site', () => {
    setAuthUser({
      role: ROLE.ADMIN,
      adty_site_owner: true,
      group: 'default',
    })

    render(<ProfileDropdown />)

    expect(screen.getByRole('menuitem', { name: 'Profile' })).toBeTruthy()
    expect(screen.getByRole('menuitem', { name: 'Wallet' })).toBeTruthy()
    expect(screen.queryByText('default')).toBeNull()
  })
})
