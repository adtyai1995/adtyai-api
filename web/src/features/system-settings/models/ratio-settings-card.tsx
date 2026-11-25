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
import { useMutation } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  buildPricingChanges,
  useModelPricing,
  useSaveModelPricing,
  type ModelPricingConfig,
} from '@/features/model-pricing/api'
import { pricingOptions } from '@/features/model-pricing/pricing'
import { useMediaQuery } from '@/hooks'
import { handleServerError } from '@/lib/handle-server-error'

import { SettingsPageTitleStatusPortal } from '../components/settings-page-context'
import { useUpdateOption } from '../hooks/use-update-option'
import { GroupRatioForm } from './group-ratio-form'
import { ModelRatioForm } from './model-ratio-form'
import { ToolPriceSettings } from './tool-price-settings'
import { UpstreamRatioSync } from './upstream-ratio-sync'
import {
  formatJsonForTextarea,
  type JsonValidationError,
  normalizeJsonString,
  validateJsonString,
} from './utils'

type Translate = (key: string, options?: Record<string, unknown>) => string

function formatJsonValidationError(
  t: Translate,
  error?: JsonValidationError,
  fallback = 'Invalid JSON'
) {
  if (!error) return t(fallback)

  if (error.type === 'required') return t('Value is required')
  if (error.type === 'structure') {
    return t(
      fallback === 'Invalid JSON' ? 'JSON structure is invalid' : fallback
    )
  }

  let locationMessage: string
  if (error.line && error.column) {
    locationMessage = t(
      'JSON is invalid at line {{line}}, column {{column}}.',
      {
        line: error.line,
        column: error.column,
      }
    )
  } else if (error.position !== undefined) {
    locationMessage = t('JSON is invalid at position {{position}}.', {
      position: error.position,
    })
  } else {
    locationMessage = t('JSON is invalid. Please check the syntax.')
  }

  const parts = [locationMessage]

  if (error.missingCommaLine) {
    parts.push(
      t('Check line {{line}} for a missing comma.', {
        line: error.missingCommaLine,
      })
    )
  }

  return parts.join(' ')
}

function createJsonStringField(
  t: Translate,
  options?: Parameters<typeof validateJsonString>[1]
) {
  return z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value, options)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: formatJsonValidationError(t, result.error, result.message),
      })
    }
  })
}

const createModelSchema = (t: Translate) =>
  z.object({
    ModelPrice: createJsonStringField(t),
    ModelRatio: createJsonStringField(t),
    CacheRatio: createJsonStringField(t),
    CreateCacheRatio: createJsonStringField(t),
    CompletionRatio: createJsonStringField(t),
    ImageRatio: createJsonStringField(t),
    AudioRatio: createJsonStringField(t),
    AudioCompletionRatio: createJsonStringField(t),
    ExposeRatioEnabled: z.boolean(),
    BillingMode: createJsonStringField(t),
    BillingExpr: createJsonStringField(t),
    PluginBillingExpr: createJsonStringField(t),
  })

const createGroupSchema = (t: Translate) =>
  z.object({
    GroupRatio: createJsonStringField(t),
    UserUsableGroups: createJsonStringField(t),
  })

type ModelFormValues = z.infer<ReturnType<typeof createModelSchema>>
type GroupFormValues = z.infer<ReturnType<typeof createGroupSchema>>

function formatGroupFormValues(values: GroupFormValues): GroupFormValues {
  return {
    GroupRatio: formatJsonForTextarea(values.GroupRatio),
    UserUsableGroups: formatJsonForTextarea(values.UserUsableGroups),
  }
}

function formatModelFormValues(values: ModelFormValues): ModelFormValues {
  return {
    ...values,
    ModelPrice: formatJsonForTextarea(values.ModelPrice),
    ModelRatio: formatJsonForTextarea(values.ModelRatio),
    CacheRatio: formatJsonForTextarea(values.CacheRatio),
    CreateCacheRatio: formatJsonForTextarea(values.CreateCacheRatio),
    CompletionRatio: formatJsonForTextarea(values.CompletionRatio),
    ImageRatio: formatJsonForTextarea(values.ImageRatio),
    AudioRatio: formatJsonForTextarea(values.AudioRatio),
    AudioCompletionRatio: formatJsonForTextarea(values.AudioCompletionRatio),
    BillingMode: formatJsonForTextarea(values.BillingMode),
    BillingExpr: formatJsonForTextarea(values.BillingExpr),
    PluginBillingExpr: formatJsonForTextarea(values.PluginBillingExpr),
  }
}

