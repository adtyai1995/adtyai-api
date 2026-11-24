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
import { EXCLUDED_GROUPS, FILTER_ALL, QUOTA_TYPE_VALUES } from '../constants'
import type { PricingModel, PricingUsableGroupValue } from '../types'

// ----------------------------------------------------------------------------
// Model Helper Utilities
// ----------------------------------------------------------------------------

/**
 * Format an effective group ratio without IEEE-754 floating-point noise
 * (e.g. 1.2000000000000002 -> "x1.2").
 */
export function formatEffectiveGroupRatio(
  ratio: number | null | undefined
): string | undefined {
  if (ratio == null || !Number.isFinite(ratio)) return undefined
  const clean = Number(ratio.toFixed(6))
  return `x${clean}`
}

/**
 * Format a group multiplier cleanly with trailing "x" (e.g. "1.2x").
 */
export function formatGroupRatioMultiplier(
  ratio: number | null | undefined
): string {
  if (ratio == null || !Number.isFinite(ratio)) return '1x'
  const clean = Number(ratio.toFixed(6))
  return `${clean}x`
}

/**
 * Resolve the human-readable name for a pricing group.
 * Prefers the multilingual display store name when resolved, otherwise falls
 * back to `usable_group[group]` from `/api/pricing`, and finally the group ID.
 */
export function resolvePricingGroupName(
  groupId: string,
  displayName: string,
  usableGroup?: Record<string, PricingUsableGroupValue>
): string {
  if (displayName && displayName !== groupId) {
    return displayName
  }
  const raw = usableGroup?.[groupId]
  if (typeof raw === 'string' && raw.trim()) {
    return raw.trim()
  }
  if (raw && typeof raw === 'object') {
    if (typeof raw.name === 'string' && raw.name.trim()) {
      return raw.name.trim()
    }
    if (typeof raw.desc === 'string' && raw.desc.trim()) {
      return raw.desc.trim()
    }
  }
  return groupId
}

/**
 * Get available groups for a model
 */
export function getAvailableGroups(
  model: PricingModel,
  usableGroup: Record<string, PricingUsableGroupValue>,
  groupRatio?: Record<string, number>
): string[] {
  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []

  const known = new Set<string>([
    ...Object.keys(usableGroup || {}),
    ...Object.keys(groupRatio || {}),
  ])

  return [...known]
    .filter((g) => !EXCLUDED_GROUPS.includes(g))
    .filter((g) => modelEnableGroups.includes(g))
}

/**
 * Read a configured group ratio while preserving valid zero ratios.
 */
export function getConfiguredGroupRatio(
  groupRatio: Record<string, number>,
  group: string
): number {
  const ratio = groupRatio[group]
  return typeof ratio === 'number' && Number.isFinite(ratio) ? ratio : 1
}

/**
 * Resolve the group ratio used by model square summary prices.
 *
 * When no specific group is selected, the model square shows the best price
 * available to the viewer. When a group filter is active, it shows that
 * group's price instead.
 */
export function getDisplayGroupRatio(
  model: PricingModel,
  selectedGroup?: string
): number {
  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []
  const groupRatio = model.group_ratio || {}

  if (
    selectedGroup &&
    selectedGroup !== FILTER_ALL &&
    modelEnableGroups.includes(selectedGroup)
  ) {
    return getConfiguredGroupRatio(groupRatio, selectedGroup)
  }

  if (modelEnableGroups.length === 0) {
    return 1
  }

  let minRatio = Number.POSITIVE_INFINITY

  for (const group of modelEnableGroups) {
    const ratio = groupRatio[group]
    if (
      typeof ratio === 'number' &&
      Number.isFinite(ratio) &&
      ratio < minRatio
    ) {
      minRatio = ratio
    }
  }

  return minRatio === Number.POSITIVE_INFINITY ? 1 : minRatio
}

/**
 * Replace model placeholder in endpoint path
 */
export function replaceModelInPath(path: string, modelName: string): string {
  return path.replaceAll('{model}', modelName)
}

/**
 * Check if model is token-based pricing
 */
export function isTokenBasedModel(model: PricingModel): boolean {
  return model.quota_type === QUOTA_TYPE_VALUES.TOKEN
}
