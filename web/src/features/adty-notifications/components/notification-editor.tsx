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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useMemo, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { DraftDiscardDialog } from '@/features/system-settings/components/draft-discard-dialog'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { getUserSites } from '@/features/users/api'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  notificationManagerKey,
  savePublication,
  type NotificationMode,
  type Publication,
} from '../api'
import {
  getNotificationPlainLength,
  MAX_NOTIFICATION_PLAIN_LENGTH,
  sanitizeNotificationHtml,
  toInitialEditorHtml,
} from '../lib/rich-text'
import {
  createNotificationSchema,
  deriveLegacyAudience,
  notificationKindLabels,
  resolveInitialTargets,
  type NotificationInput,
  type NotificationTargetAudience,
} from '../lib/schema'
import { RichNotificationEditor } from './rich-notification-editor'

interface AudienceOption {
  value: NotificationTargetAudience
  labelKey: string
  descriptionKey?: string
}

const ROOT_AUDIENCE_OPTIONS: readonly AudienceOption[] = [
  {
    value: 'main_users',
    labelKey: 'Main-site users (main site only)',
  },
  {
    value: 'all_site_owners',
    labelKey: 'All subsite owners',
  },
  {
    value: 'site_users',
    labelKey: 'Specific subsite(s) all users',
    descriptionKey: 'Includes all users and teams of the selected subsites',
  },
  {
    value: 'main_teams',
    labelKey: 'Main-site teams',
  },
]

const OWNER_AUDIENCE_OPTIONS: readonly AudienceOption[] = [
  {
    value: 'site_users',
    labelKey: 'Subsite users',
    descriptionKey: 'All users of this site',
  },
  {
    value: 'site_teams',
    labelKey: 'Subsite team users',
    descriptionKey: 'All team users of this site',
  },
]

