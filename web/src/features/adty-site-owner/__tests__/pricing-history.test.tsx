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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { PricingPanel } from '../components/pricing-panel'
import type { OwnerSite } from '../types'

vi.mock('../hooks/use-verified-owner-mutation', () => ({ useVerifiedOwnerMutation: () => ({ mutate: vi.fn(), isPending: false }) }))
vi.mock('@/features/system-settings/hooks/use-draft-navigation', () => ({ useDraftNavigation: vi.fn() }))
afterEach(() => vi.restoreAllMocks())

it('opens read-only history and retries a failed history query', async () => {
  const get = vi.spyOn(api, 'get').mockRejectedValueOnce(new Error('history unavailable')).mockResolvedValue({ data: { success: true, data: { total: 1, items: [{ id: 1, site_id: 7, user_id: 0, actor_id: 41, kind: 'model', model: 'long/model-name', group: '', action: 'scheduled', before: '1.2', after: '1.5', version: 4, effective_at: 1700086400, created_at: 1700000000 }] } } })
  const site: OwnerSite = { id: 7, status: 'active', prices: [], pricing_version: 4, markup_min: '0', markup_max: '3', cost_factor: '0.8', markup_increase_delay_hours: 24, subdomain: 'sample', root_domain: 'example.test', host: 'sample.example.test', base_url: 'https://sample.example.test', custom_domain_enabled: false, sender_domain_enabled: false, brand: { name: '', logo_url: '', tagline: '', notice: '', support_contact: '' }, bonus_methods: [], seo_index: false, agreement_version: '1', agreement_at: 0, domains: [], visibility: {} }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><PricingPanel site={site} /></QueryClientProvider>)
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Price change history' }))
  expect(await screen.findByText('history unavailable')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('long/model-name')).toBeVisible()
  expect(screen.getByText('1.2 → 1.5')).toBeVisible()
  await waitFor(() => expect(get).toHaveBeenCalledWith('/api/adty/site/pricing/history', { params: { p: 1, page_size: 20 } }))
  expect(screen.queryByRole('button', { name: /restore/i })).toBeNull()
  client.clear()
})
