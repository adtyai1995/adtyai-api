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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { hashTicketImageData, sha256HexFallback } from '../ticket-hash'
import { TicketImage } from '../ticket-image'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
  },
}))

vi.mock('@/components/ui/dialog', () => ({
  Dialog: ({
    open,
    children,
  }: {
    open?: boolean
    children?: React.ReactNode
  }) => (open ? <div role='dialog'>{children}</div> : null),
  DialogContent: ({ children }: { children?: React.ReactNode }) => (
    <div>{children}</div>
  ),
  DialogHeader: ({ children }: { children?: React.ReactNode }) => (
    <div>{children}</div>
  ),
  DialogTitle: ({ children }: { children?: React.ReactNode }) => (
    <h2>{children}</h2>
  ),
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, string>) => {
      if (!params) return key
      return Object.entries(params).reduce(
        (acc, [k, v]) => acc.replaceAll(`{{${k}}}`, v),
        key
      )
    },
  }),
}))

describe('ticket-hash', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('computes standard FIPS 180-4 SHA-256 digests in pure JS fallback', () => {
    expect(sha256HexFallback('')).toBe(
      'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
    )
    expect(sha256HexFallback('abc')).toBe(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'
    )
  })

  it('falls back to pure JS SHA-256 when window.isSecureContext is false', async () => {
    const originalSecureContext = Object.getOwnPropertyDescriptor(
      window,
      'isSecureContext'
    )
    Object.defineProperty(window, 'isSecureContext', {
      configurable: true,
      value: false,
    })
    try {
      const digest = await hashTicketImageData('abc')
      expect(digest).toBe(
        'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'
      )
    } finally {
      if (originalSecureContext) {
        Object.defineProperty(window, 'isSecureContext', originalSecureContext)
      }
    }
  })
})

describe('TicketImage', () => {
  const createObjectURL = vi.fn(() => 'blob:mock-ticket-image')
  const revokeObjectURL = vi.fn()

  beforeEach(() => {
    vi.stubGlobal('URL', {
      ...URL,
      createObjectURL,
      revokeObjectURL,
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    createObjectURL.mockClear()
    revokeObjectURL.mockClear()
  })

  it('retries with a fresh grant when initial prop grant returns non-image JSON and normalizes MIME with charset', async () => {
    const expiredBlob = new Blob(
      [JSON.stringify({ success: false, message: 'Expired grant' })],
      { type: 'application/json; charset=utf-8' }
    )
    const validPng = new Blob(['png-bytes'], {
      type: 'IMAGE/PNG; charset=binary',
    })

    const getSpy = vi.spyOn(api, 'get').mockImplementation((_url, config) => {
      const params = config?.params as { action?: string } | undefined
      const grantHeader = (config?.headers as Record<string, string> | undefined)?.[
        'X-Ticket-Image-Grant'
      ]
      if (params?.action === 'grant') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              grant: 'fresh-grant',
              expires_at: Math.floor(Date.now() / 1000) + 120,
            },
          },
          headers: {},
        })
      }
      if (grantHeader === 'stale-grant') {
        return Promise.resolve({
          data: expiredBlob,
          headers: { 'content-type': 'application/json; charset=utf-8' },
        })
      }
      if (grantHeader === 'fresh-grant') {
        return Promise.resolve({
          data: validPng,
          headers: { 'content-type': 'IMAGE/PNG; charset=binary' },
        })
      }
      return Promise.reject(new Error('unexpected call'))
    })

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={queryClient}>
        <TicketImage
          scope='platform'
          userId={1}
          ticketID={42}
          imageID={7}
          grant='stale-grant'
          expiresAt={Math.floor(Date.now() / 1000) + 60}
          label='Image #7'
        />
      </QueryClientProvider>
    )

    const img = await screen.findByAltText('Image #7')
    expect(img.getAttribute('src')).toBe('blob:mock-ticket-image')
    expect(getSpy).toHaveBeenCalledTimes(3)

    // Invalidating adty-tickets must NOT invalidate or refetch adty-ticket-image
    await queryClient.invalidateQueries({ queryKey: ['adty-tickets'] })
    await waitFor(() => {
      expect(getSpy).toHaveBeenCalledTimes(3)
    })

    // Clicking the thumbnail opens the Lightbox dialog
    fireEvent.click(
      screen.getByRole('button', { name: 'Preview image Image #7' })
    )
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toBeTruthy()
  })
})
