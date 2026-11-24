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
import { cn } from '@/lib/utils'

type LogoProps = {
  className?: string
}

/** Alipay official rounded-square vector mark (#1677FF). */
export function AlipayLogo({ className }: LogoProps) {
  return (
    <svg
      viewBox='0 0 24 24'
      className={cn('size-5 shrink-0', className)}
      aria-hidden='true'
    >
      <rect width='24' height='24' rx='5.2' fill='#1677FF' />
      <path
        fill='#FFFFFF'
        d='M11.15 4.6h1.7v2.35h4.5v1.45h-4.5v1.55h3.55v1.35H7.6V9.95h3.55V8.4h-4.5V6.95h4.5V4.6Z'
      />
      <path
        fill='#FFFFFF'
        fillRule='evenodd'
        clipRule='evenodd'
        d='M15.1 11.25c-.48 1.54-1.2 2.95-2.16 4.1 2.42.84 5.54 1.88 8.95 2.75l-.68 1.65c-3.4-.92-6.52-1.97-9.36-3.07-1.25 1.18-2.78 1.87-4.33 1.87-1.96 0-3.32-1.18-3.32-2.75 0-1.66 1.44-2.85 3.63-2.85 1.38 0 2.71.37 3.92.95.67-.82 1.17-1.72 1.5-2.65h1.85Zm-4.4 4.78c-.9-.39-1.85-.62-2.72-.62-1.2 0-1.96.52-1.96 1.32 0 .74.66 1.24 1.66 1.24 1.12 0 2.15-.65 3.02-1.94Z'
      />
    </svg>
  )
}

/** WeChat Pay official rounded-square speech-bubble & check mark (#07C160). */
export function WeChatPayLogo({ className }: LogoProps) {
  return (
    <svg
      viewBox='0 0 24 24'
      className={cn('size-5 shrink-0', className)}
      aria-hidden='true'
    >
      <rect width='24' height='24' rx='5.2' fill='#07C160' />
      <path
        fill='#FFFFFF'
        d='M12 5.3c-4.2 0-7.6 2.8-7.6 6.25 0 1.96 1.1 3.7 2.82 4.85l-.8 2.52 2.8-1.38c.87.25 1.8.38 2.78.38 4.2 0 7.6-2.8 7.6-6.25S16.2 5.3 12 5.3Z'
      />
      <path
        fill='none'
        stroke='#07C160'
        strokeWidth='1.85'
        strokeLinecap='round'
        strokeLinejoin='round'
        d='M8.6 11.75 11.1 14.1l8.1-6.2'
      />
    </svg>
  )
}

/** Tether USDT official circular vector mark (#26A17B). */
export function UsdtLogo({ className }: LogoProps) {
  return (
    <svg
      viewBox='0 0 24 24'
      className={cn('size-5 shrink-0', className)}
      aria-hidden='true'
    >
      <circle cx='12' cy='12' r='11.5' fill='#26A17B' />
      <path
        fill='#FFFFFF'
        d='M6.9 6.5h10.2v2.25h-3.85v4.45h-2.5V8.75H6.9V6.5Zm3.85 7.25h2.5v4.75h-2.5v-4.75Z'
      />
      <path
        fill='#FFFFFF'
        fillRule='evenodd'
        clipRule='evenodd'
        d='M12 10.6c3.3 0 5.95.69 5.95 1.68 0 .99-2.65 1.68-5.95 1.68s-5.95-.69-5.95-1.68c0-.99 2.65-1.68 5.95-1.68Zm0 .76c-2.55 0-4.45.4-4.45.86 0 .46 1.9.86 4.45.86s4.45-.4 4.45-.86c0-.46-1.9-.86-4.45-.86Z'
      />
      <rect x='10.75' y='8.5' width='2.5' height='4.65' fill='#FFFFFF' />
    </svg>
  )
}

type ChainKey = 'trc20' | 'bep20' | 'polygon' | 'xlayer'

function normalizeChainKey(chain?: string): ChainKey | null {
  const value = (chain ?? '').toLowerCase().trim()
  if (value === 'trc20' || value === 'usdt.trc20' || value === 'tron') {
    return 'trc20'
  }
  if (value === 'bep20' || value === 'usdt.bep20' || value === 'bsc' || value === 'bnb') {
    return 'bep20'
  }
  if (value === 'polygon' || value === 'usdt.polygon' || value === 'matic') {
    return 'polygon'
  }
  if (value === 'xlayer' || value === 'usdt.xlayer' || value === 'okx') {
    return 'xlayer'
  }
  return null
}

