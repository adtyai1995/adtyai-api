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

import {
  computeAggregateHealth,
  formatGroupRatio,
  formatLatency,
  formatSuccessRate,
  getSuccessRateToneClass,
  inferChannelPlatform,
  resolveChannelCard,
  sortChannelCards,
} from '../lib/channel-status'

describe('channel-status helpers', () => {
  it('formats latency, success rate, and group ratio consistently', () => {
    expect(formatLatency(null)).toBe('—')
    expect(formatLatency(420.4)).toBe('420ms')
    expect(formatLatency(5532)).toBe('5.53s')

    expect(formatSuccessRate(null)).toBe('—')
    expect(formatSuccessRate(0.995)).toBe('99.50%')
    expect(formatSuccessRate(1.2)).toBe('100.00%')

    expect(formatGroupRatio(0.75)).toBe('0.75')
    expect(formatGroupRatio(0.0400001)).toBe('0.04')
  })

  it('maps success rate thresholds to semantic color classes', () => {
    expect(getSuccessRateToneClass(null)).toBe('text-muted-foreground')
    expect(getSuccessRateToneClass(0.95)).toBe('text-success')
    expect(getSuccessRateToneClass(0.88)).toBe('text-warning')
    expect(getSuccessRateToneClass(0.79)).toBe('text-destructive')
  })

  it('infers known platforms when backend omits explicit platform field', () => {
    expect(inferChannelPlatform('claude-sonnet-4-5', 'Claude Pro')).toBe(
      'anthropic'
    )
    expect(inferChannelPlatform('gpt-5.4', 'GPT Plus')).toBe('openai')
    expect(inferChannelPlatform('gemini-2.5-pro', 'Gemini')).toBe('gemini')
    expect(inferChannelPlatform('glm-4.5', 'Zhipu')).toBe('zhipu')
    expect(inferChannelPlatform('deepseek-r1', 'DeepSeek')).toBe('deepseek')
    expect(inferChannelPlatform('kimi-k2', 'Moonshot')).toBe('moonshot')
    expect(inferChannelPlatform('grok-4', 'xAI')).toBe('xai')
    expect(inferChannelPlatform('gpt-4o', 'Azure East')).toBe('azure')
    expect(inferChannelPlatform('custom-model', 'Custom')).toBe('other')
  })

  it('normalizes group cards and never marks empty no-sample groups as up', () => {
    const emptyGroup: UptimeGroupResult = {
      groupId: 'grp_empty',
      categoryName: 'Empty Group',
      status: 1,
      statusLabel: 'up',
      uptime: null,
      heartbeats: [],
    }
    const card = resolveChannelCard(emptyGroup, 0, (id) =>
      id === 'grp_empty' ? 'Localized Empty' : id
    )
    expect(card.displayName).toBe('Localized Empty')
    expect(card.status).toBe('unknown')
  })

  it('computes aggregate health priority as disrupted > degraded > operational > unknown', () => {
    const makeCard = (
      groupId: string,
      statusLabel: 'up' | 'degraded' | 'down' | 'unknown'
    ) =>
      resolveChannelCard(
        {
          groupId,
          categoryName: groupId,
          statusLabel,
          uptime: statusLabel === 'unknown' ? null : 0.99,
          heartbeats:
            statusLabel === 'unknown'
              ? []
              : [{ status: 1, time: '2026-11-25 03:00:00', ping: 200 }],
        },
        0,
        (id) => id
      )

    expect(computeAggregateHealth([])).toBe('unknown')
    expect(computeAggregateHealth([makeCard('a', 'unknown')])).toBe('unknown')
    expect(
      computeAggregateHealth([makeCard('a', 'up'), makeCard('b', 'unknown')])
    ).toBe('operational')
    expect(
      computeAggregateHealth([makeCard('a', 'up'), makeCard('b', 'degraded')])
    ).toBe('degraded')
    expect(
      computeAggregateHealth([makeCard('a', 'degraded'), makeCard('b', 'down')])
    ).toBe('disrupted')
  })

  it('sorts channel cards with nulls placed last in both directions', () => {
    const groups: UptimeGroupResult[] = [
      {
        groupId: 'a',
        categoryName: 'A',
        platform: 'openai',
        groupRatio: 0.3,
        uptime: 0.99,
        avgPingMs: 5000,
        cacheHitRate: null,
        statusLabel: 'up',
        heartbeats: [{ status: 1, time: '2026-11-25 03:00:00', ping: 5000 }],
      },
      {
        groupId: 'b',
        categoryName: 'B',
        platform: 'openai',
        groupRatio: 0.04,
        uptime: 0.85,
        avgPingMs: 1200,
        cacheHitRate: 72.5,
        statusLabel: 'degraded',
        heartbeats: [{ status: 2, time: '2026-11-25 03:00:00', ping: 1200 }],
      },
      {
        groupId: 'c',
        categoryName: 'C',
        platform: 'openai',
        groupRatio: null,
        uptime: 0.5,
        avgPingMs: null,
        cacheHitRate: 40,
        statusLabel: 'down',
        heartbeats: [{ status: 0, time: '2026-11-25 03:00:00', ping: null }],
      },
    ]
    const cards = groups.map((g, i) => resolveChannelCard(g, i, (id) => id))

    expect(
      sortChannelCards(cards, 'ratio', 'asc').map((c) => c.groupId)
    ).toEqual(['b', 'a', 'c'])
    expect(
      sortChannelCards(cards, 'ratio', 'desc').map((c) => c.groupId)
    ).toEqual(['a', 'b', 'c'])
    expect(
      sortChannelCards(cards, 'avgPingMs', 'asc').map((c) => c.groupId)
    ).toEqual(['b', 'a', 'c'])
    expect(
      sortChannelCards(cards, 'avgPingMs', 'desc').map((c) => c.groupId)
    ).toEqual(['a', 'b', 'c'])
    expect(
      sortChannelCards(cards, 'status', 'asc').map((c) => c.groupId)
    ).toEqual(['a', 'b', 'c'])
    expect(
      sortChannelCards(cards, 'status', 'desc').map((c) => c.groupId)
    ).toEqual(['c', 'b', 'a'])
  })
})
