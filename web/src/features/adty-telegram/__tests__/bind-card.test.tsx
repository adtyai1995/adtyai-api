import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { TelegramBindCard } from '../bind-card'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it.each(['user', 'admin'] as const)('unbinds the %s bot and allows retry after a failed request', async (purpose) => {
  let removed = false
  const base = purpose === 'admin' ? '/api/adty/site/telegram' : '/api/adty/telegram/me'
  vi.spyOn(api, 'get').mockImplementation(async () => ({ data: { success: true, data: {
    purpose, status: removed ? 'revoked' : 'active', telegram_user_id: removed ? 0 : 997,
  } } }))
  const unbind = vi.spyOn(api, 'delete').mockResolvedValueOnce({ data: { success: false, message: 'Please retry unbinding' } }).mockImplementation(async () => {
    removed = true
    return { data: { success: true, data: { status: 'revoked' } } }
  })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const user = userEvent.setup()
  render(<QueryClientProvider client={client}><TelegramBindCard purpose={purpose} /></QueryClientProvider>)
  const button = await screen.findByRole('button', { name: 'Unbind Telegram' })
  await user.click(button)
  expect(await screen.findByText('Please retry unbinding')).toBeVisible()
  expect(screen.getByText('Bound')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Unbind Telegram' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Start binding' })).toBeVisible())
  expect(unbind).toHaveBeenNthCalledWith(1, base)
  expect(unbind).toHaveBeenNthCalledWith(2, base)
  expect(screen.queryByText('Please retry unbinding')).not.toBeInTheDocument()
  client.clear()
})
