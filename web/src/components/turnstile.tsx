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
import { useEffect, useRef } from 'react'

declare global {
  interface Window {
    turnstile?: {
      render: (
        element: HTMLElement,
        options: Record<string, unknown>
      ) => string | undefined
      remove?: (widgetId: string) => void
    }
  }
}

interface TurnstileProps {
  siteKey: string
  onVerify: (token: string) => void
  onExpire?: () => void
  className?: string
}

export function Turnstile({
  siteKey,
  onVerify,
  onExpire,
  className,
}: TurnstileProps) {
  const ref = useRef<HTMLDivElement | null>(null)
  const onVerifyRef = useRef(onVerify)
  const onExpireRef = useRef(onExpire)

  useEffect(() => {
    onVerifyRef.current = onVerify
    onExpireRef.current = onExpire
  }, [onVerify, onExpire])

  useEffect(() => {
    let cancelled = false
    let rendered = false
    let widgetId: string | undefined
    let scriptEl: HTMLScriptElement | null = null

    const render = () => {
      if (cancelled || rendered || !ref.current || !window.turnstile) return
      rendered = true
      try {
        widgetId = window.turnstile.render(ref.current, {
          sitekey: siteKey,
          callback: (token: string) => {
            if (cancelled) return
            onVerifyRef.current(token)
          },
          'error-callback': () => {
            if (cancelled) return
            onExpireRef.current?.()
          },
          'expired-callback': () => {
            if (cancelled) return
            onExpireRef.current?.()
          },
        })
      } catch {
        rendered = false
        if (!cancelled) {
          onExpireRef.current?.()
        }
      }
    }

    const handleScriptError = () => {
      scriptEl?.removeEventListener('load', render)
      scriptEl?.removeEventListener('error', handleScriptError)
      scriptEl?.remove()
      scriptEl = null
      rendered = false
      if (!cancelled) {
        onExpireRef.current?.()
      }
    }

    if (window.turnstile) {
      render()
    } else {
      const scriptId = 'cf-turnstile'
      const existing = document.querySelector<HTMLScriptElement>(`#${scriptId}`)
      if (existing) {
        scriptEl = existing
        existing.addEventListener('load', render)
        existing.addEventListener('error', handleScriptError)
      } else {
        const s = document.createElement('script')
        s.id = scriptId
        s.src =
          'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
        s.async = true
        s.defer = true
        scriptEl = s
        s.addEventListener('load', render)
        s.addEventListener('error', handleScriptError)
        document.head.append(s)
      }
    }

    return () => {
      cancelled = true
      scriptEl?.removeEventListener('load', render)
      scriptEl?.removeEventListener('error', handleScriptError)
      if (widgetId !== undefined) {
        try {
          window.turnstile?.remove?.(widgetId)
        } catch {
          /* empty */
        }
      }
    }
  }, [siteKey])

  return <div ref={ref} className={className} />
}
