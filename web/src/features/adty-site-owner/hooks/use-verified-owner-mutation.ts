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

import type { SiteOwnerAction } from '@/features/auth/secure-verification'
import { submitStaffRequest, type StaffInput } from '@/features/adty-site-staff/api'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'

import { siteOwnerKeys } from '../api'
import { useOwnerVerification } from '../components/owner-verification'

/**
 * A sensitive owner write: verify first (single-use proof bound to the action
 * and target), then call the API with the proof. Cancelling the verification
 * is not an error. On success the owner center queries are refreshed.
 */
export function useVerifiedOwnerMutation<TVars>(options: {
  action: SiteOwnerAction
  target: (vars: TVars) => number
  run?: (vars: TVars, proof: string) => Promise<unknown>
  staffRequest?: (vars: TVars) => StaffInput
  successMessage?: string
  onSuccess?: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const owner = useOwnerVerification()
  return useMutation({
    mutationFn: async (vars: TVars) => {
      const target = options.target(vars)
      const request = options.staffRequest && structuredClone(options.staffRequest(vars))
      const proof = await owner.verify(options.action, target, request)
      if (!proof) return false
      if (request) await submitStaffRequest(options.action,target,request,proof)
      else if (options.run) await options.run(vars, proof)
      else throw new Error('Missing verified action')
      return true
    },
    onSuccess: (done) => {
      if (!done) return
      toast.success(options.successMessage ?? t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      if (options.action === 'pricing') {
        void queryClient.invalidateQueries({ queryKey: ['pricing'] })
      }
      void invalidateSidebarBadges(queryClient)
      options.onSuccess?.()
    },
    onError: (err) => handleServerError(err),
  })
}
