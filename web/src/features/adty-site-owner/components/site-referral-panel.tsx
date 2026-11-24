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
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'

import {
  bindOwnerSiteInvite,
  getOwnerSiteInvites,
  getOwnerSiteReferral,
  resumeOwnerSiteCoverage,
  saveOwnerSiteReferral,
  saveOwnerSiteReferralEnabled,
  siteOwnerKeys,
  type SiteMarketingField,
  type SiteReferralView,
} from '../api'
import {
  MAX_SITE_REFERRAL_RATE,
  validSiteReferralRate,
} from '../lib/site-owner'

type PolicyField = {
  key: SiteMarketingField
  label: string
  integer?: boolean
  max?: number
  hint?: string
}

function MarketingFields(props: {
  title: string
  description: string
  fields: PolicyField[]
  policy: SiteReferralView
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [draft, setDraft] = useState<
    Partial<Record<SiteMarketingField, string>>
  >({})
  const rateDraft = draft.rate
  const rateInvalid =
    rateDraft !== undefined &&
    rateDraft.trim() !== '' &&
    !validSiteReferralRate(rateDraft)
  const save = useMutation({
    mutationFn: () => saveOwnerSiteReferral(draft),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void invalidateSidebarBadges(client)
      setDraft({})
      toast.success(t('Saved successfully'))
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <form
      className='space-y-3'
      onSubmit={(event) => {
        event.preventDefault()
        if (rateInvalid) return
        save.mutate()
      }}
    >
      <h3 className='text-sm font-medium'>{props.title}</h3>
      <p className='text-muted-foreground text-sm'>{props.description}</p>
      <div className='grid gap-3 sm:grid-cols-3'>
        {props.fields.map(({ key, label, integer, max, hint }) => {
          const currentValue = String(draft[key] ?? props.policy[key] ?? '')
          const isRate = key === 'rate'
          const showRateError = isRate && rateInvalid
          const parsedRate = isRate ? Number(currentValue) : Number.NaN
          const ratePctHint =
            isRate &&
            currentValue.trim() !== '' &&
            !showRateError &&
            Number.isFinite(parsedRate)
              ? `${Number((parsedRate * 100).toFixed(4))}%`
              : ''
          return (
            <div key={key} className='space-y-1'>
              <Label htmlFor={`site-policy-${key}`}>{label}</Label>
              <Input
                id={`site-policy-${key}`}
                type='number'
                min='0'
                max={max}
                step={integer ? '1' : 'any'}
                aria-invalid={showRateError}
                value={currentValue}
                onChange={(event) =>
                  setDraft({ ...draft, [key]: event.target.value })
                }
              />
              {showRateError ? (
                <p role='alert' className='text-destructive text-xs'>
                  {t(
                    'Commission rate must be between 0 and 0.30 (0% to 30%, up to 6 decimal places).'
                  )}
                </p>
              ) : (
                hint && (
                  <p className='text-muted-foreground text-xs'>
                    {hint}
                    {ratePctHint ? ` (${ratePctHint})` : ''}
                  </p>
                )
              )}
            </div>
          )
        })}
      </div>
      <Button
        type='submit'
        variant='outline'
        disabled={
          save.isPending || Object.keys(draft).length === 0 || rateInvalid
        }
      >
        {t('Save')}
      </Button>
    </form>
  )
}

export function SiteReferralPanel() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const rules = useQuery({
    queryKey: siteOwnerKeys.list('site-referral', {}),
    queryFn: getOwnerSiteReferral,
  })
  const invites = useQuery({
    queryKey: siteOwnerKeys.list('site-invites', {}),
    queryFn: getOwnerSiteInvites,
  })
  const [inviterId, setInviterId] = useState('')
  const [inviteeId, setInviteeId] = useState('')
  const enable = useMutation({
    mutationFn: (enabled: boolean) => saveOwnerSiteReferralEnabled(enabled),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void invalidateSidebarBadges(client)
    },
    onError: (error) => handleServerError(error),
  })
  const resume = useMutation({
    mutationFn: resumeOwnerSiteCoverage,
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void invalidateSidebarBadges(client)
    },
    onError: (error) => handleServerError(error),
  })
  const bind = useMutation({
    mutationFn: () => bindOwnerSiteInvite(Number(inviterId), Number(inviteeId)),
    onSuccess: () => {
      setInviterId('')
      setInviteeId('')
      void client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void invalidateSidebarBadges(client)
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <section className='flex flex-col gap-4'>
      <div className='flex flex-col gap-1'>
        <h2 className='text-sm font-medium'>{t('One-level invites')}</h2>
        <p className='text-muted-foreground text-sm'>
          {t('Saving a rate does not pay commission.')}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Uses the current earnings and authorized margin cycle on the original commission rows.'
          )}
        </p>
        <Button
          type='button'
          variant='outline'
          disabled={resume.isPending}
          onClick={() => resume.mutate()}
        >
          {t('Cover pending commissions')}
        </Button>
      </div>
      {rules.isPending && <LoadingState />}
      {rules.isError && <ErrorState onRetry={() => void rules.refetch()} />}
      {rules.data && (
        <div className='flex flex-col gap-5'>
          <MarketingFields
            title={t('Commission coverage')}
            description={t(
              'New orders use these terms. Zero observation days makes fully covered commission available immediately.'
            )}
            policy={rules.data}
            fields={[
              {
                key: 'rate',
                label: t('Invite rate (0 to 0.30, max 30%)'),
                max: MAX_SITE_REFERRAL_RATE,
                hint: t(
                  'Commission rate cannot exceed 30% (0 to 0.30). Example: 0.15 = 15%.'
                ),
              },
              {
                key: 'pending_coverage_cap',
                label: t('Pending coverage cap (USD)'),
              },
              {
                key: 'observation_days',
                label: t('Observation days'),
                integer: true,
              },
            ]}
          />
          <div className='flex items-center gap-3'>
            <Switch
              id='site-referral-enabled'
              checked={rules.data.enabled === true}
              disabled={enable.isPending}
              onCheckedChange={(value) => enable.mutate(value)}
            />
            <Label htmlFor='site-referral-enabled'>
              {t('Invite commission')}
            </Label>
          </div>
          <MarketingFields
            title={t('Invite commission withdrawal')}
            description={t(
              'Set the minimum, fee and exchange rate together to enable cash withdrawals'
            )}
            policy={rules.data}
            fields={[
              { key: 'min_usd', label: t('Minimum withdrawal (USD)') },
              { key: 'fee_usd', label: t('USDT fee (USD)') },
              { key: 'cancel_window_minutes', label: t('Cancel window (minutes)'), integer: true },
              {
                key: 'cny_rate',
                label: t('CNY withdrawal rate (1 USD = ? CNY)'),
              },
            ]}
          />
          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='flex flex-col gap-1'>
              <Label htmlFor='site-inviter'>{t('Inviter')}</Label>
              <Input
                id='site-inviter'
                value={inviterId}
                inputMode='numeric'
                onChange={(event) => setInviterId(event.target.value)}
              />
            </div>
            <div className='flex flex-col gap-1'>
              <Label htmlFor='site-invitee'>{t('Invitee')}</Label>
              <Input
                id='site-invitee'
                value={inviteeId}
                inputMode='numeric'
                onChange={(event) => setInviteeId(event.target.value)}
              />
            </div>
          </div>
          <Button
            type='button'
            variant='outline'
            disabled={
              bind.isPending ||
              !rules.data.rate_configured ||
              inviterId.trim() === '' ||
              inviteeId.trim() === ''
            }
            onClick={() => bind.mutate()}
          >
            {t('Bind invite')}
          </Button>
        </div>
      )}
      {invites.isError && <ErrorState onRetry={() => void invites.refetch()} />}
      {invites.isSuccess && (
        <StaticDataTable
          data={invites.data}
          getRowKey={(row) => row.id}
          tableProps={{ 'aria-label': t('One-level invites') }}
          emptyContent={<EmptyState title={t('No data')} />}
          columns={[
            {
              id: 'inviter',
              header: t('Inviter'),
              cell: (row) => (
                <span className='tabular-nums'>{row.inviter_id}</span>
              ),
            },
            {
              id: 'invitee',
              header: t('Invitee'),
              cell: (row) => (
                <span className='tabular-nums'>{row.invitee_id}</span>
              ),
            },
            {
              id: 'rate',
              header: t('Rate at binding'),
              cell: (row) => <span className='tabular-nums'>{row.rate}</span>,
            },
          ]}
        />
      )}
    </section>
  )
}
