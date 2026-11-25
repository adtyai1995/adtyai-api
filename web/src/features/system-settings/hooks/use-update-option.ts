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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'

import { siteAdminKeys } from '@/features/adty-site-admin/api'
import { adtySiteQueryKey } from '@/features/adty-site/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { updateSystemOption } from '../api'
import type {
  SystemOption,
  SystemOptionsResponse,
  UpdateOptionRequest,
} from '../types'

function isSensitiveOptionKey(key: string): boolean {
  if (key === 'TurnstileSiteKey') {
    return false
  }
  return (
    key.endsWith('Token') ||
    key.endsWith('Secret') ||
    key.endsWith('Key') ||
    key.endsWith('secret') ||
    key.endsWith('api_key')
  )
}

function upsertSystemOption(
  options: SystemOption[],
  key: string,
  value: string
): SystemOption[] {
  const index = options.findIndex((item) => item.key === key)
  if (index === -1) {
    return [...options, { key, value }]
  }
  if (options[index]?.value === value) {
    return options
  }
  const next = [...options]
  next[index] = { key, value }
  return next
}

// Configuration keys that require status refresh
const STATUS_RELATED_KEYS = new Set([
  'HeaderNavModules',
  'SidebarModulesAdmin',
  'Notice',
  'SystemName',
  'Logo',
  'Footer',
  'HomePageContent',
  'About',
  'TurnstileCheckEnabled',
  'TurnstileSiteKey',
  'EmailVerificationEnabled',
  'RegisterEnabled',
  'PasswordLoginEnabled',
  'PasswordRegisterEnabled',
  'EmailDomainRestrictionEnabled',
  'EmailDomainWhitelist',
  'TopUpLink',
  'QuotaForNewUser',
  'QuotaForInviter',
  'QuotaForInvitee',
  'legal.user_agreement',
  'legal.privacy_policy',
  'adty_legal_setting.user_agreement',
  'adty_legal_setting.privacy_policy',
  'adty_legal_setting.site_customer_terms_version',
  'adty_site_setting.refund_policy_enabled',
  'LogConsumeEnabled',
  'log_setting.user_export_enabled',
  'log_setting.max_range_days',
  'QuotaPerUnit',
  'USDExchangeRate',
  'Price',
  'DisplayInCurrencyEnabled',
  'DisplayTokenStatEnabled',
  'general_setting.docs_link',
  'general_setting.quota_display_type',
  'general_setting.custom_currency_symbol',
  'general_setting.custom_currency_exchange_rate',
  'console_setting.api_info_enabled',
  'console_setting.api_info',
  'console_setting.uptime_kuma_enabled',
  'console_setting.uptime_kuma_groups',
  'console_setting.announcements_enabled',
  'console_setting.faq_enabled',
  'console_setting.faq',
  'Chats',
  'BatchUpdateEnabled',
  'DrawingEnabled',
  'TaskEnabled',
  'MjNotifyEnabled',
  'DataExportEnabled',
  'DataExportDefaultTime',
  'DemoSiteEnabled',
  'SelfUseModeEnabled',
  'oidc.display_name',
  'ServerAddress',
  'adty_site_setting.api_base_url',
  'DefaultCollapseSidebar',
  'adty_bepusdt.enabled',
  'adty_bepusdt.base_url',
  'adty_bepusdt.secret',
  'adty_bepusdt.trade_types',
  'QuotaRemindEnabled',
  'QuotaRemindThreshold',
])

export function useUpdateOption() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: UpdateOptionRequest) =>
      requireServerSuccess(await updateSystemOption(request)),
    onSuccess: (data, variables) => {
      if (data.success) {
        const stringValue = String(variables.value)
        queryClient.setQueryData<SystemOptionsResponse>(
          ['system-options'],
          (current) => {
            if (!current || !Array.isArray(current.data)) return current
            let nextData = current.data
            if (!isSensitiveOptionKey(variables.key)) {
              nextData = upsertSystemOption(
                nextData,
                variables.key,
                stringValue
              )
            }
            if (
              variables.key === 'ResendApiKey' &&
              stringValue.trim() !== ''
            ) {
              nextData = upsertSystemOption(
                nextData,
                'ResendKeyConfigured',
                'true'
              )
            }
            if (variables.key === 'TurnstileSecretKey') {
              nextData = upsertSystemOption(
                nextData,
                'TurnstileSecretConfigured',
                stringValue.trim() !== '' ? 'true' : 'false'
              )
            }
            if (nextData === current.data) return current
            return { ...current, data: nextData }
          }
        )

        // Always refresh system-options
        // Mark the options stale for the next settings load without refetching
        // the active settings form. A refetch here replaces local drafts while
        // a multi-field form is still saving its remaining changes.
        queryClient.invalidateQueries({
          queryKey: ['system-options'],
          refetchType: 'none',
        })

        if (
          variables.key.startsWith('adty_site_setting.') ||
          variables.key === 'MinTopUp' ||
          variables.key === 'ServerAddress'
        ) {
          queryClient.invalidateQueries({ queryKey: adtySiteQueryKey })
        }
        if (variables.key === 'ResendApiKey') {
          queryClient.invalidateQueries({ queryKey: siteAdminKeys.settings })
        }

        // If updating frontend-display-related config, also refresh status
        if (STATUS_RELATED_KEYS.has(variables.key)) {
          queryClient.invalidateQueries({ queryKey: ['status'] })
          try {
            window.localStorage.removeItem('status')
          } catch {
            /* empty */
          }
        }
        if (variables.key === 'Notice') {
          queryClient.invalidateQueries({ queryKey: ['notice'] })
          queryClient.invalidateQueries({ queryKey: ['adty-notifications'] })
        }
        if (
          variables.key.startsWith('adty_legal_setting.') ||
          variables.key.startsWith('legal.')
        ) {
          queryClient.invalidateQueries({ queryKey: ['legal'] })
        }

        toast.success(i18next.t('Setting updated successfully'))
      } else {
        handleServerError(data, i18next.t('Failed to update setting'))
      }
    },
    onError: (error: Error) => {
      handleServerError(error, i18next.t('Failed to update setting'))
    },
  })
}
