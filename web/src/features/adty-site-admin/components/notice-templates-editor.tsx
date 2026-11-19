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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

export type NoticeTemplate = {
  id?: number
  key: string
  category: string
  title_zh: string
  content_zh: string
  title_en: string
  content_en: string
  revision?: number
  updated_at?: number
}

const noticeTemplateKey = ['adty-notice-templates'] as const

function sameText(left: string, right: string) {
  return left.trim() === right.trim()
}

function NoticeTemplateRow(props: { row: NoticeTemplate }) {
  const { t } = useTranslation()
  const base = useId()
  const client = useQueryClient()
  const [draft, setDraft] = useState(props.row)
  const save = useMutation({
    mutationFn: async () => {
      const response = await api.put(
        `/api/adty/admin/notice-templates/${encodeURIComponent(props.row.key)}`,
        {
          title_zh: draft.title_zh.trim(),
          content_zh: draft.content_zh.trim(),
          title_en: draft.title_en.trim(),
          content_en: draft.content_en.trim(),
        }
      )
      return requireServerSuccess(response.data).data as NoticeTemplate
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: noticeTemplateKey })
    },
    onError: (error) => handleServerError(error),
  })
  const unchanged =
    sameText(draft.title_zh, props.row.title_zh) &&
    sameText(draft.content_zh, props.row.content_zh) &&
    sameText(draft.title_en, props.row.title_en) &&
    sameText(draft.content_en, props.row.content_en)
  const incomplete =
    draft.title_zh.trim() === '' ||
    draft.content_zh.trim() === '' ||
    draft.title_en.trim() === '' ||
    draft.content_en.trim() === ''

  return (
    <fieldset className='space-y-3 border-t pt-4' disabled={save.isPending}>
      <legend className='text-sm font-medium'>
        {props.row.key}
        <span className='text-muted-foreground ml-2 font-normal'>
          {props.row.category}
          {props.row.revision ? ` · ${props.row.revision}` : ''}
        </span>
      </legend>
      <div className='grid gap-3 md:grid-cols-2'>
        <div className='space-y-1'>
          <Label htmlFor={`${base}-title-zh`}>{t('Chinese title')}</Label>
          <Input
            id={`${base}-title-zh`}
            value={draft.title_zh}
            onChange={(event) =>
              setDraft((current) => ({
                ...current,
                title_zh: event.target.value,
              }))
            }
          />
        </div>
        <div className='space-y-1'>
          <Label htmlFor={`${base}-title-en`}>{t('English title')}</Label>
          <Input
            id={`${base}-title-en`}
            value={draft.title_en}
            onChange={(event) =>
              setDraft((current) => ({
                ...current,
                title_en: event.target.value,
              }))
            }
          />
        </div>
      </div>
      <div className='grid gap-3 md:grid-cols-2'>
        <div className='space-y-1'>
          <Label htmlFor={`${base}-body-zh`}>{t('Chinese body')}</Label>
          <Textarea
            id={`${base}-body-zh`}
            value={draft.content_zh}
            onChange={(event) =>
              setDraft((current) => ({
                ...current,
                content_zh: event.target.value,
              }))
            }
          />
        </div>
        <div className='space-y-1'>
          <Label htmlFor={`${base}-body-en`}>{t('English body')}</Label>
          <Textarea
            id={`${base}-body-en`}
            value={draft.content_en}
            onChange={(event) =>
              setDraft((current) => ({
                ...current,
                content_en: event.target.value,
              }))
            }
          />
        </div>
      </div>
      <Button
        type='button'
        size='sm'
        disabled={unchanged || incomplete || save.isPending}
        onClick={() => save.mutate()}
      >
        {t('Save template')}
      </Button>
    </fieldset>
  )
}

/** Root editor for fixed bilingual system notices (ADTY-19-07). */
export function NoticeTemplatesEditor() {
  const { t } = useTranslation()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const templates = useQuery({
    queryKey: noticeTemplateKey,
    enabled: isRoot,
    queryFn: async () => {
      const response = await api.get('/api/adty/admin/notice-templates')
      const rows = requireServerSuccess(response.data).data
      return (Array.isArray(rows) ? rows : []) as NoticeTemplate[]
    },
  })

  return (
    <SettingsSection
      title={t('Fixed notice templates')}
      description={t(
        'Root maintains the Chinese and English text of fixed system notices. Placeholders must match in both languages. A sent notice uses Chinese first, then English.'
      )}
    >
      {!isRoot && (
        <p className='text-sm'>{t('Only Root can edit fixed notice templates.')}</p>
      )}
      {isRoot && templates.isPending && <LoadingState />}
      {isRoot && templates.isError && (
        <ErrorState onRetry={() => void templates.refetch()} />
      )}
      {isRoot && templates.isSuccess && (
        <div className='space-y-4'>
          {templates.data.map((row) => (
            <NoticeTemplateRow
              key={`${row.key}:${row.revision ?? 0}:${row.updated_at ?? 0}`}
              row={row}
            />
          ))}
        </div>
      )}
    </SettingsSection>
  )
}
