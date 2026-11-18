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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AnnouncementsSection } from '@/features/system-settings/content/announcements-section'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { notificationDraftSchema, notificationSchema } from '../lib/schema'

let client: QueryClient

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
})

afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

describe('notificationDraftSchema and NotificationEditor draft saving', () => {
  it('allows blank title and content in notificationDraftSchema while rejecting them in notificationSchema', () => {
    const blank = {
      title: '   ',
      content: '',
      kind: 'platform' as const,
      audience: 'all_users' as const,
      target_site_id: 0,
      business: '',
      sync_telegram: false,
      telegram_channel: '',
    }
    expect(notificationDraftSchema.safeParse(blank).success).toBe(true)
    expect(notificationSchema.safeParse(blank).success).toBe(false)

    // Still enforces max length and audience/telegram constraints on drafts
    expect(
      notificationDraftSchema.safeParse({
        ...blank,
        business: 'x'.repeat(201),
      }).success
    ).toBe(false)
    expect(
      notificationDraftSchema.safeParse({
        ...blank,
        audience: 'site',
        target_site_id: 0,
      }).success
    ).toBe(false)
    expect(
      notificationDraftSchema.safeParse({
        ...blank,
        sync_telegram: true,
        telegram_channel: '',
      }).success
    ).toBe(false)
  })

  it('saves a draft with blank title and content via Save as draft button', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true, data: { id: 10 } } })

    render(
      <QueryClientProvider client={client}>
        <AnnouncementsSection enabled={false} data='[]' />
      </QueryClientProvider>
    )

    await user.click(screen.getByRole('button', { name: 'New notification' }))
    const dialog = screen.getByRole('dialog', { name: 'New notification' })

    await user.click(
      within(dialog).getByRole('button', { name: 'Save as draft' })
    )

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/adty/admin/notifications',
        expect.objectContaining({
          title: '',
          content: '',
          draft: true,
        })
      )
    )
  })

  it('displays FieldError for business and audience constraints when saving a draft', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const post = vi.spyOn(api, 'post')

    render(
      <QueryClientProvider client={client}>
        <AnnouncementsSection enabled={false} data='[]' />
      </QueryClientProvider>
    )

    await user.click(screen.getByRole('button', { name: 'New notification' }))
    const dialog = screen.getByRole('dialog', { name: 'New notification' })

    const businessInput = within(dialog).getByLabelText('Related business')
    fireEvent.change(businessInput, { target: { value: 'b'.repeat(201) } })
    await user.selectOptions(within(dialog).getByLabelText('Recipients'), 'site')

    await user.click(
      within(dialog).getByRole('button', { name: 'Save as draft' })
    )

    expect(
      await within(dialog).findByText('Invalid parameters', {
        selector: '[role=alert]',
      })
    ).toBeVisible()
    expect(
      await within(dialog).findByText('Select a site', {
        selector: '[role=alert]',
      })
    ).toBeVisible()
    expect(post).not.toHaveBeenCalled()
  })
})