function normalizeModelFormValues(values: ModelFormValues) {
  return {
    ModelPrice: normalizeJsonString(values.ModelPrice),
    ModelRatio: normalizeJsonString(values.ModelRatio),
    CacheRatio: normalizeJsonString(values.CacheRatio),
    CreateCacheRatio: normalizeJsonString(values.CreateCacheRatio),
    CompletionRatio: normalizeJsonString(values.CompletionRatio),
    ImageRatio: normalizeJsonString(values.ImageRatio),
    AudioRatio: normalizeJsonString(values.AudioRatio),
    AudioCompletionRatio: normalizeJsonString(values.AudioCompletionRatio),
    ExposeRatioEnabled: values.ExposeRatioEnabled,
    BillingMode: normalizeJsonString(values.BillingMode),
    BillingExpr: normalizeJsonString(values.BillingExpr),
    PluginBillingExpr: normalizeJsonString(values.PluginBillingExpr),
  }
}

function mergePricingIntoModelDefaults(
  initial: ModelFormValues,
  baseline: ModelPricingConfig | null
): ModelFormValues {
  if (!baseline) return initial
  return {
    ...initial,
    ...baseline.options,
    BillingMode: baseline.options['billing_setting.billing_mode'],
    BillingExpr: baseline.options['billing_setting.billing_expr'],
    PluginBillingExpr: baseline.options['billing_setting.plugin_billing_expr'],
  }
}
type RatioTabId =
  | 'models'
  | 'unset-models'
  | 'groups'
  | 'tool-prices'
  | 'upstream-sync'

type RatioSettingsCardProps = {
  modelDefaults: ModelFormValues
  groupDefaults: GroupFormValues
  toolPricesDefault: string
  titleKey?: string
  visibleTabs?: RatioTabId[]
}

