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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { formatTimestamp } from '@/lib/format'

import { savePricing } from '../api'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import {
  newRowKey,
  pendingIncreases,
  siteMarkups,
  retailOverrideChanges,
  validRetailMarkup,
  type PendingIncrease,
} from '../lib/site-owner'
import type { OwnerSite } from '../types'
import { PricingHistory } from './pricing-history'

type GroupRow = { key: string; group: string; markup: string }
type ModelRow = { key: string; model: string; markup: string }

function pendingIncreaseGroupLabel(
  item: PendingIncrease,
  t: (key: string) => string
) {
  if (!item.model) return item.group || t('All groups')
  if (!item.group) return item.model
  return `${item.model} (${item.group})`
}

/** Site-wide markup, per-group markups, and per-model independent markups (A.10). */
export function PricingPanel(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const [defaultMarkup, setDefaultMarkup] = useState('')
  const [rows, setRows] = useState<GroupRow[]>([])
  const [modelRows, setModelRows] = useState<ModelRow[]>([])
  useEffect(() => {
    const current = siteMarkups(props.site.prices)
    setDefaultMarkup(current.defaultMarkup)
    setRows(
      Object.entries(current.groups).map(([group, markup]) => ({
        key: newRowKey(),
        group,
        markup,
      }))
    )
    setModelRows(
      Object.entries(current.models).map(([model, markup]) => ({
        key: newRowKey(),
        model,
        markup,
      }))
    )
  }, [props.site.prices])
  const savedMarkups = siteMarkups(props.site.prices)
  const groups = retailOverrideChanges(
    savedMarkups.groups,
    rows.map((row) => [row.group, row.markup])
  )
  const models = retailOverrideChanges(
    savedMarkups.models,
    modelRows.map((row) => [row.model, row.markup])
  )
  const defaultChanged = defaultMarkup !== savedMarkups.defaultMarkup
  const dirty =
    defaultChanged ||
    Object.keys(groups).length > 0 ||
    Object.keys(models).length > 0
  const valid =
    (defaultMarkup === '' || validRetailMarkup(defaultMarkup)) &&
    rows.every((row) => row.group.trim() && validRetailMarkup(row.markup)) &&
    modelRows.every(
      (row) => row.model.trim() && validRetailMarkup(row.markup)
    ) &&
    new Set(rows.map((row) => row.group.trim())).size === rows.length &&
    new Set(modelRows.map((row) => row.model.trim())).size === modelRows.length
  useDraftNavigation(dirty || !valid)
  const previews = [
    ...(defaultChanged
      ? [
          {
            name: t('Default markup'),
            before: savedMarkups.defaultMarkup || '1',
            after: defaultMarkup || '1',
          },
        ]
      : []),
    ...Object.entries(groups).map(([group, value]) => ({
      name: t('Group: {{group}}', { group }),
      before: savedMarkups.groups[group] || savedMarkups.defaultMarkup || '1',
      after: value === '0' ? defaultMarkup || '1' : value,
    })),
    ...Object.entries(models).map(([model, value]) => ({
      name: t('Model: {{model}}', { model }),
      before: savedMarkups.models[model] || savedMarkups.defaultMarkup || '1',
      after: value === '0' ? defaultMarkup || '1' : value,
    })),
  ]
  const pending = pendingIncreases(props.site.prices)
  const save = useVerifiedOwnerMutation<void>({
    action: 'pricing',
    target: () => 0,
    run: (_vars, proof) => {
      return savePricing(
        {
          default_markup: defaultChanged && defaultMarkup ? defaultMarkup : undefined,
          clear_default: defaultChanged && !defaultMarkup ? true : undefined,
          group_markups: groups,
          model_markups: models,
          expected_version: props.site.pricing_version,
        },
        proof
      )
    },
  })
  const closing = props.site.status === 'closing'

  return (
    <div className='flex flex-col gap-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Retail price equals supply price times your multiplier. Your supply cost factor is {{cost}}.',
          { cost: props.site.cost_factor }
        )}
      </p>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Saving a price or removing an override takes effect for new requests immediately. Requests and tasks already accepted keep their original price.'
        )}
      </p>
      {closing && (
        <Alert>
          <AlertDescription>
            {t('Prices cannot be changed while the site is closing')}
          </AlertDescription>
        </Alert>
      )}
      {pending.length > 0 && (
        <section
          aria-label={t('Previously scheduled price changes')}
          className='space-y-1'
        >
          <h3 className='text-sm font-medium'>
            {t('Previously scheduled price changes')}
          </h3>
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
        </section>
      )}
      <div className='max-w-xs space-y-2'>
        <Label htmlFor='default-markup'>{t('Default markup')}</Label>
        <Input
          id='default-markup'
          placeholder='1'
          type='number'
          min='0.0001'
          step='0.0001'
          value={defaultMarkup}
          disabled={closing}
          onChange={(event) => setDefaultMarkup(event.target.value)}
        />
      </div>
      <section aria-label={t('Markup by group')} className='space-y-2'>
        <h3 className='text-sm font-medium'>{t('Markup by group')}</h3>
        {rows.map((row, index) => (
          <div key={row.key} className='flex items-end gap-2'>
            <div className='space-y-2'>
              <Label htmlFor={`group-name-${index}`}>{t('Group')}</Label>
              <Input
                id={`group-name-${index}`}
                value={row.group}
                disabled={closing}
                onChange={(event) =>
                  setRows((prev) =>
                    prev.map((item, i) =>
                      i === index
                        ? { ...item, group: event.target.value }
                        : item
                    )
                  )
                }
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor={`group-markup-${index}`}>{t('Markup')}</Label>
              <Input
                id={`group-markup-${index}`}
                type='number'
                min='0.0001'
                step='0.0001'
                value={row.markup}
                disabled={closing}
                onChange={(event) =>
                  setRows((prev) =>
                    prev.map((item, i) =>
                      i === index
                        ? { ...item, markup: event.target.value }
                        : item
                    )
                  )
                }
              />
            </div>
            <Button
              variant='outline'
              disabled={closing}
              onClick={() => {
                setRows((prev) => prev.filter((_, i) => i !== index))
              }}
            >
              {t('Remove')}
            </Button>
          </div>
        ))}
        <Button
          size='sm'
          variant='outline'
          disabled={closing}
          onClick={() =>
            setRows((prev) => [
              ...prev,
              { key: newRowKey(), group: '', markup: '' },
            ])
          }
        >
          {t('Add group')}
        </Button>
      </section>
      <section aria-label={t('Markup by model')} className='space-y-2'>
        <h3 className='text-sm font-medium'>{t('Markup by model')}</h3>
        {modelRows.map((row, index) => (
          <div key={row.key} className='flex items-end gap-2'>
            <div className='space-y-2'>
              <Label htmlFor={`model-name-${index}`}>{t('Model')}</Label>
              <Input
                id={`model-name-${index}`}
                value={row.model}
                disabled={closing}
                onChange={(event) =>
                  setModelRows((prev) =>
                    prev.map((item, i) =>
                      i === index
                        ? { ...item, model: event.target.value }
                        : item
                    )
                  )
                }
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor={`model-markup-${index}`}>{t('Markup')}</Label>
              <Input
                id={`model-markup-${index}`}
                type='number'
                min='0.0001'
                step='0.0001'
                value={row.markup}
                disabled={closing}
                onChange={(event) =>
                  setModelRows((prev) =>
                    prev.map((item, i) =>
                      i === index
                        ? { ...item, markup: event.target.value }
                        : item
                    )
                  )
                }
              />
            </div>
            <Button
              variant='outline'
              disabled={closing}
              onClick={() => {
                setModelRows((prev) => prev.filter((_, i) => i !== index))
              }}
            >
              {t('Remove')}
            </Button>
          </div>
        ))}
        <Button
          size='sm'
          variant='outline'
          disabled={closing}
          onClick={() =>
            setModelRows((prev) => [
              ...prev,
              { key: newRowKey(), model: '', markup: '' },
            ])
          }
        >
          {t('Add model')}
        </Button>
      </section>
      {valid && previews.length > 0 && (
        <section className='space-y-2' aria-label={t('Price preview')}>
          <h3 className='text-sm font-medium'>{t('Price preview')}</h3>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Example retail prices for a $1 supply price. Each override is shown on its own, with the site default as its fallback.'
            )}
          </p>
          <ul className='space-y-1 text-sm tabular-nums'>
            {previews.map(({ name, before, after }) => (
              <li key={name}>
                {t('{{name}}: {{before}} USD → {{after}} USD', {
                  name,
                  before,
                  after,
                })}
              </li>
            ))}
          </ul>
        </section>
      )}
      <div>
        <Button
          variant='brand'
          disabled={closing || !dirty || !valid || save.isPending}
          onClick={() => save.mutate()}
        >
          {t('Save prices')}
        </Button>
      </div>
      <PricingHistory />
    </div>
  )
}
