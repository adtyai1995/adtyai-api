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
  createEmailSchema,
  type EmailFormValues,
} from '../email-settings-schema'

const schema = createEmailSchema((key) => key)

function makeSmtpValues(smtpFrom: string): EmailFormValues {
  return {
    EmailProvider: 'smtp',
    ResendApiKey: '',
    ResendKeyConfigured: false,
    ResendFrom: '',
    ResendSenderName: '',
    SMTPServer: 'smtp.example.com',
    SMTPPort: '587',
    SMTPAccount: 'noreply@example.com',
    SMTPFrom: smtpFrom,
    SMTPToken: '',
    SMTPSSLEnabled: false,
    SMTPStartTLSEnabled: true,
    SMTPInsecureSkipVerify: false,
    SMTPForceAuthLogin: false,
  }
}

describe('createEmailSchema SMTPFrom validation', () => {
  it.each([
    '',
    '   ',
    'noreply@example.com',
    'ADTY AI <noreply@example.com>',
    'ADTY 平台 <noreply@example.com>',
    '"ADTY AI" <noreply@example.com>',
    '"ADTY, Inc." <noreply@example.com>',
    '"ADTY \\"Pro\\"" <noreply@example.com>',
    '<noreply@example.com>',
  ])('accepts valid SMTPFrom value: %j', (smtpFrom) => {
    const result = schema.safeParse(makeSmtpValues(smtpFrom))
    expect(result.success).toBe(true)
  })

  it.each([
    'not-an-email',
    'ADTY AI <not-an-email>',
    'ADTY AI <noreply@example.com',
    'ADTY AI noreply@example.com',
    'ADTY AI <noreply@example.com> extra',
    'a@example.com, b@example.com',
    'A <a@example.com>, B <b@example.com>',
    '"unterminated <noreply@example.com>',
    'noreply@example.com\r\nBcc: evil@example.com',
    'ADTY\nAI <noreply@example.com>',
    'ADTY\rAI <noreply@example.com>',
    '\r\n',
    'ADTY\0AI <noreply@example.com>',
  ])('rejects invalid or injected SMTPFrom value: %j', (smtpFrom) => {
    const result = schema.safeParse(makeSmtpValues(smtpFrom))
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(
        result.error.issues.some((issue) => issue.path[0] === 'SMTPFrom')
      ).toBe(true)
    }
  })
})