export function RatioSettingsCard({
  modelDefaults: initialModelDefaults,
  groupDefaults,
  toolPricesDefault,
  titleKey = 'Pricing Ratios',
  visibleTabs = ['models', 'groups', 'tool-prices', 'upstream-sync'],
}: RatioSettingsCardProps) {
  const { t } = useTranslation()
  const isMobile = useMediaQuery('(max-width: 639px)')
  const updateOption = useUpdateOption()
  const [confirmOpen, setConfirmOpen] = useState(false)

  const pricingQuery = useModelPricing()
  const savePricing = useSaveModelPricing()
  const [pricingBaseline, setPricingBaseline] =
    useState<ModelPricingConfig | null>(null)
  const modelDefaults = useMemo(
    () => mergePricingIntoModelDefaults(initialModelDefaults, pricingBaseline),
    [initialModelDefaults, pricingBaseline]
  )

  const modelNormalizedDefaults = useRef({
    ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
    ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
    CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
    CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
    CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
    ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
    AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
    AudioCompletionRatio: normalizeJsonString(
      modelDefaults.AudioCompletionRatio
    ),
    ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
    BillingMode: normalizeJsonString(modelDefaults.BillingMode),
    BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
    PluginBillingExpr: normalizeJsonString(modelDefaults.PluginBillingExpr),
  })
  const modelNormalizedSerializedRef = useRef(
    JSON.stringify(modelNormalizedDefaults.current)
  )
  const [savedModelValues, setSavedModelValues] = useState(
    modelNormalizedDefaults.current
  )

  const groupNormalizedDefaults = useRef({
    GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
    UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
  })
  const groupNormalizedSerializedRef = useRef(
    JSON.stringify(groupNormalizedDefaults.current)
  )
  const groupFormDefaultsRef = useRef<GroupFormValues>(
    formatGroupFormValues(groupDefaults)
  )
  const modelSchema = useMemo(() => createModelSchema(t), [t])
  const groupSchema = useMemo(() => createGroupSchema(t), [t])

  const modelForm = useForm<ModelFormValues>({
    resolver: zodResolver(modelSchema),
    mode: 'onChange',
    defaultValues: {
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
      PluginBillingExpr: formatJsonForTextarea(modelDefaults.PluginBillingExpr),
    },
  })

  const groupForm = useForm<GroupFormValues>({
    resolver: zodResolver(groupSchema),
    mode: 'onChange',
    defaultValues: groupFormDefaultsRef.current,
  })

  const syncModelFormToDefaults = useCallback(
    (nextBaseline: ModelPricingConfig | null) => {
      const nextDefaults = mergePricingIntoModelDefaults(
        initialModelDefaults,
        nextBaseline
      )
      const nextNormalized = normalizeModelFormValues(nextDefaults)
      modelNormalizedDefaults.current = nextNormalized
      modelNormalizedSerializedRef.current = JSON.stringify(nextNormalized)
      setSavedModelValues(nextNormalized)
      modelForm.reset(formatModelFormValues(nextDefaults))
    },
    [initialModelDefaults, modelForm]
  )
  const resetMutation = useMutation({
    mutationFn: async () => {
      if (!pricingBaseline) return
      await savePricing.mutateAsync(
        pricingBaseline.entries.map((entry) => ({
          model_name: entry.model_name,
          expected_version: entry.version,
          pricing: {},
          reset: true,
        }))
      )
      const refreshed = await pricingQuery.refetch()
      const nextBaseline = refreshed.data ?? null
      setPricingBaseline(nextBaseline)
      syncModelFormToDefaults(nextBaseline)
    },
    onSuccess: () => {
      toast.success(t('Model prices reset successfully'))
      setConfirmOpen(false)
    },
    onError: (error) => handleServerError(error),
  })

  useEffect(() => {
    if (!pricingQuery.data) return
    if (
      !pricingBaseline ||
      (!modelForm.formState.isDirty &&
        !savePricing.isPending &&
        !resetMutation.isPending)
    ) {
      setPricingBaseline(pricingQuery.data)
    }
  }, [
    modelForm.formState.isDirty,
    pricingBaseline,
    pricingQuery.data,
    resetMutation.isPending,
    savePricing.isPending,
  ])

  useEffect(() => {
    const nextModelNormalized = {
      ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
      ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
      CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
      CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
      CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
      ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
      AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
      AudioCompletionRatio: normalizeJsonString(
        modelDefaults.AudioCompletionRatio
      ),
      ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
      BillingMode: normalizeJsonString(modelDefaults.BillingMode),
      BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
      PluginBillingExpr: normalizeJsonString(modelDefaults.PluginBillingExpr),
    }
    const serialized = JSON.stringify(nextModelNormalized)
    if (serialized === modelNormalizedSerializedRef.current) return
    modelNormalizedDefaults.current = nextModelNormalized
    modelNormalizedSerializedRef.current = serialized
    setSavedModelValues(nextModelNormalized)

    modelForm.reset(
      {
        ...modelDefaults,
        ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
        ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
        CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
        CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
        CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
        ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
        AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
        AudioCompletionRatio: formatJsonForTextarea(
          modelDefaults.AudioCompletionRatio
        ),
        BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
        BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
        PluginBillingExpr: formatJsonForTextarea(
          modelDefaults.PluginBillingExpr
        ),
      },
      { keepDirtyValues: true }
    )
  }, [modelDefaults, modelForm])

  useEffect(() => {
    const nextGroupNormalized = {
      GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
      UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
    }
    const serialized = JSON.stringify(nextGroupNormalized)
    if (serialized === groupNormalizedSerializedRef.current) return
    groupNormalizedDefaults.current = nextGroupNormalized
    groupNormalizedSerializedRef.current = serialized

    const nextGroupDefaults = formatGroupFormValues(groupDefaults)
    groupFormDefaultsRef.current = nextGroupDefaults
    groupForm.reset(nextGroupDefaults, { keepDirtyValues: true })
  }, [groupDefaults, groupForm])

  const saveModelRatios = useCallback(
    async (values: ModelFormValues) => {
      const normalized = {
        ModelPrice: normalizeJsonString(values.ModelPrice),
        ModelRatio: normalizeJsonString(values.ModelRatio),
        CacheRatio: normalizeJsonString(values.CacheRatio),
        CreateCacheRatio: normalizeJsonString(values.CreateCacheRatio),
        CompletionRatio: normalizeJsonString(values.CompletionRatio),
        ImageRatio: normalizeJsonString(values.ImageRatio),
        AudioRatio: normalizeJsonString(values.AudioRatio),
        AudioCompletionRatio: normalizeJsonString(values.AudioCompletionRatio),
        ExposeRatioEnabled: values.ExposeRatioEnabled,
        BillingMode: normalizeJsonString(values.BillingMode),
        BillingExpr: normalizeJsonString(values.BillingExpr),
        PluginBillingExpr: normalizeJsonString(values.PluginBillingExpr),
      }

      if (!pricingBaseline) return
      try {
        const changes = buildPricingChanges(
          pricingBaseline,
          pricingOptions(modelNormalizedDefaults.current),
          pricingOptions(normalized)
        )
        const visibilityChanged =
          normalized.ExposeRatioEnabled !==
          modelNormalizedDefaults.current.ExposeRatioEnabled
        if (!changes.length && !visibilityChanged) {
          toast.info(t('No model price changes to save'))
          return
        }
        if (changes.length > 0) {
          await savePricing.mutateAsync(changes)
          const refreshedPricing = await pricingQuery.refetch()
          if (refreshedPricing.data) {
            setPricingBaseline(refreshedPricing.data)
          }
          const partialSaved = {
            ...normalized,
            ExposeRatioEnabled:
              modelNormalizedDefaults.current.ExposeRatioEnabled,
          }
          modelNormalizedDefaults.current = partialSaved
          modelNormalizedSerializedRef.current = JSON.stringify(partialSaved)
          setSavedModelValues(partialSaved)
          modelForm.reset(formatModelFormValues(partialSaved), {
            keepValues: true,
            keepFieldsRef: true,
            keepErrors: true,
          })
        }
        if (visibilityChanged) {
          await updateOption.mutateAsync({
            key: 'ExposeRatioEnabled',
            value: normalized.ExposeRatioEnabled,
          })
        }
        modelNormalizedDefaults.current = normalized
        modelNormalizedSerializedRef.current = JSON.stringify(normalized)
        setSavedModelValues(normalized)
        modelForm.reset(formatModelFormValues(normalized))
        toast.success(t('Model pricing saved'))
      } catch (error) {
        handleServerError(error)
      }
    },
    [t, updateOption, pricingBaseline, savePricing, pricingQuery, modelForm]
  )

  const saveGroupRatios = useCallback(
    async (values: GroupFormValues) => {
      const normalized = {
        GroupRatio: normalizeJsonString(values.GroupRatio),
        UserUsableGroups: normalizeJsonString(values.UserUsableGroups),
      }

      const updates = (
        Object.keys(normalized) as Array<keyof typeof normalized>
      ).filter(
        (key) => normalized[key] !== groupNormalizedDefaults.current[key]
      )

      for (const key of updates) {
        await updateOption.mutateAsync({ key, value: normalized[key] })
        groupNormalizedDefaults.current = {
          ...groupNormalizedDefaults.current,
          [key]: normalized[key],
        }
        groupNormalizedSerializedRef.current = JSON.stringify(
          groupNormalizedDefaults.current
        )
        groupFormDefaultsRef.current = {
          ...groupFormDefaultsRef.current,
          [key]: values[key],
        }
        groupForm.reset(groupFormDefaultsRef.current, {
          keepValues: true,
          keepFieldsRef: true,
          keepErrors: true,
        })
      }
      const nextDefaults = formatGroupFormValues({
        ...groupFormDefaultsRef.current,
        ...values,
      })
      groupFormDefaultsRef.current = nextDefaults
      groupForm.reset(nextDefaults, {
        keepFieldsRef: true,
        keepErrors: true,
      })
    },
    [groupForm, updateOption]
  )

  const handleResetRatios = useCallback(() => {
    setConfirmOpen(true)
  }, [])

  const { mutate: resetMutate } = resetMutation
  const handleConfirmReset = useCallback(() => {
    resetMutate()
  }, [resetMutate])

  const tabLabels: Record<RatioTabId, string> = {
    models: 'Model prices',
    'unset-models': 'Unset price models',
    groups: 'Group ratios',
    'tool-prices': 'Tool prices',
    'upstream-sync': 'Upstream price sync',
  }
  const tabsGridClass =
    {
      1: 'grid-cols-1',
      2: 'grid-cols-2',
      3: 'grid-cols-3',
      4: 'grid-cols-4',
      5: 'grid-cols-5',
    }[visibleTabs.length] ?? 'grid-cols-4'
  const defaultTab = visibleTabs[0] ?? 'models'

  const renderTabContent = (tab: RatioTabId) => {
    if (tab === 'models' || tab === 'unset-models') {
      if (pricingQuery.isError) {
        return (
          <ErrorState
            description={pricingQuery.error.message}
            onRetry={() => void pricingQuery.refetch()}
          />
        )
      }
      if (!pricingBaseline) return <LoadingState />
      return (
        <>
          {savePricing.isError && (
            <Button
              variant='outline'
              size='sm'
              onClick={async () => {
                const refreshed = await pricingQuery.refetch()
                if (refreshed.data) {
                  setPricingBaseline(refreshed.data)
                  syncModelFormToDefaults(refreshed.data)
                }
                savePricing.reset()
              }}
            >
              {t('Reload pricing')}
            </Button>
          )}
          <ModelRatioForm
            form={modelForm}
            savedValues={savedModelValues}
            onSave={saveModelRatios}
            onReset={handleResetRatios}
            isSaving={updateOption.isPending || savePricing.isPending}
            isResetting={resetMutation.isPending}
            variant={tab === 'unset-models' ? 'unset' : 'default'}
          />
        </>
      )
    }
    if (tab === 'groups') {
      return (
        <GroupRatioForm
          form={groupForm}
          onSave={saveGroupRatios}
          isSaving={updateOption.isPending}
        />
      )
    }
    if (tab === 'tool-prices') {
      return <ToolPriceSettings defaultValue={toolPricesDefault} />
    }
    return <UpstreamRatioSync />
  }

  const renderTabSwitcher = () => (
    <TabsList
      className={
        isMobile
          ? 'flex w-full justify-start overflow-x-auto'
          : `grid w-fit max-w-full ${tabsGridClass}`
      }
    >
      {visibleTabs.map((tab) => (
        <TabsTrigger
          key={tab}
          value={tab}
          className={isMobile ? 'flex-none px-3' : undefined}
        >
          {t(tabLabels[tab])}
        </TabsTrigger>
      ))}
    </TabsList>
  )

  return (
    <>
      {visibleTabs.length === 1 ? (
        <div
          aria-label={t(titleKey)}
          className={
            defaultTab === 'models' || defaultTab === 'unset-models'
              ? 'flex min-h-0 flex-1 flex-col'
              : 'min-w-0'
          }
        >
          {renderTabContent(defaultTab)}
        </div>
      ) : (
        <Tabs defaultValue={defaultTab} className='h-full min-h-0 gap-6'>
          {/* The phone title row is too narrow for the tabs. */}
          {isMobile ? (
            renderTabSwitcher()
          ) : (
            <SettingsPageTitleStatusPortal>
              {renderTabSwitcher()}
            </SettingsPageTitleStatusPortal>
          )}

          <div
            aria-label={t(titleKey)}
            className='flex min-h-0 flex-1 flex-col overflow-auto'
          >
            {visibleTabs.map((tab) => (
              <TabsContent
                key={tab}
                value={tab}
                className={
                  tab === 'models' ||
                  tab === 'unset-models' ||
                  tab === 'upstream-sync'
                    ? 'flex min-h-0 flex-1 flex-col data-hidden:hidden'
                    : 'min-h-0 flex-1'
                }
              >
                {renderTabContent(tab)}
              </TabsContent>
            ))}
          </div>
        </Tabs>
      )}

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Reset all model prices?')}
        desc={t(
          'This will clear custom pricing ratios and revert to upstream defaults.'
        )}
        destructive
        isLoading={resetMutation.isPending}
        handleConfirm={handleConfirmReset}
        confirmText={t('Reset')}
      />
    </>
  )
}
