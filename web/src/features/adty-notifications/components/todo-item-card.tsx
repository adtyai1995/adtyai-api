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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import {
  extractAnnouncementPlainText,
  resolveLocalizedAnnouncement,
  stripTranslationNotReadyMarkers,
} from '@/features/dashboard/lib/text'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

export type SevereTodo = {
  id: number
  key: string
  title: string
  title_en?: string
  title_translation_status?: string
  content: string
  content_en?: string
  english?: string
  source?: string
  translation_status?: string
  translation_notice?: string
  content_format?: string
  status: string
  revision: number
}

function sanitizeTodoPlainText(raw: string): string {
  if (!raw) return ''
  const stripped = stripTranslationNotReadyMarkers(raw)
  if (!stripped) return ''
  const plain = extractAnnouncementPlainText(stripped)
  return stripTranslationNotReadyMarkers(plain)
}

const todoListKey = ['adty-severe-todos'] as const

function statusLabel(
  status: string,
  t: (key: string) => string
): string {
  if (status === 'pending') return t('Todo status pending')
  if (status === 'in_progress') return t('Todo status in progress')
  if (status === 'completed') return t('Completed')
  return status
}

export function TodoItemCard(props: {
  item: SevereTodo
  pending?: boolean
  onProgress: (key: string) => void
  onComplete: (key: string) => void
}) {
  const { t, i18n } = useTranslation()
  const item = props.item
  const currentLanguage = i18n.resolvedLanguage || i18n.language
  const normalizedSource =
    item.source?.trim() ||
    (item.key.startsWith('watcher:') ? 'channel_watcher' : undefined)
  const { title, content, translationNotReady } = resolveLocalizedAnnouncement(
    {
      ...item,
      source: normalizedSource,
    },
    currentLanguage
  )
  const displayTitle = sanitizeTodoPlainText(title) || item.key
  const displayContent = sanitizeTodoPlainText(content)
  return (
    <article className='space-y-2 rounded-md border p-2'>
      <div className='flex items-start justify-between gap-2'>
        <p className='text-sm font-medium'>{displayTitle}</p>
        <span className='text-muted-foreground text-xs'>
          {statusLabel(item.status, t)}
        </span>
      </div>
      {displayContent ? (
        <p className='text-muted-foreground text-xs whitespace-pre-line break-words'>
          {displayContent}
        </p>
      ) : null}
      {translationNotReady ? (
        <p className='text-muted-foreground text-xs'>
          {t('English is not ready')}
        </p>
      ) : null}
      <div className='flex gap-2'>
        {item.status === 'pending' && (
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={props.pending || item.key.trim() === ''}
            onClick={() => props.onProgress(item.key)}
          >
            {t('Mark in progress')}
          </Button>
        )}
        {item.status !== 'completed' && (
          <Button
            type='button'
            size='sm'
            disabled={props.pending || item.key.trim() === ''}
            onClick={() => props.onComplete(item.key)}
          >
            {t('Mark complete')}
          </Button>
        )}
      </div>
    </article>
  )
}

/** Open severe todos for the signed-in account. Mount only while the center is open. */
export function SevereTodoList() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const todos = useQuery({
    queryKey: todoListKey,
    queryFn: async () => {
      const response = await api.get('/api/adty/notifications/todos')
      const rows = requireServerSuccess(response.data).data
      return (Array.isArray(rows) ? rows : []) as SevereTodo[]
    },
  })
  const progress = useMutation({
    mutationFn: async (key: string) => {
      const response = await api.post(
        '/api/adty/notifications/todo/in-progress',
        { key }
      )
      requireServerSuccess(response.data)
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: todoListKey })
      void client.invalidateQueries({ queryKey: ['adty-notifications'] })
    },
    onError: (error) => handleServerError(error),
  })
  const complete = useMutation({
    mutationFn: async (key: string) => {
      const response = await api.post('/api/adty/notifications/todo/complete', {
        key,
      })
      requireServerSuccess(response.data)
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: todoListKey })
      void client.invalidateQueries({ queryKey: ['adty-notifications'] })
    },
    onError: (error) => handleServerError(error),
  })
  const pending = progress.isPending || complete.isPending

  if (todos.isPending) {
    return <p className='text-muted-foreground text-xs'>{t('Loading...')}</p>
  }
  if (todos.isError) {
    return <ErrorState onRetry={() => void todos.refetch()} className='min-h-24' />
  }
  if (todos.data.length === 0) return null

  return (
    <section className='space-y-2' aria-label={t('Severe todos')}>
      <h3 className='text-sm font-medium'>{t('Severe todos')}</h3>
      {todos.data.map((item) => (
        <TodoItemCard
          key={`${item.id}:${item.revision}`}
          item={item}
          pending={pending}
          onProgress={(key) => progress.mutate(key)}
          onComplete={(key) => complete.mutate(key)}
        />
      ))}
    </section>
  )
}
