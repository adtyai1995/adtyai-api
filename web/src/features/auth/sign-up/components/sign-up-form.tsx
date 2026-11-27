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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { z } from 'zod'

import { PasswordInput } from '@/components/password-input'
import {
  PasswordConfirmationStatus,
  PasswordStrength,
} from '@/components/password-strength'
import { Turnstile } from '@/components/turnstile'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { register } from '@/features/auth/api'
import { LegalConsent } from '@/features/auth/components/legal-consent'
import { registerFormSchema } from '@/features/auth/constants'
import { useAuthRedirect } from '@/features/auth/hooks/use-auth-redirect'
import { useEmailVerification } from '@/features/auth/hooks/use-email-verification'
import { useTurnstile } from '@/features/auth/hooks/use-turnstile'
import {
  clearSiteInviteCookie,
  readSiteInviteCookie,
} from '@/features/referral/lib/click'
import { useStatus } from '@/hooks/use-status'
import { handleServerError } from '@/lib/handle-server-error'
import { AuthOperationError } from '@/lib/secure-verification'
import { createServerError } from '@/lib/server-error-message'
import {
  readReferralEnabledFromStatus,
  STATUS_QUERY_KEY,
} from '@/lib/status-query'
import { subsiteDocument } from '@/lib/subsite-document'
import { cn } from '@/lib/utils'

function readInitialSiteInvite(): { code: string; fromUrl: boolean } {
  if (typeof window === 'undefined') return { code: '', fromUrl: false }
  const fromUrl =
    new URLSearchParams(window.location.search).get('site_invite')?.trim() ?? ''
  if (fromUrl) return { code: fromUrl, fromUrl: true }
  if (typeof document === 'undefined') return { code: '', fromUrl: false }
  return { code: readSiteInviteCookie(document)?.code ?? '', fromUrl: false }
}

