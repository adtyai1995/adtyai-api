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
import { AlertTriangle, CheckCircle2, KeyRound } from 'lucide-react'
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { api } from '@/lib/api'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { AuthOperationError } from '@/lib/secure-verification'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { ERROR_MESSAGES } from '../constants'
import type { ApiResponse, User } from '../types'
import { useUsers } from './users-provider'

const RANDOM_PASSWORD_LENGTHS = [12, 16, 20, 24] as const
type RandomPasswordLength = (typeof RANDOM_PASSWORD_LENGTHS)[number]
const DEFAULT_PASSWORD_LENGTH: RandomPasswordLength = 16

type ResetPasswordPayload = {
  password?: string
  generated_password?: string
  new_password?: string
  length?: number
  user_id?: number
  username?: string
}

type UsersResetPasswordDialogProps = {
  /** Optional controlled open state (e.g. when triggered from UsersMutateDrawer). */
  open?: boolean
  onOpenChange?: (open: boolean) => void
  user?: User | null
}

/**
 * One-click random password reset modal for main-site administrators.
 * Supports both context mode (mounted in Users page) and controlled mode (mounted in UsersMutateDrawer).
 */
export function UsersResetPasswordDialog(props: UsersResetPasswordDialogProps) {
  const { t } = useTranslation()
  const fieldId = useId()
  const {
    open: ctxOpen,
    setOpen: setCtxOpen,
    currentRow,
    triggerRefresh,
    requestVerification,
    verificationActive,
  } = useUsers()

  const isControlled = props.open !== undefined
  const isOpen = isControlled ? props.open : ctxOpen === 'reset-password'
  const targetUser = isControlled ? (props.user ?? null) : currentRow

  const [length, setLength] = useState<RandomPasswordLength>(
    DEFAULT_PASSWORD_LENGTH
  )
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [generatedPassword, setGeneratedPassword] = useState<string | null>(
    null
  )

  const targetUserId = targetUser?.id
  useEffect(() => {
    if (!isOpen) {
      setLength(DEFAULT_PASSWORD_LENGTH)
      setGeneratedPassword(null)
      setIsSubmitting(false)
    }
  }, [isOpen, targetUserId])

  const closeDialog = () => {
    if (isSubmitting) return
    if (isControlled) {
      props.onOpenChange?.(false)
    } else {
      setCtxOpen(null)
    }
  }

  const handleSubmit = async () => {
    if (!targetUser || isSubmitting) return

    const operator = useAuthStore.getState().auth.user
    if (!canAdmin(operator, ADTY_PERMISSIONS.USER_WRITE)) {
      toast.error(t('No permission to perform this action'))
      return
    }
    if (
      targetUser.role === ROLE.SUPER_ADMIN ||
      (operator?.role !== ROLE.SUPER_ADMIN &&
        (operator?.role ?? 0) <= targetUser.role)
    ) {
      toast.error(
        t('No permission to update users of same or higher permission level')
      )
      return
    }

    const identity = formatUserIdentity(targetUser.id, targetUser.username)
    setIsSubmitting(true)
    try {
      const proof = await requestVerification({
        scope: 'admin.user.update',
        context: { user_id: targetUser.id },
        title: t('Verify to reset user password'),
        description: t(
          'Confirm your identity before resetting the password for {{user}}.',
          { user: identity }
        ),
      })
      if (!proof) return

      const res = await api.post<ApiResponse<ResetPasswordPayload>>(
        `/api/user/${targetUser.id}/reset-password`,
        { random: true, length },
        {
          headers: { 'X-Security-Proof': proof.proof_token },
          singleUseAuthorization: true,
        }
      )
      const result = requireServerSuccess(res.data)
      const nextPassword =
        result.data?.password ||
        result.data?.generated_password ||
        result.data?.new_password ||
        ''
      if (!nextPassword) {
        throw new Error(t('Failed to generate random password'))
      }

      setGeneratedPassword(nextPassword)
      toast.success(t('Random password generated'))
      triggerRefresh()
    } catch (error) {
      handleServerError(
        AuthOperationError.from(error),
        t(ERROR_MESSAGES.UNEXPECTED)
      )
    } finally {
      setIsSubmitting(false)
    }
  }

  const identity = targetUser
    ? formatUserIdentity(targetUser.id, targetUser.username)
    : '-'

  return (
    <Dialog
      open={Boolean(isOpen && targetUser) && !verificationActive}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) closeDialog()
      }}
      title={
        generatedPassword
          ? t('Random password generated')
          : t('Reset random password')
      }
      description={
        generatedPassword
          ? t(
              'Save this one-time password for {{user}} now before closing.',
              { user: identity }
            )
          : t(
              'Generate a new random password for {{user}} and immediately revoke all active sessions.',
              { user: identity }
            )
      }
      contentClassName='sm:max-w-md'
      footer={
        generatedPassword ? (
          <Button type='button' variant='brand' onClick={closeDialog}>
            {t('Close')}
          </Button>
        ) : (
          <>
            <Button
              type='button'
              variant='outline'
              disabled={isSubmitting}
              onClick={closeDialog}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              variant='brand'
              disabled={isSubmitting || !targetUser}
              onClick={() => void handleSubmit()}
            >
              <KeyRound className='size-4' aria-hidden />
              {isSubmitting
                ? t('Generating...')
                : t('Reset random password')}
            </Button>
          </>
        )
      }
    >
      {generatedPassword ? (
        <div className='flex flex-col gap-4'>
          <div className='bg-muted/40 border-border flex items-center justify-between gap-2 rounded-lg border px-3 py-2 text-xs'>
            <span className='text-muted-foreground'>{t('Target user')}</span>
            <span className='font-medium'>{identity}</span>
          </div>

          <div className='space-y-1.5'>
            <Label htmlFor={`${fieldId}-generated`}>
              {t('Generated password')}
            </Label>
            <div className='flex items-center gap-2'>
              <Input
                id={`${fieldId}-generated`}
                value={generatedPassword}
                readOnly
                className='font-mono text-sm'
                onFocus={(event) => event.currentTarget.select()}
              />
              <CopyButton
                value={generatedPassword}
                variant='outline'
                size='sm'
                aria-label={t('Copy password')}
              >
                {t('Copy password')}
              </CopyButton>
            </div>
          </div>

          <Alert className='border-warning/40 bg-warning/5'>
            <AlertTriangle className='text-warning size-4' aria-hidden />
            <AlertDescription className='text-xs'>
              {t(
                'This password is shown only once and cannot be viewed again after closing this dialog. All existing sessions and personal access tokens for this user have been revoked.'
              )}
            </AlertDescription>
          </Alert>

          <div className='text-success flex items-center gap-1.5 text-xs'>
            <CheckCircle2 className='size-3.5 shrink-0' aria-hidden />
            <span>
              {t('All existing sessions and access tokens have been revoked.')}
            </span>
          </div>
        </div>
      ) : (
        <div className='flex flex-col gap-4'>
          <div className='bg-muted/40 border-border flex items-center justify-between gap-2 rounded-lg border px-3 py-2 text-xs'>
            <span className='text-muted-foreground'>{t('Target user')}</span>
            <span className='font-medium'>{identity}</span>
          </div>

          <div className='space-y-2'>
            <Label id={`${fieldId}-length-label`}>
              {t('Password length')}
            </Label>
            <div
              role='group'
              aria-labelledby={`${fieldId}-length-label`}
              className='grid grid-cols-4 gap-2'
            >
              {RANDOM_PASSWORD_LENGTHS.map((option) => (
                <Button
                  key={option}
                  type='button'
                  size='sm'
                  variant={length === option ? 'brand' : 'outline'}
                  aria-pressed={length === option}
                  disabled={isSubmitting}
                  onClick={() => setLength(option)}
                >
                  {t('{{count}} chars', { count: option })}
                </Button>
              ))}
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Uses uppercase letters, lowercase letters, and digits only. Immediately signs out all active sessions and revokes personal access tokens.'
              )}
            </p>
          </div>
        </div>
      )}
    </Dialog>
  )
}
