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
import { useMutation } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

import type { useWithdrawalIntent } from './use-withdrawal-intent'

export function WithdrawalRecovery({
  intent,
  busy,
  onReset,
  onRecovered,
}: {
  intent: ReturnType<typeof useWithdrawalIntent>
  busy: boolean
  onReset: () => void
  onRecovered: () => void
}) {
  const { t } = useTranslation()
  const check = useMutation({
    mutationFn: intent.check,
    onSuccess: (id) => {
      if (id) onRecovered()
    },
    onError: (err) => handleServerError(err),
  })
  const saved = intent.saved
  if (!saved) return null
  let message = t('Previous request: {{amount}} USD', { amount: saved.amount })
  if (saved.receipt) {
    message = t('Previous request accepted: #{{id}}', { id: saved.receipt })
  } else if (saved.accepted) {
    message = t('Previous request accepted')
  }
  return (
    <div className='flex flex-wrap items-center gap-2 text-sm' role='status'>
      <span>{message}</span>
      {saved.accepted ? (
        <Button
          type='button'
          variant='outline'
          onClick={() => {
            onReset()
            intent.clear(saved.key)
            check.reset()
          }}
        >
          {t('Start a new request')}
        </Button>
      ) : (
        <>
          <Button
            type='button'
            variant='outline'
            disabled={busy || check.isPending}
            onClick={() => check.mutate()}
          >
            {t('Check previous request')}
          </Button>
          {check.data === 0 && (
            <span>
              {t('No accepted request found. Confirm the details to retry.')}
            </span>
          )}
        </>
      )}
    </div>
  )
}
