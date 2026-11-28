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
import {
  CheckCircle2,
  Clock,
  ExternalLink,
  Gift,
  Globe,
  Info,
  Loader2,
  Send,
} from 'lucide-react'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout'
import { Turnstile } from '@/components/turnstile'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { NotFoundError } from '@/features/errors/not-found-error'
import { useStatus } from '@/hooks/use-status'
import { handleServerError } from '@/lib/handle-server-error'
import { subsiteDocument } from '@/lib/subsite-document'

import {
  getPartnerPublicConfig,
  partnerQueryKeys,
  recordPartnerClick,
  submitPartnerLead,
  type PartnerContactChannel,
  type PartnerLeadResult,
} from './api'

interface TelegramContact {
  href: string
  display: string
  copyValue: string
}

function readInitialPartnerRef(): string {
  if (typeof window === 'undefined') return ''
  return new URLSearchParams(window.location.search).get('ref')?.trim() ?? ''
}

function formatRatePercent(rate: number): string {
  if (!Number.isFinite(rate) || rate < 0) return '0'
  return String(Number(rate.toFixed(2)))
}

function resolveTelegramContact(raw: string): TelegramContact | null {
  const trimmed = raw.trim()
  if (!trimmed) return null

  if (/^https?:\/\//i.test(trimmed)) {
    try {
      const parsed = new URL(trimmed)
      if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') {
        return null
      }
      const host = parsed.hostname.toLowerCase()
      const pathSegments = parsed.pathname.split('/').filter(Boolean)
      const isTelegramHost =
        host === 't.me' || host === 'telegram.me' || host === 'telegram.dog'
      const singleHandle =
        isTelegramHost &&
        pathSegments.length === 1 &&
        !pathSegments[0].startsWith('+')
          ? `@${pathSegments[0].replace(/^@+/, '')}`
          : ''
      const href = parsed.toString()
      return {
        href,
        display: singleHandle || href,
        copyValue: singleHandle || href,
      }
    } catch {
      return null
    }
  }

  if (/^(?:t\.me|telegram\.me|telegram\.dog)\//i.test(trimmed)) {
    const href = `https://${trimmed}`
    return resolveTelegramContact(href)
  }

  const handle = trimmed.replace(/^@+/, '')
  if (!handle) return null
  return {
    href: `https://t.me/${encodeURIComponent(handle)}`,
    display: `@${handle}`,
    copyValue: `@${handle}`,
  }
}