export function NotificationEditor(props: {
  mode: NotificationMode
  siteId: number
  userId: number
  row: Publication | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const client = useQueryClient()
  const [discarding, setDiscarding] = useState(false)

  const publishSchema = useMemo(
    () => createNotificationSchema(props.mode, false),
    [props.mode]
  )
  const draftSchema = useMemo(
    () => createNotificationSchema(props.mode, true),
    [props.mode]
  )

  const initialTargets = useMemo(
    () => resolveInitialTargets(props.mode, props.row),
    [props.mode, props.row]
  )

  const form = useForm<NotificationInput>({
    resolver: zodResolver(publishSchema),
    defaultValues: props.row
      ? {
          title: props.row.title ?? '',
          content: toInitialEditorHtml(
            props.row.content ?? '',
            props.row.content_format
          ),
          content_format: 'html',
          kind: props.row.kind ?? 'platform',
          audience: props.row.audience ?? (props.mode === 'owner' ? 'site' : 'all_users'),
          target_audiences: initialTargets.target_audiences,
          target_site_ids: initialTargets.target_site_ids,
          target_site_id: props.row.target_site_id ?? props.siteId,
          target_team_id: props.row.target_team_id ?? 0,
          target_user_id: props.row.target_user_id ?? 0,
          business: props.row.business ?? '',
          sync_telegram:
            (props.row.telegram_at ?? 0) > 0 ||
            Boolean(props.row.telegram_channel),
          telegram_channel:
            props.mode === 'owner' || props.row.telegram_channel === '@private'
              ? ''
              : (props.row.telegram_channel ?? ''),
        }
      : {
          title: '',
          content: '',
          content_format: 'html',
          kind: 'platform',
          audience: props.mode === 'owner' ? 'site' : 'all_users',
          target_audiences: initialTargets.target_audiences,
          target_site_ids: initialTargets.target_site_ids,
          target_site_id: props.siteId,
          target_team_id: 0,
          target_user_id: 0,
          business: '',
          sync_telegram: false,
          telegram_channel: '',
        },
  })

  const targetAudiences = form.watch('target_audiences')
  const targetSiteIds = form.watch('target_site_ids')
  const contentValue = form.watch('content')
  const syncTelegram = form.watch('sync_telegram')

  const plainLength = getNotificationPlainLength(contentValue)
  const isContentOverLimit = plainLength > MAX_NOTIFICATION_PLAIN_LENGTH
  const showSubsitePicker =
    props.mode === 'root' && targetAudiences.includes('site_users')
  const canSpecifyTelegramChannel = props.mode === 'root'

  useDraftNavigation(form.formState.isDirty)

  const sites = useQuery({
    queryKey: ['adty-notification-targets', props.userId],
    queryFn: async () => requireServerSuccess(await getUserSites()).data,
    enabled: showSubsitePicker,
  })
  const subsites = useMemo(
    () =>
      (Array.isArray(sites.data) ? sites.data : []).filter(
        (site) => site.site_id > 0
      ),
    [sites.data]
  )

  const canSaveDraft = !props.row || props.row.status === 'draft'

  const preparePayload = (input: NotificationInput): NotificationInput => {
    const sanitizedContent = sanitizeNotificationHtml(input.content)
    const filteredSiteIds =
      props.mode === 'root' && input.target_audiences.includes('site_users')
        ? [...new Set(input.target_site_ids.filter((siteId) => siteId > 0))]
        : []
    const legacy = deriveLegacyAudience(
      props.mode,
      props.siteId,
      input.target_audiences,
      filteredSiteIds
    )
    return {
      ...input,
      content: sanitizedContent,
      content_format: 'html',
      audience: legacy.audience,
      target_audiences: [...new Set(input.target_audiences)],
      target_site_ids:
        props.mode === 'owner' && props.siteId > 0
          ? [props.siteId]
          : filteredSiteIds,
      target_site_id: legacy.target_site_id,
      target_team_id: 0,
      target_user_id: 0,
      sync_telegram: input.sync_telegram,
      telegram_channel: canSpecifyTelegramChannel ? input.telegram_channel : '',
    }
  }

  const save = useMutation({
    mutationFn: (input: NotificationInput & { draft?: boolean }) => {
      const user = useAuthStore.getState().auth.user
      if (
        user?.id !== props.userId ||
        (props.mode === 'root' && user.role < ROLE.SUPER_ADMIN)
      ) {
        throw new Error(t('Unauthorized, insufficient privileges'))
      }
      const prepared = preparePayload(input)
      return savePublication(props.mode, props.row, {
        ...prepared,
        ...(input.draft ? { draft: true } : {}),
      } as NotificationInput)
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: notificationManagerKey })
      void client.invalidateQueries({ queryKey: ['adty-notifications'] })
      void client.invalidateQueries({ queryKey: ['status'] })
      try {
        window.localStorage.removeItem('status')
      } catch {
        /* empty */
      }
      props.onClose()
    },
    onError: (error) => handleServerError(error),
  })

  const close = () => {
    if (save.isPending) return
    if (form.formState.isDirty) setDiscarding(true)
    else props.onClose()
  }

  const toggleAudience = (
    audienceKey: NotificationTargetAudience,
    checked: boolean
  ) => {
    const current = form.getValues('target_audiences')
    const next = checked
      ? [...new Set([...current, audienceKey])]
      : current.filter((item) => item !== audienceKey)
    form.setValue('target_audiences', next, {
      shouldDirty: true,
      shouldValidate: true,
    })
    if (audienceKey === 'site_users' && !checked) {
      form.setValue('target_site_ids', [], { shouldDirty: true })
      form.clearErrors('target_site_ids')
    }
  }

  const toggleSubsite = (siteId: number, checked: boolean) => {
    const current = form.getValues('target_site_ids')
    const next = checked
      ? [...new Set([...current, siteId])]
      : current.filter((existing) => existing !== siteId)
    form.setValue('target_site_ids', next, {
      shouldDirty: true,
      shouldValidate: true,
    })
  }

  const handleDraftSave = () => {
    form.clearErrors()
    const raw = form.getValues()
    const parsed = draftSchema.safeParse({
      ...raw,
      content_format: 'html',
      sync_telegram: raw.sync_telegram,
      telegram_channel: canSpecifyTelegramChannel ? raw.telegram_channel : '',
    })
    if (!parsed.success) {
      for (const issue of parsed.error.issues) {
        const field = issue.path[0] as keyof NotificationInput | undefined
        if (field) {
          form.setError(field, { type: 'manual', message: issue.message })
        }
      }
      return
    }
    save.mutate({ ...parsed.data, draft: true })
  }

  const audienceOptions =
    props.mode === 'root' ? ROOT_AUDIENCE_OPTIONS : OWNER_AUDIENCE_OPTIONS
  const errors = form.formState.errors
  let submitLabel = 'Publish notification'
  if (props.row?.status === 'draft') {
    submitLabel = 'Publish now'
  } else if (props.row) {
    submitLabel = 'Save changes'
  }

  return (
    <>
      <Dialog
        open
        contentClassName='sm:max-w-3xl'
        onOpenChange={(open) => {
          if (!open) close()
        }}
        title={t(props.row ? 'Edit notification' : 'New notification')}
        description={t(
          'Published notifications appear in the recipient notification center.'
        )}
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              disabled={save.isPending}
              onClick={close}
            >
              {t('Cancel')}
            </Button>
            {canSaveDraft && (
              <Button
                type='button'
                variant='secondary'
                disabled={save.isPending || isContentOverLimit}
                onClick={handleDraftSave}
              >
                {t('Save as draft')}
              </Button>
            )}
            <Button
              variant='brand'
              type='submit'
              form={id}
              disabled={save.isPending || isContentOverLimit}
            >
              {t(submitLabel)}
            </Button>
          </>
        }
      >
        <form
          id={id}
          onSubmit={form.handleSubmit((input) => save.mutate(input))}
        >
          <FieldGroup>
            <Field data-invalid={!!errors.title}>
              <FieldLabel htmlFor={`${id}-title`}>{t('Title')}</FieldLabel>
              <Input
                id={`${id}-title`}
                maxLength={120}
                aria-invalid={!!errors.title}
                disabled={save.isPending}
                {...form.register('title')}
              />
              <FieldError>
                {errors.title?.message ? t(errors.title.message) : null}
              </FieldError>
            </Field>

            <Field data-invalid={!!errors.content || isContentOverLimit}>
              <FieldLabel htmlFor={`${id}-content`}>{t('Content')}</FieldLabel>
              <Controller
                control={form.control}
                name='content'
                render={({ field }) => (
                  <RichNotificationEditor
                    id={`${id}-content`}
                    value={field.value}
                    onChange={(nextHtml) => field.onChange(nextHtml)}
                    disabled={save.isPending}
                    invalid={!!errors.content || isContentOverLimit}
                  />
                )}
              />
              <FieldError>
                {errors.content?.message ? t(errors.content.message) : null}
              </FieldError>
            </Field>

            <Field>
              <FieldLabel htmlFor={`${id}-kind`}>
                {t('Notification type')}
              </FieldLabel>
              <NativeSelect
                id={`${id}-kind`}
                disabled={save.isPending}
                {...form.register('kind')}
              >
                {Object.entries(notificationKindLabels).map(
                  ([value, label]) => (
                    <NativeSelectOption key={value} value={value}>
                      {t(label)}
                    </NativeSelectOption>
                  )
                )}
              </NativeSelect>
            </Field>

            <Field data-invalid={!!errors.target_audiences}>
              <FieldLabel>{t('Recipients')}</FieldLabel>
              <div className='grid gap-2 rounded-lg border p-3 sm:grid-cols-2'>
                {audienceOptions.map((option) => {
                  const checkboxId = `${id}-audience-${option.value}`
                  const isChecked = targetAudiences.includes(option.value)
                  return (
                    <div key={option.value} className='flex items-start gap-2.5'>
                      <Checkbox
                        id={checkboxId}
                        disabled={save.isPending}
                        checked={isChecked}
                        onCheckedChange={(checked) =>
                          toggleAudience(option.value, checked === true)
                        }
                      />
                      <div className='min-w-0 leading-snug'>
                        <Label
                          htmlFor={checkboxId}
                          className='cursor-pointer text-sm font-normal'
                        >
                          {t(option.labelKey)}
                        </Label>
                        {option.descriptionKey && (
                          <p className='text-muted-foreground mt-0.5 text-xs'>
                            {t(option.descriptionKey)}
                          </p>
                        )}
                      </div>
                    </div>
                  )
                })}
              </div>
              <FieldError>
                {errors.target_audiences?.message
                  ? t(errors.target_audiences.message)
                  : null}
              </FieldError>
            </Field>

            {showSubsitePicker && (
              <Field data-invalid={!!errors.target_site_ids}>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <FieldLabel>
                    {t('Select subsites')}
                    {targetSiteIds.length > 0 && (
                      <span className='text-muted-foreground ml-1.5 text-xs font-normal'>
                        {t('Selected subsites ({{count}})', {
                          count: targetSiteIds.length,
                        })}
                      </span>
                    )}
                  </FieldLabel>
                  {subsites.length > 0 && (
                    <div className='flex items-center gap-1.5'>
                      <Button
                        type='button'
                        size='xs'
                        variant='outline'
                        disabled={save.isPending}
                        onClick={() =>
                          form.setValue(
                            'target_site_ids',
                            subsites.map((site) => site.site_id),
                            { shouldDirty: true, shouldValidate: true }
                          )
                        }
                      >
                        {t('Select all')}
                      </Button>
                      <Button
                        type='button'
                        size='xs'
                        variant='ghost'
                        disabled={save.isPending || targetSiteIds.length === 0}
                        onClick={() =>
                          form.setValue('target_site_ids', [], {
                            shouldDirty: true,
                            shouldValidate: true,
                          })
                        }
                      >
                        {t('Clear')}
                      </Button>
                    </div>
                  )}
                </div>

                {sites.isLoading && (
                  <p className='text-muted-foreground text-sm'>
                    {t('Loading...')}
                  </p>
                )}
                {sites.isError && (
                  <ErrorState
                    className='min-h-24'
                    onRetry={() => void sites.refetch()}
                  />
                )}
                {sites.isSuccess && subsites.length === 0 && (
                  <p className='text-muted-foreground text-sm'>
                    {t('No subsites available')}
                  </p>
                )}
                {sites.isSuccess && subsites.length > 0 && (
                  <div className='max-h-44 overflow-y-auto rounded-lg border p-3'>
                    <div className='grid gap-2 sm:grid-cols-2'>
                      {subsites.map((site) => {
                        const siteCheckboxId = `${id}-site-${site.site_id}`
                        const isChecked = targetSiteIds.includes(site.site_id)
                        return (
                          <div
                            key={site.site_id}
                            className='flex items-center gap-2'
                          >
                            <Checkbox
                              id={siteCheckboxId}
                              disabled={save.isPending}
                              checked={isChecked}
                              onCheckedChange={(checked) =>
                                toggleSubsite(site.site_id, checked === true)
                              }
                            />
                            <Label
                              htmlFor={siteCheckboxId}
                              className='min-w-0 cursor-pointer truncate text-sm font-normal'
                              title={site.site_name}
                            >
                              {site.site_name}
                            </Label>
                          </div>
                        )
                      })}
                    </div>
                  </div>
                )}
                <FieldError>
                  {errors.target_site_ids?.message
                    ? t(errors.target_site_ids.message)
                    : null}
                </FieldError>
              </Field>
            )}

            <Field>
              <div className='flex items-center gap-2'>
                <Checkbox
                  id={`${id}-sync-telegram`}
                  disabled={save.isPending}
                  checked={syncTelegram === true}
                  onCheckedChange={(checked) =>
                    form.setValue('sync_telegram', checked === true, {
                      shouldDirty: true,
                    })
                  }
                />
                <Label htmlFor={`${id}-sync-telegram`}>
                  {t('Sync to Telegram')}
                </Label>
              </div>
              {!canSpecifyTelegramChannel && syncTelegram && (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Bound users of this site will receive a private message via the Telegram bot'
                  )}
                </p>
              )}
            </Field>

            {canSpecifyTelegramChannel && syncTelegram && (
              <Field data-invalid={!!errors.telegram_channel}>
                <FieldLabel htmlFor={`${id}-telegram-channel`}>
                  {t('Telegram channel (optional)')}
                </FieldLabel>
                <Input
                  id={`${id}-telegram-channel`}
                  maxLength={128}
                  placeholder='@your_channel'
                  aria-invalid={!!errors.telegram_channel}
                  disabled={save.isPending}
                  {...form.register('telegram_channel')}
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Leave blank to push privately to bound recipients via Telegram bot, or enter a public channel/group ID (e.g. @channel or -100...)'
                  )}
                </p>
                <FieldError>
                  {errors.telegram_channel?.message
                    ? t(errors.telegram_channel.message)
                    : null}
                </FieldError>
              </Field>
            )}

            <Field data-invalid={!!errors.business}>
              <FieldLabel htmlFor={`${id}-business`}>
                {t('Related business')}
              </FieldLabel>
              <Input
                id={`${id}-business`}
                maxLength={200}
                aria-invalid={!!errors.business}
                disabled={save.isPending}
                {...form.register('business')}
              />
              <FieldError>
                {errors.business?.message ? t(errors.business.message) : null}
              </FieldError>
            </Field>
          </FieldGroup>
        </form>
      </Dialog>
      <DraftDiscardDialog
        open={discarding}
        onOpenChange={setDiscarding}
        onDiscard={props.onClose}
      />
    </>
  )
}
