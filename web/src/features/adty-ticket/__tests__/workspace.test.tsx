// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { TicketCenter } from '../ticket-center'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/lib/api', () => ({ api }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (select: (state: unknown) => unknown) =>
    select({ auth: { user: { id: 7, role: 10 } } }),
}))

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
  window.history.replaceState(null, '', '/')
})

function mount(scope: 'platform' | 'personal') {
  Element.prototype.scrollIntoView = vi.fn()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  api.get.mockImplementation(async (path: string) => {
    if (path === '/api/adty/tickets/request-audience') {
      return { data: { success: true, data: { audience: 'team' } } }
    }
    if (path === '/api/adty/tickets') {
      return {
        data: {
          success: true,
          data: {
            tickets: [],
            total: 0,
            can_manage: true,
            can_view_team: true,
          },
        },
      }
    }
    const id = Number(path.split('/').pop())
    return {
      data: {
        success: true,
        data: {
          ticket: { id, title: `Case ${id}`, status: 'new', type: 'api' },
          actions: ['reply','note'],
          messages: [],
          events: [],
          can_manage: true,
          can_note: true,
        },
      },
    }
  })
  render(
    <QueryClientProvider client={client}>
      <TicketCenter scope={scope} />
    </QueryClientProvider>
  )
  return client
}

function navigate(hash: string) {
  act(() => {
    window.history.replaceState(null, '', hash)
    window.dispatchEvent(new HashChangeEvent('hashchange'))
  })
}

it('isolates reply and note drafts when notifications switch cases', async () => {
  window.history.replaceState(null, '', '#case-1')
  const client = mount('platform')
  await screen.findByText('Case 1')
  fireEvent.change(screen.getByRole('textbox', { name: 'Reply' }), {
    target: { value: 'Reply for A' },
  })
  fireEvent.change(screen.getByRole('textbox', { name: 'Internal note' }), {
    target: { value: 'Internal note for A' },
  })
  navigate('#case-2')
  await screen.findByText('Case 2')
  for (const name of ['Reply', 'Internal note']) {
    expect(
      (screen.getByRole('textbox', { name }) as HTMLTextAreaElement).value
    ).toBe('')
    expect(
      (screen.getByRole('button', { name }) as HTMLButtonElement).disabled
    ).toBe(true)
  }
  navigate('#case-1')
  await screen.findByText('Case 1')
  expect(
    (screen.getByRole('textbox', { name: 'Reply' }) as HTMLTextAreaElement)
      .value
  ).toBe('Reply for A')
  expect(
    (
      screen.getByRole('textbox', {
        name: 'Internal note',
      }) as HTMLTextAreaElement
    ).value
  ).toBe('Internal note for A')
  expect(api.post).not.toHaveBeenCalled()
  client.clear()
})

it('uses the authorized request payer in the new-ticket form and submission', async () => {
  window.history.replaceState(null, '', '#ticket:request:request-123')
  const client = mount('personal')
  const audience = (await screen.findByLabelText(
    'Ticket audience'
  )) as HTMLSelectElement
  await waitFor(() => expect(audience.value).toBe('team'))
  expect(audience.disabled).toBe(true)
  fireEvent.change(screen.getByLabelText('Title'), {
    target: { value: 'Team request' },
  })
  fireEvent.change(screen.getByLabelText('Description'), {
    target: { value: 'Question' },
  })
  api.post.mockResolvedValue({ data: { success: true, data: { id: 5 } } })
  fireEvent.click(screen.getByRole('button', { name: 'New ticket' }))
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/adty/tickets',
      expect.objectContaining({
        audience: 'team',
        related_kind: 'request',
        related_ref: 'request-123',
      }),
      expect.anything()
    )
  )
  client.clear()
})