export function SignUpForm({
  className,
  ...props
}: React.HTMLAttributes<HTMLFormElement>) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const [isLoading, setIsLoading] = useState(false)
  const [verificationCode, setVerificationCode] = useState('')
  const [initialSiteInvite] = useState(readInitialSiteInvite)
  const [siteInviteCode, setSiteInviteCode] = useState(initialSiteInvite.code)
  const [siteInviteEdited, setSiteInviteEdited] = useState(
    initialSiteInvite.fromUrl
  )
  const [agreedToLegal, setAgreedToLegal] = useState(false)
  const [turnstileWidgetKey, setTurnstileWidgetKey] = useState(0)
  const legalConsentErrorMessage = t('Please agree to the legal terms first')

  const { status, loading: statusLoading, error: statusError } = useStatus()
  const {
    isTurnstileEnabled,
    turnstileSiteKey,
    turnstileToken,
    setTurnstileToken,
    validateTurnstile,
  } = useTurnstile()
  const { redirectToLogin } = useAuthRedirect()
  const {
    isSending: isSendingCode,
    secondsLeft,
    isActive,
    sendCode,
  } = useEmailVerification({
    turnstileToken,
    validateTurnstile,
    onTurnstileConsumed: () => {
      if (isTurnstileEnabled) {
        setTurnstileToken('')
        setTurnstileWidgetKey((current) => current + 1)
      }
    },
  })

  const form = useForm<z.infer<typeof registerFormSchema>>({
    resolver: zodResolver(registerFormSchema),
    defaultValues: {
      username: '',
      email: '',
      password: '',
      confirmPassword: '',
    },
  })
  const passwordValue = useWatch({ control: form.control, name: 'password' })
  const confirmationValue = useWatch({
    control: form.control,
    name: 'confirmPassword',
  })

  const emailValue = form.watch('email')
  const statusReady = Boolean(status) && !statusLoading && !statusError
  const emailVerificationRequired = !!status?.email_verification
  const hasUserAgreement = Boolean(status?.user_agreement_enabled)
  const hasPrivacyPolicy = Boolean(status?.privacy_policy_enabled)
  const requiresLegalConsent = hasUserAgreement || hasPrivacyPolicy
  const isSubsite = Boolean(
    status?.adty_site || status?.data?.adty_site || subsiteDocument()
  )
  const referralEnabled = readReferralEnabledFromStatus(status) === true
  const showSiteInviteCode = isSubsite && referralEnabled
  const turnstileReady = !isTurnstileEnabled || Boolean(turnstileToken)

  useEffect(() => {
    if (!statusReady || requiresLegalConsent) {
      setAgreedToLegal(false)
    } else {
      setAgreedToLegal(true)
    }
  }, [statusReady, requiresLegalConsent])

  async function onSubmit(data: z.infer<typeof registerFormSchema>) {
    if (!statusReady) {
      toast.error(
        statusError
          ? t('Failed to load site configuration. Please retry.')
          : t('Loading site configuration...')
      )
      return
    }

    if (requiresLegalConsent && !agreedToLegal) {
      toast.error(legalConsentErrorMessage)
      return
    }

    // Validate email verification if required
    if (emailVerificationRequired) {
      if (!data.email) {
        toast.error(t('Please enter your email'))
        return
      }
      if (!verificationCode) {
        toast.error(t('Please enter the verification code'))
        return
      }
    }

    if (!validateTurnstile()) return

    setIsLoading(true)
    try {
      const res = await register({
        username: data.username,
        password: data.password,
        email: data.email || undefined,
        verification_code: verificationCode || undefined,
        turnstile: turnstileToken,
        agreement_accepted: requiresLegalConsent ? true : undefined,
        lang: i18n.resolvedLanguage || i18n.language,
        site_invite_code:
          showSiteInviteCode && siteInviteEdited
            ? siteInviteCode.trim()
            : undefined,
      })

      if (res?.success) {
        if (typeof document !== 'undefined') {
          clearSiteInviteCookie(document)
        }
        toast.success(t('Account created! Please sign in'))
        redirectToLogin()
      } else {
        handleServerError(createServerError(res, t('Failed to create account')))
      }
    } catch (error) {
      handleServerError(
        AuthOperationError.from(error, t('Failed to create account'))
      )
    } finally {
      setIsLoading(false)
      if (isTurnstileEnabled) {
        setTurnstileToken('')
        setTurnstileWidgetKey((current) => current + 1)
      }
    }
  }

  async function handleSendVerificationCode() {
    await sendCode(emailValue || '')
  }

  let verificationCodeAction: ReactNode = t('Send code')
  if (isActive) {
    verificationCodeAction = t('Resend ({{seconds}}s)', {
      seconds: secondsLeft,
    })
  } else if (isSendingCode) {
    verificationCodeAction = <Loader2 className='h-4 w-4 animate-spin' />
  }

  const registerDisabled = status?.register_enabled === false
  const passwordRegisterDisabled = status?.password_register_enabled === false
  if (registerDisabled || passwordRegisterDisabled) {
    const disabledMessage = registerDisabled
      ? t('New user registration has been disabled by administrator')
      : t('Password registration has been disabled by administrator')
    return (
      <div className='border-border/60 bg-muted/40 text-muted-foreground rounded-md border p-4 text-sm'>
        {disabledMessage}
      </div>
    )
  }

  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit(onSubmit)}
        className={cn('grid gap-4 [&_input]:h-10', className)}
        {...props}
      >
        {/* Username Field */}
        <FormField
          control={form.control}
          name='username'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Username')}</FormLabel>
              <FormControl>
                <Input placeholder={t('Enter your username')} {...field} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        {/* Password Field */}
        <FormField
          control={form.control}
          name='password'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Password')}</FormLabel>
              <FormControl describedBy='sign-up-password-strength'>
                <PasswordInput
                  placeholder={t('Enter password (8–128 characters)')}
                  autoComplete='new-password'
                  {...field}
                />
              </FormControl>
              <PasswordStrength
                id='sign-up-password-strength'
                value={passwordValue}
              />
              <FormMessage />
            </FormItem>
          )}
        />

        {/* Confirm Password Field */}
        <FormField
          control={form.control}
          name='confirmPassword'
          render={({ field, fieldState }) => (
            <FormItem>
              <FormLabel>{t('Confirm password')}</FormLabel>
              <FormControl describedBy='sign-up-password-confirmation-status'>
                <PasswordInput
                  placeholder={t('Confirm password')}
                  autoComplete='new-password'
                  {...field}
                />
              </FormControl>
              <PasswordConfirmationStatus
                id='sign-up-password-confirmation-status'
                password={passwordValue}
                confirmation={confirmationValue}
                hasFieldError={!!fieldState.error}
              />
              <FormMessage />
            </FormItem>
          )}
        />

        {/* Email Verification Section */}
        {emailVerificationRequired && (
          <>
            {/* Email Field */}
            <FormField
              control={form.control}
              name='email'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Email (required for verification)')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('name@example.com')}
                      type='email'
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {/* Verification Code Field */}
            <div className='flex items-end gap-2'>
              <div className='flex-1'>
                <Input
                  placeholder={t('Verification code')}
                  value={verificationCode}
                  onChange={(e) => setVerificationCode(e.target.value)}
                />
              </div>
              <Button
                variant='outline'
                type='button'
                className='h-10 shrink-0'
                disabled={
                  isLoading ||
                  isSendingCode ||
                  isActive ||
                  !emailValue ||
                  !turnstileReady
                }
                onClick={handleSendVerificationCode}
              >
                {verificationCodeAction}
              </Button>
            </div>
          </>
        )}

        {showSiteInviteCode && (
          <div className='space-y-1.5'>
            <Label htmlFor='sign-up-site-invite-code'>
              {t('Invite code (optional)')}
            </Label>
            <Input
              id='sign-up-site-invite-code'
              placeholder={t('Enter invite code')}
              value={siteInviteCode}
              onChange={(event) => {
                setSiteInviteEdited(true)
                setSiteInviteCode(event.target.value)
              }}
            />
          </div>
        )}

        {/* Turnstile */}
        {isTurnstileEnabled && (
          <div className='mt-2'>
            <Turnstile
              key={turnstileWidgetKey}
              siteKey={turnstileSiteKey}
              onVerify={setTurnstileToken}
              onExpire={() => setTurnstileToken('')}
            />
          </div>
        )}

        {!statusReady && (
          <div
            role={statusError ? 'alert' : 'status'}
            className='border-border/60 bg-muted/40 text-muted-foreground flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-xs'
          >
            <span className='flex items-center gap-2'>
              {!statusError && (
                <Loader2 className='h-3.5 w-3.5 animate-spin' aria-hidden />
              )}
              {statusError
                ? t('Failed to load site configuration. Please retry.')
                : t('Loading site configuration...')}
            </span>
            {statusError && (
              <Button
                type='button'
                size='sm'
                variant='outline'
                onClick={() =>
                  void queryClient.refetchQueries({
                    queryKey: STATUS_QUERY_KEY,
                  })
                }
              >
                {t('Retry')}
              </Button>
            )}
          </div>
        )}

        <LegalConsent
          status={status}
          checked={agreedToLegal}
          onCheckedChange={setAgreedToLegal}
          className='mt-1'
        />

        {/* Submit Button */}
        <Button
          type='submit'
          variant='brand'
          className='mt-1 h-10 w-full justify-center gap-2 text-[0.9375rem]'
          disabled={
            isLoading ||
            !statusReady ||
            (requiresLegalConsent && !agreedToLegal) ||
            !turnstileReady
          }
        >
          {isLoading ? <Loader2 className='h-4 w-4 animate-spin' /> : null}
          {t('Create account')}
        </Button>
      </form>
    </Form>
  )
}
