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
import { useTranslation } from 'react-i18next'

import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

import type {
  EpayChannel,
  EpayChannelInput,
  EpayMethodConfig,
  EpaySignType,
} from '../api'

type ChannelFormFieldsProps = {
  idPrefix: string
  input: EpayChannelInput
  existingChannel: EpayChannel | null
  onChange: (next: EpayChannelInput) => void
}

/** Shared form controls for creating, editing, or replacing an Epay channel. */
export function ChannelFormFields(props: ChannelFormFieldsProps) {
  const { t } = useTranslation()
  const prefix = props.idPrefix
  const input = props.input
  const existing = props.existingChannel
  const canReuseExisting =
    existing !== null &&
    existing.status !== 'revoked' &&
    !existing.credential_decrypt_error

  const set = <K extends keyof EpayChannelInput>(
    key: K,
    value: EpayChannelInput[K]
  ) => props.onChange({ ...input, [key]: value })

  const setMethod = (index: number, patch: Partial<EpayMethodConfig>) =>
    props.onChange({
      ...input,
      methods: input.methods.map((m, i) =>
        i === index ? { ...m, ...patch } : m
      ),
    })

  const anyMethod = input.methods.some((m) => m.enabled)
  const isRSA = input.sign_type === 'RSA'

  const methodLabel = (method: EpayMethodConfig['method']) =>
    method === 'alipay' ? t('Alipay') : t('WeChat Pay')

  return (
    <>
      <div className='space-y-2'>
        <Label htmlFor={`${prefix}-name`}>{t('Name')}</Label>
        <Input
          id={`${prefix}-name`}
          value={input.name}
          onChange={(e) => set('name', e.target.value)}
        />
      </div>
      <div className='space-y-2'>
        <Label htmlFor={`${prefix}-url`}>{t('Gateway address')}</Label>
        <Input
          id={`${prefix}-url`}
          value={input.gateway_url}
          placeholder='https://pay.example.com'
          onChange={(e) => set('gateway_url', e.target.value)}
        />
      </div>
      <div className='grid grid-cols-2 gap-3'>
        <div className='space-y-2'>
          <Label htmlFor={`${prefix}-pid`}>{t('Merchant ID')}</Label>
          <Input
            id={`${prefix}-pid`}
            value={input.merchant_id}
            onChange={(e) => set('merchant_id', e.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${prefix}-sign-type`}>{t('Signature type')}</Label>
          <NativeSelect
            id={`${prefix}-sign-type`}
            value={input.sign_type}
            onChange={(e) => set('sign_type', e.target.value as EpaySignType)}
          >
            <option value='MD5'>MD5</option>
            <option value='RSA'>RSA</option>
          </NativeSelect>
        </div>
      </div>
      {isRSA ? (
        <div className='space-y-3'>
          <div className='space-y-2'>
            <Label htmlFor={`${prefix}-private-key`}>
              {t('Merchant private key')}
            </Label>
            <Textarea
              id={`${prefix}-private-key`}
              rows={4}
              autoComplete='off'
              spellCheck={false}
              className='font-mono text-xs break-all'
              value={input.merchant_private_key}
              placeholder={
                canReuseExisting && existing?.has_merchant_private_key
                  ? t('Leave empty to keep the current key')
                  : 'MIIEvQIBADANBgkqhkiG9w0BAQEFAASC...'
              }
              onChange={(e) => set('merchant_private_key', e.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor={`${prefix}-public-key`}>
              {t('Platform public key')}
            </Label>
            <Textarea
              id={`${prefix}-public-key`}
              rows={4}
              autoComplete='off'
              spellCheck={false}
              className='font-mono text-xs break-all'
              value={input.platform_public_key}
              placeholder={
                canReuseExisting && existing?.has_platform_public_key
                  ? t('Leave empty to keep the current key')
                  : 'MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A...'
              }
              onChange={(e) => set('platform_public_key', e.target.value)}
            />
          </div>
        </div>
      ) : (
        <div className='space-y-2'>
          <Label htmlFor={`${prefix}-key`}>{t('Merchant key')}</Label>
          <Input
            id={`${prefix}-key`}
            type='password'
            autoComplete='new-password'
            value={input.merchant_key}
            placeholder={
              canReuseExisting && existing?.has_key
                ? t('Leave empty to keep the current key')
                : ''
            }
            onChange={(e) => set('merchant_key', e.target.value)}
          />
        </div>
      )}
      <div className='grid grid-cols-3 gap-3'>
        <div className='space-y-2'>
          <Label htmlFor={`${prefix}-priority`}>{t('Priority')}</Label>
          <Input
            id={`${prefix}-priority`}
            type='number'
            value={input.priority}
            onChange={(e) => set('priority', Number(e.target.value) || 0)}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${prefix}-weight`}>{t('Weight')}</Label>
          <Input
            id={`${prefix}-weight`}
            type='number'
            min={1}
            value={input.weight}
            onChange={(e) => set('weight', Number(e.target.value) || 1)}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${prefix}-threshold`}>{t('Unpaid limit')}</Label>
          <Input
            id={`${prefix}-threshold`}
            type='number'
            min={0}
            value={input.unpaid_threshold}
            onChange={(e) =>
              set('unpaid_threshold', Number(e.target.value) || 0)
            }
          />
        </div>
      </div>
      <div className='grid grid-cols-2 gap-3'>
        <div className='space-y-2'>
          <Label htmlFor={`${prefix}-mode`}>{t('Order mode')}</Label>
          <NativeSelect
            id={`${prefix}-mode`}
            value={input.order_mode}
            onChange={(e) =>
              set(
                'order_mode',
                e.target.value as EpayChannelInput['order_mode']
              )
            }
          >
            <option value='api'>
              {isRSA ? t('API (/api/pay/create)') : t('API (mapi.php)')}
            </option>
            <option value='submit'>
              {isRSA
                ? t('Page redirect (/api/pay/submit)')
                : t('Page redirect (submit.php)')}
            </option>
          </NativeSelect>
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${prefix}-probe`}>{t('Health check')}</Label>
          <NativeSelect
            id={`${prefix}-probe`}
            value={input.probe_mode}
            onChange={(e) => set('probe_mode', e.target.value)}
          >
            <option value='off'>{t('Off')}</option>
            <option value='query'>
              {isRSA ? t('Merchant info (/api/merchant/info)') : 'act=query'}
            </option>
            <option value='balance'>
              {isRSA
                ? t('Merchant balance (/api/merchant/info)')
                : 'act=balance'}
            </option>
            <option value='order'>
              {isRSA
                ? t('Query a missing order (/api/pay/query)')
                : t('Query a missing order')}
            </option>
          </NativeSelect>
        </div>
      </div>
      <fieldset className='space-y-3'>
        <legend className='text-sm font-medium'>{t('Payment methods')}</legend>
        {input.methods.map((m, index) => {
          const label = methodLabel(m.method)
          return (
            <div key={m.method} className='space-y-2 rounded-lg border p-3'>
              <label className='flex items-center gap-2 text-sm font-medium'>
                <Checkbox
                  checked={m.enabled}
                  onCheckedChange={(checked) =>
                    setMethod(index, { enabled: checked === true })
                  }
                />
                {label}
              </label>
              {m.enabled && (
                <div className='space-y-1'>
                  <Label
                    htmlFor={`${prefix}-name-${m.method}`}
                    className='text-xs'
                  >
                    {t('Display name')}
                  </Label>
                  <Input
                    id={`${prefix}-name-${m.method}`}
                    value={m.display_name ?? ''}
                    maxLength={32}
                    placeholder={label}
                    onChange={(e) =>
                      setMethod(index, { display_name: e.target.value })
                    }
                  />
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Shown to customers on the wallet page, for example Alipay 1. Leave empty to show the method name.'
                    )}
                  </p>
                </div>
              )}
              {m.enabled && (
                <div className='grid grid-cols-2 gap-2 sm:grid-cols-4'>
                  <div className='space-y-1'>
                    <Label
                      htmlFor={`${prefix}-type-${m.method}`}
                      className='text-xs'
                    >
                      {t('Type code')}
                    </Label>
                    <Input
                      id={`${prefix}-type-${m.method}`}
                      value={m.type_code}
                      onChange={(e) =>
                        setMethod(index, { type_code: e.target.value })
                      }
                    />
                  </div>
                  <div className='space-y-1'>
                    <Label
                      htmlFor={`${prefix}-min-${m.method}`}
                      className='text-xs'
                    >
                      {t('Min ¥')}
                    </Label>
                    <Input
                      id={`${prefix}-min-${m.method}`}
                      type='number'
                      min={0}
                      step='0.01'
                      value={m.min_cny}
                      onChange={(e) =>
                        setMethod(index, { min_cny: e.target.value })
                      }
                    />
                  </div>
                  <div className='space-y-1'>
                    <Label
                      htmlFor={`${prefix}-max-${m.method}`}
                      className='text-xs'
                    >
                      {t('Max ¥')}
                    </Label>
                    <Input
                      id={`${prefix}-max-${m.method}`}
                      type='number'
                      min={0}
                      step='0.01'
                      value={m.max_cny}
                      onChange={(e) =>
                        setMethod(index, { max_cny: e.target.value })
                      }
                    />
                  </div>
                  <div className='space-y-1'>
                    <Label
                      htmlFor={`${prefix}-daily-${m.method}`}
                      className='text-xs'
                    >
                      {t('Daily limit ¥')}
                    </Label>
                    <Input
                      id={`${prefix}-daily-${m.method}`}
                      type='number'
                      min={0}
                      step='0.01'
                      value={m.daily_limit_cny}
                      onChange={(e) =>
                        setMethod(index, { daily_limit_cny: e.target.value })
                      }
                    />
                  </div>
                </div>
              )}
            </div>
          )
        })}
        {!anyMethod && (
          <p className='text-destructive text-xs'>
            {t('Enable at least one payment method')}
          </p>
        )}
      </fieldset>
    </>
  )
}
