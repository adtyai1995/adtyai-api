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

import { retailOverrideChanges } from '../lib/site-owner'

describe('editing retail overrides', () => {
  it('keeps a re-added key, removes a renamed key and omits untouched prices', () => {
    expect(
      retailOverrideChanges({ gpt: '2', old: '3', keep: '4' }, [
        ['gpt', '5'],
        ['renamed', '3'],
        ['keep', '4'],
      ])
    ).toEqual({ gpt: '5', old: '0', renamed: '3' })
  })
})
