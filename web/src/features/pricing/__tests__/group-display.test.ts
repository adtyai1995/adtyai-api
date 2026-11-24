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

import {
  formatEffectiveGroupRatio,
  formatGroupRatioMultiplier,
  getAvailableGroups,
  getDisplayGroupRatio,
  resolvePricingGroupName,
} from '../lib/model-helpers'
import type { PricingModel } from '../types'

describe('pricing group display and ratio formatting', () => {
  it('formats effective ratios without floating-point noise', () => {
    expect(formatEffectiveGroupRatio(1)).toBe('x1')
    expect(formatEffectiveGroupRatio(1.2000000000000002)).toBe('x1.2')
    expect(formatEffectiveGroupRatio(0.15000000000000002)).toBe('x0.15')
    expect(formatEffectiveGroupRatio(2.5)).toBe('x2.5')
    expect(formatEffectiveGroupRatio(0)).toBe('x0')
    expect(formatEffectiveGroupRatio(undefined)).toBeUndefined()

    expect(formatGroupRatioMultiplier(1.2000000000000002)).toBe('1.2x')
    expect(formatGroupRatioMultiplier(0.75)).toBe('0.75x')
    expect(formatGroupRatioMultiplier(undefined)).toBe('1x')
  })

  it('resolves real group names from display store or usable_group fallback', () => {
    const usableGroup = {
      default: '默认分组',
      grp_test: '测试',
      grp_obj: { name: 'VIP 高速', desc: 'VIP description', ratio: 1.5 },
    }

    expect(resolvePricingGroupName('grp_test', '测试组', usableGroup)).toBe(
      '测试组'
    )
    expect(resolvePricingGroupName('grp_test', 'grp_test', usableGroup)).toBe(
      '测试'
    )
    expect(resolvePricingGroupName('grp_obj', 'grp_obj', usableGroup)).toBe(
      'VIP 高速'
    )
    expect(resolvePricingGroupName('grp_unknown', 'grp_unknown', {})).toBe(
      'grp_unknown'
    )
  })

  it('retains non-default groups from usableGroup and groupRatio and computes effective selling ratio', () => {
    const model: PricingModel = {
      id: 1,
      model_name: 'gpt-4o',
      quota_type: 0,
      model_ratio: 1,
      completion_ratio: 2,
      enable_groups: ['grp_test', 'grp_vip'],
      group_ratio: {
        grp_test: 1.2000000000000002,
        grp_vip: 2.4,
      },
    }

    const groups = getAvailableGroups(
      model,
      { grp_test: '测试' },
      { grp_test: 1.2000000000000002, grp_vip: 2.4 }
    )
    expect(groups).toEqual(['grp_test', 'grp_vip'])
    expect(getDisplayGroupRatio(model, 'grp_test')).toBeCloseTo(1.2, 6)
    expect(getDisplayGroupRatio(model, 'grp_vip')).toBeCloseTo(2.4, 6)
    expect(getDisplayGroupRatio(model, 'all')).toBeCloseTo(1.2, 6)
  })
})
