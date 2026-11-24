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
import { AlertTriangle, CheckSquare, RefreshCcw, X } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ButtonGroup } from '@/components/ui/button-group'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  buildPricingChanges,
  getModelPricing,
  saveModelPricing,
  invalidateModelPricing,
  type FailedModelPricingSync,
  type ModelPricingConfig,
  type ModelPricingChange,
} from '@/features/model-pricing/api'
import {
  getSitePricingCurrency,
  isValidPricingCurrency,
} from '@/features/model-pricing/currency'
import { applyPriceSyncSelections } from '@/features/model-pricing/pricing'
import { handleServerError } from '@/lib/handle-server-error'
import {
  requireServerSuccess,
  createServerError,
} from '@/lib/server-error-message'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { fetchUpstreamRatios, getUpstreamChannels } from '../api'
import type {
  DifferencesMap,
  PricingSyncModels,
  PricingSyncValues,
  UpstreamChannel,
  UpstreamConfig,
} from '../types'
import { ChannelSelectorDialog } from './channel-selector-dialog'
import {
  ConflictConfirmDialog,
  type ConflictItem,
} from './conflict-confirm-dialog'
import {
  DEFAULT_ENDPOINT,
  MODELS_DEV_PRESET_ENDPOINT,
  MODELS_DEV_PRESET_ID,
  OFFICIAL_CHANNEL_ENDPOINT,
  OFFICIAL_CHANNEL_ID,
  OPENROUTER_CHANNEL_TYPE,
  OPENROUTER_ENDPOINT,
} from './constants'
import { PriceAdjustmentSelect } from './price-adjustment-select'
import {
  adjustSyncPrice,
  type PriceAdjustment,
} from './upstream-price-adjustment'
import {
  describeSyncPrice,
  getSyncExpressionPricing,
  getUpstreamDisplayName,
  type PricingSourceSelection,
} from './upstream-ratio-sync-helpers'
import { UpstreamRatioSyncTable } from './upstream-ratio-sync-table'

function getDefaultEndpointForChannel(channel: UpstreamChannel): string {
  if (channel.id === MODELS_DEV_PRESET_ID) return MODELS_DEV_PRESET_ENDPOINT
  if (channel.id === OFFICIAL_CHANNEL_ID) return OFFICIAL_CHANNEL_ENDPOINT
  if (channel.type === OPENROUTER_CHANNEL_TYPE) return OPENROUTER_ENDPOINT
  return DEFAULT_ENDPOINT
}

type SyncPreview = {
  conflicts: ConflictItem[]
  changes: ModelPricingChange[]
  resolutions: Record<string, PricingSyncValues>
  exchangeRateSummary?: string
}

