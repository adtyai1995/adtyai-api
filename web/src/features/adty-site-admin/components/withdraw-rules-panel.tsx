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
import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getAdminWithdrawRules, saveAdminWithdrawRules } from '../api'

/** Root enters every withdrawal figure. The form does not suggest a minimum, fee, or rate. */
export function WithdrawRulesPanel() {
  const { t } = useTranslation()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const current = useQuery({
    queryKey: ['admin-site-withdraw-rules'],
    queryFn: getAdminWithdrawRules,
    enabled: isRoot,
  })
  const [minUsd, setMinUsd] = useState<string>()
  const [feeUsd, setFeeUsd] = useState<string>()
  const [usdtRate, setUsdtRate] = useState<string>()
  const save = useMutation({
    mutationFn: () =>
      saveAdminWithdrawRules({
        min_usd: (minUsd ?? current.data?.min_usd ?? '').trim(),
        fee_usd: (feeUsd ?? current.data?.fee_usd ?? '').trim(),
        usdt_usd_rate: (usdtRate ?? current.data?.usdt_usd_rate ?? '').trim(),
      }),
    onSuccess: async () => {
      await current.refetch()
      setMinUsd(undefined)
      setFeeUsd(undefined)
      setUsdtRate(undefined)
    },
    onError: (error) => handleServerError(error),
  })
  if (!isRoot) return null
  if (current.isError) {
    return <ErrorState onRetry={() => void current.refetch()} />
  }
  const ready =
    current.isSuccess &&
    [
      minUsd ?? current.data.min_usd,
      feeUsd ?? current.data.fee_usd,
      usdtRate ?? current.data.usdt_usd_rate,
    ].every((value) => !!value?.trim())
  return (
    <SettingsSection
      title={t('Withdrawal rules')}
      description={`${t('These figures apply only to new owner withdrawal requests.')} ${t(
        'Site owner earnings withdrawals are paid in USDT (TRC20 / BEP20 / Polygon / X Layer).'
      )}`}
    >
      <div className='flex flex-col gap-5'>
        <div className='grid max-w-2xl gap-4 sm:grid-cols-3'>
          <div className='flex flex-col gap-2'>
            <Label htmlFor='withdraw-min'>
              {t('Minimum withdrawal (USD)')}
            </Label>
            <Input
              id='withdraw-min'
              className='sm:max-w-48'
              value={minUsd ?? current.data?.min_usd ?? ''}
              inputMode='decimal'
              onChange={(event) => setMinUsd(event.target.value)}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <Label htmlFor='withdraw-fee'>{t('Fee (USD)')}</Label>
            <Input
              id='withdraw-fee'
              className='sm:max-w-48'
              value={feeUsd ?? current.data?.fee_usd ?? ''}
              inputMode='decimal'
              onChange={(event) => setFeeUsd(event.target.value)}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <Label htmlFor='withdraw-usdt'>{t('USDT/USD rate')}</Label>
            <Input
              id='withdraw-usdt'
              className='sm:max-w-48'
              value={usdtRate ?? current.data?.usdt_usd_rate ?? ''}
              inputMode='decimal'
              onChange={(event) => setUsdtRate(event.target.value)}
            />
          </div>
        </div>
        <div>
          <Button
            type='button'
            disabled={save.isPending || !ready}
            onClick={() => save.mutate()}
          >
            {t('Save')}
          </Button>
        </div>
      </div>
    </SettingsSection>
  )
}
