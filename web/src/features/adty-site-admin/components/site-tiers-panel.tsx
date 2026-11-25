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
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  getSiteTiers,
  saveSiteTier,
  siteAdminKeys,
  type SiteTier,
} from '../api'
import { SwitchRow } from './setting-fields'

type Draft = {
  code: string
  name: string
  min_principal_usd: string
  cost_factor: string
  deposit_usd: string
  sort_order: string
  enabled: boolean
}

const EMPTY_DRAFT: Draft = {
  code: '',
  name: '',
  min_principal_usd: '0',
  cost_factor: '',
  deposit_usd: '0',
  sort_order: '0',
  enabled: true,
}

const DECIMAL = /^\d+(\.\d+)?$/
const INTEGER = /^\d+$/

function draftOf(tier: SiteTier): Draft {
  return {
    code: tier.code,
    name: tier.name,
    min_principal_usd: tier.min_principal_usd,
    cost_factor: tier.cost_factor ?? '',
    deposit_usd: tier.deposit_usd,
    sort_order: String(tier.sort_order),
    enabled: tier.enabled,
  }
}

function draftReady(draft: Draft) {
  const code = draft.code.trim()
  const discount = draft.cost_factor.trim()
  return (
    code !== '' &&
    [...code].length <= 32 &&
    draft.name.trim() !== '' &&
    INTEGER.test(draft.sort_order.trim()) &&
    [draft.min_principal_usd, draft.deposit_usd].every((value) =>
      DECIMAL.test(value.trim())
    ) &&
    DECIMAL.test(discount) &&
    Number(discount) > 0
  )
}

/**
 * Root defines site levels (adty_site_tiers). The hourly evaluation gives each
 * automatic site the highest enabled level whose cumulative net customer
 * top-up principal threshold is met. Levels are saved by code and never
 * deleted, so a level still assigned to a site cannot disappear; disable it
 * instead.
 */