export function UpstreamRatioSync() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currencyConfig = useSystemConfigStore((state) => state.config.currency)
  const currency = useMemo(
    () => getSitePricingCurrency(currencyConfig),
    [currencyConfig]
  )
  const rate = isValidPricingCurrency(currency) ? currency.exchangeRate : null
  const [pricingBaseline, setPricingBaseline] =
    useState<ModelPricingConfig | null>(null)
  const [channelDialogOpen, setChannelDialogOpen] = useState(false)
  const [conflictDialogOpen, setConflictDialogOpen] = useState(false)
  const [selectedChannelIds, setSelectedChannelIds] = useState<number[]>([])
  const [channelEndpoints, setChannelEndpoints] = useState<
    Record<number, string>
  >({})
  const [differences, setDifferences] = useState<DifferencesMap>({})
  const [prices, setPrices] = useState<PricingSyncModels>({})
  const [selections, setSelections] = useState<
    Record<string, { source: string; adjustment: PriceAdjustment }>
  >({})
  const selectedSources = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(selections).map(([model, entry]) => [
          model,
          entry.source,
        ])
      ),
    [selections]
  )
  const adjustments = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(selections).map(([model, entry]) => [
          model,
          entry.adjustment,
        ])
      ),
    [selections]
  )
  const [preview, setPreview] = useState<SyncPreview | null>(null)
  const [loadingBaseline, setLoadingBaseline] = useState(false)
  const [syncFailureReport, setSyncFailureReport] = useState<{
    updatedCount: number
    failedModels: FailedModelPricingSync[]
  } | null>(null)
  const { data: channelsData } = useQuery({
    queryKey: ['upstream-channels'],
    queryFn: async () => requireServerSuccess(await getUpstreamChannels()),
    enabled: channelDialogOpen,
  })
  const channels = useMemo(() => channelsData?.data ?? [], [channelsData?.data])
  useEffect(() => {
    if (!channels.length) return
    setChannelEndpoints((previous) => {
      const next = { ...previous }
      for (const channel of channels) {
        next[channel.id] ??= getDefaultEndpointForChannel(channel)
      }
      return next
    })
  }, [channels])
  const adjustedPrices = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(selections).map(([model, entry]) => [
          model,
          adjustSyncPrice(
            prices[model]?.upstreams[entry.source] ?? {},
            entry.adjustment,
            rate
          ),
        ])
      ),
    [selections, prices, rate]
  )
  const hasAdjustmentErrors = Object.values(adjustedPrices).some(
    (result) => result.error
  )
  const adjustedCount = Object.values(selections).filter(
    (entry) => entry.adjustment !== 'keep'
  ).length
  const fetchMutation = useMutation({
    mutationFn: async (request: Parameters<typeof fetchUpstreamRatios>[0]) => {
      const response = await fetchUpstreamRatios(request)
      if (!response.success || !response.data?.prices) {
        throw createServerError(response, t('Failed to fetch upstream prices'))
      }
      const results = response.data.test_results
      if (
        results.length &&
        results.every((result) => result.status === 'error')
      ) {
        throw new Error(
          results
            .map(
              (result) =>
                `${getUpstreamDisplayName(result.name, t)}: ${result.error}`
            )
            .join(', ')
        )
      }
      return response
    },
    onMutate: () => {
      setPrices({})
      setDifferences({})
      setSelections({})
      setSyncFailureReport(null)
    },
    onSuccess: (response) => {
      const errors = response.data.test_results.filter(
        (result) => result.status === 'error'
      )
      if (errors.length) {
        toast.warning(
          t('Some channels failed: {{errorMsg}}', {
            errorMsg: errors
              .map(
                (result) =>
                  `${getUpstreamDisplayName(result.name, t)}: ${result.error}`
              )
              .join(', '),
          })
        )
      }
      setDifferences(response.data.differences)
      setPrices(response.data.prices)
      if (!Object.keys(response.data.prices).length) {
        toast.success(t('No price differences found'))
      }
    },
    onError: (error: Error) =>
      handleServerError(error, t('Failed to fetch upstream prices')),
  })
  const syncMutation = useMutation({
    mutationFn: async (confirmed: SyncPreview) => {
      const result = await saveModelPricing(confirmed.changes, {
        partial: true,
      })
      return { confirmed, result }
    },
    onSuccess: async ({ confirmed, result }) => {
      const failedModels = result.failed_models ?? []
      const failedSet = new Set(failedModels.map((item) => item.model_name))
      const updatedModels = Array.isArray(result.updated_models)
        ? result.updated_models
        : Object.keys(confirmed.resolutions).filter(
            (name) => !failedSet.has(name)
          )
      const updatedSet = new Set(updatedModels)
      const updatedCount = result.updated_count ?? updatedSet.size
      const failedCount = result.failed_count ?? failedModels.length

      if (updatedSet.size > 0) {
        setPrices((previous) =>
          Object.fromEntries(
            Object.entries(previous).map(([name, row]) => [
              name,
              updatedSet.has(name) && confirmed.resolutions[name]
                ? { ...row, current: confirmed.resolutions[name] }
                : row,
            ])
          )
        )
      }

      setSelections((previous) => {
        const next = { ...previous }
        for (const name of updatedSet) {
          delete next[name]
        }
        return next
      })

      setConflictDialogOpen(false)
      setPricingBaseline(null)

      if (failedCount === 0) {
        setSyncFailureReport(null)
        toast.success(
          t('{{count}} models synced', {
            count: updatedCount,
          })
        )
      } else {
        setSyncFailureReport({
          updatedCount,
          failedModels,
        })
        toast.warning(
          t('{{updated}} synced, {{failed}} failed', {
            updated: updatedCount,
            failed: failedCount,
          })
        )
      }

      await invalidateModelPricing(queryClient)
      try {
        setPricingBaseline(await getModelPricing())
      } catch (error) {
        handleServerError(error, t('Reload pricing'))
      }
    },
    onError: (error: Error) =>
      handleServerError(error, t('Failed to sync prices')),
  })
  const handleConfirmChannelSelection = async (selectedIds: number[]) => {
    const selected = channels.filter((channel) =>
      selectedIds.includes(channel.id)
    )
    if (!selected.length) {
      toast.warning(t('Please select at least one channel'))
      return
    }
    const upstreams: UpstreamConfig[] = selected.map((channel) => ({
      id: channel.id,
      name: channel.name,
      base_url: channel.base_url,
      endpoint:
        channelEndpoints[channel.id] || getDefaultEndpointForChannel(channel),
    }))
    setLoadingBaseline(true)
    setPrices({})
    setDifferences({})
    fetchMutation.reset()
    setPricingBaseline(null)
    setSelections({})
    setSyncFailureReport(null)
    syncMutation.reset()
    try {
      setPricingBaseline(await getModelPricing())
      fetchMutation.mutate({ upstreams, timeout: 10, include_unchanged: true })
    } catch (error) {
      handleServerError(error, t('Failed to load model pricing'))
    } finally {
      setLoadingBaseline(false)
    }
  }
  const handleSelectPrices = useCallback(
    (selected: PricingSourceSelection[]) => {
      setSelections((previous) => {
        const next = { ...previous }
        for (const entry of selected) {
          if (!prices[entry.model]?.upstreams[entry.source]) continue
          if (next[entry.model]?.source === entry.source) continue
          next[entry.model] = { source: entry.source, adjustment: 'keep' }
        }
        return next
      })
    },
    [prices]
  )
  const handleUnselectPrices = useCallback((models: string[]) => {
    setSelections((previous) => {
      const next = { ...previous }
      for (const model of models) delete next[model]
      return next
    })
  }, [])
  const handleAdjustPrices = useCallback(
    (models: string[], adjustment: PriceAdjustment) => {
      setSelections((previous) => {
        const next = { ...previous }
        for (const model of models) {
          if (next[model]) next[model] = { ...next[model], adjustment }
        }
        return next
      })
    },
    []
  )
  const handleApplySync = () => {
    if (!pricingBaseline || hasAdjustmentErrors) return
    const resolutions: Record<string, PricingSyncValues> = {}
    const conflicts: ConflictItem[] = []
    for (const [model, entry] of Object.entries(selections)) {
      const selected = adjustedPrices[model]?.values
      const row = prices[model]
      if (!row || !selected) return
      resolutions[model] = selected
      let adjustment = t('Keep source price')
      if (entry.adjustment === 'divide') {
        adjustment = t('Divide by exchange rate (÷ {{rate}})', {
          rate: String(rate),
        })
      }
      if (entry.adjustment === 'multiply') {
        adjustment = t('Multiply by exchange rate (× {{rate}})', {
          rate: String(rate),
        })
      }
      conflicts.push({
        model,
        channel: getUpstreamDisplayName(entry.source, t),
        current: describeSyncPrice(row.current, t),
        newVal: describeSyncPrice(selected, t),
        adjustment,
        siteVal:
          isValidPricingCurrency(currency) &&
          (selected.billing_mode !== 'tiered_expr' ||
            getSyncExpressionPricing(String(selected.billing_expr), t))
            ? describeSyncPrice(selected, t, currency)
            : undefined,
      })
    }
    try {
      const after = applyPriceSyncSelections(
        pricingBaseline.options,
        resolutions
      )
      // Freeze the exact save payload and its preview together. Changes to the
      // site exchange rate while this dialog is open cannot change the charge.
      setPreview({
        conflicts,
        resolutions,
        changes: buildPricingChanges(
          pricingBaseline,
          pricingBaseline.options,
          after
        ),
        exchangeRateSummary: isValidPricingCurrency(currency)
          ? t('Current exchange rate: 1 USD = {{rate}} {{currency}}', {
              rate: String(rate),
              currency: currency.label,
            })
          : undefined,
      })
      syncMutation.reset()
      setConflictDialogOpen(true)
    } catch (error) {
      handleServerError(error, t('Failed to sync prices'))
    }
  }
  const isLoading =
    loadingBaseline || fetchMutation.isPending || syncMutation.isPending
  const selectedModels = Object.keys(selections)
  const batchAdjustmentDisabled =
    isLoading || selectedModels.length === 0 || rate === null
  const failureReportText = useMemo(() => {
    if (!syncFailureReport?.failedModels.length) return ''
    return syncFailureReport.failedModels
      .map((item) => `${item.model_name}: ${item.error}`)
      .join('\n')
  }, [syncFailureReport])

  return (
    <div className='flex h-full min-h-0 flex-col gap-3'>
      <div className='flex shrink-0 flex-wrap items-center gap-2'>
        <PriceAdjustmentSelect
          value={null}
          currency={currency}
          label={t('Set selected prices')}
          disabled={isLoading || !Object.keys(selections).length}
          onChange={(mode) => handleAdjustPrices(Object.keys(selections), mode)}
        />
        <span className='text-dim text-[12.5px]'>
          {isValidPricingCurrency(currency)
            ? t('Current exchange rate: 1 USD = {{rate}} {{currency}}', {
                rate: String(rate),
                currency: currency.label,
              })
            : t('A valid site exchange rate is required.')}
        </span>
      </div>
      {syncFailureReport && syncFailureReport.failedModels.length > 0 && (
        <Card className='border-warning/40 bg-warning/5 shrink-0 gap-2 rounded-[14px] py-3 shadow-none'>
          <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-2 px-4 py-0'>
            <div className='flex flex-wrap items-center gap-2'>
              <AlertTriangle
                className='text-warning size-4 shrink-0'
                aria-hidden='true'
              />
              <CardTitle className='text-sm'>
                {t('Sync Failure Report')}
              </CardTitle>
              <Badge variant='outline' className='font-mono text-[11px]'>
                {t('{{updated}} synced, {{failed}} failed', {
                  updated: syncFailureReport.updatedCount,
                  failed: syncFailureReport.failedModels.length,
                })}
              </Badge>
            </div>
            <div className='flex items-center gap-1.5'>
              <CopyButton
                value={failureReportText}
                variant='outline'
                size='sm'
                className='h-7 gap-1.5 px-2.5 text-xs'
                iconClassName='size-3.5'
                tooltip={t('Copy Failures')}
              >
                {t('Copy Failures')}
              </CopyButton>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-7 gap-1 px-2 text-xs'
                onClick={() => setSyncFailureReport(null)}
              >
                <X className='size-3.5' aria-hidden='true' />
                {t('Dismiss')}
              </Button>
            </div>
          </CardHeader>
          <CardContent className='px-4 py-0'>
            <ScrollArea className='bg-background/80 max-h-44 rounded-lg border'>
              <div className='divide-border divide-y'>
                {syncFailureReport.failedModels.map((item) => (
                  <div
                    key={`${item.model_name}:${item.error}`}
                    className='flex flex-col gap-0.5 px-3 py-2 text-xs sm:flex-row sm:items-baseline sm:justify-between sm:gap-4'
                  >
                    <span className='shrink-0 font-mono font-medium'>
                      {item.model_name}
                    </span>
                    <span className='text-destructive min-w-0 break-words sm:text-right'>
                      {item.error}
                    </span>
                  </div>
                ))}
              </div>
            </ScrollArea>
          </CardContent>
        </Card>
      )}
      <div className='min-h-0 flex-1'>
        {fetchMutation.isError ? (
          <ErrorState
            description={fetchMutation.error.message}
            action={
              <Button
                variant='outline'
                onClick={() =>
                  void handleConfirmChannelSelection(selectedChannelIds)
                }
              >
                {t('Retry')}
              </Button>
            }
          />
        ) : (
          <UpstreamRatioSyncTable
            toolbar={
              <Button
                variant='outline'
                onClick={() => setChannelDialogOpen(true)}
                disabled={isLoading}
              >
                <RefreshCcw className='size-4' aria-hidden />
                {t('Select price sources')}
              </Button>
            }
            prices={prices}
            differences={differences}
            selectedSources={selectedSources}
            adjustments={adjustments}
            adjustedPrices={adjustedPrices}
            currency={currency}
            onAdjustPrices={handleAdjustPrices}
            isDisabled={isLoading}
            isSyncing={fetchMutation.isPending || loadingBaseline}
            onSelectPrices={handleSelectPrices}
            onUnselectPrices={handleUnselectPrices}
          />
        )}
      </div>
      <div className='flex shrink-0 flex-wrap items-center justify-between gap-2 border-t pt-3'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-muted-foreground text-sm' role='status'>
            {t('{{count}} selected models', {
              count: Object.keys(selectedSources).length,
            })}
          </span>
          {adjustedCount > 0 && (
            <span className='text-muted-foreground text-sm'>
              {t('Exchange rate adjustments: {{count}}', {
                count: adjustedCount,
              })}
            </span>
          )}
          <ButtonGroup aria-label={t('Set selected prices')}>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='tabular-nums'
              disabled={batchAdjustmentDisabled}
              aria-label={t('Multiply by exchange rate (× {{rate}})', {
                rate: String(rate ?? '—'),
              })}
              title={t('Multiply by exchange rate (× {{rate}})', {
                rate: String(rate ?? '—'),
              })}
              onClick={() => handleAdjustPrices(selectedModels, 'multiply')}
            >
              × {rate ?? '—'}
            </Button>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='tabular-nums'
              disabled={batchAdjustmentDisabled}
              aria-label={t('Divide by exchange rate (÷ {{rate}})', {
                rate: String(rate ?? '—'),
              })}
              title={t('Divide by exchange rate (÷ {{rate}})', {
                rate: String(rate ?? '—'),
              })}
              onClick={() => handleAdjustPrices(selectedModels, 'divide')}
            >
              ÷ {rate ?? '—'}
            </Button>
          </ButtonGroup>
          {Object.keys(selectedSources).length > 0 && (
            <Button
              variant='ghost'
              size='sm'
              disabled={isLoading}
              onClick={() => setSelections({})}
            >
              {t('Clear selection')}
            </Button>
          )}
        </div>
        <Button
          variant='brand'
          onClick={handleApplySync}
          disabled={
            !pricingBaseline ||
            !Object.keys(selectedSources).length ||
            hasAdjustmentErrors ||
            isLoading
          }
        >
          <CheckSquare className='size-4' aria-hidden />
          {t('Apply Sync')}
        </Button>
      </div>
      <ChannelSelectorDialog
        open={channelDialogOpen}
        onOpenChange={setChannelDialogOpen}
        channels={channels}
        selectedChannelIds={selectedChannelIds}
        onSelectedChannelIdsChange={setSelectedChannelIds}
        channelEndpoints={channelEndpoints}
        onChannelEndpointsChange={setChannelEndpoints}
        onConfirm={handleConfirmChannelSelection}
      />
      <ConflictConfirmDialog
        open={conflictDialogOpen}
        onOpenChange={(open) => {
          if (!syncMutation.isPending) setConflictDialogOpen(open)
        }}
        conflicts={preview?.conflicts ?? []}
        exchangeRateSummary={preview?.exchangeRateSummary}
        onConfirm={() => {
          if (preview) syncMutation.mutate(preview)
        }}
        isLoading={syncMutation.isPending}
        error={syncMutation.error?.message}
        onReload={() => {
          setConflictDialogOpen(false)
          setSelections({})
          setPrices({})
          setDifferences({})
          setPricingBaseline(null)
          syncMutation.reset()
          setChannelDialogOpen(true)
        }}
      />
    </div>
  )
}
