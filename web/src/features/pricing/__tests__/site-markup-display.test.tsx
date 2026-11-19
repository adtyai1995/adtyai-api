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
import { act, render, screen } from '@testing-library/react'
import i18next from 'i18next'
import { expect, it, vi } from 'vitest'

import { ModelCard } from '../components/model-card'
import { ModelDetailsContent } from '../components/model-details'
import { ModelPriceCell } from '../components/model-price-cell'
import {
  getCardExamplePrice,
  getDynamicPricingSummary,
  getDynamicPricingTiers,
} from '../lib/dynamic-price'
import type { PricingModel } from '../types'

vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))

const model: PricingModel & { billing_display_multiplier: string } = {
  id: 1,
  model_name: 'site-dynamic',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
  enable_groups: ['default'],
  billing_mode: 'tiered_expr',
  billing_expr: 'tier("base", p * 2 + c * 6)',
  billing_display_multiplier: '1.25',
}

it('applies the model display factor once to token, fixed, plugin and example prices', () => {
  const token = getDynamicPricingSummary(model, {
    tokenUnit: 'M',
    groupRatioMultiplier: 0.96,
  })
  expect(
    token?.entries.find((entry) => entry.field === 'inputPrice')?.formatted
  ).toContain('2.4')
  expect(
    token?.entries.find((entry) => entry.field === 'inputPrice')?.value
  ).toBe(2.5)
  const fixed = getDynamicPricingSummary(
    { ...model, billing_expr: 'tier("request", fixed(0.04))' },
    { tokenUnit: 'K', groupRatioMultiplier: 0.96 }
  )
  expect(fixed?.entries[0].formatted).toContain('0.048')
  const plugin: PricingModel = {
    ...model,
    billing_plugin_variants: [
      {
        plugin_key: 'sample',
        plugin_name: 'Sample',
        billing_mode: 'tiered_expr',
        billing_expr: 'tier("base", u("seconds") * 0.04)',
        billing_usage_schema: { seconds: { type: 'number', unit: 'second' } },
        billing_usage_examples: [
          { label: 'Six seconds', facts: { seconds: 6 } },
        ],
      },
    ],
  }
  const summary = getDynamicPricingSummary(plugin, {
    tokenUnit: 'M',
    groupRatioMultiplier: 0.96,
  })
  expect(summary?.entries[0].formatted).toContain('0.048')
  expect(
    getCardExamplePrice(plugin, { tokenUnit: 'M', groupRatioMultiplier: 0.96 })
      ?.formatted
  ).toContain('0.288')
  expect(getCardExamplePrice(plugin, { tokenUnit: 'M' })?.total).toBeCloseTo(
    0.3
  )
  expect(model.billing_expr).toBe('tier("base", p * 2 + c * 6)')
})

it('shows the same model factor in base details, breakdown and group prices', () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, enabled: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ModelDetailsContent
        model={model}
        groupRatio={{ default: 0.96 }}
        usableGroup={{ default: { desc: 'Default', ratio: 0.96 } }}
        endpointMap={{}}
        autoGroups={[]}
        priceRate={1}
        usdExchangeRate={1}
        tokenUnit='M'
      />
    </QueryClientProvider>
  )
  expect(screen.getAllByText(/2\.5/).length).toBeGreaterThan(0)
  expect(screen.getAllByText(/2\.4/).length).toBeGreaterThan(0)
})

it('keeps card and table prices consistent in both interface languages', async () => {
  const fixed: PricingModel = {
    ...model,
    billing_expr: 'tier("request", fixed(0.04))',
    group_ratio: { default: 0.96 },
  }
  const originalLanguage = i18next.language
  render(
    <>
      <div data-testid='markup-card'>
        <ModelCard model={fixed} onClick={vi.fn()} />
      </div>
      <div data-testid='markup-cell'>
        <ModelPriceCell model={fixed} />
      </div>
    </>
  )
  try {
    for (const language of ['en', 'zhCN']) {
      await act(() => i18next.changeLanguage(language))
      expect(screen.getByTestId('markup-card')).toHaveTextContent('0.048')
      expect(screen.getByTestId('markup-cell')).toHaveTextContent('0.048')
    }
  } finally {
    await act(() => i18next.changeLanguage(originalLanguage))
  }
})

it('scales monetary image and time tiers without changing conditions or request rules', () => {
  const expression =
    'len > 200000 ? tier("long", p * 4 + img * 8) : tier("short", p * 2 + img * 4)'
  const tiers = getDynamicPricingTiers({ ...model, billing_expr: expression })
  expect(tiers[0]).toMatchObject({ inputPrice: 5, imagePrice: 10 })
  expect(tiers[0].conditions).toEqual(
    getDynamicPricingTiers({
      ...model,
      billing_expr: expression,
      billing_display_multiplier: undefined,
    })[0].conditions
  )
  const timeModel = {
    ...model,
    billing_expr:
      'hour("UTC") < 12 ? tier("morning", p * 2) : tier("evening", p * 4)',
  }
  const summary = getDynamicPricingSummary(timeModel, {
    tokenUnit: 'M',
    now: new Date('2026-11-19T06:00:00Z'),
  })
  expect(summary?.primaryEntries[0].value).toBe(2.5)
  expect(timeModel.billing_expr).toContain('hour("UTC") < 12')
})
