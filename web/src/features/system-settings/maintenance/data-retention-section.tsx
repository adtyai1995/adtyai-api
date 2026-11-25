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
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ContentSection } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { api } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsSection } from '../components/settings-section'

type RetentionCategory = {
  category: string
  title: string
  auto_cleanable: boolean
  configured: boolean
  retention_days: number | null
  action: string
  min_retention_days?: number | null
  allowed_actions: string[]
  default_behavior: string
  notes: string
}

type RetentionSettings = {
  categories: RetentionCategory[]
  login_security_env_default_days?: number
  usage_log_min_retention_days?: number
}

type Draft = {
  days: string
  action: string
}

/** GET /api/adty/admin/data-retention/runs (service.DataRetentionRunView). */
type RetentionRun = {
  id: number
  started_at: number
  finished_at: number
  trigger: string
  scope: string
  items: Array<{
    id: number
    category: string
    delete_count: number
    deidentify_count: number
    failure_count: number
    skip_reason: string
  }>
}

const ACTION_LABELS: Record<string, string> = {
  delete: 'Delete after the period',
  deidentify: 'De-identify after the period',
  exclude: 'Permanent retention (not auto-cleaned)',
}

function RetentionGate(props: {
  isRoot: boolean
  isError: boolean
  error: unknown
  children: ReactNode
}) {
  const { t } = useTranslation()
  if (!props.isRoot) {
    return (
      <p className='text-sm'>
        {t('Only Root can view and change data retention periods.')}
      </p>
    )
  }
  if (props.isError) {
    return (
      <p className='text-destructive text-sm'>
        {getServerErrorMessage(props.error, t('Failed to load data'))}
      </p>
    )
  }
  return props.children
}

