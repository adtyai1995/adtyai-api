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
import { formatTimestamp } from '@/lib/format'

export type WithdrawalCancelState = {
  can_cancel: boolean
  cancel_deadline: number
  cancel_reason: string
}

export function WithdrawalCancellation({
  state,
  busy,
  onCancel,
}: {
  state: WithdrawalCancelState
  busy?: boolean
  onCancel?: () => void
}) {
  const { t } = useTranslation()
  const [now, setNow] = useState(Date.now)
  const deadline = state.cancel_deadline * 1000
  useEffect(() => {
    if (!state.can_cancel || deadline <= now) return
    const timeout = window.setTimeout(
      () => setNow(Date.now()),
      Math.min(2147483647, Math.max(0, deadline - Date.now()))
    )
    return () => window.clearTimeout(timeout)
  }, [state.can_cancel, deadline, now])
  const available = state.can_cancel && Date.now() < deadline
  return (
    <span className='flex flex-wrap items-center gap-2 text-sm'>
      {deadline > 0 && (
        <span>
          {t('Cancellation deadline')} {formatTimestamp(state.cancel_deadline)}
        </span>
      )}
      {available && !onCancel && <span>{t('Can be canceled')}</span>}
      {available && onCancel && (
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={busy}
          onClick={onCancel}
        >
          {t('Cancel')}
        </Button>
      )}
      {!available && (
        <span>{t(state.cancel_reason || 'The cancel window has passed')}</span>
      )}
    </span>
  )
}
