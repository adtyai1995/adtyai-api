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
