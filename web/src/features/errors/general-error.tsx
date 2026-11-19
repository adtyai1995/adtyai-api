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
import { useNavigate, useRouter } from '@tanstack/react-router'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { Button, buttonVariants } from '@/components/ui/button'
import { useAdtySite } from '@/features/adty-site/api'
import { cn } from '@/lib/utils'
import { subsiteDocument } from '@/lib/subsite-document'

type GeneralErrorProps = React.HTMLAttributes<HTMLDivElement> & {
  minimal?: boolean
  error?: unknown
}

function getHttpStatus(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) return undefined
  const response = (error as Record<string, unknown>).response
  if (typeof response !== 'object' || response === null) return undefined
  const status = (response as Record<string, unknown>).status
  return typeof status === 'number' ? status : undefined
}

export function GeneralError({
  className,
  minimal = false,
  error,
}: GeneralErrorProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { history } = useRouter()
  const { data: site } = useAdtySite()
  const supportEmail = site?.support_email?.trim()
  useEffect(() => {
    const isNativeModuleLoadError =
      error instanceof TypeError &&
      (/^(Failed to fetch dynamically imported module|error loading dynamically imported module): https?:\/\/\S+$/.test(
        error.message
      ) ||
        error.message === 'Importing a module script failed.')
    if (
      !(error instanceof Error) ||
      (error.name !== 'ChunkLoadError' &&
        !/^Loading (CSS )?chunk .+ failed\./.test(error.message) &&
        !isNativeModuleLoadError)
    ) {
      return
    }

    // Recover only when the failed route is displayed, never on intent preload.
    // Keep the marker across reloads so a persistent failure shows this page.
    try {
      const key = `tanstack_router_reload:${error.message}`
      if (sessionStorage.getItem(key)) return
      sessionStorage.setItem(key, '1')
    } catch {
      // Without a persistent marker, reloading could loop indefinitely.
      return
    }
    window.location.reload()
  }, [error])

  const status = getHttpStatus(error)
  const isRateLimited = status === 429
  const title = isRateLimited
    ? t('Too many requests')
    : `${t('Oops! Something went wrong')} ${`:')`}`
  const description = isRateLimited
    ? t('Please wait a moment before trying again.')
    : t('Please try again later.')

  return (
    <div
      className={cn(
        'flex min-h-svh w-full items-center justify-center px-6 py-24',
        className
      )}
    >
      <div className='flex w-full max-w-lg flex-col items-center gap-4 text-center'>
        {!minimal && status !== undefined && (
          <h1 className='text-faint/40 text-[120px] leading-[0.9] font-extrabold tracking-[-0.05em] tabular-nums select-none'>
            {status}
          </h1>
        )}
        <p className='text-foreground text-lg font-semibold tracking-tight'>
          {title}
        </p>
        <p className='text-muted-foreground max-w-sm text-sm leading-relaxed'>
          {t('We apologize for the inconvenience.')} {description}
        </p>
        {!minimal && supportEmail && (
          <p className='text-muted-foreground text-xs leading-relaxed'>
            {t('If this keeps happening, please email {{email}}.', {
              email: supportEmail,
            })}
          </p>
        )}
        {!minimal && !supportEmail && site?.support_contact && (
          <p className='text-muted-foreground max-w-full text-xs break-words'>
            {site.support_contact}
          </p>
        )}
        {!minimal && (
          <div className='mt-6 flex flex-wrap justify-center gap-3'>
            <Button variant='outline' onClick={() => history.go(-1)}>
              {t('Go Back')}
            </Button>
            {supportEmail && (
              <Button
                variant='outline'
                render={<a href={`mailto:${supportEmail}`} />}
              >
                {t('Report an issue')}
              </Button>
            )}
            {!supportEmail && subsiteDocument() && (
              <a className={buttonVariants({ variant: 'outline' })} href='/profile#tickets'>
                {t('Report an issue')}
              </a>
            )}
            <Button variant='brand' onClick={() => navigate({ to: '/' })}>
              {t('Back to Home')}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}
