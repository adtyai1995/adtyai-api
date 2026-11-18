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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { TicketCenter } from '../ticket-center'

let client: QueryClient

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})

function mount() {
  return render(
    <QueryClientProvider client={client}>
      <TicketCenter />
    </QueryClientProvider>
  )
}

describe('TicketCenter detail images, events, and mutation error handling', () => {
  it('renders authorized images with private endpoint links and handling events from detail (ISS-R1-0134)', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (url === '/api/adty/tickets') {
        return {
          data: {
            success: true,
            data: {
              tickets: [{ id: 42, title: 'Payment screenshot', status: 'in_progress', assignee_id: 7 }],
              can_note: false,
              can_manage: false,
            },
          },
        }
      }
      if (url === '/api/adty/tickets/42') {
        return {
          data: {
            success: true,
            data: {
              ticket: { id: 42, title: 'Payment screenshot', status: 'in_progress', assignee_id: 7 },
              messages: [{ id: 1, visibility: 'public', body: 'Attached my receipt' }],
              images: [
                {
                  id: 9,
                  object_id: '0123456789abcdef0123456789abcdef',
                  visibility: 'public',
                  mime: 'image/png',
                  width: 100,
                  height: 80,
                },
                {
                  id: 10,
                  object_id: 'fedcba9876543210fedcba9876543210',
                  visibility: 'internal',
                  mime: 'image/png',
                },
              ],
              events: [
                { id: 101, action: 'create', detail: 'topup' },
                { id: 102, action: 'status', detail: 'in_progress' },
              ],
              can_note: false,
              can_manage: false,
            },
          },
        }
      }
      return { data: { success: true, data: {} } }
    })

    mount()
    await user.click(await screen.findByRole('button', { name: /Payment screenshot/i }))

    const link = await screen.findByRole('link', {
      name: /Image 0123456789abcdef0123456789abcdef/i,
    })
    expect(link).toHaveAttribute('href', '/api/adty/tickets/42/images/9')

    const img = screen.getByRole('img', {
      name: /Image 0123456789abcdef0123456789abcdef/i,
    })
    expect(img).toHaveAttribute('src', '/api/adty/tickets/42/images/9')

    // Internal image is not rendered when can_note is false.
    expect(
      screen.queryByRole('link', { name: /fedcba9876543210fedcba9876543210/i })
    ).not.toBeInTheDocument()

    // Handling events from detail are rendered.
    expect(screen.getByText(/Event: create · topup/i)).toBeInTheDocument()
    expect(screen.getByText(/Event: status · Ticket status in progress \(in_progress\)/i)).toBeInTheDocument()
  })

  it('retains reply and internal note drafts when the server returns success:false and clears only after success (ISS-R1-0135)', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (url === '/api/adty/tickets') {
        return {
          data: {
            success: true,
            data: {
              tickets: [{ id: 11, title: 'Quota question', status: 'in_progress', assignee_id: 3 }],
              can_note: true,
              can_manage: true,
            },
          },
        }
      }
      if (url === '/api/adty/tickets/11') {
        return {
          data: {
            success: true,
            data: {
              ticket: { id: 11, title: 'Quota question', status: 'in_progress', assignee_id: 3 },
              messages: [{ id: 1, visibility: 'public', body: 'Initial body' }],
              images: [],
              events: [],
              can_note: true,
              can_manage: true,
            },
          },
        }
      }
      return { data: { success: true, data: {} } }
    })

    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValueOnce({
        data: { success: false, message: 'No permission to perform this action' },
      })
      .mockResolvedValueOnce({
        data: { success: true, data: { id: 2, visibility: 'public', body: 'My public reply' } },
      })
      .mockResolvedValueOnce({
        data: { success: false, message: 'Note save failed' },
      })
      .mockResolvedValueOnce({
        data: { success: true, data: { id: 3, visibility: 'internal', body: 'Staff internal note' } },
      })
      .mockResolvedValueOnce({
        data: { success: false, message: 'Only images are accepted' },
      })

    mount()
    await user.click(await screen.findByRole('button', { name: /Quota question/i }))

    const replyBox = await screen.findByRole('textbox', { name: 'Reply' })
    await user.type(replyBox, 'My public reply')
    await user.click(screen.getByRole('button', { name: 'Reply' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(await screen.findByText('No permission to perform this action')).toBeInTheDocument()
    expect(replyBox).toHaveValue('My public reply')

    // Retry reply when server returns success:true -> draft clears.
    await user.click(screen.getByRole('button', { name: 'Reply' }))
    await waitFor(() => expect(replyBox).toHaveValue(''))

    // Internal note fails with success:false -> draft stays intact and error shows.
    const noteBox = screen.getByRole('textbox', { name: 'Internal note' })
    await user.type(noteBox, 'Staff internal note')
    await user.click(screen.getByRole('button', { name: 'Internal note' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(3))
    expect(await screen.findByText('Note save failed')).toBeInTheDocument()
    expect(noteBox).toHaveValue('Staff internal note')

    // Retry note when server returns success:true -> draft clears.
    await user.click(screen.getByRole('button', { name: 'Internal note' }))
    await waitFor(() => expect(noteBox).toHaveValue(''))

    // Image upload returning HTTP 200 with success:false reports error.
    const fileInput = screen.getByLabelText('Image')
    const file = new File(['fake'], 'bad.png', { type: 'image/png' })
    await user.upload(fileInput, file)
    expect(await screen.findByText('Only images are accepted')).toBeInTheDocument()
  })
})