export function SiteTiersPanel() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const [editing, setEditing] = useState<{
    draft: Draft
    isNew: boolean
  } | null>(null)
  const query = useQuery({
    queryKey: siteAdminKeys.list('site-tiers', {}),
    queryFn: getSiteTiers,
    enabled: isRoot,
  })
  const save = useMutation({
    mutationFn: ({ draft, isNew }: { draft: Draft; isNew: boolean }) =>
      saveSiteTier(
        {
          code: draft.code.trim(),
          name: draft.name.trim(),
          min_effective_users: 0,
          min_principal_usd: draft.min_principal_usd.trim(),
          min_consumption_usd: '0',
          cost_factor: draft.cost_factor.trim(),
          deposit_usd: draft.deposit_usd.trim(),
          sort_order: Number(draft.sort_order.trim()),
          enabled: draft.enabled,
        },
        isNew
      ),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      setEditing(null)
      void client.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  if (!isRoot) return null
  const rows = Array.isArray(query.data) ? query.data : []
  const draft = editing?.draft
  const setDraft = (patch: Partial<Draft>) =>
    setEditing((current) =>
      current ? { ...current, draft: { ...current.draft, ...patch } } : current
    )
  const field = (
    key: keyof Omit<Draft, 'enabled'>,
    label: string,
    options: { decimal?: boolean; readOnly?: boolean; hint?: string } = {}
  ) => {
    const hintText =
      key === 'code'
        ? t('Level codes can contain up to 32 characters.')
        : options.hint
    const hintId = hintText ? `site-tier-${key}-hint` : undefined
    return (
      <div className='flex flex-col gap-1'>
        <Label htmlFor={`site-tier-${key}`}>{label}</Label>
        <Input
          id={`site-tier-${key}`}
          value={draft?.[key] ?? ''}
          readOnly={options.readOnly}
          aria-describedby={hintId}
          aria-invalid={
            key === 'code' && [...(draft?.code.trim() ?? '')].length > 32
          }
          inputMode={options.decimal ? 'decimal' : undefined}
          onChange={(event) => setDraft({ [key]: event.target.value })}
        />
        {hintText && (
          <p id={hintId} className='text-muted-foreground text-xs'>
            {hintText}
          </p>
        )}
      </div>
    )
  }

  return (
    <ContentSection
      title={t('Site levels')}
      description={t(
        'Automatic sites get the highest enabled level whose cumulative net customer top-up principal threshold is met. Levels cannot be deleted; disable a level instead.'
      )}
      actions={
        <Button
          type='button'
          variant='outline'
          onClick={() => setEditing({ draft: EMPTY_DRAFT, isNew: true })}
        >
          {t('Add level')}
        </Button>
      }
    >
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.isPending ? (
        <LoadingState />
      ) : (
        <StaticDataTable
          tableClassName='min-w-[860px]'
          tableProps={{ 'aria-label': t('Site levels') }}
          data={rows}
          getRowKey={(row) => row.code}
          emptyContent={
            <EmptyState className='min-h-24' title={t('No site levels')} />
          }
          columns={[
            {
              id: 'level',
              header: t('Level'),
              cell: (row) => (
                <div className='flex flex-col'>
                  <span className='font-medium'>{row.name}</span>
                  <code className='text-muted-foreground text-xs'>
                    {row.code}
                  </code>
                </div>
              ),
            },
            {
              id: 'thresholds',
              header: t('Upgrade threshold'),
              cell: (row) => (
                <div className='flex flex-col text-xs tabular-nums'>
                  <span>
                    {t('Customer net top-up principal')} ≥{' '}
                    {row.min_principal_usd} USD
                  </span>
                </div>
              ),
            },
            {
              id: 'cost',
              header: t('Supply discount'),
              cellClassName: 'tabular-nums',
              cell: (row) => row.cost_factor ?? '-',
            },
            {
              id: 'deposit',
              header: t('Margin requirement'),
              cellClassName: 'tabular-nums',
              cell: (row) => `${row.deposit_usd} USD`,
            },
            {
              id: 'status',
              header: t('Status'),
              cell: (row) => (
                <StatusBadge
                  label={row.enabled ? t('Enabled') : t('Disabled')}
                  variant={row.enabled ? 'success' : 'neutral'}
                  copyable={false}
                />
              ),
            },
            {
              id: 'actions',
              header: t('Actions'),
              className: 'text-right',
              cellClassName: 'text-right',
              cell: (row) => (
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() =>
                    setEditing({ draft: draftOf(row), isNew: false })
                  }
                >
                  {t('Edit')}
                </Button>
              ),
            },
          ]}
        />
      )}
      <Dialog
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open && !save.isPending) setEditing(null)
        }}
        title={editing?.isNew ? t('Add level') : t('Edit level')}
        description={t(
          'Saving updates the supply cost factor and margin requirement for sites on this level.'
        )}
        contentHeight='auto'
        bodyClassName='grid gap-3 sm:grid-cols-2'
        footer={
          <>
            <Button
              variant='outline'
              disabled={save.isPending}
              onClick={() => setEditing(null)}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='brand'
              disabled={!draft || !draftReady(draft) || save.isPending}
              onClick={() => editing && save.mutate(editing)}
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        {field('code', t('Level code'), { readOnly: !editing?.isNew })}
        {field('name', t('Level name'))}
        {field('sort_order', t('Sort order'))}
        <div className='flex items-end'>
          <div className='w-full'>
            <SwitchRow
              label={t('Enabled')}
              checked={draft?.enabled === true}
              onCheckedChange={(value) => setDraft({ enabled: value })}
            />
          </div>
        </div>
        <div className='border-t pt-3 sm:col-span-2'>
          <h3 className='text-sm font-medium'>
            {t('Upgrade threshold (cumulative customer net top-up)')}
          </h3>
        </div>
        <div className='sm:col-span-2'>
          {field(
            'min_principal_usd',
            t('Minimum customer net top-up principal (USD)'),
            {
              decimal: true,
              hint: t(
                'Customer top-up principal minus refunds; enter 0 for no threshold (not owner margin).'
              ),
            }
          )}
        </div>
        <div className='border-t pt-3 sm:col-span-2'>
          <h3 className='text-sm font-medium'>
            {t('Level supply discount and owner margin')}
          </h3>
        </div>
        {field('cost_factor', t('Supply discount (required, greater than 0)'), {
          decimal: true,
          hint: t(
            'Supply cost = main site standard price × supply discount (e.g. 0.8 = 80%).'
          ),
        })}
        {field('deposit_usd', t('Owner margin requirement (USD)'), {
          decimal: true,
          hint: t(
            'Deposit target required in the owner margin wallet; enter 0 if none.'
          ),
        })}
      </Dialog>
    </ContentSection>
  )
}