/** Public reseller subsite partnership landing and inquiry page (main site only). */
export function PartnerProgram() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { status } = useStatus()
  const isSubsite = Boolean(subsiteDocument() || status?.adty_site)

  const [initialRef] = useState(readInitialPartnerRef)
  const [contactName, setContactName] = useState('')
  const [contactChannel, setContactChannel] =
    useState<PartnerContactChannel>('telegram')
  const [contactValue, setContactValue] = useState('')
  const [estimatedVolume, setEstimatedVolume] = useState('')
  const [note, setNote] = useState('')
  const [referralCode, setReferralCode] = useState(initialRef)
  const [clickCookieRecorded, setClickCookieRecorded] = useState(false)
  const [turnstileToken, setTurnstileToken] = useState('')
  const [turnstileWidgetKey, setTurnstileWidgetKey] = useState(0)
  const [submittedLead, setSubmittedLead] = useState<PartnerLeadResult | null>(
    null
  )
  const clickRecordedRef = useRef(false)

  const publicConfig = useQuery({
    queryKey: partnerQueryKeys.publicConfig,
    queryFn: getPartnerPublicConfig,
    enabled: !isSubsite,
    staleTime: 5 * 60 * 1000,
    meta: { errorToast: false },
  })

  useEffect(() => {
    if (isSubsite || !initialRef || clickRecordedRef.current) return
    clickRecordedRef.current = true
    void recordPartnerClick(initialRef)
      .then(() => {
        setClickCookieRecorded(true)
      })
      .catch(() => {
        // Ignore silent attribution click errors; manual code entry remains available.
      })
  }, [initialRef, isSubsite])

  const data = publicConfig.data
  const turnstileRequired = Boolean(
    data?.turnstile_check && data?.turnstile_site_key
  )
  const turnstileReady = !turnstileRequired || Boolean(turnstileToken)

  const submitMutation = useMutation({
    mutationFn: () =>
      submitPartnerLead(
        {
          contact_name: contactName.trim(),
          contact_channel: contactChannel,
          contact_value: contactValue.trim(),
          estimated_volume: estimatedVolume.trim(),
          note: note.trim(),
          referral_code: referralCode.trim(),
        },
        turnstileRequired ? turnstileToken : undefined
      ),
    onSuccess: (result) => {
      setSubmittedLead(result)
      setClickCookieRecorded(false)
      if (turnstileRequired) {
        setTurnstileToken('')
        setTurnstileWidgetKey((current) => current + 1)
      }
      toast.success(t('Partner inquiry submitted'))
      void queryClient.invalidateQueries({
        queryKey: partnerQueryKeys.publicConfig,
      })
    },
    onError: (error) => {
      handleServerError(error, t('Failed to submit partner inquiry'))
      if (turnstileRequired) {
        setTurnstileToken('')
        setTurnstileWidgetKey((current) => current + 1)
      }
    },
  })

  if (isSubsite) {
    return <NotFoundError />
  }

  const ratePercent =
    data !== undefined ? formatRatePercent(data.default_rate_percent) : null
  const hasRefCookie = Boolean(data?.has_ref_cookie || clickCookieRecorded)
  const telegramContact = submittedLead
    ? resolveTelegramContact(submittedLead.contact_telegram)
    : null

  const channelOptions: { value: PartnerContactChannel; label: string }[] = [
    { value: 'telegram', label: t('Telegram') },
    { value: 'wechat', label: t('WeChat') },
    { value: 'email', label: t('Email') },
    { value: 'other', label: t('Other') },
  ]

  const highlights = [
    {
      icon: Globe,
      title: t('White-label Brand & Custom Domain'),
      body: t(
        'Operate your own independent API storefront with custom domain binding, brand logo, legal pages, and flexible 1.0x–10.0x retail markup pricing.'
      ),
    },
    {
      icon: Clock,
      title: t('Hourly Automated Settlement'),
      body: t(
        'Supply costs and retail margins settle automatically every hour based on actual paid usage, keeping your operating balance transparent and predictable.'
      ),
    },
    {
      icon: Gift,
      title: t('Permanent Partner Referral Rewards'),
      body:
        ratePercent !== null
          ? t(
              'Refer other reseller subsite partners and earn a permanent {{rate}}% commission on their supply cost, settled automatically every hour.',
              { rate: ratePercent }
            )
          : t(
              'Refer other reseller subsite partners and earn a permanent commission on their supply cost, settled automatically every hour.'
            ),
    },
  ]

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!contactName.trim()) {
      toast.error(t('Please enter your contact name'))
      return
    }
    if (!contactValue.trim()) {
      toast.error(t('Please enter your contact handle or address'))
      return
    }
    if (turnstileRequired && !turnstileToken) {
      toast.error(t('Please complete the verification challenge'))
      return
    }
    submitMutation.mutate()
  }

  function handleSubmitAnother() {
    setSubmittedLead(null)
    setContactName('')
    setContactChannel('telegram')
    setContactValue('')
    setEstimatedVolume('')
    setNote('')
    setReferralCode('')
    setClickCookieRecorded(false)
    if (turnstileRequired) {
      setTurnstileToken('')
      setTurnstileWidgetKey((current) => current + 1)
    }
  }

  let contactPlaceholder: string
  switch (contactChannel) {
    case 'telegram':
      contactPlaceholder = t('e.g. @yourhandle')
      break
    case 'wechat':
      contactPlaceholder = t('e.g. your_wechat_id')
      break
    case 'email':
      contactPlaceholder = t('e.g. partner@company.com')
      break
    case 'other':
      contactPlaceholder = t('Phone, Discord, WhatsApp, etc.')
      break
  }

  return (
    <PublicLayout>
      <div className='mx-auto max-w-5xl space-y-12 py-6 sm:py-10'>
        <section className='space-y-5 text-center'>
          <Badge variant='secondary' className='px-3 py-1 text-xs'>
            {t('Reseller Partner Program')}
          </Badge>
          <h1 className='text-3xl font-semibold tracking-tight text-balance sm:text-4xl lg:text-5xl'>
            {t('Launch Your Own AI API Platform')}
          </h1>
          {publicConfig.isPending && (
            <Skeleton className='mx-auto h-6 w-96 max-w-full' aria-hidden />
          )}
          {!publicConfig.isPending && ratePercent !== null && (
            <p className='text-muted-foreground mx-auto max-w-2xl text-base leading-relaxed sm:text-lg'>
              {t(
                "Run a white-label reseller subsite under your own brand and domain with flexible 1.0x–10.0x markup pricing, automated hourly settlement, and {{rate}}% permanent partner referral commission on referred subsites' supply cost.",
                { rate: ratePercent }
              )}
            </p>
          )}
          {!publicConfig.isPending && ratePercent === null && (
            <p className='text-muted-foreground mx-auto max-w-2xl text-base leading-relaxed sm:text-lg'>
              {t(
                "Run a white-label reseller subsite under your own brand and domain with flexible 1.0x–10.0x markup pricing, automated hourly settlement, and permanent partner referral commission on referred subsites' supply cost."
              )}
            </p>
          )}
        </section>

        <section className='grid gap-4 md:grid-cols-3'>
          {highlights.map((item) => {
            const Icon = item.icon
            return (
              <Card key={item.title} className='h-full'>
                <CardHeader className='space-y-3'>
                  <div className='bg-primary/10 text-primary flex size-10 items-center justify-center rounded-lg'>
                    <Icon className='size-5' aria-hidden='true' />
                  </div>
                  <CardTitle className='text-base'>{item.title}</CardTitle>
                </CardHeader>
                <CardContent className='text-muted-foreground text-sm leading-relaxed'>
                  {item.body}
                </CardContent>
              </Card>
            )
          })}
        </section>

        <section className='mx-auto max-w-2xl'>
          {publicConfig.isPending && (
            <Card>
              <CardHeader className='space-y-2'>
                <Skeleton className='h-6 w-52' aria-hidden />
                <Skeleton className='h-4 w-80 max-w-full' aria-hidden />
              </CardHeader>
              <CardContent className='space-y-4'>
                <Skeleton className='h-10 w-full' aria-hidden />
                <Skeleton className='h-10 w-full' aria-hidden />
                <Skeleton className='h-24 w-full' aria-hidden />
                <Skeleton className='h-10 w-full' aria-hidden />
              </CardContent>
            </Card>
          )}

          {publicConfig.isError && (
            <ErrorState onRetry={() => void publicConfig.refetch()} />
          )}

          {!publicConfig.isPending &&
            !publicConfig.isError &&
            submittedLead && (
              <Card>
                <CardHeader className='space-y-3'>
                  <div className='flex items-center gap-3'>
                    <div className='bg-success/10 text-success flex size-10 shrink-0 items-center justify-center rounded-full'>
                      <CheckCircle2 className='size-5' aria-hidden='true' />
                    </div>
                    <div className='space-y-1'>
                      <CardTitle className='text-xl'>
                        {t('Partnership Inquiry Submitted')}
                      </CardTitle>
                      <CardDescription>
                        {t(
                          'Your inquiry reference is #{{id}}. Our partnership team has been notified and will review your application shortly.',
                          { id: submittedLead.id }
                        )}
                      </CardDescription>
                    </div>
                  </div>
                </CardHeader>
                <CardContent className='space-y-6'>
                  {telegramContact && (
                    <div className='border-primary/25 bg-primary/5 space-y-4 rounded-xl border p-5'>
                      <div className='flex flex-wrap items-center justify-between gap-2'>
                        <div className='flex items-center gap-2'>
                          <Send
                            className='text-primary size-4 shrink-0'
                            aria-hidden='true'
                          />
                          <h2 className='text-base font-semibold'>
                            {t('Official Partnership Telegram')}
                          </h2>
                        </div>
                        <Badge variant='secondary'>
                          {t('Faster Onboarding')}
                        </Badge>
                      </div>
                      <p className='text-muted-foreground text-sm leading-relaxed'>
                        {t(
                          'Reach out directly to our partnership team on Telegram and share inquiry #{{id}} for faster onboarding.',
                          { id: submittedLead.id }
                        )}
                      </p>
                      <div className='bg-background border-border flex flex-wrap items-center justify-between gap-3 rounded-lg border px-3.5 py-2.5'>
                        <div className='min-w-0 space-y-0.5'>
                          <a
                            href={telegramContact.href}
                            target='_blank'
                            rel='noopener noreferrer'
                            className='text-primary block truncate text-sm font-medium hover:underline'
                          >
                            {telegramContact.display}
                          </a>
                          {telegramContact.display !== telegramContact.href && (
                            <p className='text-muted-foreground truncate text-xs'>
                              {telegramContact.href}
                            </p>
                          )}
                        </div>
                        <CopyButton
                          value={telegramContact.copyValue}
                          variant='outline'
                          size='sm'
                          aria-label={t('Copy Telegram contact')}
                        >
                          {t('Copy')}
                        </CopyButton>
                      </div>
                      <div className='flex flex-wrap items-center gap-3'>
                        <Button
                          variant='brand'
                          size='lg'
                          className='w-full sm:w-auto'
                          render={
                            <a
                              href={telegramContact.href}
                              target='_blank'
                              rel='noopener noreferrer'
                            />
                          }
                        >
                          <Send className='size-4' aria-hidden='true' />
                          {t('Contact on Telegram')}
                          <ExternalLink
                            className='size-3.5'
                            aria-hidden='true'
                          />
                        </Button>
                      </div>
                    </div>
                  )}

                  <div className='flex flex-wrap items-center justify-between gap-3 border-t pt-4'>
                    <p className='text-muted-foreground text-xs'>
                      {t('Inquiry Reference: #{{id}}', {
                        id: submittedLead.id,
                      })}
                    </p>
                    <Button
                      type='button'
                      variant='outline'
                      onClick={handleSubmitAnother}
                    >
                      {t('Submit Another Inquiry')}
                    </Button>
                  </div>
                </CardContent>
              </Card>
            )}

          {!publicConfig.isPending &&
            !publicConfig.isError &&
            !submittedLead && (
              <Card>
                <CardHeader className='space-y-1.5'>
                  <CardTitle className='text-xl'>
                    {t('Apply to Become a Partner')}
                  </CardTitle>
                  <CardDescription>
                    {t(
                      'Share your contact details and estimated monthly volume. Our partnership team will follow up to configure your reseller subsite.'
                    )}
                  </CardDescription>
                </CardHeader>
                <CardContent className='space-y-5'>
                  {data && !data.enabled && (
                    <Alert>
                      <Info className='size-4' aria-hidden='true' />
                      <AlertTitle>{t('Onboarding by Review')}</AlertTitle>
                      <AlertDescription>
                        {t(
                          'Automated reseller onboarding is not currently open, but you can still submit your partnership inquiry below and our team will reach out.'
                        )}
                      </AlertDescription>
                    </Alert>
                  )}

                  <form onSubmit={handleSubmit} className='space-y-5'>
                    <div className='space-y-2'>
                      <Label htmlFor='partner-contact-name' required>
                        {t('Contact Name')}
                      </Label>
                      <Input
                        id='partner-contact-name'
                        required
                        maxLength={100}
                        placeholder={t('Your name or team name')}
                        value={contactName}
                        onChange={(event) => setContactName(event.target.value)}
                        className='h-10'
                      />
                    </div>

                    <div className='grid gap-4 sm:grid-cols-[180px_minmax(0,1fr)]'>
                      <div className='space-y-2'>
                        <Label htmlFor='partner-contact-channel' required>
                          {t('Contact Channel')}
                        </Label>
                        <Select
                          items={channelOptions}
                          value={contactChannel}
                          onValueChange={(next) => {
                            if (
                              next === 'telegram' ||
                              next === 'wechat' ||
                              next === 'email' ||
                              next === 'other'
                            ) {
                              setContactChannel(next)
                            }
                          }}
                        >
                          <SelectTrigger
                            id='partner-contact-channel'
                            className='h-10 w-full'
                          >
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent alignItemWithTrigger={false}>
                            {channelOptions.map((item) => (
                              <SelectItem key={item.value} value={item.value}>
                                {item.label}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </div>

                      <div className='space-y-2'>
                        <Label htmlFor='partner-contact-value' required>
                          {t('Contact Handle / Address')}
                        </Label>
                        <Input
                          id='partner-contact-value'
                          required
                          maxLength={200}
                          placeholder={contactPlaceholder}
                          value={contactValue}
                          onChange={(event) =>
                            setContactValue(event.target.value)
                          }
                          className='h-10'
                        />
                      </div>
                    </div>

                    <div className='space-y-2'>
                      <Label htmlFor='partner-estimated-volume'>
                        {t('Estimated Monthly Volume')}
                      </Label>
                      <Input
                        id='partner-estimated-volume'
                        maxLength={128}
                        placeholder={t(
                          'e.g. $5,000–$20,000 / month (optional)'
                        )}
                        value={estimatedVolume}
                        onChange={(event) =>
                          setEstimatedVolume(event.target.value)
                        }
                        className='h-10'
                      />
                    </div>

                    <div className='space-y-2'>
                      <div className='flex items-center justify-between gap-2'>
                        <Label htmlFor='partner-note'>
                          {t('Additional Notes')}
                        </Label>
                        {note.length > 0 && (
                          <span className='text-muted-foreground text-xs tabular-nums'>
                            {note.length} / 1000
                          </span>
                        )}
                      </div>
                      <Textarea
                        id='partner-note'
                        rows={4}
                        maxLength={1000}
                        placeholder={t(
                          'Tell us about your users, target models, or launch plan (optional)'
                        )}
                        value={note}
                        onChange={(event) => setNote(event.target.value)}
                      />
                    </div>

                    <div className='space-y-2'>
                      <Label htmlFor='partner-referral-code'>
                        {t('Referral Code')}
                      </Label>
                      <Input
                        id='partner-referral-code'
                        maxLength={24}
                        placeholder={t('Optional partner referral code')}
                        value={referralCode}
                        onChange={(event) =>
                          setReferralCode(event.target.value)
                        }
                        className='h-10'
                      />
                      {hasRefCookie && !referralCode.trim() && (
                        <p className='text-muted-foreground flex items-center gap-1.5 text-xs'>
                          <CheckCircle2
                            className='text-success size-3.5 shrink-0'
                            aria-hidden='true'
                          />
                          {t('Partner referral attribution recorded')}
                        </p>
                      )}
                    </div>

                    {turnstileRequired && (
                      <div className='pt-1'>
                        <Turnstile
                          key={turnstileWidgetKey}
                          siteKey={data?.turnstile_site_key ?? ''}
                          onVerify={setTurnstileToken}
                          onExpire={() => setTurnstileToken('')}
                        />
                      </div>
                    )}

                    <Button
                      type='submit'
                      variant='brand'
                      size='lg'
                      className='h-10 w-full justify-center gap-2'
                      disabled={submitMutation.isPending || !turnstileReady}
                    >
                      {submitMutation.isPending && (
                        <Loader2
                          className='size-4 animate-spin'
                          aria-hidden='true'
                        />
                      )}
                      {t('Submit Partnership Inquiry')}
                    </Button>
                  </form>
                </CardContent>
              </Card>
            )}
        </section>
      </div>
    </PublicLayout>
  )
}
