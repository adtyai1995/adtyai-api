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
import { describe, expect, it } from 'vitest'

import type { UptimeGroupResult } from '@/features/dashboard/types'

import { FILTER_ALL } from '../constants'
import {
  classifyUptimeState,
  computeAveragePing,
  extractGroupModelStatuses,
  formatUptimePercentage,
  resolveAllGroupUptimesForModel,
  resolveModelUptimeForPricing,
} from '../lib/uptime'
import type { PricingModel } from '../types'

function makeModel(overrides: Partial<PricingModel> = {}): PricingModel {
  return {
    id: 1,
    model_name: 'gpt-4o',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 1,
    enable_groups: ['default', 'vip'],
    ...overrides,
  }
}

const SAMPLE_GROUPS: UptimeGroupResult[] = [
  {
    groupId: 'default',
    categoryName: 'Default Group',
    monitors: [],
    models: [
      {
        model: 'gpt-4o',
        monitorName: 'GPT-4o Main',
        status: 1,
        statusLabel: 'up',
        uptime: 0.9985,
        heartbeats: [
          { status: 1, time: '2026-11-24 10:00:00', ping: 120 },
          { status: 1, time: '2026-11-24 11:00:00', ping: 180 },
        ],
      },
    ],
  },
  {
    groupId: 'vip',
    categoryName: 'VIP Group',
    monitors: [],
    models: [
      {
        model: 'gpt-4o',
        status: 0,
        statusLabel: 'down',
        uptime: 0.75,
        heartbeats: [{ status: 0, time: '2026-11-24 11:00:00', ping: null }],
      },
    ],
  },
]

describe('pricing uptime helpers', () => {
  it('classifies uptime states accurately without marking unknown as online', () => {
    expect(classifyUptimeState(1, 0.995)).toBe('online')
    expect(classifyUptimeState(1, 0.9)).toBe('degraded')
    expect(classifyUptimeState(2, 0.99)).toBe('degraded')
    expect(classifyUptimeState(3, 1)).toBe('maintenance')
    expect(classifyUptimeState(0, 0.5)).toBe('offline')
    expect(classifyUptimeState(-1, null)).toBe('unknown')
  })

  it('formats uptime percentages and computes average ping', () => {
    expect(formatUptimePercentage(0.9985)).toBe('99.85%')
    expect(formatUptimePercentage(null)).toBe('—')
    expect(
      computeAveragePing([
        { status: 1, time: 't1', ping: 100 },
        { status: 1, time: 't2', ping: null },
        { status: 1, time: 't3', ping: 200 },
      ])
    ).toBe(150)
    expect(computeAveragePing([])).toBeNull()
  })

  it('resolves group-specific status when a group filter is selected', () => {
    const model = makeModel()
    const vipStatus = resolveModelUptimeForPricing(SAMPLE_GROUPS, model, 'vip')
    expect(vipStatus?.groupId).toBe('vip')
    expect(vipStatus?.state).toBe('offline')

    const defaultStatus = resolveModelUptimeForPricing(
      SAMPLE_GROUPS,
      model,
      'default'
    )
    expect(defaultStatus?.groupId).toBe('default')
    expect(defaultStatus?.state).toBe('online')
  })

  it('matches across enabled groups when FILTER_ALL is selected', () => {
    const model = makeModel({ enable_groups: ['vip', 'default'] })
    const resolved = resolveModelUptimeForPricing(
      SAMPLE_GROUPS,
      model,
      FILTER_ALL
    )
    expect(resolved?.groupId).toBe('default')
    expect(resolved?.state).toBe('online')

    const all = resolveAllGroupUptimesForModel(SAMPLE_GROUPS, model)
    expect(all).toHaveLength(2)
  })

  it('falls back to monitors when legacy groups have no explicit models array', () => {
    const legacyGroup: UptimeGroupResult = {
      categoryName: 'Legacy',
      monitors: [
        {
          name: 'claude-3-7-sonnet',
          status: 1,
          uptime: 1,
          heartbeats: [{ status: 1, time: 't1', ping: 90 }],
        },
      ],
    }
    const items = extractGroupModelStatuses(legacyGroup)
    expect(items).toHaveLength(1)
    expect(items[0].model).toBe('claude-3-7-sonnet')
    expect(items[0].state).toBe('online')
  })
})
