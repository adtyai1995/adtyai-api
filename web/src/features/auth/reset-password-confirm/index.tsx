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
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useCountdown } from '@/hooks/use-countdown'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import {
  ACCOUNT_PASSWORD_MAX_LENGTH,
  ACCOUNT_PASSWORD_MIN_LENGTH,
  countPasswordCharacters,
} from '@/lib/password-policy'
import { AuthOperationError } from '@/lib/secure-verification'
import { createServerError } from '@/lib/server-error-message'

import { AuthLayout } from '../auth-layout'

function isRateLimitedError(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) return false
  if ('response' in error) {
    const response = (error as { response?: { status?: number } }).response
    if (response?.status === 429) return true
  }
  if ('status' in error && (error as { status?: unknown }).status === 429) {
    return true
  }
  return false
}

export type ResetPasswordSearchParams = {
  email?: string
  token?: string
  username?: string
}

type ResetPasswordConfirmProps = ResetPasswordSearchParams

export function ResetPasswordConfirm({
  email,
  token,
  username,
}: ResetPasswordConfirmProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [done, setDone] = useState(false)
  const [loading, setLoading] = useState(false)
  const {
    secondsLeft,
    isActive,
    start: startCountdown,
  } = useCountdown({ initialSeconds: 30 })

  const isValidResetLink = Boolean(email && token)
  const passwordLength = countPasswordCharacters(password)
  const isPasswordLengthValid =
    passwordLength >= ACCOUNT_PASSWORD_MIN_LENGTH &&
    passwordLength <= ACCOUNT_PASSWORD_MAX_LENGTH

  async function handleSubmit() {
    if (!isValidResetLink || !email || !token) {
      toast.error(t('Invalid reset link, please request a new password reset'))
      return
    }

    if (!isPasswordLengthValid) {
      toast.error(t('Password must contain between 8 and 128 characters.'))
      return
    }
    setLoading(true)
    try {
      const res = await api.post(
        '/api/user/reset',
        { email, token, password, username },
        {
          skipBusinessError: true,
        } as Record<string, unknown>
      )

      if (res?.data?.success) {
        setDone(true)
        startCountdown()
        toast.success(t('Saved successfully'))
      } else {
        if (res?.status === 429) {
          startCountdown()
        }
        handleServerError(createServerError(res.data, t('Request failed')))
      }
    } catch (error) {
      if (isRateLimitedError(error)) {
        startCountdown()
      }
      handleServerError(AuthOperationError.from(error))
    } finally {
      setLoading(false)
    }
  }

  return (
    <AuthLayout>
      <div className='w-full space-y-8'>
        <div className='space-y-2'>
          <h2 className='text-center text-2xl font-semibold tracking-tight sm:text-left'>
            {t('Reset password')}
          </h2>
          <p className='text-muted-foreground text-left text-sm sm:text-base'>
            {done
              ? t('auth.resetPasswordConfirm.success')
              : t('auth.resetPasswordConfirm.description')}
          </p>
        </div>

        <div className='space-y-4'>
          {!isValidResetLink && (
            <Alert variant='destructive'>
              <AlertDescription>
                {t('Invalid reset link, please request a new password reset.')}
              </AlertDescription>
            </Alert>
          )}

          <div className='space-y-2'>
            <Label htmlFor='email'>{t('Email')}</Label>
            <Input
              id='email'
              type='email'
              value={email || ''}
              disabled
              placeholder={t('Waiting for email...')}
            />
          </div>

          {!done && (
            <div className='space-y-2'>
              <Label htmlFor='password'>{t('New password')}</Label>
              <Input
                id='password'
                type='password'
                autoComplete='new-password'
                maxLength={ACCOUNT_PASSWORD_MAX_LENGTH}
                placeholder={t('Enter password (8–128 characters)')}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                disabled={!isValidResetLink || loading}
              />
            </div>
          )}

          <Button
            className='w-full'
            onClick={
              done
                ? () => navigate({ to: '/sign-in', replace: true })
                : handleSubmit
            }
            disabled={
              done
                ? false
                : loading ||
                  isActive ||
                  !isValidResetLink ||
                  !isPasswordLengthValid
            }
          >
            {done && t('auth.resetPasswordConfirm.backToLogin')}
            {!done &&
              isActive &&
              t('auth.resetPasswordConfirm.retry', {
                seconds: secondsLeft,
              })}
            {!done && !isActive && t('auth.resetPasswordConfirm.confirm')}
          </Button>

          {!done && (
            <Button
              variant='link'
              className='w-full'
              onClick={() => navigate({ to: '/sign-in', replace: true })}
            >
              {t('Back to login')}
            </Button>
          )}
        </div>
      </div>
    </AuthLayout>
  )
}
