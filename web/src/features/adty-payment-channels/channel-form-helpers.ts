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
import type {
  EpayChannel,
  EpayChannelInput,
  EpayMethodConfig,
} from './api'

export const EPAY_METHODS: readonly EpayMethodConfig['method'][] = [
  'alipay',
  'wxpay',
]

export function emptyEpayMethod(
  method: EpayMethodConfig['method']
): EpayMethodConfig {
  return {
    method,
    display_name: '',
    type_code: method,
    min_cny: '0',
    max_cny: '0',
    daily_limit_cny: '0',
    enabled: false,
  }
}

export function toEpayChannelInput(
  channel: EpayChannel | null
): EpayChannelInput {
  const methods = EPAY_METHODS.map(
    (method) =>
      channel?.methods.find((item) => item.method === method) ??
      emptyEpayMethod(method)
  )
  return {
    name: channel?.name ?? '',
    gateway_url: channel?.gateway_url ?? '',
    merchant_id: channel?.merchant_id ?? '',
    sign_type: channel?.sign_type === 'RSA' ? 'RSA' : 'MD5',
    merchant_key: '',
    merchant_private_key: '',
    platform_public_key: channel?.platform_public_key ?? '',
    priority: channel?.priority ?? 0,
    weight: channel?.weight ?? 1,
    order_mode: channel?.order_mode ?? 'api',
    probe_mode: channel?.probe_mode ?? 'off',
    unpaid_threshold: channel?.unpaid_threshold ?? 0,
    methods: methods.map((m) => ({ ...m, type_code: m.type_code || m.method })),
  }
}

/** Pre-fills routing, limits, and methods from the old channel while leaving merchant identity and secrets blank. */
export function toReplacementChannelInput(
  source: EpayChannel | null
): EpayChannelInput {
  const base = toEpayChannelInput(source)
  return {
    ...base,
    name: '',
    gateway_url: '',
    merchant_id: '',
    merchant_key: '',
    merchant_private_key: '',
    platform_public_key: '',
    methods: base.methods.map((m) => ({
      ...m,
      return_type: undefined,
    })),
  }
}

export function epayChannelHasUsableCredentials(channel: EpayChannel): boolean {
  if (channel.status === 'revoked' || channel.credential_decrypt_error) {
    return false
  }
  if (channel.sign_type === 'RSA') {
    return Boolean(
      channel.has_merchant_private_key && channel.has_platform_public_key
    )
  }
  return Boolean(channel.has_key)
}

export function epayInputNeedsCredentials(
  input: EpayChannelInput,
  existing: EpayChannel | null
): boolean {
  const canReuseExisting =
    existing !== null &&
    existing.status !== 'revoked' &&
    !existing.credential_decrypt_error
  if (input.sign_type === 'RSA') {
    const hasPrivateKey =
      canReuseExisting && Boolean(existing?.has_merchant_private_key)
    const hasPublicKey =
      canReuseExisting && Boolean(existing?.has_platform_public_key)
    return (
      (!hasPrivateKey && input.merchant_private_key.trim() === '') ||
      (!hasPublicKey && input.platform_public_key.trim() === '')
    )
  }
  const hasMd5Key = canReuseExisting && Boolean(existing?.has_key)
  return !hasMd5Key && input.merchant_key.trim() === ''
}
