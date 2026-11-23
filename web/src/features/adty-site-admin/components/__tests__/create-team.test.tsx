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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { CreateTeamForm } from '../create-team-form'

const api = vi.hoisted(() => ({
  get: vi.fn<(url: string) => Promise<unknown>>(),
  post: vi.fn(),
}))
vi.mock('@/lib/api', () => ({ api }))
afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

it('creates a main-site team with its existing leader and supply discount', async () => {
  api.get.mockImplementation(async (url) => ({
    data: {
      success: true,
      data: {
        items: url.includes('site_id=0')
          ? [{ id: 9, username: 'Leader A' }]
          : [],
        total: 1,
      },
    },
  }))
  api.post.mockResolvedValue({
    data: { success: true, data: { Team: { id: 23, name: 'Finance' } } },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <CreateTeamForm />
    </QueryClientProvider>
  )
  expect(screen.queryByLabelText('Site')).toBeNull()
  await screen.findByRole('option', { name: /Leader A/ })
  fireEvent.change(screen.getByLabelText('Leader'), { target: { value: '9' } })
  const nameInput = screen.getByLabelText('Team name')
  fireEvent.change(nameInput, { target: { value: '𠮷'.repeat(65) } })
  expect(screen.getByRole('button', { name: 'Create team' })).toBeDisabled()
  expect(screen.getByRole('alert')).toHaveTextContent('Team name')
  fireEvent.change(nameInput, { target: { value: '𠮷'.repeat(64) } })
  expect(screen.getByRole('button', { name: 'Create team' })).toBeEnabled()
  fireEvent.change(nameInput, {
    target: { value: 'Finance' },
  })
  const discountInput = screen.getByLabelText('Team supply discount')
  fireEvent.change(discountInput, { target: { value: '1.2' } })
  expect(screen.getByRole('button', { name: 'Create team' })).toBeDisabled()
  fireEvent.change(discountInput, { target: { value: '0.85' } })
  expect(screen.getByRole('button', { name: 'Create team' })).toBeEnabled()
  fireEvent.click(screen.getByRole('button', { name: 'Create team' }))
  expect((await screen.findByRole('status')).textContent).toContain(
    'Finance (#23)'
  )
  expect(api.post).toHaveBeenCalledWith(
    '/api/adty/admin/teams',
    { site_id: 0, leader_id: 9, name: 'Finance', discount: '0.85' },
    { headers: { 'Idempotency-Key': expect.any(String) } }
  )
  client.clear()
})
