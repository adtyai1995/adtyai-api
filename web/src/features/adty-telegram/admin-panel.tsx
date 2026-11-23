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
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { SecureVerificationDialog } from '@/features/auth/secure-verification/components/secure-verification-dialog'
import { useSecureVerification } from '@/features/auth/secure-verification/hooks/use-secure-verification'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { api } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { TelegramBindCard } from './bind-card'

type BotPurpose = 'user' | 'admin'

type BotView = {
  purpose: BotPurpose
  bot_id: number
  username: string
  token_mask: string
  enabled: boolean
  identity_verified: boolean
  saved_at: number
  tested_at: number
  pending: number
  webhook_configured: boolean
}

function getBotStatusMeta(bot: BotView): {
  labelKey: string
  variant: 'default' | 'secondary' | 'outline'
} {
  if (!bot.token_mask) {
    return { labelKey: 'Not configured', variant: 'secondary' }
  }
  if (bot.enabled) {
    return { labelKey: 'Enabled', variant: 'default' }
  }
  return { labelKey: 'Disabled', variant: 'outline' }
}

function formatBotIdentity(bot: BotView): string {
  if (bot.username) return `@${bot.username}`
  if (bot.bot_id > 0) return `#${bot.bot_id}`
  return '—'
}

export function TelegramAdminPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const verification = useSecureVerification()
  const bots = useQuery({
    queryKey: ['adty-telegram-admin'],
    queryFn: async () => {
      const response = await api.get('/api/adty/admin/telegram/bots')
      const body = response.data as {
        success?: boolean
        data?: { bots?: BotView[] }
        message?: string
      }
      if (!body.success) {
        throw new Error(body.message || t('Failed to load data'))
      }
      return body.data?.bots ?? []
    },
  })
  const [tokens, setTokens] = useState<Partial<Record<BotPurpose, string>>>({})
  const [enabledDrafts, setEnabledDrafts] = useState<
    Partial<Record<BotPurpose, boolean>>
  >({})
  const [secrets, setSecrets] = useState<Partial<Record<BotPurpose, string>>>(
    {}
  )
  const [errors, setErrors] = useState<Partial<Record<BotPurpose, string>>>({})
  const [busyPurpose, setBusyPurpose] = useState<BotPurpose | null>(null)

  const origin =
    typeof window !== 'undefined' && window.location?.origin
      ? window.location.origin
      : ''

  const resolveEnabled = (bot: BotView) => {
    const draft = enabledDrafts[bot.purpose]
    if (draft !== undefined) return draft
    return bot.token_mask ? bot.enabled : true
  }

  const save = async (bot: BotView) => {
    const purpose = bot.purpose
    const token = (tokens[purpose] ?? '').trim()
    const enabled = resolveEnabled(bot)
    const proof = await verification.requestVerification({
      scope: 'adty.notice.configure',
      context: { action: 'telegram.save', target: purpose },
      title: t('Confirm it is you'),
      description: t('Changing a bot token requires a fresh verification.'),
    })
    if (!proof?.proof_token) return
    setBusyPurpose(purpose)
    setErrors((prev) => ({ ...prev, [purpose]: '' }))
    try {
      const response = await api.put(
        `/api/adty/admin/telegram/bots/${purpose}`,
        { token, enabled },
        { headers: { 'X-Security-Proof': proof.proof_token } }
      )
      const body = response.data as {
        success?: boolean
        message?: string
        data?: { webhook_secret?: string }
      }
      if (!body.success) throw new Error(body.message || t('Failed to save'))
      if (body.data?.webhook_secret) {
        setSecrets((prev) => ({
          ...prev,
          [purpose]: body.data?.webhook_secret || '',
        }))
      }
      setTokens((prev) => ({ ...prev, [purpose]: '' }))
      setEnabledDrafts((prev) => {
        const next = { ...prev }
        delete next[purpose]
        return next
      })
      toast.success(t('Saved'))
      await queryClient.invalidateQueries({ queryKey: ['adty-telegram-admin'] })
    } catch (cause) {
      setErrors((prev) => ({
        ...prev,
        [purpose]: getServerErrorMessage(cause, t('Failed to save')),
      }))
    } finally {
      setBusyPurpose(null)
    }
  }

  // A test checks the token with Telegram and sends a test notice to the bot
  // this Root account has bound; without a binding only the token is checked.
  const test = async (purpose: BotPurpose) => {
    const proof = await verification.requestVerification({
      scope: 'adty.notice.configure',
      context: { action: 'telegram.test', target: purpose },
      title: t('Confirm it is you'),
      description: t('Testing a bot requires a fresh verification.'),
    })
    if (!proof?.proof_token) return
    setBusyPurpose(purpose)
    setErrors((prev) => ({ ...prev, [purpose]: '' }))
    try {
      const response = await api.post(
        `/api/adty/admin/telegram/bots/${purpose}/test`,
        undefined,
        { headers: { 'X-Security-Proof': proof.proof_token } }
      )
      const body = response.data as { success?: boolean; message?: string }
      if (!body.success) throw new Error(body.message || t('Bot test failed'))
      toast.success(t('Bot test passed'))
      await queryClient.invalidateQueries({ queryKey: ['adty-telegram-admin'] })
    } catch (cause) {
      setErrors((prev) => ({
        ...prev,
        [purpose]: getServerErrorMessage(cause, t('Bot test failed')),
      }))
    } finally {
      setBusyPurpose(null)
    }
  }

  const formatTime = (value: number) =>
    value ? formatTimestamp(value) : t('Never')

  return (
    <div className='flex flex-col gap-4'>
      <SettingsSection
        title={t('Telegram bots')}
        description={t(
          'Configure the user notification bot and admin notification bot independently. Tokens are stored encrypted and shown masked.'
        )}
      >
        <div className='flex flex-col gap-4'>
          <p className='text-muted-foreground text-xs'>
            {t(
              'A test checks the token and sends a test notice to the bot you have bound yourself.'
            )}
          </p>
          {bots.isLoading && (
            <div className='grid gap-4 lg:grid-cols-2'>
              <Skeleton className='h-72 w-full' />
              <Skeleton className='h-72 w-full' />
            </div>
          )}
          {bots.isError && (
            <ErrorState onRetry={() => void bots.refetch()} />
          )}
          {bots.data && (
            <div className='grid gap-4 lg:grid-cols-2'>
              {bots.data.map((bot) => {
                const statusMeta = getBotStatusMeta(bot)
                const tokenValue = tokens[bot.purpose] ?? ''
                const enabledValue = resolveEnabled(bot)
                const isBusy = busyPurpose !== null
                const isConfigured = Boolean(bot.token_mask)
                const canSave =
                  !isBusy &&
                  (tokenValue.trim() !== '' ||
                    (isConfigured && enabledValue !== bot.enabled))
                const freshSecret = secrets[bot.purpose] || ''
                const webhookPath = `/api/adty/telegram/webhook/${bot.purpose}/${freshSecret || '<webhook_secret>'}`
                const webhookUrl = origin
                  ? `${origin}${webhookPath}`
                  : webhookPath
                const botError = errors[bot.purpose] || ''
                const botTitle =
                  bot.purpose === 'user' ? t('User bot') : t('Admin bot')
                const botDesc =
                  bot.purpose === 'user'
                    ? t('Sends notices to customers and site users.')
                    : t(
                        'Sends ticket, order, and risk alerts to administrators and site owners.'
                      )

                return (
                  <Card key={bot.purpose} data-card-hover='false'>
                    <CardHeader className='flex flex-row items-start justify-between gap-3 pb-3'>
                      <div className='flex min-w-0 flex-col gap-1'>
                        <CardTitle className='text-sm font-semibold'>
                          {botTitle}
                        </CardTitle>
                        <p className='text-muted-foreground text-xs'>
                          {botDesc}
                        </p>
                      </div>
                      <Badge variant={statusMeta.variant}>
                        {t(statusMeta.labelKey)}
                      </Badge>
                    </CardHeader>
                    <CardContent className='flex flex-col gap-4'>
                      <dl className='bg-muted/40 grid grid-cols-2 gap-x-4 gap-y-2 rounded-lg border p-3 text-xs'>
                        <div>
                          <dt className='text-muted-foreground'>
                            {t('Bot account')}
                          </dt>
                          <dd className='mt-0.5 font-mono font-medium'>
                            {formatBotIdentity(bot)}
                          </dd>
                        </div>
                        <div>
                          <dt className='text-muted-foreground'>
                            {t('Token mask')}
                          </dt>
                          <dd className='mt-0.5 font-mono'>
                            {bot.token_mask || t('Not configured')}
                          </dd>
                        </div>
                        <div>
                          <dt className='text-muted-foreground'>
                            {t('Identity status')}
                          </dt>
                          <dd className='mt-0.5'>
                            {bot.identity_verified
                              ? t('Identity verified')
                              : t('Identity not verified')}
                          </dd>
                        </div>
                        <div>
                          <dt className='text-muted-foreground'>
                            {t('Pending retries')}
                          </dt>
                          <dd className='mt-0.5 tabular-nums'>{bot.pending}</dd>
                        </div>
                        <div>
                          <dt className='text-muted-foreground'>
                            {t('Saved')}
                          </dt>
                          <dd className='mt-0.5 tabular-nums'>
                            {formatTime(bot.saved_at)}
                          </dd>
                        </div>
                        <div>
                          <dt className='text-muted-foreground'>
                            {t('Tested')}
                          </dt>
                          <dd className='mt-0.5 tabular-nums'>
                            {formatTime(bot.tested_at)}
                          </dd>
                        </div>
                      </dl>

                      <div className='flex items-center justify-between gap-3 rounded-lg border px-3 py-2.5'>
                        <div className='flex flex-col gap-0.5'>
                          <Label htmlFor={`tg-enabled-${bot.purpose}`}>
                            {t('Enable bot')}
                          </Label>
                          <p className='text-muted-foreground text-xs'>
                            {t('Pause or resume outbound notifications.')}
                          </p>
                        </div>
                        <Switch
                          id={`tg-enabled-${bot.purpose}`}
                          checked={enabledValue}
                          disabled={isBusy}
                          onCheckedChange={(checked) =>
                            setEnabledDrafts((prev) => ({
                              ...prev,
                              [bot.purpose]: checked,
                            }))
                          }
                        />
                      </div>

                      <div className='flex flex-col gap-1.5'>
                        <Label htmlFor={`tg-token-${bot.purpose}`}>
                          {t('Bot token')}
                        </Label>
                        <Input
                          id={`tg-token-${bot.purpose}`}
                          type='password'
                          autoComplete='off'
                          placeholder={
                            isConfigured
                              ? t('Leave blank to keep current token')
                              : '123456789:AA...'
                          }
                          value={tokenValue}
                          onChange={(event) =>
                            setTokens((prev) => ({
                              ...prev,
                              [bot.purpose]: event.target.value,
                            }))
                          }
                        />
                      </div>

                      {(freshSecret || bot.webhook_configured) && (
                        <div className='bg-muted/30 flex flex-col gap-2 rounded-lg border p-3 text-xs'>
                          <div className='flex items-center justify-between gap-2'>
                            <span className='font-medium'>
                              {t('Webhook URL')}
                            </span>
                            <CopyButton
                              value={webhookUrl}
                              size='icon'
                              variant='ghost'
                              className='size-6'
                              iconClassName='size-3.5'
                            />
                          </div>
                          <code className='font-mono text-[11px] break-all'>
                            {webhookUrl}
                          </code>
                          {freshSecret ? (
                            <div className='flex flex-col gap-1 border-t pt-2'>
                              <div className='flex items-center justify-between gap-2'>
                                <span className='font-medium'>
                                  {t('Webhook secret')}
                                </span>
                                <CopyButton
                                  value={freshSecret}
                                  size='icon'
                                  variant='ghost'
                                  className='size-6'
                                  iconClassName='size-3.5'
                                />
                              </div>
                              <code className='font-mono text-[11px] break-all'>
                                {freshSecret}
                              </code>
                              <p className='text-muted-foreground'>
                                {t(
                                  'Shown once. Register this URL with Telegram setWebhook now.'
                                )}
                              </p>
                            </div>
                          ) : (
                            <p className='text-muted-foreground'>
                              {t(
                                'Secret is hidden after saving. Save a new token to rotate the webhook secret.'
                              )}
                            </p>
                          )}
                        </div>
                      )}

                      <div className='flex flex-wrap gap-2'>
                        <Button
                          type='button'
                          disabled={!canSave}
                          onClick={() => void save(bot)}
                        >
                          {t('Save')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          disabled={isBusy || !isConfigured}
                          onClick={() => void test(bot.purpose)}
                        >
                          {t('Send test')}
                        </Button>
                      </div>

                      {botError && (
                        <p role='alert' className='text-destructive text-xs'>
                          {botError}
                        </p>
                      )}
                    </CardContent>
                  </Card>
                )
              })}
            </div>
          )}
        </div>
      </SettingsSection>

      <TelegramBindCard purpose='admin' platformSupport />

      <SecureVerificationDialog {...verification.dialogProps} />
    </div>
  )
}
