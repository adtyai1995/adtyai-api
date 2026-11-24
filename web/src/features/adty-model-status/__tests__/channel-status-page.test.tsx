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
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { UptimeGroupResult } from '@/features/dashboard/types'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ModelStatusPage } from '../index'

const navigateMock = vi.fn()
const refetchMock = vi.fn()
let mockedGroups: UptimeGroupResult[] = []

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
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
    i18n: { language: 'en', resolvedLanguage: 'en' },
  }),
}))

vi.mock('@/features/adty-groups/hooks/use-group-display', () => ({
  useGroupDisplay: () => ({
    name: (id: string) => (id === 'gpt_plus' ? 'GPT Plus' : id),
    desc: () => '',
    adminLabel: (id: string) => id,
    groups: [],
  }),
}))

vi.mock('@/features/pricing/hooks/use-uptime-status', () => ({
  useUptimeStatus: () => ({
    groups: mockedGroups,
    isLoading: false,
    isFetching: false,
    isError: false,
    error: null,
    refetch: refetchMock,
  }),
}))

vi.mock('@/lib/lobe-icon', () => ({
  getLobeIcon: (name: string) => <span data-testid={`icon-${name}`} />,
}))

const SAMPLE_GROUPS: UptimeGroupResult[] = [
  {
    groupId: 'gpt_plus',
    categoryName: 'GPT Plus Raw',
    platform: 'openai',
    probeType: 'text',
    probeModel: 'gpt-5.4',
    groupRatio: 0.75,
    status: 1,
    statusLabel: 'up',
    uptime: 0.995,
    avgPingMs: 5530,
    cacheHitRate: 67.3,
    lastCheckedAt: '2026-11-25 03:08:52',
    heartbeats: [{ status: 1, time: '2026-11-25 03:08:52', ping: 5530 }],
  },
  {
    groupId: 'claude_pro',
    categoryName: 'Claude Pro',
    platform: 'anthropic',
    probeType: 'image',
    probeModel: 'claude-img-1',
    groupRatio: 0.25,
    status: 2,
    statusLabel: 'degraded',
    uptime: 0.91,
    avgPingMs: 420,
    cacheHitRate: null,
    lastCheckedAt: '2026-11-25 03:08:55',
    heartbeats: [{ status: 2, time: '2026-11-25 03:08:55', ping: 420 }],
  },
]

describe('ModelStatusPage (Channel Status redesign)', () => {
  beforeEach(() => {
    mockedGroups = SAMPLE_GROUPS
    navigateMock.mockReset()
    refetchMock.mockReset()
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'member',
      role: ROLE.USER,
    })
  })

  afterEach(() => {
    cleanup()
    useAuthStore.getState().auth.reset('complete')
  })

  it('renders channel cards, platform filters, KPIs, and navigates to /keys with group pre-selection', () => {
    render(<ModelStatusPage />)

    expect(screen.getByText('Channel Status')).toBeTruthy()
    expect(screen.getByText('Partial degradation')).toBeTruthy()
    expect(screen.getByText('GPT Plus')).toBeTruthy()
    expect(screen.getByText('Claude Pro')).toBeTruthy()
    expect(screen.getByText('5.53s')).toBeTruthy()
    expect(screen.getByText('420ms')).toBeTruthy()
    expect(screen.getByText('99.50%')).toBeTruthy()
    expect(screen.queryByText('67.30%')).toBeNull()
    expect(screen.queryByText('Cache hit rate')).toBeNull()
    expect(screen.queryByRole('button', { name: /Cache hit rate/i })).toBeNull()
    expect(screen.getByText('Multiplier x0.75')).toBeTruthy()
    expect(screen.getByText('Image')).toBeTruthy()

    // Filter to OpenAI only
    fireEvent.click(screen.getByRole('button', { name: /OpenAI/i }))
    expect(screen.getByText('GPT Plus')).toBeTruthy()
    expect(screen.queryByText('Claude Pro')).toBeNull()

    // Click Create API Key on GPT Plus card
    fireEvent.click(screen.getByRole('button', { name: /Create API Key/i }))
    expect(navigateMock).toHaveBeenCalledWith({
      to: '/keys',
      search: { create: '1', group: 'gpt_plus' },
    })
  })

  it('hides Create API Key button for pure site owners and site staff', () => {
    useAuthStore.getState().auth.setUser({
      id: 2,
      username: 'owner',
      role: ROLE.USER,
      adty_site_owner: true,
    })
    const { rerender } = render(<ModelStatusPage />)
    expect(screen.queryByRole('button', { name: /Create API Key/i })).toBeNull()

    useAuthStore.getState().auth.setUser({
      id: 3,
      username: 'staff',
      role: ROLE.USER,
      staff_site_id: 9,
    })
    rerender(<ModelStatusPage />)
    expect(screen.queryByRole('button', { name: /Create API Key/i })).toBeNull()
  })

  it('supports auto-refresh interval switching to Off and manual refresh', () => {
    render(<ModelStatusPage />)

    expect(screen.getByText('60s', { selector: 'span' })).toBeTruthy()

    fireEvent.change(screen.getByLabelText('Auto-refresh'), {
      target: { value: '0' },
    })
    expect(screen.queryByText('60s', { selector: 'span' })).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(refetchMock).toHaveBeenCalledTimes(1)
  })

  it('toggles sort direction when clicking the active sort pill', () => {
    render(<ModelStatusPage />)

    const ratioSort = screen.getByRole('button', { name: /Multiplier/i })
    expect(ratioSort.getAttribute('aria-pressed')).toBe('true')
    expect(ratioSort.textContent).toContain('Low to high')

    fireEvent.click(ratioSort)
    expect(ratioSort.textContent).toContain('High to low')

    const statusSort = screen.getByRole('button', { name: /Status/i })
    fireEvent.click(statusSort)
    expect(statusSort.getAttribute('aria-pressed')).toBe('true')
    expect(statusSort.textContent).toContain('Healthy first')

    fireEvent.click(statusSort)
    expect(statusSort.textContent).toContain('Issues first')
  })
})
