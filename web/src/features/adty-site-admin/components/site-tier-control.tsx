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
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  adjustAdminSiteTierPrincipal,
  getAdminSiteTierPrincipal,
  getSiteTiers,
  setSiteTierAssignment,
  siteAdminKeys,
} from '../api'
import type {
  AdminSite,
  TierPrincipalAdjustment,
  TierPrincipalAdjustMode,
} from '../types'

const AUTOMATIC = ''
const HISTORY_PAGE_SIZE = 10

function newAdjustIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `tier-adj-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

/**
 * Root pins one site to an enabled level or hands it back to the hourly
 * evaluation, and can manually adjust the tier-evaluation cumulative customer
 * recharge total (e.g. for offline large transfers).
 */
export function SiteTierControl(props: { site: AdminSite }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const pinned = props.site.tier_mode === 'manual'
  const current = pinned ? (props.site.tier_code ?? '') : AUTOMATIC
  const [choice, setChoice] = useState(current)
  const [historyPage, setHistoryPage] = useState(1)
  const [adjustMode, setAdjustMode] =
    useState<TierPrincipalAdjustMode>('delta')
  const [adjustAmount, setAdjustAmount] = useState('')
  const [adjustReason, setAdjustReason] = useState('')
  const adjustIntentRef = useRef<{ payload: string; key: string } | null>(null)
  const tiers = useQuery({
    queryKey: siteAdminKeys.list('site-tiers', {}),
    queryFn: getSiteTiers,
    enabled: isRoot,
  })
  const principal = useQuery({
    queryKey: siteAdminKeys.list('site-tier-principal', {
      siteId: props.site.id,
      page: historyPage,
    }),
    queryFn: () =>
      getAdminSiteTierPrincipal(props.site.id, historyPage, HISTORY_PAGE_SIZE),
    enabled: isRoot,
  })
  const save = useMutation({
    mutationFn: () =>
      choice === AUTOMATIC
        ? setSiteTierAssignment(props.site.id, '', 'auto')
        : setSiteTierAssignment(props.site.id, choice, 'manual'),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  const adjust = useMutation({
    mutationFn: () => {
      const payload = {
        mode: adjustMode,
        amount_usd: adjustAmount.trim(),
        reason: adjustReason.trim(),
      }
      const serialized = `${props.site.id}:${JSON.stringify(payload)}`
      if (adjustIntentRef.current?.payload !== serialized) {
        adjustIntentRef.current = {
          payload: serialized,
          key: newAdjustIdempotencyKey(),
        }
      }
      return adjustAdminSiteTierPrincipal(
        props.site.id,
        payload,
        adjustIntentRef.current.key
      )
    },
    onSuccess: () => {
      adjustIntentRef.current = null
      setAdjustAmount('')
      setAdjustReason('')
      setHistoryPage(1)
      toast.success(t('Tier cumulative recharge updated'))
      void client.invalidateQueries({ queryKey: siteAdminKeys.all })
    },
    onError: (error) => handleServerError(error),
  })
  if (!isRoot) return null
  const enabled = (Array.isArray(tiers.data) ? tiers.data : []).filter(
    (tier) => tier.enabled
  )
  const currentName =
    (Array.isArray(tiers.data) ? tiers.data : []).find(
      (tier) => tier.code === props.site.tier_code
    )?.name ?? props.site.tier_code
  const siteStatus = String(props.site.status)
  const isSiteLocked = siteStatus === 'closed' || siteStatus === 'deleting'
  const parsedAmount = Number(adjustAmount.trim())
  const validAdjustAmount =
    adjustAmount.trim() !== '' &&
    Number.isFinite(parsedAmount) &&
    (adjustMode === 'delta' ? parsedAmount !== 0 : parsedAmount >= 0)
  const canAdjust =
    !isSiteLocked &&
    validAdjustAmount &&
    adjustReason.trim() !== '' &&
    !adjust.isPending
  const historyRows = Array.isArray(principal.data?.items)
    ? principal.data.items
    : []

  return (
    <section className='flex flex-col gap-4'>
      <div className='flex flex-col gap-2'>
        <h3 className='text-sm font-medium'>{t('Site level')}</h3>
        <p className='text-muted-foreground text-sm'>
          {currentName
            ? `${t('Current level')}: ${currentName}`
            : t('No level yet; default terms apply.')}{' '}
          ·{' '}
          {pinned
            ? t('Pinned by Root')
            : t('Evaluated automatically every hour')}
        </p>
        {tiers.isError && <ErrorState onRetry={() => void tiers.refetch()} />}
        <div className='flex flex-wrap items-end gap-2'>
          <div className='flex flex-col gap-1'>
            <Label htmlFor={`site-tier-${props.site.id}`}>
              {t('Level assignment')}
            </Label>
            <NativeSelect
              id={`site-tier-${props.site.id}`}
              value={choice}
              disabled={isSiteLocked || save.isPending}
              onChange={(event) => setChoice(event.target.value)}
            >
              <NativeSelectOption value={AUTOMATIC}>
                {t('Automatic (by thresholds)')}
              </NativeSelectOption>
              {enabled.map((tier) => (
                <NativeSelectOption key={tier.code} value={tier.code}>
                  {t('Pin to {{name}}', { name: tier.name })}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <Button
            type='button'
            variant='outline'
            disabled={isSiteLocked || save.isPending || choice === current}
            onClick={() => {
              if (!isSiteLocked) save.mutate()
            }}
          >
            {t('Save')}
          </Button>
        </div>
        <p className='text-muted-foreground text-xs'>
          {isSiteLocked
            ? t('Site is closed')
            : t(
                'Switching to automatic immediately matches the site to its qualifying level based on cumulative net customer top-up principal.'
              )}
        </p>
      </div>

      <div className='border-border flex flex-col gap-3 rounded-md border p-3'>
        <div className='flex flex-col gap-1'>
          <h4 className='text-sm font-medium'>
            {t('Tier cumulative customer recharge')}
          </h4>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Excludes all site-owner recharges (margin top-ups, owner self-use API top-ups, and manual balance credits). Root can adjust this figure for offline large transfers without modifying any wallet balance.'
            )}
          </p>
        </div>
        {principal.isPending && <LoadingState />}
        {principal.isError && (
          <ErrorState onRetry={() => void principal.refetch()} />
        )}
        {principal.isSuccess && (
          <dl className='grid grid-cols-1 gap-2 text-sm sm:grid-cols-3'>
            <div className='bg-muted/40 rounded p-2'>
              <dt className='text-muted-foreground text-xs'>
                {t('Online customer net recharge')}
              </dt>
              <dd className='font-medium tabular-nums'>
                {principal.data.online_principal_usd} USD
              </dd>
            </div>
            <div className='bg-muted/40 rounded p-2'>
              <dt className='text-muted-foreground text-xs'>
                {t('Platform manual adjustment')}
              </dt>
              <dd className='font-medium tabular-nums'>
                {principal.data.adjustment_usd} USD
              </dd>
            </div>
            <div className='bg-muted/40 rounded p-2'>
              <dt className='text-muted-foreground text-xs'>
                {t('Effective tier total')}
              </dt>
              <dd className='font-semibold tabular-nums'>
                {principal.data.effective_principal_usd} USD
              </dd>
            </div>
          </dl>
        )}

        {!isSiteLocked && (
          <div className='flex flex-col gap-2'>
            <div className='grid gap-2 sm:grid-cols-3'>
              <div className='flex flex-col gap-1'>
                <Label htmlFor={`tier-principal-mode-${props.site.id}`}>
                  {t('Adjustment mode')}
                </Label>
                <NativeSelect
                  id={`tier-principal-mode-${props.site.id}`}
                  value={adjustMode}
                  onChange={(event) =>
                    setAdjustMode(
                      event.target.value as TierPrincipalAdjustMode
                    )
                  }
                >
                  <NativeSelectOption value='delta'>
                    {t('Add or subtract (USD)')}
                  </NativeSelectOption>
                  <NativeSelectOption value='set_total'>
                    {t('Set target total (USD)')}
                  </NativeSelectOption>
                </NativeSelect>
              </div>
              <div className='flex flex-col gap-1'>
                <Label htmlFor={`tier-principal-amount-${props.site.id}`}>
                  {adjustMode === 'delta'
                    ? t('Delta amount (USD)')
                    : t('Target effective total (USD)')}
                </Label>
                <Input
                  id={`tier-principal-amount-${props.site.id}`}
                  inputMode='decimal'
                  autoComplete='off'
                  placeholder={adjustMode === 'delta' ? '50000' : '100000'}
                  value={adjustAmount}
                  onChange={(event) => setAdjustAmount(event.target.value)}
                />
              </div>
              <div className='flex flex-col gap-1'>
                <Label htmlFor={`tier-principal-reason-${props.site.id}`}>
                  {t('Reason')}
                </Label>
                <Input
                  id={`tier-principal-reason-${props.site.id}`}
                  maxLength={500}
                  value={adjustReason}
                  onChange={(event) => setAdjustReason(event.target.value)}
                />
              </div>
            </div>
            <div>
              <Button
                type='button'
                variant='outline'
                disabled={!canAdjust}
                onClick={() => adjust.mutate()}
              >
                {t('Adjust tier recharge')}
              </Button>
            </div>
          </div>
        )}

        {principal.isSuccess && (
          <div className='flex flex-col gap-2'>
            <h5 className='text-muted-foreground text-xs font-medium'>
              {t('Manual adjustment history')}
            </h5>
            <StaticDataTable
              tableProps={{ 'aria-label': t('Manual adjustment history') }}
              data={historyRows}
              getRowKey={(row: TierPrincipalAdjustment) => String(row.id)}
              emptyContent={
                <EmptyState className='min-h-16' title={t('No data')} />
              }
              columns={[
                {
                  id: 'time',
                  header: t('Time'),
                  cell: (row: TierPrincipalAdjustment) =>
                    formatTimestamp(row.created_at),
                },
                {
                  id: 'mode',
                  header: t('Adjustment mode'),
                  cell: (row: TierPrincipalAdjustment) =>
                    row.mode === 'set_total'
                      ? t('Set target total (USD)')
                      : t('Add or subtract (USD)'),
                },
                {
                  id: 'delta',
                  header: t('Delta amount (USD)'),
                  cell: (row: TierPrincipalAdjustment) => (
                    <span className='tabular-nums'>{row.delta_usd}</span>
                  ),
                },
                {
                  id: 'effective',
                  header: t('Effective tier total'),
                  cell: (row: TierPrincipalAdjustment) => (
                    <span className='tabular-nums'>
                      {row.effective_after_usd}
                    </span>
                  ),
                },
                {
                  id: 'tier',
                  header: t('Site level'),
                  cell: (row: TierPrincipalAdjustment) =>
                    row.tier_before === row.tier_after
                      ? row.tier_after || '—'
                      : `${row.tier_before || '—'} → ${row.tier_after || '—'}`,
                },
                {
                  id: 'reason',
                  header: t('Reason'),
                  cell: (row: TierPrincipalAdjustment) => (
                    <span className='break-words'>{row.reason}</span>
                  ),
                },
              ]}
            />
            <PageSwitcher
              page={historyPage}
              total={Number(principal.data.total) || 0}
              pageSize={HISTORY_PAGE_SIZE}
              onPageChange={setHistoryPage}
            />
          </div>
        )}
      </div>
    </section>
  )
}
