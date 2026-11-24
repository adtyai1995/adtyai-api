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
// @vitest-environment jsdom
import type { TFunction } from 'i18next'
import { describe, expect, it } from 'vitest'

import {
  getNotificationPlainLength,
  isHtmlNotificationContent,
  MAX_NOTIFICATION_PLAIN_LENGTH,
  sanitizeNotificationHtml,
} from '../lib/rich-text'
import {
  createNotificationSchema,
  formatNotificationRecipients,
} from '../lib/schema'

const identityT = ((key: string) => key) as unknown as TFunction

describe('notification rich-text utilities', () => {
  it('sanitizes allowed headings, bold, font-size, and color while stripping scripts and unsafe styles', () => {
    const dirty =
      '<h1>Notice</h1><p><span style="color: #dc2626; font-size: 20px; position: fixed">Alert</span><script>alert(1)</script><strong>Now</strong></p>'
    const clean = sanitizeNotificationHtml(dirty)

    expect(clean).toContain('<h1>Notice</h1>')
    expect(clean).toContain('<strong>Now</strong>')
    expect(clean).toContain('color: #dc2626')
    expect(clean).toContain('font-size: 20px')
    expect(clean).not.toContain('<script')
    expect(clean).not.toContain('position')
  })

  it('normalizes legacy font tags into safe styled spans', () => {
    const clean = sanitizeNotificationHtml(
      '<font color="#2563eb" size="5">Update</font>'
    )
    expect(clean).toContain('<span')
    expect(clean).toContain('color: #2563eb')
    expect(clean).toContain('font-size: 20px')
    expect(clean).not.toContain('<font')
  })

  it('counts plain text characters accurately without HTML tags or zero-width spaces', () => {
    const html =
      '<h2>Hello</h2><p><span style="color: #dc2626">World\u200B</span></p>'
    // "Hello\nWorld" => 11 chars
    expect(getNotificationPlainLength(html)).toBe(11)
    expect(isHtmlNotificationContent(html, 'html')).toBe(true)
    expect(isHtmlNotificationContent(html)).toBe(true)
    expect(isHtmlNotificationContent('plain markdown text')).toBe(false)
  })
})

describe('notification multi-recipient schema validation', () => {
  const baseValid = {
    title: 'Maintenance Window',
    content: '<p>Scheduled upgrade tonight</p>',
    content_format: 'html' as const,
    kind: 'maintenance' as const,
    audience: 'all_users' as const,
    target_audiences: ['main_users', 'all_site_owners'] as const,
    target_site_ids: [] as number[],
    target_site_id: 0,
    target_team_id: 0,
    target_user_id: 0,
    business: '',
    sync_telegram: false,
    telegram_channel: '',
  }

  it('requires at least one recipient group and at least one subsite when site_users is checked in root mode', () => {
    const rootSchema = createNotificationSchema('root', false)

    const emptyAudiences = rootSchema.safeParse({
      ...baseValid,
      target_audiences: [],
    })
    expect(emptyAudiences.success).toBe(false)

    const missingSubsites = rootSchema.safeParse({
      ...baseValid,
      target_audiences: ['site_users'],
      target_site_ids: [],
    })
    expect(missingSubsites.success).toBe(false)

    const validCombination = rootSchema.safeParse({
      ...baseValid,
      target_audiences: ['main_users', 'all_site_owners', 'site_users', 'main_teams'],
      target_site_ids: [2, 5],
    })
    expect(validCombination.success).toBe(true)
  })

  it('allows owner combinations without requiring target_site_ids and blocks plain text over 2000 chars', () => {
    const ownerSchema = createNotificationSchema('owner', false)

    const validOwner = ownerSchema.safeParse({
      ...baseValid,
      audience: 'site',
      target_audiences: ['site_users', 'site_teams'],
      target_site_ids: [],
    })
    expect(validOwner.success).toBe(true)

    const overLimit = ownerSchema.safeParse({
      ...baseValid,
      audience: 'site',
      target_audiences: ['site_users'],
      content: `<p>${'a'.repeat(MAX_NOTIFICATION_PLAIN_LENGTH + 1)}</p>`,
    })
    expect(overLimit.success).toBe(false)
  })

  it('formats multi-audience recipients cleanly for display', () => {
    expect(
      formatNotificationRecipients(
        {
          target_audiences: ['main_users', 'site_users'],
          target_site_names: ['alpha.example.com', 'beta.example.com'],
        },
        identityT
      )
    ).toBe(
      'Main-site users · Subsite users (alpha.example.com, beta.example.com)'
    )
  })
})
