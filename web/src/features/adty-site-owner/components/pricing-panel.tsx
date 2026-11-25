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
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { formatTimestamp } from '@/lib/format'

import { savePricing } from '../api'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import {
  MAX_RETAIL_MULTIPLIER,
  MIN_RETAIL_MULTIPLIER,
  pendingIncreases,
  siteMarkups,
  validRetailMarkup,
  type PendingIncrease,
} from '../lib/site-owner'
import type { OwnerSite } from '../types'
import { PricingHistory } from './pricing-history'

function pendingIncreaseGroupLabel(
  item: PendingIncrease,
  t: (key: string) => string
) {
  if (!item.model) return item.group || t('Site-wide retail multiplier')
  if (!item.group) return item.model
  return `${item.model} (${item.group})`
}

function formatMultiplier(value: number): string {
  return Number(value.toFixed(4)).toString()
}

/** Single site-wide retail multiplier (1.0x to 10.0x) applied to main-site supply cost. */
export function PricingPanel(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const fieldId = useId()
  const savedDefault = siteMarkups(props.site.prices).defaultMarkup || '1'
  const [retailMultiplier, setRetailMultiplier] = useState(savedDefault)

  useEffect(() => {
    setRetailMultiplier(siteMarkups(props.site.prices).defaultMarkup || '1')
  }, [props.site.prices])

  const trimmed = retailMultiplier.trim()
  const dirty = trimmed !== savedDefault
  const valid = validRetailMarkup(trimmed)
  useDraftNavigation(dirty)

  const pending = pendingIncreases(props.site.prices)
  const save = useVerifiedOwnerMutation<void>({
    action: 'pricing',
    target: () => 0,
    run: (_vars, proof) =>
      savePricing(
        {
          default_markup: trimmed,
          expected_version: props.site.pricing_version,
        },
        proof
      ),
  })

  const closing = props.site.status === 'closing'
  const parsedSupplyDiscount = Number(props.site.cost_factor)
  const supplyDiscount =
    Number.isFinite(parsedSupplyDiscount) && parsedSupplyDiscount > 0
      ? parsedSupplyDiscount
      : 1
  const supplyDiscountText = props.site.cost_factor || String(supplyDiscount)
  const previewMultiplier = valid ? Number(trimmed) : Number(savedDefault) || 1
  const effectiveMultiplier = supplyDiscount * previewMultiplier

  return (
    <div className='flex flex-col gap-5'>
      {closing && (
        <Alert>
          <AlertDescription>
            {t('Prices cannot be changed while the site is closing')}
          </AlertDescription>
        </Alert>
      )}

      {pending.length > 0 && (
        <Alert>
          <AlertDescription className='space-y-1'>
            <p className='font-medium'>
              {t('Previously scheduled price changes')}
            </p>
            <ul className='text-sm'>
              {pending.map((item) => (
                <li
                  key={`${item.model ?? ''}::${item.group || 'default'}`}
                  data-testid='pending-increase'
                >
                  {t('{{group}}: {{current}} → {{next}} from {{time}}', {
                    group: pendingIncreaseGroupLabel(item, t),
                    current: item.current,
                    next:
                      Number(item.next) > 0 ? item.next : t('Inherited price'),
                    time: formatTimestamp(item.at),
                  })}
                </li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}

      <SettingsSection
        title={t('Site-wide Retail Multiplier')}
        description={t(
          'Set a single retail multiplier for all models and billing groups on your subsite. Saving takes effect for new requests immediately.'
        )}
      >
        <div className='grid gap-8 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]'>
          <form
            noValidate
            className='flex flex-col gap-3'
            onSubmit={(event) => {
              event.preventDefault()
              if (closing || !dirty || !valid || save.isPending) return
              save.mutate()
            }}
          >
            <div className='space-y-1.5'>
              <Label htmlFor={`${fieldId}-multiplier`}>
                {t('Retail Multiplier (1.0 to 10.0)')}
              </Label>
              <Input
                id={`${fieldId}-multiplier`}
                placeholder='1.0'
                type='number'
                min={MIN_RETAIL_MULTIPLIER}
                max={MAX_RETAIL_MULTIPLIER}
                step='any'
                inputMode='decimal'
                value={retailMultiplier}
                disabled={closing}
                aria-invalid={!valid}
                onChange={(event) => setRetailMultiplier(event.target.value)}
              />
              {!valid ? (
                <p role='alert' className='text-destructive text-xs'>
                  {t(
                    'Retail multiplier must be between 1 and 10 (up to 4 decimal places).'
                  )}
                </p>
              ) : (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Default is 1.0 (sell at your supply cost). Maximum is 10.0.'
                  )}
                </p>
              )}
            </div>

            <div className='flex flex-wrap gap-2 pt-1'>
              <Button
                type='submit'
                variant='brand'
                disabled={closing || !dirty || !valid || save.isPending}
              >
                {t('Save prices')}
              </Button>
              {dirty && (
                <Button
                  type='button'
                  variant='outline'
                  disabled={save.isPending}
                  onClick={() => setRetailMultiplier(savedDefault)}
                >
                  {t('Reset')}
                </Button>
              )}
            </div>
          </form>

          <div className='flex flex-col gap-4'>
            <dl className='border-border bg-border grid grid-cols-2 gap-px overflow-hidden rounded-[14px] border'>
              <div className='bg-card flex flex-col gap-2 px-5 py-4'>
                <dt className='text-dim text-[12px] leading-none'>
                  {t('Main Supply Discount')}
                </dt>
                <dd className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
                  {supplyDiscountText}x
                </dd>
              </div>
              <div className='bg-card flex flex-col gap-2 px-5 py-4'>
                <dt className='text-dim text-[12px] leading-none'>
                  {t('Effective Customer Multiplier')}
                </dt>
                <dd className='font-display text-[22px] leading-tight font-extrabold tracking-[-0.02em] tabular-nums'>
                  {formatMultiplier(effectiveMultiplier)}x
                </dd>
              </div>
            </dl>
            <div className='bg-muted space-y-1 rounded-[12px] px-4 py-3 text-sm tabular-nums'>
              <p className='text-muted-foreground text-xs font-medium'>
                {t('Live pricing formula')}
              </p>
              <p className='font-medium'>
                {t(
                  'Main group 1.0x × Supply {{supply}} × Retail {{retail}} = {{effective}}x',
                  {
                    supply: supplyDiscountText,
                    retail: formatMultiplier(previewMultiplier),
                    effective: formatMultiplier(effectiveMultiplier),
                  }
                )}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Example: $1.0000 main-site price → ${{supply}} supply cost → ${{retail}} customer price',
                  {
                    supply: supplyDiscount.toFixed(4),
                    retail: effectiveMultiplier.toFixed(4),
                  }
                )}
              </p>
            </div>
          </div>
        </div>
      </SettingsSection>

      <PricingHistory />
    </div>
  )
}
