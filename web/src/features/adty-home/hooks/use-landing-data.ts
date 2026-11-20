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
import { useQuery } from '@tanstack/react-query'

import {
  getReferralPublicSettings,
  referralQueryKeys,
} from '@/features/referral/api'

export type LandingData = {
  affiliate: { open: boolean; rate: number | null }
}

/** Only the referral banner needs server data in the default landing sections. */
export function useLandingData(): LandingData {
  const referral = useQuery({
    queryKey: referralQueryKeys.publicSettings,
    queryFn: getReferralPublicSettings,
    staleTime: 5 * 60 * 1000,
    meta: { errorToast: false },
  })
  const settings = referral.data
  return {
    affiliate: {
      open: settings?.enabled === true,
      rate: settings?.enabled ? settings.default_rate : null,
    },
  }
}
