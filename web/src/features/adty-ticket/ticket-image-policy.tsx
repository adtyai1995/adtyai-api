import { useState } from 'react'
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
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState(props.policy)
  const [pending, setPending] = useState(false)
  const [feedback, setFeedback] = useState('')
  async function save() {
    setPending(true)
    setFeedback('')
    try {
      const response = await api.put(
        '/api/adty/admin/ticket-image-policy',
        draft
      )
      requireServerSuccess(response.data)
      await props.onSaved()
      setFeedback(t('Saved'))
    } catch (cause) {
      setFeedback(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }
  return (
    <details>
      <summary>{t('Ticket image limits')}</summary>
      <form
        className='my-3 grid gap-2'
        onSubmit={(e) => {
          e.preventDefault()
          void save()
        }}
      >
        {(Object.keys(fields) as (keyof TicketPolicy)[]).map((key) => (
          <Label key={key} className='flex flex-wrap items-center gap-2'>
            {t(fields[key])}
            <Input
              type='number'
              required
              min={1}
              value={draft[key]}
              onChange={(e) =>
                setDraft({ ...draft, [key]: Number(e.target.value) })
              }
            />
          </Label>
        ))}
        <Button disabled={pending}>{t('Save')}</Button>
        {feedback && <p role='status'>{feedback}</p>}
      </form>
    </details>
  )
}
