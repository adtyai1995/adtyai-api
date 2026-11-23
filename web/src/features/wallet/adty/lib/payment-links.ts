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
import type { AdtyOrderExt } from '../types'
import { isDisplayablePaymentQr, isSafePaymentUrl } from './safe-url'

type PaymentLinks = Pick<AdtyOrderExt, 'qr_code' | 'pay_url' | 'url_scheme'>

/** QR content for desktop: the gateway QR code, otherwise the payment link. */
export function qrContent(ext: PaymentLinks): string {
  if (isDisplayablePaymentQr(ext.qr_code)) return ext.qr_code
  if (isSafePaymentUrl(ext.pay_url)) return ext.pay_url
  if (isSafePaymentUrl(ext.url_scheme)) return ext.url_scheme
  return ''
}

/** Link for phones, tried in the order qrcode → payurl → urlscheme. */
export function mobileTarget(ext: PaymentLinks): string {
  for (const candidate of [ext.qr_code, ext.pay_url, ext.url_scheme]) {
    if (candidate && isSafePaymentUrl(candidate)) return candidate
  }
  return ''
}
