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
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'

import type { SystemOption, SystemOptionsResponse } from '../types'
import { savePolicyConfig } from './api'

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

export function useSavePolicy() {
  const client = useQueryClient()
  const { t } = useTranslation()
  return useMutation({
    mutationFn: savePolicyConfig,
    onSuccess: (data, options) => {
      client.setQueryData(['request-policy'], data)
      client.setQueryData<SystemOptionsResponse>(
        ['system-options'],
        (current) => {
          if (!current || !Array.isArray(current.data)) return current
          let nextData = current.data
          for (const [key, value] of Object.entries(options)) {
            nextData = upsertSystemOption(nextData, key, String(value))
          }
          if (nextData === current.data) return current
          return { ...current, data: nextData }
        }
      )
      void client.invalidateQueries({
        queryKey: ['system-options'],
        refetchType: 'none',
      })
      void client.invalidateQueries({ queryKey: ['channel-ops'] })
      if ('channel_affinity_setting.rules' in options) {
        void client.invalidateQueries({ queryKey: ['channel-affinity-cache'] })
      }
      toast.success(t('Saved successfully'))
    },
    onError: (error) => handleServerError(error),
    meta: { errorToast: false },
  })
}