export function DataRetentionSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const isRoot = user?.role === ROLE.SUPER_ADMIN
  const settings = useQuery({
    queryKey: ['adty-data-retention'],
    queryFn: async () => {
      const response = await api.get('/api/adty/admin/data-retention')
      const body = response.data as {
        success?: boolean
        data?: RetentionSettings
        message?: string
      }
      if (!body.success || !body.data) {
        throw new Error(body.message || 'Failed to load data')
      }
      return body.data
    },
    enabled: isRoot,
  })
  const runs = useQuery({
    queryKey: ['adty-data-retention', 'runs'],
    queryFn: async () => {
      const response = await api.get('/api/adty/admin/data-retention/runs', {
        params: { limit: 10 },
      })
      const body = response.data as {
        success?: boolean
        data?: { runs?: RetentionRun[] }
        message?: string
      }
      if (!body.success) throw new Error(body.message || 'Failed to load data')
      return Array.isArray(body.data?.runs) ? body.data.runs : []
    },
    enabled: isRoot,
  })
  const [drafts, setDrafts] = useState<Record<string, Draft>>({})
  const savedRef = useRef<Record<string, Draft> | null>(null)

  useEffect(() => {
    if (!settings.data?.categories) return
    const incoming: Record<string, Draft> = {}
    for (const category of settings.data.categories) {
      incoming[category.category] = {
        days:
          category.retention_days != null
            ? String(category.retention_days)
            : '',
        action: category.action || category.allowed_actions[0] || 'delete',
      }
    }
    const prevSaved = savedRef.current
    if (!prevSaved) {
      savedRef.current = incoming
      setDrafts(incoming)
      return
    }
    setDrafts((prev) => {
      const next: Record<string, Draft> = { ...prev }
      for (const category of settings.data?.categories ?? []) {
        const key = category.category
        const cur = prev[key]
        const base = prevSaved[key]
        const isClean =
          !cur ||
          !base ||
          (cur.days === base.days && cur.action === base.action)
        if (isClean) {
          next[key] = incoming[key]
        }
      }
      return next
    })
    savedRef.current = incoming
  }, [settings.data])

  const save = useMutation({
    mutationFn: async (
      policies: Array<{
        category: string
        clear?: boolean
        retention_days?: number
        action?: string
      }>
    ) => {
      const response = await api.put('/api/adty/admin/data-retention', {
        policies,
      })
      const body = response.data as { success?: boolean; message?: string }
      if (!body.success) {
        throw new Error(body.message || 'Failed to save')
      }
    },
    onSuccess: async (_, policies) => {
      if (savedRef.current) {
        const nextSaved = { ...savedRef.current }
        for (const item of policies) {
          delete nextSaved[item.category]
        }
        savedRef.current = nextSaved
      }
      toast.success(
        t(
          'Retention settings saved. Later cleanup runs will use the new periods.'
        )
      )
      await queryClient.invalidateQueries({ queryKey: ['adty-data-retention'] })
    },
    onError: (error) => {
      toast.error(getServerErrorMessage(error, t('Failed to save')))
    },
  })

  const categories = settings.data?.categories ?? []
  const canSave = useMemo(
    () => isRoot && categories.length > 0,
    [categories.length, isRoot]
  )

  const saveCategory = (category: RetentionCategory) => {
    const draft = drafts[category.category]
    if (!draft) return
    if (!category.auto_cleanable) return
    const days = draft.days.trim()
    if (days === '') {
      save.mutate([
        {
          category: category.category,
          clear: true,
        },
      ])
      return
    }
    const parsed = Number(days)
    if (!Number.isInteger(parsed) || parsed < 1) {
      toast.error(t('A retention period must be a positive number of days'))
      return
    }
    save.mutate([
      {
        category: category.category,
        retention_days: parsed,
        action: draft.action,
      },
    ])
  }

  return (
    <div className='flex min-w-0 flex-col gap-8'>
      <SettingsSection title={t('Data retention')} bodyClassName='space-y-6'>
        <p className='text-dim max-w-[80ch] text-[13px] leading-relaxed'>
          {t(
            'Root configures how long each data category is kept. Unconfigured categories are not auto-deleted. Changing a period only affects later cleanup runs and does not immediately delete historical data. Notification history cannot be given an automatic cleanup period.'
          )}
        </p>
        <RetentionGate
          isRoot={isRoot}
          isError={settings.isError}
          error={settings.error}
        >
          <div className='divide-border flex flex-col divide-y'>
            {categories.map((category) => {
              const draft = drafts[category.category] ?? {
                days: '',
                action: category.allowed_actions[0] || 'delete',
              }
              return (
                <div
                  key={category.category}
                  className='flex flex-col gap-3 py-5 first:pt-0 last:pb-0'
                >
                  <div className='flex flex-col gap-1'>
                    <h4 className='text-[15px] leading-snug font-semibold tracking-tight'>
                      {t(category.title)}
                    </h4>
                    {category.notes ? (
                      <p className='text-dim max-w-[80ch] text-[12.5px] leading-relaxed'>
                        {t(category.notes)}
                      </p>
                    ) : null}
                  </div>
                  {!category.auto_cleanable ? (
                    <p className='text-dim max-w-[80ch] text-[13px] leading-relaxed'>
                      {t(
                        'Permanent retention. Automatic cleanup tasks must not delete notification history. Only the user may manually delete their own deletable messages.'
                      )}
                    </p>
                  ) : (
                    <div className='flex flex-wrap items-end gap-3'>
                      <div className='flex w-36 flex-col gap-1.5'>
                        <Label
                          htmlFor={`retention-days-${category.category}`}
                          className='text-[13.5px] leading-snug font-semibold'
                        >
                          {t('Retention period (days)')}
                        </Label>
                        <Input
                          id={`retention-days-${category.category}`}
                          inputMode='numeric'
                          placeholder={t('Not configured')}
                          className='tabular-nums'
                          value={draft.days}
                          onChange={(event) =>
                            setDrafts((current) => ({
                              ...current,
                              [category.category]: {
                                ...draft,
                                days: event.target.value,
                              },
                            }))
                          }
                        />
                      </div>
                      {category.allowed_actions.length > 1 && (
                        <div className='flex min-w-52 flex-col gap-1.5'>
                          <Label
                            htmlFor={`retention-action-${category.category}`}
                            className='text-[13.5px] leading-snug font-semibold'
                          >
                            {t('Action')}
                          </Label>
                          <NativeSelect
                            id={`retention-action-${category.category}`}
                            value={draft.action}
                            onChange={(event) =>
                              setDrafts((current) => ({
                                ...current,
                                [category.category]: {
                                  ...draft,
                                  action: event.target.value,
                                },
                              }))
                            }
                          >
                            {category.allowed_actions.map((action) => (
                              <NativeSelectOption key={action} value={action}>
                                {t(ACTION_LABELS[action] ?? action)}
                              </NativeSelectOption>
                            ))}
                          </NativeSelect>
                        </div>
                      )}
                      <Button
                        variant='brand'
                        type='button'
                        size='sm'
                        onClick={() => saveCategory(category)}
                        disabled={!canSave || save.isPending}
                      >
                        {t('Save')}
                      </Button>
                    </div>
                  )}
                  {(category.min_retention_days ||
                    (!category.configured && category.auto_cleanable)) && (
                    <p className='text-dim max-w-[80ch] text-[12.5px] leading-relaxed'>
                      {category.min_retention_days
                        ? (() => {
                            let floorKey =
                              'Automatic cleanup keeps a {{days}}-day safety floor.'
                            if (category.category === 'api_usage_logs') {
                              floorKey =
                                'Usage-log cleanup keeps a {{days}}-day safety floor.'
                            } else if (
                              category.category === 'support_tickets'
                            ) {
                              floorKey =
                                'Support-ticket cleanup keeps a {{days}}-day safety floor.'
                            }
                            return t(floorKey, {
                              days: category.min_retention_days,
                            })
                          })()
                        : null}
                      {category.min_retention_days &&
                      !category.configured &&
                      category.auto_cleanable
                        ? ' '
                        : null}
                      {!category.configured && category.auto_cleanable
                        ? t(
                            'Not configured: the official cleanup task will not delete this category.'
                          )
                        : null}
                    </p>
                  )}
                </div>
              )
            })}
          </div>
        </RetentionGate>
      </SettingsSection>

      {isRoot && !settings.isError && (
        <ContentSection
          title={t('Recent cleanup runs')}
          aria-label={t('Recent cleanup runs')}
        >
          {runs.isError && (
            <p className='text-destructive text-sm'>
              {getServerErrorMessage(runs.error, t('Failed to load data'))}
            </p>
          )}
          {runs.data?.length === 0 && (
            <p className='text-dim text-[13px]'>
              {t('No cleanup has run yet.')}
            </p>
          )}
          {(runs.data?.length ?? 0) > 0 && (
            <div className='border-border bg-card divide-border divide-y overflow-hidden rounded-[14px] border'>
              {(runs.data ?? []).map((run) => {
                const runItems = Array.isArray(run.items) ? run.items : []
                const categoryTitle = (category: string) =>
                  t(
                    categories.find((item) => item.category === category)
                      ?.title ?? category
                  )
                return (
                  <div
                    key={run.id}
                    className='flex flex-col gap-1.5 px-5 py-4 text-sm'
                  >
                    <p className='font-medium tabular-nums'>
                      {formatTimestamp(run.started_at)} ·{' '}
                      {run.trigger === 'manual'
                        ? t('Started manually')
                        : t('Scheduled')}
                    </p>
                    {runItems.length === 0 ? (
                      <p className='text-dim text-[13px]'>
                        {t('Nothing was due for cleanup.')}
                      </p>
                    ) : (
                      <ul className='text-dim flex flex-col gap-0.5 text-[13px] tabular-nums'>
                        {runItems.map((item) => (
                          <li key={item.id}>
                            {categoryTitle(item.category)}:{' '}
                            {t(
                              'deleted {{deleted}}, de-identified {{deidentified}}, failed {{failed}}',
                              {
                                deleted: item.delete_count,
                                deidentified: item.deidentify_count,
                                failed: item.failure_count,
                              }
                            )}
                            {item.skip_reason
                              ? ` · ${t(item.skip_reason)}`
                              : ''}
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </ContentSection>
      )}
    </div>
  )
}
