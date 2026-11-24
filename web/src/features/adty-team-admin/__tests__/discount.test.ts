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
  validMainTeamDiscount,
  validSubsiteTeamMultiplier,
  validTeamDiscount,
} from '../../adty-team-registration/discount'

describe('team discount and supply multiplier validation', () => {
  it('validates main-site team discount ratio (> 0 and <= 10, up to 4 decimals)', () => {
    expect(validMainTeamDiscount('0.8')).toBe(true)
    expect(validMainTeamDiscount('0.0001')).toBe(true)
    expect(validMainTeamDiscount('1')).toBe(true)
    expect(validMainTeamDiscount('1.5')).toBe(true)
    expect(validMainTeamDiscount('10')).toBe(true)
    expect(validMainTeamDiscount('10.0000')).toBe(true)

    expect(validMainTeamDiscount('0')).toBe(false)
    expect(validMainTeamDiscount('-0.5')).toBe(false)
    expect(validMainTeamDiscount('10.0001')).toBe(false)
    expect(validMainTeamDiscount('0.12345')).toBe(false)
    expect(validMainTeamDiscount('')).toBe(false)
  })

  it('validates subsite team supply multiplier (1.0 to 10.0, up to 4 decimals)', () => {
    expect(validSubsiteTeamMultiplier('1')).toBe(true)
    expect(validSubsiteTeamMultiplier('1.0')).toBe(true)
    expect(validSubsiteTeamMultiplier('1.25')).toBe(true)
    expect(validSubsiteTeamMultiplier('10')).toBe(true)

    expect(validSubsiteTeamMultiplier('0.9999')).toBe(false)
    expect(validSubsiteTeamMultiplier('0.8')).toBe(false)
    expect(validSubsiteTeamMultiplier('0')).toBe(false)
    expect(validSubsiteTeamMultiplier('10.0001')).toBe(false)
    expect(validSubsiteTeamMultiplier('1.12345')).toBe(false)
  })

  it('dispatches cleanly through validTeamDiscount', () => {
    expect(validTeamDiscount('0.8')).toBe(true)
    expect(validTeamDiscount('0.8', false)).toBe(true)
    expect(validTeamDiscount('0.8', true)).toBe(false)
    expect(validTeamDiscount('1.5', true)).toBe(true)
  })
})
