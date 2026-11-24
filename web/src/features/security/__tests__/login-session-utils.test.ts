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
import { describe, expect, it } from 'vitest'

import { loginMethodLabel } from '../components/login-session-utils'

const identityT = ((key: string) => key) as unknown as TFunction

describe('loginMethodLabel', () => {
  it('maps supported login methods and collapses legacy oauth provider labels', () => {
    expect(loginMethodLabel('password', identityT)).toBe('Password')
    expect(loginMethodLabel('2fa', identityT)).toBe('Two-factor Authentication')
    expect(loginMethodLabel('telegram', identityT)).toBe('Telegram')
    expect(loginMethodLabel('oauth', identityT)).toBe('OAuth')
    expect(loginMethodLabel('oauth:github', identityT)).toBe('OAuth')
    expect(loginMethodLabel('oauth:discord', identityT)).toBe('OAuth')
    expect(loginMethodLabel('', identityT)).toBe('Unknown')
  })
})
