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
import { Save } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { CHAIN_NAMES } from '@/features/wallet/adty/lib/methods'

import { SettingsSwitchField } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

// Ported from the previous fork (adty task 4.3): keys moved to adty_bepusdt.*,
// chains are the payment method IDs that BEpusdt also uses as trade_type.
const BEPUSDT_CHAINS = Object.entries(CHAIN_NAMES).map(([id, label]) => ({
  id,
  label,
}))

const MIN_USDT_ORDER_MINUTES = 3
const MAX_USDT_ORDER_MINUTES = 60

export type BEpusdtSettingsDefaults = {
  enabled: boolean
  baseUrl: string
  tradeTypes: string
  orderMinutes: number
  secretConfigured?: boolean
}

type BEpusdtSettingsSectionProps = {
  defaultValues: BEpusdtSettingsDefaults
  inlineActions?: boolean
}

function parseTradeTypes(raw: string): string[] {
  try {
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed)
      ? parsed.filter((v): v is string => typeof v === 'string')
      : []
  } catch {
    return []
  }
}

function isValidHttpUrl(raw: string): boolean {
  const trimmed = raw.trim()
  if (!trimmed) return false
  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

export function BEpusdtSettingsSection(props: BEpusdtSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const savedRef = useRef(props.defaultValues)
  const initialChains = useMemo(
    () => parseTradeTypes(props.defaultValues.tradeTypes),
    [props.defaultValues.tradeTypes]
  )
  const [enabled, setEnabled] = useState(props.defaultValues.enabled)
  const [baseUrl, setBaseUrl] = useState(props.defaultValues.baseUrl)
  const [secret, setSecret] = useState('')
  const [secretConfigured, setSecretConfigured] = useState(
    Boolean(props.defaultValues.secretConfigured)
  )
  const [chains, setChains] = useState<string[]>(initialChains)
  const [minutes, setMinutes] = useState(
    String(props.defaultValues.orderMinutes)
  )
  const [validationSubmitted, setValidationSubmitted] = useState(false)

  // Reset drafts when the server-provided saved values change.
  const savedEnabled = props.defaultValues.enabled
  const savedBaseUrl = props.defaultValues.baseUrl
  const savedTradeTypes = props.defaultValues.tradeTypes
  const savedMinutes = props.defaultValues.orderMinutes
  const savedSecretConfigured = Boolean(props.defaultValues.secretConfigured)
  useEffect(() => {
    const prev = savedRef.current
    savedRef.current = {
      enabled: savedEnabled,
      baseUrl: savedBaseUrl,
      tradeTypes: savedTradeTypes,
      orderMinutes: savedMinutes,
      secretConfigured: savedSecretConfigured,
    }
    if (savedEnabled !== prev.enabled) {
      setEnabled((current) =>
        current === prev.enabled ? savedEnabled : current
      )
    }
    if (savedBaseUrl !== prev.baseUrl) {
      setBaseUrl((current) =>
        current === prev.baseUrl ? savedBaseUrl : current
      )
    }
    if (savedTradeTypes !== prev.tradeTypes) {
      const prevSerialized = JSON.stringify(parseTradeTypes(prev.tradeTypes))
      setChains((current) =>
        JSON.stringify(current) === prevSerialized ? initialChains : current
      )
    }
    if (savedMinutes !== prev.orderMinutes) {
      setMinutes((current) =>
        current === String(prev.orderMinutes) ? String(savedMinutes) : current
      )
    }
    if (savedSecretConfigured !== Boolean(prev.secretConfigured)) {
      setSecretConfigured(savedSecretConfigured)
    }
  }, [
    savedEnabled,
    savedBaseUrl,
    savedTradeTypes,
    savedMinutes,
    savedSecretConfigured,
    initialChains,
  ])

  const minutesValue = Number.parseInt(minutes, 10)
  const baseUrlError =
    enabled && !isValidHttpUrl(baseUrl)
      ? t('Enter a valid http(s) gateway address before enabling USDT payments')
      : ''
  const secretError =
    enabled && secret.trim() === '' && !secretConfigured
      ? t('Enter an API token before enabling USDT payments')
      : ''
  const chainError = chains.length === 0 ? t('Choose at least one chain') : ''
  const minutesError =
    !Number.isInteger(minutesValue) ||
    minutesValue < MIN_USDT_ORDER_MINUTES ||
    minutesValue > MAX_USDT_ORDER_MINUTES
      ? t('Enter a whole number between 3 and 60 minutes')
      : ''

  const save = async () => {
    setValidationSubmitted(true)
    const firstError = baseUrlError || secretError || chainError || minutesError
    if (firstError) {
      toast.error(firstError)
      return
    }
    const saved = savedRef.current
    const savedChains = parseTradeTypes(saved.tradeTypes)
    const updates: Array<{ key: string; value: string | boolean | number }> = []
    if (enabled !== saved.enabled) {
      updates.push({ key: 'adty_bepusdt.enabled', value: enabled })
    }
    if (baseUrl.trim() !== saved.baseUrl) {
      updates.push({ key: 'adty_bepusdt.base_url', value: baseUrl.trim() })
    }
    // The secret is write-only: an empty field keeps the stored one.
    if (secret.trim() !== '') {
      updates.push({ key: 'adty_bepusdt.secret', value: secret.trim() })
    }
    const serializedChains = JSON.stringify(chains)
    if (serializedChains !== JSON.stringify(savedChains)) {
      updates.push({ key: 'adty_bepusdt.trade_types', value: serializedChains })
    }
    if (minutesValue !== saved.orderMinutes) {
      updates.push({
        key: 'adty_topup_setting.usdt_order_minutes',
        value: minutesValue,
      })
    }
    updates.sort((a, b) => {
      const order = (key: string) => {
        if (key !== 'adty_bepusdt.enabled') return 1
        return enabled ? 2 : 0
      }
      return order(a.key) - order(b.key)
    })
    try {
      for (const update of updates) {
        await updateOption.mutateAsync(update)
        if (update.key === 'adty_bepusdt.secret') {
          setSecret('')
          setSecretConfigured(true)
          savedRef.current = {
            ...savedRef.current,
            secretConfigured: true,
          }
        } else if (update.key === 'adty_bepusdt.enabled') {
          savedRef.current = {
            ...savedRef.current,
            enabled: update.value as boolean,
          }
        } else if (update.key === 'adty_bepusdt.base_url') {
          savedRef.current = {
            ...savedRef.current,
            baseUrl: update.value as string,
          }
        } else if (update.key === 'adty_bepusdt.trade_types') {
          savedRef.current = {
            ...savedRef.current,
            tradeTypes: update.value as string,
          }
        } else if (update.key === 'adty_topup_setting.usdt_order_minutes') {
          savedRef.current = {
            ...savedRef.current,
            orderMinutes: update.value as number,
          }
        }
      }
      setSecret('')
    } catch {
      // Error toast is already surfaced by updateOption's onError handler.
    }
  }

  return (
    <SettingsSection title={t('USDT (BEpusdt)')}>
      {!props.inlineActions && (
        <SettingsPageFormActions
          onSave={save}
          isSaving={updateOption.isPending}
        />
      )}
      <div className='flex max-w-2xl flex-col gap-5'>
        <Alert>
          <AlertDescription>
            {t(
              'In BEpusdt set the amount match mode to has_prefix, the order timeout to the same minutes as here, the confirmations you need, and allow callbacks to this site.'
            )}
          </AlertDescription>
        </Alert>
        <SettingsSwitchField
          controlId='bepusdt-enabled'
          checked={enabled}
          onCheckedChange={setEnabled}
          label={t('Enable USDT payments')}
          className='py-1'
        />
        <div className='max-w-xl space-y-2'>
          <Label htmlFor='bepusdt-base-url'>{t('Gateway address')}</Label>
          <Input
            id='bepusdt-base-url'
            value={baseUrl}
            placeholder='https://pay.example.com'
            aria-invalid={Boolean(validationSubmitted && baseUrlError)}
            onChange={(event) => setBaseUrl(event.target.value)}
          />
          {validationSubmitted && baseUrlError && (
            <p className='text-destructive text-xs' role='alert'>
              {baseUrlError}
            </p>
          )}
        </div>
        <div className='max-w-xl space-y-2'>
          <div className='flex items-center gap-2'>
            <Label htmlFor='bepusdt-secret'>{t('API token')}</Label>
            <StatusBadge
              label={secretConfigured ? t('Configured') : t('Not configured')}
              variant={secretConfigured ? 'success' : 'neutral'}
              copyable={false}
            />
          </div>
          <Input
            id='bepusdt-secret'
            type='password'
            autoComplete='new-password'
            value={secret}
            aria-invalid={Boolean(validationSubmitted && secretError)}
            placeholder={
              secretConfigured
                ? t('Leave empty to keep the current token')
                : t('Enter your BEpusdt API token')
            }
            onChange={(event) => setSecret(event.target.value)}
          />
          {validationSubmitted && secretError && (
            <p className='text-destructive text-xs' role='alert'>
              {secretError}
            </p>
          )}
        </div>
        <fieldset className='space-y-2'>
          <legend className='text-sm font-medium'>{t('Enabled chains')}</legend>
          <div className='flex flex-wrap gap-4'>
            {BEPUSDT_CHAINS.map((chain) => (
              <label key={chain.id} className='flex items-center gap-2 text-sm'>
                <Checkbox
                  checked={chains.includes(chain.id)}
                  onCheckedChange={(checked) =>
                    setChains((prev) =>
                      checked === true
                        ? [...prev, chain.id]
                        : prev.filter((id) => id !== chain.id)
                    )
                  }
                />
                {chain.label}
              </label>
            ))}
          </div>
          {chainError && (
            <p className='text-destructive text-xs' role='alert'>
              {chainError}
            </p>
          )}
        </fieldset>
        <div className='space-y-2'>
          <Label htmlFor='bepusdt-minutes'>
            {t('Order validity (minutes)')}
          </Label>
          <Input
            id='bepusdt-minutes'
            type='number'
            min={MIN_USDT_ORDER_MINUTES}
            max={MAX_USDT_ORDER_MINUTES}
            step={1}
            className='w-32'
            value={minutes}
            onChange={(event) => setMinutes(event.target.value)}
            aria-invalid={Boolean(minutesError)}
          />
          {minutesError && (
            <p className='text-destructive text-xs' role='alert'>
              {minutesError}
            </p>
          )}
        </div>
      </div>
      {props.inlineActions && (
        <div className='mt-5 flex justify-start'>
          <Button
            variant='brand'
            type='button'
            size='sm'
            onClick={save}
            disabled={updateOption.isPending}
          >
            <Save data-icon='inline-start' />
            <span>{t('Save Changes')}</span>
          </Button>
        </div>
      )}
    </SettingsSection>
  )
}
