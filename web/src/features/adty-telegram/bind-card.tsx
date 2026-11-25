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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { api } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'

type BindView = {
  status: string
  telegram_user_id: number
  purpose: string
}

function boundLabel(
  bound: boolean,
  status: string,
  t: (key: string) => string
) {
  if (bound) return t('Bound')
  if (status === 'needs_reconfirm') return t('Needs confirmation')
  return t('Not bound')
}

export function TelegramBindCard(props: {
  purpose: 'user' | 'admin'
  platformSupport?: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const siteOwner = props.purpose === 'admin' && !props.platformSupport
  const base = siteOwner ? '/api/adty/site/telegram' : '/api/adty/telegram'
  const params = { purpose: props.purpose }
  const bindingPath = siteOwner ? base : `${base}/me`
  const status = useQuery({
    queryKey: ['adty-telegram', props.purpose],
    queryFn: async () => {
      const response = await api.get(bindingPath, { params })
      const body = response.data as {
        success?: boolean
        data?: BindView
        message?: string
      }
      if (!body.success || !body.data) {
        throw createServerError(body, t('Failed to load data'))
      }
      return body.data
    },
  })
  const [code, setCode] = useState('')
  const [botUsername, setBotUsername] = useState('')
  const [expiresAt, setExpiresAt] = useState(0)
  const [telegramId, setTelegramId] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  const start = async () => {
    setPending(true)
    setError('')
    try {
      const response = await api.post(`${base}/bind/start`, {}, { params })
      const body = response.data as {
        success?: boolean
        message?: string
        data?: { code?: string; bot_username?: string; expires_at?: number }
      }
      if (!body.success || !body.data?.code) {
        throw createServerError(body, t('Failed to save'))
      }
      setCode(body.data.code)
      setBotUsername(body.data.bot_username ?? '')
      setExpiresAt(body.data.expires_at ?? 0)
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const confirm = async () => {
    setPending(true)
    setError('')
    try {
      const response = await api.post(`${base}/bind/confirm`, {
        purpose: props.purpose,
        code,
        telegram_user_id: Number(telegramId),
      })
      const body = response.data as { success?: boolean; message?: string }
      if (!body.success) throw createServerError(body, t('Failed to save'))
      setCode('')
      setBotUsername('')
      setExpiresAt(0)
      setTelegramId('')
      await queryClient.invalidateQueries({
        queryKey: ['adty-telegram', props.purpose],
      })
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const unbind = async () => {
    setPending(true)
    setError('')
    try {
      const response = await api.delete(bindingPath, { params })
      const body = response.data as { success?: boolean; message?: string }
      if (!body.success) throw createServerError(body, t('Failed to save'))
      setCode('')
      setBotUsername('')
      setExpiresAt(0)
      setTelegramId('')
      await queryClient.invalidateQueries({
        queryKey: ['adty-telegram', props.purpose],
      })
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const bound = status.data?.status === 'active'
  return (
    <SettingsSection
      title={t(props.purpose === 'admin' ? 'Admin bot' : 'Telegram notices')}
      description={t(
        'This bot only sends notifications. Account changes stay on the website.'
      )}
      bodyClassName='flex flex-col gap-4'
    >
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='text-sm'>
          {status.isLoading && (
            <span className='text-muted-foreground'>{t('Loading...')}</span>
          )}
          {status.data && (
            <span className='inline-flex items-center gap-2'>
              <span
                aria-hidden
                className={
                  bound
                    ? 'bg-success inline-block size-2 rounded-full'
                    : 'bg-muted-foreground/40 inline-block size-2 rounded-full'
                }
              />
              <span className='font-medium'>
                {boundLabel(bound, status.data.status, t)}
              </span>
              {status.data.telegram_user_id > 0 && (
                <span className='text-muted-foreground font-mono text-xs tabular-nums'>
                  {status.data.telegram_user_id}
                </span>
              )}
            </span>
          )}
        </div>
        <div className='flex flex-wrap items-center gap-2'>
          {status.isError && (
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() => void status.refetch()}
            >
              {t('Retry')}
            </Button>
          )}
          {bound ? (
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={pending}
              onClick={() => void unbind()}
            >
              {t('Unbind Telegram')}
            </Button>
          ) : (
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={pending}
              onClick={() => void start()}
            >
              {t('Start binding')}
            </Button>
          )}
        </div>
      </div>
      {!bound && code && (
        <div className='bg-muted flex max-w-xl flex-col gap-1.5 rounded-[12px] px-4 py-3 text-sm'>
          {botUsername && (
            <div className='flex flex-wrap items-center gap-2'>
              <span className='font-mono text-xs'>@{botUsername}</span>
              <a
                href={`https://t.me/${encodeURIComponent(botUsername)}?start=${encodeURIComponent(code)}`}
                target='_blank'
                rel='noreferrer'
                className='text-primary underline underline-offset-2'
              >
                {t('Open Telegram bot')}
              </a>
            </div>
          )}
          <p className='break-all'>
            {t('Send the following command to the bot')}:{' '}
            <code className='font-mono text-xs'>/start {code}</code>
          </p>
          <p className='text-muted-foreground text-xs break-all'>
            {t('Binding code')}: {code}
          </p>
          {expiresAt > 0 && (
            <p className='text-muted-foreground text-xs'>
              {t('Expires at')}: {formatTimestamp(expiresAt)}
            </p>
          )}
        </div>
      )}
      {!bound && (
        <div className='flex flex-wrap items-end gap-3'>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor={`tg-id-${props.purpose}`}>
              {t('Telegram user id')}
            </Label>
            <Input
              id={`tg-id-${props.purpose}`}
              className='w-56 font-mono tabular-nums'
              value={telegramId}
              inputMode='numeric'
              onChange={(event) => setTelegramId(event.target.value)}
            />
          </div>
          <Button
            variant='brand'
            type='button'
            disabled={pending || code === '' || telegramId.trim() === ''}
            onClick={() => void confirm()}
          >
            {t('Confirm binding')}
          </Button>
        </div>
      )}
      {error && <p className='text-destructive text-sm'>{error}</p>}
    </SettingsSection>
  )
}
