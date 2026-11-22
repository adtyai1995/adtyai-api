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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { api } from '@/lib/api'
import {
  createServerError,
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

export function TicketImage(props: {
  scope: string
  userId?: number
  ticketID: number
  grant?: string
  expiresAt?: number
  imageID: number
  label: string
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: [
      'adty-tickets',
      props.userId,
      props.scope,
      'image',
      props.ticketID,
      props.imageID,
    ],
    queryFn: async ({ signal }) => {
      const path = `/api/adty/tickets/${props.ticketID}/images/${props.imageID}`
      let grant = props.grant
      if (!grant || (props.expiresAt ?? 0) * 1000 <= Date.now() + 1000) {
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
        grant = requireServerSuccess(authorized.data).data?.grant
      }
      if (!grant) {
        throw new Error(t('Failed to load data'))
      }
      const response = await api.get<Blob>(path, {
        params: { scope: props.scope },
        headers: { 'X-Ticket-Image-Grant': grant },
        responseType: 'blob',
        signal,
        disableDuplicate: true,
        skipErrorHandler: true,
        skipBusinessError: true,
      })
      if (
        !['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(
          response.data.type
        )
      ) {
        if (response.data.type.includes('json')) {
          const failure: unknown = JSON.parse(await response.data.text())
          throw createServerError(failure, t('Failed to load data'))
        }
        throw new Error(t('Failed to load data'))
      }
      return response.data
    },
    gcTime: 0,
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
      <img
        src={src}
        alt={props.label}
        className='h-full w-full rounded border object-contain'
        onError={() => setFailed(query.data ?? null)}
      />
    )
  }
  return (
    <div className='flex min-w-0 flex-col gap-1 text-sm'>
      {src ? (
        <a
          href={src}
          target='_blank'
          rel='noreferrer'
          className='break-all underline'
        >
          {props.label}
        </a>
      ) : (
        <span className='break-all'>{props.label}</span>
      )}
      <div className='h-48 w-full max-w-xs overflow-auto'>{content}</div>
    </div>
  )
}
