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

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  getPaymentScenes,
  paymentScenesKey,
  savePaymentScenes,
  type PaymentSceneConfig,
} from './api'
import { PaymentSceneEditor } from './scene-editor'
import { sceneBusinessLabels } from './schema'

function SceneManagement(props: { userId: number }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [editing, setEditing] = useState<{
    config: PaymentSceneConfig
    index: number
  } | null>(null)
  const [removing, setRemoving] = useState<{
    config: PaymentSceneConfig
    index: number
  } | null>(null)
  const query = useQuery({
    queryKey: [...paymentScenesKey, props.userId, window.location.host],
    queryFn: getPaymentScenes,
  })
  const remove = useMutation({
    mutationFn: (target: { config: PaymentSceneConfig; index: number }) => {
      const user = useAuthStore.getState().auth.user
      if (user?.id !== props.userId || user.role < ROLE.SUPER_ADMIN) {
        throw new Error(t('Unauthorized, insufficient privileges'))
      }
      return savePaymentScenes({
        ...target.config,
        scenes: target.config.scenes.filter(
          (_, index) => index !== target.index
        ),
      })
    },
    onSuccess: () => {
      setRemoving(null)
      void client.invalidateQueries({ queryKey: paymentScenesKey })
      void client.invalidateQueries({ queryKey: ['adty-topup'] })
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <ContentSection
      className='border-border border-b pb-6'
      title={t('Payment scenes')}
      description={t(
        'Control where each payment method can be used. Empty restrictions inherit the selected providers.'
      )}
      actions={
        <>
          <Button
            type='button'
            size='sm'
            variant='brand'
            disabled={!query.data || query.isError}
            onClick={() =>
              query.data && setEditing({ config: query.data, index: -1 })
            }
          >
            {t('New payment scene')}
          </Button>
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {t('Refresh')}
          </Button>
        </>
      }
    >
      {query.isLoading && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.data && (
        <StaticDataTable
          tableProps={{ 'aria-label': t('Payment scenes') }}
          data={query.data.scenes}
          getRowKey={(scene) => scene.key}
          emptyContent={
            <EmptyState className='min-h-24' title={t('No payment scenes')} />
          }
          columns={[
            {
              id: 'name',
              header: t('Name'),
              cell: (scene) => (
                <div className='flex min-w-0 flex-col'>
                  <span className='font-medium'>{t(scene.name)}</span>
                  <code className='text-dim font-mono text-xs'>
                    {scene.key}
                  </code>
                </div>
              ),
            },
            {
              id: 'business',
              header: t('Business'),
              cell: (scene) =>
                t(sceneBusinessLabels[scene.business] ?? scene.business),
            },
            {
              id: 'status',
              header: t('Status'),
              cell: (scene) => (
                <StatusBadge
                  variant={scene.enabled ? 'success' : 'neutral'}
                  copyable={false}
                  label={t(scene.enabled ? 'Enabled' : 'Disabled')}
                />
              ),
            },
            {
              id: 'actions',
              header: t('Actions'),
              className: 'text-right',
              cellClassName: 'text-right',
              cell: (scene, index) => (
                <div className='flex justify-end gap-1.5'>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    aria-label={t('Edit payment scene {{name}}', {
                      name: t(scene.name),
                    })}
                    onClick={() =>
                      query.data && setEditing({ config: query.data, index })
                    }
                  >
                    {t('Edit')}
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    aria-label={t('Delete payment scene {{name}}', {
                      name: t(scene.name),
                    })}
                    onClick={() =>
                      query.data && setRemoving({ config: query.data, index })
                    }
                  >
                    {t('Delete')}
                  </Button>
                </div>
              ),
            },
          ]}
        />
      )}
      {editing && (
        <PaymentSceneEditor
          config={editing.config}
          index={editing.index}
          userId={props.userId}
          onClose={() => setEditing(null)}
        />
      )}
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(open) => !open && !remove.isPending && setRemoving(null)}
        title={t('Delete payment scene')}
        desc={t(
          'New orders will use these rules. Existing orders keep their original settings.'
        )}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => removing && remove.mutate(removing)}
      />
    </ContentSection>
  )
}

export function PaymentSceneManager() {
  const user = useAuthStore((state) => state.auth.user)
  if (!user || user.role < ROLE.SUPER_ADMIN || user.staff_site_id) return null
  return <SceneManagement key={user.id} userId={user.id} />
}
