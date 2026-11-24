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

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { invalidateSidebarBadges } from '@/hooks/use-sidebar-badges'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getOwnerSiteReferral,
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
      <div className='space-y-1'>
        <h3 className='text-sm font-medium'>{props.title}</h3>
        <p className='text-muted-foreground text-sm'>{props.description}</p>
      </div>
      <div className='grid gap-4 sm:grid-cols-3'>
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
            <div key={key} className='space-y-1.5'>
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
        variant='brand'
        disabled={
          save.isPending || Object.keys(draft).length === 0 || rateInvalid
        }
      >
        {t('Save')}
      </Button>
    </form>
  )
}

/** Subsite referral policy settings: enable switch, invite rate (<= 30%), coverage & withdrawal terms. */
export function SiteReferralPanel() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const rules = useQuery({
    queryKey: siteOwnerKeys.list('site-referral', {}),
    queryFn: getOwnerSiteReferral,
  })
  const enable = useMutation({
    mutationFn: (enabled: boolean) => saveOwnerSiteReferralEnabled(enabled),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void invalidateSidebarBadges(client)
    },
    onError: (error) => handleServerError(error),
  })

  if (rules.isPending) return <LoadingState />
  if (rules.isError) return <ErrorState onRetry={() => void rules.refetch()} />
  if (!rules.data) return null

  const rateMissing = !rules.data.rate_configured
  const isEnabled = rules.data.enabled === true

  return (
    <section className='flex max-w-3xl flex-col gap-6'>
      <div className='flex items-center justify-between gap-4 rounded-lg border p-3'>
        <div className='flex flex-col gap-0.5'>
          <Label htmlFor='site-referral-enabled'>
            {t('Invite commission')}
          </Label>
          <span className='text-muted-foreground text-xs'>
            {rateMissing
              ? t('Set the invite rate first before enabling.')
              : t('Saving a rate does not pay commission.')}
          </span>
        </div>
        <Switch
          id='site-referral-enabled'
          checked={isEnabled}
          disabled={enable.isPending || (rateMissing && !isEnabled)}
          onCheckedChange={(value) => enable.mutate(value)}
        />
      </div>

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

      <MarketingFields
        title={t('Invite commission withdrawal')}
        description={t(
          'Set the minimum, fee and exchange rate together to enable cash withdrawals'
        )}
        policy={rules.data}
        fields={[
          { key: 'min_usd', label: t('Minimum withdrawal (USD)') },
          { key: 'fee_usd', label: t('USDT fee (USD)') },
          {
            key: 'cancel_window_minutes',
            label: t('Cancel window (minutes)'),
            integer: true,
          },
          {
            key: 'cny_rate',
            label: t('CNY withdrawal rate (1 USD = ? CNY)'),
          },
        ]}
      />
    </section>
  )
}
