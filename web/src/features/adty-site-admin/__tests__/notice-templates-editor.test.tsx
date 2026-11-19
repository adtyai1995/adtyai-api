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
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { NoticeTemplatesEditor } from '../components/notice-templates-editor'

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.setUser(null)
  vi.restoreAllMocks()
})

function renderEditor() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <NoticeTemplatesEditor />
    </QueryClientProvider>
  )
}

const saved = {
  id: 3,
  key: 'channel_disabled',
  category: 'channel',
  title_zh: '通道已禁用',
  content_zh: '通道「{{1}}」已被禁用',
  title_en: 'Channel disabled',
  content_en: 'Channel "{{1}}" has been disabled',
  revision: 2,
  updated_at: 10,
}

it('does not load templates for a non-root account', () => {
  useAuthStore.getState().auth.setUser({ id: 2, username: 'admin', role: 10 })
  const get = vi.spyOn(api, 'get')
  renderEditor()
  expect(
    screen.getByText('Only Root can edit fixed notice templates.')
  ).toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})

it('saves the four bilingual fields and refuses an empty draft', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [saved] },
  })
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, data: { ...saved, revision: 3 } },
  })
  const user = userEvent.setup()
  renderEditor()

  expect(await screen.findByText('channel_disabled')).toBeInTheDocument()
  const save = screen.getByRole('button', { name: 'Save template' })
  expect(save).toBeDisabled()

  await user.clear(screen.getByLabelText('Chinese title'))
  expect(screen.getByRole('button', { name: 'Save template' })).toBeDisabled()
  expect(put).not.toHaveBeenCalled()

  await user.type(screen.getByLabelText('Chinese title'), '通道停用')
  await user.click(screen.getByRole('button', { name: 'Save template' }))

  expect(put).toHaveBeenCalledWith(
    '/api/adty/admin/notice-templates/channel_disabled',
    {
      title_zh: '通道停用',
      content_zh: '通道「{{1}}」已被禁用',
      title_en: 'Channel disabled',
      content_en: 'Channel "{{1}}" has been disabled',
    }
  )
})

it('keeps the draft when the server rejects mismatched placeholders', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [saved] },
  })
  vi.spyOn(api, 'put').mockResolvedValue({
    data: {
      success: false,
      message: 'Chinese and English template placeholders do not match',
      message_key: 'Chinese and English template placeholders do not match',
    },
  })
  const user = userEvent.setup()
  renderEditor()
  await screen.findByText('channel_disabled')
  await user.type(screen.getByLabelText('English body'), ' extra')
  await user.click(screen.getByRole('button', { name: 'Save template' }))
  expect(screen.getByLabelText('English body')).toHaveValue(
    'Channel "{{1}}" has been disabled extra'
  )
  expect(screen.queryByText('Saved successfully')).not.toBeInTheDocument()
})
