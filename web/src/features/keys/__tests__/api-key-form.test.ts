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
import type { TFunction } from 'i18next'
import { describe, expect, it, vi } from 'vitest'

import {
  getApiKeyFormDefaultValues,
  getApiKeyFormSchema,
  transformApiKeyToFormDefaults,
  transformFormDataToPayload,
} from '../lib/api-key-form'
import type { ApiKey } from '../types'

vi.mock('@/lib/format', () => ({
  parseQuotaFromDollars: (dollars: number) => Math.round(dollars * 500_000),
  quotaUnitsToDollars: (units: number) => units / 500_000,
}))

const identityT = ((key: string) => key) as unknown as TFunction

describe('api key form group validation and transforms', () => {
  const schema = getApiKeyFormSchema(identityT)

  const baseForm = {
    ...getApiKeyFormDefaultValues('default'),
    name: 'prod-key',
  }

  it('rejects empty, whitespace, and auto groups', () => {
    expect(schema.safeParse({ ...baseForm, group: '' }).success).toBe(false)
    expect(schema.safeParse({ ...baseForm, group: '   ' }).success).toBe(false)
    expect(schema.safeParse({ ...baseForm, group: 'auto' }).success).toBe(false)
  })

  it('accepts concrete non-empty groups and emits clean payload', () => {
    const parsed = schema.safeParse({ ...baseForm, group: 'vip' })
    expect(parsed.success).toBe(true)
    if (!parsed.success) return

    const payload = transformFormDataToPayload(parsed.data)
    expect(payload.group).toBe('vip')
    expect(payload.auto_groups).toEqual([])
    expect(payload.cross_group_retry).toBe(false)
  })

  it('replaces legacy empty or auto group with fallback concrete group on edit', () => {
    const legacyKey = {
      id: 1,
      key: 'abc',
      status: 1,
      name: 'legacy',
      created_time: 1,
      accessed_time: 1,
      expired_time: -1,
      remain_quota: 0,
      unlimited_quota: true,
      model_limits_enabled: false,
      model_limits: '',
      allow_ips: '',
      used_quota: 0,
      group: 'auto',
      auto_groups: [],
      cross_group_retry: true,
      allow_user_quota_query: false,
      rate_limit_per_minute: null,
      rate_limit_tpm: null,
      rate_limit_concurrency: null,
    } satisfies ApiKey

    const defaults = transformApiKeyToFormDefaults(legacyKey, 'default')
    expect(defaults.group).toBe('default')
  })
})
