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
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { ContentSection } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Separator } from '@/components/ui/separator'
import { Textarea } from '@/components/ui/textarea'
import { SecureVerificationDialog } from '@/features/auth/secure-verification/components/secure-verification-dialog'
import { useSecureVerification } from '@/features/auth/secure-verification/hooks/use-secure-verification'
import { SettingsSwitchField } from '@/features/system-settings/components/settings-form-layout'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { api } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'

function translationTaskStatusLabel(
  status: string,
  t: (key: string) => string
): string {
  switch (status) {
    case 'ready':
      return t('Ready')
    case 'failed':
      return t('Failed')
    case 'pending':
      return t('Pending')
    default:
      return status
  }
}

/** GET /api/adty/admin/translation (service.AdtyTranslationView). */
type TranslationView = {
  mode: string
  protocol: string
  base_url: string
  model_name: string
  backup_model: string
  token_mask: string
  timeout_sec: number
  concurrency: number
  max_chars: number
  daily_quota: number
  retry_limit: number
  enabled: boolean
  saved_at: number
  tested_at: number
  translated_at: number
}

/** GET /api/adty/admin/translation/usage: today's calls (UTC day). */
type TranslationUsage = {
  day: string
  calls: number
  failures: number
  latency_ms: number
  cost_usd: string
}

/** GET /api/adty/admin/translation/tasks (service.AdtyTranslationTaskView). */
type TranslationTask = {
  id: number
  job_id?: number
  object_key: string
  version: number
  source: string
  english: string
  status: string
  human_edited?: boolean
  model_name?: string
  notice?: string
  updated_at?: number
}

/** GET /api/adty/admin/translation/glossary (model.AdtyGlossaryTerm). */
type GlossaryTerm = {
  id: number
  source: string
  locale: string
  target: string
}

type Draft = {
  mode: string
  protocol: string
  base_url: string
  token: string
  model_name: string
  backup_model: string
  timeout_sec: string
  concurrency: string
  max_chars: string
  daily_quota: string
  retry_limit: string
  enabled: boolean
}

type GlossaryDraft = {
  id: number
  source: string
  locale: string
  target: string
}

const NUMBER_FIELDS = [
  'timeout_sec',
  'concurrency',
  'max_chars',
  'daily_quota',
  'retry_limit',
] as const

function draftOf(view: TranslationView | undefined): Draft {
  return {
    mode: view?.mode || 'platform',
    protocol: view?.protocol || 'openai',
    base_url: view?.base_url ?? '',
    token: '',
    model_name: view?.model_name ?? '',
    backup_model: view?.backup_model ?? '',
    timeout_sec: String(view?.timeout_sec ?? 0),
    concurrency: String(view?.concurrency ?? 0),
    max_chars: String(view?.max_chars ?? 0),
    daily_quota: String(view?.daily_quota ?? 0),
    retry_limit: String(view?.retry_limit ?? 0),
    enabled: view?.enabled ?? false,
  }
}

async function readData<T>(
  promise: Promise<{ data: unknown }>,
  fallback: string
) {
  const response = await promise
  const body = response.data as {
    success?: boolean
    data?: T
    message?: string
  }
  if (!body.success || body.data === undefined) {
    throw createServerError(body, fallback)
  }
  return body.data
}

function extractList<T>(raw: unknown, key: string): T[] {
  if (Array.isArray(raw)) return raw as T[]
  if (raw && typeof raw === 'object' && key in raw) {
    const nested = (raw as Record<string, unknown>)[key]
    if (Array.isArray(nested)) return nested as T[]
  }
  return []
}

function ConfigGate(props: {
  isPending: boolean
  isError: boolean
  error: unknown
  onRetry: () => void
  children: ReactNode
}) {
  const { t } = useTranslation()
  if (props.isPending) return <LoadingState />
  if (props.isError) {
    return (
      <ErrorState
        description={getServerErrorMessage(props.error, t('Failed to load'))}
        onRetry={props.onRetry}
      />
    )
  }
  return props.children
}

