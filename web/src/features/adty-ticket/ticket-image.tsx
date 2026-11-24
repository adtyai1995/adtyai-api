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
import { useQuery } from '@tanstack/react-query'
import { ZoomIn } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { api } from '@/lib/api'
import {
  createServerError,
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

const VALID_IMAGE_MIMES = new Set([
  'image/png',
  'image/jpeg',
  'image/gif',
  'image/webp',
])

function normalizeMimeType(raw: string | undefined): string {
  return (raw ?? '').split(';')[0].trim().toLowerCase()
}

export function TicketImage(props: {
  scope: string
  userId?: number
  ticketID: number
  grant?: string
  expiresAt?: number
  imageID: number
  label: string
  onDelete?: () => void
  deleting?: boolean
}) {
  const { t } = useTranslation()
  const [lightboxOpen, setLightboxOpen] = useState(false)

  const query = useQuery({
    queryKey: [
      'adty-ticket-image',
      props.userId,
      props.scope,
      props.ticketID,
      props.imageID,
    ],
    queryFn: async ({ signal }) => {
      const path = `/api/adty/tickets/${props.ticketID}/images/${props.imageID}`

      const requestFreshGrant = async (): Promise<string> => {
        const authorized = await api.get<{
          success: boolean
          message?: string
          data?: { grant: string; expires_at: number }
        }>(path, {
          params: { action: 'grant', scope: props.scope },
          signal,
          disableDuplicate: true,
          skipErrorHandler: true,
        })
        const freshGrant = requireServerSuccess(authorized.data).data?.grant
        if (!freshGrant) {
          throw new Error(t('Failed to load data'))
        }
        return freshGrant
      }

      const fetchBlobWithGrant = async (grant: string) => {
        const response = await api.get<Blob>(path, {
          params: { scope: props.scope },
          headers: { 'X-Ticket-Image-Grant': grant },
          responseType: 'blob',
          signal,
          disableDuplicate: true,
          skipErrorHandler: true,
          skipBusinessError: true,
        })
        const headerType =
          typeof response.headers?.['content-type'] === 'string'
            ? response.headers['content-type']
            : ''
        const mime = normalizeMimeType(response.data.type || headerType)
        return { blob: response.data, mime }
      }

      const canTryPropGrant = Boolean(
        props.grant && (props.expiresAt ?? 0) * 1000 > Date.now() + 1000
      )

      let result: { blob: Blob; mime: string } | null = null
      if (canTryPropGrant && props.grant) {
        try {
          const initial = await fetchBlobWithGrant(props.grant)
          if (VALID_IMAGE_MIMES.has(initial.mime)) {
            return initial.blob
          }
          // Non-image payload (e.g. expired grant / clock skew JSON error):
          // refresh grant once and retry below.
        } catch (cause) {
          if (signal.aborted) throw cause
          // HTTP error (e.g. 403) with prop grant: refresh grant once below.
        }
        const freshGrant = await requestFreshGrant()
        result = await fetchBlobWithGrant(freshGrant)
      } else {
        const freshGrant = await requestFreshGrant()
        result = await fetchBlobWithGrant(freshGrant)
      }

      if (!VALID_IMAGE_MIMES.has(result.mime)) {
        if (result.mime.includes('json')) {
          const failure: unknown = JSON.parse(await result.blob.text())
          throw createServerError(failure, t('Failed to load data'))
        }
        throw new Error(t('Failed to load data'))
      }
      return result.blob
    },
    staleTime: 5 * 60 * 1000,
    gcTime: 5 * 60 * 1000,
    retry: false,
    meta: { errorToast: false },
  })

  const [preview, setPreview] = useState<{ blob: Blob; src: string } | null>(
    null
  )
  const [failed, setFailed] = useState<Blob | null>(null)
  useEffect(() => {
    if (!query.data) return
    const src = URL.createObjectURL(query.data)
    setPreview({ blob: query.data, src })
    return () => URL.revokeObjectURL(src)
  }, [query.data])

  const unavailable =
    query.isError || (query.data !== undefined && failed === query.data)
  const src =
    !unavailable && preview?.blob === query.data ? preview?.src : undefined

  let content = <LoadingState className='h-full min-h-0' size='sm' />
  if (unavailable) {
    content = (
      <ErrorState
        className='min-h-0 p-3'
        title={t('Failed to load data')}
        description={getServerErrorMessage(
          query.error,
          t('Failed to load data')
        )}
        onRetry={() => void query.refetch()}
      />
    )
  } else if (src) {
    content = (
      <button
        type='button'
        className='group focus-visible:ring-ring relative block h-full w-full cursor-zoom-in overflow-hidden rounded border text-left focus-visible:ring-2 focus-visible:outline-none'
        aria-label={t('Preview image {{label}}', { label: props.label })}
        onClick={() => setLightboxOpen(true)}
      >
        <img
          src={src}
          alt={props.label}
          className='h-full w-full object-contain'
          onError={() => setFailed(query.data ?? null)}
        />
        <span className='bg-background/85 text-foreground pointer-events-none absolute right-1.5 bottom-1.5 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs opacity-0 shadow-xs transition-opacity group-hover:opacity-100'>
          <ZoomIn className='size-3.5' aria-hidden='true' />
          {t('Zoom')}
        </span>
      </button>
    )
  }

  return (
    <div className='flex min-w-0 flex-col gap-1 text-sm'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        {src ? (
          <button
            type='button'
            className='text-primary cursor-zoom-in text-left break-all underline underline-offset-4'
            onClick={() => setLightboxOpen(true)}
          >
            {props.label}
          </button>
        ) : (
          <span className='break-all'>{props.label}</span>
        )}
        {props.onDelete && (
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={props.deleting}
            onClick={props.onDelete}
          >
            {t('Delete image')}
          </Button>
        )}
      </div>
      <div className='h-48 w-full max-w-xs overflow-hidden'>{content}</div>
      {src && (
        <Dialog open={lightboxOpen} onOpenChange={setLightboxOpen}>
          <DialogContent className='max-h-[90vh] w-[min(92vw,72rem)] max-w-[min(92vw,72rem)] p-4 sm:max-w-[min(92vw,72rem)]'>
            <DialogHeader>
              <DialogTitle className='pr-8 break-all'>
                {props.label}
              </DialogTitle>
            </DialogHeader>
            <div className='flex max-h-[78vh] items-center justify-center overflow-auto'>
              <img
                src={src}
                alt={props.label}
                className='max-h-[75vh] max-w-full rounded object-contain'
              />
            </div>
          </DialogContent>
        </Dialog>
      )}
    </div>
  )
}
