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
import { lazy } from 'react'

import type { ContentSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'

const AdtyLegalSection = lazy(() =>
  import('./adty-legal-section').then((module) => ({
    default: module.AdtyLegalSection,
  }))
)
const AnnouncementsSection = lazy(() =>
  import('./announcements-section').then((module) => ({
    default: module.AnnouncementsSection,
  }))
)
const DashboardSection = lazy(() =>
  import('./dashboard-section').then((module) => ({
    default: module.DashboardSection,
  }))
)
const FAQSection = lazy(() =>
  import('./faq-section').then((module) => ({ default: module.FAQSection }))
)
const UptimeKumaSection = lazy(() =>
  import('./uptime-kuma-section').then((module) => ({
    default: module.UptimeKumaSection,
  }))
)

/**
 * Validate and coerce DataExportDefaultTime to a safe value
 */
function validateDataExportDefaultTime(value: string): 'week' | 'hour' | 'day' {
  if (value === 'week' || value === 'hour' || value === 'day') {
    return value
  }
  // Default to 'hour' if value is unexpected
  return 'hour'
}

const CONTENT_SECTIONS = [
  {
    id: 'dashboard',
    titleKey: 'Data Dashboard',
    build: (settings: ContentSettings) => (
      <DashboardSection
        defaultValues={{
          DataExportEnabled: settings.DataExportEnabled,
          DataExportInterval: settings.DataExportInterval,
          DataExportDefaultTime: validateDataExportDefaultTime(
            settings.DataExportDefaultTime
          ),
        }}
      />
    ),
  },
  {
    id: 'announcements',
    titleKey: 'Announcements',
    build: (settings: ContentSettings) => (
      <AnnouncementsSection
        enabled={settings['console_setting.announcements_enabled']}
        data={settings['console_setting.announcements']}
      />
    ),
  },
  {
    id: 'faq',
    titleKey: 'FAQ',
    build: (settings: ContentSettings) => (
      <FAQSection
        enabled={settings['console_setting.faq_enabled']}
        data={settings['console_setting.faq']}
      />
    ),
  },
  {
    id: 'uptime-kuma',
    titleKey: 'Model Connectivity Monitor',
    build: (settings: ContentSettings) => (
      <UptimeKumaSection
        enabled={settings['console_setting.uptime_kuma_enabled']}
        intervalMinutes={
          settings['console_setting.model_probe_interval_minutes']
        }
        timeoutSeconds={settings['console_setting.model_probe_timeout_seconds']}
        data={settings['console_setting.uptime_kuma_groups']}
      />
    ),
  },
  {
    id: 'adty-legal',
    titleKey: 'Multilingual Legal Pages',
    build: (settings: ContentSettings) => (
      <AdtyLegalSection
        defaultValues={{
          user_agreement: settings['adty_legal_setting.user_agreement'],
          privacy_policy: settings['adty_legal_setting.privacy_policy'],
          refund_policy: settings['adty_legal_setting.refund_policy'],
        }}
      />
    ),
  },
] as const

export type ContentSectionId = (typeof CONTENT_SECTIONS)[number]['id']

const contentRegistry = createSectionRegistry<
  ContentSectionId,
  ContentSettings
>({
  sections: CONTENT_SECTIONS,
  defaultSection: 'announcements',
  basePath: '/system-settings/content',
  urlStyle: 'path',
})

export const CONTENT_SECTION_IDS = contentRegistry.sectionIds
export const CONTENT_DEFAULT_SECTION = contentRegistry.defaultSection
export const getContentSectionNavItems = contentRegistry.getSectionNavItems
export const getContentSectionContent = contentRegistry.getSectionContent
export const getContentSectionMeta = contentRegistry.getSectionMeta
