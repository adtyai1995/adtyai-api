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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
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

import {
  getAdminSiteInviteBatchPolicy,
  saveAdminSiteInviteBatchPolicy,
  siteAdminKeys,
} from '../api'

export function SiteReferralCapPanel() {
  const { t } = useTranslation()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const client = useQueryClient()
  const [thresholdDraft, setBatchThreshold] = useState<string | null>(null)
  const [windowDraft, setBatchWindow] = useState<string | null>(null)
  const batch = useQuery({
    queryKey: siteAdminKeys.list('site-invite-batch-policy', {}),
    queryFn: getAdminSiteInviteBatchPolicy,
    enabled: isRoot,
  })
  const batchThreshold = thresholdDraft ?? batch.data?.threshold ?? ''
  const batchWindow = windowDraft ?? batch.data?.window_hours ?? ''
  const saveBatch = useMutation({
    mutationFn: (disable: boolean) =>
      saveAdminSiteInviteBatchPolicy(
        disable ? '' : batchThreshold.trim(),
        disable ? '' : batchWindow.trim()
      ),
    onSuccess: () => {
      setBatchThreshold(null)
      setBatchWindow(null)
      void client.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  if (!isRoot) return null
  return (
    <SettingsSection
      title={t('Batch registration review')}
      description={t('Same-IP binds at this count enter review.')}
    >
      <div className='flex flex-col gap-5'>
        {batch.isError && <ErrorState onRetry={() => void batch.refetch()} />}
        {batch.data && (
          <p className='text-dim text-[13px] tabular-nums'>
            {batch.data.configured
              ? `${batch.data.threshold} / ${batch.data.window_hours}`
              : t('Not configured')}
          </p>
        )}
        <div className='grid max-w-md gap-4 sm:grid-cols-2'>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='site-batch-threshold'>{t('Count')}</Label>
            <Input
              id='site-batch-threshold'
              className='sm:max-w-44'
              value={batchThreshold}
              inputMode='numeric'
              autoComplete='off'
              onChange={(event) => setBatchThreshold(event.target.value)}
            />
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='site-batch-window'>{t('Window hours')}</Label>
            <Input
              id='site-batch-window'
              className='sm:max-w-44'
              value={batchWindow}
              inputMode='numeric'
              autoComplete='off'
              onChange={(event) => setBatchWindow(event.target.value)}
            />
          </div>
        </div>
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            type='button'
            variant='brand'
            disabled={
              saveBatch.isPending ||
              !batch.isSuccess ||
              !batchThreshold.trim() ||
              !batchWindow.trim() ||
              (batchThreshold.trim() === batch.data?.threshold &&
                batchWindow.trim() === batch.data?.window_hours)
            }
            onClick={() => saveBatch.mutate(false)}
          >
            {t('Save')}
          </Button>
          {batch.isSuccess && batch.data.configured && (
            <Button
              type='button'
              variant='outline'
              disabled={saveBatch.isPending}
              onClick={() => saveBatch.mutate(true)}
            >
              {t('Disable rule')}
            </Button>
          )}
        </div>
      </div>
    </SettingsSection>
  )
}