/**
 * Root configures the system translator. The form starts from the saved
 * configuration, so saving never resets fields the operator did not touch.
 * Saving and testing each need a fresh verification; customers are never
 * charged for translation.
 */
export function TranslationAdminPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const verification = useSecureVerification()
  const config = useQuery({
    queryKey: ['adty-translation'],
    queryFn: () =>
      readData<TranslationView>(
        api.get('/api/adty/admin/translation'),
        t('Failed to load')
      ),
  })
  const usage = useQuery({
    queryKey: ['adty-translation', 'usage'],
    queryFn: () =>
      readData<TranslationUsage>(
        api.get('/api/adty/admin/translation/usage'),
        t('Failed to load')
      ),
  })
  const tasksQuery = useQuery({
    queryKey: ['adty-translation', 'tasks'],
    queryFn: async () => {
      const data = await readData<unknown>(
        api.get('/api/adty/admin/translation/tasks'),
        t('Failed to load')
      )
      return extractList<TranslationTask>(data, 'tasks')
    },
  })
  const glossaryQuery = useQuery({
    queryKey: ['adty-translation', 'glossary'],
    queryFn: async () => {
      const data = await readData<unknown>(
        api.get('/api/adty/admin/translation/glossary'),
        t('Failed to load')
      )
      return extractList<GlossaryTerm>(data, 'terms')
    },
  })

  const [draft, setDraft] = useState<Draft>(() => draftOf(undefined))
  const [objectKey, setObjectKey] = useState('')
  const [source, setSource] = useState('')
  const [result, setResult] = useState('')
  const [lastTask, setLastTask] = useState<{
    object_key: string
    version: number
    status: string
  } | null>(null)
  const [editingTaskKey, setEditingTaskKey] = useState('')
  const [editingEnglish, setEditingEnglish] = useState('')
  const [glossaryDraft, setGlossaryDraft] = useState<GlossaryDraft>({
    id: 0,
    source: '',
    locale: 'en',
    target: '',
  })
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  useEffect(() => {
    if (config.data) setDraft(draftOf(config.data))
  }, [config.data])

  const independent = draft.mode === 'independent'
  const numbersValid = NUMBER_FIELDS.every((key) =>
    /^\d+$/.test(draft[key].trim())
  )
  const hasIndependentToken =
    Boolean(config.data?.token_mask?.trim()) || draft.token.trim() !== ''
  const ready =
    draft.model_name.trim() !== '' &&
    numbersValid &&
    (!independent || (draft.base_url.trim() !== '' && hasIndependentToken))

  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: ['adty-translation'] })

  const save = async () => {
    const mode = draft.mode
    const proof = await verification.requestVerification({
      scope: 'adty.notice.configure',
      context: { action: 'translation.save', target: mode },
      title: t('Confirm it is you'),
      description: t('Changing the translator requires a fresh verification.'),
    })
    if (!proof?.proof_token) return
    setPending(true)
    setError('')
    try {
      await readData(
        api.put(
          '/api/adty/admin/translation',
          {
            mode,
            protocol: independent ? draft.protocol : '',
            base_url: independent ? draft.base_url.trim() : '',
            token: independent ? draft.token.trim() : '',
            model_name: draft.model_name.trim(),
            backup_model: draft.backup_model.trim(),
            timeout_sec: Number(draft.timeout_sec),
            concurrency: Number(draft.concurrency),
            max_chars: Number(draft.max_chars),
            daily_quota: Number(draft.daily_quota),
            retry_limit: Number(draft.retry_limit),
            enabled: draft.enabled,
          },
          { headers: { 'X-Security-Proof': proof.proof_token } }
        ),
        t('Failed to save')
      )
      toast.success(t('Saved successfully'))
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Failed to save')))
    } finally {
      setPending(false)
    }
  }

  const test = async () => {
    const proof = await verification.requestVerification({
      scope: 'adty.notice.configure',
      context: { action: 'translation.test', target: 'translation' },
      title: t('Confirm it is you'),
      description: t('Testing the translator requires a fresh verification.'),
    })
    if (!proof?.proof_token) return
    setPending(true)
    setError('')
    try {
      await readData(
        api.post('/api/adty/admin/translation/test', undefined, {
          headers: { 'X-Security-Proof': proof.proof_token },
        }),
        t('Translator test failed')
      )
      toast.success(t('Translator test passed'))
      await refresh()
    } catch (cause) {
      setError(getServerErrorMessage(cause, t('Translator test failed')))
    } finally {
      setPending(false)
    }
  }

  const translate = () => {
    const targetKey =
      objectKey.trim() || lastTask?.object_key || 'notice:manual'
    if (!objectKey.trim()) {
      setObjectKey(targetKey)
    }
    setPending(true)
    setError('')
    void api
      .post('/api/adty/admin/translation/run', {
        object_key: targetKey,
        source,
      })
      .then(async (response) => {
        const body = response.data as {
          success?: boolean
          message?: string
          data?: {
            object_key?: string
            english?: string
            notice?: string
            source?: string
            status?: string
            version?: number
          }
        }
        if (!body.success) throw createServerError(body, t('Failed to save'))
        const resolvedKey = body.data?.object_key || targetKey
        const resolvedVersion = body.data?.version || 1
        const resolvedStatus =
          body.data?.status || (body.data?.english ? 'ready' : 'failed')
        setLastTask({
          object_key: resolvedKey,
          version: resolvedVersion,
          status: resolvedStatus,
        })
        const noticeText =
          !body.data?.notice || body.data.notice === '英文译文未就绪'
            ? t('English is not ready')
            : body.data.notice
        setResult(
          body.data?.english || `${body.data?.source || source}\n${noticeText}`
        )
        await refresh()
      })
      .catch((cause) =>
        setError(getServerErrorMessage(cause, t('Failed to save')))
      )
      .finally(() => setPending(false))
  }

  const retryTask = async (targetKey: string, version: number) => {
    setPending(true)
    setError('')
    try {
      const data = await readData<{
        object_key?: string
        english?: string
        notice?: string
        source?: string
        status?: string
        version?: number
      }>(
        api.post('/api/adty/admin/translation/retry', {
          object_key: targetKey,
          version,
        }),
        t('Failed to save')
      )
      setLastTask({
        object_key: data.object_key || targetKey,
        version: data.version || version,
        status: data.status || (data.english ? 'ready' : 'failed'),
      })
      const noticeText =
        !data.notice || data.notice === '英文译文未就绪'
          ? t('English is not ready')
          : data.notice
      setResult(data.english || `${data.source || source}\n${noticeText}`)
      await refresh()
    } catch (cause) {
      const message = getServerErrorMessage(cause, t('Failed to save'))
      setError(message)
      toast.error(message)
    } finally {
      setPending(false)
    }
  }

  const saveManualTranslation = async () => {
    if (!editingTaskKey || !editingEnglish.trim()) return
    setPending(true)
    setError('')
    try {
      await readData(
        api.put('/api/adty/admin/translation/edit', {
          object_key: editingTaskKey,
          english: editingEnglish.trim(),
        }),
        t('Failed to save')
      )
      setEditingTaskKey('')
      setEditingEnglish('')
      await refresh()
    } catch (cause) {
      const message = getServerErrorMessage(cause, t('Failed to save'))
      setError(message)
      toast.error(message)
    } finally {
      setPending(false)
    }
  }

  const saveGlossaryTerm = async () => {
    const src = glossaryDraft.source.trim()
    const tgt = glossaryDraft.target.trim()
    if (!src || !tgt) return
    setPending(true)
    setError('')
    try {
      if (glossaryDraft.id > 0) {
        await readData(
          api.put(`/api/adty/admin/translation/glossary/${glossaryDraft.id}`, {
            source: src,
            locale: glossaryDraft.locale,
            target: tgt,
          }),
          t('Failed to save')
        )
      } else {
        await readData(
          api.post('/api/adty/admin/translation/glossary', {
            source: src,
            locale: glossaryDraft.locale,
            target: tgt,
          }),
          t('Failed to save')
        )
      }
      setGlossaryDraft({ id: 0, source: '', locale: 'en', target: '' })
      await refresh()
    } catch (cause) {
      const message = getServerErrorMessage(cause, t('Failed to save'))
      setError(message)
      toast.error(message)
    } finally {
      setPending(false)
    }
  }

  const deleteGlossaryTerm = async (id: number) => {
    setPending(true)
    setError('')
    try {
      await readData(
        api.delete(`/api/adty/admin/translation/glossary/${id}`),
        t('Failed to delete')
      )
      if (glossaryDraft.id === id) {
        setGlossaryDraft({ id: 0, source: '', locale: 'en', target: '' })
      }
      await refresh()
    } catch (cause) {
      const message = getServerErrorMessage(cause, t('Failed to delete'))
      setError(message)
      toast.error(message)
    } finally {
      setPending(false)
    }
  }

  const time = (value?: number) => (value ? formatTimestamp(value) : t('Never'))
  const set = (patch: Partial<Draft>) =>
    setDraft((current) => ({ ...current, ...patch }))
  const numberField = (key: (typeof NUMBER_FIELDS)[number], label: string) => (
    <div className='flex flex-col gap-1.5'>
      <Label
        htmlFor={`translator-${key}`}
        className='text-[13.5px] leading-snug font-semibold'
      >
        {label}
      </Label>
      <Input
        id={`translator-${key}`}
        inputMode='numeric'
        className='tabular-nums sm:max-w-40'
        value={draft[key]}
        onChange={(event) => set({ [key]: event.target.value })}
      />
    </div>
  )

  const tasks = tasksQuery.data ?? []
  const glossaryTerms = glossaryQuery.data ?? []
  const showSections = !config.isPending && !config.isError

  return (
    <div className='flex min-w-0 flex-col gap-8'>
      <SettingsSection
        title={t('System translation')}
        bodyClassName='space-y-6'
      >
        <p className='text-dim max-w-[80ch] text-[13px] leading-relaxed'>
          {t(
            'Chinese stays available when English is not ready. Customers are not charged.'
          )}
        </p>
        <ConfigGate
          isPending={config.isPending}
          isError={config.isError}
          error={config.error}
          onRetry={() => {
            void config.refetch()
            void usage.refetch()
            void tasksQuery.refetch()
            void glossaryQuery.refetch()
          }}
        >
          <div className='flex flex-col gap-6'>
            <div className='bg-muted/40 flex flex-col gap-4 rounded-xl p-4'>
              <dl className='grid gap-x-6 gap-y-2 text-sm sm:grid-cols-3'>
                <div>
                  <dt className='text-dim text-[12px]'>{t('Saved')}</dt>
                  <dd className='mt-0.5 font-medium tabular-nums'>
                    {time(config.data?.saved_at)}
                  </dd>
                </div>
                <div>
                  <dt className='text-dim text-[12px]'>
                    {t('Last test passed')}
                  </dt>
                  <dd className='mt-0.5 font-medium tabular-nums'>
                    {time(config.data?.tested_at)}
                  </dd>
                </div>
                <div>
                  <dt className='text-dim text-[12px]'>
                    {t('Last translation')}
                  </dt>
                  <dd className='mt-0.5 font-medium tabular-nums'>
                    {time(config.data?.translated_at)}
                  </dd>
                </div>
              </dl>
              <section
                aria-label={t('Today')}
                className='border-border flex flex-col gap-1 border-t pt-3 text-sm'
              >
                <h4 className='text-[13.5px] font-semibold'>
                  {t('Today (UTC)')}
                </h4>
                {usage.isError && (
                  <p className='text-dim text-[13px]'>
                    {t('Failed to load data')}
                  </p>
                )}
                {usage.data && (
                  <p className='text-dim text-[13px] tabular-nums'>
                    {t('Calls')} {usage.data.calls ?? 0} · {t('Failures')}{' '}
                    {usage.data.failures ?? 0} · {t('Average latency')}{' '}
                    {usage.data.calls
                      ? Math.round(
                          (usage.data.latency_ms ?? 0) / usage.data.calls
                        )
                      : 0}{' '}
                    ms
                    {usage.data.cost_usd
                      ? ` · ${t('Estimated cost')} $${usage.data.cost_usd}`
                      : ''}
                    {draft.daily_quota !== '0'
                      ? ` · ${t('Daily limit')} ${draft.daily_quota}`
                      : ''}
                  </p>
                )}
              </section>
            </div>

            <div className='grid max-w-3xl gap-x-6 gap-y-4 sm:grid-cols-2'>
              <div className='flex flex-col gap-1.5'>
                <Label
                  htmlFor='translator-mode'
                  className='text-[13.5px] leading-snug font-semibold'
                >
                  {t('Translator')}
                </Label>
                <NativeSelect
                  id='translator-mode'
                  value={draft.mode}
                  onChange={(event) => set({ mode: event.target.value })}
                >
                  <NativeSelectOption value='platform'>
                    {t('Platform model')}
                  </NativeSelectOption>
                  <NativeSelectOption value='independent'>
                    {t('Independent API')}
                  </NativeSelectOption>
                </NativeSelect>
              </div>
              {independent && (
                <div className='flex flex-col gap-1.5'>
                  <Label
                    htmlFor='translator-protocol'
                    className='text-[13.5px] leading-snug font-semibold'
                  >
                    {t('Protocol')}
                  </Label>
                  <NativeSelect
                    id='translator-protocol'
                    value={draft.protocol}
                    onChange={(event) => set({ protocol: event.target.value })}
                  >
                    <NativeSelectOption value='openai'>
                      OpenAI Chat Completions
                    </NativeSelectOption>
                    <NativeSelectOption value='anthropic'>
                      Anthropic Messages
                    </NativeSelectOption>
                  </NativeSelect>
                </div>
              )}
              {independent && (
                <div className='flex max-w-xl flex-col gap-1.5 sm:col-span-2'>
                  <Label
                    htmlFor='translator-base-url'
                    className='text-[13.5px] leading-snug font-semibold'
                  >
                    {t('API address (https)')}
                  </Label>
                  <Input
                    id='translator-base-url'
                    value={draft.base_url}
                    onChange={(event) => set({ base_url: event.target.value })}
                  />
                </div>
              )}
              {independent && (
                <div className='flex max-w-xl flex-col gap-1.5 sm:col-span-2'>
                  <Label
                    htmlFor='translator-token'
                    className='text-[13.5px] leading-snug font-semibold'
                  >
                    {t('API key')}
                  </Label>
                  <Input
                    id='translator-token'
                    type='password'
                    autoComplete='off'
                    placeholder={
                      config.data?.token_mask
                        ? t('Saved: {{mask}}. Leave empty to keep it.', {
                            mask: config.data.token_mask,
                          })
                        : t('Not configured')
                    }
                    value={draft.token}
                    onChange={(event) => set({ token: event.target.value })}
                  />
                </div>
              )}
              <div className='flex flex-col gap-1.5'>
                <Label
                  htmlFor='translator-model'
                  className='text-[13.5px] leading-snug font-semibold'
                >
                  {t('Model')}
                </Label>
                <Input
                  id='translator-model'
                  value={draft.model_name}
                  onChange={(event) => set({ model_name: event.target.value })}
                />
              </div>
              <div className='flex flex-col gap-1.5'>
                <Label
                  htmlFor='translator-backup'
                  className='text-[13.5px] leading-snug font-semibold'
                >
                  {t('Backup model')}
                </Label>
                <Input
                  id='translator-backup'
                  value={draft.backup_model}
                  onChange={(event) =>
                    set({ backup_model: event.target.value })
                  }
                />
              </div>
            </div>

            <div className='grid max-w-3xl gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3'>
              {numberField('timeout_sec', t('Timeout (seconds, 0 = default)'))}
              {numberField('concurrency', t('Concurrent calls (0 = no limit)'))}
              {numberField('max_chars', t('Maximum characters (0 = no limit)'))}
              {numberField('daily_quota', t('Daily calls (0 = no limit)'))}
              {numberField(
                'retry_limit',
                t('Retries with the backup model (0 = none)')
              )}
            </div>

            <SettingsSwitchField
              controlId='translator-enabled'
              checked={draft.enabled}
              onCheckedChange={(value) => set({ enabled: value })}
              label={t('Use this translator')}
              className='max-w-3xl py-1'
            />

            <div className='flex flex-wrap gap-2'>
              <Button
                variant='brand'
                type='button'
                disabled={pending || !ready}
                onClick={() => void save()}
              >
                {t('Save')}
              </Button>
              <Button
                variant='outline'
                type='button'
                disabled={pending || !config.data?.saved_at}
                onClick={() => void test()}
              >
                {t('Test connection')}
              </Button>
            </div>

            <Separator />

            <div className='flex max-w-3xl flex-col gap-4'>
              <div className='flex max-w-sm flex-col gap-1.5'>
                <Label
                  htmlFor='translator-object-key'
                  className='text-[13.5px] leading-snug font-semibold'
                >
                  {t('Object key')}
                </Label>
                <Input
                  id='translator-object-key'
                  placeholder='notice:manual'
                  value={objectKey}
                  onChange={(event) => setObjectKey(event.target.value)}
                />
              </div>
              <div className='flex flex-col gap-1.5'>
                <Label
                  htmlFor='translator-source'
                  className='text-[13.5px] leading-snug font-semibold'
                >
                  {t('Chinese source')}
                </Label>
                <Textarea
                  id='translator-source'
                  value={source}
                  onChange={(event) => setSource(event.target.value)}
                />
              </div>
              <div className='flex flex-wrap gap-2'>
                <Button
                  type='button'
                  variant='outline'
                  disabled={pending || source.trim() === ''}
                  onClick={translate}
                >
                  {t('Translate')}
                </Button>
                {lastTask?.status === 'failed' && (
                  <Button
                    type='button'
                    variant='outline'
                    disabled={pending}
                    onClick={() =>
                      void retryTask(lastTask.object_key, lastTask.version)
                    }
                  >
                    {t('Retry task')}
                  </Button>
                )}
              </div>
              {result && (
                <p className='text-sm whitespace-pre-wrap'>{result}</p>
              )}
            </div>

            {error && <p className='text-destructive text-sm'>{error}</p>}
          </div>
        </ConfigGate>
      </SettingsSection>

      {showSections && tasks.length > 0 && (
        <ContentSection
          title={t('Translation tasks')}
          aria-label={t('Translation tasks')}
        >
          <div className='border-border bg-card divide-border divide-y overflow-hidden rounded-[14px] border text-sm'>
            {tasks.map((task) => (
              <div
                key={`${task.object_key}-${task.version}`}
                className='flex flex-col gap-2 px-5 py-4'
              >
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <span className='font-mono text-xs'>
                    {task.object_key} (v{task.version}) ·{' '}
                    {translationTaskStatusLabel(task.status, t)}
                  </span>
                  <div className='flex flex-wrap gap-2'>
                    {task.status === 'failed' && (
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={pending}
                        onClick={() =>
                          void retryTask(task.object_key, task.version)
                        }
                      >
                        {t('Retry task')}
                      </Button>
                    )}
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      disabled={pending}
                      onClick={() => {
                        setEditingTaskKey(task.object_key)
                        setEditingEnglish(task.english || '')
                      }}
                    >
                      {t('Edit translation')}
                    </Button>
                  </div>
                </div>
                <p className='text-dim text-xs'>{task.source}</p>
                {task.english && <p className='text-xs'>{task.english}</p>}
              </div>
            ))}
            {editingTaskKey && (
              <div className='bg-muted/30 flex flex-col gap-3 px-5 py-4'>
                <Label
                  htmlFor='translator-manual-english'
                  className='text-[13.5px] leading-snug font-semibold'
                >
                  {t('Manual English override')} ({editingTaskKey})
                </Label>
                <Textarea
                  id='translator-manual-english'
                  value={editingEnglish}
                  onChange={(event) => setEditingEnglish(event.target.value)}
                />
                <div className='flex justify-start'>
                  <Button
                    variant='brand'
                    type='button'
                    size='sm'
                    disabled={pending || editingEnglish.trim() === ''}
                    onClick={() => void saveManualTranslation()}
                  >
                    {t('Save translation')}
                  </Button>
                </div>
              </div>
            )}
          </div>
        </ContentSection>
      )}

      {showSections && (
        <ContentSection title={t('Glossary')} aria-label={t('Glossary')}>
          <div className='flex flex-col gap-4'>
            <div className='flex flex-wrap items-end gap-3'>
              <div className='flex w-full flex-col gap-1.5 sm:w-56'>
                <Label
                  htmlFor='glossary-source'
                  className='text-[13.5px] leading-snug font-semibold'
                >
                  {t('Source term')}
                </Label>
                <Input
                  id='glossary-source'
                  value={glossaryDraft.source}
                  onChange={(event) =>
                    setGlossaryDraft((cur) => ({
                      ...cur,
                      source: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='flex w-full flex-col gap-1.5 sm:w-56'>
                <Label
                  htmlFor='glossary-target'
                  className='text-[13.5px] leading-snug font-semibold'
                >
                  {t('Target term')}
                </Label>
                <Input
                  id='glossary-target'
                  value={glossaryDraft.target}
                  onChange={(event) =>
                    setGlossaryDraft((cur) => ({
                      ...cur,
                      target: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='flex w-32 flex-col gap-1.5'>
                <Label
                  htmlFor='glossary-locale'
                  className='text-[13.5px] leading-snug font-semibold'
                >
                  {t('Glossary locale')}
                </Label>
                <NativeSelect
                  id='glossary-locale'
                  value={glossaryDraft.locale}
                  onChange={(event) =>
                    setGlossaryDraft((cur) => ({
                      ...cur,
                      locale: event.target.value,
                    }))
                  }
                >
                  <NativeSelectOption value='en'>en</NativeSelectOption>
                  <NativeSelectOption value='zh-CN'>zh-CN</NativeSelectOption>
                </NativeSelect>
              </div>
              <Button
                variant='brand'
                type='button'
                size='sm'
                disabled={
                  pending ||
                  glossaryDraft.source.trim() === '' ||
                  glossaryDraft.target.trim() === ''
                }
                onClick={() => void saveGlossaryTerm()}
              >
                {t('Save term')}
              </Button>
            </div>
            {glossaryTerms.length > 0 && (
              <div className='border-border bg-card divide-border divide-y overflow-hidden rounded-[14px] border text-sm'>
                {glossaryTerms.map((term) => (
                  <div
                    key={term.id}
                    className='flex flex-wrap items-center justify-between gap-2 px-5 py-3'
                  >
                    <span>
                      {term.source} → {term.target} ({term.locale})
                    </span>
                    <div className='flex gap-2'>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={pending}
                        onClick={() =>
                          setGlossaryDraft({
                            id: term.id,
                            source: term.source,
                            locale: term.locale,
                            target: term.target,
                          })
                        }
                      >
                        {t('Edit term')}
                      </Button>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={pending}
                        onClick={() => void deleteGlossaryTerm(term.id)}
                      >
                        {t('Delete term')}
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </ContentSection>
      )}

      <SecureVerificationDialog {...verification.dialogProps} />
    </div>
  )
}
