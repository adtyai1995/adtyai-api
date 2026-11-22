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

it('creates a team with the selected site and its existing leader', async () => {
  api.get.mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/user/sites'
          ? [{ site_id: 41, site_name: 'Subsite A' }]
          : {
              items: url.includes('site_id=41')
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
  await screen.findByRole('option', { name: /Subsite A/ })
  fireEvent.change(screen.getByLabelText('Site'), { target: { value: '41' } })
  await screen.findByRole('option', { name: /Leader A/ })
  fireEvent.change(screen.getByLabelText('Leader'), { target: { value: '9' } })
  const nameInput=screen.getByLabelText('Team name')
  fireEvent.change(nameInput,{target:{value:'𠮷'.repeat(65)}})
  expect(screen.getByRole('button',{name:'Create team'})).toBeDisabled()
  expect(screen.getByRole('alert')).toHaveTextContent('Team name')
  fireEvent.change(nameInput,{target:{value:'𠮷'.repeat(64)}})
  expect(screen.getByRole('button',{name:'Create team'})).toBeEnabled()
  fireEvent.change(nameInput, {
    target: { value: 'Finance' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Create team' }))
  expect((await screen.findByRole('status')).textContent).toContain(
    'Finance (#23)'
  )
  expect(api.post).toHaveBeenCalledWith(
    '/api/adty/admin/teams',
    { site_id: 41, leader_id: 9, name: 'Finance' },
    { headers: { 'Idempotency-Key': expect.any(String) } }
  )
  client.clear()
})
