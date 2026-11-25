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
  USER_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformUserToFormDefaults,
} from '../lib/user-form'
import type { User } from '../types'

const BASE_USER: User = {
  id: 42,
  username: 'alice',
  display_name: 'Alice',
  quota: 500_000,
  used_quota: 0,
  request_count: 0,
  group: '',
  status: 1,
  role: 1,
}

describe('user-form group handling', () => {
  it('does not default USER_FORM_DEFAULT_VALUES.group to default', () => {
    expect(USER_FORM_DEFAULT_VALUES.group).toBe('')
  })

  it('preserves empty group without defaulting to default in transformUserToFormDefaults', () => {
    expect(transformUserToFormDefaults(BASE_USER).group).toBe('')
    expect(
      transformUserToFormDefaults({ ...BASE_USER, group: 'vip' }).group
    ).toBe('vip')
  })

  it('omits group from both create and update payloads', () => {
    const createPayload = transformFormDataToPayload({
      ...USER_FORM_DEFAULT_VALUES,
      username: 'newuser',
      display_name: 'New User',
      password: 'password123',
      group: 'default',
    })
    expect('group' in createPayload).toBe(false)

    const updatePayload = transformFormDataToPayload(
      {
        ...transformUserToFormDefaults(BASE_USER),
        remark: 'Updated note',
        group: 'default',
      },
      BASE_USER.id
    )
    expect('group' in updatePayload).toBe(false)
    expect(updatePayload).toMatchObject({
      id: 42,
      username: 'alice',
      display_name: 'Alice',
      remark: 'Updated note',
    })
  })
})
