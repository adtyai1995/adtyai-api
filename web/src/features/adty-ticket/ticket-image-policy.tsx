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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

const fields = {
  max_bytes: 'Bytes per image',
  max_count: 'Images per ticket',
  max_total: 'Total bytes per ticket',
  max_side: 'Maximum image side (pixels)',
  max_pixels: 'Maximum image pixels',
} as const
export type TicketPolicy = Record<keyof typeof fields, number>

export function TicketImagePolicySettings(props: {
  policy: TicketPolicy
  onSaved: () => Promise<void>
  embedded?: boolean
}) {
  const { t } = useTranslation()
  const { max_bytes, max_count, max_total, max_side, max_pixels } = props.policy
  const [draft, setDraft] = useState<TicketPolicy>(() => ({
    max_bytes,
    max_count,
    max_total,
    max_side,
    max_pixels,
  }))
  const [dirty, setDirty] = useState(false)
  const [pending, setPending] = useState(false)
  const [feedback, setFeedback] = useState('')

  useEffect(() => {
    if (!dirty) {
      setDraft({ max_bytes, max_count, max_total, max_side, max_pixels })
    }
  }, [dirty, max_bytes, max_count, max_total, max_side, max_pixels])

  async function save() {
    setPending(true)
    setFeedback('')
    try {
      const response = await api.put(
        '/api/adty/admin/ticket-image-policy',
        draft
      )
      requireServerSuccess(response.data)
      setDirty(false)
      await props.onSaved()
      setFeedback(t('Saved'))
    } catch (cause) {
      setFeedback(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }
  const form = (
    <form
      className={props.embedded ? 'grid gap-3' : 'my-3 grid gap-2'}
      onSubmit={(e) => {
        e.preventDefault()
        void save()
      }}
    >
      {(Object.keys(fields) as (keyof TicketPolicy)[]).map((key) => (
        <Label key={key} className='flex flex-col items-start gap-1.5'>
          <span>{t(fields[key])}</span>
          <Input
            type='number'
            required
            min={1}
            value={draft[key]}
            onChange={(e) => {
              setDirty(true)
              setDraft({ ...draft, [key]: Number(e.target.value) })
            }}
          />
        </Label>
      ))}
      <Button type='submit' variant='brand' disabled={pending}>
        {t('Save')}
      </Button>
      {feedback && (
        <p role='status' className='text-muted-foreground text-xs'>
          {feedback}
        </p>
      )}
    </form>
  )
  if (props.embedded) {
    return form
  }
  return (
    <details>
      <summary>{t('Ticket image limits')}</summary>
      {form}
    </details>
  )
}
