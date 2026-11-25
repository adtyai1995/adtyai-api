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
export const TEAM_DISCOUNT_MAX = 10
export const SUBSITE_TEAM_MULTIPLIER_MIN = 1

const MAIN_TEAM_DECIMAL_PATTERN = /^(?:\d{1,2}(?:\.\d{1,4})?|\.\d{1,4})$/
const SUBSITE_TEAM_DECIMAL_PATTERN = /^\d{1,2}(?:\.\d{1,4})?$/

/** Main-site team discount ratio: 0 < value <= 10 with up to 4 fractional digits. */
export function validMainTeamDiscount(input: string): boolean {
  const trimmed = input.trim()
  if (!MAIN_TEAM_DECIMAL_PATTERN.test(trimmed)) return false
  const value = Number(trimmed)
  return Number.isFinite(value) && value > 0 && value <= TEAM_DISCOUNT_MAX
}

/** Subsite team supply price multiplier: 1 <= value <= 10 with up to 4 fractional digits. */
export function validSubsiteTeamMultiplier(input: string): boolean {
  const trimmed = input.trim()
  if (!SUBSITE_TEAM_DECIMAL_PATTERN.test(trimmed)) return false
  const value = Number(trimmed)
  return (
    Number.isFinite(value) &&
    value >= SUBSITE_TEAM_MULTIPLIER_MIN &&
    value <= TEAM_DISCOUNT_MAX
  )
}

/** Validates a team discount ratio (main site) or supply price multiplier (subsite). */
export function validTeamDiscount(input: string, isSubsite = false): boolean {
  return isSubsite
    ? validSubsiteTeamMultiplier(input)
    : validMainTeamDiscount(input)
}
