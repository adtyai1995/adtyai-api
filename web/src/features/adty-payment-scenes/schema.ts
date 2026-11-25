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
import { z } from 'zod'

export const ACTIVE_SCENE_BUSINESSES = [
  'wallet_topup',
  'team_topup',
  'owner_margin_topup',
] as const

export type ActiveSceneBusiness = (typeof ACTIVE_SCENE_BUSINESSES)[number]

export const sceneBusinessLabels: Record<string, string> = {
  wallet_topup: 'Site-wide User Top-up',
  team_topup: 'Main-site Team Top-up',
  owner_margin_topup: 'Owner Margin Top-up',
  owner_self_use_topup: 'Owner Self-use Top-up (Legacy)',
}

export const sceneBusinessDescriptions: Record<ActiveSceneBusiness, string> = {
  wallet_topup:
    'Covers main-site users, subsite users, and subsite teams.',
  team_topup: 'Applies to main-site teams only.',
  owner_margin_topup: 'Applies to subsite owner margin deposits.',
}

const money = z
  .string()
  .regex(/^\d+(\.\d{1,8})?$/, 'Invalid parameters')
  .max(32, 'Invalid parameters')
export const paymentSceneSchema = z
  .object({
    key: z.string().regex(/^[a-z][a-z0-9_.-]{0,63}$/, 'Invalid parameters'),
    name: z
      .string()
      .trim()
      .min(1, 'Required')
      .max(120, 'Name must be 120 characters or fewer'),
    business: z.enum(ACTIVE_SCENE_BUSINESSES),
    enabled: z.boolean(),
    providers: z
      .array(z.string())
      .min(1, 'Required')
      .max(6, 'Invalid parameters'),
    methods: z
      .array(
        z
          .string()
          .min(1, 'Invalid parameters')
          .max(255, 'Invalid parameters')
      )
      .max(100, 'Invalid parameters'),
    channel_ids: z
      .array(
        z
          .number({ error: 'Enter a positive integer' })
          .int('Enter a positive integer')
          .positive('Enter a positive integer')
      )
      .max(1000, 'Invalid parameters'),
    currencies: z
      .array(z.string().regex(/^[A-Z][A-Z0-9]{1,15}$/, 'Invalid parameters'))
      .max(32, 'Invalid parameters'),
    site_scope: z.enum(['all', 'main', 'all_sites', 'selected']),
    team_ids: z
      .array(
        z
          .number({ error: 'Enter a positive integer' })
          .int('Enter a positive integer')
          .positive('Enter a positive integer')
      )
      .max(1000, 'Invalid parameters')
      .optional(),
    site_ids: z
      .array(
        z
          .number({ error: 'Enter a positive integer' })
          .int('Enter a positive integer')
          .positive('Enter a positive integer')
      )
      .max(1000, 'Invalid parameters'),
    user_types: z.array(z.string()).max(6, 'Invalid parameters'),
    min_usd: money,
    max_usd: money,
  })
  .superRefine((scene, context) => {
    if (scene.business === 'team_topup') {
      if (scene.site_scope !== 'main') {
        context.addIssue({
          code: 'custom',
          path: ['site_scope'],
          message: 'Invalid parameters',
        })
      }
      if (scene.site_ids.length > 0) {
        context.addIssue({
          code: 'custom',
          path: ['site_ids'],
          message: 'Invalid parameters',
        })
      }
    }
    if (scene.business === 'owner_margin_topup') {
      if (scene.site_scope !== 'all_sites' && scene.site_scope !== 'selected') {
        context.addIssue({
          code: 'custom',
          path: ['site_scope'],
          message: 'Invalid parameters',
        })
      }
      if (
        scene.user_types.length !== 1 ||
        scene.user_types[0] !== 'site_owner'
      ) {
        context.addIssue({
          code: 'custom',
          path: ['user_types'],
          message: 'Invalid parameters',
        })
      }
    }
    if (
      Number(scene.max_usd) > 0 &&
      Number(scene.min_usd) > Number(scene.max_usd)
    ) {
      context.addIssue({
        code: 'custom',
        path: ['max_usd'],
        message: 'Invalid parameters',
      })
    }
    if (scene.site_scope === 'selected' && scene.site_ids.length === 0) {
      context.addIssue({
        code: 'custom',
        path: ['site_ids'],
        message: 'Required',
      })
    }
  })

export type PaymentSceneFormValues = z.infer<typeof paymentSceneSchema>

export const sceneAudienceLabels = {
  all: 'All sites and main site',
  main: 'Main site',
  all_sites: 'All sub-sites',
  selected: 'Selected sites',
} as const
export const sceneUserLabels = {
  main_customer: 'Main-site customers',
  site_customer: 'Site customers',
  site_owner: 'Site owners',
  affiliate: 'Affiliates',
  admin: 'Administrators',
  root: 'Root',
} as const