/** Recognizable chain vector badges for TRON (TRC20), BNB (BEP20), Polygon, and X Layer (OKX). */
export function ChainBadgeLogo({
  chain,
  className,
}: {
  chain: string
  className?: string
}) {
  const key = normalizeChainKey(chain)
  const sizeClass = cn('size-5 shrink-0', className)

  if (key === 'trc20') {
    return (
      <svg viewBox='0 0 24 24' className={sizeClass} aria-hidden='true'>
        <circle cx='12' cy='12' r='11.5' fill='#EF0027' />
        <path
          fill='#FFFFFF'
          d='M5.7 6.8 16.4 8.2l2.4 2.3-7.6 8.8L5.7 6.8Zm1.85 1.4 3.45 8.8.78-5.05-4.23-3.75Zm5.05 4.05-.75 4.95 5.45-6.3-4.7 1.35Zm-4.1-4.4 3.85 3.45 3.15-.9-7-2.55Zm7.35 1.2-.15.08 1.65.9-1.5-.98Z'
        />
      </svg>
    )
  }

  if (key === 'bep20') {
    return (
      <svg viewBox='0 0 24 24' className={sizeClass} aria-hidden='true'>
        <circle cx='12' cy='12' r='11.5' fill='#F3BA2F' />
        <path
          fill='#FFFFFF'
          d='M12 5.4 16.6 10l-1.45 1.45L12 8.3l-3.15 3.15L7.4 10 12 5.4Zm0 4.7 1.9 1.9-1.9 1.9-1.9-1.9 1.9-1.9Zm0 8.5L7.4 14l1.45-1.45L12 15.7l3.15-3.15L16.6 14 12 18.6ZM6.9 10.55 8.35 12 6.9 13.45 5.45 12l1.45-1.45Zm10.2 0L18.55 12l-1.45 1.45L15.65 12l1.45-1.45Z'
        />
      </svg>
    )
  }

  if (key === 'polygon') {
    return (
      <svg viewBox='0 0 24 24' className={sizeClass} aria-hidden='true'>
        <circle cx='12' cy='12' r='11.5' fill='#8247E5' />
        <path
          fill='#FFFFFF'
          d='M15.42 9.93a.76.76 0 0 0-.75 0l-1.78 1.03-1.2.7-1.77 1.03a.76.76 0 0 1-.75 0l-1.4-.82a.76.76 0 0 1-.37-.65V9.6c0-.27.14-.52.37-.65l1.39-.8a.76.76 0 0 1 .75 0l1.39.8c.23.13.38.38.38.65v1.03l1.2-.7V8.89a.76.76 0 0 0-.38-.65L9.92 6.77a.76.76 0 0 0-.75 0L6.57 8.25a.76.76 0 0 0-.38.65v2.98c0 .27.15.52.38.65l2.6 1.48a.76.76 0 0 0 .75 0l1.77-1.01 1.2-.7 1.77-1.02a.76.76 0 0 1 .75 0l1.39.8c.23.13.38.38.38.65v1.62c0 .27-.15.52-.38.65l-1.38.8a.76.76 0 0 1-.75 0l-1.39-.8a.76.76 0 0 1-.38-.65v-1.02l-1.2.7v1.03c0 .27.15.52.38.65l2.6 1.48a.76.76 0 0 0 .75 0l2.6-1.48a.76.76 0 0 0 .38-.65v-2.98a.76.76 0 0 0-.38-.65l-2.6-1.47Z'
        />
      </svg>
    )
  }

  if (key === 'xlayer') {
    return (
      <svg viewBox='0 0 24 24' className={sizeClass} aria-hidden='true'>
        <circle
          cx='12'
          cy='12'
          r='11.2'
          fill='#111111'
          stroke='rgba(255,255,255,0.22)'
          strokeWidth='0.8'
        />
        <rect x='6.6' y='6.6' width='3.6' height='3.6' rx='0.55' fill='#FFFFFF' />
        <rect x='13.8' y='6.6' width='3.6' height='3.6' rx='0.55' fill='#FFFFFF' />
        <rect x='10.2' y='10.2' width='3.6' height='3.6' rx='0.55' fill='#FFFFFF' />
        <rect x='6.6' y='13.8' width='3.6' height='3.6' rx='0.55' fill='#FFFFFF' />
        <rect x='13.8' y='13.8' width='3.6' height='3.6' rx='0.55' fill='#FFFFFF' />
      </svg>
    )
  }

  return (
    <svg viewBox='0 0 24 24' className={sizeClass} aria-hidden='true'>
      <circle cx='12' cy='12' r='11.5' fill='#737373' />
      <circle cx='12' cy='12' r='4' fill='#FFFFFF' />
    </svg>
  )
}

/** Combined Tether USDT logo with a compact network badge in the bottom-right corner. */
export function UsdtChainLogo({
  chain,
  className,
}: {
  chain: string
  className?: string
}) {
  const key = normalizeChainKey(chain)
  return (
    <span
      className={cn(
        'relative inline-flex size-5 shrink-0 items-center justify-center',
        className
      )}
      aria-hidden='true'
    >
      <UsdtLogo className='size-full' />
      {key && (
        <span className='ring-background bg-background absolute -right-0.5 -bottom-0.5 flex size-[54%] items-center justify-center rounded-full ring-[1.5px]'>
          <ChainBadgeLogo chain={key} className='size-full' />
        </span>
      )}
    </span>
  )
}

/** Unified official icon for Alipay, WeChat Pay, USDT, and USDT chain methods. */
export function PaymentMethodIcon({
  method,
  tradeType,
  className,
}: {
  method?: string
  tradeType?: string
  className?: string
}) {
  const key = (tradeType || method || '').toLowerCase().trim()
  if (key === 'alipay') {
    return <AlipayLogo className={className} />
  }
  if (key === 'wxpay' || key === 'wechat' || key === 'wechat_pay') {
    return <WeChatPayLogo className={className} />
  }
  if (key === 'usdt') {
    return <UsdtLogo className={className} />
  }
  if (key.startsWith('usdt.') || normalizeChainKey(key)) {
    return <UsdtChainLogo chain={key} className={className} />
  }
  return <UsdtLogo className={className} />
}
