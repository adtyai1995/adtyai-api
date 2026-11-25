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
export function useVerifiedOwnerMutation<TVars, TResult = unknown>(options: {
  action: SiteOwnerAction
  target: (vars: TVars) => number
  request?: (vars: TVars) => object
  run?: (vars: TVars, proof: string) => Promise<TResult>
  staffRequest?: (vars: TVars) => StaffInput
  successMessage?:
    | string
    | null
    | ((result: TResult | undefined) => string | null | undefined | false)
  onSuccess?: (result: TResult | undefined) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const owner = useOwnerVerification()
  return useMutation({
    mutationFn: async (
      vars: TVars
    ): Promise<
      { done: false } | { done: true; result: TResult | undefined }
    > => {
      const target = options.target(vars)
      const staffRequest =
        options.staffRequest && structuredClone(options.staffRequest(vars))
      const requestPayload =
        staffRequest ??
        (options.request ? structuredClone(options.request(vars)) : undefined)
      const proof = await owner.verify(options.action, target, requestPayload)
      if (!proof) return { done: false }
      if (staffRequest) {
        await submitStaffRequest(options.action, target, staffRequest, proof)
        return { done: true, result: undefined }
      }
      if (options.run) {
        const result = await options.run(vars, proof)
        return { done: true, result }
      }
      throw new Error('Missing verified action')
    },
    onSuccess: (outcome) => {
      if (!outcome.done) return
      let message: string | null | undefined | false
      if (typeof options.successMessage === 'function') {
        message = options.successMessage(outcome.result)
      } else if (options.successMessage === undefined) {
        message = t('Saved successfully')
      } else {
        message = options.successMessage
      }
      if (message) {
        toast.success(message)
      }
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      if (options.action === 'pricing') {
        void queryClient.invalidateQueries({ queryKey: ['pricing'] })
      }
      void invalidateSidebarBadges(queryClient)
      options.onSuccess?.(outcome.result)
    },
    onError: (err) => handleServerError(err),
  })
}
